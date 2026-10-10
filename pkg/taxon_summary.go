package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// SummaryRanks are the ranks summarized per reference, highest first.
// SummaryRankName summarizes the mentioned names themselves.
var SummaryRanks = []string{"kingdom", "phylum", "class", "order", "family", "genus"}

const SummaryRankName = "name"

// TaxonSummary is a reference's mentioned taxa at one rank.
type TaxonSummary struct {
	Name       string // most-mentioned taxon; "" when none qualifies
	Mentions   int
	Distinct   int // taxa of this rank mentioned
	SourceID   int // verifier source; 0 for SummaryRankName
	Top        []NameCount
	Classified int // matched mentions the source places at this rank
	Matched    int // mentions with a discovery match in the source
}

// NameCount is a taxon name with its mention count.
type NameCount struct {
	Name  string
	Count int
}

// discoveryMatch is the condition on a name mention (columns prefixed
// by alias) that its match may place the paper in the classification:
// exact matches, including a species matched only through its genus.
// Fuzzy matches are left out; a near spelling often belongs to an
// unrelated organism.
func discoveryMatch(alias string) string {
	return alias + "match_type IN ('Exact', 'PartialExact')"
}

// taxonSummaryMethod versions how summaries are computed; summaries
// stored by an older method are recomputed by BackfillTaxonSummaries.
const taxonSummaryMethod = 2

// minRankCoverage is the share of a reference's matched mentions the
// source must place at a rank for the rank to be summarized, so a few
// stray matches do not stand in for a rank the source leaves
// unassigned. minTaxonShare is the share of the leading taxon's count
// another taxon needs to be listed beside it.
const (
	minRankCoverage = 0.1
	minTaxonShare   = 0.1
)

const (
	topRanked = 5
	topNames  = 20
)

// refreshTaxonSummary recomputes a reference's taxon summary from its
// name mentions. Higher ranks come from one source, the one with the
// most discovery matches (see discoveryMatch), and count only those.
func refreshTaxonSummary(tx *Tx, referenceID string) error {
	if _, err := tx.tx.ExecContext(tx.ctx,
		`DELETE FROM hive__reference_taxon_summary WHERE reference_id = ?`, referenceID); err != nil {
		return fmt.Errorf("core: clear taxon summary: %w", err)
	}
	sourceTag, sourceID, matched, err := summarySource(tx, referenceID)
	if err != nil {
		return err
	}
	if matched > 0 {
		for _, rank := range SummaryRanks {
			if err := summarizeRank(tx, referenceID, rank, sourceTag, sourceID, matched); err != nil {
				return err
			}
		}
	}
	// Names as written: each source repeats the same mention counts,
	// so a name counts once.
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT mention, MAX(mention_count) AS n FROM hive__name_mention
		WHERE reference_id = ? GROUP BY mention ORDER BY n DESC, mention`, referenceID)
	if err != nil {
		return fmt.Errorf("core: summarize names: %w", err)
	}
	names, err := scanNameCounts(rows)
	if err != nil || len(names) == 0 {
		return err
	}
	total := 0
	for _, nc := range names {
		total += nc.Count
	}
	return insertTaxonSummary(tx, referenceID, SummaryRankName, 0, names, topNames, total, total)
}

// summarySource picks the source (raw__source tag and ID) with the
// most discovery-matched mentions in the reference, preferring the
// earlier of mentions.Sources on a tie. matched is 0 when no mention
// has a discovery match.
func summarySource(tx *Tx, referenceID string) (tag string, id, matched int, err error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT raw__source, SUM(mention_count) FROM hive__name_mention
		WHERE reference_id = ? AND `+discoveryMatch("")+` AND raw_taxon_id IS NOT NULL
		GROUP BY raw__source`, referenceID)
	if err != nil {
		return "", 0, 0, fmt.Errorf("core: summary source: %w", err)
	}
	defer rows.Close()
	bySource := map[int]struct {
		tag string
		n   int
	}{}
	for rows.Next() {
		var t string
		var n, sid int
		if err := rows.Scan(&t, &n); err != nil {
			return "", 0, 0, err
		}
		if _, err := fmt.Sscanf(t, "gnverifier:%d@", &sid); err != nil {
			continue
		}
		if n > bySource[sid].n {
			bySource[sid] = struct {
				tag string
				n   int
			}{t, n}
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, 0, err
	}
	for _, src := range mentions.Sources {
		if b := bySource[src.ID]; b.n > matched {
			tag, id, matched = b.tag, src.ID, b.n
		}
	}
	return tag, id, matched, nil
}

// summarizeRank stores the reference's taxa at rank from one source.
// When the source places too few mentions at the rank the row keeps
// an empty name, so the counts still explain the empty cell.
func summarizeRank(tx *Tx, referenceID, rank, sourceTag string, sourceID, matched int) error {
	col := "col__" + rank
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT t.`+col+`, SUM(m.mention_count) AS n
		FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id = ? AND m.raw__source = ? AND `+discoveryMatch("m.")+`
			AND COALESCE(t.`+col+`, '') <> ''
		GROUP BY t.`+col+`
		ORDER BY n DESC, t.`+col, referenceID, sourceTag)
	if err != nil {
		return fmt.Errorf("core: summarize %s: %w", rank, err)
	}
	all, err := scanNameCounts(rows)
	if err != nil {
		return err
	}
	classified := 0
	for _, nc := range all {
		classified += nc.Count
	}
	var kept []NameCount
	if float64(classified) >= minRankCoverage*float64(matched) {
		for _, nc := range all {
			if float64(nc.Count) >= minTaxonShare*float64(all[0].Count) {
				kept = append(kept, nc)
			}
		}
	}
	return insertTaxonSummary(tx, referenceID, rank, sourceID, kept, topRanked, classified, matched)
}

func scanNameCounts(rows *sql.Rows) ([]NameCount, error) {
	defer rows.Close()
	var out []NameCount
	for rows.Next() {
		var nc NameCount
		if err := rows.Scan(&nc.Name, &nc.Count); err != nil {
			return nil, err
		}
		out = append(out, nc)
	}
	return out, rows.Err()
}

// insertTaxonSummary stores list (ordered by count, possibly empty)
// keeping its first top entries.
func insertTaxonSummary(tx *Tx, referenceID, rank string, sourceID int, list []NameCount, top, classified, matched int) error {
	head := list[:min(top, len(list))]
	pairs := make([][2]any, len(head))
	for i, nc := range head {
		pairs[i] = [2]any{nc.Name, nc.Count}
	}
	topJSON, _ := json.Marshal(pairs)
	name, n := "", 0
	if len(list) > 0 {
		name, n = list[0].Name, list[0].Count
	}
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO hive__reference_taxon_summary
			(reference_id, rank, source_id, name, mentions, distinct_count, top_json,
			 classified, matched, method)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		referenceID, rank, sourceID, name, n, len(list), string(topJSON),
		classified, matched, taxonSummaryMethod)
	if err != nil {
		return fmt.Errorf("core: store taxon summary %s: %w", rank, err)
	}
	return nil
}

// BackfillTaxonSummaries computes summaries for references that have
// name mentions but no summary from the current method. Returns how many were filled.
func (a *Archive) BackfillTaxonSummaries(ctx context.Context) (int, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT DISTINCT m.reference_id FROM hive__name_mention m
		WHERE NOT EXISTS (SELECT 1 FROM hive__reference_taxon_summary s
			WHERE s.reference_id = m.reference_id AND s.method = ?)`, taxonSummaryMethod)
	if err != nil {
		return 0, fmt.Errorf("core: list references without taxon summary: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for i, id := range ids {
		if ctx.Err() != nil {
			return i, ctx.Err()
		}
		if err := a.WithTx(ctx, func(tx *Tx) error { return refreshTaxonSummary(tx, id) }); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// batchTaxonSummaries returns the stored summaries of ids, by
// reference and rank.
func (a *Archive) batchTaxonSummaries(ctx context.Context, ids []string) (map[string]map[string]TaxonSummary, error) {
	in, args := inClause(ids)
	rows, err := a.db.QueryContext(ctx, `
		SELECT reference_id, rank, source_id, name, mentions, distinct_count, top_json,
			classified, matched
		FROM hive__reference_taxon_summary WHERE reference_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("core: taxon summaries: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]TaxonSummary{}
	for rows.Next() {
		var id, rank, topJSON string
		var ts TaxonSummary
		if err := rows.Scan(&id, &rank, &ts.SourceID, &ts.Name, &ts.Mentions, &ts.Distinct, &topJSON, &ts.Classified, &ts.Matched); err != nil {
			return nil, err
		}
		var pairs [][2]any
		if json.Unmarshal([]byte(topJSON), &pairs) == nil {
			for _, p := range pairs {
				name, _ := p[0].(string)
				n, _ := p[1].(float64)
				ts.Top = append(ts.Top, NameCount{name, int(n)})
			}
		}
		if out[id] == nil {
			out[id] = map[string]TaxonSummary{}
		}
		out[id][rank] = ts
	}
	return out, rows.Err()
}

func inClause(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	in := "?"
	for range ids[1:] {
		in += ",?"
	}
	return in, args
}
