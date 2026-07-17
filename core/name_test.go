package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// createTestName is a helper for name tests that goes through the real
// CreateName code path (unlike insertTestName in taxon_test.go, which uses
// a bare INSERT to avoid gnparser during taxon-focused tests). Returns the
// generated ID.
func createTestName(t *testing.T, a *Archive, ctx context.Context, verbatim string) string {
	t.Helper()
	var id string
	err := a.WithTx(ctx, func(tx *Tx) error {
		created, err := tx.CreateName(coldp.Name{
			ScientificNameString: verbatim,
		})
		if err != nil {
			return err
		}
		id = created
		return nil
	})
	if err != nil {
		t.Fatalf("createTestName(%q): %v", verbatim, err)
	}
	return id
}

func TestCreateNameStampsGnFields(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	const verbatim = "Panthera leo (Linnaeus, 1758)"
	id := createTestName(t, a, ctx, verbatim)

	got, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName: %v", err)
	}

	// gnparser populates all gn__* fields on write. Even if the caller
	// passed empty values, hive overwrites them from the parse result.
	if !got.ParseQuality.Valid || got.ParseQuality.Int64 == 0 {
		t.Errorf("ParseQuality = %+v, want valid non-zero (parseable name)", got.ParseQuality)
	}
	if got.CanonicalSimple != "Panthera leo" {
		t.Errorf("CanonicalSimple = %q, want %q", got.CanonicalSimple, "Panthera leo")
	}
	if got.CanonicalFull != "Panthera leo" {
		t.Errorf("CanonicalFull = %q, want %q", got.CanonicalFull, "Panthera leo")
	}
	if !got.Cardinality.Valid || got.Cardinality.Int64 != 2 {
		t.Errorf("Cardinality = %+v, want 2 (binomial)", got.Cardinality)
	}
	if got.Authors == "" {
		t.Errorf("Authors is empty; expected a pipe-separated author list")
	}
	if got.GnID == "" {
		t.Errorf("GnID is empty; expected a UUID v5")
	}
	// GN's UUID v5 is deterministic — same input always parses to same UUID.
	if _, err := uuid.Parse(got.GnID); err != nil {
		t.Errorf("GnID %q is not a valid UUID: %v", got.GnID, err)
	}

	// Actor / modified stamped by CreateName.
	if got.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q, want the actor ORCID", got.ModifiedBy)
	}
	if got.Modified == "" {
		t.Error("Modified is empty; expected a timestamp")
	}
}

func TestCreateNameOverridesCallerGnFields(t *testing.T) {
	// gn__* is a cache — hive always regenerates it from the current
	// scientific name string, discarding whatever the caller passed. This
	// test proves that a caller trying to seed a wrong CanonicalSimple gets
	// their value overwritten.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var id string
	err := a.WithTx(ctx, func(tx *Tx) error {
		created, err := tx.CreateName(coldp.Name{
			ScientificNameString: "Homo sapiens Linnaeus, 1758",
			CanonicalSimple:      "wrong wrong wrong", // must be overwritten
			GnID:                 "not-a-uuid",        // must be overwritten
		})
		if err != nil {
			return err
		}
		id = created
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	got, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName: %v", err)
	}
	if got.CanonicalSimple != "Homo sapiens" {
		t.Errorf("CanonicalSimple = %q, want %q (parser-derived, not caller-supplied)",
			got.CanonicalSimple, "Homo sapiens")
	}
	if _, err := uuid.Parse(got.GnID); err != nil {
		t.Errorf("GnID = %q is not a valid UUID; caller-supplied bad value leaked through", got.GnID)
	}
}

func TestCreateNameUnparseableStillSaves(t *testing.T) {
	// Per CLAUDE.md § gn__* column policy: unparseable names still save;
	// gn__parse_quality = 0 is the signal, not an error.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	id := createTestName(t, a, ctx, "!!! not a scientific name at all !!!")

	got, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName: %v", err)
	}
	if !got.ParseQuality.Valid || got.ParseQuality.Int64 != 0 {
		t.Errorf("ParseQuality = %+v, want 0 (unparseable name)", got.ParseQuality)
	}
	// Verbatim string is preserved even when parsing fails.
	if !strings.Contains(got.ScientificNameString, "not a scientific name") {
		t.Errorf("ScientificNameString = %q, want to contain original input", got.ScientificNameString)
	}
}

func TestCreateNameRequiresScientificName(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.CreateName(coldp.Name{})
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateName with empty name: got %v, want ErrValidation", err)
	}
}

func TestCreateNameFallsBackFromScientificName(t *testing.T) {
	// A caller who only supplies ScientificName (canonical form, no
	// authorship) should still succeed; hive treats ScientificName as the
	// verbatim fallback when ScientificNameString is empty, and symmetrically
	// backfills ScientificNameString from ScientificName so both NOT NULL
	// columns are populated.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var id string
	err := a.WithTx(ctx, func(tx *Tx) error {
		created, err := tx.CreateName(coldp.Name{
			ScientificName: "Felis catus",
		})
		if err != nil {
			return err
		}
		id = created
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	got, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName: %v", err)
	}
	if got.ScientificNameString != "Felis catus" {
		t.Errorf("ScientificNameString = %q, want %q (backfilled from ScientificName)",
			got.ScientificNameString, "Felis catus")
	}
	if got.CanonicalSimple != "Felis catus" {
		t.Errorf("CanonicalSimple = %q, want %q", got.CanonicalSimple, "Felis catus")
	}
}

func TestGetNameNotFound(t *testing.T) {
	a := newTestArchive(t)
	_, err := a.GetName(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetName on missing id: got %v, want ErrNotFound", err)
	}
}

func TestSearchNames(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	createTestName(t, a, ctx, "Panthera leo (Linnaeus, 1758)")
	createTestName(t, a, ctx, "Panthera tigris (Linnaeus, 1758)")
	createTestName(t, a, ctx, "Felis catus Linnaeus, 1758")

	hits, err := a.SearchNames(ctx, "Panthera", 10)
	if err != nil {
		t.Fatalf("SearchNames: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("SearchNames(\"Panthera\") returned %d hits, want 2", len(hits))
	}
	// Alphabetical order by canonical simple: leo before tigris.
	if !strings.HasPrefix(hits[0].Scientific, "Panthera leo") {
		t.Errorf("hits[0].Scientific = %q, want to start with %q", hits[0].Scientific, "Panthera leo")
	}
	if !strings.HasPrefix(hits[1].Scientific, "Panthera tigris") {
		t.Errorf("hits[1].Scientific = %q, want to start with %q", hits[1].Scientific, "Panthera tigris")
	}

	// Case-insensitive: lowercase should still match.
	lowerHits, err := a.SearchNames(ctx, "felis", 10)
	if err != nil {
		t.Fatalf("SearchNames (lowercase): %v", err)
	}
	if len(lowerHits) != 1 {
		t.Fatalf("SearchNames(\"felis\") returned %d hits, want 1", len(lowerHits))
	}
}

func TestUpdateNameRefreshesGnCache(t *testing.T) {
	// UpdateName always re-parses. If the scientific name changes, the
	// gn__* cache follows suit — even if the caller supplied stale values.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	id := createTestName(t, a, ctx, "Panthera leo (Linnaeus, 1758)")
	before, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName: %v", err)
	}

	before.ScientificNameString = "Panthera onca (Linnaeus, 1758)"
	before.ScientificName = "Panthera onca"
	before.CanonicalSimple = "stale value that should be overwritten"
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateName(*before)
	})
	if err != nil {
		t.Fatalf("UpdateName: %v", err)
	}

	after, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName after: %v", err)
	}
	if after.CanonicalSimple != "Panthera onca" {
		t.Errorf("CanonicalSimple = %q, want %q (re-parsed from new verbatim)",
			after.CanonicalSimple, "Panthera onca")
	}
	if !after.ParseQuality.Valid || after.ParseQuality.Int64 == 0 {
		t.Errorf("ParseQuality = %+v, want valid non-zero", after.ParseQuality)
	}
}

func TestUpdateNameOptimisticConcurrency(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	id := createTestName(t, a, ctx, "Ursus arctos")
	original, err := a.GetName(ctx, id)
	if err != nil {
		t.Fatalf("GetName: %v", err)
	}

	// Concurrent update from a different actor.
	err = a.WithTx(WithActor(ctx, "other-actor"), func(tx *Tx) error {
		other, _ := a.GetName(ctx, id)
		other.Remarks = "concurrent"
		return tx.UpdateName(*other)
	})
	if err != nil {
		t.Fatalf("concurrent update: %v", err)
	}

	// Our stale-token update must be rejected.
	original.Remarks = "ours"
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateName(*original)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale-token UpdateName: got %v, want ErrConflict", err)
	}
}

func TestUpdateNameNotFound(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.UpdateName(coldp.Name{
			ID:                   "nope",
			ScientificNameString: "Foo bar",
		})
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateName missing id: got %v, want ErrNotFound", err)
	}
}

func TestDeleteNameLeafOK(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	id := createTestName(t, a, ctx, "Orphan name")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteName(id)
	})
	if err != nil {
		t.Fatalf("DeleteName: %v", err)
	}
	if _, err := a.GetName(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after DeleteName: got %v, want ErrNotFound", err)
	}
}

func TestDeleteNameReferencedByTaxonRejected(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	// Create a name AND a taxon that references it.
	var nameID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nid, err := tx.CreateName(coldp.Name{ScientificNameString: "Referenced name"})
		if err != nil {
			return err
		}
		nameID = nid
		_, err = tx.CreateTaxon(taxonForTest(nameID))
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteName(nameID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteName still referenced: got %v, want ErrConflict", err)
	}
}

func TestDeleteNameNotFound(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.DeleteName("does-not-exist")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteName missing: got %v, want ErrNotFound", err)
	}
}

func TestSearchNamesLimit(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")
	for i := range 5 {
		createTestName(t, a, ctx, "Panthera species-"+string(rune('a'+i)))
	}

	hits, err := a.SearchNames(ctx, "Panthera", 3)
	if err != nil {
		t.Fatalf("SearchNames: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("SearchNames with limit=3 returned %d hits, want 3", len(hits))
	}
}
