package hive

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
// WUI/TUI use it to populate a suggestions dropdown; curators can
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
// don't change during a session unless the vocab editor mutates them.
// Editor writes call invalidateVocab() so the next Vocabulary() call
// reloads from disk.
//
// Errors from the load are cached too; callers seeing an error should
// treat it as terminal for the process (only the initial-load path can
// error; subsequent loads only happen after successful writes).
func (a *Archive) Vocabulary(ctx context.Context) (*Vocabulary, error) {
	a.vocabMu.Lock()
	defer a.vocabMu.Unlock()
	if a.vocab == nil && a.vocabErr == nil {
		a.vocab, a.vocabErr = a.loadVocabulary(ctx)
	}
	return a.vocab, a.vocabErr
}

// invalidateVocab drops the cached Vocabulary so the next Vocabulary()
// call reloads from disk. Called by vocab-editor mutation methods.
func (a *Archive) invalidateVocab() {
	a.vocabMu.Lock()
	a.vocab = nil
	a.vocabErr = nil
	a.vocabMu.Unlock()
}

// VocabTermDetail is the full-fidelity term shape for the vocab
// editor — the picker bundle at /api/vocab ships only id + name so
// the round trip stays small; the editor's list + form need the
// remaining columns (description, ontology URI via obo, inverse,
// symmetrical, superTypes) to display and edit rich vocabs like
// species_interaction_type.
//
// Not every rich vocab populates every field. Callers should treat
// empty strings and false as "unset" — the display path elides them.
type VocabTermDetail struct {
	ID          string
	Name        string
	Description string
	// Obo is an ontology URI (e.g. Relations Ontology PURL
	// "http://purl.obolibrary.org/obo/RO_0002442"). Named for sfga's
	// col__obo column even though the value is any URI, not
	// necessarily an OBO one — sfga's column naming is legacy.
	Obo         string
	Inverse     string
	Symmetrical bool
	// SuperTypes is a comma-separated list of parent term ids from
	// sfga's col__superTypes. Kept as a raw string on the wire; the
	// UI splits / rejoins.
	SuperTypes string
}

// ListSpeciesInteractionTypes returns every row in the
// species_interaction_type vocab with full detail — the editor's
// list source. Ordered by name for scannability (43-ish entries;
// a two-page scroll at most).
func (a *Archive) ListSpeciesInteractionTypes(ctx context.Context) ([]VocabTermDetail, error) {
	const q = `SELECT
		col__id,
		COALESCE(col__name, ''),
		COALESCE(col__description, ''),
		COALESCE(col__obo, ''),
		COALESCE(col__inverse, ''),
		COALESCE(col__symmetrical, 0),
		COALESCE(col__superTypes, '')
	FROM species_interaction_type
	WHERE col__id != ''
	ORDER BY col__name, col__id`
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("core: list species_interaction_type: %w", err)
	}
	defer rows.Close()
	var out []VocabTermDetail
	for rows.Next() {
		var t VocabTermDetail
		var sym int
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Description, &t.Obo,
			&t.Inverse, &sym, &t.SuperTypes,
		); err != nil {
			return nil, fmt.Errorf("core: scan species_interaction_type: %w", err)
		}
		t.Symmetrical = sym != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddSpeciesInteractionType writes a new vocab term. ID and Name are
// required; the rest are optional. Uniqueness is enforced by sfga's
// PRIMARY KEY on col__id — a duplicate returns ErrConflict.
// Invalidates the cached vocab bundle so /api/vocab reflects the new
// term on next read.
func (t *Tx) AddSpeciesInteractionType(term VocabTermDetail) error {
	if term.ID == "" {
		return fmt.Errorf("core: add species_interaction_type: %w: id required", ErrValidation)
	}
	if term.Name == "" {
		return fmt.Errorf("core: add species_interaction_type: %w: name required", ErrValidation)
	}
	sym := 0
	if term.Symmetrical {
		sym = 1
	}
	const insert = `INSERT INTO species_interaction_type (
		col__id, col__name, col__description,
		col__obo, col__inverse, col__symmetrical,
		col__superTypes
	) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := t.tx.ExecContext(t.ctx, insert,
		term.ID, term.Name, term.Description,
		term.Obo, nullIfEmpty(term.Inverse), sym,
		term.SuperTypes,
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") ||
			strings.Contains(err.Error(), "PRIMARY KEY") {
			return fmt.Errorf("core: add species_interaction_type %q: %w",
				term.ID, ErrConflict)
		}
		return fmt.Errorf("core: add species_interaction_type %q: %w",
			term.ID, err)
	}
	t.archive.invalidateVocab()
	return nil
}

// UpdateSpeciesInteractionType rewrites every editable column of the
// term at term.ID. ID itself is not editable — deleting + re-adding
// is the way to rename an id (also rare — the id is what other rows
// reference by FK).
func (t *Tx) UpdateSpeciesInteractionType(term VocabTermDetail) error {
	if term.ID == "" {
		return fmt.Errorf("core: update species_interaction_type: %w: id required", ErrValidation)
	}
	if term.Name == "" {
		return fmt.Errorf("core: update species_interaction_type: %w: name required", ErrValidation)
	}
	sym := 0
	if term.Symmetrical {
		sym = 1
	}
	const upd = `UPDATE species_interaction_type SET
		col__name = ?, col__description = ?,
		col__obo = ?, col__inverse = ?, col__symmetrical = ?,
		col__superTypes = ?
	WHERE col__id = ?`
	res, err := t.tx.ExecContext(t.ctx, upd,
		term.Name, term.Description,
		term.Obo, nullIfEmpty(term.Inverse), sym,
		term.SuperTypes,
		term.ID,
	)
	if err != nil {
		return fmt.Errorf("core: update species_interaction_type %q: %w",
			term.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update species_interaction_type %q rows: %w",
			term.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update species_interaction_type %q: %w",
			term.ID, ErrNotFound)
	}
	t.archive.invalidateVocab()
	return nil
}

// DeleteSpeciesInteractionType removes a term. sfga's FK from
// species_interaction.col__type_id to species_interaction_type.col__id
// rejects the delete if any species_interaction row still cites the
// term — returns ErrConflict in that case so the caller can prompt
// the curator to reassign the affected rows first.
func (t *Tx) DeleteSpeciesInteractionType(id string) error {
	if id == "" {
		return fmt.Errorf("core: delete species_interaction_type: %w: id required", ErrValidation)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM species_interaction_type WHERE col__id = ?`, id,
	)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return fmt.Errorf("core: delete species_interaction_type %q: %w: term is still referenced by one or more species_interaction rows",
				id, ErrConflict)
		}
		return fmt.Errorf("core: delete species_interaction_type %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete species_interaction_type %q rows: %w",
			id, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete species_interaction_type %q: %w",
			id, ErrNotFound)
	}
	t.archive.invalidateVocab()
	return nil
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

	// Hide the sfga-seeded empty-id ("(unset)") row from vocabs that
	// hive treats as required. sfga's schema keeps the empty seed row
	// so the NOT NULL FK column has a valid target when a curator
	// hasn't picked yet — but the WUI/TUI pickers shouldn't offer
	// "unset" as a legitimate choice for these vocabs. Filtering here
	// covers both frontends via the shared /api/vocab bundle.
	v.SpeciesInteractionType = stripUnsetVocabTerm(v.SpeciesInteractionType)

	return v, nil
}

// stripUnsetVocabTerm drops the leading empty-id term from a vocab
// slice. Used for vocabs hive treats as required (no "(unset)" in
// the picker). Idempotent — safe to call on any slice.
func stripUnsetVocabTerm(terms []VocabTerm) []VocabTerm {
	out := terms[:0]
	for _, t := range terms {
		if t.ID == "" {
			continue
		}
		out = append(out, t)
	}
	return out
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
