package hive

import (
	"context"
	"fmt"
	"strings"
)

// twExtensionDDL adds the TaxonWorks round-trip columns and tables to an
// sfga archive: the shape a TaxonWorks project needs to be written to
// SFGA and read back without loss. It mirrors the additions the
// TaxonWorks SFGA exporter makes (lib/sfga/export.rb there) and is a
// proposal against the sfga schema, not part of it.
//
// Everything is additive and tw__ prefixed, so a reader that ignores
// these sees an ordinary archive.
var twExtensionDDL = []string{
	// A name's status in TaxonWorks is its classifications, of which it
	// can have several; col__status_id holds one.
	`ALTER TABLE name ADD COLUMN tw__nomenclatural_classification TEXT DEFAULT ''`,
	// Protonym or Combination.
	`ALTER TABLE name ADD COLUMN tw__name_class TEXT DEFAULT ''`,
	`ALTER TABLE name ADD COLUMN tw__verbatim_name TEXT DEFAULT ''`,
	`ALTER TABLE name ADD COLUMN tw__verbatim_author TEXT DEFAULT ''`,
	`ALTER TABLE name ADD COLUMN tw__author_ids TEXT DEFAULT ''`,
	`ALTER TABLE name ADD COLUMN tw__year_of_publication TEXT DEFAULT ''`,
	`ALTER TABLE distribution ADD COLUMN tw__data_origin TEXT DEFAULT ''`,
	`ALTER TABLE distribution ADD COLUMN tw__parent_area TEXT DEFAULT ''`,
	`ALTER TABLE species_interaction ADD COLUMN tw__biological_relationship TEXT DEFAULT ''`,
	`ALTER TABLE species_interaction ADD COLUMN tw__biological_relationship_uri TEXT DEFAULT ''`,
	`ALTER TABLE species_interaction ADD COLUMN tw__related_otu_id TEXT DEFAULT ''`,
	`ALTER TABLE synonym ADD COLUMN tw__otu_id TEXT DEFAULT ''`,
	`ALTER TABLE type_material ADD COLUMN tw__biocuration TEXT DEFAULT ''`,
	// The exact status word, which TaxonWorks writes in the plural for a
	// lot, and the lot size.
	`ALTER TABLE type_material ADD COLUMN tw__type_type TEXT DEFAULT ''`,
	`ALTER TABLE type_material ADD COLUMN tw__total TEXT DEFAULT ''`,
	// The vocabulary annotations point at, and the annotations
	// themselves: tags, notes, confidences, data attributes, alternate
	// values and identifiers share one shape.
	`CREATE TABLE IF NOT EXISTS tw__term (
		id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT DEFAULT '', short_name TEXT DEFAULT '',
		uri TEXT DEFAULT '', definition TEXT DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS tw__annotation (
		object_table TEXT NOT NULL, object_type TEXT NOT NULL, object_id TEXT NOT NULL,
		object_attribute TEXT DEFAULT '', kind TEXT NOT NULL, type TEXT DEFAULT '',
		term_id TEXT DEFAULT '', term TEXT DEFAULT '', value TEXT DEFAULT '', language TEXT DEFAULT ''
	)`,
	// The OTUs a name has, where SFGA keeps one taxon row per name.
	`CREATE TABLE IF NOT EXISTS tw__otu (
		id TEXT PRIMARY KEY, name TEXT DEFAULT '', name_id TEXT DEFAULT ''
	)`,
}

// twExtensionRowID are the tables whose rows carry no identity of their
// own in sfga, and the annotation table cannot point at.
var twExtensionRowID = []string{"distribution", "vernacular", "taxon_property", "species_interaction",
	"name_relation"}

// twExtensionReferences are the tables CoLDP allows one reference each,
// which TaxonWorks cites more than once.
var twExtensionReferences = []string{"name", "name_relation", "distribution", "vernacular", "taxon_property",
	"species_interaction", "type_material"}

// twExtensionOTU are the tables whose rows return to the OTU they came
// from.
var twExtensionOTU = []string{"distribution", "vernacular", "taxon_property", "species_interaction"}

// ApplyTaxonWorksExtension adds the TaxonWorks round-trip columns and
// tables to an open archive. The tw_ext rule bundles need them and are
// skipped without them (see SkippedRulesets), so an archive gains those
// rules only after this has run and the archive has been reopened —
// bundles are loaded at Open.
//
// Idempotent: a column or table already present is left alone. Used by
// the rule fixtures and by tools/mkdemo; a real archive gets these
// columns from the TaxonWorks exporter that writes it.
func ApplyTaxonWorksExtension(ctx context.Context, a *Archive) error {
	if a.readOnly {
		return ErrReadOnly
	}
	have, err := a.tableColumns(ctx)
	if err != nil {
		return err
	}
	stmts := append([]string{}, twExtensionDDL...)
	for _, t := range twExtensionRowID {
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN tw__id TEXT DEFAULT ''", t))
	}
	for _, t := range twExtensionReferences {
		for _, c := range []string{"tw__reference_ids", "tw__reference_pages", "tw__reference_original"} {
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT DEFAULT ''", t, c))
		}
	}
	for _, t := range twExtensionOTU {
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN tw__otu_id TEXT DEFAULT ''", t))
	}
	for _, q := range stmts {
		if table, column, ok := parseAddColumn(q); ok && have[table][column] {
			continue
		}
		if _, err := a.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("core: taxonworks extension: %w", err)
		}
	}
	return nil
}

// tableColumns reads the columns of every table in the archive.
func (a *Archive) tableColumns(ctx context.Context) (map[string]map[string]bool, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT m.name, i.name FROM sqlite_master m
		 JOIN pragma_table_info(m.name) i WHERE m.type = 'table'`)
	if err != nil {
		return nil, fmt.Errorf("core: read columns: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		if out[table] == nil {
			out[table] = map[string]bool{}
		}
		out[table][column] = true
	}
	return out, rows.Err()
}

// parseAddColumn pulls the table and column out of an ALTER TABLE …
// ADD COLUMN statement, so an existing column can be skipped (SQLite
// has no ADD COLUMN IF NOT EXISTS).
func parseAddColumn(q string) (table, column string, ok bool) {
	const alter, add = "ALTER TABLE ", " ADD COLUMN "
	i := strings.Index(q, alter)
	j := strings.Index(q, add)
	if i != 0 || j < 0 {
		return "", "", false
	}
	table = q[len(alter):j]
	rest := q[j+len(add):]
	k := strings.Index(rest, " ")
	if k < 0 {
		return "", "", false
	}
	return table, rest[:k], true
}
