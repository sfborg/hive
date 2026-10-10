package hive

import (
	"context"
	"fmt"
	"strings"
)

// ReferenceSortKey is one column of a multi-column reference sort.
type ReferenceSortKey struct {
	Column string // one of ReferenceSortColumns
	Desc   bool
}

// firstPDFStats selects one hive__jats_stats column for a reference's
// first attached file, the file extraction runs read.
func firstPDFStats(col string) string {
	return `(SELECT s.` + col + ` FROM hive__reference_pdf p
		JOIN hive__jats_stats s ON s.sha512 = p.sha512
		WHERE p.reference_id = reference.col__id
		ORDER BY p.attached_at LIMIT 1)`
}

// runTokens sums a provider-reported token field over a reference's
// extraction runs that have not been deleted. Runs without an
// envelope (curator and local runs) contribute nothing.
func runTokens(field string) string {
	return `(SELECT SUM(CASE WHEN json_valid(r.envelope_json)
			THEN json_extract(r.envelope_json, '$.` + field + `') END)
		FROM hive__extraction_run r
		WHERE r.reference_id = reference.col__id AND r.deleted_at IS NULL)`
}

// summaryName selects a reference's stored most-mentioned taxon at
// rank.
func summaryName(rank string) string {
	return `(SELECT LOWER(NULLIF(s.name, '')) FROM hive__reference_taxon_summary s
		WHERE s.reference_id = reference.col__id AND s.rank = '` + rank + `')`
}

// ReferenceSortColumns maps sortable column names to SQL expressions
// over the reference table.
var ReferenceSortColumns = map[string]string{
	"author":     "LOWER(col__author)",
	"year":       "substr(col__issued, 1, 4)",
	"title":      "LOWER(col__title)",
	"modified":   "col__modified",
	"license":    "(SELECT l.license FROM hive__reference_license l WHERE l.reference_id = reference.col__id)",
	"pages":      firstPDFStats("pdf_pages"),
	"words":      firstPDFStats("words"),
	"characters": firstPDFStats("characters"),
	"tokens":     "(" + runTokens("input_tokens") + " + " + runTokens("output_tokens") + ")",
	// Open validation issues compared by error count, then warn,
	// info and debug counts.
	"issues": `COALESCE((SELECT SUM(severity = 'error') * 1000000000 + SUM(severity = 'warn') * 1000000
			+ SUM(severity = 'info') * 1000 + SUM(severity = 'debug')
		FROM __gsvalidator_results v
		WHERE v.table_name = 'reference' AND v.record_id = reference.col__id
		  AND (v.acknowledged_at IS NULL OR v.acknowledged_at = '')), 0)`,
}

func init() {
	for _, rank := range append(append([]string{}, SummaryRanks...), SummaryRankName) {
		ReferenceSortColumns[rank] = summaryName(rank)
	}
}

// ParseReferenceSort parses "col:asc,col:desc" into sort keys. The
// direction defaults to ascending.
func ParseReferenceSort(raw string) ([]ReferenceSortKey, error) {
	return parseSortKeys(raw, ReferenceSortColumns)
}

// parseSortKeys parses "col:asc,col:desc" into sort keys over the given
// column set. The direction defaults to ascending.
func parseSortKeys(raw string, columns map[string]string) ([]ReferenceSortKey, error) {
	var keys []ReferenceSortKey
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		col, dir, _ := strings.Cut(part, ":")
		if _, ok := columns[col]; !ok {
			return nil, fmt.Errorf("unknown sort column %q", col)
		}
		switch dir {
		case "", "asc":
			keys = append(keys, ReferenceSortKey{Column: col})
		case "desc":
			keys = append(keys, ReferenceSortKey{Column: col, Desc: true})
		default:
			return nil, fmt.Errorf("sort direction for %q must be asc or desc", col)
		}
	}
	return keys, nil
}

// sortOrderBy builds an ORDER BY clause for keys. Empty values sort
// last in either direction, and col__id breaks ties so paginated
// results keep a stable order.
func sortOrderBy(keys []ReferenceSortKey) string {
	var parts []string
	for _, k := range keys {
		expr := ReferenceSortColumns[k.Column]
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		parts = append(parts, "COALESCE("+expr+", '') = ''", expr+" "+dir)
	}
	parts = append(parts, "col__id")
	return strings.Join(parts, ", ")
}

// referenceSize is the size of a reference's first attached file and
// the provider-reported tokens its extraction runs used.
type referenceSize struct {
	Pages, Words, Characters int
	TokensIn, TokensOut      int
	HasRuns                  bool
	Modified                 string
}

func (a *Archive) batchReferenceSizes(ctx context.Context, ids []string) (map[string]referenceSize, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := `SELECT reference.col__id,
		COALESCE(` + firstPDFStats("pdf_pages") + `, 0),
		COALESCE(` + firstPDFStats("words") + `, 0),
		COALESCE(` + firstPDFStats("characters") + `, 0),
		COALESCE(` + runTokens("input_tokens") + `, 0),
		COALESCE(` + runTokens("output_tokens") + `, 0),
		EXISTS (SELECT 1 FROM hive__extraction_run r
			WHERE r.reference_id = reference.col__id AND r.deleted_at IS NULL),
		COALESCE(reference.col__modified, '')
		FROM reference WHERE reference.col__id IN (` + placeholders + `)`
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: reference sizes: %w", err)
	}
	defer rows.Close()
	out := map[string]referenceSize{}
	for rows.Next() {
		var id string
		var s referenceSize
		if err := rows.Scan(&id, &s.Pages, &s.Words, &s.Characters, &s.TokensIn, &s.TokensOut, &s.HasRuns, &s.Modified); err != nil {
			return nil, fmt.Errorf("core: scan reference size: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}
