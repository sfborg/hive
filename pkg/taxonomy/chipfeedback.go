package taxonomy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/hiveext"
)

// Appliers returns the taxonomy-owned CorrectionApplier set. The pkg
// root unions this with the refmanager set when it builds the kind →
// applier map. Chip feedback lives here rather than in refmanager
// because <tp:taxon-name> chips come from the GNfinder + GNverifier
// pass, which is taxonomy-specific; refmanager's kinds stay
// field-agnostic.
func Appliers() []hiveext.CorrectionApplier {
	return []hiveext.CorrectionApplier{
		chipFeedbackApplier{},
	}
}

type chipFeedbackApplier struct{}

func (chipFeedbackApplier) Kind() correction.Kind { return correction.KindChipFeedback }
func (chipFeedbackApplier) Apply(jats []byte, c *correction.Correction) ([]byte, error) {
	return ApplyChipFeedbackToJATS(jats, c)
}

// taxonNameOpenRegex matches a <tp:taxon-name …> opening tag. Safe as
// a byte scan because the annotator escapes '>' inside attribute
// values (see annotator.escapeAttrValue).
var taxonNameOpenRegex = regexp.MustCompile(`<tp:taxon-name\b[^>]*>`)

// attrRegexFor builds a matcher for one attribute of an opening tag.
func attrRegexFor(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?:\s)` + regexp.QuoteMeta(name) + `="[^"]*"`)
}

var (
	gnSourceAttrRegex = attrRegexFor("gn:source")
	fullNameAttrRegex = attrRegexFor("full-name")
)

// ApplyChipFeedbackToJATS applies a curator's verdict on one
// <tp:taxon-name> chip:
//
//   - "reject"     — unwraps the chip, leaving its text in place, so
//     downstream steps stop treating the span as a name.
//   - "reclassify" — rewrites the chip's gn:source (e.g. verified →
//     ambiguous).
//   - "edit"       — rewrites the chip's full-name attribute.
//
// The chip is located by text plus occurrence index rather than by an
// element id, because re-annotation regenerates the markup; see
// correction.ChipFeedbackPayload.
//
// Idempotent in the same sense as the other appliers: when no chip
// matches (a re-annotation dropped it, or a previous apply already
// rejected it) the JATS comes back unchanged rather than erroring, so
// a stale correction doesn't fail a re-apply pass.
func ApplyChipFeedbackToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindChipFeedback {
		return nil, fmt.Errorf("apply chip-feedback: expected chip-feedback kind, got %s", c.Kind)
	}
	if c.Payload == "" || c.Payload == "{}" {
		return nil, fmt.Errorf("apply chip-feedback: payload required")
	}
	var payload correction.ChipFeedbackPayload
	if err := json.Unmarshal([]byte(c.Payload), &payload); err != nil {
		return nil, fmt.Errorf("apply chip-feedback: parse payload: %w", err)
	}
	if strings.TrimSpace(payload.Text) == "" {
		return nil, fmt.Errorf("apply chip-feedback: payload.text required")
	}
	occurrence := payload.Occurrence
	if occurrence == 0 {
		occurrence = 1
	}
	if occurrence < 0 {
		return nil, fmt.Errorf("apply chip-feedback: payload.occurrence must be positive, got %d", occurrence)
	}

	switch payload.Action {
	case correction.ChipActionReject:
	case correction.ChipActionReclassify:
		if payload.CorrectedSource == "" {
			return nil, fmt.Errorf("apply chip-feedback: reclassify needs corrected_source")
		}
	case correction.ChipActionEdit:
		if payload.CorrectedFullName == "" {
			return nil, fmt.Errorf("apply chip-feedback: edit needs corrected_full_name")
		}
	default:
		return nil, fmt.Errorf("apply chip-feedback: unknown action %q", payload.Action)
	}

	chip, ok := findChipByText(jats, payload.Text, occurrence)
	if !ok {
		return jats, nil
	}

	var replacement []byte
	switch payload.Action {
	case correction.ChipActionReject:
		replacement = jats[chip.innerStart:chip.innerEnd]
	case correction.ChipActionReclassify:
		openTag := setTagAttr(jats[chip.start:chip.innerStart], gnSourceAttrRegex, "gn:source", payload.CorrectedSource)
		replacement = append(openTag, jats[chip.innerStart:chip.end]...)
	case correction.ChipActionEdit:
		openTag := setTagAttr(jats[chip.start:chip.innerStart], fullNameAttrRegex, "full-name", payload.CorrectedFullName)
		replacement = append(openTag, jats[chip.innerStart:chip.end]...)
	}

	var out bytes.Buffer
	out.Grow(len(jats) + len(replacement) - (chip.end - chip.start))
	out.Write(jats[:chip.start])
	out.Write(replacement)
	out.Write(jats[chip.end:])
	return out.Bytes(), nil
}

// chipSpan is one <tp:taxon-name> element's byte ranges: start..end
// covers the whole element, innerStart..innerEnd its text content.
type chipSpan struct {
	start, innerStart, innerEnd, end int
}

// findChipByText returns the nth (1-based) <tp:taxon-name> chip whose
// text content matches wantText, comparing on collapsed whitespace so
// a re-wrapped line doesn't defeat the match.
func findChipByText(jats []byte, wantText string, occurrence int) (chipSpan, bool) {
	want := collapseChipWhitespace(wantText)
	if want == "" {
		return chipSpan{}, false
	}
	const closeTag = "</tp:taxon-name>"
	seen := 0
	pos := 0
	for pos < len(jats) {
		loc := taxonNameOpenRegex.FindIndex(jats[pos:])
		if loc == nil {
			return chipSpan{}, false
		}
		start := pos + loc[0]
		innerStart := pos + loc[1]
		closeOffset := bytes.Index(jats[innerStart:], []byte(closeTag))
		if closeOffset < 0 {
			return chipSpan{}, false
		}
		innerEnd := innerStart + closeOffset
		end := innerEnd + len(closeTag)
		if collapseChipWhitespace(string(jats[innerStart:innerEnd])) == want {
			seen++
			if seen == occurrence {
				return chipSpan{start: start, innerStart: innerStart, innerEnd: innerEnd, end: end}, true
			}
		}
		pos = end
	}
	return chipSpan{}, false
}

// setTagAttr rewrites one attribute on an opening tag, appending it
// when the tag doesn't carry it yet (a chip with no gn:source, for
// instance, which the annotator leaves unstamped when GNverifier
// returned nothing).
func setTagAttr(openTag []byte, attrRe *regexp.Regexp, name, value string) []byte {
	attr := fmt.Sprintf(` %s="%s"`, name, escapeChipAttr(value))
	if attrRe.Match(openTag) {
		return attrRe.ReplaceAll(openTag, []byte(attr))
	}
	insertAt := bytes.LastIndexByte(openTag, '>')
	if insertAt < 0 {
		return openTag
	}
	// Trim any whitespace before '>' so the appended attribute is
	// separated by exactly one space.
	head := bytes.TrimRight(openTag[:insertAt], " \t\r\n")
	out := make([]byte, 0, len(openTag)+len(attr))
	out = append(out, head...)
	out = append(out, attr...)
	out = append(out, openTag[insertAt:]...)
	return out
}

// escapeChipAttr escapes an attribute value the same way the
// annotator does when it writes chips.
func escapeChipAttr(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(s)
}

// collapseChipWhitespace trims and collapses runs of whitespace so
// chip text compares equal across re-wrapped JATS.
func collapseChipWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
