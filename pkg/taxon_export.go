package hive

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ExportTaxaSFGA writes the taxon table rows to a new SFGA archive at
// path: their names, taxa (with every ancestor, so the classification
// stays whole) and synonyms, the supplemental records (vernacular
// names, distribution, interactions, type material, name relations,
// media, properties, species estimates, treatments, concept relations,
// name matches), the references those records
// cite, the vocabularies, the metadata and the validation results on
// the copied records (resolved ones included) unless validation is
// false. The rows that matched are
// listed in export_scope, and the view taxon_table_view shows them as
// the flat table, computed from the related tables.
func (a *Archive) ExportTaxaSFGA(ctx context.Context, path string, rows []TaxonTableRow, validation bool, info map[string]string) error {
	db, err := sql.Open(sqliteDriver, "file:"+path+"?mode=rwc")
	if err != nil {
		return err
	}
	defer db.Close()
	// One connection: ATTACH and TEMP tables are per connection.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("core: export schema: %w", err)
	}
	// Rows are copied table by table, so parents may arrive after their
	// children; the source archive already holds them consistently.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS src`, "file:"+a.path+"?mode=ro"); err != nil {
		return fmt.Errorf("core: export attach: %w", err)
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE src`)
	exec := func(q string, args ...any) error {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("core: export: %w\n%s", err, q)
		}
		return nil
	}

	// The matched rows, then the taxa and names they reach.
	if err := exec(`CREATE TABLE export_scope (
		name_id TEXT NOT NULL, taxon_id TEXT, synonym_id TEXT, usage TEXT NOT NULL, ordinal INTEGER NOT NULL)`); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO export_scope VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i, r := range rows {
		if _, err := ins.ExecContext(ctx, r.NameID, r.TaxonID, r.SynonymID, r.Usage, i+1); err != nil {
			ins.Close()
			tx.Rollback()
			return err
		}
	}
	ins.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	steps := []string{
		`CREATE TEMP TABLE sc_taxon (id TEXT PRIMARY KEY)`,
		`CREATE TEMP TABLE sc_name (id TEXT PRIMARY KEY)`,
		`CREATE TEMP TABLE sc_ref (id TEXT PRIMARY KEY)`,
		`INSERT OR IGNORE INTO sc_taxon
			WITH RECURSIVE up(id) AS (
				SELECT taxon_id FROM export_scope WHERE COALESCE(taxon_id, '') <> ''
				UNION
				SELECT t.col__parent_id FROM src.taxon t JOIN up ON t.col__id = up.id
				WHERE COALESCE(t.col__parent_id, '') <> '')
			SELECT id FROM up`,
		`INSERT OR IGNORE INTO sc_name SELECT col__name_id FROM src.taxon WHERE col__id IN (SELECT id FROM sc_taxon)`,
		`INSERT OR IGNORE INTO sc_name SELECT col__name_id FROM src.synonym WHERE col__taxon_id IN (SELECT id FROM sc_taxon)`,
		`INSERT OR IGNORE INTO sc_name SELECT name_id FROM export_scope`,
		`INSERT OR IGNORE INTO sc_name SELECT col__related_name_id FROM src.name_relation
			WHERE col__name_id IN (SELECT id FROM sc_name) AND COALESCE(col__related_name_id, '') <> ''`,
	}
	for _, q := range steps {
		if err := exec(q); err != nil {
			return err
		}
	}

	byTaxon := "col__taxon_id IN (SELECT id FROM sc_taxon)"
	byName := "col__name_id IN (SELECT id FROM sc_name)"
	data := []struct{ table, where string }{
		{"name", "col__id IN (SELECT id FROM sc_name)"},
		{"taxon", "col__id IN (SELECT id FROM sc_taxon)"},
		{"synonym", byTaxon},
		{"vernacular", byTaxon},
		{"distribution", byTaxon},
		{"species_interaction", byTaxon},
		{"media", byTaxon},
		{"taxon_property", byTaxon},
		{"species_estimate", byTaxon},
		{"treatment", byTaxon},
		{"taxon_concept_relation", byTaxon},
		{"type_material", byName},
		{"name_relation", byName},
		{"name_match", byName},
	}
	for _, d := range data {
		if err := a.copyExportTable(ctx, db, d.table, d.where); err != nil {
			return err
		}
	}
	// References the copied records cite.
	for _, d := range data {
		for _, col := range []string{"col__reference_id", "col__according_to_id"} {
			if ok, _ := columnIn(ctx, db, "main", d.table, col); !ok {
				continue
			}
			if err := exec(`INSERT OR IGNORE INTO sc_ref SELECT ` + col + ` FROM main.` + d.table +
				` WHERE COALESCE(` + col + `, '') <> ''`); err != nil {
				return err
			}
		}
	}
	if err := a.copyExportTable(ctx, db, "reference", "col__id IN (SELECT id FROM sc_ref)"); err != nil {
		return err
	}
	if err := exec(`DELETE FROM main.metadata`); err != nil {
		return err
	}
	if err := a.copyExportTable(ctx, db, "metadata", "1"); err != nil {
		return err
	}
	// Vocabularies: terms the archive added to the seeded ones.
	copied := []string{"reference", "metadata"}
	for _, d := range data {
		copied = append(copied, d.table)
	}
	vocab, err := sharedTables(ctx, db)
	if err != nil {
		return err
	}
	for _, t := range vocab {
		if slices.Contains(copied, t) || t == "version" {
			continue
		}
		if err := a.copyExportTableOr(ctx, db, t, "1", "OR IGNORE"); err != nil {
			return err
		}
	}

	issues := false
	if validation {
		if issues, err = a.copyExportIssues(ctx, db, copied); err != nil {
			return err
		}
	}
	if err := exec(`CREATE VIEW taxon_table_view AS ` + taxonTableViewSQL(issues)); err != nil {
		return err
	}
	if err := exec(`CREATE TABLE export_info (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return err
	}
	info["format"] = "SFGA archive with the matched rows (export_scope) and taxon_table_view"
	info["exported_at"] = time.Now().UTC().Format(time.RFC3339)
	for k, v := range info {
		if err := exec(`INSERT INTO export_info VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	return nil
}

// validatorTables hold the validation results and when each rule last
// ran.
var validatorTables = []string{"__gsvalidator_results", "__gsvalidator_rule_state"}

// copyExportIssues creates the validator tables in the export, as the
// source defines them, and copies the results on records the export
// holds. It reports false when the source has never been validated.
func (a *Archive) copyExportIssues(ctx context.Context, db *sql.DB, tables []string) (bool, error) {
	for _, t := range validatorTables {
		rows, err := db.QueryContext(ctx, `SELECT sql FROM src.sqlite_master
			WHERE tbl_name = ? AND sql IS NOT NULL ORDER BY type = 'index'`, t)
		if err != nil {
			return false, err
		}
		var ddl []string
		for rows.Next() {
			var q string
			if err := rows.Scan(&q); err != nil {
				rows.Close()
				return false, err
			}
			ddl = append(ddl, q)
		}
		rows.Close()
		if len(ddl) == 0 {
			return false, nil
		}
		for _, q := range ddl {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return false, fmt.Errorf("core: export %s: %w", t, err)
			}
		}
	}
	if err := a.copyExportTable(ctx, db, "__gsvalidator_rule_state", "1"); err != nil {
		return false, err
	}
	for _, t := range tables {
		if ok, _ := columnIn(ctx, db, "main", t, "col__id"); !ok {
			continue
		}
		where := `table_name = '` + t + `' AND record_id IN (SELECT col__id FROM main."` + t + `")`
		if err := a.copyExportTable(ctx, db, "__gsvalidator_results", where); err != nil {
			return false, err
		}
	}
	return true, nil
}

// copyExportTable copies src rows of table matching where into the
// export, over the columns both copies have.
func (a *Archive) copyExportTable(ctx context.Context, db *sql.DB, table, where string) error {
	return a.copyExportTableOr(ctx, db, table, where, "")
}

func (a *Archive) copyExportTableOr(ctx context.Context, db *sql.DB, table, where, conflict string) error {
	main, err := schemaColumns(ctx, db, "main", table)
	if err != nil {
		return err
	}
	src, err := schemaColumns(ctx, db, "src", table)
	if err != nil {
		return err
	}
	var cols []string
	for _, c := range main {
		if slices.Contains(src, c) {
			cols = append(cols, `"`+c+`"`)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	list := strings.Join(cols, ", ")
	q := `INSERT ` + conflict + ` INTO main."` + table + `" (` + list + `) SELECT ` + list +
		` FROM src."` + table + `" WHERE ` + where
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("core: export copy %s: %w", table, err)
	}
	return nil
}

func schemaColumns(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func columnIn(ctx context.Context, db *sql.DB, schema, table, col string) (bool, error) {
	cols, err := schemaColumns(ctx, db, schema, table)
	return slices.Contains(cols, col), err
}

// sharedTables lists the SFGA tables both the export and the source
// have.
func sharedTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.name FROM main.sqlite_master m
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name <> 'export_scope'
			AND m.name IN (SELECT name FROM src.sqlite_master WHERE type = 'table')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// taxonTableViewSQL is the query behind taxon_table_view: the matched
// usages of export_scope with the taxon table's columns, computed from
// the related tables. Lists are joined with "; ".
func taxonTableViewSQL(issues bool) string {
	acc := func(expr string) string { return `CASE WHEN u.usage = 'accepted' THEN (` + expr + `) END` }
	cols := []string{
		`u.name_id`, `u.taxon_id`, `NULLIF(u.synonym_id, '') AS synonym_id`,
		`n.col__scientific_name AS name`, `n.col__authorship AS authorship`,
		`lower(n.col__rank_id) AS rank`, `u.usage`,
		`CASE WHEN u.usage = 'accepted' THEN
			(SELECT pn.col__scientific_name FROM taxon p JOIN name pn ON pn.col__id = p.col__name_id
			 WHERE p.col__id = at.col__parent_id)
		ELSE (SELECT an.col__scientific_name FROM name an WHERE an.col__id = at.col__name_id) END
			AS parent_or_accepted`,
		`lower(n.col__code_id) AS code`,
	}
	for _, r := range TaxonTableRanks {
		cols = append(cols, lineageRank(r)+` AS "`+r+`"`)
	}
	cols = append(cols,
		acc(`SELECT group_concat(v.col__name || COALESCE(' (' || NULLIF(v.col__language, '') || ')', ''), '; ')
			FROM vernacular v WHERE v.col__taxon_id = u.taxon_id`)+` AS vernaculars`,
		acc(`SELECT group_concat(COALESCE(NULLIF(d.col__area, ''), d.col__area_id), '; ')
			FROM distribution d WHERE d.col__taxon_id = u.taxon_id`)+` AS distribution`,
		acc(`SELECT group_concat(COALESCE(lower(i.col__type_id), 'related to') || ' ' ||
				COALESCE(NULLIF(i.col__related_taxon_scientific_name, ''), ''), '; ')
			FROM species_interaction i WHERE i.col__taxon_id = u.taxon_id`)+` AS interactions`,
		`(SELECT group_concat(TRIM(COALESCE(lower(tm.col__status_id), 'type') || ' ' ||
				COALESCE(tm.col__institution_code, '') || ' ' || COALESCE(tm.col__catalog_number, '')), '; ')
			FROM type_material tm WHERE tm.col__name_id = u.name_id) AS type_material`,
		acc(`SELECT COUNT(*) FROM synonym s WHERE s.col__taxon_id = u.taxon_id`)+` AS synonyms`,
		`(SELECT COUNT(*) FROM name_relation nr
			WHERE nr.col__name_id = u.name_id OR nr.col__related_name_id = u.name_id) AS name_relations`,
	)
	if issues {
		// Open results on the row's name and on its taxon or synonym
		// record, as the taxon table counts them.
		open := `FROM __gsvalidator_results r WHERE r.is_resolved = 0 AND (
				(r.table_name = 'name' AND r.record_id = u.name_id)
				OR (u.usage = 'accepted' AND r.table_name = 'taxon' AND r.record_id = u.taxon_id)
				OR (u.usage <> 'accepted' AND r.table_name = 'synonym' AND r.record_id = u.synonym_id))`
		cols = append(cols,
			`(SELECT group_concat(severity || ' ' || n, '; ') FROM (SELECT r.severity, COUNT(*) AS n `+open+`
				GROUP BY r.severity ORDER BY CASE r.severity WHEN 'error' THEN 1 WHEN 'warn' THEN 2
					WHEN 'info' THEN 3 ELSE 4 END)) AS issues`,
			`(SELECT group_concat(rule_name, '; ') FROM (SELECT DISTINCT r.rule_name `+open+`
				ORDER BY r.rule_name)) AS issue_rules`,
		)
	}
	return `SELECT ` + strings.Join(cols, ",\n\t") + `
FROM export_scope u
JOIN name n ON n.col__id = u.name_id
LEFT JOIN taxon at ON at.col__id = u.taxon_id
ORDER BY u.ordinal`
}
