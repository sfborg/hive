package core

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// synSetup creates an archive with one accepted taxon plus a distinct name
// suitable to be used as a synonym. Returns (archive, taxonID, synNameID).
func synSetup(t *testing.T) (*Archive, string, string) {
	t.Helper()
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var taxonID, synNameID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		accName := insertTestName(t, tx, "Panthera leo")
		id, err := tx.CreateTaxon(taxonForTest(accName))
		if err != nil {
			return err
		}
		taxonID = id
		synNameID = insertTestName(t, tx, "Felis leo")
		return nil
	})
	if err != nil {
		t.Fatalf("synSetup: %v", err)
	}
	return a, taxonID, synNameID
}

func TestAddSynonymBasic(t *testing.T) {
	a, taxonID, synNameID := synSetup(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var synID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddSynonym(coldp.Synonym{
			TaxonID: taxonID,
			NameID:  synNameID,
			Status:  coldp.SynonymTS,
			Remarks: "test synonym",
		})
		synID = id
		return err
	})
	if err != nil {
		t.Fatalf("AddSynonym: %v", err)
	}
	if _, err := uuid.Parse(synID); err != nil {
		t.Fatalf("generated synonym ID %q is not a UUID: %v", synID, err)
	}

	got, err := a.GetSynonym(ctx, taxonID, synNameID)
	if err != nil {
		t.Fatalf("GetSynonym: %v", err)
	}
	if got.ID != synID {
		t.Errorf("ID = %q, want %q", got.ID, synID)
	}
	if got.TaxonID != taxonID {
		t.Errorf("TaxonID = %q, want %q", got.TaxonID, taxonID)
	}
	if got.NameID != synNameID {
		t.Errorf("NameID = %q, want %q", got.NameID, synNameID)
	}
	if got.Status != coldp.SynonymTS {
		t.Errorf("Status = %v, want SynonymTS", got.Status)
	}
	if got.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q, want the actor ORCID", got.ModifiedBy)
	}
	if got.Remarks != "test synonym" {
		t.Errorf("Remarks = %q, want %q", got.Remarks, "test synonym")
	}
}

func TestAddSynonymRequiresTaxonAndName(t *testing.T) {
	a, taxonID, _ := synSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSynonym(coldp.Synonym{NameID: "some-name"})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("AddSynonym without TaxonID: got %v, want ErrValidation", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxonID})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("AddSynonym without NameID: got %v, want ErrValidation", err)
	}
}

func TestAddSynonymSelfLinkRejected(t *testing.T) {
	a, taxonID, synNameID := synSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSynonym(coldp.Synonym{
			ID:      taxonID, // deliberately equal to TaxonID
			TaxonID: taxonID,
			NameID:  synNameID,
		})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("AddSynonym with synonym.ID == TaxonID: got %v, want ErrValidation", err)
	}
}

func TestListSynonyms(t *testing.T) {
	a, taxonID, _ := synSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		nameA := insertTestName(t, tx, "Aardvark old-name")
		nameZ := insertTestName(t, tx, "Zebra old-name")
		if _, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxonID, NameID: nameZ}); err != nil {
			return err
		}
		if _, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxonID, NameID: nameA}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	syns, err := a.ListSynonyms(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListSynonyms: %v", err)
	}
	if len(syns) != 2 {
		t.Fatalf("ListSynonyms returned %d, want 2", len(syns))
	}
	// Alphabetical order by name — Aardvark first.
	// (synSetup inserts Felis leo which is not a synonym here, only a name;
	// ListSynonyms filters by taxon_id and Felis leo isn't linked.)
}

func TestProParteSynonym(t *testing.T) {
	// Set up two accepted taxa; add a synonym to the first; extend to point at
	// the second via AddSynonymTaxon; verify SynonymPartners returns both.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var taxA, taxB, synName, synID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		accA := insertTestName(t, tx, "Panthera leo")
		a1, err := tx.CreateTaxon(taxonForTest(accA))
		if err != nil {
			return err
		}
		taxA = a1

		accB := insertTestName(t, tx, "Panthera onca")
		b1, err := tx.CreateTaxon(taxonForTest(accB))
		if err != nil {
			return err
		}
		taxB = b1

		synName = insertTestName(t, tx, "Felis leo")
		sid, err := tx.AddSynonym(coldp.Synonym{
			TaxonID: taxA,
			NameID:  synName,
			Status:  coldp.SynonymTS,
		})
		if err != nil {
			return err
		}
		synID = sid

		return tx.AddSynonymTaxon(synID, taxB)
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	partners, err := a.SynonymPartners(ctx, synID)
	if err != nil {
		t.Fatalf("SynonymPartners: %v", err)
	}
	if len(partners) != 2 {
		t.Fatalf("SynonymPartners returned %d, want 2 (pro-parte)", len(partners))
	}

	// Verify the pro-parte link inherited fields from the seed row.
	proP, err := a.GetSynonym(ctx, taxB, synName)
	if err != nil {
		t.Fatalf("GetSynonym pro-parte link: %v", err)
	}
	if proP.NameID != synName {
		t.Errorf("pro-parte NameID = %q, want %q", proP.NameID, synName)
	}
	if proP.Status != coldp.SynonymTS {
		t.Errorf("pro-parte Status = %v, want SynonymTS (inherited)", proP.Status)
	}
}

func TestAddSynonymTaxonIdempotent(t *testing.T) {
	a, taxonID, synNameID := synSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var synID string
	_ = a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxonID, NameID: synNameID})
		synID = id
		return err
	})

	// Calling AddSynonymTaxon with the same (synID, taxonID) is a no-op.
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.AddSynonymTaxon(synID, taxonID)
	})
	if err != nil {
		t.Fatalf("AddSynonymTaxon idempotent call returned %v", err)
	}

	partners, err := a.SynonymPartners(ctx, synID)
	if err != nil {
		t.Fatalf("SynonymPartners: %v", err)
	}
	if len(partners) != 1 {
		t.Errorf("SynonymPartners after idempotent add: got %d, want 1", len(partners))
	}
}

func TestAddSynonymTaxonUnknownSynonym(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.AddSynonymTaxon("no-such-synonym", "no-such-taxon")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddSynonymTaxon on missing synonym: got %v, want ErrNotFound", err)
	}
}

func TestUpdateSynonymOptimisticConcurrency(t *testing.T) {
	a, taxonID, synNameID := synSetup(t)
	ctx := WithActor(context.Background(), "tester")

	_ = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxonID, NameID: synNameID})
		return err
	})

	original, err := a.GetSynonym(ctx, taxonID, synNameID)
	if err != nil {
		t.Fatalf("GetSynonym: %v", err)
	}

	// Someone else updates in the meantime.
	err = a.WithTx(WithActor(ctx, "other"), func(tx *Tx) error {
		other, _ := a.GetSynonym(ctx, taxonID, synNameID)
		other.Remarks = "concurrent update"
		return tx.UpdateSynonym(*other)
	})
	if err != nil {
		t.Fatalf("concurrent update: %v", err)
	}

	// Our update with the stale Modified token must fail.
	original.Remarks = "our update"
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateSynonym(*original)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale-token UpdateSynonym: got %v, want ErrConflict", err)
	}
}

func TestRemoveSynonym(t *testing.T) {
	a, taxonID, synNameID := synSetup(t)
	ctx := WithActor(context.Background(), "tester")

	_ = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxonID, NameID: synNameID})
		return err
	})

	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.RemoveSynonym(taxonID, synNameID)
	})
	if err != nil {
		t.Fatalf("RemoveSynonym: %v", err)
	}
	if _, err := a.GetSynonym(ctx, taxonID, synNameID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after remove: got %v, want ErrNotFound", err)
	}
}

func TestRemoveSynonymNotFound(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.RemoveSynonym("no-taxon", "no-name")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveSynonym missing: got %v, want ErrNotFound", err)
	}
}

func TestRemoveSynonymProParteLeavesOtherLinks(t *testing.T) {
	// Removing one link of a pro-parte synonym must leave the others intact.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var taxA, taxB, synName, synID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		accA := insertTestName(t, tx, "Panthera leo")
		a1, _ := tx.CreateTaxon(taxonForTest(accA))
		taxA = a1
		accB := insertTestName(t, tx, "Panthera onca")
		b1, _ := tx.CreateTaxon(taxonForTest(accB))
		taxB = b1
		synName = insertTestName(t, tx, "Felis leo")
		sid, err := tx.AddSynonym(coldp.Synonym{TaxonID: taxA, NameID: synName})
		if err != nil {
			return err
		}
		synID = sid
		return tx.AddSynonymTaxon(synID, taxB)
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Remove the link to taxA. The pro-parte link to taxB must remain.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.RemoveSynonym(taxA, synName)
	})
	if err != nil {
		t.Fatalf("RemoveSynonym one leg: %v", err)
	}

	partners, err := a.SynonymPartners(ctx, synID)
	if err != nil {
		t.Fatalf("SynonymPartners after remove: %v", err)
	}
	if len(partners) != 1 || partners[0] != taxB {
		t.Fatalf("SynonymPartners = %v, want [%s]", partners, taxB)
	}
}
