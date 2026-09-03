package jatsconv

import (
	"strings"
	"testing"
)

// TestConvertMinimal covers the smallest possible input: a bare TEI
// wrapper with an empty header and one paragraph. Asserts the root
// gets renamed to <article>, the JATS namespace is on the root, and
// the paragraph flows through.
func TestConvertMinimal(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader/>
  <text><body><p>Hello world.</p></body></text>
</TEI>`)
	out, err := TEIToJATS(tei)
	if err != nil {
		t.Fatalf("TEIToJATS: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`<article`,
		`article-type="research-article"`,
		`dtd-version="1.3"`,
		`<front>`,
		`<article-meta>`,
		`<body>`,
		`<p>Hello world.</p>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	// The TEI namespace URI shouldn't leak into the output — the
	// JATS Publishing RNG expects no-namespace elements, and the
	// only prefixed elements are the tp: / gn: extensions.
	if strings.Contains(s, TEINamespace) {
		t.Errorf("TEI namespace URI leaked into JATS output:\n%s", s)
	}
	// Guard against the null-namespace regression: <article> must
	// not carry a default xmlns pointing at the JATS URI.
	if strings.Contains(s, `xmlns="`+JATSNamespace+`"`) {
		t.Errorf("unexpected default xmlns on <article>:\n%s", s)
	}
}

// TestConvertHeaderFull round-trips a realistic GROBID header
// through the converter and asserts every field lands in the
// expected JATS element inside <article-meta>.
func TestConvertHeaderFull(t *testing.T) {
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
                <forename type="middle">M</forename>
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
  <text><body><p>Body.</p></body></text>
</TEI>`)
	out, err := TEIToJATS(tei)
	if err != nil {
		t.Fatalf("TEIToJATS: %v", err)
	}
	s := string(out)
	wants := map[string]string{
		"article title":  `<article-title>A revision of some genus</article-title>`,
		"journal title":  `<journal-title>Journal of Testing</journal-title>`,
		"publisher name": `<publisher-name>Testing Press</publisher-name>`,
		"doi":            `<article-id pub-id-type="doi">10.1234/example.5678</article-id>`,
		"first author":   `<surname>Smith</surname>`,
		"middle initial": `<given-names>Alice M</given-names>`,
		"second author":  `<surname>Jones</surname>`,
		"year":           `<year>2020</year>`,
		"volume":         `<volume>42</volume>`,
		"issue":          `<issue>3</issue>`,
		"fpage":          `<fpage>101</fpage>`,
		"lpage":          `<lpage>125</lpage>`,
	}
	for label, want := range wants {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s (%q):\n%s", label, want, s)
		}
	}
}

// TestConvertDivToSec asserts nested <div><head>…</head><p>…</p></div>
// becomes <sec><title>…</title><p>…</p></sec> with the head-derived
// section title preserved.
func TestConvertDivToSec(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader/>
  <text><body>
    <div><head>Introduction</head><p>Opening remark.</p></div>
    <div><head>Methods</head><p>How we did it.</p></div>
  </body></text>
</TEI>`)
	out, err := TEIToJATS(tei)
	if err != nil {
		t.Fatalf("TEIToJATS: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`<sec>`,
		`<title>Introduction</title>`,
		`<p>Opening remark.</p>`,
		`<title>Methods</title>`,
		`<p>How we did it.</p>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<div>") || strings.Contains(s, "<head>") {
		t.Errorf("TEI element leaked into output:\n%s", s)
	}
}

// TestConvertRefList asserts a listBibl becomes a ref-list, and
// each biblStruct becomes a ref+element-citation with structured
// children in the expected JATS shape.
func TestConvertRefList(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader/>
  <text><back>
    <listBibl>
      <head>References</head>
      <biblStruct xml:id="b0">
        <analytic>
          <title level="a">On some species</title>
          <author>
            <persName>
              <forename type="first">Jane</forename>
              <surname>Doe</surname>
            </persName>
          </author>
        </analytic>
        <monogr>
          <title level="j">Foo Journal</title>
          <imprint>
            <biblScope unit="volume">10</biblScope>
            <biblScope unit="page" from="1" to="20"/>
            <date type="published" when="2019"/>
          </imprint>
        </monogr>
      </biblStruct>
    </listBibl>
  </back></text>
</TEI>`)
	out, err := TEIToJATS(tei)
	if err != nil {
		t.Fatalf("TEIToJATS: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`<ref-list>`,
		`<title>References</title>`,
		`<ref id="b0">`,
		`<element-citation publication-type="journal">`,
		`<person-group person-group-type="author">`,
		`<surname>Doe</surname>`,
		`<given-names>Jane</given-names>`,
		`<article-title>On some species</article-title>`,
		`<source>Foo Journal</source>`,
		`<year>2019</year>`,
		`<volume>10</volume>`,
		`<fpage>1</fpage>`,
		`<lpage>20</lpage>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}

// TestConvertPreservesTaxPubInline asserts that inline tp:taxon-name
// markup already present in the input (unusual — annotator normally
// runs after conversion — but a defensive round-trip is cheap)
// survives the conversion unmangled.
func TestConvertPreservesTaxPubInline(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0" xmlns:tp="http://exsansi.pensoft.net/pub-med/pub-med-tp/1.0">
  <teiHeader/>
  <text><body><p>Consider <tp:taxon-name full-name="Panthera onca">Panthera onca</tp:taxon-name>.</p></body></text>
</TEI>`)
	out, err := TEIToJATS(tei)
	if err != nil {
		t.Fatalf("TEIToJATS: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `<tp:taxon-name full-name="Panthera onca">Panthera onca</tp:taxon-name>`) {
		t.Errorf("tp:taxon-name markup was not preserved:\n%s", s)
	}
	if !strings.Contains(s, `xmlns:tp="`+taxPubNamespace+`"`) {
		t.Errorf("tp namespace declaration missing on root:\n%s", s)
	}
}

// TestSplitPageRange covers the page-range helper directly since
// the same logic serves both header and reference conversion.
func TestSplitPageRange(t *testing.T) {
	cases := []struct{ in, fp, lp string }{
		{"101-125", "101", "125"},
		{"101–125", "101", "125"}, // en-dash
		{"12", "12", ""},
		{"", "", ""},
		{"  50 – 60  ", "50", "60"},
	}
	for _, tc := range cases {
		fp, lp := splitPageRange(tc.in)
		if fp != tc.fp || lp != tc.lp {
			t.Errorf("splitPageRange(%q) = (%q, %q), want (%q, %q)",
				tc.in, fp, lp, tc.fp, tc.lp)
		}
	}
}

// TestSplitAuthors covers the semicolon-separated author parser
// used to feed contrib-group emission.
func TestSplitAuthors(t *testing.T) {
	got := splitAuthors("Alice M Smith; Bob Jones; Cher")
	want := []author{
		{Given: "Alice M", Surname: "Smith"},
		{Given: "Bob", Surname: "Jones"},
		{Given: "", Surname: "Cher"},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("author[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestConvertMissingText asserts the converter emits a well-formed
// stub <body/> when the TEI has no <text> element (some GROBID
// failure modes produce header-only output).
func TestConvertMissingText(t *testing.T) {
	tei := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader/>
</TEI>`)
	out, err := TEIToJATS(tei)
	if err != nil {
		t.Fatalf("TEIToJATS: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "<body/>") {
		t.Errorf("missing stub <body/>:\n%s", s)
	}
}
