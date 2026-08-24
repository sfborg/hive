// Package jatsconv converts GROBID TEI XML into JATS + TaxPub XML.
// This is slice 1 of the migration described in JATS_MIGRATION_PLAN.md:
// JATS becomes the canonical stored format for scholarly documents in
// hive, with TEI reduced to a transient GROBID output.
//
// The conversion is pure Go with no CGo, XSLT runtime, or external
// process dependency. It follows the byte-oriented pattern established
// in pkg/annotator — where structural rewrites cross subtree
// boundaries the converter buffers the relevant subtree into a small
// intermediate representation and re-emits it as JATS, and where a
// mapping is 1:1 the converter renames the element on the fly.
//
// Scope for slice 1:
//   - Header (teiHeader → front → article-meta) via reuse of the
//     existing grobid.ParseHeader helper — same source of truth for
//     both the header parser and this converter.
//   - Body flow (div/head/p → sec/title/p) with recursion for nested
//     sections.
//   - Reference list (listBibl → ref-list; biblStruct → ref +
//     element-citation) with structured children (person-group,
//     article-title, source, volume, fpage/lpage, year, pub-id).
//   - Namespace URI swap on the root element.
//   - Preservation of any tp:/gn: namespaced markup already present
//     in the input (defensive; the standard hive pipeline converts
//     TEI to JATS before the annotator adds tp:/gn: markup).
//
// Intentionally not covered in slice 1 (recorded here so a reader
// doesn't mistake omission for oversight):
//   - Tables (table/row/cell → table-wrap/table/tr/td). GROBID's
//     table output is rarely usable for taxonomic content and the
//     JATS table-wrap wrapper needs an XHTML table subtree we'd need
//     to shim in. Deferred until a real reader use case appears.
//   - Figures with embedded coordinate metadata. figure/figDesc
//     converts as fig/caption; the coord attributes GROBID emits
//     for figure regions are passed through as element attributes,
//     unclaimed by JATS but preserved for future PDF-overlay use.
//   - Notes (note/@type) — mapped to named-content, which is close
//     enough for readers.
package jatsconv

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/sfborg/hive/pkg/grobid"
)

// bodyItem is one categorized child of <body> collected during
// emitBody's buffer pass. Kind determines the JATS placement rules
// (paras before secs, figs go inside their referencing sec); id is
// only populated for figs (the xml:id from the source) and drives
// xref-based placement.
type bodyItem struct {
	kind  string // "para" | "sec" | "fig"
	id    string
	bytes []byte
}

// JATSNamespace is the canonical JATS namespace URI. Emitted as the
// default namespace on the root element of the converted output.
const JATSNamespace = "http://jats.nlm.nih.gov"

// TEINamespace is the GROBID-produced input namespace URI. Callers
// only need this constant for tests; the converter recognizes TEI
// elements by local name, not by namespace, matching the pattern
// used by grobid.ParseHeader.
const TEINamespace = "http://www.tei-c.org/ns/1.0"

// TaxPubNamespace matches the constant in pkg/annotator. Declared
// here as a string literal to avoid an import cycle if annotator
// ever depends on jatsconv.
const taxPubNamespace = "http://exsansi.pensoft.net/pub-med/pub-med-tp/1.0"
const gnNamespace = "https://hive.sfborg.org/ns/gn/1.0"

// TEIToJATS converts a GROBID TEI document into a JATS + TaxPub
// document. Deterministic: same TEI in → same JATS out.
//
// Malformed input surfaces as an error from the underlying XML
// decoder rather than a silent partial conversion — callers can
// then log the failing sha and fall back to storing the raw TEI.
func TEIToJATS(tei []byte) ([]byte, error) {
	header, err := grobid.ParseHeader(tei)
	if err != nil {
		return nil, fmt.Errorf("jatsconv: parse header: %w", err)
	}
	c := &converter{src: tei}
	c.emitPreamble()
	c.emitFront(header)
	if err := c.convertTextSubtree(); err != nil {
		return nil, err
	}
	c.emitPostamble()
	return c.out.Bytes(), nil
}

// converter carries the streaming state during a single conversion.
// Reused across the front-matter and body passes; the body pass
// creates its own xml.Decoder because the front pass consumes the
// stream through the teiHeader subtree.
type converter struct {
	src []byte
	out bytes.Buffer
}

func (c *converter) writeString(s string) { c.out.WriteString(s) }
func (c *converter) writeByte(b byte)     { c.out.WriteByte(b) }

// emitPreamble writes the XML declaration and the root <article>
// open tag. JATS elements are emitted in the null namespace — that
// matches NCBI's Publishing 1.3 RNG (which has no ns= on <grammar>)
// and the shape of PMC-deposited JATS in the wild. The tp: and gn:
// extension namespaces are declared preemptively so annotator
// output stays byte-identical whether or not annotations are
// present. article-type / dtd-version / xml:lang are required by
// the JATS Publishing content model.
func (c *converter) emitPreamble() {
	c.writeString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	c.writeString(`<article article-type="research-article"`)
	c.writeString(` dtd-version="1.3"`)
	c.writeString(` xml:lang="en"`)
	c.writeString(` xmlns:tp="` + taxPubNamespace + `"`)
	c.writeString(` xmlns:gn="` + gnNamespace + `"`)
	c.writeString(">\n")
}

func (c *converter) emitPostamble() {
	c.writeString("\n</article>\n")
}

// emitFront writes the JATS <front> block from the parsed header.
// The header is the single-source-of-truth grobid.ParseHeader
// output — any field expansion belongs there, not here.
//
// JATS Publishing 1.3 requires <journal-meta> as the first child
// of <front>, with at least one <journal-id> and one <issn>. hive
// synthesizes placeholders when the source PDF has no journal
// information — the alternative (omitting journal-meta or its
// required children) would produce invalid JATS.
func (c *converter) emitFront(h *grobid.BibHeader) {
	c.writeString("  <front>\n")
	c.writeString("    <journal-meta>\n")
	journalID := "hive-unknown"
	if h.ContainerTitle != "" {
		journalID = slugify(h.ContainerTitle)
	}
	c.writeString("      <journal-id journal-id-type=\"publisher-id\">")
	c.writeString(escapeText(journalID))
	c.writeString("</journal-id>\n")
	if h.ContainerTitle != "" {
		c.writeString("      <journal-title-group>\n")
		c.writeString("        <journal-title>")
		c.writeString(escapeText(h.ContainerTitle))
		c.writeString("</journal-title>\n")
		c.writeString("      </journal-title-group>\n")
	}
	// <issn> is required. Use a placeholder when the source did not
	// carry one — a curator can correct it later via the reference
	// editor. Format must be NNNN-NNNN per JATS.
	c.writeString("      <issn publication-format=\"print\">0000-0000</issn>\n")
	if h.Publisher != "" {
		c.writeString("      <publisher>\n")
		c.writeString("        <publisher-name>")
		c.writeString(escapeText(h.Publisher))
		c.writeString("</publisher-name>\n")
		c.writeString("      </publisher>\n")
	}
	c.writeString("    </journal-meta>\n")
	c.writeString("    <article-meta>\n")
	if h.DOI != "" {
		c.writeString("      <article-id pub-id-type=\"doi\">")
		c.writeString(escapeText(h.DOI))
		c.writeString("</article-id>\n")
	}
	// <title-group> is required by JATS Publishing 1.3. Emit it
	// unconditionally with either the parsed title or a placeholder;
	// a curator can correct an "Untitled" article via the reference
	// editor.
	title := h.Title
	if title == "" {
		title = "Untitled"
	}
	c.writeString("      <title-group>\n")
	c.writeString("        <article-title>")
	c.writeString(escapeText(title))
	c.writeString("</article-title>\n")
	c.writeString("      </title-group>\n")
	if h.Authors != "" {
		c.writeString("      <contrib-group>\n")
		for _, a := range splitAuthors(h.Authors) {
			c.writeString("        <contrib contrib-type=\"author\">\n")
			c.writeString("          <name>\n")
			c.writeString("            <surname>")
			c.writeString(escapeText(a.Surname))
			c.writeString("</surname>\n")
			if a.Given != "" {
				c.writeString("            <given-names>")
				c.writeString(escapeText(a.Given))
				c.writeString("</given-names>\n")
			}
			c.writeString("          </name>\n")
			c.writeString("        </contrib>\n")
		}
		c.writeString("      </contrib-group>\n")
	}
	if h.Issued != "" {
		c.writeString("      <pub-date>\n")
		c.writeString("        <year>")
		c.writeString(escapeText(h.Issued))
		c.writeString("</year>\n")
		c.writeString("      </pub-date>\n")
	}
	if h.Volume != "" {
		c.writeString("      <volume>")
		c.writeString(escapeText(h.Volume))
		c.writeString("</volume>\n")
	}
	if h.Issue != "" {
		c.writeString("      <issue>")
		c.writeString(escapeText(h.Issue))
		c.writeString("</issue>\n")
	}
	if h.Page != "" {
		fp, lp := splitPageRange(h.Page)
		if lp != "" {
			c.writeString("      <fpage>")
			c.writeString(escapeText(fp))
			c.writeString("</fpage>\n")
			c.writeString("      <lpage>")
			c.writeString(escapeText(lp))
			c.writeString("</lpage>\n")
		} else if fp != "" {
			c.writeString("      <fpage>")
			c.writeString(escapeText(fp))
			c.writeString("</fpage>\n")
		}
	}
	// <abstract> comes AFTER volume/issue/page per the JATS
	// Publishing article-meta model. GROBID typically extracts one
	// primary-language abstract; multilingual papers with a
	// non-primary abstract (e.g., a Spanish resumen in an
	// English-primary paper) usually don't reach this slot — that's
	// a GROBID classifier limitation and hive has nothing to emit.
	if len(h.AbstractParagraphs) > 0 {
		c.writeString("      <abstract>\n")
		for _, p := range h.AbstractParagraphs {
			c.writeString("        <p>")
			c.writeString(escapeText(p))
			c.writeString("</p>\n")
		}
		c.writeString("      </abstract>\n")
	}
	c.writeString("    </article-meta>\n")
	c.writeString("  </front>\n")
}

// slugify turns a free-text label (typically a journal title)
// into a compact ASCII identifier suitable for the JATS
// journal-id element. Lowercases letters, keeps digits, collapses
// runs of non-alphanumerics to a single hyphen, and caps at 40
// runes. Empty inputs yield "hive-unknown".
func slugify(s string) string {
	var b strings.Builder
	prevHyphen := true
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
			prevHyphen = false
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "hive-unknown"
	}
	return out
}

// splitPageRange breaks "101-125" into ("101", "125"); returns
// (whole, "") when the input has no hyphen. Handles en-dashes too,
// which GROBID sometimes emits for page ranges.
func splitPageRange(s string) (string, string) {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"-", "–", "—"} {
		if i := strings.Index(s, sep); i > 0 {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(sep):])
		}
	}
	return s, ""
}

// splitAuthors parses the semicolon-separated author string used
// throughout hive ("Given Surname; Given Surname; ...") back into
// per-person given/surname pairs for JATS <name> emission. The
// heuristic: rightmost whitespace-separated token is the surname,
// everything before it is the given-names blob. Matches the way
// grobid.ParseHeader composes the string in the first place, so
// the round-trip is stable for the common case.
func splitAuthors(joined string) []author {
	parts := strings.Split(joined, ";")
	out := make([]author, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		i := strings.LastIndexAny(p, " \t")
		if i < 0 {
			out = append(out, author{Surname: p})
			continue
		}
		out = append(out, author{
			Given:   strings.TrimSpace(p[:i]),
			Surname: strings.TrimSpace(p[i+1:]),
		})
	}
	return out
}

type author struct {
	Given   string
	Surname string
}

// convertTextSubtree walks the <text> subtree of the TEI, emitting
// JATS <body> and <back>. Elements outside <text> are ignored (the
// header is already handled by emitFront). The walk pushes/pops a
// stack of element-local names so context-sensitive mappings (e.g.
// <title> under <analytic> versus under <sec>) can differentiate.
func (c *converter) convertTextSubtree() error {
	dec := xml.NewDecoder(bytes.NewReader(c.src))
	// Seek to the opening <text> element.
	if err := seekTo(dec, "text"); err != nil {
		// No <text>: emit an empty <body/> so the output is valid JATS.
		c.writeString("  <body/>\n")
		return nil
	}
	// Now inside <text>. TEI puts prose in <text>/<body> and back
	// matter (including listBibl) in <text>/<back>. Walk children.
	depth := 1 // depth is 1 immediately after entering <text>.
	inBody := false
	inBack := false
	for depth > 0 {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("jatsconv: walk text: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			depth++
			switch local {
			case "body":
				if !inBody {
					inBody = true
					if err := c.emitBody(dec); err != nil {
						return err
					}
					inBody = false
					depth--
				}
			case "back":
				if !inBack {
					inBack = true
					c.writeString("  <back>\n")
					if err := c.convertContainer(dec, "back"); err != nil {
						return err
					}
					c.writeString("  </back>\n")
					inBack = false
					depth--
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	// Emit stub <body/> if none was present in the source.
	if !inBody && bytes.Count(c.out.Bytes(), []byte("<body")) == 0 {
		c.writeString("  <body/>\n")
	}
	return nil
}

// emitBody buffers the children of <body> and emits them in a
// JATS-body-model-valid order (para-level content first, then
// sec-level) with figures moved next to their first textual
// reference.
//
// TEI/GROBID emits secs then trailing figures, which unmodified
// would produce paras-after-secs and fail JATS validation.
// Rather than dumping every figure at body-top, this pass:
//
//   1. Buffers each child into a categorized item (fig / sec /
//      para) as it converts them.
//   2. For each figure, scans the buffered secs for the first
//      <xref rid="figID"> and splices the figure inside that sec
//      right before the sec's closing tag. sec-model allows
//      para-level content (figures) as sec-back-matter-mix, so
//      appending stays schema-valid.
//   3. Figures never referenced textually go into a synthetic
//      <sec sec-type="figures"><title>Figures</title>...</sec>
//      at the end of body. Rarely the ideal home but preserves
//      the content while keeping the reader flow clean.
//
// Result: figures appear right after the paragraph that first
// cites them; unreferenced figures stay findable at the end.
// Zero-config heuristic — works with existing TEI, no per-para
// coord metadata needed.
func (c *converter) emitBody(dec *xml.Decoder) error {
	c.writeString("  <body>\n")
	var items []bodyItem
	var leadingChars bytes.Buffer
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("jatsconv: walk body: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			sub := &converter{src: c.src}
			if err := sub.handleStart(dec, t); err != nil {
				return err
			}
			kind := "para"
			var id string
			switch t.Name.Local {
			case "div":
				kind = "sec"
			case "figure":
				kind = "fig"
				for _, a := range t.Attr {
					if a.Name.Local == "id" {
						id = a.Value
						break
					}
				}
			}
			items = append(items, bodyItem{
				kind:  kind,
				id:    id,
				bytes: sub.out.Bytes(),
			})
		case xml.EndElement:
			if t.Name.Local == "body" {
				c.emitBodyItems(items, leadingChars.Bytes())
				c.writeString("  </body>\n")
				return nil
			}
		case xml.CharData:
			// Whitespace-only chars between structural children get
			// buffered separately so we don't accidentally emit
			// them mid-content — but text between the last real
			// content and a sec would be invalid inside body.
			// leadingChars ends up unused unless the source had
			// bare text at body level (rare).
			leadingChars.WriteString(escapeText(string(t)))
		}
	}
	c.emitBodyItems(items, leadingChars.Bytes())
	c.writeString("  </body>\n")
	return nil
}

// emitBodyItems writes the categorized body items in JATS body-
// model order (para-level first, then sec-level, then a trailing
// synthetic <sec sec-type="figures"> holding every figure).
//
// Deliberate design: figures never interrupt narrative flow. The
// reader can jump to a figure via its xref link (see
// reconstructFigXrefs), so keeping figures at the end preserves
// reading flow while keeping them one click away. This matches
// how digital taxonomic-literature readers actually consume
// papers — the print-era convention of inline figures is a
// typesetting compromise, not a reader preference.
//
// Before emitting sec bytes, scan them for "Figure N" / "Fig. N"
// plain-text mentions and wrap each in an <xref> pointing at the
// corresponding fig. GROBID's ref-resolver misses most of these
// even when the text clearly references a figure; text-pattern
// reconstruction fills the gap so every mention is clickable.
func (c *converter) emitBodyItems(items []bodyItem, leadingChars []byte) {
	// Build the label→id map from figure captions. The caption
	// starts with a "FIGURE N." or "Table N." prefix that mirrors
	// how the paper's body text refers to the figure.
	labelToID := make(map[string]string)
	for _, it := range items {
		if it.kind != "fig" || it.id == "" {
			continue
		}
		if label := extractFigLabel(it.bytes); label != "" {
			labelToID[label] = it.id
		}
	}

	// Emit leading chars (rare — usually just whitespace between
	// <body> and its first child element).
	if len(bytes.TrimSpace(leadingChars)) > 0 {
		c.out.Write(leadingChars)
	}
	// Emit bare paras (unusual — typically GROBID puts everything
	// inside secs) in source order. para-level must come before
	// sec-level per JATS body-model.
	for _, it := range items {
		if it.kind == "para" {
			body := correctFigXrefRids(it.bytes, labelToID)
			c.out.Write(reconstructFigXrefs(body, labelToID))
		}
	}
	// Emit secs in source order — first correct any existing xref
	// rids whose text label doesn't match their current target
	// (GROBID off-by-one on sub-panel refs like "1 A2"), then wrap
	// unwrapped plain-text mentions.
	for _, it := range items {
		if it.kind == "sec" {
			body := correctFigXrefRids(it.bytes, labelToID)
			c.out.Write(reconstructFigXrefs(body, labelToID))
		}
	}
	// Trailing "Figures" sec holding every figure. Skip when the
	// paper has no figures — no empty "Figures" section.
	hasFigs := false
	for _, it := range items {
		if it.kind == "fig" {
			hasFigs = true
			break
		}
	}
	if hasFigs {
		c.writeString(`<sec sec-type="figures"><title>Figures</title>` + "\n")
		for _, it := range items {
			if it.kind == "fig" {
				c.out.Write(it.bytes)
			}
		}
		c.writeString("</sec>\n")
	}
}

// figLabelRegex matches the first "FIGURE N" or "TABLE N" (case-
// insensitive, with optional period) at the start of an emitted
// figure's caption. GROBID reliably prefixes captions with this
// label; we use it to correlate body-text mentions like
// "Figure 3" with the fig element whose caption starts
// "FIGURE 3." — regardless of GROBID's zero-indexed fig_N naming.
var figLabelRegex = regexp.MustCompile(`(?i)\b(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)\b`)

// extractFigLabel pulls the "Figure N" or "Table N" label from an
// emitted figure's caption bytes. Returns the label normalized to
// title case ("Figure 3", "Table 1") for map-key stability, or ""
// if the caption doesn't start with a recognizable label.
func extractFigLabel(fig []byte) string {
	// Scan the first ~100 bytes of the caption body — labels always
	// appear at the very start.
	captionStart := bytes.Index(fig, []byte("<caption>"))
	if captionStart < 0 {
		return ""
	}
	slice := fig[captionStart:]
	if len(slice) > 200 {
		slice = slice[:200]
	}
	m := figLabelRegex.FindSubmatch(slice)
	if m == nil {
		return ""
	}
	kind := strings.ToLower(string(m[1]))
	num := string(m[2])
	switch {
	case strings.HasPrefix(kind, "fig"):
		return "Figure " + num
	case strings.HasPrefix(kind, "tab"):
		return "Table " + num
	}
	return ""
}

// figXrefLeadingDigit extracts the first digit run from an xref's
// text content. GROBID's ref-resolver strips the "figure" / "table"
// prefix when it wraps an xref — the resulting text is just
// "1 A2" or "2" or "4". The leading digit is the paper's actual
// figure/table number, which is what correctFigXrefRids uses to
// look up the right target via labelToID.
var figXrefLeadingDigit = regexp.MustCompile(`^\s*(\d+)`)

// existingXrefRegex matches an entire <xref rid="fig_N">...</xref>
// or <xref rid="tab_N">...</xref> block plus captures rid and
// inner text. Used by correctFigXrefRids to inspect each existing
// figure/table xref and repoint it when GROBID mislabeled the rid.
var existingXrefRegex = regexp.MustCompile(`<xref rid="((?:fig|tab)_[^"]+)"([^>]*)>([^<]*)</xref>`)

// correctFigXrefRids scans body bytes for existing <xref> elements
// pointing at fig_N / tab_N and rewrites the rid attribute when
// GROBID mislabeled it. GROBID's bug: xref text like "1 A2" (Fig
// 1, sub-panel A2) sometimes gets rid="fig_1" (Figure 2) instead
// of rid="fig_0" (Figure 1) — off-by-one triggered by the sub-
// panel indicator confusing the ref-resolver.
//
// Correction logic: extract the leading digit N from the xref
// text. Build "Figure N" or "Table N" based on the current rid's
// prefix. Look up in labelToID → the fig whose caption self-labels
// as that number. If the derived id differs from the current rid,
// rewrite the rid.
//
// Preserves rid when the xref text has no leading digit (rare —
// GROBID would need to have made an xref with text like "the
// figure"), or when the label doesn't resolve (label refers to a
// fig GROBID didn't detect).
func correctFigXrefRids(src []byte, labelToID map[string]string) []byte {
	if len(labelToID) == 0 {
		return src
	}
	return existingXrefRegex.ReplaceAllFunc(src, func(match []byte) []byte {
		m := existingXrefRegex.FindSubmatch(match)
		if m == nil {
			return match
		}
		currentRID := string(m[1])
		otherAttrs := string(m[2])
		inner := string(m[3])
		digitMatch := figXrefLeadingDigit.FindStringSubmatch(inner)
		if digitMatch == nil {
			return match
		}
		num := digitMatch[1]
		prefix := "Figure "
		if strings.HasPrefix(currentRID, "tab_") {
			prefix = "Table "
		}
		targetID, ok := labelToID[prefix+num]
		if !ok || targetID == currentRID {
			return match
		}
		// Rewrite the rid to the correct target.
		return []byte(`<xref rid="` + targetID + `"` + otherAttrs + `>` + inner + `</xref>`)
	})
}

// xrefReconstructRegex matches unwrapped "Figure N" / "Fig. N" /
// "Table N" / "Tab. N" mentions. Case-insensitive so we catch
// mentions regardless of author capitalization; the label lookup
// in reconstructFigXrefs normalizes to title case before mapping.
var xrefReconstructRegex = regexp.MustCompile(`(?i)\b(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)\b`)

// reconstructFigXrefs walks the given sec/para bytes and wraps
// every unwrapped "Figure N" / "Table N" text mention in an
// <xref rid="figID"> so readers get clickable links. Skips
// matches inside already-existing xref tags (GROBID sometimes
// creates these; we don't want to double-wrap) and matches inside
// attribute values (protects tp:taxon-name full-name="…" and
// similar).
//
// Byte-level scan with tag tracking: any "<" starts a tag region
// where matches are ignored; any ">" closes it. Inside an <xref>
// element specifically, we also skip its text content so we don't
// re-wrap an existing xref.
func reconstructFigXrefs(src []byte, labelToID map[string]string) []byte {
	if len(labelToID) == 0 {
		return src
	}
	var out bytes.Buffer
	out.Grow(len(src))
	i := 0
	inTag := false      // currently inside <...>
	inXrefText := false // inside the text content of an <xref>...</xref>
	for i < len(src) {
		if src[i] == '<' {
			// Check if this opens or closes an <xref>.
			if bytes.HasPrefix(src[i:], []byte("<xref")) {
				inXrefText = true
			} else if bytes.HasPrefix(src[i:], []byte("</xref>")) {
				inXrefText = false
			}
			inTag = true
			out.WriteByte(src[i])
			i++
			continue
		}
		if src[i] == '>' {
			inTag = false
			out.WriteByte(src[i])
			i++
			continue
		}
		if inTag || inXrefText {
			out.WriteByte(src[i])
			i++
			continue
		}
		// Try matching the pattern at position i.
		rest := src[i:]
		m := xrefReconstructRegex.FindSubmatchIndex(rest)
		if m == nil || m[0] != 0 {
			// No match at this exact position — advance one byte
			// and continue scanning. FindSubmatchIndex on rest
			// gives us positions relative to rest; m[0] != 0 means
			// the match starts later, and we want to walk char-by-
			// char to keep tag/xref tracking accurate.
			out.WriteByte(src[i])
			i++
			continue
		}
		kind := strings.ToLower(string(rest[m[2]:m[3]]))
		num := string(rest[m[4]:m[5]])
		var label string
		switch {
		case strings.HasPrefix(kind, "fig"):
			label = "Figure " + num
		case strings.HasPrefix(kind, "tab"):
			label = "Table " + num
		}
		figID, ok := labelToID[label]
		if !ok {
			// Label doesn't map to a known figure — leave as text.
			out.WriteByte(src[i])
			i++
			continue
		}
		// Wrap the match in an xref.
		matchText := rest[m[0]:m[1]]
		out.WriteString(`<xref rid="`)
		out.WriteString(figID)
		out.WriteString(`">`)
		out.Write(matchText)
		out.WriteString(`</xref>`)
		i += m[1]
	}
	return out.Bytes()
}

// convertContainer walks the children of the currently-open element
// (`endName` is the element's local name — used to detect the matching
// close tag and return). Emits JATS equivalents of the TEI children.
// Recurses into structural subtrees (div, listBibl, biblStruct) via
// dedicated helpers.
func (c *converter) convertContainer(dec *xml.Decoder, endName string) error {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("jatsconv: walk %s: %w", endName, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := c.handleStart(dec, t); err != nil {
				return err
			}
		case xml.EndElement:
			if t.Name.Local == endName {
				return nil
			}
		case xml.CharData:
			c.writeString(escapeText(string(t)))
		}
	}
}

// handleStart translates one TEI start element into its JATS
// equivalent. For structural subtrees (listBibl, biblStruct, div,
// figure, table) the helper reads the whole subtree and emits a
// restructured JATS equivalent, then returns; for simple renames
// (p → p, head → title) it emits an opening tag and recurses via
// convertContainer, then emits the closing tag.
func (c *converter) handleStart(dec *xml.Decoder, se xml.StartElement) error {
	local := se.Name.Local
	switch local {
	case "listBibl":
		return c.emitRefList(dec)
	case "div":
		return c.emitSection(dec, se)
	case "head":
		return c.emitTitle(dec)
	case "p":
		return c.emitParagraph(dec, se)
	case "figure":
		return c.emitFigure(dec, se)
	case "note":
		return c.emitNote(dec, se)
	case "biblStruct":
		// A stray biblStruct outside listBibl — emit as ref anyway.
		return c.emitRef(dec, se)
	default:
		// Unknown element: skip its content silently. Preserves a
		// well-formed output at the cost of losing information; the
		// alternative (copying the source bytes) risks emitting TEI-
		// namespaced content that doesn't resolve under the JATS
		// default namespace.
		return skipElement(dec, local)
	}
}

// emitSection writes a <sec> wrapper for a TEI <div>. Copies the
// div's own id/type attributes as JATS sec attributes (`id`,
// `sec-type`) where they map cleanly.
//
// sec-model requires exactly one <title> (or a label + optional
// title). GROBID emits some divs without a <head> child — those
// need a synthesized <title> or the whole article fails to
// validate. Children are converted into a scratch buffer so we can
// peek for an emitted <title> and prepend a placeholder when the
// source didn't provide one.
func (c *converter) emitSection(dec *xml.Decoder, se xml.StartElement) error {
	var attrs strings.Builder
	for _, a := range se.Attr {
		switch a.Name.Local {
		case "id":
			attrs.WriteString(` id="`)
			attrs.WriteString(escapeAttr(a.Value))
			attrs.WriteString(`"`)
		case "type":
			attrs.WriteString(` sec-type="`)
			attrs.WriteString(escapeAttr(a.Value))
			attrs.WriteString(`"`)
		}
	}
	sub := &converter{src: c.src}
	if err := sub.convertContainer(dec, "div"); err != nil {
		return err
	}
	content := sub.out.String()
	c.writeString("<sec")
	c.writeString(attrs.String())
	c.writeString(">")
	if !strings.HasPrefix(strings.TrimSpace(content), "<title>") {
		c.writeString("<title>Section</title>")
	}
	c.writeString(content)
	c.writeString("</sec>\n")
	return nil
}

// emitTitle writes a <title> element for a TEI <head>. Content is
// pass-through text (GROBID heads rarely contain nested markup).
func (c *converter) emitTitle(dec *xml.Decoder) error {
	text, err := readElementText(dec, "head")
	if err != nil {
		return err
	}
	c.writeString("<title>")
	c.writeString(escapeText(strings.TrimSpace(text)))
	c.writeString("</title>\n")
	return nil
}

// emitParagraph writes a <p> element. TEI paragraph attributes
// don't map cleanly to JATS so they're dropped; the text content
// is preserved and any inline markup (tp:taxon-name from a prior
// annotator run) passes through by attribute copy.
func (c *converter) emitParagraph(dec *xml.Decoder, _ xml.StartElement) error {
	c.writeString("<p>")
	if err := c.convertInline(dec, "p"); err != nil {
		return err
	}
	c.writeString("</p>\n")
	return nil
}

// convertInline is the inline-content counterpart of convertContainer.
// It preserves nested tp: / gn: markup byte-for-byte via re-emission
// (needed because those elements are added by the annotator layered
// on top of this converter's output), and translates known inline
// TEI elements to their JATS equivalents.
func (c *converter) convertInline(dec *xml.Decoder, endName string) error {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("jatsconv: walk inline %s: %w", endName, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			prefix := prefixOf(t)
			// Preserve tp:/gn: prefixed elements exactly — those
			// come from the annotator and describe extraction
			// metadata JATS doesn't own.
			if prefix == "tp" || prefix == "gn" {
				c.emitOpenTagPreservePrefix(t)
				if err := c.convertInline(dec, local); err != nil {
					return err
				}
				c.writeString("</" + prefix + ":" + local + ">")
				continue
			}
			switch local {
			case "hi":
				c.writeString("<italic>")
				if err := c.convertInline(dec, "hi"); err != nil {
					return err
				}
				c.writeString("</italic>")
			case "ref":
				c.writeString("<xref")
				for _, a := range t.Attr {
					if a.Name.Local == "target" {
						c.writeString(` rid="`)
						c.writeString(escapeAttr(strings.TrimPrefix(a.Value, "#")))
						c.writeString(`"`)
					}
				}
				c.writeString(">")
				if err := c.convertInline(dec, "ref"); err != nil {
					return err
				}
				c.writeString("</xref>")
			default:
				// Unknown inline element: emit its text but drop
				// the wrapper. Safer than emitting an unrecognized
				// element into JATS content.
				if err := c.convertInline(dec, local); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == endName {
				return nil
			}
		case xml.CharData:
			c.writeString(escapeText(string(t)))
		}
	}
}

// emitOpenTagPreservePrefix writes a start tag preserving the
// caller-provided element prefix (e.g. `<tp:taxon-name ...>`).
// Attributes with a namespace prefix likewise round-trip.
func (c *converter) emitOpenTagPreservePrefix(se xml.StartElement) {
	prefix := prefixOf(se)
	c.writeByte('<')
	if prefix != "" {
		c.writeString(prefix + ":")
	}
	c.writeString(se.Name.Local)
	for _, a := range se.Attr {
		c.writeByte(' ')
		if p := attrPrefix(a); p != "" {
			c.writeString(p + ":")
		}
		c.writeString(a.Name.Local)
		c.writeString(`="`)
		c.writeString(escapeAttr(a.Value))
		c.writeByte('"')
	}
	c.writeByte('>')
}

// emitFigure writes a JATS <fig> wrapper for a TEI <figure>. JATS
// <fig> allows at most one <caption>; GROBID often emits both a
// <head> (short label like "FIGURE 1.") and a <figDesc> (the full
// caption). We concatenate them into a single caption, preferring
// the head as a title-like prefix followed by the description.
//
// GROBID also emits a <graphic coords="page,x,y,w,h"> child on
// each figure when coord-tracking is enabled. Preserve those as
// gn:pdf-page + gn:pdf-bbox attributes on the JATS <fig> so the
// reader can jump the PDF pane to the source page when a curator
// clicks a figure xref.
func (c *converter) emitFigure(dec *xml.Decoder, se xml.StartElement) error {
	var idAttr string
	for _, a := range se.Attr {
		if a.Name.Local == "id" {
			idAttr = a.Value
		}
	}
	var headText, descText, coordsAttr string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("jatsconv: walk figure: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "head":
				text, err := readElementText(dec, "head")
				if err != nil {
					return err
				}
				headText = strings.TrimSpace(text)
			case "figDesc":
				text, err := readElementText(dec, "figDesc")
				if err != nil {
					return err
				}
				descText = strings.TrimSpace(text)
			case "graphic":
				// GROBID's <graphic coords="page,x,y,w,h"> carries
				// the figure's source-page location. Preserve for
				// jump-to-PDF; only the first graphic's coords are
				// kept (rare for a figure to have more than one).
				for _, a := range t.Attr {
					if a.Name.Local == "coords" && coordsAttr == "" {
						coordsAttr = a.Value
					}
				}
				_ = skipElement(dec, "graphic")
			default:
				_ = skipElement(dec, t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local == "figure" {
				page, bbox := splitCoords(coordsAttr)
				c.writeString("<fig")
				if idAttr != "" {
					c.writeString(` id="`)
					c.writeString(escapeAttr(idAttr))
					c.writeString(`"`)
				}
				if page != "" {
					c.writeString(` gn:pdf-page="`)
					c.writeString(escapeAttr(page))
					c.writeString(`"`)
				}
				if bbox != "" {
					c.writeString(` gn:pdf-bbox="`)
					c.writeString(escapeAttr(bbox))
					c.writeString(`"`)
				}
				c.writeString(">\n")
				// figDesc is the full caption (typically bilingual
				// on multi-language journals — two "Figure N." /
				// "Figura N." runs in one figDesc). Head is just a
				// short bilingual-label duplicate; concatenating
				// both produces "Figure 1 . Figura 1 . Figure 1.
				// Dynamics... Figura 1. Dinámica..." with the label
				// appearing 3-4 times. Prefer figDesc alone; head is
				// only used when figDesc is absent (rare — some
				// GROBID configurations emit head-only figures).
				captionText := descText
				if captionText == "" {
					captionText = headText
				}
				if captionText != "" {
					c.writeString("  <caption><p>")
					c.writeString(escapeText(captionText))
					c.writeString("</p></caption>\n")
				}
				c.writeString("</fig>\n")
				return nil
			}
		}
	}
	return nil
}

// splitCoords parses GROBID's coord string "page,x,y,w,h" into
// (page, "x,y,w,h") for emission as separate JATS attributes.
// GROBID may emit multiple region-specific coord tuples separated
// by semicolons for figures that span pages; we take only the
// first (source-page anchor for jump-to-PDF).
func splitCoords(coords string) (page, bbox string) {
	if coords == "" {
		return "", ""
	}
	// Multi-region coords: keep only the first tuple.
	if i := strings.Index(coords, ";"); i > 0 {
		coords = coords[:i]
	}
	parts := strings.SplitN(coords, ",", 2)
	if len(parts) != 2 {
		return coords, ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// emitNote writes a TEI <note> as a JATS <p> — para-level so it
// validates wherever body / sec / back allow paragraph content.
// The prior implementation emitted <named-content>, but that's
// phrase-level and fails schema validation when the note appears
// as a direct child of body (GROBID typically emits copyright /
// footer notes there). The note's `type` attribute is dropped as
// there is no clean JATS attribute for it on <p>; a downstream
// pass can re-interpret the note text if the distinction matters.
func (c *converter) emitNote(dec *xml.Decoder, _ xml.StartElement) error {
	text, err := readElementText(dec, "note")
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	c.writeString("<p>")
	c.writeString(escapeText(trimmed))
	c.writeString("</p>\n")
	return nil
}

// emitRefList reads a TEI <listBibl> subtree and emits a JATS
// <ref-list> containing one <ref> per <biblStruct> child. Each
// <ref>'s child is a JATS <element-citation> derived from the
// biblStruct's structured fields — see emitRef.
func (c *converter) emitRefList(dec *xml.Decoder) error {
	c.writeString("<ref-list>\n")
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("jatsconv: walk listBibl: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "biblStruct":
				if err := c.emitRef(dec, t); err != nil {
					return err
				}
			case "head":
				text, err := readElementText(dec, "head")
				if err != nil {
					return err
				}
				c.writeString("  <title>")
				c.writeString(escapeText(strings.TrimSpace(text)))
				c.writeString("</title>\n")
			default:
				_ = skipElement(dec, t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local == "listBibl" {
				c.writeString("</ref-list>\n")
				return nil
			}
		}
	}
}

// emitRef parses a single TEI <biblStruct> subtree into an in-
// memory reference struct, then emits a JATS <ref> containing an
// <element-citation> with the structured fields. Preserves the
// biblStruct's xml:id as the ref's id (JATS xref targets bind to
// this).
func (c *converter) emitRef(dec *xml.Decoder, se xml.StartElement) error {
	var refID string
	for _, a := range se.Attr {
		if a.Name.Local == "id" {
			refID = a.Value
		}
	}
	ref, err := parseBiblStruct(dec)
	if err != nil {
		return err
	}
	c.writeString("  <ref")
	if refID != "" {
		c.writeString(` id="`)
		c.writeString(escapeAttr(refID))
		c.writeString(`"`)
	}
	c.writeString(">\n")
	c.writeString("    <element-citation")
	if ref.PubType != "" {
		c.writeString(` publication-type="`)
		c.writeString(escapeAttr(ref.PubType))
		c.writeString(`"`)
	}
	c.writeString(">\n")
	if len(ref.Authors) > 0 {
		c.writeString("      <person-group person-group-type=\"author\">\n")
		for _, a := range ref.Authors {
			c.writeString("        <name>\n")
			c.writeString("          <surname>")
			c.writeString(escapeText(a.Surname))
			c.writeString("</surname>\n")
			if a.Given != "" {
				c.writeString("          <given-names>")
				c.writeString(escapeText(a.Given))
				c.writeString("</given-names>\n")
			}
			c.writeString("        </name>\n")
		}
		c.writeString("      </person-group>\n")
	}
	if ref.ArticleTitle != "" {
		c.writeString("      <article-title>")
		c.writeString(escapeText(ref.ArticleTitle))
		c.writeString("</article-title>\n")
	}
	if ref.Source != "" {
		c.writeString("      <source>")
		c.writeString(escapeText(ref.Source))
		c.writeString("</source>\n")
	}
	if ref.Year != "" {
		c.writeString("      <year>")
		c.writeString(escapeText(ref.Year))
		c.writeString("</year>\n")
	}
	if ref.Volume != "" {
		c.writeString("      <volume>")
		c.writeString(escapeText(ref.Volume))
		c.writeString("</volume>\n")
	}
	if ref.Issue != "" {
		c.writeString("      <issue>")
		c.writeString(escapeText(ref.Issue))
		c.writeString("</issue>\n")
	}
	if ref.FPage != "" {
		c.writeString("      <fpage>")
		c.writeString(escapeText(ref.FPage))
		c.writeString("</fpage>\n")
	}
	if ref.LPage != "" {
		c.writeString("      <lpage>")
		c.writeString(escapeText(ref.LPage))
		c.writeString("</lpage>\n")
	}
	if ref.Publisher != "" {
		c.writeString("      <publisher-name>")
		c.writeString(escapeText(ref.Publisher))
		c.writeString("</publisher-name>\n")
	}
	if ref.PublisherLoc != "" {
		c.writeString("      <publisher-loc>")
		c.writeString(escapeText(ref.PublisherLoc))
		c.writeString("</publisher-loc>\n")
	}
	if ref.DOI != "" {
		c.writeString("      <pub-id pub-id-type=\"doi\">")
		c.writeString(escapeText(ref.DOI))
		c.writeString("</pub-id>\n")
	}
	c.writeString("    </element-citation>\n")
	c.writeString("  </ref>\n")
	return nil
}

// biblRef is the intermediate representation of a TEI <biblStruct>
// subtree during reference conversion. Fields parallel the JATS
// element-citation children and grobid.BibHeader's schema.
type biblRef struct {
	PubType      string // "journal", "book", ...; inferred from monogr/title/@level
	Authors      []author
	ArticleTitle string
	Source       string
	Year         string
	Volume       string
	Issue        string
	FPage        string
	LPage        string
	Publisher    string
	PublisherLoc string
	DOI          string
}

// parseBiblStruct walks a single biblStruct subtree and returns
// the flattened biblRef. The subtree is consumed up to and
// including the </biblStruct> end tag; the caller's decoder is
// positioned immediately after.
func parseBiblStruct(dec *xml.Decoder) (biblRef, error) {
	var ref biblRef
	var current *author
	// Track the enclosing element so <title level="j"> under monogr
	// can be distinguished from <title level="a"> under analytic.
	var inAnalytic, inMonogr bool
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return ref, nil
		}
		if err != nil {
			return ref, fmt.Errorf("jatsconv: parse biblStruct: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "analytic":
				inAnalytic = true
				ref.PubType = "journal"
			case "monogr":
				inMonogr = true
				if ref.PubType == "" {
					ref.PubType = "book"
				}
			case "imprint":
				// imprint is a passthrough wrapper; its children
				// (biblScope, date, publisher, pubPlace) drive the
				// output based on element name alone.
			case "author":
				current = &author{}
			case "forename":
				if current != nil {
					if text, ok := readNextCharData(dec); ok {
						text = strings.TrimSpace(text)
						if hasLetter(text) {
							if current.Given == "" {
								current.Given = text
							} else {
								current.Given += " " + text
							}
						}
					}
				}
			case "surname":
				if current != nil {
					if text, ok := readNextCharData(dec); ok {
						current.Surname = strings.TrimSpace(text)
					}
				}
			case "title":
				text, err := readElementText(dec, "title")
				if err != nil {
					return ref, err
				}
				text = strings.TrimSpace(text)
				switch {
				case inAnalytic:
					if ref.ArticleTitle == "" {
						ref.ArticleTitle = text
					}
				case inMonogr:
					if ref.Source == "" {
						ref.Source = text
					}
				}
			case "biblScope":
				var unit, from, to string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "unit":
						unit = a.Value
					case "from":
						from = a.Value
					case "to":
						to = a.Value
					}
				}
				inner, err := readElementText(dec, "biblScope")
				if err != nil {
					return ref, err
				}
				inner = strings.TrimSpace(inner)
				switch unit {
				case "volume":
					if inner != "" {
						ref.Volume = inner
					} else if from != "" {
						ref.Volume = from
					}
				case "issue":
					if inner != "" {
						ref.Issue = inner
					} else if from != "" {
						ref.Issue = from
					}
				case "page":
					if from != "" {
						ref.FPage = from
					}
					if to != "" {
						ref.LPage = to
					}
					if from == "" && inner != "" {
						fp, lp := splitPageRange(inner)
						ref.FPage = fp
						if lp != "" {
							ref.LPage = lp
						}
					}
				}
			case "date":
				var when string
				for _, a := range t.Attr {
					if a.Name.Local == "when" {
						when = a.Value
					}
				}
				if when != "" {
					if i := strings.Index(when, "-"); i > 0 {
						ref.Year = when[:i]
					} else {
						ref.Year = when
					}
				}
			case "idno":
				var typ string
				for _, a := range t.Attr {
					if a.Name.Local == "type" {
						typ = a.Value
					}
				}
				text, err := readElementText(dec, "idno")
				if err != nil {
					return ref, err
				}
				if strings.EqualFold(typ, "DOI") {
					ref.DOI = strings.TrimSpace(text)
				}
			case "publisher":
				text, err := readElementText(dec, "publisher")
				if err != nil {
					return ref, err
				}
				ref.Publisher = strings.TrimSpace(text)
			case "pubPlace":
				text, err := readElementText(dec, "pubPlace")
				if err != nil {
					return ref, err
				}
				ref.PublisherLoc = strings.TrimSpace(text)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "biblStruct":
				if current != nil && current.Surname != "" {
					ref.Authors = append(ref.Authors, *current)
				}
				return ref, nil
			case "author":
				if current != nil && current.Surname != "" {
					ref.Authors = append(ref.Authors, *current)
				}
				current = nil
			case "analytic":
				inAnalytic = false
			case "monogr":
				inMonogr = false
			}
		}
	}
}

// seekTo advances the decoder past every token until it consumes
// (and discards) a StartElement whose local name is `name`. Returns
// an error at EOF if the element isn't found.
func seekTo(dec *xml.Decoder, name string) error {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return fmt.Errorf("jatsconv: <%s> not found", name)
		}
		if err != nil {
			return err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == name {
			return nil
		}
	}
}

// skipElement discards tokens until it consumes the matching end
// element for `name`. Depth-tracks so nested identical names don't
// return early.
func skipElement(dec *xml.Decoder, name string) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == name {
				depth++
			}
		case xml.EndElement:
			if t.Name.Local == name {
				depth--
			}
		}
	}
	return nil
}

// readElementText consumes tokens until the matching end element
// for `name`, concatenating CharData along the way. Nested markup
// contributes its text content but its element wrappers are dropped
// — appropriate for fields where only the text matters (titles,
// idno values, publisher names).
func readElementText(dec *xml.Decoder, name string) (string, error) {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err == io.EOF {
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			if t.Name.Local == name {
				depth++
			}
		case xml.EndElement:
			if t.Name.Local == name {
				depth--
			}
		}
	}
	return b.String(), nil
}

// readNextCharData returns the next CharData token from the decoder,
// or ("", false) when the next token isn't CharData. Mirrors
// grobid.readCharData semantics so parseBiblStruct can pull direct
// text content from forename/surname without walking the whole
// element.
func readNextCharData(dec *xml.Decoder) (string, bool) {
	tok, err := dec.Token()
	if err != nil {
		return "", false
	}
	cd, ok := tok.(xml.CharData)
	if !ok {
		return "", false
	}
	return string(cd), true
}

// hasLetter reports whether s contains at least one ASCII letter.
// Parallels grobid.hasLetter; duplicated to avoid an internal
// dependency on grobid's unexported helpers.
func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return true
		}
	}
	return false
}

// prefixOf returns the namespace prefix (e.g. "tp") of an element
// when its Name.Space matches one of the recognized namespace URIs.
// Encoding/xml resolves prefixed elements by URI, not by prefix, so
// this mapping recovers the intended short form for pass-through.
func prefixOf(se xml.StartElement) string {
	switch se.Name.Space {
	case taxPubNamespace:
		return "tp"
	case gnNamespace:
		return "gn"
	}
	return ""
}

// attrPrefix returns the short prefix (tp / gn) for a prefixed
// attribute, or "" when the attribute isn't namespaced.
func attrPrefix(a xml.Attr) string {
	switch a.Name.Space {
	case taxPubNamespace:
		return "tp"
	case gnNamespace:
		return "gn"
	}
	return ""
}

var textEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

var attrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }
