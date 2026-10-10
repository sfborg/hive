package hive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

-- hive__reference_pdf associates a reference row with one or more
-- source-document sidecars (PDF / EPUB / HTML / JATS). The sha512
-- is the sidecar library's content-addressed key — the actual file
-- lives at <archive>.library/<sha>.<ext>. Populated by the upload
-- handler when a curator attaches a file to a reference; read by
-- the references screen and reader components to know which sha
-- (if any) to fetch for the currently-selected reference.
--
-- Composite primary key allows one reference to carry multiple
-- attachments (multi-media works, translated editions) — rare but
-- semantically valid. Same sha can appear across multiple
-- references (two curators point at the same content from
-- different reference rows) without violating uniqueness.
CREATE TABLE IF NOT EXISTS hive__reference_pdf (
	reference_id  TEXT NOT NULL,
	sha512        TEXT NOT NULL,
	attached_at   TEXT NOT NULL,
	attached_by   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (reference_id, sha512)
);

CREATE INDEX IF NOT EXISTS idx_hive__reference_pdf_sha
	ON hive__reference_pdf (sha512);

-- hive__reference_assertion is the staging table for structured
-- claims extracted from a reference's attached-PDF JATS sidecar,
-- awaiting curator review before landing in the archive proper.
-- Each row represents one claim the paper makes (a taxon
-- appearance, a distribution statement, a vernacular name, a
-- type-material designation, an interaction observation, ...) that
-- a model or curator authored.
--
-- Called "assertion" rather than "fact" because taxonomic claims
-- are hypotheses in a scientific sense: a species identification
-- can be revised, an interaction observation could reflect a
-- misidentification, a nomenclatural act could be superseded. Even
-- direct observations ("we saw species A eating species B") are
-- assertions about the world that later work can challenge. The
-- staging table records what a reference *asserts*; downstream
-- users decide what weight to give the assertion.
--
-- Columns:
--   assertion_id   UUID for the staged row
--   reference_id   which reference the assertion belongs to
--   assertion_type kind of assertion (taxon / distribution /
--                  vernacular / type_material / synonym /
--                  interaction / ...); free-form so new extractor
--                  targets don't require a schema change
--   payload        JSON — assertion-type-specific structured
--                  content; mirrors the corresponding sflib/coldp
--                  type where one exists
--   source_ptr     JSON — where in the JATS sidecar the assertion
--                  came from: {sidecar_sha, element_id, text_start,
--                  text_end}; lets the reader jump-and-highlight
--                  on click
--   origin         'model' (extractor output) or 'curator'
--                  (manually added). Model re-runs delete
--                  origin='model' rows for the reference and
--                  re-populate; curator rows are durable across
--                  re-runs.
--   status         'pending' (awaiting review) / 'accepted' /
--                  'rejected' / 'edited' (curator modified before
--                  accepting)
--   reject_reason  free-text or picklist value when rejected —
--                  feeds the training-signal loop (see
--                  MODEL_FEEDBACK_LOOP_PLAN)
--   imported_at    set when the assertion has been written into
--                  the archive proper as a real coldp row; NULL
--                  while still staged
--   created_at / updated_at / created_by  standard audit fields;
--                  created_by is an ORCID iD for curator rows and
--                  'model:<version>' for extractor runs
--
-- Indexes support the two hot query paths: "list assertions for
-- reference X" (per-reference review screen) and "count pending vs
-- imported for reference X" (dashboard step-3 / step-4 detection).
CREATE TABLE IF NOT EXISTS hive__reference_assertion (
	assertion_id   TEXT PRIMARY KEY,
	reference_id   TEXT NOT NULL,
	assertion_type TEXT NOT NULL,
	payload        TEXT NOT NULL,
	source_ptr     TEXT NOT NULL DEFAULT '',
	origin         TEXT NOT NULL CHECK (origin IN ('model', 'curator')),
	status         TEXT NOT NULL DEFAULT 'pending'
	                 CHECK (status IN ('pending', 'accepted', 'rejected', 'edited')),
	reject_reason  TEXT,
	imported_at    TEXT,
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL,
	created_by     TEXT NOT NULL DEFAULT '',
	-- source_run_id links a materialized assertion back to the
	-- hive__extraction_run row it was parsed out of. Nullable —
	-- curator-authored assertions have no run to link to.
	-- Soft-deleting the parent run hides these via a join filter
	-- rather than cascading DELETE (the row stays for audit).
	source_run_id  TEXT,
	-- asserted_by is the view-filter column per the
	-- assertions-as-source-of-truth design memory: who is CLAIMING
	-- the assertion (curator ORCID for curator entries, synthetic
	-- "llm-run:<id>" for LLM outputs). Distinct from created_by
	-- (who added the row to the local DB). Tree-render recursive
	-- CTEs filter on this column to swap views. Nullable — legacy
	-- rows predate this column and fall back to created_by for
	-- filtering.
	asserted_by    TEXT
);

CREATE INDEX IF NOT EXISTS idx_hive__reference_assertion_ref
	ON hive__reference_assertion (reference_id);

CREATE INDEX IF NOT EXISTS idx_hive__reference_assertion_status
	ON hive__reference_assertion (reference_id, status);

-- source_run_id index makes "list assertions produced by this run"
-- fast — the hot query when a curator opens a run's review screen.
CREATE INDEX IF NOT EXISTS idx_hive__reference_assertion_source_run
	ON hive__reference_assertion (source_run_id);

-- asserted_by index supports the multi-view tree-render recursive
-- CTE's WHERE clause. Sub-second even at Coleoptera scale on
-- SQLite (per assertions-as-source-of-truth benchmark note).
CREATE INDEX IF NOT EXISTS idx_hive__reference_assertion_asserted_by
	ON hive__reference_assertion (asserted_by);

-- hive__extraction_correction records curator corrections to the
-- attached-PDF extraction pipeline (GROBID → jatsconv → JATS).
-- Each row is one durable, re-applicable correction: draw a bbox
-- over a missed figure, tag a range of text as junk, fix a broken
-- xref rid, or feed back a rejected/reclassified GNfinder chip.
--
-- Corrections mutate the JATS sidecar so the digitized view
-- immediately reflects the fix, but the correction records
-- themselves are the authoritative artifact. On re-digitize (fresh
-- GROBID pass), active corrections re-apply on top of the new JATS
-- so curator work isn't lost when the model updates.
--
-- Every correction is also a labeled training example fed into the
-- model-feedback loop (see MODEL_FEEDBACK_LOOP_PLAN.md) — bbox-add
-- rows label under-detection, text-tag rows label false-positive
-- extraction, xref-fix rows label reference-resolver misses,
-- chip-feedback rows label GNfinder decisions.
--
-- Columns:
--   correction_id   UUID
--   reference_id    which reference owns the correction
--   sidecar_sha     which attached-PDF sidecar the correction
--                   applies to (a ref can have multiple PDFs)
--   correction_kind how the correction changes the JATS:
--                     'bbox-add'      — new fig/table/abstract/... region
--                     'text-tag'      — remove or reclassify body text
--                     'xref-fix'      — repoint an <xref>'s rid
--                     'chip-feedback' — reject/edit/reclassify a
--                                       tp:taxon-name chip
--   content_type    free-form string identifying what kind of
--                   content the correction concerns (figure /
--                   table / abstract / section / paragraph /
--                   page-number / junk / bib-field / ...). New
--                   types add by appending — no enum, no schema
--                   change needed for the training corpus to grow.
--   bbox_page       1-indexed PDF page for bbox-carrying kinds;
--                   NULL for text-only corrections
--   bbox_json       JSON {x, y, w, h} in PDF points for
--                   bbox-carrying kinds; NULL otherwise
--   label           curator-visible label ("abstract-2",
--                   "figure-3-fixed"). Auto-suggested by the UI
--                   from content_type + ordinal; curator override
--                   sticks.
--   anchor_ptr      JSON JATS pointer for cross-surface placement:
--                   {element_id, position: 'before'|'after'|'inside-end'}.
--                   NULL when correction has no destination anchor
--                   (fig/table default to trailing sec).
--   payload         correction-kind-specific JSON. For bbox-add
--                   it's usually {} (text extracted at apply time);
--                   for text-tag it's {action, segments, text}
--                   (see correction.TextTagPayload); for xref-fix it's
--                   {xref_element_id, old_rid, new_rid,
--                   xref_text}; for chip-feedback it's {action,
--                   chip_element_id, old_source, new_source,
--                   old_full_name, new_full_name}.
--   status          'active' (applied on next regen) or 'reverted'
--                   (skipped because a re-digitize left the
--                   correction unanchorable)
--   reverted_reason populated when status transitions to
--                   'reverted' so curator sees why
--   created_at / updated_at / created_by  audit fields; created_by
--                   is the ORCID iD of the curator who authored
--                   the correction
CREATE TABLE IF NOT EXISTS hive__extraction_correction (
	correction_id      TEXT PRIMARY KEY,
	reference_id       TEXT NOT NULL,
	sidecar_sha        TEXT NOT NULL,
	correction_kind    TEXT NOT NULL
	                     CHECK (correction_kind IN
	                       ('bbox-add', 'bbox-modify', 'bbox-remove',
	                        'text-tag', 'text-remove', 'text-restore',
	                        'xref-fix', 'chip-feedback',
	                        'ref-merge', 'ref-edit',
	                        'assertion-edit', 'page-numbering')),
	content_type       TEXT NOT NULL,
	bbox_page          INTEGER,
	bbox_json          TEXT,
	label              TEXT NOT NULL DEFAULT '',
	anchor_ptr         TEXT,
	-- target_element_id points at the JATS element (fig / table-wrap)
	-- the correction modifies. Populated for bbox-modify kind so
	-- ApplyBBoxModifyToJATS can find and patch the target; NULL for
	-- additive kinds (bbox-add) that create a new element.
	target_element_id  TEXT,
	payload            TEXT NOT NULL DEFAULT '',
	status             TEXT NOT NULL DEFAULT 'active'
	                     CHECK (status IN ('active', 'reverted')),
	reverted_reason    TEXT,
	created_at         TEXT NOT NULL,
	updated_at         TEXT NOT NULL,
	created_by         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_hive__extraction_correction_ref
	ON hive__extraction_correction (reference_id);

CREATE INDEX IF NOT EXISTS idx_hive__extraction_correction_sha
	ON hive__extraction_correction (sidecar_sha);

-- hive__reference_test_set records references curators have marked
-- for the evaluation test set. Presence of a row = the reference
-- is in the test set; absence = it isn't. The flag is separate
-- from hive__extraction_run.is_gold_standard: the test-set flag
-- marks a PAPER as belonging to the evaluation set (intent, and a
-- work queue for curator annotation), while the run flag marks a
-- specific curator-authored ANNOTATION as ground truth against
-- which LLM runs are scored. A paper can be in the test set
-- before it has a gold run (intent captured; annotation pending).
--
-- Corpus-scale invocation surfaces test-set papers first + marks
-- them in dashboards so curators know which references anchor the
-- scoring pipeline. Test-set papers get mixed into batch runs so
-- the LLM cannot distinguish them from ordinary corpus papers —
-- their scores anchor the sampling-based accuracy estimate.
CREATE TABLE IF NOT EXISTS hive__reference_test_set (
    reference_id  TEXT PRIMARY KEY,
    added_at      TEXT NOT NULL,
    added_by      TEXT NOT NULL DEFAULT '',
    notes         TEXT NOT NULL DEFAULT ''
);

-- hive__extraction_run records one invocation of an extraction
-- pipeline: an LLM NER pass, an LLM compose pass, a future
-- hive-native NER model run, or a curator's manual annotation.
-- Runs persist by default so curators can compare model outputs,
-- review after the fact, and see cost accountability. Soft-delete
-- (deleted_at) keeps cost visibility even after a curator "deletes"
-- a run — throwing away the row would lose the paid-for context.
--
-- Multiple runs per reference are expected and normal: curators may
-- run the same paper through Opus + Sonnet + Haiku to compare
-- quality vs cost, or iterate on prompt versions. The comparison UI
-- (paper + N candidate trees, plus corpus-level dashboard rolling
-- up scores across the test set) reads this table.
--
-- Assertions produced by each run will live in
-- hive__reference_assertion linked via a source_run_id column
-- (added when the assertion-materialization slice lands); the
-- source_kind + is_gold_standard columns here are what enables the
-- gold-standard test-set scoring described in
-- project_extraction_runs_multi_model_schema memory.
--
-- Columns:
--   run_id           UUID PK
--   reference_id     which paper the run processed; nullable
--                    because ad-hoc /api/llm/invoke test calls
--                    aren't necessarily paper-scoped
--   purpose          'ner' | 'compose' | future 'translate' — what
--                    this run represents in the pipeline
--   source_kind      'llm' | 'hive_ner' | 'curator' — same table
--                    houses LLM invocations AND future hive-trained
--                    NER model outputs AND curator manual entries,
--                    so multi-model comparison + gold-standard
--                    scoring work uniformly across all three
--   source_id        provider name for llm, model version for
--                    hive_ner, ORCID for curator
--   envelope_json    full llm.Envelope JSON — prompt hash, provider,
--                    model version, tokens, invoked_at, raw output;
--                    the authoritative provenance record. Empty
--                    string for source_kind='curator'.
--   raw_output_json  parsed NER/compose output (also inside envelope
--                    for llm runs; denormalized here for query
--                    speed on the comparison UI's hot path)
--   parent_run_id    nullable — compose runs link to the NER run
--                    whose assertions they consumed; the WUI walks
--                    this pointer to render "the NER output this
--                    compose was built from"
--   cost_usd         unused; always 0 for new runs. hive reports
--                    provider token counts only and never computes
--                    dollar costs. Kept so existing archives need no
--                    migration.
--   is_gold_standard 0/1 — curator flags a run as ground truth for
--                    scoring. When batch runs process a corpus,
--                    LLM output on gold-flagged references is
--                    compared to the gold run's assertions to
--                    compute precision/recall/F1 — sampling-based
--                    accuracy estimation. See scoring notes in
--                    project_extraction_runs_multi_model_schema.
--   notes            curator annotations (free text)
--   created_at
--   deleted_at       soft delete — non-NULL means "hidden from
--                    default views but still counted in cost
--                    dashboards and restorable"
CREATE TABLE IF NOT EXISTS hive__extraction_run (
    run_id            TEXT PRIMARY KEY,
    reference_id      TEXT,
    purpose           TEXT NOT NULL
                        CHECK (purpose IN ('ner', 'compose', 'translate')),
    source_kind       TEXT NOT NULL
                        CHECK (source_kind IN ('llm', 'hive_ner', 'curator')),
    source_id         TEXT NOT NULL,
    envelope_json     TEXT NOT NULL DEFAULT '',
    raw_output_json   TEXT NOT NULL DEFAULT '',
    parent_run_id     TEXT,
    cost_usd          REAL NOT NULL DEFAULT 0,
    is_gold_standard  INTEGER NOT NULL DEFAULT 0
                        CHECK (is_gold_standard IN (0, 1)),
    notes             TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    deleted_at        TEXT
);

CREATE INDEX IF NOT EXISTS idx_hive__extraction_run_ref
    ON hive__extraction_run (reference_id);

CREATE INDEX IF NOT EXISTS idx_hive__extraction_run_purpose
    ON hive__extraction_run (purpose, source_kind);

CREATE INDEX IF NOT EXISTS idx_hive__extraction_run_parent
    ON hive__extraction_run (parent_run_id);

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

-- hive__config_rulesets toggles whole ruleset bundles (hive, clb, tw,
-- tw_<code>) on or off. A missing row means the ruleset uses its default
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

-- idx_species_interaction_taxon_id supports
-- GET /api/taxon/{id}/species-interactions. sfga ships no index on
-- either species_interaction.col__taxon_id or col__related_taxon_id,
-- so per-taxon listing would scan the whole table on any archive
-- that populates this vocabulary.
CREATE INDEX IF NOT EXISTS idx_species_interaction_taxon_id
	ON species_interaction (col__taxon_id);

-- idx_distribution_taxon_id supports GET /api/taxon/{id}/distributions
-- and any per-taxon distribution query. sfga ships an index on
-- vernacular.col__taxon_id but NOT on distribution.col__taxon_id;
-- COL 26-07 has 2.2M distribution rows, so an unindexed lookup would
-- scan the whole table on every taxon-detail render.
CREATE INDEX IF NOT EXISTS idx_distribution_taxon_id ON distribution (col__taxon_id);

-- idx_synonym_name_id makes the synonym arm of SearchTaxa
-- (include_synonyms=true) fast: after matching a synonym's name row
-- via the NOCASE prefix indices above, hive follows synonym.col__name_id
-- back to the synonym row and then to the accepted taxon. sfga indexes
-- synonym.col__id and synonym.col__taxon_id but not col__name_id, so
-- the name→synonym lookup would otherwise scan the 2.7M-row synonym
-- table.
CREATE INDEX IF NOT EXISTS idx_synonym_name_id ON synonym (col__name_id);

-- name_relation and type_material indices serve the validation
-- relations that walk from a name to its relations (type name,
-- homonym, replacement, basionym) and to its type material. sfga
-- indexes neither table on its name columns, so each lookup would
-- scan the whole table once per validated name.
CREATE INDEX IF NOT EXISTS idx_name_relation_name_id ON name_relation (col__name_id, col__type_id);
CREATE INDEX IF NOT EXISTS idx_name_relation_related_name_id
	ON name_relation (col__related_name_id, col__type_id);
CREATE INDEX IF NOT EXISTS idx_type_material_name_id ON type_material (col__name_id, col__status_id);

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

-- hive__prompt holds curator-forked prompts, one row per fork.
-- Shipped built-in prompts stay in pkg/prompts/ as embedded .md
-- files (versioned in place; never edited). Curator forks live
-- here so they travel with the archive and are visible across
-- collaborators sharing one project.
--
-- name is the curator-chosen slug (unique per purpose). purpose
-- routes the prompt to the right pipeline stage. fork_of is the
-- source prompt's stable ID (either a shipped name like "ner_v6"
-- or another curator prompt's prompt_id) for provenance.
--
-- Soft-delete via is_active — hard delete would break the
-- provenance trail of any extraction_run tagged with this
-- prompt's ID. Same principle as the extraction_run soft-delete.
CREATE TABLE IF NOT EXISTS hive__prompt (
	prompt_id    TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	purpose      TEXT NOT NULL CHECK (purpose IN ('ner', 'compose')),
	content      TEXT NOT NULL,
	fork_of      TEXT NOT NULL,
	is_active    INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
	created_at   TEXT NOT NULL,
	updated_at   TEXT NOT NULL,
	created_by   TEXT NOT NULL DEFAULT '',
	UNIQUE (name, purpose)
);

CREATE INDEX IF NOT EXISTS idx_hive__prompt_purpose
    ON hive__prompt (purpose, is_active);

-- hive__paper_import stores curator-approved per-name mappings
-- from a paper's compose-tree mentions to target taxa in the
-- current SFGA tree. One row per (reference_id, paper_name_key).
--
-- This is the per-paper equivalent of a CoL "sector" but with a
-- narrower shape: subtree assembly is not a concept here. Each
-- name mentioned in the paper resolves independently to a target
-- taxon UUID. See project_assembly_interface_plan memory for
-- the design rationale.
--
-- target_taxon_id is a UUID pointer, not a name-based reference:
-- if the target gets renamed / moved / downgraded to synonym, the
-- mapping still resolves. Curator repairs dangling mappings via
-- the assembly UI when they surface after downstream backbone
-- edits.
CREATE TABLE IF NOT EXISTS hive__paper_import (
	reference_id      TEXT NOT NULL,
	paper_name_key    TEXT NOT NULL,
	target_taxon_id   TEXT NOT NULL,
	match_mode        TEXT NOT NULL DEFAULT 'curator-picked'
	                    CHECK (match_mode IN (
	                        'auto-canonical',
	                        'auto-canonical-auth',
	                        'curator-picked')),
	match_confidence  REAL,
	last_synced_at    TEXT,
	created_at        TEXT NOT NULL,
	updated_at        TEXT NOT NULL,
	created_by        TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (reference_id, paper_name_key)
);

CREATE INDEX IF NOT EXISTS idx_hive__paper_import_ref
    ON hive__paper_import (reference_id);
CREATE INDEX IF NOT EXISTS idx_hive__paper_import_target
    ON hive__paper_import (target_taxon_id);

-- hive__row_source is the provenance join for imported SFGA rows.
-- (entity_table, entity_id) identifies an SFGA row (type_material,
-- distribution, vernacular_name, ...); the (reference_id,
-- extraction_run_id, assertion_id) triple records what paper /
-- run / assertion contributed it.
--
-- Using a join table rather than adding hive__source_* columns to
-- every SFGA extension table keeps the sfga schema pristine —
-- sf / gndb round-trips still see the original shape.
--
-- Multiple rows can point at the same entity when a row was
-- re-imported (matched on same key across runs). Not currently
-- enforced unique — the executor deletes prior rows for a
-- (reference_id, entity_table, entity_id) triple before inserting.
CREATE TABLE IF NOT EXISTS hive__row_source (
	entity_table      TEXT NOT NULL,
	entity_id         TEXT NOT NULL,
	reference_id      TEXT NOT NULL,
	extraction_run_id TEXT,
	assertion_id      TEXT,
	created_at        TEXT NOT NULL,
	created_by        TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (entity_table, entity_id, reference_id)
);

CREATE INDEX IF NOT EXISTS idx_hive__row_source_ref
    ON hive__row_source (reference_id);
CREATE INDEX IF NOT EXISTS idx_hive__row_source_run
    ON hive__row_source (extraction_run_id);

-- hive__assembler_flag stores curator-actionable conditions the
-- deterministic assembler emitted during the most recent reassemble.
-- Cleared and rewritten on every reassemble so the table reflects
-- the current Layer 2 state — no history is kept here (the
-- extraction runs and Layer 1 assertions carry the durable audit
-- trail).
--
-- kind values mirror assembler.FlagKind:
--   ambiguous_match, unresolved_anchor, contradictory_synonymy,
--   ambiguous_implicit_reference, missing_implicit_reference,
--   overlay_matched_by_publication, orphaned_overlay.
--
-- taxon_id is populated when the flag anchors to a materialized
-- Layer 2 taxon (e.g., an overlay-matched-by-publication flag or a
-- concept-alignment ambiguity on a specific taxon); reference_id +
-- mention_id are populated when the flag anchors to a paper's
-- assertion (e.g., unresolved-anchor flags before a taxon exists).
-- Both can be set for flags with both anchors.
--
-- candidates_json is a JSON array of candidate ids (taxon ids for
-- ambiguity flags, reference ids for overlay-match flags) so the
-- WUI can render "did you mean one of these?" affordances without
-- a second round-trip.
CREATE TABLE IF NOT EXISTS hive__assembler_flag (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    kind              TEXT NOT NULL,
    message           TEXT NOT NULL DEFAULT '',
    reference_id      TEXT,
    mention_id        TEXT,
    taxon_id          TEXT,
    candidates_json   TEXT NOT NULL DEFAULT '[]',
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_hive__assembler_flag_taxon
    ON hive__assembler_flag (taxon_id);
CREATE INDEX IF NOT EXISTS idx_hive__assembler_flag_ref
    ON hive__assembler_flag (reference_id);
CREATE INDEX IF NOT EXISTS idx_hive__assembler_flag_kind
    ON hive__assembler_flag (kind);

-- hive__layer3_overlay holds curator-authored hypotheses that
-- diverge from Layer 2 (the machine-assembled literature view).
-- One row per (taxon_id, overlay_type) — a curator may hold a
-- synonymy override AND a placement override on the same taxon,
-- but only one of each type.
--
-- overlay_type values:
--   synonymy_override        — curator disagrees with the L2 synonymy
--                              decision (e.g., L2 says "A is syn of
--                              B" but curator says "A is accepted").
--   placement_override       — curator disagrees with the L2 parent /
--                              higher-classification decision.
--   concept_override         — curator disagrees with the L2 concept
--                              relation (sensu qualifier, RCC5 type).
--   other                    — free-form; claim_json carries the shape.
--
-- claim_json is a small JSON object describing the curator's asserted
-- value. Shape depends on overlay_type; the [[layer3-claim-shapes]]
-- memory documents each. Kept as JSON so overlay_type can evolve
-- without a schema migration.
--
-- citation_reference_id is empty until a supporting publication
-- appears (either curator-attached or auto-detected by the
-- reassemble Layer 3 diff pass). A non-empty value means the
-- overlay has become published-backed.
--
-- retired_at is stamped when the curator retires the overlay
-- (either because a paper agrees with it and they promote the L2
-- default, or because they change their mind). Retired rows stay
-- for provenance; the reader filters them out unless asked.
CREATE TABLE IF NOT EXISTS hive__layer3_overlay (
    id                     TEXT PRIMARY KEY,
    taxon_id               TEXT NOT NULL,
    overlay_type           TEXT NOT NULL
                             CHECK (overlay_type IN (
                                 'synonymy_override',
                                 'placement_override',
                                 'concept_override',
                                 'other')),
    claim_json             TEXT NOT NULL DEFAULT '{}',
    notes                  TEXT NOT NULL DEFAULT '',
    citation_reference_id  TEXT,
    retired_at             TEXT,
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL,
    created_by             TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_hive__layer3_overlay_unique
    ON hive__layer3_overlay (taxon_id, overlay_type)
    WHERE retired_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_hive__layer3_overlay_taxon
    ON hive__layer3_overlay (taxon_id);
CREATE INDEX IF NOT EXISTS idx_hive__layer3_overlay_active
    ON hive__layer3_overlay (retired_at)
    WHERE retired_at IS NULL;

-- hive__reference_license records the verified open-access
-- license of a reference alongside where the verification came
-- from and who did it. One active row per reference; a re-verify
-- upserts. Kept separate from the reference row so hive can add
-- license bookkeeping without depending on future coldp.Reference
-- schema evolution.
--
-- license values (curator picks from a controlled vocabulary):
--   CC-BY, CC-BY-SA, CC-BY-NC, CC0, PD, PD-BHL, other-verified,
--   unknown. "unknown" is the safe default when the license
--   status genuinely can't be verified from the source — never
--   default to a permissive license.
--
-- source_url is where the license was verified (publisher page,
-- Crossref record, BHL item metadata). Empty is legal for
-- offline verifications with a paper trail.
CREATE TABLE IF NOT EXISTS hive__reference_license (
    reference_id      TEXT PRIMARY KEY,
    license           TEXT NOT NULL DEFAULT 'unknown'
                        CHECK (license IN (
                            'CC-BY', 'CC-BY-SA', 'CC-BY-NC',
                            'CC0', 'PD', 'PD-BHL',
                            'other-verified', 'unknown')),
    source_url        TEXT NOT NULL DEFAULT '',
    verified_at       TEXT,
    verified_by       TEXT NOT NULL DEFAULT '',
    updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_hive__reference_license_license
    ON hive__reference_license (license);

-- hive__user_setting holds each user's interface preferences (column
-- choices, sort order, theme, reading positions) keyed by actor, the
-- ORCID iD in hosted mode or the configured local identity. Values
-- are JSON. Kept in the archive so settings survive clearing browser
-- data and stay separate per user on a shared server.
CREATE TABLE IF NOT EXISTS hive__user_setting (
    actor       TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (actor, key)
);

-- hive__reference_taxon_summary records, per reference and rank
-- (kingdom … genus), the taxa the paper's mentioned names fall under,
-- from the verifier source with the most exact matches, or the next
-- source where that one leaves the rank mostly unassigned; rank 'name'
-- holds the names as written. name is the most mentioned taxon ('' when
-- the source places under a tenth of the matched mentions at the
-- rank), distinct_count how many taxa have at least a tenth of its
-- count, top_json the leading ones as [[name, count], …],
-- classified the matched mentions the source places at that rank and
-- matched the mentions with an exact match. method versions the
-- computation. Discovery data from the name-mention index; refreshed
-- whenever the paper's mentions are indexed.
CREATE TABLE IF NOT EXISTS hive__reference_taxon_summary (
    reference_id    TEXT NOT NULL,
    rank            TEXT NOT NULL,
    source_id       INTEGER NOT NULL,
    name            TEXT NOT NULL,
    mentions        INTEGER NOT NULL,
    distinct_count  INTEGER NOT NULL,
    top_json        TEXT NOT NULL,
    classified      INTEGER NOT NULL,
    matched         INTEGER NOT NULL,
    method          INTEGER NOT NULL,
    PRIMARY KEY (reference_id, rank)
);

-- hive__focal_taxonomic_group lists the taxa of the archive that the
-- dataset is about (its focal taxonomic groups), in order. It belongs
-- to the metadata row (col__metadata_id, like the ColDP agent tables)
-- and points at the archive's own taxa, so names and vernacular names
-- are read through the taxon. Exportable as EML taxonomicCoverage with
-- this archive as the taxonId provider. Name matching for references
-- prefers lineages inside these groups.
CREATE TABLE IF NOT EXISTS hive__focal_taxonomic_group (
    col__metadata_id INTEGER NOT NULL,
    taxon_id         TEXT NOT NULL,
    ordinal          INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (col__metadata_id, taxon_id)
);

-- hive__jats_stats records the size of each attached file's JATS
-- sidecar, the text sent to extraction models: words in its text,
-- characters in the file (markup included), and the source PDF's page
-- count. Refreshed whenever the JATS sidecar is written.
CREATE TABLE IF NOT EXISTS hive__jats_stats (
    sha512      TEXT PRIMARY KEY,
    pdf_pages   INTEGER,
    words       INTEGER NOT NULL,
    characters  INTEGER NOT NULL,
    updated_at  TEXT NOT NULL
);
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
	if err := ensureRawNameTables(ctx, db); err != nil {
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
	if err := migrateExtractionCorrectionForModify(ctx, db); err != nil {
		return err
	}
	if err := migrateTaxonSummaryMethod(ctx, db); err != nil {
		return err
	}
	if err := migrateReferenceAssertionForRunLink(ctx, db); err != nil {
		return err
	}
	return nil
}

// migrateReferenceAssertionForRunLink adds source_run_id and
// asserted_by columns to hive__reference_assertion on archives
// that predate the extraction-run schema. Both nullable — no
// backfill needed; legacy rows just show as unlinked (which is
// accurate — they weren't produced by an extraction_run).
// Idempotent: skips columns that already exist.
// migrateTaxonSummaryMethod drops a hive__reference_taxon_summary
// that predates the method column. The table holds derived data;
// BackfillTaxonSummaries refills it.
func migrateTaxonSummaryMethod(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, "hive__reference_taxon_summary")
	if err != nil || !exists {
		return err
	}
	has, err := columnExists(ctx, db, "hive__reference_taxon_summary", "method")
	if err != nil || has {
		return err
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE hive__reference_taxon_summary`); err != nil {
		return fmt.Errorf("core: drop old taxon summary: %w", err)
	}
	return nil
}

func migrateReferenceAssertionForRunLink(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, "hive__reference_assertion")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasSource, err := columnExists(ctx, db, "hive__reference_assertion", "source_run_id")
	if err != nil {
		return err
	}
	if !hasSource {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE hive__reference_assertion ADD COLUMN source_run_id TEXT`,
		); err != nil {
			return fmt.Errorf("core: add source_run_id: %w", err)
		}
	}
	hasAsserted, err := columnExists(ctx, db, "hive__reference_assertion", "asserted_by")
	if err != nil {
		return err
	}
	if !hasAsserted {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE hive__reference_assertion ADD COLUMN asserted_by TEXT`,
		); err != nil {
			return fmt.Errorf("core: add asserted_by: %w", err)
		}
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

// migrateExtractionCorrectionForModify brings a pre-1e
// hive__extraction_correction table up to the current shape by:
//   - Adding target_element_id (nullable) via ALTER TABLE.
//   - Widening the correction_kind CHECK constraint from
//     {bbox-add, text-tag, xref-fix, chip-feedback} to also allow
//     bbox-modify, via SQLite's rebuild-table pattern.
//
// Idempotent: skips when the column already exists AND the check
// already accepts bbox-modify. Fresh archives get the current
// shape directly from CREATE TABLE IF NOT EXISTS.
func migrateExtractionCorrectionForModify(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, "hive__extraction_correction")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	// Add the target_element_id column if missing. Nullable — old
	// rows are all additive kinds and don't need a target.
	hasTargetCol, err := columnExists(ctx, db, "hive__extraction_correction", "target_element_id")
	if err != nil {
		return err
	}
	if !hasTargetCol {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE hive__extraction_correction
			   ADD COLUMN target_element_id TEXT`,
		); err != nil {
			return fmt.Errorf("core: add target_element_id: %w", err)
		}
	}
	// Widen the CHECK constraint. SQLite doesn't support altering
	// CHECK constraints directly; the accepted pattern is
	// rename → create-new → copy → drop-old. Only run when the
	// current constraint doesn't yet accept the newest kind
	// (probed by attempting a dummy insert; rolled back either
	// way).
	// Probe with the newest correction kind — the rebuild target
	// includes every kind up through today, so any archive whose
	// CHECK doesn't accept the newest kind needs rebuilding.
	needsRebuild, err := checkAcceptsCorrectionKind(ctx, db, "page-numbering")
	if err != nil {
		return err
	}
	if needsRebuild {
		stmts := []string{
			`ALTER TABLE hive__extraction_correction
			   RENAME TO hive__extraction_correction_old`,
			`CREATE TABLE hive__extraction_correction (
				correction_id      TEXT PRIMARY KEY,
				reference_id       TEXT NOT NULL,
				sidecar_sha        TEXT NOT NULL,
				correction_kind    TEXT NOT NULL
				                     CHECK (correction_kind IN
				                       ('bbox-add', 'bbox-modify', 'bbox-remove',
				                        'text-tag', 'text-remove', 'text-restore',
				                        'xref-fix', 'chip-feedback',
				                        'ref-merge', 'ref-edit',
				                        'assertion-edit', 'page-numbering')),
				content_type       TEXT NOT NULL,
				bbox_page          INTEGER,
				bbox_json          TEXT,
				label              TEXT NOT NULL DEFAULT '',
				anchor_ptr         TEXT,
				target_element_id  TEXT,
				payload            TEXT NOT NULL DEFAULT '',
				status             TEXT NOT NULL DEFAULT 'active'
				                     CHECK (status IN ('active', 'reverted')),
				reverted_reason    TEXT,
				created_at         TEXT NOT NULL,
				updated_at         TEXT NOT NULL,
				created_by         TEXT NOT NULL DEFAULT ''
			)`,
			`INSERT INTO hive__extraction_correction
				(correction_id, reference_id, sidecar_sha, correction_kind,
				 content_type, bbox_page, bbox_json, label, anchor_ptr,
				 target_element_id, payload, status, reverted_reason,
				 created_at, updated_at, created_by)
			 SELECT
				correction_id, reference_id, sidecar_sha, correction_kind,
				content_type, bbox_page, bbox_json, label, anchor_ptr,
				target_element_id, payload, status, reverted_reason,
				created_at, updated_at, created_by
			 FROM hive__extraction_correction_old`,
			`DROP TABLE hive__extraction_correction_old`,
		}
		for _, s := range stmts {
			if _, err := db.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("core: rebuild hive__extraction_correction: %w", err)
			}
		}
	}
	return nil
}

// checkAcceptsCorrectionKind probes whether the current CHECK
// constraint on hive__extraction_correction accepts the given
// kind. Returns true when the table needs to be rebuilt with the
// wider constraint.
func checkAcceptsCorrectionKind(ctx context.Context, db *sql.DB, kind string) (needsRebuild bool, err error) {
	// SAVEPOINT + rollback keeps the probe side-effect-free even
	// if the insert succeeds. Named savepoint so we can roll
	// back precisely regardless of an outer transaction.
	if _, err = db.ExecContext(ctx, "SAVEPOINT probe_correction_kind"); err != nil {
		return false, fmt.Errorf("probe savepoint: %w", err)
	}
	defer func() {
		_, rbErr := db.ExecContext(ctx, "ROLLBACK TO SAVEPOINT probe_correction_kind")
		if rbErr == nil {
			_, _ = db.ExecContext(ctx, "RELEASE SAVEPOINT probe_correction_kind")
		}
	}()
	_, insErr := db.ExecContext(ctx,
		`INSERT INTO hive__extraction_correction
		 (correction_id, reference_id, sidecar_sha, correction_kind,
		  content_type, created_at, updated_at)
		 VALUES ('probe', 'probe-ref', 'probe-sha', ?,
		         'figure', '', '')`,
		kind,
	)
	if insErr == nil {
		return false, nil
	}
	if strings.Contains(insErr.Error(), "CHECK constraint") ||
		strings.Contains(insErr.Error(), "constraint failed") {
		return true, nil
	}
	return false, fmt.Errorf("probe insert: %w", insErr)
}

// columnExists returns true when the given table has a column of
// the given name. Uses PRAGMA table_info which is cheap and does
// not require any migration lock.
func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid                        int
			name, ctype                string
			notnull, pk                int
			dfltValue                  sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return false, err
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
