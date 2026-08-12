package hive

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/gdower/gsvalidator/domain"
	"github.com/gdower/gsvalidator/usecase/validator"
	"github.com/google/uuid"
)

// syncIssues runs syncIssuesLocal on the record, then propagates
// one hop: for every aggregate-shaped rule whose validator exposes
// a NeighborhoodProvider, it finds the records whose results may
// have changed and re-syncs each of them (locally, no further
// propagation). Only "new-side" neighbors are discoverable — a
// record that USED to match but no longer does needs a pre-mutation
// snapshot; call syncIssuesWithSnapshot for that path.
//
// Call sites: any mutation that doesn't need old-side coverage
// (currently the CREATE paths that have no pre-state). Bulk reindex
// uses syncIssuesLocal directly to avoid the redundant propagation
// work.
func (a *Archive) syncIssues(ctx context.Context, table, recordID string) error {
	return a.syncIssuesWithSnapshot(ctx, table, recordID, dirtyEntry{})
}

// syncIssuesWithSnapshot runs syncIssuesLocal on the record (unless
// entry.deleted, in which case it just prunes the record's own
// __gsvalidator_results rows), then propagates to BOTH new-side
// neighbors (derived from the current record) and old-side
// neighbors (derived from entry.preRecord). Deduplicates so any
// given neighbor is synced at most once per call.
func (a *Archive) syncIssuesWithSnapshot(ctx context.Context, table, recordID string, entry dirtyEntry) error {
	if entry.deleted {
		if err := a.deleteRecordIssues(ctx, table, recordID); err != nil {
			return err
		}
	} else {
		if err := a.syncIssuesLocal(ctx, table, recordID); err != nil {
			return err
		}
	}
	if a.validator == nil {
		return nil
	}

	touched := map[[2]string]bool{{table, recordID}: true}
	syncNeighbor := func(n validator.NeighborRef) error {
		key := [2]string{n.TableName, n.RecordID}
		if touched[key] {
			return nil
		}
		touched[key] = true
		return a.syncIssuesLocal(ctx, n.TableName, n.RecordID)
	}

	// New-side: neighbors implied by the record's current values.
	// Skipped when the record was deleted — nothing to load from.
	if !entry.deleted {
		post, err := a.validator.Neighborhoods(ctx, table, recordID)
		if err != nil {
			return fmt.Errorf("core: neighborhood scan for %s %s: %w", table, recordID, err)
		}
		for _, n := range post {
			if err := syncNeighbor(n); err != nil {
				return fmt.Errorf("core: propagate sync to %s %s: %w", n.TableName, n.RecordID, err)
			}
		}
	}

	// Old-side: neighbors implied by the record's pre-mutation values.
	// Skipped when we have no snapshot (CREATE, or an update path that
	// hasn't captured one yet).
	if entry.preRecord != nil {
		pre, err := a.validator.NeighborhoodsFromRecord(ctx, table, recordID, entry.preRecord)
		if err != nil {
			return fmt.Errorf("core: pre-neighborhood scan for %s %s: %w", table, recordID, err)
		}
		for _, n := range pre {
			if err := syncNeighbor(n); err != nil {
				return fmt.Errorf("core: propagate pre-sync to %s %s: %w", n.TableName, n.RecordID, err)
			}
		}
	}
	return nil
}

// deleteRecordIssues removes every __gsvalidator_results row for a
// record that no longer exists. Runs when the write path marks the
// record as deleted (see Tx.markNameDeleted / markTaxonDeleted).
func (a *Archive) deleteRecordIssues(ctx context.Context, table, recordID string) error {
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM __gsvalidator_results WHERE table_name = ? AND record_id = ?`,
		table, recordID,
	)
	if err != nil {
		return fmt.Errorf("core: delete issues for %s %s: %w", table, recordID, err)
	}
	return nil
}

// syncIssuesLocal runs the validator against a single record and
// reconciles __gsvalidator_results with the freshly-computed set,
// without propagating to related records. Intended for reindex
// sweeps and as the inner step of syncIssues.
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
func (a *Archive) syncIssuesLocal(ctx context.Context, table, recordID string) error {
	if a.readOnly || a.validator == nil || table == "" || recordID == "" {
		return nil
	}
	results, err := a.validator.Execute(ctx, table, recordID)
	if err != nil {
		return fmt.Errorf("core: sync issues for %s %s: %w", table, recordID, err)
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
		// created_at; existing rows keep both (excluded.result_id and
		// excluded.created_at are ignored in the update clause).
		_, err := tx.ExecContext(ctx,
			`INSERT INTO __gsvalidator_results (
				result_id, table_name, record_id,
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
			uuid.NewString(), table, recordID,
			r.RuleID, r.RuleName, r.FieldName,
			sev, enf, r.Message,
			nullableString(r.ActualValue),
			nullableString(r.ExpectedValue),
			now,
		)
		if err != nil {
			return fmt.Errorf("core: upsert issue for %s %s rule %s: %w",
				table, recordID, r.RuleID, err)
		}
	}

	// Prune rows for rules that no longer fire on this record.
	if err := pruneStaleIssues(ctx, tx, table, recordID, keep); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("core: commit sync tx: %w", err)
	}
	return nil
}

// Per-aggregate sync helpers so mutation methods can express intent
// at the call site (Tx.CreateName → syncNameIssues) without knowing
// the "name" table string. Metadata takes an int id because the sfga
// metadata table's col__id is INTEGER, not the string UUID that name
// and taxon use — the id gets stringified for storage.
func (a *Archive) syncNameIssues(ctx context.Context, id string) error {
	return a.syncIssues(ctx, "name", id)
}
func (a *Archive) syncTaxonIssues(ctx context.Context, id string) error {
	return a.syncIssues(ctx, "taxon", id)
}
func (a *Archive) syncReferenceIssues(ctx context.Context, id string) error {
	return a.syncIssues(ctx, "reference", id)
}
func (a *Archive) syncMetadataIssues(ctx context.Context, id int) error {
	return a.syncIssues(ctx, "metadata", strconv.Itoa(id))
}

// The -WithSnapshot variants let WithTx's post-commit loop pass the
// captured dirtyEntry through so aggregate rules see both old-side
// and new-side neighbors.
func (a *Archive) syncNameIssuesWithSnapshot(ctx context.Context, id string, entry dirtyEntry) error {
	return a.syncIssuesWithSnapshot(ctx, "name", id, entry)
}
func (a *Archive) syncTaxonIssuesWithSnapshot(ctx context.Context, id string, entry dirtyEntry) error {
	return a.syncIssuesWithSnapshot(ctx, "taxon", id, entry)
}
func (a *Archive) syncReferenceIssuesWithSnapshot(ctx context.Context, id string, entry dirtyEntry) error {
	return a.syncIssuesWithSnapshot(ctx, "reference", id, entry)
}
func (a *Archive) syncMetadataIssuesWithSnapshot(ctx context.Context, id int, entry dirtyEntry) error {
	return a.syncIssuesWithSnapshot(ctx, "metadata", strconv.Itoa(id), entry)
}

// pruneStaleIssues deletes __gsvalidator_results rows for the given
// record whose (rule_id, field_name) key is not in keep. Called by
// syncIssues after upserting every current rule so rules that
// stopped firing (or were removed from the rule set) get cleaned up
// without touching the ones that still fire.
func pruneStaleIssues(ctx context.Context, tx *sql.Tx, table, recordID string, keep map[[2]string]bool) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT result_id, rule_id, field_name
		 FROM __gsvalidator_results
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
			`DELETE FROM __gsvalidator_results WHERE result_id = ?`, id,
		); err != nil {
			return fmt.Errorf("core: delete stale issue %s: %w", id, err)
		}
	}
	return nil
}

// readIssues returns the persisted issue set for a single record.
// Empty when the row has never been synced or has no known problems —
// callers cannot distinguish those two states from this function alone.
func (a *Archive) readIssues(ctx context.Context, table, recordID string) ([]ValidationWarning, error) {
	if table == "" || recordID == "" {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx,
		`SELECT rule_id, COALESCE(rule_name, ''), COALESCE(field_name, ''),
		        severity, message
		 FROM __gsvalidator_results
		 WHERE table_name = ? AND record_id = ?
		 ORDER BY severity, rule_id`,
		table, recordID,
	)
	if err != nil {
		return nil, fmt.Errorf("core: read issues for %s %s: %w", table, recordID, err)
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

// readNameIssues / readTaxonIssues / readMetadataIssues are thin
// per-aggregate wrappers used by Archive.NameWarnings / TaxonWarnings /
// MetadataWarnings. Kept as separate call sites so the intent is
// legible where the read happens.
func (a *Archive) readNameIssues(ctx context.Context, id string) ([]ValidationWarning, error) {
	return a.readIssues(ctx, "name", id)
}
func (a *Archive) readTaxonIssues(ctx context.Context, id string) ([]ValidationWarning, error) {
	return a.readIssues(ctx, "taxon", id)
}
func (a *Archive) readMetadataIssues(ctx context.Context, id int) ([]ValidationWarning, error) {
	return a.readIssues(ctx, "metadata", strconv.Itoa(id))
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
// rewrites its __gsvalidator_results rows to match the current rule
// set. Backfills archives edited before persistence landed and repairs
// caches when a rule is added, tuned, or removed. Progress fires
// once per row so a CLI or SSE stream can render a live counter;
// pass nil to skip reporting.
//
// Iteration order: name → taxon → metadata. Each table walks one row
// at a time in its own tx via syncIssues — the write lock is held
// briefly, curators editing the archive in parallel see only per-row
// contention. Cancellation via ctx stops between rows; rows already
// synced stay synced.
func (a *Archive) ReindexValidation(ctx context.Context, progress func(ReindexProgress)) error {
	if a.readOnly {
		return ErrReadOnly
	}
	// Every hive-validated table is walked in a uniform loop so
	// adding a new table (e.g. reference) is a one-line change. Uses
	// syncIssuesLocal (no per-row neighborhood propagation) because
	// the outer loop already visits every row — propagating from each
	// would produce O(rows^2) work with the same end state.
	tables := []struct {
		name   string
		listQ  string
		syncFn func(context.Context, string) error
	}{
		{
			name:  "name",
			listQ: `SELECT col__id FROM name ORDER BY col__id`,
			syncFn: func(ctx context.Context, id string) error {
				return a.syncIssuesLocal(ctx, "name", id)
			},
		},
		{
			name:  "taxon",
			listQ: `SELECT col__id FROM taxon ORDER BY col__id`,
			syncFn: func(ctx context.Context, id string) error {
				return a.syncIssuesLocal(ctx, "taxon", id)
			},
		},
		{
			name:  "metadata",
			listQ: `SELECT col__id FROM metadata ORDER BY col__id`,
			// Metadata ids are ints in the source schema but reindex
			// walks strings for uniformity; validate the coercion.
			syncFn: func(ctx context.Context, id string) error {
				if _, err := strconv.Atoi(id); err != nil {
					return fmt.Errorf("core: metadata id %q not numeric: %w", id, err)
				}
				return a.syncIssuesLocal(ctx, "metadata", id)
			},
		},
		{
			name:  "reference",
			listQ: `SELECT col__id FROM reference ORDER BY col__id`,
			syncFn: func(ctx context.Context, id string) error {
				return a.syncIssuesLocal(ctx, "reference", id)
			},
		},
		{
			// Creator carries per-agent ORCIDs (and other identifier
			// fields) that benefit from check-digit rules. Reindex
			// walks its rows; per-mutation sync is a follow-up.
			name:  "creator",
			listQ: `SELECT col__id FROM creator ORDER BY col__id`,
			syncFn: func(ctx context.Context, id string) error {
				return a.syncIssuesLocal(ctx, "creator", id)
			},
		},
	}
	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.reindexTable(ctx, t.name, t.listQ, t.syncFn, progress); err != nil {
			return err
		}
	}
	return nil
}

func (a *Archive) reindexTable(
	ctx context.Context,
	table, listQuery string,
	sync func(context.Context, string) error,
	progress func(ReindexProgress),
) error {
	rows, err := a.db.QueryContext(ctx, listQuery)
	if err != nil {
		return fmt.Errorf("core: reindex list %s: %w", table, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("core: reindex scan %s id: %w", table, err)
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
		if err := sync(ctx, id); err != nil {
			return fmt.Errorf("core: reindex %s %s: %w", table, id, err)
		}
		if progress != nil {
			progress(ReindexProgress{
				Table:   table,
				Done:    i + 1,
				Total:   total,
				Current: id,
			})
		}
	}
	return nil
}

// defaultRecheckDays is the fallback cadence when a time-based rule
// leaves RecheckDays unset. Weekly matches the "not too chatty, not
// too silent" middle ground for the current rule set.
const defaultRecheckDays = 7

// RefreshTimeBasedIssues evaluates every enabled time-based rule
// whose cooldown has elapsed and re-syncs its target records. Called
// from Archive.Open (covers restart), periodic tickers in the server
// and TUI (covers long-running sessions), and implicitly by
// ReindexValidation (which walks everything unconditionally).
//
// State lives in __gsvalidator_rule_state.last_run_at, keyed by rule id.
// A rule missing from that table (never run) is treated as due.
// Successful evaluation updates the timestamp; failure leaves the
// previous value in place so a transient error doesn't push the
// next check by another RecheckDays.
//
// Currently limited to rules whose TableName is one of the tables
// hive has issue infrastructure for (name / taxon / metadata) — a
// rule targeting an unknown table is silently skipped.
func (a *Archive) RefreshTimeBasedIssues(ctx context.Context) error {
	if a.readOnly || a.validator == nil || a.ruleLoader == nil {
		return nil
	}
	rules, err := a.ruleLoader.LoadRules(ctx)
	if err != nil {
		return fmt.Errorf("core: refresh time-based: load rules: %w", err)
	}
	now := time.Now().UTC()
	for _, rule := range rules {
		if !rule.IsActive || rule.EffectiveTrigger() != domain.TriggerTimeBased {
			continue
		}
		cadence := rule.RecheckDays
		if cadence <= 0 {
			cadence = defaultRecheckDays
		}
		var lastRunStr sql.NullString
		if err := a.db.QueryRowContext(ctx,
			`SELECT last_run_at FROM __gsvalidator_rule_state WHERE rule_id = ?`,
			rule.ID,
		).Scan(&lastRunStr); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("core: refresh time-based: read state %s: %w", rule.ID, err)
		}
		if lastRunStr.Valid {
			last, parseErr := time.Parse(time.RFC3339Nano, lastRunStr.String)
			if parseErr == nil && now.Sub(last) < time.Duration(cadence)*24*time.Hour {
				continue // still within cooldown
			}
		}
		if err := a.evaluateTimeBasedRule(ctx, rule); err != nil {
			// Log-worthy but non-fatal: one bad rule shouldn't stop
			// the sweep. Skip updating last_run_at so the next call
			// retries.
			continue
		}
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO __gsvalidator_rule_state (rule_id, last_run_at) VALUES (?, ?)
			 ON CONFLICT (rule_id) DO UPDATE SET last_run_at = excluded.last_run_at`,
			rule.ID, now.Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("core: refresh time-based: update state %s: %w", rule.ID, err)
		}
	}
	return nil
}

// evaluateTimeBasedRule syncs issues for every record the rule's
// scope selects. For per-record tables (name, taxon) that's every
// row in the table; for metadata (singleton) it's the one row. Uses
// syncIssuesLocal for the same reason ReindexValidation does — the
// full sweep already covers every record, so per-row propagation is
// wasted work.
func (a *Archive) evaluateTimeBasedRule(ctx context.Context, rule *domain.Rule) error {
	switch rule.TableName {
	case "name":
		return a.reindexTable(ctx, "name",
			`SELECT col__id FROM name ORDER BY col__id`,
			func(ctx context.Context, id string) error {
				return a.syncIssuesLocal(ctx, "name", id)
			}, nil)
	case "taxon":
		return a.reindexTable(ctx, "taxon",
			`SELECT col__id FROM taxon ORDER BY col__id`,
			func(ctx context.Context, id string) error {
				return a.syncIssuesLocal(ctx, "taxon", id)
			}, nil)
	case "metadata":
		return a.reindexTable(ctx, "metadata",
			`SELECT col__id FROM metadata ORDER BY col__id`,
			func(ctx context.Context, id string) error {
				if _, err := strconv.Atoi(id); err != nil {
					return fmt.Errorf("core: metadata id %q not numeric: %w", id, err)
				}
				return a.syncIssuesLocal(ctx, "metadata", id)
			}, nil)
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
