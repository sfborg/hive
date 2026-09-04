package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/hive/pkg/refmanager/sidecar"
)

// TestCitationCoverageRule runs the validator end-to-end via
// ReindexValidation, mirroring the JATS-validation test's shape.
// Verifies that a reference whose sidecar cites a missing figure
// gets exactly one __gsvalidator_results row.
//
// Lives in pkg root (not pkg/refmanager) because it drives the
// public Archive API (Create, ReindexValidation, and the db handle)
// end-to-end. The pure body-vs-caption diff has its own unit test
// in pkg/refmanager/citation_coverage_validator_test.go.
func TestCitationCoverageRule(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "citation-coverage.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	shaComplete := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaMissing := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	complete := []byte(`<article><body>
<p>See <xref rid="fig_0">Figure 1</xref>.</p>
<fig id="fig_0"><caption><p>Figure 1. Present.</p></caption></fig>
</body></article>`)
	missing := []byte(`<article><body>
<p>See figure 1 and figure 3.</p>
<fig id="fig_0"><caption><p>Figure 1. Present.</p></caption></fig>
</body></article>`)

	if err := sidecar.Store(path, shaComplete, "jats.xml", complete); err != nil {
		t.Fatalf("store complete sidecar: %v", err)
	}
	if err := sidecar.Store(path, shaMissing, "jats.xml", missing); err != nil {
		t.Fatalf("store missing sidecar: %v", err)
	}

	rows := []struct {
		id       string
		sha      string
		wantFlag bool
	}{
		{"ref-complete", shaComplete, false},
		{"ref-missing", shaMissing, true},
		{"ref-nopdf", "", false},
	}
	for _, r := range rows {
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO reference (col__id, col__citation) VALUES (?, ?)`,
			r.id, "some citation",
		); err != nil {
			t.Fatalf("insert reference %q: %v", r.id, err)
		}
		if r.sha != "" {
			if _, err := a.db.ExecContext(ctx,
				`INSERT INTO hive__reference_pdf (reference_id, sha512, attached_at, attached_by)
				 VALUES (?, ?, datetime('now'), 'test')`,
				r.id, r.sha,
			); err != nil {
				t.Fatalf("attach pdf to %q: %v", r.id, err)
			}
		}
	}

	if err := a.ReindexValidation(ctx, nil); err != nil {
		t.Fatalf("ReindexValidation: %v", err)
	}

	for _, r := range rows {
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM __gsvalidator_results
			 WHERE table_name = 'reference'
			   AND record_id = ?
			   AND rule_id = 'hive_citation_coverage'`,
			r.id,
		).Scan(&n); err != nil {
			t.Fatalf("query results for %q: %v", r.id, err)
		}
		got := n > 0
		if got != r.wantFlag {
			t.Errorf("reference %q: got flagged=%v, want=%v", r.id, got, r.wantFlag)
		}
	}
}
