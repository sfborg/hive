// Package labels extracts fig/table labels and geometric regions from
// augmented-JATS sidecars. The correction UI reads the label list to
// help a curator pick a coherent new label without duplicating an
// existing number; the PDF viewer reads the region list to draw
// overlay rectangles for every detected fig/table (curator-added
// regions distinguished from GROBID-extracted ones by id prefix).
//
// Depends only on pkg/correction for the shared BBox shape; no pkg-
// root dependencies. Lives under pkg/refmanager because JATS sidecar
// parsing is refmanager territory (all callers are the reference-
// ingest / correction paths).
package labels

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/sfborg/hive/pkg/correction"
)

// Label is one figure or table label found in a JATS
// sidecar — used by the correction-preview form to show a curator
// which figs/tables already exist in the paper so they can pick a
// coherent label for a new correction without accidentally
// duplicating an existing number.
type Label struct {
	Text string // "Figure 1", "Table 2" — as extracted from the caption
	Kind string // "figure" | "table"
	Page int    // source PDF page from hive:pdf-page, 0 when unset
	ID   string // JATS element id (fig_0, tab_1, curator_abc, ...)
}

// Region adds bbox coords to an Label — used by
// the Digitize-tab PDF viewer to render every fig/table-wrap in
// the JATS as an overlay rectangle, distinguishing curator-added
// regions from GROBID-detected ones by their id prefix.
type Region struct {
	Label
	BBox   *correction.BBox // nil when hive:pdf-bbox missing or unparseable
	Origin string           // "grobid" (default) or "curator" (id starts with "curator_")
}

// extractedLabelRegex mirrors the caption prefix jatsconv looks
// for on write. Case-insensitive; matches the exact shape a paper
// usually renders ("Figure 3.", "Table 1", "Fig 4", "Tab. 2").
var extractedLabelRegex = regexp.MustCompile(`(?i)\b(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)\b`)

// Extract walks the JATS bytes and returns every
// <fig>/<table-wrap> element's caption-derived label plus its
// source PDF page (when hive:pdf-page is set). Ordered by source
// page — same order the curator reads through the paper, so the
// form's "existing labels" list scans naturally.
//
// Returns empty slice when the JATS has no figs/tables. Malformed
// JATS returns an error so the caller can degrade gracefully
// (form still opens without the existing-labels hint).
func Extract(jats []byte) ([]Label, error) {
	var out []Label
	dec := xml.NewDecoder(bytes.NewReader(jats))
	dec.Strict = false // JATS sidecars occasionally have quirks; be permissive
	var (
		inFigOrTable bool
		currentKind  string
		currentID    string
		currentPage  int
		inCaption    int
		captionBuf   bytes.Buffer
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("labels: parse jats: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "fig" || t.Name.Local == "table-wrap" {
				inFigOrTable = true
				currentKind = "figure"
				if t.Name.Local == "table-wrap" {
					currentKind = "table"
				}
				currentID = ""
				currentPage = 0
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "id":
						currentID = a.Value
					case "pdf-page":
						if n, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
							currentPage = n
						}
					}
				}
				continue
			}
			if inFigOrTable && t.Name.Local == "caption" {
				inCaption++
				if inCaption == 1 {
					captionBuf.Reset()
				}
			}
		case xml.EndElement:
			if t.Name.Local == "caption" && inCaption > 0 {
				inCaption--
				if inCaption == 0 {
					// Extract label from the buffered caption text.
					m := extractedLabelRegex.FindSubmatch(captionBuf.Bytes())
					if m != nil {
						kind := strings.ToLower(string(m[1]))
						num := string(m[2])
						label := "Figure " + num
						if strings.HasPrefix(kind, "tab") {
							label = "Table " + num
						}
						out = append(out, Label{
							Text: label,
							Kind:  currentKind,
							Page:  currentPage,
							ID:    currentID,
						})
					}
				}
				continue
			}
			if inFigOrTable && (t.Name.Local == "fig" || t.Name.Local == "table-wrap") {
				inFigOrTable = false
				currentKind = ""
				currentID = ""
				currentPage = 0
			}
		case xml.CharData:
			if inCaption > 0 {
				captionBuf.Write(t)
			}
		}
	}
	// Stable sort by page (0 last so unpaged entries land at the end).
	// Simple insertion sort — hive papers have well under 100 figs/tables.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && labelSortLess(out[j], out[j-1]) {
			out[j], out[j-1] = out[j-1], out[j]
			j--
		}
	}
	return out, nil
}

// ExtractRegions is a superset of Extract
// that also captures the hive:pdf-page + hive:pdf-bbox coord
// attributes on each fig/table-wrap, plus a synthetic Origin
// tag ("curator" or "grobid") derived from the element id prefix.
//
// Used by the PDF viewer to render every fig/table region in the
// paper as an overlay rectangle (curator additions in one color,
// GROBID-extracted in another) so a curator can see the full set
// of detected regions at a glance and spot missing / mis-bounded
// ones for correction.
func ExtractRegions(jats []byte) ([]Region, error) {
	var out []Region
	dec := xml.NewDecoder(bytes.NewReader(jats))
	dec.Strict = false
	var (
		inFigOrTable bool
		currentKind  string
		currentID    string
		currentPage  int
		currentBBox  *correction.BBox
		inCaption    int
		captionBuf   bytes.Buffer
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("regions: parse jats: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "fig" || t.Name.Local == "table-wrap" {
				inFigOrTable = true
				currentKind = "figure"
				if t.Name.Local == "table-wrap" {
					currentKind = "table"
				}
				currentID = ""
				currentPage = 0
				currentBBox = nil
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "id":
						currentID = a.Value
					case "pdf-page":
						if n, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
							currentPage = n
						}
					case "pdf-bbox":
						if b := parseBBoxAttr(a.Value); b != nil {
							currentBBox = b
						}
					}
				}
				continue
			}
			if inFigOrTable && t.Name.Local == "caption" {
				inCaption++
				if inCaption == 1 {
					captionBuf.Reset()
				}
			}
		case xml.EndElement:
			if t.Name.Local == "caption" && inCaption > 0 {
				inCaption--
				continue
			}
			if inFigOrTable && (t.Name.Local == "fig" || t.Name.Local == "table-wrap") {
				label := ""
				m := extractedLabelRegex.FindSubmatch(captionBuf.Bytes())
				if m != nil {
					kind := strings.ToLower(string(m[1]))
					num := string(m[2])
					if strings.HasPrefix(kind, "tab") {
						label = "Table " + num
					} else {
						label = "Figure " + num
					}
				}
				origin := "grobid"
				if strings.HasPrefix(currentID, "curator_") {
					origin = "curator"
				}
				out = append(out, Region{
					Label: Label{
						Text: label,
						Kind:  currentKind,
						Page:  currentPage,
						ID:    currentID,
					},
					BBox:   currentBBox,
					Origin: origin,
				})
				inFigOrTable = false
				currentKind = ""
				currentID = ""
				currentPage = 0
				currentBBox = nil
			}
		case xml.CharData:
			if inCaption > 0 {
				captionBuf.Write(t)
			}
		}
	}
	return out, nil
}

// parseBBoxAttr parses a "x,y,w,h" bbox attribute value into a
// BBox pointer. Returns nil on any parse error (partial parse,
// wrong component count, non-numeric field) so the caller can
// treat missing coords as "no bbox" rather than failing the
// whole region walk.
func parseBBoxAttr(v string) *correction.BBox {
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return nil
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil
		}
		vals[i] = n
	}
	return &correction.BBox{X: vals[0], Y: vals[1], W: vals[2], H: vals[3]}
}

func labelSortLess(a, b Label) bool {
	// Unpaged entries (Page == 0) sort last.
	if a.Page == 0 && b.Page != 0 {
		return false
	}
	if b.Page == 0 && a.Page != 0 {
		return true
	}
	if a.Page != b.Page {
		return a.Page < b.Page
	}
	// Same-page tiebreak on label so figs and tables interleave
	// predictably.
	return a.Text < b.Text
}
