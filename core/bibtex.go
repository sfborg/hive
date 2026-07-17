package core

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/sfborg/sflib/pkg/coldp"
)

// BibTeX → coldp.Reference conversion for the add-reference modal's
// BibTeX tab (see task #75). Deliberately small: single-entry parse
// with the common fields and entry types; no @string macros, no
// preamble handling, no multi-entry-file processing (paste one
// citation at a time).
//
// PROTOTYPE — LIFT-TO-SFLIB CANDIDATE. Same rationale as the OpenAlex
// and BHLnames clients — every SFBorg tool that ingests references
// benefits from this. Written self-contained so migration is easy.

// ErrBibTeXParse wraps parser failures. Callers can errors.Is-check it
// to render "malformed BibTeX" without leaking internal messages.
var ErrBibTeXParse = errors.New("bibtex: parse error")

// ParseBibTeX converts a BibTeX entry string into a coldp.Reference.
// If the input contains multiple entries the first one wins — the
// modal's paste-in-one-at-a-time convention.
func ParseBibTeX(input string) (*coldp.Reference, error) {
	e, err := parseFirstEntry(input)
	if err != nil {
		return nil, err
	}
	return entryToReference(e), nil
}

// bibtexEntry is the intermediate shape between tokens and coldp.Reference.
// Fields is keyed by lowercased field name for lookup convenience.
type bibtexEntry struct {
	Type   string // "article", "book", ... (lowercased)
	Key    string // the citation key (e.g. "smith2020"); unused by hive but preserved for round-trip debugging
	Fields map[string]string
}

// parseFirstEntry scans the input for the first @<type>{key, fields}
// block. Handles nested braces + escaped quotes so field values with
// braces (common in BibTeX for verbatim text) survive intact.
func parseFirstEntry(input string) (*bibtexEntry, error) {
	// Locate the first '@'.
	i := strings.IndexByte(input, '@')
	if i < 0 {
		return nil, fmt.Errorf("%w: no entry marker '@' found", ErrBibTeXParse)
	}
	s := input[i+1:]

	// Entry type — letters until '{' or whitespace.
	typeEnd := 0
	for typeEnd < len(s) && (unicode.IsLetter(rune(s[typeEnd])) || s[typeEnd] == '_') {
		typeEnd++
	}
	if typeEnd == 0 {
		return nil, fmt.Errorf("%w: missing entry type after @", ErrBibTeXParse)
	}
	entryType := strings.ToLower(s[:typeEnd])
	s = s[typeEnd:]

	// Skip whitespace, expect '{'.
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if len(s) == 0 || s[0] != '{' {
		return nil, fmt.Errorf("%w: expected '{' after entry type", ErrBibTeXParse)
	}
	s = s[1:]

	// Locate the matching '}' at brace depth 0, respecting nested
	// braces inside field values.
	inner, err := extractBraceContent(s)
	if err != nil {
		return nil, err
	}

	// Split the inner block: first token before the first comma is the
	// citation key; everything after is field=value pairs.
	key, fieldsBlock := splitFirstComma(inner)
	entry := &bibtexEntry{
		Type:   entryType,
		Key:    strings.TrimSpace(key),
		Fields: map[string]string{},
	}
	// Parse field = value pairs.
	rest := fieldsBlock
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest == "" {
			break
		}
		name, val, remain, ok, err := parseField(rest)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		entry.Fields[strings.ToLower(strings.TrimSpace(name))] = val
		rest = remain
	}
	return entry, nil
}

// extractBraceContent takes a string starting immediately after an
// opening '{' and returns the substring up to (but not including)
// the matching '}'. Handles nested braces.
func extractBraceContent(s string) (string, error) {
	depth := 1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i], nil
			}
		case '\\':
			// Skip escaped character (\{, \}, \\).
			if i+1 < len(s) {
				i++
			}
		}
	}
	return "", fmt.Errorf("%w: unbalanced braces (missing '}')", ErrBibTeXParse)
}

// splitFirstComma splits the entry-body block on the first comma at
// brace depth 0. Left half = citation key; right half = field pairs.
// A body with no fields (no comma) returns (whole, "").
func splitFirstComma(s string) (string, string) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				return s[:i], s[i+1:]
			}
		}
	}
	return s, ""
}

// parseField pulls the next `name = value` pair from s. Returns the
// field name, the value (with braces / quotes stripped), the
// remainder of s (after the trailing comma, if any), a bool for
// whether a field was actually found (false when s is only whitespace
// / a bare comma), and any parse error. Content without an '=' is
// treated as malformed rather than silently dropped so curators
// notice the problem.
func parseField(s string) (name, value, remain string, ok bool, err error) {
	// Field name: letters / digits until '='.
	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		// Content but no '=' → malformed. An empty / whitespace-only
		// s already exited before reaching here (caller trimmed).
		return "", "", "", false, fmt.Errorf("%w: field without '=' in %q",
			ErrBibTeXParse, strings.TrimSpace(s))
	}
	name = s[:eq]
	rest := strings.TrimLeftFunc(s[eq+1:], unicode.IsSpace)
	if rest == "" {
		return "", "", "", false, fmt.Errorf("%w: missing value for field %q", ErrBibTeXParse, strings.TrimSpace(name))
	}
	switch rest[0] {
	case '{':
		inner, err := extractBraceContent(rest[1:])
		if err != nil {
			return "", "", "", false, err
		}
		value = inner
		rest = rest[len(inner)+2:]
	case '"':
		// Double-quoted value. May contain escaped quotes \" but rarely does.
		end := -1
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\\' && i+1 < len(rest) {
				i++
				continue
			}
			if rest[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return "", "", "", false, fmt.Errorf("%w: unclosed quoted value for %q", ErrBibTeXParse, strings.TrimSpace(name))
		}
		value = rest[1:end]
		rest = rest[end+1:]
	default:
		// Bare value (number or @string macro name) — read until
		// comma or whitespace.
		end := 0
		for end < len(rest) && rest[end] != ',' && !unicode.IsSpace(rune(rest[end])) {
			end++
		}
		value = rest[:end]
		rest = rest[end:]
	}
	// Consume trailing whitespace + optional comma.
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if len(rest) > 0 && rest[0] == ',' {
		rest = rest[1:]
	}
	return name, cleanValue(value), rest, true, nil
}

// cleanValue collapses BibTeX-value quirks:
//   - collapse internal whitespace runs → single space
//   - strip ALL unescaped braces — outer wrappers ({{X}} → X) plus
//     inner capitalization-preservation markers (title = {A {Foo} bar}
//     → "A Foo bar"). Hive doesn't apply case transforms so the
//     braces are noise for display purposes.
//   - apply a small TeX escape table for the escapes most often seen
//     in real .bib files.
//
// Not a full LaTeX-to-Unicode converter — good enough for pasting a
// citation into a preview form. Curators can polish the value in the
// modal before saving.
func cleanValue(s string) string {
	// Collapse whitespace.
	s = strings.Join(strings.Fields(s), " ")
	// Handle a small TeX escape table BEFORE stripping braces so
	// \{ / \} survive as literal braces (rare but happens in URLs).
	// Do the -- / --- replacements first so page ranges look right.
	replacer := strings.NewReplacer(
		"---", "—",
		"--", "–",
		`\&`, "&",
		`\_`, "_",
		`\%`, "%",
		`\$`, "$",
		`\#`, "#",
		`\{`, "\x00LB\x00", // temporary token; restored below
		`\}`, "\x00RB\x00",
		"~", " ",
	)
	s = replacer.Replace(s)
	// Strip unescaped braces. BibTeX uses them both for value
	// delimiting and for capitalization preservation; hive doesn't
	// need either at display time.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '}' {
			continue
		}
		b.WriteByte(s[i])
	}
	// Restore any escaped braces the curator meant literally.
	out := b.String()
	out = strings.ReplaceAll(out, "\x00LB\x00", "{")
	out = strings.ReplaceAll(out, "\x00RB\x00", "}")
	return out
}

// bibTypeToRefType maps common BibTeX entry types to sfga
// reference_type enum values. Unknown types return "" so hive doesn't
// invent a type the curator didn't intend.
func bibTypeToRefType(bibType string) string {
	switch bibType {
	case "article":
		return "ARTICLE_JOURNAL"
	case "book", "booklet", "manual":
		return "BOOK"
	case "inbook", "incollection":
		return "CHAPTER"
	case "inproceedings", "conference":
		return "PAPER_CONFERENCE"
	case "proceedings":
		return "BOOK"
	case "mastersthesis", "phdthesis":
		return "THESIS"
	case "techreport":
		return "REPORT"
	case "unpublished":
		return "MANUSCRIPT"
	case "online":
		return "WEBPAGE"
	case "dataset":
		return "DATASET"
	case "software":
		return "REPORT" // no distinct software type in sfga
	case "misc":
		return "" // ambiguous — let curator pick
	}
	return ""
}

// entryToReference projects the parsed BibTeX entry into a
// coldp.Reference. Fields hive doesn't have a target for (abstract,
// keywords, mendeley-groups, etc.) are silently dropped — modal
// preview shows what landed, curator confirms.
func entryToReference(e *bibtexEntry) *coldp.Reference {
	f := e.Fields
	r := &coldp.Reference{
		Type:                coldp.NewReferenceType(bibTypeToRefType(e.Type)),
		Title:               f["title"],
		Author:              bibtexAuthorToColdp(f["author"]),
		Editor:              bibtexAuthorToColdp(f["editor"]),
		Volume:              f["volume"],
		Publisher:           f["publisher"],
		PublisherPlace:      f["address"],
		DOI:                 strings.TrimPrefix(strings.TrimPrefix(f["doi"], "https://doi.org/"), "http://doi.org/"),
		ISBN:                f["isbn"],
		ISSN:                f["issn"],
		Link:                f["url"],
		Remarks:             f["note"],
	}
	// Container: journal for @article, booktitle for @in* / proceedings.
	if v, ok := f["journal"]; ok && v != "" {
		r.ContainerTitle = v
	} else if v, ok := f["booktitle"]; ok && v != "" {
		r.ContainerTitle = v
	}
	// Issue: BibTeX "number" is the same concept.
	if v, ok := f["number"]; ok && v != "" {
		r.Issue = v
	} else if v, ok := f["issue"]; ok && v != "" {
		r.Issue = v
	}
	// Pages: BibTeX often writes "1--10"; cleanValue already turned
	// that into "1–10" via the -- → – replacement.
	r.Page = f["pages"]
	// Issued: prefer month + year → YYYY-MM if both are present, else
	// bare year. Month is often a three-letter code (jan, feb, …).
	year := f["year"]
	month := monthNumber(f["month"])
	switch {
	case year != "" && month != "":
		r.Issued = year + "-" + month
	case year != "":
		r.Issued = year
	}
	return r
}

// bibtexAuthorToColdp converts BibTeX's " and " separator into
// coldp's "; " convention. Preserves the "Last, First" ordering
// BibTeX already uses; a "First Last" author gets written through
// unchanged (hive doesn't try to guess).
func bibtexAuthorToColdp(s string) string {
	if s == "" {
		return ""
	}
	// Split on " and " (case-insensitive) at depth 0. Braces protect
	// names like "{van der Waals, J.D.}".
	var (
		parts []string
		start = 0
		depth = 0
	)
	lower := strings.ToLower(s)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth == 0 && i+5 <= len(s) && lower[i:i+5] == " and " {
			parts = append(parts, strings.TrimSpace(cleanValue(s[start:i])))
			start = i + 5
			i += 4
		}
	}
	parts = append(parts, strings.TrimSpace(cleanValue(s[start:])))
	return strings.Join(parts, "; ")
}

// monthNumber turns BibTeX's month token (three-letter code, full
// name, or two-digit number) into a "MM" string. Returns "" for
// unrecognized input so the caller falls back to year-only.
func monthNumber(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	table := map[string]string{
		"1": "01", "01": "01", "jan": "01", "january": "01",
		"2": "02", "02": "02", "feb": "02", "february": "02",
		"3": "03", "03": "03", "mar": "03", "march": "03",
		"4": "04", "04": "04", "apr": "04", "april": "04",
		"5": "05", "05": "05", "may": "05",
		"6": "06", "06": "06", "jun": "06", "june": "06",
		"7": "07", "07": "07", "jul": "07", "july": "07",
		"8": "08", "08": "08", "aug": "08", "august": "08",
		"9": "09", "09": "09", "sep": "09", "september": "09",
		"10": "10", "oct": "10", "october": "10",
		"11": "11", "nov": "11", "november": "11",
		"12": "12", "dec": "12", "december": "12",
	}
	return table[s]
}
