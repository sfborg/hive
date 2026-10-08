package refmanager_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/refmanager"
)

const nsOpen = `<article xmlns:hive="https://hive.sfborg.org/ns/1.0" xmlns:tp="http://example.org/tp"><body>`
const nsClose = `</body></article>`

func doc(body string) []byte { return []byte(nsOpen + body + nsClose) }

// seg builds a segment the way the reader does: block text, the
// selection, and up to 32 characters of context either side.
func seg(blockText, exact string, occurrence int) correction.TextSegment {
	start := -1
	from := 0
	for i := 0; i <= occurrence; i++ {
		k := strings.Index(blockText[from:], exact)
		if k < 0 {
			panic("exact not in block text")
		}
		start = from + k
		from = start + 1
	}
	end := start + len(exact)
	pre := blockText[:start]
	if len(pre) > 32 {
		pre = pre[len(pre)-32:]
	}
	suf := blockText[end:]
	if len(suf) > 32 {
		suf = suf[:32]
	}
	return correction.TextSegment{
		BlockText: blockText, Exact: exact, Prefix: pre, Suffix: suf,
		Start: start, End: end,
	}
}

func removeRange(t *testing.T, jats []byte, id, label string, segs ...correction.TextSegment) []byte {
	t.Helper()
	payload, _ := json.Marshal(correction.TextRemovePayload{Label: label, Segments: segs})
	out, err := refmanager.ApplyTextRemoveToJATS(jats, &correction.Correction{
		ID: id, Kind: correction.KindTextRemove, Payload: string(payload),
	})
	if err != nil {
		t.Fatalf("ApplyTextRemoveToJATS: %v", err)
	}
	mustWellFormed(t, out)
	return out
}

func restore(t *testing.T, jats []byte, id string) []byte {
	t.Helper()
	payload, _ := json.Marshal(correction.TextRestorePayload{RemoveCorrectionID: id})
	out, err := refmanager.ApplyTextRestoreToJATS(jats, &correction.Correction{
		ID: "rs-" + id, Kind: correction.KindTextRestore, Payload: string(payload),
	})
	if err != nil {
		t.Fatalf("ApplyTextRestoreToJATS: %v", err)
	}
	mustWellFormed(t, out)
	return out
}

func mustWellFormed(t *testing.T, b []byte) {
	t.Helper()
	if err := correction.CheckWellFormed(b); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, b)
	}
}

var commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

func live(b []byte) string { return commentRe.ReplaceAllString(string(b), "") }

func TestRangeRemoveRoundTrips(t *testing.T) {
	tests := []struct {
		name, body, blockText, exact string
		occurrence                   int
		wantLive                     string
	}{
		{
			name:      "plain text",
			body:      `<p>Alpha beta gamma.</p>`,
			blockText: "Alpha beta gamma.", exact: "beta ",
			wantLive: `<p>Alpha <hive:noise correction-id="c1" segment="0" label="running-head" original="fragment"></hive:noise>gamma.</p>`,
		},
		{
			name:      "crosses an inline boundary",
			body:      `<p>a <italic>bcd</italic> ef</p>`,
			blockText: "a bcd ef", exact: "d e",
			wantLive: `<p>a <italic>bc</italic><hive:noise correction-id="c1" segment="0" label="running-head" original="fragment"></hive:noise>f</p>`,
		},
		{
			name:      "crosses nested inline elements",
			body:      `<p>x <italic>y <tp:taxon-name full-name="Aus bus">Aus bus</tp:taxon-name></italic> z</p>`,
			blockText: "x y Aus bus z", exact: "bus z",
			wantLive: `<p>x <italic>y <tp:taxon-name full-name="Aus bus">Aus </tp:taxon-name></italic><hive:noise correction-id="c1" segment="0" label="running-head" original="fragment"></hive:noise></p>`,
		},
		{
			name:      "entities and hyphen runs",
			body:      `<p>A &amp; B ---- C</p>`,
			blockText: "A & B ---- C", exact: "& B ----",
			wantLive: `<p>A <hive:noise correction-id="c1" segment="0" label="running-head" original="fragment"></hive:noise> C</p>`,
		},
		{
			name:      "second occurrence picked by context",
			body:      `<p>foo bar foo baz</p>`,
			blockText: "foo bar foo baz", exact: "foo", occurrence: 1,
			wantLive: `<p>foo bar <hive:noise correction-id="c1" segment="0" label="running-head" original="fragment"></hive:noise> baz</p>`,
		},
		{
			name:      "whole block",
			body:      `<p>Header <italic>text</italic></p>`,
			blockText: "Header text", exact: "Header text",
			wantLive: `<hive:noise correction-id="c1" segment="0" label="running-head" original="element"></hive:noise>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := doc(tc.body)
			out := removeRange(t, in, "c1", "running-head", seg(tc.blockText, tc.exact, tc.occurrence))
			if got := live(out); got != nsOpen+tc.wantLive+nsClose {
				t.Errorf("after remove:\n got %s\nwant %s", got, nsOpen+tc.wantLive+nsClose)
			}
			if again := removeRange(t, out, "c1", "running-head", seg(tc.blockText, tc.exact, tc.occurrence)); string(again) != string(out) {
				t.Errorf("second apply changed the document:\n%s", again)
			}
			if back := restore(t, out, "c1"); string(back) != string(in) {
				t.Errorf("restore not lossless:\n got %s\nwant %s", back, in)
			}
		})
	}
}

func TestRangeRemoveAcrossBlocks(t *testing.T) {
	in := doc(`<p>end of one caption</p><p>Start of next. Body continues</p>`)
	out := removeRange(t, in, "c2", "other",
		seg("end of one caption", "one caption", 0),
		seg("Start of next. Body continues", "Start of next. ", 0))
	want := nsOpen + `<p>end of <hive:noise correction-id="c2" segment="0" label="other" original="fragment"></hive:noise></p>` +
		`<p><hive:noise correction-id="c2" segment="1" label="other" original="fragment"></hive:noise>Body continues</p>` + nsClose
	if got := live(out); got != want {
		t.Errorf("after remove:\n got %s\nwant %s", got, want)
	}
	if back := restore(t, out, "c2"); string(back) != string(in) {
		t.Errorf("restore not lossless:\n got %s\nwant %s", back, in)
	}
}

func TestRangeRemoveSkipsDecorationsInBlockText(t *testing.T) {
	// The reader does not show <label>, so block text omits it.
	in := doc(`<p><label>1</label>Running head text here</p>`)
	out := removeRange(t, in, "c3", "running-head", seg("Running head text here", "Running head", 0))
	if !strings.Contains(live(out), `<label>1</label><hive:noise correction-id="c3"`) {
		t.Errorf("label not kept before wrapper:\n%s", out)
	}
}

func TestMoveToCaption(t *testing.T) {
	in := doc(`<p>Body text. Fig. 6 Network of <italic>Aus</italic> strains</p>` +
		`<sec sec-type="figures"><fig id="curator_x" hive:pdf-page="9"><caption><p>figure-6</p></caption></fig></sec>`)
	payload, _ := json.Marshal(correction.TextTagPayload{
		Action:   correction.TextTagMoveToCaption,
		Segments: []correction.TextSegment{seg("Body text. Fig. 6 Network of Aus strains", "Fig. 6 Network of Aus strains", 0)},
	})
	c := &correction.Correction{
		ID: "c4", Kind: correction.KindTextTag, TargetElementID: "curator_x", Payload: string(payload),
	}
	out, err := refmanager.ApplyTextTagToJATS(in, c)
	if err != nil {
		t.Fatalf("ApplyTextTagToJATS: %v", err)
	}
	mustWellFormed(t, out)
	got := live(out)
	if !strings.Contains(got, `<p>Body text. <hive:noise correction-id="c4" segment="0" label="caption" moved-to="curator_x" original="fragment"></hive:noise></p>`) {
		t.Errorf("body not wrapped:\n%s", got)
	}
	if !strings.Contains(got, `<caption><p>figure-6</p><p hive:moved-from="c4">Fig. 6 Network of <italic>Aus</italic> strains</p></caption>`) {
		t.Errorf("caption not appended:\n%s", got)
	}
	if again, _ := refmanager.ApplyTextTagToJATS(out, c); string(again) != string(out) {
		t.Errorf("second apply changed the document")
	}
	if back := restore(t, out, "c4"); string(back) != string(in) {
		t.Errorf("restore not lossless:\n got %s\nwant %s", back, in)
	}
}

func TestMoveToCaptionUnknownFigure(t *testing.T) {
	payload, _ := json.Marshal(correction.TextTagPayload{
		Action:   correction.TextTagMoveToCaption,
		Segments: []correction.TextSegment{seg("Some text", "Some", 0)},
	})
	_, err := refmanager.ApplyTextTagToJATS(doc(`<p>Some text</p>`), &correction.Correction{
		ID: "c5", Kind: correction.KindTextTag, TargetElementID: "nope", Payload: string(payload),
	})
	if err == nil {
		t.Fatal("expected an error for a missing figure")
	}
}

// TestBBoxModifyAttachesRegionToCaptionOnlyFigure covers drawing a
// region for a figure that has a caption but no coordinates: the
// figure gains both hive:pdf-page and hive:pdf-bbox, which image
// extraction needs.
func TestBBoxModifyAttachesRegionToCaptionOnlyFigure(t *testing.T) {
	in := doc(`<sec sec-type="figures"><fig id="fig_3"><caption><p>Fig. 3 A caption</p></caption></fig></sec>`)
	payload, _ := json.Marshal(correction.BBoxModifyPayload{
		Original:  correction.BBoxModifyState{ContentType: "figure", Label: "Figure 3"},
		Corrected: correction.BBoxModifyState{BBox: &correction.BBox{X: 10, Y: 20, W: 300, H: 200}, Page: 6},
	})
	out, err := refmanager.ApplyBBoxModifyToJATS(in, &correction.Correction{
		ID: "c6", Kind: correction.KindBBoxModify, TargetElementID: "fig_3", Payload: string(payload),
	})
	if err != nil {
		t.Fatalf("ApplyBBoxModifyToJATS: %v", err)
	}
	mustWellFormed(t, out)
	want := `<fig id="fig_3" hive:pdf-bbox="10.00,20.00,300.00,200.00" hive:pdf-page="6"><caption><p>Fig. 3 A caption</p></caption></fig>`
	if !strings.Contains(string(out), want) {
		t.Errorf("figure not attached:\n got %s\nwant %s", out, want)
	}
}

func mergeNext(t *testing.T, jats []byte, id string, s correction.TextSegment) []byte {
	t.Helper()
	payload, _ := json.Marshal(correction.TextTagPayload{
		Action: correction.TextTagMergeNext, Segments: []correction.TextSegment{s},
	})
	out, err := refmanager.ApplyTextTagToJATS(jats, &correction.Correction{
		ID: id, Kind: correction.KindTextTag, Payload: string(payload),
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	mustWellFormed(t, out)
	return out
}

// TestMergeNextAcrossPageBreakAndEmptiedParagraph covers a paragraph
// split by a page break, with a removed block and a paragraph whose
// text was all moved away in between.
func TestMergeNextAcrossPageBreakAndEmptiedParagraph(t *testing.T) {
	in := doc(`<sec><p>similar to those of</p>` +
		`<hive:noise correction-id="r1" segment="0" original="element"><!-- <p>Noise</p> --></hive:noise>` +
		`<p><hive:noise correction-id="m1" segment="0" original="fragment"><!-- moved --></hive:noise></p>` +
		`<p content-type="hive-page-break"><hive:page-break page="183"/></p>` +
		`<p><italic>Aus bus</italic>. Next sentence.</p><p>Later paragraph.</p></sec>`)
	out := mergeNext(t, in, "g1", seg("similar to those of", "those", 0))
	want := `<sec><p>similar to those of` +
		`<hive:noise correction-id="r1" segment="0" original="element"></hive:noise>` +
		`<hive:noise correction-id="m1" segment="0" original="fragment"></hive:noise>` +
		`<hive:page-break page="183"/>` +
		`<hive:noise correction-id="g1" segment="0" label="merge" original="merge"></hive:noise>` +
		` <italic>Aus bus</italic>. Next sentence.</p><p>Later paragraph.</p></sec>`
	if got := live(out); got != nsOpen+want+nsClose {
		t.Errorf("after merge:\n got %s\nwant %s", got, nsOpen+want+nsClose)
	}
	if back := restore(t, out, "g1"); string(back) != string(in) {
		t.Errorf("undo not lossless:\n got %s\nwant %s", back, in)
	}
	// A removal that ended up inside the merged paragraph restores
	// inline rather than as a nested paragraph.
	inline := restore(t, out, "r1")
	if !strings.Contains(live(inline), `similar to those ofNoise<hive:noise correction-id="m1"`) {
		t.Errorf("inline restore wrong:\n%s", live(inline))
	}
}

func TestMergeNextNeedsFollowingParagraph(t *testing.T) {
	payload, _ := json.Marshal(correction.TextTagPayload{
		Action:   correction.TextTagMergeNext,
		Segments: []correction.TextSegment{seg("Last one", "Last", 0)},
	})
	_, err := refmanager.ApplyTextTagToJATS(doc(`<sec><p>Last one</p></sec>`), &correction.Correction{
		ID: "g2", Kind: correction.KindTextTag, Payload: string(payload),
	})
	if err == nil {
		t.Fatal("expected an error when no paragraph follows")
	}
}

// TestMergeNextAcrossUntitledSections covers figure text that sat in
// untitled sections of its own between the two halves of a paragraph.
func TestMergeNextAcrossUntitledSections(t *testing.T) {
	in := doc(`<sec><title>Discussion</title><p>similar to those of</p></sec>` +
		`<sec><hive:noise correction-id="r1" segment="0" original="element"><!-- <p>Helix</p> --></hive:noise></sec>` +
		`<sec><p>Continues here.</p><p>Next.</p></sec>`)
	out := mergeNext(t, in, "g3", seg("similar to those of", "those", 0))
	want := `<sec><title>Discussion</title><p>similar to those of` +
		`<hive:noise correction-id="r1" segment="0" original="element"></hive:noise>` +
		`<hive:noise correction-id="g3" segment="0" label="merge" tags="4" original="merge"></hive:noise>` +
		` Continues here.</p></sec><sec></sec><sec><p>Next.</p></sec>`
	if got := live(out); got != nsOpen+want+nsClose {
		t.Errorf("after merge:\n got %s\nwant %s", got, nsOpen+want+nsClose)
	}
	if back := restore(t, out, "g3"); string(back) != string(in) {
		t.Errorf("undo not lossless:\n got %s\nwant %s", back, in)
	}
	_, err := refmanager.ApplyTextTagToJATS(doc(`<sec><p>A para</p></sec><sec><title>Methods</title><p>B</p></sec>`),
		&correction.Correction{ID: "g4", Kind: correction.KindTextTag, Payload: func() string {
			b, _ := json.Marshal(correction.TextTagPayload{Action: correction.TextTagMergeNext,
				Segments: []correction.TextSegment{seg("A para", "A", 0)}})
			return string(b)
		}()})
	if err == nil {
		t.Error("expected merge into a titled section to fail")
	}
}

// TestMergeSelectedParagraphs covers a selection spanning several
// paragraphs: everything from the first through the last becomes one
// paragraph, and one restore undoes it.
func TestMergeSelectedParagraphs(t *testing.T) {
	in := doc(`<sec><p>One ends</p>` +
		`<p content-type="hive-page-break"><hive:page-break page="7"/></p>` +
		`<p>two goes on</p><p>three-</p><p>four ends.</p><p>Five stays.</p></sec>`)
	whole := func(text string) correction.TextSegment {
		return correction.TextSegment{BlockText: text, Exact: text, End: len(text)}
	}
	payload, _ := json.Marshal(correction.TextTagPayload{
		Action:   correction.TextTagMergeNext,
		Segments: []correction.TextSegment{whole("One ends"), whole("four ends.")},
	})
	out, err := refmanager.ApplyTextTagToJATS(in, &correction.Correction{
		ID: "g5", Kind: correction.KindTextTag, Payload: string(payload),
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	mustWellFormed(t, out)
	want := `<sec><p>One ends<hive:page-break page="7"/>` +
		`<hive:noise correction-id="g5" segment="0" label="merge" original="merge"></hive:noise>` +
		` two goes on three-four ends.</p><p>Five stays.</p></sec>`
	if got := live(out); got != nsOpen+want+nsClose {
		t.Errorf("after merge:\n got %s\nwant %s", got, nsOpen+want+nsClose)
	}
	if back := restore(t, out, "g5"); string(back) != string(in) {
		t.Errorf("undo not lossless:\n got %s\nwant %s", back, in)
	}
}
