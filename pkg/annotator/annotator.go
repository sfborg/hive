// Package annotator produces TaxPub-augmented XML from GROBID TEI
// documents, wrapping every scientific name GNfinder detects in
// <tp:taxon-name> markup so downstream readers can render chips,
// toggles, and expansions on top of the source text.
//
// This is the first slice of the extraction pipeline documented in
// REFERENCE_PDF_PLAN.md § Reader UI. Later slices add abbreviation
// expansion (via in-paper first-letter heuristic + GNverifier),
// vernacular substitution, and broader NER — each layered on top of
// this pass by adding attributes / nested elements to the same
// <tp:taxon-name> markup.
//
// LIFT-TO-SFLIB CANDIDATE. Every SFBorg tool that ingests scholarly
// papers wants this. Kept self-contained (no reach into hive-
// internal state, no singleton — the caller owns the *Annotator's
// lifetime) so the eventual migration to sflib/pkg/aitoolpack/ is a
// clean copy.
package annotator

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
	"sync"

	gnfinder "github.com/gnames/gnfinder/pkg"
	gnconfig "github.com/gnames/gnfinder/pkg/config"
	"github.com/gnames/gnfinder/pkg/ent/nlp"
	"github.com/gnames/gnfinder/pkg/io/dict"
	vlib "github.com/gnames/gnlib/ent/verifier"
	gnverifier "github.com/gnames/gnverifier/pkg"
	gnverifierconfig "github.com/gnames/gnverifier/pkg/config"
	"github.com/gnames/gnverifier/pkg/io/verifrest"
)

// TaxPubNamespace is the JATS-TaxPub namespace URI. Consumers that
// prefer a different prefix can rewrite it — but the namespace URI
// must stay stable so downstream tools (Plazi, Pensoft) recognize
// the markup as TaxPub.
const TaxPubNamespace = "http://exsansi.pensoft.net/pub-med/pub-med-tp/1.0"

// TaxPubPrefix is the conventional short prefix for the TaxPub
// namespace. Rendered as `xmlns:tp="…"` on the root element so
// every <tp:taxon-name> child in the augmented output resolves.
const TaxPubPrefix = "tp"

// GNNamespace is hive's own namespace for metadata TaxPub doesn't
// cover — expansion source, verifier score, ambiguity candidates.
// Kept under a hive.sfborg URI so downstream tools can filter it
// cleanly if they only want plain TaxPub.
const GNNamespace = "https://hive.sfborg.org/ns/gn/1.0"

// GNPrefix is the short prefix for the GN namespace; rendered as
// `xmlns:gn="…"` on the root of expanded output.
const GNPrefix = "gn"

// Annotator wraps a lazily-initialized GNfinder instance and (on
// first ExpandAbbreviations call) a GNverifier HTTP client.
// FindNames is cheap; ExpandAbbreviations issues one GNverifier
// batch call per document. Callers should keep a single *Annotator
// for the process lifetime.
type Annotator struct {
	initOnce sync.Once
	initErr  error
	gnf      gnfinder.GNfinder

	verifierOnce sync.Once
	verifier     gnverifier.GNverifier
}

// New returns an *Annotator with lazy GNfinder initialization.
// Actual dictionary + Bayes-weight loading happens on the first
// FindNames call; construction itself is cheap.
func New() *Annotator {
	return &Annotator{}
}

func (a *Annotator) ensureReady() error {
	a.initOnce.Do(func() {
		dictionary, err := dict.LoadDictionary()
		if err != nil {
			a.initErr = fmt.Errorf("annotator: load gnfinder dictionary: %w", err)
			return
		}
		weights, err := nlp.BayesWeights()
		if err != nil {
			a.initErr = fmt.Errorf("annotator: load gnfinder bayes weights: %w", err)
			return
		}
		// WithBayes on (default) keeps precision high on natural-
		// language body text. WithPositionInBytes lines up offsets
		// with our byte-oriented XML rewrite loop below — otherwise
		// gnfinder returns UTF-8 rune positions that don't map 1:1
		// against slice indices.
		cfg := gnconfig.New(
			gnconfig.OptWithBayes(true),
			// Byte-oriented offsets align with our slice-based XML
			// rewrite loop; the default rune offsets would need a
			// per-chunk transcoding pass to map back to indices.
			// (Method name is a typo in gnfinder — "Positon".)
			gnconfig.OptWithPositonInBytes(true),
		)
		a.gnf = gnfinder.New(cfg, dictionary, weights)
	})
	return a.initErr
}

// FindNames walks a GROBID TEI document, runs GNfinder over the
// body text (everything inside <text> — the paper's own content),
// and returns a new document with <tp:taxon-name> wrapped around
// every detected scientific name. Content outside <text> (the
// header, sourceDesc bibliography, encoding metadata) is copied
// through unchanged — GNfinder isn't useful on structured metadata,
// and header content already has its own domain-specific parsing
// (see pkg/grobid ParseHeader / ExtractDOI).
//
// Deterministic: same TEI in → same augmented XML out. Runs
// GNfinder per-text-node (not per-document) so cross-node names —
// which don't happen in well-formed XML anyway, since GROBID never
// splits a name across element boundaries — aren't detectable, but
// intra-node names are found and wrapped correctly.
//
// The returned bytes carry the TaxPub namespace declaration on the
// root element. Downstream readers that walk the augmented XML get
// resolvable <tp:taxon-name> elements.
func (a *Annotator) FindNames(tei []byte) ([]byte, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	// Byte-offset-based rewrite. We don't want to re-encode the
	// input via xml.Encoder — Go's encoding/xml mangles namespace
	// declarations when the input carries prefixed attrs, producing
	// duplicate xmlns= attributes and stray _xmlns:X aliasing that
	// browsers reject as malformed XML. Instead: walk the input to
	// identify text-node byte ranges, run GNfinder on each, and
	// splice in <tp:taxon-name> wrappers as raw bytes at the exact
	// insertion positions. Everything outside the insertions is
	// byte-identical to the input.
	var edits []insertion
	// Insert xmlns:tp on the root open tag.
	rootAttrPos, err := rootOpenTagAttrPos(tei)
	if err != nil {
		return nil, err
	}
	edits = append(edits, insertion{
		pos:  rootAttrPos,
		text: ` xmlns:` + TaxPubPrefix + `="` + TaxPubNamespace + `"`,
	})

	dec := xml.NewDecoder(bytes.NewReader(tei))
	textDepth := 0
	var beforeOffset int64
	for {
		beforeOffset = dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("annotator: parse TEI: %w", err)
		}
		afterOffset := dec.InputOffset()
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "text" {
				textDepth++
			}
		case xml.EndElement:
			if t.Name.Local == "text" && textDepth > 0 {
				textDepth--
			}
		case xml.CharData:
			if textDepth <= 0 || !hasWordContent(t) {
				continue
			}
			raw := tei[beforeOffset:afterOffset]
			decoded := string(t)
			// Skip chunks where decoding differs from the raw
			// bytes — entities (&amp; etc.) make GNfinder's
			// plaintext offsets non-mappable to raw positions.
			// Scientific names rarely contain XML-special chars,
			// so the loss is negligible; safety wins.
			if string(raw) != decoded {
				continue
			}
			res := a.gnf.Find("", decoded)
			for _, n := range res.Names {
				if n.OffsetStart < 0 || n.OffsetEnd > len(decoded) ||
					n.OffsetStart >= n.OffsetEnd {
					continue
				}
				startPos := beforeOffset + int64(n.OffsetStart)
				endPos := beforeOffset + int64(n.OffsetEnd)
				edits = append(edits, insertion{
					pos: startPos,
					text: `<` + TaxPubPrefix + `:taxon-name full-name="` +
						escapeAttrValue(n.Name) + `">`,
				})
				edits = append(edits, insertion{
					pos:  endPos,
					text: `</` + TaxPubPrefix + `:taxon-name>`,
				})
			}
		}
	}
	return applyInsertions(tei, edits), nil
}

// insertion is one atomic "insert this text at this byte position"
// operation. applyInsertions sorts by position so end-of-name
// closing tags land after the opening ones even when GNfinder
// returns tightly-packed hits.
type insertion struct {
	pos  int64
	text string
}

// applyInsertions builds a new byte slice by walking the source
// once and interleaving the edits at their positions. Uses
// sort.SliceStable so edits at the same position stay in input
// order (matters for wrapping — the opening and closing tags for
// two adjacent names must interleave correctly).
func applyInsertions(src []byte, edits []insertion) []byte {
	if len(edits) == 0 {
		return src
	}
	all := make([]insertion, len(edits))
	copy(all, edits)
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].pos < all[j].pos
	})
	var buf bytes.Buffer
	buf.Grow(len(src) + 64*len(all))
	var srcPos int64
	for _, e := range all {
		if e.pos > srcPos {
			buf.Write(src[srcPos:e.pos])
			srcPos = e.pos
		}
		buf.WriteString(e.text)
	}
	if srcPos < int64(len(src)) {
		buf.Write(src[srcPos:])
	}
	return buf.Bytes()
}

// rootOpenTagAttrPos returns the byte position inside the root
// element's opening tag where new attributes can be inserted —
// specifically, immediately before the closing `>`. Skips the XML
// declaration and any doctype prologue. Errors when the input has
// no root element open tag.
func rootOpenTagAttrPos(src []byte) (int64, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	for {
		beforeOffset := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return 0, fmt.Errorf("annotator: locate root open tag: %w", err)
		}
		if _, ok := tok.(xml.StartElement); !ok {
			continue
		}
		afterOffset := dec.InputOffset()
		tagBytes := src[beforeOffset:afterOffset]
		// Insert just before the terminating '>' — accounting for
		// '/>' self-closing forms (unusual on a root open tag but
		// safe to handle).
		end := len(tagBytes) - 1
		if end > 0 && tagBytes[end-1] == '/' {
			end--
		}
		return beforeOffset + int64(end), nil
	}
}

// escapeAttrValue XML-escapes a value for insertion into a double-
// quoted attribute. Scientific names don't normally contain
// quotable characters, but a defensive escape keeps output
// well-formed if GNfinder ever emits one.
func escapeAttrValue(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return replacer.Replace(s)
}

// SetVerifier lets tests inject a stub GNverifier so
// ExpandAbbreviations doesn't hit the network. Callers in
// production shouldn't use this — the lazy default hits the public
// verifier endpoint on first ExpandAbbreviations call.
func (a *Annotator) SetVerifier(v gnverifier.GNverifier) {
	a.verifierOnce.Do(func() { a.verifier = v })
}

// Annotate is the full pipeline: FindNames + ExpandAbbreviations.
// Convenience wrapper for callers that want the augmented output
// in one shot; equivalent to calling the two individually.
func (a *Annotator) Annotate(ctx context.Context, tei []byte) ([]byte, error) {
	marked, err := a.FindNames(tei)
	if err != nil {
		return nil, err
	}
	return a.ExpandAbbreviations(ctx, marked)
}

// ensureVerifier lazily constructs the GNverifier HTTP client on
// first ExpandAbbreviations call. Uses the public verifier endpoint;
// no auth or per-request config needed. One client per Annotator so
// connection pooling holds across a document's batch call.
func (a *Annotator) ensureVerifier() {
	a.verifierOnce.Do(func() {
		cfg := gnverifierconfig.New()
		a.verifier = gnverifier.New(cfg, verifrest.New(cfg.VerifierURL))
	})
}

// ExpandAbbreviations rewrites <tp:taxon-name> elements whose
// full-name attribute is an abbreviated binomial ("C. strepitans")
// with a resolved full-form ("Culex strepitans"). The candidate
// genera come from full-form names appearing elsewhere in the same
// document; each candidate is verified against GNverifier's
// authoritative-database matches, and only verified matches are
// promoted.
//
// Attribute conventions on the rewritten element:
//   - full-name — the resolved expansion (was the abbreviated form).
//   - gn:source — "verified" when a single candidate resolved,
//     "ambiguous" when multiple candidates verified, "unverified"
//     when the heuristic proposed an expansion but GNverifier didn't
//     match, "unresolved" when no in-document candidate fit.
//   - gn:score — GNverifier sort score (0-1) for verified matches.
//   - gn:candidates — comma-separated alternatives when ambiguous.
//
// Deterministic per input + GNverifier's current state; repeat calls
// with the same corpus give the same output as long as GNverifier
// hasn't been updated.
func (a *Annotator) ExpandAbbreviations(ctx context.Context, augmented []byte) ([]byte, error) {
	fullGenera, abbrevs, err := collectNames(augmented)
	if err != nil {
		return nil, err
	}
	if len(abbrevs) == 0 {
		return augmented, nil
	}
	candidatesByAbbrev := make(map[string][]string, len(abbrevs))
	var toVerify []string
	seen := make(map[string]bool)
	for abbrev := range abbrevs {
		cands := expansionCandidates(abbrev, fullGenera)
		candidatesByAbbrev[abbrev] = cands
		for _, c := range cands {
			if !seen[c] {
				seen[c] = true
				toVerify = append(toVerify, c)
			}
		}
	}
	verifyResults := make(map[string]*vlib.Name)
	if len(toVerify) > 0 {
		a.ensureVerifier()
		batch := a.verifier.VerifyBatch(ctx, toVerify)
		for i := range batch {
			verifyResults[toVerify[i]] = &batch[i]
		}
	}
	return rewriteAbbreviations(augmented, candidatesByAbbrev, verifyResults)
}

// collectNames does a single pass over the augmented XML gathering
// two things: the set of candidate full-form genera and the set of
// abbreviated full-name attribute values that need expansion.
//
// Candidate genera come from any full-form (non-abbreviated) name
// GNfinder detected — binomials contribute their first token, and
// uninomials contribute themselves as-is. Including uninomials
// matters for papers that introduce a genus without an accompanying
// species ("the genus Coccus") before switching to abbreviated
// binomials ("C. bus", "C. dus"). GNverifier filters out non-genera
// (family / suborder / higher-taxon uninomials) at candidate-
// verification time by simply not resolving "Coccidae bus" as a
// valid binomial, so false positives cost nothing.
func collectNames(augmented []byte) (fullGenera map[string]bool, abbrevs map[string]bool, err error) {
	fullGenera = make(map[string]bool)
	abbrevs = make(map[string]bool)
	dec := xml.NewDecoder(bytes.NewReader(augmented))
	for {
		tok, tokErr := dec.Token()
		if tokErr != nil {
			if tokErr.Error() == "EOF" {
				break
			}
			return nil, nil, fmt.Errorf("annotator: parse augmented XML: %w", tokErr)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "taxon-name" {
			continue
		}
		var fullName string
		for _, attr := range se.Attr {
			if attr.Name.Local == "full-name" {
				fullName = attr.Value
				break
			}
		}
		if fullName == "" {
			continue
		}
		parts := strings.SplitN(fullName, " ", 2)
		firstToken := parts[0]
		if strings.HasSuffix(firstToken, ".") {
			if len(parts) > 1 {
				abbrevs[fullName] = true
			}
			continue
		}
		if len(parts) >= 2 {
			// Binomial+: first token is a full-form genus.
			fullGenera[firstToken] = true
		} else if firstToken != "" {
			// Uninomial: use as-is. Might be a genus, family, or
			// higher taxon — GNverifier's candidate-verification
			// pass filters out non-genera because family+ names
			// don't combine with a specific epithet to make a
			// valid binomial.
			fullGenera[firstToken] = true
		}
	}
	return fullGenera, abbrevs, nil
}

// expansionCandidates returns possible full-form binomials by
// combining each in-document genus whose leading letters match the
// abbreviation prefix with the abbreviated name's remainder.
// "C. strepitans" against {Culex, Culicoides, Chionaspis} yields
// {"Culex strepitans", "Culicoides strepitans", "Chionaspis
// strepitans"}. GNverifier decides which (if any) is a real name.
func expansionCandidates(abbrev string, fullGenera map[string]bool) []string {
	parts := strings.SplitN(abbrev, " ", 2)
	if len(parts) < 2 {
		return nil
	}
	prefix := strings.TrimSuffix(parts[0], ".")
	rest := strings.TrimSpace(parts[1])
	if prefix == "" || rest == "" {
		return nil
	}
	var out []string
	for genus := range fullGenera {
		if strings.HasPrefix(genus, prefix) {
			out = append(out, genus+" "+rest)
		}
	}
	return out
}

// rewriteAbbreviations walks the augmented XML, records the byte
// ranges of each <tp:taxon-name> opening tag, and rewrites those
// tags in place with expanded full-name + gn:source/score/candidates
// attributes. Byte-based (not xml.Encoder-based) for the same
// reason FindNames is: encoding/xml re-serialization mangles
// namespace declarations on complex inputs. Everything outside the
// rewritten tags is preserved byte-perfectly.
func rewriteAbbreviations(
	augmented []byte,
	candidates map[string][]string,
	verifyResults map[string]*vlib.Name,
) ([]byte, error) {
	// Insert xmlns:gn on the root open tag so gn:source and friends
	// resolve.
	rootAttrPos, err := rootOpenTagAttrPos(augmented)
	if err != nil {
		return nil, err
	}
	edits := []insertion{{
		pos:  rootAttrPos,
		text: ` xmlns:` + GNPrefix + `="` + GNNamespace + `"`,
	}}
	// Replacements: for each <tp:taxon-name ...> opening tag we
	// need to modify, we record the byte range of the whole opening
	// tag plus the replacement text. Applied as a delete + insert
	// via the same insertion machinery (an edit at pos deletes
	// [pos, pos+delLen) and inserts the replacement text).
	var reps []replacement
	dec := xml.NewDecoder(bytes.NewReader(augmented))
	var beforeOffset int64
	for {
		beforeOffset = dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("annotator: parse augmented XML: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "taxon-name" {
			continue
		}
		afterOffset := dec.InputOffset()
		newTag, changed := rewriteTaxonNameTag(se, candidates, verifyResults)
		if !changed {
			continue
		}
		reps = append(reps, replacement{
			start:   beforeOffset,
			end:     afterOffset,
			replace: newTag,
		})
	}
	return applyReplacementsAndInsertions(augmented, edits, reps), nil
}

// replacement is a [start, end) byte range that should be replaced
// with the given text. Used by ExpandAbbreviations to rewrite
// <tp:taxon-name> opening tags whose attributes we've resolved.
type replacement struct {
	start, end int64
	replace    string
}

// applyReplacementsAndInsertions combines both edit types into a
// single byte-slice reconstruction pass. Replacements and insertions
// are ordered by position; conflicts (an insertion inside a
// replacement range) resolve by dropping the insertion since the
// replacement supersedes it.
func applyReplacementsAndInsertions(
	src []byte,
	edits []insertion,
	reps []replacement,
) []byte {
	// Convert replacements into a form comparable to insertions
	// while remembering the delete range. Sort both by start pos.
	sort.SliceStable(reps, func(i, j int) bool {
		return reps[i].start < reps[j].start
	})
	sort.SliceStable(edits, func(i, j int) bool {
		return edits[i].pos < edits[j].pos
	})
	var buf bytes.Buffer
	buf.Grow(len(src) + 64*(len(edits)+len(reps)))
	var srcPos int64
	ei, ri := 0, 0
	for srcPos < int64(len(src)) {
		// Which comes next — an insertion at srcPos or a
		// replacement starting at srcPos?
		nextIns := int64(-1)
		if ei < len(edits) {
			nextIns = edits[ei].pos
		}
		nextRep := int64(-1)
		if ri < len(reps) {
			nextRep = reps[ri].start
		}
		if nextIns < 0 && nextRep < 0 {
			buf.Write(src[srcPos:])
			break
		}
		// Determine the next event position.
		var next int64
		switch {
		case nextIns < 0:
			next = nextRep
		case nextRep < 0:
			next = nextIns
		case nextIns <= nextRep:
			next = nextIns
		default:
			next = nextRep
		}
		if next > srcPos {
			buf.Write(src[srcPos:next])
			srcPos = next
		}
		// Emit any insertions AT this exact position first (so an
		// insertion at the start of a replacement's range lands
		// before the replacement text), then the replacement.
		for ei < len(edits) && edits[ei].pos == srcPos {
			buf.WriteString(edits[ei].text)
			ei++
		}
		if ri < len(reps) && reps[ri].start == srcPos {
			buf.WriteString(reps[ri].replace)
			srcPos = reps[ri].end
			// Skip any insertions that fell inside the replaced
			// range — the replacement supersedes them.
			for ei < len(edits) && edits[ei].pos < srcPos {
				ei++
			}
			ri++
		}
	}
	// Trailing edits past end-of-src (shouldn't happen, but write
	// them anyway rather than silently dropping).
	for ei < len(edits) {
		buf.WriteString(edits[ei].text)
		ei++
	}
	return buf.Bytes()
}

// rewriteTaxonNameTag decides whether a <tp:taxon-name> opening
// tag needs updating based on its full-name attribute (is it an
// abbreviation? what did verification return?) and returns the
// replacement tag text. changed=false means the tag doesn't need
// rewriting (full-form name, or an abbreviation with no in-doc
// candidates that we already annotated).
func rewriteTaxonNameTag(
	se xml.StartElement,
	candidates map[string][]string,
	verifyResults map[string]*vlib.Name,
) (string, bool) {
	var fullName string
	for _, attr := range se.Attr {
		if attr.Name.Local == "full-name" {
			fullName = attr.Value
			break
		}
	}
	if fullName == "" {
		return "", false
	}
	parts := strings.SplitN(fullName, " ", 2)
	if len(parts) < 2 || !strings.HasSuffix(parts[0], ".") {
		return "", false
	}
	newFullName := fullName
	var extra []xml.Attr
	cands := candidates[fullName]
	if len(cands) == 0 {
		extra = append(extra, xml.Attr{
			Name:  xml.Name{Local: GNPrefix + ":source"},
			Value: "unresolved",
		})
	} else {
		type verified struct {
			name  string
			score float64
		}
		var vs []verified
		for _, c := range cands {
			r := verifyResults[c]
			if r == nil || r.MatchType == vlib.NoMatch || r.BestResult == nil {
				continue
			}
			vs = append(vs, verified{name: c, score: r.BestResult.SortScore})
		}
		switch len(vs) {
		case 0:
			extra = append(extra, xml.Attr{
				Name:  xml.Name{Local: GNPrefix + ":source"},
				Value: "unverified",
			})
		case 1:
			newFullName = vs[0].name
			extra = append(extra,
				xml.Attr{
					Name:  xml.Name{Local: GNPrefix + ":source"},
					Value: "verified",
				},
				xml.Attr{
					Name:  xml.Name{Local: GNPrefix + ":score"},
					Value: formatScore(vs[0].score),
				},
			)
		default:
			names := make([]string, len(vs))
			for i, v := range vs {
				names[i] = v.name
			}
			extra = append(extra,
				xml.Attr{
					Name:  xml.Name{Local: GNPrefix + ":source"},
					Value: "ambiguous",
				},
				xml.Attr{
					Name:  xml.Name{Local: GNPrefix + ":candidates"},
					Value: strings.Join(names, ", "),
				},
			)
		}
	}
	// Rebuild the opening tag from scratch. Preserves the original
	// prefix (tp:) and keeps only the full-name + our new
	// attributes — other attributes on a <tp:taxon-name> from
	// FindNames don't exist, so this is exhaustive.
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(TaxPubPrefix)
	b.WriteString(":taxon-name full-name=\"")
	b.WriteString(escapeAttrValue(newFullName))
	b.WriteByte('"')
	for _, a := range extra {
		b.WriteByte(' ')
		b.WriteString(a.Name.Local)
		b.WriteString("=\"")
		b.WriteString(escapeAttrValue(a.Value))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	return b.String(), true
}

func formatScore(s float64) string {
	return fmt.Sprintf("%.3f", s)
}

// hasWordContent reports whether a char-data chunk contains any
// letter — cheap guard so we don't spin up GNfinder for whitespace-
// only tokens (which are ~half the tokens in a typical TEI document).
func hasWordContent(cd xml.CharData) bool {
	for _, r := range string(cd) {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return true
		}
	}
	return false
}

