package server

import (
	_ "embed"
	"net/http"

	"github.com/sfborg/hive/internal/wui"
)

// The OpenAPI spec is generated from the swaggo annotations on
// internal/server/*.go by the `just openapi` recipe (which calls
// `swag init -o internal/server/openapi/`). Both formats are embedded
// so integrators can pick whichever their tooling prefers — the
// spec is identical.

//go:embed openapi/swagger.yaml
var openAPIYAML []byte

//go:embed openapi/swagger.json
var openAPIJSON []byte

// handleOpenAPIYAML serves the OpenAPI spec as YAML. The MIME type
// `application/yaml` is the IANA-registered form (RFC 9512).
func (s *server) handleOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openAPIYAML)
}

// handleOpenAPIJSON serves the OpenAPI spec as JSON for tooling that
// prefers JSON over YAML.
func (s *server) handleOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openAPIJSON)
}

// handleDocs serves the RapiDoc-based OpenAPI viewer shell. The HTML is
// stored under internal/wui/dist/docs.html and points RapiDoc at
// /api/openapi.yaml.
func (s *server) handleDocs(w http.ResponseWriter, r *http.Request) {
	b, err := wui.DistFS.ReadFile("dist/docs.html")
	if err != nil {
		http.Error(w, "docs page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
