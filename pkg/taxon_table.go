package hive

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// The taxon table lists every name usage in the archive, accepted
// taxa and synonyms alike, one row each, with the classification of
// the accepted taxon and optional columns drawn from the supplemental
// tables (vernacular names, distribution, interactions, type material,
// name relations, media, properties, species estimates) and from
// validation issues. It backs the filterable
// table in the web interface, as the reference listing backs the
// reference table.

// TaxonTableQuery selects, filters and orders taxon table rows.
type TaxonTableQuery struct {
	// Q keeps names containing the text (case-insensitive).
	Q string
	// Usage keeps "accepted" or "synonym" rows; "" keeps both.
	Usage string
	// Ranks keeps names of these rank IDs; empty keeps all.
	Ranks []string
	// Issue keeps rows with open validation issues: "any", a severity
	// ("error", "warn", "info", "debug"), or "rule:<rule_id>"; "" keeps
	// all.
	Issue string
	// Within keeps the usages of this taxon and every taxon below it.
	Within string
	// Has and Missing keep rows that have, or lack, data of a kind
	// (see TaxonTableData). Taxon-level kinds apply to accepted rows.
	Has, Missing string
	Sort         []ReferenceSortKey
	// Limit is the page size (default 100, at most 1000); -1 returns
	// every row, without counting them separately.
	Limit int
	// DetailLimit bounds the values listed per supplemental cell; 0
	// means the table's default (20). Exports list everything.
	DetailLimit int
	// Offset skips that many rows of the ordered result.
	Offset int
	// Lineage lists the classification ranks to fill in, as lowercase
	// rank IDs of the archive's rank vocabulary; nil means
	// TaxonTableRanks.
	Lineage []string
}

// TaxonTableRow is one name usage.
type TaxonTableRow struct {
	NameID string
	// TaxonID is the taxon for accepted rows and the accepted taxon for
	// synonym rows.
	TaxonID    string
	SynonymID  string
	Usage      string // "accepted" | "synonym"
	Status     string // taxonomic status ID
	Name       string
	Authorship string
	Rank       string
	Code       string
	// Related is the parent's name for accepted rows and the accepted
	// name for synonym rows.
	Related        string
	Classification map[string]string // the query's Lineage ranks of the accepted taxon, itself included
	Extinct        *bool
	Modified       string

	// Supplemental data. For synonym rows, taxon-level data
	// (vernaculars, distribution, interactions) is left empty: it
	// belongs to the accepted taxon's row.
	Vernaculars   []string
	Distribution  []string
	Interactions  []string
	TypeMaterial  []string
	Media         []string
	Properties    []string
	Estimates     []string
	Synonyms      int
	NameRelations int

	IssuesBySeverity map[string]int
	IssueRules       []string
}

// TaxonTableData maps the kinds of supplemental data rows can be
// filtered on to the condition that the usage row u has some.
var TaxonTableData = map[string]string{
	"vernaculars":    "EXISTS (SELECT 1 FROM vernacular x WHERE x.col__taxon_id = u.taxon_id)",
	"distribution":   "EXISTS (SELECT 1 FROM distribution x WHERE x.col__taxon_id = u.taxon_id)",
	"interactions":   "EXISTS (SELECT 1 FROM species_interaction x WHERE x.col__taxon_id = u.taxon_id)",
	"media":          "EXISTS (SELECT 1 FROM media x WHERE x.col__taxon_id = u.taxon_id)",
	"properties":     "EXISTS (SELECT 1 FROM taxon_property x WHERE x.col__taxon_id = u.taxon_id)",
	"estimates":      "EXISTS (SELECT 1 FROM species_estimate x WHERE x.col__taxon_id = u.taxon_id)",
	"synonyms":       "EXISTS (SELECT 1 FROM synonym x WHERE x.col__taxon_id = u.taxon_id)",
	"type_material":  "EXISTS (SELECT 1 FROM type_material x WHERE x.col__name_id = u.name_id)",
	"name_relations": "EXISTS (SELECT 1 FROM name_relation x WHERE x.col__name_id = u.name_id OR x.col__related_name_id = u.name_id)",
}

// taxonLevelData are the kinds held on the taxon rather than the name;
// a synonym row neither has nor misses them.
var taxonLevelData = map[string]bool{
	"vernaculars": true, "distribution": true, "interactions": true, "media": true,
	"properties": true, "estimates": true, "synonyms": true,
}

// TaxonTableRanks are the classification ranks the table fills in
// unless the query lists others.
var TaxonTableRanks = []string{"kingdom", "phylum", "class", "order", "family", "genus"}

// denormalizedRanks are the ranks the taxon table has a stored
// classification column for (col__family, …).
var denormalizedRanks = map[string]bool{
	"species": true, "section": true, "subgenus": true, "genus": true, "subtribe": true,
	"tribe": true, "subfamily": true, "family": true, "superfamily": true, "suborder": true,
	"order": true, "subclass": true, "class": true, "subphylum": true, "phylum": true,
	"kingdom": true,
}

// rankColumnID matches what can be a rank column: a lowercase rank ID.
var rankColumnID = regexp.MustCompile(`^[a-z][a-z_]*$`)

// TaxonRank is a rank of the archive's vocabulary, offered as a
// classification column.
type TaxonRank struct {
	ID      string // lowercase rank ID, the column's id
	Name    string
	Linnean bool
	Taxa    int // accepted taxa of this rank in the archive
}

// TaxonRanks lists the archive's ranks in hierarchy order, with how
// many taxa each has.
func (a *Archive) TaxonRanks(ctx context.Context) ([]TaxonRank, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT LOWER(r.col__id), COALESCE(r.col__name, ''), COALESCE(r.col__linnean, 0), COALESCE(c.n, 0)
		FROM rank r
		LEFT JOIN (SELECT UPPER(n.col__rank_id) AS id, COUNT(*) AS n
			FROM taxon t JOIN name n ON n.col__id = t.col__name_id GROUP BY 1) c ON c.id = UPPER(r.col__id)
		WHERE r.col__id <> ''
		ORDER BY r.rowid`)
	if err != nil {
		return nil, fmt.Errorf("core: taxon ranks: %w", err)
	}
	defer rows.Close()
	var out []TaxonRank
	for rows.Next() {
		var r TaxonRank
		if err := rows.Scan(&r.ID, &r.Name, &r.Linnean, &r.Taxa); err != nil {
			return nil, err
		}
		if rankColumnID.MatchString(r.ID) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// lineageRanks checks ranks against the archive's vocabulary and
// returns them in hierarchy order, without repeats.
func (a *Archive) lineageRanks(ctx context.Context, ranks []string) ([]string, error) {
	if ranks == nil {
		return TaxonTableRanks, nil
	}
	known, err := a.TaxonRanks(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, r := range ranks {
		want[strings.ToLower(strings.TrimSpace(r))] = true
	}
	delete(want, "")
	var out []string
	for _, r := range known {
		if want[r.ID] {
			out = append(out, r.ID)
			delete(want, r.ID)
		}
	}
	for r := range want {
		return nil, fmt.Errorf("%w: unknown rank %q", ErrValidation, r)
	}
	return out, nil
}

// taxonUsages selects every name usage: one row per taxon and one per
// synonym, with the accepted taxon each belongs to.
const taxonUsages = `
	SELECT t.col__name_id AS name_id, t.col__id AS taxon_id, '' AS synonym_id,
		'accepted' AS usage, COALESCE(t.col__status_id, '') AS status,
		COALESCE(t.col__modified, '') AS modified, t.rowid AS ord
	FROM taxon t
	UNION ALL
	SELECT s.col__name_id, s.col__taxon_id, COALESCE(s.col__id, ''),
		'synonym', COALESCE(s.col__status_id, ''),
		COALESCE(s.col__modified, ''), -s.rowid
	FROM synonym s`

// issueMatch is the condition that a validation result belongs to the
// usage row u: issues on its name, on the taxon of an accepted row,
// and on the synonym record of a synonym row.
const issueMatch = `r.is_resolved = 0 AND (
		(r.table_name = 'name' AND r.record_id = u.name_id)
		OR (u.usage = 'accepted' AND r.table_name = 'taxon' AND r.record_id = u.taxon_id)
		OR (u.usage = 'synonym' AND r.table_name = 'synonym' AND r.record_id = u.synonym_id))`

// acceptedCount counts rows of a taxon-level table for accepted rows.
func acceptedCount(table string) string {
	return `CASE WHEN u.usage = 'accepted' THEN (SELECT COUNT(*) FROM ` + table +
		` x WHERE x.col__taxon_id = u.taxon_id) END`
}

// TaxonTableSortColumns maps sortable column names to SQL expressions
// over the usage row u, its name n and its accepted taxon at.
var TaxonTableSortColumns = map[string]string{
	"name":          "LOWER(n.col__scientific_name)",
	"authorship":    "LOWER(n.col__authorship)",
	"rank":          "LOWER(n.col__rank_id)",
	"usage":         "u.usage",
	"status":        "u.status",
	"code":          "n.col__code_id",
	"modified":      "u.modified",
	"vernaculars":   acceptedCount("vernacular"),
	"distribution":  acceptedCount("distribution"),
	"interactions":  acceptedCount("species_interaction"),
	"type_material": `(SELECT COUNT(*) FROM type_material x WHERE x.col__name_id = u.name_id)`,
	"synonyms": `CASE WHEN u.usage = 'accepted' THEN
		(SELECT COUNT(*) FROM synonym x WHERE x.col__taxon_id = u.taxon_id) END`,
	"name_relations": `(SELECT COUNT(*) FROM name_relation x
		WHERE x.col__name_id = u.name_id OR x.col__related_name_id = u.name_id)`,
	"media":      acceptedCount("media"),
	"properties": acceptedCount("taxon_property"),
	"estimates":  acceptedCount("species_estimate"),
	// Worst open severity (1 = error … 4 = debug), so ascending puts
	// the most serious first.
	"issues": `(SELECT MIN(CASE r.severity WHEN 'error' THEN 1 WHEN 'warn' THEN 2
		WHEN 'info' THEN 3 ELSE 4 END) FROM __gsvalidator_results r WHERE ` + issueMatch + `)`,
	"issue_rules": `(SELECT MIN(r.rule_name) FROM __gsvalidator_results r WHERE ` + issueMatch + `)`,
}

// lineageTable defines cls: for every taxon, the names at ranks in its
// classification (itself included), from one walk of the parent
// chains. Column c_<rank> holds each rank's name.
func lineageTable(ranks []string) string {
	cols := make([]string, len(ranks))
	in := make([]string, len(ranks))
	for i, r := range ranks {
		up := strings.ToUpper(r)
		cols[i] = `MAX(CASE WHEN UPPER(nn.col__rank_id) = '` + up +
			`' THEN COALESCE(NULLIF(nn.gn__canonical_simple, ''), nn.col__scientific_name) END) AS "c_` + r + `"`
		in[i] = `'` + up + `'`
	}
	return `WITH RECURSIVE up(start, id, d) AS (
		SELECT col__id, col__id, 0 FROM taxon
		UNION ALL
		SELECT up.start, p.col__parent_id, up.d + 1 FROM up JOIN taxon p ON p.col__id = up.id
		WHERE COALESCE(p.col__parent_id, '') <> '' AND up.d < 60),
	cls AS MATERIALIZED (SELECT up.start AS taxon_id, ` + strings.Join(cols, ", ") + `
		FROM up JOIN taxon x ON x.col__id = up.id JOIN name nn ON nn.col__id = x.col__name_id
		WHERE UPPER(nn.col__rank_id) IN (` + strings.Join(in, ", ") + `)
		GROUP BY up.start)
	`
}

// lineageRank selects the name at rank in the classification of the
// usage row's accepted taxon: the taxon itself or its nearest ancestor
// of that rank, found by walking the parent chain. The stored
// denormalized column (col__family, …), which importers may fill but
// hive does not maintain, is the fallback when the chain has no taxon
// of that rank.
func lineageRank(rank string) string {
	return `COALESCE((WITH RECURSIVE up(id, d) AS (
			SELECT u.taxon_id, 0
			UNION ALL
			SELECT p.col__parent_id, up.d + 1 FROM up JOIN taxon p ON p.col__id = up.id
			WHERE COALESCE(p.col__parent_id, '') <> '' AND up.d < 60)
		SELECT COALESCE(NULLIF(nn.gn__canonical_simple, ''), nn.col__scientific_name)
		FROM up JOIN taxon x ON x.col__id = up.id JOIN name nn ON nn.col__id = x.col__name_id
		WHERE UPPER(nn.col__rank_id) = '` + strings.ToUpper(rank) + `'
		ORDER BY up.d LIMIT 1)` + denormalized(rank) + `)`
}

// denormalized is the stored classification column to fall back on for
// rank, as ", NULLIF(at.col__<rank>, ”)", or "" when there is none.
func denormalized(rank string) string {
	if !denormalizedRanks[rank] {
		return ", NULL"
	}
	return `, NULLIF(at.col__` + rank + `, '')`
}

// ParseTaxonTableSort parses "col:asc,col:desc" against the taxon
// table's columns. Any lowercase rank ID is accepted as a
// classification column; TaxonTable checks it against the archive's
// ranks.
func ParseTaxonTableSort(raw string) ([]ReferenceSortKey, error) {
	columns := maps.Clone(TaxonTableSortColumns)
	for _, part := range strings.Split(raw, ",") {
		col, _, _ := strings.Cut(strings.TrimSpace(part), ":")
		if _, ok := columns[col]; !ok && rankColumnID.MatchString(col) {
			columns[col] = ""
		}
	}
	return parseSortKeys(raw, columns)
}

// predicate returns the WHERE condition over u, n and at.
func (q TaxonTableQuery) predicate() (string, []any) {
	var conds []string
	var args []any
	if s := strings.TrimSpace(q.Q); s != "" {
		conds = append(conds, "n.col__scientific_name LIKE ? ESCAPE '\\'")
		args = append(args, "%"+likeEscape(s)+"%")
	}
	switch q.Usage {
	case "accepted", "synonym":
		conds = append(conds, "u.usage = ?")
		args = append(args, q.Usage)
	}
	if len(q.Ranks) > 0 {
		in, a := inClause(q.Ranks)
		conds = append(conds, "UPPER(n.col__rank_id) IN ("+in+")")
		for i := range a {
			a[i] = strings.ToUpper(a[i].(string))
		}
		args = append(args, a...)
	}
	switch issue := q.Issue; {
	case issue == "":
	case issue == "any":
		conds = append(conds, "EXISTS (SELECT 1 FROM __gsvalidator_results r WHERE "+issueMatch+")")
	case strings.HasPrefix(issue, "rule:"):
		conds = append(conds, "EXISTS (SELECT 1 FROM __gsvalidator_results r WHERE "+issueMatch+" AND r.rule_id = ?)")
		args = append(args, strings.TrimPrefix(issue, "rule:"))
	default:
		conds = append(conds, "EXISTS (SELECT 1 FROM __gsvalidator_results r WHERE "+issueMatch+" AND r.severity = ?)")
		args = append(args, issue)
	}
	if q.Within != "" {
		conds = append(conds, `u.taxon_id IN (
			WITH RECURSIVE below(id) AS (
				SELECT ? UNION SELECT t.col__id FROM taxon t JOIN below ON t.col__parent_id = below.id)
			SELECT id FROM below)`)
		args = append(args, q.Within)
	}
	for kind, negate := range map[string]bool{q.Has: false, q.Missing: true} {
		cond, ok := TaxonTableData[kind]
		if !ok {
			continue
		}
		if negate {
			cond = "NOT " + cond
		}
		if taxonLevelData[kind] {
			cond = "u.usage = 'accepted' AND " + cond
		}
		conds = append(conds, "("+cond+")")
	}
	if len(conds) == 0 {
		return "1", nil
	}
	return strings.Join(conds, " AND "), args
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// TaxonTable returns a page of the taxon table and the number of rows
// the query matches.
func (a *Archive) TaxonTable(ctx context.Context, q TaxonTableQuery) ([]TaxonTableRow, int, error) {
	where, args := q.predicate()
	from := `FROM (` + taxonUsages + `) u
		JOIN name n ON n.col__id = u.name_id
		LEFT JOIN taxon at ON at.col__id = u.taxon_id`

	all := q.Limit < 0
	var total int
	if !all {
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) `+from+` WHERE `+where, args...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("core: count taxon table: %w", err)
		}
	}

	lineage, err := a.lineageRanks(ctx, q.Lineage)
	if err != nil {
		return nil, 0, err
	}
	// Sorting by a classification rank needs every row's lineage: walk
	// all taxa once into a materialized table rather than per row.
	var sortRanks []string
	for _, k := range q.Sort {
		if _, ok := TaxonTableSortColumns[k.Column]; !ok {
			sortRanks = append(sortRanks, k.Column)
		}
	}
	if sortRanks, err = a.lineageRanks(ctx, sortRanks); err != nil {
		return nil, 0, err
	}
	prefix, join := "", ""
	if len(sortRanks) > 0 {
		prefix, join = lineageTable(sortRanks), ` LEFT JOIN cls ON cls.taxon_id = u.taxon_id`
	}
	order := make([]string, 0, len(q.Sort)+2)
	for _, k := range q.Sort {
		expr, ok := TaxonTableSortColumns[k.Column]
		if !ok {
			expr = `LOWER(COALESCE(cls."c_` + k.Column + `"` + denormalized(k.Column) + `))`
		}
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		// Empty values sort last in either direction.
		order = append(order, "NULLIF("+expr+", '') "+dir+" NULLS LAST")
	}
	order = append(order, "LOWER(n.col__scientific_name)", "u.ord")
	limit := q.Limit
	if all {
		limit = -1 // SQLite: no limit
	} else if limit <= 0 || limit > 1000 {
		limit = 100
	}
	cols := []string{
		"u.name_id", "u.taxon_id", "u.synonym_id", "u.usage", "u.status", "u.modified",
		"COALESCE(n.col__scientific_name, '')", "COALESCE(n.col__authorship, '')",
		"COALESCE(n.col__rank_id, '')", "COALESCE(n.col__code_id, '')",
		"at.col__extinct",
		// Parent of an accepted row, accepted name of a synonym row.
		`CASE WHEN u.usage = 'accepted' THEN
			(SELECT pn.col__scientific_name FROM taxon p JOIN name pn ON pn.col__id = p.col__name_id
			 WHERE p.col__id = at.col__parent_id)
		ELSE (SELECT an.col__scientific_name FROM name an WHERE an.col__id = at.col__name_id) END`,
	}
	for _, r := range lineage {
		cols = append(cols, "COALESCE("+lineageRank(r)+", '')")
	}
	rows, err := a.db.QueryContext(ctx, prefix+`SELECT `+strings.Join(cols, ", ")+` `+from+join+
		` WHERE `+where+` ORDER BY `+strings.Join(order, ", ")+` LIMIT ? OFFSET ?`,
		append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("core: taxon table: %w", err)
	}
	var out []TaxonTableRow
	for rows.Next() {
		var r TaxonTableRow
		var extinct sql.NullBool
		var related sql.NullString
		ranks := make([]string, len(lineage))
		dest := []any{&r.NameID, &r.TaxonID, &r.SynonymID, &r.Usage, &r.Status, &r.Modified,
			&r.Name, &r.Authorship, &r.Rank, &r.Code, &extinct, &related}
		for i := range ranks {
			dest = append(dest, &ranks[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if extinct.Valid {
			r.Extinct = &extinct.Bool
		}
		r.Related = related.String
		r.Classification = map[string]string{}
		for i, rank := range lineage {
			if ranks[i] != "" {
				r.Classification[rank] = ranks[i]
			}
		}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// Supplemental details in chunks: one IN list per query.
	for start := 0; start < len(out); start += 500 {
		if err := a.addTaxonTableDetails(ctx, out[start:min(start+500, len(out))], q.DetailLimit); err != nil {
			return nil, 0, err
		}
	}
	if all {
		total = len(out)
	}
	return out, total, nil
}

// detailLimit bounds the distinct values listed per supplemental cell;
// the counts used for sorting are exact.
const detailLimit = 20

// addTaxonTableDetails fills the supplemental lists, counts and issues
// of a page of rows.
func (a *Archive) addTaxonTableDetails(ctx context.Context, rows []TaxonTableRow, limit int) error {
	if len(rows) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = detailLimit
	}
	byTaxon := map[string][]*TaxonTableRow{}
	byName := map[string][]*TaxonTableRow{}
	var taxonIDs, nameIDs []string
	for i := range rows {
		r := &rows[i]
		if r.Usage == "accepted" {
			if byTaxon[r.TaxonID] == nil {
				taxonIDs = append(taxonIDs, r.TaxonID)
			}
			byTaxon[r.TaxonID] = append(byTaxon[r.TaxonID], r)
		}
		if byName[r.NameID] == nil {
			nameIDs = append(nameIDs, r.NameID)
		}
		byName[r.NameID] = append(byName[r.NameID], r)
	}
	// list runs a query returning (key, value) and appends each value
	// to the rows the key selects.
	list := func(query string, ids []string, rowsOf map[string][]*TaxonTableRow, add func(*TaxonTableRow, string)) error {
		if len(ids) == 0 {
			return nil
		}
		in, ids1 := inClause(ids)
		// The ID list may appear more than once in the query.
		var args []any
		for range strings.Count(query, "(?)") {
			args = append(args, ids1...)
		}
		res, err := a.db.QueryContext(ctx, strings.ReplaceAll(query, "(?)", "("+in+")"), args...)
		if err != nil {
			return fmt.Errorf("core: taxon table details: %w", err)
		}
		defer res.Close()
		for res.Next() {
			var key, value string
			if err := res.Scan(&key, &value); err != nil {
				return err
			}
			for _, r := range rowsOf[key] {
				add(r, value)
			}
		}
		return res.Err()
	}
	appendCapped := func(dst *[]string, v string) {
		if v != "" && len(*dst) < limit {
			*dst = append(*dst, v)
		}
	}
	steps := []error{
		list(`SELECT DISTINCT col__taxon_id, col__name || CASE WHEN COALESCE(col__language, '') <> ''
				THEN ' (' || col__language || ')' ELSE '' END
			FROM vernacular WHERE col__taxon_id IN (?)
			ORDER BY col__preferred DESC, col__language, col__name`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { appendCapped(&r.Vernaculars, v) }),
		list(`SELECT DISTINCT col__taxon_id, COALESCE(NULLIF(col__area, ''), col__area_id)
			FROM distribution WHERE col__taxon_id IN (?) ORDER BY 2`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { appendCapped(&r.Distribution, v) }),
		list(`SELECT DISTINCT i.col__taxon_id,
				COALESCE(NULLIF(lower(i.col__type_id), ''), 'related to') || ' ' ||
				COALESCE(NULLIF(i.col__related_taxon_scientific_name, ''),
					(SELECT n.col__scientific_name FROM taxon t JOIN name n ON n.col__id = t.col__name_id
					 WHERE t.col__id = i.col__related_taxon_id), '')
			FROM species_interaction i WHERE i.col__taxon_id IN (?) ORDER BY 2`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { appendCapped(&r.Interactions, v) }),
		list(`SELECT DISTINCT col__name_id, TRIM(COALESCE(lower(col__status_id), 'type') || ' ' ||
				COALESCE(NULLIF(col__institution_code, ''), '') || ' ' || COALESCE(col__catalog_number, ''))
			FROM type_material WHERE col__name_id IN (?)`,
			nameIDs, byName, func(r *TaxonTableRow, v string) { appendCapped(&r.TypeMaterial, v) }),
		list(`SELECT DISTINCT col__taxon_id, COALESCE(NULLIF(col__title, ''), col__url)
			FROM media WHERE col__taxon_id IN (?) ORDER BY 2`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { appendCapped(&r.Media, v) }),
		list(`SELECT DISTINCT col__taxon_id, col__property || ': ' || COALESCE(col__value, '')
			FROM taxon_property WHERE col__taxon_id IN (?) ORDER BY col__ordinal, col__property`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { appendCapped(&r.Properties, v) }),
		list(`SELECT DISTINCT col__taxon_id, CAST(col__estimate AS TEXT) ||
				CASE WHEN COALESCE(col__type_id, '') <> '' THEN ' ' || lower(replace(col__type_id, '_', ' ')) ELSE '' END
			FROM species_estimate WHERE col__taxon_id IN (?)`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { appendCapped(&r.Estimates, v) }),
		list(`SELECT col__taxon_id, CAST(COUNT(*) AS TEXT) FROM synonym
			WHERE col__taxon_id IN (?) GROUP BY 1`,
			taxonIDs, byTaxon, func(r *TaxonTableRow, v string) { fmt.Sscan(v, &r.Synonyms) }),
		list(`SELECT id, CAST(COUNT(*) AS TEXT) FROM (
				SELECT col__name_id AS id FROM name_relation WHERE col__name_id IN (?)
				UNION ALL
				SELECT col__related_name_id FROM name_relation WHERE col__related_name_id IN (?))
			GROUP BY 1`,
			nameIDs, byName, func(r *TaxonTableRow, v string) { fmt.Sscan(v, &r.NameRelations) }),
	}
	for _, err := range steps {
		if err != nil {
			return err
		}
	}
	return a.addTaxonTableIssues(ctx, rows, limit)
}

// addTaxonTableIssues fills each row's open issues: counts per
// severity and the rule names, most serious first.
func (a *Archive) addTaxonTableIssues(ctx context.Context, rows []TaxonTableRow, limit int) error {
	type key struct{ table, id string }
	rowsOf := map[key][]*TaxonTableRow{}
	var ids []string
	add := func(table, id string, r *TaxonTableRow) {
		if id == "" {
			return
		}
		k := key{table, id}
		if rowsOf[k] == nil {
			ids = append(ids, id)
		}
		rowsOf[k] = append(rowsOf[k], r)
	}
	for i := range rows {
		r := &rows[i]
		add("name", r.NameID, r)
		if r.Usage == "accepted" {
			add("taxon", r.TaxonID, r)
		} else {
			add("synonym", r.SynonymID, r)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	in, args := inClause(ids)
	res, err := a.db.QueryContext(ctx, `
		SELECT table_name, record_id, severity, rule_name FROM __gsvalidator_results
		WHERE is_resolved = 0 AND table_name IN ('name', 'taxon', 'synonym')
			AND record_id IN (`+in+`)
		ORDER BY CASE severity WHEN 'error' THEN 1 WHEN 'warn' THEN 2 WHEN 'info' THEN 3 ELSE 4 END,
			rule_name`, args...)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return fmt.Errorf("core: taxon table issues: %w", err)
	}
	defer res.Close()
	for res.Next() {
		var table, id, severity, rule string
		if err := res.Scan(&table, &id, &severity, &rule); err != nil {
			return err
		}
		for _, r := range rowsOf[key{table, id}] {
			if r.IssuesBySeverity == nil {
				r.IssuesBySeverity = map[string]int{}
			}
			r.IssuesBySeverity[severity]++
			if len(r.IssueRules) < limit && !slices.Contains(r.IssueRules, rule) {
				r.IssueRules = append(r.IssueRules, rule)
			}
		}
	}
	return res.Err()
}

// IssueRule is a validation rule with open issues on names, taxa or
// synonyms.
type IssueRule struct {
	ID, Name, Severity string
	Count              int
}

// TaxonIssueRules lists the rules with open issues on names, taxa or
// synonyms, most frequent first.
func (a *Archive) TaxonIssueRules(ctx context.Context) ([]IssueRule, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT rule_id, MAX(rule_name), MAX(severity), COUNT(*) FROM __gsvalidator_results
		WHERE is_resolved = 0 AND table_name IN ('name', 'taxon', 'synonym')
		GROUP BY rule_id ORDER BY 4 DESC, 2`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("core: taxon issue rules: %w", err)
	}
	defer rows.Close()
	var out []IssueRule
	for rows.Next() {
		var r IssueRule
		if err := rows.Scan(&r.ID, &r.Name, &r.Severity, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
