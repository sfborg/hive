package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestNoSelfCycleValidator_DetectsSyntheticCycle installs a 3-taxon
// cycle A → B → C → A by bypassing MoveTaxon's cycle guard (direct
// SQL update). ReindexValidation should then flag all three taxa
// under hive_taxon_parent_cycle.
func TestNoSelfCycleValidator_DetectsSyntheticCycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cycle.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// Three names for three taxa.
	var nameIDs [3]string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		for i, n := range []string{"Foo a", "Foo b", "Foo c"} {
			id, err := tx.CreateName(coldp.Name{ScientificName: n})
			if err != nil {
				return err
			}
			nameIDs[i] = id
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}

	// Three taxa, no parent yet — the cycle is stitched in below.
	var taxonIDs [3]string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		for i := range taxonIDs {
			id, err := tx.CreateTaxon(coldp.Taxon{NameID: nameIDs[i]})
			if err != nil {
				return err
			}
			taxonIDs[i] = id
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateTaxon: %v", err)
	}

	// Stitch A → B → C → A by direct SQL. Bypasses MoveTaxon's
	// cycle guard on purpose — the whole point of this validator
	// is to catch cycles that slipped in outside the normal write
	// path (bad import, migration bug, hand-edit).
	for i, id := range taxonIDs {
		parentID := taxonIDs[(i+1)%3]
		if _, err := a.db.ExecContext(ctx,
			`UPDATE taxon SET col__parent_id = ? WHERE col__id = ?`,
			parentID, id,
		); err != nil {
			t.Fatalf("stitch cycle: %v", err)
		}
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	for i, id := range taxonIDs {
		var count int
		err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = ? AND record_id = ? AND rule_id = ?`,
			"taxon", id, "hive_taxon_parent_cycle",
		).Scan(&count)
		if err != nil {
			t.Fatalf("query issues for taxon %d: %v", i, err)
		}
		if count == 0 {
			t.Errorf("taxon %d (%s) was on a cycle but no issue fired", i, id)
		}
	}
}
