package serve

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sfborg/hive/core"
)

// problem is a hive-scoped RFC 7807 (application/problem+json) response
// body. The `Type` field uses a hive URN under a stable prefix so external
// consumers can dispatch on it without parsing the human-readable title.
type problem struct {
	Type     string           `json:"type"`
	Title    string           `json:"title"`
	Status   int              `json:"status"`
	Detail   string           `json:"detail,omitempty"`
	Instance string           `json:"instance,omitempty"`
	Errors   []problemField   `json:"errors,omitempty"`
}

// problemField carries per-field validation detail.
type problemField struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

const problemPrefix = "urn:hive:error:"

// writeProblem renders a problem+json response for the given error, mapping
// hive's sentinel errors to the appropriate HTTP status codes. Non-hive
// errors get 500 with their message in `detail`.
func writeProblem(w http.ResponseWriter, r *http.Request, err error) {
	p := problem{Instance: r.URL.Path}
	switch {
	case errors.Is(err, core.ErrNotFound):
		p.Status = http.StatusNotFound
		p.Type = problemPrefix + "not-found"
		p.Title = "not found"
	case errors.Is(err, core.ErrValidation):
		p.Status = http.StatusUnprocessableEntity
		p.Type = problemPrefix + "validation"
		p.Title = "validation failed"
	case errors.Is(err, core.ErrConflict):
		p.Status = http.StatusConflict
		p.Type = problemPrefix + "conflict"
		p.Title = "conflict"
	case errors.Is(err, core.ErrReadOnly):
		p.Status = http.StatusForbidden
		p.Type = problemPrefix + "read-only"
		p.Title = "archive is read-only"
	case errors.Is(err, core.ErrExists):
		p.Status = http.StatusConflict
		p.Type = problemPrefix + "exists"
		p.Title = "already exists"
	default:
		p.Status = http.StatusInternalServerError
		p.Type = problemPrefix + "internal"
		p.Title = "internal error"
	}
	p.Detail = err.Error()

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// writeBadRequest renders a 400 problem+json for malformed requests (bad
// query params, unparseable cursors, etc.) — separate from 422 which
// signals valid input that violates business rules.
func writeBadRequest(w http.ResponseWriter, r *http.Request, detail string) {
	p := problem{
		Type:     problemPrefix + "bad-request",
		Title:    "bad request",
		Status:   http.StatusBadRequest,
		Detail:   detail,
		Instance: r.URL.Path,
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// writeProblemDetail renders a problem+json with a caller-supplied status,
// type URN, title, and detail. Used by handlers that talk to external
// services (OpenAlex, BHLnames) — those errors don't map to hive sentinels
// but still deserve structured 502/504 responses so the modal can render
// something useful.
func writeProblemDetail(w http.ResponseWriter, r *http.Request, status int, typeURN, title, detail string) {
	p := problem{
		Type:     typeURN,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: r.URL.Path,
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
