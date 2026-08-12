package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestRuleConfig_SeverityOverride exercises the config-driven
// severity rewrite: create data that fires clb_duplicate_name at
// its default (info), override to warn, reopen, confirm the stored
// severity flips.
func TestRuleConfig_SeverityOverride(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cfg_sev.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two names with same canonical → duplicates.
	if err := a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"}); err != nil {
			return err
		}
		_, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		return err
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}
	// Default severity for clb_duplicate_name is info.
	sev := issueSeverityFor(t, ctx, a, "clb_duplicate_name")
	if sev != "info" {
		t.Fatalf("baseline severity: got %q, want info", sev)
	}
	// Set severity override to warn — then reopen archive so the
	// loader re-reads config.
	if err := a.SetRuleConfig(ctx, RuleConfig{
		RuleID:           "clb_duplicate_name",
		SeverityOverride: "warn",
	}); err != nil {
		t.Fatalf("SetRuleConfig: %v", err)
	}
	a.Close()

	a2, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a2.Close()
	if err := a2.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}
	sev = issueSeverityFor(t, ctx, a2, "clb_duplicate_name")
	if sev != "warn" {
		t.Errorf("after override: severity got %q, want warn", sev)
	}
}

// TestRuleConfig_MuteRule verifies that setting Enabled=false on a
// rule stops it firing entirely — no issue rows survive the next
// reindex.
func TestRuleConfig_MuteRule(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cfg_mute.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"}); err != nil {
			return err
		}
		_, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		return err
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}
	if issueCount(t, ctx, a, "clb_duplicate_name") == 0 {
		t.Fatalf("baseline: expected duplicate issues, got zero")
	}

	disabled := false
	if err := a.SetRuleConfig(ctx, RuleConfig{
		RuleID:  "clb_duplicate_name",
		Enabled: &disabled,
	}); err != nil {
		t.Fatalf("SetRuleConfig: %v", err)
	}
	a.Close()

	a2, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a2.Close()
	if err := a2.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}
	if n := issueCount(t, ctx, a2, "clb_duplicate_name"); n != 0 {
		t.Errorf("muted rule still fires: %d issues remain", n)
	}
}

// TestRulesetConfig_DisableRuleset drops a whole ruleset via the
// coarser toggle — every clb_* rule stops firing.
func TestRulesetConfig_DisableRuleset(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cfg_ruleset.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"}); err != nil {
			return err
		}
		_, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
		return err
	}); err != nil {
		t.Fatalf("CreateName: %v", err)
	}
	if issueCount(t, ctx, a, "clb_duplicate_name") == 0 {
		t.Fatalf("baseline: expected clb duplicate issues")
	}

	if err := a.SetRulesetConfig(ctx, "clb", false); err != nil {
		t.Fatalf("SetRulesetConfig: %v", err)
	}
	a.Close()

	a2, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a2.Close()
	if err := a2.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}
	if n := issueCount(t, ctx, a2, "clb_duplicate_name"); n != 0 {
		t.Errorf("disabled ruleset still fires: %d issues remain", n)
	}
}

// TestSetRulesetConfig_RejectsUnknownName verifies typos in
// ruleset names are caught before hitting the DB.
func TestSetRulesetConfig_RejectsUnknownName(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cfg_unknown.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()
	if err := a.SetRulesetConfig(ctx, "mystery_source", true); err == nil {
		t.Fatal("expected error for unknown ruleset name")
	}
}

// TestSetRuleConfig_RejectsBadSeverity verifies the accessor
// mirrors the DB CHECK constraint on severity values.
func TestSetRuleConfig_RejectsBadSeverity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cfg_badsev.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()
	if err := a.SetRuleConfig(ctx, RuleConfig{
		RuleID:           "clb_duplicate_name",
		SeverityOverride: "critical",
	}); err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

func issueCount(t *testing.T, ctx context.Context, a *Archive, ruleID string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results WHERE rule_id = ?`, ruleID,
	).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", ruleID, err)
	}
	return n
}

func issueSeverityFor(t *testing.T, ctx context.Context, a *Archive, ruleID string) string {
	t.Helper()
	var sev string
	if err := a.db.QueryRowContext(ctx,
		`SELECT severity FROM __gsvalidator_results WHERE rule_id = ? LIMIT 1`, ruleID,
	).Scan(&sev); err != nil {
		t.Fatalf("severity for %s: %v", ruleID, err)
	}
	return sev
}
