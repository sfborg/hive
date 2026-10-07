package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sfborg/hive/pkg/taxonomy"
	"github.com/sfborg/sflib/pkg/coldp"
)

// ComposeCitation builds a full journal-style citation string from a
// reference's structured fields. Used to auto-populate col__citation
// when it's empty — the citation string is what most search paths
// hit, so an empty one makes the reference invisible to text search.
//
// Format matches the reader's plain-text citation output:
//
//	"Author (Year). Title. Journal Volume(Issue): Page. doi:DOI"
//
// Segments elide when their field is empty; joining punctuation is
// conditional so an omitted field doesn't leave a stranded period.
// Returns empty when there's not enough content to build something
// meaningful (no author AND no title AND no citation-worthy fields).
func ComposeCitation(r *coldp.Reference) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	wrote := false
	writeSep := func(sep string) {
		if wrote {
			b.WriteString(sep)
		}
	}
	if r.Author != "" {
		b.WriteString(r.Author)
		wrote = true
	}
	if year := yearFromIssued(r.Issued); year != "" {
		writeSep(" ")
		b.WriteString("(")
		b.WriteString(year)
		b.WriteString(").")
		wrote = true
	} else if r.Author != "" {
		b.WriteString(".")
	}
	if r.Title != "" {
		writeSep(" ")
		b.WriteString(r.Title)
		if !strings.HasSuffix(r.Title, ".") {
			b.WriteString(".")
		}
		wrote = true
	}
	if r.ContainerTitle != "" {
		writeSep(" ")
		b.WriteString(r.ContainerTitle)
		wrote = true
	}
	if r.Volume != "" {
		writeSep(" ")
		b.WriteString(r.Volume)
		if r.Issue != "" {
			b.WriteString("(")
			b.WriteString(r.Issue)
			b.WriteString(")")
		}
		wrote = true
	}
	if r.Page != "" {
		if r.Volume != "" || r.ContainerTitle != "" {
			b.WriteString(": ")
		} else {
			writeSep(" ")
		}
		b.WriteString(r.Page)
		wrote = true
	}
	if r.Publisher != "" {
		writeSep(". ")
		b.WriteString(r.Publisher)
		if r.PublisherPlace != "" {
			b.WriteString(", ")
			b.WriteString(r.PublisherPlace)
		}
		wrote = true
	}
	if wrote && !strings.HasSuffix(b.String(), ".") {
		b.WriteString(".")
	}
	if r.DOI != "" {
		if wrote {
			b.WriteString(" ")
		}
		b.WriteString("https://doi.org/")
		b.WriteString(r.DOI)
	}
	return strings.TrimSpace(b.String())
}

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
	// Workflow is the per-step progress of the reference through the
	// four-step reference workflow.
	Workflow ReferenceWorkflow
	// License is the verified license from hive__reference_license,
	// or "" when none has been recorded.
	License string
	// MentionsIndexed is true when the name-mention index has rows for
	// the reference.
	MentionsIndexed bool
	// ColFamily is the family with the most name mentions according
	// to the index's verifier source, with that mention total. Empty
	// when no mentioned name verified to a family.
	ColFamily         string
	ColFamilyMentions int
	// ColMatch is set for queries with a ColParent: the reference's
	// strongest matching mention.
	ColMatch *ColMatch
}

// WorkflowState is how far a reference has got through one workflow
// step.
type WorkflowState string

const (
	WorkflowNone    WorkflowState = "none"    // not started
	WorkflowPartial WorkflowState = "partial" // started, needs work or review
	WorkflowDone    WorkflowState = "done"    // complete
)

// ReferenceWorkflow is a reference's state for each of the four
// workflow steps, using the same step definitions as
// [Archive.ReferenceDashboard]:
//
//   - Bibliographic: done when author, issued date and title are all
//     present; partial when some are.
//   - Digitized: done when a source document is attached.
//   - Assertions: done when assertions exist and none are pending
//     review; partial while any are pending. Assertions from
//     soft-deleted extraction runs are not counted.
//   - Imported: done when any assertion has been imported.
type ReferenceWorkflow struct {
	Bibliographic WorkflowState
	Digitized     WorkflowState
	Assertions    WorkflowState
	Imported      WorkflowState
}

// referenceStatus is the per-reference data the reference list
// derives from tables beyond the reference row.
type referenceStatus struct {
	Workflow ReferenceWorkflow
	License  string
}

// ReferenceWorkflowFor returns the workflow state of one reference.
// A reference that does not exist yields the all-"none" state.
func (a *Archive) ReferenceWorkflowFor(ctx context.Context, id string) (ReferenceWorkflow, error) {
	st, err := a.batchReferenceStatus(ctx, []string{id})
	if err != nil {
		return ReferenceWorkflow{}, err
	}
	if s, ok := st[id]; ok {
		return s.Workflow, nil
	}
	none := ReferenceWorkflow{WorkflowNone, WorkflowNone, WorkflowNone, WorkflowNone}
	return none, nil
}

// batchReferenceStatus computes workflow state and license for the
// given reference IDs in one query.
func (a *Archive) batchReferenceStatus(ctx context.Context, ids []string) (map[string]referenceStatus, error) {
	out := make(map[string]referenceStatus, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT r.col__id,
		(COALESCE(r.col__author, '') != '')
		  + (COALESCE(r.col__issued, '') != '')
		  + (COALESCE(r.col__title, '') != ''),
		EXISTS (SELECT 1 FROM hive__reference_pdf p
		        WHERE p.reference_id = r.col__id),
		(SELECT COUNT(*) FROM hive__reference_assertion ra
		   LEFT JOIN hive__extraction_run er ON er.run_id = ra.source_run_id
		  WHERE ra.reference_id = r.col__id AND er.deleted_at IS NULL),
		(SELECT COUNT(*) FROM hive__reference_assertion ra
		   LEFT JOIN hive__extraction_run er ON er.run_id = ra.source_run_id
		  WHERE ra.reference_id = r.col__id AND er.deleted_at IS NULL
		    AND ra.status = 'pending'),
		EXISTS (SELECT 1 FROM hive__reference_assertion ra
		        WHERE ra.reference_id = r.col__id
		          AND ra.imported_at IS NOT NULL),
		COALESCE((SELECT l.license FROM hive__reference_license l
		          WHERE l.reference_id = r.col__id), '')
	FROM reference r
	WHERE r.col__id IN (` + strings.Repeat("?,", len(ids)-1) + `?)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: reference status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                          string
			bibFields, assertions, pend int
			digitized, imported         bool
			license                     string
		)
		if err := rows.Scan(&id, &bibFields, &digitized, &assertions, &pend, &imported, &license); err != nil {
			return nil, fmt.Errorf("core: scan reference status: %w", err)
		}
		w := ReferenceWorkflow{
			Bibliographic: WorkflowNone,
			Digitized:     WorkflowNone,
			Assertions:    WorkflowNone,
			Imported:      WorkflowNone,
		}
		switch {
		case bibFields == 3:
			w.Bibliographic = WorkflowDone
		case bibFields > 0:
			w.Bibliographic = WorkflowPartial
		}
		if digitized {
			w.Digitized = WorkflowDone
		}
		switch {
		case pend > 0:
			w.Assertions = WorkflowPartial
		case assertions > 0:
			w.Assertions = WorkflowDone
		}
		if imported {
			w.Imported = WorkflowDone
		}
		out[id] = referenceStatus{Workflow: w, License: license}
	}
	return out, rows.Err()
}

// ReferenceSort selects the ordering ListReferencesPage uses.
type ReferenceSort int

const (
	// ReferenceSortAuthor orders alphabetically by author + year +
	// title. Default; matches the "browse Andersons" mental model
	// for scanning a small-medium archive.
	ReferenceSortAuthor ReferenceSort = iota
	// ReferenceSortRecent orders by col__modified descending, so
	// newly-imported and recently-edited references land at the
	// top. Preferred landing view for large archives where
	// alphabetical scan is impractical.
	ReferenceSortRecent
	// ReferenceSortYear orders by publication date, newest first,
	// then author and title.
	ReferenceSortYear
)

// ReferenceFilter narrows ListReferencesPage and SearchReferences to
// references at a given stage of the extraction workflow.
type ReferenceFilter string

const (
	// ReferenceFilterNone applies no workflow filter.
	ReferenceFilterNone ReferenceFilter = ""
	// ReferenceFilterRuns keeps references with at least one
	// non-deleted extraction run, including runs that produced no
	// assertions.
	ReferenceFilterRuns ReferenceFilter = "runs"
	// ReferenceFilterAssertions keeps references with at least one
	// assertion whose source run (if any) is not soft-deleted.
	ReferenceFilterAssertions ReferenceFilter = "assertions"
	// ReferenceFilterPending keeps references with at least one
	// visible assertion still in 'pending' status.
	ReferenceFilterPending ReferenceFilter = "pending"
)

// ParseReferenceFilter maps a wire value to a ReferenceFilter. The
// boolean is false for unrecognized values.
func ParseReferenceFilter(s string) (ReferenceFilter, bool) {
	switch f := ReferenceFilter(s); f {
	case ReferenceFilterNone, ReferenceFilterRuns,
		ReferenceFilterAssertions, ReferenceFilterPending:
		return f, true
	}
	return ReferenceFilterNone, false
}

// referenceFilterSQL returns a WHERE predicate over the reference
// table for f, or "" when f applies no filter. Assertions whose
// source run is soft-deleted are excluded, matching the join-filter
// semantics documented on hive__reference_assertion.source_run_id.
func referenceFilterSQL(f ReferenceFilter) string {
	const visibleAssertion = `EXISTS (
		SELECT 1 FROM hive__reference_assertion ra
		LEFT JOIN hive__extraction_run er ON er.run_id = ra.source_run_id
		WHERE ra.reference_id = reference.col__id
		  AND er.deleted_at IS NULL`
	switch f {
	case ReferenceFilterRuns:
		return `EXISTS (
		SELECT 1 FROM hive__extraction_run er
		WHERE er.reference_id = reference.col__id
		  AND er.deleted_at IS NULL)`
	case ReferenceFilterAssertions:
		return visibleAssertion + `)`
	case ReferenceFilterPending:
		return visibleAssertion + ` AND ra.status = 'pending')`
	}
	return ""
}

// ReferenceQuery narrows a reference listing or search.
type ReferenceQuery struct {
	// Filter restricts results to a workflow stage.
	Filter ReferenceFilter
	// ColParent keeps references that mention a name the
	// name-mention index's verifier source (the Catalogue of Life)
	// places in this higher taxon, matched case-insensitively against
	// the record's own name and every rank of its classification.
	ColParent string
	// MinMentions counts only names mentioned at least this many times
	// toward ColParent. Values below 1 mean 1.
	MinMentions int
}

func (rq ReferenceQuery) minMentions() int {
	return max(rq.MinMentions, 1)
}

// colParentCond is the condition, over hive__name_mention m joined to
// raw__taxon t, that the mention's record lies in rq.ColParent.
func (rq ReferenceQuery) colParentCond() (string, []any) {
	cols := make([]string, len(classificationColumns))
	for i, c := range classificationColumns {
		cols[i] = "LOWER(t." + c + ")"
	}
	return `m.mention_count >= ? AND LOWER(?) IN (` + strings.Join(cols, ", ") + `)`,
		[]any{rq.minMentions(), strings.TrimSpace(rq.ColParent)}
}

// predicate returns the WHERE condition over the reference table, or
// "" when rq applies no restriction.
func (rq ReferenceQuery) predicate() (string, []any) {
	var conds []string
	var args []any
	if p := referenceFilterSQL(rq.Filter); p != "" {
		conds = append(conds, p)
	}
	if strings.TrimSpace(rq.ColParent) != "" {
		cond, a := rq.colParentCond()
		conds = append(conds, `EXISTS (
		SELECT 1 FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id = reference.col__id AND `+cond+`)`)
		args = append(args, a...)
	}
	return strings.Join(conds, " AND "), args
}

// ColMatch explains why a reference matched a ColParent query: its
// most-mentioned name that the verifier places in the taxon.
type ColMatch struct {
	Mention string
	Count   int
}

// addMentionSummaries fills each hit's name-mention summary: whether
// the reference has been indexed, its most-mentioned family according
// to the verifier source, and, when rq has a ColParent, the strongest
// matching mention.
func (a *Archive) addMentionSummaries(ctx context.Context, hits []ReferenceHit, ids []string, rq ReferenceQuery) error {
	in := strings.Repeat("?,", len(ids)-1) + "?"
	idArgs := make([]any, len(ids))
	for i, id := range ids {
		idArgs[i] = id
	}
	byID := make(map[string]*ReferenceHit, len(hits))
	for i := range hits {
		byID[hits[i].ID] = &hits[i]
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT m.reference_id, COALESCE(t.col__family, ''), SUM(m.mention_count)
		FROM hive__name_mention m
		LEFT JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id IN (`+in+`)
		GROUP BY m.reference_id, COALESCE(t.col__family, '')`, idArgs...)
	if err != nil {
		return fmt.Errorf("core: mention families: %w", err)
	}
	for rows.Next() {
		var id, family string
		var n int
		if err := rows.Scan(&id, &family, &n); err != nil {
			rows.Close()
			return err
		}
		h := byID[id]
		if h == nil {
			continue
		}
		h.MentionsIndexed = true
		if family != "" && (n > h.ColFamilyMentions ||
			(n == h.ColFamilyMentions && family < h.ColFamily)) {
			h.ColFamily, h.ColFamilyMentions = family, n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if strings.TrimSpace(rq.ColParent) == "" {
		return nil
	}
	cond, condArgs := rq.colParentCond()
	rows, err = a.db.QueryContext(ctx, `
		SELECT m.reference_id, m.mention, m.mention_count
		FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id IN (`+in+`) AND `+cond+`
		ORDER BY m.mention_count DESC, m.mention`, append(idArgs, condArgs...)...)
	if err != nil {
		return fmt.Errorf("core: mention matches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, mention string
		var n int
		if err := rows.Scan(&id, &mention, &n); err != nil {
			return err
		}
		if h := byID[id]; h != nil && h.ColMatch == nil {
			h.ColMatch = &ColMatch{Mention: mention, Count: n}
		}
	}
	return rows.Err()
}

// ListReferencesPage returns references with SQL-level LIMIT/OFFSET
// pagination. limit == 0 disables the LIMIT clause. Sort order
// selected by the sort parameter; filter restricts the rows (and the
// returned total) to a workflow stage.
func (a *Archive) ListReferencesPage(ctx context.Context, limit, offset int, sort ReferenceSort, rq ReferenceQuery) ([]ReferenceHit, int, error) {
	where := ""
	pred, predArgs := rq.predicate()
	if pred != "" {
		where = " WHERE " + pred
	}

	var total int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reference`+where, predArgs...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("core: count references: %w", err)
	}

	orderBy := "col__author, col__issued, col__title"
	switch sort {
	case ReferenceSortRecent:
		// col__modified is set on every write; DESC puts newest
		// first. Tie-break by id for a deterministic sort so
		// paginated infinite-scroll doesn't re-shuffle rows across
		// LIMIT/OFFSET windows.
		orderBy = "col__modified DESC, col__id"
	case ReferenceSortYear:
		// Undated references sort last.
		orderBy = "COALESCE(col__issued, '') = '', col__issued DESC, col__author, col__title, col__id"
	}

	q := `SELECT
		col__id,
		COALESCE(col__author, ''),
		COALESCE(substr(col__issued, 1, 4), ''),
		COALESCE(col__title, ''),
		COALESCE(col__citation, ''),
		COALESCE(col__type_id, '')
	FROM reference` + where + `
	ORDER BY ` + orderBy

	args := append([]any{}, predArgs...)
	if limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	return a.scanReferenceHits(ctx, q, args, total, rq)
}

// minIDPrefixLen is the shortest query SearchReferences treats as a
// possible col__id prefix. Shorter queries would match unrelated
// UUIDs that happen to start with common hex-letter words.
const minIDPrefixLen = 8

// SearchReferences returns references whose author, title, citation,
// or DOI matches q as a case-insensitive substring, or whose col__id
// starts with q (when q is at least minIDPrefixLen characters).
// rq restricts results further. Ordered by author + year for stable
// browsing.
func (a *Archive) SearchReferences(ctx context.Context, q string, limit int, rq ReferenceQuery) ([]ReferenceHit, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + q + "%"
	match := `(LOWER(col__author)   LIKE LOWER(?)
		   OR LOWER(col__title)    LIKE LOWER(?)
		   OR LOWER(col__citation) LIKE LOWER(?)
		   OR LOWER(col__doi)      LIKE LOWER(?)`
	args := []any{pattern, pattern, pattern, pattern}
	if len(q) >= minIDPrefixLen {
		match += `
		   OR LOWER(col__id) LIKE LOWER(?)`
		args = append(args, q+"%")
	}
	match += `)`
	if pred, predArgs := rq.predicate(); pred != "" {
		match += " AND " + pred
		args = append(args, predArgs...)
	}
	query := `SELECT
		col__id,
		COALESCE(col__author, ''),
		COALESCE(substr(col__issued, 1, 4), ''),
		COALESCE(col__title, ''),
		COALESCE(col__citation, ''),
		COALESCE(col__type_id, '')
	FROM reference
	WHERE ` + match + `
	ORDER BY col__author, col__issued, col__title
	LIMIT ?`
	args = append(args, limit)
	hits, _, err := a.scanReferenceHits(ctx, query, args, 0, rq)
	return hits, err
}

// scanReferenceHits runs the given query with args and returns the
// matching ReferenceHit slice. total is passed through unchanged (the
// paginated list path fetches it separately; search returns 0).
// Folds in open-issue counts per hit in a single batched pass —
// mirrors the vernacular / distribution / nomen-history patterns.
func (a *Archive) scanReferenceHits(ctx context.Context, query string, args []any, total int, rq ReferenceQuery) ([]ReferenceHit, int, error) {
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
		status, err := a.batchReferenceStatus(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		for i := range hits {
			if st, ok := status[hits[i].ID]; ok {
				hits[i].Workflow = st.Workflow
				hits[i].License = st.License
			}
		}
		if err := a.addMentionSummaries(ctx, hits, ids, rq); err != nil {
			return nil, 0, err
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

// The reference-citation FK-site list moved to pkg/taxonomy — see
// taxonomy.ReferenceFKSites for the current entries and the rationale
// for keeping name first. DeleteReference and the uncited-reference
// validator both consume that function so the two stay in lockstep.

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
	// col__citation is a derived / cached field. Auto-compose it
	// when the caller didn't supply one — the citation string is
	// what search paths hit against and it would otherwise stay
	// blank for the many callers (OpenAlex resolve, GROBID pre-
	// fill, manual entry) that populate only the structured fields.
	// Imported citations that came with the source are preserved
	// (r.Citation != "" case).
	if r.Citation == "" {
		r.Citation = ComposeCitation(&r)
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
	// Auto-compose the citation when empty, mirroring CreateReference.
	// Existing non-empty citations are preserved so imported records
	// (and any curator-edited citations from other tools) survive.
	if r.Citation == "" {
		r.Citation = ComposeCitation(&r)
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
// still points at it via one of the FK sites returned by
// taxonomy.ReferenceFKSites — safer than silently NULLing those refs.
// Curator must reassign the dependents (or delete them) first.
//
//   - Missing reference → ErrNotFound.
//   - Any dependent row → ErrConflict with the offending table name.
// FindReferencesByDOI returns every reference row whose
// col__doi matches (case-insensitive) the given DOI. The DOI is
// normalized before comparison: leading "doi:" is stripped and
// a leading "https://doi.org/" or "http://doi.org/" prefix is
// dropped. Returns an empty slice when nothing matches.
//
// Used by the upload path to dedup multi-format uploads: dropping
// the JATS for a paper whose PDF is already ingested lands the
// JATS as a sidecar attachment on the existing reference row
// instead of minting a duplicate reference.
func (a *Archive) FindReferencesByDOI(ctx context.Context, doi string) ([]coldp.Reference, error) {
	if doi == "" {
		return nil, nil
	}
	needle := strings.TrimSpace(doi)
	needle = strings.TrimPrefix(needle, "https://doi.org/")
	needle = strings.TrimPrefix(needle, "http://doi.org/")
	needle = strings.TrimPrefix(needle, "doi:")
	if needle == "" {
		return nil, nil
	}
	// Multi-row match is rare (would indicate an existing
	// duplicate in the archive); we return every match so the
	// caller can surface the ambiguity rather than pick
	// arbitrarily.
	const q = `SELECT col__id, COALESCE(col__citation, ''), COALESCE(col__title, ''), COALESCE(col__doi, '')
	    FROM reference
	    WHERE LOWER(REPLACE(REPLACE(REPLACE(
	        COALESCE(col__doi, ''),
	        'https://doi.org/', ''),
	        'http://doi.org/', ''),
	        'doi:', '')) = LOWER(?)
	    ORDER BY col__id ASC`
	rows, err := a.db.QueryContext(ctx, q, needle)
	if err != nil {
		return nil, fmt.Errorf("core: FindReferencesByDOI(%s): %w", needle, err)
	}
	defer rows.Close()
	var out []coldp.Reference
	for rows.Next() {
		var r coldp.Reference
		if err := rows.Scan(&r.ID, &r.Citation, &r.Title, &r.DOI); err != nil {
			return nil, fmt.Errorf("core: FindReferencesByDOI scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AttachReferencePDF associates a sidecar-library sha512 with a
// reference row. Idempotent — repeat attachments with the same
// (reference_id, sha512) are no-ops. Stamps attached_at + the
// actor from the context so the audit trail records who linked
// the file.
func (t *Tx) AttachReferencePDF(referenceID, sha512 string) error {
	if referenceID == "" || sha512 == "" {
		return fmt.Errorf("core: AttachReferencePDF: both reference_id and sha512 required")
	}
	actor := ActorFromContext(t.ctx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `INSERT INTO hive__reference_pdf
		(reference_id, sha512, attached_at, attached_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(reference_id, sha512) DO NOTHING`
	if _, err := t.tx.ExecContext(t.ctx, q, referenceID, sha512, now, actor); err != nil {
		return fmt.Errorf("core: attach pdf to reference %s: %w", referenceID, err)
	}
	return nil
}

// DetachReferencePDF removes the (reference_id, sha512) row from
// hive__reference_pdf. Idempotent — detaching an already-absent
// association returns nil. Also deletes every extraction correction
// attached to the same sha for the given reference so the DB and
// the sidecar-derived JATS don't drift out of sync when a curator
// deletes their PDF and re-attaches later.
//
// Does not touch sidecar files — the caller is responsible for
// filesystem cleanup once it verifies no other reference still
// points at the sha (see IsOrphanSHA).
func (t *Tx) DetachReferencePDF(referenceID, sha512 string) error {
	if referenceID == "" || sha512 == "" {
		return fmt.Errorf("core: DetachReferencePDF: both reference_id and sha512 required")
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__reference_pdf
		 WHERE reference_id = ? AND sha512 = ?`,
		referenceID, sha512,
	); err != nil {
		return fmt.Errorf("core: detach pdf %s from %s: %w", sha512, referenceID, err)
	}
	// Also drop this reference's correction rows for that sha —
	// keeping them would silently re-apply the corrections onto
	// any future re-attach, which is not what "delete this PDF"
	// implies.
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__extraction_correction
		 WHERE reference_id = ? AND sidecar_sha = ?`,
		referenceID, sha512,
	); err != nil {
		return fmt.Errorf("core: cascade delete corrections: %w", err)
	}
	return nil
}

// IsOrphanSHA returns true when no reference row still has a
// hive__reference_pdf row for the given sha. Callers use this
// after a detach to decide whether to delete sidecar files (safe
// only when no other reference still uses the content).
func (a *Archive) IsOrphanSHA(ctx context.Context, sha512 string) (bool, error) {
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM hive__reference_pdf WHERE sha512 = ?`,
		sha512,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("core: count sha references: %w", err)
	}
	return n == 0, nil
}

// DeleteCorrectionsForSHA drops every correction row targeting
// the given sha across all references. Called after detach when
// IsOrphanSHA returns true so orphaned correction data doesn't
// linger and re-apply if the same PDF is later re-uploaded.
func (t *Tx) DeleteCorrectionsForSHA(sha512 string) error {
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__extraction_correction WHERE sidecar_sha = ?`,
		sha512,
	); err != nil {
		return fmt.Errorf("core: delete corrections for sha %s: %w", sha512, err)
	}
	return nil
}

// ReferencePDFs returns every sha512 attached to the given
// reference, in attach order. Empty slice when nothing is attached.
func (a *Archive) ReferencePDFs(ctx context.Context, referenceID string) ([]string, error) {
	const q = `SELECT sha512 FROM hive__reference_pdf
		WHERE reference_id = ? ORDER BY attached_at`
	rows, err := a.db.QueryContext(ctx, q, referenceID)
	if err != nil {
		return nil, fmt.Errorf("core: list attached PDFs for %s: %w", referenceID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out = append(out, sha)
	}
	return out, rows.Err()
}

// RegenerateCitations rewrites col__citation on reference rows
// using ComposeCitation over each row's current structured fields.
// Two modes:
//
//   - overwrite=false: only rows whose col__citation is currently
//     empty get filled. Preserves imported citations that came with
//     their own format.
//   - overwrite=true: every row's citation is replaced with the
//     freshly-composed one. Use to normalize an archive imported
//     from a source with poorly-formatted citations.
//
// Skips rows where ComposeCitation returns empty (no structured
// fields to build from). Returns the number of rows actually
// updated. Bumps col__modified and col__modified_by on each
// updated row, so the change is visible in the audit trail.
func (t *Tx) RegenerateCitations(overwrite bool) (int, error) {
	actor := ActorFromContext(t.ctx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Read every reference's id + current citation + composable
	// structured fields. Cheap for the archive sizes hive targets
	// (up to millions of rows still fits comfortably in memory as
	// pointers; drop to a cursor loop later if needed).
	const sel = `SELECT
		col__id, COALESCE(col__citation, ''),
		COALESCE(col__author, ''), COALESCE(col__issued, ''),
		COALESCE(col__title, ''), COALESCE(col__container_title, ''),
		COALESCE(col__volume, ''), COALESCE(col__issue, ''),
		COALESCE(col__page, ''), COALESCE(col__publisher, ''),
		COALESCE(col__publisher_place, ''), COALESCE(col__doi, '')
	FROM reference`
	rows, err := t.tx.QueryContext(t.ctx, sel)
	if err != nil {
		return 0, fmt.Errorf("core: read references for regenerate: %w", err)
	}
	type refRow struct {
		id, current string
		r           coldp.Reference
	}
	var toUpdate []refRow
	for rows.Next() {
		var rr refRow
		if err := rows.Scan(
			&rr.id, &rr.current,
			&rr.r.Author, &rr.r.Issued,
			&rr.r.Title, &rr.r.ContainerTitle,
			&rr.r.Volume, &rr.r.Issue,
			&rr.r.Page, &rr.r.Publisher,
			&rr.r.PublisherPlace, &rr.r.DOI,
		); err != nil {
			rows.Close()
			return 0, fmt.Errorf("core: scan reference for regenerate: %w", err)
		}
		if !overwrite && rr.current != "" {
			continue
		}
		fresh := ComposeCitation(&rr.r)
		if fresh == "" {
			continue
		}
		if fresh == rr.current {
			continue
		}
		toUpdate = append(toUpdate, refRow{id: rr.id, current: fresh})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("core: iterate references for regenerate: %w", err)
	}
	const upd = `UPDATE reference
		SET col__citation = ?, col__modified = ?, col__modified_by = ?
		WHERE col__id = ?`
	updated := 0
	for _, rr := range toUpdate {
		if _, err := t.tx.ExecContext(t.ctx, upd, rr.current, now, actor, rr.id); err != nil {
			return updated, fmt.Errorf("core: regenerate citation for %s: %w", rr.id, err)
		}
		updated++
	}
	return updated, nil
}

func (t *Tx) DeleteReference(id string) error {
	if id == "" {
		return fmt.Errorf("core: delete reference: %w: id required", ErrValidation)
	}
	preSnapshot, _ := t.snapshotReference(id)
	// Refuse if any dependent row exists. First-match reporting keeps
	// the error message actionable ("dependency in vernacular") without
	// enumerating every table.
	for _, dep := range taxonomy.ReferenceFKSites() {
		var count int
		q := "SELECT COUNT(*) FROM " + dep.Table + " WHERE " + dep.Column + " = ?"
		if err := t.tx.QueryRowContext(t.ctx, q, id).Scan(&count); err != nil {
			return fmt.Errorf("core: check %s.%s for %s: %w", dep.Table, dep.Column, id, err)
		}
		if count > 0 {
			return fmt.Errorf(
				"core: delete reference %s: %w: %d %s row(s) still cite it (reassign or delete first)",
				id, ErrConflict, count, dep.Table,
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
