package core

import (
	"errors"
	"strings"
	"testing"
)

// TestBibTeXArticle covers the common @article path with author list,
// journal, volume/issue/pages, month+year → YYYY-MM, doi + url.
func TestBibTeXArticle(t *testing.T) {
	src := `
@article{smith2020,
  title   = {Molecular phylogeny of {Panthera leo}},
  author  = {Smith, John and Doe, Jane and van der Waals, K.},
  journal = {Journal of Cat Studies},
  volume  = {12},
  number  = {3},
  pages   = {45--67},
  year    = {2020},
  month   = mar,
  doi     = {10.0000/example.42},
  url     = {https://example.org/leo},
  note    = {Includes supplementary appendix.},
}
`
	r, err := ParseBibTeX(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := r.Type.ID(); got != "ARTICLE_JOURNAL" {
		t.Errorf("Type = %q, want ARTICLE_JOURNAL", got)
	}
	// Title: the {{Panthera leo}} inner braces should be stripped.
	if r.Title != "Molecular phylogeny of Panthera leo" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Author != "Smith, John; Doe, Jane; van der Waals, K." {
		t.Errorf("Author = %q", r.Author)
	}
	if r.ContainerTitle != "Journal of Cat Studies" {
		t.Errorf("ContainerTitle = %q", r.ContainerTitle)
	}
	if r.Volume != "12" || r.Issue != "3" {
		t.Errorf("Volume/Issue = %q / %q", r.Volume, r.Issue)
	}
	// -- → – normalization via cleanValue.
	if r.Page != "45–67" {
		t.Errorf("Page = %q, want '45–67' (en dash)", r.Page)
	}
	if r.Issued != "2020-03" {
		t.Errorf("Issued = %q, want '2020-03'", r.Issued)
	}
	if r.DOI != "10.0000/example.42" {
		t.Errorf("DOI = %q", r.DOI)
	}
	if r.Link != "https://example.org/leo" {
		t.Errorf("Link = %q", r.Link)
	}
	if r.Remarks != "Includes supplementary appendix." {
		t.Errorf("Remarks = %q", r.Remarks)
	}
}

// TestBibTeXBook covers @book with editor, publisher/place, ISBN,
// year-only Issued.
func TestBibTeXBook(t *testing.T) {
	src := `@book{darwin1859,
  title     = "On the Origin of Species",
  author    = "Darwin, Charles",
  publisher = "John Murray",
  address   = "London",
  year      = "1859",
  isbn      = "0-000-00000-0"
}`
	r, err := ParseBibTeX(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Type.ID() != "BOOK" {
		t.Errorf("Type = %q, want BOOK", r.Type.ID())
	}
	if r.Title != "On the Origin of Species" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Author != "Darwin, Charles" {
		t.Errorf("Author = %q", r.Author)
	}
	if r.Publisher != "John Murray" || r.PublisherPlace != "London" {
		t.Errorf("Publisher/Place = %q / %q", r.Publisher, r.PublisherPlace)
	}
	if r.Issued != "1859" {
		t.Errorf("Issued = %q, want year-only", r.Issued)
	}
	if r.ISBN != "0-000-00000-0" {
		t.Errorf("ISBN = %q", r.ISBN)
	}
}

// TestBibTeXChapter covers @incollection using booktitle for the
// container title.
func TestBibTeXChapter(t *testing.T) {
	src := `@incollection{k2000,
  title = {A chapter about cats},
  author = {Knuth, D.},
  booktitle = {The big cat book},
  editor = {Editor, E.},
  publisher = {Some Press},
  year = {2000},
  pages = {100--120}
}`
	r, err := ParseBibTeX(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Type.ID() != "CHAPTER" {
		t.Errorf("Type = %q, want CHAPTER", r.Type.ID())
	}
	if r.ContainerTitle != "The big cat book" {
		t.Errorf("ContainerTitle = %q (want booktitle)", r.ContainerTitle)
	}
	if r.Editor != "Editor, E." {
		t.Errorf("Editor = %q", r.Editor)
	}
	if r.Page != "100–120" {
		t.Errorf("Page = %q", r.Page)
	}
}

// TestBibTeXDOIStripsPrefix — DOI field is often pasted as a full URL.
func TestBibTeXDOIStripsPrefix(t *testing.T) {
	src := `@article{x, title={t}, doi={https://doi.org/10.0000/example}, year={2020}}`
	r, err := ParseBibTeX(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.DOI != "10.0000/example" {
		t.Errorf("DOI = %q, want stripped", r.DOI)
	}
}

// TestBibTeXMalformed covers the parse-error path so callers see a
// wrapped ErrBibTeXParse (for errors.Is checks).
func TestBibTeXMalformed(t *testing.T) {
	cases := []string{
		"",
		"not bibtex at all",
		"@article",
		"@article{",
		"@article{no_close,",
		"@article{k, field_without_value}",
	}
	for i, s := range cases {
		_, err := ParseBibTeX(s)
		if err == nil {
			t.Errorf("case %d: expected error for %q", i, s)
			continue
		}
		// Empty-input case falls through the "no @" branch; others
		// hit specific parser errors. All wrap ErrBibTeXParse.
		if !errors.Is(err, ErrBibTeXParse) {
			t.Errorf("case %d: err %v is not ErrBibTeXParse", i, err)
		}
	}
}

// TestBibTypeToRefType — direct spot check on the mapping table.
func TestBibTypeToRefType(t *testing.T) {
	cases := map[string]string{
		"article":       "ARTICLE_JOURNAL",
		"book":          "BOOK",
		"incollection":  "CHAPTER",
		"inproceedings": "PAPER_CONFERENCE",
		"phdthesis":     "THESIS",
		"techreport":    "REPORT",
		"unpublished":   "MANUSCRIPT",
		"online":        "WEBPAGE",
		"dataset":       "DATASET",
		"misc":          "",
		"nonsense":      "",
	}
	for in, want := range cases {
		if got := bibTypeToRefType(in); got != want {
			t.Errorf("bibTypeToRefType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBibTeXMonthCoercion covers month-token normalization: three-
// letter, full, numeric, unknown.
func TestBibTeXMonthCoercion(t *testing.T) {
	cases := map[string]string{
		"jan": "01", "January": "01", "1": "01", "01": "01",
		"jun": "06", "june": "06",
		"dec": "12", "December": "12", "12": "12",
		"":         "",
		"whatever": "",
	}
	for in, want := range cases {
		if got := monthNumber(in); got != want {
			t.Errorf("monthNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBibTeXAuthorSplit checks the " and " → "; " conversion and
// preservation of braced groups (e.g. "{van der Waals, K.}").
func TestBibTeXAuthorSplit(t *testing.T) {
	cases := map[string]string{
		"Smith, J.":                    "Smith, J.",
		"Smith, J. and Doe, J.":        "Smith, J.; Doe, J.",
		"Smith, J. AND Doe, J.":        "Smith, J.; Doe, J.",  // case-insensitive
		"{van der Waals, K.}":          "van der Waals, K.",
		"A and B and C":                "A; B; C",
		"":                             "",
	}
	for in, want := range cases {
		if got := bibtexAuthorToColdp(in); got != want {
			t.Errorf("bibtexAuthorToColdp(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBibTeXMultipleEntriesTakesFirst — paste with a stray second
// entry shouldn't blow up; first entry wins per the doc.
func TestBibTeXMultipleEntriesTakesFirst(t *testing.T) {
	src := `
@article{one, title = {First}, year = {2020}}
@article{two, title = {Second}, year = {2021}}
`
	r, err := ParseBibTeX(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Title != "First" {
		t.Errorf("Title = %q, want 'First'", r.Title)
	}
	// Sanity: parser stopped at the first entry cleanly (no crash on
	// the second one) — the strings.TrimPrefix check keeps us out of
	// the second entry entirely.
	if strings.Contains(r.Title, "Second") {
		t.Errorf("leaked second entry: %q", r.Title)
	}
}
