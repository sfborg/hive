package hive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// The name-mention index records, per reference, how often each
// scientific name is tagged in the paper and how the name verifies
// against an external name source. It is a discovery aid: it makes
// papers findable by higher taxon before they are assimilated. Its
// rows never feed project data.
//
// Verified records are stored in raw__name and raw__taxon, tables with
// the columns of the SFGA name and taxon tables plus provenance
// columns. A record's position in the source's classification is
// stored in the taxon row's rank columns (col__family, col__order, …).

// rawProvenanceColumns are added to every raw__ table.
var rawProvenanceColumns = []string{"raw__source", "raw__reference_id", "raw__source_row"}

// rawTables are the SFGA tables mirrored as raw__ tables.
var rawTables = []string{"name", "taxon"}

// ensureRawNameTables creates the raw__ mirrors of the SFGA name and
// taxon tables and the hive__name_mention table. Mirrors take their
// columns from the SFGA table without its constraints, and gain any
// column the SFGA table has added since they were created.
func ensureRawNameTables(ctx context.Context, db *sql.DB) error {
	for _, t := range rawTables {
		raw := "raw__" + t
		if _, err := db.ExecContext(ctx,
			`CREATE TABLE IF NOT EXISTS `+raw+` AS SELECT * FROM `+t+` WHERE 0`,
		); err != nil {
			return fmt.Errorf("core: create %s: %w", raw, err)
		}
		cols, err := tableColumns(ctx, db, t)
		if err != nil {
			return err
		}
		cols = append(cols, rawProvenanceColumns...)
		have, err := tableColumns(ctx, db, raw)
		if err != nil {
			return err
		}
		haveSet := make(map[string]bool, len(have))
		for _, c := range have {
			haveSet[c] = true
		}
		for _, c := range cols {
			if haveSet[c] {
				continue
			}
			if _, err := db.ExecContext(ctx,
				`ALTER TABLE `+raw+` ADD COLUMN `+c+` TEXT`,
			); err != nil {
				return fmt.Errorf("core: add %s.%s: %w", raw, c, err)
			}
		}
		if _, err := db.ExecContext(ctx,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_`+raw+`_source_id ON `+raw+` (raw__source, col__id)`,
		); err != nil {
			return fmt.Errorf("core: index %s: %w", raw, err)
		}
	}
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS hive__name_mention (
			reference_id  TEXT NOT NULL,
			-- The name as tagged in the paper (full-name attribute).
			mention       TEXT NOT NULL,
			mention_count INTEGER NOT NULL,
			-- Verifier source and release, as in raw__*.raw__source.
			raw__source   TEXT NOT NULL,
			-- raw__taxon.col__id of the verified record; NULL when the
			-- name did not verify in the source.
			raw_taxon_id  TEXT,
			match_type    TEXT NOT NULL DEFAULT '',
			verified_at   TEXT NOT NULL,
			PRIMARY KEY (reference_id, mention, raw__source)
		);
		CREATE INDEX IF NOT EXISTS idx_hive__name_mention_taxon
			ON hive__name_mention (raw__source, raw_taxon_id);`)
	if err != nil {
		return fmt.Errorf("core: create hive__name_mention: %w", err)
	}
	return nil
}

// tableColumns lists a table's column names in declaration order.
func tableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("core: columns of %s: %w", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// rankColumns maps verifier rank names to the SFGA taxon columns that
// hold a taxon's classification.
var rankColumns = map[string]string{
	"realm":       "sf__realm",
	"kingdom":     "col__kingdom",
	"phylum":      "col__phylum",
	"subphylum":   "col__subphylum",
	"class":       "col__class",
	"subclass":    "col__subclass",
	"order":       "col__order",
	"suborder":    "col__suborder",
	"superfamily": "col__superfamily",
	"family":      "col__family",
	"subfamily":   "col__subfamily",
	"tribe":       "col__tribe",
	"subtribe":    "col__subtribe",
	"genus":       "col__genus",
	"subgenus":    "col__subgenus",
	"section":     "col__section",
	"species":     "col__species",
}

// classificationColumns are the raw__taxon rank columns searched by a
// higher-taxon filter.
var classificationColumns = func() []string {
	cols := make([]string, 0, len(rankColumns))
	for _, c := range []string{"realm", "kingdom", "phylum", "subphylum", "class",
		"subclass", "order", "suborder", "superfamily", "family", "subfamily",
		"tribe", "subtribe", "genus", "subgenus", "section", "species"} {
		cols = append(cols, rankColumns[c])
	}
	return cols
}()

// ReplaceNameMentions replaces a reference's mention rows for one
// verifier source. counts holds the mention count per name; matches
// holds the verification result for the names that matched. Records
// are upserted into raw__name and raw__taxon, shared by every
// reference that mentions them.
func (a *Archive) ReplaceNameMentions(ctx context.Context, referenceID, source string,
	counts map[string]int, matches map[string]mentions.Match) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(tx.ctx,
			`DELETE FROM hive__name_mention WHERE reference_id = ? AND raw__source = ?`,
			referenceID, source,
		); err != nil {
			return fmt.Errorf("core: clear mentions: %w", err)
		}
		for name, n := range counts {
			var taxonID any
			matchType := "NoMatch"
			if m, ok := matches[name]; ok {
				if err := upsertRawRecord(tx, source, m); err != nil {
					return err
				}
				taxonID = m.RecordID
				matchType = m.MatchType
			}
			if _, err := tx.tx.ExecContext(tx.ctx, `
				INSERT INTO hive__name_mention
					(reference_id, mention, mention_count, raw__source,
					 raw_taxon_id, match_type, verified_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				referenceID, name, n, source, taxonID, matchType, now,
			); err != nil {
				return fmt.Errorf("core: insert mention %q: %w", name, err)
			}
		}
		return nil
	})
}

// upsertRawRecord writes a verified record's name and taxon rows.
func upsertRawRecord(tx *Tx, source string, m mentions.Match) error {
	leaf := m.Path[len(m.Path)-1]
	if _, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO raw__name (col__id, col__scientific_name, col__rank_id, raw__source)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (raw__source, col__id) DO UPDATE SET
			col__scientific_name = excluded.col__scientific_name,
			col__rank_id = excluded.col__rank_id`,
		leaf.ID, leaf.Name, leaf.Rank, source,
	); err != nil {
		return fmt.Errorf("core: upsert raw__name %s: %w", leaf.ID, err)
	}
	cols := []string{"col__id", "col__name_id", "col__parent_id", "raw__source"}
	parent := ""
	if len(m.Path) > 1 {
		parent = m.Path[len(m.Path)-2].ID
	}
	args := []any{leaf.ID, leaf.ID, parent, source}
	for _, r := range m.Path {
		if col, ok := rankColumns[r.Rank]; ok {
			cols = append(cols, col)
			args = append(args, r.Name)
		}
	}
	sets := make([]string, 0, len(cols))
	for _, c := range cols[1:] {
		if c == "raw__source" {
			continue
		}
		sets = append(sets, c+" = excluded."+c)
	}
	q := `INSERT INTO raw__taxon (` + strings.Join(cols, ", ") + `)
		VALUES (` + strings.Repeat("?, ", len(cols)-1) + `?)
		ON CONFLICT (raw__source, col__id) DO UPDATE SET ` + strings.Join(sets, ", ")
	if _, err := tx.tx.ExecContext(tx.ctx, q, args...); err != nil {
		return fmt.Errorf("core: upsert raw__taxon %s: %w", leaf.ID, err)
	}
	return nil
}

// NameMentionCounts reads a reference's attached documents and returns
// the merged mention counts across them, with the number of documents
// that had augmented JATS. read returns a document's augmented JATS,
// or nil when it has none.
func (a *Archive) NameMentionCounts(ctx context.Context, referenceID string,
	read func(sha string) ([]byte, error)) (map[string]int, int, error) {
	shas, err := a.ReferencePDFs(ctx, referenceID)
	if err != nil {
		return nil, 0, err
	}
	var sets []map[string]int
	for _, sha := range shas {
		jats, err := read(sha)
		if err != nil {
			return nil, 0, err
		}
		if jats == nil {
			continue
		}
		c, err := mentions.Count(jats)
		if err != nil {
			return nil, 0, fmt.Errorf("core: count mentions in %s: %w", sha, err)
		}
		sets = append(sets, c)
	}
	return mentions.Merge(sets...), len(sets), nil
}

// ColTaxon is a higher taxon from the name-mention index's verifier
// source, with the number of indexed papers mentioning names in it.
type ColTaxon struct {
	Name   string
	Rank   string
	Papers int
}

// colTaxonRanks are the ranks offered when looking up higher taxa.
var colTaxonRanks = []string{"kingdom", "phylum", "subphylum", "class",
	"subclass", "order", "suborder", "family", "subfamily", "genus"}

// ColTaxa returns higher taxa in the name-mention index whose names
// start with prefix (case-insensitive), most-mentioned first. They are
// the values a ColParent query can match.
func (a *Archive) ColTaxa(ctx context.Context, prefix string, limit int) ([]ColTaxon, error) {
	if limit <= 0 {
		limit = 20
	}
	parts := make([]string, 0, len(colTaxonRanks))
	args := make([]any, 0, len(colTaxonRanks)+1)
	for _, r := range colTaxonRanks {
		col := rankColumns[r]
		parts = append(parts, `SELECT t.`+col+` AS name, '`+r+`' AS rank, m.reference_id
			FROM hive__name_mention m
			JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
			WHERE t.`+col+` != '' AND LOWER(t.`+col+`) LIKE LOWER(?) ESCAPE '\'`)
		args = append(args, likePrefix(prefix))
	}
	q := `SELECT name, rank, COUNT(DISTINCT reference_id) AS papers FROM (` +
		strings.Join(parts, " UNION ALL ") + `)
		GROUP BY name, rank ORDER BY papers DESC, name LIMIT ?`
	args = append(args, limit)
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: col taxa: %w", err)
	}
	defer rows.Close()
	var out []ColTaxon
	for rows.Next() {
		var t ColTaxon
		if err := rows.Scan(&t.Name, &t.Rank, &t.Papers); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// likePrefix escapes LIKE wildcards in s and appends %.
func likePrefix(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(strings.TrimSpace(s)) + "%"
}
