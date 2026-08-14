package hive

import (
	"context"
	"errors"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

func distSetup(t *testing.T) (*Archive, string) {
	t.Helper()
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")
	var taxonID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Panthera leo")
		id, err := tx.CreateTaxon(taxonForTest(nm))
		if err != nil {
			return err
		}
		taxonID = id
		return nil
	})
	if err != nil {
		t.Fatalf("distSetup: %v", err)
	}
	return a, taxonID
}

func TestAddDistributionBasic(t *testing.T) {
	a, taxonID := distSetup(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddDistribution(coldp.Distribution{
			TaxonID:   taxonID,
			Area:      "Sub-Saharan Africa",
			AreaID:    "AFR",
			Gazetteer: coldp.NewGazetteerEnt("TDWG"),
			Status:    coldp.NewDistrStatus("NATIVE"),
			Remarks:   "widely distributed",
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddDistribution: %v", err)
	}
	if rowid <= 0 {
		t.Fatalf("expected positive rowid, got %d", rowid)
	}

	hits, err := a.ListDistributions(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListDistributions: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 distribution, got %d", len(hits))
	}
	h := hits[0]
	if h.RowID != rowid {
		t.Errorf("RowID = %d, want %d", h.RowID, rowid)
	}
	if h.Area != "Sub-Saharan Africa" {
		t.Errorf("Area = %q, want %q", h.Area, "Sub-Saharan Africa")
	}
	if h.Gazetteer.ID() != "TDWG" {
		t.Errorf("Gazetteer = %q, want %q", h.Gazetteer.ID(), "TDWG")
	}
	if h.Status.ID() != "NATIVE" {
		t.Errorf("Status = %q, want %q", h.Status.ID(), "NATIVE")
	}
	if h.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q, want actor", h.ModifiedBy)
	}
}

func TestAddDistributionRequiresTaxon(t *testing.T) {
	a, _ := distSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddDistribution(coldp.Distribution{Area: "somewhere"})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
}

func TestUpdateDistributionHappy(t *testing.T) {
	a, taxonID := distSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddDistribution(coldp.Distribution{
			TaxonID: taxonID,
			Area:    "Kenya",
			Status:  coldp.NewDistrStatus("NATIVE"),
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateDistribution(rowid, coldp.Distribution{
			TaxonID:   taxonID,
			Area:      "East Africa",
			AreaID:    "EAF",
			Gazetteer: coldp.NewGazetteerEnt("TDWG"),
			Status:    coldp.NewDistrStatus("ALIEN"),
			Remarks:   "revised",
		})
	})
	if err != nil {
		t.Fatalf("UpdateDistribution: %v", err)
	}
	got, err := a.GetDistribution(ctx, rowid)
	if err != nil {
		t.Fatalf("GetDistribution: %v", err)
	}
	if got.Area != "East Africa" {
		t.Errorf("Area = %q, want %q", got.Area, "East Africa")
	}
	if got.Status.ID() != "ALIEN" {
		t.Errorf("Status = %q, want %q", got.Status.ID(), "ALIEN")
	}
}

func TestUpdateDistributionNotFound(t *testing.T) {
	a, taxonID := distSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateDistribution(999999, coldp.Distribution{
			TaxonID: taxonID, Area: "phantom",
		})
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteDistribution(t *testing.T) {
	a, taxonID := distSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var doomedID, survivorID int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddDistribution(coldp.Distribution{
			TaxonID: taxonID, Area: "Kenya",
		})
		if err != nil {
			return err
		}
		doomedID = id
		id, err = tx.AddDistribution(coldp.Distribution{
			TaxonID: taxonID, Area: "Tanzania",
		})
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
		return tx.DeleteDistribution(doomedID)
	})
	if err != nil {
		t.Fatalf("DeleteDistribution: %v", err)
	}

	hits, err := a.ListDistributions(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListDistributions: %v", err)
	}
	if len(hits) != 1 || hits[0].RowID != survivorID {
		t.Errorf("expected only survivor %d remaining; got %+v", survivorID, hits)
	}
}

func TestDeleteDistributionNotFound(t *testing.T) {
	a, _ := distSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteDistribution(999999)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListDistributionsOrderedByGazetteerThenArea(t *testing.T) {
	a, taxonID := distSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		// Insert out of alphabetical order to prove SQL orders it.
		if _, err := tx.AddDistribution(coldp.Distribution{
			TaxonID: taxonID, Area: "Kenya", Gazetteer: coldp.NewGazetteerEnt("TDWG"),
		}); err != nil {
			return err
		}
		if _, err := tx.AddDistribution(coldp.Distribution{
			TaxonID: taxonID, Area: "Angola", Gazetteer: coldp.NewGazetteerEnt("TDWG"),
		}); err != nil {
			return err
		}
		if _, err := tx.AddDistribution(coldp.Distribution{
			TaxonID: taxonID, Area: "Botswana", Gazetteer: coldp.NewGazetteerEnt("ISO"),
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := a.ListDistributions(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListDistributions: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	// ISO sorts before TDWG; within TDWG, Angola before Kenya.
	wantOrder := []string{"Botswana", "Angola", "Kenya"}
	for i, want := range wantOrder {
		if hits[i].Area != want {
			t.Errorf("[%d] area = %q, want %q", i, hits[i].Area, want)
		}
	}
}

func TestGetDistributionNotFound(t *testing.T) {
	a, _ := distSetup(t)
	_, err := a.GetDistribution(context.Background(), 999999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
