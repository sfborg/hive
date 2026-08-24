package hive

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sfborg/hive/pkg/sidecar"
)

// TestJATSValidationRule covers the developer-telemetry rule that
// flags references whose attached-PDF augmented-JATS sidecar fails
// schema validation. Three fixtures: valid JATS, invalid JATS, and
// no sidecar. Only the invalid case should produce a
// __gsvalidator_results row.
//
// Skipped when xmllint isn't installed — the rule silent-skips in
// that mode and there's nothing meaningful to assert.
func TestJATSValidationRule(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not installed; skipping JATS validation rule test")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jats-validation.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	shaValid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaInvalid := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaMissing := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	validJATS := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<article article-type="research-article" dtd-version="1.3" xml:lang="en">
  <front>
    <journal-meta>
      <journal-id journal-id-type="publisher-id">hive-unknown</journal-id>
      <issn publication-format="print">0000-0000</issn>
    </journal-meta>
    <article-meta>
      <title-group><article-title>Valid</article-title></title-group>
    </article-meta>
  </front>
  <body><p>Body text.</p></body>
</article>`)
	invalidJATS := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<article article-type="research-article" dtd-version="1.3" xml:lang="en">
  <front>
    <journal-meta>
      <journal-id journal-id-type="publisher-id">hive-unknown</journal-id>
      <issn publication-format="print">0000-0000</issn>
    </journal-meta>
    <article-meta>
      <title-group><article-title>Invalid</article-title></title-group>
    </article-meta>
  </front>
  <body><wrong-element>not a JATS element</wrong-element></body>
</article>`)

	if err := sidecar.Store(path, shaValid, "jats.xml", validJATS); err != nil {
		t.Fatalf("store valid sidecar: %v", err)
	}
	if err := sidecar.Store(path, shaInvalid, "jats.xml", invalidJATS); err != nil {
		t.Fatalf("store invalid sidecar: %v", err)
	}

	rows := []struct {
		id       string
		sha      string
		wantFlag bool
	}{
		{"ref-valid", shaValid, false},
		{"ref-invalid", shaInvalid, true},
		{"ref-nosidecar", shaMissing, false}, // sha attached but no sidecar on disk → skip
		{"ref-nopdf", "", false},              // no attached PDF at all → skip
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
			   AND rule_id = 'hive_jats_validation'`,
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
