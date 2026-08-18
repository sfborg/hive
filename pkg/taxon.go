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

	// ParentName / ParentAuthorship / ParentRank / ParentLabel carry
	// the immediate parent taxon's display context so search
	// front-ends can disambiguate homonyms (two accepted taxa sharing
	// a canonical name) and give synonym rows a "this is where you
	// land" cue. Populated only by SearchTaxa; list endpoints
	// (ListChildrenPage, roots, Classification) leave them zero and
	// the wire converter elides the parent object when ParentName is
	// empty. Root-level taxa (no parent) also leave them zero.
	ParentName       string
	ParentAuthorship string
	ParentRank       string
	ParentLabel      Label
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
		COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, ''),
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
		COALESCE(NULLIF(gn__canonical_full, ''), NULLIF(gn__canonical_simple, ''), col__scientific_name, ''),
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

// SearchMode selects the matching algorithm SearchTaxa uses to find
// candidate names. Prefix — the default — is fast and unambiguous but
// only matches names that start with the query. Partial uses an FTS5
// mirror to match on any word-prefix in the canonical or scientific
// name string, so typing an epithet like "rusci" finds
// "Ceroplastes rusci". Fuzzy (added in a follow-up step) will use a
// trigram FTS5 mirror to tolerate typos.
type SearchMode string

const (
	SearchModePrefix  SearchMode = "prefix"
	SearchModePartial SearchMode = "partial"
	SearchModeFuzzy   SearchMode = "fuzzy" // TODO(step-3)
)

// SearchOpts bundles the knobs SearchTaxa accepts. Zero value
// (empty Mode, IncludeSynonyms=false, Limit=0) is a valid call that
// behaves as prefix-only, accepted-only, limit=50 — the default
// combobox contract.
type SearchOpts struct {
	// Mode selects the matching algorithm. Empty is treated as
	// SearchModePrefix so calls that don't care about mode stay
	// compatible.
	Mode SearchMode
	// IncludeSynonyms extends results with accepted taxa reached
	// via a matching synonym (see SearchTaxa for the resolution
	// semantics).
	IncludeSynonyms bool
	// Limit caps the number of returned hits. Zero → 50.
	Limit int
}

// SearchTaxa returns up to opts.Limit taxa whose associated name
// canonical or scientific-name string matches q under opts.Mode.
// Returns thin TaxonHit projections — same shape as ListChildren so
// the WUI's tree components can render either result set uniformly.
//
// When opts.IncludeSynonyms is true, the result set also includes
// accepted taxa reached via a matching synonym: for each synonym
// whose name matches q, the accepted taxon it points at appears in
// the results with IsSynonym=true and MatchedName set to the
// synonym's text. Pro-parte synonyms — one synonym row family
// pointing at multiple accepted taxa — produce one hit per resolved
// accepted taxon so curators see every destination. If the same
// accepted taxon matches both by its own name and via a synonym,
// the accepted row wins; the synonym row is dropped.
//
// Ordering is alphabetical by matched_name so synonym and accepted
// hits interleave in the order curators would look for them.
//
// Every hit carries parent context (ParentName / ParentLabel /
// ParentRank) so front-ends can disambiguate homonyms and give
// synonym rows a "you land under X" cue. Root-level accepted taxa
// leave the parent fields empty.
//
// See DEFERRED.md § Substring name search (FTS follow-up) for the
// substring-semantics extension that composes with partial mode.
func (a *Archive) SearchTaxa(ctx context.Context, q string, opts SearchOpts) ([]TaxonHit, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	var (
		body string
		args []any
		err  error
	)
	switch opts.Mode {
	case "", SearchModePrefix:
		body, args = a.searchArmsPrefix(q, opts.IncludeSynonyms, opts.Limit)
	case SearchModePartial:
		body, args, err = a.searchArmsPartial(q, opts.IncludeSynonyms, opts.Limit)
		if err != nil {
			return nil, err
		}
	case SearchModeFuzzy:
		body, args, err = a.searchArmsFuzzy(q, opts.IncludeSynonyms, opts.Limit)
		if err != nil {
			return nil, err
		}
		if body == "" {
			// Query too short to yield trigrams (< 3 characters) —
			// fall back to prefix so the curator gets *some*
			// candidates for a 1- or 2-char lead. Also matches the
			// combobox's implicit contract: whatever they typed
			// produces reasonable results.
			body, args = a.searchArmsPrefix(q, opts.IncludeSynonyms, opts.Limit)
		}
	default:
		return nil, fmt.Errorf("core: search taxa: unknown mode %q: %w", opts.Mode, ErrValidation)
	}
	if body == "" {
		return nil, nil
	}
	// Over-fetch for FTS modes so the reranker sees enough candidates
	// to promote a bm25-underranked target. Ceroplastes ranks ~13th
	// by bm25 for query "Cerpolastes" (Herpolasia trigram-overlaps
	// more); a top-10 fetch would never reach the reranker. Prefix
	// mode doesn't rerank, so no over-fetch needed there.
	outerLimit := opts.Limit
	if opts.Mode == SearchModePartial || opts.Mode == SearchModeFuzzy {
		outerLimit = max(opts.Limit*10, 100)
	}
	args = append(args, outerLimit)
	rows, err := a.db.QueryContext(ctx, wrapSearchBody(body), args...)
	if err != nil {
		return nil, fmt.Errorf("core: search taxa %q: %w", q, err)
	}
	defer rows.Close()
	hits, err := scanSearchHits(rows)
	if err != nil {
		return nil, err
	}
	// Composite re-ranking for the FTS-backed modes. Prefix mode's
	// SQL ordering (alphabetical by matched_name) is already the
	// right UX — every candidate is an equally-good position-0 match
	// and curators want stable A-Z browsing. Partial and fuzzy modes
	// come out of the arm ranked by bm25; layering the taxonomic
	// signals (prefix anchor, epithet position, capitalization hint,
	// authorship boost, edit distance) on top of that pool moves the
	// intended target closer to the top than bare bm25 manages.
	if opts.Mode == SearchModePartial || opts.Mode == SearchModeFuzzy {
		hits = a.rerankHits(q, opts.Mode, hits)
		if len(hits) > opts.Limit {
			hits = hits[:opts.Limit]
		}
	}
	return hits, nil
}

// wrapSearchBody wraps a mode-specific UNION ALL body in the shared
// outer pipeline: ROW_NUMBER dedup preferring the accepted row per
// taxon id, LEFT JOINs for parent context, final ORDER BY + LIMIT.
// Callers append the outer LIMIT to their args slice.
//
// Parent context (pn.gn__canonical_simple / col__authorship /
// col__rank_id) is joined on the OUTER select — after dedup and
// LIMIT — so the JOIN runs on at most `limit` rows (typically ≤ 50).
// Both joins are PK lookups; LEFT JOIN keeps root-level taxa (empty
// parent_id) and orphaned parent links from dropping the row.
// Each search arm supplies a rank column: 0 for prefix arms (no
// FTS ordering signal, tiebreak on matched_name below), and
// bm25(...) for FTS-backed arms (lower = better match by SQLite's
// convention). The wrapper's dedup and outer ORDER BY both key on
// rank first, so relevance ordering from partial and fuzzy modes
// survives into the final result set — the pre-rank version
// silently re-alphabetized everything and threw away bm25's work.
func wrapSearchBody(body string) string {
	return `SELECT
		picked.id,
		picked.parent_id,
		picked.name_id,
		picked.display_name,
		picked.authorship,
		picked.rank_id,
		picked.status_id,
		picked.extinct,
		picked.is_synonym,
		picked.matched_name,
		EXISTS(SELECT 1 FROM taxon c WHERE c.col__parent_id = picked.id) AS has_children,
		COALESCE(NULLIF(pn.gn__canonical_full, ''), NULLIF(pn.gn__canonical_simple, ''), pn.col__scientific_name, '') AS parent_name,
		COALESCE(pn.col__authorship, '') AS parent_authorship,
		COALESCE(pn.col__rank_id, '') AS parent_rank
	FROM (
		SELECT *, ROW_NUMBER() OVER (
			PARTITION BY id ORDER BY is_synonym, rank, matched_name
		) AS rn
		FROM (` + body + `)
	) picked
	LEFT JOIN taxon pt ON pt.col__id = picked.parent_id AND picked.parent_id <> ''
	LEFT JOIN name  pn ON pn.col__id = pt.col__name_id
	WHERE picked.rn = 1
	ORDER BY picked.rank, picked.matched_name
	LIMIT ?`
}

// scanSearchHits reads the shared search projection into a []TaxonHit
// slice, computing Label and ParentLabel from the raw column values.
func scanSearchHits(rows *sql.Rows) ([]TaxonHit, error) {
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
			&h.ParentName, &h.ParentAuthorship, &h.ParentRank,
		); err != nil {
			return nil, fmt.Errorf("core: scan taxa hit: %w", err)
		}
		h.IsSynonym = isSynonym == 1
		h.Label = BuildLabel(h.Name, h.Authorship, h.Rank, h.Extinct.Valid && h.Extinct.Bool)
		if h.ParentName != "" {
			// Parent labels don't carry the extinct dagger — the parent
			// is context, not the row itself. Extinct annotation belongs
			// on the row we're actually navigating to.
			h.ParentLabel = BuildLabel(h.ParentName, h.ParentAuthorship, h.ParentRank, false)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// searchArmsPrefix builds the UNION ALL body for prefix-mode search:
// two arms per source (canonical + scientific text columns), each
// hitting a dedicated NOCASE index (an OR predicate on both columns
// would prevent the planner from using either). Per-arm LIMIT keeps
// the intermediate result set bounded even when the query is a very
// common prefix (a single letter); the outer dedup + LIMIT then
// picks the top `limit` after cross-arm collapsing.
func (a *Archive) searchArmsPrefix(q string, includeSynonyms bool, limit int) (string, []any) {
	pattern := stripLikeWildcards(q) + "%"
	acceptedArms := `
		SELECT * FROM (
			SELECT
				t.col__id AS id,
				COALESCE(t.col__parent_id, '') AS parent_id,
				t.col__name_id AS name_id,
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
				COALESCE(n.col__authorship, '') AS authorship,
				COALESCE(n.col__rank_id, '') AS rank_id,
				COALESCE(t.col__status_id, '') AS status_id,
				t.col__extinct AS extinct,
				0 AS is_synonym,
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS matched_name,
				0.0 AS rank
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
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, ''),
				COALESCE(n.col__authorship, ''),
				COALESCE(n.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				0,
				n.col__scientific_name,
				0.0
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
				COALESCE(NULLIF(acc.gn__canonical_full, ''), NULLIF(acc.gn__canonical_simple, ''), acc.col__scientific_name, ''),
				COALESCE(acc.col__authorship, ''),
				COALESCE(acc.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				1,
				COALESCE(NULLIF(sname.gn__canonical_full, ''), sname.gn__canonical_simple),
				0.0
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
				COALESCE(NULLIF(acc.gn__canonical_full, ''), NULLIF(acc.gn__canonical_simple, ''), acc.col__scientific_name, ''),
				COALESCE(acc.col__authorship, ''),
				COALESCE(acc.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				1,
				sname.col__scientific_name,
				0.0
			FROM name sname
			JOIN synonym s ON s.col__name_id = sname.col__id
			JOIN taxon t ON t.col__id = s.col__taxon_id
			JOIN name acc ON acc.col__id = t.col__name_id
			WHERE sname.col__scientific_name LIKE ? COLLATE NOCASE
			LIMIT ?
		)`
	body := acceptedArms
	args := []any{pattern, limit, pattern, limit}
	if includeSynonyms {
		body += synonymArms
		args = append(args, pattern, limit, pattern, limit)
	}
	return body, args
}

// searchArmsPartial builds the UNION ALL body for partial-mode
// search: token-prefix MATCH against the hive__name_fts mirror.
// One accepted arm and (when opts.IncludeSynonyms is set) one
// synonym arm — the FTS MATCH searches both indexed columns at
// once, so we don't need the per-column split the prefix mode
// requires.
//
// buildFTSMatch runs gnparser on the query first: parseable
// binomials return their canonical (authorship stripped) as the
// tokenized input, so pasting "Panthera leo (Linnaeus, 1758)" is
// equivalent to typing "Panthera leo". Unparseable input is
// tokenized as-is by whitespace-splitting.
//
// Returns "" body if the query yields no tokens — the caller
// treats that as an empty result set.
func (a *Archive) searchArmsPartial(q string, includeSynonyms bool, limit int) (string, []any, error) {
	match := a.buildFTSMatch(q)
	if match == "" {
		return "", nil, nil
	}
	// Per-arm LIMIT is inflated (perArmLimit) so the FTS pool
	// contains enough relevant candidates to survive dedup and
	// outer alphabetization. bm25 ordering picks the most relevant
	// candidates per FTS5's built-in relevance score — shorter /
	// rarer-token matches float up. Step 4's composite ranking will
	// override this with a taxonomy-aware score (epithet-position,
	// capitalization hint, authorship boost); for now bm25 is a
	// sensible interim so the top-N always contains the "obvious"
	// matches for a query.
	perArmLimit := max(limit*4, 50)
	acceptedArm := `
		SELECT * FROM (
			SELECT
				t.col__id AS id,
				COALESCE(t.col__parent_id, '') AS parent_id,
				t.col__name_id AS name_id,
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
				COALESCE(n.col__authorship, '') AS authorship,
				COALESCE(n.col__rank_id, '') AS rank_id,
				COALESCE(t.col__status_id, '') AS status_id,
				t.col__extinct AS extinct,
				0 AS is_synonym,
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS matched_name,
				bm25(hive__name_fts) AS rank
			FROM hive__name_fts fts
			JOIN name n ON n.rowid = fts.rowid
			JOIN taxon t ON t.col__name_id = n.col__id
			WHERE hive__name_fts MATCH ?
			ORDER BY bm25(hive__name_fts)
			LIMIT ?
		)`
	synonymArm := `
		UNION ALL
		SELECT * FROM (
			SELECT
				t.col__id,
				COALESCE(t.col__parent_id, ''),
				t.col__name_id,
				COALESCE(NULLIF(acc.gn__canonical_full, ''), NULLIF(acc.gn__canonical_simple, ''), acc.col__scientific_name, ''),
				COALESCE(acc.col__authorship, ''),
				COALESCE(acc.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				1,
				COALESCE(NULLIF(sname.gn__canonical_full, ''), NULLIF(sname.gn__canonical_simple, ''), sname.col__scientific_name, ''),
				bm25(hive__name_fts)
			FROM hive__name_fts fts
			JOIN name sname ON sname.rowid = fts.rowid
			JOIN synonym s ON s.col__name_id = sname.col__id
			JOIN taxon t ON t.col__id = s.col__taxon_id
			JOIN name acc ON acc.col__id = t.col__name_id
			WHERE hive__name_fts MATCH ?
			ORDER BY bm25(hive__name_fts)
			LIMIT ?
		)`
	body := acceptedArm
	args := []any{match, perArmLimit}
	if includeSynonyms {
		body += synonymArm
		args = append(args, match, perArmLimit)
	}
	return body, args, nil
}

// buildFTSMatch turns a user query into an FTS5 MATCH expression.
// Uses gnparser to detect binomials and strip authorship, then
// converts the remaining canonical (or the raw input for
// unparseable queries) into a space-joined list of quoted
// token-prefix terms — the FTS5 form that supports word-boundary
// matches in any order.
//
// Returns "" for a query that yields no usable tokens (empty
// input, all-punctuation input, etc.) so the caller can short-
// circuit to an empty result set.
func (a *Archive) buildFTSMatch(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	// Try gnparser first — a well-formed binomial + authorship
	// ("Panthera leo (Linnaeus, 1758)") parses cleanly and the
	// canonical drops the authorship for us. Cardinality >= 2
	// signals the parser recognized a multi-token name; single-
	// word fragments (Cardinality == 1) and unparseable strings
	// (Cardinality == 0) fall through to the raw-text path.
	a.parserMu.Lock()
	parsed := a.parser.ParseName(q).Flatten()
	a.parserMu.Unlock()
	text := q
	if parsed.Cardinality >= 2 && parsed.CanonicalSimple != "" {
		text = parsed.CanonicalSimple
	}
	return ftsMatchFromTokens(strings.Fields(text))
}

// ftsMatchFromTokens joins tokens into an FTS5 MATCH expression of
// the form `"tok1"* "tok2"* …`. Each token is double-quoted so
// hyphens / punctuation don't collide with FTS5's query operators,
// and suffixed with `*` for token-prefix matching. Embedded double
// quotes are escaped by doubling per FTS5 syntax.
func ftsMatchFromTokens(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		t = strings.ReplaceAll(t, `"`, `""`)
		parts = append(parts, `"`+t+`"*`)
	}
	return strings.Join(parts, " ")
}

// searchArmsFuzzy builds the UNION ALL body for fuzzy-mode search:
// trigram-OR MATCH against the hive__name_fts_tri mirror. The
// query is broken into its own overlapping 3-character sequences
// (via buildFTSTrigramMatch), OR-joined, and the FTS bm25 rank
// picks the rows with the most trigram overlap.
//
// One accepted arm and (when includeSynonyms is set) one synonym
// arm — the trigram tokenizer indexes both name columns together,
// so no per-column split is needed.
//
// Returns "" body when the query yields no trigrams (< 3 chars);
// the caller falls back to prefix mode in that case.
//
// Interim bm25 ranking sometimes lets long noise-y names outrank
// the intended match (e.g., "Osmia hyperplastica" outrunning
// "Ceroplastes" for query "Cerpolastes"). Step 4's composite
// ranking layers Levenshtein re-scoring on top to fix this.
func (a *Archive) searchArmsFuzzy(q string, includeSynonyms bool, limit int) (string, []any, error) {
	match := a.buildFTSTrigramMatch(q)
	if match == "" {
		return "", nil, nil
	}
	perArmLimit := max(limit*4, 50)
	acceptedArm := `
		SELECT * FROM (
			SELECT
				t.col__id AS id,
				COALESCE(t.col__parent_id, '') AS parent_id,
				t.col__name_id AS name_id,
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
				COALESCE(n.col__authorship, '') AS authorship,
				COALESCE(n.col__rank_id, '') AS rank_id,
				COALESCE(t.col__status_id, '') AS status_id,
				t.col__extinct AS extinct,
				0 AS is_synonym,
				COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS matched_name,
				bm25(hive__name_fts_tri) AS rank
			FROM hive__name_fts_tri fts
			JOIN name n ON n.rowid = fts.rowid
			JOIN taxon t ON t.col__name_id = n.col__id
			WHERE hive__name_fts_tri MATCH ?
			ORDER BY bm25(hive__name_fts_tri)
			LIMIT ?
		)`
	synonymArm := `
		UNION ALL
		SELECT * FROM (
			SELECT
				t.col__id,
				COALESCE(t.col__parent_id, ''),
				t.col__name_id,
				COALESCE(NULLIF(acc.gn__canonical_full, ''), NULLIF(acc.gn__canonical_simple, ''), acc.col__scientific_name, ''),
				COALESCE(acc.col__authorship, ''),
				COALESCE(acc.col__rank_id, ''),
				COALESCE(t.col__status_id, ''),
				t.col__extinct,
				1,
				COALESCE(NULLIF(sname.gn__canonical_full, ''), NULLIF(sname.gn__canonical_simple, ''), sname.col__scientific_name, ''),
				bm25(hive__name_fts_tri)
			FROM hive__name_fts_tri fts
			JOIN name sname ON sname.rowid = fts.rowid
			JOIN synonym s ON s.col__name_id = sname.col__id
			JOIN taxon t ON t.col__id = s.col__taxon_id
			JOIN name acc ON acc.col__id = t.col__name_id
			WHERE hive__name_fts_tri MATCH ?
			ORDER BY bm25(hive__name_fts_tri)
			LIMIT ?
		)`
	body := acceptedArm
	args := []any{match, perArmLimit}
	if includeSynonyms {
		body += synonymArm
		args = append(args, match, perArmLimit)
	}
	return body, args, nil
}

// buildFTSTrigramMatch turns a user query into an FTS5 MATCH
// expression for the trigram-tokenized mirror. Splits the query
// into overlapping 3-character windows, dedupes, quotes each
// trigram, and joins with OR so any partial match returns
// candidates (bm25 then ranks by cumulative trigram overlap so
// higher-overlap rows float to the top).
//
// gnparser preprocessing mirrors buildFTSMatch: a parseable
// binomial has its authorship stripped before trigram generation,
// so pasting "Panthera leo (Linnaeus, 1758)" doesn't produce
// authorship trigrams that would pollute the OR predicate.
//
// Query is lowercased before trigram generation to match the
// trigram tokenizer's case-folded index (unicode61-style case
// folding is baked into the trigram tokenizer).
//
// Trigram count is capped at 30 to bound OR expansion. Longer
// queries drop trigrams from the middle — start and end carry
// more signal for anchor / termination matching. Returns "" for
// queries that yield no trigrams (< 3 characters after preprocess).
func (a *Archive) buildFTSTrigramMatch(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	a.parserMu.Lock()
	parsed := a.parser.ParseName(q).Flatten()
	a.parserMu.Unlock()
	text := q
	if parsed.Cardinality >= 2 && parsed.CanonicalSimple != "" {
		text = parsed.CanonicalSimple
	}
	trigrams := generateTrigrams(strings.ToLower(text), 15)
	if len(trigrams) == 0 {
		return ""
	}
	parts := make([]string, len(trigrams))
	for i, tg := range trigrams {
		parts[i] = `"` + strings.ReplaceAll(tg, `"`, `""`) + `"`
	}
	return strings.Join(parts, " OR ")
}

// generateTrigrams builds a deduped list of overlapping 3-rune
// windows from s. Order-preserving; skips repeats. Returns nil for
// inputs shorter than 3 runes.
//
// If cap > 0 and the deduped list exceeds cap, keeps the first
// half from the beginning and the remainder from the end. The
// middle of a long query carries the weakest disambiguating signal
// — start-anchor and end-anchor trigrams help identify which name
// the curator meant.
func generateTrigrams(s string, cap int) []string {
	r := []rune(s)
	if len(r) < 3 {
		return nil
	}
	seen := make(map[string]bool, len(r))
	out := make([]string, 0, len(r)-2)
	for i := 0; i <= len(r)-3; i++ {
		tg := string(r[i : i+3])
		if seen[tg] {
			continue
		}
		seen[tg] = true
		out = append(out, tg)
	}
	if cap > 0 && len(out) > cap {
		keep := cap / 2
		head := out[:keep]
		tail := out[len(out)-(cap-keep):]
		out = append(append(make([]string, 0, cap), head...), tail...)
	}
	return out
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
		COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
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
			COALESCE(NULLIF(n.gn__canonical_full, ''), NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS display_name,
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
