package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	hive "github.com/sfborg/hive/pkg"
)

// apiTestSetStatus is the response shape for GET
// /api/reference/{id}/test-set. When InTestSet is false, the other
// fields are zero — the WUI's checkbox binds to InTestSet alone.
type apiTestSetStatus struct {
	InTestSet bool       `json:"in_test_set"`
	AddedAt   *time.Time `json:"added_at,omitempty"`
	AddedBy   string     `json:"added_by,omitempty"`
	Notes     string     `json:"notes,omitempty"`
}

// apiTestSetAddRequest is POST /api/reference/{id}/test-set body.
// Notes is optional. When absent on re-add, the existing notes on
// a previous membership row are preserved (see [hive.Tx.AddToTestSet]).
type apiTestSetAddRequest struct {
	Notes string `json:"notes,omitempty"`
}

// handleGetTestSetStatus returns whether the reference is in the
// evaluation test set + the audit fields when it is. WUI binds a
// checkbox to the in_test_set field.
func (s *server) handleGetTestSetStatus(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	m, err := s.a.GetTestSetMembership(r.Context(), refID)
	if err != nil {
		if errors.Is(err, hive.ErrNotFound) {
			// Not in the set — return the shape with a false flag,
			// not a 404. The checkbox needs a definite answer, and
			// "not in set" is a normal state, not an error.
			writeJSON(w, http.StatusOK, apiTestSetStatus{InTestSet: false})
			return
		}
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apiTestSetStatus{
		InTestSet: true,
		AddedAt:   &m.AddedAt,
		AddedBy:   m.AddedBy,
		Notes:     m.Notes,
	})
}

// handleAddToTestSet inserts (or updates) a membership row.
// Idempotent — the underlying accessor upserts, so a WUI
// double-click doesn't error. Returns the fresh status.
func (s *server) handleAddToTestSet(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	var body apiTestSetAddRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeBadRequest(w, r, "invalid JSON body: "+err.Error())
			return
		}
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.AddToTestSet(refID, body.Notes)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	s.handleGetTestSetStatus(w, r)
}

// handleRemoveFromTestSet drops the membership row. Idempotent —
// removing an already-absent reference returns 204 so a WUI
// uncheck-when-not-checked doesn't produce a confusing 404.
func (s *server) handleRemoveFromTestSet(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.RemoveFromTestSet(refID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiTestSetMember is one member of the test set with its audit
// fields. Sorted client-side; the endpoint returns oldest-first
// (which is the natural work-queue order — "papers we added
// earliest still need annotation").
type apiTestSetMember struct {
	ReferenceID string    `json:"reference_id"`
	AddedAt     time.Time `json:"added_at"`
	AddedBy     string    `json:"added_by,omitempty"`
	Notes       string    `json:"notes,omitempty"`
}

type apiTestSetList struct {
	Items []apiTestSetMember `json:"items"`
}

// handleListTestSet returns the full test-set roster. Backs the
// curator work-queue view: "papers marked as evaluation set" plus
// derivable status ("has a gold run yet?" via a join in a future
// endpoint).
func (s *server) handleListTestSet(w http.ResponseWriter, r *http.Request) {
	members, err := s.a.ListTestSetMembers(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiTestSetMember, 0, len(members))
	for _, m := range members {
		items = append(items, apiTestSetMember{
			ReferenceID: m.ReferenceID,
			AddedAt:     m.AddedAt,
			AddedBy:     m.AddedBy,
			Notes:       m.Notes,
		})
	}
	writeJSON(w, http.StatusOK, apiTestSetList{Items: items})
}
