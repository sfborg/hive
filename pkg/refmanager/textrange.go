package refmanager

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sfborg/hive/pkg/correction"
)

// Range corrections act on a span of text inside one body block (<p>
// or <title>). Only the target block is parsed, into the small tree
// below; the rest of the document is copied byte for byte. Start tags
// are kept verbatim, so namespace prefixes and attribute order survive
// a round trip.

type xnodeKind int

const (
	xText xnodeKind = iota
	xElem
	xRaw // comment, processing instruction, CDATA: copied verbatim
)

type xnode struct {
	kind      xnodeKind
	raw       string // text: escaped character data; elem: start tag; raw: whole construct
	name      string // elem only
	selfClose bool
	children  []*xnode
	parent    *xnode
}

// parseXMLFragment parses a well-formed XML fragment into a node
// sequence attached to a synthetic root.
func parseXMLFragment(src []byte) (*xnode, error) {
	root := &xnode{kind: xElem}
	cur := root
	i := 0
	for i < len(src) {
		if src[i] != '<' {
			j := bytes.IndexByte(src[i:], '<')
			if j < 0 {
				j = len(src) - i
			}
			cur.append(&xnode{kind: xText, raw: string(src[i : i+j])})
			i += j
			continue
		}
		rest := src[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			end := bytes.Index(rest, []byte("-->"))
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			cur.append(&xnode{kind: xRaw, raw: string(rest[:end+3])})
			i += end + 3
		case bytes.HasPrefix(rest, []byte("<![CDATA[")):
			end := bytes.Index(rest, []byte("]]>"))
			if end < 0 {
				return nil, fmt.Errorf("unterminated CDATA")
			}
			cur.append(&xnode{kind: xRaw, raw: string(rest[:end+3])})
			i += end + 3
		case bytes.HasPrefix(rest, []byte("<?")):
			end := bytes.Index(rest, []byte("?>"))
			if end < 0 {
				return nil, fmt.Errorf("unterminated processing instruction")
			}
			cur.append(&xnode{kind: xRaw, raw: string(rest[:end+2])})
			i += end + 2
		case bytes.HasPrefix(rest, []byte("</")):
			end := bytes.IndexByte(rest, '>')
			if end < 0 {
				return nil, fmt.Errorf("unterminated end tag")
			}
			name := strings.TrimSpace(string(rest[2:end]))
			if cur == root || cur.name != name {
				return nil, fmt.Errorf("unexpected end tag </%s>", name)
			}
			cur = cur.parent
			i += end + 1
		default:
			end := tagEnd(rest)
			if end < 0 {
				return nil, fmt.Errorf("unterminated start tag")
			}
			tag := string(rest[:end+1])
			n := &xnode{kind: xElem, raw: tag, name: tagName(tag)}
			n.selfClose = strings.HasSuffix(tag, "/>")
			cur.append(n)
			if !n.selfClose {
				cur = n
			}
			i += end + 1
		}
	}
	if cur != root {
		return nil, fmt.Errorf("unclosed element <%s>", cur.name)
	}
	return root, nil
}

// tagEnd returns the index of the '>' closing a start tag, skipping
// quoted attribute values (which may contain '>').
func tagEnd(b []byte) int {
	var quote byte
	for i := 1; i < len(b); i++ {
		c := b[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

func tagName(tag string) string {
	s := strings.TrimPrefix(tag, "<")
	end := strings.IndexAny(s, " \t\r\n/>")
	if end < 0 {
		return s
	}
	return s[:end]
}

func (n *xnode) append(c *xnode) {
	c.parent = n
	n.children = append(n.children, c)
}

func (n *xnode) index() int {
	for i, c := range n.parent.children {
		if c == n {
			return i
		}
	}
	return -1
}

func (n *xnode) attr(name string) string {
	re := regexp.MustCompile(`\s` + regexp.QuoteMeta(name) + `="([^"]*)"`)
	if m := re.FindStringSubmatch(n.raw); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// serialize writes the node and its subtree back to XML.
func (n *xnode) serialize(b *strings.Builder) {
	switch n.kind {
	case xText, xRaw:
		b.WriteString(n.raw)
	case xElem:
		b.WriteString(n.raw)
		if n.selfClose {
			return
		}
		for _, c := range n.children {
			c.serialize(b)
		}
		b.WriteString("</" + n.name + ">")
	}
}

func serializeNodes(nodes []*xnode) string {
	var b strings.Builder
	for _, n := range nodes {
		n.serialize(&b)
	}
	return b.String()
}

// skipForText reports whether an element's text is excluded from
// block text: content the reader replaces or decorates, and earlier
// corrections' wrappers.
func skipForText(n *xnode) bool {
	switch n.name {
	case "label", "fig", "table-wrap", "hive:noise", "hive:page-break":
		return true
	}
	return false
}

// textLeaf is a text node counted in block text, with its decoded
// content and its rune offset within the block.
type textLeaf struct {
	node  *xnode
	text  []rune
	start int
}

func collectLeaves(n *xnode, out *[]textLeaf, pos *int) {
	for _, c := range n.children {
		switch c.kind {
		case xText:
			t := []rune(html.UnescapeString(c.raw))
			*out = append(*out, textLeaf{node: c, text: t, start: *pos})
			*pos += len(t)
		case xElem:
			if !skipForText(c) {
				collectLeaves(c, out, pos)
			}
		}
	}
}

// blockText returns the block's text as the reader shows it.
func blockText(n *xnode) []rune {
	var leaves []textLeaf
	pos := 0
	collectLeaves(n, &leaves, &pos)
	var out []rune
	for _, l := range leaves {
		out = append(out, l.text...)
	}
	return out
}

// splitLeafAt splits the text leaf containing rune offset off so a
// node boundary falls there. It returns the parent and the child index
// of that boundary. atEnd picks the leaf that ends at off when off
// falls between two leaves, so an end boundary stays inside the
// selection's last node.
func splitLeafAt(block *xnode, off int, atEnd bool) (*xnode, int, error) {
	var leaves []textLeaf
	pos := 0
	collectLeaves(block, &leaves, &pos)
	for _, l := range leaves {
		end := l.start + len(l.text)
		inside := off >= l.start && off < end
		if atEnd {
			inside = off > l.start && off <= end
		}
		if !inside {
			continue
		}
		k := off - l.start
		idx := l.node.index()
		if k == 0 {
			return l.node.parent, idx, nil
		}
		if k == len(l.text) {
			return l.node.parent, idx + 1, nil
		}
		// The existing node keeps the right part, so a caller holding
		// it as the node before a later boundary still points at the
		// text adjacent to that boundary.
		p := l.node.parent
		left := &xnode{kind: xText, raw: correction.XMLEscape(string(l.text[:k])), parent: p}
		l.node.raw = correction.XMLEscape(string(l.text[k:]))
		p.children = append(p.children[:idx], append([]*xnode{left}, p.children[idx:]...)...)
		return p, idx + 1, nil
	}
	if off == pos {
		return block, len(block.children), nil
	}
	return nil, 0, fmt.Errorf("offset %d outside block text", off)
}

// liftBoundary moves a boundary at (parent, idx) up to a child index
// of root, splitting each element it passes through into two copies
// that share the original start tag.
func liftBoundary(root, parent *xnode, idx int) int {
	for parent != root {
		gp := parent.parent
		pi := parent.index()
		switch {
		case idx == 0:
			idx = pi
		case idx >= len(parent.children):
			idx = pi + 1
		default:
			right := &xnode{kind: xElem, raw: parent.raw, name: parent.name, parent: gp}
			right.children = append([]*xnode{}, parent.children[idx:]...)
			for _, c := range right.children {
				c.parent = right
			}
			parent.children = parent.children[:idx]
			gp.children = append(gp.children[:pi+1], append([]*xnode{right}, gp.children[pi+1:]...)...)
			idx = pi + 1
		}
		parent = gp
	}
	return idx
}

// isolateRange makes the text range [s, e) of block a contiguous run
// of block's children and returns that run's bounds.
func isolateRange(block *xnode, s, e int) (int, int, error) {
	// Split the end first so the start split doesn't shift it, and
	// remember a node next to the end boundary: the start lift can
	// move nodes into new parents or shift their indices.
	ep, ei, err := splitLeafAt(block, e, true)
	if err != nil {
		return 0, 0, err
	}
	var before, after *xnode
	if ei > 0 {
		before = ep.children[ei-1]
	} else if ei < len(ep.children) {
		after = ep.children[ei]
	}
	sp, si, err := splitLeafAt(block, s, false)
	if err != nil {
		return 0, 0, err
	}
	i := liftBoundary(block, sp, si)
	switch {
	case before != nil:
		ep, ei = before.parent, before.index()+1
	case after != nil:
		ep, ei = after.parent, after.index()
	}
	j := liftBoundary(block, ep, ei)
	if j <= i {
		return 0, 0, fmt.Errorf("empty range")
	}
	return i, j, nil
}

// mergeAt joins block children idx-1 and idx when the split in
// liftBoundary or splitLeafAt produced them: adjacent text nodes, or
// adjacent elements with identical start tags. Recurses into the
// merged element so nested splits are joined too.
func mergeAt(parent *xnode, idx int) {
	if idx <= 0 || idx >= len(parent.children) {
		return
	}
	l, r := parent.children[idx-1], parent.children[idx]
	switch {
	case l.kind == xText && r.kind == xText:
		l.raw += r.raw
	case l.kind == xElem && r.kind == xElem && !l.selfClose && !r.selfClose && l.raw == r.raw:
		join := len(l.children)
		for _, c := range r.children {
			c.parent = l
		}
		l.children = append(l.children, r.children...)
		defer mergeAt(l, join)
	default:
		return
	}
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)
}

// locateQuote finds the rune offsets of a segment's selection within
// text, preferring the occurrence whose surrounding text matches the
// recorded prefix and suffix, then the one nearest the offset hint.
func locateQuote(text []rune, seg correction.TextSegment) (int, int, bool) {
	exact := []rune(seg.Exact)
	if len(exact) == 0 {
		return 0, 0, false
	}
	str := string(text)
	prefix, suffix := []rune(seg.Prefix), []rune(seg.Suffix)
	best, bestScore, bestDist := -1, -1, 0
	for from := 0; ; {
		k := strings.Index(str[from:], seg.Exact)
		if k < 0 {
			break
		}
		byteOff := from + k
		s := utf8.RuneCountInString(str[:byteOff])
		score := commonSuffix(text[:s], prefix) + commonPrefix(text[s+len(exact):], suffix)
		dist := s - seg.Start
		if dist < 0 {
			dist = -dist
		}
		if score > bestScore || (score == bestScore && dist < bestDist) {
			best, bestScore, bestDist = s, score, dist
		}
		from = byteOff + 1
		for from < len(str) && !utf8.RuneStart(str[from]) {
			from++
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, best + len(exact), true
}

func commonSuffix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func commonPrefix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// blockSpan is one candidate block in the document.
type blockSpan struct {
	start, end int
	node       *xnode
	text       []rune
	pageMatch  bool
}

// findSegmentBlock locates the block a segment was taken from: the
// block whose text equals the recorded block text, else the block
// whose text contains the selection with the best-matching context.
// pdfPage narrows candidates when the same text repeats on several
// pages.
func findSegmentBlock(jats []byte, seg correction.TextSegment) (*blockSpan, error) {
	want := collapseWhitespace(strings.TrimSpace(seg.BlockText))
	var exact, contains []*blockSpan
	pos := 0
	for {
		absStart, absOpenEnd, name, ok := nextBlockTag(jats, pos, len(jats))
		if !ok {
			break
		}
		pos = absOpenEnd
		if bytes.Contains(jats[absStart:absOpenEnd], []byte(`content-type="hive-page-break"`)) {
			continue
		}
		absEnd, ok := matchingCloseEnd(jats, absOpenEnd, name)
		if !ok {
			break
		}
		root, err := parseXMLFragment(jats[absStart:absEnd])
		if err != nil || len(root.children) != 1 {
			continue
		}
		b := &blockSpan{start: absStart, end: absEnd, node: root.children[0]}
		b.pageMatch = seg.PDFPage > 0 && pageBefore(jats, absStart) == seg.PDFPage
		b.text = blockText(b.node)
		if want != "" && collapseWhitespace(strings.TrimSpace(string(b.text))) == want {
			exact = append(exact, b)
		} else if strings.Contains(string(b.text), seg.Exact) {
			contains = append(contains, b)
		}
	}
	// Context match ranks first; a matching page breaks ties between
	// blocks with identical text on different pages.
	pick := func(cands []*blockSpan) *blockSpan {
		var best *blockSpan
		bestScore := -1
		for _, b := range cands {
			s, _, ok := locateQuote(b.text, seg)
			if !ok {
				continue
			}
			score := 2 * (commonSuffix(b.text[:s], []rune(seg.Prefix)) +
				commonPrefix(b.text[s+len([]rune(seg.Exact)):], []rune(seg.Suffix)))
			if b.pageMatch {
				score++
			}
			if score > bestScore {
				best, bestScore = b, score
			}
		}
		return best
	}
	if b := pick(exact); b != nil {
		return b, nil
	}
	if b := pick(contains); b != nil {
		return b, nil
	}
	return nil, fmt.Errorf("selected text not found")
}

// pageBefore returns the page number of the last page-break marker
// before offset, or 0.
func pageBefore(jats []byte, offset int) int {
	page := 0
	for _, m := range pageBreakRegex.FindAllSubmatchIndex(jats[:offset], -1) {
		if n, err := strconv.Atoi(string(jats[m[2]:m[3]])); err == nil {
			page = n
		}
	}
	return page
}

// wrapperAttrs holds the attributes written on a range wrapper.
type wrapperAttrs struct {
	correctionID string
	segment      int
	label        string
	movedTo      string
	tags         int // merge only: section tags written after the merged paragraph
}

func (w wrapperAttrs) startTag(original string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<hive:noise correction-id="%s" segment="%d"`,
		correction.XMLEscape(w.correctionID), w.segment)
	if w.label != "" {
		fmt.Fprintf(&b, ` label="%s"`, attrEscape(w.label))
	}
	if w.movedTo != "" {
		fmt.Fprintf(&b, ` moved-to="%s"`, attrEscape(w.movedTo))
	}
	if w.tags > 0 {
		fmt.Fprintf(&b, ` tags="%d"`, w.tags)
	}
	fmt.Fprintf(&b, ` original="%s">`, original)
	return b.String()
}

func attrEscape(s string) string {
	return strings.ReplaceAll(correction.XMLEscape(s), `"`, "&quot;")
}

// wrapSegment applies one segment: it isolates the selected range in
// its block and replaces it with a wrapper holding the range's XML in
// a comment. A selection covering the whole block wraps the block
// element itself. It returns the new document and the XML that was
// wrapped (for moves, the content to place elsewhere; for whole
// blocks, the block's inner XML).
func wrapSegment(jats []byte, seg correction.TextSegment, w wrapperAttrs) ([]byte, string, error) {
	b, err := findSegmentBlock(jats, seg)
	if err != nil {
		return nil, "", err
	}
	s, e, ok := locateQuote(b.text, seg)
	if !ok {
		return nil, "", fmt.Errorf("selected text not found in block")
	}
	var replacement, moved string
	if s == 0 && e == len(b.text) {
		replacement = w.startTag("element") +
			"<!-- " + escapeComment(string(jats[b.start:b.end])) + " --></hive:noise>"
		moved = serializeNodes(b.node.children)
	} else {
		i, j, err := isolateRange(b.node, s, e)
		if err != nil {
			return nil, "", err
		}
		run := b.node.children[i:j]
		moved = serializeNodes(run)
		wrapper := &xnode{kind: xRaw, parent: b.node,
			raw: w.startTag("fragment") + "<!-- " + escapeComment(moved) + " --></hive:noise>"}
		b.node.children = append(b.node.children[:i],
			append([]*xnode{wrapper}, b.node.children[j:]...)...)
		var sb strings.Builder
		b.node.serialize(&sb)
		replacement = sb.String()
	}
	var out bytes.Buffer
	out.Write(jats[:b.start])
	out.WriteString(replacement)
	out.Write(jats[b.end:])
	return out.Bytes(), moved, nil
}

// hasWrapper reports whether the document already holds a wrapper for
// the correction, which makes applying it again a no-op.
func hasWrapper(jats []byte, correctionID string) bool {
	return bytes.Contains(jats, []byte(`<hive:noise correction-id="`+correction.XMLEscape(correctionID)+`"`))
}

// ApplyTextRangeRemoveToJATS applies a text-remove whose payload
// carries selection segments.
func ApplyTextRangeRemoveToJATS(jats []byte, c *correction.Correction, p correction.TextRemovePayload) ([]byte, error) {
	if hasWrapper(jats, c.ID) {
		return jats, nil
	}
	for i, seg := range p.Segments {
		out, _, err := wrapSegment(jats, seg, wrapperAttrs{correctionID: c.ID, segment: i, label: p.Label})
		if err != nil {
			return nil, fmt.Errorf("apply text-remove segment %d: %w", i, err)
		}
		jats = out
	}
	return jats, nil
}

// ApplyTextTagToJATS applies a text-tag correction. The supported
// action, move-to-caption, takes the selected text out of the body
// and appends it to the caption of the figure or table named by
// TargetElementID.
func ApplyTextTagToJATS(jats []byte, c *correction.Correction) ([]byte, error) {
	if c.Kind != correction.KindTextTag {
		return nil, fmt.Errorf("apply text-tag: expected text-tag kind, got %s", c.Kind)
	}
	p, err := correction.ParseTextTagPayload(c.Payload)
	if err != nil {
		return nil, fmt.Errorf("apply text-tag: %w", err)
	}
	switch p.Action {
	case correction.TextTagMoveToCaption:
	case correction.TextTagMergeNext:
		return applyMergeNext(jats, c, p)
	default:
		return nil, fmt.Errorf("apply text-tag: unsupported action %q", p.Action)
	}
	if c.TargetElementID == "" {
		return nil, fmt.Errorf("apply text-tag: target_element_id required")
	}
	if hasWrapper(jats, c.ID) {
		return jats, nil
	}
	if _, _, ok := findFigOrTable(jats, c.TargetElementID); !ok {
		return nil, fmt.Errorf("apply text-tag: no figure or table %q", c.TargetElementID)
	}
	var parts []string
	for i, seg := range p.Segments {
		out, moved, err := wrapSegment(jats, seg, wrapperAttrs{
			correctionID: c.ID, segment: i, label: "caption", movedTo: c.TargetElementID,
		})
		if err != nil {
			return nil, fmt.Errorf("apply text-tag segment %d: %w", i, err)
		}
		jats = out
		parts = append(parts, strings.TrimSpace(moved))
	}
	para := fmt.Sprintf(`<p hive:moved-from="%s">%s</p>`,
		correction.XMLEscape(c.ID), strings.Join(parts, " "))
	return appendToCaption(jats, c.TargetElementID, para)
}

// findFigOrTable returns the byte range of the <fig> or <table-wrap>
// with the given id.
func findFigOrTable(jats []byte, id string) (int, int, bool) {
	re := regexp.MustCompile(`<(fig|table-wrap)\s[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>`)
	loc := re.FindSubmatchIndex(jats)
	if loc == nil {
		return 0, 0, false
	}
	end, ok := matchingCloseEnd(jats, loc[1], string(jats[loc[2]:loc[3]]))
	if !ok {
		return 0, 0, false
	}
	return loc[0], end, true
}

// appendToCaption inserts para as the last child of the element's
// <caption>, creating the caption (after any <label>) when missing.
func appendToCaption(jats []byte, id, para string) ([]byte, error) {
	start, end, ok := findFigOrTable(jats, id)
	if !ok {
		return nil, fmt.Errorf("no figure or table %q", id)
	}
	root, err := parseXMLFragment(jats[start:end])
	if err != nil || len(root.children) != 1 {
		return nil, fmt.Errorf("parse %q: %v", id, err)
	}
	el := root.children[0]
	var caption *xnode
	labelIdx := -1
	for i, ch := range el.children {
		if ch.kind != xElem {
			continue
		}
		switch ch.name {
		case "caption":
			caption = ch
		case "label":
			labelIdx = i
		}
	}
	pn, err := parseXMLFragment([]byte(para))
	if err != nil {
		return nil, fmt.Errorf("moved text: %w", err)
	}
	if caption == nil {
		caption = &xnode{kind: xElem, raw: "<caption>", name: "caption", parent: el}
		at := labelIdx + 1
		el.children = append(el.children[:at], append([]*xnode{caption}, el.children[at:]...)...)
	}
	for _, n := range pn.children {
		caption.append(n)
	}
	var sb strings.Builder
	el.serialize(&sb)
	var out bytes.Buffer
	out.Write(jats[:start])
	out.WriteString(sb.String())
	out.Write(jats[end:])
	return out.Bytes(), nil
}

// restoreWrappers reverses every wrapper written for a correction:
// element wrappers are replaced by the original element, fragment
// wrappers are replaced by their fragment and the inline elements
// split around them are joined again. Text moved into a caption is
// removed from the caption.
func restoreWrappers(jats []byte, correctionID string) ([]byte, error) {
	for {
		openIdx, closeIdx, original, form, ok := findNoiseWrapper(jats, correctionID)
		if !ok {
			break
		}
		switch form {
		case "element":
			// A whole-block wrapper that a merge later moved inside a
			// paragraph comes back as inline content, not a nested
			// block.
			if _, _, inside := enclosingBlock(jats, openIdx, closeIdx); inside {
				if root, err := parseXMLFragment([]byte(original)); err == nil &&
					len(root.children) == 1 && root.children[0].kind == xElem {
					original = serializeNodes(root.children[0].children)
				}
			}
			jats = splice(jats, openIdx, closeIdx, original)
		case "merge":
			// Undo a merge: the joined paragraph is replaced by the
			// original run of blocks it was made from.
			bStart, bEnd, ok := enclosingBlock(jats, openIdx, closeIdx)
			if !ok {
				return nil, fmt.Errorf("merged paragraph not found")
			}
			if n := wrapperTagCount(jats[openIdx:closeIdx]); n > 0 {
				if bEnd, ok = skipSectionTags(jats, bEnd, n); !ok {
					return nil, fmt.Errorf("section tags after merged paragraph changed")
				}
			}
			jats = splice(jats, bStart, bEnd, original)
		case "fragment":
			out, err := restoreFragment(jats, openIdx, closeIdx, original)
			if err != nil {
				return nil, err
			}
			jats = out
		default:
			jats = splice(jats, openIdx, closeIdx, "<p>"+correction.XMLEscape(original)+"</p>")
		}
	}
	movedRe := regexp.MustCompile(`<p hive:moved-from="` + regexp.QuoteMeta(correction.XMLEscape(correctionID)) + `">`)
	for {
		loc := movedRe.FindIndex(jats)
		if loc == nil {
			break
		}
		end, ok := matchingCloseEnd(jats, loc[1], "p")
		if !ok {
			return nil, fmt.Errorf("unterminated moved caption paragraph")
		}
		jats = splice(jats, loc[0], end, "")
	}
	return jats, nil
}

func splice(jats []byte, start, end int, repl string) []byte {
	var out bytes.Buffer
	out.Grow(len(jats) - (end - start) + len(repl))
	out.Write(jats[:start])
	out.WriteString(repl)
	out.Write(jats[end:])
	return out.Bytes()
}

// restoreFragment puts a fragment wrapper's content back inside its
// enclosing block and joins split inline elements at both seams.
func restoreFragment(jats []byte, openIdx, closeIdx int, fragment string) ([]byte, error) {
	bStart, bEnd, ok := enclosingBlock(jats, openIdx, closeIdx)
	if !ok {
		return splice(jats, openIdx, closeIdx, fragment), nil
	}
	root, err := parseXMLFragment(jats[bStart:bEnd])
	if err != nil || len(root.children) != 1 {
		return nil, fmt.Errorf("parse enclosing block: %v", err)
	}
	block := root.children[0]
	frag, err := parseXMLFragment([]byte(fragment))
	if err != nil {
		return nil, fmt.Errorf("parse removed fragment: %w", err)
	}
	wrapperOffset := openIdx - bStart
	idx, pos := -1, len(block.raw)
	for i, ch := range block.children {
		if pos == wrapperOffset && ch.kind == xElem && ch.name == "hive:noise" {
			idx = i
			break
		}
		var sb strings.Builder
		ch.serialize(&sb)
		pos += sb.Len()
	}
	if idx < 0 {
		return splice(jats, openIdx, closeIdx, fragment), nil
	}
	nodes := frag.children
	for _, n := range nodes {
		n.parent = block
	}
	block.children = append(block.children[:idx], append(nodes, block.children[idx+1:]...)...)
	// Join the trailing seam first so the leading seam's index stays
	// valid.
	mergeAt(block, idx+len(nodes))
	mergeAt(block, idx)
	var sb strings.Builder
	block.serialize(&sb)
	return splice(jats, bStart, bEnd, sb.String()), nil
}

// enclosingBlock returns the innermost <p> or <title> containing the
// byte range [from, to).
func enclosingBlock(jats []byte, from, to int) (int, int, bool) {
	bestStart, bestEnd, found := 0, 0, false
	pos := 0
	for {
		absStart, absOpenEnd, name, ok := nextBlockTag(jats, pos, from)
		if !ok {
			break
		}
		pos = absOpenEnd
		end, ok := matchingCloseEnd(jats, absOpenEnd, name)
		if ok && end >= to {
			bestStart, bestEnd, found = absStart, end, true
		}
	}
	return bestStart, bestEnd, found
}

var secOpenRegex = regexp.MustCompile(`^<sec(\s[^>]*)?>`)

// skipSectionTags returns the offset after n section tags (</sec> or
// <sec …>) starting at pos, skipping whitespace between them.
func skipSectionTags(jats []byte, pos, n int) (int, bool) {
	for i := 0; i < n; i++ {
		for pos < len(jats) && strings.ContainsRune(" \t\r\n", rune(jats[pos])) {
			pos++
		}
		switch {
		case bytes.HasPrefix(jats[pos:], []byte("</sec>")):
			pos += len("</sec>")
		case secOpenRegex.Match(jats[pos:]):
			pos += len(secOpenRegex.Find(jats[pos:]))
		default:
			return 0, false
		}
	}
	return pos, true
}

// nextBlockTag finds the next <p> or <title> start tag at or after
// pos and before limit, skipping comments and self-closing tags.
func nextBlockTag(jats []byte, pos, limit int) (start, openEnd int, name string, ok bool) {
	for pos < limit {
		next := blockTagOpenRegex.FindSubmatchIndex(jats[pos:limit])
		if next == nil {
			return 0, 0, "", false
		}
		absStart := pos + next[0]
		absOpenEnd := pos + next[1]
		if c := bytes.Index(jats[pos:absStart], []byte("<!--")); c >= 0 {
			end := bytes.Index(jats[pos+c+4:], []byte("-->"))
			if end < 0 {
				return 0, 0, "", false
			}
			pos = pos + c + 4 + end + 3
			continue
		}
		if bytes.HasSuffix(jats[absStart:absOpenEnd], []byte("/>")) {
			pos = absOpenEnd
			continue
		}
		return absStart, absOpenEnd, string(jats[pos+next[2] : pos+next[3]]), true
	}
	return 0, 0, "", false
}

// applyMergeNext joins paragraphs. With one segment it joins the
// paragraph holding the segment with the next paragraph that has
// text. With several, the first and last segments name the first and
// last paragraphs, and everything from the first through the last
// becomes one paragraph. On the way it absorbs what a paragraph
// broken by layout typically leaves behind: page-break paragraphs
// (kept as inline page-break markers), paragraphs whose text was all
// removed or moved, removal wrappers, and untitled sections that held
// only such content. The original run of blocks is kept in one
// wrapper at the first join, so a single restore undoes the merge.
func applyMergeNext(jats []byte, c *correction.Correction, p correction.TextTagPayload) ([]byte, error) {
	if hasWrapper(jats, c.ID) {
		return jats, nil
	}
	if len(p.Segments) == 0 {
		return nil, fmt.Errorf("apply merge: no paragraph selected")
	}
	b, err := findSegmentBlock(jats, p.Segments[0])
	if err != nil {
		return nil, fmt.Errorf("apply merge: %w", err)
	}
	if b.node.name != "p" {
		return nil, fmt.Errorf("apply merge: only paragraphs can be merged")
	}
	target := ""
	if len(p.Segments) > 1 {
		target = collapseWhitespace(strings.TrimSpace(p.Segments[len(p.Segments)-1].BlockText))
	}
	var out, between strings.Builder
	out.WriteString(b.node.raw)
	out.WriteString(serializeNodes(b.node.children))
	lastText := blockText(b.node)
	joined := false
	// Section tags crossed on the way. GROBID often puts figure text
	// in untitled sections of its own; once that text is removed,
	// the paragraph continues past them.
	var structure []string
	pos := b.end
	for {
		for pos < len(jats) && strings.ContainsRune(" \t\r\n", rune(jats[pos])) {
			pos++
		}
		rest := jats[pos:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			end := bytes.Index(rest, []byte("-->"))
			if end < 0 {
				return nil, fmt.Errorf("apply merge: unterminated comment")
			}
			pos += end + 3
			continue
		case bytes.HasPrefix(rest, []byte("</sec>")):
			structure = append(structure, "</sec>")
			pos += len("</sec>")
			continue
		case secOpenRegex.Match(rest):
			tag := secOpenRegex.Find(rest)
			after := bytes.TrimLeft(rest[len(tag):], " \t\r\n")
			if bytes.HasPrefix(after, []byte("<title")) || bytes.Contains(tag, []byte(`sec-type="figures"`)) {
				return nil, fmt.Errorf("apply merge: the next section has a heading")
			}
			structure = append(structure, string(tag))
			pos += len(tag)
			continue
		case bytes.HasPrefix(rest, []byte("<hive:noise")):
			end := noiseWrapperEnd(jats, pos)
			if end < 0 {
				return nil, fmt.Errorf("apply merge: unterminated wrapper")
			}
			between.Write(jats[pos:end])
			pos = end
			continue
		}
		start, openEnd, name, ok := nextBlockTag(jats, pos, len(jats))
		if !ok || start != pos || name != "p" {
			if target != "" {
				return nil, fmt.Errorf("apply merge: the last selected paragraph was not reached")
			}
			return nil, fmt.Errorf("apply merge: no following paragraph in the same section")
		}
		end, ok := matchingCloseEnd(jats, openEnd, name)
		if !ok {
			return nil, fmt.Errorf("apply merge: unterminated paragraph")
		}
		root, err := parseXMLFragment(jats[start:end])
		if err != nil || len(root.children) != 1 {
			return nil, fmt.Errorf("apply merge: parse following paragraph: %v", err)
		}
		next := root.children[0]
		pos = end
		if bytes.Contains(jats[start:openEnd], []byte(`content-type="hive-page-break"`)) ||
			strings.TrimSpace(string(blockText(next))) == "" {
			between.WriteString(serializeNodes(next.children))
			continue
		}
		// Join with a space unless one is already there or the text
		// so far ends in a hyphen.
		nextText := blockText(next)
		sep := ""
		if len(lastText) > 0 && len(nextText) > 0 &&
			!unicode.IsSpace(lastText[len(lastText)-1]) && lastText[len(lastText)-1] != '-' &&
			!unicode.IsSpace(nextText[0]) {
			sep = " "
		}
		out.WriteString(between.String())
		between.Reset()
		if !joined {
			// Placeholder for the undo wrapper, filled in once the
			// whole run (and its section tag count) is known.
			out.WriteString("\x00merge\x00")
			joined = true
		}
		out.WriteString(sep)
		out.WriteString(serializeNodes(next.children))
		lastText = nextText
		if target != "" && collapseWhitespace(strings.TrimSpace(string(nextText))) != target {
			continue
		}
		w := wrapperAttrs{correctionID: c.ID, label: "merge", tags: len(structure)}
		wrapper := w.startTag("merge") + "<!-- " + escapeComment(string(jats[b.start:end])) + " --></hive:noise>"
		merged := strings.Replace(out.String(), "\x00merge\x00", wrapper, 1) +
			"</p>" + strings.Join(structure, "")
		return splice(jats, b.start, end, merged), nil
	}
}

func wrapperTagCount(wrapper []byte) int {
	gt := bytes.IndexByte(wrapper, '>')
	if gt < 0 {
		return 0
	}
	m := regexp.MustCompile(`\stags="(\d+)"`).FindSubmatch(wrapper[:gt])
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}
