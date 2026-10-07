// Package casing learns how a document writes its words, so that text
// copied from the document in all capitals (a title, a heading) can be
// restored to its usual case.
//
// A word's usual form is how it is written in running text, away from
// the start of a sentence: "Zygogonium", "Austria", "rbcL", "DNA".
// Words the document writes in lower case mid-sentence stay lower case.
package casing

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// word matches a word token: a letter followed by letters, digits,
// apostrophes or hyphens.
var word = regexp.MustCompile(`\p{L}[\p{L}\p{N}'’\-]*`)

// shoutingShare is the share of upper-case letters above which a
// segment is treated as written in capitals (a heading) and ignored.
const shoutingShare = 0.6

// Vocabulary returns the usual written form of each word that the
// segments write with capitals more often than in lower case, keyed by
// the word in lower case. Words at the start of a sentence, and
// segments written mostly in capitals, do not count as evidence.
func Vocabulary(segments []string) map[string]string {
	lowerCount := map[string]int{}
	formCount := map[string]map[string]int{}
	for _, seg := range segments {
		if Shouting(seg) {
			continue
		}
		locs := word.FindAllStringIndex(seg, -1)
		caps := make([]bool, len(locs))
		for i, loc := range locs {
			caps[i] = allCaps(seg[loc[0]:loc[1]])
		}
		for i, loc := range locs {
			tok := seg[loc[0]:loc[1]]
			low := strings.ToLower(tok)
			if tok == low {
				lowerCount[low]++
				continue
			}
			if sentenceStart(seg[:loc[0]]) {
				continue
			}
			// A word in capitals next to another word in capitals is
			// part of a heading or running head embedded in the text,
			// and a long word in capitals is not an acronym; neither is
			// evidence of the word's usual form.
			if caps[i] && (len([]rune(tok)) > maxAcronymLen ||
				(i > 0 && caps[i-1]) || (i < len(locs)-1 && caps[i+1])) {
				continue
			}
			if formCount[low] == nil {
				formCount[low] = map[string]int{}
			}
			formCount[low][tok]++
		}
	}
	out := map[string]string{}
	for low, forms := range formCount {
		best, n := "", 0
		for f, c := range forms {
			if c > n || (c == n && f < best) {
				best, n = f, c
			}
		}
		if n > lowerCount[low] {
			out[low] = best
		}
	}
	return out
}

// maxAcronymLen is the longest all-capitals word taken as an acronym.
const maxAcronymLen = 6

// allCaps reports whether tok has at least two letters, all upper case.
func allCaps(tok string) bool {
	var letters int
	for _, r := range tok {
		if unicode.IsLetter(r) {
			if !unicode.IsUpper(r) {
				return false
			}
			letters++
		}
	}
	return letters >= 2
}

// sentenceStart reports whether a word following prefix begins a
// sentence: nothing but whitespace or opening punctuation since the
// start of the segment or since sentence-ending punctuation.
func sentenceStart(prefix string) bool {
	for i := len(prefix) - 1; i >= 0; i-- {
		c := prefix[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '(' || c == '[' || c == '"' || c == '\'':
			continue
		case c == '.' || c == '!' || c == '?' || c == ':':
			return true
		default:
			return false
		}
	}
	return true
}

// Shouting reports whether s is written mostly in capitals.
func Shouting(s string) bool {
	var letters, upper int
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters >= 4 && float64(upper) >= shoutingShare*float64(letters)
}

// JATSSegments returns the text of a JATS document's abstract and body
// as one segment per text-bearing element, skipping reference lists.
// The title is skipped too: it is the text being restored.
func JATSSegments(jats []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(jats))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	var (
		segs                      []string
		buf                       strings.Builder
		inAbstract, inBody, inRef int
		inTitle                   int
	)
	flush := func() {
		if t := strings.TrimSpace(buf.String()); t != "" {
			segs = append(segs, t)
		}
		buf.Reset()
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			flush()
			return segs, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "abstract", "trans-abstract":
				inAbstract++
			case "body":
				inBody++
			case "ref-list":
				inRef++
			case "article-title", "title":
				inTitle++
			case "p", "sec", "td", "th", "caption", "list-item":
				flush()
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "abstract", "trans-abstract":
				inAbstract--
			case "body":
				inBody--
			case "ref-list":
				inRef--
			case "article-title", "title":
				inTitle--
				flush()
			case "p", "sec", "td", "th", "caption", "list-item":
				flush()
			}
		case xml.CharData:
			if inRef == 0 && inTitle == 0 && (inAbstract > 0 || inBody > 0) {
				buf.Write(t)
			}
		}
	}
}
