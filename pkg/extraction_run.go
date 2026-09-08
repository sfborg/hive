package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ExtractionRun is one row of hive__extraction_run — the persisted
// record of an LLM invocation (NER, compose) or a future hive-native
// NER model run or curator annotation. See the schema comment on
// hive__extraction_run in hive_tables.go for column semantics.
//
// Runs are persisted by default so multi-model comparison (paper
// through Opus + Sonnet + Haiku) and cost accountability work
// without curators losing paid-for results. Soft-delete via
// DeletedAt keeps the row for cost dashboards even after the
// curator "removes" it.
type ExtractionRun struct {
	// RunID is a UUID. Populated by CreateExtractionRun when the
	// caller-supplied ID is empty.
	RunID string
	// ReferenceID is which paper this run processed. Empty for
	// ad-hoc invocations not scoped to a paper (a test call, a
	// direct API poke). Nullable in the schema.
	ReferenceID string
	// Purpose is "ner" | "compose" | "translate" — what pipeline
	// stage the run represents.
	Purpose string
	// SourceKind is "llm" | "hive_ner" | "curator" — same table
	// houses all three so comparison + scoring work uniformly.
	SourceKind string
	// SourceID identifies the specific source within the kind —
	// provider name for llm ("anthropic-main"), model version for
	// hive_ner, ORCID for curator.
	SourceID string
	// EnvelopeJSON is the full llm.Envelope as JSON — the
	// authoritative provenance record. Empty for source_kind =
	// "curator".
	EnvelopeJSON string
	// RawOutputJSON is the parsed NER / compose output. Same
	// content the envelope's Output field carries; denormalized
	// here for query-time speed on the comparison-UI hot path.
	RawOutputJSON string
	// ParentRunID links a compose run to the NER run whose
	// assertions it consumed. Empty for runs without a parent
	// (NER passes, curator entries, ad-hoc calls).
	ParentRunID string
	// CostUSD is the actual invocation cost in USD. Populated from
	// llm.EstimateCost using the envelope's token counts (accurate
	// because they're the provider-reported values, not estimates).
	// Zero for source_kind = "hive_ner" or "curator".
	CostUSD float64
	// IsGoldStandard flags a run as ground-truth for scoring.
	// Curator toggles this on runs they've annotated rigorously;
	// downstream scoring compares LLM runs to gold runs on the
	// same reference to compute precision/recall/F1.
	IsGoldStandard bool
	// Notes is free-text curator annotation.
	Notes string
	// CreatedAt is the wall-clock time of insertion. Not the same
	// as envelope.InvokedAt (which is when the LLM call started) —
	// the delta shows how long persistence took.
	CreatedAt time.Time
	// DeletedAt is non-zero when the run has been soft-deleted.
	// Row stays visible to cost dashboards; hidden from default
	// comparison-UI lists. Restore via RestoreExtractionRun.
	DeletedAt time.Time
}

// CreateExtractionRun inserts a new hive__extraction_run row and
// returns the assigned RunID. If run.RunID is empty a fresh UUID
// is minted; otherwise the caller-supplied ID is used (useful for
// deterministic tests). CreatedAt is stamped from wall-clock time
// unless the caller has already set it.
//
// Purpose and SourceKind are validated by SQLite CHECK constraints;
// a bad value returns a wrapped error from the underlying INSERT.
func (t *Tx) CreateExtractionRun(run ExtractionRun) (string, error) {
	if run.Purpose == "" {
		return "", errors.New("core: CreateExtractionRun: purpose is required")
	}
	if run.SourceKind == "" {
		return "", errors.New("core: CreateExtractionRun: source_kind is required")
	}
	if run.SourceID == "" {
		return "", errors.New("core: CreateExtractionRun: source_id is required")
	}
	if run.RunID == "" {
		run.RunID = uuid.NewString()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	gold := 0
	if run.IsGoldStandard {
		gold = 1
	}
	const q = `INSERT INTO hive__extraction_run
		(run_id, reference_id, purpose, source_kind, source_id,
		 envelope_json, raw_output_json, parent_run_id, cost_usd,
		 is_gold_standard, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := t.tx.ExecContext(t.ctx, q,
		run.RunID,
		nullableText(run.ReferenceID),
		run.Purpose,
		run.SourceKind,
		run.SourceID,
		run.EnvelopeJSON,
		run.RawOutputJSON,
		nullableText(run.ParentRunID),
		run.CostUSD,
		gold,
		run.Notes,
		run.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", fmt.Errorf("core: insert hive__extraction_run: %w", err)
	}
	return run.RunID, nil
}

// GetExtractionRun returns the run with the given ID. Returns
// [ErrNotFound] when no row exists.
func (a *Archive) GetExtractionRun(ctx context.Context, runID string) (*ExtractionRun, error) {
	const q = `SELECT run_id, COALESCE(reference_id, ''), purpose,
		source_kind, source_id, envelope_json, raw_output_json,
		COALESCE(parent_run_id, ''), cost_usd, is_gold_standard,
		notes, created_at, COALESCE(deleted_at, '')
		FROM hive__extraction_run
		WHERE run_id = ?`
	row := a.db.QueryRowContext(ctx, q, runID)
	run, err := scanExtractionRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("core: extraction run %q: %w", runID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// ListExtractionRunsByReference returns every run associated with
// the given reference, most-recent first. When includeDeleted is
// false, soft-deleted rows are omitted (the default view). When
// true, all rows come back and callers can filter — used by the
// cost dashboard where hiding deleted rows would hide spend.
func (a *Archive) ListExtractionRunsByReference(
	ctx context.Context,
	referenceID string,
	includeDeleted bool,
) ([]ExtractionRun, error) {
	q := `SELECT run_id, COALESCE(reference_id, ''), purpose,
		source_kind, source_id, envelope_json, raw_output_json,
		COALESCE(parent_run_id, ''), cost_usd, is_gold_standard,
		notes, created_at, COALESCE(deleted_at, '')
		FROM hive__extraction_run
		WHERE reference_id = ?`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := a.db.QueryContext(ctx, q, referenceID)
	if err != nil {
		return nil, fmt.Errorf("core: list extraction runs for %s: %w", referenceID, err)
	}
	defer rows.Close()
	var out []ExtractionRun
	for rows.Next() {
		run, err := scanExtractionRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// SoftDeleteExtractionRun stamps deleted_at with the current time.
// Row remains in the database — cost dashboards and audit views
// still see it. Idempotent: re-deleting an already-deleted row
// updates the timestamp (harmless).
func (t *Tx) SoftDeleteExtractionRun(runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__extraction_run SET deleted_at = ? WHERE run_id = ?`,
		now, runID,
	)
	if err != nil {
		return fmt.Errorf("core: soft-delete extraction run %s: %w", runID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: extraction run %q: %w", runID, ErrNotFound)
	}
	return nil
}

// RestoreExtractionRun clears deleted_at, un-hiding a
// soft-deleted run from default views. No-op on rows that aren't
// currently deleted.
func (t *Tx) RestoreExtractionRun(runID string) error {
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__extraction_run SET deleted_at = NULL WHERE run_id = ?`,
		runID,
	)
	if err != nil {
		return fmt.Errorf("core: restore extraction run %s: %w", runID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: extraction run %q: %w", runID, ErrNotFound)
	}
	return nil
}

// SetExtractionRunGoldStandard toggles the is_gold_standard flag
// used by the scoring pipeline. Curators mark a run as gold after
// rigorously annotating a reference; batch NER runs then get
// scored against the gold assertion set on the same reference.
func (t *Tx) SetExtractionRunGoldStandard(runID string, gold bool) error {
	flag := 0
	if gold {
		flag = 1
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__extraction_run SET is_gold_standard = ? WHERE run_id = ?`,
		flag, runID,
	)
	if err != nil {
		return fmt.Errorf("core: set gold on run %s: %w", runID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: extraction run %q: %w", runID, ErrNotFound)
	}
	return nil
}

// scanExtractionRun reads a row into an ExtractionRun. Shared
// between GetExtractionRun (sql.Row) and the list helper
// (sql.Rows) via the rowScanner interface.
func scanExtractionRun(r rowScanner) (*ExtractionRun, error) {
	var (
		run                       ExtractionRun
		gold                      int
		createdAt, deletedAtEmpty string
	)
	if err := r.Scan(
		&run.RunID,
		&run.ReferenceID,
		&run.Purpose,
		&run.SourceKind,
		&run.SourceID,
		&run.EnvelopeJSON,
		&run.RawOutputJSON,
		&run.ParentRunID,
		&run.CostUSD,
		&gold,
		&run.Notes,
		&createdAt,
		&deletedAtEmpty,
	); err != nil {
		return nil, err
	}
	run.IsGoldStandard = gold == 1
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		run.CreatedAt = t
	}
	if deletedAtEmpty != "" {
		if t, err := time.Parse(time.RFC3339Nano, deletedAtEmpty); err == nil {
			run.DeletedAt = t
		}
	}
	return &run, nil
}

// rowScanner is the minimal subset of sql.Row / sql.Rows that
// [scanExtractionRun] uses. Local to this file to avoid touching
// the wider package's scan-helper conventions.
type rowScanner interface {
	Scan(dest ...any) error
}

// nullableText returns a driver.Value that maps empty strings to
// SQL NULL. Used for reference_id + parent_run_id where the schema
// allows NULL and empty-string-vs-null would matter for indexed
// COUNT queries and CHECK constraints on parent runs.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
