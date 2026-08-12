package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestAncestorExistsValidator_WalksMultipleHops builds a small
// classification — Family → Genus → Species → Subspecies — then
// verifies that:
//   - The subspecies passes clb_parent_species_missing
//     (SPECIES ancestor found at hop 1)
//   - The species passes clb_parent_genus_missing
//     (GENUS ancestor found at hop 1)
//   - A separate species placed directly under the Family (no
//     intervening genus) fires clb_parent_genus_missing.
//
// The multi-hop walk is exactly the case the user called out:
// PARENT_GENUS_MISSING has to look through subgenera and infraspecific
// ranks, not just the immediate parent.
func TestAncestorExistsValidator_WalksMultipleHops(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ancestor.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	var (
		nFamilyID, nGenusID, nSpeciesID, nSubspeciesID, nOrphanSpeciesID string
		tFamilyID, tGenusID, tSpeciesID, tSubspeciesID, tOrphanSpeciesID string
	)

	if err := a.WithTx(ctx, func(tx *Tx) error {
		mk := func(sn, rankStr string) (string, error) {
			return tx.CreateName(coldp.Name{ScientificName: sn, Rank: ParseRank(rankStr)})
		}
		var err error
		if nFamilyID, err = mk("Felidae Fischer de Waldheim, 1817", "family"); err != nil {
			return err
		}
		if nGenusID, err = mk("Panthera Oken, 1816", "genus"); err != nil {
			return err
		}
		if nSpeciesID, err = mk("Panthera leo (Linnaeus, 1758)", "species"); err != nil {
			return err
		}
		if nSubspeciesID, err = mk("Panthera leo leo (Linnaeus, 1758)", "subspecies"); err != nil {
			return err
		}
		if nOrphanSpeciesID, err = mk("Foo bar Author, 2020", "species"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}

	// Stitch the classification: Family → Genus → Species → Subspecies.
	// Orphan species goes directly under Family (skipping the genus).
	if err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if tFamilyID, err = tx.CreateTaxon(coldp.Taxon{NameID: nFamilyID}); err != nil {
			return err
		}
		if tGenusID, err = tx.CreateTaxon(coldp.Taxon{NameID: nGenusID, ParentID: tFamilyID}); err != nil {
			return err
		}
		if tSpeciesID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSpeciesID, ParentID: tGenusID}); err != nil {
			return err
		}
		if tSubspeciesID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSubspeciesID, ParentID: tSpeciesID}); err != nil {
			return err
		}
		if tOrphanSpeciesID, err = tx.CreateTaxon(coldp.Taxon{NameID: nOrphanSpeciesID, ParentID: tFamilyID}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateTaxon: %v", err)
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	assertNoIssue := func(taxonID, ruleID string) {
		t.Helper()
		var count int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results WHERE table_name = 'taxon' AND record_id = ? AND rule_id = ?`,
			taxonID, ruleID,
		).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", ruleID, err)
		}
		if count > 0 {
			t.Errorf("expected %s to be clean of %s; got %d issues", taxonID, ruleID, count)
		}
	}
	assertHasIssue := func(taxonID, ruleID string) {
		t.Helper()
		var count int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results WHERE table_name = 'taxon' AND record_id = ? AND rule_id = ?`,
			taxonID, ruleID,
		).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", ruleID, err)
		}
		if count == 0 {
			t.Errorf("expected %s to fire %s; got zero", taxonID, ruleID)
		}
	}

	// Properly-placed species: genus is the immediate parent (1 hop).
	assertNoIssue(tSpeciesID, "clb_parent_genus_missing")
	// Subspecies has a species parent (1 hop) AND a genus grand-parent
	// (2 hops) — should pass both rules.
	assertNoIssue(tSubspeciesID, "clb_parent_species_missing")
	// Orphan species is placed directly under the family — no genus
	// anywhere in the parent chain, rule fires.
	assertHasIssue(tOrphanSpeciesID, "clb_parent_genus_missing")
}
