package bhlnames

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestLookupMapping runs the client against an httptest server that
// echoes a canned /name_refs response, and asserts the JSON → Hit
// projection is correct. Covers both the "specific part" path
// (article in a journal) and the "no part" path (whole item / book)
// without hitting the live BHLnames service.
func TestLookupMapping(t *testing.T) {
	const canned = `{
		"references": [
			{
				"name": {"matchName": "Panthera leo (Linnaeus, 1758)", "name": "Panthera leo"},
				"refMatchQuality": 5,
				"score": {"total": 15},
				"reference": {
					"titleName": "Systema Naturae",
					"titleId": 42,
					"pageId": 12345,
					"pageNum": 41,
					"url": "https://www.biodiversitylibrary.org/page/12345",
					"volume": "vol. 1",
					"yearAggr": 1758
				}
			},
			{
				"name": {"matchName": "Panthera leo", "name": "Panthera leo"},
				"refMatchQuality": 4,
				"score": {"total": 12},
				"reference": {
					"titleName": "Zoological Journal of the Linnean Society",
					"titleId": 99,
					"pageId": 55555,
					"url": "https://www.biodiversitylibrary.org/page/55555",
					"volume": "vol. 100",
					"yearAggr": 1990,
					"part": {
						"id": 7788,
						"name": "Phylogeny of Panthera",
						"pages": "10-25",
						"year": 1990,
						"doi": "10.0000/example.42"
					}
				}
			}
		]
	}`
	// Verify the request body while we're at it — checks canonical /
	// authors / year are wired through correctly.
	var seenBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/name_refs" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canned))
	}))
	defer srv.Close()

	client := New(srv.URL)
	hits, err := client.LookupName(context.Background(), "Panthera leo", "Linnaeus", 1758, LookupOpts{})
	if err != nil {
		t.Fatalf("LookupName: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits=%d, want 2", len(hits))
	}

	// Request body sanity — the fields the client sends drive the
	// quality of BHLnames scoring, so this pins them.
	name := seenBody["name"].(map[string]any)
	if name["canonical"] != "Panthera leo" || name["authors"] != "Linnaeus" || name["year"].(float64) != 1758 {
		t.Errorf("request body name = %+v", name)
	}
	params := seenBody["params"].(map[string]any)
	if params["refsLimit"].(float64) != 5 {
		t.Errorf("request refsLimit = %v, want default 5", params["refsLimit"])
	}

	// First hit: no `part` → treated as whole item (book).
	h0 := hits[0]
	if h0.Quality != 5 || h0.Score != 15 {
		t.Errorf("h0 quality/score = %d/%d", h0.Quality, h0.Score)
	}
	if h0.Reference.Title != "Systema Naturae" {
		t.Errorf("h0 title = %q", h0.Reference.Title)
	}
	if h0.Reference.ContainerTitle != "" {
		t.Errorf("h0 container should be empty (no part), got %q", h0.Reference.ContainerTitle)
	}
	if h0.Reference.Page != "41" {
		t.Errorf("h0 page = %q, want '41' from PageNum", h0.Reference.Page)
	}
	if h0.Reference.Issued != "1758" {
		t.Errorf("h0 issued = %q", h0.Reference.Issued)
	}
	if h0.Reference.Type.ID() != "BOOK" {
		t.Errorf("h0 type = %q, want BOOK", h0.Reference.Type.ID())
	}

	// Second hit: has `part` → article within a journal.
	h1 := hits[1]
	if h1.Reference.Title != "Phylogeny of Panthera" {
		t.Errorf("h1 title = %q (want part.name)", h1.Reference.Title)
	}
	if h1.Reference.ContainerTitle != "Zoological Journal of the Linnean Society" {
		t.Errorf("h1 container = %q", h1.Reference.ContainerTitle)
	}
	if h1.Reference.Page != "10-25" {
		t.Errorf("h1 page = %q", h1.Reference.Page)
	}
	if h1.Reference.DOI != "10.0000/example.42" {
		t.Errorf("h1 DOI = %q", h1.Reference.DOI)
	}
	if h1.Reference.Type.ID() != "ARTICLE_JOURNAL" {
		t.Errorf("h1 type = %q, want ARTICLE_JOURNAL", h1.Reference.Type.ID())
	}
	if h1.Reference.Volume != "vol. 100" {
		t.Errorf("h1 volume = %q", h1.Reference.Volume)
	}
	if h1.Reference.Author != "Linnaeus" {
		t.Errorf("h1 author = %q, want passed-through input", h1.Reference.Author)
	}
	if h1.PageURL != "https://www.biodiversitylibrary.org/page/55555" {
		t.Errorf("h1 PageURL = %q", h1.PageURL)
	}
}

// TestEmptyCanonical proves the input-validation guard — canonical is
// required, otherwise we'd shoot a garbage request at BHLnames.
func TestEmptyCanonical(t *testing.T) {
	client := New("http://example.invalid")
	_, err := client.LookupName(context.Background(), "   ", "", 0, LookupOpts{})
	if err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Errorf("empty canonical: got %v, want a 'canonical required' error", err)
	}
}

// TestLive hits the real BHLnames service. Skipped by default so
// hive's test suite doesn't depend on network; run with
// `HIVE_BHLNAMES_LIVE=1 go test ./pkg/bhlnames/ -run TestLive`.
//
// Uses Pardosa moesta Banks 1892 — the API doc's own example, so BHL
// definitely has confident matches for it. Runs both flavors:
//
//   - Non-nomen mode: broader match set, no quality score.
//   - Nomen-event mode: strict protologue matching, scored 1-5.
//
// If either query fails outright (network / API error) the test
// fails. Zero hits from nomen mode is not a failure — that's a valid
// "we don't know" answer from BHLnames.
func TestLive(t *testing.T) {
	if os.Getenv("HIVE_BHLNAMES_LIVE") == "" {
		t.Skip("set HIVE_BHLNAMES_LIVE=1 to hit bhlnames.globalnames.org")
	}
	client := New(DefaultBaseURL)
	ctx := context.Background()

	broad, err := client.LookupName(ctx, "Pardosa moesta", "Banks", 1892,
		LookupOpts{RefsLimit: 3})
	if err != nil {
		t.Fatalf("live LookupName (broad): %v", err)
	}
	if len(broad) == 0 {
		t.Fatalf("live broad: no hits for Pardosa moesta — API changed or BHL coverage dropped?")
	}
	t.Logf("live broad: %d hits", len(broad))
	for i, h := range broad {
		t.Logf("  %d. %s (%s) → %s", i+1, h.Reference.Title, h.Reference.Issued, h.PageURL)
	}

	nomen, err := client.LookupName(ctx, "Pardosa moesta", "Banks", 1892,
		LookupOpts{RefsLimit: 3, NomenEvent: true})
	if err != nil {
		t.Fatalf("live LookupName (nomen): %v", err)
	}
	t.Logf("live nomen: %d hits", len(nomen))
	for i, h := range nomen {
		t.Logf("  %d. Q%d/S%d %s (%s) → %s", i+1,
			h.Quality, h.Score, h.Reference.Title, h.Reference.Issued, h.PageURL)
	}
}
