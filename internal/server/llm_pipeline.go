package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/llm"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/hive/pkg/taxonomy/labeldate"
)

// apiLLMPipelineRequest orchestrates the two-step NER → Compose
// pipeline in one HTTP call so curators see it as a single "Run"
// action. Each step can pick a different provider (typically:
// cheap model for NER's bulk entity tagging, mid-tier for compose's
// attribution + tree assembly).
//
// Input is not sent by the client — the handler fetches the JATS
// body server-side from the reference's first attached PDF sidecar.
// Curators don't want to see or edit a 100KB XML blob in a form,
// and letting them accidentally break well-formed XML is a foot-gun.
// Prompt editing (the actually-editorial layer) happens elsewhere
// via the prompt-picker + fork flow, not here.
type apiLLMPipelineRequest struct {
	// ReferenceID is which paper the pipeline runs against. Required —
	// the handler looks up the attached PDF sha and reads the JATS
	// body from the sidecar. Papers without an attached PDF are
	// rejected with a helpful message.
	ReferenceID string `json:"reference_id"`

	// NERProvider names the registered provider for the NER pass.
	// Required. NERModel optionally overrides the provider's
	// configured DefaultModel for this run.
	NERProvider string `json:"ner_provider"`
	NERModel    string `json:"ner_model,omitempty"`

	// TreeProvider names the registered provider for the compose
	// (tree assembly) pass. Required. TreeModel optionally
	// overrides the provider's configured DefaultModel.
	TreeProvider string `json:"tree_provider"`
	TreeModel    string `json:"tree_model,omitempty"`

	// NERPromptID and ComposePromptID select which prompt to send
	// to each stage. Value is either a shipped built-in name
	// ("ner_v6") or a curator fork's UUID. Empty falls back to
	// the current shipped default (defaultNERPromptID /
	// defaultComposePromptID). Resolves through the curator DB
	// first, then the built-in library.
	NERPromptID     string `json:"ner_prompt_id,omitempty"`
	ComposePromptID string `json:"compose_prompt_id,omitempty"`

	// Notes annotates both runs. Curator-facing free text.
	Notes string `json:"notes,omitempty"`

	// Per-step max_tokens overrides. Compose typically needs more
	// output budget (whole-tree JSON) than NER (assertion array).
	NERMaxTokens  int `json:"ner_max_tokens,omitempty"`
	TreeMaxTokens int `json:"tree_max_tokens,omitempty"`

	// Temperature applied uniformly to both steps. Zero-value =
	// provider default (usually deterministic-ish).
	Temperature float64 `json:"temperature,omitempty"`

	// Per-step reasoning effort (low | medium | high | xhigh | max).
	// Empty uses the model default. Sent only to Anthropic
	// providers. Separate per step because NER typically needs less
	// reasoning than tree composition.
	NEREffort  string `json:"ner_effort,omitempty"`
	TreeEffort string `json:"tree_effort,omitempty"`

	// ThinkingSummary requests a summary of the model's reasoning,
	// stored on each run's envelope. Applied per step only where it
	// adds no cost: Anthropic providers with a model that thinks by
	// default. Other steps run without it.
	ThinkingSummary bool `json:"thinking_summary,omitempty"`
}

// apiLLMPipelineResponse returns both persisted-run summaries plus
// the parsed tree JSON so the WUI can render immediately. When a
// step fails, the fields for the failed step's run are still
// populated (envelope + error) so curators can see what happened
// and inspect the raw output. Compose is skipped when NER fails.
type apiLLMPipelineResponse struct {
	NERRun     apiLLMRun       `json:"ner_run"`
	ComposeRun apiLLMRun       `json:"compose_run,omitempty"`
	Tree       json.RawMessage `json:"tree,omitempty" swaggertype:"object"`
	// AssertionsMaterialized is how many hive__reference_assertion
	// rows the NER step produced after parsing its output. Zero
	// with a non-empty NERMaterializeError means the NER output
	// was malformed (typically truncated at max_tokens).
	AssertionsMaterialized int    `json:"assertions_materialized,omitempty"`
	NERMaterializeError    string `json:"ner_materialize_error,omitempty"`
	// FailedStage is "ner" | "compose" | "" (empty on full
	// success). Lets the WUI show a targeted error UI instead of
	// having to reason about which step's error message applies.
	FailedStage string `json:"failed_stage,omitempty"`
	Error       string `json:"error,omitempty"`
}

// handleRunLLMPipeline runs NER then compose against the same
// paper, persisting an extraction_run row for each and linking
// compose→NER via parent_run_id. Curator sees one "Run" action;
// hive records the two-step provenance separately so multi-model
// comparison at either layer stays possible.
//
// Pipeline flow:
//  1. Resolve models from picker + provider defaults.
//  2. Run NER with JATS as input. Persist run + materialize
//     assertions in one tx. On failure: response reports NER
//     error, no compose run is attempted.
//  3. Build compose input by concatenating "# Paper JATS body\n
//     {jats}\n# NER assertions\n{ner_json}" per compose_v1's
//     Input format.
//  4. Run compose. Persist run with parent_run_id = ner run id.
//     Parse output into Tree field.
//
// Both runs land regardless of parse-of-output success — curator
// keeps paid-for envelopes even when the model's JSON is malformed.
//
// @Summary      Run the two-step NER → compose pipeline
// @Description  Runs NER then compose against the paper's JATS body, persisting an extraction_run row for each and linking compose→NER via parent_run_id.
// @Tags         llm
// @Accept       json
// @Produce      json
// @Param        body body apiLLMPipelineRequest true "Pipeline request"
// @Success      200 {object} apiLLMPipelineResponse
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/pipeline [post]
func (s *server) handleRunLLMPipeline(w http.ResponseWriter, r *http.Request) {
	if s.llmRunner == nil {
		writeBadRequest(w, r, "no llm providers configured in user config (llm_providers)")
		return
	}
	var body apiLLMPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.NERProvider == "" || body.TreeProvider == "" {
		writeBadRequest(w, r, "ner_provider and tree_provider are required")
		return
	}
	if body.ReferenceID == "" {
		writeBadRequest(w, r, "reference_id is required")
		return
	}

	// Fetch the paper's JATS body server-side from its attached
	// PDF sidecar. First-attached wins — multi-attachment papers
	// (multi-media works, translated editions) aren't the common
	// case and picker UX for "which attachment" belongs on the
	// reference-detail side, not the invoke modal.
	jatsBody, jatsErr := s.readReferenceJATS(r.Context(), body.ReferenceID)
	if jatsErr != nil {
		writeBadRequest(w, r, "cannot read paper JATS: "+jatsErr.Error())
		return
	}

	// Resolve concrete model names so envelopes record the base
	// identifier used for pricing lookup (not the provider's
	// response-echoed version string).
	nerModel := s.resolveModel(body.NERProvider, body.NERModel)
	treeModel := s.resolveModel(body.TreeProvider, body.TreeModel)

	if !llm.ValidEffort(body.NEREffort) || !llm.ValidEffort(body.TreeEffort) {
		writeBadRequest(w, r, "invalid 'ner_effort' or 'tree_effort' value (low | medium | high | xhigh | max)")
		return
	}

	// Default max_tokens to the model's per-call output cap so a
	// full-paper NER pass doesn't truncate mid-JSON on 4096-token
	// adapter defaults. Curators can still override per-step from
	// the API; the WUI doesn't expose this today but backend
	// contract keeps the override available.
	nerMax := body.NERMaxTokens
	if nerMax == 0 {
		nerMax = s.defaultMaxForModel(r.Context(), body.NERProvider, nerModel)
	}
	treeMax := body.TreeMaxTokens
	if treeMax == 0 {
		treeMax = s.defaultMaxForModel(r.Context(), body.TreeProvider, treeModel)
	}

	// ---------- Step 1: NER ----------
	// ner_v6 adds the "one assertion per mention" invariant so
	// per-specimen curator verification stays per-specimen. See
	// feedback_per_mention_assertions memory. Older archives still
	// carry assertions tagged ner_v5 with the pre-invariant
	// behavior; that file remains embedded for provenance lookup.
	//
	// The curator can override either default via the prompt
	// picker; resolvePromptForRun tries the archive's curator
	// forks first, falls back to the shipped built-in.
	nerPromptID := body.NERPromptID
	if nerPromptID == "" {
		nerPromptID = defaultNERPromptID
	}
	nerPromptContent, resolvedNERPromptID, err := s.resolvePromptForRun(r.Context(), nerPromptID, "ner")
	if err != nil {
		writeBadRequest(w, r, fmt.Sprintf("NER prompt: %v", err))
		return
	}
	nerEnv, nerErr := s.llmRunner.RunWithPrompt(
		r.Context(),
		body.NERProvider,
		resolvedNERPromptID,
		nerPromptContent,
		jatsBody,
		llm.WithModel(nerModel),
		llm.WithMaxTokens(nerMax),
		llm.WithTemperature(body.Temperature),
		llm.WithEffort(body.NEREffort),
		llm.WithThinkingSummary(body.ThinkingSummary && s.thinkingSummaryFree(body.NERProvider, nerModel)),
	)
	if nerErr != nil {
		writeJSON(w, http.StatusOK, apiLLMPipelineResponse{
			NERRun:      apiLLMRun{}, // no run persisted
			FailedStage: "ner",
			Error:       nerErr.Error(),
		})
		return
	}
	// LLMs commonly wrap JSON output in ```json ... ``` code
	// fences despite explicit instructions to emit raw JSON.
	// Strip before persisting so downstream json.Unmarshal and
	// json.RawMessage consumers get clean input.
	nerEnv.Output = llm.StripMarkdownFences(nerEnv.Output)

	nerRun, materializedCount, materializeErr, nerRunErr := s.persistNERRun(r.Context(), body, nerEnv)
	if nerRunErr != nil {
		slog.WarnContext(r.Context(), "pipeline: persist NER run failed",
			"err", nerRunErr, "provider", body.NERProvider)
	}
	if materializeErr != nil {
		slog.WarnContext(r.Context(), "pipeline: materialize NER assertions failed",
			"err", materializeErr, "provider", body.NERProvider)
	}

	// ---------- Step 2: Compose ----------
	// compose_v4 renames accordingToAuthor -> accordingTo (matching
	// both CoLDP's taxon.col__according_to_id and TCS2's
	// tcs:accordingTo property) and documents the implied
	// concept-relation vocabulary as CoLDP's taxon_concept_rel_type
	// (EQUALS / INCLUDES / INCLUDED_IN / OVERLAPS / EXCLUDES).
	// compose_v3 added sensu-qualifier fields (namePhrase,
	// accordingToAuthor, nomenclaturalNote) on name_usages and
	// preserves per-mention rows so ambiguous-synonymy and
	// primary-homonym cases don't collapse. compose_v2 stopped
	// emitting the ISO `date` field (server derives it via
	// pkg/taxonomy/labeldate) and prohibited reusing source IDs
	// across distinct compose rows. Older archives still carry
	// runs tagged compose_v1 / compose_v2 / compose_v3; those files
	// remain embedded for provenance lookup.
	composePromptID := body.ComposePromptID
	if composePromptID == "" {
		composePromptID = defaultComposePromptID
	}
	composePromptContent, resolvedComposePromptID, err := s.resolvePromptForRun(r.Context(), composePromptID, "compose")
	if err != nil {
		writeBadRequest(w, r, fmt.Sprintf("compose prompt: %v", err))
		return
	}
	composeInput := buildComposeInput(jatsBody, nerEnv.Output)
	composeEnv, composeErr := s.llmRunner.RunWithPrompt(
		r.Context(),
		body.TreeProvider,
		resolvedComposePromptID,
		composePromptContent,
		composeInput,
		llm.WithModel(treeModel),
		llm.WithMaxTokens(treeMax),
		llm.WithTemperature(body.Temperature),
		llm.WithEffort(body.TreeEffort),
		llm.WithThinkingSummary(body.ThinkingSummary && s.thinkingSummaryFree(body.TreeProvider, treeModel)),
	)

	// Prepare response, filling as much as we can even when
	// compose failed (curator still gets the NER run).
	resp := apiLLMPipelineResponse{
		NERRun:                 nerRun,
		AssertionsMaterialized: materializedCount,
	}
	if materializeErr != nil {
		resp.NERMaterializeError = materializeErr.Error()
	}
	if nerRunErr != nil {
		// Surface the persist-warning without failing the
		// whole request — same policy as /api/llm/invoke.
		resp.NERRun.Envelope = mustJSON(nerEnv)
	}
	if composeErr != nil {
		resp.FailedStage = "compose"
		resp.Error = composeErr.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}

	composeEnv.Output = llm.StripMarkdownFences(composeEnv.Output)
	// Normalize taxonomic label-format dates (DD-<roman>-YYYY)
	// to ISO 8601 in the compose output. Deterministic Go parse
	// (pkg/taxonomy/labeldate) — better than trusting the LLM
	// on a mechanical transform where it might slip. Verbatim
	// label form is preserved on verbatimDate; only the ISO
	// `date` field is (re)derived. See
	// project_taxonomic_date_conventions memory.
	composeEnv.Output = normalizeLabelDates(composeEnv.Output)

	composeRun, composeRunErr := s.persistComposeRun(r.Context(), body, composeEnv, nerRun.RunID)
	if composeRunErr != nil {
		slog.WarnContext(r.Context(), "pipeline: persist compose run failed",
			"err", composeRunErr, "provider", body.TreeProvider)
	}
	resp.ComposeRun = composeRun

	// Best-effort tree parse so the WUI can render without a
	// second fetch. Failure just leaves Tree unset — the raw
	// output is inside ComposeRun.RawOutput as JSON string, so
	// the WUI can retry the parse client-side.
	if json.Valid([]byte(composeEnv.Output)) {
		resp.Tree = json.RawMessage(composeEnv.Output)
	}

	writeJSON(w, http.StatusOK, resp)
}

// persistNERRun writes the NER extraction_run row and materializes
// per-fact assertions from its raw output. Returns the wire-shape
// run, the materialized-assertion count, any materialize error
// (surfaced separately so callers can render it distinctly from
// persistence failures), and any persistence error.
func (s *server) persistNERRun(
	ctx context.Context,
	req apiLLMPipelineRequest,
	env llm.Envelope,
) (apiLLMRun, int, error, error) {
	envJSON := mustJSON(env)
	run := hive.ExtractionRun{
		ReferenceID:   req.ReferenceID,
		Purpose:       "ner",
		SourceKind:    "llm",
		SourceID:      req.NERProvider,
		EnvelopeJSON:  string(envJSON),
		RawOutputJSON: env.Output,
		Notes:         req.Notes,
	}
	var (
		runID           string
		materializedN   int
		materializedErr error
	)
	err := s.a.WithTx(ctx, func(tx *hive.Tx) error {
		id, err := tx.CreateExtractionRun(run)
		if err != nil {
			return err
		}
		runID = id
		saved := run
		saved.RunID = id
		n, mErr := tx.MaterializeAssertionsFromRun(ctx, saved)
		materializedN = n
		materializedErr = mErr
		// Don't roll back the tx on materialize error — the
		// LLM call succeeded and the run row is worth
		// preserving so a curator can inspect raw_output_json.
		return nil
	})
	if err != nil {
		return apiLLMRun{}, 0, materializedErr, err
	}
	fresh, gErr := s.a.GetExtractionRun(ctx, runID)
	if gErr != nil {
		return apiLLMRun{}, 0, materializedErr, gErr
	}
	return runToAPI(*fresh), materializedN, materializedErr, nil
}

// persistComposeRun writes the compose extraction_run row linked
// to its parent NER run via parent_run_id. No assertion
// materialization — compose emits a tree, not per-fact assertions;
// materialization of the tree into hive__reference_assertion rows
// is a separate design that hasn't landed yet.
func (s *server) persistComposeRun(
	ctx context.Context,
	req apiLLMPipelineRequest,
	env llm.Envelope,
	parentRunID string,
) (apiLLMRun, error) {
	envJSON := mustJSON(env)
	run := hive.ExtractionRun{
		ReferenceID:   req.ReferenceID,
		Purpose:       "compose",
		SourceKind:    "llm",
		SourceID:      req.TreeProvider,
		EnvelopeJSON:  string(envJSON),
		RawOutputJSON: env.Output,
		ParentRunID:   parentRunID,
		Notes:         req.Notes,
	}
	var runID string
	err := s.a.WithTx(ctx, func(tx *hive.Tx) error {
		id, err := tx.CreateExtractionRun(run)
		if err != nil {
			return err
		}
		runID = id
		return nil
	})
	if err != nil {
		return apiLLMRun{}, err
	}
	fresh, gErr := s.a.GetExtractionRun(ctx, runID)
	if gErr != nil {
		return apiLLMRun{}, gErr
	}
	return runToAPI(*fresh), nil
}

// buildComposeInput assembles the two-section input compose_v1
// expects: paper JATS body + NER assertion JSON, delimited by
// markdown section headers matching the prompt's Input format.
func buildComposeInput(jatsBody, nerOutput string) string {
	return "# Paper JATS body\n\n" + jatsBody +
		"\n\n# NER assertions\n\n" + nerOutput + "\n"
}

// readReferenceJATS looks up the reference's first attached PDF
// sha and reads the JATS body from the sidecar. Returns a helpful
// error when nothing is attached (curator needs to upload a PDF)
// or when the sidecar has no jats.xml yet (GROBID hasn't run or
// the pipeline failed).
func (s *server) readReferenceJATS(ctx context.Context, refID string) (string, error) {
	shas, err := s.a.ReferencePDFs(ctx, refID)
	if err != nil {
		return "", fmt.Errorf("list attached PDFs: %w", err)
	}
	if len(shas) == 0 {
		return "", errors.New("no PDF attached to this reference — upload one via the reference-edit modal first")
	}
	data, err := sidecar.Read(s.archivePath, shas[0], "jats.xml")
	if err != nil {
		return "", fmt.Errorf("read jats.xml for sha %s: %w", shas[0], err)
	}
	if len(data) == 0 {
		return "", errors.New("attached PDF has no JATS sidecar yet — GROBID may still be processing, or the extraction failed")
	}
	return string(data), nil
}

// normalizeLabelDates walks compose_v1 output and rewrites the
// ISO `date` field on every type_material row by deterministically
// re-parsing its verbatim label form. Preserves the compose_v1
// {value, sources} wrapper and reuses the verbatimDate's source
// list — since the ISO form is derived from it, provenance is
// transparently the same. Returns the original string unchanged
// on any parse/marshal failure so a bug here can't corrupt
// otherwise-valid compose output.
func normalizeLabelDates(rawJSON string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &doc); err != nil {
		return rawJSON
	}
	tms, ok := doc["type_material"].([]any)
	if !ok {
		return rawJSON
	}
	changed := false
	for _, tmRaw := range tms {
		tm, ok := tmRaw.(map[string]any)
		if !ok {
			continue
		}
		verb, verbSources := unwrapValueAndSources(tm["verbatimDate"])
		if verb == "" {
			continue
		}
		iso, ok := labeldate.Parse(verb)
		if !ok {
			continue
		}
		cur, _ := unwrapValueAndSources(tm["date"])
		if cur == iso {
			continue
		}
		// Sources array reflects the verbatim origin — the ISO
		// form is provably derived from the same NER assertion.
		tm["date"] = map[string]any{
			"value":   iso,
			"sources": verbSources,
		}
		changed = true
	}
	if !changed {
		return rawJSON
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return rawJSON
	}
	return string(out)
}

// unwrapValueAndSources pulls (value, sources) out of a compose_v1
// {value, sources} field wrapper. Returns "" + nil for missing or
// wrong-shape fields; returns the plain value + nil when the field
// isn't wrapped (defensive against future prompt-shape drift).
func unwrapValueAndSources(field any) (string, []any) {
	if field == nil {
		return "", nil
	}
	m, ok := field.(map[string]any)
	if !ok {
		if s, ok := field.(string); ok {
			return s, nil
		}
		return "", nil
	}
	value, _ := m["value"].(string)
	sources, _ := m["sources"].([]any)
	return value, sources
}

// defaultMaxForModel returns a per-call output-token cap tuned to
// the model's own MaxOutputTokens limit (see [server.modelLimits]).
// Prevents mid-JSON truncation on a full-paper NER pass when the
// caller doesn't specify max_tokens.
//
// When the model's limits are unknown, fall back to 32_768 —
// high enough to cover most modern frontier models so an
// unknown-to-hive model with a real 32K+ cap doesn't silently
// truncate. If such a model has a lower cap than the fallback, the
// provider returns a 400 — loud diagnostic, cheaper failure mode
// than silent truncation.
func (s *server) defaultMaxForModel(ctx context.Context, provider, model string) int {
	if lim, ok := s.modelLimits(ctx, provider, model); ok && lim.MaxOutputTokens > 0 {
		return lim.MaxOutputTokens
	}
	return 32_768
}

// modelLimits returns the context window and output cap for model,
// preferring the provider's own models listing (fetched and cached
// on first use when the adapter supports listing) over hive's
// static limits table. A failed listing falls back to the table.
func (s *server) modelLimits(ctx context.Context, provider, model string) (llm.Limits, bool) {
	if model != "" {
		for _, m := range s.providerModels(ctx, provider) {
			if m.ID == model && m.MaxOutputTokens > 0 {
				return llm.Limits{ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens}, true
			}
		}
	}
	return llm.LookupLimits(model)
}

// providerModels returns the provider's models listing, from the
// per-process cache or fetched on demand when the adapter supports
// listing. Returns nil when the provider can't list models or the
// fetch fails; callers treat that as "unknown".
func (s *server) providerModels(ctx context.Context, provider string) []llm.ModelInfo {
	if provider == "" || s.llmRunner == nil {
		return nil
	}
	if models, ok := cachedModels(provider); ok {
		return models
	}
	p, err := s.llmRunner.Providers.Get(provider)
	if err != nil {
		return nil
	}
	lister, ok := p.(llm.ModelLister)
	if !ok {
		return nil
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		slog.WarnContext(ctx, "models list failed", "provider", provider, "err", err)
		return nil
	}
	storeModels(provider, models)
	return models
}

// providerKind returns the configured adapter kind for a provider
// name, or "" when the name is unknown.
func (s *server) providerKind(name string) string {
	for _, pc := range s.llmProviders {
		if pc.Name == name {
			return pc.Kind
		}
	}
	return ""
}

// thinkingSummaryFree reports whether a thinking summary can be
// requested for (provider, model) without adding cost.
func (s *server) thinkingSummaryFree(provider, model string) bool {
	return s.providerKind(provider) == llm.KindAnthropic && llm.ThinkingByDefault(model)
}

// thinkingSummaryProblem returns a client-facing reason why a
// thinking summary can't be requested for (provider, model), or ""
// when it can. Non-Anthropic providers ignore the request, so they
// pass. Anthropic models that don't think by default are refused
// because the request would turn thinking on and add cost.
func (s *server) thinkingSummaryProblem(provider, model string) string {
	if s.providerKind(provider) != llm.KindAnthropic {
		return ""
	}
	if !llm.ThinkingByDefault(model) {
		return fmt.Sprintf("thinking summary is not available for %s: the model does not think by default, so requesting it would increase cost", model)
	}
	return ""
}

// resolveModel returns the model name to use for a step —
// explicit override wins; falls back to the provider's configured
// DefaultModel.
func (s *server) resolveModel(providerName, override string) string {
	if override != "" {
		return override
	}
	for _, pc := range s.llmProviders {
		if pc.Name == providerName {
			return pc.DefaultModel
		}
	}
	return ""
}

// mustJSON marshals v or returns an empty byte slice on error
// (rare — we're marshaling our own struct). Log-and-continue
// keeps the pipeline flowing even if envelope encoding hiccups.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(fmt.Sprintf(`{"error":"marshal: %s"}`, err))
	}
	return b
}

// pin errors import in case future error-wrapping needs it.
var _ = errors.New

// Shipped-default prompt IDs the pipeline falls back to when the
// curator doesn't pick a specific fork. Bumped when a new version
// lands in pkg/prompts/ that we want to make the default.
const (
	defaultNERPromptID     = "ner_v6"
	defaultComposePromptID = "compose_v4"
)

// resolvePromptForRun resolves a prompt ID (curator fork UUID or
// shipped built-in name) to its content. Curator forks take
// precedence — a curator can fork a shipped name if they want,
// though the current fork UI generates fresh names.
//
// Returns (content, canonicalID, error). canonicalID is what to
// stamp on the extraction_run for provenance — the UUID for
// curator prompts, the shipped name for built-ins. Purpose
// mismatch (asking for a compose prompt but passing an NER
// prompt's ID) is rejected as a bad request.
func (s *server) resolvePromptForRun(
	ctx context.Context, promptID, expectedPurpose string,
) (content string, canonicalID string, err error) {
	if promptID == "" {
		return "", "", fmt.Errorf("prompt id is empty")
	}
	if p, gerr := s.a.GetPrompt(ctx, promptID); gerr == nil {
		if !p.IsActive {
			return "", "", fmt.Errorf(
				"prompt %q is deleted; pick another fork", promptID,
			)
		}
		if p.Purpose != expectedPurpose {
			return "", "", fmt.Errorf(
				"prompt %q has purpose %q, expected %q",
				promptID, p.Purpose, expectedPurpose,
			)
		}
		return p.Content, p.PromptID, nil
	} else if !errors.Is(gerr, hive.ErrNotFound) {
		return "", "", gerr
	}
	// Fall back to the shipped library.
	builtin, lerr := loadBuiltinPrompt(promptID)
	if lerr != nil {
		return "", "", fmt.Errorf(
			"prompt %q not found in curator forks or shipped library: %w",
			promptID, lerr,
		)
	}
	if got := purposeFromBuiltinName(promptID); got != expectedPurpose {
		return "", "", fmt.Errorf(
			"prompt %q has purpose %q, expected %q",
			promptID, got, expectedPurpose,
		)
	}
	return builtin, promptID, nil
}
