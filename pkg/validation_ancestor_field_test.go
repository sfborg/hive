package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestAncestorFieldCheck_PublishedBeforeGenus exercises the
// ancestor_field_check plumbing end-to-end.
//
// Layout:
//
//	Family (no year)
//	├── Genus (published 1900)
//	│   ├── Species-OK  (published 1950)  — later than genus, passes
//	│   ├── Species-BAD (published 1850)  — earlier than genus, fires
//	│   └── Species-NoYear (empty year)   — empty year, silently skipped
//	└── Species-Loose (published 1800, no genus ancestor) — no matching
//	    ancestor, rule not applicable → passes
func TestAncestorFieldCheck_PublishedBeforeGenus(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "afc.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	mkName := func(tx *Tx, sn, rankStr, year string) (string, error) {
		return tx.CreateName(coldp.Name{
			ScientificName:    sn,
			Rank:              ParseRank(rankStr),
			PublishedInYear:   year,
		})
	}

	var nFamID, nGenID, nSpeciesOKID, nSpeciesBADID, nSpeciesEmptyID, nSpeciesLooseID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if nFamID, err = mkName(tx, "Felidae Fischer de Waldheim, 1817", "family", ""); err != nil {
			return err
		}
		if nGenID, err = mkName(tx, "Panthera Oken, 1900", "genus", "1900"); err != nil {
			return err
		}
		if nSpeciesOKID, err = mkName(tx, "Panthera later (Author, 1950)", "species", "1950"); err != nil {
			return err
		}
		if nSpeciesBADID, err = mkName(tx, "Panthera prior (Author, 1850)", "species", "1850"); err != nil {
			return err
		}
		if nSpeciesEmptyID, err = mkName(tx, "Panthera undated (Author, 2020)", "species", ""); err != nil {
			return err
		}
		if nSpeciesLooseID, err = mkName(tx, "Foo bar (Author, 1800)", "species", "1800"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}

	var tFamID, tGenID, tOKID, tBADID, tEmptyID, tLooseID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if tFamID, err = tx.CreateTaxon(coldp.Taxon{NameID: nFamID}); err != nil {
			return err
		}
		if tGenID, err = tx.CreateTaxon(coldp.Taxon{NameID: nGenID, ParentID: tFamID}); err != nil {
			return err
		}
		if tOKID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSpeciesOKID, ParentID: tGenID}); err != nil {
			return err
		}
		if tBADID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSpeciesBADID, ParentID: tGenID}); err != nil {
			return err
		}
		if tEmptyID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSpeciesEmptyID, ParentID: tGenID}); err != nil {
			return err
		}
		if tLooseID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSpeciesLooseID, ParentID: tFamID}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateTaxon: %v", err)
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	hasIssue := func(taxonID string) bool {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'taxon' AND record_id = ? AND rule_id = 'hive_published_before_genus'`,
			taxonID,
		).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		return n > 0
	}

	if hasIssue(tOKID) {
		t.Errorf("OK species (year 1950) should not fire — its year is later than genus year 1900")
	}
	if !hasIssue(tBADID) {
		t.Errorf("BAD species (year 1850) should fire — earlier than genus year 1900")
	}
	if hasIssue(tEmptyID) {
		t.Errorf("Empty-year species should not fire — skip_if_either_empty applies")
	}
	if hasIssue(tLooseID) {
		t.Errorf("Loose species (no genus ancestor) should not fire — rule not applicable")
	}
	// Suppress unused-var noise for taxa we only need for the setup.
	_ = tGenID
	_ = tFamID
}
