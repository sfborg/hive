package core

import (
	"context"
	"database/sql"
	"fmt"
)

// hiveSchemaDDL is the DDL for every hive-managed table. Every hive__*
// table lives here so a curator inspecting the archive can find the
// full hive-owned schema surface in one place. Every statement is
// idempotent (CREATE TABLE / INDEX IF NOT EXISTS) so applying the DDL
// on an already-initialized archive is a no-op.
//
// Naming: hive__ prefix guarantees sflib / sf / gndb ignore these
// tables (they operate on the sfga col__ / gn__ / sf__ / tw__
// namespaces). Exporting via `sf to coldp` drops the hive__ tables
// cleanly; round-tripping through `sf from sfga && sf to sfga`
// preserves them. See CLAUDE.md § Schema handling.
const hiveSchemaDDL = `
-- hive__validation_issue caches gsvalidator results so read paths don't
-- have to re-execute rules on every fetch. Owner columns: table_name
-- and record_id identify the flagged row; rule_id + rule_name pin the
-- rule that produced the issue; severity / enforcement carry both
-- axes; message is the emit-time text so we can render without
-- re-running the validator. actual_value / expected_value are the
-- best-effort string forms of the two values a numeric / range /
-- length rule compared (TEXT so any type coerces cleanly).
--
-- The row is a snapshot, not a live query: after a name is edited
-- syncNameIssues is called to upsert its issues in place. The unique
-- key (table_name, record_id, rule_id, field_name) is the identity of
-- an issue — that tuple should always map to at most one row — so
-- re-syncs update the mutable columns (message, severity, actual /
-- expected value) without disturbing created_at or acknowledgment
-- state. Rules that stopped firing are DELETEd by the sync path
-- before the upsert loop runs.
CREATE TABLE IF NOT EXISTS hive__validation_issue (
	id                 TEXT PRIMARY KEY,     -- UUID
	table_name         TEXT NOT NULL,
	record_id          TEXT NOT NULL,
	rule_id            TEXT NOT NULL,
	rule_name          TEXT,
	field_name         TEXT NOT NULL DEFAULT '',
	severity           TEXT NOT NULL,
	enforcement        TEXT NOT NULL,
	message            TEXT NOT NULL,
	actual_value       TEXT,
	expected_value     TEXT,
	created_at         TEXT NOT NULL,
	acknowledged_by    TEXT,
	acknowledged_at    TEXT
);

-- Separate unique index (not a table-level constraint) so upgrades of
-- archives created before this index existed can pick it up on next
-- open. CREATE TABLE IF NOT EXISTS would not add the constraint
-- retroactively; CREATE UNIQUE INDEX IF NOT EXISTS does.
CREATE UNIQUE INDEX IF NOT EXISTS uq_hive__validation_issue_identity
	ON hive__validation_issue(table_name, record_id, rule_id, field_name);

CREATE INDEX IF NOT EXISTS ix_hive__validation_issue_record
	ON hive__validation_issue(table_name, record_id);

CREATE INDEX IF NOT EXISTS ix_hive__validation_issue_rule
	ON hive__validation_issue(rule_id);
`

// ensureHiveTables applies the DDL for every hive-managed table.
// Safe to call on every openReadWrite / Create — each statement is
// idempotent. Refuses to run on a read-only archive (caller responsibility
// to guard, but the DDL would fail anyway).
func ensureHiveTables(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, hiveSchemaDDL); err != nil {
		return fmt.Errorf("core: ensure hive__ tables: %w", err)
	}
	return nil
}
