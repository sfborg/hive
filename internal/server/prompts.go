package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/prompts"
)

// apiPrompt is the wire shape for one prompt (shipped built-in or
// curator fork). Content is included in GET-by-id responses but
// omitted from list responses so the picker payload stays lean.
type apiPrompt struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Purpose   string     `json:"purpose"`
	Source    string     `json:"source"` // "builtin" | "curator"
	ForkOf    string     `json:"fork_of,omitempty"`
	Content   string     `json:"content,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
}

type apiPromptList struct {
	Items []apiPrompt `json:"items"`
}

// handleListPrompts merges shipped built-ins (pkg/prompts) with
// curator forks (hive__prompt) into one list. Query params:
//
//	?purpose=ner            — restrict to one purpose
//	?include_content=1      — inline content bytes (default omit)
//
// Curator prompts appear first (newest first), then built-ins in
// name order — so the omnibox surfaces the curator's own forks
// above the shipped defaults without a separate section header.
//
// @Summary      List prompts
// @Description  Merged list of shipped built-in prompts and curator forks. Filter by purpose; include_content=1 inlines the prompt body.
// @Tags         prompts
// @Produce      json
// @Param        purpose query string false "Restrict to one purpose (ner|compose)"
// @Param        include_content query int false "Inline prompt content bytes (default omitted)"
// @Success      200 {object} apiPromptList
// @Failure      500 {object} problem
// @Router       /prompt [get]
func (s *server) handleListPrompts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	purposeFilter := q.Get("purpose")
	includeContent := q.Get("include_content") == "1" || q.Get("include_content") == "true"

	items := make([]apiPrompt, 0)

	curators, err := s.a.ListPrompts(r.Context(), hive.PromptListFilter{
		Purpose: purposeFilter,
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	for _, p := range curators {
		createdAt := p.CreatedAt
		item := apiPrompt{
			ID:        p.PromptID,
			Name:      p.Name,
			Purpose:   p.Purpose,
			Source:    "curator",
			ForkOf:    p.ForkOf,
			CreatedAt: &createdAt,
			CreatedBy: p.CreatedBy,
		}
		if includeContent {
			item.Content = p.Content
		}
		items = append(items, item)
	}

	builtins, err := prompts.Available()
	if err != nil {
		writeProblem(w, r, fmt.Errorf("list builtin prompts: %w", err))
		return
	}
	sort.Strings(builtins)
	for _, name := range builtins {
		purpose := purposeFromBuiltinName(name)
		if purpose == "" {
			// Unknown builtin naming convention — skip so the
			// picker doesn't offer prompts of ambiguous purpose.
			continue
		}
		if purposeFilter != "" && purposeFilter != purpose {
			continue
		}
		item := apiPrompt{
			ID:      name,
			Name:    name,
			Purpose: purpose,
			Source:  "builtin",
		}
		if includeContent {
			body, err := prompts.Load(name)
			if err == nil {
				item.Content = body
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, apiPromptList{Items: items})
}

// purposeFromBuiltinName classifies a shipped prompt name by its
// slug prefix — "ner_v6" → "ner", "compose_v2" → "compose".
// Kept as a naming convention rather than a manifest so adding a
// new versioned builtin is a single file drop under pkg/prompts/.
func purposeFromBuiltinName(name string) string {
	switch {
	case strings.HasPrefix(name, "ner_"):
		return "ner"
	case strings.HasPrefix(name, "compose_"):
		return "compose"
	default:
		return ""
	}
}

// loadBuiltinPrompt is a thin wrapper around prompts.Load that
// keeps the pipeline resolver from importing pkg/prompts directly.
// Same file wraps the equivalent for HTTP GET; both paths share
// the shipped library lookup.
func loadBuiltinPrompt(name string) (string, error) {
	return prompts.Load(name)
}

// handleGetPrompt returns one prompt (built-in OR curator) by ID.
// Built-in IDs are the prompt name ("ner_v6"); curator IDs are
// UUIDs. The resolver tries the DB first (UUID hit) and falls
// back to the built-in library.
//
// @Summary      Get a prompt
// @Description  Return one prompt (built-in or curator fork) by ID, including its content.
// @Tags         prompts
// @Produce      json
// @Param        id path string true "Prompt ID (builtin name or curator UUID)"
// @Success      200 {object} apiPrompt
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Router       /prompt/{id} [get]
func (s *server) handleGetPrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "prompt id is required")
		return
	}
	// Curator prompt first.
	if p, err := s.a.GetPrompt(r.Context(), id); err == nil {
		createdAt := p.CreatedAt
		writeJSON(w, http.StatusOK, apiPrompt{
			ID:        p.PromptID,
			Name:      p.Name,
			Purpose:   p.Purpose,
			Source:    "curator",
			ForkOf:    p.ForkOf,
			Content:   p.Content,
			CreatedAt: &createdAt,
			CreatedBy: p.CreatedBy,
		})
		return
	} else if !errors.Is(err, hive.ErrNotFound) {
		writeProblem(w, r, err)
		return
	}
	// Fall back to a built-in.
	body, err := prompts.Load(id)
	if err != nil {
		writeProblem(w, r, fmt.Errorf("prompt %q: %w", id, hive.ErrNotFound))
		return
	}
	writeJSON(w, http.StatusOK, apiPrompt{
		ID:      id,
		Name:    id,
		Purpose: purposeFromBuiltinName(id),
		Source:  "builtin",
		Content: body,
	})
}

// apiPromptCreate is the POST body for creating a curator fork.
type apiPromptCreate struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Content string `json:"content"`
	ForkOf  string `json:"fork_of"`
}

// handleCreatePrompt inserts a curator fork. Purpose + ForkOf are
// required for provenance. Name must be unique per purpose (DB
// constraint enforces).
//
// @Summary      Create a prompt fork
// @Description  Insert a curator fork of an existing prompt. Name must be unique per purpose.
// @Tags         prompts
// @Accept       json
// @Produce      json
// @Param        body body apiPromptCreate true "Fork body"
// @Success      201 {object} apiPrompt
// @Failure      400 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /prompt [post]
func (s *server) handleCreatePrompt(w http.ResponseWriter, r *http.Request) {
	var body apiPromptCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeBadRequest(w, r, "name is required")
		return
	}
	var newID string
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		var err error
		newID, err = tx.CreatePrompt(hive.Prompt{
			Name:    body.Name,
			Purpose: body.Purpose,
			Content: body.Content,
			ForkOf:  body.ForkOf,
		})
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetPrompt(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	createdAt := fresh.CreatedAt
	writeJSON(w, http.StatusCreated, apiPrompt{
		ID:        fresh.PromptID,
		Name:      fresh.Name,
		Purpose:   fresh.Purpose,
		Source:    "curator",
		ForkOf:    fresh.ForkOf,
		Content:   fresh.Content,
		CreatedAt: &createdAt,
		CreatedBy: fresh.CreatedBy,
	})
}

// apiPromptPatch is the PATCH body for updating a curator fork's
// content. Name/purpose are immutable post-create.
type apiPromptPatch struct {
	Content string `json:"content"`
}

// handlePatchPrompt updates a curator fork's content. Name and
// purpose are immutable post-create; forkOf tracks the original.
//
// @Summary      Patch a curator prompt
// @Description  Update the content of a curator prompt fork. Name and purpose are immutable post-create.
// @Tags         prompts
// @Accept       json
// @Produce      json
// @Param        id path string true "Prompt ID"
// @Param        body body apiPromptPatch true "Prompt patch"
// @Success      200 {object} apiPrompt
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /prompt/{id} [patch]
func (s *server) handlePatchPrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "prompt id is required")
		return
	}
	var body apiPromptPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.UpdatePromptContent(id, body.Content)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetPrompt(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	createdAt := fresh.CreatedAt
	writeJSON(w, http.StatusOK, apiPrompt{
		ID:        fresh.PromptID,
		Name:      fresh.Name,
		Purpose:   fresh.Purpose,
		Source:    "curator",
		ForkOf:    fresh.ForkOf,
		Content:   fresh.Content,
		CreatedAt: &createdAt,
		CreatedBy: fresh.CreatedBy,
	})
}

// handleDeletePrompt soft-deletes a curator prompt. Built-in
// prompts can't be deleted (they're on disk, not in the DB).
//
// @Summary      Delete a curator prompt
// @Description  Soft-delete a curator prompt fork. Built-in prompts cannot be deleted.
// @Tags         prompts
// @Param        id path string true "Prompt ID"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /prompt/{id} [delete]
func (s *server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "prompt id is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.SoftDeletePrompt(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
