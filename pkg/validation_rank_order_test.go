package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gnames/gnlib/ent/nomcode"
	"github.com/sfborg/sflib/pkg/coldp"
)

// TestParentRankHigher exercises hive_parent_rank_higher end-to-end.
// Builds a fresh archive with two parent/child pairs:
//   - OK: Genus "Panthera" parents Species "Panthera leo"
//   - Violation: Genus "Foo" parents Order "Foo-order" (impossibly
//     placed — an order can't sit under a genus).
// Asserts the OK pair stays clean and the violation fires.
func TestParentRankHigher(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rank_order.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	mkName := func(tx *Tx, sn, rankStr string) (string, error) {
		return tx.CreateName(coldp.Name{
			ScientificName: sn,
			Rank:           ParseRank(rankStr),
			Code:           nomcode.New("iczn"),
		})
	}

	var nGenusOKID, nSpeciesOKID, nGenusBADID, nOrderBADID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if nGenusOKID, err = mkName(tx, "Panthera Oken, 1816", "genus"); err != nil {
			return err
		}
		if nSpeciesOKID, err = mkName(tx, "Panthera leo (Linnaeus, 1758)", "species"); err != nil {
			return err
		}
		if nGenusBADID, err = mkName(tx, "Foo Author, 1900", "genus"); err != nil {
			return err
		}
		if nOrderBADID, err = mkName(tx, "Foo-ordera Author, 1901", "order"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}

	var tGenusOKID, tSpeciesOKID, tGenusBADID, tOrderBADID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if tGenusOKID, err = tx.CreateTaxon(coldp.Taxon{NameID: nGenusOKID}); err != nil {
			return err
		}
		if tSpeciesOKID, err = tx.CreateTaxon(coldp.Taxon{NameID: nSpeciesOKID, ParentID: tGenusOKID}); err != nil {
			return err
		}
		if tGenusBADID, err = tx.CreateTaxon(coldp.Taxon{NameID: nGenusBADID}); err != nil {
			return err
		}
		// Order-under-genus is a rank inversion — hive's MoveTaxon
		// doesn't guard against ranks (that's a downstream check),
		// so CreateTaxon accepts it and the rule flags it.
		if tOrderBADID, err = tx.CreateTaxon(coldp.Taxon{NameID: nOrderBADID, ParentID: tGenusBADID}); err != nil {
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
			 WHERE table_name = 'taxon' AND record_id = ? AND rule_id = 'hive_parent_rank_higher'`,
			taxonID,
		).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		return n > 0
	}

	if hasIssue(tSpeciesOKID) {
		t.Errorf("species under genus should be clean; got a rank-order issue")
	}
	if !hasIssue(tOrderBADID) {
		t.Errorf("order under genus should fire hive_parent_rank_higher; got zero")
	}
	// Roots and unaffected taxa shouldn't fire either.
	if hasIssue(tGenusOKID) {
		t.Errorf("root genus should be clean (no parent)")
	}
	if hasIssue(tGenusBADID) {
		t.Errorf("root genus should be clean (no parent)")
	}
}
