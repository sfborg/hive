// Package sfgarules holds the SFGAMapper — hive's implementation
// of gsvalidator's SchemaMapper and joins.PrimaryKeyProvider
// interfaces for the sfga schema (col__ / gn__ / sf__ prefixes,
// col__id as the primary key across every table). The package name
// is a legacy of when it also carried sfga-specific Go validators;
// those have all been decomposed into JSON rules that use
// gsvalidator's generic mechanisms.
package sfgarules

import (
	"context"
	"database/sql"
	"fmt"
)

// SFGAMapper implements gsvalidator's SchemaMapper interface for
// the sfga schema. Handles sfga's prefix conventions (col__
// columns, gn__ GlobalNames cache, sf__ species-file fields) and
// the archive-wide col__id primary-key convention.
//
// A truly generic gsvalidator reads
// relations from a bundle's `relations` block rather than
// requiring a Go-hardcoded mapper. This mapper stays until that
// loader lands.
type SFGAMapper struct {
	// Prefix mappings
	columnPrefix string
	genericPrefix string
	speciesFilePrefix string
}

// NewSFGAMapper creates a new SFGA schema mapper.
func NewSFGAMapper() *SFGAMapper {
	return &SFGAMapper{
		columnPrefix:      "col__",
		genericPrefix:     "gn__",
		speciesFilePrefix: "sf__",
	}
}

// GetTableName resolves logical table name to physical table name.
// For SFGA, table names are typically unprefixed (e.g., "name", "taxon").
func (m *SFGAMapper) GetTableName(logicalName string) string {
	// SFGA tables don't have prefixes
	return logicalName
}

// GetFieldName resolves logical field name to physical field name.
// For SFGA, most fields have the col__ prefix.
func (m *SFGAMapper) GetFieldName(tableName, logicalName string) string {
	// Check if already prefixed
	if hasPrefix(logicalName, m.columnPrefix, m.genericPrefix, m.speciesFilePrefix) {
		return logicalName
	}

	// Apply column prefix by default
	return m.columnPrefix + logicalName
}

// GetPrimaryKeyField returns the primary-key column name for a given
// SFGA table. Most sfga tables use col__id, but a handful of pure
// link / annotation tables (vernacular, distribution,
// species_interaction, name_relation) have no col__id column — sfga
// identifies rows there by a composite of their FK / value tuple.
// Hive uses SQLite's implicit rowid as the opaque handle for those
// tables so gsvalidator's per-record lookup has an addressable
// column to bind against.
func (m *SFGAMapper) GetPrimaryKeyField(tableName string) string {
	switch tableName {
	case "vernacular", "distribution", "species_interaction", "name_relation":
		return "rowid"
	}
	return m.columnPrefix + "id"
}

// LoadRecord loads a record by table name and ID.
func (m *SFGAMapper) LoadRecord(ctx context.Context, db *sql.DB, tableName, id string) (map[string]interface{}, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ?", tableName, m.GetPrimaryKeyField(tableName))

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("record not found: %s", id)
	}

	return scanRowToMap(rows)
}

// LoadRelatedRecords loads related records via foreign key.
func (m *SFGAMapper) LoadRelatedRecords(ctx context.Context, db *sql.DB, tableName, foreignKey, id string) ([]map[string]interface{}, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ?", tableName, foreignKey)

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query related records: %w", err)
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		record, err := scanRowToMap(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, record)
	}

	return results, nil
}

// LoadAllRecords loads all records from a table.
func (m *SFGAMapper) LoadAllRecords(ctx context.Context, db *sql.DB, tableName string) ([]map[string]interface{}, error) {
	query := fmt.Sprintf("SELECT * FROM %s", tableName)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query records: %w", err)
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		record, err := scanRowToMap(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, record)
	}

	return results, nil
}

// hasPrefix checks if the field name has any of the SFGA prefixes.
func hasPrefix(fieldName string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if len(fieldName) > len(prefix) && fieldName[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// scanRowToMap converts a SQL row to a map[string]interface{}.
func scanRowToMap(rows *sql.Rows) (map[string]interface{}, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	record := make(map[string]interface{})
	for i, col := range columns {
		val := values[i]
		// Convert []byte to string
		if b, ok := val.([]byte); ok {
			record[col] = string(b)
		} else {
			record[col] = val
		}
	}

	return record, nil
}
