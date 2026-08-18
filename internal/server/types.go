package server

import (
	"database/sql"
	"strconv"

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

	// Tail is the unparseable trailing text gnparser rejected. Populated
	// only on the /api/name/parse response — persisted name rows never
	// carry a tail (gn__* is a cache; tail is re-derivable via re-parse).
	// Empty for clean parses; front-ends render a soft-warning banner
	// when non-empty so curators see exactly where the parse gave up.
	Tail string `json:"tail,omitempty"`
}

// apiNomenHistory is the wire projection of a taxon's basionym-
// anchored nomenclatural history — one cluster per original
// combination, each an ordered list of the basionym plus its
// recombinations. See hive.NomenclaturalHistory for the assembly
// semantics.
type apiNomenHistory struct {
	Clusters []apiNomenCluster `json:"clusters"`
}

type apiNomenCluster struct {
	// Role is "accepted" when the cluster contains the taxon's
	// currently accepted name, "synonym" otherwise. Exactly one
	// "accepted" cluster per response (empty when the taxon has
	// no accepted name row — pathological but tolerated).
	Role  string         `json:"role"`
	Names []apiNomenName `json:"names"`
}

type apiNomenName struct {
	// NameID is the id of the name row this entry stands for.
	// Front-ends navigate to /api/name/{id} for the full detail.
	NameID string `json:"name_id"`
	// Label is the server-rendered display (text + italicized HTML).
	Label apiLabel `json:"label,omitzero"`
	// Authorship / Rank / Year are surfaced independently so the
	// front-end can compose alternate renderings (chronological
	// gutter, rank badge, etc.) without re-parsing Label.
	Authorship string `json:"authorship,omitempty"`
	Rank       string `json:"rank,omitempty"`
	Year       string `json:"year,omitempty"`
	// IsBasionym is true for the anchor name of the cluster —
	// exactly one per cluster (the original combination).
	IsBasionym bool `json:"is_basionym,omitempty"`
	// Involvement records how this name relates to the taxon being
	// viewed: "accepted", "synonym", or "unlinked" (a sibling
	// recombination discovered via name_relation but not currently
	// attached to this taxon).
	Involvement string `json:"involvement"`
	// SynonymID is populated when Involvement == "synonym" — the
	// synonym-link row's own id (may be empty on legacy archives
	// where synonym.col__id wasn't set).
	SynonymID string `json:"synonym_id,omitempty"`
	// ReferenceID / ReferenceLabel — the name's own publication
	// citation (from name.col__reference_id), resolved to a display
	// string by the handler for the footnote-accumulator flow.
	// Independent from any synonym-link reference (that lives on
	// apiSynonym).
	ReferenceID    string `json:"reference_id,omitempty"`
	ReferenceLabel string `json:"reference_label,omitempty"`
	// Atomized authorship — surfaced so the WUI's "Standardized
	// authorship" toggle can compose the hybrid render (basionym
	// author+year in parens, then combination author+year) that
	// bridges ICZN and ICN conventions without dropping either
	// side's information. Absent when the underlying column is
	// empty — the toggle falls back to whichever pieces exist.
	BasionymAuthorship        string `json:"basionym_authorship,omitempty"`
	BasionymAuthorshipYear    string `json:"basionym_authorship_year,omitempty"`
	CombinationAuthorship     string `json:"combination_authorship,omitempty"`
	CombinationAuthorshipYear string `json:"combination_authorship_year,omitempty"`
	// IssueCount is the number of open validation issues currently
	// filed against this name row (table_name='name', not-yet-
	// acknowledged). Zero → elided from JSON so the wire shape stays
	// flat for the common case; front-ends render a warn icon on
	// Nomenclatural-history rows when > 0.
	IssueCount int `json:"issue_count,omitempty"`
	// MaxSeverity is the highest severity among the row's open
	// issues ("error" / "warn" / "info" / "debug"). Empty when
	// IssueCount is 0. Drives the WUI badge color via the shared
	// validationSeverityBadge helper.
	MaxSeverity string `json:"max_severity,omitempty"`
}

// apiVernacular is the wire form of a vernacular-name row on a
// taxon. sfga's vernacular table has no col__id; hive uses SQLite's
// implicit rowid as the opaque handle and ships it as `id` (a
// string per the "all ids are strings in JSON" rule). Curators
// PATCH/DELETE against /api/vernacular/{id}.
//
// Preferred is a *bool because sfga distinguishes NULL (unknown /
// unset) from false (explicitly-not-preferred). Frontends that
// only expose a checkbox render NULL as unchecked; explicit false
// still round-trips.
type apiVernacular struct {
	ID              string `json:"id"`
	TaxonID         string `json:"taxon_id"`
	Name            string `json:"name"`
	Transliteration string `json:"transliteration,omitempty"`
	Language        string `json:"language,omitempty"`
	Preferred       *bool  `json:"preferred,omitempty"`
	Country         string `json:"country,omitempty"`
	Area            string `json:"area,omitempty"`
	Sex             string `json:"sex,omitempty"`
	SourceID        string `json:"source_id,omitempty"`
	ReferenceID     string `json:"reference_id,omitempty"`
	Remarks         string `json:"remarks,omitempty"`
	Modified        string `json:"modified,omitempty"`
	ModifiedBy      string `json:"modified_by,omitempty"`
	// IssueCount is the number of open validation issues currently
	// filed against this vernacular row (table_name='vernacular',
	// not-yet-acknowledged). Zero → elided from JSON. Front-ends
	// render a warn icon on the row when > 0.
	IssueCount int `json:"issue_count,omitempty"`
	// MaxSeverity is the highest severity present among the row's
	// open issues ("error", "warn", "info", "debug"). Empty when
	// IssueCount is 0; elided from JSON in that case. Drives the
	// badge color the WUI paints on the row.
	MaxSeverity string `json:"max_severity,omitempty"`
}

// apiVernacularPatch mirrors apiVernacular but keeps every editable
// field as a pointer so a curator can send only what changed. Nil
// pointer means "leave alone"; a set pointer to zero-value means
// "clear this field" (empty string / false). TaxonID isn't included
// — reparenting a vernacular is delete+add on a different taxon.
type apiVernacularPatch struct {
	Name            *string `json:"name,omitempty"`
	Transliteration *string `json:"transliteration,omitempty"`
	Language        *string `json:"language,omitempty"`
	Preferred       *bool   `json:"preferred,omitempty"`
	Country         *string `json:"country,omitempty"`
	Area            *string `json:"area,omitempty"`
	Sex             *string `json:"sex,omitempty"`
	SourceID        *string `json:"source_id,omitempty"`
	ReferenceID     *string `json:"reference_id,omitempty"`
	Remarks         *string `json:"remarks,omitempty"`
}

// apiDistribution is the wire form of a distribution row. Same
// rowid-as-string handle as apiVernacular; sfga's distribution
// table has no col__id column, so hive uses SQLite's implicit
// rowid.
//
// Area is the free-text label for the region ("Australia: Western
// Australia"); AreaID is the code within the gazetteer ("AU-WA").
// Both may be set together or one alone — TEXT-gazetteer rows
// carry only Area; ISO/TDWG/TEOW-gazetteer rows usually carry
// both. Gazetteer and Status are enum ids matching sfga's
// gazetteer / distribution_status vocab tables (served by
// /api/vocab).
type apiDistribution struct {
	ID          string `json:"id"`
	TaxonID     string `json:"taxon_id"`
	Area        string `json:"area,omitempty"`
	AreaID      string `json:"area_id,omitempty"`
	Gazetteer   string `json:"gazetteer,omitempty"`
	Status      string `json:"status,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
	ReferenceID string `json:"reference_id,omitempty"`
	Remarks     string `json:"remarks,omitempty"`
	Modified    string `json:"modified,omitempty"`
	ModifiedBy  string `json:"modified_by,omitempty"`
	// IssueCount is the number of open validation issues currently
	// filed against this distribution row (table_name='distribution',
	// not-yet-acknowledged). Zero → elided from JSON. Front-ends
	// render a warn icon on the row when > 0.
	IssueCount int `json:"issue_count,omitempty"`
	// MaxSeverity is the highest severity present among the row's
	// open issues ("error", "warn", "info", "debug"). Empty when
	// IssueCount is 0; elided from JSON in that case. Drives the
	// badge color the WUI paints on the row.
	MaxSeverity string `json:"max_severity,omitempty"`
}

// apiDistributionPatch mirrors apiDistribution as pointer-optional
// fields so a curator can send only what changed. Same "nil =
// leave alone, set to zero-value = clear" contract as
// apiVernacularPatch. TaxonID isn't included — reparenting a
// distribution is delete+add on a different taxon.
type apiDistributionPatch struct {
	Area        *string `json:"area,omitempty"`
	AreaID      *string `json:"area_id,omitempty"`
	Gazetteer   *string `json:"gazetteer,omitempty"`
	Status      *string `json:"status,omitempty"`
	SourceID    *string `json:"source_id,omitempty"`
	ReferenceID *string `json:"reference_id,omitempty"`
	Remarks     *string `json:"remarks,omitempty"`
}

// apiSynonym is the wire form of a synonym link. `label` is a server-
// rendered display (text + html) for the associated name so front-ends
// don't have to re-fetch each name row — matches the shape used for
// apiTaxon and any other resolved reference. See hive.BuildLabel.
// apiSpeciesInteraction is the wire form of a species-interaction
// row. Same rowid-as-string handle as vernacular / distribution;
// sfga's species_interaction table has no col__id.
//
// Directional per sfga: taxon_id is the subject, related_taxon_id
// the object ("parasite parasiteOf host"). GET
// /api/taxon/{id}/species-interactions only returns rows where the
// taxon is the subject; the reverse view is available from the
// related taxon's page.
//
// RelatedTaxonScientificName is a free-text annotation stored
// alongside the FK — useful when a curator wants to preserve the
// exact citation form the source used. sfga still requires
// RelatedTaxonID to point at a real taxon; the scientific-name
// field can't stand on its own. RelatedTaxonLabel is the server-
// resolved display for the FK'd taxon so front-ends don't need a
// per-row fetch.
type apiSpeciesInteraction struct {
	ID                         string   `json:"id"`
	TaxonID                    string   `json:"taxon_id"`
	RelatedTaxonID             string   `json:"related_taxon_id,omitempty"`
	RelatedTaxonScientificName string   `json:"related_taxon_scientific_name,omitempty"`
	RelatedTaxonLabel          apiLabel `json:"related_taxon_label,omitzero"`
	Type                       string   `json:"type,omitempty"`
	SourceID                   string   `json:"source_id,omitempty"`
	ReferenceID                string   `json:"reference_id,omitempty"`
	Remarks                    string   `json:"remarks,omitempty"`
	Modified                   string   `json:"modified,omitempty"`
	ModifiedBy                 string   `json:"modified_by,omitempty"`
	IssueCount                 int      `json:"issue_count,omitempty"`
}

// apiSpeciesInteractionPatch — pointer-optional editable fields.
// TaxonID isn't included (delete+add on a different taxon to
// reparent).
type apiSpeciesInteractionPatch struct {
	RelatedTaxonID             *string `json:"related_taxon_id,omitempty"`
	RelatedTaxonScientificName *string `json:"related_taxon_scientific_name,omitempty"`
	Type                       *string `json:"type,omitempty"`
	SourceID                   *string `json:"source_id,omitempty"`
	ReferenceID                *string `json:"reference_id,omitempty"`
	Remarks                    *string `json:"remarks,omitempty"`
}

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
	// ReferenceID is the synonym-relation's own reference (verbatim
	// from synonym.col__reference_id — a comma-separated list per the
	// sfga schema). Nil-empty for legacy rows without a cited source.
	ReferenceID string `json:"reference_id,omitempty"`
	// ReferenceLabel is the server-rendered "Author (Year) Title" of
	// the first id in ReferenceID, resolved by the handler so the
	// nomenclatural-history footnote accumulator can display citations
	// inline without a per-row round trip. Elided when the reference
	// is unset or the resolution failed.
	ReferenceLabel string `json:"reference_label,omitempty"`
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
	// IssueCount is the number of open validation issues currently
	// filed against this reference row. Zero → elided from JSON so
	// the wire shape stays flat; front-ends render a warn badge on
	// picker results when > 0 and route a click to the reference-
	// quick-fix modal (feedback_no_side_quests).
	IssueCount int `json:"issue_count,omitempty"`
	// MaxSeverity is the highest severity among the record's open
	// issues ("error" / "warn" / "info" / "debug"). Empty when
	// IssueCount is 0. Drives the WUI badge color via the shared
	// validationSeverityBadge helper so every picker/list surface
	// renders the same signal.
	MaxSeverity string `json:"max_severity,omitempty"`
	// HasSourceDoc is true when the reference has an ingested source
	// document available in the archive's sidecar directory (PDF
	// → JATS conversion complete, ready for annotation and
	// AI-assisted extraction — see REFERENCE_PDF_PLAN.md).
	// Drives the gold-star tier of the reference-picker badge:
	// structured-metadata + source-attached → curator has fully
	// upgraded this reference. Elided from JSON (omitempty) so a
	// legacy row with no source document doesn't clutter the wire
	// shape. Falsy today until PDF ingest slice lands.
	HasSourceDoc bool `json:"has_source_doc,omitempty"`
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
	// HasSourceDoc mirrors the field on apiReferenceHit — true when
	// the ingested source document is available in the sidecar.
	// See apiReferenceHit for the full rationale.
	//
	// (Issue count + max severity are NOT mirrored here — the
	// resolver on the WUI side fetches /api/issue directly and
	// computes both locally to keep this endpoint fast.)
	HasSourceDoc bool `json:"has_source_doc,omitempty"`
}

func referenceHitToAPI(h hive.ReferenceHit) apiReferenceHit {
	return apiReferenceHit{
		ID:          h.ID,
		Author:      h.Author,
		Year:        h.Year,
		Title:       h.Title,
		Citation:    h.Citation,
		Type:        h.Type,
		IssueCount:  h.IssueCount,
		MaxSeverity: h.MaxSeverity,
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

	// SynonymStatus is applied by handleAddSynonym only — the
	// taxonomic_status of the synonym-link row (SYNONYM,
	// AMBIGUOUS_SYNONYM, MISAPPLIED). Ignored by other endpoints
	// (handleCreateTaxon / handleAddBasionym). Empty defaults to
	// SYNONYM inside AddSynonym.
	SynonymStatus string `json:"synonym_status,omitempty"`

	// Optional basionym link — set exactly ONE of the following to
	// link this new name to its original combination in the same
	// atomic transaction as the primary create. See CLAUDE.md
	// § Combination bracketing rules.
	//
	//   BasionymNameID — id of an existing name row already in the
	//     archive. Handler links a BASIONYM name_relation from the
	//     primary name to this id.
	//
	//   Basionym       — nested createTaxonBody-shaped payload for a
	//     brand-new original-combination name. Handler CreateNames
	//     it first, then uses the resulting id as the BASIONYM link
	//     target.
	//
	// Setting both is a 400 (ambiguous intent). Setting neither
	// preserves today's behavior (no basionym link — the primary
	// name is either an original combination or a curator-added-later-
	// basionym recomb).
	BasionymNameID string           `json:"basionym_name_id,omitempty"`
	Basionym       *createTaxonBody `json:"basionym,omitempty"`
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

// apiReferencePatch is the wire body for PATCH /api/reference/{id}.
// Every field is a pointer so callers can distinguish "omit" (leave
// alone) from "clear" (send empty string via a non-nil pointer to "").
// Field set mirrors apiReference minus id / modified / modified_by
// (server-authored).
type apiReferencePatch struct {
	AlternativeID       *string `json:"alternative_id,omitempty"`
	SourceID            *string `json:"source_id,omitempty"`
	Citation            *string `json:"citation,omitempty"`
	Type                *string `json:"type,omitempty"`
	Author              *string `json:"author,omitempty"`
	AuthorID            *string `json:"author_id,omitempty"`
	Editor              *string `json:"editor,omitempty"`
	EditorID            *string `json:"editor_id,omitempty"`
	Title               *string `json:"title,omitempty"`
	TitleShort          *string `json:"title_short,omitempty"`
	ContainerAuthor     *string `json:"container_author,omitempty"`
	ContainerTitle      *string `json:"container_title,omitempty"`
	ContainerTitleShort *string `json:"container_title_short,omitempty"`
	Issued              *string `json:"issued,omitempty"`
	Accessed            *string `json:"accessed,omitempty"`
	CollectionTitle     *string `json:"collection_title,omitempty"`
	CollectionEditor    *string `json:"collection_editor,omitempty"`
	Volume              *string `json:"volume,omitempty"`
	Issue               *string `json:"issue,omitempty"`
	Edition             *string `json:"edition,omitempty"`
	Page                *string `json:"page,omitempty"`
	Publisher           *string `json:"publisher,omitempty"`
	PublisherPlace      *string `json:"publisher_place,omitempty"`
	Version             *string `json:"version,omitempty"`
	ISBN                *string `json:"isbn,omitempty"`
	ISSN                *string `json:"issn,omitempty"`
	DOI                 *string `json:"doi,omitempty"`
	Link                *string `json:"link,omitempty"`
	Remarks             *string `json:"remarks,omitempty"`
}

// applyReferencePatch mutates r in place. Enum values (Type) go
// through the empty-safe helper so unset stays unset instead of
// coercing to a zero enum value.
func applyReferencePatch(r *coldp.Reference, p apiReferencePatch) {
	if p.AlternativeID != nil {
		r.AlternativeID = *p.AlternativeID
	}
	if p.SourceID != nil {
		r.SourceID = *p.SourceID
	}
	if p.Citation != nil {
		r.Citation = *p.Citation
	}
	if p.Type != nil {
		r.Type = coldp.NewReferenceType(*p.Type)
	}
	if p.Author != nil {
		r.Author = *p.Author
	}
	if p.AuthorID != nil {
		r.AuthorID = *p.AuthorID
	}
	if p.Editor != nil {
		r.Editor = *p.Editor
	}
	if p.EditorID != nil {
		r.EditorID = *p.EditorID
	}
	if p.Title != nil {
		r.Title = *p.Title
	}
	if p.TitleShort != nil {
		r.TitleShort = *p.TitleShort
	}
	if p.ContainerAuthor != nil {
		r.ContainerAuthor = *p.ContainerAuthor
	}
	if p.ContainerTitle != nil {
		r.ContainerTitle = *p.ContainerTitle
	}
	if p.ContainerTitleShort != nil {
		r.ContainerTitleShort = *p.ContainerTitleShort
	}
	if p.Issued != nil {
		r.Issued = *p.Issued
	}
	if p.Accessed != nil {
		r.Accessed = *p.Accessed
	}
	if p.CollectionTitle != nil {
		r.CollectionTitle = *p.CollectionTitle
	}
	if p.CollectionEditor != nil {
		r.CollectionEditor = *p.CollectionEditor
	}
	if p.Volume != nil {
		r.Volume = *p.Volume
	}
	if p.Issue != nil {
		r.Issue = *p.Issue
	}
	if p.Edition != nil {
		r.Edition = *p.Edition
	}
	if p.Page != nil {
		r.Page = *p.Page
	}
	if p.Publisher != nil {
		r.Publisher = *p.Publisher
	}
	if p.PublisherPlace != nil {
		r.PublisherPlace = *p.PublisherPlace
	}
	if p.Version != nil {
		r.Version = *p.Version
	}
	if p.ISBN != nil {
		r.ISBN = *p.ISBN
	}
	if p.ISSN != nil {
		r.ISSN = *p.ISSN
	}
	if p.DOI != nil {
		r.DOI = *p.DOI
	}
	if p.Link != nil {
		r.Link = *p.Link
	}
	if p.Remarks != nil {
		r.Remarks = *p.Remarks
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
		ID:          h.ID,
		TaxonID:     h.TaxonID,
		NameID:      h.NameID,
		Label:       apiLabel{Text: h.Label.Text, HTML: h.Label.HTML},
		NamePhrase:  h.NamePhrase,
		Status:      h.Status,
		Link:        h.Link,
		Remarks:     h.Remarks,
		Modified:    h.Modified,
		ModifiedBy:  h.ModifiedBy,
		ReferenceID: h.ReferenceID,
	}
}

// vernacularHitToAPI mirrors synonymHitToAPI: converts the pkg-side
// projection into the wire type, stringifying the rowid handle and
// projecting sql.NullBool → *bool for the preferred flag.
func vernacularHitToAPI(h hive.VernacularHit) apiVernacular {
	return apiVernacular{
		ID:              strconv.FormatInt(h.RowID, 10),
		TaxonID:         h.TaxonID,
		Name:            h.Name,
		Transliteration: h.Transliteration,
		Language:        h.Language,
		Preferred:       nullBoolToPtr(h.Preferred),
		Country:         h.Country,
		Area:            h.Area,
		Sex:             h.Sex.ID(),
		SourceID:        h.SourceID,
		ReferenceID:     h.ReferenceID,
		Remarks:         h.Remarks,
		Modified:        h.Modified,
		ModifiedBy:      h.ModifiedBy,
		IssueCount:      h.IssueCount,
		MaxSeverity:     h.MaxSeverity,
	}
}

// distributionHitToAPI mirrors vernacularHitToAPI for the
// distribution wire type. Stringifies the rowid handle and turns
// the coldp enum values into their string ids for the wire.
func distributionHitToAPI(h hive.DistributionHit) apiDistribution {
	return apiDistribution{
		ID:          strconv.FormatInt(h.RowID, 10),
		TaxonID:     h.TaxonID,
		Area:        h.Area,
		AreaID:      h.AreaID,
		Gazetteer:   h.Gazetteer.ID(),
		Status:      h.Status.ID(),
		SourceID:    h.SourceID,
		ReferenceID: h.ReferenceID,
		Remarks:     h.Remarks,
		Modified:    h.Modified,
		ModifiedBy:  h.ModifiedBy,
		IssueCount:  h.IssueCount,
		MaxSeverity: h.MaxSeverity,
	}
}

// nullBoolToPtr converts sql.NullBool → *bool. Invalid → nil (omitted from
// JSON via omitempty). Valid → &bool.
// speciesInteractionHitToAPI mirrors the vernacular / distribution
// converters. Stringifies the rowid handle, projects the enum id,
// and lifts the resolved RelatedTaxonLabel across to the wire.
func speciesInteractionHitToAPI(h hive.SpeciesInteractionHit) apiSpeciesInteraction {
	out := apiSpeciesInteraction{
		ID:                         strconv.FormatInt(h.RowID, 10),
		TaxonID:                    h.TaxonID,
		RelatedTaxonID:             h.RelatedTaxonID,
		RelatedTaxonScientificName: h.RelatedTaxonScientificName,
		Type:                       h.Type.ID(),
		SourceID:                   h.SourceID,
		ReferenceID:                h.ReferenceID,
		Remarks:                    h.Remarks,
		Modified:                   h.Modified,
		ModifiedBy:                 h.ModifiedBy,
		IssueCount:                 h.IssueCount,
	}
	if h.RelatedTaxonLabel.Text != "" || h.RelatedTaxonLabel.HTML != "" {
		out.RelatedTaxonLabel = apiLabel{
			Text: h.RelatedTaxonLabel.Text,
			HTML: h.RelatedTaxonLabel.HTML,
		}
	}
	return out
}

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
