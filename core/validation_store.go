package core

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gdower/gsvalidator/domain"
	"github.com/google/uuid"
)

// syncNameIssues runs the validator against the given name row and
// reconciles hive__validation_issue with the freshly-computed set.
//
// Semantics: rules that still fire are upserted in place (INSERT ...
// ON CONFLICT DO UPDATE on the (table_name, record_id, rule_id,
// field_name) identity). Rules that stopped firing since the previous
// sync are DELETEd. This preserves created_at and any acknowledgment
// state (acknowledged_by / acknowledged_at) across reindex runs — a
// curator who has muted a rule doesn't lose that decision when the
// engine re-runs.
//
// Runs in its own short transaction so a sync failure never rolls
// back the write that triggered it — the read path shows the last
// successful sync's issues until a subsequent write re-syncs.
func (a *Archive) syncNameIssues(ctx context.Context, nameID string) error {
	if a.readOnly || a.validator == nil || nameID == "" {
		return nil
	}
	results, err := a.validator.Execute(ctx, "name", nameID)
	if err != nil {
		return fmt.Errorf("core: sync issues for name %s: %w", nameID, err)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("core: begin sync tx: %w", err)
	}
	defer tx.Rollback()

	// Build the "keep" set — every (rule_id, field_name) that fires
	// this pass — so we can delete rows for rules that no longer do.
	keep := make(map[[2]string]bool, len(results))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range results {
		if r.Passed {
			continue
		}
		keep[[2]string{r.RuleID, r.FieldName}] = true

		enf := string(r.Enforcement)
		if enf == "" {
			if r.IsHardFailure() {
				enf = string(domain.EnforcementHard)
			} else {
				enf = string(domain.EnforcementSoft)
			}
		}
		sev := string(r.Severity)
		if sev == "" {
			switch {
			case r.IsError():
				sev = string(domain.SeverityError)
			case r.IsWarning():
				sev = string(domain.SeverityWarn)
			case r.IsInfo():
				sev = string(domain.SeverityInfo)
			case r.IsDebug():
				sev = string(domain.SeverityDebug)
			default:
				sev = string(domain.SeverityWarn)
			}
		}
		// Upsert on the identity tuple. New rows get a fresh UUID +
		// created_at; existing rows keep both (excluded.id and
		// excluded.created_at are ignored in the update clause).
		_, err := tx.ExecContext(ctx,
			`INSERT INTO hive__validation_issue (
				id, table_name, record_id,
				rule_id, rule_name, field_name,
				severity, enforcement, message,
				actual_value, expected_value, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (table_name, record_id, rule_id, field_name) DO UPDATE SET
				rule_name      = excluded.rule_name,
				severity       = excluded.severity,
				enforcement    = excluded.enforcement,
				message        = excluded.message,
				actual_value   = excluded.actual_value,
				expected_value = excluded.expected_value`,
			uuid.NewString(), "name", nameID,
			r.RuleID, r.RuleName, r.FieldName,
			sev, enf, r.Message,
			nullableString(r.ActualValue),
			nullableString(r.ExpectedValue),
			now,
		)
		if err != nil {
			return fmt.Errorf("core: upsert issue for name %s rule %s: %w",
				nameID, r.RuleID, err)
		}
	}

	// Prune rows for rules that no longer fire on this record.
	if err := pruneStaleIssues(ctx, tx, "name", nameID, keep); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("core: commit sync tx: %w", err)
	}
	return nil
}

// pruneStaleIssues deletes hive__validation_issue rows for the given
// record whose (rule_id, field_name) key is not in keep. Called by
// syncNameIssues after upserting every current rule so rules that
// stopped firing (or were removed from the rule set) get cleaned up
// without touching the ones that still fire.
func pruneStaleIssues(ctx context.Context, tx *sql.Tx, table, recordID string, keep map[[2]string]bool) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, rule_id, field_name
		 FROM hive__validation_issue
		 WHERE table_name = ? AND record_id = ?`,
		table, recordID,
	)
	if err != nil {
		return fmt.Errorf("core: list stale issues for %s %s: %w", table, recordID, err)
	}
	defer rows.Close()
	var stale []string
	for rows.Next() {
		var id, ruleID, fieldName string
		if err := rows.Scan(&id, &ruleID, &fieldName); err != nil {
			return fmt.Errorf("core: scan stale issue: %w", err)
		}
		if !keep[[2]string{ruleID, fieldName}] {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM hive__validation_issue WHERE id = ?`, id,
		); err != nil {
			return fmt.Errorf("core: delete stale issue %s: %w", id, err)
		}
	}
	return nil
}

// readNameIssues returns the persisted issue set for a name row.
// Empty when the row has never been synced or has no known problems —
// callers cannot distinguish those two states from this function alone.
func (a *Archive) readNameIssues(ctx context.Context, nameID string) ([]ValidationWarning, error) {
	if nameID == "" {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx,
		`SELECT rule_id, COALESCE(rule_name, ''), COALESCE(field_name, ''),
		        severity, message
		 FROM hive__validation_issue
		 WHERE table_name = ? AND record_id = ?
		 ORDER BY severity, rule_id`,
		"name", nameID,
	)
	if err != nil {
		return nil, fmt.Errorf("core: read issues for name %s: %w", nameID, err)
	}
	defer rows.Close()
	var out []ValidationWarning
	for rows.Next() {
		var w ValidationWarning
		if err := rows.Scan(&w.RuleID, &w.RuleName, &w.FieldName, &w.Severity, &w.Message); err != nil {
			return nil, fmt.Errorf("core: scan issue row: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ReindexProgress reports the state of a long-running reindex to the
// caller. Sent once per row plus a final call with Done == Total.
type ReindexProgress struct {
	Table   string
	Done    int
	Total   int
	Current string // record ID that just finished; empty on the summary tick
}

// ReindexValidation walks every row in every hive-validated table and
// rewrites its hive__validation_issue rows to match the current rule
// set. Backfills archives edited before persistence landed and repairs
// caches when a rule is added, tuned, or removed. Progress fires
// once per row so a CLI or SSE stream can render a live counter;
// pass nil to skip reporting.
//
// Runs one row at a time in its own tx via syncNameIssues — the write
// lock is held briefly, curators editing the archive in parallel see
// only per-row contention. Cancellation via ctx stops between rows;
// rows already synced stay synced.
func (a *Archive) ReindexValidation(ctx context.Context, progress func(ReindexProgress)) error {
	if a.readOnly {
		return ErrReadOnly
	}
	rows, err := a.db.QueryContext(ctx, `SELECT col__id FROM name ORDER BY col__id`)
	if err != nil {
		return fmt.Errorf("core: reindex list names: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("core: reindex scan name id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	total := len(ids)
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.syncNameIssues(ctx, id); err != nil {
			return fmt.Errorf("core: reindex name %s: %w", id, err)
		}
		if progress != nil {
			progress(ReindexProgress{
				Table:   "name",
				Done:    i + 1,
				Total:   total,
				Current: id,
			})
		}
	}
	return nil
}

// nullableString turns any value from a validator result into a TEXT
// column value, using NULL when the value is nil or empty. Numeric
// values coerce via fmt so ranged / length rules store their actual
// number (as text) for later display.
func nullableString(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	s := fmt.Sprintf("%v", v)
	if s == "" {
		return nil
	}
	return s
}
