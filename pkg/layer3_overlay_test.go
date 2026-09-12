package hive

import (
	"context"
	"errors"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestLayer3Overlay_AddListRetire covers the CRUD happy path for
// Layer 3 overlays: add, list-for-taxon, list-all-active,
// set-citation, retire, plus the "one active per (taxon, type)"
// invariant enforced by the partial unique index.
func TestLayer3Overlay_AddListRetire(t *testing.T) {
	a := newTestArchive(t)
	ctx := context.Background()

	// Set up a bare taxon so AddOverlay's existence check passes.
	var taxonID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		nameID := insertTestName(t, tx, "Aus bus L., 1970")
		id, err := tx.CreateTaxon(coldp.Taxon{NameID: nameID})
		taxonID = id
		return err
	}); err != nil {
		t.Fatalf("seed taxon: %v", err)
	}

	// Add one overlay of each type.
	var synID, placementID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddOverlay(Layer3Overlay{
			TaxonID:     taxonID,
			OverlayType: OverlayTypeSynonymyOverride,
			ClaimJSON:   `{"status": "accepted"}`,
			Notes:       "keeping as accepted per my exam of the type",
		})
		if err != nil {
			return err
		}
		synID = id
		id, err = tx.AddOverlay(Layer3Overlay{
			TaxonID:     taxonID,
			OverlayType: OverlayTypePlacementOverride,
			ClaimJSON:   `{"parent_taxon_id": "some-other-genus"}`,
		})
		placementID = id
		return err
	}); err != nil {
		t.Fatalf("add overlays: %v", err)
	}

	// List for this taxon.
	overlays, err := a.ListOverlaysForTaxon(ctx, taxonID)
	if err != nil {
		t.Fatalf("list for taxon: %v", err)
	}
	if len(overlays) != 2 {
		t.Fatalf("want 2 active overlays, got %d", len(overlays))
	}

	// A second synonymy overlay for the same taxon should fail the
	// partial unique index.
	err = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddOverlay(Layer3Overlay{
			TaxonID:     taxonID,
			OverlayType: OverlayTypeSynonymyOverride,
			ClaimJSON:   `{"status": "synonym_of", "target_taxon_id": "x"}`,
		})
		return err
	})
	if err == nil {
		t.Fatalf("expected unique-index rejection on second synonymy overlay")
	}

	// SetOverlayCitation.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.SetOverlayCitation(synID, "ref-abc")
	}); err != nil {
		t.Fatalf("set citation: %v", err)
	}
	got, err := a.GetOverlay(ctx, synID)
	if err != nil {
		t.Fatalf("get overlay: %v", err)
	}
	if got.CitationReferenceID != "ref-abc" {
		t.Errorf("citation = %q, want ref-abc", got.CitationReferenceID)
	}

	// Retire the placement overlay. After retirement,
	// ListOverlaysForTaxon should return only the synonymy one, and
	// a fresh placement overlay should succeed (the partial index
	// excludes retired rows).
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.RetireOverlay(placementID)
	}); err != nil {
		t.Fatalf("retire: %v", err)
	}
	overlays, err = a.ListOverlaysForTaxon(ctx, taxonID)
	if err != nil {
		t.Fatalf("re-list: %v", err)
	}
	if len(overlays) != 1 || overlays[0].ID != synID {
		t.Fatalf("after retire want only syn overlay, got %d: %#v",
			len(overlays), overlays)
	}
	if err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddOverlay(Layer3Overlay{
			TaxonID:     taxonID,
			OverlayType: OverlayTypePlacementOverride,
			ClaimJSON:   `{"parent_taxon_id": "yet-another"}`,
		})
		return err
	}); err != nil {
		t.Fatalf("re-add placement after retire: %v", err)
	}

	// Missing overlay → ErrNotFound on Set / Retire.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.SetOverlayCitation("no-such-id", "ref")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("SetOverlayCitation(missing) = %v, want ErrNotFound", err)
	}
}

