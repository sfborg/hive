package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// The species_interaction table has no col__id column in sfga —
// a row is identified by (taxon_id, related_taxon_id, type_id) in
// practice, with nothing at the schema level preventing duplicates.
// Hive uses SQLite's implicit rowid as the opaque handle for
// read/update/delete, same pattern as vernacular / distribution.
//
// Both endpoints are required by the schema: col__taxon_id and
// col__related_taxon_id both have FKs to taxon(col__id).
// col__related_taxon_scientific_name lives alongside as a free-
// text annotation (useful when a curator wants to preserve the
// source's exact citation form) but doesn't substitute for the
// FK.
//
// Directionality: sfga's species_interaction is *directed* — a row
// describes the interaction from the subject taxon's perspective
// ("parasite parasiteOf host", "predator preysOn prey"). The
// per-taxon list returns only rows where the taxon is the subject
// (col__taxon_id). Curators who want to see the reverse view
// (interactions in which THIS taxon is the object) navigate to the
// related taxon's page. Two-sided rendering can layer on top later
// without changing the schema.

// SpeciesInteractionHit is the projection returned by
// ListSpeciesInteractions and the read side of the CRUD.
// RelatedTaxonLabel is server-resolved from the related taxon +
// name row so the front-end row can render "Panthera leo"
// without a per-row fetch. Empty when RelatedTaxonID is empty or
// the referenced taxon can't be resolved (free-text-only
// interactions rely on RelatedTaxonScientificName instead).
type SpeciesInteractionHit struct {
	RowID                      int64
	TaxonID                    string
	RelatedTaxonID             string
	RelatedTaxonScientificName string
	RelatedTaxonLabel          Label
	Type                       coldp.SpInteractionType
	// TypeRaw is the col__type_id verbatim from the DB — preserved
	// so freeform values (e.g. "eats" from datasets that don't
	// follow the SCREAMING_SNAKE_CASE convention) survive the round
	// trip. sflib's NewSpInteractionType maps unknown strings to
	// UnknownSpIntT whose .ID() is "", which would otherwise both
	// hide the value from the WUI display and silently zero-out the
	// column on any PATCH that doesn't touch the type. Callers
	// prefer Type.ID() (normalized enum) if non-empty, else fall
	// back to TypeRaw so freeform values render as-authored.
	TypeRaw    string
	SourceID   string
	ReferenceID                string
	Remarks                    string
	Modified                   string
	ModifiedBy                 string
	// IssueCount is the number of open (not-yet-acknowledged)
	// validation issues currently filed against this row. Populated
	// per-hit in a single batched grouping query — mirrors
	// vernacular / distribution.
	IssueCount int
}

// ListSpeciesInteractions returns every interaction row where the
// given taxon is the subject (col__taxon_id). Ordered by type then
// resolved related-name so the WUI list groups by interaction kind
// and reads alphabetically within a kind.
//
// Left-joins to the related taxon's name row when RelatedTaxonID
// is populated, so the returned Label carries a rendered display
// string without a follow-up per-row lookup. Free-text interactions
// (RelatedTaxonID empty, RelatedTaxonScientificName populated)
// leave Label empty; the frontend renders the free-text field.
func (a *Archive) ListSpeciesInteractions(ctx context.Context, taxonID string) ([]SpeciesInteractionHit, error) {
	const q = `SELECT
		si.rowid,
		si.col__taxon_id,
		COALESCE(si.col__related_taxon_id, ''),
		COALESCE(si.col__related_taxon_scientific_name, ''),
		COALESCE(si.col__type_id, ''),
		COALESCE(si.col__source_id, ''),
		COALESCE(si.col__reference_id, ''),
		COALESCE(si.col__remarks, ''),
		COALESCE(si.col__modified, ''),
		COALESCE(si.col__modified_by, ''),
		COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS related_name,
		COALESCE(n.col__authorship, '') AS related_authorship,
		COALESCE(n.col__rank_id, '') AS related_rank,
		t.col__extinct AS related_extinct
	FROM species_interaction si
	LEFT JOIN taxon t
		ON t.col__id = si.col__related_taxon_id
		AND si.col__related_taxon_id <> ''
	LEFT JOIN name n ON n.col__id = t.col__name_id
	WHERE si.col__taxon_id = ?
	ORDER BY si.col__type_id, related_name, si.col__related_taxon_id`
	rows, err := a.db.QueryContext(ctx, q, taxonID)
	if err != nil {
		return nil, fmt.Errorf("core: list species interactions of %q: %w", taxonID, err)
	}
	defer rows.Close()
	var hits []SpeciesInteractionHit
	for rows.Next() {
		var (
			h                        SpeciesInteractionHit
			typeID                   string
			relName, relAuth, relRnk string
			relExtinct               sql.NullBool
		)
		if err := rows.Scan(
			&h.RowID, &h.TaxonID,
			&h.RelatedTaxonID, &h.RelatedTaxonScientificName,
			&typeID,
			&h.SourceID, &h.ReferenceID, &h.Remarks,
			&h.Modified, &h.ModifiedBy,
			&relName, &relAuth, &relRnk, &relExtinct,
		); err != nil {
			return nil, fmt.Errorf("core: scan species interaction of %q: %w", taxonID, err)
		}
		h.Type = coldp.NewSpInteractionType(typeID)
		h.TypeRaw = typeID
		if relName != "" {
			h.RelatedTaxonLabel = BuildLabel(
				relName, relAuth, relRnk,
				relExtinct.Valid && relExtinct.Bool,
			)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fold in open-issue counts per rowid in one batched pass —
	// same pattern as vernacular / distribution.
	if len(hits) > 0 {
		ids := make([]any, len(hits))
		for i, h := range hits {
			ids[i] = fmt.Sprintf("%d", h.RowID)
		}
		q2 := `SELECT record_id, COUNT(*)
			FROM __gsvalidator_results
			WHERE table_name = 'species_interaction'
			  AND (acknowledged_at IS NULL OR acknowledged_at = '')
			  AND record_id IN (` + placeholders(len(ids)) + `)
			GROUP BY record_id`
		irows, err := a.db.QueryContext(ctx, q2, ids...)
		if err != nil {
			return nil, fmt.Errorf("core: species-interaction issue counts of %q: %w", taxonID, err)
		}
		counts := make(map[string]int, len(hits))
		for irows.Next() {
			var id string
			var n int
			if err := irows.Scan(&id, &n); err != nil {
				irows.Close()
				return nil, fmt.Errorf("core: scan species-interaction issue count: %w", err)
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

// GetSpeciesInteraction returns the row with the given rowid, or
// ErrNotFound when nothing matches. Frontends usually have the
// row from a list call already; this method exists for direct-
// address paths (deep links, PATCH round-trips that want the
// pre-image, tests).
func (a *Archive) GetSpeciesInteraction(ctx context.Context, rowid int64) (*SpeciesInteractionHit, error) {
	const q = `SELECT
		si.rowid,
		si.col__taxon_id,
		COALESCE(si.col__related_taxon_id, ''),
		COALESCE(si.col__related_taxon_scientific_name, ''),
		COALESCE(si.col__type_id, ''),
		COALESCE(si.col__source_id, ''),
		COALESCE(si.col__reference_id, ''),
		COALESCE(si.col__remarks, ''),
		COALESCE(si.col__modified, ''),
		COALESCE(si.col__modified_by, ''),
		COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS related_name,
		COALESCE(n.col__authorship, '') AS related_authorship,
		COALESCE(n.col__rank_id, '') AS related_rank,
		t.col__extinct AS related_extinct
	FROM species_interaction si
	LEFT JOIN taxon t
		ON t.col__id = si.col__related_taxon_id
		AND si.col__related_taxon_id <> ''
	LEFT JOIN name n ON n.col__id = t.col__name_id
	WHERE si.rowid = ? LIMIT 1`
	var (
		h                        SpeciesInteractionHit
		typeID                   string
		relName, relAuth, relRnk string
		relExtinct               sql.NullBool
	)
	err := a.db.QueryRowContext(ctx, q, rowid).Scan(
		&h.RowID, &h.TaxonID,
		&h.RelatedTaxonID, &h.RelatedTaxonScientificName,
		&typeID,
		&h.SourceID, &h.ReferenceID, &h.Remarks,
		&h.Modified, &h.ModifiedBy,
		&relName, &relAuth, &relRnk, &relExtinct,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: species interaction %d: %w", rowid, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get species interaction %d: %w", rowid, err)
	}
	h.Type = coldp.NewSpInteractionType(typeID)
	h.TypeRaw = typeID
	if relName != "" {
		h.RelatedTaxonLabel = BuildLabel(
			relName, relAuth, relRnk,
			relExtinct.Valid && relExtinct.Bool,
		)
	}
	return &h, nil
}

// AddSpeciesInteraction inserts a new interaction row and returns
// the new rowid as the opaque handle for later PATCH/DELETE.
//
//   - s.TaxonID and s.RelatedTaxonID are both required. sfga's
//     schema declares col__related_taxon_id NOT NULL with a FK to
//     taxon(col__id), so a purely free-text interaction (with only
//     RelatedTaxonScientificName populated) would fail the FK
//     check. The scientific-name field remains available as an
//     annotation alongside the FK, matching sfga's shape.
//   - Nullable FKs ("" → NULL): source_id, type_id, reference_id.
//   - typeRaw takes precedence over s.Type when non-empty — lets
//     callers preserve freeform vocab values (e.g. "eats" from
//     datasets that don't follow the controlled-vocab SCREAMING_
//     SNAKE_CASE convention) instead of coercing them to
//     UnknownSpIntT.ID() = "". Pass "" to fall back to s.Type.ID().
//   - col__modified / col__modified_by stamped from tx context.
func (t *Tx) AddSpeciesInteraction(s coldp.SpeciesInteraction, typeRaw string) (int64, error) {
	if s.TaxonID == "" {
		return 0, fmt.Errorf("core: add species interaction: %w: taxon_id required", ErrValidation)
	}
	if s.RelatedTaxonID == "" {
		return 0, fmt.Errorf("core: add species interaction: %w: related_taxon_id required", ErrValidation)
	}
	typeValue := typeRaw
	if typeValue == "" {
		typeValue = s.Type.ID()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const insert = `INSERT INTO species_interaction (
		col__taxon_id, col__related_taxon_id, col__source_id,
		col__related_taxon_scientific_name,
		col__type_id, col__reference_id,
		col__remarks, col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := t.tx.ExecContext(t.ctx, insert,
		s.TaxonID, s.RelatedTaxonID, nullIfEmpty(s.SourceID),
		s.RelatedTaxonScientificName,
		nullIfEmpty(typeValue), nullIfEmpty(s.ReferenceID),
		s.Remarks, now, t.actor,
	)
	if err != nil {
		return 0, fmt.Errorf("core: insert species interaction (taxon=%s, related=%s): %w",
			s.TaxonID, s.RelatedTaxonID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("core: species interaction rowid: %w", err)
	}
	return id, nil
}

// UpdateSpeciesInteraction rewrites every editable column of the
// row at rowid. TaxonID is NOT editable — reparenting is
// semantically a delete+add on a different taxon. Zero-row affected
// (unknown rowid) surfaces as ErrNotFound so the handler maps to 404.
// typeRaw takes precedence over s.Type when non-empty (see
// AddSpeciesInteraction for the rationale).
func (t *Tx) UpdateSpeciesInteraction(rowid int64, s coldp.SpeciesInteraction, typeRaw string) error {
	typeValue := typeRaw
	if typeValue == "" {
		typeValue = s.Type.ID()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const upd = `UPDATE species_interaction SET
		col__related_taxon_id = ?, col__source_id = ?,
		col__related_taxon_scientific_name = ?,
		col__type_id = ?, col__reference_id = ?,
		col__remarks = ?, col__modified = ?, col__modified_by = ?
	WHERE rowid = ?`
	res, err := t.tx.ExecContext(t.ctx, upd,
		s.RelatedTaxonID, nullIfEmpty(s.SourceID),
		s.RelatedTaxonScientificName,
		nullIfEmpty(typeValue), nullIfEmpty(s.ReferenceID),
		s.Remarks, now, t.actor,
		rowid,
	)
	if err != nil {
		return fmt.Errorf("core: update species interaction %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update species interaction %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update species interaction %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// DeleteSpeciesInteraction removes the row at rowid. Unknown
// rowid → ErrNotFound so the handler surfaces a 404.
func (t *Tx) DeleteSpeciesInteraction(rowid int64) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM species_interaction WHERE rowid = ?`, rowid,
	)
	if err != nil {
		return fmt.Errorf("core: delete species interaction %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete species interaction %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete species interaction %d: %w", rowid, ErrNotFound)
	}
	return nil
}
