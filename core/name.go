package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gnames/gnlib/ent/nomcode"
	"github.com/gnames/gnparser"
	"github.com/gnames/gnparser/ent/parsed"
	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// fillFromParse copies structural fields from gnparser's atomized output
// into n WHEN THE CALLER LEFT THEM EMPTY. Never overwrites a caller-
// supplied value — an explicit override survives even if the parser
// disagrees. Unparseable names contribute nothing (empty parse → no
// fills), which matches the "gn__parse_quality = 0 is a soft signal,
// not an error" policy.
//
// Structural fields covered:
//   * col__authorship (raw)
//   * col__uninomial, col__genus, col__infrageneric_epithet (subgenus),
//     col__specific_epithet, col__infraspecific_epithet,
//     col__cultivar_epithet
//   * col__combination_authorship, col__combination_ex_authorship,
//     col__combination_authorship_year
//   * col__basionym_authorship, col__basionym_ex_authorship,
//     col__basionym_authorship_year
//
// NOT covered (deferred — need code-scoped rank rules that also read
// cardinality + suffix, coming in step 2):
//   * col__rank_id — RankGuess belongs on the API/preview side so the
//     curator sees the guess before commit.
//   * col__notho_id — gnparser returns notho as a lowercase word
//     ("genus", "species", …); mapping to sfga's nom_part enum needs
//     one lookup pass. Skipped for now to keep this change small; a
//     rare-enough case that manual entry is fine in the meantime.
func fillFromParse(n *coldp.Name, p parsed.ParsedFlat) {
	// Fill helper: set target to source when target is empty. Small
	// but the CreateName pipeline has ~13 candidates so a helper keeps
	// the callsite scannable.
	fill := func(dst *string, src string) {
		if *dst == "" && src != "" {
			*dst = src
		}
	}
	fill(&n.Authorship, p.Authorship)
	fill(&n.Uninomial, p.Uninomial)
	fill(&n.Genus, p.Genus)
	fill(&n.InfragenericEpithet, p.Subgenus)
	fill(&n.SpecificEpithet, p.Species)
	fill(&n.InfraspecificEpithet, p.Infraspecies)
	fill(&n.CultivarEpithet, p.CultivarEpithet)
	fill(&n.CombinationAuthorship, p.CombinationAuthorship)
	fill(&n.CombinationExAuthorship, p.CombinationExAuthorship)
	fill(&n.CombinationAuthorshipYear, p.CombinationAuthorshipYear)
	fill(&n.BasionymAuthorship, p.BasionymAuthorship)
	fill(&n.BasionymExAuthorship, p.BasionymExAuthorship)
	fill(&n.BasionymAuthorshipYear, p.BasionymAuthorshipYear)
}

// PrimaryReferenceID returns the first entry from name.col__reference_id.
// sfga stores this column as a comma-separated list (a name can be cited
// in multiple references), but hive v1 treats it as a single primary
// value — the picker and PATCH surface both operate on one id at a time.
// Multi-reference support is deferred; existing multi-entry values on
// read are preserved through Update (only the primary is exposed to the
// picker; the raw column round-trips untouched otherwise).
func PrimaryReferenceID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if first, _, ok := strings.Cut(raw, ","); ok {
		return strings.TrimSpace(first)
	}
	return raw
}

// ParseNamePreview runs the archive's gnparser on a verbatim scientific
// name and returns a coldp.Name populated with everything the parser can
// derive — gn__* cache + atomized col__* fields + a rank guess (via
// RankGuess) scoped to the given nom_code. The returned Name is a
// *preview* only: no row is written, ID is left empty, Modified/ModifiedBy
// are not stamped.
//
// Backs the /api/name/parse endpoint that drives the two-step name-add
// form. The same Archive gnparser instance is used (single-flight via
// parserMu) so the preview and the eventual CreateName agree on the
// atomization — same version, same options, same result.
func (a *Archive) ParseNamePreview(codeID, verbatim string) *coldp.Name {
	verbatim = strings.TrimSpace(verbatim)
	if verbatim == "" {
		return &coldp.Name{}
	}
	// Feed the nomenclatural code into gnparser so its code-specific
	// tuning kicks in — cultivar quotes, botanical hybrid marks,
	// zoological subgenus parentheses, and other edge cases parse
	// more accurately when the code is known. Only the first-ever
	// taxon in an empty archive has no code context; every other
	// create inherits from the parent chain via CodeForParent.
	// ChangeConfig returns a variant parser without mutating the
	// shared instance, so parserMu still just guards the shared
	// parser's not-known-to-be-concurrent-safe state.
	a.parserMu.Lock()
	p := a.parser.ChangeConfig(gnparser.OptCode(parseCodeID(codeID))).
		ParseName(verbatim).Flatten()
	a.parserMu.Unlock()

	n := &coldp.Name{
		ScientificName:       verbatim,
		ScientificNameString: verbatim,
		Code:                 parseCodeID(codeID),
	}
	// Reuse the same fill helper CreateName uses so the preview matches
	// what will actually be written, byte-for-byte.
	fillFromParse(n, p)
	// gn__* cache — also filled here so the preview response is complete.
	n.ParseQuality = sqlNullInt(int64(p.ParseQuality))
	n.CanonicalSimple = p.CanonicalSimple
	n.CanonicalFull = p.CanonicalFull
	n.CanonicalStemmed = p.CanonicalStemmed
	n.Cardinality = sqlNullInt(int64(p.Cardinality))
	n.Virus = sqlNullBool(p.Virus)
	n.Hybrid = p.Hybrid
	n.Surrogate = p.Surrogate
	n.Authors = p.Authors
	n.GnID = p.VerbatimID
	// Rank guess — code-scoped, curator can override in the preview form.
	if guess := RankGuess(codeID, p, p.CanonicalSimple); guess != "" {
		n.Rank = ParseRank(guess)
	}
	return n
}

// parseCodeID mirrors core.ParseRank / ParseNomStatus: empty-in maps to
// empty-out so an unset code stays unset instead of coercing to a zero
// enum. gnlib's nomcode.New is stricter; wrapping it keeps the "empty
// means unset" convention consistent across all the enum-safe helpers.
func parseCodeID(id string) nomcode.Code {
	if id == "" {
		return nomcode.Code(0)
	}
	return nomcode.New(id)
}

func sqlNullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
func sqlNullBool(v bool) sql.NullBool  { return sql.NullBool{Bool: v, Valid: true} }

// NameHit is the search-result projection returned by SearchNames. Callers
// fetch the full coldp.Name only when opening a detail view.
type NameHit struct {
	ID         string
	Scientific string // gn__canonical_simple falling back to col__scientific_name
	Full       string // col__scientific_name (raw)
	Authorship string
	Rank       string // rank ID
	Code       string // nom_code ID
	Status     string // nom_status ID
}

// GetName returns the name with the given col__id, including all gn__*
// GlobalNames fields.
//
// COALESCE folds NULL columns back to ” for the plain-string coldp.Name
// fields (see core/taxon.go for the "" ↔ NULL convention).
func (a *Archive) GetName(ctx context.Context, id string) (*coldp.Name, error) {
	const q = `SELECT
		col__id, col__alternative_id, col__source_id, tw__taxon_name_id,
		gn__scientific_name_string, gn__parse_quality,
		gn__canonical_simple, gn__canonical_full, gn__canonical_stemmed,
		gn__cardinality, gn__virus, gn__hybrid, gn__surrogate, gn__authors, gn__id,
		col__scientific_name, col__authorship,
		COALESCE(col__rank_id, ''),
		col__uninomial, col__genus, col__infrageneric_epithet,
		col__specific_epithet, col__infraspecific_epithet, col__cultivar_epithet,
		col__notho_id, col__original_spelling,
		col__combination_authorship, col__combination_authorship_id,
		col__combination_ex_authorship, col__combination_ex_authorship_id,
		col__combination_authorship_year,
		col__basionym_authorship, col__basionym_authorship_id,
		col__basionym_ex_authorship, col__basionym_ex_authorship_id,
		col__basionym_authorship_year,
		COALESCE(col__code_id, ''),
		COALESCE(col__status_id, ''),
		col__reference_id, col__published_in_year, col__published_in_page,
		col__published_in_page_link,
		COALESCE(col__gender_id, ''),
		col__gender_agreement, col__etymology,
		col__link, col__remarks, col__modified, col__modified_by
	FROM name WHERE col__id = ? LIMIT 1`

	var (
		n        coldp.Name
		rankStr  string
		codeStr  string
		statStr  string
		genStr   string
		nothoStr string
	)
	err := a.db.QueryRowContext(ctx, q, id).Scan(
		&n.ID, &n.AlternativeID, &n.SourceID, &n.TwTaxonNameID,
		&n.ScientificNameString, &n.ParseQuality,
		&n.CanonicalSimple, &n.CanonicalFull, &n.CanonicalStemmed,
		&n.Cardinality, &n.Virus, &n.Hybrid, &n.Surrogate, &n.Authors, &n.GnID,
		&n.ScientificName, &n.Authorship,
		&rankStr,
		&n.Uninomial, &n.Genus, &n.InfragenericEpithet,
		&n.SpecificEpithet, &n.InfraspecificEpithet, &n.CultivarEpithet,
		&nothoStr, &n.OriginalSpelling,
		&n.CombinationAuthorship, &n.CombinationAuthorshipID,
		&n.CombinationExAuthorship, &n.CombinationExAuthorshipID,
		&n.CombinationAuthorshipYear,
		&n.BasionymAuthorship, &n.BasionymAuthorshipID,
		&n.BasionymExAuthorship, &n.BasionymExAuthorshipID,
		&n.BasionymAuthorshipYear,
		&codeStr, &statStr,
		&n.ReferenceID, &n.PublishedInYear, &n.PublishedInPage,
		&n.PublishedInPageLink,
		&genStr,
		&n.GenderAgreement, &n.Etymology,
		&n.Link, &n.Remarks, &n.Modified, &n.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: name %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get name %s: %w", id, err)
	}

	// Routed through the empty-safe helpers so a NULL col__rank_id /
	// col__status_id read stays empty instead of being coerced into
	// UNRANKED / ESTABLISHED by sflib's defaulting. Status is stored
	// as a raw string in the hive-side cache (see NameRawStatus) so
	// NOMEN URIs survive round-trips — sflib's NomStatus enum can't
	// represent them.
	n.Rank = ParseRank(rankStr)
	n.Code = nomcode.New(codeStr)
	n.Status = ParseNomStatus(statStr)
	rememberRawStatus(n.ID, statStr)
	n.Gender = coldp.NewGender(genStr)
	n.Notho = coldp.NewNamePart(nothoStr)

	return &n, nil
}

// rawStatusCache and its guards are a hive-side sidecar to coldp.Name:
// GetName remembers the raw col__status_id string per name ID, keyed
// on the ID it just fetched, so read paths that only have a coldp.Name
// in hand can still resolve the URI. Bounded so long-running processes
// don't accumulate entries forever; oldest entries evict on overflow.
//
// A per-Archive map would be more principled but coldp.Name is passed
// around by value through multiple layers (HTTP, TUI, edit forms);
// threading an Archive reference through everything would be invasive.
// Package-level with a size cap gives the read path what it needs
// without leaking memory.
var (
	rawStatusMu     sync.Mutex
	rawStatusCache  = make(map[string]string, 512)
	rawStatusOrder  []string
	rawStatusMaxLen = 4096
)

func rememberRawStatus(nameID, raw string) {
	if nameID == "" {
		return
	}
	rawStatusMu.Lock()
	defer rawStatusMu.Unlock()
	if _, ok := rawStatusCache[nameID]; !ok {
		rawStatusOrder = append(rawStatusOrder, nameID)
		if len(rawStatusOrder) > rawStatusMaxLen {
			evict := rawStatusOrder[0]
			rawStatusOrder = rawStatusOrder[1:]
			delete(rawStatusCache, evict)
		}
	}
	rawStatusCache[nameID] = raw
}

// NameRawStatus returns the raw col__status_id string for a name we
// previously read via GetName, or "" if we haven't seen it. Callers
// that need to display or PATCH a NOMEN URI use this instead of
// n.Status.ID().
func NameRawStatus(nameID string) string {
	rawStatusMu.Lock()
	defer rawStatusMu.Unlock()
	return rawStatusCache[nameID]
}

// SearchNames returns up to `limit` names matching q as a substring against
// gn__canonical_simple, and falls back to col__scientific_name for legacy
// rows whose gn__* fields are empty. Case-insensitive. Ordered by canonical
// simple form.
//
// This is deliberately simple for v0: no rank filters, no authorship, no
// FTS operators. Advanced search is a follow-up; the CLAUDE.md § Deliberately
// deferred list calls this out.
func (a *Archive) SearchNames(ctx context.Context, q string, limit int) ([]NameHit, error) {
	if limit <= 0 {
		limit = 50
	}
	// Case-insensitive substring on canonical/scientific. The COALESCE
	// display column mirrors ListChildren's convention.
	const sql = `SELECT
		col__id,
		COALESCE(NULLIF(gn__canonical_simple, ''), col__scientific_name, '') AS display_name,
		col__scientific_name,
		col__authorship,
		COALESCE(col__rank_id, ''),
		COALESCE(col__code_id, ''),
		COALESCE(col__status_id, '')
	FROM name
	WHERE LOWER(gn__canonical_simple) LIKE LOWER(?)
	   OR LOWER(col__scientific_name) LIKE LOWER(?)
	ORDER BY display_name
	LIMIT ?`

	pattern := "%" + q + "%"
	rows, err := a.db.QueryContext(ctx, sql, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("core: search names %q: %w", q, err)
	}
	defer rows.Close()

	var hits []NameHit
	for rows.Next() {
		var h NameHit
		if err := rows.Scan(
			&h.ID, &h.Scientific, &h.Full, &h.Authorship,
			&h.Rank, &h.Code, &h.Status,
		); err != nil {
			return nil, fmt.Errorf("core: scan search hit: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("core: iterate search results: %w", err)
	}
	return hits, nil
}

// CreateName inserts a new name row.
//
// Behavior per CLAUDE.md § gn__* column policy:
//   - gnparser is always invoked on the incoming scientific-name string.
//   - All gn__* fields are populated from the parse result. Unparseable
//     names still save; gn__parse_quality = 0 is the signal, not an error.
//   - Structural col__* fields (uninomial, genus, subgenus, species,
//     infraspecies, cultivar_epithet, authorship, combination_authorship,
//     combination_authorship_year, basionym_authorship,
//     basionym_authorship_year, combination_ex_authorship,
//     basionym_ex_authorship) are auto-filled from the parse result IF
//     the caller didn't supply a value — same "fill gaps, don't overwrite"
//     rule that CoLDP import uses for gn__*. Authorship-in-title now
//     works for any name the parser can atomize (which is nearly all of
//     them); curators who need to override a bad parse can send explicit
//     col__ values on Create.
//
// If n.ID is empty a UUID v4 is generated and returned. Imported string IDs
// are preserved verbatim.
//
// If both n.ScientificNameString and n.ScientificName are empty, the write
// is rejected — sfga's schema requires both NOT NULL. gnparser needs a
// verbatim string to work with; hive uses ScientificNameString as the
// source of truth and falls back to ScientificName if the caller only
// supplied the canonical form.
func (t *Tx) CreateName(n coldp.Name) (string, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	verbatim := n.ScientificNameString
	if verbatim == "" {
		verbatim = n.ScientificName
	}
	if verbatim == "" {
		return "", fmt.Errorf("core: create name: %w: scientific name is required",
			ErrValidation)
	}
	// Symmetric fallback so both NOT NULL columns are populated.
	if n.ScientificName == "" {
		n.ScientificName = verbatim
	}
	if n.ScientificNameString == "" {
		n.ScientificNameString = verbatim
	}

	// Parse and stamp gn__* fields. gnparser is single-flight per Archive.
	t.archive.parserMu.Lock()
	parsed := t.archive.parser.ParseName(verbatim).Flatten()
	t.archive.parserMu.Unlock()

	n.ParseQuality = sql.NullInt64{Int64: int64(parsed.ParseQuality), Valid: true}
	n.CanonicalSimple = parsed.CanonicalSimple
	n.CanonicalFull = parsed.CanonicalFull
	n.CanonicalStemmed = parsed.CanonicalStemmed
	n.Cardinality = sql.NullInt64{Int64: int64(parsed.Cardinality), Valid: true}
	n.Virus = sql.NullBool{Bool: parsed.Virus, Valid: true}
	n.Hybrid = parsed.Hybrid
	n.Surrogate = parsed.Surrogate
	n.Authors = parsed.Authors
	n.GnID = parsed.VerbatimID

	// Fill-gaps semantics for structural col__* fields: caller-supplied
	// values always win, so an explicit override survives even when the
	// parser has a different opinion. Empty caller values get the parser's
	// atomization — matching how CoLDP import treats gn__*.
	fillFromParse(&n, parsed)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	const insert = `INSERT INTO name (
		col__id, col__alternative_id, col__source_id, tw__taxon_name_id,
		gn__scientific_name_string, gn__parse_quality,
		gn__canonical_simple, gn__canonical_full, gn__canonical_stemmed,
		gn__cardinality, gn__virus, gn__hybrid, gn__surrogate, gn__authors, gn__id,
		col__scientific_name, col__authorship,
		col__rank_id,
		col__uninomial, col__genus, col__infrageneric_epithet,
		col__specific_epithet, col__infraspecific_epithet, col__cultivar_epithet,
		col__notho_id, col__original_spelling,
		col__combination_authorship, col__combination_authorship_id,
		col__combination_ex_authorship, col__combination_ex_authorship_id,
		col__combination_authorship_year,
		col__basionym_authorship, col__basionym_authorship_id,
		col__basionym_ex_authorship, col__basionym_ex_authorship_id,
		col__basionym_authorship_year,
		col__code_id, col__status_id,
		col__reference_id, col__published_in_year, col__published_in_page,
		col__published_in_page_link,
		col__gender_id, col__gender_agreement, col__etymology,
		col__link, col__remarks, col__modified, col__modified_by
	) VALUES (
		?, ?, ?, ?,
		?, ?,
		?, ?, ?,
		?, ?, ?, ?, ?, ?,
		?, ?,
		?,
		?, ?, ?,
		?, ?, ?,
		?, ?,
		?, ?,
		?, ?,
		?,
		?, ?,
		?, ?,
		?,
		?, ?,
		?, ?, ?,
		?,
		?, ?, ?,
		?, ?, ?, ?
	)`

	_, err := t.tx.ExecContext(t.ctx, insert,
		n.ID, n.AlternativeID, n.SourceID, n.TwTaxonNameID,
		n.ScientificNameString, n.ParseQuality,
		n.CanonicalSimple, n.CanonicalFull, n.CanonicalStemmed,
		n.Cardinality, n.Virus, n.Hybrid, n.Surrogate, n.Authors, n.GnID,
		n.ScientificName, n.Authorship,
		// coldp enum types have .String() (lowercase, spaced — human display)
		// AND .ID() (uppercase-underscore — sfga schema value). Writes MUST
		// use .ID() so the FK targets match seeded enum-table rows.
		nullIfEmpty(n.Rank.ID()),
		n.Uninomial, n.Genus, n.InfragenericEpithet,
		n.SpecificEpithet, n.InfraspecificEpithet, n.CultivarEpithet,
		n.Notho.ID(), n.OriginalSpelling,
		n.CombinationAuthorship, n.CombinationAuthorshipID,
		n.CombinationExAuthorship, n.CombinationExAuthorshipID,
		n.CombinationAuthorshipYear,
		n.BasionymAuthorship, n.BasionymAuthorshipID,
		n.BasionymExAuthorship, n.BasionymExAuthorshipID,
		n.BasionymAuthorshipYear,
		nullIfEmpty(n.Code.ID()), nullIfEmpty(n.Status.ID()),
		n.ReferenceID, n.PublishedInYear, n.PublishedInPage,
		n.PublishedInPageLink,
		nullIfEmpty(n.Gender.ID()), n.GenderAgreement, n.Etymology,
		n.Link, n.Remarks, now, t.actor,
	)
	if err != nil {
		return "", fmt.Errorf("core: insert name %s: %w", n.ID, err)
	}
	t.markNameDirty(n.ID)
	return n.ID, nil
}

// UpdateName writes a coldp.Name back to an existing row keyed by n.ID.
//
// Semantics:
//   - n.ID is required; empty → ErrValidation.
//   - Missing row → ErrNotFound.
//   - Optimistic concurrency: if n.Modified is non-empty, it must match the
//     row's current col__modified or ErrConflict is returned.
//   - gnparser always re-runs on the incoming verbatim string. All gn__*
//     fields are refreshed from the parse result, regardless of what the
//     caller supplied. Parsing is microseconds; keeping the cache invariant
//     tight is more valuable than saving those microseconds.
//   - Symmetric fallback between ScientificName and ScientificNameString
//     (same rule as CreateName).
//   - col__modified / col__modified_by stamped from context.
func (t *Tx) UpdateName(n coldp.Name) error {
	if n.ID == "" {
		return fmt.Errorf("core: update name: %w: ID required", ErrValidation)
	}

	if n.Modified != "" {
		var current string
		err := t.tx.QueryRowContext(t.ctx,
			"SELECT COALESCE(col__modified, '') FROM name WHERE col__id = ?",
			n.ID,
		).Scan(&current)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("core: update name %s: %w", n.ID, ErrNotFound)
			}
			return fmt.Errorf("core: read current modified for name %s: %w", n.ID, err)
		}
		if current != n.Modified {
			return fmt.Errorf(
				"core: update name %s: %w: If-Match mismatch (have %q, want %q)",
				n.ID, ErrConflict, current, n.Modified,
			)
		}
	}

	verbatim := n.ScientificNameString
	if verbatim == "" {
		verbatim = n.ScientificName
	}
	if verbatim == "" {
		return fmt.Errorf(
			"core: update name %s: %w: scientific name is required",
			n.ID, ErrValidation,
		)
	}
	if n.ScientificName == "" {
		n.ScientificName = verbatim
	}
	if n.ScientificNameString == "" {
		n.ScientificNameString = verbatim
	}

	t.archive.parserMu.Lock()
	parsed := t.archive.parser.ParseName(verbatim).Flatten()
	t.archive.parserMu.Unlock()

	n.ParseQuality = sql.NullInt64{Int64: int64(parsed.ParseQuality), Valid: true}
	n.CanonicalSimple = parsed.CanonicalSimple
	n.CanonicalFull = parsed.CanonicalFull
	n.CanonicalStemmed = parsed.CanonicalStemmed
	n.Cardinality = sql.NullInt64{Int64: int64(parsed.Cardinality), Valid: true}
	n.Virus = sql.NullBool{Bool: parsed.Virus, Valid: true}
	n.Hybrid = parsed.Hybrid
	n.Surrogate = parsed.Surrogate
	n.Authors = parsed.Authors
	n.GnID = parsed.VerbatimID

	now := time.Now().UTC().Format(time.RFC3339Nano)

	const update = `UPDATE name SET
		col__alternative_id = ?, col__source_id = ?, tw__taxon_name_id = ?,
		gn__scientific_name_string = ?, gn__parse_quality = ?,
		gn__canonical_simple = ?, gn__canonical_full = ?, gn__canonical_stemmed = ?,
		gn__cardinality = ?, gn__virus = ?, gn__hybrid = ?, gn__surrogate = ?,
		gn__authors = ?, gn__id = ?,
		col__scientific_name = ?, col__authorship = ?,
		col__rank_id = ?,
		col__uninomial = ?, col__genus = ?, col__infrageneric_epithet = ?,
		col__specific_epithet = ?, col__infraspecific_epithet = ?, col__cultivar_epithet = ?,
		col__notho_id = ?, col__original_spelling = ?,
		col__combination_authorship = ?, col__combination_authorship_id = ?,
		col__combination_ex_authorship = ?, col__combination_ex_authorship_id = ?,
		col__combination_authorship_year = ?,
		col__basionym_authorship = ?, col__basionym_authorship_id = ?,
		col__basionym_ex_authorship = ?, col__basionym_ex_authorship_id = ?,
		col__basionym_authorship_year = ?,
		col__code_id = ?,
		col__reference_id = ?, col__published_in_year = ?, col__published_in_page = ?,
		col__published_in_page_link = ?,
		col__gender_id = ?, col__gender_agreement = ?, col__etymology = ?,
		col__link = ?, col__remarks = ?, col__modified = ?, col__modified_by = ?
	WHERE col__id = ?`

	// col__status_id is intentionally NOT updated here. sflib's
	// coldp.NomStatus enum can only round-trip its known CoLDP terms;
	// hive stores NOMEN URIs which the enum silently maps to Unknown
	// (empty). Status writes go through Tx.SetNameStatus so URIs
	// survive the round-trip untouched. See core/nomen.go and
	// CLAUDE.md § Nomenclatural status vocabulary.
	res, err := t.tx.ExecContext(t.ctx, update,
		n.AlternativeID, n.SourceID, n.TwTaxonNameID,
		n.ScientificNameString, n.ParseQuality,
		n.CanonicalSimple, n.CanonicalFull, n.CanonicalStemmed,
		n.Cardinality, n.Virus, n.Hybrid, n.Surrogate,
		n.Authors, n.GnID,
		n.ScientificName, n.Authorship,
		nullIfEmpty(n.Rank.ID()),
		n.Uninomial, n.Genus, n.InfragenericEpithet,
		n.SpecificEpithet, n.InfraspecificEpithet, n.CultivarEpithet,
		n.Notho.ID(), n.OriginalSpelling,
		n.CombinationAuthorship, n.CombinationAuthorshipID,
		n.CombinationExAuthorship, n.CombinationExAuthorshipID,
		n.CombinationAuthorshipYear,
		n.BasionymAuthorship, n.BasionymAuthorshipID,
		n.BasionymExAuthorship, n.BasionymExAuthorshipID,
		n.BasionymAuthorshipYear,
		nullIfEmpty(n.Code.ID()),
		n.ReferenceID, n.PublishedInYear, n.PublishedInPage,
		n.PublishedInPageLink,
		nullIfEmpty(n.Gender.ID()), n.GenderAgreement, n.Etymology,
		n.Link, n.Remarks, now, t.actor,
		n.ID,
	)
	if err != nil {
		return fmt.Errorf("core: update name %s: %w", n.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update name %s rows affected: %w", n.ID, err)
	}
	if rows == 0 {
		return fmt.Errorf("core: update name %s: %w", n.ID, ErrNotFound)
	}
	t.markNameDirty(n.ID)
	return nil
}

// DeleteName removes a name and its auxiliary rows.
//
//   - Refuses with ErrConflict if any taxon or synonym still references the
//     name via col__name_id. Curators must delete or reassign the taxa /
// SetNameStatus writes col__status_id directly as a raw string,
// bypassing sflib's coldp.NomStatus enum. Necessary because the enum
// silently maps NOMEN URIs to UnknownNomStatus (which ID()'s back to
// "") — any UpdateName that went through the enum would blank a
// URI-shaped status on every edit.
//
// Empty status writes NULL. Non-empty must exist in nom_status
// (SeedNomenIntoNomStatus takes care of the NOMEN URIs on Open).
// col__modified / col__modified_by are stamped so audit trails work
// for status-only changes too.
func (t *Tx) SetNameStatus(nameID, status string) error {
	if nameID == "" {
		return fmt.Errorf("core: set name status: %w: name id required", ErrValidation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE name
		 SET col__status_id = ?, col__modified = ?, col__modified_by = ?
		 WHERE col__id = ?`,
		nullIfEmpty(status), now, t.actor, nameID,
	)
	if err != nil {
		return fmt.Errorf("core: set name status %s: %w", nameID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: set name status %s: rows: %w", nameID, err)
	}
	if n == 0 {
		return fmt.Errorf("core: set name status %s: %w", nameID, ErrNotFound)
	}
	return nil
}

//     synonyms first — hive never orphans a taxon into a nameless state.
//   - Cascades auxiliary rows keyed by the name: type_material, name_match,
//     and name_relation (both directions — col__name_id and
//     col__related_name_id). The sfga schema does not declare ON DELETE
//     CASCADE, so hive does the cleanup explicitly.
//   - Missing name → ErrNotFound.
func (t *Tx) DeleteName(id string) error {
	if id == "" {
		return fmt.Errorf("core: delete name: %w: id required", ErrValidation)
	}

	// Refuse if any taxon or synonym still points at this name.
	var refCount int
	if err := t.tx.QueryRowContext(t.ctx,
		`SELECT (SELECT COUNT(*) FROM taxon   WHERE col__name_id = ?)
		      + (SELECT COUNT(*) FROM synonym WHERE col__name_id = ?)`,
		id, id,
	).Scan(&refCount); err != nil {
		return fmt.Errorf("core: count refs to name %s: %w", id, err)
	}
	if refCount > 0 {
		return fmt.Errorf(
			"core: delete name %s: %w: still referenced by %d taxa/synonyms",
			id, ErrConflict, refCount,
		)
	}

	// Cascade single-side FKs.
	singleFKTables := []string{
		"type_material",
		"name_match",
	}
	for _, tbl := range singleFKTables {
		if _, err := t.tx.ExecContext(t.ctx,
			"DELETE FROM "+tbl+" WHERE col__name_id = ?", id,
		); err != nil {
			return fmt.Errorf("core: delete %s for name %s: %w", tbl, id, err)
		}
	}

	// name_relation: name may be either side of the relation.
	if _, err := t.tx.ExecContext(t.ctx,
		"DELETE FROM name_relation WHERE col__name_id = ? OR col__related_name_id = ?",
		id, id,
	); err != nil {
		return fmt.Errorf("core: delete name_relation for %s: %w", id, err)
	}

	res, err := t.tx.ExecContext(t.ctx, "DELETE FROM name WHERE col__id = ?", id)
	if err != nil {
		return fmt.Errorf("core: delete name %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete name %s rows affected: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("core: delete name %s: %w", id, ErrNotFound)
	}
	return nil
}
