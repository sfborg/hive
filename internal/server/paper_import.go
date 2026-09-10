package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	hive "github.com/sfborg/hive/pkg"
)

// apiPaperImportMapping is the wire shape for one row of
// hive__paper_import.
type apiPaperImportMapping struct {
	ReferenceID     string     `json:"reference_id"`
	PaperNameKey    string     `json:"paper_name_key"`
	TargetTaxonID   string     `json:"target_taxon_id"`
	MatchMode       string     `json:"match_mode"`
	MatchConfidence float64    `json:"match_confidence,omitempty"`
	LastSyncedAt    *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CreatedBy       string     `json:"created_by,omitempty"`
}

type apiPaperImportMappingList struct {
	Items []apiPaperImportMapping `json:"items"`
}

func mappingToAPI(m hive.PaperImportMapping) apiPaperImportMapping {
	api := apiPaperImportMapping{
		ReferenceID:     m.ReferenceID,
		PaperNameKey:    m.PaperNameKey,
		TargetTaxonID:   m.TargetTaxonID,
		MatchMode:       m.MatchMode,
		MatchConfidence: m.MatchConfidence,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		CreatedBy:       m.CreatedBy,
	}
	if !m.LastSyncedAt.IsZero() {
		t := m.LastSyncedAt
		api.LastSyncedAt = &t
	}
	return api
}

// handleListPaperImportMappings returns the curator-approved
// mappings for one paper. Backs the assembly UI's "restore
// existing state on open" flow.
//
// @Summary      List paper-import mappings
// @Description  Curator-approved paper-name → target-taxon mappings for one reference.
// @Tags         assertions
// @Produce      json
// @Param        id path string true "Reference ID"
// @Success      200 {object} apiPaperImportMappingList
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/paper-import [get]
func (s *server) handleListPaperImportMappings(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	rows, err := s.a.ListPaperImportMappings(r.Context(), refID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiPaperImportMapping, 0, len(rows))
	for _, m := range rows {
		items = append(items, mappingToAPI(m))
	}
	writeJSON(w, http.StatusOK, apiPaperImportMappingList{Items: items})
}

// apiPaperImportMappingUpsert is the POST body for creating /
// updating one mapping. Called by the drag-drop UI on each drop
// so the same body works for first-mapping and re-mapping.
type apiPaperImportMappingUpsert struct {
	PaperNameKey    string  `json:"paper_name_key"`
	TargetTaxonID   string  `json:"target_taxon_id"`
	MatchMode       string  `json:"match_mode,omitempty"`
	MatchConfidence float64 `json:"match_confidence,omitempty"`
}

// handleUpsertPaperImportMapping creates or updates one paper-name →
// target-taxon mapping for the given reference. Drag-drop assembly
// UI calls this on every drop so first-mapping and re-mapping share
// one code path.
//
// @Summary      Upsert a paper-import mapping
// @Description  Create or update one paper-name → target-taxon mapping for a reference.
// @Tags         assertions
// @Accept       json
// @Param        id path string true "Reference ID"
// @Param        body body apiPaperImportMappingUpsert true "Mapping body"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/paper-import [post]
func (s *server) handleUpsertPaperImportMapping(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	var body apiPaperImportMappingUpsert
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.PaperNameKey == "" || body.TargetTaxonID == "" {
		writeBadRequest(w, r, "paper_name_key and target_taxon_id are required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.UpsertPaperImportMapping(hive.PaperImportMapping{
			ReferenceID:     refID,
			PaperNameKey:    body.PaperNameKey,
			TargetTaxonID:   body.TargetTaxonID,
			MatchMode:       body.MatchMode,
			MatchConfidence: body.MatchConfidence,
		})
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePaperImportMapping removes one paper-name mapping
// from the reference's assembly state.
//
// @Summary      Delete a paper-import mapping
// @Description  Remove one paper-name → target-taxon mapping from a reference's assembly state.
// @Tags         assertions
// @Param        id path string true "Reference ID"
// @Param        key path string true "Paper-name key"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/paper-import/{key} [delete]
func (s *server) handleDeletePaperImportMapping(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	key := r.PathValue("key")
	if refID == "" || key == "" {
		writeBadRequest(w, r, "reference id and paper_name_key are required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeletePaperImportMapping(refID, key)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiPaperImportExecuteRequest picks which compose run drives the
// import. Curator picks a run in the assembly UI (typically the
// active/preferred one from the Assertions tab); the executor
// materializes extension rows from THAT run's compose output.
type apiPaperImportExecuteRequest struct {
	ExtractionRunID string `json:"extraction_run_id"`
}

type apiPaperImportExecuteResponse struct {
	TypeMaterialInserted int      `json:"type_material_inserted"`
	DistributionInserted int      `json:"distribution_inserted"`
	VernacularInserted   int      `json:"vernacular_inserted"`
	Removed              int      `json:"removed"`
	Warnings             []string `json:"warnings,omitempty"`
}

// handleExecutePaperImport materializes the paper's compose
// output as SFGA extension rows on the mapped target taxa. See
// pkg/paper_import_exec.go for the semantics (additive-only,
// delete-then-reinsert on re-run, provenance via hive__row_source).
//
// @Summary      Execute a paper import
// @Description  Materialize the paper's compose output as SFGA extension rows on the mapped target taxa. Additive-only; delete-then-reinsert on re-run.
// @Tags         assertions
// @Accept       json
// @Produce      json
// @Param        id path string true "Reference ID"
// @Param        body body apiPaperImportExecuteRequest true "Execute body"
// @Success      200 {object} apiPaperImportExecuteResponse
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/paper-import/execute [post]
func (s *server) handleExecutePaperImport(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	var body apiPaperImportExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.ExtractionRunID == "" {
		writeBadRequest(w, r, "extraction_run_id is required")
		return
	}
	// Fetch the compose run's raw output — that's the tree the
	// import materializes from.
	run, err := s.a.GetExtractionRun(r.Context(), body.ExtractionRunID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if run.Purpose != "compose" {
		writeBadRequest(w, r, fmt.Sprintf(
			"extraction run %s has purpose %q; import requires a compose run",
			body.ExtractionRunID, run.Purpose))
		return
	}
	if run.RawOutputJSON == "" {
		writeBadRequest(w, r, "compose run has no raw_output_json (empty or failed run)")
		return
	}
	var result *hive.ExecutePaperImportResult
	ctx := r.Context()
	err = s.a.WithTx(ctx, func(tx *hive.Tx) error {
		r, err := tx.ExecutePaperImport(
			ctx, refID, body.ExtractionRunID, run.RawOutputJSON,
		)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apiPaperImportExecuteResponse{
		TypeMaterialInserted: result.TypeMaterialInserted,
		DistributionInserted: result.DistributionInserted,
		VernacularInserted:   result.VernacularInserted,
		Removed:              result.Removed,
		Warnings:             result.Warnings,
	})
}
