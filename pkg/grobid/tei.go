package grobid

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
)

// BibHeader captures the bibliographic metadata GROBID extracts from
// a paper's front matter (title / authors / journal / volume / etc.).
// Populated by ParseHeader from the TEI XML GROBID returns.
//
// Kept as a plain struct so callers (currently the upload handler)
// can map it onto whatever wire type they want without dragging in
// a coldp.Reference dependency. Fields are optional — GROBID's
// coverage varies by paper; empty fields elide on the wire via
// omitempty.
type BibHeader struct {
	// Title of the paper — GROBID reads it from the <analytic><title>
	// element in <biblStruct type="header">.
	Title string
	// Authors formatted as "Given Surname; Given Surname; ..." — the
	// semicolon-separated convention hive already uses for coldp
	// author fields. Order preserved from the TEI (which preserves
	// the paper's byline order).
	Authors string
	// DOI extracted from <idno type="DOI"> in the header. Same
	// field as the upload response's DOI; parsing it here as well
	// keeps ParseHeader self-contained.
	DOI string
	// ContainerTitle — journal or book title from <monogr><title>.
	ContainerTitle string
	// Issued (year of publication) from <date type="published"
	// when="YYYY">. Only the year is kept; downstream code paths
	// don't need month/day.
	Issued string
	// Volume / Issue / Page from <biblScope unit="volume|issue|page">.
	Volume string
	Issue  string
	Page   string
	// Publisher from <imprint><publisher>. Rarely populated for
	// journal articles; more common for books.
	Publisher string
}

// ParseHeader extracts bibliographic metadata from GROBID's TEI XML.
// Returns a populated *BibHeader on success, or an error when the
// XML is malformed. Returns a non-nil BibHeader even when no fields
// resolve — callers check individual fields for emptiness.
//
// Namespace handling: GROBID emits TEI with the default namespace
// `http://www.tei-c.org/ns/1.0`. We match on local names via the
// token-loop pattern (ignoring the namespace) so this stays robust
// against GROBID version bumps that might tweak the namespace URI.
func ParseHeader(tei []byte) (*BibHeader, error) {
	out := &BibHeader{}
	dec := xml.NewDecoder(strings.NewReader(string(tei)))

	// Track the path we're inside so field extraction knows whether
	// a <title> belongs to <analytic> (paper title) vs <monogr>
	// (journal title). Local-name stack — namespace ignored.
	stack := []string{}
	// Inside <biblStruct type="header"> is the header we care about.
	// GROBID also emits biblStructs for references cited by the paper;
	// we skip those.
	inHeaderBiblStruct := false
	// Authors accumulate as we walk. Formatted at the end.
	type person struct{ Forename, Surname string }
	var authors []person
	var currentAuthor *person

	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("grobid: parse TEI header: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			stack = append(stack, local)

			switch local {
			case "biblStruct":
				for _, a := range t.Attr {
					if a.Name.Local == "type" && a.Value == "header" {
						inHeaderBiblStruct = true
					}
				}
			case "author":
				if inHeaderBiblStruct {
					currentAuthor = &person{}
				}
			case "forename":
				if currentAuthor != nil {
					if text, ok := readCharData(dec); ok {
						currentAuthor.Forename = strings.TrimSpace(text)
					}
				}
			case "surname":
				if currentAuthor != nil {
					if text, ok := readCharData(dec); ok {
						currentAuthor.Surname = strings.TrimSpace(text)
					}
				}
			case "title":
				if !inHeaderBiblStruct {
					break
				}
				// <analytic><title> → paper title.
				// <monogr><title level="j"> → journal title.
				// n=1 (immediate parent) because stack top is
				// already the "title" element we just pushed.
				parent := parentOf(stack, 1)
				if parent == "analytic" {
					if text, ok := readCharData(dec); ok {
						out.Title = collapseSpaces(text)
					}
				} else if parent == "monogr" {
					if text, ok := readCharData(dec); ok {
						out.ContainerTitle = collapseSpaces(text)
					}
				}
			case "idno":
				if !inHeaderBiblStruct {
					break
				}
				var idnoType string
				for _, a := range t.Attr {
					if a.Name.Local == "type" {
						idnoType = a.Value
					}
				}
				if strings.EqualFold(idnoType, "DOI") {
					if text, ok := readCharData(dec); ok {
						out.DOI = strings.TrimSpace(text)
					}
				}
			case "date":
				if !inHeaderBiblStruct {
					break
				}
				var dateType, when string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "type":
						dateType = a.Value
					case "when":
						when = a.Value
					}
				}
				if dateType == "published" && when != "" {
					// "when" can be YYYY, YYYY-MM, or YYYY-MM-DD.
					// Keep only the year for coldp.Reference.issued.
					if i := strings.Index(when, "-"); i > 0 {
						out.Issued = when[:i]
					} else {
						out.Issued = when
					}
				}
			case "biblScope":
				if !inHeaderBiblStruct {
					break
				}
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
				switch unit {
				case "volume":
					if text, ok := readCharData(dec); ok {
						out.Volume = strings.TrimSpace(text)
					}
				case "issue":
					if text, ok := readCharData(dec); ok {
						out.Issue = strings.TrimSpace(text)
					}
				case "page":
					// Prefer from-to attributes; fall back to
					// inline text for older GROBID output.
					if from != "" && to != "" {
						out.Page = from + "-" + to
					} else if from != "" {
						out.Page = from
					} else if text, ok := readCharData(dec); ok {
						out.Page = strings.TrimSpace(text)
					}
				}
			case "publisher":
				if !inHeaderBiblStruct {
					break
				}
				if text, ok := readCharData(dec); ok {
					out.Publisher = collapseSpaces(text)
				}
			}
		case xml.EndElement:
			local := t.Name.Local
			if len(stack) > 0 && stack[len(stack)-1] == local {
				stack = stack[:len(stack)-1]
			}
			switch local {
			case "biblStruct":
				inHeaderBiblStruct = false
			case "author":
				if currentAuthor != nil {
					// Only keep authors with at least a surname —
					// GROBID sometimes emits empty author records.
					if currentAuthor.Surname != "" {
						authors = append(authors, *currentAuthor)
					}
					currentAuthor = nil
				}
			}
		}
	}

	if len(authors) > 0 {
		parts := make([]string, 0, len(authors))
		for _, a := range authors {
			if a.Forename != "" {
				parts = append(parts, a.Forename+" "+a.Surname)
			} else {
				parts = append(parts, a.Surname)
			}
		}
		out.Authors = strings.Join(parts, "; ")
	}
	return out, nil
}

// doiRegex matches the DOI syntax per the DOI Handbook:
// `10.<registrant-code>/<suffix>` where the registrant is 4-9 digits
// and the suffix is any run of non-whitespace. Trailing punctuation
// (`.`, `,`, `;`, `)`, `]`, `>`) is stripped by trimDOIPunct because
// the regex captures greedily.
var doiRegex = regexp.MustCompile(`10\.\d{4,9}/[^\s<>"']+`)

// ExtractDOI pulls the paper's own DOI from GROBID's TEI, restricted
// to the <teiHeader> section so cited-reference DOIs (which live
// under <text>/<back>/<div type="references">) can't be picked up
// by mistake. Returns empty when no DOI resolves in the header —
// callers should prefer "no DOI" over "wrong DOI", since a wrong
// DOI triggers an OpenAlex fetch of an unrelated paper.
//
// Strategy inside the header slice:
//  1. Canonical location: `<idno type="DOI">10.xxx/yyy</idno>`.
//  2. Fallback: plain-text regex over the header slice — covers
//     cases where GROBID emitted the DOI as free text (front-matter
//     footer, header note) without a typed idno wrapper.
func ExtractDOI(tei []byte) string {
	// Bound the scan to the header. If the document has no
	// closing </teiHeader> for any reason, scan up to <text>
	// (references live inside <text>/<back>). If neither anchor
	// is present, treat the whole document as suspect and return
	// empty — safer than an unbounded scan that could hit a
	// reference DOI.
	header := teiHeaderSlice(tei)
	if len(header) == 0 {
		return ""
	}
	// Canonical idno first.
	needle := []byte(`<idno type="DOI">`)
	if i := bytesIndex(header, needle); i >= 0 {
		rest := header[i+len(needle):]
		if end := bytesIndex(rest, []byte("</idno>")); end > 0 {
			candidate := strings.TrimSpace(string(rest[:end]))
			if doiRegex.MatchString(candidate) {
				return trimDOIPunct(candidate)
			}
		}
	}
	// Free-text fallback inside the header slice only.
	if m := doiRegex.Find(header); m != nil {
		return trimDOIPunct(string(m))
	}
	return ""
}

// teiHeaderSlice returns the bytes between <teiHeader> and </teiHeader>
// (inclusive of open, exclusive of close), or an empty slice when the
// anchors aren't present. Empty triggers ExtractDOI's safe-refusal
// path — better than falling through to an unbounded scan.
func teiHeaderSlice(tei []byte) []byte {
	open := bytesIndex(tei, []byte(`<teiHeader`))
	if open < 0 {
		return nil
	}
	close := bytesIndex(tei[open:], []byte(`</teiHeader>`))
	if close < 0 {
		return nil
	}
	return tei[open : open+close]
}

// trimDOIPunct strips trailing punctuation the DOI regex captured
// greedily but that isn't part of the identifier. Common when a DOI
// appears at end of sentence or inside parentheses in reference text.
func trimDOIPunct(s string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == '.' || last == ',' || last == ';' ||
			last == ')' || last == ']' || last == '>' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// bytesIndex wraps strings.Index for the []byte-first idiom.
func bytesIndex(haystack, needle []byte) int {
	return strings.Index(string(haystack), string(needle))
}

// readCharData consumes the next CharData token from the decoder
// and returns it. Skips whitespace-only tokens (elements with
// mixed content shouldn't hit this — the fields we care about are
// leaf text-content). Returns ("", false) when the next token
// isn't CharData (element has no text, or nested elements).
func readCharData(dec *xml.Decoder) (string, bool) {
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

// parentOf returns the local name n levels above the current top
// of the stack. `n=1` is the immediate parent, `n=2` grandparent.
// Returns "" when the stack isn't deep enough.
func parentOf(stack []string, n int) string {
	if len(stack) < n+1 {
		return ""
	}
	return stack[len(stack)-1-n]
}

// collapseSpaces trims outer whitespace and replaces internal runs
// of whitespace (including newlines) with a single space. TEI text
// often has line breaks inside <title> content; readers expect one
// visual line.
func collapseSpaces(s string) string {
	s = strings.TrimSpace(s)
	// Fast path — no whitespace worth collapsing.
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	return b.String()
}
