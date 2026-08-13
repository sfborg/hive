package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	hive "github.com/sfborg/hive/pkg"
)

// apiAgent is the wire shape of a hive.Agent. Role is a URL-path
// parameter rather than a body field so the client never has to
// keep the two in sync.
type apiAgent struct {
	ID           int    `json:"id"`
	Role         string `json:"role"`
	Orcid        string `json:"orcid,omitempty"`
	Given        string `json:"given,omitempty"`
	Family       string `json:"family,omitempty"`
	RorID        string `json:"rorid,omitempty"`
	Department   string `json:"department,omitempty"`
	Organisation string `json:"organisation,omitempty"`
	City         string `json:"city,omitempty"`
	State        string `json:"state,omitempty"`
	Country      string `json:"country,omitempty"`
	Email        string `json:"email,omitempty"`
	URL          string `json:"url,omitempty"`
	Note         string `json:"note,omitempty"`
}

// apiAgentPatch is the PATCH body. Pointer-optional fields carry
// the "leave alone vs. clear" distinction — a nil pointer means
// the field is untouched; a pointer to "" clears it. Same
// convention as apiMetadataPatch.
type apiAgentPatch struct {
	Orcid        *string `json:"orcid,omitempty"`
	Given        *string `json:"given,omitempty"`
	Family       *string `json:"family,omitempty"`
	RorID        *string `json:"rorid,omitempty"`
	Department   *string `json:"department,omitempty"`
	Organisation *string `json:"organisation,omitempty"`
	City         *string `json:"city,omitempty"`
	State        *string `json:"state,omitempty"`
	Country      *string `json:"country,omitempty"`
	Email        *string `json:"email,omitempty"`
	URL          *string `json:"url,omitempty"`
	Note         *string `json:"note,omitempty"`
}

// apiAgentList wraps a role's rows under an "items" key so the
// shape can grow later (totals, filters) without breaking clients.
type apiAgentList struct {
	Items []apiAgent `json:"items"`
}

func agentToAPI(a hive.Agent) apiAgent {
	return apiAgent{
		ID:           a.ID,
		Role:         string(a.Role),
		Orcid:        a.Orcid,
		Given:        a.Given,
		Family:       a.Family,
		RorID:        a.RorID,
		Department:   a.Department,
		Organisation: a.Organisation,
		City:         a.City,
		State:        a.State,
		Country:      a.Country,
		Email:        a.Email,
		URL:          a.URL,
		Note:         a.Note,
	}
}

func agentFromAPI(role hive.Role, a apiAgent) hive.Agent {
	return hive.Agent{
		ID:           a.ID,
		Role:         role,
		Orcid:        a.Orcid,
		Given:        a.Given,
		Family:       a.Family,
		RorID:        a.RorID,
		Department:   a.Department,
		Organisation: a.Organisation,
		City:         a.City,
		State:        a.State,
		Country:      a.Country,
		Email:        a.Email,
		URL:          a.URL,
		Note:         a.Note,
	}
}

// applyAgentPatch mutates ag in place per the patch. Absent key =
// leave alone; present value = set (including empty string for
// "clear"). Same convention as applyMetadataPatch.
func applyAgentPatch(ag *hive.Agent, p apiAgentPatch) {
	if p.Orcid != nil {
		ag.Orcid = *p.Orcid
	}
	if p.Given != nil {
		ag.Given = *p.Given
	}
	if p.Family != nil {
		ag.Family = *p.Family
	}
	if p.RorID != nil {
		ag.RorID = *p.RorID
	}
	if p.Department != nil {
		ag.Department = *p.Department
	}
	if p.Organisation != nil {
		ag.Organisation = *p.Organisation
	}
	if p.City != nil {
		ag.City = *p.City
	}
	if p.State != nil {
		ag.State = *p.State
	}
	if p.Country != nil {
		ag.Country = *p.Country
	}
	if p.Email != nil {
		ag.Email = *p.Email
	}
	if p.URL != nil {
		ag.URL = *p.URL
	}
	if p.Note != nil {
		ag.Note = *p.Note
	}
}

// parseRolePath extracts and validates the {role} URL parameter.
// Bad role → 400 with a helpful message listing accepted values;
// caller returns immediately.
func (s *server) parseRolePath(w http.ResponseWriter, r *http.Request) (hive.Role, bool) {
	roleStr := r.PathValue("role")
	role, err := hive.ParseRole(roleStr)
	if err != nil {
		accepted := make([]string, 0, len(hive.AllRoles))
		for _, r := range hive.AllRoles {
			accepted = append(accepted, string(r))
		}
		writeBadRequest(w, r, fmt.Sprintf(
			"unknown role %q (accepted: %v)", roleStr, accepted))
		return "", false
	}
	return role, true
}

// parseAgentIDPath extracts the numeric {id} URL parameter. Bad
// or zero id → 400.
func (s *server) parseAgentIDPath(w http.ResponseWriter, r *http.Request) (int, bool) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeBadRequest(w, r, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

// handleListAgents returns every row from the role's table.
// Ordering follows the pkg/ default (family, given, id).
func (s *server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	role, ok := s.parseRolePath(w, r)
	if !ok {
		return
	}
	agents, err := s.a.ListAgents(r.Context(), role)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiAgent, 0, len(agents))
	for _, a := range agents {
		items = append(items, agentToAPI(a))
	}
	writeJSON(w, http.StatusOK, apiAgentList{Items: items})
}

// handleGetAgent returns a single row by (role, id).
func (s *server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	role, ok := s.parseRolePath(w, r)
	if !ok {
		return
	}
	id, ok := s.parseAgentIDPath(w, r)
	if !ok {
		return
	}
	ag, err := s.a.GetAgent(r.Context(), role, id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, agentToAPI(*ag))
}

// handleCreateAgent inserts a new row. Request body is a full
// apiAgent (client authors every relevant field); the URL role
// wins if the body's role disagrees.
func (s *server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	role, ok := s.parseRolePath(w, r)
	if !ok {
		return
	}
	var body apiAgent
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ag := agentFromAPI(role, body)
	ag.ID = 0
	var newID int
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.CreateAgent(ag)
		if err != nil {
			return err
		}
		newID = id
		return nil
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetAgent(r.Context(), role, newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentToAPI(*fresh))
}

// handlePatchAgent applies a partial update. Fetches current row,
// applies the patch, then writes back — same shape as
// handlePatchMetadata so per-field null-vs-clear behavior stays
// consistent across the API.
func (s *server) handlePatchAgent(w http.ResponseWriter, r *http.Request) {
	role, ok := s.parseRolePath(w, r)
	if !ok {
		return
	}
	id, ok := s.parseAgentIDPath(w, r)
	if !ok {
		return
	}
	var patch apiAgentPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetAgent(r.Context(), role, id)
		if err != nil {
			return err
		}
		applyAgentPatch(current, patch)
		return tx.UpdateAgent(*current)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetAgent(r.Context(), role, id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, agentToAPI(*fresh))
}

// handleDeleteAgent removes a row. 204 on success.
func (s *server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	role, ok := s.parseRolePath(w, r)
	if !ok {
		return
	}
	id, ok := s.parseAgentIDPath(w, r)
	if !ok {
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteAgent(role, id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiCopyAgentRequest is the body for POST /api/agent/{role}/{id}/copy
// and .../move. Target role travels in the body rather than a query
// param so it can grow into a fuller request payload (per-field
// include/exclude, for example) without a URL-shape change.
type apiCopyAgentRequest struct {
	ToRole    string `json:"to_role"`
	BlankNote *bool  `json:"blank_note,omitempty"` // default true when absent
}

// handleCopyAgent creates a new agent in the target role from the
// source (URL role + id). Returns the newly-created agent.
func (s *server) handleCopyAgent(w http.ResponseWriter, r *http.Request) {
	s.handleCopyOrMoveAgent(w, r, false)
}

// handleMoveAgent reassigns an agent to a different role atomically
// (create target + delete source, one tx). Returns the newly-created
// agent in the target role; the source id is gone after this call.
func (s *server) handleMoveAgent(w http.ResponseWriter, r *http.Request) {
	s.handleCopyOrMoveAgent(w, r, true)
}

// handleCopyOrMoveAgent shares the request-body parsing and result
// shape between /copy and /move — they differ only in whether the
// source row survives.
func (s *server) handleCopyOrMoveAgent(w http.ResponseWriter, r *http.Request, move bool) {
	fromRole, ok := s.parseRolePath(w, r)
	if !ok {
		return
	}
	id, ok := s.parseAgentIDPath(w, r)
	if !ok {
		return
	}
	var body apiCopyAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	toRole, err := hive.ParseRole(body.ToRole)
	if err != nil {
		writeBadRequest(w, r, fmt.Sprintf("unknown to_role %q", body.ToRole))
		return
	}
	// Default: blank the note field, since it's role-specific.
	// Curator can override to preserve when the note applies uniformly.
	blank := true
	if body.BlankNote != nil {
		blank = *body.BlankNote
	}
	var newID int
	err = s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		if move {
			newID, err = tx.MoveAgentToRole(fromRole, id, toRole, blank)
		} else {
			newID, err = tx.CopyAgentToRole(fromRole, id, toRole, blank)
		}
		return err
	})
	if err != nil {
		if errors.Is(err, hive.ErrNotFound) {
			writeProblem(w, r, err)
			return
		}
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetAgent(r.Context(), toRole, newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentToAPI(*fresh))
}
