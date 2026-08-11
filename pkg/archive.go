package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/gdower/gsvalidator/adapter/repository"
	"github.com/gdower/gsvalidator/usecase"
	"github.com/gnames/gnparser"
	"github.com/sfborg/sflib"
	"github.com/sfborg/sflib/pkg/sfga"

	_ "modernc.org/sqlite"
)

const sqliteDriver = "sqlite"

// Archive is an sfga SQLite archive opened for reading and, unless the
// ReadOnly option was used, writing.
//
// Reads are methods on *Archive. Writes are methods on *Tx, obtained via
// WithTx. This split makes it impossible to accidentally mutate the archive
// outside a transaction.
//
// A read-write Archive wraps a sflib sfga.Archive handle so we can share
// version/compat checks, schema migration, and import/enrichment flows with
// the rest of the SFBorg ecosystem. Read-only archives skip sflib because
// sflib's Connect() sets journal_mode=WAL, which requires write access.
type Archive struct {
	sf       sfga.Archive // nil when readOnly
	db       *sql.DB
	path     string
	readOnly bool

	// parser is used by write paths to populate gn__* fields on every
	// mutation of a name row. Read-only archives leave it nil.
	// gnparser.GNparser is not documented as concurrent-safe; parserMu
	// serializes calls. Given writes go through WithTx one at a time, this
	// is not a hot lock. If parser contention becomes a real cost, switch
	// to gnparser.NewPool().
	parser   gnparser.GNparser
	parserMu sync.Mutex

	// vocab lazily loads the controlled-vocabulary bundle (rank, nom_code,
	// taxonomic_status, …) on first call. Cached for the archive lifetime —
	// the tables don't change during a session. Set/read via sync.Once so
	// concurrent HTTP requests share the same load.
	vocab     *Vocabulary
	vocabErr  error
	vocabOnce sync.Once

	// validator is hive's gsvalidator use case, wired at Open with the
	// embedded rule set + sfga schema mapper + built-in and sfga-custom
	// validators. Callers reach it via Archive.ValidateName /
	// ValidateTaxon. See pkg/validation.go and PLANNING.md §
	// Validation engine.
	validator *usecase.ValidateRecordUseCase

	// ruleLoader is the same bundle loader wired into `validator`.
	// Kept as a direct reference so RefreshTimeBasedIssues can read
	// Rule metadata (Trigger, RecheckDays) without going through the
	// use case, which only exposes evaluation.
	ruleLoader *repository.BundleLoader
}

// OpenOption configures Open. Options are functional; pass them variadically.
type OpenOption func(*openOpts)

type openOpts struct {
	readOnly bool
}

// ReadOnly opens the archive in read-only mode. Any WithTx call on a
// read-only archive returns ErrReadOnly without touching the database.
func ReadOnly() OpenOption { return func(o *openOpts) { o.readOnly = true } }

// Open opens an existing sfga archive at path.
func Open(path string, opts ...OpenOption) (*Archive, error) {
	o := openOpts{}
	for _, opt := range opts {
		opt(&o)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("core: open %s: %w", path, ErrNotFound)
		}
		return nil, fmt.Errorf("core: stat %s: %w", path, err)
	}

	if o.readOnly {
		return openReadOnly(path)
	}
	return openReadWrite(path)
}

func openReadOnly(path string) (*Archive, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", path)
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("core: sql.Open: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("core: enable foreign_keys: %w", err)
	}
	a := &Archive{db: db, path: path, readOnly: true}
	var vErr error
	a.validator, a.ruleLoader, vErr = newHiveValidator(db)
	if vErr != nil {
		db.Close()
		return nil, vErr
	}
	return a, nil
}

func openReadWrite(path string) (*Archive, error) {
	sf := sflib.NewSfga()
	sf.SetDb(path)
	db, err := sf.Connect()
	if err != nil {
		return nil, fmt.Errorf("core: sflib connect %s: %w", path, err)
	}
	// sflib's Connect enables temp_store=MEMORY and journal_mode=WAL; add
	// foreign_keys here since sflib doesn't set it session-wide.
	if _, err := db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("core: enable foreign_keys: %w", err)
	}
	// Seed NOMEN URIs into the nom_status vocab so col__status_id can
	// hold them without tripping the FK. Idempotent — no-op after the
	// first Open on a given archive. See CLAUDE.md § Nomenclatural
	// status vocabulary (NOMEN).
	if err := SeedNomenIntoNomStatus(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	// Add hive-managed tables (hive__* prefix). Idempotent — subsequent
	// Opens are a no-op. Skipped on read-only archives; the read paths
	// tolerate a missing __gsvalidator_results table on legacy files.
	if err := ensureHiveTables(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	a := &Archive{
		sf:   sf,
		db:   db,
		path: path,
		// WithDetails is required for Flatten() to populate the atomized
		// fields (genus, species, basionym_authorship, …). Without it,
		// only top-level fields (canonical, cardinality, authorship,
		// authors) come through — enough for the gn__* cache, not
		// enough for the col__* structural columns hive fills on write.
		parser: gnparser.New(gnparser.NewConfig(gnparser.OptWithDetails(true))),
	}
	var vErr error
	a.validator, a.ruleLoader, vErr = newHiveValidator(db)
	if vErr != nil {
		db.Close()
		return nil, vErr
	}
	// Verify the bundle's Requires block is satisfied by the archive.
	// Failure aborts open — better to refuse than silently misapply
	// rules against an incompatible schema.
	if pkg, err := a.ruleLoader.LoadPackage(context.Background()); err == nil {
		// availableRelations is the union of what this bundle
		// declares (later layered bundles will contribute their own).
		available := make(map[string]bool, len(pkg.Relations))
		for name := range pkg.Relations {
			available[name] = true
		}
		if err := usecase.CheckRequires(context.Background(), db, pkg.Requires, available); err != nil {
			db.Close()
			return nil, fmt.Errorf("core: hive_sfga bundle: %w", err)
		}
	}
	// Evaluate any time-based rules whose cooldown has elapsed since
	// the last recorded run. Cheap on a small rule set — an empty
	// return in normal conditions. Long-running processes cover the
	// "session outlasts a rule's cadence" case via periodic tickers
	// (see internal/server for the HTTP-side loop, internal/tui for
	// Bubble Tea's tea.Every).
	_ = a.RefreshTimeBasedIssues(context.Background())
	return a, nil
}

// Create creates a new sfga archive at path by applying the embedded schema
// DDL. It refuses to overwrite an existing file (returns ErrExists).
//
// This bypasses sflib's Create(dir) workflow, which is geared toward
// archive-conversion pipelines and writes to a fixed schema.sqlite name in a
// working directory. Hive's Create-at-path is simpler: fresh file, apply the
// embedded schema.sql, then hand off to the standard Open path so the
// resulting Archive is indistinguishable from any other read-write open.
func Create(path string) (*Archive, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("core: %s: %w", path, ErrExists)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("core: stat %s: %w", path, err)
	}

	dsn := fmt.Sprintf("file:%s?mode=rwc", path)
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("core: sql.Open: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), Schema); err != nil {
		db.Close()
		os.Remove(path)
		return nil, fmt.Errorf("core: apply schema: %w", err)
	}
	// Seed a placeholder metadata row so col__metadata_id=1 FKs on
	// taxon / name / etc. resolve on the very first write. The
	// placeholder title ("Untitled archive") is intended to be edited
	// by the curator via UpdateMetadata / the PATCH endpoint.
	if err := seedMetadata(context.Background(), db); err != nil {
		db.Close()
		os.Remove(path)
		return nil, err
	}
	if err := db.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("core: close after create: %w", err)
	}
	return openReadWrite(path)
}

// Close releases the underlying database handle.
func (a *Archive) Close() error { return a.db.Close() }

// Path returns the on-disk path of the archive.
func (a *Archive) Path() string { return a.path }

// IsReadOnly reports whether the archive was opened read-only.
func (a *Archive) IsReadOnly() bool { return a.readOnly }

// DB returns the underlying *sql.DB for read operations that don't fit any
// higher-level API. Callers must not begin transactions directly through
// this handle — use WithTx.
func (a *Archive) DB() *sql.DB { return a.db }

// Sflib returns the underlying sflib sfga.Archive for read-write archives.
// Returns nil for read-only archives. Callers use this to drive sflib flows
// (import via Reader/Writer, migration via Migrator, enrichment via
// Enricher) without hive owning a parallel implementation.
func (a *Archive) Sflib() sfga.Archive { return a.sf }

// SchemaVersion returns the version stamped in the archive's `version` table.
// This is the version the archive was created with; it may differ from the
// core package's SchemaVersion constant if the archive is older.
func (a *Archive) SchemaVersion(ctx context.Context) (string, error) {
	// Prefer sflib's cached Version() when available (read-write archives).
	// Read-only archives fall back to a direct query since they bypass sflib.
	if a.sf != nil {
		if v := a.sf.Version(); v != "" {
			return v, nil
		}
	}
	var v string
	err := a.db.QueryRowContext(ctx, "SELECT sf__id FROM version LIMIT 1").Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("core: version row missing: %w", ErrNotFound)
		}
		return "", fmt.Errorf("core: read version: %w", err)
	}
	return v, nil
}

// WithTx runs fn inside a transaction. If fn returns nil the transaction is
// committed; otherwise it is rolled back and fn's error is returned
// unchanged. The *Tx passed to fn carries the actor from ctx (see WithActor).
//
// WithTx returns ErrReadOnly on a read-only archive without opening a
// transaction. It always uses the provided context for both begin and commit.
//
// After a successful commit, WithTx runs each name-side validation sync
// requested via Tx.markNameDirty. Sync errors are swallowed: the write
// already committed, and a stale issue set is preferable to failing the
// caller's request. A future reindex flow will backfill any misses.
func (a *Archive) WithTx(ctx context.Context, fn func(*Tx) error) error {
	if a.readOnly {
		return ErrReadOnly
	}
	sqlTx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("core: begin tx: %w", err)
	}
	tx := &Tx{archive: a, tx: sqlTx, ctx: ctx, actor: ActorFromContext(ctx)}
	if err := fn(tx); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("core: commit: %w", err)
	}
	for id, entry := range tx.dirtyNames {
		_ = a.syncNameIssuesWithSnapshot(ctx, id, entry)
	}
	for id, entry := range tx.dirtyTaxa {
		_ = a.syncTaxonIssuesWithSnapshot(ctx, id, entry)
	}
	for id, entry := range tx.dirtyMetadata {
		_ = a.syncMetadataIssuesWithSnapshot(ctx, id, entry)
	}
	return nil
}

// Tx is a transactional edit context. All mutations in pkg/ take *Tx as a
// receiver, ensuring writes cannot happen outside a transaction. Tx carries a
// back-pointer to its Archive so aggregate methods can reach shared services
// like the gnparser instance and cached enums.
type Tx struct {
	archive *Archive
	tx      *sql.Tx
	ctx     context.Context
	actor   string

	// dirtyNames / dirtyTaxa / dirtyMetadata collect the ids of rows
	// created, updated, or deleted during this transaction plus a
	// pre-mutation snapshot when one was captured. After WithTx
	// commits, each entry drives a post-commit sync that reconciles
	// the record's own __gsvalidator_results AND propagates re-sync
	// to both new-side neighbors (derived from the current record)
	// and old-side neighbors (derived from the snapshot, when set).
	// Sync is best-effort; see WithTx for the failure semantics.
	dirtyNames    map[string]dirtyEntry
	dirtyTaxa     map[string]dirtyEntry
	dirtyMetadata map[int]dirtyEntry
}

// dirtyEntry carries the pre-mutation snapshot of a record touched
// by a mutation, plus a flag indicating whether the record was
// deleted (in which case syncIssuesLocal on the record itself would
// fail to load — the post-commit path skips the local sync and only
// propagates to old-side neighbors + prunes the deleted record's
// own issue rows).
//
// A nil preRecord means "no snapshot captured" — either a CREATE
// (no pre-state exists) or a mutation path that hasn't been
// updated to capture one yet. Post-commit falls back to
// current-value-only neighborhood propagation.
type dirtyEntry struct {
	preRecord map[string]interface{}
	deleted   bool
}

// markNameDirty records that a name row was touched in this transaction
// so WithTx can refresh its validation-issue cache after commit. Called
// from CreateName. Update paths call markNameDirtyWithPre so old-side
// aggregate neighbors get re-synced too.
func (t *Tx) markNameDirty(id string) {
	t.markNameDirtyWithPre(id, nil)
}

// markNameDirtyWithPre records a mutation with an optional
// pre-mutation snapshot. Later dirty marks on the same id preserve
// whichever snapshot arrives first (typically captured before the
// mutation runs).
func (t *Tx) markNameDirtyWithPre(id string, pre map[string]interface{}) {
	if id == "" {
		return
	}
	if t.dirtyNames == nil {
		t.dirtyNames = make(map[string]dirtyEntry)
	}
	existing, seen := t.dirtyNames[id]
	if seen && existing.preRecord != nil {
		pre = existing.preRecord
	}
	t.dirtyNames[id] = dirtyEntry{preRecord: pre, deleted: existing.deleted}
}

// markNameDeleted records a name deletion. Post-commit skips the
// local sync (record no longer exists) but still propagates to
// the pre-neighborhood and prunes the record's own issue rows.
func (t *Tx) markNameDeleted(id string, pre map[string]interface{}) {
	if id == "" {
		return
	}
	if t.dirtyNames == nil {
		t.dirtyNames = make(map[string]dirtyEntry)
	}
	t.dirtyNames[id] = dirtyEntry{preRecord: pre, deleted: true}
}

func (t *Tx) markTaxonDirty(id string) {
	t.markTaxonDirtyWithPre(id, nil)
}

func (t *Tx) markTaxonDirtyWithPre(id string, pre map[string]interface{}) {
	if id == "" {
		return
	}
	if t.dirtyTaxa == nil {
		t.dirtyTaxa = make(map[string]dirtyEntry)
	}
	existing, seen := t.dirtyTaxa[id]
	if seen && existing.preRecord != nil {
		pre = existing.preRecord
	}
	t.dirtyTaxa[id] = dirtyEntry{preRecord: pre, deleted: existing.deleted}
}

func (t *Tx) markTaxonDeleted(id string, pre map[string]interface{}) {
	if id == "" {
		return
	}
	if t.dirtyTaxa == nil {
		t.dirtyTaxa = make(map[string]dirtyEntry)
	}
	t.dirtyTaxa[id] = dirtyEntry{preRecord: pre, deleted: true}
}

func (t *Tx) markMetadataDirty(id int) {
	t.markMetadataDirtyWithPre(id, nil)
}

func (t *Tx) markMetadataDirtyWithPre(id int, pre map[string]interface{}) {
	if t.dirtyMetadata == nil {
		t.dirtyMetadata = make(map[int]dirtyEntry)
	}
	existing, seen := t.dirtyMetadata[id]
	if seen && existing.preRecord != nil {
		pre = existing.preRecord
	}
	t.dirtyMetadata[id] = dirtyEntry{preRecord: pre, deleted: existing.deleted}
}

// snapshotRow reads every column of one row into a map suitable for
// gsvalidator's NeighborhoodsFromRecord. Uses the tx (not the raw
// db) so the snapshot reflects any prior write in the same
// transaction. Returns nil + no error when the row doesn't exist —
// the caller is a mutation path that will surface the "missing
// record" case through its own error path, and the snapshot is
// just best-effort for propagation.
func (t *Tx) snapshotRow(table, pkColumn, id string) (map[string]interface{}, error) {
	if err := safeSnapshotIdent(table); err != nil {
		return nil, err
	}
	if err := safeSnapshotIdent(pkColumn); err != nil {
		return nil, err
	}
	q := `SELECT * FROM "` + table + `" WHERE "` + pkColumn + `" = ?`
	rows, err := t.tx.QueryContext(t.ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("core: snapshot %s %s: %w", table, id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]interface{}, len(cols))
	for i, name := range cols {
		v := values[i]
		if b, ok := v.([]byte); ok {
			out[name] = string(b)
		} else {
			out[name] = v
		}
	}
	return out, nil
}

// snapshotName / snapshotTaxon / snapshotMetadata wrap snapshotRow
// for the three hive-validated tables. Called by mutation methods
// before executing their write so the pre-mutation record is
// captured for post-commit old-side neighbor propagation.
func (t *Tx) snapshotName(id string) (map[string]interface{}, error) {
	return t.snapshotRow("name", "col__id", id)
}
func (t *Tx) snapshotTaxon(id string) (map[string]interface{}, error) {
	return t.snapshotRow("taxon", "col__id", id)
}
func (t *Tx) snapshotMetadata(id int) (map[string]interface{}, error) {
	return t.snapshotRow("metadata", "col__id", strconv.Itoa(id))
}

// safeSnapshotIdent restricts identifier characters mirror of the
// safeIdent helpers in gsvalidator (letters, digits, underscore) so
// snapshotRow's string-interpolated SQL stays injection-safe.
// Duplicated here because the mutation path is inside hive, not
// gsvalidator.
func safeSnapshotIdent(s string) error {
	if s == "" {
		return fmt.Errorf("core: snapshot identifier empty")
	}
	for _, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("core: snapshot identifier %q unsafe", s)
	}
	return nil
}

// Actor returns the actor string carried by this transaction, taken from the
// context passed to WithTx at begin time.
func (t *Tx) Actor() string { return t.actor }

// Context returns the context this transaction was begun with.
func (t *Tx) Context() context.Context { return t.ctx }

// SQL returns the underlying *sql.Tx for aggregate-specific mutation code in
// pkg/taxon.go, pkg/name.go, etc. Not intended for callers outside pkg/.
func (t *Tx) SQL() *sql.Tx { return t.tx }
