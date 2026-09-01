package hive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

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
	// ApplyBBoxModifyToJATS to replace the &lt;table&gt; block inside
	// the target &lt;table-wrap&gt;. Empty means "no row change".
	TableRows [][]string `json:"table_rows,omitempty"`
}

// ApplyBBoxModifyToJATS patches the element identified by
// c.TargetElementID with the curator's changes from the
// bbox-modify payload. Edit axes:
//
//   - Content-type swap (figure ↔ table): replaces the element
//     name from <fig> to <table-wrap> or vice versa. All
//     attributes and children stay put.
//   - Label / caption text: replaces the inner text of the
//     element's <caption><p>...</p></caption> block. Curator's
//     new caption text goes in verbatim.
//   - BBox coordinates: rewrites the element's hive:pdf-bbox
//     attribute to the curator's resized rectangle. Figure PNG
//     re-crop lands automatically on the trailing figextract pass
//     that runs after every apply. Table row re-parse is handled
//     here: when the payload carries fresh TableRows (populated by
//     the handler via pdftext.ExtractTable), the <table> block
//     inside the target <table-wrap> is replaced with a fresh
//     render so a resize that uncovers missing rows makes them
//     visible in the reader without a manual reparse.
//
// Returns the mutated JATS on success. Returns an error when the
// target element isn't found in the JATS (either it was never
// there, or another correction removed it) so the caller can mark
// the correction as reverted.
func ApplyBBoxModifyToJATS(jats []byte, c *Correction) ([]byte, error) {
	if c.Kind != CorrectionKindBBoxModify {
		return nil, fmt.Errorf("apply bbox-modify: expected bbox-modify kind, got %s", c.Kind)
	}
	if c.TargetElementID == "" {
		return nil, fmt.Errorf("apply bbox-modify: target_element_id required")
	}
	var payload BBoxModifyPayload
	if c.Payload != "" && c.Payload != "{}" {
		if err := json.Unmarshal([]byte(c.Payload), &payload); err != nil {
			return nil, fmt.Errorf("apply bbox-modify: parse payload: %w", err)
		}
	}
	// Find the target element by id — walk the bytes to locate the
	// opening tag whose id="<target>" attribute matches.
	openIdx, closeIdx, elemName, ok := findElementByID(jats, c.TargetElementID)
	if !ok {
		return nil, fmt.Errorf("apply bbox-modify: target element %s not found in JATS",
			c.TargetElementID)
	}

	// Determine new element name if content type changed.
	newElemName := elemName
	if payload.Corrected.ContentType != "" {
		switch payload.Corrected.ContentType {
		case "figure":
			newElemName = "fig"
		case "table":
			newElemName = "table-wrap"
		default:
			return nil, fmt.Errorf("apply bbox-modify: unsupported content_type %q",
				payload.Corrected.ContentType)
		}
	}

	// Extract the full element bytes.
	elemBytes := jats[openIdx:closeIdx]
	newBytes := elemBytes

	// Replace element name if changed. Both opening tag and closing
	// tag get renamed. Element name occupies bytes right after '<'
	// in the opening tag and after '</' in the closing tag.
	if newElemName != elemName {
		newBytes = renameElementTags(newBytes, elemName, newElemName)
	}

	// Rewrite hive:pdf-bbox on the opening tag if the corrected
	// payload carries new coordinates. Same "%.2f,%.2f,%.2f,%.2f"
	// format that BBoxAddFigureJATSElement and jatsconv both emit so
	// downstream parsers (labels.parseBBoxAttr, figextract) see a
	// consistent representation.
	if payload.Corrected.BBox != nil {
		newBBoxAttr := fmt.Sprintf("%.2f,%.2f,%.2f,%.2f",
			payload.Corrected.BBox.X, payload.Corrected.BBox.Y,
			payload.Corrected.BBox.W, payload.Corrected.BBox.H)
		newBytes = replacePdfBBoxAttr(newBytes, newBBoxAttr)
	}

	// Replace caption content if new caption is provided.
	if payload.Corrected.Caption != "" {
		safeCaption := xmlEscape(payload.Corrected.Caption)
		newCaption := "<caption><p>" + safeCaption + "</p></caption>"
		newBytes = replaceCaptionBlock(newBytes, newCaption)
	} else if payload.Corrected.Label != "" {
		// Caption not overridden but label is — put the label into
		// the caption placeholder text so the trailing "Figures and
		// tables" list picks up the corrected label.
		safeLabel := xmlEscape(payload.Corrected.Label)
		newCaption := "<caption><p>" + safeLabel + "</p></caption>"
		newBytes = replaceCaptionBlock(newBytes, newCaption)
	}

	// Splice a fresh <table> block when the payload carries newly
	// parsed rows (populated by the handler after a table resize via
	// pdftext.ExtractTable). Replaces any existing <table> or inserts
	// one after the caption if none is present. Same
	// renderJATSTableFromRows shape used by bbox-add so the
	// downstream reader sees consistent structure regardless of how
	// the region originated.
	if len(payload.Corrected.TableRows) > 0 {
		newTable := renderJATSTableFromRows(payload.Corrected.TableRows)
		if newTable != "" {
			newBytes = replaceTableBlock(newBytes, newTable)
		}
	}

	var out bytes.Buffer
	out.Grow(len(jats) + len(newBytes) - len(elemBytes))
	out.Write(jats[:openIdx])
	out.Write(newBytes)
	out.Write(jats[closeIdx:])
	return out.Bytes(), nil
}

// ApplyBBoxRemoveToJATS removes the fig/table-wrap element
// identified by c.TargetElementID from the JATS bytes entirely.
// Used to suppress GROBID false positives — a bbox-remove
// correction records "this element shouldn't exist" as a durable,
// re-applicable decision that survives re-import cycles.
//
// Idempotent: if the target is already absent (e.g., an earlier
// re-import already stripped it), returns the JATS unchanged.
// The correction row stays as a training signal regardless.
func ApplyBBoxRemoveToJATS(jats []byte, c *Correction) ([]byte, error) {
	if c.Kind != CorrectionKindBBoxRemove {
		return nil, fmt.Errorf("apply bbox-remove: expected bbox-remove kind, got %s", c.Kind)
	}
	if c.TargetElementID == "" {
		return nil, fmt.Errorf("apply bbox-remove: target_element_id required")
	}
	openIdx, closeIdx, _, ok := findElementByID(jats, c.TargetElementID)
	if !ok {
		return jats, nil
	}
	var out bytes.Buffer
	out.Grow(len(jats) - (closeIdx - openIdx))
	out.Write(jats[:openIdx])
	out.Write(jats[closeIdx:])
	return out.Bytes(), nil
}

// ApplyRefMergeToJATS merges the <ref> named by c.TargetElementID
// into the preceding <ref> in the same <ref-list>. Used to repair
// GROBID's ref-tokenizer glitches — a messy inline URL or an
// address tail that gets parsed as a new author list, splitting a
// single citation into two rows.
//
// Merge strategy: take the target ref's element-citation textContent
// (everything after the leading author/year fields that GROBID gave
// it), append it as a trailing <comment> child on the previous
// ref's element-citation, then delete the target ref. Using
// <comment> keeps the merged text visible to curators without
// pretending it's structured data — the split lost the field
// boundaries and we're not going to reinvent them from a fragment.
//
// Idempotent: returns the JATS unchanged when the target ref is
// absent (already merged by a prior apply pass, or a re-import
// happened to produce a cleaner tokenization).
func ApplyRefMergeToJATS(jats []byte, c *Correction) ([]byte, error) {
	if c.Kind != CorrectionKindRefMerge {
		return nil, fmt.Errorf("apply ref-merge: expected ref-merge kind, got %s", c.Kind)
	}
	if c.TargetElementID == "" {
		return nil, fmt.Errorf("apply ref-merge: target_element_id required")
	}
	targetOpen, targetClose, ok := findRefByID(jats, c.TargetElementID)
	if !ok {
		return jats, nil
	}
	// Walk backward from targetOpen to find the previous <ref>.
	prevOpen, prevClose, ok := findPreviousRef(jats, targetOpen)
	if !ok {
		// No prior ref — nothing to merge into. Keep the JATS as
		// is; the curator can revert the correction if they meant
		// a different target.
		return jats, nil
	}
	// Extract the target's plain-text content and its element-citation
	// wrapper (if present, to know where the field-level content sits).
	target := jats[targetOpen:targetClose]
	fragment := extractRefTextForMerge(target)
	// Splice the fragment as a trailing <comment> into the previous
	// ref's element-citation (or mixed-citation) block, then remove
	// the target ref entirely.
	prev := jats[prevOpen:prevClose]
	mergedPrev := appendCommentToCitation(prev, fragment)

	var out bytes.Buffer
	out.Grow(len(jats) - (targetClose - targetOpen))
	out.Write(jats[:prevOpen])
	out.Write(mergedPrev)
	// Skip any whitespace + the target ref itself.
	out.Write(trimLeadingWhitespaceBetween(jats[prevClose:targetOpen]))
	out.Write(jats[targetClose:])
	return out.Bytes(), nil
}

// findRefByID locates the byte range of a <ref id="target">...</ref>
// element within a JATS document. Distinct from findElementByID
// because we're matching on the <ref> element name specifically —
// its ids look like "b23", "b24" and could collide with other id
// attributes if we didn't scope by element name.
func findRefByID(src []byte, targetID string) (openIdx, closeIdx int, ok bool) {
	pattern := `<ref\b[^>]*\bid="` + regexp.QuoteMeta(targetID) + `"[^>]*>`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, 0, false
	}
	loc := re.FindIndex(src)
	if loc == nil {
		return 0, 0, false
	}
	closeTag := []byte("</ref>")
	closeStart := bytes.Index(src[loc[0]:], closeTag)
	if closeStart < 0 {
		return 0, 0, false
	}
	return loc[0], loc[0] + closeStart + len(closeTag), true
}

// findPreviousRef finds the <ref>…</ref> whose closing tag ends
// closest to (but before) the given cursor position. Used by
// ApplyRefMergeToJATS to identify the merge target's predecessor.
func findPreviousRef(src []byte, cursor int) (openIdx, closeIdx int, ok bool) {
	if cursor <= 0 {
		return 0, 0, false
	}
	closeTag := []byte("</ref>")
	prevClose := bytes.LastIndex(src[:cursor], closeTag)
	if prevClose < 0 {
		return 0, 0, false
	}
	closeIdx = prevClose + len(closeTag)
	// Find the matching <ref … > opening tag. Scan backward for
	// the last "<ref " or "<ref>" before prevClose.
	openPattern := regexp.MustCompile(`<ref\b[^>]*>`)
	locs := openPattern.FindAllIndex(src[:prevClose], -1)
	if len(locs) == 0 {
		return 0, 0, false
	}
	openIdx = locs[len(locs)-1][0]
	return openIdx, closeIdx, true
}

// extractRefTextForMerge pulls the human-readable text out of a
// spurious <ref>…</ref> block to append as a trailing comment on
// the merge target's predecessor. Strips XML tags and collapses
// runs of whitespace so the merged output reads naturally.
func extractRefTextForMerge(refElem []byte) string {
	// Strip all tags.
	tagRe := regexp.MustCompile(`<[^>]+>`)
	plain := tagRe.ReplaceAllString(string(refElem), " ")
	// Collapse whitespace.
	wsRe := regexp.MustCompile(`\s+`)
	plain = strings.TrimSpace(wsRe.ReplaceAllString(plain, " "))
	return plain
}

// appendCommentToCitation splices a <comment> child holding the
// merged-in text into the previous ref's element-citation (or
// mixed-citation, or the ref itself when neither is present). The
// <comment> element is a JATS-native structured slot for
// annotator commentary on a citation — a reasonable home for
// text that got tokenized as its own row but really belongs to
// this citation.
func appendCommentToCitation(refElem []byte, fragment string) []byte {
	if fragment == "" {
		return refElem
	}
	comment := "<comment>" + xmlEscape(fragment) + "</comment>"
	// Prefer inserting before </element-citation> or </mixed-citation>.
	// Falls back to inserting before </ref> when neither wrapper
	// exists (rare — jatsconv always wraps).
	for _, closer := range []string{"</element-citation>", "</mixed-citation>", "</ref>"} {
		idx := bytes.LastIndex(refElem, []byte(closer))
		if idx < 0 {
			continue
		}
		var out bytes.Buffer
		out.Grow(len(refElem) + len(comment))
		out.Write(refElem[:idx])
		out.WriteString(comment)
		out.Write(refElem[idx:])
		return out.Bytes()
	}
	return refElem
}

// trimLeadingWhitespaceBetween strips leading whitespace from a
// slice used as the gap between two spliced regions. Prevents the
// merge from leaving a doubled blank line where the removed <ref>
// used to sit.
func trimLeadingWhitespaceBetween(gap []byte) []byte {
	i := 0
	for i < len(gap) && (gap[i] == ' ' || gap[i] == '\t' || gap[i] == '\n' || gap[i] == '\r') {
		i++
	}
	return gap[i:]
}

// findElementByID locates the byte range of the <name id="target"
// ...>...</name> element and returns the range plus the element
// name. Returns ok=false when the id doesn't appear in a
// fig/table-wrap opening tag.
//
// Precise regex-based scan required because naive substring search
// for `id="target"` also matches `rid="target"` in <xref> elements.
func findElementByID(src []byte, targetID string) (openIdx, closeIdx int, elemName string, ok bool) {
	pattern := `<(fig|table-wrap)\b[^>]*\bid="` + regexp.QuoteMeta(targetID) + `"[^>]*>`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, 0, "", false
	}
	loc := re.FindSubmatchIndex(src)
	if loc == nil {
		return 0, 0, "", false
	}
	tagStart := loc[0]
	elemName = string(src[loc[2]:loc[3]])
	// Find the matching close tag </elemName>. fig/table-wrap
	// don't nest each other in JATS bodies emitted by jatsconv or
	// bbox-add, so a simple next-match walk suffices.
	closeTag := []byte("</" + elemName + ">")
	closeStart := bytes.Index(src[tagStart:], closeTag)
	if closeStart < 0 {
		return 0, 0, "", false
	}
	return tagStart, tagStart + closeStart + len(closeTag), elemName, true
}

// renameElementTags rewrites both the opening and closing tag
// names of an isolated element. Preserves attributes on the
// opening tag.
func renameElementTags(elem []byte, oldName, newName string) []byte {
	s := string(elem)
	// Opening: "<oldName" → "<newName". Only the first occurrence
	// (the element's own opening tag).
	s = strings.Replace(s, "<"+oldName, "<"+newName, 1)
	// Closing: "</oldName>" → "</newName>". Only the last.
	closeOld := "</" + oldName + ">"
	closeNew := "</" + newName + ">"
	if lastIdx := strings.LastIndex(s, closeOld); lastIdx >= 0 {
		s = s[:lastIdx] + closeNew + s[lastIdx+len(closeOld):]
	}
	return []byte(s)
}

// pdfBBoxAttrRegex matches the hive:pdf-bbox="..." attribute on
// the element's opening tag. Anchored to the first '<' so we only
// touch the outer element, not any nested markup that could
// theoretically carry the same attribute.
var pdfBBoxAttrRegex = regexp.MustCompile(`(<[^>]*\bhive:pdf-bbox=")[^"]*(")`)

// replacePdfBBoxAttr rewrites the hive:pdf-bbox="..." value on the
// element's opening tag. Adds the attribute just before the tag's
// closing '>' when no existing attribute is present so a resize
// promotes a bbox-less element into one figextract can crop.
func replacePdfBBoxAttr(elem []byte, newValue string) []byte {
	if pdfBBoxAttrRegex.Match(elem) {
		return pdfBBoxAttrRegex.ReplaceAll(elem, []byte("${1}"+newValue+"${2}"))
	}
	gt := bytes.IndexByte(elem, '>')
	if gt < 0 {
		return elem
	}
	var out bytes.Buffer
	out.Grow(len(elem) + len(newValue) + 20)
	out.Write(elem[:gt])
	out.WriteString(` hive:pdf-bbox="`)
	out.WriteString(newValue)
	out.WriteString(`"`)
	out.Write(elem[gt:])
	return out.Bytes()
}

// replaceTableBlock swaps the first <table>...</table> block in
// the element for the given replacement. When no <table> is
// present, inserts one after the caption; when there's no caption
// either, appends before the element's closing tag. Mirrors the
// caption-block helper's shape for consistency.
func replaceTableBlock(elem []byte, newTable string) []byte {
	s := string(elem)
	tabOpen := strings.Index(s, "<table>")
	if tabOpen >= 0 {
		tabClose := strings.Index(s[tabOpen:], "</table>")
		if tabClose < 0 {
			return elem
		}
		end := tabOpen + tabClose + len("</table>")
		return []byte(s[:tabOpen] + newTable + s[end:])
	}
	// No existing table — insert after the caption block if
	// present; otherwise before the closing tag.
	if capClose := strings.Index(s, "</caption>"); capClose >= 0 {
		insertAt := capClose + len("</caption>")
		return []byte(s[:insertAt] + newTable + s[insertAt:])
	}
	lastLT := strings.LastIndex(s, "</")
	if lastLT < 0 {
		return elem
	}
	return []byte(s[:lastLT] + newTable + s[lastLT:])
}

// replaceCaptionBlock swaps the first <caption>...</caption>
// block in the element for the given replacement. Insertion point
// is right after the opening tag when no existing caption exists.
func replaceCaptionBlock(elem []byte, newCaption string) []byte {
	s := string(elem)
	capOpen := strings.Index(s, "<caption>")
	if capOpen < 0 {
		// No caption yet — insert after the first '>' (end of the
		// element's opening tag).
		gt := strings.Index(s, ">")
		if gt < 0 {
			return elem
		}
		return []byte(s[:gt+1] + newCaption + s[gt+1:])
	}
	capClose := strings.Index(s[capOpen:], "</caption>")
	if capClose < 0 {
		return elem
	}
	end := capOpen + capClose + len("</caption>")
	return []byte(s[:capOpen] + newCaption + s[end:])
}

// figuresSecOpenRegex matches the opening tag of the trailing
// figures-and-tables sec that jatsconv emits. Case-sensitive; matches
// the exact string jatsconv writes so a mismatch means a schema
// change we should notice rather than silently work around.
var figuresSecOpenRegex = regexp.MustCompile(`<sec sec-type="figures">`)

// closingBodyOrArticleRegex is the fallback splice point when no
// figures sec exists yet — insert a new sec just before </body>.
// Matches </body> or </article> as a permissive fallback (some
// simpler test JATS bytes may skip <body>).
var closingBodyOrArticleRegex = regexp.MustCompile(`</body>|</article>`)

// ApplyBBoxAddToJATS splices the given bbox-add correction into the
// provided JATS bytes and returns the mutated JATS. Two cases:
//
//   - The document already has a trailing figures-and-tables sec
//     (jatsconv's default when the source paper has any figures) —
//     the new fig/table element is appended just inside its closing
//     </sec>.
//   - The document has no such sec — a new sec is inserted just
//     before </body> with a title of "Figures and tables" and the
//     new element as its only child. Matches the shape jatsconv
//     would emit for a paper with a single fig, so subsequent
//     jatsconv passes over the same content look consistent.
//
// Returns an error only for malformed input (correction rejects,
// JATS entirely missing the splice points). A well-formed JATS with
// a valid bbox-add correction always applies.
func ApplyBBoxAddToJATS(jats []byte, c *Correction) ([]byte, error) {
	elem, err := BBoxAddFigureJATSElement(c)
	if err != nil {
		return nil, err
	}

	// Case 1: existing figures sec — append inside it just before </sec>.
	if loc := figuresSecOpenRegex.FindIndex(jats); loc != nil {
		// Find the matching </sec>. Depth-tracking scan starting
		// from the end of the opening tag; a naive "next </sec>"
		// search would land inside a nested sec if the paper has
		// sub-secs, though jatsconv doesn't currently nest under
		// the figures sec. Cheap to be safe.
		start := loc[1]
		closeIdx := findMatchingCloseSec(jats, start)
		if closeIdx < 0 {
			return nil, fmt.Errorf("apply bbox-add: found <sec sec-type=\"figures\"> without matching </sec>")
		}
		var out bytes.Buffer
		out.Grow(len(jats) + len(elem))
		out.Write(jats[:closeIdx])
		out.WriteString(elem)
		out.Write(jats[closeIdx:])
		return out.Bytes(), nil
	}

	// Case 2: no figures sec — insert a fresh one before </body>.
	loc := closingBodyOrArticleRegex.FindIndex(jats)
	if loc == nil {
		return nil, fmt.Errorf("apply bbox-add: JATS has neither figures sec nor </body>")
	}
	newSec := `<sec sec-type="figures"><title>Figures and tables</title>` + elem + `</sec>`
	var out bytes.Buffer
	out.Grow(len(jats) + len(newSec))
	out.Write(jats[:loc[0]])
	out.WriteString(newSec)
	out.Write(jats[loc[0]:])
	return out.Bytes(), nil
}

// findMatchingCloseSec returns the byte offset of the </sec> that
// closes the sec whose opening tag ended at `start`. Returns -1 if
// no such closing exists (malformed JATS).
//
// Depth-tracking scan: count nested <sec ...> opens, decrement on
// each </sec>. Return the offset of the outer </sec>.
func findMatchingCloseSec(src []byte, start int) int {
	depth := 1
	i := start
	openTag := []byte("<sec")
	closeTag := []byte("</sec>")
	for i < len(src) {
		nextOpen := bytes.Index(src[i:], openTag)
		nextClose := bytes.Index(src[i:], closeTag)
		if nextClose < 0 {
			return -1
		}
		if nextOpen >= 0 && nextOpen < nextClose {
			// Confirm this is really an opening sec tag, not a
			// close (</sec> starts with <, so nextOpen matches
			// both). Peek at the char after "<sec".
			absOpen := i + nextOpen
			after := absOpen + len(openTag)
			if after < len(src) && (src[after] == ' ' || src[after] == '>' || src[after] == '\t' || src[after] == '\n') {
				depth++
			}
			i = absOpen + len(openTag)
			continue
		}
		depth--
		absClose := i + nextClose
		if depth == 0 {
			return absClose
		}
		i = absClose + len(closeTag)
	}
	return -1
}
