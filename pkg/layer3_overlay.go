package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Overlay types are the discriminator on hive__layer3_overlay.
// Each value picks the shape claim_json carries (see
// [[layer3-claim-shapes]] memory).
const (
	// OverlayTypeSynonymyOverride marks the curator's disagreement
	// with the Layer 2 synonymy decision for the overlay's taxon
	// (e.g., L2 lumped A into B; curator keeps A accepted).
	OverlayTypeSynonymyOverride = "synonymy_override"

	// OverlayTypePlacementOverride marks the curator's disagreement
	// with the Layer 2 parent / higher-classification assignment.
	OverlayTypePlacementOverride = "placement_override"

	// OverlayTypeConceptOverride marks a curator-authored concept
	// relation that diverges from the Layer 2 relation (sensu
	// qualifier, RCC5 type).
	OverlayTypeConceptOverride = "concept_override"

	// OverlayTypeOther is the free-form escape hatch; claim_json
	// carries the payload shape.
	OverlayTypeOther = "other"
)

// Layer3Overlay is one row of hive__layer3_overlay — a
// curator-authored hypothesis that diverges from the Layer 2
// (machine-assembled) view. See
// docs/2026-09-12_LITERATURE_VIEW.md § Layer 3 for semantics.
type Layer3Overlay struct {
	ID                  string
	TaxonID             string
	OverlayType         string
	ClaimJSON           string // JSON blob; shape depends on OverlayType
	Notes               string
	CitationReferenceID string // empty until a publication supports the overlay
	RetiredAt           time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CreatedBy           string
}

// IsActive reports whether the overlay hasn't been retired.
func (o Layer3Overlay) IsActive() bool { return o.RetiredAt.IsZero() }

// validOverlayType reports whether t is a known overlay type.
func validOverlayType(t string) bool {
	switch t {
	case OverlayTypeSynonymyOverride, OverlayTypePlacementOverride,
		OverlayTypeConceptOverride, OverlayTypeOther:
		return true
	}
	return false
}

// AddOverlay inserts a new active overlay row. Enforces the
// "one active row per (taxon_id, overlay_type)" invariant
// declared by the schema's partial unique index.
func (t *Tx) AddOverlay(o Layer3Overlay) (string, error) {
	if o.TaxonID == "" {
		return "", fmt.Errorf("core: AddOverlay: %w: taxon_id required", ErrValidation)
	}
	if !validOverlayType(o.OverlayType) {
		return "", fmt.Errorf("core: AddOverlay: %w: unknown overlay_type %q",
			ErrValidation, o.OverlayType)
	}
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	if o.ClaimJSON == "" {
		o.ClaimJSON = "{}"
	}
	if _, err := t.getTaxonExists(t.ctx, o.TaxonID); err != nil {
		return "", fmt.Errorf("core: AddOverlay: %w: taxon %s not found",
			ErrNotFound, o.TaxonID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `INSERT INTO hive__layer3_overlay (
	    id, taxon_id, overlay_type, claim_json, notes,
	    citation_reference_id, retired_at,
	    created_at, updated_at, created_by
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`
	if _, err := t.tx.ExecContext(t.ctx, q,
		o.ID, o.TaxonID, o.OverlayType, o.ClaimJSON, o.Notes,
		nullIfEmpty(o.CitationReferenceID),
		now, now, t.actor,
	); err != nil {
		return "", fmt.Errorf("core: insert hive__layer3_overlay: %w", err)
	}
	return o.ID, nil
}

// SetOverlayCitation populates citation_reference_id on the
// overlay. Called by the assembler's L3 diff pass when a paper's
// assertion is detected to agree with an existing overlay, and
// exposed via the HTTP API so curators can attach a citation
// manually.
func (t *Tx) SetOverlayCitation(overlayID, referenceID string) error {
	if overlayID == "" {
		return fmt.Errorf("core: SetOverlayCitation: %w: overlay_id required", ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__layer3_overlay
		    SET citation_reference_id = ?, updated_at = ?
		  WHERE id = ? AND retired_at IS NULL`,
		nullIfEmpty(referenceID), now, overlayID)
	if err != nil {
		return fmt.Errorf("core: SetOverlayCitation(%s): %w", overlayID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: SetOverlayCitation(%s): %w", overlayID, ErrNotFound)
	}
	return nil
}

// RetireOverlay stamps retired_at on an active overlay so it
// stops participating in Layer 4 rendering and the L3 diff pass.
// Retired rows stay in the table for provenance.
func (t *Tx) RetireOverlay(overlayID string) error {
	if overlayID == "" {
		return fmt.Errorf("core: RetireOverlay: %w: overlay_id required", ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__layer3_overlay
		    SET retired_at = ?, updated_at = ?
		  WHERE id = ? AND retired_at IS NULL`,
		now, now, overlayID)
	if err != nil {
		return fmt.Errorf("core: RetireOverlay(%s): %w", overlayID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: RetireOverlay(%s): %w", overlayID, ErrNotFound)
	}
	return nil
}

// GetOverlay returns one overlay by id — active OR retired. The
// HTTP layer decides whether to surface retired rows.
func (a *Archive) GetOverlay(ctx context.Context, id string) (*Layer3Overlay, error) {
	row := a.db.QueryRowContext(ctx, overlaySelectSQL+` WHERE id = ?`, id)
	o, err := scanOverlay(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("core: overlay %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return o, nil
}

// ListOverlaysForTaxon returns the active overlays for one taxon,
// ordered by overlay_type for stable output.
func (a *Archive) ListOverlaysForTaxon(ctx context.Context, taxonID string) ([]Layer3Overlay, error) {
	if taxonID == "" {
		return nil, errors.New("core: ListOverlaysForTaxon: taxon_id required")
	}
	rows, err := a.db.QueryContext(ctx,
		overlaySelectSQL+` WHERE taxon_id = ? AND retired_at IS NULL
		 ORDER BY overlay_type ASC`, taxonID)
	if err != nil {
		return nil, fmt.Errorf("core: list overlays for taxon %s: %w", taxonID, err)
	}
	defer rows.Close()
	var out []Layer3Overlay
	for rows.Next() {
		o, err := scanOverlay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// ListActiveOverlays returns every non-retired overlay in the
// archive. Used by the assembler's Layer 3 diff pass.
func (a *Archive) ListActiveOverlays(ctx context.Context) ([]Layer3Overlay, error) {
	rows, err := a.db.QueryContext(ctx,
		overlaySelectSQL+` WHERE retired_at IS NULL
		 ORDER BY taxon_id ASC, overlay_type ASC`)
	if err != nil {
		return nil, fmt.Errorf("core: list active overlays: %w", err)
	}
	defer rows.Close()
	var out []Layer3Overlay
	for rows.Next() {
		o, err := scanOverlay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// Tx-visible variant of ListActiveOverlays for reads inside a
// reassemble transaction.
func (t *Tx) listActiveOverlays() ([]Layer3Overlay, error) {
	rows, err := t.tx.QueryContext(t.ctx,
		overlaySelectSQL+` WHERE retired_at IS NULL
		 ORDER BY taxon_id ASC, overlay_type ASC`)
	if err != nil {
		return nil, fmt.Errorf("core: tx list active overlays: %w", err)
	}
	defer rows.Close()
	var out []Layer3Overlay
	for rows.Next() {
		o, err := scanOverlay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

const overlaySelectSQL = `SELECT
    id, taxon_id, overlay_type, claim_json, notes,
    COALESCE(citation_reference_id, ''),
    COALESCE(retired_at, ''),
    created_at, updated_at, created_by
FROM hive__layer3_overlay`

func scanOverlay(r rowScanner) (*Layer3Overlay, error) {
	var o Layer3Overlay
	var retiredAt, createdAt, updatedAt string
	if err := r.Scan(
		&o.ID, &o.TaxonID, &o.OverlayType, &o.ClaimJSON, &o.Notes,
		&o.CitationReferenceID,
		&retiredAt,
		&createdAt, &updatedAt, &o.CreatedBy,
	); err != nil {
		return nil, err
	}
	if retiredAt != "" {
		o.RetiredAt, _ = time.Parse(time.RFC3339Nano, retiredAt)
	}
	o.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	o.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &o, nil
}
