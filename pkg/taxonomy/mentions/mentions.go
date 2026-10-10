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
	"time"

	vlib "github.com/gnames/gnlib/ent/verifier"
	gnverifier "github.com/gnames/gnverifier/pkg"
	gnverifierconfig "github.com/gnames/gnverifier/pkg/config"
	"github.com/gnames/gnverifier/pkg/io/verifrest"
)

// GNverifier data source IDs of the discovery sources.
const (
	CatalogueOfLife = 1
	NCBI            = 4
)

// Source is a name source the index verifies against.
type Source struct {
	// ID is the GNverifier data source ID.
	ID int
	// Key names the source in query parameters and facets
	// ("col" → col_parent, "ncbi" → ncbi_parent).
	Key string
	// Label is the source's display name.
	Label string
}

// Sources are the discovery sources, in display-preference order: a
// summary that can come from either source uses the first that has
// it. NCBI Taxonomy comes first because it covers algae the Catalogue
// of Life base release lacks.
var Sources = []Source{
	{ID: NCBI, Key: "ncbi", Label: "NCBI Taxonomy"},
	{ID: CatalogueOfLife, Key: "col", Label: "Catalogue of Life"},
}

// SourceByKey returns the source with the given key.
func SourceByKey(key string) (Source, bool) {
	for _, s := range Sources {
		if s.Key == key {
			return s, true
		}
	}
	return Source{}, false
}

// SourceByID returns the source with the given GNverifier ID.
func SourceByID(id int) (Source, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// TagPrefix is the raw__source prefix shared by every release of a
// source.
func TagPrefix(id int) string {
	return fmt.Sprintf("gnverifier:%d@", id)
}

// abbreviated matches a full-name attribute that still holds an
// abbreviated binomial ("C. strepitans") because abbreviation
// expansion could not resolve it.
var abbreviated = regexp.MustCompile(`^[A-Z][a-z]?\.\s`)

// Count returns how many times each name is tagged in augmented JATS.
// A tag's name is its full-name attribute; publisher TaxPub tags,
// which carry no full-name, use their text (the joined
// taxon-name-part pieces). Names that are still unresolved
// abbreviations are skipped.
func Count(jats []byte) (map[string]int, error) {
	counts := make(map[string]int)
	add := func(name string) {
		name = strings.Join(strings.Fields(name), " ")
		if name != "" && !abbreviated.MatchString(name) {
			counts[name]++
		}
	}
	dec := xml.NewDecoder(bytes.NewReader(jats))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	// collecting > 0 while inside a tag without full-name; text goes
	// to buf until that tag closes.
	var collecting int
	var buf strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return counts, nil
		}
		if err != nil {
			return nil, fmt.Errorf("mentions: parse JATS: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "taxon-name" {
				continue
			}
			if collecting > 0 {
				collecting++
				continue
			}
			full := ""
			for _, attr := range t.Attr {
				if attr.Name.Local == "full-name" {
					full = attr.Value
				}
			}
			if full != "" {
				add(full)
				continue
			}
			collecting = 1
			buf.Reset()
		case xml.CharData:
			if collecting > 0 {
				buf.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == "taxon-name" && collecting > 0 {
				collecting--
				if collecting == 0 {
					add(buf.String())
				}
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
	// Homonyms are the other lineages the name matches with the same
	// match type, in GNverifier's score order: the same name-string
	// used for unrelated organisms. Their own Homonyms are empty.
	Homonyms []Match
}

// Verifier is the subset of the GNverifier client the index uses.
type Verifier interface {
	VerifyBatch(ctx context.Context, names []string) []vlib.Name
	DataSource(id int) (vlib.DataSource, error)
}

// NewVerifier returns a GNverifier client restricted to one data
// source. It asks for all matches, so homonyms in the source are
// returned too.
func NewVerifier(sourceID int) Verifier {
	cfg := gnverifierconfig.New(
		gnverifierconfig.OptDataSources([]int{sourceID}),
		gnverifierconfig.OptWithAllMatches(true),
	)
	return gnverifier.New(cfg, verifrest.New(cfg.VerifierURL))
}

// SourceTag identifies the data source and its release for provenance,
// as "gnverifier:<id>@<release>". The release is the source's version
// string, else its release date, else the date GNverifier harvested it.
// A release date in the future is treated as a metadata error and
// skipped.
func SourceTag(v Verifier, id int) (string, error) {
	ds, err := v.DataSource(id)
	if err != nil {
		return "", fmt.Errorf("mentions: data source %d: %w", id, err)
	}
	version := ds.Version
	if version == "" && !inFuture(ds.RevisionDate) {
		version = ds.RevisionDate
	}
	if version == "" {
		version = ds.UpdatedAt
	}
	if version == "" {
		version = "unknown"
	}
	return TagPrefix(id) + version, nil
}

// inFuture reports whether a YYYY-MM-DD, YYYY-MM or YYYY date lies
// after today.
func inFuture(date string) bool {
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, date); err == nil {
			return t.After(time.Now())
		}
	}
	return false
}

// batchSize bounds each VerifyBatch call.
const batchSize = 1000

// Verify verifies names and returns the result for each name that
// matched a record with a classification: the top-scored lineage with
// any homonyms. Names without a match are absent from the result.
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

// maxHomonyms bounds the lineages kept per name.
const maxHomonyms = 5

// toMatch converts a verification result into a Match. With all
// matches requested the results list every matching record; records
// of the name's best match type are grouped by lineage (a source may
// hold one record per strain), keeping the shortest path of each. It
// reports false when no record has a classification path.
func toMatch(r vlib.Name) (Match, bool) {
	results := r.Results
	if len(results) == 0 && r.BestResult != nil {
		results = []*vlib.ResultData{r.BestResult}
	}
	var out []Match
	byLineage := map[string]int{}
	for _, res := range results {
		if res == nil || (len(r.Results) > 0 && res.MatchType != r.MatchType) {
			continue
		}
		m, ok := resultToMatch(res)
		if !ok {
			continue
		}
		m.MatchType = r.MatchType.String()
		key := lineageKey(m.Path)
		if i, seen := byLineage[key]; seen {
			if len(m.Path) < len(out[i].Path) {
				out[i] = m
			}
			continue
		}
		if len(out) > maxHomonyms {
			continue
		}
		byLineage[key] = len(out)
		out = append(out, m)
	}
	if len(out) == 0 {
		return Match{}, false
	}
	best := out[0]
	if len(out) > 1 {
		best.Homonyms = out[1:]
	}
	return best, true
}

// lineageKey identifies a classification down to its genus, so
// records that differ only below the genus (species, strains) count
// as one lineage.
func lineageKey(path []Rank) string {
	end := len(path)
	for i, r := range path {
		if r.Rank == "genus" {
			end = i + 1
		}
	}
	names := make([]string, end)
	for i := range end {
		names[i] = path[i].Name
	}
	return strings.Join(names, "|")
}

// resultToMatch converts one result record. It reports false for
// records without a classification path.
func resultToMatch(b *vlib.ResultData) (Match, bool) {
	if b.ClassificationPath == "" {
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
		RecordID:    leaf.ID,
		MatchedName: b.MatchedCanonicalSimple,
		CurrentName: b.CurrentCanonicalSimple,
		Path:        path,
	}, true
}
