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
}

// ApplyBBoxModifyToJATS patches the element identified by
// c.TargetElementID with the curator's changes from the
// bbox-modify payload. Handles three edit axes:
//
//   - Content-type swap (figure ↔ table): replaces the element
//     name from <fig> to <table-wrap> or vice versa. All
//     attributes and children stay put.
//   - Label / caption text: replaces the inner text of the
//     element's <caption><p>...</p></caption> block. Curator's
//     new caption text goes in verbatim.
//
// If BBox coordinate changes are stored in the payload, they
// should be reflected on the element's hive:pdf-bbox attribute.
// Slice 1e doesn't support coord editing yet (no resize handles
// in the viewer) so this path is a no-op on the bbox coord front.
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
