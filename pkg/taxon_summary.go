package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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

// isDiscoveryMatch reports whether a match type passes discoveryMatch.
func isDiscoveryMatch(matchType string) bool {
	return matchType == "Exact" || matchType == "PartialExact"
}

// taxonSummaryMethod versions how summaries are computed; summaries
// stored by an older method are recomputed by BackfillTaxonSummaries.
const taxonSummaryMethod = 3

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
// name mentions. Higher ranks count only discovery matches (see
// discoveryMatch), taking each homonym's chosen lineage. Each rank
// comes from the source with the most matched mentions in the paper,
// or from the next source when that one places too few at the rank.
func refreshTaxonSummary(tx *Tx, referenceID string) error {
	if _, err := tx.tx.ExecContext(tx.ctx,
		`DELETE FROM hive__reference_taxon_summary WHERE reference_id = ?`, referenceID); err != nil {
		return fmt.Errorf("core: clear taxon summary: %w", err)
	}
	if err := chooseHomonymRows(tx, referenceID); err != nil {
		return err
	}
	sources, err := summarySources(tx, referenceID)
	if err != nil {
		return err
	}
	// Lowest rank first: a rank taken from another source counts only
	// records that agree with the taxa already shown below it. Genera
	// are not used for that, since homonyms share the genus name.
	var anchors []string
	for i := len(SummaryRanks) - 1; i >= 0 && len(sources) > 0; i-- {
		rank := SummaryRanks[i]
		kept, err := summarizeRank(tx, referenceID, rank, sources, anchors)
		if err != nil {
			return err
		}
		if rank == "genus" {
			continue
		}
		for _, nc := range kept {
			anchors = append(anchors, nc.Name)
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

// summarySource is a verifier source's release in a reference's
// mentions, with the mentions it matched.
type summarySource struct {
	tag     string // raw__source
	id      int
	matched int
}

// summarySources returns the sources with discovery matches in the
// reference, most matched mentions first; ties follow
// mentions.Sources.
func summarySources(tx *Tx, referenceID string) ([]summarySource, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT raw__source, SUM(mention_count) FROM hive__name_mention
		WHERE reference_id = ? AND `+discoveryMatch("")+` AND chosen = 1 AND raw_taxon_id IS NOT NULL
		GROUP BY raw__source`, referenceID)
	if err != nil {
		return nil, fmt.Errorf("core: summary sources: %w", err)
	}
	defer rows.Close()
	bySource := map[int]summarySource{}
	for rows.Next() {
		var src summarySource
		if err := rows.Scan(&src.tag, &src.matched); err != nil {
			return nil, err
		}
		if _, err := fmt.Sscanf(src.tag, "gnverifier:%d@", &src.id); err != nil {
			continue
		}
		if src.matched > bySource[src.id].matched {
			bySource[src.id] = src
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []summarySource
	for _, s := range mentions.Sources {
		if src, ok := bySource[s.ID]; ok {
			out = append(out, src)
		}
	}
	slices.SortStableFunc(out, func(a, b summarySource) int { return b.matched - a.matched })
	return out, nil
}

// summarizeRank stores the reference's taxa at rank from the first of
// sources that places at least minRankCoverage of its matched
// mentions there, and returns them. Sources after the first count
// only records whose classification contains one of anchors, the
// taxa already summarized at lower ranks, so a source that knows few
// of the paper's names cannot fill the rank with stray matches. When
// no source qualifies, the row keeps an empty name with the first
// source's counts, which explain the empty cell.
func summarizeRank(tx *Tx, referenceID, rank string, sources []summarySource, anchors []string) ([]NameCount, error) {
	type result struct {
		src        summarySource
		kept       []NameCount
		classified int
	}
	var first *result
	for i, src := range sources {
		if i > 0 && len(anchors) == 0 {
			break
		}
		var within []string
		if i > 0 {
			within = anchors
		}
		all, err := rankCounts(tx, referenceID, rank, src.tag, within)
		if err != nil {
			return nil, err
		}
		r := result{src: src}
		for _, nc := range all {
			r.classified += nc.Count
		}
		if float64(r.classified) >= minRankCoverage*float64(src.matched) {
			for _, nc := range all {
				if float64(nc.Count) >= minTaxonShare*float64(all[0].Count) {
					r.kept = append(r.kept, nc)
				}
			}
		}
		if len(r.kept) > 0 {
			return r.kept, insertTaxonSummary(tx, referenceID, rank, src.id, r.kept, topRanked, r.classified, src.matched)
		}
		if first == nil {
			first = &r
		}
	}
	return nil, insertTaxonSummary(tx, referenceID, rank, first.src.id, nil, topRanked, first.classified, first.src.matched)
}

// rankCounts returns the mentions per taxon at rank in one source
// release, most first. A non-empty within keeps only records whose
// classification contains one of those taxa.
func rankCounts(tx *Tx, referenceID, rank, sourceTag string, within []string) ([]NameCount, error) {
	col := "col__" + rank
	args := []any{referenceID, sourceTag}
	cond := ""
	if len(within) > 0 {
		ors := make([]string, len(within))
		for i, name := range within {
			ors[i] = "INSTR(t.raw__path, '|' || ? || '|') > 0"
			args = append(args, name)
		}
		cond = " AND (" + strings.Join(ors, " OR ") + ")"
	}
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT t.`+col+`, SUM(m.mention_count) AS n
		FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id = ? AND m.raw__source = ? AND m.chosen = 1 AND `+discoveryMatch("m.")+`
			AND COALESCE(t.`+col+`, '') <> ''`+cond+`
		GROUP BY t.`+col+`
		ORDER BY n DESC, t.`+col, args...)
	if err != nil {
		return nil, fmt.Errorf("core: summarize %s: %w", rank, err)
	}
	return scanNameCounts(rows)
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
