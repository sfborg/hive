package annotator

import (
	"strings"
	"testing"
)

// TestFindNamesWrapsHits round-trips a small TEI fragment through
// the annotator and asserts that GNfinder-detected names inside the
// <text><body> region get wrapped in <tp:taxon-name> elements. The
// fixture uses two well-known binomials that GNfinder recognizes
// with high confidence in every dictionary bundle it ships with.
func TestFindNamesWrapsHits(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader/>
  <text>
    <body>
      <p>Panthera onca is native to the Americas. Homo sapiens is not.</p>
    </body>
  </text>
</TEI>`)
	a := New()
	out, err := a.FindNames(tei)
	if err != nil {
		t.Fatalf("FindNames: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `xmlns:tp="`+TaxPubNamespace+`"`) {
		t.Errorf("output missing TaxPub namespace declaration:\n%s", s)
	}
	for _, want := range []string{
		`full-name="Panthera onca"`,
		`>Panthera onca<`,
		`full-name="Homo sapiens"`,
		`>Homo sapiens<`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

// TestFindNamesSkipsHeader — GNfinder should not run on content
// outside <text>, so a scientific-name-shaped string in the header
// (title, author affiliation, journal name) stays plain. Only body
// content is a valid target for the reader's chip UX.
func TestFindNamesSkipsHeader(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc><titleStmt>
      <title>Notes on Panthera onca in Belize</title>
    </titleStmt></fileDesc>
  </teiHeader>
  <text><body><p>Nothing to see here.</p></body></text>
</TEI>`)
	a := New()
	out, err := a.FindNames(tei)
	if err != nil {
		t.Fatalf("FindNames: %v", err)
	}
	s := string(out)
	// The header's "Panthera onca" should NOT be wrapped.
	// The wrapped form we'd expect if it leaked: <tp:taxon-name ...>Panthera onca</tp:taxon-name>
	// Search for the tp: element around that string specifically.
	if idx := strings.Index(s, "Panthera onca"); idx >= 0 {
		// Find the tag that opens just before "Panthera onca".
		before := s[:idx]
		lastOpen := strings.LastIndex(before, "<")
		if lastOpen >= 0 && strings.HasPrefix(before[lastOpen:], "<tp:taxon-name") {
			t.Errorf("header content was annotated; expected header names to pass through untouched:\n%s", s)
		}
	}
}

// TestFindNamesPassthroughOnNoHits — a body with no scientific names
// should round-trip without adding any <tp:taxon-name> elements
// (though the namespace declaration is still added, which is fine).
func TestFindNamesPassthroughOnNoHits(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <text><body><p>Just some prose about nothing in particular.</p></body></text>
</TEI>`)
	a := New()
	out, err := a.FindNames(tei)
	if err != nil {
		t.Fatalf("FindNames: %v", err)
	}
	if strings.Contains(string(out), "<tp:taxon-name") {
		t.Errorf("unexpected taxon-name markup for name-free body:\n%s", string(out))
	}
}
