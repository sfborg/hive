package hive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Issue is a stored validation result read back from
// __gsvalidator_results. Frontends consume this directly on both the
// per-record detail banner (via NameWarnings) and the Issues screen
// (via ListIssues). Includes the record's owning-taxon id when the
// rule fired on a name row and that name is used by exactly one
// taxon — the common case, and enough for the Issues screen to route
// row clicks into the tree.
type Issue struct {
	ID             string
	TableName      string // "name" (taxon/reference/... later)
	RecordID       string // col__id of the flagged row
	RuleID         string
	RuleName       string
	FieldName      string
	Severity       string
	Enforcement    string
	Message        string
	ActualValue    string
	ExpectedValue  string
	CreatedAt      string
	AcknowledgedBy string
	AcknowledgedAt string

	// RecordLabel is a human-readable label for the flagged record —
	// scientific name for names, taxon path for taxa, citation for
	// references. Server-resolved so the Issues list renders in one
	// round-trip instead of N follow-ups.
	RecordLabel string

	// LinkTaxonID is the taxon id a click should navigate to, when
	// resolvable. For a name-scoped issue, this is the taxon using
	// that name (nil when the name has no taxon or has more than one).
	LinkTaxonID string
}

// IssueSummaryRow is one bar in the issue-count breakdown: how many
// issues of a given rule + severity exist on a given table. Frontends
// group or filter this list however suits their layout.
type IssueSummaryRow struct {
	TableName string
	RuleID    string
	RuleName  string
	Severity  string
	Count     int
}

// IssueFilter narrows a ListIssues call. Empty fields mean "no filter
// on that axis." Severities is OR-matched (any of); TableName and
// RuleID are exact matches. HideAcknowledged drops rows with a
// non-empty acknowledged_at, for the default "still-open" view.
type IssueFilter struct {
	TableName        string
	RuleID           string
	Severities       []string
	HideAcknowledged bool
}

// IssueSummary returns every (table, rule, severity, count) triple
// present in the archive. Ordered by count desc so the top of the
// list is the biggest bar in any chart.
func (a *Archive) IssueSummary(ctx context.Context) ([]IssueSummaryRow, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT table_name, rule_id, COALESCE(rule_name, ''), severity, COUNT(*)
		 FROM __gsvalidator_results
		 GROUP BY table_name, rule_id, rule_name, severity
		 ORDER BY COUNT(*) DESC, table_name, rule_id, severity`,
	)
	if err != nil {
		return nil, fmt.Errorf("core: issue summary: %w", err)
	}
	defer rows.Close()
	var out []IssueSummaryRow
	for rows.Next() {
		var r IssueSummaryRow
		if err := rows.Scan(&r.TableName, &r.RuleID, &r.RuleName, &r.Severity, &r.Count); err != nil {
			return nil, fmt.Errorf("core: scan issue summary row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListIssues returns a page of stored issues matching the filter,
// with record labels + navigation hints pre-resolved so the frontend
// renders in one round-trip. Ordered by severity (error first) then
// rule then most-recent-first, so the top of the list is the highest-
// priority open issue.
func (a *Archive) ListIssues(ctx context.Context, f IssueFilter, limit, offset int) ([]Issue, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// Build the WHERE clause dynamically since the filter set is small
	// but variable. Parameterized either way — no string interpolation
	// of user values.
	var (
		where []string
		args  []any
	)
	if f.TableName != "" {
		where = append(where, "table_name = ?")
		args = append(args, f.TableName)
	}
	if f.RuleID != "" {
		where = append(where, "rule_id = ?")
		args = append(args, f.RuleID)
	}
	if len(f.Severities) > 0 {
		ph := strings.Repeat("?,", len(f.Severities))
		ph = ph[:len(ph)-1]
		where = append(where, "severity IN ("+ph+")")
		for _, s := range f.Severities {
			args = append(args, s)
		}
	}
	if f.HideAcknowledged {
		where = append(where, "(acknowledged_at IS NULL OR acknowledged_at = '')")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	// Count first (unfiltered by limit/offset) so the frontend can
	// render a "1-50 of 2892" indicator without a second call.
	var total int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results `+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("core: count issues: %w", err)
	}

	// severity sort: error > warn > info > debug. SQLite lacks
	// ENUM-based ordering; a CASE expression puts them in the right
	// bucket. Ties broken by rule_id + created_at desc so a curator
	// scanning the list sees related issues near each other.
	listQuery := `
		SELECT result_id, table_name, record_id, rule_id, COALESCE(rule_name, ''),
		       COALESCE(field_name, ''), severity, enforcement, message,
		       COALESCE(actual_value, ''), COALESCE(expected_value, ''),
		       created_at, COALESCE(acknowledged_by, ''), COALESCE(acknowledged_at, '')
		FROM __gsvalidator_results
		` + whereSQL + `
		ORDER BY CASE severity
		    WHEN 'error' THEN 0
		    WHEN 'warn'  THEN 1
		    WHEN 'info'  THEN 2
		    WHEN 'debug' THEN 3
		    ELSE 4
		END, rule_id, created_at DESC
		LIMIT ? OFFSET ?`
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := a.db.QueryContext(ctx, listQuery, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("core: list issues: %w", err)
	}
	defer rows.Close()

	var out []Issue
	for rows.Next() {
		var i Issue
		if err := rows.Scan(
			&i.ID, &i.TableName, &i.RecordID, &i.RuleID, &i.RuleName,
			&i.FieldName, &i.Severity, &i.Enforcement, &i.Message,
			&i.ActualValue, &i.ExpectedValue,
			&i.CreatedAt, &i.AcknowledgedBy, &i.AcknowledgedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("core: scan issue row: %w", err)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Resolve record labels + navigation hints per row. One resolver
	// per table; each writes RecordLabel and (when applicable)
	// LinkTaxonID so the Issues list renders in a single round-trip.
	for i := range out {
		switch out[i].TableName {
		case "name":
			resolveNameIssue(ctx, a.db, &out[i])
		case "taxon":
			resolveTaxonIssue(ctx, a.db, &out[i])
		case "metadata":
			resolveMetadataIssue(ctx, a.db, &out[i])
		}
	}

	return out, total, nil
}

// resolveNameIssue fills RecordLabel and LinkTaxonID for a name-scoped
// issue. Best-effort — a lookup failure leaves both empty and the
// frontend renders the raw record id.
func resolveNameIssue(ctx context.Context, db *sql.DB, i *Issue) {
	var (
		canonicalFull  sql.NullString
		scientificName sql.NullString
	)
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(gn__canonical_full, ''), COALESCE(col__scientific_name, '')
		 FROM name WHERE col__id = ?`,
		i.RecordID,
	).Scan(&canonicalFull, &scientificName)
	if err == nil {
		switch {
		case canonicalFull.String != "":
			i.RecordLabel = canonicalFull.String
		case scientificName.String != "":
			i.RecordLabel = scientificName.String
		}
	}
	// Find owning taxon — the common case, where the name has exactly
	// one taxon. Multi-taxon names (rare) leave LinkTaxonID empty; the
	// frontend can still display the row and disable the click target.
	var (
		taxonID string
		count   int
	)
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MIN(col__id), ''), COUNT(*)
		 FROM taxon WHERE col__name_id = ?`,
		i.RecordID,
	).Scan(&taxonID, &count); err == nil && count == 1 {
		i.LinkTaxonID = taxonID
	}
}

// resolveTaxonIssue fills RecordLabel with the taxon's canonical name
// (fetched from its associated name row) and LinkTaxonID with the
// taxon id itself so a click on the row opens the taxon detail pane.
func resolveTaxonIssue(ctx context.Context, db *sql.DB, i *Issue) {
	// Set the navigation target unconditionally — a taxon-scoped
	// issue always knows how to route back to its own detail pane.
	i.LinkTaxonID = i.RecordID
	var (
		canonicalFull  sql.NullString
		scientificName sql.NullString
	)
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(n.gn__canonical_full, ''),
		        COALESCE(n.col__scientific_name, '')
		 FROM taxon t
		 LEFT JOIN name n ON n.col__id = t.col__name_id
		 WHERE t.col__id = ?`,
		i.RecordID,
	).Scan(&canonicalFull, &scientificName)
	if err != nil {
		return
	}
	switch {
	case canonicalFull.String != "":
		i.RecordLabel = canonicalFull.String
	case scientificName.String != "":
		i.RecordLabel = scientificName.String
	}
}

// resolveMetadataIssue fills RecordLabel with the archive's metadata
// title. LinkTaxonID stays empty — clicking a metadata-scoped issue
// should route to the Metadata screen instead of a taxon detail
// pane; the frontend handles that dispatch.
func resolveMetadataIssue(ctx context.Context, db *sql.DB, i *Issue) {
	var title sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(col__title, '') FROM metadata WHERE col__id = ?`,
		i.RecordID,
	).Scan(&title)
	if err != nil {
		return
	}
	if title.String != "" {
		i.RecordLabel = "Archive metadata: " + title.String
	} else {
		i.RecordLabel = "Archive metadata"
	}
}
