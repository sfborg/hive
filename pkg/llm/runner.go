package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// PromptLoaderFunc looks up a prompt template by name. Matches the
// signature of pkg/prompts.Load — callers wire that in directly:
//
//	runner := &llm.Runner{
//	    Providers:  registry,
//	    LoadPrompt: prompts.Load,
//	}
//
// The function-typed dep (rather than importing pkg/prompts here)
// keeps pkg/llm free of hive-internal imports, preserving the
// lift-to-sflib property. Any caller with a "name → prompt text"
// mapping can plug in.
type PromptLoaderFunc func(name string) (string, error)

// Runner ties a provider registry together with a prompt loader so
// callers invoke a prompt by name in one call instead of
// hand-composing Registry.Get + LoadPrompt + Invoke.
//
// Both fields must be set — the zero value is not useful. Runner
// itself is stateless past construction; concurrent-safe when
// Providers is a *Registry (its default) and LoadPrompt is a pure
// function.
type Runner struct {
	Providers  *Registry
	LoadPrompt PromptLoaderFunc
}

// RunOption tweaks the [Request] passed to the provider. Fields
// left unset by any option keep their zero value (which each
// adapter treats as "provider default").
type RunOption func(*Request)

// WithModel overrides the provider's DefaultModel for this call.
func WithModel(m string) RunOption { return func(r *Request) { r.Model = m } }

// WithMaxTokens caps the completion length.
func WithMaxTokens(n int) RunOption { return func(r *Request) { r.MaxTokens = n } }

// WithTemperature sets sampling temperature (0..1).
func WithTemperature(t float64) RunOption { return func(r *Request) { r.Temperature = t } }

// WithResponseFormat sets the response-format hint. "json" turns
// on OpenAI-compat json_object mode; ignored by adapters that
// don't support it.
func WithResponseFormat(f string) RunOption { return func(r *Request) { r.ResponseFormat = f } }

// Run loads the named prompt, sends it as the system message with
// input as the user message, and returns the invocation Envelope.
// The caller interprets Envelope.Output — use [Runner.RunJSON] for
// the common case where the prompt is instructed to emit JSON
// (hive's ner_v5 and compose_v1 both do).
func (r *Runner) Run(
	ctx context.Context,
	providerName, promptName, input string,
	opts ...RunOption,
) (Envelope, error) {
	prompt, err := r.LoadPrompt(promptName)
	if err != nil {
		return Envelope{}, fmt.Errorf("llm run %s: load prompt: %w", promptName, err)
	}
	return r.RunWithPrompt(ctx, providerName, promptName, prompt, input, opts...)
}

// RunWithPrompt is the caller-supplies-content variant of Run.
// Uses when the prompt body doesn't come from the shipped library —
// curator forks stored in the DB, prompt-preview flows, tests that
// want to inject a synthetic prompt without a filesystem detour.
//
// promptName is still recorded on the envelope for provenance so
// downstream extraction_run rows can name what was actually used.
// For curator prompts pass the stable prompt_id (UUID); for
// built-ins pass the shipped name ("ner_v6").
func (r *Runner) RunWithPrompt(
	ctx context.Context,
	providerName, promptName, promptContent, input string,
	opts ...RunOption,
) (Envelope, error) {
	p, err := r.Providers.Get(providerName)
	if err != nil {
		return Envelope{}, err
	}
	req := Request{
		System: promptContent,
		User:   input,
	}
	for _, o := range opts {
		o(&req)
	}
	return Invoke(ctx, p, promptName, req)
}

// RunJSON runs the prompt via [Runner.Run] and unmarshals
// Envelope.Output into out. Returns the Envelope even on unmarshal
// error so a caller can inspect what the model actually produced
// when the parse fails — that's the diagnostic curators (and
// prompt authors) want when a prompt starts drifting.
func (r *Runner) RunJSON(
	ctx context.Context,
	providerName, promptName, input string,
	out any,
	opts ...RunOption,
) (Envelope, error) {
	env, err := r.Run(ctx, providerName, promptName, input, opts...)
	if err != nil {
		return env, err
	}
	if err := json.Unmarshal([]byte(env.Output), out); err != nil {
		return env, fmt.Errorf("llm run %s: parse JSON: %w", promptName, err)
	}
	return env, nil
}
