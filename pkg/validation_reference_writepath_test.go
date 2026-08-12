package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestReferenceWritePathSync exercises the reference write path's
// dirty-tracking hook end-to-end. Create → immediate issue if bad
// ISSN. Update to fix the ISSN → issue clears without a reindex.
// Delete → issue row pruned as part of the mutation.
func TestReferenceWritePathSync(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ref_writepath.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// Create a reference with a bad ISSN. Post-commit sync should
	// fire the check_digit rule immediately.
	var refID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateReference(coldp.Reference{
			Citation: "Test reference",
			ISSN:     "2049-3603", // transposed digits → invalid check
		})
		if err != nil {
			return err
		}
		refID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateReference: %v", err)
	}
	if !hasReferenceISSNIssue(t, ctx, a, refID) {
		t.Fatalf("bad ISSN should fire immediately after create")
	}

	// Update to a valid ISSN. Post-commit sync should clear the issue
	// via the same syncIssuesLocal path (rule now passes).
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateReference(coldp.Reference{
			ID:       refID,
			Citation: "Test reference (fixed)",
			ISSN:     "2049-3630", // valid ISSN (Nature)
		})
	}); err != nil {
		t.Fatalf("UpdateReference: %v", err)
	}
	if hasReferenceISSNIssue(t, ctx, a, refID) {
		t.Errorf("valid ISSN should have cleared the issue after update")
	}

	// Break the ISSN again so we have something to prune on delete.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateReference(coldp.Reference{
			ID:       refID,
			Citation: "Test reference (broken again)",
			ISSN:     "2049-3603",
		})
	}); err != nil {
		t.Fatalf("UpdateReference (re-break): %v", err)
	}
	if !hasReferenceISSNIssue(t, ctx, a, refID) {
		t.Fatalf("bad ISSN should re-fire after update")
	}

	// Delete the reference. Its issue row should be pruned by the
	// deleted-record path in syncIssuesWithSnapshot.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteReference(refID)
	}); err != nil {
		t.Fatalf("DeleteReference: %v", err)
	}
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results WHERE table_name = 'reference' AND record_id = ?`,
		refID,
	).Scan(&n); err != nil {
		t.Fatalf("count post-delete: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 issue rows for deleted reference; got %d", n)
	}
}

func hasReferenceISSNIssue(t *testing.T, ctx context.Context, a *Archive, id string) bool {
	t.Helper()
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results
		 WHERE table_name = 'reference' AND record_id = ? AND rule_id = 'hive_reference_issn_check_digit'`,
		id,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	return n > 0
}
