package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/config"
	"github.com/sfborg/hive/pkg/refmanager/orcid"
)

// actorSource holds the actor stamped into each request. The curator
// can change their ORCID in the account dialog while the server runs.
type actorSource struct{ v atomic.Pointer[string] }

func newActorSource(actor string) *actorSource {
	a := &actorSource{}
	a.set(actor)
	return a
}

func (a *actorSource) get() string { return *a.v.Load() }

func (a *actorSource) set(actor string) {
	if actor == "" {
		actor = "local"
	}
	a.v.Store(&actor)
}

// apiAccount is the curator's identity on this machine. *Locked is set
// when the value comes from a command-line flag or an environment
// variable, which override the config file.
type apiAccount struct {
	Name          string `json:"name"`
	ORCID         string `json:"orcid"`
	ORCIDLocked   string `json:"orcid_locked,omitempty"` // "flag" | "env"
	OpenAlexEmail string `json:"openalex_email"`
	EmailLocked   string `json:"openalex_email_locked,omitempty"`
}

type apiAccountPatch struct {
	Name          *string `json:"name,omitempty"`
	ORCID         *string `json:"orcid,omitempty"`
	OpenAlexEmail *string `json:"openalex_email,omitempty"`
}

func accountToAPI(id config.Identity) apiAccount {
	locked := func(from string) string {
		if from == config.SourceFlag || from == config.SourceEnv {
			return from
		}
		return ""
	}
	return apiAccount{
		Name:          id.Name,
		ORCID:         id.ORCID,
		ORCIDLocked:   locked(id.ORCIDFrom),
		OpenAlexEmail: id.OpenAlexEmail,
		EmailLocked:   locked(id.EmailFrom),
	}
}

// handleGetAccount returns the curator's name, ORCID iD and OpenAlex
// email.
//
// @Summary      Get account
// @Tags         account
// @Produce      json
// @Success      200 {object} apiAccount
// @Router       /account [get]
func (s *server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, accountToAPI(config.CurrentIdentity()))
}

// handlePatchAccount updates the curator's identity in the config file
// and applies it to the running server: later writes are attributed to
// the new ORCID iD. Values set by a flag or environment variable cannot
// be changed here.
//
// @Summary      Update account
// @Tags         account
// @Accept       json
// @Produce      json
// @Param        body body apiAccountPatch true "Fields to update"
// @Success      200 {object} apiAccount
// @Failure      400 {object} problem
// @Failure      422 {object} problem
// @Router       /account [patch]
func (s *server) handlePatchAccount(w http.ResponseWriter, r *http.Request) {
	var patch apiAccountPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	id := config.CurrentIdentity()
	cfg, err := config.Load()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	invalid := func(msg string) {
		writeProblem(w, r, fmt.Errorf("%w: %s", hive.ErrValidation, msg))
	}
	if patch.ORCID != nil {
		if acc := accountToAPI(id); acc.ORCIDLocked != "" {
			invalid("the ORCID iD is set by the --orcid flag or HIVE_ORCID; change it there")
			return
		}
		value := strings.TrimSpace(*patch.ORCID)
		if value != "" {
			if value, err = orcid.Normalize(value); err != nil {
				invalid("not a valid ORCID iD")
				return
			}
		}
		cfg.ORCID, id.ORCID = value, value
		id.ORCIDFrom = ""
		if value != "" {
			id.ORCIDFrom = config.SourceConfig
		}
	}
	if patch.OpenAlexEmail != nil {
		if acc := accountToAPI(id); acc.EmailLocked != "" {
			invalid("the OpenAlex email is set by --openalex-email or HIVE_OPENALEX_EMAIL; change it there")
			return
		}
		value := strings.TrimSpace(*patch.OpenAlexEmail)
		if value != "" && !strings.Contains(value, "@") {
			invalid("not an email address")
			return
		}
		cfg.OpenAlexEmail, id.OpenAlexEmail = value, value
		id.EmailFrom = ""
		if value != "" {
			id.EmailFrom = config.SourceConfig
		}
	}
	if patch.Name != nil {
		cfg.Name = strings.TrimSpace(*patch.Name)
		id.Name = cfg.Name
	}
	if err := config.Save(cfg); err != nil {
		writeProblem(w, r, err)
		return
	}
	config.SetIdentity(id)
	if s.actor != nil {
		s.actor.set(id.ORCID)
	}
	writeJSON(w, http.StatusOK, accountToAPI(id))
}

// handleORCIDLookup returns the public display name registered for an
// ORCID iD, so the account dialog can fill in the curator's name.
//
// @Summary      Look up an ORCID iD's public name
// @Tags         account
// @Produce      json
// @Param        orcid query string true "ORCID iD"
// @Success      200 {object} map[string]string
// @Failure      404 {object} problem
// @Router       /account/orcid-name [get]
func (s *server) handleORCIDLookup(w http.ResponseWriter, r *http.Request) {
	id, err := orcid.Normalize(r.URL.Query().Get("orcid"))
	if err != nil {
		writeProblem(w, r, fmt.Errorf("%w: not a valid ORCID iD", hive.ErrValidation))
		return
	}
	client, err := orcid.New()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	p, err := client.Lookup(ctx, id)
	if err != nil {
		if errors.Is(err, orcid.ErrNotFound) {
			writeProblem(w, r, fmt.Errorf("%w: no public ORCID record for %s", hive.ErrNotFound, id))
			return
		}
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"orcid": id, "name": p.DisplayName()})
}
