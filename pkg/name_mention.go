package hive

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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
// stored in the taxon row's rank columns (col__family, col__order, …)
// and, including unranked levels, in raw__path.

// rawProvenanceColumns are added to every raw__ table. raw__path holds
// a verifier record's full classification as "|top|…|record|", so
// unranked levels can be matched too.
var rawProvenanceColumns = []string{"raw__source", "raw__reference_id", "raw__source_row", "raw__path"}

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
	if err := migrateNameMentionCandidates(ctx, db); err != nil {
		return err
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
			-- A name matching several lineages (homonyms) has one row
			-- per lineage, numbered in the verifier's score order.
			candidate     INTEGER NOT NULL DEFAULT 0,
			-- 1 on the row that fits the paper's other names best;
			-- summaries read only chosen rows, the higher-taxon filter
			-- reads every row.
			chosen        INTEGER NOT NULL DEFAULT 1,
			-- The authorship the paper gives the name, '' when none,
			-- and the verifier's author-match score for this lineage
			-- (0–1, -1 when the record has no authorship; NULL when
			-- the paper gives none).
			authorship    TEXT NOT NULL DEFAULT '',
			author_score  REAL,
			PRIMARY KEY (reference_id, mention, raw__source, candidate)
		);
		CREATE INDEX IF NOT EXISTS idx_hive__name_mention_taxon
			ON hive__name_mention (raw__source, raw_taxon_id);`)
	if err != nil {
		return fmt.Errorf("core: create hive__name_mention: %w", err)
	}
	return nil
}

// migrateNameMentionCandidates rebuilds a hive__name_mention that
// predates homonym candidates; existing rows become each name's only,
// chosen candidate.
func migrateNameMentionCandidates(ctx context.Context, db *sql.DB) error {
	cols, err := tableColumns(ctx, db, "hive__name_mention")
	if err != nil || len(cols) == 0 {
		return err
	}
	if slices.Contains(cols, "candidate") {
		for _, add := range []struct{ col, ddl string }{
			{"authorship", `ALTER TABLE hive__name_mention ADD COLUMN authorship TEXT NOT NULL DEFAULT ''`},
			{"author_score", `ALTER TABLE hive__name_mention ADD COLUMN author_score REAL`},
		} {
			if slices.Contains(cols, add.col) {
				continue
			}
			if _, err := db.ExecContext(ctx, add.ddl); err != nil {
				return fmt.Errorf("core: add hive__name_mention.%s: %w", add.col, err)
			}
		}
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE hive__name_mention RENAME TO hive__name_mention_old`,
		`DROP INDEX IF EXISTS idx_hive__name_mention_taxon`,
		`CREATE TABLE hive__name_mention (
			reference_id  TEXT NOT NULL,
			mention       TEXT NOT NULL,
			mention_count INTEGER NOT NULL,
			raw__source   TEXT NOT NULL,
			raw_taxon_id  TEXT,
			match_type    TEXT NOT NULL DEFAULT '',
			verified_at   TEXT NOT NULL,
			candidate     INTEGER NOT NULL DEFAULT 0,
			chosen        INTEGER NOT NULL DEFAULT 1,
			authorship    TEXT NOT NULL DEFAULT '',
			author_score  REAL,
			PRIMARY KEY (reference_id, mention, raw__source, candidate)
		)`,
		`INSERT INTO hive__name_mention
			(reference_id, mention, mention_count, raw__source, raw_taxon_id, match_type, verified_at)
			SELECT reference_id, mention, mention_count, raw__source, raw_taxon_id, match_type, verified_at
			FROM hive__name_mention_old`,
		`DROP TABLE hive__name_mention_old`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("core: migrate hive__name_mention: %w", err)
		}
	}
	return tx.Commit()
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


// ReplaceNameMentions replaces a reference's mention rows for one
// verifier source without authorships (see
// ReplaceNameMentionsWithAuthors).
func (a *Archive) ReplaceNameMentions(ctx context.Context, referenceID, source string,
	counts map[string]int, matches map[string]mentions.Match) error {
	return a.ReplaceNameMentionsWithAuthors(ctx, referenceID, source, counts, matches, nil)
}

// NameAuthorship is the authorship a paper gives a name and, per
// candidate lineage of the name's match, the verifier's author-match
// score (see mentions.AuthorScores).
type NameAuthorship struct {
	Authorship string
	Scores     []float32
}

// ReplaceNameMentionsWithAuthors replaces a reference's mention rows
// for one verifier source. counts holds the mention count per name;
// matches holds the verification result for the names that matched;
// authors holds the authorship the paper gives a name, if any. A name
// with homonyms gets a row per lineage; the lineage that fits the
// name's authorship, else the paper's other names, is marked chosen
// when the summary is refreshed (see chooseHomonymRows). Records are
// upserted into raw__name and raw__taxon, shared by every reference
// that mentions them.
func (a *Archive) ReplaceNameMentionsWithAuthors(ctx context.Context, referenceID, source string,
	counts map[string]int, matches map[string]mentions.Match, authors map[string]NameAuthorship) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(tx.ctx,
			`DELETE FROM hive__name_mention WHERE reference_id = ? AND raw__source = ?`,
			referenceID, source,
		); err != nil {
			return fmt.Errorf("core: clear mentions: %w", err)
		}
		insert := func(name string, n, candidate int, taxonID any, matchType string, score any) error {
			if _, err := tx.tx.ExecContext(tx.ctx, `
				INSERT INTO hive__name_mention
					(reference_id, mention, mention_count, raw__source, raw_taxon_id,
					 match_type, verified_at, candidate, chosen, authorship, author_score)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				referenceID, name, n, source, taxonID, matchType, now, candidate,
				candidate == 0, authors[name].Authorship, score,
			); err != nil {
				return fmt.Errorf("core: insert mention %q: %w", name, err)
			}
			return nil
		}
		for name, n := range counts {
			m, ok := matches[name]
			if !ok {
				if err := insert(name, n, 0, nil, "NoMatch", nil); err != nil {
					return err
				}
				continue
			}
			au := authors[name]
			for i, c := range candidates(m) {
				if err := upsertRawRecord(tx, source, c); err != nil {
					return err
				}
				var score any
				if au.Authorship != "" && i < len(au.Scores) {
					score = au.Scores[i]
				}
				if err := insert(name, n, i, c.RecordID, m.MatchType, score); err != nil {
					return err
				}
			}
		}
		return refreshTaxonSummary(tx, referenceID)
	})
}

// candidates returns a match's lineages: the top-scored one, then its
// homonyms.
func candidates(m mentions.Match) []mentions.Match {
	return append([]mentions.Match{m}, m.Homonyms...)
}

// homonymGroup is one name's candidate lineages in one source, as
// raw__path taxon names.
type homonymGroup struct {
	count int
	paths [][]string
	// species is true for an exact match of a name below the genus,
	// which is rarely shared by unrelated organisms.
	species bool
	// uninomial is true for a single-word name (a genus or higher).
	uninomial bool
	// authorship is what the paper gives the name; scores are the
	// candidates' author-match scores (see NameAuthorship).
	authorship string
	scores     []float64
}

// Choices returned by chooseHomonyms besides a candidate index.
const (
	// noCandidate rejects every candidate: the paper's dated
	// authorship matches none of them.
	noCandidate = -1
)

// byAuthorship returns the candidate the name's authorship points to:
// the one scoring highest above 0, or noCandidate when the name is a
// single word, the authorship carries a year, and every candidate's
// record has an authorship that does not match. Only single-word
// names are rejected: names below the genus are rarely homonyms, and
// a mismatch there usually means authors written differently. ok is
// false when the authorship decides nothing.
func (g *homonymGroup) byAuthorship() (choice int, ok bool) {
	if g.authorship == "" || len(g.scores) != len(g.paths) {
		return 0, false
	}
	best, bestScore, tied := 0, 0.0, false
	allMismatch := true
	for i, sc := range g.scores {
		if sc != 0 {
			allMismatch = false
		}
		switch {
		case sc > bestScore:
			best, bestScore, tied = i, sc, false
		case sc == bestScore && sc > 0:
			tied = true
		}
	}
	if bestScore > 0 && !tied {
		return best, true
	}
	if allMismatch && g.uninomial && mentions.HasYear(g.authorship) {
		return noCandidate, true
	}
	return 0, false
}

// chooseHomonymRows marks, for each of the reference's names with
// homonyms, the candidate chosen by chooseHomonyms. Context comes from
// every source, so a name one source places unambiguously settles
// the same name's homonyms in another.
func chooseHomonymRows(tx *Tx, referenceID string) error {
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT m.raw__source, m.mention, m.candidate, m.mention_count, m.match_type,
			t.raw__path, m.authorship, COALESCE(m.author_score, -1)
		FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.reference_id = ? AND `+discoveryMatch("m.")+`
		ORDER BY m.raw__source, m.mention, m.candidate`, referenceID)
	if err != nil {
		return fmt.Errorf("core: homonym candidates: %w", err)
	}
	groups := map[[2]string]*homonymGroup{}
	for rows.Next() {
		var source, mention, matchType, path, authorship string
		var candidate, n int
		var score float64
		if err := rows.Scan(&source, &mention, &candidate, &n, &matchType, &path, &authorship, &score); err != nil {
			rows.Close()
			return err
		}
		key := [2]string{source, mention}
		g := groups[key]
		if g == nil {
			g = &homonymGroup{count: n, authorship: authorship,
				species:   matchType == "Exact" && strings.Contains(mention, " "),
				uninomial: !strings.Contains(mention, " ")}
			groups[key] = g
		}
		g.paths = append(g.paths, strings.Split(strings.Trim(path, "|"), "|"))
		g.scores = append(g.scores, score)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.tx.ExecContext(tx.ctx,
		`UPDATE hive__name_mention SET chosen = (candidate = 0) WHERE reference_id = ?`,
		referenceID); err != nil {
		return fmt.Errorf("core: reset homonym choices: %w", err)
	}
	for key, best := range chooseHomonyms(groups) {
		if _, err := tx.tx.ExecContext(tx.ctx, `
			UPDATE hive__name_mention SET chosen = (candidate = ?)
			WHERE reference_id = ? AND raw__source = ? AND mention = ?`,
			best, referenceID, key[0], key[1]); err != nil {
			return fmt.Errorf("core: choose homonym: %w", err)
		}
	}
	return nil
}

// chooseHomonyms picks a candidate lineage per group. A name whose
// authorship in the paper matches one lineage takes it, and one whose
// dated authorship matches none is rejected (noCandidate) unless the
// paper's other names support one of its lineages. Other
// names with several candidates take the lineage that shares the most
// with the paper's settled names: every taxon in a settled name's
// classification (unranked levels included) is weighted by the name's
// mention count, and a candidate scores the weights of the taxa in
// its own classification. A name is settled when it has one
// candidate or its authorship chose one. Only exact species-level
// matches and authorship-settled names give context while the paper
// has any: a genus or other single-word name can look unambiguous in
// a source that lacks its homonym, and most words mistaken for names
// are single words. Taxa every context name shares (such as
// Eukaryota) score only when no other taxon separates the candidates,
// since a candidate with a shorter classification would otherwise
// lose on them alone. Ties keep the verifier's order. It returns the
// choice for every group with several candidates or an authorship.
func chooseHomonyms[K comparable](groups map[K]*homonymGroup) map[K]int {
	chosen := map[K]int{}
	// A context is a settled name's path; strong marks species-level
	// or authorship evidence.
	type context struct {
		path   []string
		count  int
		strong bool
	}
	var contexts []context
	var disputed []K
	for key, g := range groups {
		if choice, ok := g.byAuthorship(); ok {
			if choice == noCandidate {
				disputed = append(disputed, key)
				continue
			}
			chosen[key] = choice
			contexts = append(contexts, context{g.paths[choice], g.count, true})
			continue
		}
		if len(g.paths) == 1 {
			contexts = append(contexts, context{g.paths[0], g.count, g.species})
		}
	}
	strongOnly := false
	for _, c := range contexts {
		if c.strong {
			strongOnly = true
			break
		}
	}
	weight := map[string]int{}
	total := 0
	for _, c := range contexts {
		if strongOnly && !c.strong {
			continue
		}
		total += c.count
		seen := map[string]bool{}
		for _, name := range c.path {
			if name != "" && !seen[name] {
				seen[name] = true
				weight[name] += c.count
			}
		}
	}
	informative := map[string]int{}
	for name, w := range weight {
		if w < total {
			informative[name] = w
		}
	}
	// A name whose authorship matches no lineage is rejected only when
	// the paper's other names give its lineages no support below the
	// top two levels (domain, kingdom) either; a higher taxon's
	// authority is often written differently.
	for _, key := range disputed {
		paths := make([][]string, len(groups[key].paths))
		for i, p := range groups[key].paths {
			if len(p) > 3 {
				p = p[2:]
			}
			paths[i] = p
		}
		if best, ok := bestCandidate(paths, weight); ok {
			chosen[key] = best
		} else {
			chosen[key] = noCandidate
		}
	}
	for key, g := range groups {
		if _, done := chosen[key]; done || len(g.paths) < 2 {
			continue
		}
		best, ok := bestCandidate(g.paths, informative)
		if !ok {
			best, _ = bestCandidate(g.paths, weight)
		}
		chosen[key] = best
	}
	return chosen
}

// bestCandidate returns the index of the highest-scoring path under
// weight, and whether that score is above zero.
func bestCandidate(paths [][]string, weight map[string]int) (int, bool) {
	best, bestScore := 0, 0
	for i, path := range paths {
		score := 0
		seen := map[string]bool{}
		for _, name := range path {
			if !seen[name] {
				seen[name] = true
				score += weight[name]
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best, bestScore > 0
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
	cols := []string{"col__id", "col__name_id", "col__parent_id", "raw__source", "raw__path"}
	parent := ""
	if len(m.Path) > 1 {
		parent = m.Path[len(m.Path)-2].ID
	}
	names := make([]string, len(m.Path))
	for i, r := range m.Path {
		names[i] = r.Name
	}
	args := []any{leaf.ID, leaf.ID, parent, source, "|" + strings.Join(names, "|") + "|"}
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

// NameMentionScan reads a reference's attached documents and returns
// their tagged names and authorships merged (see mentions.MergeScans),
// with the number of documents that had augmented JATS. read returns a
// document's augmented JATS, or nil when it has none.
func (a *Archive) NameMentionScan(ctx context.Context, referenceID string,
	read func(sha string) ([]byte, error)) (mentions.Scan, int, error) {
	shas, err := a.ReferencePDFs(ctx, referenceID)
	if err != nil {
		return mentions.Scan{}, 0, err
	}
	var scans []mentions.Scan
	for _, sha := range shas {
		jats, err := read(sha)
		if err != nil {
			return mentions.Scan{}, 0, err
		}
		if jats == nil {
			continue
		}
		s, err := mentions.ScanJATS(jats)
		if err != nil {
			return mentions.Scan{}, 0, fmt.Errorf("core: count mentions in %s: %w", sha, err)
		}
		scans = append(scans, s)
	}
	return mentions.MergeScans(scans...), len(scans), nil
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

// ColTaxa returns ranked higher taxa from one verifier source in the
// name-mention index whose names start with prefix (case-insensitive),
// most-mentioned first. They are values an [IndexParent] query for
// that source can match.
func (a *Archive) ColTaxa(ctx context.Context, sourceID int, prefix string, limit int) ([]ColTaxon, error) {
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
			WHERE m.raw__source LIKE ? AND t.`+col+` != '' AND `+discoveryMatch("m.")+`
			  AND LOWER(t.`+col+`) LIKE LOWER(?) ESCAPE '\'`)
		args = append(args, mentions.TagPrefix(sourceID)+"%", likePrefix(prefix))
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
