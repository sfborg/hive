package hive

import (
	"context"
	"path/filepath"
	"testing"
)

// TestCheckDigit_FiresOnBadISSN plants two reference rows — one
// with a valid ISSN, one with a transposed-digit variant that has
// the wrong check digit — and asserts the rule catches only the
// bad one.
func TestCheckDigit_FiresOnBadISSN(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "check_digit.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// Direct INSERTs (bypasses the Tx.CreateReference machinery
	// since we don't need CoLDP shaping — just two rows with known
	// ISSNs). FK enforcement briefly disabled because the other
	// nullable FK columns default to '' which no seed row satisfies.
	rows := []struct {
		id, issn string
	}{
		{"ref-good", "2049-3630"}, // valid ISSN (Nature)
		{"ref-bad", "2049-3603"},  // transposed check → invalid
	}
	for _, stmt := range []string{"PRAGMA foreign_keys = OFF"} {
		if _, err := a.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("pragma: %v", err)
		}
	}
	for _, r := range rows {
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO reference (col__id, col__issn) VALUES (?, ?)`,
			r.id, r.issn,
		); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma back on: %v", err)
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	hasIssue := func(id string) bool {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'reference' AND record_id = ? AND rule_id = 'hive_reference_issn_check_digit'`,
			id,
		).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		return n > 0
	}

	if hasIssue("ref-good") {
		t.Errorf("good ISSN should not fire; got an issue")
	}
	if !hasIssue("ref-bad") {
		t.Errorf("bad ISSN should fire; got zero")
	}
}
