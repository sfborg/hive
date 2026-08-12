package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestSyncIssuesPropagatesToAggregateNeighbors verifies that after
// writing record B, aggregate-shaped rules also re-sync record A
// (which now has B as a duplicate neighbor). Regression coverage for
// the "one Pardosa moesta flagged, the other not" bug fixed by the
// NeighborhoodProvider plumbing.
func TestSyncIssuesPropagatesToAggregateNeighbors(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "propagation.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	var aID, bID string

	// First name — no duplicate exists yet, so the rule shouldn't
	// flag it after this write.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		aID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateName A: %v", err)
	}
	if hasDup, _ := hasDuplicateNameIssue(ctx, a, aID); hasDup {
		t.Fatalf("record A flagged too early (before any duplicate existed)")
	}

	// Second name with the same canonical form. Post-commit sync
	// runs on B; propagation should also re-sync A.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		bID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateName B: %v", err)
	}

	// Both records should now carry the duplicate-name issue — B
	// via its own direct sync, A via propagation.
	if hasDup, err := hasDuplicateNameIssue(ctx, a, bID); err != nil {
		t.Fatalf("check B: %v", err)
	} else if !hasDup {
		t.Errorf("record B not flagged after duplicate creation")
	}
	if hasDup, err := hasDuplicateNameIssue(ctx, a, aID); err != nil {
		t.Fatalf("check A: %v", err)
	} else if !hasDup {
		t.Errorf("record A not flagged — propagation did not run")
	}
}

func hasDuplicateNameIssue(ctx context.Context, a *Archive, nameID string) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results
		 WHERE table_name = ? AND record_id = ? AND rule_id = ?`,
		"name", nameID, "clb_duplicate_name",
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
