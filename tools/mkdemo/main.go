// mkdemo generates a small SFGA archive with a tiny taxonomic tree so that
// developers can smoke-test `hive view` and other frontends against real
// data. Not part of the shipped binary — invoked via `just demo` or
// `go run ./tools/mkdemo <path>`.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/gnames/gnlib/ent/nomcode"
	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/sflib/pkg/coldp"
)

func main() {
	path := "./demo.db"
	if len(os.Args) == 2 {
		path = os.Args[1]
	} else if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: mkdemo [path]")
		os.Exit(2)
	}

	// Delete any existing archive (and its WAL/SHM sidecars) so the recipe
	// is idempotent.
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")

	a, err := hive.Create(path)
	if err != nil {
		die("create: %v", err)
	}
	defer a.Close()

	ctx := hive.WithActor(context.Background(), "0000-0002-1825-0097")

	err = a.WithTx(ctx, func(tx *hive.Tx) error {
		if err := tx.UpdateMetadata(hive.Metadata{
			Title:       "Hive demo — Felidae",
			Alias:       "demo",
			Description: "A tiny zoological subtree (Animalia → Felidae) used to smoke-test hive frontends.",
			License:     "CC0-1.0",
			Version:     "0.1",
		}); err != nil {
			return err
		}
		return populate(tx)
	})
	if err != nil {
		die("populate: %v", err)
	}
	// Apply every registered rule fixture (bad + good cases) so the
	// demo archive contains representative data for every validation
	// rule hive ships. A developer can then browse via `hive view`
	// or the Issues screen and see every rule fire on its own
	// fixture. Cases are applied sequentially; a per-case failure
	// is logged but doesn't abort the whole seed (some fixtures
	// depend on empty state that a prior fixture may have taken —
	// this is best-effort inspection data, not a test).
	appliedBad, appliedGood, skipped := 0, 0, 0
	for _, f := range hive.RuleFixtures {
		for _, c := range f.Bad {
			if err := c.Setup(ctx, a); err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s bad %q: %v\n", f.RuleID, c.Note, err)
				skipped++
				continue
			}
			appliedBad++
		}
		for _, c := range f.Good {
			if err := c.Setup(ctx, a); err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s good %q: %v\n", f.RuleID, c.Note, err)
				skipped++
				continue
			}
			appliedGood++
		}
	}
	if err := a.ReindexValidation(ctx, nil); err != nil {
		die("reindex: %v", err)
	}
	fmt.Printf("wrote %s — %d fixture cases applied (%d bad, %d good; %d skipped)\n",
		path, appliedBad+appliedGood, appliedBad, appliedGood, skipped)
}

// populate inserts a small Animalia subtree ending in Panthera / Felis, plus
// a synonym on Panthera leo so that later slices (synonym tab, search) have
// something to render.
func populate(tx *hive.Tx) error {
	type step struct {
		label      string
		scientific string
		rank       string // sfga rank id — needed for italicization
		parent     string // "" for root
		extinct    bool   // true for fossil / known-extinct taxa
	}
	classification := []step{
		{"animalia", "Animalia", "KINGDOM", "", false},
		{"chordata", "Chordata", "PHYLUM", "animalia", false},
		{"mammalia", "Mammalia", "CLASS", "chordata", false},
		{"carnivora", "Carnivora", "ORDER", "mammalia", false},
		{"felidae", "Felidae Fischer de Waldheim, 1817", "FAMILY", "carnivora", false},
		{"panthera", "Panthera Oken, 1816", "GENUS", "felidae", false},
		{"panthera_leo", "Panthera leo (Linnaeus, 1758)", "SPECIES", "panthera", false},
		{"panthera_tigris", "Panthera tigris (Linnaeus, 1758)", "SPECIES", "panthera", false},
		{"panthera_atrox", "Panthera atrox (Leidy, 1853)", "SPECIES", "panthera", true},
		{"felis", "Felis Linnaeus, 1758", "GENUS", "felidae", false},
		{"felis_catus", "Felis catus Linnaeus, 1758", "SPECIES", "felis", false},
		{"smilodon", "Smilodon Lund, 1842", "GENUS", "felidae", true},
		{"smilodon_fatalis", "Smilodon fatalis (Leidy, 1868)", "SPECIES", "smilodon", true},
	}
	labelToID := map[string]string{}
	for _, s := range classification {
		nameID, err := tx.CreateName(coldp.Name{
			ScientificNameString: s.scientific,
			Rank:                 coldp.NewRank(s.rank),
			// Demo tree is entirely zoological — setting code on every
			// name lets the "code inherits from parent" behavior show
			// up in `hive edit ./demo.db` without extra clicks.
			Code: nomcode.New("ZOOLOGICAL"),
		})
		if err != nil {
			return fmt.Errorf("create name %s: %w", s.label, err)
		}
		t := coldp.Taxon{NameID: nameID}
		if s.parent != "" {
			t.ParentID = labelToID[s.parent]
		}
		if s.extinct {
			t.Extinct = sql.NullBool{Bool: true, Valid: true}
		}
		id, err := tx.CreateTaxon(t)
		if err != nil {
			return fmt.Errorf("create taxon %s: %w", s.label, err)
		}
		labelToID[s.label] = id
	}

	// One synonym so downstream slices (synonyms tab, search) can render it.
	leoID := labelToID["panthera_leo"]
	synName, err := tx.CreateName(coldp.Name{
		ScientificNameString: "Felis leo Linnaeus, 1758",
		Rank:                 coldp.NewRank("SPECIES"),
		Code:                 nomcode.New("ZOOLOGICAL"),
	})
	if err != nil {
		return err
	}
	_, err = tx.AddSynonym(coldp.Synonym{
		TaxonID: leoID,
		NameID:  synName,
		Status:  coldp.SynonymTS,
		Remarks: "demo synonym",
	})
	if err != nil {
		return err
	}
	return seedReferences(tx)
}

// seedReferences drops a few references into the demo archive via the
// core write layer (Tx.CreateReference). Earlier revisions used raw
// SQL because CreateReference didn't exist yet.
func seedReferences(tx *hive.Tx) error {
	refs := []coldp.Reference{
		{
			ID:       "ref-linnaeus-1758",
			Citation: "Linnaeus, C. (1758). Systema naturae per regna tria naturae, secundum classes, ordines, genera, species, cum characteribus, differentiis, synonymis, locis. 10th edition. Laurentii Salvii, Stockholm.",
			Author:   "Linnaeus, C.",
			Title:    "Systema naturae per regna tria naturae, secundum classes, ordines, genera, species, cum characteribus, differentiis, synonymis, locis",
			Issued:   "1758",
			Type:     coldp.NewReferenceType("BOOK"),
			Link:     "https://www.biodiversitylibrary.org/bibliography/542",
		},
		{
			ID:       "ref-fischer-1817",
			Citation: "Fischer de Waldheim, G. (1817). Adversaria zoologica. Memoires de la Societe Imperiale des Naturalistes de Moscou, 5, 357–472.",
			Author:   "Fischer de Waldheim, G.",
			Title:    "Adversaria zoologica",
			Issued:   "1817",
			Type:     coldp.NewReferenceType("ARTICLE_JOURNAL"),
		},
		{
			ID:       "ref-leidy-1868",
			Citation: "Leidy, J. (1868). Notice of some remains of extinct pachyderms. Proceedings of the Academy of Natural Sciences of Philadelphia, 20, 230–233.",
			Author:   "Leidy, J.",
			Title:    "Notice of some remains of extinct pachyderms",
			Issued:   "1868",
			Type:     coldp.NewReferenceType("ARTICLE_JOURNAL"),
		},
	}
	for _, r := range refs {
		if _, err := tx.CreateReference(r); err != nil {
			return fmt.Errorf("seed reference %s: %w", r.ID, err)
		}
	}
	return nil
}

func die(f string, args ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
	os.Exit(1)
}
