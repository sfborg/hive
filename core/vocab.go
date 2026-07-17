package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// The sfga schema calls its controlled-value tables "controlled
// vocabularies" (per the SFBorg paper). Hive exposes them as one bundle
// via GET /api/vocab so the frontends can render pickers without a
// per-field round trip. The data is read-only for the lifetime of the
// process — vocabularies don't change during a session — so the whole
// bundle is cached on the Archive after the first request.

// VocabTerm is one entry in a controlled vocabulary. `Name` is the
// human-facing label. For flat vocabularies (nom_code, gender, sex, …)
// the schema stores only the ID; hive derives a display Name by
// lower-casing and replacing underscores with spaces (matching the
// convention coldp.ToStr already uses).
//
// The empty-string term (id == "") is always the first entry in every
// vocabulary — the schema seeds it as the "unspecified" value — and
// frontends can present it as "(unset)".
type VocabTerm struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Vocabulary is the JSON-serializable bundle GET /api/vocab returns.
// One field per sfga enum table; more can be added without breaking the
// wire contract since JSON is additive.
//
// Licenses is a hive-curated suggestion list — sfga's col__license is
// a free TEXT column with no FK, so this vocab is display-only. The
// PWA/TUI use it to populate a suggestions dropdown; curators can
// still type a value that isn't in the list.
type Vocabulary struct {
	NomCode                []VocabTerm `json:"nom_code"`
	NomStatus              []VocabTerm `json:"nom_status"`
	TaxonomicStatus        []VocabTerm `json:"taxonomic_status"`
	Rank                   []VocabTerm `json:"rank"`
	Gender                 []VocabTerm `json:"gender"`
	Sex                    []VocabTerm `json:"sex"`
	DistributionStatus     []VocabTerm `json:"distribution_status"`
	NomRelType             []VocabTerm `json:"nom_rel_type"`
	EstimateType           []VocabTerm `json:"estimate_type"`
	NamePart               []VocabTerm `json:"name_part"`
	MatchType              []VocabTerm `json:"match_type"`
	ReferenceType          []VocabTerm `json:"reference_type"`
	TypeStatus             []VocabTerm `json:"type_status"`
	Gazetteer              []VocabTerm `json:"gazetteer"`
	GeoTime                []VocabTerm `json:"geo_time"`
	SpeciesInteractionType []VocabTerm `json:"species_interaction_type"`
	TaxonConceptRelType    []VocabTerm `json:"taxon_concept_rel_type"`
	Licenses               []VocabTerm `json:"licenses"`
}

// licenseSuggestions is the hand-curated list of SPDX identifiers
// hive suggests for the metadata License field. Covers the licenses
// CoLDP / ChecklistBank consumers accept plus a handful of
// data-commons licenses commonly used by biodiversity datasets. Not
// exhaustive — curators can still type a custom value into the picker.
var licenseSuggestions = []VocabTerm{
	{ID: "CC0-1.0", Name: "CC0 1.0 — public domain dedication"},
	{ID: "CC-BY-4.0", Name: "CC BY 4.0 — attribution"},
	{ID: "CC-BY-SA-4.0", Name: "CC BY-SA 4.0 — attribution, share-alike"},
	{ID: "CC-BY-NC-4.0", Name: "CC BY-NC 4.0 — attribution, non-commercial"},
	{ID: "CC-BY-NC-SA-4.0", Name: "CC BY-NC-SA 4.0 — attribution, non-commercial, share-alike"},
	{ID: "CC-BY-ND-4.0", Name: "CC BY-ND 4.0 — attribution, no derivatives"},
	{ID: "CC-BY-NC-ND-4.0", Name: "CC BY-NC-ND 4.0 — attribution, non-commercial, no derivatives"},
	{ID: "ODbL-1.0", Name: "ODbL 1.0 — Open Database License"},
	{ID: "PDDL-1.0", Name: "PDDL 1.0 — Public Domain Dedication and License"},
	{ID: "OGL-UK-3.0", Name: "OGL 3.0 — UK Open Government Licence"},
}

// Vocabulary returns the cached vocabulary, loading it on the first call.
// Subsequent callers share the same *Vocabulary — safe because the tables
// don't change during a session. Errors from the initial load are cached
// too; callers seeing an error should treat it as terminal for the process.
func (a *Archive) Vocabulary(ctx context.Context) (*Vocabulary, error) {
	a.vocabOnce.Do(func() {
		a.vocab, a.vocabErr = a.loadVocabulary(ctx)
	})
	return a.vocab, a.vocabErr
}

// vocab-related fields on Archive live in archive.go; declared there so
// this file only holds vocab logic and doesn't touch the struct layout.

func (a *Archive) loadVocabulary(ctx context.Context) (*Vocabulary, error) {
	v := &Vocabulary{}
	var err error

	// Flat tables — schema has only col__id. Everything except the
	// hardcoded "" seed row is a real enum value.
	flatTables := []struct {
		table string
		dst   *[]VocabTerm
	}{
		{"nom_code", &v.NomCode},
		{"nom_status", &v.NomStatus},
		{"gender", &v.Gender},
		{"sex", &v.Sex},
		{"distribution_status", &v.DistributionStatus},
		{"nom_rel_type", &v.NomRelType},
		{"estimate_type", &v.EstimateType},
		{"name_part", &v.NamePart},
		{"match_type", &v.MatchType},
		{"reference_type", &v.ReferenceType},
	}
	for _, ft := range flatTables {
		terms, err2 := a.loadFlatVocab(ctx, ft.table)
		if err2 != nil {
			return nil, fmt.Errorf("core: load vocab %q: %w", ft.table, err2)
		}
		*ft.dst = terms
	}

	// Rich tables with col__name — use the schema-provided name instead
	// of the derived one. Sort by col__id for a stable client experience;
	// rank is the notable exception (see below).
	v.TaxonomicStatus, err = a.loadNamedVocab(ctx, "taxonomic_status", false)
	if err != nil {
		return nil, err
	}
	v.TypeStatus, err = a.loadNamedVocab(ctx, "type_status", false)
	if err != nil {
		return nil, err
	}
	v.Gazetteer, err = a.loadNamedVocab(ctx, "gazetteer", false)
	if err != nil {
		return nil, err
	}
	v.SpeciesInteractionType, err = a.loadNamedVocab(ctx, "species_interaction_type", false)
	if err != nil {
		return nil, err
	}
	v.TaxonConceptRelType, err = a.loadNamedVocab(ctx, "taxon_concept_rel_type", false)
	if err != nil {
		return nil, err
	}

	// Rank: 150+ entries — sort by col__name (alphabetical human form) so a
	// browser <select> shows "class / family / genus / kingdom …" instead
	// of the noisier ID order.
	v.Rank, err = a.loadNamedVocab(ctx, "rank", true)
	if err != nil {
		return nil, err
	}

	// geo_time: 200+ entries; also sort by col__name for scannability.
	v.GeoTime, err = a.loadNamedVocab(ctx, "geo_time", true)
	if err != nil {
		return nil, err
	}

	// Licenses aren't in a sfga table — hive-curated suggestions
	// only. Copy so the caller can't mutate the shared slice.
	v.Licenses = append([]VocabTerm(nil), licenseSuggestions...)

	return v, nil
}

// loadFlatVocab reads a table whose only column is col__id.
// Table name comes from a fixed const slice in loadVocabulary — never user
// input — so the string concatenation is safe.
func (a *Archive) loadFlatVocab(ctx context.Context, table string) ([]VocabTerm, error) {
	q := "SELECT col__id FROM " + table + " ORDER BY col__id"
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []VocabTerm
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		terms = append(terms, VocabTerm{
			ID:   id,
			Name: deriveVocabName(id),
		})
	}
	return terms, rows.Err()
}

// loadNamedVocab reads a table with col__id and col__name. If sortByName
// is true, results are ordered by human-readable name; otherwise by ID.
func (a *Archive) loadNamedVocab(ctx context.Context, table string, sortByName bool) ([]VocabTerm, error) {
	orderBy := "col__id"
	if sortByName {
		orderBy = "col__name, col__id"
	}
	q := "SELECT col__id, COALESCE(col__name, '') FROM " + table + " ORDER BY " + orderBy
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []VocabTerm
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if name == "" {
			name = deriveVocabName(id)
		}
		terms = append(terms, VocabTerm{ID: id, Name: name})
	}
	return terms, rows.Err()
}

// deriveVocabName turns an enum ID like "PROVISIONALLY_ACCEPTED" into the
// display form "provisionally accepted". Matches coldp.ToStr's convention.
func deriveVocabName(id string) string {
	if id == "" {
		return ""
	}
	return strings.ToLower(strings.ReplaceAll(id, "_", " "))
}

// syncOnceKey is intentionally unexported — the sync.Once and its cached
// results live on Archive, but the helper below documents the invariant
// so package readers see it in one place.
var _ = new(sync.Once) // silence import if code above shrinks
