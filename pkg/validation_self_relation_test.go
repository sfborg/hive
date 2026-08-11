package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestSelfRelationRule_FiresOnSelfReferenceRow plants a
// name_relation row whose subject and object are the same name by
// bypassing Tx.LinkNameRelation's own guard via direct SQL — the
// same shape an archive arrives in after an import that skipped the
// guard. hive_name_on_self_relation should then fire on that name
// during reindex.
func TestSelfRelationRule_FiresOnSelfReferenceRow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "self_rel.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	var subjectID, controlID string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		if err != nil {
			return err
		}
		subjectID = id
		id, err = tx.CreateName(coldp.Name{ScientificName: "Baz qux"})
		if err != nil {
			return err
		}
		controlID = id
		return nil
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}

	// Plant a self-referenced row. Uses BASIONYM as a placeholder
	// type_id — the specific rel type doesn't matter for this rule.
	// FK enforcement is toggled off around the INSERT because the
	// other nullable FK columns on name_relation default to '',
	// which no seed row satisfies.
	for _, stmt := range []struct {
		sql  string
		args []interface{}
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`INSERT INTO name_relation (col__name_id, col__related_name_id, col__type_id) VALUES (?, ?, 'BASIONYM')`,
			[]interface{}{subjectID, subjectID}},
		{`PRAGMA foreign_keys = ON`, nil},
	} {
		if _, err := a.db.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("plant: %v", err)
		}
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	hasIssue := func(nameID string) bool {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'name' AND record_id = ? AND rule_id = 'hive_name_on_self_relation'`,
			nameID,
		).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		return n > 0
	}

	if !hasIssue(subjectID) {
		t.Errorf("subject name on self-relation should fire; got zero")
	}
	if hasIssue(controlID) {
		t.Errorf("control name (not on any relation) should not fire")
	}
}
