package openalex

import (
	"encoding/json"
	"testing"
)

// TestToReference exercises the JSON → coldp.Reference mapping without
// hitting the network. The fixture is a trimmed
// GET /works/doi:10.7717/peerj.4375 response — real fields, fake
// values, enough to cover the mapping paths.
func TestToReference(t *testing.T) {
	const fixture = `{
		"id": "https://openalex.org/W2741809807",
		"doi": "https://doi.org/10.7717/peerj.4375",
		"title": "The state of OA",
		"display_name": "The state of OA",
		"publication_year": 2018,
		"publication_date": "2018-02-13",
		"type": "article",
		"authorships": [
			{"raw_author_name": "Piwowar, Heather"},
			{"raw_author_name": "Priem, Jason"}
		],
		"primary_location": {
			"landing_page_url": "https://peerj.com/articles/4375",
			"source": {
				"display_name": "PeerJ",
				"type": "journal",
				"issn": ["2167-8359"],
				"host_organization_name": "PeerJ"
			}
		},
		"biblio": {
			"volume": "6",
			"first_page": "e4375",
			"last_page": null
		}
	}`
	var w work
	if err := json.Unmarshal([]byte(fixture), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r := w.toReference()

	if r.DOI != "10.7717/peerj.4375" {
		t.Errorf("DOI = %q, want bare identifier", r.DOI)
	}
	if r.Title != "The state of OA" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Author != "Piwowar, Heather; Priem, Jason" {
		t.Errorf("Author = %q", r.Author)
	}
	if r.Issued != "2018-02-13" {
		t.Errorf("Issued = %q, want full date", r.Issued)
	}
	if r.ContainerTitle != "PeerJ" {
		t.Errorf("ContainerTitle = %q", r.ContainerTitle)
	}
	if r.Volume != "6" || r.Page != "e4375" {
		t.Errorf("Volume/Page = %q / %q", r.Volume, r.Page)
	}
	if r.ISSN != "2167-8359" {
		t.Errorf("ISSN = %q", r.ISSN)
	}
	if r.Link != "https://peerj.com/articles/4375" {
		t.Errorf("Link = %q", r.Link)
	}
	if got := r.Type.ID(); got != "ARTICLE_JOURNAL" {
		t.Errorf("Type = %q, want ARTICLE_JOURNAL", got)
	}
}

// TestMapType covers the OpenAlex → sfga reference_type translation.
// Unknown types map to empty (curator picks).
func TestMapType(t *testing.T) {
	cases := []struct {
		oa, container, want string
	}{
		{"article", "journal", "ARTICLE_JOURNAL"},
		{"article", "repository", "ARTICLE"},
		{"book-chapter", "", "CHAPTER"},
		{"book", "", "BOOK"},
		{"dissertation", "", "THESIS"},
		{"dataset", "", "DATASET"},
		{"conference-paper", "", "PAPER_CONFERENCE"},
		{"preprint", "", "ARTICLE"},
		{"nonsense", "", ""},
	}
	for _, c := range cases {
		if got := mapType(c.oa, c.container); got != c.want {
			t.Errorf("mapType(%q, %q) = %q, want %q",
				c.oa, c.container, got, c.want)
		}
	}
}

// TestUserAgent covers the polite-pool signal shape.
// TestRepairTagBoundarySpacing covers the JATS-to-string conversion
// bug seen at Taylor & Francis (and other publishers of digitized
// backfile content) where spaces around italic tag boundaries get
// eaten. The exact malformed title in the first case comes from
// OpenAlex/CrossRef for DOI 10.1080/00222935709487884.
func TestRepairTagBoundarySpacing(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			`X.—<i>List of</i>Coleoptera<i>received from Old Calabar, on the West Coast of Africa</i>`,
			`X.—<i>List of</i> Coleoptera <i>received from Old Calabar, on the West Coast of Africa</i>`,
		},
		// Already-clean input is a no-op.
		{
			`<i>List of</i> Coleoptera <i>received from</i>`,
			`<i>List of</i> Coleoptera <i>received from</i>`,
		},
		// Tag-free input is a no-op.
		{
			`A plain title with no markup`,
			`A plain title with no markup`,
		},
		// Non-italic tags (sub / sup) legitimately attach — leave alone.
		{
			`CO<sub>2</sub> flux`,
			`CO<sub>2</sub> flux`,
		},
		// Diacritics on either side of the boundary.
		{
			`<i>Über</i>Käfer<i>aus</i>`,
			`<i>Über</i> Käfer <i>aus</i>`,
		},
		// Multiple runs, idempotent.
		{
			`<i>A</i>B<i>C</i>D<em>E</em>`,
			`<i>A</i> B <i>C</i> D <em>E</em>`,
		},
	}
	for _, c := range cases {
		got := repairTagBoundarySpacing(c.in)
		if got != c.want {
			t.Errorf("repairTagBoundarySpacing(%q)\n  got  %q\n  want %q", c.in, got, c.want)
		}
	}
}

// TestTitleCaseIfShouting covers publisher-uppercased author name
// repair. Only fires when the input has zero lowercase letters —
// mixed-case input passes through untouched.
func TestTitleCaseIfShouting(t *testing.T) {
	cases := []struct{ in, want string }{
		// Publisher-shouted: repaired.
		{"DOUGLASS R. MILLER", "Douglass R. Miller"},
		{"JOHN A. DAVIDSON", "John A. Davidson"},
		// Particle handling: van/de/von etc. stay lowercase after
		// the first word.
		{"JOHN VAN DER BERG", "John van der Berg"},
		{"MARIA DE LA CRUZ", "Maria de la Cruz"},
		{"HANS VON HUMBOLDT", "Hans von Humboldt"},
		// Mc prefix.
		{"PIERRE MCDONALD", "Pierre McDonald"},
		// Already mixed-case: no-op.
		{"Douglass R. Miller", "Douglass R. Miller"},
		{"John de Olso", "John de Olso"},
		// Curator's edge case: genuinely-capital "De" gets
		// preserved because the input is mixed-case.
		{"John De Olso", "John De Olso"},
		// Empty and pure-punctuation: no-op.
		{"", ""},
		{"; ,", "; ,"},
	}
	for _, c := range cases {
		got := titleCaseIfShouting(c.in)
		if got != c.want {
			t.Errorf("titleCaseIfShouting(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUserAgent(t *testing.T) {
	withEmail := New("curator@example.org").userAgent()
	if withEmail != "hive/0.1 (mailto:curator@example.org)" {
		t.Errorf("with-email UA = %q", withEmail)
	}
	anon := New("").userAgent()
	if anon != "hive/0.1 (+https://github.com/sfborg/hive)" {
		t.Errorf("anonymous UA = %q", anon)
	}
}
