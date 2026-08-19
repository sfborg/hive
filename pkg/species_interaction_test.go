package hive

import (
	"context"
	"errors"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// siSetup creates an archive with two taxa — a "focus" taxon and a
// separate "related" taxon so interaction rows can point between
// them. Returns (archive, focusID, relatedID).
func siSetup(t *testing.T) (*Archive, string, string) {
	t.Helper()
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")
	var focusID, relatedID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		focusName := insertTestName(t, tx, "Panthera leo")
		id, err := tx.CreateTaxon(taxonForTest(focusName))
		if err != nil {
			return err
		}
		focusID = id
		relName := insertTestName(t, tx, "Ixodes scapularis")
		id, err = tx.CreateTaxon(taxonForTest(relName))
		if err != nil {
			return err
		}
		relatedID = id
		return nil
	})
	if err != nil {
		t.Fatalf("siSetup: %v", err)
	}
	return a, focusID, relatedID
}

func TestAddSpeciesInteractionBasic(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID:        focusID,
			RelatedTaxonID: relatedID,
			Type:           coldp.NewSpInteractionType("HOST_OF"),
			Remarks:        "observed in field study",
		}, "")
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddSpeciesInteraction: %v", err)
	}
	if rowid <= 0 {
		t.Fatalf("expected positive rowid, got %d", rowid)
	}
	hits, err := a.ListSpeciesInteractions(ctx, focusID)
	if err != nil {
		t.Fatalf("ListSpeciesInteractions: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.RowID != rowid {
		t.Errorf("RowID = %d, want %d", h.RowID, rowid)
	}
	if h.RelatedTaxonID != relatedID {
		t.Errorf("RelatedTaxonID = %q, want %q", h.RelatedTaxonID, relatedID)
	}
	if h.RelatedTaxonLabel.Text == "" {
		t.Errorf("RelatedTaxonLabel.Text is empty; expected the rendered label for the related taxon")
	}
	if h.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q, want actor", h.ModifiedBy)
	}
}

// TestSpeciesInteractionFreeformTypeSurvivesRead — sfga stores
// col__type_id as TEXT with a soft FK to species_interaction_type;
// imports that ran with foreign_keys OFF can plant values not in
// the vocab (e.g. lowercase "eats" from 3i.db). The read path
// must surface the raw string via TypeRaw so the WUI displays it
// as-authored instead of blanking on the enum coercion (sflib's
// NewSpInteractionType returns UnknownSpIntT whose .ID() is "").
func TestSpeciesInteractionFreeformTypeSurvivesRead(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "tester")
	// Plant a freeform type via direct SQL with FK checks off —
	// mirrors how bulk imports produce rows the vocab doesn't cover.
	if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("pragma off: %v", err)
	}
	res, err := a.db.ExecContext(ctx,
		`INSERT INTO species_interaction (col__taxon_id, col__related_taxon_id, col__type_id) VALUES (?, ?, ?)`,
		focusID, relatedID, "eats",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	rowid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma on: %v", err)
	}
	got, err := a.GetSpeciesInteraction(ctx, rowid)
	if err != nil {
		t.Fatalf("GetSpeciesInteraction: %v", err)
	}
	if got.TypeRaw != "eats" {
		t.Errorf("TypeRaw = %q, want %q", got.TypeRaw, "eats")
	}
	if got.Type.ID() != "" {
		t.Errorf("Type.ID() = %q, want empty (unknown-vocab value)", got.Type.ID())
	}
	hits, err := a.ListSpeciesInteractions(ctx, focusID)
	if err != nil {
		t.Fatalf("ListSpeciesInteractions: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].TypeRaw != "eats" {
		t.Errorf("list TypeRaw = %q, want %q", hits[0].TypeRaw, "eats")
	}
}

func TestAddSpeciesInteractionRequiresTaxon(t *testing.T) {
	a, _, _ := siSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			RelatedTaxonScientificName: "some free-text",
		}, "")
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
}

// TestAddSpeciesInteractionRequiresRelatedTaxon — sfga's
// col__related_taxon_id is NOT NULL with a FK to taxon; a free-
// text-only interaction (RelatedTaxonScientificName set but
// RelatedTaxonID empty) violates the FK. Hive rejects it up-front
// with ErrValidation so curators get a clear message rather than
// a SQL constraint error.
func TestAddSpeciesInteractionRequiresRelatedTaxon(t *testing.T) {
	a, focusID, _ := siSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID:                    focusID,
			RelatedTaxonScientificName: "Trypanosoma brucei",
			Type:                       coldp.NewSpInteractionType("PARASITE_OF"),
		}, "")
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
}

// TestAddSpeciesInteractionKeepsScientificName — when both the
// FK and the free-text scientific name are set, both round-trip.
// The scientific-name field is useful when the source cited a
// different name than what's currently stored on the related
// taxon.
func TestAddSpeciesInteractionKeepsScientificName(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "tester")
	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID:                    focusID,
			RelatedTaxonID:             relatedID,
			RelatedTaxonScientificName: "Ixodes dammini",
			Type:                       coldp.NewSpInteractionType("HOST_OF"),
		}, "")
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddSpeciesInteraction: %v", err)
	}
	got, err := a.GetSpeciesInteraction(ctx, rowid)
	if err != nil {
		t.Fatalf("GetSpeciesInteraction: %v", err)
	}
	if got.RelatedTaxonScientificName != "Ixodes dammini" {
		t.Errorf("RelatedTaxonScientificName = %q", got.RelatedTaxonScientificName)
	}
	if got.RelatedTaxonID != relatedID {
		t.Errorf("RelatedTaxonID = %q, want %q", got.RelatedTaxonID, relatedID)
	}
}

func TestUpdateSpeciesInteractionHappy(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID:        focusID,
			RelatedTaxonID: relatedID,
			Type:           coldp.NewSpInteractionType("HOST_OF"),
		}, "")
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateSpeciesInteraction(rowid, coldp.SpeciesInteraction{
			TaxonID:        focusID,
			RelatedTaxonID: relatedID,
			Type:           coldp.NewSpInteractionType("PREYS_UPON"),
			Remarks:        "revised interpretation",
		}, "")
	})
	if err != nil {
		t.Fatalf("UpdateSpeciesInteraction: %v", err)
	}
	got, err := a.GetSpeciesInteraction(ctx, rowid)
	if err != nil {
		t.Fatalf("GetSpeciesInteraction: %v", err)
	}
	if got.Type.ID() != "PREYS_UPON" {
		t.Errorf("Type = %q, want %q", got.Type.ID(), "PREYS_UPON")
	}
	if got.Remarks != "revised interpretation" {
		t.Errorf("Remarks = %q", got.Remarks)
	}
}

func TestUpdateSpeciesInteractionNotFound(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateSpeciesInteraction(999999, coldp.SpeciesInteraction{
			TaxonID:        focusID,
			RelatedTaxonID: relatedID,
			Type:           coldp.NewSpInteractionType("HOST_OF"),
		}, "")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteSpeciesInteraction(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var doomedID, survivorID int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID: focusID, RelatedTaxonID: relatedID,
			Type: coldp.NewSpInteractionType("HOST_OF"),
		}, "")
		if err != nil {
			return err
		}
		doomedID = id
		id, err = tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID: focusID, RelatedTaxonID: relatedID,
			Type: coldp.NewSpInteractionType("PREYS_UPON"),
		}, "")
		if err != nil {
			return err
		}
		survivorID = id
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteSpeciesInteraction(doomedID)
	})
	if err != nil {
		t.Fatalf("DeleteSpeciesInteraction: %v", err)
	}
	hits, err := a.ListSpeciesInteractions(ctx, focusID)
	if err != nil {
		t.Fatalf("ListSpeciesInteractions: %v", err)
	}
	if len(hits) != 1 || hits[0].RowID != survivorID {
		t.Errorf("expected only survivor %d remaining; got %+v", survivorID, hits)
	}
}

func TestDeleteSpeciesInteractionNotFound(t *testing.T) {
	a, _, _ := siSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteSpeciesInteraction(999999)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListSpeciesInteractionsDirectional — the list returns only
// rows where the given taxon is the SUBJECT (col__taxon_id). Rows
// where it's the object (col__related_taxon_id) belong to the
// other taxon's page.
func TestListSpeciesInteractionsDirectional(t *testing.T) {
	a, focusID, relatedID := siSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		// Focus is subject in one row.
		if _, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID: focusID, RelatedTaxonID: relatedID,
			Type: coldp.NewSpInteractionType("HOST_OF"),
		}, ""); err != nil {
			return err
		}
		// Focus is object (not subject) in another row.
		if _, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID: relatedID, RelatedTaxonID: focusID,
			Type: coldp.NewSpInteractionType("PARASITE_OF"),
		}, ""); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	focusHits, err := a.ListSpeciesInteractions(ctx, focusID)
	if err != nil {
		t.Fatalf("ListSpeciesInteractions(focus): %v", err)
	}
	if len(focusHits) != 1 {
		t.Errorf("focus should see 1 outgoing interaction, got %d", len(focusHits))
	}
	relatedHits, err := a.ListSpeciesInteractions(ctx, relatedID)
	if err != nil {
		t.Fatalf("ListSpeciesInteractions(related): %v", err)
	}
	if len(relatedHits) != 1 {
		t.Errorf("related should see 1 outgoing interaction, got %d", len(relatedHits))
	}
}

func TestGetSpeciesInteractionNotFound(t *testing.T) {
	a, _, _ := siSetup(t)
	_, err := a.GetSpeciesInteraction(context.Background(), 999999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
