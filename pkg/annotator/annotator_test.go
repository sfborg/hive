package annotator

import (
	"context"
	"strings"
	"testing"

	"github.com/gnames/gnlib/ent/gnvers"
	vlib "github.com/gnames/gnlib/ent/verifier"
	"github.com/gnames/gnquery/ent/search"
	gnverifier "github.com/gnames/gnverifier/pkg"
	gnverifierconfig "github.com/gnames/gnverifier/pkg/config"
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

// stubVerifier satisfies the GNverifier interface for tests. Only
// VerifyBatch is exercised by ExpandAbbreviations; the other
// methods panic if called so accidental use surfaces immediately.
type stubVerifier struct {
	verified map[string]bool // names this stub reports as verified
}

func (s *stubVerifier) VerifyBatch(_ context.Context, names []string) []vlib.Name {
	out := make([]vlib.Name, len(names))
	for i, name := range names {
		if s.verified[name] {
			out[i] = vlib.Name{
				Name:      name,
				MatchType: vlib.Exact,
				BestResult: &vlib.ResultData{
					MatchedCanonicalFull: name,
					SortScore:            0.95,
				},
			}
		} else {
			out[i] = vlib.Name{Name: name, MatchType: vlib.NoMatch}
		}
	}
	return out
}

func (*stubVerifier) VerifyOne(string) (vlib.Name, error) { panic("unused") }
func (*stubVerifier) VerifyStream(context.Context, <-chan []string, chan []vlib.Name) {
	panic("unused")
}
func (*stubVerifier) Search(context.Context, search.Input) ([]vlib.Name, error) {
	panic("unused")
}
func (*stubVerifier) ChangeConfig(...gnverifierconfig.Option) gnverifier.GNverifier {
	panic("unused")
}
func (*stubVerifier) Config() gnverifierconfig.Config          { panic("unused") }
func (*stubVerifier) DataSources() ([]vlib.DataSource, error)  { panic("unused") }
func (*stubVerifier) DataSource(int) (vlib.DataSource, error)  { panic("unused") }
func (*stubVerifier) NameString(vlib.NameStringInput) (vlib.NameStringOutput, error) {
	panic("unused")
}
func (*stubVerifier) GetVersion() gnvers.Version { panic("unused") }

// TestExpandAbbreviationsSingleVerifiedCandidate covers the happy
// path: "C. strepitans" appears alongside "Culex strepitans"
// elsewhere in the paper, GNverifier confirms "Culex strepitans"
// as a real name, and the abbreviated element's full-name is
// promoted with gn:source="verified" and a score attribute.
func TestExpandAbbreviationsSingleVerifiedCandidate(t *testing.T) {
	// Input mimics what FindNames would emit: <tp:taxon-name> around
	// both the full and abbreviated forms.
	augmented := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0" xmlns:tp="` + TaxPubNamespace + `">
  <text><body>
    <p><tp:taxon-name full-name="Culex strepitans">Culex strepitans</tp:taxon-name>
       is a mosquito. Later we call it
       <tp:taxon-name full-name="C. strepitans">C. strepitans</tp:taxon-name>.</p>
  </body></text>
</TEI>`)
	a := New()
	a.SetVerifier(&stubVerifier{
		verified: map[string]bool{"Culex strepitans": true},
	})
	out, err := a.ExpandAbbreviations(context.Background(), augmented)
	if err != nil {
		t.Fatalf("ExpandAbbreviations: %v", err)
	}
	s := string(out)
	// The abbreviated element should be rewritten with full name +
	// verification metadata. The full-form element stays as-is.
	if !strings.Contains(s, `full-name="Culex strepitans"`) {
		t.Errorf("expected full-name promoted to Culex strepitans:\n%s", s)
	}
	if strings.Contains(s, `full-name="C. strepitans"`) {
		t.Errorf("abbreviated form should have been rewritten:\n%s", s)
	}
	if !strings.Contains(s, `source="verified"`) {
		t.Errorf("expected source=verified attribute:\n%s", s)
	}
}

// TestExpandAbbreviationsAmbiguous — two full genera share the
// abbreviation prefix and both verify, so the annotator keeps the
// abbreviated form and marks candidates for curator review.
func TestExpandAbbreviationsAmbiguous(t *testing.T) {
	augmented := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0" xmlns:tp="` + TaxPubNamespace + `">
  <text><body>
    <p><tp:taxon-name full-name="Culex pipiens">Culex pipiens</tp:taxon-name>,
       <tp:taxon-name full-name="Culicoides pipiens">Culicoides pipiens</tp:taxon-name>,
       and <tp:taxon-name full-name="C. pipiens">C. pipiens</tp:taxon-name>.</p>
  </body></text>
</TEI>`)
	a := New()
	a.SetVerifier(&stubVerifier{
		verified: map[string]bool{
			"Culex pipiens":      true,
			"Culicoides pipiens": true,
		},
	})
	out, err := a.ExpandAbbreviations(context.Background(), augmented)
	if err != nil {
		t.Fatalf("ExpandAbbreviations: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `source="ambiguous"`) {
		t.Errorf("expected source=ambiguous:\n%s", s)
	}
	if !strings.Contains(s, "Culex pipiens") || !strings.Contains(s, "Culicoides pipiens") {
		t.Errorf("candidates attribute missing options:\n%s", s)
	}
	// full-name shouldn't have been promoted since we couldn't
	// disambiguate. The abbreviated form persists.
	if !strings.Contains(s, `full-name="C. pipiens"`) {
		t.Errorf("abbreviated form should persist when ambiguous:\n%s", s)
	}
}

// TestExpandAbbreviationsUnresolved — no in-document full-form
// genus starts with the abbreviation prefix. Marked as unresolved;
// no verifier call needed.
func TestExpandAbbreviationsUnresolved(t *testing.T) {
	augmented := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0" xmlns:tp="` + TaxPubNamespace + `">
  <text><body>
    <p><tp:taxon-name full-name="Homo sapiens">Homo sapiens</tp:taxon-name>
       and <tp:taxon-name full-name="Q. rex">Q. rex</tp:taxon-name>.</p>
  </body></text>
</TEI>`)
	a := New()
	a.SetVerifier(&stubVerifier{})
	out, err := a.ExpandAbbreviations(context.Background(), augmented)
	if err != nil {
		t.Fatalf("ExpandAbbreviations: %v", err)
	}
	if !strings.Contains(string(out), `source="unresolved"`) {
		t.Errorf("expected source=unresolved:\n%s", string(out))
	}
}

// TestExpandAbbreviationsUnverified — a candidate expansion exists
// (matching prefix) but GNverifier doesn't recognize it. Marked as
// unverified so a curator can inspect.
func TestExpandAbbreviationsUnverified(t *testing.T) {
	augmented := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0" xmlns:tp="` + TaxPubNamespace + `">
  <text><body>
    <p><tp:taxon-name full-name="Culex pipiens">Culex pipiens</tp:taxon-name>
       and <tp:taxon-name full-name="C. bogus">C. bogus</tp:taxon-name>.</p>
  </body></text>
</TEI>`)
	a := New()
	a.SetVerifier(&stubVerifier{}) // nothing verifies
	out, err := a.ExpandAbbreviations(context.Background(), augmented)
	if err != nil {
		t.Fatalf("ExpandAbbreviations: %v", err)
	}
	if !strings.Contains(string(out), `source="unverified"`) {
		t.Errorf("expected source=unverified:\n%s", string(out))
	}
}

// TestExpandAbbreviationsFromUninomial — a paper introduces the
// genus as a standalone uninomial ("the genus Coccus") and then
// switches to abbreviated binomials. The uninomial should be a
// valid candidate source for the expansion.
func TestExpandAbbreviationsFromUninomial(t *testing.T) {
	augmented := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0" xmlns:tp="` + TaxPubNamespace + `">
  <text><body>
    <p>The genus <tp:taxon-name full-name="Coccus">Coccus</tp:taxon-name>
       contains <tp:taxon-name full-name="C. bus">C. bus</tp:taxon-name>
       and <tp:taxon-name full-name="C. dus">C. dus</tp:taxon-name>.</p>
  </body></text>
</TEI>`)
	a := New()
	a.SetVerifier(&stubVerifier{
		verified: map[string]bool{
			"Coccus bus": true,
			"Coccus dus": true,
		},
	})
	out, err := a.ExpandAbbreviations(context.Background(), augmented)
	if err != nil {
		t.Fatalf("ExpandAbbreviations: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `full-name="Coccus bus"`) {
		t.Errorf("expected C. bus expanded to Coccus bus:\n%s", s)
	}
	if !strings.Contains(s, `full-name="Coccus dus"`) {
		t.Errorf("expected C. dus expanded to Coccus dus:\n%s", s)
	}
}

func TestExpansionCandidates(t *testing.T) {
	full := map[string]bool{"Culex": true, "Culicoides": true, "Chionaspis": true}
	got := expansionCandidates("C. pipiens", full)
	want := map[string]bool{
		"Culex pipiens":      true,
		"Culicoides pipiens": true,
		"Chionaspis pipiens": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d: %v", len(got), len(want), got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected candidate %q", g)
		}
	}
	// Multi-letter prefix like "Cx." only matches genera starting
	// with those exact letters.
	got2 := expansionCandidates("Cx. pipiens", map[string]bool{
		"Culex": true, "Cxena": true, "Culicoides": true,
	})
	if len(got2) != 1 || got2[0] != "Cxena pipiens" {
		t.Errorf("Cx.-prefix expansion = %v, want [Cxena pipiens]", got2)
	}
	// Uninomial (no space) has no expansion.
	if got3 := expansionCandidates("C", full); got3 != nil {
		t.Errorf("uninomial should not yield candidates, got %v", got3)
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
