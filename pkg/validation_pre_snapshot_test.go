package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestPreMutationSnapshot_UpdateClearsOldSide covers the completeness
// gap in aggregate propagation: when B's canonical changes, records
// that used to match B's OLD canonical (e.g. A) must be re-synced to
// drop their now-stale duplicate flag. Without the pre-mutation
// snapshot, only B's NEW-side neighbors get re-synced, leaving A
// stuck with an obsolete issue until a full reindex runs.
func TestPreMutationSnapshot_UpdateClearsOldSide(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "presnap.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	var aID, bID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		aID = id
		id, err = tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		bID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}
	// Both records should carry the duplicate flag after propagation
	// through count_across (verified in the sibling propagation test).
	if !hasDuplicateNameIssue2(t, ctx, a, aID) {
		t.Fatalf("baseline: A should be flagged after B was created")
	}
	if !hasDuplicateNameIssue2(t, ctx, a, bID) {
		t.Fatalf("baseline: B should be flagged after B was created")
	}

	// Update B to have a distinct canonical. A now stands alone;
	// pre-snapshot propagation should re-sync A and clear its issue.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateName(coldp.Name{ID: bID, ScientificName: "Baz qux"})
	}); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}

	if hasDuplicateNameIssue2(t, ctx, a, aID) {
		t.Errorf("A should be un-flagged after B's canonical changed (old-side propagation gap)")
	}
	if hasDuplicateNameIssue2(t, ctx, a, bID) {
		t.Errorf("B should be un-flagged — its canonical is now unique")
	}
}

// TestPreMutationSnapshot_DeleteClearsOldSide covers the same gap
// but for DELETE: after B is deleted, A no longer has a duplicate
// neighbor and its issue must clear. The DELETE also prunes B's
// own __gsvalidator_results rows so no orphan records linger.
func TestPreMutationSnapshot_DeleteClearsOldSide(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "presnap_del.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	var aID, bID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		aID = id
		id, err = tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		bID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}
	if !hasDuplicateNameIssue2(t, ctx, a, aID) || !hasDuplicateNameIssue2(t, ctx, a, bID) {
		t.Fatalf("baseline: both A and B should be flagged")
	}

	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteName(bID)
	}); err != nil {
		t.Fatalf("DeleteName: %v", err)
	}

	if hasDuplicateNameIssue2(t, ctx, a, aID) {
		t.Errorf("A should be un-flagged after B was deleted")
	}
	// B's own issue rows should be pruned as part of the delete.
	var bIssueCount int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results WHERE table_name = 'name' AND record_id = ?`,
		bID,
	).Scan(&bIssueCount); err != nil {
		t.Fatalf("count B issues: %v", err)
	}
	if bIssueCount != 0 {
		t.Errorf("B should have zero issue rows after delete; got %d", bIssueCount)
	}
}

func hasDuplicateNameIssue2(t *testing.T, ctx context.Context, a *Archive, id string) bool {
	t.Helper()
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results
		 WHERE table_name = 'name' AND record_id = ? AND rule_id = 'hive_duplicate_name'`,
		id,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	return n > 0
}
