package server

import (
	"encoding/json"
	"io"
	"net/http"

	hive "github.com/sfborg/hive/pkg"
)

// handleListSettings returns the requesting user's interface settings
// as a JSON object keyed by setting name.
func (s *server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.a.UserSettings(r.Context(), hive.ActorFromContext(r.Context()))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// handlePutSetting stores one setting for the requesting user. The
// request body is the setting's JSON value.
func (s *server) handlePutSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hive.MaxUserSettingBytes+1))
	if err != nil {
		writeBadRequest(w, r, "setting value too large or unreadable")
		return
	}
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.SetUserSetting(key, json.RawMessage(body))
	}); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSetting removes one setting for the requesting user.
func (s *server) handleDeleteSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteUserSetting(key)
	}); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
