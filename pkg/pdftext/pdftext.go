// Package pdftext extracts text from a PDF region via pdftotext.
// Companion to figextract (which renders PDF regions to PNGs): both
// take a bbox on a specific PDF page and return the corresponding
// content in their respective formats.
//
// Used by the correction-preview endpoint so a curator's drawn
// bbox surfaces its text content (for auto-detecting content type
// and suggesting a real caption-based label) alongside the PNG
// preview.
//
// Requires poppler-utils on PATH. Silent-skips when unavailable —
// the caller renders the form without smart-labeling instead of
// erroring out, matching figextract's opportunistic pattern.
package pdftext

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// figLabelRegex matches "Figure N", "Fig N", "Fig. N", "Table N",
// "Tab. N" at the very start of a text block. Anchored to the
// beginning (^) so a bbox that captures body prose mentioning
// "Table 1" mid-paragraph doesn't falsely detect as a caption —
// captions start with the label, body text embeds it.
var figLabelRegex = regexp.MustCompile(`(?is)^\s*(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)\b`)

// Detection carries the smart-labeling hint derived from a bbox's
// extracted text. Empty ContentType + Label mean "nothing
// recognizable in the extracted text" and the form falls back to
// curator-typed values.
type Detection struct {
	ContentType string // "figure" | "table" | "" when unrecognized
	Label       string // "Figure 3" | "Table 1" | "" when unrecognized
	FullText    string // raw extracted text, trimmed
}

// ExtractTable runs pdftotext -layout on the given bbox and
// parses the layout-preserved output into rows of columns.
// Heuristic: split on newlines for rows, then split each row on
// runs of 2+ consecutive spaces for columns. Works well for
// well-formed tables with visually-separated columns; degrades
// gracefully on complex layouts (fewer columns than expected).
//
// Called from the correction handler when content_type is
// "table" so the resulting JATS <table-wrap> gets a real
// <table><tr><td> child instead of just a caption placeholder.
//
// Returns empty rows (no error) when pdftotext isn't installed
// or the region has no recoverable text — caller falls back to
// the caption-only element for those cases.
func ExtractTable(pdfPath string, page int, x, y, w, h float64) ([][]string, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return nil, nil
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, fmt.Errorf("pdftext table: pdf: %w", err)
	}
	cmd := exec.Command("pdftotext",
		"-layout",
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-x", strconv.Itoa(int(x)),
		"-y", strconv.Itoa(int(y)),
		"-W", strconv.Itoa(int(w)),
		"-H", strconv.Itoa(int(h)),
		pdfPath,
		"-",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdftext table: pdftotext: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return ParseTableFromLayout(string(out)), nil
}

// ParseTableFromLayout converts pdftotext -layout output into
// rows of columns. Rules:
//   - Split into lines; skip empty and label-prefix lines
//     ("Table N", "Tabla N") that pdftotext often puts above
//     the actual table rows.
//   - For each remaining line, split on runs of 2+ spaces to
//     recover columns. This works because well-formed PDF
//     tables have clear column gaps that pdftotext preserves.
//   - Trim each cell.
//
// Exported so tests can exercise it against known layouts
// without needing pdftotext installed.
func ParseTableFromLayout(text string) [][]string {
	multiSpace := regexp.MustCompile(`\s{2,}`)
	var rows [][]string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Skip caption-prefix lines pdftotext often puts above
		// the actual table body (English + Spanish variants).
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "table ") ||
			strings.HasPrefix(lower, "tabla ") ||
			strings.HasPrefix(lower, "fig ") ||
			strings.HasPrefix(lower, "figure ") ||
			strings.HasPrefix(lower, "figura ") {
			continue
		}
		cells := multiSpace.Split(line, -1)
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		// Skip lines that only produced one non-empty cell —
		// those are caption prose, not table rows.
		nonEmpty := 0
		for _, c := range cells {
			if c != "" {
				nonEmpty++
			}
		}
		if nonEmpty < 2 {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

// ExtractRegion runs pdftotext on the given bbox and returns a
// Detection with the extracted text and any recognized fig/table
// caption prefix. bbox coords are PDF points (72 DPI); page is
// 1-indexed.
//
// Returns an empty Detection (no error) when pdftotext isn't
// installed or when the region contains no recoverable text —
// image-only PDFs land here and the caller should fall back to
// OCR (see OCR.md) if the sidecar sha has an image-layer flag.
func ExtractRegion(pdfPath string, page int, x, y, w, h float64) (*Detection, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return &Detection{}, nil
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, fmt.Errorf("pdftext: pdf: %w", err)
	}
	// pdftotext's -x/-y/-W/-H coords are in "pixels" at the default
	// 72 DPI resolution — same as PDF points, so we pass bbox
	// coords directly with no scaling. -layout preserves reading
	// order so the "Table N." prefix stays at the start.
	cmd := exec.Command("pdftotext",
		"-layout",
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-x", strconv.Itoa(int(x)),
		"-y", strconv.Itoa(int(y)),
		"-W", strconv.Itoa(int(w)),
		"-H", strconv.Itoa(int(h)),
		pdfPath,
		"-",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdftext: pdftotext: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	text := strings.TrimSpace(string(out))
	d := &Detection{FullText: text}
	if m := figLabelRegex.FindStringSubmatch(text); m != nil {
		kind := strings.ToLower(m[1])
		num := m[2]
		if strings.HasPrefix(kind, "fig") {
			d.ContentType = "figure"
			d.Label = "Figure " + num
		} else if strings.HasPrefix(kind, "tab") {
			d.ContentType = "table"
			d.Label = "Table " + num
		}
	}
	return d, nil
}
