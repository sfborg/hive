package hive

import (
	"context"
	"testing"
)

func TestTaxonTree(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedMentions(t, a, ctx)

	type row struct {
		path   string
		papers int
		match  bool
	}
	tests := []struct {
		name string
		q    TaxonTreeQuery
		want []row
	}{
		{"top, chain followed to the first branch", TaxonTreeQuery{SourceID: 1}, []row{
			{"|Plantae|", 2, false},
			{"|Plantae|Fictophyta|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|", 2, false},
		}},
		{"two levels", TaxonTreeQuery{SourceID: 1, Depth: 2}, []row{
			{"|Plantae|", 2, false},
			{"|Plantae|Fictophyta|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|Inventa|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|Fictogenus|", 1, false},
		}},
		{"below a path", TaxonTreeQuery{SourceID: 1, Path: "|Plantae|Fictophyta|Fictaceae|"}, []row{
			{"|Plantae|Fictophyta|Fictaceae|Inventa|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|Fictogenus|", 1, false},
		}},
		{"search with ancestors", TaxonTreeQuery{SourceID: 1, Q: "fictog"}, []row{
			{"|Plantae|", 2, false},
			{"|Plantae|Fictophyta|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|", 2, false},
			{"|Plantae|Fictophyta|Fictaceae|Fictogenus|", 1, true},
		}},
		{"min mentions", TaxonTreeQuery{SourceID: 1, MinMentions: 2, Depth: 3}, []row{
			{"|Plantae|", 1, false},
			{"|Plantae|Fictophyta|", 1, false},
			{"|Plantae|Fictophyta|Fictaceae|", 1, false},
			{"|Plantae|Fictophyta|Fictaceae|Fictogenus|", 1, false},
		}},
		{"other source", TaxonTreeQuery{SourceID: 4}, nil},
	}
	for _, tc := range tests {
		got, err := a.TaxonTree(ctx, tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
			continue
		}
		for i, w := range tc.want {
			g := got[i]
			if g.Path != w.path || g.Papers != w.papers || g.Match != w.match {
				t.Errorf("%s [%d]: got %+v, want %+v", tc.name, i, g, w)
			}
		}
	}
	got, _ := a.TaxonTree(ctx, TaxonTreeQuery{SourceID: 1, Q: "fictac"})
	if len(got) != 3 || got[2].Rank != "family" || got[2].Children != 2 {
		t.Errorf("rank/children: got %+v", got)
	}
}
