package hive

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// The NOMEN ontology is a controlled vocabulary of nomenclatural
// statuses maintained by SpeciesFileGroup. Hive surfaces it to the name-
// status picker so curators can record the code-specific status their
// nomenclatural code defines. See CLAUDE.md § Nomenclatural status
// vocabulary (NOMEN).
//
// nomen.owl is embedded via //go:embed so the binary stays offline-
// capable. Parsing runs once per process; a sync.Once guards the cache
// so concurrent callers share the same result.
//
// PROTOTYPE — LIFT-TO-SFLIB CANDIDATE. This whole file (OWL parse,
// TW enrichment merge, CoL downcast walk) is genuinely shared
// infrastructure: sf, harvester, gndb, and any other SFBorg tool that
// touches nomenclatural status will want the same thing. It lives in
// hive/pkg/ for now because hive is where the need first surfaced.
// The code is written self-contained (no reach into hive-specific
// structures beyond the seed-into-nom_status helper at the bottom) so
// migration to sflib is a straight cut.

//go:embed nomen.owl
var nomenOWL []byte

//go:embed nomen_tw.json
var nomenTWJSON []byte

// NomenTerm is one class from the NOMEN ontology, projected to the
// fields hive's status picker needs.
//
// Code — nomenclatural-code prefix detected from the label (ICZN, ICN,
//   ICNP, ICVCN, ICNCP, ICPN) so the frontend can filter by the current
//   name's code. Empty when the term is code-general.
// Kind — TW-authoritative partition (see below).
// Parent — parent URI from OWL's rdfs:subClassOf / rdfs:subPropertyOf;
//   drives the CoL-style downcast walk.
// GbifStatus — TW's Latin annotation (`invalidum`, `conservandum`, …).
//   Useful for display; not the CoLDP source of truth.
// ColStatus — the CoLDP-generalized bucket resolved by walking the
//   Parent chain up to a mapped root. Populated at parse time.
//   Matches ChecklistBank's nom_status vocabulary (ESTABLISHED,
//   NOT_ESTABLISHED, ACCEPTABLE, UNACCEPTABLE, CONSERVED, REJECTED,
//   DOUBTFUL). Empty when no mapped ancestor exists.
// Popular — true for the ~24 CoL-mapped root URIs (ICZN Available,
//   ICZN Unavailable, ICN validly published, etc.). The picker floats
//   these to the top of the empty-query results so the common
//   90%-of-cases choices land in front of the curator without
//   scrolling — matches TW's "popular statuses on the first tab" UX.
//
// Kind values:
//   "classification" — attaches to a single name (col__status_id target)
//   "relationship"   — links two names (col__type_id on name_relation)
//   ""               — not modeled by TW (ranks, name-parts, ...); NOT
//                      surfaced in the status picker
type NomenTerm struct {
	ID         string `json:"id"`
	Local      string `json:"local"`
	Label      string `json:"label"`
	Code       string `json:"code,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Class      string `json:"class,omitempty"`
	GbifStatus string `json:"gbif_status,omitempty"`
	ColStatus  string `json:"col_status,omitempty"`
	Parent     string `json:"parent,omitempty"`
	Popular    bool   `json:"popular,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// twEntry mirrors tools/mknomen's output row.
type twEntry struct {
	URI    string `json:"uri"`
	Kind   string `json:"kind"`
	Class  string `json:"class"`
	Gbif   string `json:"gbif_status,omitempty"`
	Ranks  string `json:"applicable_ranks,omitempty"`
	Assign string `json:"assignable,omitempty"`
}

var (
	nomenTerms    []NomenTerm
	nomenTermsErr error
	nomenOnce     sync.Once
)

// colRootStatus is the CoL-authoritative mapping from NOMEN root URIs
// to the ChecklistBank-generalized nom_status buckets. Transcribed
// from CoL's parser/src/main/java/life/catalogue/parser/NomenOntology.java
// (mapNomen). Every descendant of a mapped root inherits its bucket via
// the parent chain; unmapped subtrees resolve to "" (no CoLDP target).
//
// Kept as a small in-source table because CoL updates it infrequently
// and hand-verified accuracy matters. Sync manually if CoL changes.
var colRootStatus = map[string]string{
	// ICN
	"http://purl.obolibrary.org/obo/NOMEN_0000007": "ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000008": "NOT_ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000009": "CONSERVED",
	"http://purl.obolibrary.org/obo/NOMEN_0000377": "NOT_ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000384": "ACCEPTABLE",
	"http://purl.obolibrary.org/obo/NOMEN_0000385": "CONSERVED",
	"http://purl.obolibrary.org/obo/NOMEN_0000386": "UNACCEPTABLE",
	// ICNP
	"http://purl.obolibrary.org/obo/NOMEN_0000082": "NOT_ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000083": "NOT_ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000084": "ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000085": "UNACCEPTABLE",
	"http://purl.obolibrary.org/obo/NOMEN_0000086": "ACCEPTABLE",
	// ICTV (viruses)
	"http://purl.obolibrary.org/obo/NOMEN_0000125": "ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000126": "NOT_ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000127": "ACCEPTABLE",
	"http://purl.obolibrary.org/obo/NOMEN_0000128": "UNACCEPTABLE",
	// ICZN
	"http://purl.obolibrary.org/obo/NOMEN_0000129": "DOUBTFUL",
	"http://purl.obolibrary.org/obo/NOMEN_0000168": "NOT_ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000219": "REJECTED",
	"http://purl.obolibrary.org/obo/NOMEN_0000223": "ESTABLISHED",
	"http://purl.obolibrary.org/obo/NOMEN_0000224": "ACCEPTABLE",
	"http://purl.obolibrary.org/obo/NOMEN_0000225": "DOUBTFUL",
	"http://purl.obolibrary.org/obo/NOMEN_0000226": "UNACCEPTABLE",
}

// Nomen returns the parsed NOMEN vocabulary. Parses the embedded OWL
// file on first call, merges in TW's kind/gbif_status partition from
// nomen_tw.json, resolves the CoLDP-generalized downcast for every
// term via CoL's mapping table + subClassOf walk, and caches the
// result — vocab is process-scoped and immutable, so a mutex isn't
// needed on subsequent reads.
func Nomen() ([]NomenTerm, error) {
	nomenOnce.Do(func() {
		terms, err := parseNomen(nomenOWL)
		if err != nil {
			nomenTerms, nomenTermsErr = nil, err
			return
		}
		var tw []twEntry
		if err := json.Unmarshal(nomenTWJSON, &tw); err != nil {
			nomenTerms, nomenTermsErr = nil, fmt.Errorf("core: parse nomen_tw.json: %w", err)
			return
		}
		byURI := make(map[string]twEntry, len(tw))
		for _, e := range tw {
			byURI[e.URI] = e
		}
		for i, t := range terms {
			if e, ok := byURI[t.ID]; ok {
				terms[i].Kind = e.Kind
				terms[i].Class = e.Class
				terms[i].GbifStatus = e.Gbif
			}
		}
		// Resolve the CoLDP downcast for every term by walking each
		// term's parent chain until we hit a colRootStatus-mapped
		// ancestor. Do a full pass with an index so repeated lookups
		// are cheap.
		parentIdx := make(map[string]*NomenTerm, len(terms))
		for i := range terms {
			parentIdx[terms[i].ID] = &terms[i]
		}
		for i := range terms {
			terms[i].ColStatus = resolveColStatus(terms[i].ID, parentIdx, map[string]bool{})
			if _, isRoot := colRootStatus[terms[i].ID]; isRoot {
				terms[i].Popular = true
			}
		}
		nomenTerms, nomenTermsErr = terms, nil
	})
	return nomenTerms, nomenTermsErr
}

// resolveColStatus walks up the Parent chain from uri, returning the
// first colRootStatus mapping found. seen guards against pathological
// cycles (shouldn't happen in a well-formed ontology, but cheap
// insurance). Returns "" when no mapped ancestor exists.
func resolveColStatus(uri string, idx map[string]*NomenTerm, seen map[string]bool) string {
	if uri == "" || seen[uri] {
		return ""
	}
	if v, ok := colRootStatus[uri]; ok {
		return v
	}
	seen[uri] = true
	t, ok := idx[uri]
	if !ok || t.Parent == "" {
		return ""
	}
	return resolveColStatus(t.Parent, idx, seen)
}

// NomenDowncast returns the ChecklistBank-generalized nom_status
// bucket for a NOMEN URI, walking the OWL subClassOf hierarchy up to
// a mapped root. Used at CoLDP export time (and at write time once
// tw__name_status lands — see PROJECTS.md § tw__name_status future
// direction). Empty return means "no CoLDP-generalized target for
// this URI"; the caller writes NULL to col__status_id.
func NomenDowncast(uri string) string {
	if _, err := Nomen(); err != nil {
		return ""
	}
	for _, t := range nomenTerms {
		if t.ID == uri {
			return t.ColStatus
		}
	}
	return ""
}

// parseNomen walks the OWL file with an xml.Decoder to extract every
// <owl:Class rdf:about="…"> block's URI, label, and comment. Token
// streaming keeps us tolerant of the surrounding RDF/OWL structure
// (annotation properties, subClassOf refs, disjoint-with edges, etc.)
// without pulling in a full OWL library.
func parseNomen(data []byte) ([]NomenTerm, error) {
	const (
		owlNS  = "http://www.w3.org/2002/07/owl#"
		rdfNS  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
		rdfsNS = "http://www.w3.org/2000/01/rdf-schema#"
	)
	dec := xml.NewDecoder(bytes.NewReader(data))

	var (
		terms     []NomenTerm
		current   *NomenTerm
		text      strings.Builder
		inLabel   bool
		inComment bool
	)

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("core: parse nomen: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// NOMEN encodes classifications as owl:Class and relations
			// as owl:ObjectProperty (a name-to-name link is an object
			// property in RDF). Pick up both — the kind partition
			// comes later from nomen_tw.json.
			if t.Name.Space == owlNS &&
				(t.Name.Local == "Class" || t.Name.Local == "ObjectProperty") {
				uri := attr(t, rdfNS, "about")
				if uri == "" {
					continue // unnamed / anonymous — skip
				}
				current = &NomenTerm{ID: uri, Local: localOf(uri)}
				continue
			}
			if current == nil {
				continue
			}
			if t.Name.Space == rdfsNS && t.Name.Local == "label" {
				inLabel = true
				text.Reset()
			} else if t.Name.Space == rdfsNS && t.Name.Local == "comment" {
				inComment = true
				text.Reset()
			} else if t.Name.Space == rdfsNS &&
				(t.Name.Local == "subClassOf" || t.Name.Local == "subPropertyOf") {
				// rdfs:subClassOf on classes, rdfs:subPropertyOf on
				// ObjectProperty — same semantics for hive: identifies
				// the term's parent in the ontology. Both are usually
				// self-closing with an rdf:resource attribute pointing
				// at the parent URI. Multiple parents can appear
				// (multiple inheritance) — we keep the last, matching
				// CoL's NomenOntology which does the same.
				if parent := attr(t, rdfNS, "resource"); parent != "" {
					current.Parent = parent
				}
			}
		case xml.CharData:
			if inLabel || inComment {
				text.Write(t)
			}
		case xml.EndElement:
			if t.Name.Space == owlNS &&
				(t.Name.Local == "Class" || t.Name.Local == "ObjectProperty") {
				if current != nil && current.Label != "" {
					current.Code = codePrefixOf(current.Label)
					terms = append(terms, *current)
				}
				current = nil
				continue
			}
			if inLabel && t.Name.Space == rdfsNS && t.Name.Local == "label" {
				if current != nil {
					current.Label = strings.TrimSpace(text.String())
				}
				inLabel = false
			}
			if inComment && t.Name.Space == rdfsNS && t.Name.Local == "comment" {
				if current != nil {
					current.Comment = strings.TrimSpace(text.String())
				}
				inComment = false
			}
		}
	}
	return terms, nil
}

// attr fetches an attribute value by namespace + local name.
func attr(t xml.StartElement, ns, local string) string {
	for _, a := range t.Attr {
		if a.Name.Space == ns && a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// localOf returns the short identifier at the end of an OBO-style URI
// ("http://purl.obolibrary.org/obo/NOMEN_0000007" → "NOMEN_0000007").
func localOf(uri string) string {
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

// codePrefixOf reads the first whitespace-delimited token of the label
// and returns it when it matches a known nomenclatural-code abbreviation.
// The convention in NOMEN is that code-specific classes start their
// label with the code name; general classes don't.
func codePrefixOf(label string) string {
	fields := strings.Fields(label)
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "ICZN", "ICN", "ICNP", "ICVCN", "ICNCP", "ICPN":
		return fields[0]
	}
	return ""
}

// NomenLabelFor returns the human label for a NOMEN URI, or "" if the
// URI isn't in the ontology. Read paths use this so a name's stored
// col__status_id renders as "ICZN objective synonym" rather than the
// raw URI. Legacy col__status_id values (CoLDP-generalized enums like
// "ESTABLISHED") won't be in the map — callers should fall back to
// their own display derivation.
func NomenLabelFor(uri string) string {
	if uri == "" {
		return ""
	}
	if _, err := Nomen(); err != nil {
		return ""
	}
	return nomenByURI()[uri]
}

var (
	nomenByURIMap  map[string]string
	nomenByURIOnce sync.Once
)

// nomenByURI builds a URI → label index once per process for O(1)
// lookups. Called only after Nomen() has succeeded so the terms slice
// is populated.
func nomenByURI() map[string]string {
	nomenByURIOnce.Do(func() {
		m := make(map[string]string, len(nomenTerms))
		for _, t := range nomenTerms {
			m[t.ID] = t.Label
		}
		nomenByURIMap = m
	})
	return nomenByURIMap
}

// SeedNomenIntoNomStatus inserts NOMEN classification URIs into the
// sfga nom_status vocab table, and NOMEN relationship URIs into
// nom_rel_type — the two FK targets for col__status_id (on name /
// synonym) and col__type_id (on name_relation) respectively.
// Idempotent via INSERT OR IGNORE; safe to call on every Archive.Open.
//
// Only URIs with a TW-known Kind ("classification" or "relationship")
// get seeded — ranks, name-parts, and other NOMEN concepts don't
// belong in either FK-checked vocabulary. Existing CoLDP-generalized
// rows in both tables are untouched; NOMEN URIs coexist alongside them.
func SeedNomenIntoNomStatus(ctx context.Context, db *sql.DB) error {
	terms, err := Nomen()
	if err != nil {
		return fmt.Errorf("core: seed nomen: parse: %w", err)
	}
	// Batch under a single tx — the seed runs on every Open and
	// fsync-per-row on WAL is measurable on large archives.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("core: seed nomen: begin tx: %w", err)
	}
	defer tx.Rollback()
	stmtStatus, err := tx.PrepareContext(ctx,
		"INSERT OR IGNORE INTO nom_status (col__id) VALUES (?)")
	if err != nil {
		return fmt.Errorf("core: seed nomen (status): prepare: %w", err)
	}
	defer stmtStatus.Close()
	stmtRel, err := tx.PrepareContext(ctx,
		"INSERT OR IGNORE INTO nom_rel_type (col__id) VALUES (?)")
	if err != nil {
		return fmt.Errorf("core: seed nomen (rel): prepare: %w", err)
	}
	defer stmtRel.Close()
	for _, t := range terms {
		switch t.Kind {
		case "classification":
			if _, err := stmtStatus.ExecContext(ctx, t.ID); err != nil {
				return fmt.Errorf("core: seed nomen status %s: %w", t.Local, err)
			}
		case "relationship":
			if _, err := stmtRel.ExecContext(ctx, t.ID); err != nil {
				return fmt.Errorf("core: seed nomen rel %s: %w", t.Local, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("core: seed nomen: commit: %w", err)
	}
	return nil
}
