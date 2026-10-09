package hive

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// SummaryRanks are the ranks summarized per reference, highest first.
// SummaryRankName summarizes the mentioned names themselves.
var SummaryRanks = []string{"kingdom", "phylum", "class", "order", "family", "genus"}

const SummaryRankName = "name"

// TaxonSummary is a reference's most-mentioned taxon at one rank.
type TaxonSummary struct {
	Name     string
	Mentions int
	Distinct int // taxa of this rank mentioned
	SourceID int // verifier source; 0 for SummaryRankName
	Top      []NameCount
}

// NameCount is a taxon name with its mention count.
type NameCount struct {
	Name  string
	Count int
}

// refreshTaxonSummary recomputes a reference's taxon summary from its
// name mentions.
func refreshTaxonSummary(tx *Tx, referenceID string) error {
	if _, err := tx.tx.ExecContext(tx.ctx,
		`DELETE FROM hive__reference_taxon_summary WHERE reference_id = ?`, referenceID); err != nil {
		return fmt.Errorf("core: clear taxon summary: %w", err)
	}
	for _, rank := range SummaryRanks {
		col := "col__" + rank
		rows, err := tx.tx.QueryContext(tx.ctx, `
			SELECT m.raw__source, t.`+col+`, SUM(m.mention_count) AS n
			FROM hive__name_mention m
			JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
			WHERE m.reference_id = ? AND COALESCE(t.`+col+`, '') <> ''
			GROUP BY m.raw__source, t.`+col+`
			ORDER BY n DESC, t.`+col, referenceID)
		if err != nil {
			return fmt.Errorf("core: summarize %s: %w", rank, err)
		}
		bySource := map[int][]NameCount{}
		for rows.Next() {
			var source, name string
			var n int
			if err := rows.Scan(&source, &name, &n); err != nil {
				rows.Close()
				return err
			}
			var sid int
			if _, err := fmt.Sscanf(source, "gnverifier:%d@", &sid); err != nil {
				continue
			}
			bySource[sid] = append(bySource[sid], NameCount{name, n})
		}
		rows.Close()
		for _, src := range mentions.Sources {
			if list := bySource[src.ID]; len(list) > 0 {
				if err := insertTaxonSummary(tx, referenceID, rank, src.ID, list); err != nil {
					return err
				}
				break
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
	var names []NameCount
	for rows.Next() {
		var nc NameCount
		if err := rows.Scan(&nc.Name, &nc.Count); err != nil {
			rows.Close()
			return err
		}
		names = append(names, nc)
	}
	rows.Close()
	if len(names) > 0 {
		return insertTaxonSummary(tx, referenceID, SummaryRankName, 0, names)
	}
	return nil
}

// insertTaxonSummary stores the first of list (already ordered by
// count) with the top five.
func insertTaxonSummary(tx *Tx, referenceID, rank string, sourceID int, list []NameCount) error {
	top := list[:min(5, len(list))]
	pairs := make([][2]any, len(top))
	for i, nc := range top {
		pairs[i] = [2]any{nc.Name, nc.Count}
	}
	topJSON, _ := json.Marshal(pairs)
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO hive__reference_taxon_summary
			(reference_id, rank, source_id, name, mentions, distinct_count, top_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		referenceID, rank, sourceID, list[0].Name, list[0].Count, len(list), string(topJSON))
	if err != nil {
		return fmt.Errorf("core: store taxon summary %s: %w", rank, err)
	}
	return nil
}

// BackfillTaxonSummaries computes summaries for references that have
// name mentions but no summary yet. Returns how many were filled.
func (a *Archive) BackfillTaxonSummaries(ctx context.Context) (int, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT DISTINCT m.reference_id FROM hive__name_mention m
		WHERE NOT EXISTS (SELECT 1 FROM hive__reference_taxon_summary s
			WHERE s.reference_id = m.reference_id)`)
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
		SELECT reference_id, rank, source_id, name, mentions, distinct_count, top_json
		FROM hive__reference_taxon_summary WHERE reference_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("core: taxon summaries: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]TaxonSummary{}
	for rows.Next() {
		var id, rank, topJSON string
		var ts TaxonSummary
		if err := rows.Scan(&id, &rank, &ts.SourceID, &ts.Name, &ts.Mentions, &ts.Distinct, &topJSON); err != nil {
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
