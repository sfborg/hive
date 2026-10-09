package pdftext

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// WordPage is one word from a PDF with its source page number.
// Emitted by WordPages in document order. Coords are PDF points
// (72 DPI) — origin at top-left, x grows right, y grows down.
//
// Used by jatsconv to align GROBID's paragraph text against
// ground-truth word placement, so page-break markers can land at
// the exact word boundary rather than at paragraph boundaries.
type WordPage struct {
	Text string
	Page int
	// YMin is the top of the word's bounding box. Callers use this
	// to filter out running headers / footers by y-position when
	// the alignment step needs body-only text.
	YMin float64
}

// WordPages runs pdftotext -bbox on the given PDF and returns
// every word with its source page number in document order.
// Silent skip (returns nil, nil) when pdftotext isn't installed
// or the PDF is unreadable — callers fall back to paragraph-only
// tracking in that case.
func WordPages(pdfPath string) ([]WordPage, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return nil, nil
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, fmt.Errorf("wordpages: pdf: %w", err)
	}
	// -bbox-layout uses a flow/block/line/word hierarchy that
	// respects natural reading order (top-to-bottom, left-to-right
	// within blocks), whereas -bbox is raw xy-sorted and interleaves
	// side-by-side items (header + title, footer + body last line)
	// unhelpfully.
	cmd := exec.Command("pdftotext", "-bbox-layout", pdfPath, "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("wordpages: pdftotext: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return parseBBoxXHTML(out)
}

// parseBBoxXHTML walks pdftotext -bbox output (XHTML) and pulls
// out (word, page) tuples in document order. Ignores the DTD /
// header wrapper — the parser is set to strict=false so the
// XHTML declaration doesn't trip it up.
func parseBBoxXHTML(src []byte) ([]WordPage, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var (
		out         []WordPage
		currentPage int
		inWord      bool
		wordText    strings.Builder
		wordY       float64
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			// EOF is the expected termination; any other error means
			// the decoder is wedged on a malformed span. `continue`
			// used to be here on the theory that pdftotext quirks
			// were skippable, but the decoder's offset does not
			// advance on error — so the same error re-fires on the
			// next call and the loop spins forever, pegging a core.
			// Bail with what we've collected so far; downstream
			// alignment is tolerant of a short/incomplete stream.
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "page":
				currentPage++
			case "word":
				inWord = true
				wordText.Reset()
				wordY = 0
				for _, a := range t.Attr {
					if a.Name.Local == "yMin" {
						fmt.Sscanf(a.Value, "%f", &wordY)
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "word" && inWord {
				text := strings.TrimSpace(wordText.String())
				if text != "" && currentPage > 0 {
					out = append(out, WordPage{
						Text: text,
						Page: currentPage,
						YMin: wordY,
					})
				}
				inWord = false
			}
		case xml.CharData:
			if inWord {
				wordText.Write(t)
			}
		}
	}
	return out, nil
}

// AlignParagraphPages walks GROBID's paragraph text word-by-word
// against a pdftotext WordPage stream and returns the page number
// of each paragraph word. Words that can't be aligned (rare
// hyphenation quirks, ligature collapses) inherit the previous
// aligned word's page — so a single mismatch never derails
// downstream page-break detection.
//
// Uses a simple forward-scan match with normalized comparison
// (lowercase, alphanumeric-only). The wordCursor points at the
// current position in the pdftotext stream; the caller advances
// it across paragraph invocations so alignment across the
// document stays monotonic.
//
// Returns the pages slice plus the updated cursor position for
// the next call.
type AlignResult struct {
	Pages     []int // same length as paragraph words
	NextIndex int   // resume cursor for the next paragraph
}

// ParaWords splits a paragraph's plain text into words the way
// AlignParagraphPages expects — whitespace-separated tokens,
// preserving order.
func ParaWords(text string) []string {
	fields := strings.Fields(text)
	return fields
}

// AlignParagraphPages matches paragraph words against the word
// stream starting at wordCursor, returning the page of each
// paragraph word.
//
// Two-tier look-ahead:
//   - First word of the paragraph: multi-word signature match
//     across the entire remaining stream (skip past headers,
//     footers, abstract text, or any other non-body noise). Uses
//     a 3-word signature to avoid false matches on common words.
//   - Subsequent words: 24-word look-ahead per word to skip over
//     header/footer intrusions between adjacent body words.
func AlignParagraphPages(paraWords []string, words []WordPage, wordCursor int) AlignResult {
	pages := make([]int, len(paraWords))
	i := wordCursor
	lastMatchedPage := 0
	if i > 0 && i-1 < len(words) {
		lastMatchedPage = words[i-1].Page
	}
	// Signature seek for the first substantive word of the
	// paragraph. Take the first three non-trivial words as a
	// signature and scan the remaining stream for a 3-word run
	// matching them in order. This anchors the cursor at the
	// paragraph's actual start rather than trusting the caller-
	// supplied wordCursor blindly.
	if idx := seekSignature(paraWords, words, i); idx >= 0 {
		i = idx
	}
	const windowSize = 24
	for pi, pw := range paraWords {
		norm := normalizeWord(pw)
		if norm == "" {
			pages[pi] = lastMatchedPage
			continue
		}
		matched := -1
		for k := 0; k < windowSize && i+k < len(words); k++ {
			if normalizeWord(words[i+k].Text) == norm {
				matched = i + k
				break
			}
		}
		if matched < 0 {
			// No match in the look-ahead window; inherit previous
			// page and keep scanning. Common case: hyphenated line
			// wrap turning "labora-\ntory" into two pdftotext
			// words ("labora-" + "tory") vs one paragraph word.
			pages[pi] = lastMatchedPage
			continue
		}
		pages[pi] = words[matched].Page
		lastMatchedPage = words[matched].Page
		i = matched + 1
	}
	return AlignResult{Pages: pages, NextIndex: i}
}

// seekSignature searches the word stream (starting at cursor) for
// the first 3 non-trivial words of the paragraph appearing in
// order. Returns the stream index of the signature's first word,
// or -1 when not found. Anchors alignment even when the caller
// supplied cursor is stale (initial 0 pointing at header/abstract
// text that must be skipped).
//
// The signature words don't have to be adjacent in the stream —
// they only have to appear in order within a bounded window.
// GROBID's paragraph tokenization can differ from pdftotext's
// (e.g., "D." vs a paragraph tokenizer producing "D"), and short
// filler words are skipped during signature-building but still
// occupy positions in the pdftotext stream. Requiring exact
// adjacency would miss almost every real paragraph start.
func seekSignature(paraWords []string, words []WordPage, cursor int) int {
	// Collect up to 3 signature words that are at least 3 chars
	// after normalization — common short words ("the", "of") make
	// false matches too easy.
	sig := make([]string, 0, 3)
	for _, w := range paraWords {
		n := normalizeWord(w)
		if len(n) < 3 {
			continue
		}
		sig = append(sig, n)
		if len(sig) == 3 {
			break
		}
	}
	if len(sig) == 0 {
		return -1
	}
	// Look for sig[0] anywhere from cursor onward. When sig[0]
	// matches, verify sig[1] and sig[2] appear within the same
	// small window (in order) — allows a few filler words between
	// signature words but rejects false anchoring on unrelated
	// occurrences of the first signature word.
	const gap = 12
	for i := cursor; i < len(words); i++ {
		if normalizeWord(words[i].Text) != sig[0] {
			continue
		}
		// Walk forward looking for sig[1], then sig[2], each
		// within `gap` words of the previous match.
		matched := true
		pos := i
		for _, s := range sig[1:] {
			found := -1
			for k := pos + 1; k < len(words) && k <= pos+gap; k++ {
				if normalizeWord(words[k].Text) == s {
					found = k
					break
				}
			}
			if found < 0 {
				matched = false
				break
			}
			pos = found
		}
		if matched {
			return i
		}
	}
	return -1
}

// normalizedRe strips everything except letters and digits so
// punctuation, hyphens, and stray spaces don't derail matching.
var normalizedRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normalizeWord(s string) string {
	return strings.ToLower(normalizedRe.ReplaceAllString(s, ""))
}

// JournalPageOfPDFPage1 scans the first pdftotext -bbox page for
// the paper's page range (e.g., "83-93." in the running header)
// and returns the starting page — 83 in the example. Used to
// compute the offset for translating PDF-index page numbers to
// journal-labeled page numbers curators cite from.
//
// Returns 0 (no error) when no page range is found; caller falls
// back to raw PDF-index page numbers in that case.
//
// Detection is deliberately conservative: matches a page range such
// as `175-188`, `175–188` or `82:175–188` (volume:range) on page 1
// words only, requires the two numbers to be plausibly
// consecutive-ish (second > first, second - first < 200) and
// small enough to be page numbers.
func JournalPageOfPDFPage1(words []WordPage) int {
	rangeRe := regexp.MustCompile(`^(?:\d{1,4}:)?(\d{1,4})[-\x{2010}-\x{2015}](\d{1,4})[.,;]?$`)
	for _, w := range words {
		if w.Page != 1 {
			if w.Page > 1 {
				return 0
			}
			continue
		}
		m := rangeRe.FindStringSubmatch(w.Text)
		if m == nil {
			continue
		}
		var start, end int
		fmt.Sscanf(m[1], "%d", &start)
		fmt.Sscanf(m[2], "%d", &end)
		if start < 1 || start > 9999 || end <= start || end-start > 200 {
			continue
		}
		return start
	}
	return 0
}
