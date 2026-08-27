package hive

import (
	"context"
	"path/filepath"
	"testing"
)

// TestReferenceDashboard covers the four step-completion cases plus
// the three attention buckets. Table-driven seed of references at
// each stage; asserts the aggregate counts and the attention rows
// that should surface.
func TestReferenceDashboard(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dashboard.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	// Seed refs across all four workflow stages.
	seeds := []struct {
		id, author, issued, title string
		attachPDF                 bool
		addAssertion              bool
		importAssertion           bool
	}{
		// bib incomplete (missing author) — stuck below step 1
		{"r_stub", "", "2020", "Some title", false, false, false},
		// step 1 done, needs digitize
		{"r_bib_only", "Doe", "2020", "T1", false, false, false},
		// step 2 done, needs extraction
		{"r_digitized", "Doe", "2021", "T2", true, false, false},
		// step 3 done, needs review
		{"r_extracted", "Doe", "2022", "T3", true, true, false},
		// step 4 done
		{"r_imported", "Doe", "2023", "T4", true, true, true},
	}
	for _, s := range seeds {
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO reference (col__id, col__author, col__issued, col__title, col__citation)
			 VALUES (?, ?, ?, ?, ?)`,
			s.id, s.author, s.issued, s.title, "cite",
		); err != nil {
			t.Fatalf("insert %s: %v", s.id, err)
		}
		if s.attachPDF {
			if _, err := a.db.ExecContext(ctx,
				`INSERT INTO hive__reference_pdf (reference_id, sha512, attached_at, attached_by)
				 VALUES (?, ?, datetime('now'), 'test')`,
				s.id, s.id+"-sha",
			); err != nil {
				t.Fatalf("attach %s: %v", s.id, err)
			}
		}
		if s.addAssertion {
			importedAt := any(nil)
			if s.importAssertion {
				importedAt = "2026-01-01T00:00:00Z"
			}
			if _, err := a.db.ExecContext(ctx,
				`INSERT INTO hive__reference_assertion
				 (assertion_id, reference_id, assertion_type, payload, origin, status, imported_at, created_at, updated_at, created_by)
				 VALUES (?, ?, 'taxon', '{}', 'model', 'pending', ?, datetime('now'), datetime('now'), 'model:v1')`,
				s.id+"-a1", s.id, importedAt,
			); err != nil {
				t.Fatalf("assertion %s: %v", s.id, err)
			}
		}
	}

	d, err := a.ReferenceDashboard(ctx)
	if err != nil {
		t.Fatalf("ReferenceDashboard: %v", err)
	}

	// 5 refs total.
	if d.Total != 5 {
		t.Errorf("Total = %d, want 5", d.Total)
	}
	// bib done: r_bib_only, r_digitized, r_extracted, r_imported = 4
	if d.Steps.Bibliographic.Done != 4 {
		t.Errorf("Bibliographic.Done = %d, want 4", d.Steps.Bibliographic.Done)
	}
	// digitized done: r_digitized, r_extracted, r_imported = 3
	if d.Steps.Digitized.Done != 3 {
		t.Errorf("Digitized.Done = %d, want 3", d.Steps.Digitized.Done)
	}
	// assertions done: r_extracted, r_imported = 2
	if d.Steps.AssertionsExtracted.Done != 2 {
		t.Errorf("AssertionsExtracted.Done = %d, want 2", d.Steps.AssertionsExtracted.Done)
	}
	// imported done: r_imported = 1
	if d.Steps.Imported.Done != 1 {
		t.Errorf("Imported.Done = %d, want 1", d.Steps.Imported.Done)
	}

	// Attention:
	//  needs digitize: r_stub + r_bib_only = 2
	//  needs extract:  r_digitized = 1
	//  needs review:   r_extracted = 1
	wantAttention := map[int]int{2: 2, 3: 1, 4: 1}
	if len(d.Attention) != 3 {
		t.Fatalf("Attention count = %d, want 3", len(d.Attention))
	}
	for _, a := range d.Attention {
		want, ok := wantAttention[a.Step]
		if !ok {
			t.Errorf("unexpected attention step %d", a.Step)
			continue
		}
		if a.Count != want {
			t.Errorf("Attention[step=%d].Count = %d, want %d", a.Step, a.Count, want)
		}
	}
}

// TestReferenceDashboardEmpty confirms zero-rows returns zero
// counts (not an error from SUM-over-empty-set NULL handling).
func TestReferenceDashboardEmpty(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dashboard-empty.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	d, err := a.ReferenceDashboard(ctx)
	if err != nil {
		t.Fatalf("ReferenceDashboard: %v", err)
	}
	if d.Total != 0 || d.Steps.Bibliographic.Done != 0 || len(d.Attention) != 0 {
		t.Errorf("empty archive should return zero counts and empty attention, got %+v", d)
	}
}
