package refmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sfborg/hive/pkg/correction"
)

var (
	pageOffsetAttrRegex = regexp.MustCompile(`\shive:pdf-page-offset="(-?\d+)"`)
	articleOpenRegex    = regexp.MustCompile(`<article[\s>]`)
	pageBreakAttrRegex  = regexp.MustCompile(`(<hive:page-break\s+page=")(\d+)(")`)
	fpageRegex          = regexp.MustCompile(`<fpage>[^<]*</fpage>`)
	lpageRegex          = regexp.MustCompile(`<lpage>[^<]*</lpage>`)
)

// pageOffset returns the offset between printed page numbers and PDF
// page indexes recorded on <article>, or 0.
func pageOffset(jats []byte) int {
	loc := articleOpenRegex.FindIndex(jats)
	if loc == nil {
		return 0
	}
	end := bytes.IndexByte(jats[loc[0]:], '>')
	if end < 0 {
		return 0
	}
	m := pageOffsetAttrRegex.FindSubmatch(jats[loc[0] : loc[0]+end])
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// ApplyPageNumberingToJATS sets the printed page number of PDF page 1.
// Every page-break marker is renumbered from its PDF page index, the
// article's hive:pdf-page-offset records the new offset, and the
// article's <fpage>/<lpage> are set (added when missing) to its first
// and last printed pages so the JATS carries the right numbers
// outside hive too.
// Page numbers in the reference list are left alone.
func ApplyPageNumberingToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindPageNumbering {
		return nil, fmt.Errorf("apply page-numbering: expected page-numbering kind, got %s", c.Kind)
	}
	var p correction.PageNumberingPayload
	if err := json.Unmarshal([]byte(c.Payload), &p); err != nil {
		return nil, fmt.Errorf("apply page-numbering: parse payload: %w", err)
	}
	if p.PDFPage1 < 1 {
		return nil, fmt.Errorf("apply page-numbering: pdf_page_1 must be at least 1")
	}
	cur := pageOffset(jats)
	next := p.PDFPage1 - 1
	lastIndex := 1
	out := pageBreakAttrRegex.ReplaceAllFunc(jats, func(m []byte) []byte {
		sub := pageBreakAttrRegex.FindSubmatch(m)
		n, _ := strconv.Atoi(string(sub[2]))
		index := n - cur
		if index > lastIndex {
			lastIndex = index
		}
		return []byte(string(sub[1]) + strconv.Itoa(index+next) + string(sub[3]))
	})
	out = setArticlePageOffset(out, next)
	// Only the article's own pages change; citations in the
	// reference list carry their own fpage/lpage.
	start := bytes.Index(out, []byte("<article-meta"))
	end := bytes.Index(out, []byte("</article-meta>"))
	if start < 0 || end < start {
		return out, nil
	}
	meta := out[start:end]
	fpage := "<fpage>" + strconv.Itoa(p.PDFPage1) + "</fpage>"
	lpage := "<lpage>" + strconv.Itoa(lastIndex+next) + "</lpage>"
	if fpageRegex.Match(meta) {
		meta = fpageRegex.ReplaceAll(meta, []byte(fpage))
		if lpageRegex.Match(meta) {
			meta = lpageRegex.ReplaceAll(meta, []byte(lpage))
		} else {
			meta = []byte(strings.Replace(string(meta), fpage, fpage+lpage, 1))
		}
	} else {
		// JATS places fpage/lpage after volume and issue and before
		// the elements below; insert before the first one present.
		at := len(meta)
		if loc := afterPagesRegex.FindIndex(meta); loc != nil {
			at = loc[0]
		}
		meta = []byte(string(meta[:at]) + fpage + lpage + string(meta[at:]))
	}
	return splice(out, start, end, string(meta)), nil
}

var afterPagesRegex = regexp.MustCompile(
	`<(page-range|elocation-id|product|supplementary-material|history|permissions|self-uri|related-article|related-object|abstract|trans-abstract|kwd-group|funding-group|support-group|conference|counts|custom-meta-group)[\s>]`)

// setArticlePageOffset writes hive:pdf-page-offset on the <article>
// start tag, replacing an existing value.
func setArticlePageOffset(jats []byte, offset int) []byte {
	loc := articleOpenRegex.FindIndex(jats)
	if loc == nil {
		return jats
	}
	end := bytes.IndexByte(jats[loc[0]:], '>')
	if end < 0 {
		return jats
	}
	end += loc[0]
	tag := jats[loc[0]:end]
	value := strconv.Itoa(offset)
	var newTag []byte
	if pageOffsetAttrRegex.Match(tag) {
		newTag = pageOffsetAttrRegex.ReplaceAll(tag, []byte(` hive:pdf-page-offset="`+value+`"`))
	} else {
		newTag = append(append([]byte{}, tag...), []byte(` hive:pdf-page-offset="`+value+`"`)...)
	}
	return splice(jats, loc[0], end, string(newTag))
}
