package hive

import (
	"context"
	"testing"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
	"github.com/sfborg/sflib/pkg/coldp"
)

const testSource = "gnverifier:1@2026-08-20"

func fictPath(genus, gid string) []mentions.Rank {
	return []mentions.Rank{
		{Name: "Plantae", Rank: "kingdom", ID: "k1"},
		{Name: "Fictophyta", Rank: "phylum", ID: "p1"},
		{Name: "Fictaceae", Rank: "family", ID: "f1"},
		{Name: genus, Rank: "genus", ID: gid},
	}
}

func seedMentions(t *testing.T, a *Archive, ctx context.Context) {
	t.Helper()
	err := a.WithTx(ctx, func(tx *Tx) error {
		for _, id := range []string{"ref-a", "ref-b", "ref-c"} {
			if _, err := tx.CreateReference(coldp.Reference{ID: id, Author: "Doe, J.", Title: id, Issued: "2025"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed refs: %v", err)
	}
	alpha := mentions.Match{MatchType: "Exact", RecordID: "g1", MatchedName: "Fictogenus", Path: fictPath("Fictogenus", "g1")}
	beta := mentions.Match{MatchType: "Exact", RecordID: "g2", MatchedName: "Inventa", Path: fictPath("Inventa", "g2")}
	// ref-a: Fictogenus 5×, Inventa 1×, one unverified name.
	if err := a.ReplaceNameMentions(ctx, "ref-a", testSource,
		map[string]int{"Fictogenus alpha": 5, "Inventa nova": 1, "Nonesuch": 2},
		map[string]mentions.Match{"Fictogenus alpha": alpha, "Inventa nova": beta},
	); err != nil {
		t.Fatalf("ReplaceNameMentions ref-a: %v", err)
	}
	// ref-b: Inventa 1× only.
	if err := a.ReplaceNameMentions(ctx, "ref-b", testSource,
		map[string]int{"Inventa nova": 1},
		map[string]mentions.Match{"Inventa nova": beta},
	); err != nil {
		t.Fatalf("ReplaceNameMentions ref-b: %v", err)
	}
	// ref-c is not indexed.
}

func TestColParentFilter(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)

	tests := []struct {
		rq   ReferenceQuery
		want []string
	}{
		{ReferenceQuery{Parents: []IndexParent{{1, "fictaceae"}}}, []string{"ref-a", "ref-b"}},
		{ReferenceQuery{Parents: []IndexParent{{1, "Fictogenus"}}}, []string{"ref-a"}},
		{ReferenceQuery{Parents: []IndexParent{{1, "Plantae"}}, MinMentions: 2}, []string{"ref-a"}},
		{ReferenceQuery{Parents: []IndexParent{{1, "Animalia"}}}, nil},
	}
	for _, tc := range tests {
		hits, total, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, tc.rq)
		if err != nil {
			t.Fatalf("%+v: %v", tc.rq, err)
		}
		got := hitIDs(hits)
		if len(got) != len(tc.want) || total != len(tc.want) {
			t.Errorf("%+v: ids = %v (total %d), want %v", tc.rq, got, total, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%+v: ids = %v, want %v", tc.rq, got, tc.want)
			}
		}
	}

	hits, err := a.SearchReferences(ctx, "ref", 50, ReferenceQuery{Parents: []IndexParent{{1, "Fictaceae"}}})
	if err != nil {
		t.Fatalf("SearchReferences: %v", err)
	}
	if got := hitIDs(hits); len(got) != 2 {
		t.Errorf("search with col_parent = %v, want ref-a, ref-b", got)
	}
}

func TestMentionSummaries(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)

	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{Parents: []IndexParent{{1, "Fictaceae"}}})
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	byID := map[string]ReferenceHit{}
	for _, h := range hits {
		byID[h.ID] = h
	}
	ra := byID["ref-a"]
	if !ra.MentionsIndexed || ra.IndexFamily != "Fictaceae" || ra.IndexFamilyMentions != 6 || ra.IndexFamilySourceID != 1 {
		t.Errorf("ref-a summary = %+v", ra)
	}
	if ra.IndexMatch == nil || ra.IndexMatch.Mention != "Fictogenus alpha" || ra.IndexMatch.Count != 5 ||
		ra.IndexMatch.SourceID != 1 || ra.IndexMatch.Parent != "Fictaceae" {
		t.Errorf("ref-a match = %+v", ra.IndexMatch)
	}

	all, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{})
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	for _, h := range all {
		if h.ID == "ref-c" && (h.MentionsIndexed || h.IndexFamily != "") {
			t.Errorf("unindexed ref-c summary = %+v", h)
		}
		if h.IndexMatch != nil {
			t.Errorf("%s: IndexMatch set without a Parents query", h.ID)
		}
	}
}

func TestReplaceNameMentionsReplaces(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)
	if err := a.ReplaceNameMentions(ctx, "ref-a", testSource, map[string]int{"Nonesuch": 1}, nil); err != nil {
		t.Fatalf("ReplaceNameMentions: %v", err)
	}
	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{Parents: []IndexParent{{1, "Fictogenus"}}})
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("stale mentions survived replacement: %v", hitIDs(hits))
	}
}

func TestColTaxa(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)
	got, err := a.ColTaxa(ctx, 1, "fict", 10)
	if err != nil {
		t.Fatalf("ColTaxa: %v", err)
	}
	want := []ColTaxon{
		{Name: "Fictaceae", Rank: "family", Papers: 2},
		{Name: "Fictophyta", Rank: "phylum", Papers: 2},
		{Name: "Fictogenus", Rank: "genus", Papers: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("ColTaxa = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ColTaxa[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestIndexSourcesAreSeparate(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)
	// The same name in a second source, placed in a different family
	// and under an unranked clade.
	ncbi := mentions.Match{MatchType: "Exact", RecordID: "n9", Path: []mentions.Rank{
		{Name: "Eukaryota", Rank: "domain", ID: "n1"},
		{Name: "Fictoclada", Rank: "unranked", ID: "n2"},
		{Name: "Inventaceae", Rank: "family", ID: "n3"},
		{Name: "Fictogenus", Rank: "genus", ID: "n9"},
	}}
	if err := a.ReplaceNameMentions(ctx, "ref-a", "gnverifier:4@2026-09-28",
		map[string]int{"Fictogenus alpha": 5, "Inventa nova": 1},
		map[string]mentions.Match{"Fictogenus alpha": ncbi, "Inventa nova": ncbi},
	); err != nil {
		t.Fatalf("ReplaceNameMentions ncbi: %v", err)
	}

	// An unranked level matches through the classification path.
	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor,
		ReferenceQuery{Parents: []IndexParent{{mentions.NCBI, "fictoclada"}}})
	if err != nil {
		t.Fatalf("ncbi query: %v", err)
	}
	if got := hitIDs(hits); len(got) != 1 || got[0] != "ref-a" {
		t.Errorf("ncbi_parent Fictoclada = %v, want [ref-a]", got)
	}
	// A taxon from one source is not found through the other.
	hits, _, err = a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor,
		ReferenceQuery{Parents: []IndexParent{{mentions.CatalogueOfLife, "Inventaceae"}}})
	if err != nil {
		t.Fatalf("col query: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("col_parent Inventaceae matched NCBI rows: %v", hitIDs(hits))
	}
	// Both sources match six mentions exactly; the tie goes to NCBI.
	all, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, h := range all {
		if h.ID == "ref-a" && (h.IndexFamily != "Inventaceae" || h.IndexFamilySourceID != mentions.NCBI) {
			t.Errorf("ref-a family = %q from %d, want Inventaceae from NCBI", h.IndexFamily, h.IndexFamilySourceID)
		}
	}
}

func TestTaxonSummaryLeavesOutStrayMatches(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)
	// Mostly names the source leaves without a phylum, one stray
	// exact match under an animal phylum, and a fuzzy match to another.
	unranked := mentions.Match{MatchType: "Exact", RecordID: "d1", Path: []mentions.Rank{
		{Name: "Dinophyceae", Rank: "class", ID: "c1"},
		{Name: "Dinogenus", Rank: "genus", ID: "d1"},
	}}
	animal := func(id string, mt string) mentions.Match {
		return mentions.Match{MatchType: mt, RecordID: id, Path: []mentions.Rank{
			{Name: "Metazoa", Rank: "kingdom", ID: "k9"},
			{Name: "Arthropoda", Rank: "phylum", ID: "p9"},
			{Name: "Insecta", Rank: "class", ID: "c9"},
			{Name: "Stray" + id, Rank: "genus", ID: id},
		}}
	}
	if err := a.ReplaceNameMentions(ctx, "ref-b", testSource,
		map[string]int{"Dinogenus alpha": 40, "Strayx": 2, "Dinogenus pontica": 30},
		map[string]mentions.Match{
			"Dinogenus alpha":   unranked,
			"Strayx":            animal("x", "Exact"),
			"Dinogenus pontica": animal("y", "Fuzzy"),
		},
	); err != nil {
		t.Fatalf("ReplaceNameMentions: %v", err)
	}
	sums, err := a.batchTaxonSummaries(ctx, []string{"ref-b"})
	if err != nil {
		t.Fatalf("batchTaxonSummaries: %v", err)
	}
	s := sums["ref-b"]
	if p := s["phylum"]; p.Name != "" || p.Classified != 2 || p.Matched != 42 {
		t.Errorf("phylum = %+v, want empty name with 2 of 42 classified", p)
	}
	if c := s["class"]; c.Name != "Dinophyceae" || len(c.Top) != 1 {
		t.Errorf("class = %+v, want Dinophyceae alone", c)
	}
	if n := s[SummaryRankName]; len(n.Top) != 3 || n.Top[0].Name != "Dinogenus alpha" {
		t.Errorf("names = %+v, want all three as written", n.Top)
	}
}
