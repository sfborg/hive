package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sfborg/hive/pkg/config"
	"github.com/sfborg/hive/pkg/llm"
	"github.com/sfborg/hive/pkg/prompts"
)

// modelsCache is a per-server in-memory cache of provider
// models-listing responses. TTL keeps entries fresh across long
// sessions without hammering the upstream endpoint on every
// invoke-modal open. Shared across the process — safe for
// concurrent access via mu.
var modelsCache = struct {
	mu      sync.RWMutex
	entries map[string]modelsCacheEntry
}{entries: make(map[string]modelsCacheEntry)}

type modelsCacheEntry struct {
	models    []llm.ModelInfo
	fetchedAt time.Time
}

// modelsCacheTTL is how long a cached models list stays valid.
// One hour balances "curator adds a new model to their vendor
// account and wants it visible" against "don't hit /models on
// every modal open."
const modelsCacheTTL = time.Hour

// cachedModels returns the cached models list for provider name
// when fresh; empty + false when the cache is cold or expired.
func cachedModels(name string) ([]llm.ModelInfo, bool) {
	modelsCache.mu.RLock()
	defer modelsCache.mu.RUnlock()
	e, ok := modelsCache.entries[name]
	if !ok {
		return nil, false
	}
	if time.Since(e.fetchedAt) > modelsCacheTTL {
		return nil, false
	}
	return e.models, true
}

func storeModels(name string, models []llm.ModelInfo) {
	modelsCache.mu.Lock()
	defer modelsCache.mu.Unlock()
	modelsCache.entries[name] = modelsCacheEntry{
		models:    models,
		fetchedAt: time.Now(),
	}
}

// apiLLMModelList is the wire shape returned by
// GET /api/llm/providers/{name}/models.
type apiLLMModelList struct {
	Items []llm.ModelInfo `json:"items" swaggertype:"array,object"`
	// PricingSource names where any listed_price values came from
	// (the provider's API host), so the WUI can attribute them.
	// Empty when no model in the list carries a listed price.
	PricingSource string `json:"pricing_source,omitempty"`
}

// handleListLLMProviderModels calls the provider's models-listing
// endpoint (via its optional ModelLister interface) and returns
// the normalized model list. Cached per-process for
// [modelsCacheTTL] so repeated invoke-modal opens don't hammer
// the upstream; ?fresh=1 bypasses the cache (and refreshes it) so
// listed prices shown to the curator are current. 501 when the underlying adapter doesn't support
// models listing — the WUI falls back to a free-text override
// input in that case.
//
// @Summary      List models for a provider
// @Description  Call the provider's models-listing endpoint and return the normalized model list. Cached per-process for an hour.
// @Tags         llm
// @Produce      json
// @Param        name path string true "Provider name"
// @Param        fresh query bool false "Bypass the cache and fetch from the provider"
// @Success      200 {object} apiLLMModelList
// @Failure      400 {object} problem
// @Failure      501 {object} map[string]string
// @Failure      502 {object} problem
// @Router       /llm/providers/{name}/models [get]
func (s *server) handleListLLMProviderModels(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeBadRequest(w, r, "provider name is required")
		return
	}
	if s.llmRunner == nil {
		writeBadRequest(w, r, "no llm providers configured")
		return
	}
	fresh := r.URL.Query().Get("fresh") == "1" || r.URL.Query().Get("fresh") == "true"
	if cached, ok := cachedModels(name); ok && !fresh {
		writeJSON(w, http.StatusOK, s.modelListResponse(name, cached))
		return
	}
	provider, err := s.llmRunner.Providers.Get(name)
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	lister, ok := provider.(llm.ModelLister)
	if !ok {
		writeJSON(w, http.StatusNotImplemented,
			map[string]string{"error": "provider adapter does not support models listing"})
		return
	}
	models, err := lister.ListModels(r.Context())
	if err != nil {
		writeProblem(w, r, fmt.Errorf("models list: %w", err))
		return
	}
	storeModels(name, models)
	writeJSON(w, http.StatusOK, s.modelListResponse(name, models))
}

// modelListResponse wraps models for the wire, attributing listed
// prices to the provider's API host (falling back to the provider
// name when no base URL is configured).
func (s *server) modelListResponse(provider string, models []llm.ModelInfo) apiLLMModelList {
	out := apiLLMModelList{Items: models}
	for _, m := range models {
		if m.ListedPrice == nil {
			continue
		}
		out.PricingSource = provider
		for _, pc := range s.llmProviders {
			if pc.Name == provider && pc.BaseURL != "" {
				if u, err := url.Parse(pc.BaseURL); err == nil && u.Host != "" {
					out.PricingSource = u.Host
				}
			}
		}
		break
	}
	return out
}

// apiLLMProviderCreateRequest is the WUI-paste-flow body for POST
// /api/llm/providers. Source discriminates how the credential is
// stored:
//
//   - "keyring" (default): APIKey is written to the OS keyring via
//     [llm.SetKeyringSecret] and never touches the config file.
//     Best default on native desktop; broken inside containers
//     without a D-Bus bridge to a host keyring.
//   - "env": EnvVar names an environment variable that the server
//     reads at invoke time. No secret is stored anywhere by hive;
//     the operator manages the env value out-of-band (docker-compose
//     environment, systemd unit, .env file, ...).
//   - "plaintext": APIKey is written directly into the config file.
//     Least secure — the file lands with 0600 perms but a plaintext
//     API key is an obvious grep target for info-stealers. Useful
//     for quick tests when neither keyring nor env fits.
type apiLLMProviderCreateRequest struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	DefaultModel string `json:"default_model,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	Source       string `json:"source,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	EnvVar       string `json:"env_var,omitempty"`
}

// handleCreateLLMProvider adds a new provider entry to the user
// config and stores its API key in the OS keyring. Live: the new
// provider is registered in the runner's registry so subsequent
// invocations can use it without a restart.
//
// Rejects duplicates: the WUI should PATCH to update (once we add
// that endpoint) rather than shadow-with-later-wins semantics that
// would leave orphaned keyring entries.
//
// @Summary      Create an LLM provider
// @Description  Add a new provider entry to the user config and store its API key per the requested source (keyring / env / plaintext). Live-registers the provider so subsequent invocations work without a restart.
// @Tags         llm
// @Accept       json
// @Produce      json
// @Param        body body apiLLMProviderCreateRequest true "Provider create body"
// @Success      201 {object} apiLLMProvider
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/providers [post]
func (s *server) handleCreateLLMProvider(w http.ResponseWriter, r *http.Request) {
	var body apiLLMProviderCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.Name == "" {
		writeBadRequest(w, r, "name is required")
		return
	}
	if body.Kind == "" {
		writeBadRequest(w, r, "kind is required (anthropic | openai_compat)")
		return
	}
	if body.Kind == llm.KindOpenAICompat && body.BaseURL == "" {
		writeBadRequest(w, r, "base_url is required for openai_compat providers")
		return
	}

	// Default source is "keyring" — matches the previous single-
	// mode behaviour so unchanged clients still work.
	source := body.Source
	if source == "" {
		source = "keyring"
	}
	// Per-source validation. Each mode has a different required
	// field set; catch omissions before touching the keyring or
	// writing to the config file.
	switch source {
	case "keyring", "plaintext":
		if body.APIKey == "" {
			writeBadRequest(w, r, "api_key is required for source "+source)
			return
		}
	case "env":
		if body.EnvVar == "" {
			writeBadRequest(w, r, "env_var is required for source env")
			return
		}
	default:
		writeBadRequest(w, r, fmt.Sprintf("unknown source %q (accepted: keyring | env | plaintext)", source))
		return
	}

	// Reject duplicates so the config never has two entries with
	// the same name (which would silently shadow via last-wins).
	cfg, err := config.Load()
	if err != nil {
		writeProblem(w, r, fmt.Errorf("load config: %w", err))
		return
	}
	for _, existing := range cfg.LLMProviders {
		if existing.Name == body.Name {
			writeBadRequest(w, r, fmt.Sprintf("provider %q already exists (delete first if replacing)", body.Name))
			return
		}
	}

	// Build the CredentialConfig for the chosen source. Keyring
	// mode writes the secret to the keyring first — if that fails
	// (e.g., no D-Bus session inside a container), we haven't
	// touched config yet and there's nothing to roll back.
	credCfg := llm.CredentialConfig{Source: source}
	switch source {
	case "keyring":
		if err := llm.SetKeyringSecret(body.Name, body.APIKey); err != nil {
			writeProblem(w, r, fmt.Errorf("keyring write: %w", err))
			return
		}
		credCfg.Value = body.Name
	case "env":
		credCfg.EnvVar = body.EnvVar
	case "plaintext":
		credCfg.Value = body.APIKey
	}

	pc := llm.ProviderConfig{
		Name:         body.Name,
		Kind:         body.Kind,
		BaseURL:      body.BaseURL,
		DefaultModel: body.DefaultModel,
		Credential:   credCfg,
	}
	cfg.LLMProviders = append(cfg.LLMProviders, pc)
	if err := config.Save(cfg); err != nil {
		// Best-effort keyring cleanup — we've already stored the
		// secret but failed to save config, so the orphaned entry
		// would linger. No-op for env / plaintext modes.
		if source == "keyring" {
			_ = llm.DeleteKeyringSecret(body.Name)
		}
		writeProblem(w, r, fmt.Errorf("save config: %w", err))
		return
	}

	// Live-register so this hive instance can use the new provider
	// immediately, without a restart. Runner may be nil when the
	// server started with an empty llm_providers list; construct
	// it on demand in that case.
	provider, err := llm.NewProvider(pc)
	if err != nil {
		writeProblem(w, r, fmt.Errorf("build provider: %w", err))
		return
	}
	if s.llmRunner == nil {
		s.llmRunner = &llm.Runner{
			Providers:  llm.NewRegistry(),
			LoadPrompt: prompts.Load,
		}
	}
	s.llmRunner.Providers.Register(provider)
	s.llmProviders = cfg.LLMProviders

	writeJSON(w, http.StatusCreated, apiLLMProvider{
		Name:         pc.Name,
		Kind:         pc.Kind,
		DefaultModel: pc.DefaultModel,
	})
}

// apiLLMProviderPatchRequest updates an existing provider in place.
// All fields are optional; non-empty fields overwrite. Rules:
//
//   - Name is fixed (URL param) — rename is delete + create.
//   - Kind / DefaultModel / BaseURL: set if non-empty.
//   - Source: when supplied, swaps the credential entirely (any
//     previous keyring secret under this provider's name is
//     deleted as part of the switch).
//   - APIKey: when supplied AND the (possibly-swapped) source is
//     keyring or plaintext, rotates the stored value. Empty
//     APIKey leaves the credential alone — that's the "change
//     the default model but keep the current key" flow.
//   - EnvVar: when supplied AND source ends up "env", updates
//     the env-var name.
type apiLLMProviderPatchRequest struct {
	Kind         string `json:"kind,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	Source       string `json:"source,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	EnvVar       string `json:"env_var,omitempty"`
}

// handlePatchLLMProvider applies a partial update to an existing
// provider entry. Live-re-registers the modified provider in the
// runner's registry so subsequent invocations use the new config
// without a server restart.
//
// @Summary      Patch an LLM provider
// @Description  Partial update to an existing provider (kind, default model, base URL, credential source, rotated API key). Live-re-registers the entry.
// @Tags         llm
// @Accept       json
// @Produce      json
// @Param        name path string true "Provider name"
// @Param        body body apiLLMProviderPatchRequest true "Provider patch body"
// @Success      200 {object} apiLLMProvider
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/providers/{name} [patch]
func (s *server) handlePatchLLMProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeBadRequest(w, r, "provider name is required")
		return
	}
	var body apiLLMProviderPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeProblem(w, r, fmt.Errorf("load config: %w", err))
		return
	}
	idx := -1
	for i, existing := range cfg.LLMProviders {
		if existing.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeBadRequest(w, r, fmt.Sprintf("provider %q not found", name))
		return
	}
	pc := cfg.LLMProviders[idx]

	// Apply non-credential fields first — cheap and safe.
	if body.Kind != "" {
		pc.Kind = body.Kind
	}
	if body.DefaultModel != "" {
		pc.DefaultModel = body.DefaultModel
	}
	if body.BaseURL != "" {
		pc.BaseURL = body.BaseURL
	}
	// Kind == openai_compat still requires a base URL.
	if pc.Kind == llm.KindOpenAICompat && pc.BaseURL == "" {
		writeBadRequest(w, r, "base_url is required for openai_compat providers")
		return
	}

	// Credential update. Three cases:
	//
	//   1. Source changed: swap entirely, deleting any old keyring
	//      entry.
	//   2. Source unchanged + APIKey supplied: rotate the stored
	//      value (keyring or plaintext).
	//   3. Source unchanged + no APIKey: leave credential alone.
	oldSource := pc.Credential.Source
	if body.Source != "" && body.Source != oldSource {
		// Source swap. Drop old keyring entry if we had one.
		if oldSource == "keyring" {
			_ = llm.DeleteKeyringSecret(name)
		}
		switch body.Source {
		case "keyring":
			if body.APIKey == "" {
				writeBadRequest(w, r, "api_key is required when switching to source keyring")
				return
			}
			if err := llm.SetKeyringSecret(name, body.APIKey); err != nil {
				writeProblem(w, r, fmt.Errorf("keyring write: %w", err))
				return
			}
			pc.Credential = llm.CredentialConfig{Source: "keyring", Value: name}
		case "env":
			if body.EnvVar == "" {
				writeBadRequest(w, r, "env_var is required when switching to source env")
				return
			}
			pc.Credential = llm.CredentialConfig{Source: "env", EnvVar: body.EnvVar}
		case "plaintext":
			if body.APIKey == "" {
				writeBadRequest(w, r, "api_key is required when switching to source plaintext")
				return
			}
			pc.Credential = llm.CredentialConfig{Source: "plaintext", Value: body.APIKey}
		default:
			writeBadRequest(w, r, fmt.Sprintf("unknown source %q", body.Source))
			return
		}
	} else {
		// Source unchanged. Rotate value if given.
		if body.APIKey != "" {
			switch pc.Credential.Source {
			case "keyring":
				if err := llm.SetKeyringSecret(name, body.APIKey); err != nil {
					writeProblem(w, r, fmt.Errorf("keyring rotate: %w", err))
					return
				}
			case "plaintext":
				pc.Credential.Value = body.APIKey
			}
		}
		if body.EnvVar != "" && pc.Credential.Source == "env" {
			pc.Credential.EnvVar = body.EnvVar
		}
	}

	cfg.LLMProviders[idx] = pc
	if err := config.Save(cfg); err != nil {
		writeProblem(w, r, fmt.Errorf("save config: %w", err))
		return
	}

	// Re-register in the live runner so subsequent invocations
	// use the new config without a restart. Registry.Register
	// overwrites the same-name entry.
	provider, err := llm.NewProvider(pc)
	if err != nil {
		writeProblem(w, r, fmt.Errorf("rebuild provider: %w", err))
		return
	}
	if s.llmRunner == nil {
		s.llmRunner = &llm.Runner{
			Providers:  llm.NewRegistry(),
			LoadPrompt: prompts.Load,
		}
	}
	s.llmRunner.Providers.Register(provider)
	s.llmProviders = cfg.LLMProviders

	writeJSON(w, http.StatusOK, apiLLMProvider{
		Name:         pc.Name,
		Kind:         pc.Kind,
		DefaultModel: pc.DefaultModel,
	})
}

// handleDeleteLLMProvider removes the named provider from the user
// config AND deletes its keyring entry. Idempotent — deleting an
// already-absent provider succeeds with 204 so a WUI double-click
// doesn't 404.
//
// The live runner's registry keeps the provider until the process
// restarts (Registry has no Deregister — deliberate to keep
// invocations in flight uninterrupted). The next hive restart
// picks up the config-cleared state and the deleted provider is
// gone for good.
//
// @Summary      Delete an LLM provider
// @Description  Remove the named provider from the user config and delete its keyring entry. Idempotent — deleting an already-absent provider returns 204.
// @Tags         llm
// @Param        name path string true "Provider name"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /llm/providers/{name} [delete]
func (s *server) handleDeleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeBadRequest(w, r, "provider name is required")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeProblem(w, r, fmt.Errorf("load config: %w", err))
		return
	}

	// Drop the entry (order-preserving so config diff stays clean).
	kept := cfg.LLMProviders[:0]
	found := false
	for _, pc := range cfg.LLMProviders {
		if pc.Name == name {
			found = true
			continue
		}
		kept = append(kept, pc)
	}
	if !found {
		// Idempotent — nothing to do. Still try to clean the
		// keyring in case it's orphaned from a prior partial delete.
		_ = llm.DeleteKeyringSecret(name)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cfg.LLMProviders = kept
	if err := config.Save(cfg); err != nil {
		writeProblem(w, r, fmt.Errorf("save config: %w", err))
		return
	}

	// Best-effort keyring cleanup — no-op when the deleted entry
	// was never a keyring-source provider. Missing entries are not
	// errors (see llm.DeleteKeyringSecret).
	if err := llm.DeleteKeyringSecret(name); err != nil {
		// Log-and-continue: the config change already landed.
		// Leaving an orphaned keyring entry is a minor
		// housekeeping issue, not a request failure.
		_ = err
	}

	s.llmProviders = cfg.LLMProviders
	w.WriteHeader(http.StatusNoContent)
}

