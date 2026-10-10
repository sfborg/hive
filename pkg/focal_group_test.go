package hive

import (
	"context"
	"testing"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

func TestFocalTaxonomicGroups(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	var diatoms, dinos string
	err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if diatoms, err = tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Bacillariophyta"))); err != nil {
			return err
		}
		dinos, err = tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Dinophyceae")))
		return err
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := a.db.Exec(`INSERT INTO vernacular (col__taxon_id, col__name, col__language) VALUES (?, 'diatoms', 'eng')`, diatoms); err != nil {
		t.Fatalf("vernacular: %v", err)
	}
	if err := a.WithTx(ctx, func(tx *Tx) error { return tx.SetFocalTaxonomicGroups([]string{dinos, "nope"}) }); err == nil {
		t.Errorf("unknown taxon accepted")
	}
	if err := a.WithTx(ctx, func(tx *Tx) error { return tx.SetFocalTaxonomicGroups([]string{dinos, diatoms}) }); err != nil {
		t.Fatalf("SetFocalTaxonomicGroups: %v", err)
	}
	got, err := a.FocalTaxonomicGroups(ctx)
	if err != nil {
		t.Fatalf("FocalTaxonomicGroups: %v", err)
	}
	if len(got) != 2 || got[0].Name != "Dinophyceae" || got[1].Name != "Bacillariophyta" {
		t.Fatalf("groups = %+v, want Dinophyceae then Bacillariophyta", got)
	}
	if len(got[1].Vernaculars) != 1 || got[1].Vernaculars[0].Name != "diatoms" {
		t.Errorf("vernaculars = %+v", got[1].Vernaculars)
	}

	// A homonym takes the lineage inside a focal group over the paper's
	// context.
	animal := mentions.Match{MatchType: "Exact", RecordID: "x9", Path: []mentions.Rank{
		{Name: "Metazoa", Rank: "kingdom", ID: "x7"},
		{Name: "Arthropoda", Rank: "phylum", ID: "x8"},
		{Name: "Insecta", Rank: "class", ID: "x10"},
		{Name: "Curculionidae", Rank: "family", ID: "x11"},
		{Name: "Amphora", Rank: "genus", ID: "x9"},
	}}
	diatom := mentions.Match{MatchType: "Exact", RecordID: "d9", Path: []mentions.Rank{
		{Name: "Bacillariophyta", Rank: "phylum", ID: "d8"},
		{Name: "Bacillariophyceae", Rank: "class", ID: "d11"},
		{Name: "Catenulaceae", Rank: "family", ID: "d10"},
		{Name: "Amphora", Rank: "genus", ID: "d9"},
	}}
	animal.Homonyms = []mentions.Match{diatom}
	beetle := mentions.Match{MatchType: "Exact", RecordID: "b9", Path: []mentions.Rank{
		{Name: "Metazoa", Rank: "kingdom", ID: "x7"},
		{Name: "Arthropoda", Rank: "phylum", ID: "x8"},
		{Name: "Carabus", Rank: "genus", ID: "b9"},
	}}
	if err := a.ReplaceNameMentions(ctx, "ref-x", testSource,
		map[string]int{"Amphora": 2, "Carabus nitens": 9},
		map[string]mentions.Match{"Amphora": animal, "Carabus nitens": beetle}); err != nil {
		t.Fatalf("ReplaceNameMentions: %v", err)
	}
	var phylum string
	if err := a.db.QueryRow(`SELECT t.col__phylum FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id = 'ref-x' AND m.mention = 'Amphora' AND m.chosen = 1`).Scan(&phylum); err != nil {
		t.Fatalf("chosen Amphora: %v", err)
	}
	if phylum != "Bacillariophyta" {
		t.Errorf("Amphora chosen in %s, want Bacillariophyta", phylum)
	}

	// A second source that knows only the insect genus drops the
	// mention once the first places it in a focal group.
	insectOnly := animal
	insectOnly.Homonyms = nil
	if err := a.ReplaceNameMentions(ctx, "ref-x", "gnverifier:4@2026-09-28",
		map[string]int{"Amphora": 2}, map[string]mentions.Match{"Amphora": insectOnly}); err != nil {
		t.Fatalf("ReplaceNameMentions ncbi: %v", err)
	}
	// A source that files the same diatom genus under other group names
	// keeps its match when the lineages share a lower taxon.
	otherNames := mentions.Match{MatchType: "Exact", RecordID: "o9", Path: []mentions.Rank{
		{Name: "Chromista", Rank: "kingdom", ID: "o5"},
		{Name: "Ochrophyta", Rank: "phylum", ID: "o6"},
		{Name: "Bacillariophyceae", Rank: "class", ID: "o7"},
		{Name: "Catenulaceae", Rank: "family", ID: "o8"},
		{Name: "Amphora", Rank: "genus", ID: "o9"},
	}}
	diatom.Path = []mentions.Rank{
		{Name: "Eukaryota", Rank: "domain", ID: "d6"},
		{Name: "Stramenopiles", Rank: "clade", ID: "d7"},
		{Name: "Bacillariophyta", Rank: "phylum", ID: "d8"},
		{Name: "Catenulaceae", Rank: "family", ID: "d10"},
		{Name: "Amphora", Rank: "genus", ID: "d9"},
	}
	animal.Homonyms = []mentions.Match{diatom}
	if err := a.ReplaceNameMentions(ctx, "ref-y", testSource,
		map[string]int{"Amphora": 2}, map[string]mentions.Match{"Amphora": animal}); err != nil {
		t.Fatalf("ReplaceNameMentions ref-y: %v", err)
	}
	if err := a.ReplaceNameMentions(ctx, "ref-y", "gnverifier:4@2026-09-28",
		map[string]int{"Amphora": 2}, map[string]mentions.Match{"Amphora": otherNames}); err != nil {
		t.Fatalf("ReplaceNameMentions ref-y ncbi: %v", err)
	}
	// A truncated lineage without class, order or family cannot be
	// shown to disagree and is kept.
	bare := mentions.Match{MatchType: "Exact", RecordID: "z9", Path: []mentions.Rank{
		{Name: "Amphora", Rank: "genus", ID: "z9"},
	}}
	if err := a.ReplaceNameMentions(ctx, "ref-z", testSource,
		map[string]int{"Amphora": 2}, map[string]mentions.Match{"Amphora": animal}); err != nil {
		t.Fatalf("ReplaceNameMentions ref-z: %v", err)
	}
	if err := a.ReplaceNameMentions(ctx, "ref-z", "gnverifier:4@2026-09-28",
		map[string]int{"Amphora": 2}, map[string]mentions.Match{"Amphora": bare}); err != nil {
		t.Fatalf("ReplaceNameMentions ref-z ncbi: %v", err)
	}
	for ref, want := range map[string]int{"ref-x": 0, "ref-y": 1, "ref-z": 1} {
		var chosen int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM hive__name_mention
			WHERE reference_id = ? AND mention = 'Amphora' AND raw__source LIKE 'gnverifier:4@%' AND chosen = 1`, ref).Scan(&chosen); err != nil {
			t.Fatalf("chosen in second source: %v", err)
		}
		if chosen != want {
			t.Errorf("%s: second source chosen rows = %d, want %d", ref, chosen, want)
		}
	}
}

func TestFocalScopeFollowsTaxaAboveGenus(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	child := func(tx *Tx, name, rank, parent string) string {
		nm := insertTestName(t, tx, name)
		if _, err := tx.tx.ExecContext(tx.ctx, `UPDATE name SET col__rank_id = ? WHERE col__id = ?`, rank, nm); err != nil {
			t.Fatalf("rank: %v", err)
		}
		tx2 := taxonForTest(nm)
		tx2.ParentID = parent
		id, err := tx.CreateTaxon(tx2)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return id
	}
	var reds, florideo string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		reds = child(tx, "Rhodophytina", "SUBPHYLUM", "")
		florideo = child(tx, "Florideophyceae", "CLASS", reds)
		child(tx, "Gelidium", "GENUS", florideo)
		return tx.SetFocalTaxonomicGroups([]string{reds})
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	var names map[string]bool
	if err := a.WithTx(ctx, func(tx *Tx) (err error) { names, err = focalNames(tx); return }); err != nil {
		t.Fatalf("focalNames: %v", err)
	}
	if !names["Rhodophytina"] || !names["Florideophyceae"] || names["Gelidium"] {
		t.Errorf("focal names = %v, want the group and its class but not the genus", names)
	}

	// The summaries are cleared once for the new scope, then left
	// alone until the scope changes.
	if cleared, err := a.RefreshFocalScope(ctx); err != nil || !cleared {
		t.Fatalf("first refresh = %v, %v; want cleared", cleared, err)
	}
	if cleared, _ := a.RefreshFocalScope(ctx); cleared {
		t.Errorf("unchanged scope cleared the summaries")
	}
	if err := a.WithTx(ctx, func(tx *Tx) error { child(tx, "Bangiophyceae", "CLASS", reds); return nil }); err != nil {
		t.Fatalf("add class: %v", err)
	}
	if cleared, _ := a.RefreshFocalScope(ctx); !cleared {
		t.Errorf("a class added under a focal group did not refresh the summaries")
	}
}
