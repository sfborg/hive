package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sfborg/hive/pkg/config"
)

func TestPatchAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer config.SetIdentity(config.CurrentIdentity())
	config.SetIdentity(config.Identity{OpenAlexEmail: "set@example.org", EmailFrom: config.SourceEnv})
	s := &server{actor: newActorSource("")}

	patch := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handlePatchAccount(w, httptest.NewRequest(http.MethodPatch, "/api/account", strings.NewReader(body)))
		return w
	}
	if w := patch(`{"orcid":"0000-0002-1825-0098"}`); w.Code < 400 {
		t.Errorf("bad checksum accepted: %d", w.Code)
	}
	if w := patch(`{"openalex_email":"other@example.org"}`); w.Code < 400 {
		t.Errorf("email set by the environment changed: %d", w.Code)
	}
	w := patch(`{"orcid":"https://orcid.org/0000-0002-1825-0097","name":" Josiah Carberry "}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if got := s.actor.get(); got != "0000-0002-1825-0097" {
		t.Errorf("actor = %q, want the new ORCID iD", got)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ORCID != "0000-0002-1825-0097" || cfg.Name != "Josiah Carberry" || cfg.OpenAlexEmail != "" {
		t.Errorf("config = %+v", cfg)
	}
}
