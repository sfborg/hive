package correction

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// IsBBoxKind returns true for correction kinds that mutate a JATS
// fig or table-wrap element and therefore require content_type in
// {"figure","table"}. Used by callers that reapply a stored batch of
// corrections and want to defensively skip rows with a mismatched
// content_type (shouldn't happen post-validation, but old rows may
// exist).
func IsBBoxKind(k Kind) bool {
	switch k {
	case KindBBoxAdd, KindBBoxModify, KindBBoxRemove:
		return true
	}
	return false
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
	if c.Kind != KindBBoxAdd {
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
	elemID := CurationElementID(c.ID)
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
	safeLabel := XMLEscape(label)

	// Optional structured-table body when payload carries parsed
	// rows (populated at handler save time via pdftext.ExtractTable).
	tableBody := ""
	captionText := safeLabel
	if c.Payload != "" && c.Payload != "{}" {
		var p BBoxAddPayload
		if err := json.Unmarshal([]byte(c.Payload), &p); err == nil {
			if c.ContentType == "table" && len(p.TableRows) > 0 {
				tableBody = RenderJATSTableFromRows(p.TableRows)
			}
			if strings.TrimSpace(p.Caption) != "" {
				captionText = XMLEscape(strings.TrimSpace(p.Caption))
			}
		}
	}

	return fmt.Sprintf(
		`<%s id="%s" hive:pdf-page="%d" hive:pdf-bbox="%s"><caption><p>%s</p></caption>%s</%s>`,
		elemName, elemID, c.BBoxPage, bbox, captionText, tableBody, elemName,
	), nil
}

// RenderJATSTableFromRows converts a [][]string table (rows of
// columns) into a JATS <table> block. First row becomes <thead>
// with <th> cells; remaining rows become <tbody> with <td> cells.
// Escapes each cell for XML safety.
//
// Exported because pkg/refmanager's bbox-modify applier also uses
// it when the curator resizes a table region and the handler
// re-parses the rows via pdftext.ExtractTable.
func RenderJATSTableFromRows(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<table>")
	if len(rows) > 1 {
		b.WriteString("<thead><tr>")
		for _, cell := range rows[0] {
			b.WriteString("<th>")
			b.WriteString(XMLEscape(cell))
			b.WriteString("</th>")
		}
		b.WriteString("</tr></thead><tbody>")
		for _, row := range rows[1:] {
			b.WriteString("<tr>")
			for _, cell := range row {
				b.WriteString("<td>")
				b.WriteString(XMLEscape(cell))
				b.WriteString("</td>")
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody>")
	} else {
		b.WriteString("<tbody><tr>")
		for _, cell := range rows[0] {
			b.WriteString("<td>")
			b.WriteString(XMLEscape(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr></tbody>")
	}
	b.WriteString("</table>")
	return b.String()
}

// XMLEscape escapes a caption string for embedding in XML text
// content. Exported so pkg/refmanager's appliers can reuse the same
// escaper without importing jatsconv's internals.
func XMLEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

// CheckWellFormed reports whether doc parses as well-formed XML.
// Appliers splice raw bytes, so every applied correction is checked
// before the result is stored.
func CheckWellFormed(doc []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("not well-formed XML: %w", err)
		}
	}
}
