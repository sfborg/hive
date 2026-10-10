package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gnames/gnlib/ent/nomcode"
	"github.com/sfborg/sflib/pkg/coldp"
)

func TestCodeOptional(t *testing.T) {
	for rank, want := range map[string]bool{
		"DOMAIN": true, "domain": true, "UNRANKED": true, "SUPERKINGDOM": true,
		"KINGDOM": false, "PHYLUM": false, "SPECIES": false, "": false,
	} {
		if got := CodeOptional(rank); got != want {
			t.Errorf("CodeOptional(%q) = %v, want %v", rank, got, want)
		}
	}
}

// TestMissingCodeRule checks hive_name_missing_code: a phylum without a
// code is flagged; a domain or an unranked name without one is not.
func TestMissingCodeRule(t *testing.T) {
	ctx := context.Background()
	a, err := Create(filepath.Join(t.TempDir(), "code.db"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()
	ids := map[string]string{}
	if err := a.WithTx(ctx, func(tx *Tx) error {
		for _, n := range []struct{ name, rank string }{
			{"Eukaryota", "domain"}, {"Archaeplastida", "unranked"}, {"Rhodophyta", "phylum"},
		} {
			id, err := tx.CreateName(coldp.Name{ScientificName: n.name, Rank: ParseRank(n.rank)})
			if err != nil {
				return err
			}
			ids[n.name] = id
		}
		_, err := tx.CreateName(coldp.Name{ScientificName: "Chlorophyta", Rank: ParseRank("phylum"),
			Code: nomcode.New("icn")})
		return err
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}
	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}
	flagged := func(nameID string) bool {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM __gsvalidator_results
			WHERE table_name = 'name' AND record_id = ? AND rule_id = 'hive_name_missing_code'`, nameID).Scan(&n)
		return n > 0
	}
	if !flagged(ids["Rhodophyta"]) {
		t.Error("phylum without a code is not flagged")
	}
	for _, name := range []string{"Eukaryota", "Archaeplastida"} {
		if flagged(ids[name]) {
			t.Errorf("%s is flagged, but its rank needs no code", name)
		}
	}
}
