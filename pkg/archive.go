package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"

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

	// ruleLoader is the same loader wired into `validator`. Kept as a
	// direct reference so RefreshTimeBasedIssues can read Rule metadata
	// (Trigger, RecheckDays) without going through the use case,
	// which only exposes evaluation.
	ruleLoader *embeddedRuleLoader
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
	a.validator, a.ruleLoader = newHiveValidator(db)
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
	a.validator, a.ruleLoader = newHiveValidator(db)
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
	for id := range tx.dirtyNames {
		_ = a.syncNameIssues(ctx, id)
	}
	for id := range tx.dirtyTaxa {
		_ = a.syncTaxonIssues(ctx, id)
	}
	for id := range tx.dirtyMetadata {
		_ = a.syncMetadataIssues(ctx, id)
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
	// created or updated during this transaction, one map per
	// hive-validated table. After WithTx commits, each entry drives a
	// post-commit call to the corresponding syncXIssues so the
	// __gsvalidator_results cache stays fresh. Sync is best-effort;
	// see WithTx for the failure semantics.
	dirtyNames    map[string]bool
	dirtyTaxa     map[string]bool
	dirtyMetadata map[int]bool
}

// markNameDirty records that a name row was touched in this transaction
// so WithTx can refresh its validation-issue cache after commit. Called
// from CreateName and UpdateName. markTaxonDirty / markMetadataDirty
// serve the same role for their tables; each aggregate's mutation
// methods call the matching helper.
func (t *Tx) markNameDirty(id string) {
	if id == "" {
		return
	}
	if t.dirtyNames == nil {
		t.dirtyNames = make(map[string]bool)
	}
	t.dirtyNames[id] = true
}

func (t *Tx) markTaxonDirty(id string) {
	if id == "" {
		return
	}
	if t.dirtyTaxa == nil {
		t.dirtyTaxa = make(map[string]bool)
	}
	t.dirtyTaxa[id] = true
}

func (t *Tx) markMetadataDirty(id int) {
	if t.dirtyMetadata == nil {
		t.dirtyMetadata = make(map[int]bool)
	}
	t.dirtyMetadata[id] = true
}

// Actor returns the actor string carried by this transaction, taken from the
// context passed to WithTx at begin time.
func (t *Tx) Actor() string { return t.actor }

// Context returns the context this transaction was begun with.
func (t *Tx) Context() context.Context { return t.ctx }

// SQL returns the underlying *sql.Tx for aggregate-specific mutation code in
// pkg/taxon.go, pkg/name.go, etc. Not intended for callers outside pkg/.
func (t *Tx) SQL() *sql.Tx { return t.tx }
