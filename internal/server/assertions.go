package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/correction"
)

// apiAssertion is the wire shape of a hive.ReferenceAssertion.
// Payload and SourcePtr are json.RawMessage passthroughs — the
// stored strings are already JSON, no reparse needed.
type apiAssertion struct {
	AssertionID   string          `json:"assertion_id"`
	ReferenceID   string          `json:"reference_id"`
	AssertionType string          `json:"assertion_type"`
	Payload       json.RawMessage `json:"payload,omitempty" swaggertype:"object"`
	SourcePtr     json.RawMessage `json:"source_ptr,omitempty" swaggertype:"object"`
	Origin        string          `json:"origin"`
	Status        string          `json:"status"`
	RejectReason  string          `json:"reject_reason,omitempty"`
	ImportedAt    *time.Time      `json:"imported_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	CreatedBy     string          `json:"created_by,omitempty"`
	SourceRunID   string          `json:"source_run_id,omitempty"`
	AssertedBy    string          `json:"asserted_by,omitempty"`
	// LatestCorrection surfaces the newest active assertion-edit
	// correction (if any) so the WUI can render the corrected
	// value + author + timestamp without a second fetch. Older
	// corrections are still retrievable from the corrections list
	// when curators want to see history.
	LatestCorrection *apiAssertionCorrection `json:"latest_correction,omitempty"`
}

// apiAssertionCorrection is the compact wire shape for one
// assertion-edit correction. Mirrors correction.AssertionEditPayload
// but adds provenance fields (who + when + correction_id) that live
// on the correction row, not the payload.
type apiAssertionCorrection struct {
	CorrectionID string    `json:"correction_id"`
	Corrected    string    `json:"corrected"`
	Original     string    `json:"original,omitempty"`
	Field        string    `json:"field,omitempty"`
	Notes        string    `json:"notes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
}

type apiAssertionList struct {
	Items []apiAssertion `json:"items"`
}

func assertionToAPI(a hive.ReferenceAssertion) apiAssertion {
	api := apiAssertion{
		AssertionID:   a.AssertionID,
		ReferenceID:   a.ReferenceID,
		AssertionType: a.AssertionType,
		Origin:        a.Origin,
		Status:        a.Status,
		RejectReason:  a.RejectReason,
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
		CreatedBy:     a.CreatedBy,
		SourceRunID:   a.SourceRunID,
		AssertedBy:    a.AssertedBy,
	}
	if a.Payload != "" {
		api.Payload = json.RawMessage(a.Payload)
	}
	if a.SourcePtr != "" {
		api.SourcePtr = json.RawMessage(a.SourcePtr)
	}
	if !a.ImportedAt.IsZero() {
		t := a.ImportedAt
		api.ImportedAt = &t
	}
	return api
}

// handleListReferenceAssertions returns per-fact staged assertions
// for a reference. Query params filter the result:
//
//	?status=pending&status=accepted   — multi-select status filter
//	?source_run_id=<uuid>             — one run only
//	?asserted_by=<label>              — one asserter only (view filter)
//
// Empty filter returns everything for the reference, oldest first.
// Backs the paper-level assertion-review UI and, via the
// asserted_by filter, the multi-view comparison rendering.
//
// @Summary      List staged assertions for a reference
// @Description  Per-fact staged assertions for a reference, filterable by status (repeatable), source_run_id, and asserted_by. Includes latest active edit-correction inline.
// @Tags         assertions
// @Produce      json
// @Param        id path string true "Reference ID"
// @Param        status query []string false "Status filter (pending|accepted|rejected|edited, repeatable)"
// @Param        source_run_id query string false "Filter by extraction run"
// @Param        asserted_by query string false "Filter by asserter label"
// @Success      200 {object} apiAssertionList
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/assertions [get]
func (s *server) handleListReferenceAssertions(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	q := r.URL.Query()
	f := hive.AssertionListFilter{
		ReferenceID: refID,
		Status:      q["status"],
		SourceRunID: q.Get("source_run_id"),
		AssertedBy:  q.Get("asserted_by"),
	}
	rows, err := s.a.ListAssertions(r.Context(), f)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Attach the newest active assertion-edit correction to each
	// assertion (if any) so the WUI can render corrected values +
	// author + timestamp without a follow-up fetch. Silent if the
	// corrections lookup errors — the assertions themselves still
	// render fine without correction metadata.
	latestByAssertion := s.latestAssertionCorrections(r, refID)
	items := make([]apiAssertion, 0, len(rows))
	for _, row := range rows {
		api := assertionToAPI(row)
		if c, ok := latestByAssertion[row.AssertionID]; ok {
			api.LatestCorrection = c
		}
		items = append(items, api)
	}
	writeJSON(w, http.StatusOK, apiAssertionList{Items: items})
}

// latestAssertionCorrections returns a per-assertion map of the
// newest active assertion-edit correction for the given reference.
// Empty map on error — this is a display convenience, not a
// correctness-critical join.
func (s *server) latestAssertionCorrections(
	r *http.Request, refID string,
) map[string]*apiAssertionCorrection {
	out := map[string]*apiAssertionCorrection{}
	corrs, err := s.a.ListCorrectionsForReference(r.Context(), refID)
	if err != nil {
		return out
	}
	for i := range corrs {
		c := corrs[i]
		if c.Kind != correction.KindAssertionEdit {
			continue
		}
		if c.Status != correction.StatusActive {
			continue
		}
		if c.TargetElementID == "" {
			continue
		}
		// ListCorrectionsForReference returns oldest-first, so a
		// later iteration always overwrites earlier — we end up
		// with the most recent per assertion by natural order.
		var p correction.AssertionEditPayload
		if c.Payload != "" {
			_ = json.Unmarshal([]byte(c.Payload), &p)
		}
		out[c.TargetElementID] = &apiAssertionCorrection{
			CorrectionID: c.ID,
			Corrected:    p.Corrected,
			Original:     p.Original,
			Field:        p.Field,
			Notes:        p.Notes,
			CreatedAt:    c.CreatedAt,
			CreatedBy:    c.CreatedBy,
		}
	}
	return out
}

// apiAssertionStatusPatch flips an assertion's review status.
// RejectReason is required when status = "rejected"; ignored
// otherwise. Bad status → 400. Missing assertion → 404.
type apiAssertionStatusPatch struct {
	Status       string `json:"status"`
	RejectReason string `json:"reject_reason,omitempty"`
}

// handlePatchAssertionStatus updates the status column and
// (optionally) reject_reason. Curator review flow: iterate the
// list, hit accept / reject / edit per row.
//
// @Summary      Patch assertion status
// @Description  Flip an assertion's review status (pending/accepted/rejected/edited). reject_reason required when status is 'rejected'.
// @Tags         assertions
// @Accept       json
// @Produce      json
// @Param        id path string true "Assertion ID"
// @Param        body body apiAssertionStatusPatch true "Status patch"
// @Success      200 {object} apiAssertion
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /assertion/{id}/status [patch]
func (s *server) handlePatchAssertionStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "assertion id is required")
		return
	}
	var body apiAssertionStatusPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	switch body.Status {
	case "pending", "accepted", "rejected", "edited":
		// valid
	default:
		writeBadRequest(w, r, "status must be one of: pending, accepted, rejected, edited")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.UpdateAssertionStatus(id, body.Status, body.RejectReason)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Return the updated row for the WUI's optimistic-render verify.
	fresh, err := s.a.GetAssertion(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, assertionToAPI(*fresh))
}

// apiAssertionCorrectionPost is the wire body for POST
// /api/assertion/{id}/correction — the curator's fix to one
// NER-extracted field. Field is the compose-output slot name
// (e.g. "verbatimDate", "country") the correction applies to;
// blank when the assertion type unambiguously identifies the
// field. Notes captures the curator's rationale when they choose
// to record one.
type apiAssertionCorrectionPost struct {
	Corrected string `json:"corrected"`
	Field     string `json:"field,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

// handlePostAssertionCorrection creates an assertion-edit
// correction row for the assertion and flips the assertion's
// status to 'edited' in the same transaction. The correction row
// is additive — the original NER value stays on the assertion so
// downstream training pipelines can build contrastive pairs.
//
// Returns the fresh assertion (with LatestCorrection attached)
// so the WUI can render the corrected value without a follow-up
// list fetch.
//
// @Summary      Create an assertion-edit correction
// @Description  Record a curator's fix to one NER-extracted field. The correction is additive; the original NER value stays on the assertion for training pipelines. Flips the assertion status to 'edited'.
// @Tags         assertions
// @Accept       json
// @Produce      json
// @Param        id path string true "Assertion ID"
// @Param        body body apiAssertionCorrectionPost true "Correction body"
// @Success      200 {object} apiAssertion
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /assertion/{id}/correction [post]
func (s *server) handlePostAssertionCorrection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "assertion id is required")
		return
	}
	var body apiAssertionCorrectionPost
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	// Fetch the assertion first so we can (a) verify it exists,
	// (b) capture the original value + type for the payload, and
	// (c) resolve the reference_id for the correction row.
	assertion, err := s.a.GetAssertion(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Extract the original value from the assertion payload —
	// ner_v5 shape carries {text, subject, evidence, ...}. We
	// treat payload.text as the model's extracted verbatim value
	// (which is what the curator is disagreeing with). Falls
	// back to empty when the payload doesn't parse.
	original := ""
	if assertion.Payload != "" {
		var p map[string]any
		if err := json.Unmarshal([]byte(assertion.Payload), &p); err == nil {
			if v, ok := p["text"].(string); ok {
				original = v
			}
		}
	}
	payloadBytes, err := json.Marshal(correction.AssertionEditPayload{
		AssertionType: assertion.AssertionType,
		Field:         body.Field,
		Original:      original,
		Corrected:     body.Corrected,
		Notes:         body.Notes,
	})
	if err != nil {
		writeProblem(w, r, fmt.Errorf("marshal correction payload: %w", err))
		return
	}
	c := &correction.Correction{
		ReferenceID:     assertion.ReferenceID,
		Kind:            correction.KindAssertionEdit,
		ContentType:     "application/vnd.hive.assertion-edit+json",
		TargetElementID: id,
		Payload:         string(payloadBytes),
		Status:          correction.StatusActive,
	}
	err = s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		if err := tx.CreateCorrection(c); err != nil {
			return err
		}
		return tx.UpdateAssertionStatus(id, "edited", "")
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetAssertion(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	api := assertionToAPI(*fresh)
	api.LatestCorrection = &apiAssertionCorrection{
		CorrectionID: c.ID,
		Corrected:    body.Corrected,
		Original:     original,
		Field:        body.Field,
		Notes:        body.Notes,
		CreatedAt:    c.CreatedAt,
		CreatedBy:    c.CreatedBy,
	}
	writeJSON(w, http.StatusOK, api)
}
