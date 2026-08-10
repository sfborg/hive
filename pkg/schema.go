package hive

import _ "embed"

// Schema is the sfga SQLite schema DDL, embedded at build time.
// Source: github.com/sfborg/sfga (schema.sql). Update by re-copying from
// upstream when sfga cuts a new version. The SchemaVersion constant must be
// kept in sync with the version stored in the sfga `version` table.
//
//go:embed schema.sql
var Schema string

// SchemaVersion is the version stamped into the embedded schema. Kept as a
// constant so callers can compare against archives without executing SQL.
const SchemaVersion = "v0.5.1"
