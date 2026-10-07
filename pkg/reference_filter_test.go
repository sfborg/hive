package hive

import (
	"context"
	"slices"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// seedReferenceFilterFixtures creates references at each workflow
// stage the ReferenceFilter values distinguish.
func seedReferenceFilterFixtures(t *testing.T, a *Archive, ctx context.Context) {
	t.Helper()
	err := a.WithTx(ctx, func(tx *Tx) error {
		for _, r := range []struct{ id, title string }{
			{"ref-none", "No runs"},
			{"ref-runonly", "Run without assertions"},
			{"ref-accepted", "Accepted assertion"},
			{"abcdef01-0000-4000-8000-000000000001", "Pending assertion"},
			{"ref-deleted", "Assertion from deleted run"},
			{"ref-curator", "Curator assertion"},
		} {
			if _, err := tx.CreateReference(coldp.Reference{
				ID: r.id, Author: "Doe, J.", Title: r.title, Issued: "2025",
			}); err != nil {
				return err
			}
		}
		for _, run := range []struct{ id, ref string }{
			{"run-runonly", "ref-runonly"},
			{"run-accepted", "ref-accepted"},
			{"run-pending", "abcdef01-0000-4000-8000-000000000001"},
			{"run-deleted", "ref-deleted"},
		} {
			if _, err := tx.CreateExtractionRun(ExtractionRun{
				RunID: run.id, ReferenceID: run.ref, Purpose: "compose",
				SourceKind: "llm", SourceID: "test",
			}); err != nil {
				return err
			}
		}
		if err := tx.SoftDeleteExtractionRun("run-deleted"); err != nil {
			return err
		}
		for _, as := range []struct{ ref, run, origin string }{
			{"ref-accepted", "run-accepted", "model"},
			{"abcdef01-0000-4000-8000-000000000001", "run-pending", "model"},
			{"ref-deleted", "run-deleted", "model"},
			{"ref-curator", "", "curator"},
		} {
			id, err := tx.CreateAssertion(ReferenceAssertion{
				ReferenceID: as.ref, AssertionType: "dwc:scientificName",
				Payload: `{"text":"X"}`, Origin: as.origin, SourceRunID: as.run,
			})
			if err != nil {
				return err
			}
			if as.ref == "ref-accepted" {
				if err := tx.UpdateAssertionStatus(id, "accepted", ""); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func hitIDs(hits []ReferenceHit) []string {
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	slices.Sort(ids)
	return ids
}

func TestListReferencesPageFilter(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedReferenceFilterFixtures(t, a, ctx)

	tests := []struct {
		filter ReferenceFilter
		want   []string
	}{
		{ReferenceFilterNone, []string{
			"abcdef01-0000-4000-8000-000000000001", "ref-accepted",
			"ref-curator", "ref-deleted", "ref-none", "ref-runonly",
		}},
		{ReferenceFilterRuns, []string{
			"abcdef01-0000-4000-8000-000000000001", "ref-accepted", "ref-runonly",
		}},
		{ReferenceFilterAssertions, []string{
			"abcdef01-0000-4000-8000-000000000001", "ref-accepted", "ref-curator",
		}},
		{ReferenceFilterPending, []string{
			"abcdef01-0000-4000-8000-000000000001", "ref-curator",
		}},
	}
	for _, tc := range tests {
		t.Run(string(tc.filter), func(t *testing.T) {
			hits, total, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, tc.filter)
			if err != nil {
				t.Fatalf("ListReferencesPage: %v", err)
			}
			if got := hitIDs(hits); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
			if total != len(tc.want) {
				t.Errorf("total = %d, want %d", total, len(tc.want))
			}
		})
	}
}

func TestSearchReferencesFilterAndIDPrefix(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedReferenceFilterFixtures(t, a, ctx)

	hits, err := a.SearchReferences(ctx, "assertion", 50, ReferenceFilterPending)
	if err != nil {
		t.Fatalf("SearchReferences: %v", err)
	}
	want := []string{"abcdef01-0000-4000-8000-000000000001", "ref-curator"}
	if got := hitIDs(hits); !slices.Equal(got, want) {
		t.Errorf("filtered search ids = %v, want %v", got, want)
	}

	hits, err = a.SearchReferences(ctx, "ABCDEF01", 50, ReferenceFilterNone)
	if err != nil {
		t.Fatalf("SearchReferences: %v", err)
	}
	if got := hitIDs(hits); !slices.Equal(got, want[:1]) {
		t.Errorf("id-prefix search ids = %v, want %v", got, want[:1])
	}

	// Queries shorter than minIDPrefixLen do not match on ID.
	hits, err = a.SearchReferences(ctx, "abcdef", 50, ReferenceFilterNone)
	if err != nil {
		t.Fatalf("SearchReferences: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("short query matched ids %v, want none", hitIDs(hits))
	}
}

func TestReferenceWorkflowStates(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	seedReferenceFilterFixtures(t, a, ctx)

	tests := []struct {
		id   string
		want ReferenceWorkflow
	}{
		{"ref-none", ReferenceWorkflow{WorkflowDone, WorkflowNone, WorkflowNone, WorkflowNone}},
		{"ref-accepted", ReferenceWorkflow{WorkflowDone, WorkflowNone, WorkflowDone, WorkflowNone}},
		{"abcdef01-0000-4000-8000-000000000001", ReferenceWorkflow{WorkflowDone, WorkflowNone, WorkflowPartial, WorkflowNone}},
		// Assertions from a soft-deleted run do not count.
		{"ref-deleted", ReferenceWorkflow{WorkflowDone, WorkflowNone, WorkflowNone, WorkflowNone}},
		{"ref-missing", ReferenceWorkflow{WorkflowNone, WorkflowNone, WorkflowNone, WorkflowNone}},
	}
	for _, tc := range tests {
		got, err := a.ReferenceWorkflowFor(ctx, tc.id)
		if err != nil {
			t.Fatalf("ReferenceWorkflowFor(%s): %v", tc.id, err)
		}
		if got != tc.want {
			t.Errorf("%s: workflow = %+v, want %+v", tc.id, got, tc.want)
		}
	}

	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceFilterNone)
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	for _, h := range hits {
		if h.Workflow.Bibliographic == "" {
			t.Errorf("hit %s has no workflow state", h.ID)
		}
	}
}

func TestListReferencesPageSortYear(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	err := a.WithTx(ctx, func(tx *Tx) error {
		for _, r := range []struct{ id, issued string }{
			{"r-2019", "2019"}, {"r-undated", ""}, {"r-2024", "2024-05"},
		} {
			if _, err := tx.CreateReference(coldp.Reference{
				ID: r.id, Author: "Doe, J.", Title: r.id, Issued: r.issued,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortYear, ReferenceFilterNone)
	if err != nil {
		t.Fatalf("ListReferencesPage: %v", err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.ID)
	}
	want := []string{"r-2024", "r-2019", "r-undated"}
	if !slices.Equal(got, want) {
		t.Errorf("year order = %v, want %v", got, want)
	}
}
