package refmanager_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/refmanager"
	"github.com/sfborg/hive/pkg/refmanager/pdftext"
)

func pageNumbering(t *testing.T, jats []byte, first int) []byte {
	t.Helper()
	payload, _ := json.Marshal(correction.PageNumberingPayload{PDFPage1: first})
	out, err := refmanager.ApplyPageNumberingToJATS(jats, &correction.Correction{
		ID: "pn", Kind: correction.KindPageNumbering, Payload: string(payload),
	})
	if err != nil {
		t.Fatalf("ApplyPageNumberingToJATS: %v", err)
	}
	mustWellFormed(t, out)
	return out
}

func TestPageNumberingRenumbersFromPDFIndex(t *testing.T) {
	in := []byte(`<article xmlns:hive="https://hive.sfborg.org/ns/1.0"><front><article-meta><fpage>403</fpage><lpage>416</lpage></article-meta></front><body>` +
		`<p content-type="hive-page-break"><hive:page-break page="2"/></p><p>x</p>` +
		`<p content-type="hive-page-break"><hive:page-break page="14" placement="approximate"/></p></body>` +
		`<back><ref-list><ref><element-citation><fpage>12</fpage><lpage>30</lpage></element-citation></ref></ref-list></back></article>`)
	out := string(pageNumbering(t, in, 175))
	for _, want := range []string{
		`<article xmlns:hive="https://hive.sfborg.org/ns/1.0" hive:pdf-page-offset="174">`,
		`<hive:page-break page="176"/>`,
		`<hive:page-break page="188" placement="approximate"/>`,
		`<fpage>175</fpage><lpage>188</lpage>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if !strings.Contains(out, `<fpage>12</fpage><lpage>30</lpage>`) {
		t.Errorf("reference-list pages changed:\n%s", out)
	}
	// Setting it again starts from the PDF index, not the printed
	// numbers it already holds.
	again := string(pageNumbering(t, []byte(out), 1))
	if !strings.Contains(again, `<hive:page-break page="2"/>`) || !strings.Contains(again, `hive:pdf-page-offset="0"`) {
		t.Errorf("renumbering not idempotent from the PDF index:\n%s", again)
	}
}

func TestJournalPageOfPDFPage1Forms(t *testing.T) {
	for word, want := range map[string]int{
		"175-188":    175,
		"175–188":    175,
		"82:175–188": 175,
		"83-93.":     83,
		"9–17%":      0,
		"2020":       0,
	} {
		got := pdftext.JournalPageOfPDFPage1([]pdftext.WordPage{{Text: word, Page: 1}})
		if got != want {
			t.Errorf("%q: got %d, want %d", word, got, want)
		}
	}
}

func TestPageNumberingAddsMissingPages(t *testing.T) {
	in := []byte(`<article xmlns:hive="https://hive.sfborg.org/ns/1.0"><front><article-meta><pub-date><year>2020</year></pub-date><abstract><p>A</p></abstract></article-meta></front>` +
		`<body><p content-type="hive-page-break"><hive:page-break page="3"/></p></body></article>`)
	out := string(pageNumbering(t, in, 175))
	if !strings.Contains(out, `</pub-date><fpage>175</fpage><lpage>177</lpage><abstract>`) {
		t.Errorf("pages not inserted before the abstract:\n%s", out)
	}
}
