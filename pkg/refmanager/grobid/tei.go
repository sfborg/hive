package grobid

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sfborg/hive/pkg/refmanager/doi"
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
	// Abstract paragraphs from <abstract><div><p>... GROBID extracts
	// the paper's abstract into the header when it can identify one.
	// Multiple paragraphs preserved as a slice; downstream can join
	// on newline or emit each as a separate <p> element. Multilingual
	// papers with more than one abstract typically get only their
	// primary-language abstract extracted here — GROBID's abstract
	// classifier is language-single. Non-primary abstracts (a Spanish
	// resumen in an English paper, etc.) may appear in the body
	// under a <div> with a heading like "Resumen".
	AbstractParagraphs []string
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
	// Abstract accumulation: <abstract><div><p>...</p></div></abstract>
	// under <teiHeader>. Each <p> we hit inside the abstract subtree
	// contributes one paragraph to the output slice.
	inAbstract := false

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
			case "abstract":
				// GROBID emits the paper's abstract inside
				// <teiHeader><profileDesc><abstract>. Guard on stack
				// so a stray <abstract> inside body content (rare but
				// possible in some producers) doesn't leak in.
				if stackContains(stack, "teiHeader") {
					inAbstract = true
				}
			case "p":
				// Abstract paragraphs: collect the text content of
				// each <p> inside <abstract>. Uses readElementContent
				// so mixed content (nested spans, formatting) still
				// contributes text. Skips empty paragraphs.
				if inAbstract {
					if text, ok := readElementContent(dec, "p"); ok {
						trimmed := collapseSpaces(text)
						if trimmed != "" {
							out.AbstractParagraphs = append(out.AbstractParagraphs, trimmed)
						}
						// readElementContent consumed the </p>, pop stack.
						if len(stack) > 0 && stack[len(stack)-1] == "p" {
							stack = stack[:len(stack)-1]
						}
					}
				}
			case "biblStruct":
				// Any <biblStruct> reached from inside <teiHeader> is
				// the paper's own metadata by TEI convention (there's
				// only one per header). GROBID sometimes emits
				// <biblStruct type="header"> and sometimes just
				// <biblStruct>, so relying on the attribute misses
				// many real papers; the structural check via the
				// stack works either way. Reference biblStructs live
				// under <text>/<back>/<listBibl>, never inside
				// <teiHeader>.
				if stackContains(stack, "teiHeader") {
					inHeaderBiblStruct = true
				}
			case "author":
				if inHeaderBiblStruct {
					currentAuthor = &person{}
				}
			case "forename":
				// TEI persName can contain multiple <forename>
				// elements (first + middle + rarely more). Keep
				// each one that has at least one letter and join
				// them with spaces in document order — GROBID
				// emits type="first" before type="middle" reliably,
				// so document order matches natural byline order.
				//
				// The letter filter catches the pathological case
				// where GROBID misfiles an author-separator
				// artifact into the middle-name slot as pure
				// punctuation ("Douglass ; Miller"): the ";"
				// forename has no letters and gets dropped, so
				// output collapses to "Douglass Miller" while a
				// real middle initial ("John A Smith") survives.
				if currentAuthor != nil {
					if text, ok := readCharData(dec); ok {
						trimmed := strings.TrimSpace(text)
						if hasLetter(trimmed) {
							if currentAuthor.Forename == "" {
								currentAuthor.Forename = trimmed
							} else {
								currentAuthor.Forename += " " + trimmed
							}
						}
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
			case "abstract":
				inAbstract = false
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
// (`.`, `,`, `;`, `)`, `]`, `>`) is stripped by doi.Normalize because
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
				return doi.Normalize(candidate)
			}
		}
	}
	// Free-text fallback inside the header slice only.
	if m := doiRegex.Find(header); m != nil {
		return doi.Normalize(string(m))
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

// bytesIndex wraps strings.Index for the []byte-first idiom.
func bytesIndex(haystack, needle []byte) int {
	return strings.Index(string(haystack), string(needle))
}

// readElementContent consumes tokens until it hits the matching
// end tag for `name` and returns the concatenated text of every
// CharData token encountered, including text inside nested
// descendants. Nested markup (e.g. <hi>, <ref>) contributes its
// text but its wrapper tags are dropped — appropriate for abstract
// paragraphs where the plain text is the payload and inline
// formatting isn't preserved. Returns ("", false) at EOF or when
// the next token isn't a start of usable content.
func readElementContent(dec *xml.Decoder, name string) (string, bool) {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			if b.Len() == 0 {
				return "", false
			}
			return b.String(), true
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
		case xml.CharData:
			b.Write(t)
		}
	}
	return b.String(), true
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

// hasLetter reports whether s contains at least one unicode letter.
// Used to filter forename tokens that GROBID misfiled as pure
// punctuation ("; ", "-") into the middle-name slot.
func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// stackContains reports whether the tag stack has an ancestor with
// the given local name. Used to detect header context structurally
// (any <biblStruct> reached from inside <teiHeader> is the paper's
// own metadata) instead of relying on an attribute GROBID doesn't
// always emit.
func stackContains(stack []string, name string) bool {
	for _, s := range stack {
		if s == name {
			return true
		}
	}
	return false
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
