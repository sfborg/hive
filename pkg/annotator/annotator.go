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
	"encoding/xml"
	"fmt"
	"sync"

	gnfinder "github.com/gnames/gnfinder/pkg"
	gnconfig "github.com/gnames/gnfinder/pkg/config"
	"github.com/gnames/gnfinder/pkg/ent/nlp"
	"github.com/gnames/gnfinder/pkg/io/dict"
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

// Annotator wraps a lazily-initialized GNfinder instance. Cheap to
// call FindNames on; expensive to construct because gnfinder loads
// several MB of dictionaries and Bayesian weights on first use.
// Callers should keep a single *Annotator for the process lifetime.
type Annotator struct {
	initOnce sync.Once
	initErr  error
	gnf      gnfinder.GNfinder
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

