package core

import (
	"context"
	"errors"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

func TestReferenceCreateGetUpdateDelete(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	// Create.
	var newID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateReference(coldp.Reference{
			ID:       "ref-test-1",
			Author:   "Doe, J.",
			Title:    "A tiny paper",
			Issued:   "2025",
			Type:     coldp.NewReferenceType("ARTICLE_JOURNAL"),
			DOI:      "10.0000/testing.1",
			Citation: "Doe, J. (2025). A tiny paper. Test Journal 1: 1-10.",
		})
		if err != nil {
			return err
		}
		newID = id
		return nil
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}
	if newID != "ref-test-1" {
		t.Errorf("newID = %q, want preserved supplied id", newID)
	}

	// Get + audit stamps populated.
	got, err := a.GetReference(ctx, newID)
	if err != nil {
		t.Fatalf("GetReference: %v", err)
	}
	if got.Title != "A tiny paper" || got.Author != "Doe, J." {
		t.Errorf("read back mismatch: %+v", got)
	}
	if got.Modified == "" {
		t.Errorf("expected col__modified stamp on create")
	}
	if got.ModifiedBy != "0000-0000-0000-0000" {
		t.Errorf("ModifiedBy = %q", got.ModifiedBy)
	}

	// Update with correct If-Match.
	got.Title = "A tiny paper, revised"
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateReference(*got)
	})
	if err != nil {
		t.Fatalf("UpdateReference: %v", err)
	}
	after, _ := a.GetReference(ctx, newID)
	if after.Title != "A tiny paper, revised" {
		t.Errorf("revised title = %q", after.Title)
	}
	if after.Modified == got.Modified {
		t.Errorf("expected col__modified to bump on update")
	}

	// Update with stale If-Match → ErrConflict.
	stale := *after
	stale.Modified = got.Modified // pre-update timestamp
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateReference(stale)
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("stale update: want ErrConflict, got %v", err)
	}

	// Delete succeeds when no dependents.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteReference(newID)
	})
	if err != nil {
		t.Fatalf("DeleteReference: %v", err)
	}
	_, err = a.GetReference(ctx, newID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("post-delete get: want ErrNotFound, got %v", err)
	}
}

// TestReferenceDeleteRefusesDependents proves the FK-safe delete
// policy: any downstream row pointing at the reference blocks
// deletion until it's reassigned or removed.
func TestReferenceDeleteRefusesDependents(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	// Create a reference + a taxon that cites it via col__according_to_id
	// (one of the 10 FK columns in referenceCitationTables).
	var refID, taxonID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		refID, err = tx.CreateReference(coldp.Reference{
			ID:     "ref-cited",
			Author: "Doe",
			Title:  "Cited paper",
			Issued: "2025",
		})
		if err != nil {
			return err
		}
		nameID := insertTestName(t, tx, "Panthera onca")
		taxonID, err = tx.CreateTaxon(coldp.Taxon{
			NameID:        nameID,
			AccordingToID: refID,
		})
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_ = taxonID

	// Attempted delete should surface ErrConflict, message names the
	// blocking table so a curator knows where to look.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteReference(refID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("delete-cited: want ErrConflict, got %v", err)
	}
	if err != nil && !containsString(err.Error(), "taxon") {
		t.Errorf("error should name the blocking table (taxon), got: %v", err)
	}

	// Reference still exists.
	if _, err := a.GetReference(ctx, refID); err != nil {
		t.Errorf("reference should still be present after refused delete: %v", err)
	}
}

// containsString is stdlib-strings.Contains, avoiding the import for
// this one call.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
