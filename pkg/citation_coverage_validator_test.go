package hive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sfborg/hive/pkg/refmanager/sidecar"
)

// TestFindMissingFigTableLabels exercises the pure body-vs-caption
// diff on hand-built JATS fragments. Covers the shapes we observe
// in real GROBID output: xref citations, unwrapped "Figure N"
// prose, and the caption-self-reference case that must not be
// double-counted as a citation of the fig itself.
func TestFindMissingFigTableLabels(t *testing.T) {
	tests := []struct {
		name string
		jats string
		want []string
	}{
		{
			name: "all cited figures present",
			jats: `<article><body>
<p>See <xref rid="fig_0">Figure 1</xref> and figure 2.</p>
<fig id="fig_0"><caption><p>Figure 1. First fig.</p></caption></fig>
<fig id="fig_1"><caption><p>Figure 2. Second fig.</p></caption></fig>
</body></article>`,
			want: nil,
		},
		{
			name: "cited but missing figure",
			jats: `<article><body>
<p>See figure 1 and figure 3.</p>
<fig id="fig_0"><caption><p>Figure 1. Present.</p></caption></fig>
</body></article>`,
			want: []string{"Figure 3"},
		},
		{
			name: "cited but missing table",
			jats: `<article><body>
<p>Data in table 1 and table 2.</p>
<table-wrap id="tab_0"><caption><p>Table 1. Present.</p></caption></table-wrap>
</body></article>`,
			want: []string{"Table 2"},
		},
		{
			name: "sub-panel xref counts as parent figure",
			jats: `<article><body>
<p>See <xref rid="fig_0">1 A1</xref>.</p>
<fig id="fig_0"><caption><p>Figure 1. Parent.</p></caption></fig>
</body></article>`,
			want: nil,
		},
		{
			name: "caption self-reference not counted",
			jats: `<article><body>
<fig id="fig_2"><caption><p>Figure 3. Third fig described.</p></caption></fig>
</body></article>`,
			want: nil,
		},
		{
			name: "multiple missing sorted",
			jats: `<article><body>
<p>Figure 5 and table 3 and figure 4.</p>
</body></article>`,
			want: []string{"Figure 4", "Figure 5", "Table 3"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findMissingFigTableLabels([]byte(tc.jats))
			if !stringSliceEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCitationCoverageRule runs the validator end-to-end via
// ReindexValidation, mirroring the JATS-validation test's shape.
// Verifies that a reference whose sidecar cites a missing figure
// gets exactly one __gsvalidator_results row.
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
