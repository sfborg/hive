package hive

import (
	"context"
	"path/filepath"
	"testing"
)

// TestRuleFixtures walks the RuleFixtures registry and asserts, for
// every fixture:
//   - each Bad case, applied to a fresh archive and reindexed,
//     fires the rule at least once.
//   - each Good case, applied the same way, does NOT fire the rule.
//
// Fresh archives keep cases isolated so cross-contamination between
// fixtures can't hide a bug.
func TestRuleFixtures(t *testing.T) {
	for _, f := range RuleFixtures {
		f := f
		t.Run(f.RuleID, func(t *testing.T) {
			if len(f.Bad) == 0 {
				t.Fatalf("fixture %s has no Bad cases — every rule needs at least one canonical failure",
					f.RuleID)
			}
			for i, c := range f.Bad {
				c := c
				t.Run(fixtureSubtestName("bad", i, c.Note), func(t *testing.T) {
					n := runFixtureCase(t, f.RuleID, c)
					if n == 0 {
						t.Errorf("Bad case %q on %s produced 0 issues; rule should have fired",
							c.Note, f.RuleID)
					}
				})
			}
			for i, c := range f.Good {
				c := c
				t.Run(fixtureSubtestName("good", i, c.Note), func(t *testing.T) {
					n := runFixtureCase(t, f.RuleID, c)
					if n != 0 {
						t.Errorf("Good case %q on %s produced %d issues; rule should NOT have fired",
							c.Note, f.RuleID, n)
					}
				})
			}
		})
	}
}

// runFixtureCase applies one case to a fresh archive, reindexes,
// and returns the count of issues emitted by the target rule.
func runFixtureCase(t *testing.T, ruleID string, c FixtureCase) int {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fixture.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()
	if err := c.Setup(ctx, a); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}
	var n int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM __gsvalidator_results WHERE rule_id = ?`, ruleID,
	).Scan(&n); err != nil {
		t.Fatalf("query issues: %v", err)
	}
	return n
}

func fixtureSubtestName(kind string, i int, note string) string {
	// Keep the subtest names short but readable in the test output.
	// Special chars in Note would confuse test runners, so keep it
	// alphanumeric-ish; the full note appears in the failure message.
	suffix := note
	if len(suffix) > 40 {
		suffix = suffix[:40]
	}
	return kind + "-" + itoaFixture(i) + "-" + suffix
}

func itoaFixture(i int) string {
	// Small helper so we don't need strconv just for this.
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	}
	// Fallback for larger indices — unlikely in practice.
	return "N"
}
