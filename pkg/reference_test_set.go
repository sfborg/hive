package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TestSetMembership captures a reference's presence in the
// evaluation test set (see the hive__reference_test_set schema
// comment). Rows are shallow — the useful signal is just "is this
// paper in the set?" — but AddedAt / AddedBy give an audit trail
// when curators need to reconstruct set composition changes over
// time.
type TestSetMembership struct {
	ReferenceID string
	AddedAt     time.Time
	AddedBy     string
	Notes       string
}

// IsInTestSet reports whether the reference is in the evaluation
// test set. Zero-cost lookup — the row's presence IS the flag,
// nothing else to read.
func (a *Archive) IsInTestSet(ctx context.Context, referenceID string) (bool, error) {
	var n int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM hive__reference_test_set WHERE reference_id = ?`,
		referenceID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("core: check test set %s: %w", referenceID, err)
	}
	return n > 0, nil
}

// GetTestSetMembership returns the membership row (AddedAt / AddedBy
// / Notes) for a reference. Returns [ErrNotFound] when the paper
// is not in the set — callers that just need a bool use
// [Archive.IsInTestSet] instead.
func (a *Archive) GetTestSetMembership(ctx context.Context, referenceID string) (*TestSetMembership, error) {
	var (
		m         TestSetMembership
		addedAt   string
	)
	err := a.db.QueryRowContext(ctx,
		`SELECT reference_id, added_at, added_by, notes
		 FROM hive__reference_test_set WHERE reference_id = ?`,
		referenceID,
	).Scan(&m.ReferenceID, &addedAt, &m.AddedBy, &m.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("core: reference %q not in test set: %w", referenceID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("core: read test set row %s: %w", referenceID, err)
	}
	if t, err := time.Parse(time.RFC3339Nano, addedAt); err == nil {
		m.AddedAt = t
	}
	return &m, nil
}

// ListTestSetMembers returns every reference currently in the
// test set, oldest addition first (so a "recently added to test
// set" list reverses the order at query time when needed).
func (a *Archive) ListTestSetMembers(ctx context.Context) ([]TestSetMembership, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT reference_id, added_at, added_by, notes
		 FROM hive__reference_test_set
		 ORDER BY added_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("core: list test set: %w", err)
	}
	defer rows.Close()
	var out []TestSetMembership
	for rows.Next() {
		var (
			m       TestSetMembership
			addedAt string
		)
		if err := rows.Scan(&m.ReferenceID, &addedAt, &m.AddedBy, &m.Notes); err != nil {
			return nil, fmt.Errorf("core: scan test set row: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, addedAt); err == nil {
			m.AddedAt = t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddToTestSet inserts a membership row. Idempotent — adding an
// already-present reference updates AddedAt (and AddedBy from
// context) so the audit trail reflects the most recent curator
// intent. Notes are preserved on re-add unless the caller supplies
// a new value.
func (t *Tx) AddToTestSet(referenceID, notes string) error {
	if referenceID == "" {
		return errors.New("core: AddToTestSet: reference_id is required")
	}
	actor := ActorFromContext(t.ctx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO hive__reference_test_set (reference_id, added_at, added_by, notes)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(reference_id) DO UPDATE SET
		   added_at = excluded.added_at,
		   added_by = excluded.added_by,
		   notes = CASE WHEN excluded.notes = '' THEN notes ELSE excluded.notes END`,
		referenceID, now, actor, notes,
	)
	if err != nil {
		return fmt.Errorf("core: add test set %s: %w", referenceID, err)
	}
	return nil
}

// RemoveFromTestSet drops the membership row. Idempotent — removing
// an already-absent reference returns nil (curator intent — "not in
// set" — is already satisfied).
func (t *Tx) RemoveFromTestSet(referenceID string) error {
	if referenceID == "" {
		return errors.New("core: RemoveFromTestSet: reference_id is required")
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__reference_test_set WHERE reference_id = ?`,
		referenceID,
	); err != nil {
		return fmt.Errorf("core: remove test set %s: %w", referenceID, err)
	}
	return nil
}
