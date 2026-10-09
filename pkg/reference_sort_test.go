package hive

import (
	"context"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

func TestListReferencesMultiColumnSort(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	refs := []coldp.Reference{
		{ID: "r1", Author: "Beta, B.", Title: "One", Issued: "2001"},
		{ID: "r2", Author: "Alpha, A.", Title: "Two", Issued: "2001"},
		{ID: "r3", Author: "Alpha, A.", Title: "Three", Issued: "1999"},
		{ID: "r4", Author: "Gamma, G.", Title: "Four"},
	}
	if err := a.WithTx(ctx, func(tx *Tx) error {
		for _, r := range refs {
			r.Type = coldp.NewReferenceType("ARTICLE_JOURNAL")
			if _, err := tx.CreateReference(r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	order := func(sort string) []string {
		t.Helper()
		keys, err := ParseReferenceSort(sort)
		if err != nil {
			t.Fatal(err)
		}
		hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{Sort: keys})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, h := range hits {
			ids = append(ids, h.ID)
		}
		return ids
	}
	for sort, want := range map[string]string{
		"year:desc,author:asc": "r2 r1 r3 r4", // undated last even when descending
		"year:asc,title:desc":  "r3 r2 r1 r4",
		"author,year:desc":     "r2 r3 r1 r4",
	} {
		got := order(sort)
		if s := join(got); s != want {
			t.Errorf("%s: got %s, want %s", sort, s, want)
		}
	}
	if _, err := ParseReferenceSort("nonsense:asc"); err == nil {
		t.Error("expected an error for an unknown column")
	}
}

func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += id
	}
	return out
}
