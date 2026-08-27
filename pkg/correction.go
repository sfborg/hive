package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Correction is one row of hive__extraction_correction — a durable,
// re-applicable curator correction to the attached-PDF extraction
// pipeline. See DIGITIZE_CORRECTIONS_PLAN.md § Storage for the full
// column semantics.
type Correction struct {
	ID              string
	ReferenceID     string
	SidecarSHA      string
	Kind            CorrectionKind
	ContentType     string
	BBoxPage        int    // 0 when the correction is text-side only
	BBox            *BBox  // nil when text-side only
	Label           string
	AnchorPtr       *AnchorPtr // nil when correction has no destination anchor
	// TargetElementID points at the JATS element (fig / table-wrap)
	// this correction modifies. Populated for bbox-modify kind so
	// the apply step knows which element to patch; empty for
	// additive kinds like bbox-add that create a new element.
	TargetElementID string
	Payload         string     // opaque JSON, kind-specific — see plan doc for shapes
	Status          CorrectionStatus
	RevertedReason  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CreatedBy       string
}

// CorrectionKind classifies how a correction mutates the JATS.
// Matches the CHECK constraint on hive__extraction_correction.correction_kind.
type CorrectionKind string

const (
	CorrectionKindBBoxAdd      CorrectionKind = "bbox-add"
	CorrectionKindBBoxModify   CorrectionKind = "bbox-modify"
	CorrectionKindBBoxRemove   CorrectionKind = "bbox-remove"
	CorrectionKindTextTag      CorrectionKind = "text-tag"
	CorrectionKindXrefFix      CorrectionKind = "xref-fix"
	CorrectionKindChipFeedback CorrectionKind = "chip-feedback"
)

// CorrectionStatus tracks whether a correction still applies to
// the current sidecar. 'reverted' rows are kept for the training
// corpus even after they stop mutating output.
type CorrectionStatus string

const (
	CorrectionStatusActive   CorrectionStatus = "active"
	CorrectionStatusReverted CorrectionStatus = "reverted"
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

// CreateCorrection inserts a new correction row. Generates a UUID
// when Correction.ID is empty. Timestamps are stamped server-side;
// callers don't need to populate CreatedAt / UpdatedAt.
//
// Applying the correction to the sidecar (mutating the JATS,
// re-running figextract, etc.) is a separate step — see
// ApplyCorrection. This split lets the HTTP handler write the row
// inside a transaction and then run the potentially-slow sidecar
// mutation outside the DB lock.
func (t *Tx) CreateCorrection(c *Correction) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = CorrectionStatusActive
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.CreatedAt, _ = time.Parse(time.RFC3339, now)
	c.UpdatedAt = c.CreatedAt
	if c.CreatedBy == "" {
		c.CreatedBy = ActorFromContext(t.ctx)
	}

	bboxJSON := sql.NullString{}
	if c.BBox != nil {
		b, err := json.Marshal(c.BBox)
		if err != nil {
			return fmt.Errorf("core: marshal bbox: %w", err)
		}
		bboxJSON = sql.NullString{String: string(b), Valid: true}
	}
	anchorJSON := sql.NullString{}
	if c.AnchorPtr != nil {
		b, err := json.Marshal(c.AnchorPtr)
		if err != nil {
			return fmt.Errorf("core: marshal anchor_ptr: %w", err)
		}
		anchorJSON = sql.NullString{String: string(b), Valid: true}
	}
	bboxPage := sql.NullInt64{}
	if c.BBoxPage > 0 {
		bboxPage = sql.NullInt64{Int64: int64(c.BBoxPage), Valid: true}
	}

	targetID := sql.NullString{}
	if c.TargetElementID != "" {
		targetID = sql.NullString{String: c.TargetElementID, Valid: true}
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO hive__extraction_correction
		 (correction_id, reference_id, sidecar_sha, correction_kind,
		  content_type, bbox_page, bbox_json, label, anchor_ptr,
		  target_element_id, payload, status,
		  created_at, updated_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ReferenceID, c.SidecarSHA, string(c.Kind),
		c.ContentType, bboxPage, bboxJSON, c.Label, anchorJSON,
		targetID, c.Payload, string(c.Status),
		now, now, c.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("core: insert correction: %w", err)
	}
	return nil
}

// ListCorrectionsForReference returns every correction attached to
// the given reference in creation order. Both active and reverted
// rows are returned so the UI can show a "corrections that stopped
// applying" section after a re-digitize.
func (a *Archive) ListCorrectionsForReference(ctx context.Context, referenceID string) ([]Correction, error) {
	const q = `
		SELECT correction_id, reference_id, sidecar_sha, correction_kind,
		       content_type, bbox_page, bbox_json, label, anchor_ptr,
		       target_element_id, payload, status, COALESCE(reverted_reason, ''),
		       created_at, updated_at, created_by
		FROM hive__extraction_correction
		WHERE reference_id = ?
		ORDER BY created_at`
	rows, err := a.db.QueryContext(ctx, q, referenceID)
	if err != nil {
		return nil, fmt.Errorf("core: list corrections for %s: %w", referenceID, err)
	}
	defer rows.Close()
	var out []Correction
	for rows.Next() {
		var (
			c          Correction
			kind       string
			status     string
			bboxPage   sql.NullInt64
			bboxJSON   sql.NullString
			anchorJSON sql.NullString
			targetID   sql.NullString
			createdAt  string
			updatedAt  string
		)
		if err := rows.Scan(
			&c.ID, &c.ReferenceID, &c.SidecarSHA, &kind,
			&c.ContentType, &bboxPage, &bboxJSON, &c.Label, &anchorJSON,
			&targetID, &c.Payload, &status, &c.RevertedReason,
			&createdAt, &updatedAt, &c.CreatedBy,
		); err != nil {
			return nil, fmt.Errorf("core: scan correction: %w", err)
		}
		c.Kind = CorrectionKind(kind)
		c.Status = CorrectionStatus(status)
		if bboxPage.Valid {
			c.BBoxPage = int(bboxPage.Int64)
		}
		if bboxJSON.Valid {
			var b BBox
			if err := json.Unmarshal([]byte(bboxJSON.String), &b); err == nil {
				c.BBox = &b
			}
		}
		if anchorJSON.Valid {
			var a AnchorPtr
			if err := json.Unmarshal([]byte(anchorJSON.String), &a); err == nil {
				c.AnchorPtr = &a
			}
		}
		if targetID.Valid {
			c.TargetElementID = targetID.String
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCorrection fetches a single correction row by id. Returns
// hive.ErrNotFound when the id doesn't exist.
func (a *Archive) GetCorrection(ctx context.Context, id string) (*Correction, error) {
	const q = `
		SELECT correction_id, reference_id, sidecar_sha, correction_kind,
		       content_type, bbox_page, bbox_json, label, anchor_ptr,
		       target_element_id, payload, status, COALESCE(reverted_reason, ''),
		       created_at, updated_at, created_by
		FROM hive__extraction_correction
		WHERE correction_id = ?`
	var (
		c          Correction
		kind       string
		status     string
		bboxPage   sql.NullInt64
		bboxJSON   sql.NullString
		anchorJSON sql.NullString
		targetID   sql.NullString
		createdAt  string
		updatedAt  string
	)
	err := a.db.QueryRowContext(ctx, q, id).Scan(
		&c.ID, &c.ReferenceID, &c.SidecarSHA, &kind,
		&c.ContentType, &bboxPage, &bboxJSON, &c.Label, &anchorJSON,
		&targetID, &c.Payload, &status, &c.RevertedReason,
		&createdAt, &updatedAt, &c.CreatedBy,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("core: get correction %s: %w", id, err)
	}
	c.Kind = CorrectionKind(kind)
	c.Status = CorrectionStatus(status)
	if bboxPage.Valid {
		c.BBoxPage = int(bboxPage.Int64)
	}
	if bboxJSON.Valid {
		var b BBox
		if err := json.Unmarshal([]byte(bboxJSON.String), &b); err == nil {
			c.BBox = &b
		}
	}
	if anchorJSON.Valid {
		var ap AnchorPtr
		if err := json.Unmarshal([]byte(anchorJSON.String), &ap); err == nil {
			c.AnchorPtr = &ap
		}
	}
	if targetID.Valid {
		c.TargetElementID = targetID.String
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &c, nil
}

// DeleteCorrection removes a correction row entirely. Prefer this
// over marking status='reverted' when the curator explicitly wants
// the correction gone (as opposed to the system marking it reverted
// because a re-digitize invalidated it).
//
// Sidecar re-mutation to un-apply the correction is the caller's
// responsibility — DeleteCorrection only touches the DB row.
func (t *Tx) DeleteCorrection(id string) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__extraction_correction WHERE correction_id = ?`, id)
	if err != nil {
		return fmt.Errorf("core: delete correction %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BBoxAddPayload is the payload shape for a bbox-add correction.
// TableRows is populated at save time by the handler (via
// pdftext.ExtractTable) when content_type is 'table' — its
// presence causes BBoxAddFigureJATSElement to emit a real
// <table><tr><td> child so the reader's Digitized tab has
// structured content instead of an empty placeholder.
type BBoxAddPayload struct {
	TableRows [][]string `json:"table_rows,omitempty"`
}

// BBoxAddFigureJATSElement renders a JATS <fig> or <table-wrap>
// element for a bbox-add correction of content_type 'figure' or
// 'table'. Includes the hive:pdf-page + hive:pdf-bbox coord attrs
// that figextract and the reader modal both look at, plus a
// placeholder caption naming this as a curator-added region so
// the trailing "Figures and tables" sec displays it identifiably.
//
// For table content_type: when the payload has TableRows, emits a
// structured <table> child with heuristically-parsed rows so the
// modal's Digitized tab shows a real HTML table instead of only
// the PNG. Empty rows fall back to a bare element (PNG-only view).
//
// Element id is derived from the correction id so the JATS xref
// system can address it (figextract writes to <sha>.figures/<id>.png).
// Prefix distinguishes curator-added regions from GROBID-extracted
// ones without ambiguity.
func BBoxAddFigureJATSElement(c *Correction) (string, error) {
	if c.Kind != CorrectionKindBBoxAdd {
		return "", fmt.Errorf("figure element: expected bbox-add, got %s", c.Kind)
	}
	if c.BBox == nil || c.BBoxPage == 0 {
		return "", fmt.Errorf("figure element: bbox + page required")
	}
	elemName := "fig"
	if c.ContentType == "table" {
		elemName = "table-wrap"
	} else if c.ContentType != "figure" {
		return "", fmt.Errorf("figure element: unsupported content_type %q", c.ContentType)
	}
	elemID := "curator_" + strings.ReplaceAll(c.ID, "-", "")
	if len(elemID) > 40 {
		elemID = elemID[:40]
	}
	label := c.Label
	if label == "" {
		if c.ContentType == "table" {
			label = "Curator-added table"
		} else {
			label = "Curator-added figure"
		}
	}
	bbox := fmt.Sprintf("%.2f,%.2f,%.2f,%.2f",
		c.BBox.X, c.BBox.Y, c.BBox.W, c.BBox.H)
	safeLabel := xmlEscape(label)

	// Optional structured-table body when payload carries parsed
	// rows (populated at handler save time via pdftext.ExtractTable).
	tableBody := ""
	if c.ContentType == "table" && c.Payload != "" && c.Payload != "{}" {
		var p BBoxAddPayload
		if err := json.Unmarshal([]byte(c.Payload), &p); err == nil && len(p.TableRows) > 0 {
			tableBody = renderJATSTableFromRows(p.TableRows)
		}
	}

	return fmt.Sprintf(
		`<%s id="%s" hive:pdf-page="%d" hive:pdf-bbox="%s"><caption><p>%s</p></caption>%s</%s>`,
		elemName, elemID, c.BBoxPage, bbox, safeLabel, tableBody, elemName,
	), nil
}

// renderJATSTableFromRows converts a [][]string table (rows of
// columns) into a JATS <table> block. First row becomes <thead>
// with <th> cells; remaining rows become <tbody> with <td> cells.
// Escapes each cell for XML safety.
func renderJATSTableFromRows(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<table>")
	// First row as header. If only one row, treat it as body to
	// avoid an empty tbody (JATS accepts thead-only, but reader
	// UX is better with a body).
	if len(rows) > 1 {
		b.WriteString("<thead><tr>")
		for _, cell := range rows[0] {
			b.WriteString("<th>")
			b.WriteString(xmlEscape(cell))
			b.WriteString("</th>")
		}
		b.WriteString("</tr></thead><tbody>")
		for _, row := range rows[1:] {
			b.WriteString("<tr>")
			for _, cell := range row {
				b.WriteString("<td>")
				b.WriteString(xmlEscape(cell))
				b.WriteString("</td>")
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody>")
	} else {
		b.WriteString("<tbody><tr>")
		for _, cell := range rows[0] {
			b.WriteString("<td>")
			b.WriteString(xmlEscape(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr></tbody>")
	}
	b.WriteString("</table>")
	return b.String()
}

// xmlEscape escapes a caption string for embedding in XML text
// content. Keeps this file free of cross-package deps on jatsconv's
// internal escapers.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

// CurationElementID computes the JATS element id a bbox-add
// correction ends up with. Kept as a pure function so the reader
// (which needs to look up the extracted PNG by id) doesn't have to
// re-derive the same slug independently.
func CurationElementID(correctionID string) string {
	id := "curator_" + strings.ReplaceAll(correctionID, "-", "")
	if len(id) > 40 {
		id = id[:40]
	}
	return id
}
