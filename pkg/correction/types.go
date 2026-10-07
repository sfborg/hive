// Package correction defines the wire shapes for hive's extraction-
// correction system — durable, re-applicable curator edits to the
// attached-PDF extraction pipeline (JATS sidecars produced by GROBID
// + jatsconv).
//
// This package is a leaf: it depends only on the standard library.
// Both pkg/ root (which owns Archive/Tx methods on the
// hive__extraction_correction table) and pkg/refmanager (which owns
// the kind-specific Apply functions that mutate JATS) reference the
// types here — the shared home keeps the two sides from cycling.
package correction

import "time"

// Correction is one row of hive__extraction_correction — a durable,
// re-applicable curator correction to the attached-PDF extraction
// pipeline. See DIGITIZE_CORRECTIONS_PLAN.md § Storage for the full
// column semantics.
type Correction struct {
	ID          string
	ReferenceID string
	SidecarSHA  string
	Kind        Kind
	ContentType string
	BBoxPage    int   // 0 when the correction is text-side only
	BBox        *BBox // nil when text-side only
	Label       string
	AnchorPtr   *AnchorPtr // nil when correction has no destination anchor
	// TargetElementID points at the JATS element (fig / table-wrap)
	// this correction modifies. Populated for bbox-modify kind so
	// the apply step knows which element to patch; empty for
	// additive kinds like bbox-add that create a new element.
	TargetElementID string
	Payload         string // opaque JSON, kind-specific — see plan doc for shapes
	Status          Status
	RevertedReason  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CreatedBy       string
}

// Kind classifies how a correction mutates the JATS. Matches the
// CHECK constraint on hive__extraction_correction.correction_kind.
type Kind string

const (
	KindBBoxAdd      Kind = "bbox-add"
	KindBBoxModify   Kind = "bbox-modify"
	KindBBoxRemove   Kind = "bbox-remove"
	KindTextTag      Kind = "text-tag"
	// KindTextRemove wraps a body element (paragraph, section, title,
	// …) whose text is GROBID noise (running headers/footers, watermark
	// captions, editorial artifacts) with <hive:noise correction-id="…">
	// containing an XML comment holding the original text. Generic
	// renderers see nothing (comments are parser-erased); hive-aware
	// tooling correlates the wrapper's correction-id back to the
	// hive__extraction_correction row for un-remove + training-data
	// export.
	KindTextRemove Kind = "text-remove"
	// KindTextRestore reverses a KindTextRemove: finds the
	// <hive:noise correction-id="…"> wrapper for the referenced
	// text-remove correction and unwraps it back to a <p> holding
	// the original text (recovered from the comment inside). Kept as
	// a first-class correction rather than a delete-and-regenerate
	// so the audit trail records "curator marked as noise, then
	// unmarked" — signal for the training corpus that the removal
	// was not confidently correct.
	KindTextRestore  Kind = "text-restore"
	KindXrefFix      Kind = "xref-fix"
	KindChipFeedback Kind = "chip-feedback"
	// KindRefMerge merges a spurious <ref> into the previous one.
	// Fires when GROBID's ref tokenizer splits a single citation
	// into two rows (typical trigger: an inline URL fragment, or an
	// address tail parsed as a new author list). TargetElementID
	// names the row to be absorbed; applying the correction
	// concatenates its textContent into the previous <ref> and
	// deletes the target row.
	KindRefMerge Kind = "ref-merge"
	// KindRefEdit rewrites individual fields on a bibliography
	// <ref>'s element-citation — the curator-driven counterpart to
	// the extractor's automated output. Used when GROBID parsed the
	// ref but got a field wrong (misread year, dropped an author,
	// garbled a title, wrong DOI). TargetElementID names the ref;
	// Payload carries a RefEditPayload whose Corrected map holds
	// only the changed fields — untouched fields stay as GROBID had
	// them, so a re-import that improves extraction still benefits.
	KindRefEdit Kind = "ref-edit"
	// KindAssertionEdit records a curator correction to the value
	// of one NER assertion (a single {field, value} extracted from
	// the paper). TargetElementID names the assertion_id from
	// hive__reference_assertion. Payload carries an
	// AssertionEditPayload with both original and corrected values
	// so the row is a self-contained training-signal record even
	// if the underlying assertion is later re-materialized.
	//
	// The original NER value STAYS on the assertion row unchanged
	// — corrections are additive, not destructive, so a re-run of
	// NER training against the same paper can pair "what the model
	// said" with "what the curator said" without a JOIN chain.
	KindAssertionEdit Kind = "assertion-edit"
)

// Status tracks whether a correction still applies to the current
// sidecar. 'reverted' rows are kept for the training corpus even
// after they stop mutating output.
type Status string

const (
	StatusActive   Status = "active"
	StatusReverted Status = "reverted"
)

// BBox is a rectangular region on a PDF page in PDF points (72 DPI).
// Matches the coord units GROBID emits and figextract consumes.
type BBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// AnchorPtr locates a destination in the JATS for cross-surface
// placement corrections (missed abstract inserted after primary
// abstract, missed paragraph inserted between two existing paras).
type AnchorPtr struct {
	ElementID string `json:"element_id"`
	Position  string `json:"position"` // 'before' | 'after' | 'inside-end'
}

// BBoxAddPayload is the payload shape for a bbox-add correction.
// TableRows is populated at save time by the handler (via
// pdftext.ExtractTable) when content_type is 'table' — its presence
// causes BBoxAddFigureJATSElement to emit a real <table><tr><td>
// child so the reader's Digitized tab has structured content instead
// of an empty placeholder.
type BBoxAddPayload struct {
	TableRows [][]string `json:"table_rows,omitempty"`
}

// BBoxModifyPayload is the JSON stored in
// hive__extraction_correction.payload when correction_kind =
// 'bbox-modify'. Captures both the original element state and the
// curator's desired changes so training-corpus export can emit
// labeled deltas. Nil-valued fields in Corrected mean "curator
// didn't change this axis" (the original stays in effect).
type BBoxModifyPayload struct {
	Original  BBoxModifyState `json:"original"`
	Corrected BBoxModifyState `json:"corrected"`
}

type BBoxModifyState struct {
	ContentType string `json:"content_type,omitempty"`
	Label       string `json:"label,omitempty"`
	Caption     string `json:"caption,omitempty"`
	// BBox carries a curator-adjusted rectangle in PDF points.
	// Nil means "coord change not part of this correction" — the
	// element's existing hive:pdf-bbox stays untouched.
	BBox *BBox `json:"bbox,omitempty"`
	// Page is the 1-indexed PDF page hosting the bbox. Carried so
	// server-side re-extraction (e.g. pdftext.ExtractTable) has the
	// page without a separate JATS lookup. Zero means "unchanged".
	Page int `json:"page,omitempty"`
	// TableRows carries a fresh heuristic parse of the region for
	// content_type='table'. Populated by the handler at save time
	// via pdftext.ExtractTable when the bbox changed, then read by
	// the bbox-modify applier to replace the <table> block inside
	// the target <table-wrap>. Empty means "no row change".
	TableRows [][]string `json:"table_rows,omitempty"`
}

// RefEditPayload is the JSON stored in
// hive__extraction_correction.payload when correction_kind =
// 'ref-edit'. Same shape both directions: Original captures the
// pre-edit JATS field values (for the training corpus and for
// diffing at apply time), Corrected holds only the field values
// the curator changed. Empty-string values in Corrected mean the
// curator explicitly cleared that field.
//
// Field names mirror hive's manualReference form (author, title,
// issued, volume, ...) so the WUI can serialize its form state
// directly into this struct without an intermediate mapping.
type RefEditPayload struct {
	Original  RefEditFields `json:"original"`
	Corrected RefEditFields `json:"corrected"`
}

// RefEditFields collects the field slots an element-citation carries
// in the JATS shape jatsconv emits. Pointer strings distinguish
// "not touched by this correction" (nil) from "cleared by curator"
// (non-nil pointing at ""). Callers construct the struct from the
// curator form's diff — only fields that differ from Original get
// populated on Corrected.
type RefEditFields struct {
	Author              *string `json:"author,omitempty"`
	Title               *string `json:"title,omitempty"`
	ContainerTitle      *string `json:"container_title,omitempty"`
	ContainerTitleShort *string `json:"container_title_short,omitempty"`
	Issued              *string `json:"issued,omitempty"`
	Volume              *string `json:"volume,omitempty"`
	Issue               *string `json:"issue,omitempty"`
	Page                *string `json:"page,omitempty"`
	Publisher           *string `json:"publisher,omitempty"`
	PublisherPlace      *string `json:"publisher_place,omitempty"`
	DOI                 *string `json:"doi,omitempty"`
	Citation            *string `json:"citation,omitempty"`
}

// TextRemovePayload is the JSON stored in
// hive__extraction_correction.payload when correction_kind =
// 'text-remove'. Captures the exact original text bytes so the
// training corpus retains them even after the applier turns the
// target element into a hidden <hive:noise> wrapper. PDFPage lets a
// future re-locate-by-text fallback narrow the search when a re-
// import produces different element ids.
type TextRemovePayload struct {
	// Text is the verbatim body of the removed element (element
	// text-content with tags stripped). Authoritative source for the
	// training corpus; the XML comment inside the applied wrapper is
	// a curator-inspection hint, not the source of truth.
	Text string `json:"text"`
	// PDFPage is the source page hosting the removed text (from the
	// element's hive:pdf-page attribute, when set). Zero means
	// unknown.
	PDFPage int `json:"pdf_page,omitempty"`
	// Reason lets the curator record why the removal was needed
	// ("running header", "watermark", "editorial artifact"). Feeds
	// per-cause aggregate stats and future auto-classification
	// hints for the extraction model. Empty when the curator didn't
	// pick a reason.
	Reason string `json:"reason,omitempty"`
}

// TextRestorePayload is the JSON stored in
// hive__extraction_correction.payload when correction_kind =
// 'text-restore'. Points back at the text-remove correction whose
// wrapper the restore un-wraps.
type TextRestorePayload struct {
	// RemoveCorrectionID identifies the text-remove correction the
	// curator is reversing. The applier finds the corresponding
	// <hive:noise correction-id="…"> element in the sidecar and
	// unwraps it.
	RemoveCorrectionID string `json:"remove_correction_id"`
}

// ChipFeedbackPayload is the JSON stored in
// hive__extraction_correction.payload when correction_kind =
// 'chip-feedback'. Records a curator's verdict on one
// <tp:taxon-name> chip produced by the annotator (GNfinder +
// GNverifier).
//
// The chip is identified by its text and its occurrence index in
// the document rather than by an element id: re-annotation
// regenerates the markup, so ids do not survive a re-run, while
// text and document order do. Occurrence is 1-based over the chips
// whose text matches Text.
//
// Original* and Corrected* are stored side by side so the row is a
// self-contained training example, the same shape
// AssertionEditPayload uses.
type ChipFeedbackPayload struct {
	// Action is one of "reject" (this span is not a scientific
	// name), "reclassify" (change gn:source) or "edit" (change the
	// full-name attribute).
	Action     string `json:"action"`
	Text       string `json:"text"`
	Occurrence int    `json:"occurrence,omitempty"`

	OriginalSource    string `json:"original_source,omitempty"`
	CorrectedSource   string `json:"corrected_source,omitempty"`
	OriginalFullName  string `json:"original_full_name,omitempty"`
	CorrectedFullName string `json:"corrected_full_name,omitempty"`
	// Reason categorizes a reject: why the span is not a name
	// ("not a name", "wrong boundary", "wrong classification",
	// "other"). Feeds per-cause aggregates and, later, the feedback
	// payload sent upstream. Empty when the curator didn't pick one.
	Reason string `json:"reason,omitempty"`
	// Notes records the curator's rationale when they give one.
	Notes string `json:"notes,omitempty"`
}

// Chip-feedback actions.
const (
	ChipActionReject     = "reject"
	ChipActionReclassify = "reclassify"
	ChipActionEdit       = "edit"
)

// AssertionEditPayload is the JSON stored in
// hive__extraction_correction.payload when correction_kind =
// 'assertion-edit'. Records the curator's fix to a single NER-
// extracted field on hive__reference_assertion.
//
// Original + Corrected are stored side-by-side so the correction
// is a self-contained training-signal record — a downstream
// training pipeline can build contrastive pairs ({original ←
// model prediction, corrected ← ground truth}) without joining
// back through the assertion row (whose payload might get
// re-materialized on a fresh NER run).
//
// AssertionType echoes the DwC term of the field being corrected
// (e.g. "dwc:eventDate"). Field names the compose-output slot for
// UI-side reconstruction ("verbatimDate", "country", ...). Notes
// captures the curator's rationale when they choose to record
// one; empty otherwise.
type AssertionEditPayload struct {
	AssertionType string `json:"assertion_type"`
	Field         string `json:"field,omitempty"`
	Original      string `json:"original"`
	Corrected     string `json:"corrected"`
	Notes         string `json:"notes,omitempty"`
}
