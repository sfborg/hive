package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// Type material rows attach to a name (col__name_id), matching the
// ColDP rule that type material is associated with the original
// name — not a recombination — and never with a taxon. The sfga
// table's col__id is optional and non-unique, so hive uses SQLite's
// implicit rowid as the addressable handle (same convention as
// vernacular / distribution / species_interaction) and surfaces
// col__id as the curator-facing SpecimenID field.
//
// Nullable coordinates and altitude round-trip through
// sql.NullFloat64 / sql.NullInt64 so an unset row stays NULL rather
// than being coerced to 0. Server callers project these to
// pointer-optional JSON fields.

// TypeMaterialHit is the read projection returned by
// ListTypeMaterials and GetTypeMaterial. RowID carries the SQLite
// rowid handle; SpecimenID is the optional col__id value the
// curator may fill (e.g. the collection / catalogue identifier).
type TypeMaterialHit struct {
	RowID               int64
	SpecimenID          string
	NameID              string
	SourceID            string
	Citation            string
	Status              coldp.TypeStatus
	InstitutionCode     string
	CatalogNumber       string
	ReferenceID         string
	Locality            string
	Country             string
	Latitude            sql.NullFloat64
	Longitude           sql.NullFloat64
	Altitude            sql.NullInt64
	Host                string
	Sex                 coldp.Sex
	Date                string
	Collector           string
	AssociatedSequences string
	Link                string
	Remarks             string
	Modified            string
	ModifiedBy          string
	// IssueCount is the number of open validation issues currently
	// filed against this type_material row. Populated per-hit by
	// ListTypeMaterials in a batched grouping query. Zero when
	// clean; front-ends render a warn icon when > 0.
	IssueCount int
	// MaxSeverity is the highest severity ("error" > "warn" > "info"
	// > "debug") among the row's open issues. Empty string when
	// IssueCount is 0. Drives the badge color the WUI paints.
	MaxSeverity string
}

// ListTypeMaterials returns every type_material row attached to
// the given name, ordered by status then rowid so holotype /
// lectotype records sort near the top of the display.
func (a *Archive) ListTypeMaterials(ctx context.Context, nameID string) ([]TypeMaterialHit, error) {
	const q = `SELECT
		rowid,
		COALESCE(col__id, ''),
		col__name_id,
		COALESCE(col__source_id, ''),
		COALESCE(col__citation, ''),
		COALESCE(col__status_id, ''),
		COALESCE(col__institution_code, ''),
		COALESCE(col__catalog_number, ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__locality, ''),
		COALESCE(col__country, ''),
		col__latitude,
		col__longitude,
		col__altitude,
		COALESCE(col__host, ''),
		COALESCE(col__sex_id, ''),
		COALESCE(col__date, ''),
		COALESCE(col__collector, ''),
		COALESCE(col__associated_sequences, ''),
		COALESCE(col__link, ''),
		COALESCE(col__remarks, ''),
		COALESCE(col__modified, ''),
		COALESCE(col__modified_by, '')
	FROM type_material
	WHERE col__name_id = ?
	ORDER BY col__status_id, rowid`
	rows, err := a.db.QueryContext(ctx, q, nameID)
	if err != nil {
		return nil, fmt.Errorf("core: list type_material of %q: %w", nameID, err)
	}
	defer rows.Close()
	var hits []TypeMaterialHit
	for rows.Next() {
		var (
			h                 TypeMaterialHit
			statusID, sexID string
		)
		if err := rows.Scan(
			&h.RowID, &h.SpecimenID, &h.NameID, &h.SourceID,
			&h.Citation, &statusID,
			&h.InstitutionCode, &h.CatalogNumber, &h.ReferenceID,
			&h.Locality, &h.Country,
			&h.Latitude, &h.Longitude, &h.Altitude,
			&h.Host, &sexID,
			&h.Date, &h.Collector, &h.AssociatedSequences,
			&h.Link, &h.Remarks,
			&h.Modified, &h.ModifiedBy,
		); err != nil {
			return nil, fmt.Errorf("core: scan type_material of %q: %w", nameID, err)
		}
		h.Status = coldp.NewTypeStatus(statusID)
		h.Sex = coldp.NewSex(sexID)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fold in per-row open-issue summaries (count + max severity) in
	// one batched pass via the shared helper. Record IDs are the
	// rowid stringified — the type_material table's col__id is
	// optional / non-unique and unfit as a validation record key.
	if len(hits) > 0 {
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = fmt.Sprintf("%d", h.RowID)
		}
		summaries, err := a.batchIssueSummaries(ctx, "type_material", ids)
		if err != nil {
			return nil, fmt.Errorf("core: type_material issue summaries of %q: %w", nameID, err)
		}
		for i := range hits {
			s := summaries[ids[i]]
			hits[i].IssueCount = s.Count
			hits[i].MaxSeverity = s.MaxSeverity
		}
	}
	return hits, nil
}

// GetTypeMaterial returns the row with the given rowid or
// ErrNotFound when nothing matches. Frontends typically already
// have the row from a list call; this method exists for direct-
// address paths (deep links, PATCH round-trips, tests).
func (a *Archive) GetTypeMaterial(ctx context.Context, rowid int64) (*TypeMaterialHit, error) {
	const q = `SELECT
		rowid,
		COALESCE(col__id, ''),
		col__name_id,
		COALESCE(col__source_id, ''),
		COALESCE(col__citation, ''),
		COALESCE(col__status_id, ''),
		COALESCE(col__institution_code, ''),
		COALESCE(col__catalog_number, ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__locality, ''),
		COALESCE(col__country, ''),
		col__latitude,
		col__longitude,
		col__altitude,
		COALESCE(col__host, ''),
		COALESCE(col__sex_id, ''),
		COALESCE(col__date, ''),
		COALESCE(col__collector, ''),
		COALESCE(col__associated_sequences, ''),
		COALESCE(col__link, ''),
		COALESCE(col__remarks, ''),
		COALESCE(col__modified, ''),
		COALESCE(col__modified_by, '')
	FROM type_material WHERE rowid = ? LIMIT 1`
	var (
		h               TypeMaterialHit
		statusID, sexID string
	)
	err := a.db.QueryRowContext(ctx, q, rowid).Scan(
		&h.RowID, &h.SpecimenID, &h.NameID, &h.SourceID,
		&h.Citation, &statusID,
		&h.InstitutionCode, &h.CatalogNumber, &h.ReferenceID,
		&h.Locality, &h.Country,
		&h.Latitude, &h.Longitude, &h.Altitude,
		&h.Host, &sexID,
		&h.Date, &h.Collector, &h.AssociatedSequences,
		&h.Link, &h.Remarks,
		&h.Modified, &h.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: type_material %d: %w", rowid, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get type_material %d: %w", rowid, err)
	}
	h.Status = coldp.NewTypeStatus(statusID)
	h.Sex = coldp.NewSex(sexID)
	return &h, nil
}

// AddTypeMaterial inserts a new type_material row and returns the
// new rowid as the opaque handle for later PATCH/DELETE.
//
//   - t.NameID is required; empty → ErrValidation.
//   - Nullable FKs ("" → NULL): source_id, status_id, reference_id, sex_id.
//   - Nullable numerics: Latitude, Longitude, Altitude — Valid=false
//     stores NULL, Valid=true stores the given value (0 included).
//   - col__id (SpecimenID) passes through verbatim including empty.
//   - col__modified / col__modified_by stamped from tx context.
func (t *Tx) AddTypeMaterial(m coldp.TypeMaterial) (int64, error) {
	if m.NameID == "" {
		return 0, fmt.Errorf("core: add type_material: %w: name_id required", ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const insert = `INSERT INTO type_material (
		col__id, col__source_id, col__name_id, col__citation,
		col__status_id, col__institution_code, col__catalog_number,
		col__reference_id, col__locality, col__country,
		col__latitude, col__longitude, col__altitude,
		col__host, col__sex_id, col__date, col__collector,
		col__associated_sequences, col__link, col__remarks,
		col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := t.tx.ExecContext(t.ctx, insert,
		m.ID, nullIfEmpty(m.SourceID), m.NameID, m.Citation,
		nullIfEmpty(m.Status.ID()), m.InstitutionCode, m.CatalogNumber,
		nullIfEmpty(m.ReferenceID), m.Locality, m.Country,
		nullFloatArg(m.Latitude), nullFloatArg(m.Longitude), nullIntArg(m.Altitude),
		m.Host, nullIfEmpty(m.Sex.ID()), m.Date, m.Collector,
		m.AssociatedSequences, m.Link, m.Remarks,
		now, t.actor,
	)
	if err != nil {
		return 0, fmt.Errorf("core: insert type_material (name=%s): %w", m.NameID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("core: type_material rowid: %w", err)
	}
	return id, nil
}

// UpdateTypeMaterial rewrites every editable column of the row at
// rowid. NameID is NOT editable — reparenting a type_material row is
// semantically a delete+add on a different name. Zero rows affected
// (unknown rowid) surfaces as ErrNotFound so the handler maps to 404.
func (t *Tx) UpdateTypeMaterial(rowid int64, m coldp.TypeMaterial) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const upd = `UPDATE type_material SET
		col__id = ?, col__source_id = ?, col__citation = ?,
		col__status_id = ?, col__institution_code = ?, col__catalog_number = ?,
		col__reference_id = ?, col__locality = ?, col__country = ?,
		col__latitude = ?, col__longitude = ?, col__altitude = ?,
		col__host = ?, col__sex_id = ?, col__date = ?, col__collector = ?,
		col__associated_sequences = ?, col__link = ?, col__remarks = ?,
		col__modified = ?, col__modified_by = ?
	WHERE rowid = ?`
	res, err := t.tx.ExecContext(t.ctx, upd,
		m.ID, nullIfEmpty(m.SourceID), m.Citation,
		nullIfEmpty(m.Status.ID()), m.InstitutionCode, m.CatalogNumber,
		nullIfEmpty(m.ReferenceID), m.Locality, m.Country,
		nullFloatArg(m.Latitude), nullFloatArg(m.Longitude), nullIntArg(m.Altitude),
		m.Host, nullIfEmpty(m.Sex.ID()), m.Date, m.Collector,
		m.AssociatedSequences, m.Link, m.Remarks,
		now, t.actor,
		rowid,
	)
	if err != nil {
		return fmt.Errorf("core: update type_material %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update type_material %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update type_material %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// DeleteTypeMaterial removes the row at rowid. Unknown rowid →
// ErrNotFound so the handler surfaces a 404 rather than silently
// no-op'ing.
func (t *Tx) DeleteTypeMaterial(rowid int64) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM type_material WHERE rowid = ?`, rowid,
	)
	if err != nil {
		return fmt.Errorf("core: delete type_material %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete type_material %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete type_material %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// nullFloatArg turns a sql.NullFloat64 into the value to hand
// ExecContext for a nullable REAL column: nil for invalid, the
// float otherwise (0 included).
func nullFloatArg(f sql.NullFloat64) any {
	if !f.Valid {
		return nil
	}
	return f.Float64
}

// nullIntArg turns a sql.NullInt64 into the value to hand
// ExecContext for a nullable INTEGER column: nil for invalid, the
// int otherwise.
func nullIntArg(i sql.NullInt64) any {
	if !i.Valid {
		return nil
	}
	return i.Int64
}
