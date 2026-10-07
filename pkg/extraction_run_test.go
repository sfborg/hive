package hive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCreateAndGetExtractionRun(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	var runID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateExtractionRun(ExtractionRun{
			ReferenceID:   "ref-42",
			Purpose:       "ner",
			SourceKind:    "llm",
			SourceID:      "anthropic-main",
			EnvelopeJSON:  `{"prompt_name":"ner_v5"}`,
			RawOutputJSON: `{"assertions":[]}`,
			Notes:         "first run against dascilloidea-3",
		})
		if err != nil {
			return err
		}
		runID = id
		return nil
	})
	if err != nil {
		t.Fatalf("CreateExtractionRun: %v", err)
	}
	if runID == "" {
		t.Fatalf("expected minted UUID, got empty ID")
	}

	got, err := a.GetExtractionRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetExtractionRun: %v", err)
	}
	if got.ReferenceID != "ref-42" {
		t.Errorf("ReferenceID = %q", got.ReferenceID)
	}
	if got.Purpose != "ner" || got.SourceKind != "llm" || got.SourceID != "anthropic-main" {
		t.Errorf("purpose/kind/source_id: %+v", got)
	}
	if got.IsGoldStandard {
		t.Errorf("expected is_gold_standard=false by default")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not stamped")
	}
	if !got.DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be zero on fresh row")
	}
	if got.Notes != "first run against dascilloidea-3" {
		t.Errorf("Notes not persisted: %q", got.Notes)
	}
}

func TestGetExtractionRunMissingReturnsNotFound(t *testing.T) {
	a := newTestArchive(t)
	ctx := context.Background()
	_, err := a.GetExtractionRun(ctx, "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateExtractionRunRequiredFields(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	cases := []struct {
		name    string
		run     ExtractionRun
		wantMsg string
	}{
		{"no purpose", ExtractionRun{SourceKind: "llm", SourceID: "x"}, "purpose"},
		{"no source_kind", ExtractionRun{Purpose: "ner", SourceID: "x"}, "source_kind"},
		{"no source_id", ExtractionRun{Purpose: "ner", SourceKind: "llm"}, "source_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := a.WithTx(ctx, func(tx *Tx) error {
				_, err := tx.CreateExtractionRun(tc.run)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			}
		})
	}
}

func TestCreateExtractionRunRejectsBadPurpose(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	err := a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.CreateExtractionRun(ExtractionRun{
			Purpose:    "bogus",
			SourceKind: "llm",
			SourceID:   "x",
		})
		return err
	})
	if err == nil {
		t.Fatalf("expected CHECK constraint failure for purpose='bogus'")
	}
}

func TestListExtractionRunsByReferenceOrdersNewestFirst(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	// Insert three runs with monotonically increasing timestamps.
	// Explicit CreatedAt so ordering is deterministic under fast
	// consecutive inserts.
	now := time.Now().UTC()
	err := a.WithTx(ctx, func(tx *Tx) error {
		for i, delta := range []time.Duration{0, time.Second, 2 * time.Second} {
			_, err := tx.CreateExtractionRun(ExtractionRun{
				RunID:       "r" + string(rune('1'+i)),
				ReferenceID: "ref-order",
				Purpose:     "ner",
				SourceKind:  "llm",
				SourceID:    "anthropic-main",
				CreatedAt:   now.Add(delta),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	runs, err := a.ListExtractionRunsByReference(ctx, "ref-order", false)
	if err != nil {
		t.Fatalf("ListExtractionRunsByReference: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	if runs[0].RunID != "r3" || runs[1].RunID != "r2" || runs[2].RunID != "r1" {
		t.Errorf("wrong ordering: %v %v %v", runs[0].RunID, runs[1].RunID, runs[2].RunID)
	}
}

func TestSoftDeleteAndRestoreExtractionRun(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	var runID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateExtractionRun(ExtractionRun{
			ReferenceID: "ref-del",
			Purpose:     "ner",
			SourceKind:  "llm",
			SourceID:    "anthropic-main",
		})
		runID = id
		return err
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Soft-delete.
	err = a.WithTx(ctx, func(tx *Tx) error { return tx.SoftDeleteExtractionRun(runID) })
	if err != nil {
		t.Fatalf("SoftDeleteExtractionRun: %v", err)
	}

	// Excluded from default list.
	runs, _ := a.ListExtractionRunsByReference(ctx, "ref-del", false)
	if len(runs) != 0 {
		t.Errorf("deleted run leaked into default list: %+v", runs)
	}

	// Still visible with includeDeleted=true — cost stays queryable.
	runs, _ = a.ListExtractionRunsByReference(ctx, "ref-del", true)
	if len(runs) != 1 {
		t.Fatalf("includeDeleted got %d runs, want 1", len(runs))
	}
	if runs[0].DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be populated on soft-deleted row")
	}

	// Restore.
	err = a.WithTx(ctx, func(tx *Tx) error { return tx.RestoreExtractionRun(runID) })
	if err != nil {
		t.Fatalf("RestoreExtractionRun: %v", err)
	}
	runs, _ = a.ListExtractionRunsByReference(ctx, "ref-del", false)
	if len(runs) != 1 {
		t.Fatalf("restored run should appear in default list, got %d", len(runs))
	}
	if !runs[0].DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be cleared after restore")
	}
}

func TestSoftDeleteMissingRunReturnsNotFound(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	err := a.WithTx(ctx, func(tx *Tx) error { return tx.SoftDeleteExtractionRun("nope") })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSetExtractionRunGoldStandard(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	var runID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateExtractionRun(ExtractionRun{
			ReferenceID: "ref-gold",
			Purpose:     "ner",
			SourceKind:  "curator",
			SourceID:    "0000-0000-0000-0000",
		})
		runID = id
		return err
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = a.WithTx(ctx, func(tx *Tx) error { return tx.SetExtractionRunGoldStandard(runID, true) })
	if err != nil {
		t.Fatalf("SetExtractionRunGoldStandard true: %v", err)
	}
	got, _ := a.GetExtractionRun(ctx, runID)
	if !got.IsGoldStandard {
		t.Errorf("gold flag did not persist")
	}

	err = a.WithTx(ctx, func(tx *Tx) error { return tx.SetExtractionRunGoldStandard(runID, false) })
	if err != nil {
		t.Fatalf("SetExtractionRunGoldStandard false: %v", err)
	}
	got, _ = a.GetExtractionRun(ctx, runID)
	if got.IsGoldStandard {
		t.Errorf("gold flag did not clear")
	}
}
