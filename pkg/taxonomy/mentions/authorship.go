package mentions

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gnames/gnparser"
	"github.com/gnames/gnparser/ent/parsed"
)

// Scan is what one augmented JATS file says about its tagged names.
type Scan struct {
	// Names holds how many times each name is tagged (see Count).
	Names map[string]int
	// Authors holds, per name, the authorships that follow it in the
	// text, with how often each appears.
	Authors map[string]map[string]int
}

// tailRunes bounds the text read after a tagged name when looking for
// its authorship. It is generous; gnparser marks where the authorship
// ends.
const tailRunes = 250

// inlineElements may occur inside a sentence, so the text after a
// name continues through them. Any other element ends the text a name
// can take an authorship from.
var inlineElements = map[string]bool{
	"italic": true, "bold": true, "sc": true, "sup": true, "sub": true,
	"underline": true, "monospace": true, "roman": true, "sans-serif": true,
	"styled-content": true, "named-content": true, "taxon-name": true,
	"taxon-name-part": true, "ext-link": true, "xref": true, "break": true,
	"noise": true, "page-break": true,
}

// skippedElements hold text that is not part of the sentence: removed
// text and page markers.
var skippedElements = map[string]bool{"noise": true, "page-break": true}

// bareName is a tagged name parsed alone: its canonical form and the
// warnings it raises by itself.
type bareName struct {
	canonical string
	warnings  map[parsed.Warning]bool
}

// pendingTail collects the text after one tagged name.
type pendingTail struct {
	name string
	// uncounted marks an unresolved abbreviation: not counted, but its
	// authorship still claims words tagged by mistake.
	uncounted bool
	buf  strings.Builder
	n    int // runes in buf
	// tagged are the names tagged inside the tail, with their byte
	// span in buf and the occurrence they were counted as.
	tagged []taggedSpan
}

type taggedSpan struct {
	name       string
	start, end int
	occurrence int
}

// ScanJATS counts the tagged names in augmented JATS (see Count) and
// reads the authorship that follows each name, if any. A word the
// annotator tagged as a name but which turns out to be part of the
// preceding name's authorship ("Navicula gottlandica Grunow") is not
// counted.
func ScanJATS(jats []byte) (Scan, error) {
	s := Scan{Names: map[string]int{}, Authors: map[string]map[string]int{}}
	parser := gnparser.New(gnparser.NewConfig())
	bare := map[string]bareName{}
	bareOf := func(name string) bareName {
		b, ok := bare[name]
		if !ok {
			r := parser.ParseName(name)
			if r.Canonical != nil {
				b.canonical = r.Canonical.Simple
			}
			b.warnings = map[parsed.Warning]bool{}
			for _, w := range r.QualityWarnings {
				b.warnings[w.Warning] = true
			}
			bare[name] = b
		}
		return b
	}
	// counted[i] is the name of the i-th counted occurrence; retracted
	// marks occurrences taken back as part of an authorship.
	var counted []string
	retracted := map[int]bool{}
	var pending []*pendingTail

	finish := func(t *pendingTail) {
		auth, swallowed := authorshipOf(parser, t, bareOf(t.name))
		if swallowed >= 0 {
			retracted[swallowed] = true
		}
		if auth == "" || t.uncounted {
			return
		}
		if s.Authors[t.name] == nil {
			s.Authors[t.name] = map[string]int{}
		}
		s.Authors[t.name][auth]++
	}
	flush := func() {
		for _, t := range pending {
			finish(t)
		}
		pending = pending[:0]
	}
	write := func(text string) {
		kept := pending[:0]
		for _, t := range pending {
			t.buf.WriteString(text)
			t.n += utf8.RuneCountInString(text)
			if t.n >= tailRunes {
				finish(t)
				continue
			}
			kept = append(kept, t)
		}
		pending = kept
	}

	dec := xml.NewDecoder(bytes.NewReader(jats))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	// collecting > 0 while inside a tag without full-name, whose text
	// is the name; inFull while inside a tag with one. skip > 0 inside
	// removed text.
	var collecting, skip int
	var inFull bool
	var buf strings.Builder
	var current string // name of the open taxon-name
	// tagStart is where the open taxon-name began in each open tail.
	tagStart := map[*pendingTail]int{}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Scan{}, fmt.Errorf("mentions: parse JATS: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			if skippedElements[local] {
				skip++
				continue
			}
			if skip > 0 {
				continue
			}
			if !inlineElements[local] {
				flush()
				continue
			}
			if local != "taxon-name" {
				continue
			}
			if collecting > 0 || inFull {
				if collecting > 0 {
					collecting++
				}
				continue
			}
			full := ""
			for _, attr := range t.Attr {
				if attr.Name.Local == "full-name" {
					full = attr.Value
				}
			}
			clear(tagStart)
			for _, p := range pending {
				tagStart[p] = p.buf.Len()
			}
			// The tag's text still flows into open tails; the name is
			// counted when the tag closes.
			if full != "" {
				current, inFull = normalizeName(full), true
				continue
			}
			collecting = 1
			buf.Reset()
		case xml.CharData:
			if skip > 0 {
				continue
			}
			if collecting > 0 {
				buf.Write(t)
			}
			write(string(t))
		case xml.EndElement:
			local := t.Name.Local
			if skippedElements[local] {
				if skip > 0 {
					skip--
				}
				continue
			}
			if skip > 0 {
				continue
			}
			if !inlineElements[local] {
				flush()
				continue
			}
			if local != "taxon-name" || (collecting == 0 && !inFull) {
				continue
			}
			if collecting > 0 {
				collecting--
				if collecting > 0 {
					continue
				}
				current = normalizeName(buf.String())
			}
			inFull = false
			name := current
			if name == "" {
				continue
			}
			if abbreviated.MatchString(name) {
				pending = append(pending, &pendingTail{name: name, uncounted: true})
				continue
			}
			s.Names[name]++
			occurrence := len(counted)
			counted = append(counted, name)
			for _, p := range pending {
				if start, ok := tagStart[p]; ok {
					p.tagged = append(p.tagged, taggedSpan{name, start, p.buf.Len(), occurrence})
				}
			}
			pending = append(pending, &pendingTail{name: name})
		}
	}
	flush()
	for occ := range retracted {
		name := counted[occ]
		if s.Names[name]--; s.Names[name] <= 0 {
			delete(s.Names, name)
		}
	}
	return s, nil
}

func normalizeName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// authorParticles are lowercase words that occur inside an
// authorship ("Grunow in Van Heurck", "Kütz. ex Rabenh.").
var authorParticles = map[string]bool{
	"in": true, "ex": true, "et": true, "de": true, "del": true, "della": true,
	"der": true, "den": true, "van": true, "von": true, "da": true, "du": true,
	"dos": true, "le": true, "la": true, "f.": true, "fil.": true, "emend.": true,
	"zu": true, "y": true, "al.": true, "d'": true,
}

// initial matches an author's initial ("C.", "D.G.").
var initial = regexp.MustCompile(`^(?:\p{Lu}\.)+$`)

// year matches a publication year.
var year = regexp.MustCompile(`\b(1[5-9]\d\d|20\d\d)\b`)

// HasYear reports whether an authorship includes a year.
func HasYear(authorship string) bool { return year.MatchString(authorship) }

// trimTail cuts the text after a name to the part that can hold an
// authorship: it stops at the first lowercase word that is not an
// authorship particle, at ":" or ";", and at a sentence end (a word
// ending in "." other than an initial, followed by a capitalized word
// that ends the text).
func trimTail(tail string) string {
	if i := strings.IndexAny(tail, ":;"); i >= 0 {
		tail = tail[:i]
	}
	words := strings.Fields(tail)
	n := 0
	for i, w := range words {
		r, _ := utf8.DecodeRuneInString(w)
		if unicode.IsLower(r) && !authorParticles[w] {
			break
		}
		n = i + 1
	}
	words = words[:n]
	for i := 0; i+1 < len(words); i++ {
		w := words[i]
		if strings.HasSuffix(w, ".") && !initial.MatchString(w) && i+2 == len(words) {
			words = words[:i+1]
			break
		}
	}
	return strings.Join(words, " ")
}

// continues matches an authorship that may go on into a following
// tagged word: a basionym author alone ("(Kützing)") or one ending in
// a joining word ("Grunow in", "Kütz. ex").
var continues = regexp.MustCompile(`^\([^()]*\)$|(?:^|\s)(?:in|ex|et|&|and|emend\.)$`)

// authorshipOf finds the authorship after a tagged name. The text is
// read up to the next tagged name, since a list of names ("Ulva
// L., Umbraulva E.H.Bae & I.K.Lee") would otherwise run together;
// it goes on through that one tagged word only when the authorship
// so far is empty or plainly continues ("(Kützing) Grunow",
// "(Grunow) Hendey"), and
// then the word, tagged by mistake, is part of the authorship. It
// returns the authorship and the occurrence of the swallowed word,
// or -1.
func authorshipOf(parser gnparser.GNparser, t *pendingTail, name bareName) (string, int) {
	text := t.buf.String()
	if len(t.tagged) == 0 {
		auth, _ := authorshipAfter(parser, t.name, name, text)
		return auth, -1
	}
	first := t.tagged[0]
	auth, end := authorshipAfter(parser, t.name, name, text[:first.start])
	if strings.Trim(text[end:first.start], " \t\n(") != "" || (auth != "" && !continues.MatchString(auth)) {
		return auth, -1
	}
	limit := len(text)
	if len(t.tagged) > 1 {
		limit = t.tagged[1].start
	}
	if longer, end := authorshipAfter(parser, t.name, name, text[:limit]); longer != "" && end > first.start {
		return longer, first.occurrence
	}
	return auth, -1
}

// authorshipAfter parses a name followed by the text after it and
// returns the authorship gnparser finds, with the byte offset in tail
// where that authorship ends. It returns "" when the parse does not
// keep the name's canonical form or adds a serious warning other than
// an unparsed tail.
func authorshipAfter(parser gnparser.GNparser, name string, b bareName, tail string) (string, int) {
	trimmed := trimTail(tail)
	if trimmed == "" || b.canonical == "" {
		return "", 0
	}
	r := parser.ParseName(name + " " + trimmed)
	if !r.Parsed || r.Canonical == nil || r.Canonical.Simple != b.canonical || r.Authorship == nil {
		return "", 0
	}
	for _, w := range r.QualityWarnings {
		if w.Warning != parsed.TailWarn && w.Quality >= 3 && !b.warnings[w.Warning] {
			return "", 0
		}
	}
	auth := strings.TrimSpace(r.Authorship.Verbatim)
	// A genus or higher taxon has no basionym author, so parentheses
	// after one hold a citation ("Coleofasciculus (Siegesmund et al.
	// 2008)").
	if auth == "" || (strings.HasPrefix(auth, "(") && !strings.Contains(b.canonical, " ")) {
		return "", 0
	}
	// Find where the authorship ends in the raw tail, word by word,
	// since the tail's whitespace may differ.
	pos := 0
	for _, w := range strings.Fields(auth) {
		i := strings.Index(tail[pos:], w)
		if i < 0 {
			return auth, len(tail)
		}
		pos += i + len(w)
	}
	return auth, pos
}

// MergeScans combines the scans of several files of the same paper:
// each name and each authorship keeps its highest count (see Merge).
func MergeScans(scans ...Scan) Scan {
	out := Scan{Authors: map[string]map[string]int{}}
	names := make([]map[string]int, len(scans))
	for i, s := range scans {
		names[i] = s.Names
		for name, auths := range s.Authors {
			if out.Authors[name] == nil {
				out.Authors[name] = map[string]int{}
			}
			for a, n := range auths {
				out.Authors[name][a] = max(out.Authors[name][a], n)
			}
		}
	}
	out.Names = Merge(names...)
	return out
}

// PickAuthorship returns the authorship to verify a name with: one
// with a year if any, then the most frequent, then the longest.
func PickAuthorship(auths map[string]int) string {
	best := ""
	better := func(a string) bool {
		if HasYear(a) != HasYear(best) {
			return HasYear(a)
		}
		if auths[a] != auths[best] {
			return auths[a] > auths[best]
		}
		if len(a) != len(best) {
			return len(a) > len(best)
		}
		return a < best
	}
	for a := range auths {
		if best == "" || better(a) {
			best = a
		}
	}
	return best
}
