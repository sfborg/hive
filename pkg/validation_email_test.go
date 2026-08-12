package hive

import (
	"context"
	"path/filepath"
	"testing"
)

// TestEmailRegex covers the HTML5 email rule: valid addresses pass,
// obvious typos fire, empty emails skip via the conditions gate,
// and permissive edge cases (plus tags, new gTLDs) are accepted.
func TestEmailRegex(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "email.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	rows := []struct {
		id    int
		email string
		want  bool // true = expect to be flagged as unusual
	}{
		{1, "alice@example.com", false},               // classic
		{2, "alice+tag@example.com", false},           // plus tag
		{3, "alice@example.museum", false},            // new gTLD
		{4, "alice@example.technology", false},        // new gTLD
		{5, "alice.smith@sub.example.co.uk", false},   // multi-label
		{6, "", false},                                 // empty → condition gate → skip
		{7, "not an email", true},                      // gross typo
		{8, "alice@", true},                            // missing domain
		{9, "@example.com", true},                      // missing local
	}
	for _, r := range rows {
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO creator (col__id, col__email, col__given, col__family) VALUES (?, ?, ?, ?)`,
			r.id, r.email, "First", "Last",
		); err != nil {
			t.Fatalf("insert row %d: %v", r.id, err)
		}
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	for _, r := range rows {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'creator' AND record_id = ? AND rule_id = 'hive_creator_email_format'`,
			r.id,
		).Scan(&n); err != nil {
			t.Fatalf("query row %d: %v", r.id, err)
		}
		got := n > 0
		if got != r.want {
			t.Errorf("email %q: got flagged=%v, want=%v", r.email, got, r.want)
		}
	}
}
