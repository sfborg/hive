package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// The sfga schema treats a synonym as a link row between (accepted) taxon and
// name, with an optional col__id. Hive always assigns a UUID to new synonyms
// so pro-parte relationships (one synonym → many accepted taxa) have a stable
// join key. See CLAUDE.md § Pro-parte synonymy.
//
// The natural key for a single synonym row is (col__taxon_id, col__name_id).
// A pro-parte synonym is represented as multiple rows sharing the same col__id
// but with different col__taxon_id values.

// SynonymHit is a synonym row plus a server-rendered Label for its name
// — the projection front-ends want so they don't render raw name UUIDs.
// Populated by ListSynonymHits in one query (no N+1 lookup per row). The
// Label carries text + HTML forms; see core.BuildLabel for the rules.
type SynonymHit struct {
	ID         string
	TaxonID    string
	NameID     string
	Label      Label
	NamePhrase string
	Status     string // taxonomic_status ID (raw enum ID; front-end can pretty-print)
	Link       string
	Remarks    string
	Modified   string
	ModifiedBy string
}

// ListSynonymHits returns synonyms for a taxon with each row's name
// rendered as a Label (canonical + authorship, italicized per rank). Same
// ordering as ListSynonyms — alphabetical by canonical name — so the two
// list views stay consistent across the TUI and WUI.
func (a *Archive) ListSynonymHits(ctx context.Context, taxonID string) ([]SynonymHit, error) {
	const q = `SELECT
		COALESCE(s.col__id, ''),
		s.col__taxon_id,
		s.col__name_id,
		COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name, '') AS canon,
		COALESCE(n.col__authorship, ''),
		COALESCE(n.col__rank_id, ''),
		s.col__name_phrase,
		COALESCE(s.col__status_id, ''),
		s.col__link,
		s.col__remarks,
		s.col__modified,
		s.col__modified_by
	FROM synonym s
	LEFT JOIN name n ON n.col__id = s.col__name_id
	WHERE s.col__taxon_id = ?
	ORDER BY canon`

	rows, err := a.db.QueryContext(ctx, q, taxonID)
	if err != nil {
		return nil, fmt.Errorf("core: list synonym hits of %q: %w", taxonID, err)
	}
	defer rows.Close()

	var hits []SynonymHit
	for rows.Next() {
		var (
			h                      SynonymHit
			canon, authorship, rnk string
		)
		if err := rows.Scan(
			&h.ID, &h.TaxonID, &h.NameID,
			&canon, &authorship, &rnk,
			&h.NamePhrase,
			&h.Status, &h.Link, &h.Remarks,
			&h.Modified, &h.ModifiedBy,
		); err != nil {
			return nil, fmt.Errorf("core: scan synonym hit of %q: %w", taxonID, err)
		}
		// Synonyms don't carry their own extinct flag — that's a property
		// of the (accepted) taxon, not the synonym link. No dagger here.
		if canon == "" {
			canon = h.NameID // fallback so nothing renders blank
		}
		h.Label = BuildLabel(canon, authorship, rnk, false)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// ListSynonyms returns all synonym rows pointing at the given taxon.
// Ordered by the associated name's canonical form for stable UI display.
func (a *Archive) ListSynonyms(ctx context.Context, taxonID string) ([]coldp.Synonym, error) {
	const q = `SELECT
		COALESCE(s.col__id, ''),
		s.col__taxon_id,
		COALESCE(s.col__source_id, ''),
		s.col__name_id,
		s.col__name_phrase,
		COALESCE(s.col__according_to_id, ''),
		COALESCE(s.col__status_id, ''),
		s.col__reference_id,
		s.col__link,
		s.col__remarks,
		s.col__modified,
		s.col__modified_by
	FROM synonym s
	LEFT JOIN name n ON n.col__id = s.col__name_id
	WHERE s.col__taxon_id = ?
	ORDER BY COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name)`

	rows, err := a.db.QueryContext(ctx, q, taxonID)
	if err != nil {
		return nil, fmt.Errorf("core: list synonyms of %q: %w", taxonID, err)
	}
	defer rows.Close()

	var syns []coldp.Synonym
	for rows.Next() {
		var (
			s      coldp.Synonym
			status string
		)
		if err := rows.Scan(
			&s.ID, &s.TaxonID, &s.SourceID, &s.NameID, &s.NamePhrase,
			&s.AccordingToID, &status,
			&s.ReferenceID, &s.Link, &s.Remarks,
			&s.Modified, &s.ModifiedBy,
		); err != nil {
			return nil, fmt.Errorf("core: scan synonym of %q: %w", taxonID, err)
		}
		s.Status = coldp.NewTaxonomicStatus(status)
		syns = append(syns, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("core: iterate synonyms of %q: %w", taxonID, err)
	}
	return syns, nil
}

// GetSynonym returns the single synonym link between taxonID and nameID.
// A single call fetches one row even if the synonym is pro-parte; see
// SynonymPartners to enumerate the taxa a pro-parte synonym links to.
func (a *Archive) GetSynonym(ctx context.Context, taxonID, nameID string) (*coldp.Synonym, error) {
	const q = `SELECT
		COALESCE(col__id, ''),
		col__taxon_id,
		COALESCE(col__source_id, ''),
		col__name_id,
		col__name_phrase,
		COALESCE(col__according_to_id, ''),
		COALESCE(col__status_id, ''),
		col__reference_id, col__link, col__remarks,
		col__modified, col__modified_by
	FROM synonym
	WHERE col__taxon_id = ? AND col__name_id = ?
	LIMIT 1`

	var (
		s      coldp.Synonym
		status string
	)
	err := a.db.QueryRowContext(ctx, q, taxonID, nameID).Scan(
		&s.ID, &s.TaxonID, &s.SourceID, &s.NameID, &s.NamePhrase,
		&s.AccordingToID, &status,
		&s.ReferenceID, &s.Link, &s.Remarks,
		&s.Modified, &s.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf(
				"core: synonym (taxon=%s, name=%s): %w",
				taxonID, nameID, ErrNotFound,
			)
		}
		return nil, fmt.Errorf("core: get synonym (taxon=%s, name=%s): %w", taxonID, nameID, err)
	}
	s.Status = coldp.NewTaxonomicStatus(status)
	return &s, nil
}

// SynonymPartners returns every taxon ID that a pro-parte synonym points at.
// For a non-pro-parte synonym the result is a single-element slice. Returns
// an empty slice if synonymID is unknown or is "" (the schema allows anonymous
// synonyms, but hive always assigns an ID so a hive-managed archive will not
// contain them).
func (a *Archive) SynonymPartners(ctx context.Context, synonymID string) ([]string, error) {
	if synonymID == "" {
		return nil, nil
	}
	const q = `SELECT col__taxon_id FROM synonym WHERE col__id = ? ORDER BY col__taxon_id`
	rows, err := a.db.QueryContext(ctx, q, synonymID)
	if err != nil {
		return nil, fmt.Errorf("core: synonym partners of %q: %w", synonymID, err)
	}
	defer rows.Close()

	var partners []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("core: scan synonym partner: %w", err)
		}
		partners = append(partners, t)
	}
	return partners, rows.Err()
}

// AddSynonym inserts a new synonym link.
//
//   - s.TaxonID and s.NameID are required; empty either → ErrValidation.
//   - s.TaxonID == s.ID is refused by the sfga CHECK constraint; hive
//     forbids explicit self-links via ErrValidation up-front.
//   - If s.ID is empty a UUID is generated so the row can participate in
//     pro-parte relationships later.
//   - Nullable FKs ("" → NULL): source_id, according_to_id, status_id.
//   - col__modified / col__modified_by stamped from the transaction context.
//
// Returns the synonym's ID (generated or preserved) so the caller can use it
// with AddSynonymTaxon for pro-parte.
func (t *Tx) AddSynonym(s coldp.Synonym) (string, error) {
	if s.TaxonID == "" || s.NameID == "" {
		return "", fmt.Errorf(
			"core: add synonym: %w: taxon_id and name_id required",
			ErrValidation,
		)
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.ID == s.TaxonID {
		return "", fmt.Errorf(
			"core: add synonym %s: %w: synonym id must differ from taxon id",
			s.ID, ErrValidation,
		)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	const insert = `INSERT INTO synonym (
		col__id, col__taxon_id, col__source_id,
		col__name_id, col__name_phrase,
		col__according_to_id, col__status_id,
		col__reference_id, col__link, col__remarks,
		col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := t.tx.ExecContext(t.ctx, insert,
		s.ID, s.TaxonID, nullIfEmpty(s.SourceID),
		s.NameID, s.NamePhrase,
		nullIfEmpty(s.AccordingToID), nullIfEmpty(s.Status.ID()),
		s.ReferenceID, s.Link, s.Remarks,
		now, t.actor,
	)
	if err != nil {
		return "", fmt.Errorf("core: insert synonym (taxon=%s, name=%s): %w",
			s.TaxonID, s.NameID, err)
	}
	return s.ID, nil
}

// AddSynonymTaxon extends an existing synonym into a pro-parte relationship
// by adding another accepted-taxon link with the same synonym ID.
//
//   - synonymID must already exist in the archive; unknown → ErrNotFound.
//   - additionalTaxonID must not be the synonym's own ID (schema CHECK).
//   - The new link inherits source_id, name_id, name_phrase, according_to_id,
//     status_id, reference_id, and link from the FIRST existing row with the
//     given synonym ID. Curators wanting a different name or status per taxon
//     should use AddSynonym with a fresh ID rather than extending pro-parte.
//   - col__modified / col__modified_by on the NEW row are stamped from context.
func (t *Tx) AddSynonymTaxon(synonymID, additionalTaxonID string) error {
	if synonymID == "" || additionalTaxonID == "" {
		return fmt.Errorf(
			"core: add synonym taxon: %w: synonym_id and taxon_id required",
			ErrValidation,
		)
	}
	if synonymID == additionalTaxonID {
		return fmt.Errorf(
			"core: add synonym taxon %s: %w: synonym id must differ from taxon id",
			synonymID, ErrValidation,
		)
	}

	// Copy from the first existing row for this synonym ID.
	const src = `SELECT
		COALESCE(col__source_id, ''),
		col__name_id, col__name_phrase,
		COALESCE(col__according_to_id, ''),
		COALESCE(col__status_id, ''),
		col__reference_id, col__link, col__remarks
	FROM synonym WHERE col__id = ? LIMIT 1`
	var (
		sourceID, nameID, phrase, accToID, status string
		refID, link, remarks                      string
	)
	err := t.tx.QueryRowContext(t.ctx, src, synonymID).Scan(
		&sourceID, &nameID, &phrase, &accToID, &status,
		&refID, &link, &remarks,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("core: synonym %s: %w", synonymID, ErrNotFound)
		}
		return fmt.Errorf("core: lookup synonym %s: %w", synonymID, err)
	}

	// Idempotence: if the additional link already exists, no-op.
	var existing int
	if err := t.tx.QueryRowContext(t.ctx,
		"SELECT COUNT(*) FROM synonym WHERE col__id = ? AND col__taxon_id = ?",
		synonymID, additionalTaxonID,
	).Scan(&existing); err != nil {
		return fmt.Errorf("core: check existing synonym link: %w", err)
	}
	if existing > 0 {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	const insert = `INSERT INTO synonym (
		col__id, col__taxon_id, col__source_id,
		col__name_id, col__name_phrase,
		col__according_to_id, col__status_id,
		col__reference_id, col__link, col__remarks,
		col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := t.tx.ExecContext(t.ctx, insert,
		synonymID, additionalTaxonID, nullIfEmpty(sourceID),
		nameID, phrase,
		nullIfEmpty(accToID), nullIfEmpty(status),
		refID, link, remarks,
		now, t.actor,
	); err != nil {
		return fmt.Errorf("core: insert pro-parte synonym link (syn=%s, taxon=%s): %w",
			synonymID, additionalTaxonID, err)
	}
	return nil
}

// UpdateSynonym updates a synonym link identified by (s.TaxonID, s.NameID).
//
// Optimistic concurrency: if s.Modified is non-empty it is treated as an
// If-Match token vs. col__modified; mismatch → ErrConflict.
//
// Editable fields: NamePhrase, AccordingToID, Status, ReferenceID, SourceID,
// Link, Remarks. The natural-key columns (TaxonID, NameID) and structural ID
// are NOT changed here — a curator switching the target taxon should remove
// this synonym and add a fresh one, keeping the change auditable.
func (t *Tx) UpdateSynonym(s coldp.Synonym) error {
	if s.TaxonID == "" || s.NameID == "" {
		return fmt.Errorf(
			"core: update synonym: %w: taxon_id and name_id required",
			ErrValidation,
		)
	}

	if s.Modified != "" {
		var current string
		err := t.tx.QueryRowContext(t.ctx,
			`SELECT COALESCE(col__modified, '') FROM synonym
			 WHERE col__taxon_id = ? AND col__name_id = ? LIMIT 1`,
			s.TaxonID, s.NameID,
		).Scan(&current)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf(
					"core: update synonym (taxon=%s, name=%s): %w",
					s.TaxonID, s.NameID, ErrNotFound,
				)
			}
			return fmt.Errorf("core: read synonym modified: %w", err)
		}
		if current != s.Modified {
			return fmt.Errorf(
				"core: update synonym (taxon=%s, name=%s): %w: If-Match mismatch",
				s.TaxonID, s.NameID, ErrConflict,
			)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	const update = `UPDATE synonym SET
		col__source_id = ?, col__name_phrase = ?,
		col__according_to_id = ?, col__status_id = ?,
		col__reference_id = ?, col__link = ?, col__remarks = ?,
		col__modified = ?, col__modified_by = ?
	WHERE col__taxon_id = ? AND col__name_id = ?`

	res, err := t.tx.ExecContext(t.ctx, update,
		nullIfEmpty(s.SourceID), s.NamePhrase,
		nullIfEmpty(s.AccordingToID), nullIfEmpty(s.Status.ID()),
		s.ReferenceID, s.Link, s.Remarks,
		now, t.actor,
		s.TaxonID, s.NameID,
	)
	if err != nil {
		return fmt.Errorf("core: update synonym (taxon=%s, name=%s): %w",
			s.TaxonID, s.NameID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update synonym rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf(
			"core: update synonym (taxon=%s, name=%s): %w",
			s.TaxonID, s.NameID, ErrNotFound,
		)
	}
	return nil
}

// RemoveSynonym deletes the synonym link between taxonID and nameID.
//
// For a pro-parte synonym, this removes only the specified accepted-taxon
// link; other links with the same synonym ID are preserved. To delete the
// entire pro-parte structure, iterate SynonymPartners and call RemoveSynonym
// for each pair, or use RemoveSynonymByID (added when needed).
func (t *Tx) RemoveSynonym(taxonID, nameID string) error {
	if taxonID == "" || nameID == "" {
		return fmt.Errorf(
			"core: remove synonym: %w: taxon_id and name_id required",
			ErrValidation,
		)
	}
	res, err := t.tx.ExecContext(t.ctx,
		"DELETE FROM synonym WHERE col__taxon_id = ? AND col__name_id = ?",
		taxonID, nameID,
	)
	if err != nil {
		return fmt.Errorf("core: remove synonym (taxon=%s, name=%s): %w",
			taxonID, nameID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: remove synonym rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf(
			"core: remove synonym (taxon=%s, name=%s): %w",
			taxonID, nameID, ErrNotFound,
		)
	}
	return nil
}
