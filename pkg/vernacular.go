package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// The vernacular table has no col__id column in sfga — a vernacular
// row is identified only by its (taxon_id, name, language, country,
// area) tuple, and even that isn't unique in principle (nothing
// stops two rows for the same taxon+language+name if a curator
// hasn't cleaned up). Hive uses SQLite's implicit rowid as the
// opaque handle for read/update/delete so the API has a stable
// per-row identifier without extending the schema. Rowids are
// stable across normal operations (INSERT keeps existing rowids
// intact); VACUUM can reassign them but hive never triggers a
// VACUUM. Callers treat the id as an opaque string on the wire.

// VernacularHit is the projection returned by ListVernaculars and
// the read side of the vernacular CRUD.  RowID carries SQLite's
// implicit primary key so update/delete calls can address a single
// row unambiguously even when multiple rows share (taxon, name).
type VernacularHit struct {
	RowID           int64
	TaxonID         string
	SourceID        string
	Name            string
	Transliteration string
	Language        string
	Preferred       sql.NullBool
	Country         string
	Area            string
	Sex             coldp.Sex
	ReferenceID     string
	Remarks         string
	Modified        string
	ModifiedBy      string
	// IssueCount is the number of open (not-yet-acknowledged)
	// validation issues currently filed against this vernacular row.
	// Populated per-hit by ListVernaculars in a single batched
	// grouping query — cheap even with a large per-taxon vernacular
	// set. Zero when the row is clean; front-ends render a warn icon
	// on the row when > 0.
	IssueCount int
}

// ListVernaculars returns every vernacular row attached to the
// given taxon, ordered preferred-first, then by language then name
// so the WUI list renders the "one preferred per language" pattern
// curators expect near the top.
func (a *Archive) ListVernaculars(ctx context.Context, taxonID string) ([]VernacularHit, error) {
	const q = `SELECT
		rowid,
		col__taxon_id,
		COALESCE(col__source_id, ''),
		col__name,
		COALESCE(col__transliteration, ''),
		COALESCE(col__language, ''),
		col__preferred,
		COALESCE(col__country, ''),
		COALESCE(col__area, ''),
		COALESCE(col__sex_id, ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__remarks, ''),
		COALESCE(col__modified, ''),
		COALESCE(col__modified_by, '')
	FROM vernacular
	WHERE col__taxon_id = ?
	ORDER BY col__preferred IS NULL, col__preferred DESC, col__language, col__name`
	rows, err := a.db.QueryContext(ctx, q, taxonID)
	if err != nil {
		return nil, fmt.Errorf("core: list vernaculars of %q: %w", taxonID, err)
	}
	defer rows.Close()
	var hits []VernacularHit
	for rows.Next() {
		var (
			h      VernacularHit
			sexID  string
		)
		if err := rows.Scan(
			&h.RowID, &h.TaxonID, &h.SourceID, &h.Name,
			&h.Transliteration, &h.Language, &h.Preferred,
			&h.Country, &h.Area, &sexID,
			&h.ReferenceID, &h.Remarks,
			&h.Modified, &h.ModifiedBy,
		); err != nil {
			return nil, fmt.Errorf("core: scan vernacular of %q: %w", taxonID, err)
		}
		h.Sex = coldp.NewSex(sexID)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fold in open issue counts in one batched pass — cheap indexed
	// grouping. Matches the pattern used by NomenclaturalHistory's
	// batchNomenIssueCounts. Table filter: table_name='vernacular';
	// record_id is the rowid stringified (sfga vernacular rows have
	// no col__id).
	if len(hits) > 0 {
		ids := make([]any, len(hits))
		for i, h := range hits {
			ids[i] = fmt.Sprintf("%d", h.RowID)
		}
		q2 := `SELECT record_id, COUNT(*)
			FROM __gsvalidator_results
			WHERE table_name = 'vernacular'
			  AND (acknowledged_at IS NULL OR acknowledged_at = '')
			  AND record_id IN (` + placeholders(len(ids)) + `)
			GROUP BY record_id`
		irows, err := a.db.QueryContext(ctx, q2, ids...)
		if err != nil {
			return nil, fmt.Errorf("core: vernacular issue counts of %q: %w", taxonID, err)
		}
		counts := make(map[string]int, len(hits))
		for irows.Next() {
			var id string
			var n int
			if err := irows.Scan(&id, &n); err != nil {
				irows.Close()
				return nil, fmt.Errorf("core: scan vernacular issue count: %w", err)
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

// GetVernacular returns the row with the given rowid, or ErrNotFound
// when nothing matches. Frontends generally have the row from a list
// call already; this method exists for direct-address paths (deep
// links, PATCH round-trips that want the pre-image, tests).
func (a *Archive) GetVernacular(ctx context.Context, rowid int64) (*VernacularHit, error) {
	const q = `SELECT
		rowid,
		col__taxon_id,
		COALESCE(col__source_id, ''),
		col__name,
		COALESCE(col__transliteration, ''),
		COALESCE(col__language, ''),
		col__preferred,
		COALESCE(col__country, ''),
		COALESCE(col__area, ''),
		COALESCE(col__sex_id, ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__remarks, ''),
		COALESCE(col__modified, ''),
		COALESCE(col__modified_by, '')
	FROM vernacular WHERE rowid = ? LIMIT 1`
	var (
		h     VernacularHit
		sexID string
	)
	err := a.db.QueryRowContext(ctx, q, rowid).Scan(
		&h.RowID, &h.TaxonID, &h.SourceID, &h.Name,
		&h.Transliteration, &h.Language, &h.Preferred,
		&h.Country, &h.Area, &sexID,
		&h.ReferenceID, &h.Remarks,
		&h.Modified, &h.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: vernacular %d: %w", rowid, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get vernacular %d: %w", rowid, err)
	}
	h.Sex = coldp.NewSex(sexID)
	return &h, nil
}

// AddVernacular inserts a new vernacular row and returns the new
// rowid as the opaque handle for later PATCH/DELETE.
//
//   - v.TaxonID and v.Name are required; empty either → ErrValidation.
//   - Nullable FKs ("" → NULL): source_id, sex_id, reference_id.
//     Empty non-FK strings pass through as empty strings.
//   - Preferred is stored verbatim from sql.NullBool — invalid
//     stays NULL, valid stores 0/1.
//   - col__modified / col__modified_by stamped from tx context.
func (t *Tx) AddVernacular(v coldp.Vernacular) (int64, error) {
	if v.TaxonID == "" {
		return 0, fmt.Errorf("core: add vernacular: %w: taxon_id required", ErrValidation)
	}
	if v.Name == "" {
		return 0, fmt.Errorf("core: add vernacular: %w: name required", ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const insert = `INSERT INTO vernacular (
		col__taxon_id, col__source_id, col__name,
		col__transliteration, col__language, col__preferred,
		col__country, col__area,
		col__sex_id, col__reference_id,
		col__remarks, col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := t.tx.ExecContext(t.ctx, insert,
		v.TaxonID, nullIfEmpty(v.SourceID), v.Name,
		v.Transliteration, v.Language, nullBoolArg(v.Preferred),
		v.Country, v.Area,
		nullIfEmpty(v.Sex.ID()), nullIfEmpty(v.ReferenceID),
		v.Remarks, now, t.actor,
	)
	if err != nil {
		return 0, fmt.Errorf("core: insert vernacular (taxon=%s, name=%q): %w",
			v.TaxonID, v.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("core: vernacular rowid: %w", err)
	}
	return id, nil
}

// UpdateVernacular rewrites every editable column of the row at
// rowid. TaxonID is NOT editable — reparenting a vernacular is
// semantically a delete+add on a different taxon. Zero-row affected
// (unknown rowid) surfaces as ErrNotFound so the handler maps to 404.
//
// The full-row rewrite is intentional: sfga vernaculars are small
// and the whole-row PATCH matches how the frontend edit-form
// submits (all fields at once). Callers wanting field-level PATCH
// semantics load-then-modify-then-update.
func (t *Tx) UpdateVernacular(rowid int64, v coldp.Vernacular) error {
	if v.Name == "" {
		return fmt.Errorf("core: update vernacular %d: %w: name required", rowid, ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const upd = `UPDATE vernacular SET
		col__source_id = ?, col__name = ?,
		col__transliteration = ?, col__language = ?, col__preferred = ?,
		col__country = ?, col__area = ?,
		col__sex_id = ?, col__reference_id = ?,
		col__remarks = ?, col__modified = ?, col__modified_by = ?
	WHERE rowid = ?`
	res, err := t.tx.ExecContext(t.ctx, upd,
		nullIfEmpty(v.SourceID), v.Name,
		v.Transliteration, v.Language, nullBoolArg(v.Preferred),
		v.Country, v.Area,
		nullIfEmpty(v.Sex.ID()), nullIfEmpty(v.ReferenceID),
		v.Remarks, now, t.actor,
		rowid,
	)
	if err != nil {
		return fmt.Errorf("core: update vernacular %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update vernacular %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update vernacular %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// DeleteVernacular removes the row at rowid. Unknown rowid →
// ErrNotFound so the handler surfaces a 404 rather than silently
// no-op'ing (the frontend's confirm modal already assumes the row
// existed).
func (t *Tx) DeleteVernacular(rowid int64) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM vernacular WHERE rowid = ?`, rowid,
	)
	if err != nil {
		return fmt.Errorf("core: delete vernacular %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete vernacular %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete vernacular %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// nullBoolArg turns a sql.NullBool into the value to hand to
// ExecContext for an INTEGER-typed nullable column: nil for
// invalid, 0/1 for valid. Kept local to this file since only
// vernacular currently binds a NullBool from the coldp side.
func nullBoolArg(b sql.NullBool) any {
	if !b.Valid {
		return nil
	}
	if b.Bool {
		return 1
	}
	return 0
}

