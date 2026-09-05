package hive

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/refmanager"
)

// TestCorrectionRoundTrip covers Create → List → Get → Delete on
// the storage layer for a representative bbox-add correction.
// Verifies that BBox, AnchorPtr, and audit fields survive a
// round-trip through the DB (JSON marshal/unmarshal, null
// handling).
func TestCorrectionRoundTrip(t *testing.T) {
	ctx := WithActor(context.Background(), "test-actor")
	path := filepath.Join(t.TempDir(), "correction.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// Seed a reference so foreign-key-shaped queries have a target.
	if _, err := a.db.ExecContext(ctx,
		`INSERT INTO reference (col__id, col__citation) VALUES ('ref-1', 'citation')`,
	); err != nil {
		t.Fatalf("insert reference: %v", err)
	}

	c := &correction.Correction{
		ReferenceID: "ref-1",
		SidecarSHA:  "sha-1",
		Kind:        correction.KindBBoxAdd,
		ContentType: "table",
		BBoxPage:    4,
		BBox:        &correction.BBox{X: 72.0, Y: 100.0, W: 400.0, H: 150.0},
		Label:       "table-1",
		Payload:     "{}",
	}
	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.CreateCorrection(c)
	}); err != nil {
		t.Fatalf("CreateCorrection: %v", err)
	}
	if c.ID == "" {
		t.Fatalf("expected UUID to be generated, got empty")
	}
	if c.CreatedBy != "test-actor" {
		t.Errorf("CreatedBy = %q, want test-actor", c.CreatedBy)
	}

	got, err := a.GetCorrection(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCorrection: %v", err)
	}
	if got.ContentType != "table" || got.BBoxPage != 4 || got.BBox == nil {
		t.Errorf("Get round-trip mismatch: %+v", got)
	}
	if got.BBox.W != 400.0 {
		t.Errorf("BBox.W round-trip = %v, want 400.0", got.BBox.W)
	}
	if got.Status != correction.StatusActive {
		t.Errorf("default status = %q, want active", got.Status)
	}

	list, err := a.ListCorrectionsForReference(ctx, "ref-1")
	if err != nil {
		t.Fatalf("ListCorrectionsForReference: %v", err)
	}
	if len(list) != 1 || list[0].ID != c.ID {
		t.Errorf("list mismatch: %+v", list)
	}

	if err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.DeleteCorrection(c.ID)
	}); err != nil {
		t.Fatalf("DeleteCorrection: %v", err)
	}
	if _, err := a.GetCorrection(ctx, c.ID); err != ErrNotFound {
		t.Errorf("post-delete Get: got err %v, want ErrNotFound", err)
	}
}

// TestApplyBBoxAddInsertsIntoExistingFiguresSec covers the common
// case: paper has GROBID-extracted figures, so the trailing
// figures-and-tables sec already exists — the new element appends
// inside just before its </sec>.
func TestApplyBBoxAddInsertsIntoExistingFiguresSec(t *testing.T) {
	jats := []byte(`<article><body>
<sec><title>Body</title><p>text</p></sec>
<sec sec-type="figures"><title>Figures and tables</title>
<fig id="fig_0"><caption><p>Figure 1.</p></caption></fig>
</sec>
</body></article>`)
	c := &correction.Correction{
		ID:          "uuid-1234",
		Kind:        correction.KindBBoxAdd,
		ContentType: "table",
		BBoxPage:    5,
		BBox:        &correction.BBox{X: 72, Y: 100, W: 400, H: 150},
		Label:       "table-1",
	}
	out, err := refmanager.ApplyBBoxAddToJATS(jats, c)
	if err != nil {
		t.Fatalf("ApplyBBoxAddToJATS: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<table-wrap id="curator_uuid1234"`) {
		t.Errorf("missing new table-wrap element:\n%s", got)
	}
	if !strings.Contains(got, `hive:pdf-page="5"`) {
		t.Errorf("missing hive:pdf-page:\n%s", got)
	}
	if !strings.Contains(got, `hive:pdf-bbox="72.00,100.00,400.00,150.00"`) {
		t.Errorf("missing hive:pdf-bbox:\n%s", got)
	}
	// Placement: new element sits after fig_0 and before </sec>
	figIdx := strings.Index(got, `<fig id="fig_0"`)
	newIdx := strings.Index(got, `<table-wrap id="curator_uuid1234"`)
	secCloseIdx := strings.Index(got, "</sec>\n</body>")
	if !(figIdx < newIdx && newIdx < secCloseIdx) {
		t.Errorf("bad placement: fig=%d new=%d secClose=%d", figIdx, newIdx, secCloseIdx)
	}
}

// TestApplyBBoxAddCreatesFiguresSecWhenMissing covers the case
// where GROBID missed every figure so no trailing sec exists —
// applying the first bbox correction must create the sec, matching
// the shape jatsconv would have emitted.
func TestApplyBBoxAddCreatesFiguresSecWhenMissing(t *testing.T) {
	jats := []byte(`<article><body>
<sec><title>Body</title><p>text</p></sec>
</body></article>`)
	c := &correction.Correction{
		ID:          "uuid-abcd",
		Kind:        correction.KindBBoxAdd,
		ContentType: "figure",
		BBoxPage:    3,
		BBox:        &correction.BBox{X: 100, Y: 200, W: 300, H: 400},
		Label:       "figure-1",
	}
	out, err := refmanager.ApplyBBoxAddToJATS(jats, c)
	if err != nil {
		t.Fatalf("ApplyBBoxAddToJATS: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<sec sec-type="figures"><title>Figures and tables</title>`) {
		t.Errorf("missing generated figures sec:\n%s", got)
	}
	if !strings.Contains(got, `<fig id="curator_uuidabcd"`) {
		t.Errorf("missing new fig element:\n%s", got)
	}
	// New sec placement: before </body>
	secIdx := strings.Index(got, `<sec sec-type="figures">`)
	bodyCloseIdx := strings.Index(got, "</body>")
	if !(secIdx < bodyCloseIdx) {
		t.Errorf("bad placement: secIdx=%d bodyClose=%d", secIdx, bodyCloseIdx)
	}
}

// TestApplyBBoxAddRejectsBadContentType covers the guard against
// callers submitting a bbox-add for a content type the JATS layer
// doesn't know how to render (abstract, section, ...). Those go
// through a different apply path.
func TestApplyBBoxAddRejectsBadContentType(t *testing.T) {
	jats := []byte(`<article><body><p>x</p></body></article>`)
	c := &correction.Correction{
		ID:          "uuid-x",
		Kind:        correction.KindBBoxAdd,
		ContentType: "abstract", // not supported by this apply path
		BBoxPage:    1,
		BBox:        &correction.BBox{X: 0, Y: 0, W: 100, H: 100},
	}
	if _, err := refmanager.ApplyBBoxAddToJATS(jats, c); err == nil {
		t.Errorf("expected error for content_type=abstract, got nil")
	}
}

// TestApplyTextRemoveWrapsMatchingParagraph covers the common case:
// a body paragraph whose text matches the payload gets replaced by a
// <hive:noise> wrapper carrying the original text inside a comment.
// Untouched paragraphs stay put.
func TestApplyTextRemoveWrapsMatchingParagraph(t *testing.T) {
	jats := []byte(`<article><body>
<p>Real body content that should stay.</p>
<p>Journal of Taxonomy Vol. 60</p>
<p>More real content here.</p>
</body></article>`)
	payload, _ := json.Marshal(correction.TextRemovePayload{
		Text: "Journal of Taxonomy Vol. 60",
	})
	c := &correction.Correction{
		ID:      "c-abc123",
		Kind:    correction.KindTextRemove,
		Payload: string(payload),
	}
	out, err := refmanager.ApplyTextRemoveToJATS(jats, c)
	if err != nil {
		t.Fatalf("ApplyTextRemoveToJATS: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<hive:noise correction-id="c-abc123">`) {
		t.Errorf("missing hive:noise wrapper:\n%s", got)
	}
	if !strings.Contains(got, `<!-- Journal of Taxonomy Vol. 60 -->`) {
		t.Errorf("missing text hint in comment:\n%s", got)
	}
	if strings.Contains(got, `<p>Journal of Taxonomy Vol. 60</p>`) {
		t.Errorf("original <p> should be replaced:\n%s", got)
	}
	if !strings.Contains(got, `<p>Real body content that should stay.</p>`) {
		t.Errorf("unrelated paragraph should be untouched:\n%s", got)
	}
}

// TestApplyTextRemoveDisambiguatesByPage covers papers where the
// same running-header text appears on multiple pages: pdf_page in
// the payload narrows the match to the intended occurrence.
func TestApplyTextRemoveDisambiguatesByPage(t *testing.T) {
	jats := []byte(`<article><body>
<p content-type="hive-page-break"><hive:page-break page="1"/></p>
<p>Header text</p>
<p>Body on page 1.</p>
<p content-type="hive-page-break"><hive:page-break page="2"/></p>
<p>Header text</p>
<p>Body on page 2.</p>
</body></article>`)
	payload, _ := json.Marshal(correction.TextRemovePayload{
		Text:    "Header text",
		PDFPage: 2,
	})
	c := &correction.Correction{
		ID:      "c-page2",
		Kind:    correction.KindTextRemove,
		Payload: string(payload),
	}
	out, err := refmanager.ApplyTextRemoveToJATS(jats, c)
	if err != nil {
		t.Fatalf("ApplyTextRemoveToJATS: %v", err)
	}
	got := string(out)
	wrapIdx := strings.Index(got, `<hive:noise correction-id="c-page2">`)
	page2Idx := strings.Index(got, `page="2"`)
	if wrapIdx < 0 || wrapIdx < page2Idx {
		t.Errorf("wrapper should appear after page 2 marker:\nwrap=%d page2=%d\n%s",
			wrapIdx, page2Idx, got)
	}
	// The page-1 occurrence should still be a plain <p>.
	if strings.Count(got, `<p>Header text</p>`) != 1 {
		t.Errorf("expected page-1 Header text unchanged:\n%s", got)
	}
}

// TestApplyTextRemoveSanitizesDoubleHyphenInHint ensures the comment
// stays valid XML when the removed text contains "--" — sanitized
// with a zero-width space between the hyphens.
func TestApplyTextRemoveSanitizesDoubleHyphenInHint(t *testing.T) {
	jats := []byte(`<article><body>
<p>Author -- Title -- Journal</p>
</body></article>`)
	payload, _ := json.Marshal(correction.TextRemovePayload{
		Text: "Author -- Title -- Journal",
	})
	c := &correction.Correction{
		ID:      "c-hyphen",
		Kind:    correction.KindTextRemove,
		Payload: string(payload),
	}
	out, err := refmanager.ApplyTextRemoveToJATS(jats, c)
	if err != nil {
		t.Fatalf("ApplyTextRemoveToJATS: %v", err)
	}
	got := string(out)
	// The literal "--" should not appear inside a hive:noise comment
	// (would produce invalid XML).
	if strings.Contains(got, `<!-- Author -- Title`) {
		t.Errorf("literal -- inside comment breaks XML:\n%s", got)
	}
	if !strings.Contains(got, `<hive:noise correction-id="c-hyphen">`) {
		t.Errorf("missing wrapper:\n%s", got)
	}
}

// TestApplyTextRestoreRoundTrip covers the remove + restore pair:
// applying a text-remove wraps the paragraph in <hive:noise>, then
// applying a text-restore targeting the same correction id unwraps
// the noise back to a <p> with the original text.
func TestApplyTextRestoreRoundTrip(t *testing.T) {
	jats := []byte(`<article><body>
<p>Journal of Taxonomy Vol. 60</p>
<p>Real content.</p>
</body></article>`)
	rmPayload, _ := json.Marshal(correction.TextRemovePayload{
		Text: "Journal of Taxonomy Vol. 60",
	})
	rm := &correction.Correction{
		ID:      "c-rm",
		Kind:    correction.KindTextRemove,
		Payload: string(rmPayload),
	}
	afterRemove, err := refmanager.ApplyTextRemoveToJATS(jats, rm)
	if err != nil {
		t.Fatalf("ApplyTextRemoveToJATS: %v", err)
	}
	if !strings.Contains(string(afterRemove), `<hive:noise correction-id="c-rm">`) {
		t.Fatalf("remove didn't wrap:\n%s", afterRemove)
	}
	rsPayload, _ := json.Marshal(correction.TextRestorePayload{
		RemoveCorrectionID: "c-rm",
	})
	rs := &correction.Correction{
		ID:      "c-rs",
		Kind:    correction.KindTextRestore,
		Payload: string(rsPayload),
	}
	afterRestore, err := refmanager.ApplyTextRestoreToJATS(afterRemove, rs)
	if err != nil {
		t.Fatalf("ApplyTextRestoreToJATS: %v", err)
	}
	got := string(afterRestore)
	if strings.Contains(got, "hive:noise") {
		t.Errorf("restore should have unwrapped the noise element:\n%s", got)
	}
	if !strings.Contains(got, `<p>Journal of Taxonomy Vol. 60</p>`) {
		t.Errorf("restore should have brought back original paragraph:\n%s", got)
	}
}

// TestApplyTextRestoreIdempotentWhenAlreadyUnwrapped covers the
// re-apply case: a text-restore whose target hive:noise is absent
// (already unwrapped by a prior apply pass, or the text-remove was
// never applied) returns the JATS unchanged rather than erroring.
func TestApplyTextRestoreIdempotentWhenAlreadyUnwrapped(t *testing.T) {
	jats := []byte(`<article><body>
<p>Nothing wrapped in here.</p>
</body></article>`)
	rsPayload, _ := json.Marshal(correction.TextRestorePayload{
		RemoveCorrectionID: "c-missing",
	})
	rs := &correction.Correction{
		ID:      "c-rs",
		Kind:    correction.KindTextRestore,
		Payload: string(rsPayload),
	}
	out, err := refmanager.ApplyTextRestoreToJATS(jats, rs)
	if err != nil {
		t.Fatalf("ApplyTextRestoreToJATS: %v", err)
	}
	if !bytes.Equal(out, jats) {
		t.Errorf("expected unchanged JATS, got:\n%s", out)
	}
}
