package hive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sfborg/gsvalidator/adapter/repository"
)

// TestTWRuleFixtures applies the data-driven fixtures of the
// TaxonWorks-derived bundles with the same fire / no-fire invariant
// as TestRuleFixtures, and checks that every TaxonWorks rule has a
// fixture.
func TestTWRuleFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/tw_rulefixtures.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	fixtures, err := RuleFixturesFromJSON(data)
	if err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	covered := make(map[string]bool, len(fixtures))
	for _, f := range fixtures {
		covered[f.RuleID] = true
	}
	for _, src := range rulesetSources {
		if src.name != "tw" && !strings.HasPrefix(src.name, "tw_") {
			continue
		}
		rules, err := bundleRules(src.bundle)
		if err != nil {
			t.Fatalf("%s bundle: %v", src.name, err)
		}
		for _, id := range rules {
			if !covered[id] {
				t.Errorf("%s: rule %s has no fixture", src.name, id)
			}
		}
	}
	for _, f := range fixtures {
		t.Run(f.RuleID, func(t *testing.T) {
			if len(f.Bad) == 0 || len(f.Good) == 0 {
				t.Fatalf("fixture %s needs Bad and Good cases", f.RuleID)
			}
			for i, c := range f.Bad {
				t.Run(fixtureSubtestName("bad", i, c.Note), func(t *testing.T) {
					if n := runSchemaFixtureCase(t, f.RuleID, f.Schema, c); n == 0 {
						t.Errorf("Bad case %q on %s produced 0 issues; rule should have fired", c.Note, f.RuleID)
					}
				})
			}
			for i, c := range f.Good {
				t.Run(fixtureSubtestName("good", i, c.Note), func(t *testing.T) {
					if n := runSchemaFixtureCase(t, f.RuleID, f.Schema, c); n != 0 {
						t.Errorf("Good case %q on %s produced %d issues; rule should NOT have fired",
							c.Note, f.RuleID, n)
					}
				})
			}
		})
	}
}

// bundleRules returns the rule ids of an embedded bundle.
func bundleRules(bundle []byte) ([]string, error) {
	pkg, err := repository.NewBytesBundleLoader(bundle).LoadPackage(context.Background())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(pkg.Rules))
	for _, r := range pkg.Rules {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// runSchemaFixtureCase is runFixtureCase for fixtures whose bundle needs
// a schema the archive does not ship with. Bundles load at Open, so the
// TaxonWorks round-trip columns are added to a fresh archive and the
// archive is reopened before the case runs; without them the tw_ext
// bundles are skipped and no rule could fire.
func runSchemaFixtureCase(t *testing.T, ruleID, schema string, c FixtureCase) int {
	t.Helper()
	if schema == "" {
		return runFixtureCase(t, ruleID, c)
	}
	if schema != "tw_ext" {
		t.Fatalf("fixture %s: unknown schema %q", ruleID, schema)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fixture.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ApplyTaxonWorksExtension(ctx, a); err != nil {
		t.Fatalf("ApplyTaxonWorksExtension: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	a, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a.Close()
	if skips := a.SkippedRulesets(); len(skips) > 0 {
		t.Fatalf("rulesets skipped after applying the extension: %v", skips)
	}
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

// TestTaxonWorksExtensionSkipped checks the loud path: a plain archive
// cannot satisfy the tw_ext bundles, so they are reported as skipped
// with the columns that are missing, and their rules do not run.
func TestTaxonWorksExtensionSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()
	skips := a.SkippedRulesets()
	if len(skips) == 0 {
		t.Fatal("no rulesets skipped on a plain archive; expected the tw_ext bundles")
	}
	for _, s := range skips {
		if !strings.HasPrefix(s.Ruleset, "tw_ext") {
			t.Errorf("unexpected skip %q", s.Ruleset)
		}
		if s.Rules == 0 || len(s.MissingColumns)+len(s.MissingTables) == 0 {
			t.Errorf("skip %q says nothing useful: %+v", s.Ruleset, s)
		}
		if !strings.Contains(s.Summary(), "did not run") {
			t.Errorf("skip summary is not explicit: %q", s.Summary())
		}
	}
}
