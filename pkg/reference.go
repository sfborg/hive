package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// ReferenceLabel composes a compact "Author (Year) Title" display from
// a coldp.Reference — the same format the /api/reference/search hits
// use and the shape the TUI/WUI pickers show curators.
//
// Falls back to Citation, then ID, when the structured fields are
// empty so the picker never shows a blank line for a stored reference.
func ReferenceLabel(r *coldp.Reference) string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if r.Author != "" {
		parts = append(parts, r.Author)
	}
	if year := yearFromIssued(r.Issued); year != "" {
		parts = append(parts, "("+year+")")
	}
	if r.Title != "" {
		parts = append(parts, r.Title)
	}
	if len(parts) == 0 {
		if r.Citation != "" {
			return r.Citation
		}
		return r.ID
	}
	return strings.Join(parts, " ")
}

// yearFromIssued pulls the leading 4-digit year from a col__issued value
// (which may be "2020", "2020-03", or "2020-03-15").
func yearFromIssued(issued string) string {
	if len(issued) >= 4 {
		return issued[:4]
	}
	return ""
}

// ReferenceHit is a thin projection for list / search endpoints. Full
// coldp.Reference is fetched only when opening a detail view. Mirrors
// TaxonHit / NameHit so front-end list components can render either
// aggregate uniformly.
//
// Display: hive picks a short form (author + year + title-short) when
// the full citation is verbose. The `Citation` field falls back to a
// composed "Author (Year) Title" when col__citation is empty — many
// imports leave citation blank and rely on the structured fields.
type ReferenceHit struct {
	ID       string
	Author   string
	Year     string
	Title    string
	Citation string
	Type     string // reference_type ID (raw enum ID; front-end can pretty-print)
	// IssueCount is the number of open (not-yet-acknowledged)
	// validation issues currently filed against this reference row.
	// Zero → elided from the wire projection (apiReferenceHit uses
	// omitempty). Populated by scanReferenceHits via a batched
	// __gsvalidator_results lookup.
	IssueCount int
	// MaxSeverity is the highest severity among the record's open
	// issues ("error" > "warn" > "info" > "debug"). Empty when
	// IssueCount is 0. Drives the WUI badge color via the shared
	// validationSeverityBadge helper — same signal appears on every
	// picker/list rendering this record.
	MaxSeverity string
}

// ListReferencesPage returns references with SQL-level LIMIT/OFFSET
// pagination. limit == 0 disables the LIMIT clause. Ordered by author
// then year for a stable, curator-scannable list.
func (a *Archive) ListReferencesPage(ctx context.Context, limit, offset int) ([]ReferenceHit, int, error) {
	var total int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reference`,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("core: count references: %w", err)
	}

	q := `SELECT
		col__id,
		COALESCE(col__author, ''),
		COALESCE(substr(col__issued, 1, 4), ''),
		COALESCE(col__title, ''),
		COALESCE(col__citation, ''),
		COALESCE(col__type_id, '')
	FROM reference
	ORDER BY col__author, col__issued, col__title`

	args := []any{}
	if limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	return a.scanReferenceHits(ctx, q, args, total)
}

// SearchReferences returns references whose author, title, or citation
// matches q as a case-insensitive substring. Ordered by author + year
// for stable browsing.
func (a *Archive) SearchReferences(ctx context.Context, q string, limit int) ([]ReferenceHit, error) {
	if limit <= 0 {
		limit = 50
	}
	const query = `SELECT
		col__id,
		COALESCE(col__author, ''),
		COALESCE(substr(col__issued, 1, 4), ''),
		COALESCE(col__title, ''),
		COALESCE(col__citation, ''),
		COALESCE(col__type_id, '')
	FROM reference
	WHERE LOWER(col__author)   LIKE LOWER(?)
	   OR LOWER(col__title)    LIKE LOWER(?)
	   OR LOWER(col__citation) LIKE LOWER(?)
	   OR LOWER(col__doi)      LIKE LOWER(?)
	ORDER BY col__author, col__issued, col__title
	LIMIT ?`
	pattern := "%" + q + "%"
	hits, _, err := a.scanReferenceHits(
		ctx, query, []any{pattern, pattern, pattern, pattern, limit}, 0,
	)
	return hits, err
}

// scanReferenceHits runs the given query with args and returns the
// matching ReferenceHit slice. total is passed through unchanged (the
// paginated list path fetches it separately; search returns 0).
// Folds in open-issue counts per hit in a single batched pass —
// mirrors the vernacular / distribution / nomen-history patterns.
func (a *Archive) scanReferenceHits(ctx context.Context, query string, args []any, total int) ([]ReferenceHit, int, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("core: query references: %w", err)
	}
	defer rows.Close()

	var hits []ReferenceHit
	for rows.Next() {
		var h ReferenceHit
		if err := rows.Scan(&h.ID, &h.Author, &h.Year, &h.Title, &h.Citation, &h.Type); err != nil {
			return nil, 0, fmt.Errorf("core: scan reference hit: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("core: iterate references: %w", err)
	}

	if len(hits) > 0 {
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		summaries, err := a.batchIssueSummaries(ctx, "reference", ids)
		if err != nil {
			return nil, 0, err
		}
		for i := range hits {
			if s, ok := summaries[hits[i].ID]; ok {
				hits[i].IssueCount = s.Count
				hits[i].MaxSeverity = s.MaxSeverity
			}
		}
	}

	return hits, total, nil
}

// GetReference returns the full coldp.Reference for id.
func (a *Archive) GetReference(ctx context.Context, id string) (*coldp.Reference, error) {
	const q = `SELECT
		col__id, col__alternative_id, COALESCE(col__source_id, ''),
		col__citation, COALESCE(col__type_id, ''),
		col__author, col__author_id,
		col__editor, col__editor_id,
		col__title, col__title_short,
		col__container_author, col__container_title, col__container_title_short,
		col__issued, col__accessed,
		col__collection_title, col__collection_editor,
		col__volume, col__issue, col__edition, col__page,
		col__publisher, col__publisher_place,
		col__version,
		col__isbn, col__issn, col__doi, col__link,
		col__remarks, col__modified, col__modified_by
	FROM reference WHERE col__id = ? LIMIT 1`

	var (
		r          coldp.Reference
		typeStr    string
	)
	err := a.db.QueryRowContext(ctx, q, id).Scan(
		&r.ID, &r.AlternativeID, &r.SourceID,
		&r.Citation, &typeStr,
		&r.Author, &r.AuthorID,
		&r.Editor, &r.EditorID,
		&r.Title, &r.TitleShort,
		&r.ContainerAuthor, &r.ContainerTitle, &r.ContainerTitleShort,
		&r.Issued, &r.Accessed,
		&r.CollectionTitle, &r.CollectionEditor,
		&r.Volume, &r.Issue, &r.Edition, &r.Page,
		&r.Publisher, &r.PublisherPlace,
		&r.Version,
		&r.ISBN, &r.ISSN, &r.DOI, &r.Link,
		&r.Remarks, &r.Modified, &r.ModifiedBy,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: reference %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("core: get reference %s: %w", id, err)
	}
	r.Type = coldp.NewReferenceType(typeStr)
	return &r, nil
}

// referenceCitationTables lists every table whose FK constraint points
// at reference(col__id). DeleteReference walks this list to refuse
// deletion of a reference that any downstream row still depends on —
// same "safe by default" policy as DeleteTaxon's "has children" check.
// The comma-separated free-form lists on name.col__reference_id and
// name.col__alternative_id are NOT FK-enforced and thus not checked
// here; scrubbing those is a data-hygiene concern out of scope for
// the delete operation.
var referenceCitationTables = []struct {
	table, col string
}{
	// name.col__reference_id is the primary citation path (a name's
	// publication reference). Missing it here meant DeleteReference
	// could silently delete a reference still cited by a name — a
	// latent bug hidden by SQLite's FK check while FKs were on, now
	// exposed as FKs are off and hive owns integrity via validators.
	{"name", "col__reference_id"},
	{"taxon", "col__according_to_id"},
	{"synonym", "col__according_to_id"},
	{"vernacular", "col__reference_id"},
	{"name_relation", "col__reference_id"},
	{"type_material", "col__reference_id"},
	{"distribution", "col__reference_id"},
	{"species_estimate", "col__reference_id"},
	{"taxon_property", "col__reference_id"},
	{"species_interaction", "col__reference_id"},
	{"taxon_concept_relation", "col__reference_id"},
}

// CreateReference inserts a new reference row.
//
//   - If r.ID is empty a UUID v4 is generated and returned; imported
//     string IDs are preserved verbatim (same convention as CreateTaxon
//     / CreateName).
//   - col__source_id and col__type_id have FK constraints against
//     content / enum tables; empty values are translated to SQL NULL
//     via nullIfEmpty so PRAGMA foreign_keys stays happy.
//   - col__modified is stamped with the current time in RFC3339 UTC.
//     col__modified_by comes from the tx's actor context (WithActor).
func (t *Tx) CreateReference(r coldp.Reference) (string, error) {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	const insert = `INSERT INTO reference (
		col__id, col__alternative_id, col__source_id,
		col__citation, col__type_id,
		col__author, col__author_id,
		col__editor, col__editor_id,
		col__title, col__title_short,
		col__container_author, col__container_title, col__container_title_short,
		col__issued, col__accessed,
		col__collection_title, col__collection_editor,
		col__volume, col__issue, col__edition, col__page,
		col__publisher, col__publisher_place,
		col__version,
		col__isbn, col__issn, col__doi, col__link,
		col__remarks, col__modified, col__modified_by
	) VALUES (
		?, ?, ?, ?, ?,
		?, ?, ?, ?, ?, ?,
		?, ?, ?, ?, ?,
		?, ?, ?, ?, ?, ?,
		?, ?, ?, ?, ?, ?, ?,
		?, ?, ?
	)`
	_, err := t.tx.ExecContext(t.ctx, insert,
		r.ID, r.AlternativeID, nullIfEmpty(r.SourceID),
		r.Citation, nullIfEmpty(r.Type.ID()),
		r.Author, r.AuthorID,
		r.Editor, r.EditorID,
		r.Title, r.TitleShort,
		r.ContainerAuthor, r.ContainerTitle, r.ContainerTitleShort,
		r.Issued, r.Accessed,
		r.CollectionTitle, r.CollectionEditor,
		r.Volume, r.Issue, r.Edition, r.Page,
		r.Publisher, r.PublisherPlace,
		r.Version,
		r.ISBN, r.ISSN, r.DOI, r.Link,
		r.Remarks, now, t.actor,
	)
	if err != nil {
		return "", fmt.Errorf("core: insert reference %s: %w", r.ID, err)
	}
	t.markReferenceDirty(r.ID)
	return r.ID, nil
}

// UpdateReference writes r back to the existing row keyed by r.ID.
//
//   - r.ID required (ErrValidation on empty).
//   - Missing row → ErrNotFound.
//   - Optimistic concurrency: non-empty r.Modified is compared against
//     the current col__modified; mismatch → ErrConflict. Empty
//     Modified skips the check (blind write).
//   - col__modified / col__modified_by are re-stamped from the tx.
func (t *Tx) UpdateReference(r coldp.Reference) error {
	if r.ID == "" {
		return fmt.Errorf("core: update reference: %w: ID required", ErrValidation)
	}
	preSnapshot, _ := t.snapshotReference(r.ID)
	if r.Modified != "" {
		var current string
		err := t.tx.QueryRowContext(t.ctx,
			"SELECT COALESCE(col__modified, '') FROM reference WHERE col__id = ?",
			r.ID,
		).Scan(&current)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("core: update reference %s: %w", r.ID, ErrNotFound)
			}
			return fmt.Errorf("core: read current modified for %s: %w", r.ID, err)
		}
		if current != r.Modified {
			return fmt.Errorf(
				"core: update reference %s: %w: If-Match mismatch (have %q, want %q)",
				r.ID, ErrConflict, current, r.Modified,
			)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const update = `UPDATE reference SET
		col__alternative_id = ?, col__source_id = ?,
		col__citation = ?, col__type_id = ?,
		col__author = ?, col__author_id = ?,
		col__editor = ?, col__editor_id = ?,
		col__title = ?, col__title_short = ?,
		col__container_author = ?, col__container_title = ?, col__container_title_short = ?,
		col__issued = ?, col__accessed = ?,
		col__collection_title = ?, col__collection_editor = ?,
		col__volume = ?, col__issue = ?, col__edition = ?, col__page = ?,
		col__publisher = ?, col__publisher_place = ?,
		col__version = ?,
		col__isbn = ?, col__issn = ?, col__doi = ?, col__link = ?,
		col__remarks = ?, col__modified = ?, col__modified_by = ?
	WHERE col__id = ?`
	res, err := t.tx.ExecContext(t.ctx, update,
		r.AlternativeID, nullIfEmpty(r.SourceID),
		r.Citation, nullIfEmpty(r.Type.ID()),
		r.Author, r.AuthorID,
		r.Editor, r.EditorID,
		r.Title, r.TitleShort,
		r.ContainerAuthor, r.ContainerTitle, r.ContainerTitleShort,
		r.Issued, r.Accessed,
		r.CollectionTitle, r.CollectionEditor,
		r.Volume, r.Issue, r.Edition, r.Page,
		r.Publisher, r.PublisherPlace,
		r.Version,
		r.ISBN, r.ISSN, r.DOI, r.Link,
		r.Remarks, now, t.actor,
		r.ID,
	)
	if err != nil {
		return fmt.Errorf("core: update reference %s: %w", r.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update reference %s rows affected: %w", r.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update reference %s: %w", r.ID, ErrNotFound)
	}
	t.markReferenceDirtyWithPre(r.ID, preSnapshot)
	return nil
}

// DeleteReference removes a reference. Refuses if any downstream row
// still points at it via one of the FK columns in
// referenceCitationTables — safer than silently NULLing those refs.
// Curator must reassign the dependents (or delete them) first.
//
//   - Missing reference → ErrNotFound.
//   - Any dependent row → ErrConflict with the offending table name.
func (t *Tx) DeleteReference(id string) error {
	if id == "" {
		return fmt.Errorf("core: delete reference: %w: id required", ErrValidation)
	}
	preSnapshot, _ := t.snapshotReference(id)
	// Refuse if any dependent row exists. First-match reporting keeps
	// the error message actionable ("dependency in vernacular") without
	// enumerating every table.
	for _, dep := range referenceCitationTables {
		var count int
		q := "SELECT COUNT(*) FROM " + dep.table + " WHERE " + dep.col + " = ?"
		if err := t.tx.QueryRowContext(t.ctx, q, id).Scan(&count); err != nil {
			return fmt.Errorf("core: check %s.%s for %s: %w", dep.table, dep.col, id, err)
		}
		if count > 0 {
			return fmt.Errorf(
				"core: delete reference %s: %w: %d %s row(s) still cite it (reassign or delete first)",
				id, ErrConflict, count, dep.table,
			)
		}
	}
	res, err := t.tx.ExecContext(t.ctx, "DELETE FROM reference WHERE col__id = ?", id)
	if err != nil {
		return fmt.Errorf("core: delete reference %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete reference %s rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete reference %s: %w", id, ErrNotFound)
	}
	t.markReferenceDeleted(id, preSnapshot)
	return nil
}
