package refmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/hiveext"
)

// Appliers returns the refmanager-owned CorrectionApplier set. The
// pkg root walks this slice at Archive-open time and builds a
// map[correction.Kind]hiveext.CorrectionApplier keyed by each
// applier's Kind() — so adding a new correction kind here surfaces
// in Archive.ApplyCorrection automatically.
//
// All current kinds mutate JATS produced by GROBID + jatsconv, which
// is refmanager territory. If a future taxonomy-side correction kind
// arrives, pkg/taxonomy would grow a parallel Appliers() accessor
// and the pkg root would union the two.
func Appliers() []hiveext.CorrectionApplier {
	return []hiveext.CorrectionApplier{
		bboxAddApplier{},
		bboxModifyApplier{},
		bboxRemoveApplier{},
		refMergeApplier{},
		refEditApplier{},
		textRemoveApplier{},
		textRestoreApplier{},
	}
}

type bboxAddApplier struct{}

func (bboxAddApplier) Kind() correction.Kind { return correction.KindBBoxAdd }
func (bboxAddApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyBBoxAddToJATS(jats, c)
}

type bboxModifyApplier struct{}

func (bboxModifyApplier) Kind() correction.Kind { return correction.KindBBoxModify }
func (bboxModifyApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyBBoxModifyToJATS(jats, c)
}

type bboxRemoveApplier struct{}

func (bboxRemoveApplier) Kind() correction.Kind { return correction.KindBBoxRemove }
func (bboxRemoveApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyBBoxRemoveToJATS(jats, c)
}

type refMergeApplier struct{}

func (refMergeApplier) Kind() correction.Kind { return correction.KindRefMerge }
func (refMergeApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyRefMergeToJATS(jats, c)
}

type refEditApplier struct{}

func (refEditApplier) Kind() correction.Kind { return correction.KindRefEdit }
func (refEditApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyRefEditToJATS(jats, c)
}

type textRemoveApplier struct{}

func (textRemoveApplier) Kind() correction.Kind { return correction.KindTextRemove }
func (textRemoveApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyTextRemoveToJATS(jats, c)
}

type textRestoreApplier struct{}

func (textRestoreApplier) Kind() correction.Kind { return correction.KindTextRestore }
func (textRestoreApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyTextRestoreToJATS(jats, c)
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
func ApplyBBoxModifyToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindBBoxModify {
		return nil, fmt.Errorf("apply bbox-modify: expected bbox-modify kind, got %s", c.Kind)
	}
	if c.TargetElementID == "" {
		return nil, fmt.Errorf("apply bbox-modify: target_element_id required")
	}
	var payload correction.BBoxModifyPayload
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
	// format that correction.BBoxAddFigureJATSElement and jatsconv both emit so
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
		safeCaption := correction.XMLEscape(payload.Corrected.Caption)
		newCaption := "<caption><p>" + safeCaption + "</p></caption>"
		newBytes = replaceCaptionBlock(newBytes, newCaption)
	} else if payload.Corrected.Label != "" {
		// Caption not overridden but label is — put the label into
		// the caption placeholder text so the trailing "Figures and
		// tables" list picks up the corrected label.
		safeLabel := correction.XMLEscape(payload.Corrected.Label)
		newCaption := "<caption><p>" + safeLabel + "</p></caption>"
		newBytes = replaceCaptionBlock(newBytes, newCaption)
	}

	// Splice a fresh <table> block when the payload carries newly
	// parsed rows (populated by the handler after a table resize via
	// pdftext.ExtractTable). Replaces any existing <table> or inserts
	// one after the caption if none is present. Same
	// correction.RenderJATSTableFromRows shape used by bbox-add so the
	// downstream reader sees consistent structure regardless of how
	// the region originated.
	if len(payload.Corrected.TableRows) > 0 {
		newTable := correction.RenderJATSTableFromRows(payload.Corrected.TableRows)
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
func ApplyBBoxRemoveToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindBBoxRemove {
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
func ApplyRefMergeToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindRefMerge {
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
	comment := "<comment>" + correction.XMLEscape(fragment) + "</comment>"
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

// ApplyRefEditToJATS rewrites individual fields on the target
// <ref>'s <element-citation>, one per Corrected field pointer that
// is non-nil. Untouched fields (nil pointers) stay as GROBID had
// them — a re-import with better extraction still benefits.
//
// Each field maps to a specific JATS child of element-citation:
//
//	Author          -> person-group/name/* — rebuilt from a
//	                   comma-separated list
//	Title           -> article-title
//	ContainerTitle  -> source
//	Issued          -> year (4-digit token when the curator's value
//	                   contains one; otherwise the raw string)
//	Volume/Issue    -> volume / issue
//	Page            -> fpage/lpage (split on '-'), or fpage alone
//	Publisher       -> publisher-name
//	PublisherPlace  -> publisher-loc
//	DOI             -> pub-id[pub-id-type="doi"]
//	Citation        -> attached as a trailing <comment> when the
//	                   curator overrode the verbatim citation string
//	                   without touching structured fields
//
// Idempotent: returns the JATS unchanged when the target ref is
// absent (rare — usually means a prior ref-merge absorbed it).
func ApplyRefEditToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindRefEdit {
		return nil, fmt.Errorf("apply ref-edit: expected ref-edit kind, got %s", c.Kind)
	}
	if c.TargetElementID == "" {
		return nil, fmt.Errorf("apply ref-edit: target_element_id required")
	}
	if c.Payload == "" || c.Payload == "{}" {
		return jats, nil
	}
	var payload correction.RefEditPayload
	if err := json.Unmarshal([]byte(c.Payload), &payload); err != nil {
		return nil, fmt.Errorf("apply ref-edit: parse payload: %w", err)
	}
	openIdx, closeIdx, ok := findRefByID(jats, c.TargetElementID)
	if !ok {
		return jats, nil
	}
	refElem := jats[openIdx:closeIdx]
	updated := applyRefEditFields(refElem, &payload.Corrected)
	if bytes.Equal(updated, refElem) {
		return jats, nil
	}
	var out bytes.Buffer
	out.Grow(len(jats) + len(updated) - len(refElem))
	out.Write(jats[:openIdx])
	out.Write(updated)
	out.Write(jats[closeIdx:])
	return out.Bytes(), nil
}

// applyRefEditFields rewrites element-citation children on a single
// <ref>...</ref> blob per the non-nil pointers in `fields`.
// Kept private because the field-to-JATS mapping isn't reusable
// outside the ref-edit apply path.
func applyRefEditFields(refElem []byte, fields *correction.RefEditFields) []byte {
	out := refElem
	if fields.Author != nil {
		out = replacePersonGroup(out, *fields.Author)
	}
	if fields.Title != nil {
		out = replaceCitationChild(out, "article-title", *fields.Title)
	}
	if fields.ContainerTitle != nil {
		out = replaceCitationChild(out, "source", *fields.ContainerTitle)
	}
	if fields.Issued != nil {
		out = replaceCitationChild(out, "year", extractYear(*fields.Issued))
	}
	if fields.Volume != nil {
		out = replaceCitationChild(out, "volume", *fields.Volume)
	}
	if fields.Issue != nil {
		out = replaceCitationChild(out, "issue", *fields.Issue)
	}
	if fields.Page != nil {
		out = replacePageRange(out, *fields.Page)
	}
	if fields.Publisher != nil {
		out = replaceCitationChild(out, "publisher-name", *fields.Publisher)
	}
	if fields.PublisherPlace != nil {
		out = replaceCitationChild(out, "publisher-loc", *fields.PublisherPlace)
	}
	if fields.DOI != nil {
		out = replacePubIDDOI(out, *fields.DOI)
	}
	if fields.Citation != nil && *fields.Citation != "" {
		// Verbatim-citation override — attach as a trailing
		// <comment> so the reader can surface it alongside the
		// structured fields. Reuses the ref-merge comment splicer.
		out = appendCommentToCitation(out, "verbatim: "+*fields.Citation)
	}
	return out
}

// replaceCitationChild rewrites (or inserts) the first direct child
// of element-citation with the given local name. Empty newValue
// removes the child entirely — curator explicitly cleared the
// field, so downstream renderers shouldn't fall back to the old
// GROBID value.
func replaceCitationChild(refElem []byte, localName, newValue string) []byte {
	s := string(refElem)
	openTag := "<" + localName + ">"
	closeTag := "</" + localName + ">"
	openIdx := strings.Index(s, openTag)
	if openIdx >= 0 {
		closeIdx := strings.Index(s[openIdx:], closeTag)
		if closeIdx < 0 {
			return refElem
		}
		end := openIdx + closeIdx + len(closeTag)
		if newValue == "" {
			// Also strip any trailing whitespace/newline the
			// element used to end with, so removing a field
			// doesn't leave visible blank space in the output.
			return []byte(s[:openIdx] + strings.TrimLeft(s[end:], " \t\n\r"))
		}
		return []byte(s[:openIdx] + openTag + correction.XMLEscape(newValue) + closeTag + s[end:])
	}
	if newValue == "" {
		return refElem
	}
	// No existing child — insert before the closing element-citation
	// (or mixed-citation, or ref) tag.
	replacement := openTag + correction.XMLEscape(newValue) + closeTag
	for _, closer := range []string{"</element-citation>", "</mixed-citation>", "</ref>"} {
		idx := strings.LastIndex(s, closer)
		if idx < 0 {
			continue
		}
		return []byte(s[:idx] + replacement + s[idx:])
	}
	return refElem
}

// replacePersonGroup rewrites the <person-group person-group-type="author">
// element with names parsed from a comma-separated author string
// ("Smith J, Doe A B" → two <name> children). Splits each entry on
// the first whitespace: everything before is the surname, the
// remainder becomes given-names. Empty author string removes the
// person-group entirely.
func replacePersonGroup(refElem []byte, authorList string) []byte {
	s := string(refElem)
	// Find <person-group person-group-type="author"> ... </person-group>
	pgRe := regexp.MustCompile(`<person-group[^>]*person-group-type="author"[^>]*>[\s\S]*?</person-group>`)
	loc := pgRe.FindStringIndex(s)
	newPG := buildPersonGroup(authorList)
	if loc != nil {
		if newPG == "" {
			return []byte(s[:loc[0]] + strings.TrimLeft(s[loc[1]:], " \t\n\r"))
		}
		return []byte(s[:loc[0]] + newPG + s[loc[1]:])
	}
	if newPG == "" {
		return refElem
	}
	// No existing person-group — insert right after <element-citation ...>
	ecRe := regexp.MustCompile(`<element-citation[^>]*>|<mixed-citation[^>]*>`)
	ecLoc := ecRe.FindStringIndex(s)
	if ecLoc == nil {
		return refElem
	}
	return []byte(s[:ecLoc[1]] + newPG + s[ecLoc[1]:])
}

// buildPersonGroup constructs a <person-group person-group-type="author">
// element from a comma-separated author list. Each name splits into
// surname + given-names by taking the first whitespace-delimited
// token as the surname and the rest as given-names. Returns empty
// string when the list has no usable entries.
func buildPersonGroup(list string) string {
	list = strings.TrimSpace(list)
	if list == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<person-group person-group-type="author">`)
	any := false
	for _, raw := range strings.Split(list, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		any = true
		parts := strings.SplitN(s, " ", 2)
		surname := strings.TrimSpace(parts[0])
		given := ""
		if len(parts) == 2 {
			given = strings.TrimSpace(parts[1])
		}
		b.WriteString("<name><surname>")
		b.WriteString(correction.XMLEscape(surname))
		b.WriteString("</surname>")
		if given != "" {
			b.WriteString("<given-names>")
			b.WriteString(correction.XMLEscape(given))
			b.WriteString("</given-names>")
		}
		b.WriteString("</name>")
	}
	b.WriteString("</person-group>")
	if !any {
		return ""
	}
	return b.String()
}

// replacePageRange rewrites the <fpage>/<lpage> pair on an
// element-citation from a "start-end" (or "start") string. Handles
// three shapes: "12-34" (both), "12" (fpage only), "" (clear both).
func replacePageRange(refElem []byte, pageRange string) []byte {
	pageRange = strings.TrimSpace(pageRange)
	var fpage, lpage string
	if pageRange != "" {
		if idx := strings.IndexAny(pageRange, "-–—"); idx >= 0 {
			fpage = strings.TrimSpace(pageRange[:idx])
			lpage = strings.TrimSpace(pageRange[idx+1:])
		} else {
			fpage = pageRange
		}
	}
	out := replaceCitationChild(refElem, "fpage", fpage)
	out = replaceCitationChild(out, "lpage", lpage)
	return out
}

// replacePubIDDOI rewrites (or inserts) the DOI pub-id on an
// element-citation. Empty newDOI removes the element. Distinct
// from replaceCitationChild because <pub-id> takes an attribute.
func replacePubIDDOI(refElem []byte, newDOI string) []byte {
	s := string(refElem)
	pubRe := regexp.MustCompile(`<pub-id\b[^>]*pub-id-type="doi"[^>]*>[\s\S]*?</pub-id>`)
	loc := pubRe.FindStringIndex(s)
	newDOI = strings.TrimSpace(newDOI)
	if loc != nil {
		if newDOI == "" {
			return []byte(s[:loc[0]] + strings.TrimLeft(s[loc[1]:], " \t\n\r"))
		}
		replacement := `<pub-id pub-id-type="doi">` + correction.XMLEscape(newDOI) + `</pub-id>`
		return []byte(s[:loc[0]] + replacement + s[loc[1]:])
	}
	if newDOI == "" {
		return refElem
	}
	replacement := `<pub-id pub-id-type="doi">` + correction.XMLEscape(newDOI) + `</pub-id>`
	for _, closer := range []string{"</element-citation>", "</mixed-citation>", "</ref>"} {
		idx := strings.LastIndex(s, closer)
		if idx < 0 {
			continue
		}
		return []byte(s[:idx] + replacement + s[idx:])
	}
	return refElem
}

// extractYear pulls the first 4-digit year token from a free-form
// date string ("2020", "2020-06-15", "June 2020", "in press 2020").
// Falls back to the raw string when no 4-digit run is found so
// non-year "issued" values (e.g. "in press") still land in the JATS.
func extractYear(issued string) string {
	m := regexp.MustCompile(`\b(1[6-9]\d{2}|20\d{2}|21\d{2})\b`).FindString(issued)
	if m != "" {
		return m
	}
	return strings.TrimSpace(issued)
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
func ApplyBBoxAddToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	elem, err := correction.BBoxAddFigureJATSElement(c)
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

// ApplyTextRemoveToJATS finds a body element whose text content
// matches the payload's Text (normalized whitespace) and replaces it
// with a <hive:noise correction-id="…"> wrapper carrying the same
// text inside an XML comment. Generic renderers see nothing (comments
// are parser-erased); hive-aware tools correlate correction-id back
// to the hive__extraction_correction row.
//
// Matching is by content rather than element id because jatsconv
// doesn't emit ids on body paragraphs today, and text-content match
// is more re-import-robust anyway: GROBID's noise text (running
// headers, watermarks) is deterministic across re-runs even when
// paragraph positions shift. When PDFPage is set in the payload, the
// match is narrowed to paragraphs near a <hive:page-break page="N"/>
// marker with the matching page number.
//
// Idempotent: skips wrapping when the match is already a
// <hive:noise> element (a re-apply pass) or when no match is found
// (a re-import stripped the noise).
func ApplyTextRemoveToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindTextRemove {
		return nil, fmt.Errorf("apply text-remove: expected text-remove kind, got %s", c.Kind)
	}
	if c.Payload == "" || c.Payload == "{}" {
		return nil, fmt.Errorf("apply text-remove: payload with text required")
	}
	var payload correction.TextRemovePayload
	if err := json.Unmarshal([]byte(c.Payload), &payload); err != nil {
		return nil, fmt.Errorf("apply text-remove: parse payload: %w", err)
	}
	if strings.TrimSpace(payload.Text) == "" {
		return nil, fmt.Errorf("apply text-remove: payload.text required")
	}
	openIdx, closeIdx, ok := findParagraphByText(jats, payload.Text, payload.PDFPage)
	if !ok {
		return jats, nil
	}
	wrapper := renderNoiseWrapper(c.ID, payload.Text)
	var out bytes.Buffer
	out.Grow(len(jats) + len(wrapper) - (closeIdx - openIdx))
	out.Write(jats[:openIdx])
	out.WriteString(wrapper)
	out.Write(jats[closeIdx:])
	return out.Bytes(), nil
}

// pTagOpenRegex matches the opening <p ...> tag of a body paragraph.
// Includes any attributes but skips <p> tags that are already hive-
// namespaced (hive:page-break wrappers have content-type set) so the
// paragraph scan doesn't mistake infrastructure markup for prose.
var pTagOpenRegex = regexp.MustCompile(`<p\b([^>]*)>`)

// findParagraphByText walks every <p>…</p> in the JATS looking for
// one whose normalized text content equals wantText. When pdfPage is
// non-zero, the match is filtered to paragraphs whose immediately
// preceding <hive:page-break page="N"/> marker (or the paragraph's
// own hive:pdf-page attribute) matches pdfPage — resolves ambiguity
// when identical noise text appears on multiple pages.
//
// Returns the byte range of the matched element (opening '<' through
// closing '>') so the caller can splice in a wrapper.
func findParagraphByText(jats []byte, wantText string, pdfPage int) (openIdx, closeIdx int, ok bool) {
	wantNormalized := collapseWhitespace(strings.TrimSpace(wantText))
	if wantNormalized == "" {
		return 0, 0, false
	}
	pageBreakRe := regexp.MustCompile(`<hive:page-break\s+page="(\d+)"`)
	pos := 0
	for pos < len(jats) {
		nextP := pTagOpenRegex.FindIndex(jats[pos:])
		if nextP == nil {
			return 0, 0, false
		}
		absStart := pos + nextP[0]
		absOpenEnd := pos + nextP[1]
		closeStart := bytes.Index(jats[absOpenEnd:], []byte("</p>"))
		if closeStart < 0 {
			return 0, 0, false
		}
		absCloseEnd := absOpenEnd + closeStart + len("</p>")
		// Skip infrastructure paragraphs (hive:page-break wrappers)
		// so they don't get scanned as content candidates.
		if bytes.Contains(jats[absStart:absOpenEnd], []byte(`content-type="hive-page-break"`)) {
			pos = absCloseEnd
			continue
		}
		content := stripXMLTags(jats[absOpenEnd : absCloseEnd-len("</p>")])
		gotNormalized := collapseWhitespace(strings.TrimSpace(content))
		if gotNormalized == wantNormalized {
			if pdfPage == 0 {
				return absStart, absCloseEnd, true
			}
			// Recover the current page from the last page-break
			// marker preceding this paragraph. Cheap for typical
			// papers (dozens of markers) and avoids threading page
			// state through the outer loop where infrastructure
			// paragraph skips would otherwise miss them.
			currentPage := 0
			for _, m := range pageBreakRe.FindAllSubmatchIndex(jats[:absStart], -1) {
				if n, err := strconv.Atoi(string(jats[m[2]:m[3]])); err == nil {
					currentPage = n
				}
			}
			if currentPage == pdfPage {
				return absStart, absCloseEnd, true
			}
		}
		pos = absCloseEnd
	}
	return 0, 0, false
}

// renderNoiseWrapper produces the <hive:noise correction-id="…">
// element that replaces a text-remove target. The original text goes
// inside an XML comment so every generic renderer drops it; hive-aware
// tools inspect the wrapper by correction-id.
//
// XML comments can't contain the "--" sequence. Sanitize by inserting
// a zero-width space between adjacent hyphens — invisible to human
// inspection, reversible for any tool that wants exact bytes (though
// the authoritative source is the correction row's payload).
func renderNoiseWrapper(correctionID, originalText string) string {
	// Trim + normalize whitespace for the hint. Long headers get
	// truncated so the comment doesn't balloon; the full text is
	// always in the correction row's payload.
	hint := strings.TrimSpace(originalText)
	hint = collapseWhitespace(hint)
	const maxHint = 200
	if len(hint) > maxHint {
		hint = hint[:maxHint] + "…"
	}
	// Escape "--" per the XML spec's ban on double-hyphen inside
	// comments. Zero-width space (U+200B) between the two hyphens
	// leaves the sequence recognizable but no longer a syntax
	// collision.
	hint = strings.ReplaceAll(hint, "--", "-​-")
	// A trailing hyphen would make the closing "-->" a triple-hyphen
	// sequence; the XML spec bans "---" too when it ends a comment.
	// Guard by inserting a trailing space when needed.
	if strings.HasSuffix(hint, "-") {
		hint += " "
	}
	return fmt.Sprintf(`<hive:noise correction-id="%s"><!-- %s --></hive:noise>`,
		correction.XMLEscape(correctionID), hint)
}

// stripXMLTags removes all XML tags from a byte slice and returns the
// text content. Used as a fallback when a text-remove correction
// arrives without a payload — the applier reads the current element
// content directly so the wrapper still carries a hint.
func stripXMLTags(src []byte) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return string(re.ReplaceAll(src, []byte("")))
}

// collapseWhitespace normalizes runs of whitespace to a single space.
// Keeps the noise-comment hint readable regardless of how the source
// element wrapped its text.
func collapseWhitespace(s string) string {
	re := regexp.MustCompile(`\s+`)
	return re.ReplaceAllString(s, " ")
}

// ApplyTextRestoreToJATS reverses a text-remove correction: locates
// the <hive:noise correction-id="…"> wrapper produced by the referenced
// text-remove correction and unwraps it back to a <p> containing the
// original text (recovered from the XML comment inside the wrapper).
//
// The two-correction shape ("remove then restore") is deliberate —
// the pair is preserved as training-corpus signal that the curator
// changed their mind, which is useful for calibrating the eventual
// noise-detection model. Callers that want the removal simply gone
// from the DB should use DeleteCorrection on the text-remove row
// instead; this applier is for the in-JATS reversal.
//
// Idempotent: returns the JATS unchanged when the referenced
// hive:noise wrapper is absent (either the original text-remove
// hadn't been applied yet, or a prior restore already unwrapped it).
func ApplyTextRestoreToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindTextRestore {
		return nil, fmt.Errorf("apply text-restore: expected text-restore kind, got %s", c.Kind)
	}
	if c.Payload == "" || c.Payload == "{}" {
		return nil, fmt.Errorf("apply text-restore: payload with remove_correction_id required")
	}
	var payload correction.TextRestorePayload
	if err := json.Unmarshal([]byte(c.Payload), &payload); err != nil {
		return nil, fmt.Errorf("apply text-restore: parse payload: %w", err)
	}
	if payload.RemoveCorrectionID == "" {
		return nil, fmt.Errorf("apply text-restore: payload.remove_correction_id required")
	}
	openIdx, closeIdx, original, ok := findNoiseWrapper(jats, payload.RemoveCorrectionID)
	if !ok {
		return jats, nil
	}
	replacement := "<p>" + correction.XMLEscape(original) + "</p>"
	var out bytes.Buffer
	out.Grow(len(jats) + len(replacement) - (closeIdx - openIdx))
	out.Write(jats[:openIdx])
	out.WriteString(replacement)
	out.Write(jats[closeIdx:])
	return out.Bytes(), nil
}

// noiseWrapperRegex matches the opening tag of a hive:noise wrapper
// keyed by correction-id. Capture group 1 is the whole opening tag
// span (so callers know where the element starts).
var noiseWrapperRegex = regexp.MustCompile(
	`<hive:noise\s+correction-id="([^"]+)"\s*>`)

// findNoiseWrapper locates the <hive:noise correction-id="X"> element
// matching the given correction id, returning its byte range and the
// original text recovered from the XML comment inside. Reverses the
// -/ZWSP/- escape renderNoiseWrapper applies so the recovered text is
// the original bytes (modulo the safe-hint truncation the wrapper's
// comment carried — which is why callers targeting exact-bytes
// training-data should read the text-remove correction's payload
// rather than the wrapper).
func findNoiseWrapper(jats []byte, removeCorrectionID string) (openIdx, closeIdx int, original string, ok bool) {
	pattern := `<hive:noise\s+correction-id="` + regexp.QuoteMeta(removeCorrectionID) + `"\s*>`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, 0, "", false
	}
	loc := re.FindIndex(jats)
	if loc == nil {
		return 0, 0, "", false
	}
	closeTag := []byte("</hive:noise>")
	closeStart := bytes.Index(jats[loc[1]:], closeTag)
	if closeStart < 0 {
		return 0, 0, "", false
	}
	inner := jats[loc[1] : loc[1]+closeStart]
	// The wrapper's child is a single XML comment; strip the
	// "<!--" and "-->" delimiters and the sanitize-escape ZWSP so
	// the recovered text reads naturally.
	commentText := ""
	if commentOpen := bytes.Index(inner, []byte("<!--")); commentOpen >= 0 {
		commentClose := bytes.Index(inner[commentOpen:], []byte("-->"))
		if commentClose >= 0 {
			raw := inner[commentOpen+4 : commentOpen+commentClose]
			commentText = strings.TrimSpace(string(raw))
			commentText = strings.ReplaceAll(commentText, "-​-", "--")
		}
	}
	return loc[0], loc[1] + closeStart + len(closeTag), commentText, true
}
