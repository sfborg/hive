package hive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCreateAssertionRoundTrip(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	var id string
	err := a.WithTx(ctx, func(tx *Tx) error {
		newID, err := tx.CreateAssertion(ReferenceAssertion{
			ReferenceID:   "ref-1",
			AssertionType: "dwc:scientificNameAuthorship",
			Payload:       `{"text":"Ruiz & Delgado, 2019"}`,
			Origin:        "model",
			CreatedBy:     "llm-run:test-uuid",
			SourceRunID:   "test-uuid",
			AssertedBy:    "llm-run:test-uuid",
		})
		id = newID
		return err
	})
	if err != nil {
		t.Fatalf("CreateAssertion: %v", err)
	}

	got, err := a.GetAssertion(ctx, id)
	if err != nil {
		t.Fatalf("GetAssertion: %v", err)
	}
	if got.ReferenceID != "ref-1" {
		t.Errorf("ReferenceID = %q", got.ReferenceID)
	}
	if got.AssertionType != "dwc:scientificNameAuthorship" {
		t.Errorf("AssertionType = %q", got.AssertionType)
	}
	if got.Status != "pending" {
		t.Errorf("default Status = %q, want pending", got.Status)
	}
	if got.SourceRunID != "test-uuid" {
		t.Errorf("SourceRunID = %q", got.SourceRunID)
	}
	if got.AssertedBy != "llm-run:test-uuid" {
		t.Errorf("AssertedBy = %q", got.AssertedBy)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not stamped")
	}
}

func TestListAssertionsFilters(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	err := a.WithTx(ctx, func(tx *Tx) error {
		// Two runs' worth of assertions on same reference; different
		// asserters + statuses so filter tests can distinguish.
		mkList := func(runID string, count int) error {
			for i := 0; i < count; i++ {
				if _, err := tx.CreateAssertion(ReferenceAssertion{
					ReferenceID:   "ref-many",
					AssertionType: "dwc:family",
					Payload:       `{}`,
					Origin:        "model",
					SourceRunID:   runID,
					AssertedBy:    "llm-run:" + runID,
				}); err != nil {
					return err
				}
			}
			return nil
		}
		if err := mkList("run-A", 3); err != nil {
			return err
		}
		if err := mkList("run-B", 2); err != nil {
			return err
		}
		// A curator-authored one, different asserter + status.
		_, err := tx.CreateAssertion(ReferenceAssertion{
			ReferenceID:   "ref-many",
			AssertionType: "dwc:family",
			Payload:       `{}`,
			Origin:        "curator",
			Status:        "accepted",
			CreatedBy:     "0000-0000-0000-1111",
			AssertedBy:    "0000-0000-0000-1111",
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// No filter: everything (6 rows).
	all, err := a.ListAssertions(ctx, AssertionListFilter{ReferenceID: "ref-many"})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 6 {
		t.Errorf("all = %d, want 6", len(all))
	}

	// By source_run_id.
	runB, _ := a.ListAssertions(ctx, AssertionListFilter{ReferenceID: "ref-many", SourceRunID: "run-B"})
	if len(runB) != 2 {
		t.Errorf("run-B = %d, want 2", len(runB))
	}

	// By asserted_by.
	curator, _ := a.ListAssertions(ctx, AssertionListFilter{
		ReferenceID: "ref-many",
		AssertedBy:  "0000-0000-0000-1111",
	})
	if len(curator) != 1 {
		t.Errorf("curator = %d, want 1", len(curator))
	}

	// By status.
	accepted, _ := a.ListAssertions(ctx, AssertionListFilter{
		ReferenceID: "ref-many",
		Status:      []string{"accepted"},
	})
	if len(accepted) != 1 {
		t.Errorf("accepted = %d, want 1", len(accepted))
	}
	pending, _ := a.ListAssertions(ctx, AssertionListFilter{
		ReferenceID: "ref-many",
		Status:      []string{"pending"},
	})
	if len(pending) != 5 {
		t.Errorf("pending = %d, want 5", len(pending))
	}
}

func TestUpdateAssertionStatus(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	var id string
	_ = a.WithTx(ctx, func(tx *Tx) error {
		newID, err := tx.CreateAssertion(ReferenceAssertion{
			ReferenceID:   "ref-status",
			AssertionType: "dwc:family",
			Payload:       `{}`,
			Origin:        "model",
			AssertedBy:    "llm-run:x",
		})
		id = newID
		return err
	})

	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateAssertionStatus(id, "rejected", "hallucinated — not in source")
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := a.GetAssertion(ctx, id)
	if got.Status != "rejected" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.RejectReason != "hallucinated — not in source" {
		t.Errorf("RejectReason = %q", got.RejectReason)
	}

	// Missing id returns ErrNotFound.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.UpdateAssertionStatus("does-not-exist", "rejected", "")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMaterializeAssertionsFromRun(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	// Realistic-shaped NER output subset. Includes the anchor field
	// so we exercise source_ptr population.
	nerOut := nerOutput{
		Assertions: []nerAssertion{
			{
				ID:         "a1",
				Type:       "dwc:family",
				Text:       "Testidae",
				Subject:    "Neotestus montanus",
				Evidence:   "stated",
				Confidence: "high",
				Anchor:     json.RawMessage(`{"before":"foo","text":"Testidae","after":"bar"}`),
			},
			{
				ID:         "a2",
				Type:       "dwc:country",
				Text:       "Spain",
				Subject:    "Neotestus montanus",
				Evidence:   "context_inferred",
				Confidence: "low",
				Scope:      "type_material",
			},
		},
		TagReviews: []map[string]any{
			{"tp_taxon_name_text": "Pterothorax", "verdict": "false_positive"},
		},
	}
	rawJSON, _ := json.Marshal(nerOut)

	var runID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateExtractionRun(ExtractionRun{
			ReferenceID:   "ref-materialize",
			Purpose:       "ner",
			SourceKind:    "llm",
			SourceID:      "anthropic-main",
			EnvelopeJSON:  "{}",
			RawOutputJSON: string(rawJSON),
		})
		runID = id
		return err
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}

	run, err := a.GetExtractionRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	var count int
	err = a.WithTx(ctx, func(tx *Tx) error {
		n, err := tx.MaterializeAssertionsFromRun(ctx, *run)
		count = n
		return err
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if count != 2 {
		t.Errorf("materialized %d rows, want 2 (tag_reviews should not count)", count)
	}

	// Assertions land linked to the run + tagged via asserted_by.
	linked, err := a.ListAssertions(ctx, AssertionListFilter{
		ReferenceID: "ref-materialize",
		SourceRunID: runID,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("linked = %d, want 2", len(linked))
	}
	for _, a := range linked {
		if a.AssertedBy != "llm-run:"+runID {
			t.Errorf("AssertedBy = %q, want llm-run:%s", a.AssertedBy, runID)
		}
		if a.Origin != "model" {
			t.Errorf("Origin = %q, want model", a.Origin)
		}
		if a.Status != "pending" {
			t.Errorf("Status = %q, want pending", a.Status)
		}
	}
	// First assertion has an anchor → source_ptr populated as JSON.
	if !strings.Contains(linked[0].SourcePtr, "before") {
		t.Errorf("expected anchor JSON in SourcePtr, got %q", linked[0].SourcePtr)
	}
}

func TestMaterializeIsIdempotent(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	raw, _ := json.Marshal(nerOutput{
		Assertions: []nerAssertion{
			{Type: "dwc:family", Text: "Testidae"},
			{Type: "dwc:genus", Text: "Neotestus"},
		},
	})

	var runID string
	_ = a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateExtractionRun(ExtractionRun{
			ReferenceID:   "ref-idem",
			Purpose:       "ner",
			SourceKind:    "llm",
			SourceID:      "test",
			RawOutputJSON: string(raw),
		})
		runID = id
		return err
	})
	run, _ := a.GetExtractionRun(ctx, runID)

	// First materialize.
	_ = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.MaterializeAssertionsFromRun(ctx, *run)
		return err
	})
	first, _ := a.ListAssertions(ctx, AssertionListFilter{
		ReferenceID: "ref-idem",
		SourceRunID: runID,
	})
	if len(first) != 2 {
		t.Fatalf("first materialize count = %d, want 2", len(first))
	}

	// Second materialize on same run: should REPLACE not accumulate.
	_ = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.MaterializeAssertionsFromRun(ctx, *run)
		return err
	})
	second, _ := a.ListAssertions(ctx, AssertionListFilter{
		ReferenceID: "ref-idem",
		SourceRunID: runID,
	})
	if len(second) != 2 {
		t.Errorf("second materialize count = %d, want 2 (replace not accumulate)", len(second))
	}
}

func TestMaterializeSkipsNonNERRuns(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	var runID string
	_ = a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateExtractionRun(ExtractionRun{
			ReferenceID:   "ref-compose",
			Purpose:       "compose",
			SourceKind:    "llm",
			SourceID:      "test",
			RawOutputJSON: `{"name_usages":[]}`,
		})
		runID = id
		return err
	})
	run, _ := a.GetExtractionRun(ctx, runID)

	var count int
	err := a.WithTx(ctx, func(tx *Tx) error {
		n, err := tx.MaterializeAssertionsFromRun(ctx, *run)
		count = n
		return err
	})
	if err != nil {
		t.Fatalf("materialize compose: %v", err)
	}
	if count != 0 {
		t.Errorf("compose materialize count = %d, want 0 (not implemented yet)", count)
	}
}

// TestParseNEROutputWrappingObject covers the ner_v5..v7 shape.
func TestParseNEROutputWrappingObject(t *testing.T) {
	raw := `{
	  "assertions": [
	    {"id":"a1","type":"dwc:family","text":"Sebdeniaceae","subject":"Crassitegula bouchetii","evidence":"stated","anchor":{"start":10,"end":22},"confidence":"high"},
	    {"id":"a2","type":"dwc:genus","text":"Crassitegula","subject":"Crassitegula bouchetii","evidence":"stated","anchor":{"start":24,"end":36},"confidence":"high"}
	  ],
	  "tag_reviews": []
	}`
	got, err := parseNEROutput(raw)
	if err != nil {
		t.Fatalf("parseNEROutput: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("assertions = %d, want 2", len(got))
	}
	if got[0].Type != "dwc:family" || got[1].Type != "dwc:genus" {
		t.Errorf("assertion types wrong: %+v", got)
	}
}

// TestParseNEROutputNDJSON covers the ner_v8 shape.
func TestParseNEROutputNDJSON(t *testing.T) {
	raw := `{"kind":"header","schema_version":"ner_v8"}
{"kind":"assertion","id":"a1","type":"dwc:family","text":"Sebdeniaceae","subject":"Crassitegula bouchetii","evidence":"stated","anchor":{"start":10,"end":22},"confidence":"high"}
{"kind":"assertion","id":"a2","type":"dwc:genus","text":"Crassitegula","subject":"Crassitegula bouchetii","evidence":"stated","anchor":{"start":24,"end":36},"confidence":"high"}
{"kind":"tag_review","tp_taxon_name_text":"Wettst.","verdict":"false_positive","confidence":"high","notes":"author abbreviation, not a taxon"}
`
	got, err := parseNEROutput(raw)
	if err != nil {
		t.Fatalf("parseNEROutput: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("assertions = %d, want 2 (header + tag_review should be filtered):\n%+v", len(got), got)
	}
	if got[0].ID != "a1" || got[1].ID != "a2" {
		t.Errorf("assertion ids wrong: %v, %v", got[0].ID, got[1].ID)
	}
}

// TestParseNEROutputNDJSONTruncationTolerant is the whole reason
// ner_v8 exists — a mid-object truncation of the last line must
// not lose the complete assertions before it.
func TestParseNEROutputNDJSONTruncationTolerant(t *testing.T) {
	raw := `{"kind":"header","schema_version":"ner_v8"}
{"kind":"assertion","id":"a1","type":"dwc:family","text":"Sebdeniaceae","subject":"Crassitegula bouchetii","evidence":"stated","anchor":{"start":10,"end":22},"confidence":"high"}
{"kind":"assertion","id":"a2","type":"dwc:genus","text":"Crassitegula","subject":"Crassitegula bouchetii","evidence":"stated","anchor":{"start":24,"end":36},"confidence":"high"}
{"kind":"assertion","id":"a3","type":"dwc:scientificNameAuthorship","text":"Wettst`
	got, err := parseNEROutput(raw)
	if err != nil {
		t.Fatalf("parseNEROutput: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("assertions = %d, want 2 complete lines (truncated a3 dropped)", len(got))
	}
	if got[0].ID != "a1" || got[1].ID != "a2" {
		t.Errorf("complete assertions wrong: %+v", got)
	}
}
