package jatsconv

import (
	"os/exec"
	"strings"
	"testing"
)

// TestJATSValidates runs every jatsconv fixture through Validate
// and asserts the output validates against schema/hive-jats.rng.
// Tripwire for JATS-shape regressions — when the converter starts
// emitting invalid structure, this test prints the exact
// validator error lines pointing at the offending element.
//
// Skipped when xmllint isn't installed (Validate returns
// Executed=false). Also skipped when schema extraction fails,
// which shouldn't happen in a normal build.
func TestJATSValidates(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not installed; skipping JATS RNG validation")
	}

	for name, tei := range jatsconvFixtures() {
		t.Run(name, func(t *testing.T) {
			jats, err := TEIToJATS(tei)
			if err != nil {
				t.Fatalf("TEIToJATS: %v", err)
			}
			res, err := Validate(jats)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !res.Executed {
				t.Skip("xmllint not available at test time")
			}
			if !res.Valid {
				t.Errorf("xmllint reported invalid JATS:\n%s\n---\n%s",
					strings.Join(res.Errors, "\n"), jats)
			}
		})
	}

	// Extra case: prove the hive-jats overlay accepts tp:taxon-name
	// chips inside <p>. Uses hand-authored augmented JATS so the
	// test doesn't drag in gnfinder/verifier deps just to smoke-
	// test the overlay's phrase.class extension.
	t.Run("with-tp-chips", func(t *testing.T) {
		augmented := []byte(strings.NewReplacer(
			"__TP__", "http://exsansi.pensoft.net/pub-med/pub-med-tp/1.0",
			"__GN__", "https://hive.sfborg.org/ns/gn/1.0",
		).Replace(`<?xml version="1.0" encoding="UTF-8"?>
<article article-type="research-article" dtd-version="1.3" xml:lang="en"
         xmlns:tp="__TP__" xmlns:gn="__GN__">
  <front>
    <journal-meta>
      <journal-id journal-id-type="publisher-id">hive-unknown</journal-id>
      <issn publication-format="print">0000-0000</issn>
    </journal-meta>
    <article-meta>
      <title-group><article-title>Chip smoketest</article-title></title-group>
    </article-meta>
  </front>
  <body>
    <p>We observed <tp:taxon-name full-name="Panthera onca" gn:source="verified" gn:score="0.95">Panthera onca</tp:taxon-name> here.</p>
  </body>
</article>`))
		res, err := Validate(augmented)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if !res.Valid {
			t.Errorf("overlay rejected a tp:taxon-name chip:\n%s\n---\n%s",
				strings.Join(res.Errors, "\n"), augmented)
		}
	})
}

// jatsconvFixtures returns the TEI fixtures used by TestJATSValidates.
// Kept broad enough to exercise the full front/body/back pipeline —
// header conversion, div→sec, listBibl→ref-list — because a
// regression in any of those breaks real GROBID output.
func jatsconvFixtures() map[string][]byte {
	minimal := []byte(`<?xml version="1.0"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
  <teiHeader/>
  <text><body><p>Hello world.</p></body></text>
</TEI>`)

	full := []byte(`<?xml version="1.0"?>
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
  <text><body>
    <div><head>Introduction</head><p>Opening prose about Panthera onca.</p></div>
    <div><head>Methods</head><p>Details of the study.</p></div>
  </body>
  <back>
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

	return map[string][]byte{
		"minimal": minimal,
		"full":    full,
	}
}
