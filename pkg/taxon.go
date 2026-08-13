package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sfborg/hive/pkg/ui"
	"github.com/sfborg/sflib/pkg/coldp"
)

// TaxonHit is a thin projection returned by list/search endpoints. Callers
// fetch the full coldp.Taxon only when opening a detail view. This split
// matters for the WUI over HTTP as much as for TUI tree rendering.
//
// Label is server-rendered from Name+Authorship+Rank+Extinct via
// BuildLabel; the individual fields are still exposed for callers that
// want to compose their own display (e.g., the TUI's tree, which applies
// italics via lipgloss instead of HTML).
type TaxonHit struct {
	ID          string
	ParentID    string
	NameID      string
	Name        string // best-effort display: gn__canonical_simple falling back to col__scientific_name
	Authorship  string
	Rank        string // rank ID (references rank.col__id)
	Status      string // taxonomic_status ID
	Extinct     sql.NullBool
	HasChildren bool
	Label       Label

	// IsSynonym is true when this hit was resolved via a synonym
	// pointing at the accepted taxon (ID). Set only by SearchTaxa
	// when its includeSynonyms parameter is true; false elsewhere.
	IsSynonym bool
	// MatchedName is the name string that actually satisfied the
	// query — the synonym's canonical when IsSynonym, else the
	// accepted taxon's own canonical (same as Name). Callers
	// display it alongside Name so curators can see why a result
	// appeared when it came in via a synonym.
	MatchedName string
}

// GetTaxon returns the taxon with the given col__id.
//
// Denormalized classification columns (col__genus, col__family, col__kingdom,
// …) and their sf__* ID siblings are NOT populated here — they are a
// query-time cache managed by MoveTaxon, not part of the taxon's identity.
// Callers wanting a full classification should walk the parent chain via
// TaxonPath (once implemented) or query the columns directly for legacy
// archives where they were pre-populated by an importer.
func (a *Archive) GetTaxon(ctx context.Context, id string) (*coldp.Taxon, error) {
	// COALESCE folds SQL NULL into '' for reads. Hive writes NULL to nullable
	// FK columns when the value is unset (empty string can't satisfy a FK
	// against tables without a '' seed row like `source`, `taxon`, `reference`).
	// The coldp.Taxon shape uses plain strings — the "" ↔ NULL translation
	// happens at the SQL boundary.
	const q = `SELECT
		col__id, col__alternative_id, gn__local_id, gn__global_id, tw__otu_id,
		COALESCE(col__source_id, ''), COALESCE(col__parent_id, ''),
		col__ordinal, col__branch_length,
		col__name_id, col__name_phrase,
		COALESCE(col__according_to_id, ''), col__according_to_page, col__according_to_page_link,
		col__scrutinizer, col__scrutinizer_id, col__scrutinizer_date,
		COALESCE(col__status_id, ''), col__reference_id, col__extinct,
		COALESCE(col__temporal_range_start_id, ''), COALESCE(col__temporal_range_end_id, ''),
		col__environment_id,
		col__link, col__remarks, col__modified, col__modified_by
	FROM taxon WHERE col__id = ? LIMIT 1`

	var (
		t          coldp.Taxon
		trStart    string
		trEnd      string
		envIDs     string
		provStatus string
	)
	err := a.db.QueryRowContext(ctx, q, id).Scan(
		&t.ID, &t.AlternativeID, &t.LocalID, &t.GlobalID, &t.OtuID,
		&t.SourceID, &t.ParentID, &t.Ordinal, &t.BranchLength,
		&t.NameID, &t.NamePhrase,
		&t.AccordingToID, &t.AccordingToPage, &t.AccordingToPageLink,
		&t.Scrutinizer, &t.ScrutinizerID, &t.ScrutinizerDate,
		&provStatus, &t.ReferenceID, &t.Extinct,
		&trStart, &trEnd,
		&envIDs,
		&t.Link, &t.Remarks, &t.Modified, &t.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: taxon %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get taxon %s: %w", id, err)
	}

	// Provisional flag is derived from status per CoLDP semantics — the sfga
	// schema encodes it as a distinct taxonomic_status value rather than a
	// separate column.
	if provStatus == "PROVISIONALLY_ACCEPTED" || provStatus == "PROVISIONALLY_VALID" {
		t.Provisional = sql.NullBool{Bool: true, Valid: true}
	}

	t.TemporalRangeStart = coldp.NewGeoTime(trStart)
	t.TemporalRangeEnd = coldp.NewGeoTime(trEnd)
	t.Environment = coldp.GetEnvironments(envIDs)

	return &t, nil
}

// ListChildren returns every direct child of parentID. Convenience wrapper
// over ListChildrenPage with limit == 0. Suits the TUI, which wants the
// full sibling list to render an expandable tree node.
func (a *Archive) ListChildren(ctx context.Context, parentID string) ([]TaxonHit, error) {
	hits, _, err := a.ListChildrenPage(ctx, parentID, 0, 0)
	return hits, err
}

// TaxonRef returns id + rendered label for a taxon in one query. Missing
// taxa return a Ref containing just the id (as the label text), so callers
// using this as a display resolver get graceful fallback rather than a
// hard failure. Empty ID short-circuits with a zero-value Ref.
func (a *Archive) TaxonRef(ctx context.Context, id string) (Ref, error) {
	if id == "" {
		return Ref{}, nil
	}
	const q = `SELECT
		COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, ''),
		COALESCE(n.col__authorship, ''),
		COALESCE(n.col__rank_id, ''),
		t.col__extinct
	FROM taxon t
	LEFT JOIN name n ON n.col__id = t.col__name_id
	WHERE t.col__id = ?`
	var (
		canonical, authorship, rank string
		extinct                     sql.NullBool
	)
	err := a.db.QueryRowContext(ctx, q, id).Scan(&canonical, &authorship, &rank, &extinct)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Ref{ID: id, Label: Label{Text: id}}, nil
		}
		return Ref{}, fmt.Errorf("core: taxon ref %s: %w", id, err)
	}
	if canonical == "" {
		canonical = id
	}
	return Ref{
		ID:    id,
		Label: BuildLabel(canonical, authorship, rank, extinct.Valid && extinct.Bool),
	}, nil
}

// NameRef returns id + rendered label for a name — the analogue of
// TaxonRef but keyed on the name row directly (no taxon lookup). Used
// by name_relation display paths where the counterpart is a Name that
// may or may not have its own Taxon row (basionyms are usually
// synonyms; there's no accepted taxon to route through).
//
// Missing name → Ref containing just the id as the label text.
func (a *Archive) NameRef(ctx context.Context, id string) (Ref, error) {
	if id == "" {
		return Ref{}, nil
	}
	const q = `SELECT
		COALESCE(NULLIF(gn__canonical_simple, ''), col__scientific_name, ''),
		COALESCE(col__authorship, ''),
		COALESCE(col__rank_id, '')
	FROM name WHERE col__id = ?`
	var canonical, authorship, rank string
	err := a.db.QueryRowContext(ctx, q, id).Scan(&canonical, &authorship, &rank)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Ref{ID: id, Label: Label{Text: id}}, nil
		}
		return Ref{}, fmt.Errorf("core: name ref %s: %w", id, err)
	}
	if canonical == "" {
		canonical = id
	}
	// Extinct dagger doesn't apply to name-only refs — that flag lives
	// on the taxon. Pass false; BuildLabel just skips the dagger.
	return Ref{
		ID:    id,
		Label: BuildLabel(canonical, authorship, rank, false),
	}, nil
}

// ValidChildRanks returns the rank IDs (plus typical_use flags) a
// new child of parentID should be allowed to pick from — filtered
// per the parent's own rank and nomenclatural code via the
// TaxonWorks-derived rank hierarchy (pkg/ui.ValidChildRanks).
//
// An empty parentID or an archive whose parent lacks a rank / code
// returns nil, signaling "no filter" so the frontend shows every
// rank in the vocab. Curator overrides the guess on the form as
// needed; filtering just removes the obviously wrong picks.
func (a *Archive) ValidChildRanks(ctx context.Context, parentID string) ([]ui.ChildRank, error) {
	if parentID == "" {
		return nil, nil
	}
	const q = `SELECT
		COALESCE(n.col__rank_id, ''),
		COALESCE(n.col__code_id, '')
	FROM taxon t
	LEFT JOIN name n ON n.col__id = t.col__name_id
	WHERE t.col__id = ?`
	var rankID, codeID string
	err := a.db.QueryRowContext(ctx, q, parentID).Scan(&rankID, &codeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("core: valid child ranks for parent %s: %w", parentID, err)
	}
	if codeID == "" {
		return nil, nil
	}
	return ui.ValidChildRanks(codeID, rankID), nil
}

// CreateNamePrefix returns the string a new child of parentID should
// start with — the parent's canonical name plus a trailing space, so
// the curator only types the new epithet. Empty string means "no
// prefix, curator types the full name" (for parents above the
// genus-group where the child is a fresh uninomial).
//
// Examples:
//   parent Felis (GENUS)         → "Felis "
//   parent Felis catus (SPECIES) → "Felis catus "     (child is subspecies)
//   parent Felidae (FAMILY)      → ""                 (child is a genus)
//
// Uses gn__canonical_full (from gnparser) — no authorship, subgenus
// parens preserved — so a curator adding a subspecies of "Felis catus
// Linnaeus, 1758" gets "Felis catus " to type after, not "Felis catus
// Linnaeus, 1758 " (each child usually has its own authorship). Falls
// back to canonical_simple if _full is unset, then to
// col__scientific_name only if both parser caches are empty (very
// legacy archive).
//
// Sfga's rank vocab flags (col__genus_group, col__infraspecific) plus
// a special case for SPECIES / SPECIES_AGGREGATE decide when to
// prefix at all.
func (a *Archive) CreateNamePrefix(ctx context.Context, parentID string) (string, error) {
	if parentID == "" {
		return "", nil
	}
	const q = `SELECT
		COALESCE(n.gn__canonical_full, ''),
		COALESCE(n.gn__canonical_simple, ''),
		COALESCE(n.col__scientific_name, ''),
		COALESCE(r.col__genus_group, 0),
		COALESCE(r.col__infraspecific, 0),
		COALESCE(n.col__rank_id, '')
	FROM taxon t
	LEFT JOIN name n ON n.col__id = t.col__name_id
	LEFT JOIN rank r ON r.col__id = n.col__rank_id
	WHERE t.col__id = ?`
	var (
		canonicalFull   string
		canonicalSimple string
		sciName         string
		genusGroup      int
		infrasp         int
		rankID          string
	)
	err := a.db.QueryRowContext(ctx, q, parentID).Scan(
		&canonicalFull, &canonicalSimple, &sciName, &genusGroup, &infrasp, &rankID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("core: create-name prefix for parent %s: %w", parentID, err)
	}
	// SPECIES and SPECIES_AGGREGATE aren't flagged genus_group or
	// infraspecific in the rank vocab; catch them explicitly so their
	// subspecies-level children get a trinomial prefix.
	isSpeciesLevel := rankID == "SPECIES" || rankID == "SPECIES_AGGREGATE"
	if genusGroup == 0 && infrasp == 0 && !isSpeciesLevel {
		return "", nil
	}
	// Prefer the parser's canonical form (no authorship). Fall through
	// on empties in case gnparser choked on the parent's name.
	name := canonicalFull
	if name == "" {
		name = canonicalSimple
	}
	if name == "" {
		name = sciName
	}
	if name == "" {
		return "", nil
	}
	return name + " ", nil
}

// CodeForParent returns the nomenclatural code (col__code_id on the
// parent taxon's associated name row) so the new-taxon form can seed
// its code picker from the parent by default. Empty parentID → "".
// Missing parent, no name, or no code all return "" — the caller is
// expected to treat an empty result as "no default known, ask the user."
func (a *Archive) CodeForParent(ctx context.Context, parentID string) (string, error) {
	if parentID == "" {
		return "", nil
	}
	const q = `SELECT COALESCE(n.col__code_id, '')
		FROM taxon t
		LEFT JOIN name n ON n.col__id = t.col__name_id
		WHERE t.col__id = ?`
	var code string
	err := a.db.QueryRowContext(ctx, q, parentID).Scan(&code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("core: code for parent %s: %w", parentID, err)
	}
	return code, nil
}

// stripLikeWildcards removes '%' and '_' from q so appending '%' for a
// prefix search doesn't let a user-typed wildcard broaden the pattern.
// Deliberately NOT using LIKE ... ESCAPE '\' for the escape route:
// SQLite's LIKE-uses-index optimization only fires when there is no
// ESCAPE clause (regardless of whether the escape char is present in
// the actual pattern), so the ESCAPE approach forces a full table
// scan and defeats the NOCASE indices. Scientific names never contain
// '%' or '_'; stripping them is a no-op for real queries.
func stripLikeWildcards(q string) string {
	if !strings.ContainsAny(q, "%_") {
		return q
	}
	r := strings.NewReplacer("%", "", "_", "")
	return r.Replace(q)
}

// SearchTaxa returns up to `limit` taxa whose associated name canonical or
// scientific-name string matches q as a case-insensitive prefix. Returns
// thin TaxonHit projections — same shape as ListChildren so the WUI's
// tree components can render either result set uniformly.
//
// When includeSynonyms is true, the result set also includes accepted
// taxa reached via a matching synonym: for each synonym whose name
// prefix-matches q, the accepted taxon it points at appears in the
// results with IsSynonym=true and MatchedName set to the synonym's
// text. Pro-parte synonyms — one synonym row family pointing at
// multiple accepted taxa — produce one hit per resolved accepted
// taxon so curators see every destination. If the same accepted
// taxon matches both by its own name and via a synonym, the accepted
// row wins; the synonym row is dropped.
//
// Ordering is alphabetical by matched_name so synonym and accepted
// hits interleave in the order curators would look for them.
//
// Prefix (LIKE 'q%') rather than substring lets the query use the
// NOCASE indices on name (and idx_synonym_name_id on synonym) added
// in ensureHiveTables. On COL 26-07 (5.4M names, 2.7M synonyms) the
// full include_synonyms path returns in under 100ms; see DEFERRED.md
// § Substring name search (FTS follow-up) for the substring-semantics
// follow-up.
func (a *Archive) SearchTaxa(ctx context.Context, q string, limit int, includeSynonyms bool) ([]TaxonHit, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := stripLikeWildcards(q) + "%"

	// Two arms per source (canonical + scientific text columns) each
	// hit a dedicated NOCASE index — UNION ALL keeps each arm on its
	// own index (an OR predicate on both columns would prevent the
	// planner from using either). The outer ROW_NUMBER partitions by
	// accepted-taxon id so a synonym pointing at a taxon that also
	// matches by its own name collapses to the accepted row.
	//
	// Per-arm LIMIT keeps the intermediate result set bounded even
	// when the query is a very common prefix (e.g., a single letter);
	// each arm is guaranteed to contribute enough candidates to
	// satisfy the outer LIMIT after dedup, since limit ≤ per-arm cap.
	acceptedArms := `
		SELECT * FROM (
			SELECT
				t.col__id AS id,
				COALESCE(t.col__parent_id, '') AS parent_id,
				t.col__name_id AS name_id,
				COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
				COALESCE(n.col__authorship, '') AS authorship,
				COALESCE(n.col__rank_id, '') AS rank_id,
				COALESCE(t.col__status_id, '') AS status_id,
				t.col__extinct AS extinct,
				0 AS is_synonym,
				COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS matched_name
			FROM name n
			JOIN taxon t ON t.col__name_id = n.col__id
			WHERE n.gn__canonical_simple LIKE ? COLLATE NOCASE
			LIMIT ?
		)
		UNION ALL
		SELECT * FROM (
			SELECT
				t.col__id,
				COALESCE(t.col__parent_id, ''),
				t.col__name_id,
				COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, ''),
				COALESCE(n.col__authorship, ''),
				COALESCE(n.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				0,
				n.col__scientific_name
			FROM name n
			JOIN taxon t ON t.col__name_id = n.col__id
			WHERE n.col__scientific_name LIKE ? COLLATE NOCASE
			LIMIT ?
		)`

	synonymArms := `
		UNION ALL
		SELECT * FROM (
			SELECT
				t.col__id,
				COALESCE(t.col__parent_id, ''),
				t.col__name_id,
				COALESCE(NULLIF(acc.gn__canonical_simple, ''), acc.col__scientific_name, ''),
				COALESCE(acc.col__authorship, ''),
				COALESCE(acc.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				1,
				sname.gn__canonical_simple
			FROM name sname
			JOIN synonym s ON s.col__name_id = sname.col__id
			JOIN taxon t ON t.col__id = s.col__taxon_id
			JOIN name acc ON acc.col__id = t.col__name_id
			WHERE sname.gn__canonical_simple LIKE ? COLLATE NOCASE
			LIMIT ?
		)
		UNION ALL
		SELECT * FROM (
			SELECT
				t.col__id,
				COALESCE(t.col__parent_id, ''),
				t.col__name_id,
				COALESCE(NULLIF(acc.gn__canonical_simple, ''), acc.col__scientific_name, ''),
				COALESCE(acc.col__authorship, ''),
				COALESCE(acc.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				1,
				sname.col__scientific_name
			FROM name sname
			JOIN synonym s ON s.col__name_id = sname.col__id
			JOIN taxon t ON t.col__id = s.col__taxon_id
			JOIN name acc ON acc.col__id = t.col__name_id
			WHERE sname.col__scientific_name LIKE ? COLLATE NOCASE
			LIMIT ?
		)`

	// Dedup: PARTITION BY accepted-taxon id and pick the accepted row
	// when both present (is_synonym=0 sorts before 1). Pro-parte
	// preserved automatically since distinct accepted taxa land in
	// distinct partitions. has_children is computed in the outer
	// select over the dedup'd rows so we don't run the EXISTS probe
	// for rows we're about to drop.
	body := acceptedArms
	if includeSynonyms {
		body += synonymArms
	}
	query := `SELECT
		id, parent_id, name_id, display_name, authorship, rank_id,
		status_id, extinct, is_synonym, matched_name,
		EXISTS(SELECT 1 FROM taxon c WHERE c.col__parent_id = id) AS has_children
	FROM (
		SELECT *, ROW_NUMBER() OVER (
			PARTITION BY id ORDER BY is_synonym, matched_name
		) AS rn
		FROM (` + body + `)
	)
	WHERE rn = 1
	ORDER BY matched_name
	LIMIT ?`

	args := []any{pattern, limit, pattern, limit}
	if includeSynonyms {
		args = append(args, pattern, limit, pattern, limit)
	}
	args = append(args, limit)

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("core: search taxa %q: %w", q, err)
	}
	defer rows.Close()

	var hits []TaxonHit
	for rows.Next() {
		var (
			h         TaxonHit
			isSynonym int
		)
		if err := rows.Scan(
			&h.ID, &h.ParentID, &h.NameID,
			&h.Name, &h.Authorship, &h.Rank,
			&h.Status, &h.Extinct,
			&isSynonym, &h.MatchedName,
			&h.HasChildren,
		); err != nil {
			return nil, fmt.Errorf("core: scan taxa hit: %w", err)
		}
		h.IsSynonym = isSynonym == 1
		h.Label = BuildLabel(h.Name, h.Authorship, h.Rank, h.Extinct.Valid && h.Extinct.Bool)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// ListChildrenPage returns direct children with SQL-level LIMIT/OFFSET
// pagination. limit == 0 disables the LIMIT clause and returns every row
// (matching ListChildren). The full sibling count (independent of the
// page window) is also returned so pagers can render "N of M".
//
// Ordered by col__ordinal (NULLs last), then by scientific name — the same
// stable order in every page so callers can advance a cursor safely.
//
// See GetTaxon for the "" ↔ NULL parent_id COALESCE convention.
func (a *Archive) ListChildrenPage(ctx context.Context, parentID string, limit, offset int) ([]TaxonHit, int, error) {
	// Fork the WHERE clause on parentID rather than wrapping the column
	// in COALESCE. SQLite can't push an equality predicate through
	// COALESCE(col__parent_id, '') = ? to use idx_taxon_parent_id, so
	// the wrapped form falls back to a full-table scan (2.7M rows on
	// COL 26-07). Splitting into a root predicate vs equality lets each
	// branch use the index directly (MULTI-INDEX OR for roots, single
	// index seek for a named parent).
	//
	// Roots come in two shapes across the archives we see: NULL
	// (hive-created) and '' (harvester-created, sfga imports through
	// certain paths). The `IS NULL OR = ''` predicate covers both and
	// still uses idx_taxon_parent_id via SQLite's MULTI-INDEX OR plan.
	var (
		countQ    string
		whereQ    string
		countArgs []any
		whereArgs []any
	)
	if parentID == "" {
		countQ = `SELECT COUNT(*) FROM taxon WHERE col__parent_id IS NULL OR col__parent_id = ''`
		whereQ = `WHERE t.col__parent_id IS NULL OR t.col__parent_id = ''`
	} else {
		countQ = `SELECT COUNT(*) FROM taxon WHERE col__parent_id = ?`
		countArgs = []any{parentID}
		whereQ = `WHERE t.col__parent_id = ?`
		whereArgs = []any{parentID}
	}
	var total int
	if err := a.db.QueryRowContext(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("core: count children of %q: %w", parentID, err)
	}

	q := `SELECT
		t.col__id,
		COALESCE(t.col__parent_id, ''),
		t.col__name_id,
		COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
		COALESCE(n.col__authorship, ''),
		COALESCE(n.col__rank_id, ''),
		COALESCE(t.col__status_id, ''),
		t.col__extinct,
		EXISTS(SELECT 1 FROM taxon c WHERE c.col__parent_id = t.col__id) AS has_children
	FROM taxon t
	LEFT JOIN name n ON n.col__id = t.col__name_id
	` + whereQ + `
	ORDER BY t.col__ordinal IS NULL, t.col__ordinal, display_name`

	args := whereArgs
	if limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("core: list children of %q: %w", parentID, err)
	}
	defer rows.Close()

	var hits []TaxonHit
	for rows.Next() {
		var h TaxonHit
		if err := rows.Scan(
			&h.ID, &h.ParentID, &h.NameID,
			&h.Name, &h.Authorship, &h.Rank,
			&h.Status, &h.Extinct, &h.HasChildren,
		); err != nil {
			return nil, 0, fmt.Errorf("core: scan child of %q: %w", parentID, err)
		}
		h.Label = BuildLabel(h.Name, h.Authorship, h.Rank, h.Extinct.Valid && h.Extinct.Bool)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("core: iterate children of %q: %w", parentID, err)
	}
	return hits, total, nil
}

// CreateTaxon inserts a new taxon row.
//
// If t.ID is empty a UUID v4 is generated and returned. If t.ID is set (e.g.,
// from an import where the source ID must be preserved verbatim) it is used
// as-is; sfga treats col__id as an opaque string, so numeric-looking or
// slug-like values round-trip untouched.
//
// col__modified is stamped with the current time in RFC3339 UTC. col__modified_by
// is stamped with the actor from the transaction's context (see WithActor).
//
// Denormalized classification columns (col__genus, col__family, sf__genus_id,
// …) are set to empty regardless of what t contains. They are managed by
// MoveTaxon-driven reclassification, never by direct writes.
func (t *Tx) CreateTaxon(taxon coldp.Taxon) (string, error) {
	if taxon.ID == "" {
		taxon.ID = uuid.NewString()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Environment []Environment -> comma-separated string of enum IDs.
	// Use .ID() (raw enum value: BRACKISH, MARINE, …) not .String()
	// (lowercase display form).
	envParts := make([]string, 0, len(taxon.Environment))
	for _, e := range taxon.Environment {
		if s := e.ID(); s != "" {
			envParts = append(envParts, s)
		}
	}
	envIDs := strings.Join(envParts, ",")

	const insert = `INSERT INTO taxon (
		col__id, col__alternative_id, gn__local_id, gn__global_id, tw__otu_id,
		col__source_id, col__parent_id, col__ordinal, col__branch_length,
		col__name_id, col__name_phrase,
		col__according_to_id, col__according_to_page, col__according_to_page_link,
		col__scrutinizer, col__scrutinizer_id, col__scrutinizer_date,
		col__status_id, col__reference_id, col__extinct,
		col__temporal_range_start_id, col__temporal_range_end_id, col__environment_id,
		col__link, col__remarks, col__modified, col__modified_by
	) VALUES (
		?, ?, ?, ?, ?,
		?, ?, ?, ?,
		?, ?,
		?, ?, ?,
		?, ?, ?,
		?, ?, ?,
		?, ?, ?,
		?, ?, ?, ?
	)`

	// Nullable FK columns get NULL when the field is empty. The sfga schema
	// declares them with `DEFAULT ''`, but SQLite's FK enforcement (which
	// hive enables via PRAGMA foreign_keys=ON) rejects '' unless the parent
	// table has a '' seed row. Only enum tables have that seed; content
	// tables (source, taxon, reference) do not. Translation: "" -> NULL.
	_, err := t.tx.ExecContext(t.ctx, insert,
		taxon.ID, taxon.AlternativeID, taxon.LocalID, taxon.GlobalID, taxon.OtuID,
		nullIfEmpty(taxon.SourceID), nullIfEmpty(taxon.ParentID), taxon.Ordinal, taxon.BranchLength,
		taxon.NameID, taxon.NamePhrase,
		nullIfEmpty(taxon.AccordingToID), taxon.AccordingToPage, taxon.AccordingToPageLink,
		taxon.Scrutinizer, taxon.ScrutinizerID, taxon.ScrutinizerDate,
		provisionalToStatus(taxon.Provisional), taxon.ReferenceID, taxon.Extinct,
		taxon.TemporalRangeStart.ID(), taxon.TemporalRangeEnd.ID(), envIDs,
		taxon.Link, taxon.Remarks, now, t.actor,
	)
	if err != nil {
		return "", fmt.Errorf("core: insert taxon %s: %w", taxon.ID, err)
	}
	t.markTaxonDirty(taxon.ID)
	return taxon.ID, nil
}

// provisionalToStatus derives the sfga col__status_id from the CoLDP
// `provisional` boolean on coldp.Taxon:
//
//	Provisional == true  → PROVISIONALLY_ACCEPTED
//	otherwise            → ACCEPTED
//
// This mapping is intentionally lossy on write: sfga distinguishes ACCEPTED,
// PROVISIONALLY_ACCEPTED, VALID, PROVISIONALLY_VALID, and others, but CoLDP
// exposes only a single `provisional` bit at the taxon level. Curators who
// need to write PROVISIONALLY_VALID (a zoological-code variant) or any
// non-accepted status directly will use a future explicit-status write API;
// UpdateTaxon in this file only covers the CoLDP-shaped path for now.
//
// Round-trip note: Get → mutate → Create can degrade VALID to ACCEPTED. The
// GetTaxon reader sets Provisional=true when it sees PROVISIONALLY_ACCEPTED
// or PROVISIONALLY_VALID, and false otherwise (including for VALID). When
// that value round-trips through CreateTaxon here, VALID becomes ACCEPTED.
// This is called out again in CLAUDE.md § Deliberately deferred once the
// explicit-status write API lands.
// nullIfEmpty returns nil when s is empty, and s otherwise. Used for
// nullable FK columns so hive's INSERTs satisfy PRAGMA foreign_keys=ON
// against sfga's content tables (which don't seed a ” placeholder row
// the way enum tables do).
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func provisionalToStatus(prov sql.NullBool) string {
	if prov.Valid && prov.Bool {
		return "PROVISIONALLY_ACCEPTED"
	}
	return "ACCEPTED"
}

// UpdateTaxon writes a coldp.Taxon back to an existing row, keyed by taxon.ID.
//
// Semantics:
//   - taxon.ID is required; empty returns ErrValidation.
//   - Missing row returns ErrNotFound.
//   - Optimistic concurrency: if taxon.Modified is non-empty, it is treated
//     as an If-Match token and compared against the current col__modified.
//     Mismatch returns ErrConflict. Empty Modified skips the check (blind
//     write — the HTTP layer enforces the header requirement per request).
//   - col__parent_id is NOT updated here. Reparenting goes through MoveTaxon
//     (an explicit verb with cycle checks). Any ParentID in the input is
//     silently ignored — the field is present on coldp.Taxon for round-trip
//     purposes but hive owns the tree structure.
//   - Denormalized classification columns (col__genus/family/…/sf__*_id) and
//     read-only cache fields (col__branch_length) are also silently ignored;
//     they are managed by the tree-structure code path.
//
// col__modified is stamped with the current time in RFC3339 UTC. col__modified_by
// with the actor from the transaction's context.
func (t *Tx) UpdateTaxon(taxon coldp.Taxon) error {
	if taxon.ID == "" {
		return fmt.Errorf("core: update taxon: %w: ID required", ErrValidation)
	}

	preSnapshot, _ := t.snapshotTaxon(taxon.ID)

	// Optimistic concurrency check.
	if taxon.Modified != "" {
		var currentModified string
		err := t.tx.QueryRowContext(t.ctx,
			"SELECT COALESCE(col__modified, '') FROM taxon WHERE col__id = ?",
			taxon.ID,
		).Scan(&currentModified)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("core: update taxon %s: %w", taxon.ID, ErrNotFound)
			}
			return fmt.Errorf("core: read current modified for %s: %w", taxon.ID, err)
		}
		if currentModified != taxon.Modified {
			return fmt.Errorf(
				"core: update taxon %s: %w: If-Match mismatch (have %q, want %q)",
				taxon.ID, ErrConflict, currentModified, taxon.Modified,
			)
		}
	}

	envParts := make([]string, 0, len(taxon.Environment))
	for _, e := range taxon.Environment {
		if s := e.String(); s != "" {
			envParts = append(envParts, s)
		}
	}
	envIDs := strings.Join(envParts, ",")

	now := time.Now().UTC().Format(time.RFC3339Nano)

	const update = `UPDATE taxon SET
		col__alternative_id = ?, gn__local_id = ?, gn__global_id = ?, tw__otu_id = ?,
		col__source_id = ?, col__ordinal = ?,
		col__name_id = ?, col__name_phrase = ?,
		col__according_to_id = ?, col__according_to_page = ?, col__according_to_page_link = ?,
		col__scrutinizer = ?, col__scrutinizer_id = ?, col__scrutinizer_date = ?,
		col__status_id = ?, col__reference_id = ?, col__extinct = ?,
		col__temporal_range_start_id = ?, col__temporal_range_end_id = ?, col__environment_id = ?,
		col__link = ?, col__remarks = ?, col__modified = ?, col__modified_by = ?
	WHERE col__id = ?`

	res, err := t.tx.ExecContext(t.ctx, update,
		taxon.AlternativeID, taxon.LocalID, taxon.GlobalID, taxon.OtuID,
		nullIfEmpty(taxon.SourceID), taxon.Ordinal,
		taxon.NameID, taxon.NamePhrase,
		nullIfEmpty(taxon.AccordingToID), taxon.AccordingToPage, taxon.AccordingToPageLink,
		taxon.Scrutinizer, taxon.ScrutinizerID, taxon.ScrutinizerDate,
		provisionalToStatus(taxon.Provisional), taxon.ReferenceID, taxon.Extinct,
		taxon.TemporalRangeStart.ID(), taxon.TemporalRangeEnd.ID(), envIDs,
		taxon.Link, taxon.Remarks, now, t.actor,
		taxon.ID,
	)
	if err != nil {
		return fmt.Errorf("core: update taxon %s: %w", taxon.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update taxon %s rows affected: %w", taxon.ID, err)
	}
	if rows == 0 {
		return fmt.Errorf("core: update taxon %s: %w", taxon.ID, ErrNotFound)
	}
	t.markTaxonDirtyWithPre(taxon.ID, preSnapshot)
	return nil
}

// MoveTaxon changes the parent of the taxon identified by id.
//
//   - newParentID == "" moves the taxon to root level (stored as SQL NULL).
//   - id == newParentID returns ErrValidation.
//   - If newParentID is already a descendant of id, the move would create a
//     cycle; the operation returns ErrValidation.
//   - Missing target taxon returns ErrNotFound.
//
// The taxon's col__modified and col__modified_by are stamped.
//
// **Denormalized classification is NOT refreshed in v0.** Columns like
// col__genus/family/…/sf__*_id on the moved taxon and its descendants may
// become stale relative to the new parent chain. This is a deliberate v0
// scoping — a future Reclassify(id) operation will rebuild the cache;
// callers who care about those columns should invoke it after a move (once
// it lands) or rely on downstream re-import. The parent_id link is the
// source of truth; the classification columns are a query-time convenience.
// See CLAUDE.md § pkg/ package conventions.
func (t *Tx) MoveTaxon(id, newParentID string) error {
	if id == "" {
		return fmt.Errorf("core: move taxon: %w: id required", ErrValidation)
	}
	if id == newParentID {
		return fmt.Errorf(
			"core: move taxon %s: %w: cannot be its own parent",
			id, ErrValidation,
		)
	}

	preSnapshot, _ := t.snapshotTaxon(id)

	// Cycle detection: newParentID must not be id or a descendant of id.
	if newParentID != "" {
		cycle, err := t.isDescendantOf(newParentID, id)
		if err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf(
				"core: move taxon %s under %s: %w: would create a cycle",
				id, newParentID, ErrValidation,
			)
		}
	}

	// Snapshot and mark every descendant BEFORE the move so their
	// ancestor-dependent validation rules re-run under the new
	// hierarchy. A genus moved to a different family shifts the
	// ancestor chain for every species / infraspecies under it —
	// PARENT_GENUS_MISSING and probable-incertae-sedis rules walk
	// up looking for a genus and may light up (or clear) once the
	// grandparent context changes.
	if err := t.snapshotAndMarkDescendantsDirty(id); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE taxon
		   SET col__parent_id  = ?,
		       col__modified   = ?,
		       col__modified_by = ?
		 WHERE col__id = ?`,
		nullIfEmpty(newParentID), now, t.actor, id,
	)
	if err != nil {
		return fmt.Errorf("core: move taxon %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: move taxon %s rows affected: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("core: move taxon %s: %w", id, ErrNotFound)
	}
	t.markTaxonDirtyWithPre(id, preSnapshot)
	return nil
}

// Classification returns the full ancestor chain of a taxon in root-down
// order, INCLUDING the taxon itself as the final element. Each hit
// carries the display name, rank, authorship, and status — enough for
// callers to render breadcrumbs, expand a tree path, or feed a picker
// without a second round trip per ancestor.
//
// Empty return means id is unknown. A one-element return means id has
// no parent (root-level taxon).
//
// One recursive-CTE query with an outer JOIN against name replaces what
// the old reveal path took N sequential Ancestors + ListChildren calls
// to compute. See DEFERRED.md § Bulk classification endpoint (now
// resolved by this method).
func (a *Archive) Classification(ctx context.Context, id string) ([]TaxonHit, error) {
	if id == "" {
		return nil, nil
	}
	const q = `
		WITH RECURSIVE chain(id, parent_id, depth) AS (
			SELECT col__id, col__parent_id, 0 FROM taxon WHERE col__id = ?
			UNION ALL
			SELECT t.col__id, t.col__parent_id, c.depth + 1
			  FROM taxon t
			  JOIN chain c ON t.col__id = c.parent_id
		)
		SELECT
			t.col__id,
			COALESCE(t.col__parent_id, ''),
			t.col__name_id,
			COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
			COALESCE(n.col__authorship, ''),
			COALESCE(n.col__rank_id, ''),
			COALESCE(t.col__status_id, ''),
			t.col__extinct,
			EXISTS(SELECT 1 FROM taxon x WHERE x.col__parent_id = t.col__id) AS has_children
		FROM chain c
		JOIN taxon t ON t.col__id = c.id
		LEFT JOIN name n ON n.col__id = t.col__name_id
		ORDER BY c.depth DESC`
	rows, err := a.db.QueryContext(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("core: classification %s: %w", id, err)
	}
	defer rows.Close()
	var hits []TaxonHit
	for rows.Next() {
		var h TaxonHit
		if err := rows.Scan(
			&h.ID, &h.ParentID, &h.NameID,
			&h.Name, &h.Authorship, &h.Rank,
			&h.Status, &h.Extinct, &h.HasChildren,
		); err != nil {
			return nil, fmt.Errorf("core: scan classification hit: %w", err)
		}
		h.Label = BuildLabel(h.Name, h.Authorship, h.Rank, h.Extinct.Valid && h.Extinct.Bool)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Ancestors returns the parent chain of a taxon in root-down order,
// excluding the taxon itself. Empty return means id is at root level (or
// unknown — callers checking existence should GetTaxon first).
//
// Used by the tree panes to expand the path from the root down to a
// freshly-moved taxon so it appears in the correct place without a full
// tree reload. Runs in O(depth) via a recursive CTE.
//
// Prefer Classification for callers that also need display data —
// Ancestors is the id-only shim kept for the legacy /ancestors
// endpoint and any caller that truly just needs the id chain.
func (a *Archive) Ancestors(ctx context.Context, id string) ([]string, error) {
	if id == "" {
		return nil, nil
	}
	const q = `
		WITH RECURSIVE ancestors(id, parent_id, depth) AS (
			SELECT col__id, col__parent_id, 0 FROM taxon WHERE col__id = ?
			UNION ALL
			SELECT t.col__id, t.col__parent_id, a.depth + 1
			  FROM taxon t
			  JOIN ancestors a ON t.col__id = a.parent_id
		)
		SELECT id FROM ancestors WHERE id != ? ORDER BY depth DESC`
	rows, err := a.db.QueryContext(ctx, q, id, id)
	if err != nil {
		return nil, fmt.Errorf("core: ancestors %s: %w", id, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("core: scan ancestor: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// snapshotAndMarkDescendantsDirty walks every taxon descended from
// rootID (excluding rootID itself), captures a pre-mutation snapshot
// of each, and marks each dirty via markTaxonDirtyWithPre. WithTx's
// post-commit validation sync then re-runs every affected record so
// ancestor-dependent rules (parent-genus-missing, probable incertae
// sedis, etc.) fire correctly under the new hierarchy.
//
// Callers invoke this BEFORE any mutation that changes ancestor
// context — MoveTaxon (parent changes → descendants' ancestor chain
// changes) and ReparentAndDeleteTaxon (deleted node shortens the
// chain for every descendant). Snapshot-first so old-side neighbor
// propagation sees the pre-mutation column values.
//
// Perf note: O(N) queries where N is the descendant count. For a
// deep subtree with many thousands of descendants, this can add
// noticeable latency to a move commit; acceptable for v0 since
// large-scale moves are rare and correctness is more important
// than speed. A future optimization would batch the snapshots into
// a single SELECT.
func (t *Tx) snapshotAndMarkDescendantsDirty(rootID string) error {
	const q = `
		WITH RECURSIVE descendants(id) AS (
			SELECT col__id FROM taxon WHERE col__parent_id = ?
			UNION ALL
			SELECT c.col__id
			  FROM taxon c
			  JOIN descendants d ON c.col__parent_id = d.id
		)
		SELECT id FROM descendants`
	rows, err := t.tx.QueryContext(t.ctx, q, rootID)
	if err != nil {
		return fmt.Errorf("core: enumerate descendants of %s: %w", rootID, err)
	}
	var ids []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return fmt.Errorf("core: scan descendant of %s: %w", rootID, err)
		}
		ids = append(ids, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("core: iterate descendants of %s: %w", rootID, err)
	}
	for _, d := range ids {
		pre, _ := t.snapshotTaxon(d)
		t.markTaxonDirtyWithPre(d, pre)
	}
	return nil
}

// isDescendantOf returns true when candidateID lies in the subtree rooted at
// ancestorID (inclusive of ancestorID itself). Uses a recursive CTE so it
// stays O(depth) instead of O(tree) via repeated parent-chain walks.
func (t *Tx) isDescendantOf(candidateID, ancestorID string) (bool, error) {
	const q = `
		WITH RECURSIVE descendants(id) AS (
			SELECT col__id FROM taxon WHERE col__id = ?
			UNION ALL
			SELECT t.col__id
			  FROM taxon t
			  JOIN descendants d ON t.col__parent_id = d.id
		)
		SELECT COUNT(*) FROM descendants WHERE id = ?`
	var n int
	if err := t.tx.QueryRowContext(t.ctx, q, ancestorID, candidateID).Scan(&n); err != nil {
		return false, fmt.Errorf("core: cycle-check %s under %s: %w", candidateID, ancestorID, err)
	}
	return n > 0, nil
}

// DeleteTaxon removes a taxon and its per-taxon associations (synonyms,
// vernaculars, distributions, media, treatments, species estimates, taxon
// properties, and both directions of species-interaction and
// taxon-concept-relation rows).
//
// The taxon's associated `name` row is NOT deleted — names are shared
// entities across the archive and often referenced by other taxa or
// synonyms. Curators wanting to delete a name go through DeleteName.
//
//   - If the taxon has children, DeleteTaxon returns ErrConflict without
//     modifying anything. Curators must reparent or delete children first.
//     A bulk DeleteSubtree(id) is a future addition.
//   - Missing taxon returns ErrNotFound.
//
// This method exists because the sfga schema does not declare ON DELETE
// CASCADE on FK columns pointing at taxon; without hive doing the cascade
// explicitly, the DELETE would fail with a foreign-key error.
func (t *Tx) DeleteTaxon(id string) error {
	if id == "" {
		return fmt.Errorf("core: delete taxon: %w: id required", ErrValidation)
	}

	preSnapshot, _ := t.snapshotTaxon(id)

	// Refuse if children exist.
	var childCount int
	if err := t.tx.QueryRowContext(t.ctx,
		"SELECT COUNT(*) FROM taxon WHERE col__parent_id = ?", id,
	).Scan(&childCount); err != nil {
		return fmt.Errorf("core: count children of %s: %w", id, err)
	}
	if childCount > 0 {
		return fmt.Errorf(
			"core: delete taxon %s: %w: has %d children (reparent or delete them first)",
			id, ErrConflict, childCount,
		)
	}

	// One-sided dependents: rows keyed by col__taxon_id.
	singleFKTables := []string{
		"synonym",
		"vernacular",
		"distribution",
		"media",
		"treatment",
		"species_estimate",
		"taxon_property",
	}
	for _, tbl := range singleFKTables {
		if _, err := t.tx.ExecContext(t.ctx,
			// Table names come from a fixed const slice — no user input.
			// SQL bind params handle the id.
			"DELETE FROM "+tbl+" WHERE col__taxon_id = ?", id,
		); err != nil {
			return fmt.Errorf("core: delete %s for taxon %s: %w", tbl, id, err)
		}
	}

	// Bi-directional dependents: the taxon may appear on either side of the
	// relation.
	biFKTables := []string{
		"species_interaction",
		"taxon_concept_relation",
	}
	for _, tbl := range biFKTables {
		if _, err := t.tx.ExecContext(t.ctx,
			"DELETE FROM "+tbl+" WHERE col__taxon_id = ? OR col__related_taxon_id = ?",
			id, id,
		); err != nil {
			return fmt.Errorf("core: delete %s for taxon %s: %w", tbl, id, err)
		}
	}

	// Finally the taxon row itself.
	res, err := t.tx.ExecContext(t.ctx,
		"DELETE FROM taxon WHERE col__id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("core: delete taxon %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete taxon %s rows affected: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("core: delete taxon %s: %w", id, ErrNotFound)
	}
	t.markTaxonDeleted(id, preSnapshot)
	return nil
}

// TaxonDeletePreview summarizes what a cascade delete of the given
// taxon would remove: the descendant set (including the taxon itself)
// and the per-taxon association rows attached to any member of that
// set. Returned to the WUI's delete-confirm modal so curators see
// concrete numbers before they type DELETE.
//
// DirectChildCount is broken out separately so the modal can pick the
// right flow (leaf → simple confirm; parent → three-option UI).
// DescendantCount includes the taxon itself.
type TaxonDeletePreview struct {
	DirectChildCount     int
	DescendantCount      int // includes self
	SynonymCount         int
	VernacularCount      int
	DistributionCount    int
	MediaCount           int
	TreatmentCount       int
	SpeciesEstimateCount int
	TaxonPropertyCount   int
	SpeciesInteractionCount   int
	TaxonConceptRelationCount int
	// ParentID of the taxon being previewed. Empty when the taxon is a
	// root — reparent-flow callers use this to decide the destination
	// (children become new roots when their parent was already a root).
	ParentID string
}

// TaxonDeletePreview computes the counts a cascade delete would touch.
// Read-only; runs in its own connection since it's a read path.
func (a *Archive) TaxonDeletePreview(ctx context.Context, id string) (TaxonDeletePreview, error) {
	var p TaxonDeletePreview
	if id == "" {
		return p, fmt.Errorf("core: delete preview: %w: id required", ErrValidation)
	}

	// Fetch parent and confirm the taxon exists in one shot.
	var parentID sql.NullString
	if err := a.db.QueryRowContext(ctx,
		"SELECT col__parent_id FROM taxon WHERE col__id = ?", id,
	).Scan(&parentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, fmt.Errorf("core: delete preview %s: %w", id, ErrNotFound)
		}
		return p, fmt.Errorf("core: fetch parent for preview %s: %w", id, err)
	}
	p.ParentID = parentID.String

	// Direct children — informs the modal's flow selection.
	if err := a.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM taxon WHERE col__parent_id = ?", id,
	).Scan(&p.DirectChildCount); err != nil {
		return p, fmt.Errorf("core: count direct children of %s: %w", id, err)
	}

	// Descendant set (including self) — driven by a recursive CTE.
	// Reused as a temp view for the per-table COUNTs below via a WITH
	// clause on each query, which SQLite compiles to a single pass.
	countWithDescendants := func(sqlCore string, args ...any) (int, error) {
		q := `WITH RECURSIVE descendants(id) AS (
			SELECT col__id FROM taxon WHERE col__id = ?
			UNION ALL
			SELECT t.col__id
			  FROM taxon t
			  JOIN descendants d ON t.col__parent_id = d.id
		) ` + sqlCore
		full := append([]any{id}, args...)
		var n int
		if err := a.db.QueryRowContext(ctx, q, full...).Scan(&n); err != nil {
			return 0, err
		}
		return n, nil
	}

	var err error
	if p.DescendantCount, err = countWithDescendants(
		"SELECT COUNT(*) FROM descendants",
	); err != nil {
		return p, fmt.Errorf("core: count descendants of %s: %w", id, err)
	}

	// Per-table single-FK counts.
	for tbl, dest := range map[string]*int{
		"synonym":         &p.SynonymCount,
		"vernacular":      &p.VernacularCount,
		"distribution":    &p.DistributionCount,
		"media":           &p.MediaCount,
		"treatment":       &p.TreatmentCount,
		"species_estimate": &p.SpeciesEstimateCount,
		"taxon_property":  &p.TaxonPropertyCount,
	} {
		n, err := countWithDescendants(
			"SELECT COUNT(*) FROM " + tbl +
				" WHERE col__taxon_id IN (SELECT id FROM descendants)",
		)
		if err != nil {
			return p, fmt.Errorf("core: count %s for %s: %w", tbl, id, err)
		}
		*dest = n
	}

	// Bi-directional FK tables — count rows where either side is in
	// the descendant set. DISTINCT prevents double-counting a row that
	// has both sides in the set.
	for tbl, dest := range map[string]*int{
		"species_interaction":     &p.SpeciesInteractionCount,
		"taxon_concept_relation":  &p.TaxonConceptRelationCount,
	} {
		n, err := countWithDescendants(
			"SELECT COUNT(*) FROM " + tbl +
				" WHERE col__taxon_id IN (SELECT id FROM descendants)" +
				"    OR col__related_taxon_id IN (SELECT id FROM descendants)",
		)
		if err != nil {
			return p, fmt.Errorf("core: count %s for %s: %w", tbl, id, err)
		}
		*dest = n
	}

	return p, nil
}

// ReparentAndDeleteTaxon moves the taxon's direct children up one
// level (to its parent — empty/NULL when the taxon is a root, so
// children become new roots) and then deletes the taxon as a leaf.
// Flat one-level reparent — grandchildren stay under their parents,
// which stay under the newly-promoted children.
//
// Curators who want the deeper "delete this subtree entirely" flow
// use CascadeDeleteTaxon.
func (t *Tx) ReparentAndDeleteTaxon(id string) error {
	if id == "" {
		return fmt.Errorf("core: reparent-delete taxon: %w: id required", ErrValidation)
	}

	// Fetch the taxon's own parent id so we know where to move the
	// children. Sql.NullString handles the root case (NULL parent).
	var parentID sql.NullString
	if err := t.tx.QueryRowContext(t.ctx,
		"SELECT col__parent_id FROM taxon WHERE col__id = ?", id,
	).Scan(&parentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("core: reparent-delete taxon %s: %w", id, ErrNotFound)
		}
		return fmt.Errorf("core: fetch parent of %s: %w", id, err)
	}

	// Snapshot and mark every descendant BEFORE the move. Direct
	// children get a new parent_id (the deleted taxon's parent), and
	// grandchildren+ keep their direct parent unchanged but see a
	// shortened ancestor chain — both cases can flip ancestor-
	// dependent validation rules (parent-genus-missing, incertae
	// sedis) so the whole subtree needs re-validation after commit.
	if err := t.snapshotAndMarkDescendantsDirty(id); err != nil {
		return err
	}

	// Move children up one level. Stamp modified so the change is
	// visible in the audit trail. Same "NULL vs empty" treatment as
	// MoveTaxon — nullIfEmpty coerces "" to a proper SQL NULL.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := t.tx.ExecContext(t.ctx, `
		UPDATE taxon
		   SET col__parent_id  = ?,
		       col__modified   = ?,
		       col__modified_by = ?
		 WHERE col__parent_id = ?`,
		nullIfEmpty(parentID.String), now, t.actor, id,
	); err != nil {
		return fmt.Errorf("core: reparent children of %s: %w", id, err)
	}

	// Now that children are moved, this taxon is a leaf; the standard
	// DeleteTaxon (with its per-taxon association cleanup) applies.
	return t.DeleteTaxon(id)
}

// CascadeDeleteTaxon removes the taxon, every descendant, and every
// per-taxon association attached to any member of that set. Runs in
// a single transaction with deferred FK checks so we can delete rows
// in any order — the self-referential col__parent_id constraint waits
// until COMMIT, by which point every descendant is gone.
//
// The taxa's associated `name` rows are NOT deleted — names are shared
// across taxa and often referenced elsewhere. Curators who also want
// to remove names go through DeleteName after the cascade.
func (t *Tx) CascadeDeleteTaxon(id string) error {
	if id == "" {
		return fmt.Errorf("core: cascade delete taxon: %w: id required", ErrValidation)
	}

	// Snapshot the root of the cascade for the audit log entry. The
	// dispersed per-descendant rows aren't individually snapshotted
	// (cost O(N)) — the audit records the trigger, not every leaf.
	preSnapshot, _ := t.snapshotTaxon(id)

	// Deferred FK checks let us DELETE parents before children in the
	// taxon table without violating the self-referential constraint.
	// Scoped to this transaction; commit-time enforcement still runs.
	if _, err := t.tx.ExecContext(t.ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("core: enable deferred FK: %w", err)
	}

	// Collect the descendant set (including self). Materializing the
	// IDs into a Go slice lets each subsequent DELETE re-use the same
	// list without re-running the recursive CTE.
	const descQ = `
		WITH RECURSIVE descendants(id) AS (
			SELECT col__id FROM taxon WHERE col__id = ?
			UNION ALL
			SELECT t.col__id
			  FROM taxon t
			  JOIN descendants d ON t.col__parent_id = d.id
		)
		SELECT id FROM descendants`
	rows, err := t.tx.QueryContext(t.ctx, descQ, id)
	if err != nil {
		return fmt.Errorf("core: collect descendants of %s: %w", id, err)
	}
	var descIDs []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return fmt.Errorf("core: scan descendant: %w", err)
		}
		descIDs = append(descIDs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("core: iterate descendants: %w", err)
	}
	if len(descIDs) == 0 {
		return fmt.Errorf("core: cascade delete taxon %s: %w", id, ErrNotFound)
	}

	// Build a single IN-clause placeholder + args slice; both are
	// reused for every table cleanup below.
	placeholders := strings.TrimRight(strings.Repeat("?,", len(descIDs)), ",")
	args := make([]any, len(descIDs))
	for i, d := range descIDs {
		args[i] = d
	}

	// One-sided dependents keyed by col__taxon_id.
	singleFKTables := []string{
		"synonym",
		"vernacular",
		"distribution",
		"media",
		"treatment",
		"species_estimate",
		"taxon_property",
	}
	for _, tbl := range singleFKTables {
		q := "DELETE FROM " + tbl + " WHERE col__taxon_id IN (" + placeholders + ")"
		if _, err := t.tx.ExecContext(t.ctx, q, args...); err != nil {
			return fmt.Errorf("core: cascade delete %s: %w", tbl, err)
		}
	}

	// Bi-directional dependents where either side may be in the set.
	biFKTables := []string{
		"species_interaction",
		"taxon_concept_relation",
	}
	for _, tbl := range biFKTables {
		q := "DELETE FROM " + tbl +
			" WHERE col__taxon_id IN (" + placeholders + ")" +
			"    OR col__related_taxon_id IN (" + placeholders + ")"
		biArgs := append(append([]any{}, args...), args...)
		if _, err := t.tx.ExecContext(t.ctx, q, biArgs...); err != nil {
			return fmt.Errorf("core: cascade delete %s: %w", tbl, err)
		}
	}

	// Capture pre-delete snapshots for every descendant BEFORE the
	// DELETE so the post-commit sync can propagate to old-side
	// neighbors (external records that referenced these taxa via
	// species-interaction or taxon-concept-relation, and now don't).
	// Excludes the root — its snapshot was captured at the top of
	// this method and passed into markTaxonDeleted below.
	descPreSnaps := make(map[string]map[string]any, len(descIDs))
	for _, d := range descIDs {
		if d == id {
			continue
		}
		descPreSnaps[d], _ = t.snapshotTaxon(d)
	}

	// Delete the taxa themselves. Deferred FK checks make the order
	// irrelevant here.
	if _, err := t.tx.ExecContext(t.ctx,
		"DELETE FROM taxon WHERE col__id IN ("+placeholders+")", args...,
	); err != nil {
		return fmt.Errorf("core: cascade delete taxon set: %w", err)
	}

	// Mark every deleted taxon so post-commit sync prunes their
	// __gsvalidator_results rows and re-syncs any external neighbor
	// records (species-interaction / taxon-concept-relation on the
	// other side of the FK) that lost a reference. Root goes last
	// because it takes the explicit pre-snapshot we already captured.
	for _, d := range descIDs {
		if d == id {
			continue
		}
		t.markTaxonDeleted(d, descPreSnaps[d])
	}
	t.markTaxonDeleted(id, preSnapshot)
	return nil
}
