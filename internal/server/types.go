package server

import (
	"database/sql"

	"github.com/gnames/gnlib/ent/nomcode"
	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/sflib/pkg/coldp"
)

// This file translates between hive's internal domain types (coldp.* from
// sflib, which use sql.NullBool / sql.NullInt64 for optional columns) and
// clean HTTP wire types (pointer-optional fields that serialize as `null`
// when absent, plain values when present).
//
// A dedicated API layer here keeps sql-flavored types out of JSON responses
// and gives the WUI a predictable wire format. It also means we can evolve
// the API shape without touching core or forcing changes upstream in sflib.
//
// The types intentionally cover only what the read-only endpoints need in
// v0. Adding a field is one line here + one line in the corresponding
// converter — no upstream ripples.

// apiArchive summarizes an opened archive for GET /api/archive.
type apiArchive struct {
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version"`
	ReadOnly      bool   `json:"read_only"`
}

// apiMetadata is the wire form of the dataset metadata row. Fields
// mirror hive.Metadata but with wire-friendly JSON tags. Pointer-
// optional fields (Confidence / Completeness / Private) serialize as
// null when unset — matches how the DB stores NULL.
type apiMetadata struct {
	ID              int    `json:"id"`
	DOI             string `json:"doi,omitempty"`
	Title           string `json:"title"`
	Alias           string `json:"alias,omitempty"`
	Description     string `json:"description,omitempty"`
	Issued          string `json:"issued,omitempty"`
	Version         string `json:"version,omitempty"`
	Keywords        string `json:"keywords,omitempty"`
	GeographicScope string `json:"geographic_scope,omitempty"`
	TaxonomicScope  string `json:"taxonomic_scope,omitempty"`
	TemporalScope   string `json:"temporal_scope,omitempty"`
	Confidence      *int   `json:"confidence,omitempty"`
	Completeness    *int   `json:"completeness,omitempty"`
	License         string `json:"license,omitempty"`
	URL             string `json:"url,omitempty"`
	Logo            string `json:"logo,omitempty"`
	Label           string `json:"label,omitempty"`
	Citation        string `json:"citation,omitempty"`
	Private         *bool  `json:"private,omitempty"`

	// Warnings surfaces stored issues that apply to the metadata row
	// (e.g. hive_stale_metadata). Same shape as apiTaxon.Warnings.
	Warnings []apiValidationWarning `json:"warnings,omitempty"`
}

// apiMetadataPatch is the PATCH body. Pointer-optional throughout so
// "leave alone" is distinguishable from "clear" — same convention as
// apiTaxonPatch / apiNamePatch.
type apiMetadataPatch struct {
	DOI             *string `json:"doi,omitempty"`
	Title           *string `json:"title,omitempty"`
	Alias           *string `json:"alias,omitempty"`
	Description     *string `json:"description,omitempty"`
	Issued          *string `json:"issued,omitempty"`
	Version         *string `json:"version,omitempty"`
	Keywords        *string `json:"keywords,omitempty"`
	GeographicScope *string `json:"geographic_scope,omitempty"`
	TaxonomicScope  *string `json:"taxonomic_scope,omitempty"`
	TemporalScope   *string `json:"temporal_scope,omitempty"`
	Confidence      *int    `json:"confidence,omitempty"`
	Completeness    *int    `json:"completeness,omitempty"`
	License         *string `json:"license,omitempty"`
	URL             *string `json:"url,omitempty"`
	Logo            *string `json:"logo,omitempty"`
	Label           *string `json:"label,omitempty"`
	Citation        *string `json:"citation,omitempty"`
	Private         *bool   `json:"private,omitempty"`
}

func metadataToAPI(m *hive.Metadata) apiMetadata {
	return apiMetadata{
		ID:              m.ID,
		DOI:             m.DOI,
		Title:           m.Title,
		Alias:           m.Alias,
		Description:     m.Description,
		Issued:          m.Issued,
		Version:         m.Version,
		Keywords:        m.Keywords,
		GeographicScope: m.GeographicScope,
		TaxonomicScope:  m.TaxonomicScope,
		TemporalScope:   m.TemporalScope,
		Confidence:      m.Confidence,
		Completeness:    m.Completeness,
		License:         m.License,
		URL:             m.URL,
		Logo:            m.Logo,
		Label:           m.Label,
		Citation:        m.Citation,
		Private:         m.Private,
	}
}

// applyMetadataPatch mutates m in place per the patch. Same "absent
// key = leave alone, present value = set" semantics as applyTaxonPatch.
func applyMetadataPatch(m *hive.Metadata, p apiMetadataPatch) {
	if p.DOI != nil {
		m.DOI = *p.DOI
	}
	if p.Title != nil {
		m.Title = *p.Title
	}
	if p.Alias != nil {
		m.Alias = *p.Alias
	}
	if p.Description != nil {
		m.Description = *p.Description
	}
	if p.Issued != nil {
		m.Issued = *p.Issued
	}
	if p.Version != nil {
		m.Version = *p.Version
	}
	if p.Keywords != nil {
		m.Keywords = *p.Keywords
	}
	if p.GeographicScope != nil {
		m.GeographicScope = *p.GeographicScope
	}
	if p.TaxonomicScope != nil {
		m.TaxonomicScope = *p.TaxonomicScope
	}
	if p.TemporalScope != nil {
		m.TemporalScope = *p.TemporalScope
	}
	if p.Confidence != nil {
		v := *p.Confidence
		m.Confidence = &v
	}
	if p.Completeness != nil {
		v := *p.Completeness
		m.Completeness = &v
	}
	if p.License != nil {
		m.License = *p.License
	}
	if p.URL != nil {
		m.URL = *p.URL
	}
	if p.Logo != nil {
		m.Logo = *p.Logo
	}
	if p.Label != nil {
		m.Label = *p.Label
	}
	if p.Citation != nil {
		m.Citation = *p.Citation
	}
	if p.Private != nil {
		v := *p.Private
		m.Private = &v
	}
}

// apiTaxonHit is a tree-node projection used by list endpoints. Mirrors
// hive.TaxonHit but with wire-friendly JSON keys and pointer-optional
// fields. `label` is the server-rendered display (see hive.BuildLabel);
// front-ends use it for the tree row and search picker to keep styling
// consistent with the taxon detail view.
type apiTaxonHit struct {
	ID          string   `json:"id"`
	ParentID    string   `json:"parent_id,omitempty"`
	NameID      string   `json:"name_id,omitempty"`
	Name        string   `json:"name"`
	Authorship  string   `json:"authorship,omitempty"`
	Rank        string   `json:"rank,omitempty"`
	Status      string   `json:"status,omitempty"`
	Extinct     *bool    `json:"extinct,omitempty"`
	HasChildren bool     `json:"has_children"`
	Label       apiLabel `json:"label,omitzero"`
	// IsSynonym is set only when the search returned this row via a
	// synonym pointing at the accepted taxon (id). Elided when false
	// so responses from list endpoints (ListChildren, roots) stay
	// identical to the pre-synonyms wire shape.
	IsSynonym bool `json:"is_synonym,omitempty"`
	// MatchedName carries the text that actually satisfied the query.
	// Equals name for accepted-name matches; equals the synonym's
	// canonical for synonym matches. Elided when unset (list
	// endpoints leave it empty; front-ends fall back to name).
	MatchedName string `json:"matched_name,omitempty"`
	// Parent carries the immediate parent taxon's id + rendered
	// label (rank baked in via BuildLabel) so front-ends can
	// disambiguate homonyms — two accepted taxa sharing a canonical
	// — and give synonym rows a "you land under X" cue. Populated
	// only by SearchTaxa; list endpoints (children, roots) leave it
	// nil, which the JSON omitempty erases. Root-level taxa also
	// leave it nil.
	Parent *apiRef `json:"parent,omitempty"`
}

// apiNameHit is a name-search projection.
type apiNameHit struct {
	ID         string `json:"id"`
	Scientific string `json:"scientific"`
	Full       string `json:"full,omitempty"`
	Authorship string `json:"authorship,omitempty"`
	Rank       string `json:"rank,omitempty"`
	Code       string `json:"code,omitempty"`
	Status     string `json:"status,omitempty"`
}

// apiLabel is the wire form of a rendered display string — plain text
// plus HTML-styled variant. See hive.BuildLabel for the formatting rules;
// front-ends pick whichever form fits their medium (WUI renders `html`
// via unsafeHTML; TUI uses `text`).
type apiLabel struct {
	Text string `json:"text,omitempty"`
	HTML string `json:"html,omitempty"`
}

// apiRef is a lightweight pointer to another entity — id + rendered
// label. Used wherever a response would otherwise ship a raw UUID a
// curator can't read (parent taxon, according-to reference, name
// relations, ...). Nested objects keep the top-level API tidy even as
// more resolved references get added.
//
// Rank is populated only where callers need it for disambiguation
// (search hits' parent, currently); other uses leave it empty and the
// JSON omitempty erases it. BuildLabel's text form doesn't carry the
// rank, so front-ends that want a "Panthera (genus)" style hint read
// this field directly.
type apiRef struct {
	ID    string   `json:"id"`
	Label apiLabel `json:"label,omitzero"`
	Rank  string   `json:"rank,omitempty"`
}

// apiTaxon is the detail-view projection for GET /api/taxon/{id}. Covers
// the v0 read-only surface (identity + display + audit fields). Grow as
// needed. `label` is the taxon's own rendered display; `parent` is the
// resolved parent reference — both server-side joins so front-ends
// don't chase them separately.
type apiTaxon struct {
	ID              string   `json:"id"`
	Label           apiLabel `json:"label,omitzero"`
	ParentID        string   `json:"parent_id,omitempty"`
	Parent          *apiRef  `json:"parent,omitempty"`
	NameID          string   `json:"name_id,omitempty"`
	NamePhrase      string   `json:"name_phrase,omitempty"`
	Ordinal         *int     `json:"ordinal,omitempty"`
	Extinct         *bool    `json:"extinct,omitempty"`
	Status          string   `json:"status,omitempty"`
	Scrutinizer     string   `json:"scrutinizer,omitempty"`
	ScrutinizerID   string   `json:"scrutinizer_id,omitempty"`
	ScrutinizerDate string   `json:"scrutinizer_date,omitempty"`
	Link            string   `json:"link,omitempty"`
	Remarks         string   `json:"remarks,omitempty"`
	Modified        string   `json:"modified"`
	ModifiedBy      string   `json:"modified_by,omitempty"`
	// Soft warnings from gsvalidator, populated on create/update
	// responses. Empty / omitted on GET reads. See PLANNING.md §
	// Validation engine.
	Warnings []apiValidationWarning `json:"warnings,omitempty"`
}

// apiValidationWarning is the wire shape of a soft validation
// result — the rule id, human message, and which field triggered it
// (empty when the rule is record-level). Hard errors take a
// different shape (RFC 7807 problem), not this one.
type apiValidationWarning struct {
	RuleID    string `json:"rule_id"`
	RuleName  string `json:"rule_name,omitempty"`
	FieldName string `json:"field_name,omitempty"`
	Severity  string `json:"severity,omitempty"` // warn | info | debug
	Message   string `json:"message"`
}

// apiName is the detail-view projection for GET /api/name/{id}. Field set
// matches what apiNamePatch accepts so a caller can round-trip GET→edit→
// PATCH without discovering that some field is writable but not readable.
type apiName struct {
	ID                   string `json:"id"`
	ScientificName       string `json:"scientific_name"`
	ScientificNameString string `json:"scientific_name_string"`
	Authorship           string `json:"authorship,omitempty"`
	Rank                 string `json:"rank,omitempty"`
	Code                 string `json:"code,omitempty"`
	Status               string `json:"status,omitempty"`
	Gender               string `json:"gender,omitempty"`
	Notho                string `json:"notho,omitempty"`

	// Structural pieces the curator can edit.
	Uninomial            string `json:"uninomial,omitempty"`
	Genus                string `json:"genus,omitempty"`
	InfragenericEpithet  string `json:"infrageneric_epithet,omitempty"`
	SpecificEpithet      string `json:"specific_epithet,omitempty"`
	InfraspecificEpithet string `json:"infraspecific_epithet,omitempty"`
	CultivarEpithet      string `json:"cultivar_epithet,omitempty"`

	// Atomized authorship — gnparser's split of the raw Authorship
	// field into original-vs-recombiner components. Populated on write
	// via core's fill-from-parse (respects caller-supplied overrides).
	// Zoology: BasionymAuthorship holds the parenthetical author of a
	// recombined name (e.g., "Linnaeus" for "Panthera onca (Linnaeus, 1758)");
	// CombinationAuthorship holds the recombiner when cited. Botany/
	// microbiology: BasionymAuthorship holds the protologue author of
	// the underlying basionym.
	CombinationAuthorship     string `json:"combination_authorship,omitempty"`
	CombinationExAuthorship   string `json:"combination_ex_authorship,omitempty"`
	CombinationAuthorshipYear string `json:"combination_authorship_year,omitempty"`
	BasionymAuthorship        string `json:"basionym_authorship,omitempty"`
	BasionymExAuthorship      string `json:"basionym_ex_authorship,omitempty"`
	BasionymAuthorshipYear    string `json:"basionym_authorship_year,omitempty"`

	// gn__ cache. Exposed but marked so the WUI can hide them behind a
	// "parse details" affordance rather than treat them as editable.
	CanonicalSimple  string `json:"canonical_simple,omitempty"`
	CanonicalFull    string `json:"canonical_full,omitempty"`
	CanonicalStemmed string `json:"canonical_stemmed,omitempty"`
	ParseQuality     *int   `json:"parse_quality,omitempty"`
	Cardinality      *int   `json:"cardinality,omitempty"`
	Authors          string `json:"authors,omitempty"`
	GnID             string `json:"gn_id,omitempty"`

	// Basionym is the linked basionym / original combination — the
	// separate Name row this name derives from via a name_relation of
	// type BASIONYM. Populated on read when the linkage exists.
	// Curator-facing "Basionym: <label>" row on the taxon detail
	// pane resolves through this field so the display stays in sync
	// with the actual name_relation (as opposed to just the atomized
	// col__basionym_authorship string, which lives on this name and
	// isn't necessarily linked to a separate row).
	Basionym *apiRef `json:"basionym,omitempty"`

	// ReferenceID is the "published in" reference — sfga stores it as
	// a comma-separated list of ids (name can be cited in multiple
	// refs) but hive v1 treats it as a single value. Legacy multi-
	// value strings surface as the first-entry ID; edits write one
	// ID back. Multi-reference support tracked as a later slice.
	ReferenceID         string `json:"reference_id,omitempty"`
	// ReferenceLabel is a server-resolved display for ReferenceID so
	// front-ends don't have to fetch the reference separately.
	// "Author (Year) Title" per hive convention. Empty when the ID
	// doesn't resolve.
	ReferenceLabel      string `json:"reference_label,omitempty"`
	PublishedInYear     string `json:"published_in_year,omitempty"`
	PublishedInPage     string `json:"published_in_page,omitempty"`
	PublishedInPageLink string `json:"published_in_page_link,omitempty"`
	Etymology           string `json:"etymology,omitempty"`
	Link                string `json:"link,omitempty"`

	Remarks    string `json:"remarks,omitempty"`
	Modified   string `json:"modified"`
	ModifiedBy string `json:"modified_by,omitempty"`
}

// apiSynonym is the wire form of a synonym link. `label` is a server-
// rendered display (text + html) for the associated name so front-ends
// don't have to re-fetch each name row — matches the shape used for
// apiTaxon and any other resolved reference. See hive.BuildLabel.
type apiSynonym struct {
	ID         string   `json:"id,omitempty"`
	TaxonID    string   `json:"taxon_id"`
	NameID     string   `json:"name_id"`
	Label      apiLabel `json:"label,omitzero"`
	NamePhrase string   `json:"name_phrase,omitempty"`
	Status     string   `json:"status,omitempty"`
	Link       string   `json:"link,omitempty"`
	Remarks    string   `json:"remarks,omitempty"`
	Modified   string   `json:"modified,omitempty"`
	ModifiedBy string   `json:"modified_by,omitempty"`
}

// apiReferenceHit is the list/search projection for references. Lean
// fields so a table of hits stays fast to render; full detail comes
// from GET /api/reference/{id}.
type apiReferenceHit struct {
	ID       string `json:"id"`
	Author   string `json:"author,omitempty"`
	Year     string `json:"year,omitempty"`
	Title    string `json:"title,omitempty"`
	Citation string `json:"citation,omitempty"`
	Type     string `json:"type,omitempty"`
}

// apiReference is the detail projection for a single reference.
// Mirrors the sfga reference table; every column present, since
// citation-building UIs often need the structured fields even when
// col__citation is populated.
type apiReference struct {
	ID                  string `json:"id"`
	AlternativeID       string `json:"alternative_id,omitempty"`
	SourceID            string `json:"source_id,omitempty"`
	Citation            string `json:"citation,omitempty"`
	Type                string `json:"type,omitempty"`
	Author              string `json:"author,omitempty"`
	AuthorID            string `json:"author_id,omitempty"`
	Editor              string `json:"editor,omitempty"`
	EditorID            string `json:"editor_id,omitempty"`
	Title               string `json:"title,omitempty"`
	TitleShort          string `json:"title_short,omitempty"`
	ContainerAuthor     string `json:"container_author,omitempty"`
	ContainerTitle      string `json:"container_title,omitempty"`
	ContainerTitleShort string `json:"container_title_short,omitempty"`
	Issued              string `json:"issued,omitempty"`
	Accessed            string `json:"accessed,omitempty"`
	CollectionTitle     string `json:"collection_title,omitempty"`
	CollectionEditor    string `json:"collection_editor,omitempty"`
	Volume              string `json:"volume,omitempty"`
	Issue               string `json:"issue,omitempty"`
	Edition             string `json:"edition,omitempty"`
	Page                string `json:"page,omitempty"`
	Publisher           string `json:"publisher,omitempty"`
	PublisherPlace      string `json:"publisher_place,omitempty"`
	Version             string `json:"version,omitempty"`
	ISBN                string `json:"isbn,omitempty"`
	ISSN                string `json:"issn,omitempty"`
	DOI                 string `json:"doi,omitempty"`
	Link                string `json:"link,omitempty"`
	Remarks             string `json:"remarks,omitempty"`
	Modified            string `json:"modified,omitempty"`
	ModifiedBy          string `json:"modified_by,omitempty"`
}

func referenceHitToAPI(h hive.ReferenceHit) apiReferenceHit {
	return apiReferenceHit{
		ID:       h.ID,
		Author:   h.Author,
		Year:     h.Year,
		Title:    h.Title,
		Citation: h.Citation,
		Type:     h.Type,
	}
}

func referenceToAPI(r *coldp.Reference) apiReference {
	return apiReference{
		ID:                  r.ID,
		AlternativeID:       r.AlternativeID,
		SourceID:            r.SourceID,
		Citation:            r.Citation,
		Type:                r.Type.ID(),
		Author:              r.Author,
		AuthorID:            r.AuthorID,
		Editor:              r.Editor,
		EditorID:            r.EditorID,
		Title:               r.Title,
		TitleShort:          r.TitleShort,
		ContainerAuthor:     r.ContainerAuthor,
		ContainerTitle:      r.ContainerTitle,
		ContainerTitleShort: r.ContainerTitleShort,
		Issued:              r.Issued,
		Accessed:            r.Accessed,
		CollectionTitle:     r.CollectionTitle,
		CollectionEditor:    r.CollectionEditor,
		Volume:              r.Volume,
		Issue:               r.Issue,
		Edition:             r.Edition,
		Page:                r.Page,
		Publisher:           r.Publisher,
		PublisherPlace:      r.PublisherPlace,
		Version:             r.Version,
		ISBN:                r.ISBN,
		ISSN:                r.ISSN,
		DOI:                 r.DOI,
		Link:                r.Link,
		Remarks:             r.Remarks,
		Modified:            r.Modified,
		ModifiedBy:          r.ModifiedBy,
	}
}

// createTaxonBody is the wire form for POST /api/taxon. Mirrors the
// atomized-preview fields the name-add modal collects: scientific_name +
// code are required, every other name field is optional and — when
// omitted — gets filled from gnparser by hive.CreateName's fill-from-
// parse pipeline. parent_id and name_phrase land on the taxon row.
//
// Kept intentionally symmetric with apiName: a caller can POST the same
// JSON shape they GET back on /api/name/{id}, minus the read-only
// fields (id / modified / modified_by / gn__* cache — those are
// server-authored on write and ignored if the client sends them).
type createTaxonBody struct {
	// Name aggregate (verbatim + code required; rest optional).
	ScientificName            string `json:"scientific_name"`
	ScientificNameString      string `json:"scientific_name_string,omitempty"`
	Authorship                string `json:"authorship,omitempty"`
	Rank                      string `json:"rank,omitempty"`
	Code                      string `json:"code"`
	Status                    string `json:"status,omitempty"`
	Gender                    string `json:"gender,omitempty"`
	Notho                     string `json:"notho,omitempty"`
	Uninomial                 string `json:"uninomial,omitempty"`
	Genus                     string `json:"genus,omitempty"`
	InfragenericEpithet       string `json:"infrageneric_epithet,omitempty"`
	SpecificEpithet           string `json:"specific_epithet,omitempty"`
	InfraspecificEpithet      string `json:"infraspecific_epithet,omitempty"`
	CultivarEpithet           string `json:"cultivar_epithet,omitempty"`
	CombinationAuthorship     string `json:"combination_authorship,omitempty"`
	CombinationExAuthorship   string `json:"combination_ex_authorship,omitempty"`
	CombinationAuthorshipYear string `json:"combination_authorship_year,omitempty"`
	BasionymAuthorship        string `json:"basionym_authorship,omitempty"`
	BasionymExAuthorship      string `json:"basionym_ex_authorship,omitempty"`
	BasionymAuthorshipYear    string `json:"basionym_authorship_year,omitempty"`
	ReferenceID               string `json:"reference_id,omitempty"`
	PublishedInYear           string `json:"published_in_year,omitempty"`
	PublishedInPage           string `json:"published_in_page,omitempty"`
	PublishedInPageLink       string `json:"published_in_page_link,omitempty"`
	Etymology                 string `json:"etymology,omitempty"`
	Link                      string `json:"link,omitempty"`
	Remarks                   string `json:"remarks,omitempty"`

	// Taxon aggregate.
	ParentID   string `json:"parent_id,omitempty"`
	NamePhrase string `json:"name_phrase,omitempty"`
}

// toColdpName packs the name-side fields into a coldp.Name ready for
// hive.CreateName. Enum fields go through the empty-safe helpers so
// unset values stay NULL in the DB (rather than being coerced to a
// zero enum value like UNRANKED). Structural col__ fields not sent by
// the client stay empty on the struct and are filled by CreateName's
// fill-from-parse pass.
func (b createTaxonBody) toColdpName() coldp.Name {
	return coldp.Name{
		ScientificName:            b.ScientificName,
		ScientificNameString:      b.ScientificNameString,
		Authorship:                b.Authorship,
		Rank:                      hive.ParseRank(b.Rank),
		Code:                      nomcode.New(b.Code),
		Status:                    hive.ParseNomStatus(b.Status),
		Gender:                    coldp.NewGender(b.Gender),
		Notho:                     coldp.NewNamePart(b.Notho),
		Uninomial:                 b.Uninomial,
		Genus:                     b.Genus,
		InfragenericEpithet:       b.InfragenericEpithet,
		SpecificEpithet:           b.SpecificEpithet,
		InfraspecificEpithet:      b.InfraspecificEpithet,
		CultivarEpithet:           b.CultivarEpithet,
		CombinationAuthorship:     b.CombinationAuthorship,
		CombinationExAuthorship:   b.CombinationExAuthorship,
		CombinationAuthorshipYear: b.CombinationAuthorshipYear,
		BasionymAuthorship:        b.BasionymAuthorship,
		BasionymExAuthorship:      b.BasionymExAuthorship,
		BasionymAuthorshipYear:    b.BasionymAuthorshipYear,
		ReferenceID:               b.ReferenceID,
		PublishedInYear:           b.PublishedInYear,
		PublishedInPage:           b.PublishedInPage,
		PublishedInPageLink:       b.PublishedInPageLink,
		Etymology:                 b.Etymology,
		Link:                      b.Link,
		Remarks:                   b.Remarks,
	}
}

// apiReferenceToColdp is the reverse translator, used by POST endpoints
// that accept an apiReference-shaped body (create-from-modal, and the
// preview responses parsed back into a coldp.Reference before save).
func apiReferenceToColdp(a apiReference) coldp.Reference {
	return coldp.Reference{
		ID:                  a.ID,
		AlternativeID:       a.AlternativeID,
		SourceID:            a.SourceID,
		Citation:            a.Citation,
		Type:                coldp.NewReferenceType(a.Type),
		Author:              a.Author,
		AuthorID:            a.AuthorID,
		Editor:              a.Editor,
		EditorID:            a.EditorID,
		Title:               a.Title,
		TitleShort:          a.TitleShort,
		ContainerAuthor:     a.ContainerAuthor,
		ContainerTitle:      a.ContainerTitle,
		ContainerTitleShort: a.ContainerTitleShort,
		Issued:              a.Issued,
		Accessed:            a.Accessed,
		CollectionTitle:     a.CollectionTitle,
		CollectionEditor:    a.CollectionEditor,
		Volume:              a.Volume,
		Issue:               a.Issue,
		Edition:             a.Edition,
		Page:                a.Page,
		Publisher:           a.Publisher,
		PublisherPlace:      a.PublisherPlace,
		Version:             a.Version,
		ISBN:                a.ISBN,
		ISSN:                a.ISSN,
		DOI:                 a.DOI,
		Link:                a.Link,
		Remarks:             a.Remarks,
	}
}

// apiBHLnameHit is the wire shape for one BHLnames match. Reference is
// an unsaved preview the picker can hand straight into POST /api/reference
// on the "add this one" click. Quality (1-5) and Score are surface
// signals for sorting / annotating in the modal.
type apiBHLnameHit struct {
	Reference   apiReference `json:"reference"`
	Quality     int          `json:"quality"`
	Score       int          `json:"score"`
	PageURL     string       `json:"page_url,omitempty"`
	PageID      int          `json:"page_id,omitempty"`
	MatchedName string       `json:"matched_name,omitempty"`
}

func bhlnameHitToAPI(h hive.BHLnameHit) apiBHLnameHit {
	return apiBHLnameHit{
		Reference:   referenceToAPI(&h.Reference),
		Quality:     h.Quality,
		Score:       h.Score,
		PageURL:     h.PageURL,
		PageID:      h.PageID,
		MatchedName: h.MatchedName,
	}
}

// apiPage envelopes a paginated result set. Only lists use this; single
// resources return the object directly (per CLAUDE.md § HTTP API shape).
type apiPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      *int   `json:"total,omitempty"`
}

// ---------- patch types ----------
//
// PATCH request bodies use pointer-optional fields with these semantics:
//   * absent key or JSON null → leave the current value untouched
//   * present string with value "" → clear the field
//   * present string with a value → set the field
//   * present *bool → set the field to that value (no way to clear to
//     unknown/null yet; a v0 limitation. When we need it we'll switch that
//     field to a tri-state wrapper)
//
// Note that Go's encoding/json makes absent vs. explicit-null on a *T field
// indistinguishable at the type level. Callers wanting to distinguish
// "clear" from "leave alone" for a string field use "" for clear (the
// idiomatic convention). Distinguishing null-clear from omitted-leave-alone
// would require json.RawMessage per field; not worth the complexity in v0.

// apiTaxonPatch is the wire body for PATCH /api/taxon/{id}. Editable fields
// only — parent moves go through /api/taxon/{id}/move, denormalized
// classification is never editable, and the audit stamps (modified /
// modified_by) are set server-side from the request context.
type apiTaxonPatch struct {
	NamePhrase          *string `json:"name_phrase,omitempty"`
	AccordingToPage     *string `json:"according_to_page,omitempty"`
	AccordingToPageLink *string `json:"according_to_page_link,omitempty"`
	Scrutinizer         *string `json:"scrutinizer,omitempty"`
	ScrutinizerID       *string `json:"scrutinizer_id,omitempty"`
	ScrutinizerDate     *string `json:"scrutinizer_date,omitempty"`
	Extinct             *bool   `json:"extinct,omitempty"`
	Link                *string `json:"link,omitempty"`
	Remarks             *string `json:"remarks,omitempty"`
}

// applyTaxonPatch mutates t in place per the patch. Zero-length patches are
// no-ops; the caller is responsible for detecting that case if it wants to
// short-circuit the whole tx.
func applyTaxonPatch(t *coldp.Taxon, p apiTaxonPatch) {
	if p.NamePhrase != nil {
		t.NamePhrase = *p.NamePhrase
	}
	if p.AccordingToPage != nil {
		t.AccordingToPage = *p.AccordingToPage
	}
	if p.AccordingToPageLink != nil {
		t.AccordingToPageLink = *p.AccordingToPageLink
	}
	if p.Scrutinizer != nil {
		t.Scrutinizer = *p.Scrutinizer
	}
	if p.ScrutinizerID != nil {
		t.ScrutinizerID = *p.ScrutinizerID
	}
	if p.ScrutinizerDate != nil {
		t.ScrutinizerDate = *p.ScrutinizerDate
	}
	if p.Extinct != nil {
		t.Extinct = sql.NullBool{Bool: *p.Extinct, Valid: true}
	}
	if p.Link != nil {
		t.Link = *p.Link
	}
	if p.Remarks != nil {
		t.Remarks = *p.Remarks
	}
}

// apiNamePatch is the wire body for PATCH /api/name/{id}. Structural
// gn__* fields are not accepted — they are cache values regenerated by
// gnparser on every write (per CLAUDE.md § gn__* column policy).
type apiNamePatch struct {
	ScientificName       *string `json:"scientific_name,omitempty"`
	ScientificNameString *string `json:"scientific_name_string,omitempty"`
	Authorship           *string `json:"authorship,omitempty"`
	// Rank / Code / Status / Gender are typed enums; they arrive as raw
	// enum-ID strings (RANK_KEY, ZOOLOGICAL, ACCEPTED, MASCULINE, …).
	Rank    *string `json:"rank,omitempty"`
	Code    *string `json:"code,omitempty"`
	Status  *string `json:"status,omitempty"`
	Gender  *string `json:"gender,omitempty"`
	Notho   *string `json:"notho,omitempty"`

	// Structural pieces the curator edits.
	Uninomial            *string `json:"uninomial,omitempty"`
	Genus                *string `json:"genus,omitempty"`
	InfragenericEpithet  *string `json:"infrageneric_epithet,omitempty"`
	SpecificEpithet      *string `json:"specific_epithet,omitempty"`
	InfraspecificEpithet *string `json:"infraspecific_epithet,omitempty"`
	CultivarEpithet      *string `json:"cultivar_epithet,omitempty"`

	// Atomized authorship — the CoLDP split of the raw Authorship
	// string. See apiName's field-level comments for the code-specific
	// semantics; both pointers accept empty-string to clear the slot.
	CombinationAuthorship     *string `json:"combination_authorship,omitempty"`
	CombinationExAuthorship   *string `json:"combination_ex_authorship,omitempty"`
	CombinationAuthorshipYear *string `json:"combination_authorship_year,omitempty"`
	BasionymAuthorship        *string `json:"basionym_authorship,omitempty"`
	BasionymExAuthorship      *string `json:"basionym_ex_authorship,omitempty"`
	BasionymAuthorshipYear    *string `json:"basionym_authorship_year,omitempty"`

	// ReferenceID is the "published in" reference ID (v1: single value
	// stored into sfga's comma-separated col__reference_id). Sending
	// "" clears the link.
	ReferenceID         *string `json:"reference_id,omitempty"`
	PublishedInYear     *string `json:"published_in_year,omitempty"`
	PublishedInPage     *string `json:"published_in_page,omitempty"`
	PublishedInPageLink *string `json:"published_in_page_link,omitempty"`
	Etymology           *string `json:"etymology,omitempty"`
	Link                *string `json:"link,omitempty"`
	Remarks             *string `json:"remarks,omitempty"`
}

// applyNamePatch mutates n in place. gn__* fields are NOT touched — they
// are regenerated by CreateName/UpdateName from the (possibly patched)
// verbatim name string.
func applyNamePatch(n *coldp.Name, p apiNamePatch) {
	if p.ScientificName != nil {
		n.ScientificName = *p.ScientificName
	}
	if p.ScientificNameString != nil {
		n.ScientificNameString = *p.ScientificNameString
	}
	if p.Authorship != nil {
		n.Authorship = *p.Authorship
	}
	if p.Rank != nil {
		n.Rank = hive.ParseRank(*p.Rank)
	}
	if p.Code != nil {
		n.Code = nomcode.New(*p.Code)
	}
	if p.Status != nil {
		n.Status = hive.ParseNomStatus(*p.Status)
	}
	if p.Gender != nil {
		n.Gender = coldp.NewGender(*p.Gender)
	}
	if p.Notho != nil {
		n.Notho = coldp.NewNamePart(*p.Notho)
	}
	if p.Uninomial != nil {
		n.Uninomial = *p.Uninomial
	}
	if p.Genus != nil {
		n.Genus = *p.Genus
	}
	if p.InfragenericEpithet != nil {
		n.InfragenericEpithet = *p.InfragenericEpithet
	}
	if p.SpecificEpithet != nil {
		n.SpecificEpithet = *p.SpecificEpithet
	}
	if p.InfraspecificEpithet != nil {
		n.InfraspecificEpithet = *p.InfraspecificEpithet
	}
	if p.CultivarEpithet != nil {
		n.CultivarEpithet = *p.CultivarEpithet
	}
	if p.CombinationAuthorship != nil {
		n.CombinationAuthorship = *p.CombinationAuthorship
	}
	if p.CombinationExAuthorship != nil {
		n.CombinationExAuthorship = *p.CombinationExAuthorship
	}
	if p.CombinationAuthorshipYear != nil {
		n.CombinationAuthorshipYear = *p.CombinationAuthorshipYear
	}
	if p.BasionymAuthorship != nil {
		n.BasionymAuthorship = *p.BasionymAuthorship
	}
	if p.BasionymExAuthorship != nil {
		n.BasionymExAuthorship = *p.BasionymExAuthorship
	}
	if p.BasionymAuthorshipYear != nil {
		n.BasionymAuthorshipYear = *p.BasionymAuthorshipYear
	}
	if p.ReferenceID != nil {
		// name.col__reference_id is a comma-separated list in sfga;
		// hive v1 writes a single value into it. Empty string clears
		// the link.
		n.ReferenceID = *p.ReferenceID
	}
	if p.PublishedInYear != nil {
		n.PublishedInYear = *p.PublishedInYear
	}
	if p.PublishedInPage != nil {
		n.PublishedInPage = *p.PublishedInPage
	}
	if p.PublishedInPageLink != nil {
		n.PublishedInPageLink = *p.PublishedInPageLink
	}
	if p.Etymology != nil {
		n.Etymology = *p.Etymology
	}
	if p.Link != nil {
		n.Link = *p.Link
	}
	if p.Remarks != nil {
		n.Remarks = *p.Remarks
	}
}

// ---------- converters ----------

func hitToAPI(h hive.TaxonHit) apiTaxonHit {
	out := apiTaxonHit{
		ID:          h.ID,
		ParentID:    h.ParentID,
		NameID:      h.NameID,
		Name:        h.Name,
		Authorship:  h.Authorship,
		Rank:        h.Rank,
		Status:      h.Status,
		Extinct:     nullBoolToPtr(h.Extinct),
		HasChildren: h.HasChildren,
		Label:       apiLabel{Text: h.Label.Text, HTML: h.Label.HTML},
		IsSynonym:   h.IsSynonym,
		MatchedName: h.MatchedName,
	}
	// Parent context is search-only. List endpoints leave ParentName
	// empty so the wire object stays nil (omitempty drops the field).
	if h.ParentName != "" {
		out.Parent = &apiRef{
			ID: h.ParentID,
			Label: apiLabel{
				Text: h.ParentLabel.Text,
				HTML: h.ParentLabel.HTML,
			},
			Rank: h.ParentRank,
		}
	}
	return out
}

func nameHitToAPI(h hive.NameHit) apiNameHit {
	return apiNameHit{
		ID:         h.ID,
		Scientific: h.Scientific,
		Full:       h.Full,
		Authorship: h.Authorship,
		Rank:       h.Rank,
		Code:       h.Code,
		Status:     h.Status,
	}
}

func taxonToAPI(t *coldp.Taxon) apiTaxon {
	// A synthetic "status" field mirrors what the TUI detail pane derives:
	// hive stores the raw taxonomic_status ID (ACCEPTED, PROVISIONALLY_*)
	// but coldp.Taxon only exposes the Provisional bool. For a richer read
	// we'd add an explicit status field on coldp.Taxon or query it directly
	// via SQL; v0 sends "provisionally accepted" or "accepted".
	status := "accepted"
	if t.Provisional.Valid && t.Provisional.Bool {
		status = "provisionally accepted"
	}
	return apiTaxon{
		ID:              t.ID,
		ParentID:        t.ParentID,
		NameID:          t.NameID,
		NamePhrase:      t.NamePhrase,
		Ordinal:         nullInt64ToPtr(t.Ordinal),
		Extinct:         nullBoolToPtr(t.Extinct),
		Status:          status,
		Scrutinizer:     t.Scrutinizer,
		ScrutinizerID:   t.ScrutinizerID,
		ScrutinizerDate: t.ScrutinizerDate,
		Link:            t.Link,
		Remarks:         t.Remarks,
		Modified:        t.Modified,
		ModifiedBy:      t.ModifiedBy,
	}
}

// statusOf resolves the raw col__status_id — the NOMEN URI when hive
// last read it (via hive.NameRawStatus), falling back to the enum's
// stringified form for legacy CoLDP-generalized values. See
// pkg/nomen.go for the sidecar cache.
func statusOf(n *coldp.Name) string {
	if raw := hive.NameRawStatus(n.ID); raw != "" {
		return raw
	}
	return n.Status.ID()
}

func nameToAPI(n *coldp.Name) apiName {
	return apiName{
		ID:                   n.ID,
		ScientificName:       n.ScientificName,
		ScientificNameString: n.ScientificNameString,
		Authorship:           n.Authorship,
		Rank:                 n.Rank.ID(),
		Code:                 n.Code.ID(),
		Status:               statusOf(n),
		Gender:               n.Gender.ID(),
		Notho:                n.Notho.ID(),
		Uninomial:                 n.Uninomial,
		Genus:                     n.Genus,
		InfragenericEpithet:       n.InfragenericEpithet,
		SpecificEpithet:           n.SpecificEpithet,
		InfraspecificEpithet:      n.InfraspecificEpithet,
		CultivarEpithet:           n.CultivarEpithet,
		CombinationAuthorship:     n.CombinationAuthorship,
		CombinationExAuthorship:   n.CombinationExAuthorship,
		CombinationAuthorshipYear: n.CombinationAuthorshipYear,
		BasionymAuthorship:        n.BasionymAuthorship,
		BasionymExAuthorship:      n.BasionymExAuthorship,
		BasionymAuthorshipYear:    n.BasionymAuthorshipYear,
		CanonicalSimple:      n.CanonicalSimple,
		CanonicalFull:        n.CanonicalFull,
		CanonicalStemmed:     n.CanonicalStemmed,
		ParseQuality:         nullInt64ToPtr(n.ParseQuality),
		Cardinality:          nullInt64ToPtr(n.Cardinality),
		Authors:              n.Authors,
		GnID:                 n.GnID,
		ReferenceID:          hive.PrimaryReferenceID(n.ReferenceID),
		PublishedInYear:      n.PublishedInYear,
		PublishedInPage:      n.PublishedInPage,
		PublishedInPageLink:  n.PublishedInPageLink,
		Etymology:            n.Etymology,
		Link:                 n.Link,
		Remarks:              n.Remarks,
		Modified:             n.Modified,
		ModifiedBy:           n.ModifiedBy,
	}
}

func synonymToAPI(s coldp.Synonym) apiSynonym {
	return apiSynonym{
		ID:         s.ID,
		TaxonID:    s.TaxonID,
		NameID:     s.NameID,
		NamePhrase: s.NamePhrase,
		Status:     s.Status.ID(),
		Link:       s.Link,
		Remarks:    s.Remarks,
		Modified:   s.Modified,
		ModifiedBy: s.ModifiedBy,
	}
}

// synonymHitToAPI converts a hive.SynonymHit (with pre-built Label) into
// the wire form used by GET /api/taxon/{id}/synonyms. Preferred over
// synonymToAPI when the handler has already gone through ListSynonymHits.
func synonymHitToAPI(h hive.SynonymHit) apiSynonym {
	return apiSynonym{
		ID:         h.ID,
		TaxonID:    h.TaxonID,
		NameID:     h.NameID,
		Label:      apiLabel{Text: h.Label.Text, HTML: h.Label.HTML},
		NamePhrase: h.NamePhrase,
		Status:     h.Status,
		Link:       h.Link,
		Remarks:    h.Remarks,
		Modified:   h.Modified,
		ModifiedBy: h.ModifiedBy,
	}
}

// nullBoolToPtr converts sql.NullBool → *bool. Invalid → nil (omitted from
// JSON via omitempty). Valid → &bool.
func nullBoolToPtr(nb sql.NullBool) *bool {
	if !nb.Valid {
		return nil
	}
	v := nb.Bool
	return &v
}

// nullInt64ToPtr converts sql.NullInt64 → *int. Invalid → nil. Downcast to
// int is safe for hive's counts and ordinals; sfga integer columns are
// bounded well under int32 in practice.
func nullInt64ToPtr(ni sql.NullInt64) *int {
	if !ni.Valid {
		return nil
	}
	v := int(ni.Int64)
	return &v
}
