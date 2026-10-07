// Package mentions builds the per-paper name-mention index: how often
// each scientific name is tagged in a paper's augmented JATS, and how
// the names verify against an external name source.
//
// The verification result is a discovery aid. It makes papers findable
// by higher taxon before they are assimilated and is never used as
// project data.
package mentions

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	vlib "github.com/gnames/gnlib/ent/verifier"
	gnverifier "github.com/gnames/gnverifier/pkg"
	gnverifierconfig "github.com/gnames/gnverifier/pkg/config"
	"github.com/gnames/gnverifier/pkg/io/verifrest"
)

// CatalogueOfLife is the GNverifier data source ID of the Catalogue of
// Life, the only discovery source the index uses.
const CatalogueOfLife = 1

// abbreviated matches a full-name attribute that still holds an
// abbreviated binomial ("C. strepitans") because abbreviation
// expansion could not resolve it.
var abbreviated = regexp.MustCompile(`^[A-Z][a-z]?\.\s`)

// Count returns how many times each name is tagged in augmented JATS,
// keyed by the tag's full-name attribute. Tags whose full-name is
// still an unresolved abbreviation are skipped.
func Count(jats []byte) (map[string]int, error) {
	counts := make(map[string]int)
	dec := xml.NewDecoder(bytes.NewReader(jats))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return counts, nil
		}
		if err != nil {
			return nil, fmt.Errorf("mentions: parse JATS: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "taxon-name" {
			continue
		}
		for _, attr := range se.Attr {
			if attr.Name.Local != "full-name" {
				continue
			}
			name := strings.Join(strings.Fields(attr.Value), " ")
			if name != "" && !abbreviated.MatchString(name) {
				counts[name]++
			}
		}
	}
}

// Merge combines counts from several files of the same paper (for
// example its PDF-derived and publisher JATS). Each name keeps its
// highest count, so a name is not counted once per file.
func Merge(sets ...map[string]int) map[string]int {
	out := make(map[string]int)
	for _, set := range sets {
		for name, n := range set {
			if n > out[name] {
				out[name] = n
			}
		}
	}
	return out
}

// Rank is one element of a classification path.
type Rank struct {
	Name string
	Rank string
	ID   string
}

// Match is a name's best verification result in one data source.
type Match struct {
	// MatchType is GNverifier's match type ("Exact", "PartialExact",
	// "Fuzzy", ...). A partial match verified only part of the name,
	// usually the genus, and Path then ends at that part.
	MatchType string
	// RecordID identifies the taxon the classification path leads to:
	// the last element of Path.
	RecordID string
	// MatchedName and CurrentName are canonical forms of the matched
	// record and of its currently accepted name.
	MatchedName string
	CurrentName string
	// Path is the classification from the top down, ending at the
	// matched taxon.
	Path []Rank
}

// Verifier is the subset of the GNverifier client the index uses.
type Verifier interface {
	VerifyBatch(ctx context.Context, names []string) []vlib.Name
	DataSource(id int) (vlib.DataSource, error)
}

// NewVerifier returns a GNverifier client restricted to the Catalogue
// of Life.
func NewVerifier() Verifier {
	cfg := gnverifierconfig.New(gnverifierconfig.OptDataSources([]int{CatalogueOfLife}))
	return gnverifier.New(cfg, verifrest.New(cfg.VerifierURL))
}

// SourceTag identifies the data source and its release for provenance,
// as "gnverifier:<id>@<release>". The release is the source's version
// string, else its release date, else the date GNverifier harvested it.
func SourceTag(v Verifier, id int) (string, error) {
	ds, err := v.DataSource(id)
	if err != nil {
		return "", fmt.Errorf("mentions: data source %d: %w", id, err)
	}
	version := ds.Version
	if version == "" {
		version = ds.RevisionDate
	}
	if version == "" {
		version = ds.UpdatedAt
	}
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("gnverifier:%d@%s", id, version), nil
}

// batchSize bounds each VerifyBatch call.
const batchSize = 1000

// Verify verifies names and returns the best result for each name
// that matched a record with a classification. Names without a match
// are absent from the result.
func Verify(ctx context.Context, v Verifier, names []string) map[string]Match {
	out := make(map[string]Match, len(names))
	for start := 0; start < len(names); start += batchSize {
		end := min(start+batchSize, len(names))
		res := v.VerifyBatch(ctx, names[start:end])
		for i, r := range res {
			if i >= end-start {
				break
			}
			if m, ok := toMatch(r); ok {
				out[names[start+i]] = m
			}
		}
	}
	return out
}

// toMatch converts a verification result into a Match. It reports
// false for results without a classification path.
func toMatch(r vlib.Name) (Match, bool) {
	b := r.BestResult
	if b == nil || b.ClassificationPath == "" {
		return Match{}, false
	}
	names := strings.Split(b.ClassificationPath, "|")
	ranks := strings.Split(b.ClassificationRanks, "|")
	ids := strings.Split(b.ClassificationIDs, "|")
	path := make([]Rank, len(names))
	for i, n := range names {
		path[i].Name = n
		if i < len(ranks) {
			path[i].Rank = strings.ToLower(ranks[i])
		}
		if i < len(ids) {
			path[i].ID = ids[i]
		}
	}
	leaf := path[len(path)-1]
	if leaf.ID == "" {
		return Match{}, false
	}
	return Match{
		MatchType:   r.MatchType.String(),
		RecordID:    leaf.ID,
		MatchedName: b.MatchedCanonicalSimple,
		CurrentName: b.CurrentCanonicalSimple,
		Path:        path,
	}, true
}
