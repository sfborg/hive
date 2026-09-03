// Package figextract renders per-figure and per-table image crops
// from a source PDF using GROBID's bounding-box coordinates.
//
// GROBID's TEI includes <graphic coords="page,x,y,w,h"> children
// on each detected figure/table (when coord-tracking is enabled);
// jatsconv preserves those as hive:pdf-page + hive:pdf-bbox
// attributes on the JATS <fig> and <table-wrap> elements. This
// package walks the JATS, extracts those coord attributes (matched
// by local name — namespace-agnostic), and invokes pdftocairo to
// render each region to a PNG sidecar under <sha>.figures/<id>.png.
//
// Rendering happens at a fixed 200 DPI — high enough for readable
// on-screen display in the reader modal, low enough to keep file
// sizes reasonable (~50-500 KB per crop). Regenerable: nothing
// authoritative lives in the extracted PNGs; if we later change
// DPI or fix bbox handling, re-running Extract rebuilds them.
//
// Requires poppler-utils on PATH (Docker image ships it). When
// pdftocairo isn't installed, Extract returns (0, nil) rather
// than an error — the extraction is opportunistic and callers
// should tolerate its absence, same pattern as jatsconv.Validate
// with xmllint.
package figextract

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DPI is the render resolution for figure/table crops. 200 balances
// readability against file size; higher would help for tiny insets
// but bloat overall storage without matching curator use.
const DPI = 200

// Result reports how many figures were rendered and any per-figure
// warnings. Callers log warnings and move on — extraction is
// best-effort and failures shouldn't block the upload flow.
type Result struct {
	Rendered int
	Warnings []string
}

// Extract renders every <fig>/<table-wrap> in `jats` whose
// hive:pdf-page + hive:pdf-bbox attributes are set, writing each
// to `outDir` as `<id>.png`. Requires the source pdfPath and a
// pdftocairo binary on PATH.
//
// Returns Result{Rendered:0} + nil error when pdftocairo isn't
// installed (opportunistic extraction; caller decides whether to
// surface the absence). Fatal errors (bad JATS, unreadable PDF,
// unwritable outDir) return an error.
func Extract(jats []byte, pdfPath, outDir string) (Result, error) {
	if _, err := exec.LookPath("pdftocairo"); err != nil {
		return Result{}, nil
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return Result{}, fmt.Errorf("figextract: source pdf: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("figextract: create outdir: %w", err)
	}
	regions, err := collectRegions(jats)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, r := range regions {
		if err := renderRegion(pdfPath, outDir, r); err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s: %v", r.ID, err))
			continue
		}
		res.Rendered++
	}
	return res, nil
}

// region holds the coord data for one figure or table.
type region struct {
	ID   string
	Page int
	X, Y, W, H float64
}

// collectRegions walks the JATS and returns one region per
// <fig>/<table-wrap> that has usable coord attributes. Elements
// missing either attribute are skipped silently — the caller
// won't try to extract them.
func collectRegions(jats []byte) ([]region, error) {
	var out []region
	dec := xml.NewDecoder(bytes.NewReader(jats))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("figextract: parse jats: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "fig" && se.Name.Local != "table-wrap" {
			continue
		}
		var id, page, bbox string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "id":
				id = a.Value
			case "pdf-page":
				page = a.Value
			case "pdf-bbox":
				bbox = a.Value
			}
		}
		if id == "" || page == "" || bbox == "" {
			continue
		}
		r, err := parseRegion(id, page, bbox)
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// parseRegion converts the coord string forms hive stores back
// into typed field values. bbox is "x,y,w,h" in PDF points (72
// DPI). page is a 1-indexed page number.
func parseRegion(id, page, bbox string) (region, error) {
	pageNum, err := strconv.Atoi(strings.TrimSpace(page))
	if err != nil {
		return region{}, fmt.Errorf("bad page: %w", err)
	}
	parts := strings.Split(bbox, ",")
	if len(parts) != 4 {
		return region{}, fmt.Errorf("bbox needs 4 components")
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return region{}, fmt.Errorf("bbox[%d]: %w", i, err)
		}
		vals[i] = v
	}
	return region{
		ID:   id,
		Page: pageNum,
		X:    vals[0], Y: vals[1], W: vals[2], H: vals[3],
	}, nil
}

// PreviewRegion renders a single arbitrary PDF region to PNG and
// returns the bytes in memory. Used by the correction-preview
// endpoint so a curator sees what their drawn bbox will actually
// crop to before saving. Separate from Extract because preview
// output is transient (in-memory bytes, no sidecar files) while
// Extract populates the persistent <sha>.figures/ directory.
//
// bbox coords are PDF points (72 DPI), the same unit hive stores
// on <fig>/<table-wrap> in the JATS. page is 1-indexed.
//
// Returns (nil, nil) when pdftocairo isn't installed — the caller
// treats absent bytes as "no preview available" rather than an
// error. Real errors (unreadable PDF, bad coords) return an error.
func PreviewRegion(pdfPath string, page int, x, y, w, h float64) ([]byte, error) {
	if _, err := exec.LookPath("pdftocairo"); err != nil {
		return nil, nil
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, fmt.Errorf("preview region: pdf: %w", err)
	}
	tmpDir, err := os.MkdirTemp("", "hive-preview-*")
	if err != nil {
		return nil, fmt.Errorf("preview region: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	outBase := filepath.Join(tmpDir, "preview")
	if err := renderRegion(pdfPath, tmpDir, region{
		ID: "preview", Page: page, X: x, Y: y, W: w, H: h,
	}); err != nil {
		return nil, err
	}
	png, err := os.ReadFile(outBase + ".png")
	if err != nil {
		return nil, fmt.Errorf("preview region: read output: %w", err)
	}
	return png, nil
}

// renderRegion invokes pdftocairo to render a single region to PNG.
// pdftocairo's -x/-y/-W/-H flags are integer pixel offsets at the
// output resolution — we convert from PDF points to pixels using
// (points * DPI / 72). The result is written to <outDir>/<ID>.png.
func renderRegion(pdfPath, outDir string, r region) error {
	scale := float64(DPI) / 72.0
	x := int(r.X * scale)
	y := int(r.Y * scale)
	w := int(r.W * scale)
	h := int(r.H * scale)
	outFile := filepath.Join(outDir, r.ID)
	// pdftocairo appends -1.png (page number) when writing PNG.
	// Use -singlefile to get exactly <outFile>.png without the
	// page suffix.
	cmd := exec.Command("pdftocairo",
		"-png",
		"-r", strconv.Itoa(DPI),
		"-f", strconv.Itoa(r.Page),
		"-l", strconv.Itoa(r.Page),
		"-x", strconv.Itoa(x),
		"-y", strconv.Itoa(y),
		"-W", strconv.Itoa(w),
		"-H", strconv.Itoa(h),
		"-singlefile",
		pdfPath,
		outFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pdftocairo: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
