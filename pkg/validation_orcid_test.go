package hive

import (
	"context"
	"path/filepath"
	"testing"
)

// TestCheckDigit_FiresOnBadORCID plants two creator rows — one
// with a real ORCID, one with a transposed-digit variant — and
// asserts the check_digit rule catches only the bad one.
func TestCheckDigit_FiresOnBadORCID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orcid.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// Create() already seeded a metadata row with id=1, which
	// creator.col__metadata_id defaults to; we just insert creator
	// rows directly.
	rows := []struct {
		id                int
		orcid, given, fam string
	}{
		{1, "0000-0002-1825-0097", "Josiah", "Carberry"}, // real
		{2, "0000-0002-1852-0097", "Fake", "Person"},     // transposed 18↔85 → invalid
	}
	for _, r := range rows {
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO creator (col__id, col__orcid, col__given, col__family) VALUES (?, ?, ?, ?)`,
			r.id, r.orcid, r.given, r.fam,
		); err != nil {
			t.Fatalf("insert creator %d: %v", r.id, err)
		}
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	hasIssue := func(id string) bool {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'creator' AND record_id = ? AND rule_id = 'hive_creator_orcid_check_digit'`,
			id,
		).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		return n > 0
	}

	if hasIssue("1") {
		t.Errorf("valid ORCID should not fire; got an issue")
	}
	if !hasIssue("2") {
		t.Errorf("invalid ORCID should fire; got zero")
	}
}
