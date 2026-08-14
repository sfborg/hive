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

// TestSearchTaxaFuzzyFindsTypo — Step 3. Fuzzy mode uses the
// trigram FTS mirror with an OR-joined query so a 1-2 character
// typo still surfaces the intended match (trigram overlap stays
// high enough for the target row to rank).
func TestSearchTaxaFuzzyFindsTypo(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var targetID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Ceroplastes")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ? WHERE col__id = ?`,
			"Ceroplastes", nm,
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

	// "Cerpolastes" is Ceroplastes with the 'o' and 'p' swapped —
	// trigram overlap should still surface the intended match.
	hits, err := a.SearchTaxa(ctx, "Cerpolastes", SearchOpts{
		Mode:  SearchModeFuzzy,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchTaxa fuzzy: %v", err)
	}
	var found bool
	for _, h := range hits {
		if h.ID == targetID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fuzzy mode did not match target for a 2-char typo; hits=%+v", hits)
	}
}

// TestSearchTaxaFuzzyShortQueryFallback — a query too short to
// yield any trigrams (< 3 chars) falls back to prefix so the
// curator gets *some* candidates from the leading characters.
func TestSearchTaxaFuzzyShortQueryFallback(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Panthera")
		if _, err := tx.CreateTaxon(taxonForTest(nm)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// 2-char query has no trigrams; expect prefix-style match.
	hits, err := a.SearchTaxa(ctx, "Pa", SearchOpts{
		Mode:  SearchModeFuzzy,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("SearchTaxa fuzzy short: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("2-char fuzzy query returned no hits; expected prefix fallback to find Panthera")
	}
}

// TestGenerateTrigrams covers the rune-aware sliding window,
// dedup semantics, and the cap-with-head+tail truncation.
func TestGenerateTrigrams(t *testing.T) {
	cases := []struct {
		name string
		in   string
		cap  int
		want []string
	}{
		{"empty", "", 0, nil},
		{"too short", "ab", 0, nil},
		{"exactly 3", "cat", 0, []string{"cat"}},
		{"deduped", "aaaa", 0, []string{"aaa"}},
		{"basic", "abcd", 0, []string{"abc", "bcd"}},
		{"unicode", "æøå_", 0, []string{"æøå", "øå_"}},
		{
			// cap=4 keeps head 2 + tail 2 = 4, dropping "cde" and "def"
			// from the middle of "abcdefgh" (trigrams: abc bcd cde def
			// efg fgh — cap=4 → abc bcd fgh + one tail, so keep head=2
			// and last 2).
			name: "cap drops middle",
			in:   "abcdefgh",
			cap:  4,
			want: []string{"abc", "bcd", "efg", "fgh"},
		},
	}
	for _, tc := range cases {
		got := generateTrigrams(tc.in, tc.cap)
		if len(got) != len(tc.want) {
			t.Errorf("%s: len=%d want %d; got %v want %v",
				tc.name, len(got), len(tc.want), got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: [%d] = %q want %q; got %v",
					tc.name, i, got[i], tc.want[i], got)
			}
		}
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

// TestSearchTaxaPartialExactEpithetOutranksPrefix — Step 4's
// exact-epithet bonus: for q="rusci" the taxon whose last token
// EQUALS "rusci" should rank above one whose last token merely
// starts with it ("ruscifolia").
func TestSearchTaxaPartialExactEpithetOutranksPrefix(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var exactID, prefixID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		exact := insertTestName(t, tx, "Ceroplastes rusci")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ? WHERE col__id = ?`,
			"Ceroplastes rusci", exact,
		); err != nil {
			return err
		}
		id, err := tx.CreateTaxon(taxonForTest(exact))
		if err != nil {
			return err
		}
		exactID = id
		prefix := insertTestName(t, tx, "Alyxia ruscifolia")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ? WHERE col__id = ?`,
			"Alyxia ruscifolia", prefix,
		); err != nil {
			return err
		}
		id, err = tx.CreateTaxon(taxonForTest(prefix))
		if err != nil {
			return err
		}
		prefixID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hits, err := a.SearchTaxa(ctx, "rusci", SearchOpts{
		Mode:  SearchModePartial,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("SearchTaxa partial: %v", err)
	}
	var exactPos, prefixPos = -1, -1
	for i, h := range hits {
		if h.ID == exactID {
			exactPos = i
		}
		if h.ID == prefixID {
			prefixPos = i
		}
	}
	if exactPos < 0 || prefixPos < 0 {
		t.Fatalf("expected both hits present; got exact=%d prefix=%d; hits=%+v",
			exactPos, prefixPos, hits)
	}
	if exactPos >= prefixPos {
		t.Errorf("exact-epithet match ranked %d, prefix-of-epithet ranked %d; expected exact < prefix",
			exactPos, prefixPos)
	}
}

// TestSearchTaxaPartialFindsByAuthorship — Step 4 extension.
// The FTS mirrors now index col__authorship alongside the name
// columns, so a query like "sigillatus Walker" (epithet + author
// surname) resolves via a single MATCH.
func TestSearchTaxaPartialFindsByAuthorship(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var targetID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nm := insertTestName(t, tx, "Gryllodes sigillatus")
		if _, err := tx.tx.ExecContext(tx.ctx,
			`UPDATE name SET gn__canonical_simple = ?, col__authorship = ? WHERE col__id = ?`,
			"Gryllodes sigillatus", "(Walker, 1869)", nm,
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

	hits, err := a.SearchTaxa(ctx, "sigillatus Walker", SearchOpts{
		Mode:  SearchModePartial,
		Limit: 5,
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
		t.Fatalf("did not find Gryllodes sigillatus via 'sigillatus Walker'; hits=%+v", hits)
	}
}

// TestFTSHasColumn — migration helper honesty check. Ensures a
// freshly-created FTS mirror reports the expected columns, and
// that the migration correctly detects the old 2-column shape.
func TestFTSHasColumn(t *testing.T) {
	a := newTestArchive(t)
	ctx := context.Background()

	has, err := ftsHasColumn(ctx, a.db, "hive__name_fts", "col__authorship")
	if err != nil {
		t.Fatalf("ftsHasColumn: %v", err)
	}
	if !has {
		t.Error("current hive__name_fts should have col__authorship")
	}

	// Create a legacy-shaped table to confirm detection returns false.
	if _, err := a.db.ExecContext(ctx, `
		DROP TABLE IF EXISTS fts_legacy_test;
		CREATE VIRTUAL TABLE fts_legacy_test USING fts5(
			gn__canonical_simple, col__scientific_name,
			content='name', tokenize='unicode61'
		)`); err != nil {
		t.Fatalf("create legacy FTS: %v", err)
	}
	has, err = ftsHasColumn(ctx, a.db, "fts_legacy_test", "col__authorship")
	if err != nil {
		t.Fatalf("ftsHasColumn on legacy: %v", err)
	}
	if has {
		t.Error("legacy FTS should not have col__authorship")
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
