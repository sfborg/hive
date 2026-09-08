// Package llm is hive's LLM invocation client — a small provider
// interface, a registry, and adapters for the wire protocols hive
// speaks: OpenAI-compatible (covers OpenAI, Gemini, Ollama, LiteLLM,
// vLLM, and anything else with an OpenAI-shaped /chat/completions
// endpoint) and native Anthropic /v1/messages (for prompt caching
// and access to Anthropic-only features).
//
// The package's job is a single call — hand it a prompt and an
// input, get back the raw completion plus an [Envelope] recording
// which prompt, which provider, which model, and when. Everything
// upstream that persists LLM output (assertion staging, compose
// pipeline, future translation) stores the envelope alongside the
// result so provenance is never lost.
//
// LIFT-TO-SFLIB / STANDALONE CANDIDATE. Written self-contained (no
// hive-internal reach, no singletons) so it can migrate into sflib
// or graduate to its own repository as a reusable Go LLM client.
// TaxonWorks (and other SFBorg-adjacent tools) will want the same
// invocation shape — see the AI-toolpack project memory.
package llm

import (
	"context"
)

// Request is the abstract shape of an LLM invocation. Adapters
// translate this into the wire format their provider expects.
type Request struct {
	// Model is the provider-specific model identifier (e.g.
	// "claude-opus-4-7", "gpt-4o", "llama3.1:70b"). Empty falls
	// back to the provider's configured DefaultModel.
	Model string

	// System is the system-role prompt — the stable, cacheable
	// content (a NER / compose prompt template). For OpenAI-compat
	// providers this becomes the first "system" message; for
	// Anthropic it becomes the top-level `system` field with an
	// ephemeral cache_control marker so repeated calls hit the
	// prompt cache.
	System string

	// User is the user-role prompt — the per-invocation input
	// (JATS body, NER-assertion JSON, ...). For OpenAI-compat this
	// becomes a single "user" message; for Anthropic a single
	// user-role message with one text content block.
	User string

	// MaxTokens caps output length. Anthropic requires a value —
	// adapters supply a safe default of 4096 when zero. OpenAI-compat
	// treats zero as "provider default."
	MaxTokens int

	// Temperature 0..1. Zero-value means "leave to provider
	// default"; hive's NER/compose calls want low temperatures for
	// stable structured output but don't force that at the client
	// layer — the caller decides.
	Temperature float64

	// ResponseFormat hints at expected output shape. "json" asks
	// OpenAI-compat providers to constrain output via json_object
	// mode; ignored by providers that don't support it. Empty leaves
	// output unconstrained. Anthropic doesn't currently expose a
	// hard JSON mode — the prompt has to instruct it, which is what
	// hive's compose and NER prompts already do.
	ResponseFormat string
}

// Response is the adapter's normalised view of a completion. Text
// is the raw model output; downstream code parses it (e.g. as JSON)
// according to what the prompt asked for.
type Response struct {
	// Text is the raw completion — the concatenation of text content
	// blocks in the order the provider returned them.
	Text string

	// ModelVersionID is the provider's identifier for the specific
	// model snapshot that answered (e.g. an OpenAI response's
	// `model` echo, or Anthropic's returned model string). Recorded
	// on the Envelope so reproducibility survives model-version
	// rollovers on the provider side.
	ModelVersionID string

	// InputTokens / OutputTokens are the provider-reported usage
	// counters when available; zero when the provider does not
	// report them. Cache hits count as input tokens on Anthropic.
	InputTokens  int
	OutputTokens int
}

// Provider is the invocation surface every adapter implements.
// Concrete Providers are constructed via [NewProvider] (from a
// config-file entry) or directly by the caller.
//
// Providers must be safe for concurrent use — hive's HTTP handlers
// and background jobs can all share a single instance.
type Provider interface {
	// Name identifies the provider instance (matches the config
	// entry's Name — "anthropic-main", "ollama-local" — NOT the
	// wire kind).
	Name() string

	// Kind returns the adapter kind — currently
	// [KindOpenAICompat] or [KindAnthropic]. Recorded on the
	// Envelope for downstream filtering.
	Kind() string

	// Complete runs the request and returns the model's completion.
	// Context cancellation aborts the in-flight HTTP call.
	Complete(ctx context.Context, req Request) (Response, error)
}

// Well-known Kind values. New adapters add a constant here so
// callers can reason about the space without magic strings.
const (
	KindOpenAICompat = "openai_compat"
	KindAnthropic    = "anthropic"
)

// ModelInfo describes one model as returned by a provider's
// models-listing endpoint (OpenAI's /v1/models, OpenRouter's
// /api/v1/models, etc.). Prices are per-million tokens in USD;
// zero means "unknown / not returned by this provider" and
// callers should fall back to hive's static [Pricing] table.
type ModelInfo struct {
	ID                    string  `json:"id"`
	DisplayName           string  `json:"display_name,omitempty"`
	ContextWindow         int     `json:"context_window,omitempty"`
	MaxOutputTokens       int     `json:"max_output_tokens,omitempty"`
	InputPricePerMillion  float64 `json:"input_price_per_million,omitempty"`
	OutputPricePerMillion float64 `json:"output_price_per_million,omitempty"`
}

// ModelLister is the optional interface a Provider implements
// when its upstream API exposes a models-listing endpoint.
// Callers type-assert to detect support; providers without
// this interface fall back to the hive-static pricing table
// and the curator-typed model-override text-box.
//
// Kept optional (rather than a Provider-interface method) so
// adapters for providers that don't have a models endpoint —
// or for adapters where the endpoint isn't worth the code —
// don't have to stub out a "not supported" method.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}
