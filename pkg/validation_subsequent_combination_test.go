package hive

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestNameRelationSharedReferenceRules covers the three
// same-shape rules that fire when a name reuses its predecessor's
// nomenclatural reference — one per relevant NomRelType. Each
// case walks the matrix:
//
//   - Same reference on both names → the rule fires on the derived
//     name (subject of the name_relation row).
//   - Distinct references → the rule stays silent.
//   - Empty reference on the derived name → the rule stays silent
//     (skip_if_either_empty).
//
// The predecessor row is on the receiving side of the relation and
// does not fire.
func TestNameRelationSharedReferenceRules(t *testing.T) {
	cases := []struct {
		name             string
		relType          coldp.NomRelType
		ruleID           string
		derivedSciName   string
		predecessorName  string
	}{
		{
			name:            "basionym",
			relType:         coldp.Basionym,
			ruleID:          "hive_subsequent_combination_shares_reference",
			derivedSciName:  "Panthera onca",
			predecessorName: "Felis onca",
		},
		{
			name:            "replacement",
			relType:         coldp.ReplacementName,
			ruleID:          "hive_replacement_name_shares_reference",
			derivedSciName:  "Aus novum Author, 1900",
			predecessorName: "Aus preoccupatum Author, 1850",
		},
		{
			name:            "spelling_correction",
			relType:         coldp.SpellingCorrection,
			ruleID:          "hive_spelling_correction_shares_reference",
			derivedSciName:  "Aus corrigendum Author, 1900",
			predecessorName: "Aus corigendum Author, 1850",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", tc.name))

			a, err := Create(path)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			defer a.Close()

			var refA, refB, predID, derivedID string
			if err := a.WithTx(ctx, func(tx *Tx) error {
				if refA, err = tx.CreateReference(coldp.Reference{Citation: "Ref A"}); err != nil {
					return err
				}
				if refB, err = tx.CreateReference(coldp.Reference{Citation: "Ref B"}); err != nil {
					return err
				}
				if predID, err = tx.CreateName(coldp.Name{
					ScientificName: tc.predecessorName,
					ReferenceID:    refA,
				}); err != nil {
					return err
				}
				if derivedID, err = tx.CreateName(coldp.Name{
					ScientificName: tc.derivedSciName,
					ReferenceID:    refA, // wrong on purpose — reuses predecessor's ref
				}); err != nil {
					return err
				}
				return tx.LinkNameRelation(coldp.NameRelation{
					NameID:        derivedID,
					RelatedNameID: predID,
					Type:          tc.relType,
				})
			}); err != nil {
				t.Fatalf("setup: %v", err)
			}

			// Full-archive reindex — the name write path doesn't
			// currently re-sync when a relation edge is added later.
			if err := a.ReindexValidation(ctx, nil); err != nil {
				t.Fatalf("ReindexValidation: %v", err)
			}

			if !hasNameRuleIssue(t, ctx, a, derivedID, tc.ruleID) {
				t.Fatalf("derived name sharing the predecessor's reference should fire %s", tc.ruleID)
			}
			if hasNameRuleIssue(t, ctx, a, predID, tc.ruleID) {
				t.Errorf("predecessor name should not carry %s", tc.ruleID)
			}

			// Point the derived name at a distinct reference — the rule
			// should now pass and the issue clear.
			if err := a.WithTx(ctx, func(tx *Tx) error {
				return tx.UpdateName(coldp.Name{
					ID:             derivedID,
					ScientificName: tc.derivedSciName,
					ReferenceID:    refB,
				})
			}); err != nil {
				t.Fatalf("UpdateName (distinct ref): %v", err)
			}
			if err := a.ReindexValidation(ctx, nil); err != nil {
				t.Fatalf("ReindexValidation post-fix: %v", err)
			}
			if hasNameRuleIssue(t, ctx, a, derivedID, tc.ruleID) {
				t.Errorf("distinct references should have cleared %s", tc.ruleID)
			}

			// Clear the derived name's reference — skip_if_either_empty
			// should keep the rule silent.
			if err := a.WithTx(ctx, func(tx *Tx) error {
				return tx.UpdateName(coldp.Name{
					ID:             derivedID,
					ScientificName: tc.derivedSciName,
					ReferenceID:    "",
				})
			}); err != nil {
				t.Fatalf("UpdateName (empty ref): %v", err)
			}
			if err := a.ReindexValidation(ctx, nil); err != nil {
				t.Fatalf("ReindexValidation post-empty: %v", err)
			}
			if hasNameRuleIssue(t, ctx, a, derivedID, tc.ruleID) {
				t.Errorf("empty reference on the derived name should skip %s", tc.ruleID)
			}
		})
	}
}

func hasNameRuleIssue(t *testing.T, ctx context.Context, a *Archive, nameID, ruleID string) bool {
	t.Helper()
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results
		 WHERE table_name = 'name' AND record_id = ? AND rule_id = ?`,
		nameID, ruleID,
	).Scan(&n); err != nil {
		t.Fatalf("query issue count: %v", err)
	}
	return n > 0
}
