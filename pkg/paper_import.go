package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PaperImportMapping is one row of hive__paper_import — a
// curator-approved binding of a paper mention to a target taxon
// UUID. See the schema comment in hive_tables.go for column
// semantics.
type PaperImportMapping struct {
	ReferenceID     string
	PaperNameKey    string
	TargetTaxonID   string
	MatchMode       string // 'auto-canonical' | 'auto-canonical-auth' | 'curator-picked'
	MatchConfidence float64
	LastSyncedAt    time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CreatedBy       string
}

// UpsertPaperImportMapping inserts a new mapping or updates the
// existing row for the same (reference_id, paper_name_key). Used
// by the drag-drop UI where each drop is a re-assertion of the
// mapping regardless of whether it existed before. Stamps
// updated_at every call; created_at only on first insert.
func (t *Tx) UpsertPaperImportMapping(m PaperImportMapping) error {
	if m.ReferenceID == "" {
		return errors.New("core: UpsertPaperImportMapping: reference_id is required")
	}
	if m.PaperNameKey == "" {
		return errors.New("core: UpsertPaperImportMapping: paper_name_key is required")
	}
	if m.TargetTaxonID == "" {
		return errors.New("core: UpsertPaperImportMapping: target_taxon_id is required")
	}
	if m.MatchMode == "" {
		m.MatchMode = "curator-picked"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if m.CreatedBy == "" {
		m.CreatedBy = ActorFromContext(t.ctx)
	}
	// ON CONFLICT preserves original created_at + created_by so we
	// don't reset provenance when a curator adjusts a mapping.
	const q = `INSERT INTO hive__paper_import
		(reference_id, paper_name_key, target_taxon_id,
		 match_mode, match_confidence, last_synced_at,
		 created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (reference_id, paper_name_key) DO UPDATE SET
		 target_taxon_id  = excluded.target_taxon_id,
		 match_mode       = excluded.match_mode,
		 match_confidence = excluded.match_confidence,
		 updated_at       = excluded.updated_at`
	var lastSynced any
	if !m.LastSyncedAt.IsZero() {
		lastSynced = m.LastSyncedAt.Format(time.RFC3339Nano)
	}
	var confidence any
	if m.MatchConfidence > 0 {
		confidence = m.MatchConfidence
	}
	_, err := t.tx.ExecContext(t.ctx, q,
		m.ReferenceID, m.PaperNameKey, m.TargetTaxonID,
		m.MatchMode, confidence, lastSynced,
		now, now, m.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("core: upsert hive__paper_import: %w", err)
	}
	return nil
}

// ListPaperImportMappings returns every mapping for the given
// paper, oldest first (curator sees mappings in the order they
// were created).
func (a *Archive) ListPaperImportMappings(ctx context.Context, referenceID string) ([]PaperImportMapping, error) {
	if referenceID == "" {
		return nil, errors.New("core: ListPaperImportMappings: reference_id is required")
	}
	const q = `SELECT
		reference_id, paper_name_key, target_taxon_id,
		match_mode, COALESCE(match_confidence, 0),
		COALESCE(last_synced_at, ''),
		created_at, updated_at, created_by
	FROM hive__paper_import
	WHERE reference_id = ?
	ORDER BY created_at ASC, paper_name_key ASC`
	rows, err := a.db.QueryContext(ctx, q, referenceID)
	if err != nil {
		return nil, fmt.Errorf("core: list paper import mappings: %w", err)
	}
	defer rows.Close()
	var out []PaperImportMapping
	for rows.Next() {
		m, err := scanPaperImportMapping(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// DeletePaperImportMapping removes one mapping by (reference,
// paper_name_key). Returns ErrNotFound when no matching row.
func (t *Tx) DeletePaperImportMapping(referenceID, paperNameKey string) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__paper_import
		 WHERE reference_id = ? AND paper_name_key = ?`,
		referenceID, paperNameKey,
	)
	if err != nil {
		return fmt.Errorf("core: delete paper import mapping: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: paper import mapping (%s, %s): %w",
			referenceID, paperNameKey, ErrNotFound)
	}
	return nil
}

// MarkPaperImportSynced stamps last_synced_at on every mapping
// for the paper to the given time. Called at the end of a
// successful import execution so the assembly UI can show
// "synced 5 minutes ago" vs. "never synced" per mapping.
func (t *Tx) MarkPaperImportSynced(referenceID string, syncedAt time.Time) error {
	if syncedAt.IsZero() {
		syncedAt = time.Now().UTC()
	}
	_, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__paper_import SET last_synced_at = ?
		 WHERE reference_id = ?`,
		syncedAt.Format(time.RFC3339Nano), referenceID,
	)
	if err != nil {
		return fmt.Errorf("core: mark paper import synced: %w", err)
	}
	return nil
}

func scanPaperImportMapping(r rowScanner) (*PaperImportMapping, error) {
	var (
		m                             PaperImportMapping
		confidence                    sql.NullFloat64
		lastSynced, createdAt, updated string
	)
	if err := r.Scan(
		&m.ReferenceID, &m.PaperNameKey, &m.TargetTaxonID,
		&m.MatchMode, &confidence, &lastSynced,
		&createdAt, &updated, &m.CreatedBy,
	); err != nil {
		return nil, err
	}
	if confidence.Valid {
		m.MatchConfidence = confidence.Float64
	}
	if lastSynced != "" {
		if t, err := time.Parse(time.RFC3339Nano, lastSynced); err == nil {
			m.LastSyncedAt = t
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		m.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		m.UpdatedAt = t
	}
	return &m, nil
}

// RowSource is one hive__row_source row — provenance of an
// imported SFGA extension row back to the paper / run / assertion
// that produced it. See the schema comment in hive_tables.go.
type RowSource struct {
	EntityTable     string
	EntityID        string
	ReferenceID     string
	ExtractionRunID string
	AssertionID     string
	CreatedAt       time.Time
	CreatedBy       string
}

// InsertRowSource records provenance for a newly-materialized
// SFGA row. The (entity_table, entity_id, reference_id) triple
// is the PK; INSERT OR REPLACE keeps re-import idempotent.
func (t *Tx) InsertRowSource(s RowSource) error {
	if s.EntityTable == "" || s.EntityID == "" || s.ReferenceID == "" {
		return errors.New("core: InsertRowSource: entity_table + entity_id + reference_id required")
	}
	if s.CreatedBy == "" {
		s.CreatedBy = ActorFromContext(t.ctx)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var runID, assertionID any
	if s.ExtractionRunID != "" {
		runID = s.ExtractionRunID
	}
	if s.AssertionID != "" {
		assertionID = s.AssertionID
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT OR REPLACE INTO hive__row_source
		 (entity_table, entity_id, reference_id,
		  extraction_run_id, assertion_id, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.EntityTable, s.EntityID, s.ReferenceID,
		runID, assertionID, now, s.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("core: insert hive__row_source: %w", err)
	}
	return nil
}

// DeleteRowSourcesForReference wipes every provenance row this
// reference contributed AND returns the (entity_table, entity_id)
// pairs so the caller can also delete the SFGA rows themselves
// (the delete-and-reinsert re-sync path). Runs inside the caller's
// transaction so the sweep is atomic with the re-insert.
//
// Not exposed via HTTP directly — only the import executor and
// (future) bulk re-sync flow call this.
func (t *Tx) DeleteRowSourcesForReference(referenceID string) ([][2]string, error) {
	if referenceID == "" {
		return nil, errors.New("core: DeleteRowSourcesForReference: reference_id required")
	}
	rows, err := t.tx.QueryContext(t.ctx,
		`SELECT entity_table, entity_id FROM hive__row_source
		 WHERE reference_id = ?`,
		referenceID,
	)
	if err != nil {
		return nil, fmt.Errorf("core: list row sources for %s: %w", referenceID, err)
	}
	var out [][2]string
	for rows.Next() {
		var table, id string
		if err := rows.Scan(&table, &id); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, [2]string{table, id})
	}
	rows.Close()
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__row_source WHERE reference_id = ?`,
		referenceID,
	); err != nil {
		return nil, fmt.Errorf("core: delete row sources for %s: %w", referenceID, err)
	}
	return out, nil
}
