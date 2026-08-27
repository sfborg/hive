package hive

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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

	c := &Correction{
		ReferenceID: "ref-1",
		SidecarSHA:  "sha-1",
		Kind:        CorrectionKindBBoxAdd,
		ContentType: "table",
		BBoxPage:    4,
		BBox:        &BBox{X: 72.0, Y: 100.0, W: 400.0, H: 150.0},
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
	if got.Status != CorrectionStatusActive {
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
	c := &Correction{
		ID:          "uuid-1234",
		Kind:        CorrectionKindBBoxAdd,
		ContentType: "table",
		BBoxPage:    5,
		BBox:        &BBox{X: 72, Y: 100, W: 400, H: 150},
		Label:       "table-1",
	}
	out, err := ApplyBBoxAddToJATS(jats, c)
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
	c := &Correction{
		ID:          "uuid-abcd",
		Kind:        CorrectionKindBBoxAdd,
		ContentType: "figure",
		BBoxPage:    3,
		BBox:        &BBox{X: 100, Y: 200, W: 300, H: 400},
		Label:       "figure-1",
	}
	out, err := ApplyBBoxAddToJATS(jats, c)
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
	c := &Correction{
		ID:          "uuid-x",
		Kind:        CorrectionKindBBoxAdd,
		ContentType: "abstract", // not supported by this apply path
		BBoxPage:    1,
		BBox:        &BBox{X: 0, Y: 0, W: 100, H: 100},
	}
	if _, err := ApplyBBoxAddToJATS(jats, c); err == nil {
		t.Errorf("expected error for content_type=abstract, got nil")
	}
}
