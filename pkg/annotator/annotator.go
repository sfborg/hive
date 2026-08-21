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
	dec := xml.NewDecoder(bytes.NewReader(tei))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)

	// depth inside <text>. Only body-text content should be scanned
	// for names — header content is separately parsed by pkg/grobid.
	textDepth := 0
	rootSeen := false

	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("annotator: parse TEI: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// On the root element, add the TaxPub namespace
			// declaration so downstream <tp:taxon-name> elements
			// resolve. If the caller's TEI already declared it,
			// the duplicate is harmless — xml.Encoder emits it once.
			if !rootSeen {
				t.Attr = append(t.Attr, xml.Attr{
					Name:  xml.Name{Local: "xmlns:" + TaxPubPrefix},
					Value: TaxPubNamespace,
				})
				rootSeen = true
			}
			if t.Name.Local == "text" {
				textDepth++
			} else if textDepth > 0 {
				// nothing special — still inside text subtree
			}
			if err := enc.EncodeToken(t); err != nil {
				return nil, fmt.Errorf("annotator: encode start: %w", err)
			}
		case xml.EndElement:
			if t.Name.Local == "text" && textDepth > 0 {
				textDepth--
			}
			if err := enc.EncodeToken(t); err != nil {
				return nil, fmt.Errorf("annotator: encode end: %w", err)
			}
		case xml.CharData:
			if textDepth > 0 && hasWordContent(t) {
				if err := a.emitAnnotated(enc, t); err != nil {
					return nil, err
				}
			} else {
				if err := enc.EncodeToken(t); err != nil {
					return nil, fmt.Errorf("annotator: encode chardata: %w", err)
				}
			}
		default:
			if err := enc.EncodeToken(t); err != nil {
				return nil, fmt.Errorf("annotator: encode token: %w", err)
			}
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("annotator: flush: %w", err)
	}
	return out.Bytes(), nil
}

// emitAnnotated runs GNfinder over the char-data chunk and emits a
// sequence of tokens: text-before-name / <tp:taxon-name>name</tp:taxon-name> /
// text-between-names / ... / text-after-last-name. Names are emitted
// in offset order (GNfinder returns them that way).
//
// When GNfinder returns no names, the chunk is passed through as-is
// — no attribute-free wrapping so the output stays byte-identical
// where possible.
func (a *Annotator) emitAnnotated(enc *xml.Encoder, chunk xml.CharData) error {
	text := string(chunk)
	res := a.gnf.Find("", text)
	if len(res.Names) == 0 {
		return enc.EncodeToken(chunk)
	}
	pos := 0
	tpName := xml.Name{Space: TaxPubNamespace, Local: "taxon-name"}
	for _, n := range res.Names {
		if n.OffsetStart < pos || n.OffsetEnd > len(text) {
			// Defensive: skip a finding whose offsets are outside
			// what we can express. Shouldn't happen with
			// WithPositionInBytes, but let it fall through as
			// plain text rather than corrupt the output.
			continue
		}
		if n.OffsetStart > pos {
			if err := enc.EncodeToken(xml.CharData(text[pos:n.OffsetStart])); err != nil {
				return fmt.Errorf("annotator: encode pre-name text: %w", err)
			}
		}
		start := xml.StartElement{
			Name: tpName,
			Attr: []xml.Attr{{
				Name:  xml.Name{Local: "full-name"},
				Value: n.Name,
			}},
		}
		if err := enc.EncodeToken(start); err != nil {
			return fmt.Errorf("annotator: encode taxon-name start: %w", err)
		}
		if err := enc.EncodeToken(xml.CharData(text[n.OffsetStart:n.OffsetEnd])); err != nil {
			return fmt.Errorf("annotator: encode taxon-name text: %w", err)
		}
		if err := enc.EncodeToken(xml.EndElement{Name: tpName}); err != nil {
			return fmt.Errorf("annotator: encode taxon-name end: %w", err)
		}
		pos = n.OffsetEnd
	}
	if pos < len(text) {
		if err := enc.EncodeToken(xml.CharData(text[pos:])); err != nil {
			return fmt.Errorf("annotator: encode post-name text: %w", err)
		}
	}
	return nil
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
// two things: the set of full-form genera (first tokens of any
// binomial+ whose first token isn't itself abbreviated) and the set
// of abbreviated full-name attribute values that need expansion.
// Uninomial full-name values are ignored — they're already resolved.
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

// rewriteAbbreviations walks the augmented XML a second time,
// updating <tp:taxon-name> elements whose full-name is an
// abbreviation. Non-abbreviated elements and everything outside
// <tp:taxon-name> is copied through unchanged. Attribute additions
// use the gn: prefix declared on the root by ensureGNNamespace.
func rewriteAbbreviations(
	augmented []byte,
	candidates map[string][]string,
	verifyResults map[string]*vlib.Name,
) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(augmented))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	gnAdded := false
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("annotator: parse augmented XML for rewrite: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			if !gnAdded {
				se.Attr = append(se.Attr, xml.Attr{
					Name:  xml.Name{Local: "xmlns:" + GNPrefix},
					Value: GNNamespace,
				})
				gnAdded = true
			}
			if se.Name.Local == "taxon-name" {
				se = decorateAbbrev(se, candidates, verifyResults)
			}
			if err := enc.EncodeToken(se); err != nil {
				return nil, fmt.Errorf("annotator: encode start: %w", err)
			}
			continue
		}
		if err := enc.EncodeToken(tok); err != nil {
			return nil, fmt.Errorf("annotator: encode token: %w", err)
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("annotator: flush: %w", err)
	}
	return buf.Bytes(), nil
}

// decorateAbbrev updates the attributes of a <tp:taxon-name>
// StartElement in place when the full-name is an abbreviation
// covered by the verification results. Returns the possibly-modified
// element. Not-an-abbreviation elements are returned unchanged.
func decorateAbbrev(
	se xml.StartElement,
	candidates map[string][]string,
	verifyResults map[string]*vlib.Name,
) xml.StartElement {
	var fullNameIdx = -1
	var fullName string
	for i, attr := range se.Attr {
		if attr.Name.Local == "full-name" {
			fullNameIdx = i
			fullName = attr.Value
			break
		}
	}
	if fullNameIdx < 0 {
		return se
	}
	parts := strings.SplitN(fullName, " ", 2)
	if len(parts) < 2 || !strings.HasSuffix(parts[0], ".") {
		return se
	}
	cands := candidates[fullName]
	if len(cands) == 0 {
		se.Attr = append(se.Attr, xml.Attr{
			Name:  xml.Name{Space: GNNamespace, Local: "source"},
			Value: "unresolved",
		})
		return se
	}
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
		// Heuristic proposed candidates; none verified.
		se.Attr = append(se.Attr, xml.Attr{
			Name:  xml.Name{Space: GNNamespace, Local: "source"},
			Value: "unverified",
		})
	case 1:
		se.Attr[fullNameIdx].Value = vs[0].name
		se.Attr = append(se.Attr,
			xml.Attr{
				Name:  xml.Name{Space: GNNamespace, Local: "source"},
				Value: "verified",
			},
			xml.Attr{
				Name:  xml.Name{Space: GNNamespace, Local: "score"},
				Value: formatScore(vs[0].score),
			},
		)
	default:
		names := make([]string, len(vs))
		for i, v := range vs {
			names[i] = v.name
		}
		se.Attr = append(se.Attr,
			xml.Attr{
				Name:  xml.Name{Space: GNNamespace, Local: "source"},
				Value: "ambiguous",
			},
			xml.Attr{
				Name:  xml.Name{Space: GNNamespace, Local: "candidates"},
				Value: strings.Join(names, ", "),
			},
		)
	}
	return se
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

