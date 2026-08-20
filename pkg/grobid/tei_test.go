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
