package hive

import (
	"context"
	"testing"

	vlib "github.com/gnames/gnlib/ent/verifier"
	"github.com/sfborg/hive/pkg/taxonomy/mentions"
	"github.com/sfborg/sflib/pkg/coldp"
)

// stubSource answers every name with one lineage in its data source.
type stubSource struct{ id int }

func (s stubSource) VerifyBatch(_ context.Context, names []string) []vlib.Name {
	out := make([]vlib.Name, len(names))
	for i, n := range names {
		out[i] = vlib.Name{Name: n, MatchType: vlib.Exact, BestResult: &vlib.ResultData{
			MatchedCanonicalSimple: n, MatchedCanonicalFull: n, MatchedName: n,
			ClassificationPath: "Chromista|Bacillariophyceae|" + n,
			ClassificationRanks: "kingdom|class|genus", ClassificationIDs: "k|c|g-" + n,
		}}
	}
	return out
}

func (s stubSource) DataSource(id int) (vlib.DataSource, error) {
	return vlib.DataSource{ID: id, Version: "v1"}, nil
}

func TestIndexNameMentionsFollowsArchiveSources(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	var ref string
	if err := a.WithTx(ctx, func(tx *Tx) (err error) {
		ref, err = tx.CreateReference(coldp.Reference{Title: "Diatoms"})
		return err
	}); err != nil {
		t.Fatalf("reference: %v", err)
	}
	if _, err := a.db.Exec(`INSERT INTO hive__reference_pdf (reference_id, sha512, attached_at) VALUES (?, 'sha1', '2026')`, ref); err != nil {
		t.Fatalf("attach: %v", err)
	}
	jats := []byte(`<article xmlns:tp="http://www.plazi.org/taxpub"><body><p>
		<tp:taxon-name full-name="Navicula">Navicula</tp:taxon-name> and
		<tp:taxon-name full-name="Navicula">Navicula</tp:taxon-name></p></body></article>`)
	read := func(string) ([]byte, error) { return jats, nil }
	verifier := func(id int) mentions.Verifier { return stubSource{id} }
	sourcesOf := func() map[string]int {
		rows, err := a.db.Query(`SELECT raw__source, COUNT(*) FROM hive__name_mention GROUP BY 1`)
		if err != nil {
			t.Fatalf("rows: %v", err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var s string
			var n int
			rows.Scan(&s, &n)
			out[s] = n
		}
		return out
	}

	var stages []string
	rep, err := a.IndexNameMentions(ctx, nil, read, verifier, func(p IndexProgress) {
		if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
			stages = append(stages, p.Stage)
		}
	})
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if rep.Papers != 1 || len(rep.Sources) != 2 {
		t.Fatalf("report = %+v, want 1 paper and the 2 default sources", rep)
	}
	if got := sourcesOf(); len(got) != 2 {
		t.Errorf("rows by source = %v, want the two defaults", got)
	}
	if len(stages) < 4 || stages[len(stages)-1] != "Done" {
		t.Errorf("progress stages = %v", stages)
	}

	// Switching to one other source replaces the rows.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.SetNameSources([]mentions.Source{{ID: 195, Label: "Some source"}})
	}); err != nil {
		t.Fatalf("SetNameSources: %v", err)
	}
	if _, err := a.IndexNameMentions(ctx, nil, read, verifier, nil); err != nil {
		t.Fatalf("re-index: %v", err)
	}
	got := sourcesOf()
	if len(got) != 1 || got["gnverifier:195@v1"] != 1 {
		t.Errorf("rows by source after switch = %v, want only gnverifier:195@v1", got)
	}
	sources, _ := a.NameSources(ctx)
	if len(sources) != 1 || sources[0].Key != "gn195" {
		t.Errorf("NameSources = %+v", sources)
	}
}
