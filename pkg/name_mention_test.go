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
		{ReferenceQuery{ColParent: "fictaceae"}, []string{"ref-a", "ref-b"}},
		{ReferenceQuery{ColParent: "Fictogenus"}, []string{"ref-a"}},
		{ReferenceQuery{ColParent: "Plantae", MinMentions: 2}, []string{"ref-a"}},
		{ReferenceQuery{ColParent: "Animalia"}, nil},
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

	hits, err := a.SearchReferences(ctx, "ref", 50, ReferenceQuery{ColParent: "Fictaceae"})
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

	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{ColParent: "Fictaceae"})
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	byID := map[string]ReferenceHit{}
	for _, h := range hits {
		byID[h.ID] = h
	}
	ra := byID["ref-a"]
	if !ra.MentionsIndexed || ra.ColFamily != "Fictaceae" || ra.ColFamilyMentions != 6 {
		t.Errorf("ref-a summary = %+v", ra)
	}
	if ra.ColMatch == nil || ra.ColMatch.Mention != "Fictogenus alpha" || ra.ColMatch.Count != 5 {
		t.Errorf("ref-a match = %+v", ra.ColMatch)
	}

	all, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{})
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	for _, h := range all {
		if h.ID == "ref-c" && (h.MentionsIndexed || h.ColFamily != "") {
			t.Errorf("unindexed ref-c summary = %+v", h)
		}
		if h.ColMatch != nil {
			t.Errorf("%s: ColMatch set without a ColParent query", h.ID)
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
	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{ColParent: "Fictogenus"})
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
	got, err := a.ColTaxa(ctx, "fict", 10)
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
