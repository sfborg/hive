package hive

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// hiveSchemaDDL is the DDL for every metadata table hive manages
// inside an sfga archive. Every statement is idempotent
// (CREATE TABLE / INDEX IF NOT EXISTS) so applying the DDL on an
// already-initialized archive is a no-op.
//
// Two naming conventions coexist:
//
//   - __gsvalidator_* — canonical shape shared with any other
//     gsvalidator-using tool (GrandSchema, future consumers). A
//     hive archive validated by hive can be opened in another
//     gsvalidator-aware tool and its issues render immediately.
//     See SCHEMA_COMMONS_REFACTOR.md § "Canonical results table"
//     for the rationale.
//   - hive__* — hive-specific extensions (e.g. metadata modification
//     timestamps that sfga's metadata table lacks). These are hive's
//     own concern, not part of the gsvalidator ecosystem.
//
// Both prefixes are ignored by sflib / sf / gndb / harvester
// (those tools operate on the sfga col__ / gn__ / sf__ / tw__
// namespaces and ignore unknown tables). Exporting via `sf to
// coldp` drops both cleanly; round-tripping through
// `sf from sfga && sf to sfga` preserves them.
const hiveSchemaDDL = `
-- __gsvalidator_results caches validation-rule results so read paths
-- don't have to re-execute rules on every fetch. Canonical name and
-- shape shared with every gsvalidator-using tool so results are
-- portable across the ecosystem.
--
-- Columns:
--   table_name / record_id  — the flagged row
--   rule_id / rule_name     — the rule (name denormalized for
--                             render-without-JOIN)
--   field_name              — which field triggered (empty for
--                             row-level rules; NOT NULL DEFAULT ''
--                             so the identity index below works)
--   severity                — error / warn / info / debug
--   enforcement             — hard / soft
--   message                 — emit-time text
--   actual_value / expected_value — best-effort string forms of
--                             what the rule saw vs what it wanted
--   is_resolved / resolved_at — resolution model (from GrandSchema):
--                             the underlying data was fixed
--   acknowledged_by / acknowledged_at — acknowledgment model (from
--                             hive): a human decided this specific
--                             violation is intentional and should
--                             be muted without fixing the data
--   ruleset_package / ruleset_version — origin tracking: which
--                             SchemaCommons bundle authored this rule
--                             (blank until the bundle-loader lands)
--
-- Resolution and acknowledgment are separate concepts. "Resolved"
-- means the data is now correct. "Acknowledged" means the curator
-- confirmed the violation is intentional. Both flags can be set;
-- neither implies the other.
--
-- The row is a snapshot, not a live query. The unique index below
-- makes (table_name, record_id, rule_id, field_name) the identity
-- of an issue so re-syncs upsert in place, preserving created_at,
-- acknowledgment, and resolution state across reindex runs.
CREATE TABLE IF NOT EXISTS __gsvalidator_results (
	result_id          TEXT PRIMARY KEY,     -- UUID
	table_name         TEXT NOT NULL,
	record_id          TEXT NOT NULL,
	rule_id            TEXT NOT NULL,
	rule_name          TEXT,
	field_name         TEXT NOT NULL DEFAULT '',
	severity           TEXT NOT NULL,
	enforcement        TEXT NOT NULL,
	message            TEXT NOT NULL,
	actual_value       TEXT,
	expected_value     TEXT,
	created_at         TEXT NOT NULL,
	is_resolved        INTEGER NOT NULL DEFAULT 0,
	resolved_at        TEXT,
	acknowledged_by    TEXT,
	acknowledged_at    TEXT,
	ruleset_package    TEXT,
	ruleset_version    TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS uq___gsvalidator_results_identity
	ON __gsvalidator_results(table_name, record_id, rule_id, field_name);

CREATE INDEX IF NOT EXISTS ix___gsvalidator_results_record
	ON __gsvalidator_results(table_name, record_id);

CREATE INDEX IF NOT EXISTS ix___gsvalidator_results_rule
	ON __gsvalidator_results(rule_id);

-- __gsvalidator_rule_state remembers when each time-based rule was
-- last evaluated. A rule with trigger=time_based and recheck_days=N
-- is eligible to re-run once (now - last_run_at) exceeds N days.
-- Persistence keeps rules from re-running on every open when they
-- fired recently; long app-idle stretches still let a rule re-fire
-- because the check happens on Archive.Open and via periodic
-- tickers.
--
-- Also canonical / shared shape with other gsvalidator-using tools
-- so scheduler state moves across the ecosystem alongside results.
CREATE TABLE IF NOT EXISTS __gsvalidator_rule_state (
	rule_id      TEXT PRIMARY KEY,
	last_run_at  TEXT NOT NULL
);

-- hive__metadata_touched records when the archive's metadata row was
-- last modified. sfga's metadata table lacks a col__modified column
-- and CLAUDE.md § Schema handling forbids extending sfga tables, so
-- hive tracks the timestamp in a hive-owned sidecar. Populated by
-- Tx.UpdateMetadata + seeded on Create; read by the future
-- stale-metadata rule. One row per metadata id (id=1 in normal
-- single-metadata-row use). Not gsvalidator's concern — this is a
-- hive-specific workaround for a sfga gap (see SFGA_CHANGES.md).
CREATE TABLE IF NOT EXISTS hive__metadata_touched (
	id          INTEGER PRIMARY KEY,
	touched_at  TEXT NOT NULL
);

-- hive__config_rules holds per-rule overrides of enabled state and
-- severity. Missing row (or NULL columns) means "use the ruleset
-- bundle's default." CHECK constraints reject bad values at write
-- time so a typo in a CLI or hand-edit can't silently mis-load.
--
-- Purpose-built rather than a generic KV so the shape is
-- self-documenting, sfga diff of two curators' archives shows
-- clean per-rule row differences, and typos in setting names are
-- impossible.
CREATE TABLE IF NOT EXISTS hive__config_rules (
	rule_id            TEXT PRIMARY KEY,
	enabled            INTEGER CHECK (enabled IS NULL OR enabled IN (0, 1)),
	severity_override  TEXT    CHECK (severity_override IS NULL OR severity_override IN ('error', 'warn', 'info', 'debug')),
	updated_at         TEXT    NOT NULL,
	updated_by         TEXT    NOT NULL DEFAULT ''
);

-- hive__config_rulesets toggles whole ruleset bundles (hive / clb /
-- tw) on or off. A missing row means the ruleset uses its default
-- (enabled). Coarser than hive__config_rules — sits above it in the
-- precedence order: disabling a ruleset skips all its rules regardless
-- of per-rule overrides.
CREATE TABLE IF NOT EXISTS hive__config_rulesets (
	ruleset_name  TEXT PRIMARY KEY,
	enabled       INTEGER NOT NULL CHECK (enabled IN (0, 1)),
	updated_at    TEXT    NOT NULL,
	updated_by    TEXT    NOT NULL DEFAULT ''
);

-- Indices on sfga tables. Named with the standard idx_ prefix (not
-- hive__) so that if sfga adopts them upstream the CREATE IF NOT EXISTS
-- guard turns into a no-op on newly-imported archives. sflib/sf/gndb
-- ignore extra indices.
--
-- idx_taxon_parent_id turns tree-navigation queries — ListChildrenPage,
-- Ancestors' recursive CTE, MoveTaxon cycle checks, the has_children
-- EXISTS probe — from full table scans of the taxon table into index
-- range scans. On a 2.7M-row taxon table (COL 26-07) this drops
-- children-of-X from ~150 ms per query to under 1 ms, which is the
-- difference between a snappy tree reveal and a multi-second wait.
CREATE INDEX IF NOT EXISTS idx_taxon_parent_id ON taxon (col__parent_id);

-- idx_taxon_name_id lets JOIN-through-taxon queries (SearchTaxa,
-- classification, synonym resolution) use an index on the taxon side
-- instead of scanning taxon and looking up name by PK per row.
CREATE INDEX IF NOT EXISTS idx_taxon_name_id ON taxon (col__name_id);

-- Case-insensitive prefix indices on the two name-text columns the
-- typeahead searches. sfga ships BINARY-collation indices on both
-- columns (idx_name_scientific_name, idx_name_canonical_simple);
-- those don't satisfy LIKE 'q%' because SQLite requires a NOCASE
-- index (or PRAGMA case_sensitive_like=ON, which we don't want to
-- flip session-wide). SearchTaxa uses the query pattern q||'%' so
-- both indices are index-range-scanned instead of full-scanned.
CREATE INDEX IF NOT EXISTS idx_name_scientific_name_nocase
	ON name (col__scientific_name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_name_canonical_simple_nocase
	ON name (gn__canonical_simple COLLATE NOCASE);

-- idx_synonym_name_id makes the synonym arm of SearchTaxa
-- (include_synonyms=true) fast: after matching a synonym's name row
-- via the NOCASE prefix indices above, hive follows synonym.col__name_id
-- back to the synonym row and then to the accepted taxon. sfga indexes
-- synonym.col__id and synonym.col__taxon_id but not col__name_id, so
-- the name→synonym lookup would otherwise scan the 2.7M-row synonym
-- table.
CREATE INDEX IF NOT EXISTS idx_synonym_name_id ON synonym (col__name_id);

-- hive__name_fts is an FTS5 mirror of name.gn__canonical_simple and
-- name.col__scientific_name, tokenized with unicode61 (word-based,
-- diacritic-stripped). Backs the "partial" search mode: curators can
-- type an epithet like "rusci" and MATCH 'rusci*' finds "Ceroplastes
-- rusci" via token-prefix, or a multi-word fragment like "cero rusci"
-- and MATCH 'cero* rusci*' finds it regardless of word order.
--
-- content='name' is FTS5's external-content mode — the FTS table
-- stores only the tokenized index, not the text itself, so storage
-- overhead is small (~4% on COL 26-07: 5.4M rows, ~200MB index).
-- Query results give us name.rowid; we JOIN back for the actual
-- columns.
--
-- Initial populate is a one-shot INSERT ... SELECT that runs the
-- first time hive opens an archive with a populated name table but
-- an empty FTS mirror (see ensureNameFTSPopulated). On COL 26-07 the
-- build takes ~9s; subsequent opens are instant.
--
-- remove_diacritics 2 strips accents so 'Flüela' and 'Fluela' match
-- interchangeably — useful for taxonomic names that carry Latin /
-- German umlauts.
-- col__authorship is indexed alongside the name columns so a
-- query like "sigillatus Walker" — epithet + author surname —
-- resolves via a single FTS MATCH that requires both tokens.
-- sfga stores authorship in a dedicated column (not appended to
-- col__scientific_name), so without indexing it here the FTS arms
-- would silently miss the author-search case entirely.
CREATE VIRTUAL TABLE IF NOT EXISTS hive__name_fts USING fts5(
  gn__canonical_simple,
  col__scientific_name,
  col__authorship,
  content='name',
  tokenize='unicode61 remove_diacritics 2'
);

-- Triggers keep the FTS index in sync with the name table on every
-- INSERT / UPDATE / DELETE. External-content FTS5 requires the
-- special 'delete' command form for removals — passing old.rowid +
-- old columns tells FTS5 which tokens to unindex. On UPDATE we emit
-- a delete + insert pair so a canonical-name change (e.g., gnparser
-- refresh) doesn't leave stale tokens.
CREATE TRIGGER IF NOT EXISTS hive__name_fts_ai AFTER INSERT ON name BEGIN
  INSERT INTO hive__name_fts(rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES (new.rowid, new.gn__canonical_simple, new.col__scientific_name, new.col__authorship);
END;
CREATE TRIGGER IF NOT EXISTS hive__name_fts_ad AFTER DELETE ON name BEGIN
  INSERT INTO hive__name_fts(hive__name_fts, rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES ('delete', old.rowid, old.gn__canonical_simple, old.col__scientific_name, old.col__authorship);
END;
CREATE TRIGGER IF NOT EXISTS hive__name_fts_au AFTER UPDATE ON name BEGIN
  INSERT INTO hive__name_fts(hive__name_fts, rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES ('delete', old.rowid, old.gn__canonical_simple, old.col__scientific_name, old.col__authorship);
  INSERT INTO hive__name_fts(rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES (new.rowid, new.gn__canonical_simple, new.col__scientific_name, new.col__authorship);
END;

-- hive__name_fts_tri is a second FTS5 mirror using the trigram
-- tokenizer — every column value is broken into overlapping 3-
-- character sequences. Backs the "fuzzy" search mode: at query
-- time we split the user's input into its own trigrams and OR
-- them against the index, so a typo like "Cerpolastes" (which
-- shares 5 of 8 trigrams with "Ceroplastes") still surfaces the
-- intended match. bm25 ranks by cumulative trigram overlap so
-- the closest matches float to the top.
--
-- Costs more to build and to store than the unicode61 mirror
-- (COL 26-07: ~26 s build one-time, ~700 MB overhead vs 200 MB)
-- because it indexes overlapping windows rather than words, but
-- storage is still under 15% of the archive and query time stays
-- under 500 ms per fuzzy search — well inside the combobox's
-- 500 ms debounce.
--
-- remove_diacritics 1 (rather than the unicode61 mirror's 2)
-- strips accents without extra Unicode normalization — enough
-- for taxonomic names and cheaper on very large builds.
CREATE VIRTUAL TABLE IF NOT EXISTS hive__name_fts_tri USING fts5(
  gn__canonical_simple,
  col__scientific_name,
  col__authorship,
  content='name',
  tokenize='trigram remove_diacritics 1'
);

-- Trigger set mirrors the unicode61 sync pattern above. Every
-- INSERT / UPDATE / DELETE on name propagates to the trigram
-- index so both mirrors stay coherent under normal editing.
CREATE TRIGGER IF NOT EXISTS hive__name_fts_tri_ai AFTER INSERT ON name BEGIN
  INSERT INTO hive__name_fts_tri(rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES (new.rowid, new.gn__canonical_simple, new.col__scientific_name, new.col__authorship);
END;
CREATE TRIGGER IF NOT EXISTS hive__name_fts_tri_ad AFTER DELETE ON name BEGIN
  INSERT INTO hive__name_fts_tri(hive__name_fts_tri, rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES ('delete', old.rowid, old.gn__canonical_simple, old.col__scientific_name, old.col__authorship);
END;
CREATE TRIGGER IF NOT EXISTS hive__name_fts_tri_au AFTER UPDATE ON name BEGIN
  INSERT INTO hive__name_fts_tri(hive__name_fts_tri, rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES ('delete', old.rowid, old.gn__canonical_simple, old.col__scientific_name, old.col__authorship);
  INSERT INTO hive__name_fts_tri(rowid, gn__canonical_simple, col__scientific_name, col__authorship)
  VALUES (new.rowid, new.gn__canonical_simple, new.col__scientific_name, new.col__authorship);
END;
`

// ensureHiveTables applies the DDL for every hive-managed metadata
// table and runs one-shot migrations for archives created against
// an older schema. Safe to call on every openReadWrite / Create —
// each statement is idempotent. Not safe on a read-only archive.
//
// Order matters:
//  1. migrate any pre-canonical table names (hive__validation_issue
//     → __gsvalidator_results, hive__rule_state →
//     __gsvalidator_rule_state) before running the CREATE TABLE
//     statements, so the CREATEs don't collide with old-name tables
//     still holding data.
//  2. apply the canonical DDL — creates any missing tables and
//     indices.
//  3. backfill hive__metadata_touched for any metadata row without
//     a tracked timestamp.
func ensureHiveTables(ctx context.Context, db *sql.DB) error {
	if err := migratePreCanonicalTables(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, hiveSchemaDDL); err != nil {
		return fmt.Errorf("core: ensure hive tables: %w", err)
	}
	// Backfill hive__metadata_touched for any metadata row without a
	// tracked timestamp. New archives seed it via touchMetadata during
	// Create's seedMetadata; older archives (opened for the first
	// time under a hive version that ships this table) get a fresh
	// baseline stamped as "now" — rules that key off staleness start
	// their clock from first open, not from an unknowable historical
	// modification date.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `
		INSERT INTO hive__metadata_touched (id, touched_at)
		SELECT col__id, ? FROM metadata
		WHERE col__id NOT IN (SELECT id FROM hive__metadata_touched)`,
		now,
	)
	if err != nil {
		return fmt.Errorf("core: backfill hive__metadata_touched: %w", err)
	}
	if err := ensureFTSPopulated(ctx, db, "hive__name_fts"); err != nil {
		return err
	}
	if err := ensureFTSPopulated(ctx, db, "hive__name_fts_tri"); err != nil {
		return err
	}
	return nil
}

// ensureFTSPopulated runs the one-shot initial build of an FTS5
// mirror table when its token index is empty but the name table is
// not. Handles two cases:
//
//  1. Fresh archive from a non-hive tool (harvester, sf, older
//     hive without the mirror in question) — the CREATE VIRTUAL
//     TABLE runs for the first time and the token index is empty.
//  2. Archive where an earlier hive version's populate path used
//     `INSERT INTO fts(rowid, col1, col2) SELECT ...`, which under
//     modernc.org/sqlite silently populates the docsize shadow
//     table without actually tokenizing content — leaving MATCH
//     queries returning zero results despite `SELECT COUNT(*) FROM
//     fts` reporting a positive number.
//
// Detection checks `<fts>_idx`, the segment index shadow table that
// FTS5's own build path populates. Docsize-populated-but-idx-empty
// is the broken state; `rebuild` fixes it without a DROP TABLE
// round-trip. On a truly empty archive both shadow tables are
// empty, and we skip the rebuild.
//
// Deliberately does NOT try to repair partial staleness (name rows
// added by an external tool that bypasses the sync triggers).
// v0 assumes hive is the sole writer once an archive is opened by
// hive; a POST /api/reindex/name-fts endpoint would let curators
// force a rebuild if that assumption breaks.
//
// ftsTable is a code-controlled constant (see ensureHiveTables); it
// is string-interpolated into the SQL, which is safe because it is
// never user-derived.
//
// Build cost on COL 26-07 (5.4M name rows): ~9 s for unicode61,
// ~26 s for trigram — both one-time. Total first-open latency on a
// COL-scale archive with both mirrors missing is ~35 s.
func ensureFTSPopulated(ctx context.Context, db *sql.DB, ftsTable string) error {
	var idxRows int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+ftsTable+`_idx`,
	).Scan(&idxRows); err != nil {
		return fmt.Errorf("core: count %s_idx: %w", ftsTable, err)
	}
	if idxRows > 0 {
		return nil
	}
	var nameRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM name`).Scan(&nameRows); err != nil {
		return fmt.Errorf("core: count name: %w", err)
	}
	if nameRows == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO `+ftsTable+`(`+ftsTable+`) VALUES('rebuild')`,
	); err != nil {
		return fmt.Errorf("core: rebuild %s: %w", ftsTable, err)
	}
	return nil
}

// migratePreCanonicalTables renames archives' legacy hive-owned
// metadata tables to the canonical gsvalidator-shared names. Runs
// before the canonical DDL so ALTER TABLE ... RENAME doesn't
// collide with a same-named IF-NOT-EXISTS create.
//
//   - hive__validation_issue → __gsvalidator_results (+ id → result_id
//     column rename, + is_resolved / resolved_at / ruleset_package /
//     ruleset_version columns added). Done in place: the old table's
//     rows carry over unchanged.
//   - hive__rule_state → __gsvalidator_rule_state (schema-identical
//     rename).
//
// The migration is idempotent: if the old table doesn't exist, or
// the new table already does, the corresponding step is a no-op.
// Both tables have small footprints — hive__validation_issue caps at
// (rules × records) rows and is rewritten on every reindex anyway —
// so an in-place migration is safe and fast.
func migratePreCanonicalTables(ctx context.Context, db *sql.DB) error {
	if err := migrateValidationIssueTable(ctx, db); err != nil {
		return err
	}
	if err := migrateRuleStateTable(ctx, db); err != nil {
		return err
	}
	if err := migrateNameFTSSchema(ctx, db); err != nil {
		return err
	}
	return nil
}

// migrateNameFTSSchema drops any hive__name_fts / hive__name_fts_tri
// virtual table that predates the col__authorship column so the
// canonical DDL in hiveSchemaDDL can recreate it with the current
// column set. Detection uses PRAGMA table_info; if the FTS's column
// list doesn't include col__authorship, we DROP and the subsequent
// ensureFTSPopulated call rebuilds fresh.
//
// The alternative (ADD COLUMN via `INSERT INTO fts(fts, rank)
// VALUES('rebuild', ...)`) doesn't exist for FTS5 — column set is
// baked in at CREATE. Drop + recreate + rebuild is the canonical
// path per the FTS5 docs.
func migrateNameFTSSchema(ctx context.Context, db *sql.DB) error {
	for _, tbl := range []string{"hive__name_fts", "hive__name_fts_tri"} {
		exists, err := tableExists(ctx, db, tbl)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		has, err := ftsHasColumn(ctx, db, tbl, "col__authorship")
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS `+tbl); err != nil {
			return fmt.Errorf("core: drop stale %s (missing col__authorship): %w", tbl, err)
		}
	}
	return nil
}

// ftsHasColumn reports whether the FTS5 virtual table lists column
// among its indexed columns. Reads PRAGMA table_info — which for
// FTS5 returns the user-facing column list including RANK and
// hidden columns, so we filter by exact name match.
func ftsHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("core: table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("core: scan table_info %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func migrateValidationIssueTable(ctx context.Context, db *sql.DB) error {
	hasOld, err := tableExists(ctx, db, "hive__validation_issue")
	if err != nil {
		return err
	}
	hasNew, err := tableExists(ctx, db, "__gsvalidator_results")
	if err != nil {
		return err
	}
	if !hasOld || hasNew {
		return nil
	}
	// Rename the table and its column, then let the canonical DDL
	// create the missing indices + augment nullable columns. SQLite
	// doesn't support ADD COLUMN + set NOT NULL in one step, so the
	// new columns are added nullable / with a default and never need
	// backfilling: is_resolved defaults to 0, the rest stay NULL
	// until acknowledgment / resolution / origin tracking wire up.
	stmts := []string{
		`ALTER TABLE hive__validation_issue RENAME TO __gsvalidator_results`,
		`ALTER TABLE __gsvalidator_results RENAME COLUMN id TO result_id`,
		`ALTER TABLE __gsvalidator_results ADD COLUMN is_resolved INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE __gsvalidator_results ADD COLUMN resolved_at TEXT`,
		`ALTER TABLE __gsvalidator_results ADD COLUMN ruleset_package TEXT`,
		`ALTER TABLE __gsvalidator_results ADD COLUMN ruleset_version TEXT`,
		// Drop the old indices so the CREATE INDEX IF NOT EXISTS
		// statements below add fresh ones under the new table name.
		`DROP INDEX IF EXISTS uq_hive__validation_issue_identity`,
		`DROP INDEX IF EXISTS ix_hive__validation_issue_record`,
		`DROP INDEX IF EXISTS ix_hive__validation_issue_rule`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("core: migrate hive__validation_issue: %s: %w", s, err)
		}
	}
	return nil
}

func migrateRuleStateTable(ctx context.Context, db *sql.DB) error {
	hasOld, err := tableExists(ctx, db, "hive__rule_state")
	if err != nil {
		return err
	}
	hasNew, err := tableExists(ctx, db, "__gsvalidator_rule_state")
	if err != nil {
		return err
	}
	if !hasOld || hasNew {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`ALTER TABLE hive__rule_state RENAME TO __gsvalidator_rule_state`,
	); err != nil {
		return fmt.Errorf("core: migrate hive__rule_state: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`,
		name,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("core: check for table %s: %w", name, err)
	}
	return count > 0, nil
}
