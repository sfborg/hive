package hive

import (
	"context"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestSearchTaxaParentContext covers Step 1 of the search rollout:
// SearchTaxa populates ParentName / ParentAuthorship / ParentRank /
// ParentLabel on hits so front-ends can disambiguate homonyms and
// give synonym rows a "you land under X" cue.
func TestSearchTaxaParentContext(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var (
		familyID string
		genusID  string
	)
	err := a.WithTx(ctx, func(tx *Tx) error {
		famName := insertTestName(t, tx, "Felidae")
		id, err := tx.CreateTaxon(taxonForTest(famName))
		if err != nil {
			return err
		}
		familyID = id

		genusName := insertTestName(t, tx, "Panthera")
		g := taxonForTest(genusName)
		g.ParentID = familyID
		id, err = tx.CreateTaxon(g)
		if err != nil {
			return err
		}
		genusID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.SearchTaxa(ctx, "Panthera", SearchOpts{Limit: 10})
	if err != nil {
		t.Fatalf("SearchTaxa: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("SearchTaxa returned no hits for %q", "Panthera")
	}
	var h *TaxonHit
	for i := range hits {
		if hits[i].ID == genusID {
			h = &hits[i]
			break
		}
	}
	if h == nil {
		t.Fatalf("no hit for genus id %q; got %+v", genusID, hits)
	}
	if h.ParentName != "Felidae" {
		t.Errorf("ParentName = %q, want %q", h.ParentName, "Felidae")
	}
	if h.ParentLabel.Text == "" {
		t.Errorf("ParentLabel.Text is empty; expected a rendered label for Felidae")
	}
}

// TestSearchTaxaRootHasNoParent — a root-level taxon leaves the
// parent fields zero so the wire converter can elide the parent
// object cleanly.
func TestSearchTaxaRootHasNoParent(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var rootID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Animalia")
		id, err := tx.CreateTaxon(taxonForTest(nm))
		if err != nil {
			return err
		}
		rootID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.SearchTaxa(ctx, "Animalia", SearchOpts{Limit: 10})
	if err != nil {
		t.Fatalf("SearchTaxa: %v", err)
	}
	var h *TaxonHit
	for i := range hits {
		if hits[i].ID == rootID {
			h = &hits[i]
			break
		}
	}
	if h == nil {
		t.Fatalf("no hit for root id %q; got %+v", rootID, hits)
	}
	if h.ParentName != "" {
		t.Errorf("ParentName = %q, want empty for a root taxon", h.ParentName)
	}
	if h.ParentLabel.Text != "" {
		t.Errorf("ParentLabel.Text = %q, want empty for a root taxon", h.ParentLabel.Text)
	}
}

// TestSearchTaxaSynonymCarriesAcceptedParent — a synonym match must
// carry the ACCEPTED taxon's parent, not the synonym-name's own row
// (a name has no parent, but the accepted taxon does — this is the
// disambiguation curators need when two accepted taxa share a name).
func TestSearchTaxaSynonymCarriesAcceptedParent(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var (
		acceptedTaxonID string
	)
	err := a.WithTx(ctx, func(tx *Tx) error {
		famName := insertTestName(t, tx, "Felidae")
		familyID, err := tx.CreateTaxon(taxonForTest(famName))
		if err != nil {
			return err
		}
		accName := insertTestName(t, tx, "Panthera leo")
		acc := taxonForTest(accName)
		acc.ParentID = familyID
		id, err := tx.CreateTaxon(acc)
		if err != nil {
			return err
		}
		acceptedTaxonID = id
		// Attach a synonym pointing at the accepted taxon.
		synName := insertTestName(t, tx, "Felis leo")
		if _, err := tx.AddSynonym(coldp.Synonym{
			TaxonID: acceptedTaxonID,
			NameID:  synName,
			Status:  coldp.SynonymTS,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// Search on the synonym text with includeSynonyms=true.
	hits, err := a.SearchTaxa(ctx, "Felis leo", SearchOpts{
		Limit:           10,
		IncludeSynonyms: true,
	})
	if err != nil {
		t.Fatalf("SearchTaxa: %v", err)
	}
	var h *TaxonHit
	for i := range hits {
		if hits[i].IsSynonym && hits[i].ID == acceptedTaxonID {
			h = &hits[i]
			break
		}
	}
	if h == nil {
		t.Fatalf("no synonym hit resolving to accepted %q; got %+v", acceptedTaxonID, hits)
	}
	if h.MatchedName != "Felis leo" {
		t.Errorf("MatchedName = %q, want %q", h.MatchedName, "Felis leo")
	}
	if h.ParentName != "Felidae" {
		t.Errorf("ParentName = %q, want %q (accepted taxon's parent, not the synonym's)",
			h.ParentName, "Felidae")
	}
}

// TestSearchTaxaPartialFindsEpithet — Step 2. The partial mode
// uses the FTS5 mirror to match on any token-prefix in the
// canonical or scientific name, not just names starting with the
// query. Typing an epithet finds the binomial it belongs to.
func TestSearchTaxaPartialFindsEpithet(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var targetID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		famName := insertTestName(t, tx, "Felidae")
		famID, err := tx.CreateTaxon(taxonForTest(famName))
		if err != nil {
			return err
		}
		// The name row insert helper sets col__scientific_name;
		// populate gn__canonical_simple explicitly so FTS tokenizes
		// the epithet.
		spName := insertTestName(t, tx, "Panthera leo")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ? WHERE col__id = ?`,
			"Panthera leo", spName,
		); err != nil {
			return err
		}
		sp := taxonForTest(spName)
		sp.ParentID = famID
		id, err := tx.CreateTaxon(sp)
		if err != nil {
			return err
		}
		targetID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// Prefix mode: "leo" does NOT match "Panthera leo" (the name
	// doesn't start with "leo") — that's expected prefix behavior.
	hits, err := a.SearchTaxa(ctx, "leo", SearchOpts{
		Mode:  SearchModePrefix,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchTaxa prefix: %v", err)
	}
	for _, h := range hits {
		if h.ID == targetID {
			t.Errorf("prefix mode unexpectedly matched %q via epithet — prefix should be start-anchored", h.Name)
		}
	}

	// Partial mode: "leo" matches "Panthera leo" via the second
	// token — this is the core partial-mode capability.
	hits, err = a.SearchTaxa(ctx, "leo", SearchOpts{
		Mode:  SearchModePartial,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchTaxa partial: %v", err)
	}
	var found bool
	for _, h := range hits {
		if h.ID == targetID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("partial mode did not match %q via epithet %q; hits=%+v", "Panthera leo", "leo", hits)
	}
}

// TestSearchTaxaPartialWordOrderAgnostic — partial mode's FTS5
// AND semantics don't care about token order, so "leo Panthera"
// and "Panthera leo" both find the same rows.
func TestSearchTaxaPartialWordOrderAgnostic(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var targetID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Panthera leo")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ? WHERE col__id = ?`,
			"Panthera leo", nm,
		); err != nil {
			return err
		}
		id, err := tx.CreateTaxon(taxonForTest(nm))
		if err != nil {
			return err
		}
		targetID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	for _, q := range []string{"Panth leo", "leo Panth", "Panthera leo"} {
		hits, err := a.SearchTaxa(ctx, q, SearchOpts{
			Mode:  SearchModePartial,
			Limit: 5,
		})
		if err != nil {
			t.Fatalf("SearchTaxa partial %q: %v", q, err)
		}
		var found bool
		for _, h := range hits {
			if h.ID == targetID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("partial mode query %q did not match target; hits=%+v", q, hits)
		}
	}
}

// TestSearchTaxaPartialStripsAuthorship — pasting a fully-cited
// name should still find the taxon. gnparser extracts the
// canonical from "Panthera leo (Linnaeus, 1758)" and the FTS
// tokens are searched without the authorship noise.
func TestSearchTaxaPartialStripsAuthorship(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var targetID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Panthera leo")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ? WHERE col__id = ?`,
			"Panthera leo", nm,
		); err != nil {
			return err
		}
		id, err := tx.CreateTaxon(taxonForTest(nm))
		if err != nil {
			return err
		}
		targetID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.SearchTaxa(ctx, "Panthera leo (Linnaeus, 1758)", SearchOpts{
		Mode:  SearchModePartial,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("SearchTaxa partial with authorship: %v", err)
	}
	var found bool
	for _, h := range hits {
		if h.ID == targetID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("partial mode did not match target when input carried authorship; hits=%+v", hits)
	}
}

// TestFTSMatchFromTokens covers the small syntax builder. Punctuation
// in a token gets quoted so it doesn't collide with FTS5 operators;
// embedded double quotes are escaped by doubling.
func TestFTSMatchFromTokens(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"", "  "}, ""},
		{[]string{"Cerop"}, `"Cerop"*`},
		{[]string{"Cerop", "rusci"}, `"Cerop"* "rusci"*`},
		{[]string{"Panth-era"}, `"Panth-era"*`},
		{[]string{`he"llo`}, `"he""llo"*`},
	}
	for _, tc := range cases {
		got := ftsMatchFromTokens(tc.in)
		if got != tc.want {
			t.Errorf("ftsMatchFromTokens(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestListChildrenPageLeavesParentEmpty — parent context is a
// search-only concern. List endpoints must not populate it or the
// wire responses for /children and /roots would change shape.
func TestListChildrenPageLeavesParentEmpty(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var familyID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		famName := insertTestName(t, tx, "Felidae")
		id, err := tx.CreateTaxon(taxonForTest(famName))
		if err != nil {
			return err
		}
		familyID = id
		genusName := insertTestName(t, tx, "Panthera")
		g := taxonForTest(genusName)
		g.ParentID = familyID
		if _, err := tx.CreateTaxon(g); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, _, err := a.ListChildrenPage(ctx, familyID, 10, 0)
	if err != nil {
		t.Fatalf("ListChildrenPage: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("ListChildrenPage returned no hits")
	}
	for _, h := range hits {
		if h.ParentName != "" || h.ParentLabel.Text != "" {
			t.Errorf("list endpoint hit id=%q leaked parent context: name=%q label=%q",
				h.ID, h.ParentName, h.ParentLabel.Text)
		}
	}
}
