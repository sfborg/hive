package hive

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// newTestArchive is a test helper that creates a fresh sfga archive in a
// temp dir and returns an *Archive already opened for read-write. The
// archive is closed automatically at test cleanup.
func newTestArchive(t *testing.T) *Archive {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// insertTestName inserts a minimal name row so taxa can reference it via
// col__name_id (NOT NULL REFERENCES name(col__id)). Returns the name's ID.
// Uses tx.tx directly since pkg/name.go doesn't exist yet.
func insertTestName(t *testing.T, tx *Tx, scientific string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := tx.tx.ExecContext(tx.ctx,
		`INSERT INTO name (col__id, gn__scientific_name_string, col__scientific_name)
		 VALUES (?, ?, ?)`,
		id, scientific, scientific,
	)
	if err != nil {
		t.Fatalf("insert test name: %v", err)
	}
	return id
}

func TestCreateAndGetTaxon(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var (
		taxonID string
		nameID  string
	)
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID = insertTestName(t, tx, "Panthera leo (Linnaeus, 1758)")
		id, err := tx.CreateTaxon(taxonForTest(nameID))
		if err != nil {
			return err
		}
		taxonID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	got, err := a.GetTaxon(ctx, taxonID)
	if err != nil {
		t.Fatalf("GetTaxon: %v", err)
	}
	if got.ID != taxonID {
		t.Errorf("ID = %q, want %q", got.ID, taxonID)
	}
	if got.NameID != nameID {
		t.Errorf("NameID = %q, want %q", got.NameID, nameID)
	}
	if got.Remarks != "test taxon" {
		t.Errorf("Remarks = %q, want %q", got.Remarks, "test taxon")
	}
	if got.ModifiedBy != "0000-0002-1825-0097" {
		t.Errorf("ModifiedBy = %q, want the actor ORCID", got.ModifiedBy)
	}
	if got.Modified == "" {
		t.Error("Modified is empty; expected a timestamp")
	}
	// Provisional=false input round-trips to Provisional=false output
	// (status stored as ACCEPTED).
	if got.Provisional.Valid && got.Provisional.Bool {
		t.Errorf("Provisional = true, want false")
	}
}

func TestCreateTaxonGeneratesUUIDWhenIDEmpty(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var generatedID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID := insertTestName(t, tx, "Test name")
		taxon := taxonForTest(nameID)
		taxon.ID = "" // explicitly ask for UUID generation
		id, err := tx.CreateTaxon(taxon)
		if err != nil {
			return err
		}
		generatedID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	if _, err := uuid.Parse(generatedID); err != nil {
		t.Fatalf("generated ID %q is not a UUID: %v", generatedID, err)
	}
}

func TestCreateTaxonPreservesGivenID(t *testing.T) {
	// COLDP imports may bring numeric-looking or slug-like IDs; hive must
	// preserve them verbatim. Test with a numeric-looking string that would
	// be a footgun if hive tried to coerce it.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	const importedID = "12345"
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID := insertTestName(t, tx, "Test name")
		taxon := taxonForTest(nameID)
		taxon.ID = importedID
		id, err := tx.CreateTaxon(taxon)
		if err != nil {
			return err
		}
		if id != importedID {
			t.Fatalf("CreateTaxon returned %q, want %q", id, importedID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	got, err := a.GetTaxon(ctx, importedID)
	if err != nil {
		t.Fatalf("GetTaxon: %v", err)
	}
	if got.ID != importedID {
		t.Fatalf("round-tripped ID = %q, want %q (verbatim)", got.ID, importedID)
	}
}

func TestGetTaxonNotFound(t *testing.T) {
	a := newTestArchive(t)
	_, err := a.GetTaxon(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTaxon on missing id: got %v, want ErrNotFound", err)
	}
}

func TestListChildren(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var parentID, childA, childB string
	err := a.WithTx(ctx, func(tx *Tx) error {
		parentName := insertTestName(t, tx, "Felidae Fischer de Waldheim, 1817")
		p, err := tx.CreateTaxon(taxonForTest(parentName))
		if err != nil {
			return err
		}
		parentID = p

		aName := insertTestName(t, tx, "Panthera Oken, 1816")
		child := taxonForTest(aName)
		child.ParentID = parentID
		ca, err := tx.CreateTaxon(child)
		if err != nil {
			return err
		}
		childA = ca

		bName := insertTestName(t, tx, "Felis Linnaeus, 1758")
		child.ParentID = parentID
		child.ID = ""
		child.NameID = bName
		cb, err := tx.CreateTaxon(child)
		if err != nil {
			return err
		}
		childB = cb
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.ListChildren(context.Background(), parentID)
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("ListChildren returned %d hits, want 2", len(hits))
	}
	// Deterministic order (col__ordinal NULL for both, so ordering falls to
	// display_name alphabetically): Felis before Panthera.
	if hits[0].ID != childB {
		t.Errorf("hits[0].ID = %q, want Felis child %q", hits[0].ID, childB)
	}
	if hits[1].ID != childA {
		t.Errorf("hits[1].ID = %q, want Panthera child %q", hits[1].ID, childA)
	}
	for _, h := range hits {
		if h.HasChildren {
			t.Errorf("child %q reports HasChildren=true, want false", h.ID)
		}
		if h.ParentID != parentID {
			t.Errorf("child %q ParentID = %q, want %q", h.ID, h.ParentID, parentID)
		}
	}
}

func TestListChildrenHasChildrenTrue(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var grandparent, parent string
	err := a.WithTx(ctx, func(tx *Tx) error {
		gpName := insertTestName(t, tx, "Carnivora Bowdich, 1821")
		gp, err := tx.CreateTaxon(taxonForTest(gpName))
		if err != nil {
			return err
		}
		grandparent = gp

		pName := insertTestName(t, tx, "Felidae")
		pTaxon := taxonForTest(pName)
		pTaxon.ParentID = grandparent
		p, err := tx.CreateTaxon(pTaxon)
		if err != nil {
			return err
		}
		parent = p

		cName := insertTestName(t, tx, "Panthera")
		cTaxon := taxonForTest(cName)
		cTaxon.ParentID = parent
		_, err = tx.CreateTaxon(cTaxon)
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.ListChildren(context.Background(), grandparent)
	if err != nil {
		t.Fatalf("ListChildren(grandparent): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 child of grandparent, got %d", len(hits))
	}
	if !hits[0].HasChildren {
		t.Errorf("hits[0].HasChildren = false, want true (parent has a child)")
	}
	if hits[0].ID != parent {
		t.Errorf("hits[0].ID = %q, want parent %q", hits[0].ID, parent)
	}
}

func TestListChildrenRoots(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var rootID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		name := insertTestName(t, tx, "Animalia")
		root, err := tx.CreateTaxon(taxonForTest(name))
		if err != nil {
			return err
		}
		rootID = root
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.ListChildren(context.Background(), "")
	if err != nil {
		t.Fatalf("ListChildren(root): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 root taxon, got %d", len(hits))
	}
	if hits[0].ID != rootID {
		t.Errorf("root hit ID = %q, want %q", hits[0].ID, rootID)
	}
	if hits[0].ParentID != "" {
		t.Errorf("root hit ParentID = %q, want empty", hits[0].ParentID)
	}
}

// taxonForTest returns a minimal coldp.Taxon suitable for use in Create tests.
// The nameID must reference an existing name row.
func taxonForTest(nameID string) coldp.Taxon {
	return coldp.Taxon{
		NameID:      nameID,
		Provisional: sql.NullBool{Bool: false, Valid: true},
		Remarks:     "test taxon",
	}
}

func TestUpdateTaxonEditableFields(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0002-1825-0097")

	var taxonID, nameID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID = insertTestName(t, tx, "Ursus arctos")
		id, err := tx.CreateTaxon(taxonForTest(nameID))
		if err != nil {
			return err
		}
		taxonID = id
		return nil
	})
	if err != nil {
		t.Fatalf("setup WithTx: %v", err)
	}

	// Mutate a couple of editable fields via UpdateTaxon.
	before, err := a.GetTaxon(ctx, taxonID)
	if err != nil {
		t.Fatalf("GetTaxon before: %v", err)
	}
	before.Remarks = "updated remarks"
	before.Link = "https://example.org/ursus"

	err = a.WithTx(WithActor(ctx, "0000-0003-2222-1111"), func(tx *Tx) error {
		return tx.UpdateTaxon(*before)
	})
	if err != nil {
		t.Fatalf("UpdateTaxon: %v", err)
	}

	after, err := a.GetTaxon(ctx, taxonID)
	if err != nil {
		t.Fatalf("GetTaxon after: %v", err)
	}
	if after.Remarks != "updated remarks" {
		t.Errorf("Remarks = %q, want %q", after.Remarks, "updated remarks")
	}
	if after.Link != "https://example.org/ursus" {
		t.Errorf("Link = %q, want the updated URL", after.Link)
	}
	if after.ModifiedBy != "0000-0003-2222-1111" {
		t.Errorf("ModifiedBy = %q, want the new actor", after.ModifiedBy)
	}
	if after.Modified == before.Modified {
		t.Errorf("Modified did not change on update (was %q)", after.Modified)
	}
}

func TestUpdateTaxonIgnoresParentID(t *testing.T) {
	// coldp.Taxon carries ParentID but UpdateTaxon must never move via it —
	// reparenting is MoveTaxon's job. Silent ignore is the documented policy.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var childID, originalParent, otherParent string
	err := a.WithTx(ctx, func(tx *Tx) error {
		originalParentName := insertTestName(t, tx, "Felidae")
		p, err := tx.CreateTaxon(taxonForTest(originalParentName))
		if err != nil {
			return err
		}
		originalParent = p

		otherParentName := insertTestName(t, tx, "Canidae")
		o, err := tx.CreateTaxon(taxonForTest(otherParentName))
		if err != nil {
			return err
		}
		otherParent = o

		childName := insertTestName(t, tx, "Panthera")
		child := taxonForTest(childName)
		child.ParentID = originalParent
		c, err := tx.CreateTaxon(child)
		if err != nil {
			return err
		}
		childID = c
		return nil
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Now try to sneak a ParentID change through UpdateTaxon. It must NOT
	// take effect — the child must still be under originalParent.
	err = a.WithTx(ctx, func(tx *Tx) error {
		child, err := a.GetTaxon(ctx, childID)
		if err != nil {
			return err
		}
		child.ParentID = otherParent // deliberately wrong path
		return tx.UpdateTaxon(*child)
	})
	if err != nil {
		t.Fatalf("UpdateTaxon: %v", err)
	}

	got, err := a.GetTaxon(ctx, childID)
	if err != nil {
		t.Fatalf("GetTaxon: %v", err)
	}
	if got.ParentID != originalParent {
		t.Fatalf("ParentID = %q; want %q (UpdateTaxon must not reparent)",
			got.ParentID, originalParent)
	}
}

func TestUpdateTaxonOptimisticConcurrency(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var taxonID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID := insertTestName(t, tx, "Lynx lynx")
		id, err := tx.CreateTaxon(taxonForTest(nameID))
		if err != nil {
			return err
		}
		taxonID = id
		return nil
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := a.GetTaxon(ctx, taxonID)
	if err != nil {
		t.Fatalf("GetTaxon: %v", err)
	}

	// A second actor updates the taxon between our read and our write.
	err = a.WithTx(WithActor(ctx, "other-actor"), func(tx *Tx) error {
		other, _ := a.GetTaxon(ctx, taxonID)
		other.Remarks = "changed by other"
		return tx.UpdateTaxon(*other)
	})
	if err != nil {
		t.Fatalf("concurrent update: %v", err)
	}

	// Now our update, with the stale Modified token, must fail with ErrConflict.
	got.Remarks = "our update"
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateTaxon(*got)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateTaxon with stale token: got %v, want ErrConflict", err)
	}

	// Blind update (no token) still works.
	got.Modified = ""
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateTaxon(*got)
	})
	if err != nil {
		t.Fatalf("UpdateTaxon with empty token: %v", err)
	}
}

func TestUpdateTaxonNotFound(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.UpdateTaxon(coldp.Taxon{ID: "no-such-taxon"})
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateTaxon on missing id: got %v, want ErrNotFound", err)
	}
}

func TestMoveTaxonHappyPath(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var childID, oldParent, newParent string
	err := a.WithTx(ctx, func(tx *Tx) error {
		oldParent, _ = tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Felidae")))
		newParent, _ = tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Ursidae")))
		child := taxonForTest(insertTestName(t, tx, "Ursus"))
		child.ParentID = oldParent
		id, err := tx.CreateTaxon(child)
		if err != nil {
			return err
		}
		childID = id
		return nil
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.MoveTaxon(childID, newParent)
	})
	if err != nil {
		t.Fatalf("MoveTaxon: %v", err)
	}

	got, err := a.GetTaxon(ctx, childID)
	if err != nil {
		t.Fatalf("GetTaxon: %v", err)
	}
	if got.ParentID != newParent {
		t.Fatalf("ParentID = %q, want %q", got.ParentID, newParent)
	}
}

func TestMoveTaxonToRoot(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var childID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		parent, _ := tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Felidae")))
		child := taxonForTest(insertTestName(t, tx, "Panthera"))
		child.ParentID = parent
		id, err := tx.CreateTaxon(child)
		childID = id
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.MoveTaxon(childID, "")
	})
	if err != nil {
		t.Fatalf("MoveTaxon to root: %v", err)
	}

	got, err := a.GetTaxon(ctx, childID)
	if err != nil {
		t.Fatalf("GetTaxon: %v", err)
	}
	if got.ParentID != "" {
		t.Fatalf("ParentID = %q, want empty (root)", got.ParentID)
	}
}

func TestMoveTaxonSelfIsError(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")
	var id string
	_ = a.WithTx(ctx, func(tx *Tx) error {
		id, _ = tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "N")))
		return nil
	})

	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.MoveTaxon(id, id)
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("MoveTaxon to self: got %v, want ErrValidation", err)
	}
}

func TestMoveTaxonCycleRejected(t *testing.T) {
	// Tree: root -> child -> grandchild. Try moving root under grandchild.
	// That would create a cycle; MoveTaxon must reject.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var rootID, grandchildID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		root, _ := tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Kingdom")))
		rootID = root

		mid := taxonForTest(insertTestName(t, tx, "Phylum"))
		mid.ParentID = root
		midID, _ := tx.CreateTaxon(mid)

		gc := taxonForTest(insertTestName(t, tx, "Class"))
		gc.ParentID = midID
		gcID, _ := tx.CreateTaxon(gc)
		grandchildID = gcID
		return nil
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.MoveTaxon(rootID, grandchildID)
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("MoveTaxon creating cycle: got %v, want ErrValidation", err)
	}
}

func TestMoveTaxonNotFound(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.MoveTaxon("nonexistent", "")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("MoveTaxon on missing id: got %v, want ErrNotFound", err)
	}
}

func TestDeleteTaxonLeafOK(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var id string
	err := a.WithTx(ctx, func(tx *Tx) error {
		created, err := tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Test")))
		id = created
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteTaxon(id)
	})
	if err != nil {
		t.Fatalf("DeleteTaxon leaf: %v", err)
	}
	if _, err := a.GetTaxon(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: got %v, want ErrNotFound", err)
	}
}

func TestDeleteTaxonWithChildrenRejected(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var parentID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		p, _ := tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Parent")))
		parentID = p
		child := taxonForTest(insertTestName(t, tx, "Child"))
		child.ParentID = parentID
		_, err := tx.CreateTaxon(child)
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteTaxon(parentID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteTaxon on parent with children: got %v, want ErrConflict", err)
	}
}

func TestDeleteTaxonCascadesDependents(t *testing.T) {
	// Verify that deleting a taxon removes rows in dependent tables so the
	// underlying DELETE doesn't fail on FK constraints, and that stale rows
	// don't remain in synonym / vernacular / etc.
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var taxonID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID := insertTestName(t, tx, "Taxon to delete")
		id, err := tx.CreateTaxon(taxonForTest(nameID))
		if err != nil {
			return err
		}
		taxonID = id
		// Insert a vernacular row so DeleteTaxon has something to cascade.
		// vernacular has FKs to source and reference; the schema declares
		// them with DEFAULT '' but with PRAGMA foreign_keys=ON that fails
		// unless '' matches a seeded row. Explicit NULLs here — a future
		// pkg/vernacular.go will apply the same "" -> NULL pattern from
		// taxon.go / name.go.
		_, err = tx.tx.ExecContext(tx.ctx,
			`INSERT INTO vernacular
				(col__taxon_id, col__name, col__language, col__source_id, col__reference_id)
			 VALUES (?, ?, ?, NULL, NULL)`,
			taxonID, "common name", "en",
		)
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteTaxon(taxonID)
	})
	if err != nil {
		t.Fatalf("DeleteTaxon with dependents: %v", err)
	}

	var vernCount int
	if err := a.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM vernacular WHERE col__taxon_id = ?", taxonID,
	).Scan(&vernCount); err != nil {
		t.Fatalf("count vernaculars: %v", err)
	}
	if vernCount != 0 {
		t.Errorf("vernacular rows remained after delete: %d", vernCount)
	}
}

func TestDeleteTaxonNotFound(t *testing.T) {
	a := newTestArchive(t)
	err := a.WithTx(context.Background(), func(tx *Tx) error {
		return tx.DeleteTaxon("does-not-exist")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteTaxon missing id: got %v, want ErrNotFound", err)
	}
}
