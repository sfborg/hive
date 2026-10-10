// Package doi normalizes Digital Object Identifiers as they arrive from
// PDFs, BibTeX, publisher XML, lookup services and curator input.
package doi

import (
	"net/url"
	"regexp"
	"strings"
)

// prefixes are the resolver and scheme forms a DOI is often written
// with; Normalize strips them, matched case-insensitively.
var prefixes = []string{
	"https://doi.org/",
	"http://doi.org/",
	"https://dx.doi.org/",
	"http://dx.doi.org/",
	"doi.org/",
	"urn:doi:",
	"doi:",
}

// shape matches a DOI: the 10. directory prefix, a registrant code and
// a non-empty suffix.
var shape = regexp.MustCompile(`^10\.\d{4,9}/\S+$`)

// Normalize returns s with surrounding whitespace, a resolver or "doi:"
// prefix, URL escaping and trailing punctuation removed. Trailing
// punctuation is what a DOI picks up at the end of a sentence or a
// link ("…/s13199-020-00711-x/", "…-x)."); a DOI suffix may contain
// these characters, but never ends with them in practice. The case of
// the DOI is kept: DOIs compare case-insensitively (see Equal). A
// value that does not look like a DOI is returned trimmed but
// otherwise unchanged.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	if strings.Contains(s, "%") {
		if u, err := url.PathUnescape(s); err == nil {
			s = u
		}
	}
	trimmed := strings.TrimRight(s, "/.,;:)]}>\"'")
	if shape.MatchString(trimmed) {
		return trimmed
	}
	return s
}

// Valid reports whether s, once normalized, has the shape of a DOI.
func Valid(s string) bool {
	return shape.MatchString(Normalize(s))
}

// Equal reports whether a and b name the same DOI.
func Equal(a, b string) bool {
	return strings.EqualFold(Normalize(a), Normalize(b))
}
