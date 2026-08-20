package grobid

import "testing"

// TestParseHeaderFull covers the typical GROBID output for a
// journal article: title, authors, journal, volume, issue, page,
// date, DOI. All fields should populate.
func TestParseHeaderFull(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc>
      <sourceDesc>
        <biblStruct type="header">
          <analytic>
            <title level="a" type="main">A revision of some genus</title>
            <author>
              <persName>
                <forename type="first">Alice</forename>
                <surname>Smith</surname>
              </persName>
            </author>
            <author>
              <persName>
                <forename type="first">Bob</forename>
                <surname>Jones</surname>
              </persName>
            </author>
            <idno type="DOI">10.1234/example.5678</idno>
          </analytic>
          <monogr>
            <title level="j">Journal of Testing</title>
            <imprint>
              <biblScope unit="volume">42</biblScope>
              <biblScope unit="issue">3</biblScope>
              <biblScope unit="page" from="101" to="125"/>
              <date type="published" when="2020-06-15"/>
              <publisher>Testing Press</publisher>
            </imprint>
          </monogr>
        </biblStruct>
      </sourceDesc>
    </fileDesc>
  </teiHeader>
</TEI>`)
	h, err := ParseHeader(tei)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.Title != "A revision of some genus" {
		t.Errorf("Title = %q", h.Title)
	}
	if h.Authors != "Alice Smith; Bob Jones" {
		t.Errorf("Authors = %q", h.Authors)
	}
	if h.DOI != "10.1234/example.5678" {
		t.Errorf("DOI = %q", h.DOI)
	}
	if h.ContainerTitle != "Journal of Testing" {
		t.Errorf("ContainerTitle = %q", h.ContainerTitle)
	}
	if h.Volume != "42" {
		t.Errorf("Volume = %q", h.Volume)
	}
	if h.Issue != "3" {
		t.Errorf("Issue = %q", h.Issue)
	}
	if h.Page != "101-125" {
		t.Errorf("Page = %q", h.Page)
	}
	if h.Issued != "2020" {
		t.Errorf("Issued = %q", h.Issued)
	}
	if h.Publisher != "Testing Press" {
		t.Errorf("Publisher = %q", h.Publisher)
	}
}

// TestParseHeaderIgnoresReferenceBiblStructs — GROBID emits
// biblStructs for every cited reference in the paper's References
// section. Header parsing should only pick up the one with
// type="header" and ignore the rest.
func TestParseHeaderIgnoresReferenceBiblStructs(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc>
      <sourceDesc>
        <biblStruct type="header">
          <analytic>
            <title>Header Title</title>
            <idno type="DOI">10.1234/header</idno>
          </analytic>
        </biblStruct>
      </sourceDesc>
    </fileDesc>
  </teiHeader>
  <text>
    <back>
      <div type="references">
        <biblStruct>
          <analytic>
            <title>Reference Title</title>
            <idno type="DOI">10.5678/reference</idno>
          </analytic>
        </biblStruct>
      </div>
    </back>
  </text>
</TEI>`)
	h, err := ParseHeader(tei)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.Title != "Header Title" {
		t.Errorf("Title = %q, want 'Header Title' (not reference)", h.Title)
	}
	if h.DOI != "10.1234/header" {
		t.Errorf("DOI = %q, want header DOI (not reference DOI)", h.DOI)
	}
}

func TestParseHeaderYearOnly(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader><fileDesc><sourceDesc>
    <biblStruct type="header">
      <monogr>
        <imprint>
          <date type="published" when="2018"/>
        </imprint>
      </monogr>
    </biblStruct>
  </sourceDesc></fileDesc></teiHeader>
</TEI>`)
	h, _ := ParseHeader(tei)
	if h.Issued != "2018" {
		t.Errorf("Issued = %q, want '2018'", h.Issued)
	}
}

func TestParseHeaderEmptyReturnsEmptyStruct(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?><TEI xmlns="http://www.tei-c.org/ns/1.0"/>`)
	h, err := ParseHeader(tei)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil header, got nil")
	}
	if h.Title != "" || h.DOI != "" || h.Authors != "" {
		t.Errorf("expected empty fields, got %+v", h)
	}
}

// TestParseHeaderNoTypeAttribute covers GROBID output where the
// header biblStruct is emitted without a type="header" attribute
// (the common case in real 0.8.x GROBID output). ParseHeader must
// still recognize it as the paper's own metadata via its position
// inside <teiHeader>, not by requiring the attribute — otherwise
// title / authors / DOI all silently return empty and downstream
// title-mismatch checks against the extracted DOI can't fire.
func TestParseHeaderNoTypeAttribute(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc>
      <sourceDesc>
        <biblStruct>
          <analytic>
            <author><persName>
              <forename type="first">Jane</forename>
              <surname>Doe</surname>
            </persName></author>
            <title level="a" type="main">A paper about beetles</title>
          </analytic>
          <idno type="DOI">10.1234/beetles</idno>
        </biblStruct>
      </sourceDesc>
    </fileDesc>
  </teiHeader>
</TEI>`)
	h, err := ParseHeader(tei)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.Title != "A paper about beetles" {
		t.Errorf("Title = %q", h.Title)
	}
	if h.Authors != "Jane Doe" {
		t.Errorf("Authors = %q", h.Authors)
	}
	if h.DOI != "10.1234/beetles" {
		t.Errorf("DOI = %q", h.DOI)
	}
}

// TestParseHeaderMultipleForenames covers GROBID's multi-<forename>
// output (typically type="first" + type="middle"). Regression case
// from a real PDF where GROBID misfiled an author-separator
// character ("Douglass ; Miller") into the middle-name slot; the
// parser should keep only the given name, not overwrite it.
func TestParseHeaderMultipleForenames(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader><fileDesc><sourceDesc>
    <biblStruct>
      <analytic>
        <author><persName>
          <forename type="first">Douglass</forename>
          <forename type="middle">;</forename>
          <surname>Miller</surname>
        </persName></author>
      </analytic>
    </biblStruct>
  </sourceDesc></fileDesc></teiHeader>
</TEI>`)
	h, _ := ParseHeader(tei)
	if h.Authors != "Douglass Miller" {
		t.Errorf("Authors = %q, want 'Douglass Miller'", h.Authors)
	}
}

// TestExtractDOIIgnoresReferenceSection is the whole point of
// bounding the DOI scan to <teiHeader>: a paper that itself has no
// DOI but whose References section cites papers-with-DOIs should
// return empty, not a random citation DOI (which would trigger an
// OpenAlex fetch of the wrong paper).
func TestExtractDOIIgnoresReferenceSection(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc>
      <sourceDesc>
        <biblStruct type="header">
          <analytic>
            <title>Paper with no DOI</title>
          </analytic>
        </biblStruct>
      </sourceDesc>
    </fileDesc>
  </teiHeader>
  <text>
    <back>
      <div type="references">
        <listBibl>
          <biblStruct>
            <analytic>
              <idno type="DOI">10.1234/cited.reference.one</idno>
            </analytic>
          </biblStruct>
          <biblStruct>
            <analytic>
              <idno type="DOI">10.5678/cited.reference.two</idno>
            </analytic>
          </biblStruct>
        </listBibl>
      </div>
    </back>
  </text>
</TEI>`)
	if got := ExtractDOI(tei); got != "" {
		t.Errorf("ExtractDOI = %q, want empty (cited reference DOI leaked past header boundary)", got)
	}
}

func TestExtractDOIHeaderIdno(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc>
      <sourceDesc>
        <biblStruct type="header">
          <analytic>
            <idno type="DOI">10.1080/00222935709487884</idno>
          </analytic>
        </biblStruct>
      </sourceDesc>
    </fileDesc>
  </teiHeader>
  <text><back>
    <div type="references">
      <biblStruct><analytic>
        <idno type="DOI">10.9999/should.not.win</idno>
      </analytic></biblStruct>
    </div>
  </back></text>
</TEI>`)
	if got := ExtractDOI(tei); got != "10.1080/00222935709487884" {
		t.Errorf("ExtractDOI = %q, want header DOI", got)
	}
}

// TestExtractDOIHeaderFreeText covers GROBID output where the DOI
// appears in the header as free text (e.g. inside <note> or an
// author affiliation) without a typed <idno> wrapper. The regex
// fallback should still find it — but only within the header slice.
func TestExtractDOIHeaderFreeText(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader>
    <fileDesc><sourceDesc><biblStruct type="header">
      <note>doi: 10.1234/paper.own.doi retrieved 2026-01-01.</note>
    </biblStruct></sourceDesc></fileDesc>
  </teiHeader>
  <text><back><biblStruct><analytic>
    <idno type="DOI">10.9999/reference.should.not.win</idno>
  </analytic></biblStruct></back></text>
</TEI>`)
	if got := ExtractDOI(tei); got != "10.1234/paper.own.doi" {
		t.Errorf("ExtractDOI = %q, want header free-text DOI", got)
	}
}

func TestExtractDOITrailingPunct(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader><fileDesc><sourceDesc><biblStruct type="header">
    <note>See 10.1234/foo.bar).</note>
  </biblStruct></sourceDesc></fileDesc></teiHeader>
</TEI>`)
	if got := ExtractDOI(tei); got != "10.1234/foo.bar" {
		t.Errorf("ExtractDOI = %q, want trailing-punct stripped", got)
	}
}

func TestExtractDOIEmptyOnNoHeader(t *testing.T) {
	// No <teiHeader> anchor → refuse rather than scan the whole
	// document (which would leak reference DOIs).
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <text><back>
    <biblStruct><analytic>
      <idno type="DOI">10.9999/leaked</idno>
    </analytic></biblStruct>
  </back></text>
</TEI>`)
	if got := ExtractDOI(tei); got != "" {
		t.Errorf("ExtractDOI = %q, want empty on missing header", got)
	}
}

func TestCollapseSpaces(t *testing.T) {
	cases := []struct{ in, want string }{
		{"simple", "simple"},
		{"  padded  ", "padded"},
		{"line\nbreak", "line break"},
		{"multiple\n\n\tspaces", "multiple spaces"},
	}
	for _, c := range cases {
		got := collapseSpaces(c.in)
		if got != c.want {
			t.Errorf("collapseSpaces(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
