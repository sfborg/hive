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

// SearchTaxa returns up to `limit` taxa whose associated name canonical or
// scientific-name string matches q as a case-insensitive substring. Returns
// thin TaxonHit projections — same shape as ListChildren so the WUI's
// tree components can render either result set uniformly.
//
// Ordering is alphabetical by display name for a stable client experience.
// Only taxa with an attached name row are returned; a taxon whose col__name_id
// doesn't resolve is skipped (rare — legacy archives may have orphaned rows).
func (a *Archive) SearchTaxa(ctx context.Context, q string, limit int) ([]TaxonHit, error) {
	if limit <= 0 {
		limit = 50
	}
	const query = `SELECT
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
	JOIN name n ON n.col__id = t.col__name_id
	WHERE LOWER(n.gn__canonical_simple) LIKE LOWER(?)
	   OR LOWER(n.col__scientific_name) LIKE LOWER(?)
	ORDER BY display_name
	LIMIT ?`

	pattern := "%" + q + "%"
	rows, err := a.db.QueryContext(ctx, query, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("core: search taxa %q: %w", q, err)
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
			return nil, fmt.Errorf("core: scan taxa hit: %w", err)
		}
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
	var total int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM taxon WHERE COALESCE(col__parent_id, '') = ?`,
		parentID,
	).Scan(&total); err != nil {
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
	WHERE COALESCE(t.col__parent_id, '') = ?
	ORDER BY t.col__ordinal IS NULL, t.col__ordinal, display_name`

	args := []any{parentID}
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
	return nil
}

// Ancestors returns the parent chain of a taxon in root-down order,
// excluding the taxon itself. Empty return means id is at root level (or
// unknown — callers checking existence should GetTaxon first).
//
// Used by the tree panes to expand the path from the root down to a
// freshly-moved taxon so it appears in the correct place without a full
// tree reload. Runs in O(depth) via a recursive CTE.
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
	return nil
}
