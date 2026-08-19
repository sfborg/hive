package hive

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// tmSetup creates an archive with a name that type_material rows
// can attach to. Returns (archive, nameID).
func tmSetup(t *testing.T) (*Archive, string) {
	t.Helper()
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")
	var nameID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID = insertTestName(t, tx, "Panthera onca")
		return nil
	})
	if err != nil {
		t.Fatalf("tmSetup: %v", err)
	}
	return a, nameID
}

// TestAddTypeMaterialRoundTrip covers create → list → get with a
// fully-populated row: every scalar, every enum, and the nullable
// coordinate triple. Verifies nulls come back as invalid and set
// values (including zero) round-trip cleanly.
func TestAddTypeMaterialRoundTrip(t *testing.T) {
	a, nameID := tmSetup(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddTypeMaterial(coldp.TypeMaterial{
			ID:                  "USNM 12345",
			NameID:              nameID,
			Citation:            "Holotype: USNM 12345, adult male",
			Status:              coldp.NewTypeStatus("holotype"),
			InstitutionCode:     "USNM",
			CatalogNumber:       "12345",
			Locality:            "Brazil, Amazonas, Manaus",
			Country:             "BR",
			Latitude:            sql.NullFloat64{Float64: -3.1, Valid: true},
			Longitude:           sql.NullFloat64{Float64: -60.0, Valid: true},
			Altitude:            sql.NullInt64{Int64: 92, Valid: true},
			Host:                "",
			Sex:                 coldp.NewSex("male"),
			Date:                "1901-06-15",
			Collector:           "H. H. Smith",
			AssociatedSequences: "GenBank:AF123456",
			Link:                "https://collections.nmnh.si.edu/vz/12345",
			Remarks:             "Skin + skull",
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddTypeMaterial: %v", err)
	}
	if rowid <= 0 {
		t.Fatalf("expected a positive rowid, got %d", rowid)
	}

	hits, err := a.ListTypeMaterials(ctx, nameID)
	if err != nil {
		t.Fatalf("ListTypeMaterials: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 type_material, got %d", len(hits))
	}
	h := hits[0]
	if h.RowID != rowid {
		t.Errorf("RowID = %d, want %d", h.RowID, rowid)
	}
	if h.SpecimenID != "USNM 12345" {
		t.Errorf("SpecimenID = %q, want %q", h.SpecimenID, "USNM 12345")
	}
	if h.NameID != nameID {
		t.Errorf("NameID = %q, want %q", h.NameID, nameID)
	}
	if h.Status.ID() != "HOLOTYPE" {
		t.Errorf("Status = %q, want HOLOTYPE", h.Status.ID())
	}
	if h.Sex.ID() != "MALE" {
		t.Errorf("Sex = %q, want MALE", h.Sex.ID())
	}
	if !h.Latitude.Valid || h.Latitude.Float64 != -3.1 {
		t.Errorf("Latitude = %+v, want valid -3.1", h.Latitude)
	}
	if !h.Longitude.Valid || h.Longitude.Float64 != -60.0 {
		t.Errorf("Longitude = %+v, want valid -60.0", h.Longitude)
	}
	if !h.Altitude.Valid || h.Altitude.Int64 != 92 {
		t.Errorf("Altitude = %+v, want valid 92", h.Altitude)
	}
	if h.AssociatedSequences != "GenBank:AF123456" {
		t.Errorf("AssociatedSequences = %q", h.AssociatedSequences)
	}
	if h.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q", h.ModifiedBy)
	}
}

// TestAddTypeMaterialNullCoordinates verifies that omitting the
// coordinate triple stores NULL rather than 0, so reads distinguish
// "unset" from "0,0,0" — matters because 0/0 is a real coordinate
// (Gulf of Guinea) and shouldn't be indistinguishable from missing.
func TestAddTypeMaterialNullCoordinates(t *testing.T) {
	a, nameID := tmSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddTypeMaterial(coldp.TypeMaterial{
			NameID:   nameID,
			Citation: "no coordinates on record",
			Status:   coldp.NewTypeStatus("paratype"),
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddTypeMaterial: %v", err)
	}
	h, err := a.GetTypeMaterial(ctx, rowid)
	if err != nil {
		t.Fatalf("GetTypeMaterial: %v", err)
	}
	if h.Latitude.Valid {
		t.Errorf("Latitude should be NULL, got %v", h.Latitude.Float64)
	}
	if h.Longitude.Valid {
		t.Errorf("Longitude should be NULL, got %v", h.Longitude.Float64)
	}
	if h.Altitude.Valid {
		t.Errorf("Altitude should be NULL, got %v", h.Altitude.Int64)
	}
}

// TestUpdateAndDeleteTypeMaterial covers the mutation half of the
// contract. Update rewrites every editable column; delete removes
// the row; both surface ErrNotFound on unknown rowids so handlers
// can 404.
func TestUpdateAndDeleteTypeMaterial(t *testing.T) {
	a, nameID := tmSetup(t)
	ctx := WithActor(context.Background(), "tester")

	var rowid int64
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.AddTypeMaterial(coldp.TypeMaterial{
			NameID:   nameID,
			Citation: "initial",
			Status:   coldp.NewTypeStatus("holotype"),
		})
		rowid = id
		return err
	})
	if err != nil {
		t.Fatalf("AddTypeMaterial: %v", err)
	}

	// Update: change status, add locality, set coordinates.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateTypeMaterial(rowid, coldp.TypeMaterial{
			ID:              "MHNP 1900-42",
			NameID:          nameID, // ignored on update path but harmless
			Citation:        "updated citation",
			Status:          coldp.NewTypeStatus("lectotype"),
			InstitutionCode: "MHNP",
			CatalogNumber:   "1900-42",
			Locality:        "French Guiana",
			Country:         "GF",
			Latitude:        sql.NullFloat64{Float64: 4.0, Valid: true},
			Longitude:       sql.NullFloat64{Float64: -53.0, Valid: true},
		})
	})
	if err != nil {
		t.Fatalf("UpdateTypeMaterial: %v", err)
	}
	h, err := a.GetTypeMaterial(ctx, rowid)
	if err != nil {
		t.Fatalf("GetTypeMaterial post-update: %v", err)
	}
	if h.SpecimenID != "MHNP 1900-42" {
		t.Errorf("SpecimenID post-update = %q", h.SpecimenID)
	}
	if h.Status.ID() != "LECTOTYPE" {
		t.Errorf("Status post-update = %q, want LECTOTYPE", h.Status.ID())
	}
	if !h.Latitude.Valid || h.Latitude.Float64 != 4.0 {
		t.Errorf("Latitude post-update = %+v", h.Latitude)
	}

	// Unknown rowid on update → ErrNotFound.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateTypeMaterial(999999, coldp.TypeMaterial{NameID: nameID, Citation: "nope"})
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("update on missing rowid: got %v, want ErrNotFound", err)
	}

	// Delete: removes the row; second delete returns ErrNotFound.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteTypeMaterial(rowid)
	})
	if err != nil {
		t.Fatalf("DeleteTypeMaterial: %v", err)
	}
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteTypeMaterial(rowid)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("delete on missing rowid: got %v, want ErrNotFound", err)
	}

	// List should now be empty.
	hits, err := a.ListTypeMaterials(ctx, nameID)
	if err != nil {
		t.Fatalf("ListTypeMaterials post-delete: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 rows post-delete, got %d", len(hits))
	}
}

// TestAddTypeMaterialRequiresNameID covers the sole hard-required
// field. Empty NameID must return ErrValidation without touching
// the table.
func TestAddTypeMaterialRequiresNameID(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.AddTypeMaterial(coldp.TypeMaterial{Citation: "orphan"})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("missing NameID: got %v, want ErrValidation", err)
	}
}
