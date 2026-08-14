package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// The distribution table has no col__id column in sfga — a row is
// identified only by its (taxon_id, area_id, gazetteer_id, status_id)
// tuple in practice, with nothing at the schema level preventing
// duplicates. Hive uses SQLite's implicit rowid as the opaque handle
// for read/update/delete so the API has a stable per-row identifier
// without extending the schema. Same rationale as vernacular.

// DistributionHit is the projection returned by ListDistributions
// and the read side of the distribution CRUD. RowID carries
// SQLite's implicit primary key so update/delete calls can address
// a single row unambiguously even when multiple rows share
// (taxon, area).
type DistributionHit struct {
	RowID       int64
	TaxonID     string
	SourceID    string
	Area        string
	AreaID      string
	Gazetteer   coldp.GazetteerEnt
	Status      coldp.DistrStatus
	ReferenceID string
	Remarks     string
	Modified    string
	ModifiedBy  string
	// IssueCount is the number of open (not-yet-acknowledged)
	// validation issues currently filed against this distribution
	// row. Populated per-hit by ListDistributions in a single
	// batched grouping query — cheap even with a large per-taxon
	// distribution set. Zero when the row is clean; front-ends
	// render a warn icon on the row when > 0.
	IssueCount int
}

// ListDistributions returns every distribution row attached to
// the given taxon, ordered by gazetteer then area for a stable
// display where curators expect to see the geographic groups
// (ISO / TDWG / TEOW / TEXT) laid out together.
func (a *Archive) ListDistributions(ctx context.Context, taxonID string) ([]DistributionHit, error) {
	const q = `SELECT
		rowid,
		col__taxon_id,
		COALESCE(col__source_id, ''),
		COALESCE(col__area, ''),
		COALESCE(col__area_id, ''),
		COALESCE(col__gazetteer_id, ''),
		COALESCE(col__status_id, ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__remarks, ''),
		COALESCE(col__modified, ''),
		COALESCE(col__modified_by, '')
	FROM distribution
	WHERE col__taxon_id = ?
	ORDER BY col__gazetteer_id, col__area, col__area_id`
	rows, err := a.db.QueryContext(ctx, q, taxonID)
	if err != nil {
		return nil, fmt.Errorf("core: list distributions of %q: %w", taxonID, err)
	}
	defer rows.Close()
	var hits []DistributionHit
	for rows.Next() {
		var (
			h                   DistributionHit
			gazetteerID, status string
		)
		if err := rows.Scan(
			&h.RowID, &h.TaxonID, &h.SourceID,
			&h.Area, &h.AreaID, &gazetteerID, &status,
			&h.ReferenceID, &h.Remarks,
			&h.Modified, &h.ModifiedBy,
		); err != nil {
			return nil, fmt.Errorf("core: scan distribution of %q: %w", taxonID, err)
		}
		h.Gazetteer = coldp.NewGazetteerEnt(gazetteerID)
		h.Status = coldp.NewDistrStatus(status)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fold in open-issue counts per rowid in one batched pass —
	// mirrors the vernacular pattern. table_name = 'distribution',
	// record_id is the stringified rowid.
	if len(hits) > 0 {
		ids := make([]any, len(hits))
		for i, h := range hits {
			ids[i] = fmt.Sprintf("%d", h.RowID)
		}
		q2 := `SELECT record_id, COUNT(*)
			FROM __gsvalidator_results
			WHERE table_name = 'distribution'
			  AND (acknowledged_at IS NULL OR acknowledged_at = '')
			  AND record_id IN (` + placeholders(len(ids)) + `)
			GROUP BY record_id`
		irows, err := a.db.QueryContext(ctx, q2, ids...)
		if err != nil {
			return nil, fmt.Errorf("core: distribution issue counts of %q: %w", taxonID, err)
		}
		counts := make(map[string]int, len(hits))
		for irows.Next() {
			var id string
			var n int
			if err := irows.Scan(&id, &n); err != nil {
				irows.Close()
				return nil, fmt.Errorf("core: scan distribution issue count: %w", err)
			}
			counts[id] = n
		}
		irows.Close()
		for i := range hits {
			hits[i].IssueCount = counts[fmt.Sprintf("%d", hits[i].RowID)]
		}
	}
	return hits, nil
}

// GetDistribution returns the row with the given rowid, or
// ErrNotFound when nothing matches. Frontends usually have the
// row from a list call already; this method exists for direct-
// address paths (deep links, PATCH round-trips that want the
// pre-image, tests).
func (a *Archive) GetDistribution(ctx context.Context, rowid int64) (*DistributionHit, error) {
	const q = `SELECT
		rowid,
		col__taxon_id,
		COALESCE(col__source_id, ''),
		COALESCE(col__area, ''),
		COALESCE(col__area_id, ''),
		COALESCE(col__gazetteer_id, ''),
		COALESCE(col__status_id, ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__remarks, ''),
		COALESCE(col__modified, ''),
		COALESCE(col__modified_by, '')
	FROM distribution WHERE rowid = ? LIMIT 1`
	var (
		h                   DistributionHit
		gazetteerID, status string
	)
	err := a.db.QueryRowContext(ctx, q, rowid).Scan(
		&h.RowID, &h.TaxonID, &h.SourceID,
		&h.Area, &h.AreaID, &gazetteerID, &status,
		&h.ReferenceID, &h.Remarks,
		&h.Modified, &h.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: distribution %d: %w", rowid, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get distribution %d: %w", rowid, err)
	}
	h.Gazetteer = coldp.NewGazetteerEnt(gazetteerID)
	h.Status = coldp.NewDistrStatus(status)
	return &h, nil
}

// AddDistribution inserts a new distribution row and returns the
// new rowid as the opaque handle for later PATCH/DELETE.
//
//   - d.TaxonID is required; empty → ErrValidation.
//   - Every other field is optional. sfga's schema has NOT-NULL
//     defaults of '' on col__area / col__area_id, and the FK
//     columns (source_id, gazetteer_id, status_id, reference_id)
//     accept '' → NULL so unset values round-trip cleanly.
//   - col__modified / col__modified_by stamped from tx context.
func (t *Tx) AddDistribution(d coldp.Distribution) (int64, error) {
	if d.TaxonID == "" {
		return 0, fmt.Errorf("core: add distribution: %w: taxon_id required", ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const insert = `INSERT INTO distribution (
		col__taxon_id, col__source_id,
		col__area, col__area_id,
		col__gazetteer_id, col__status_id, col__reference_id,
		col__remarks, col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := t.tx.ExecContext(t.ctx, insert,
		d.TaxonID, nullIfEmpty(d.SourceID),
		d.Area, d.AreaID,
		nullIfEmpty(d.Gazetteer.ID()), nullIfEmpty(d.Status.ID()), nullIfEmpty(d.ReferenceID),
		d.Remarks, now, t.actor,
	)
	if err != nil {
		return 0, fmt.Errorf("core: insert distribution (taxon=%s, area=%q): %w",
			d.TaxonID, d.Area, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("core: distribution rowid: %w", err)
	}
	return id, nil
}

// UpdateDistribution rewrites every editable column of the row at
// rowid. TaxonID is NOT editable — reparenting a distribution is
// semantically a delete+add on a different taxon. Zero-row affected
// (unknown rowid) surfaces as ErrNotFound so the handler maps to 404.
func (t *Tx) UpdateDistribution(rowid int64, d coldp.Distribution) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const upd = `UPDATE distribution SET
		col__source_id = ?,
		col__area = ?, col__area_id = ?,
		col__gazetteer_id = ?, col__status_id = ?, col__reference_id = ?,
		col__remarks = ?, col__modified = ?, col__modified_by = ?
	WHERE rowid = ?`
	res, err := t.tx.ExecContext(t.ctx, upd,
		nullIfEmpty(d.SourceID),
		d.Area, d.AreaID,
		nullIfEmpty(d.Gazetteer.ID()), nullIfEmpty(d.Status.ID()), nullIfEmpty(d.ReferenceID),
		d.Remarks, now, t.actor,
		rowid,
	)
	if err != nil {
		return fmt.Errorf("core: update distribution %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update distribution %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update distribution %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// DeleteDistribution removes the row at rowid. Unknown rowid →
// ErrNotFound so the handler surfaces a 404.
func (t *Tx) DeleteDistribution(rowid int64) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM distribution WHERE rowid = ?`, rowid,
	)
	if err != nil {
		return fmt.Errorf("core: delete distribution %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete distribution %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete distribution %d: %w", rowid, ErrNotFound)
	}
	return nil
}
