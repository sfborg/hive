// Package annotator produces TaxPub-augmented XML from JATS
// documents, wrapping every scientific name GNfinder detects in
// <tp:taxon-name> markup so downstream readers can render chips,
// toggles, and expansions on top of the source text.
//
// The upstream pipeline converts GROBID's TEI output into JATS via
// pkg/jatsconv before invoking this annotator — see
// JATS_MIGRATION_PLAN.md for the migration context.
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

// FindNames walks a JATS document, runs GNfinder over the body
// text (everything inside <body> — the paper's own prose), and
// returns a new document with <tp:taxon-name> wrapped around every
// detected scientific name. Content outside <body> (the <front>
// article-meta and <back> ref-list) is copied through unchanged —
// GNfinder isn't useful on structured metadata, and header content
// already has its own domain-specific parsing (see pkg/grobid
// ParseHeader / ExtractDOI).
//
// Deterministic: same JATS in → same augmented XML out. Runs
// GNfinder per-text-node (not per-document) so cross-node names —
// which don't happen in well-formed XML anyway — aren't detectable,
// but intra-node names are found and wrapped correctly.
//
// The returned bytes carry the TaxPub namespace declaration on the
// root element. Downstream readers that walk the augmented XML get
// resolvable <tp:taxon-name> elements.
func (a *Annotator) FindNames(jats []byte) ([]byte, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	// Byte-offset-based rewrite. We don't want to re-encode the
	// input via xml.Encoder — Go's encoding/xml does not round-trip
	// namespace declarations when the input carries prefixed attrs, producing
	// duplicate xmlns= attributes and stray _xmlns:X aliasing that
	// browsers reject as malformed XML. Instead: walk the input to
	// identify text-node byte ranges, run GNfinder on each, and
	// splice in <tp:taxon-name> wrappers as raw bytes at the exact
	// insertion positions. Everything outside the insertions is
	// byte-identical to the input.
	var edits []insertion
	// Insert xmlns:tp on the root open tag when the input does not
	// already bind the tp prefix. Publisher JATS often ships with
	// its own tp binding (plazi.org/taxpub) — matching by prefix
	// (not URI) avoids emitting a second xmlns:tp with our URI
	// and yielding a duplicate-attribute XML parse error. Our
	// <tp:taxon-name> insertions inherit whichever URI the file
	// already declares.
	if !hasNamespacePrefixBinding(jats, TaxPubPrefix) {
		rootAttrPos, err := rootOpenTagAttrPos(jats)
		if err != nil {
			return nil, err
		}
		edits = append(edits, insertion{
			pos:  rootAttrPos,
			text: ` xmlns:` + TaxPubPrefix + `="` + TaxPubNamespace + `"`,
		})
	}

	dec := xml.NewDecoder(bytes.NewReader(jats))
	bodyDepth := 0
	var beforeOffset int64
	for {
		beforeOffset = dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("annotator: parse JATS: %w", err)
		}
		afterOffset := dec.InputOffset()
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "body" {
				bodyDepth++
			}
		case xml.EndElement:
			if t.Name.Local == "body" && bodyDepth > 0 {
				bodyDepth--
			}
		case xml.CharData:
			if bodyDepth <= 0 || !hasWordContent(t) {
				continue
			}
			raw := jats[beforeOffset:afterOffset]
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
				if isLabelWord(n.Name) {
					// Skip common label words that also happen to
					// exist as real genus names in CoL. See
					// isLabelWord for the rationale and list.
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
	return applyInsertions(jats, edits), nil
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

// hasNamespacePrefixBinding reports whether the input already
// declares an xmlns binding for the given prefix on the root
// element — matched by prefix rather than URI so a publisher-
// supplied JATS that binds "tp" to plazi.org/taxpub is treated
// as already-declared and we do not emit a second xmlns:tp with
// our own URI (a duplicate-attribute XML parse error). Only the
// root open tag is scanned; deeper occurrences don't matter for
// our root-tag insertions.
func hasNamespacePrefixBinding(src []byte, prefix string) bool {
	dec := xml.NewDecoder(bytes.NewReader(src))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		for _, a := range se.Attr {
			if a.Name.Space == "xmlns" && a.Name.Local == prefix {
				return true
			}
		}
		return false
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

// Annotate is the full pipeline: FindNames + VerifyFullNames +
// ExpandAbbreviations. Convenience wrapper for callers that want
// the augmented output in one shot; equivalent to calling the
// three passes individually.
func (a *Annotator) Annotate(ctx context.Context, jats []byte) ([]byte, error) {
	marked, err := a.FindNames(jats)
	if err != nil {
		return nil, err
	}
	verified, err := a.VerifyFullNames(ctx, marked)
	if err != nil {
		return nil, err
	}
	return a.ExpandAbbreviations(ctx, verified)
}

// VerifyFullNames batch-verifies every full-form <tp:taxon-name>
// chip in the input against GNverifier and stamps the outcome as
// gn:source (verified / unverified) plus gn:score for verified
// matches. Abbreviated chips (full-name attribute contains a
// letter-then-period token such as "C. strepitans") are left
// untouched — those go through ExpandAbbreviations, which does
// its own candidate-based verification.
//
// One GNverifier VerifyBatch call per document. Deduplicates
// candidate names before sending so a paper that mentions
// Panthera onca fifty times still costs one row in the batch.
//
// Attribute semantics on the rewritten chip:
//   - gn:source = "verified"   — GNverifier returned a full-name
//     match (Exact / Fuzzy / SpeciesGroup / Virus). Includes score.
//   - gn:source = "unverified" — GNverifier returned NoMatch or a
//     Partial* match (only genus verified, species didn't). The
//     chip stays but the reader can style it as "GNfinder said so
//     but no authority backs it up".
//
// Idempotent: chips that already carry gn:source keep their
// existing value (ExpandAbbreviations may have added one; that
// pass runs after VerifyFullNames, but a defensive check keeps
// the pipeline order flexible).
func (a *Annotator) VerifyFullNames(ctx context.Context, augmented []byte) ([]byte, error) {
	fullNames, err := collectFullNames(augmented)
	if err != nil {
		return nil, err
	}
	if len(fullNames) == 0 {
		return augmented, nil
	}
	a.ensureVerifier()
	batch := a.verifier.VerifyBatch(ctx, fullNames)
	verifyResults := make(map[string]*vlib.Name, len(fullNames))
	for i := range batch {
		verifyResults[fullNames[i]] = &batch[i]
	}
	return rewriteFullNames(augmented, verifyResults)
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
		var fullName, source string
		for _, attr := range se.Attr {
			switch attr.Name.Local {
			case "full-name":
				fullName = attr.Value
			case "source":
				source = attr.Value
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
		// Only accept genera from chips that verified against
		// GNverifier. VerifyFullNames runs before ExpandAbbrev-
		// iations and stamps every full-form chip with
		// gn:source={verified,unverified}. Filtering here prevents
		// GNfinder false positives (capitalized-word sequences
		// that look name-shaped but aren't actual taxa) from
		// polluting the abbreviation-candidate pool.
		//
		// Chips with no gn:source are older/legacy — accept them
		// as a fallback so pipelines that skip VerifyFullNames
		// still work.
		if source != "" && source != "verified" {
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

// collectFullNames walks the augmented XML and returns the
// deduplicated set of full-name attribute values on <tp:taxon-name>
// chips that aren't abbreviations. Fed to VerifyFullNames' batch
// call so we hit GNverifier once per document with one row per
// unique candidate name.
func collectFullNames(augmented []byte) ([]string, error) {
	seen := make(map[string]bool)
	dec := xml.NewDecoder(bytes.NewReader(augmented))
	for {
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
		var fullName string
		alreadyMarked := false
		for _, attr := range se.Attr {
			switch attr.Name.Local {
			case "full-name":
				fullName = attr.Value
			case "source":
				// gn:source already set — likely by an earlier
				// verification pass (or a hand-edited chip).
				// Skip re-verifying so we don't paper over
				// curator-supplied metadata.
				alreadyMarked = true
			}
		}
		if alreadyMarked || fullName == "" {
			continue
		}
		parts := strings.SplitN(fullName, " ", 2)
		if strings.HasSuffix(parts[0], ".") {
			// Abbreviated — ExpandAbbreviations handles this.
			continue
		}
		if seen[fullName] {
			continue
		}
		seen[fullName] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// rewriteFullNames walks the augmented XML and, for each full-form
// <tp:taxon-name> chip:
//
//   - If GNverifier confirmed the name (Exact / Fuzzy / SpeciesGroup
//     / Virus match type), stamp gn:source="verified" and gn:score
//     on the opening tag.
//   - If GNverifier can't confirm it, UNWRAP the chip — delete the
//     open and close tp:taxon-name tags, leaving the inner text as
//     plain body prose. GNfinder uses patterns (italics, capital-
//     ization) that produce false positives, and asserting a chip
//     the authoritative DB can't confirm would put wrong metadata
//     into the JATS that downstream tools would take at face value.
//
// Abbreviated chips (e.g. "C. strepitans") and chips already
// carrying gn:source are left alone — ExpandAbbreviations handles
// abbreviations, and any pre-stamped chip is treated as
// authoritative (a curator or earlier pass supplied the metadata).
func rewriteFullNames(
	augmented []byte,
	verifyResults map[string]*vlib.Name,
) ([]byte, error) {
	var edits []insertion
	if !hasNamespacePrefixBinding(augmented, GNPrefix) {
		rootAttrPos, err := rootOpenTagAttrPos(augmented)
		if err != nil {
			return nil, err
		}
		edits = append(edits, insertion{
			pos:  rootAttrPos,
			text: ` xmlns:` + GNPrefix + `="` + GNNamespace + `"`,
		})
	}
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
		afterOpenOffset := dec.InputOffset()
		action, verifiedTag := planFullNameAction(se, verifyResults)
		switch action {
		case fullNameKeep:
			continue
		case fullNameStamp:
			// Rewrite the opening tag in place with the verified
			// attributes.
			reps = append(reps, replacement{
				start:   beforeOffset,
				end:     afterOpenOffset,
				replace: verifiedTag,
			})
		case fullNameUnwrap:
			// Delete the opening tag now, then find and delete the
			// matching closing tag. Uses depth tracking so nested
			// tp:taxon-name chips (biologically legitimate — see
			// jatsconv nesting rationale) close-match correctly.
			reps = append(reps, replacement{
				start:   beforeOffset,
				end:     afterOpenOffset,
				replace: "",
			})
			closeStart, closeEnd, err := findMatchingClose(dec)
			if err != nil {
				return nil, err
			}
			reps = append(reps, replacement{
				start:   closeStart,
				end:     closeEnd,
				replace: "",
			})
		}
	}
	return applyReplacementsAndInsertions(augmented, edits, reps), nil
}

// fullNameAction is what rewriteFullNames should do with a
// full-form chip based on verification outcome.
type fullNameAction int

const (
	fullNameKeep   fullNameAction = iota // leave untouched (already stamped, or abbreviated)
	fullNameStamp                        // rewrite open tag with gn:source="verified" + gn:score
	fullNameUnwrap                       // delete open + close tags, keep inner text
)

// planFullNameAction inspects a <tp:taxon-name> opening tag and
// decides what rewriteFullNames should do with the chip. Returns
// the verified opening-tag replacement text only when the action
// is fullNameStamp.
func planFullNameAction(
	se xml.StartElement,
	verifyResults map[string]*vlib.Name,
) (fullNameAction, string) {
	var fullName string
	for _, attr := range se.Attr {
		switch attr.Name.Local {
		case "full-name":
			fullName = attr.Value
		case "source":
			// Already stamped — trust the earlier pass.
			return fullNameKeep, ""
		}
	}
	if fullName == "" {
		return fullNameKeep, ""
	}
	parts := strings.SplitN(fullName, " ", 2)
	if strings.HasSuffix(parts[0], ".") {
		// Abbreviated — ExpandAbbreviations owns this case.
		return fullNameKeep, ""
	}
	r := verifyResults[fullName]
	if r == nil || r.BestResult == nil || !isFullNameMatch(r.MatchType) {
		return fullNameUnwrap, ""
	}
	// Rebuild the opening tag with the verified metadata.
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(TaxPubPrefix)
	b.WriteString(":taxon-name full-name=\"")
	b.WriteString(escapeAttrValue(fullName))
	b.WriteString(`" `)
	b.WriteString(GNPrefix)
	b.WriteString(`:source="verified" `)
	b.WriteString(GNPrefix)
	b.WriteString(`:score="`)
	b.WriteString(escapeAttrValue(formatScore(r.BestResult.SortScore)))
	b.WriteString(`">`)
	return fullNameStamp, b.String()
}

// findMatchingClose consumes tokens from the decoder until it
// hits the closing tag that balances an already-open
// <tp:taxon-name>. Returns the byte range of that closing tag
// (start inclusive, end exclusive). Uses depth tracking so nested
// tp:taxon-name inside the target don't confuse the balance.
func findMatchingClose(dec *xml.Decoder) (int64, int64, error) {
	depth := 1
	for {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, fmt.Errorf("annotator: find matching close: %w", err)
		}
		end := dec.InputOffset()
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "taxon-name" {
				depth++
			}
		case xml.EndElement:
			if t.Name.Local == "taxon-name" {
				depth--
				if depth == 0 {
					return start, end, nil
				}
			}
		}
	}
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
// reason FindNames is: encoding/xml re-serialization does not
// preserve namespace declarations on complex inputs. Everything outside the
// rewritten tags is preserved byte-perfectly.
func rewriteAbbreviations(
	augmented []byte,
	candidates map[string][]string,
	verifyResults map[string]*vlib.Name,
) ([]byte, error) {
	// Insert xmlns:gn on the root open tag so gn:source and friends
	// resolve — but skip when the input already declares the
	// namespace (jatsconv emits both tp: and gn: preemptively so
	// annotator output stays byte-stable across annotated and
	// un-annotated inputs).
	var edits []insertion
	if !hasNamespacePrefixBinding(augmented, GNPrefix) {
		rootAttrPos, err := rootOpenTagAttrPos(augmented)
		if err != nil {
			return nil, err
		}
		edits = append(edits, insertion{
			pos:  rootAttrPos,
			text: ` xmlns:` + GNPrefix + `="` + GNNamespace + `"`,
		})
	}
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
		// Dedupe candidates by resolved-taxon identity: when two
		// candidate strings both verify but point at the SAME
		// underlying record in CoL, they aren't really ambiguous.
		// Common causes: (a) the paper spells the genus two ways
		// (e.g., "Paecilomyces" and its typo "Paecelomyces") and
		// both verify — one Exact, one Fuzzy — against the same
		// CoL entry; (b) the paper uses a genus + its synonym and
		// both resolve to the same currently-accepted name.
		//
		// Key preference: CurrentNameID (accepted-taxon UUID, handles
		// synonymy) → MatchedNameID (matched-record UUID, handles
		// spelling variants of the same record) → MatchedCanonicalFull
		// (canonical string, defensive fallback for records missing
		// UUIDs). Keep the highest-scoring representative per cluster
		// so Exact matches beat Fuzzy matches when both resolve to
		// the same taxon.
		byTaxon := make(map[string]verified)
		for _, c := range cands {
			r := verifyResults[c]
			if r == nil || r.BestResult == nil || !isFullNameMatch(r.MatchType) {
				continue
			}
			key := candidateTaxonKey(r.BestResult)
			cand := verified{name: c, score: r.BestResult.SortScore}
			if existing, found := byTaxon[key]; !found || cand.score > existing.score {
				byTaxon[key] = cand
			}
		}
		vs := make([]verified, 0, len(byTaxon))
		for _, v := range byTaxon {
			vs = append(vs, v)
		}
		// Order matters for reproducible output — sort by descending
		// score so the top match is always first (matters for the
		// gn:candidates attribute rendering when we end up ambiguous).
		sort.SliceStable(vs, func(i, j int) bool {
			return vs[i].score > vs[j].score
		})
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

// candidateTaxonKey derives a dedup key for a GNverifier result
// row. Two candidate name-strings that share this key represent
// the same underlying taxon — either because they matched the same
// record in the data source (spelling variants), or because they
// resolve to the same accepted name via synonymy.
//
// Preference order:
//   - CurrentNameID: the accepted taxon's UUID after synonymy is
//     resolved. Two names that are synonyms of the same accepted
//     name share this key; ideal for dedup.
//   - MatchedNameID: the matched record's UUID. Falls back to
//     this when the data source doesn't track a current name (some
//     legacy DBs). Two spellings that hit the same record share
//     the UUID.
//   - MatchedCanonicalFull: canonical string form. Last-resort
//     fallback when both UUIDs are empty (shouldn't happen for
//     CoL-backed results but defends against future data sources
//     with sparser metadata).
func candidateTaxonKey(r *vlib.ResultData) string {
	if r.CurrentNameID != "" {
		return "current:" + r.CurrentNameID
	}
	if r.MatchedNameID != "" {
		return "matched:" + r.MatchedNameID
	}
	return "canonical:" + r.MatchedCanonicalFull
}

// isFullNameMatch reports whether the given GNverifier match type
// represents a match against the FULL input name-string. Partial
// matches (PartialExact / PartialFuzzy / PartialFuzzyRelaxed) mean
// GNverifier fell back to matching a shorter form — for a binomial
// input like "Bacillus bassiana" that produces a Partial* match,
// only the genus "Bacillus" verified, and the species "bassiana"
// doesn't exist in that genus. Treating such matches as verified
// is what let phantom binomials into the candidate-expansion set,
// pushing legitimate resolutions toward "ambiguous".
//
// Accepted match types: Exact / ExactSpeciesGroup / Fuzzy /
// FuzzyRelaxed / FuzzySpeciesGroup / FuzzySpeciesGroupRelaxed /
// Virus. Everything else fails the check.
func isFullNameMatch(mt vlib.MatchTypeValue) bool {
	switch mt {
	case vlib.Exact,
		vlib.ExactSpeciesGroup,
		vlib.Fuzzy,
		vlib.FuzzyRelaxed,
		vlib.FuzzySpeciesGroup,
		vlib.FuzzySpeciesGroupRelaxed,
		vlib.Virus:
		return true
	}
	return false
}

// labelWordSet holds single-word GNfinder hits that also happen
// to be real (obscure) genus names in CoL, but in scientific-
// paper text virtually always appear as figure/table label words
// in some language rather than as taxonomic references.
//
// Examples of what we filter and why:
//   - Figura: Spanish/Portuguese/Italian for "Figure" — GNfinder
//     picks it up in bilingual captions ("Figura 1. Dinámica..."),
//     GNverifier confirms it as Figura Piesch (1927, a nematode
//     genus). Real taxonomic references to that genus would be
//     "Figura Piesch" or "Figura sp." — never bare "Figura".
//   - Same rationale for other label words we've seen tokenized.
//
// A real Figura-genus mention (rare) would be missed by this
// filter — accepted tradeoff. Scientific-paper false-positive
// rate on these words is essentially 100%; true-positive rate is
// vanishingly small.
var labelWordSet = map[string]bool{
	"Figure": true, "Figura": true, "Fig": true,
	"Table": true, "Tabla": true, "Tab": true,
	"Abbildung": true, // German for "Figure"
	"Tabelle":   true, // German for "Table"
	"Tableau":   true, // French for "Table"
}

// isLabelWord reports whether a GNfinder name is a single-word
// hit from the labelWordSet — a common non-taxonomic word that
// GNverifier happens to have as a valid genus.
func isLabelWord(name string) bool {
	// Only single-word hits qualify. A binomial containing one of
	// these words (e.g. "Figura piesch"; unlikely but well-formed)
	// falls through to the normal verification pipeline.
	if strings.ContainsRune(name, ' ') {
		return false
	}
	return labelWordSet[name]
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

