package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/llm"
	"github.com/sfborg/hive/pkg/prompts"
)

// apiLLMProvider is the wire shape of a configured LLM provider —
// what the WUI dropdown renders. Credentials are never exposed;
// the response carries only what a curator needs to pick a provider.
type apiLLMProvider struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	DefaultModel string `json:"default_model,omitempty"`
}

// apiLLMProviderList wraps the provider set under an "items" key so
// the shape can grow later (default-selection, per-provider status,
// last-invocation timestamps) without breaking clients.
type apiLLMProviderList struct {
	Items []apiLLMProvider `json:"items"`
}

// apiLLMInvokeRequest is POST /api/llm/invoke body. Provider names
// the registered provider (matches an entry from
// /api/llm/providers). Prompt is the shipped-library slug (e.g.
// "ner_v5", "compose_v1"). Input is the per-invocation user-role
// content — a JATS body for NER, an NER-assertion JSON string for
// compose. Purpose describes what the run represents in the
// pipeline (required so persistence knows how to categorize the
// row). Optional fields override adapter/provider defaults for the
// single call; zero values mean "leave to provider default."
type apiLLMInvokeRequest struct {
	Provider    string `json:"provider"`
	Prompt      string `json:"prompt"`
	Input       string `json:"input"`
	Purpose     string `json:"purpose"`                 // "ner" | "compose" | "translate"
	ReferenceID string `json:"reference_id,omitempty"` // which paper this run is for
	ParentRunID string `json:"parent_run_id,omitempty"` // compose→NER link
	Notes       string `json:"notes,omitempty"`

	Model          string  `json:"model,omitempty"`
	MaxTokens      int     `json:"max_tokens,omitempty"`
	Temperature    float64 `json:"temperature,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
}

// apiLLMInvokeResponse returns the persisted run's ID alongside
// the full envelope. When persistence failed (e.g. transient
// database error), RunID is empty and PersistError carries the
// message — the envelope is still returned because the LLM call
// succeeded and the curator paid for the result; forcing a 500
// would lose that data.
//
// AssertionsMaterialized is populated for purpose="ner" runs that
// successfully persisted AND parsed. When the raw output was
// unparseable JSON or the assertions array was empty,
// MaterializeError carries the diagnostic and AssertionsMaterialized
// stays zero — the run row still exists so the curator can inspect
// raw_output_json for prompt-drift debugging.
type apiLLMInvokeResponse struct {
	RunID                  string       `json:"run_id"`
	Envelope               llm.Envelope `json:"envelope" swaggertype:"object"`
	PersistError           string       `json:"persist_error,omitempty"`
	AssertionsMaterialized int          `json:"assertions_materialized,omitempty"`
	MaterializeError       string       `json:"materialize_error,omitempty"`
}

// handleListLLMProviders returns the configured provider list.
// Empty items array (200) when no providers are configured — an
// empty dropdown UI is more useful than a hard 404, and the WUI
// treats zero providers as "prompt the curator to add one."
//
// @Summary      List LLM providers
// @Description  Configured provider entries with kind + default model. Credentials are never exposed.
// @Tags         llm
// @Produce      json
// @Success      200 {object} apiLLMProviderList
// @Router       /llm/providers [get]
func (s *server) handleListLLMProviders(w http.ResponseWriter, r *http.Request) {
	items := make([]apiLLMProvider, 0, len(s.llmProviders))
	for _, pc := range s.llmProviders {
		items = append(items, apiLLMProvider{
			Name:         pc.Name,
			Kind:         pc.Kind,
			DefaultModel: pc.DefaultModel,
		})
	}
	writeJSON(w, http.StatusOK, apiLLMProviderList{Items: items})
}

// apiLLMEstimateRequest is POST /api/llm/estimate body. Provider,
// Prompt, and Input mirror /api/llm/invoke — the estimate uses the
// same inputs the invocation would so the WUI can compute "cost of
// what I'm about to submit" without duplicating any call-shape
// logic. Optional fields override provider defaults for the check.
type apiLLMEstimateRequest struct {
	Provider  string `json:"provider"`
	Prompt    string `json:"prompt"`
	Input     string `json:"input"`
	Model     string `json:"model,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// apiLLMEstimateResponse is what the WUI renders next to the
// submit button. When PricingKnown is false, WUI should show
// "cost unknown for this model" instead of "$0.00" and always
// confirm on submit. When WouldExceedContext is true, WUI should
// disable submit with a "too long for this model" message — no
// point spending a round-trip on a request the provider will 400.
type apiLLMEstimateResponse struct {
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
	InputTokens         int     `json:"input_tokens"`
	MaxOutputTokens     int     `json:"max_output_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	PricingKnown        bool    `json:"pricing_known"`
	ConfirmThresholdUSD float64 `json:"confirm_threshold_usd"`
	ContextWindow       int     `json:"context_window,omitempty"`
	LimitsKnown         bool    `json:"limits_known"`
	WouldExceedContext  bool    `json:"would_exceed_context"`
}

// handleEstimateLLM computes an approximate token count and USD
// cost for a would-be invocation without actually calling the
// provider. Used by the WUI to display cost next to the submit
// button and to trigger a confirmation dialog above
// [llm.ConfirmThresholdUSD]. Also reports whether the payload
// would exceed the model's context window so the WUI can disable
// submit for hopeless cases (e.g. a 700-page paper on a 200K-token
// Claude model — chunking support is deferred as its own concern).
//
// @Summary      Estimate an LLM invocation
// @Description  Compute approximate token count and USD cost for a would-be invocation without calling the provider.
// @Tags         llm
// @Accept       json
// @Produce      json
// @Param        body body apiLLMEstimateRequest true "Estimate request"
// @Success      200 {object} apiLLMEstimateResponse
// @Failure      400 {object} problem
// @Router       /llm/estimate [post]
func (s *server) handleEstimateLLM(w http.ResponseWriter, r *http.Request) {
	if s.llmRunner == nil {
		writeBadRequest(w, r, "no llm providers configured in user config (llm_providers)")
		return
	}
	var body apiLLMEstimateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.Provider == "" {
		writeBadRequest(w, r, "provider is required")
		return
	}
	if body.Prompt == "" {
		writeBadRequest(w, r, "prompt is required")
		return
	}
	// Resolve the model. Explicit override wins; otherwise fall
	// back to the provider's configured DefaultModel. The Provider
	// interface intentionally doesn't expose DefaultModel — the
	// server keeps the config-list around for exactly this kind of
	// descriptive query.
	model := body.Model
	if model == "" {
		for _, pc := range s.llmProviders {
			if pc.Name == body.Provider {
				model = pc.DefaultModel
				break
			}
		}
	}
	if model == "" {
		writeBadRequest(w, r, fmt.Sprintf(
			"no model available for provider %q (no default, no override)", body.Provider))
		return
	}
	// Load the prompt so its tokens are counted against the input
	// budget — the prompt is what dominates a NER call, not the
	// per-invocation user input.
	prompt, err := prompts.Load(body.Prompt)
	if err != nil {
		writeBadRequest(w, r, "load prompt: "+err.Error())
		return
	}
	inputTokens := llm.EstimateTokens(prompt) + llm.EstimateTokens(body.Input)
	maxOut := body.MaxTokens
	if maxOut == 0 {
		// Matches the Anthropic adapter's default so the estimate
		// reflects the same request the invocation would actually
		// send. OpenAI-compat treats zero as "provider default"
		// (typically small), so the estimate is conservative there.
		maxOut = 4096
	}
	cost, priceKnown := estimateCostForProvider(body.Provider, model, inputTokens, maxOut)
	limits, limitsKnown := llm.LookupLimits(model)
	exceeds := false
	if limitsKnown {
		exceeds = inputTokens+maxOut > limits.ContextWindow
	}
	writeJSON(w, http.StatusOK, apiLLMEstimateResponse{
		Provider:            body.Provider,
		Model:               model,
		InputTokens:         inputTokens,
		MaxOutputTokens:     maxOut,
		EstimatedCostUSD:    cost,
		PricingKnown:        priceKnown,
		ConfirmThresholdUSD: llm.ConfirmThresholdUSD,
		ContextWindow:       limits.ContextWindow,
		LimitsKnown:         limitsKnown,
		WouldExceedContext:  exceeds,
	})
}

// handleInvokeLLM runs the named prompt via the named provider,
// persists the invocation as a [hive.ExtractionRun] row so the
// curator keeps paid-for results across sessions, and returns the
// run ID + full envelope. Deliberately generic: no per-pipeline
// JSON parsing happens here, so the same endpoint covers NER,
// compose, translation, and whatever else ships as a prompt-
// library entry.
//
// Persistence failure does NOT roll back the LLM call — the
// curator has already paid for the completion. Instead the
// response returns an empty RunID + PersistError message so the
// WUI can show a warning ("results not saved; consider retrying
// or exporting manually") without losing the envelope content.
//
// @Summary      Invoke an LLM prompt
// @Description  Run a shipped-library prompt via a configured provider. Persists the invocation as an ExtractionRun row and materializes assertions for NER purpose runs.
// @Tags         llm
// @Accept       json
// @Produce      json
// @Param        body body apiLLMInvokeRequest true "Invoke request"
// @Success      200 {object} apiLLMInvokeResponse
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Failure      502 {object} problem "Provider refused on account grounds (spend limit, credits, API key); type urn:hive:error:provider-account-limit"
// @Router       /llm/invoke [post]
func (s *server) handleInvokeLLM(w http.ResponseWriter, r *http.Request) {
	if s.llmRunner == nil {
		writeBadRequest(w, r, "no llm providers configured in user config (llm_providers)")
		return
	}
	var body apiLLMInvokeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.Provider == "" {
		writeBadRequest(w, r, "provider is required")
		return
	}
	if body.Prompt == "" {
		writeBadRequest(w, r, "prompt is required")
		return
	}
	if body.Purpose == "" {
		writeBadRequest(w, r, "purpose is required (ner | compose | translate)")
		return
	}

	// Resolve the model up front so envelope.Model records the
	// requested identifier (matches the pricing table key) rather
	// than the provider's response-echoed version string. Cost
	// computation and downstream pricing lookups all key on this
	// name.
	model := body.Model
	if model == "" {
		for _, pc := range s.llmProviders {
			if pc.Name == body.Provider {
				model = pc.DefaultModel
				break
			}
		}
	}

	// Default max_tokens to the model's per-call output cap when
	// caller doesn't specify — never truncate output arbitrarily
	// (Anthropic bills on actual tokens, not the max_tokens
	// setting, so an over-generous cap has no cost impact). Same
	// policy as the pipeline handler.
	maxTokens := body.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxForModel(model)
	}

	env, err := s.llmRunner.Run(
		r.Context(),
		body.Provider,
		body.Prompt,
		body.Input,
		llm.WithModel(model),
		llm.WithMaxTokens(maxTokens),
		llm.WithTemperature(body.Temperature),
		llm.WithResponseFormat(body.ResponseFormat),
	)
	if err != nil {
		// Spend limit reached, credits exhausted or key rejected:
		// every further request would be refused the same way, so
		// batch callers key on this type to stop sending.
		if reason, ok := llm.AccountLimit(err); ok {
			writeProblemDetail(w, r, http.StatusBadGateway,
				problemPrefix+"provider-account-limit",
				"LLM provider refused the request: "+reason, err.Error())
			return
		}
		writeProblem(w, r, err)
		return
	}
	// Strip markdown fences from Output before persistence — LLMs
	// wrap JSON in ```json ... ``` despite prompt instructions;
	// downstream json.Unmarshal and json.RawMessage consumers
	// need clean input.
	env.Output = llm.StripMarkdownFences(env.Output)

	// Serialize the envelope once for storage — the row keeps the
	// full provenance record so a curator opening a saved run
	// months later still sees exactly which prompt version /
	// provider / model produced it.
	envJSON, mErr := json.Marshal(env)
	if mErr != nil {
		// Encoding failure on our own struct is a programmer bug,
		// not an operator concern. Log and continue with empty
		// envelope_json so the run still lands.
		slog.ErrorContext(r.Context(), "llm invoke: marshal envelope",
			"err", mErr, "provider", body.Provider, "prompt", body.Prompt)
		envJSON = []byte{}
	}

	// Actual cost from provider-reported token counts (accurate,
	// not the estimate's char-based approximation). Zero when
	// no source (dynamic cache or static table) knows the model.
	cost, _ := estimateCostForProvider(body.Provider, env.Model, env.InputTokens, env.OutputTokens)

	run := hive.ExtractionRun{
		ReferenceID:   body.ReferenceID,
		Purpose:       body.Purpose,
		SourceKind:    "llm",
		SourceID:      body.Provider,
		EnvelopeJSON:  string(envJSON),
		RawOutputJSON: env.Output,
		ParentRunID:   body.ParentRunID,
		CostUSD:       cost,
		Notes:         body.Notes,
	}

	var runID string
	var materializedCount int
	var materializeErr error
	persistErr := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.CreateExtractionRun(run)
		if err != nil {
			return err
		}
		runID = id
		// Materialize per-fact assertion rows in the SAME tx so a
		// partial insert (some assertions succeed, later ones fail)
		// rolls back cleanly and doesn't leave a run row with
		// half-populated assertions. Materialization is a no-op for
		// non-NER runs (compose materialization is deferred) and
		// for runs without a reference_id, so ad-hoc test calls
		// still work.
		created := run
		created.RunID = id
		n, mErr := tx.MaterializeAssertionsFromRun(r.Context(), created)
		materializedCount = n
		materializeErr = mErr
		// Don't fail the tx on materialize error — the LLM call
		// succeeded and the run is worth preserving even if the
		// output was malformed JSON. Curator inspects
		// raw_output_json to diagnose. Whole-tx rollback here
		// would lose everything.
		return nil
	})
	if persistErr != nil {
		slog.WarnContext(r.Context(), "llm invoke: persist run failed",
			"err", persistErr, "provider", body.Provider, "prompt", body.Prompt)
	}
	if materializeErr != nil {
		slog.WarnContext(r.Context(), "llm invoke: materialize assertions failed",
			"err", materializeErr, "run_id", runID)
	}

	resp := apiLLMInvokeResponse{
		RunID:                  runID,
		Envelope:               env,
		AssertionsMaterialized: materializedCount,
	}
	if persistErr != nil {
		resp.PersistError = persistErr.Error()
	}
	if materializeErr != nil {
		resp.MaterializeError = materializeErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// apiLLMRun is the wire shape of a persisted [hive.ExtractionRun].
// Envelope and RawOutput are json.RawMessage passthroughs — the
// stored strings are already JSON, no reparse needed.
type apiLLMRun struct {
	RunID          string          `json:"run_id"`
	ReferenceID    string          `json:"reference_id,omitempty"`
	Purpose        string          `json:"purpose"`
	SourceKind     string          `json:"source_kind"`
	SourceID       string          `json:"source_id"`
	Envelope       json.RawMessage `json:"envelope,omitempty" swaggertype:"object"`
	RawOutput      json.RawMessage `json:"raw_output,omitempty" swaggertype:"object"`
	ParentRunID    string          `json:"parent_run_id,omitempty"`
	CostUSD        float64         `json:"cost_usd"`
	IsGoldStandard bool            `json:"is_gold_standard"`
	Notes          string          `json:"notes,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	DeletedAt      *time.Time      `json:"deleted_at,omitempty"`
}

type apiLLMRunList struct {
	Items []apiLLMRun `json:"items"`
}

// estimateCostForProvider computes the USD cost of an invocation
// using the best pricing source available:
//
//  1. Provider's own cached models list (populated by
//     /api/llm/providers/{name}/models). Works for aggregators
//     like OpenRouter that return per-token pricing per model,
//     without needing hive to hand-maintain a table for every
//     OR-mediated model ID.
//  2. hive's static pricing table (pkg/llm/pricing.go). Covers
//     the well-known native provider models.
//
// Returns known=false when neither source has pricing for this
// (provider, model). Callers surface "cost unknown" so the WUI
// always confirms before submit rather than silently showing
// $0.00.
func estimateCostForProvider(providerName, model string, inputTokens, outputTokens int) (float64, bool) {
	if providerName != "" && model != "" {
		if cached, ok := cachedModels(providerName); ok {
			for _, m := range cached {
				if m.ID != model {
					continue
				}
				if m.InputPricePerMillion == 0 && m.OutputPricePerMillion == 0 {
					break
				}
				in := float64(inputTokens) / 1_000_000 * m.InputPricePerMillion
				out := float64(outputTokens) / 1_000_000 * m.OutputPricePerMillion
				return in + out, true
			}
		}
	}
	return llm.EstimateCost(model, inputTokens, outputTokens)
}

func runToAPI(r hive.ExtractionRun) apiLLMRun {
	api := apiLLMRun{
		RunID:          r.RunID,
		ReferenceID:    r.ReferenceID,
		Purpose:        r.Purpose,
		SourceKind:     r.SourceKind,
		SourceID:       r.SourceID,
		ParentRunID:    r.ParentRunID,
		CostUSD:        r.CostUSD,
		IsGoldStandard: r.IsGoldStandard,
		Notes:          r.Notes,
		CreatedAt:      r.CreatedAt,
	}
	// json.RawMessage MUST be valid JSON — assigning malformed
	// content (e.g. a stray markdown fence that slipped past the
	// stripper) makes json.Encoder fail mid-write on the enclosing
	// response, and the client receives status 200 with an empty
	// body. Validate defensively; drop the field on invalid content
	// so at least the rest of the response makes it through. The
	// stored raw_output_json is still inspectable via the run's
	// detail endpoint if a curator needs to debug what went wrong.
	if r.EnvelopeJSON != "" && json.Valid([]byte(r.EnvelopeJSON)) {
		api.Envelope = json.RawMessage(r.EnvelopeJSON)
	}
	if r.RawOutputJSON != "" && json.Valid([]byte(r.RawOutputJSON)) {
		api.RawOutput = json.RawMessage(r.RawOutputJSON)
	}
	if !r.DeletedAt.IsZero() {
		t := r.DeletedAt
		api.DeletedAt = &t
	}
	return api
}

// handleListReferenceLLMRuns returns every run for a given
// reference, most-recent first. include_deleted=1 query param
// toggles inclusion of soft-deleted rows (default false; the cost
// dashboard uses true so spend stays visible).
//
// @Summary      List LLM runs for a reference
// @Description  Every extraction run linked to the reference, most-recent first.
// @Tags         llm
// @Produce      json
// @Param        id path string true "Reference ID"
// @Param        include_deleted query int false "Include soft-deleted rows (1 = yes)"
// @Success      200 {object} apiLLMRunList
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/llm-runs [get]
func (s *server) handleListReferenceLLMRuns(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	includeDeleted := r.URL.Query().Get("include_deleted") == "1"
	runs, err := s.a.ListExtractionRunsByReference(r.Context(), refID, includeDeleted)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiLLMRun, 0, len(runs))
	for _, run := range runs {
		items = append(items, runToAPI(run))
	}
	writeJSON(w, http.StatusOK, apiLLMRunList{Items: items})
}

// handleGetLLMRun returns one persisted run by ID.
//
// @Summary      Get an LLM run
// @Description  Return one persisted extraction run by ID (including its envelope + raw output).
// @Tags         llm
// @Produce      json
// @Param        id path string true "Run ID"
// @Success      200 {object} apiLLMRun
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Router       /llm/run/{id} [get]
func (s *server) handleGetLLMRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeBadRequest(w, r, "run id is required")
		return
	}
	run, err := s.a.GetExtractionRun(r.Context(), runID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runToAPI(*run))
}

// handleSoftDeleteLLMRun stamps deleted_at on the run row without
// removing it. Cost dashboards still see the spend; default lists
// filter it out. Use POST /api/llm/run/{id}/restore (future) to
// undo.
//
// @Summary      Soft-delete an LLM run
// @Description  Stamp deleted_at on the run row. Cost dashboards still see the spend; default lists filter it out.
// @Tags         llm
// @Param        id path string true "Run ID"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/run/{id} [delete]
func (s *server) handleSoftDeleteLLMRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeBadRequest(w, r, "run id is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.SoftDeleteExtractionRun(runID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRestoreLLMRun clears deleted_at, un-hiding a soft-deleted
// run.
//
// @Summary      Restore a soft-deleted LLM run
// @Description  Clear deleted_at on the run row.
// @Tags         llm
// @Param        id path string true "Run ID"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/run/{id}/restore [post]
func (s *server) handleRestoreLLMRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeBadRequest(w, r, "run id is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.RestoreExtractionRun(runID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiSetGoldStandardRequest toggles the is_gold_standard flag on a
// run. Bool in JSON body is cleaner than a query param for a
// mutation.
type apiSetGoldStandardRequest struct {
	Gold bool `json:"gold"`
}

// handleSetLLMRunGoldStandard flips a run's ground-truth flag.
// Gold runs anchor sampling-based accuracy scoring — LLM runs on
// gold-flagged references get scored against the gold assertion
// set. See project_extraction_runs_multi_model_schema memory.
//
// @Summary      Set an LLM run's gold-standard flag
// @Description  Toggle is_gold_standard on the run row. Gold runs anchor sampling-based accuracy scoring.
// @Tags         llm
// @Accept       json
// @Produce      json
// @Param        id path string true "Run ID"
// @Param        body body apiSetGoldStandardRequest true "Gold-standard toggle"
// @Success      200 {object} apiLLMRun
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/run/{id}/gold-standard [post]
func (s *server) handleSetLLMRunGoldStandard(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeBadRequest(w, r, "run id is required")
		return
	}
	var body apiSetGoldStandardRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.SetExtractionRunGoldStandard(runID, body.Gold)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Return the updated row so the WUI's optimistic-render can
	// verify the flip landed without a second GET round-trip.
	run, err := s.a.GetExtractionRun(r.Context(), runID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runToAPI(*run))
}

