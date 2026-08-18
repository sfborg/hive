package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestSubsequentCombinationSharedReferenceRule creates an original
// combination and a subsequent combination, links them via a
// BASIONYM name_relation, and covers the three states of
// hive_subsequent_combination_shares_reference:
//
//   - Same reference on both names → rule fires on the subsequent.
//   - Distinct references → rule stays silent.
//   - Empty reference on either side → rule stays silent
//     (skip_if_either_empty).
//
// The rule attaches to the subsequent-combination row (the one that
// carries the BASIONYM name_relation with itself as col__name_id);
// the original combination is on the receiving side and does not
// fire.
func TestSubsequentCombinationSharedReferenceRule(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "subseq_combo.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	var refA, refB, origID, subseqID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		if refA, err = tx.CreateReference(coldp.Reference{Citation: "Ref A (original description)"}); err != nil {
			return err
		}
		if refB, err = tx.CreateReference(coldp.Reference{Citation: "Ref B (recombination act)"}); err != nil {
			return err
		}
		if origID, err = tx.CreateName(coldp.Name{
			ScientificName: "Felis onca",
			ReferenceID:    refA,
		}); err != nil {
			return err
		}
		if subseqID, err = tx.CreateName(coldp.Name{
			ScientificName: "Panthera onca",
			ReferenceID:    refA, // wrong on purpose — reuses the basionym's ref
		}); err != nil {
			return err
		}
		return tx.LinkNameRelation(coldp.NameRelation{
			NameID:        subseqID,
			RelatedNameID: origID,
			Type:          coldp.Basionym,
		})
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Full-archive reindex so the rule evaluates now that the
	// name_relation is in place. The name write path doesn't
	// currently re-sync when a relation edge is added later.
	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	if !hasSubseqRefIssue(t, ctx, a, subseqID) {
		t.Fatalf("subsequent combination sharing the basionym's reference should fire the rule")
	}
	if hasSubseqRefIssue(t, ctx, a, origID) {
		t.Errorf("original combination should not carry the subsequent-combination rule")
	}

	// Point the subsequent combination at the recombination
	// reference — the rule should now pass and the issue clear.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateName(coldp.Name{
			ID:             subseqID,
			ScientificName: "Panthera onca",
			ReferenceID:    refB,
		})
	}); err != nil {
		t.Fatalf("UpdateName (distinct ref): %v", err)
	}
	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation post-fix: %v", err)
	}
	if hasSubseqRefIssue(t, ctx, a, subseqID) {
		t.Errorf("distinct references should have cleared the issue")
	}

	// Clear the subsequent combination's reference entirely — the
	// skip_if_either_empty parameter should keep the rule silent.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateName(coldp.Name{
			ID:             subseqID,
			ScientificName: "Panthera onca",
			ReferenceID:    "",
		})
	}); err != nil {
		t.Fatalf("UpdateName (empty ref): %v", err)
	}
	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation post-empty: %v", err)
	}
	if hasSubseqRefIssue(t, ctx, a, subseqID) {
		t.Errorf("empty reference on the subsequent combination should skip the rule")
	}
}

func hasSubseqRefIssue(t *testing.T, ctx context.Context, a *Archive, nameID string) bool {
	t.Helper()
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results
		 WHERE table_name = 'name' AND record_id = ?
		 AND rule_id = 'hive_subsequent_combination_shares_reference'`,
		nameID,
	).Scan(&n); err != nil {
		t.Fatalf("query issue count: %v", err)
	}
	return n > 0
}
