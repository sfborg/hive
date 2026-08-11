package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestForeignKeyExistsValidator_FiresOnDanglingReference creates a
// name whose col__reference_id points at a nonexistent reference,
// then asserts hive_name_reference_id_invalid fires. Direct SQL is
// used to bypass sfga's FK constraint — the same shape an archive
// arrives in after an import bug or hand-edit that turned off
// PRAGMA foreign_keys.
func TestForeignKeyExistsValidator_FiresOnDanglingReference(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fk.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// One name with no reference (control), one that will be
	// mutated to point at a nonexistent id.
	var goodID, danglingID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		goodID = id
		id, err = tx.CreateName(coldp.Name{ScientificName: "Baz qux"})
		if err != nil {
			return err
		}
		danglingID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}

	// Drop FK enforcement, set a dangling reference_id, re-enable.
	// Simulates an archive arriving from an import that skipped the
	// FK check.
	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`UPDATE name SET col__reference_id = 'ref-does-not-exist' WHERE col__id = ?`,
		`PRAGMA foreign_keys = ON`,
	} {
		if stmt == `UPDATE name SET col__reference_id = 'ref-does-not-exist' WHERE col__id = ?` {
			if _, err := a.db.ExecContext(ctx, stmt, danglingID); err != nil {
				t.Fatalf("dangle set: %v", err)
			}
			continue
		}
		if _, err := a.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("pragma: %v", err)
		}
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	hasIssue := func(nameID string) bool {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'name' AND record_id = ? AND rule_id = 'hive_name_reference_id_invalid'`,
			nameID,
		).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		return n > 0
	}

	if hasIssue(goodID) {
		t.Errorf("good name (empty reference_id) should not fire — skip on empty")
	}
	if !hasIssue(danglingID) {
		t.Errorf("dangling name should fire hive_name_reference_id_invalid")
	}
}
