package core

import (
	"context"
	"fmt"
	"time"

	"github.com/gdower/gsvalidator/domain"
	"github.com/google/uuid"
)

// syncNameIssues runs the validator against the given name row and
// replaces every hive__validation_issue row for that (table, record)
// tuple with the freshly-computed set. Runs in its own short
// transaction so a sync failure never rolls back the write that
// triggered it — the read path shows the last successful sync's issues
// until a subsequent write re-syncs.
//
// A validator returning no failures still triggers a DELETE, so a rule
// that used to fire and no longer does gets its stale row cleared.
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

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM hive__validation_issue WHERE table_name = ? AND record_id = ?`,
		"name", nameID,
	); err != nil {
		return fmt.Errorf("core: clear issues for name %s: %w", nameID, err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range results {
		if r.Passed {
			continue
		}
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
		_, err := tx.ExecContext(ctx,
			`INSERT INTO hive__validation_issue (
				id, table_name, record_id,
				rule_id, rule_name, field_name,
				severity, enforcement, message,
				actual_value, expected_value, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), "name", nameID,
			r.RuleID, r.RuleName, r.FieldName,
			sev, enf, r.Message,
			nullableString(r.ActualValue),
			nullableString(r.ExpectedValue),
			now,
		)
		if err != nil {
			return fmt.Errorf("core: insert issue for name %s rule %s: %w",
				nameID, r.RuleID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("core: commit sync tx: %w", err)
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
