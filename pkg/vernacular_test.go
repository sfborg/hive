package hive

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// vernSetup creates an archive with a taxon that vernacular rows
// can attach to. Returns (archive, taxonID).
func vernSetup(t *testing.T) (*Archive, string) {
	t.Helper()
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")
	var taxonID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Canis lupus")
		id, err := tx.CreateTaxon(taxonForTest(nm))
		if err != nil {
			return err
		}
		taxonID = id
		return nil
	})
	if err != nil {
		t.Fatalf("vernSetup: %v", err)
	}
	return a, taxonID
}

// TestAddVernacularBasic covers the happy path: add a vernacular
// row, receive a rowid handle, retrieve it via ListVernaculars
// with every field intact.
func TestAddVernacularBasic(t *testing.T) {
	a, taxonID := vernSetup(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddVernacular(coldp.Vernacular{
			TaxonID:   taxonID,
			Name:      "gray wolf",
			Language:  "eng",
			Preferred: sql.NullBool{Bool: true, Valid: true},
			Country:   "US",
			Remarks:   "common in North America",
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddVernacular: %v", err)
	}
	if rowid <= 0 {
		t.Fatalf("expected a positive rowid, got %d", rowid)
	}

	hits, err := a.ListVernaculars(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListVernaculars: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 vernacular, got %d", len(hits))
	}
	h := hits[0]
	if h.RowID != rowid {
		t.Errorf("RowID = %d, want %d", h.RowID, rowid)
	}
	if h.Name != "gray wolf" {
		t.Errorf("Name = %q, want %q", h.Name, "gray wolf")
	}
	if h.Language != "eng" {
		t.Errorf("Language = %q, want %q", h.Language, "eng")
	}
	if !h.Preferred.Valid || !h.Preferred.Bool {
		t.Errorf("Preferred = %+v, want valid+true", h.Preferred)
	}
	if h.Country != "US" {
		t.Errorf("Country = %q, want %q", h.Country, "US")
	}
	if h.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q, want actor", h.ModifiedBy)
	}
	if h.Modified == "" {
		t.Error("Modified is empty; expected a timestamp")
	}
}

// TestAddVernacularRequiresTaxonAndName — empty taxon_id or name
// must fail validation before the row hits SQL.
func TestAddVernacularRequiresTaxonAndName(t *testing.T) {
	a, taxonID := vernSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddVernacular(coldp.Vernacular{TaxonID: taxonID})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("missing name: err = %v, want ErrValidation", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddVernacular(coldp.Vernacular{Name: "loup"})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("missing taxon_id: err = %v, want ErrValidation", err)
	}
}

// TestUpdateVernacularHappy covers a straight-through update:
// change language + preferred + area, everything round-trips.
func TestUpdateVernacularHappy(t *testing.T) {
	a, taxonID := vernSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddVernacular(coldp.Vernacular{
			TaxonID:  taxonID,
			Name:     "wolf",
			Language: "eng",
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateVernacular(rowid, coldp.Vernacular{
			TaxonID:   taxonID,
			Name:      "gray wolf",
			Language:  "eng",
			Preferred: sql.NullBool{Bool: true, Valid: true},
			Area:      "North America",
		})
	})
	if err != nil {
		t.Fatalf("UpdateVernacular: %v", err)
	}

	got, err := a.GetVernacular(ctx, rowid)
	if err != nil {
		t.Fatalf("GetVernacular: %v", err)
	}
	if got.Name != "gray wolf" {
		t.Errorf("Name = %q, want %q", got.Name, "gray wolf")
	}
	if !got.Preferred.Valid || !got.Preferred.Bool {
		t.Errorf("Preferred = %+v, want valid+true", got.Preferred)
	}
	if got.Area != "North America" {
		t.Errorf("Area = %q, want %q", got.Area, "North America")
	}
}

// TestUpdateVernacularNotFound — unknown rowid returns ErrNotFound
// so the handler surfaces a 404 rather than a silent no-op.
func TestUpdateVernacularNotFound(t *testing.T) {
	a, taxonID := vernSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateVernacular(999999, coldp.Vernacular{
			TaxonID: taxonID,
			Name:    "phantom",
		})
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestDeleteVernacular removes a specific row and confirms the
// list shrinks; sibling rows on the same taxon survive.
func TestDeleteVernacular(t *testing.T) {
	a, taxonID := vernSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var doomedID, survivorID int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddVernacular(coldp.Vernacular{
			TaxonID: taxonID, Name: "loup", Language: "fra",
		})
		if err != nil {
			return err
		}
		doomedID = id
		id, err = tx.AddVernacular(coldp.Vernacular{
			TaxonID: taxonID, Name: "wolf", Language: "eng",
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
		return tx.DeleteVernacular(doomedID)
	})
	if err != nil {
		t.Fatalf("DeleteVernacular: %v", err)
	}

	hits, err := a.ListVernaculars(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListVernaculars: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(hits))
	}
	if hits[0].RowID != survivorID {
		t.Errorf("survivor = %d, want %d", hits[0].RowID, survivorID)
	}
}

// TestDeleteVernacularNotFound — unknown rowid → ErrNotFound.
func TestDeleteVernacularNotFound(t *testing.T) {
	a, _ := vernSetup(t)
	ctx := WithActor(context.Background(), "tester")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteVernacular(999999)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListVernacularsPreferredFirst — preferred rows sort above
// non-preferred within a taxon; ties within preferred sort by
// language then name for a stable UX.
func TestListVernacularsPreferredFirst(t *testing.T) {
	a, taxonID := vernSetup(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.AddVernacular(coldp.Vernacular{
			TaxonID: taxonID, Name: "loup", Language: "fra",
		}); err != nil {
			return err
		}
		if _, err := tx.AddVernacular(coldp.Vernacular{
			TaxonID:   taxonID,
			Name:      "gray wolf",
			Language:  "eng",
			Preferred: sql.NullBool{Bool: true, Valid: true},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := a.ListVernaculars(ctx, taxonID)
	if err != nil {
		t.Fatalf("ListVernaculars: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2, got %d", len(hits))
	}
	if hits[0].Name != "gray wolf" {
		t.Errorf("first = %q, want the preferred row 'gray wolf'", hits[0].Name)
	}
}

// TestGetVernacularNotFound covers the address-by-rowid path.
func TestGetVernacularNotFound(t *testing.T) {
	a, _ := vernSetup(t)
	_, err := a.GetVernacular(context.Background(), 999999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
