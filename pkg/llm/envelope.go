package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Envelope wraps an LLM completion with the provenance metadata
// hive persists alongside every generated assertion. Downstream
// code (assertion staging, hive__assertion columns per task #212)
// stores these fields so any assertion traces back to the exact
// prompt content, provider, model snapshot, and invocation time
// that produced it.
//
// The prompt itself is identified two ways: PromptName (the human
// slug — "ner_v5", "compose_v1") is the reference to what was run;
// PromptVersion (sha256 of the system-prompt content actually sent)
// pins the byte-for-byte content — so a curator-authored fork
// under the same name is distinguishable from the shipped default.
type Envelope struct {
	PromptName     string    `json:"prompt_name"`
	PromptVersion  string    `json:"prompt_version"`
	Provider       string    `json:"provider"`
	ProviderKind   string    `json:"provider_kind"`
	Model          string    `json:"model"`
	ModelVersionID string    `json:"model_version_id"`
	InvokedAt      time.Time `json:"invoked_at"`
	InputTokens    int       `json:"input_tokens,omitempty"`
	OutputTokens   int       `json:"output_tokens,omitempty"`
	Output         string    `json:"output"`
}

// Invoke runs the provider with req and wraps the response in an
// Envelope. promptName is the caller-supplied slug — typically the
// name passed to pkg/prompts.Load. The system-prompt content sent
// to the model (req.System) is hashed into PromptVersion.
//
// Callers wanting to route the same prompt to multiple providers
// hold the *Provider values and call Invoke for each; the registry
// is only involved when the choice is dynamic.
func Invoke(
	ctx context.Context,
	p Provider,
	promptName string,
	req Request,
) (Envelope, error) {
	started := time.Now().UTC()
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return Envelope{}, fmt.Errorf("llm invoke %s: %w", promptName, err)
	}
	model := req.Model
	if model == "" {
		model = resp.ModelVersionID
	}
	return Envelope{
		PromptName:     promptName,
		PromptVersion:  HashPrompt(req.System),
		Provider:       p.Name(),
		ProviderKind:   p.Kind(),
		Model:          model,
		ModelVersionID: resp.ModelVersionID,
		InvokedAt:      started,
		InputTokens:    resp.InputTokens,
		OutputTokens:   resp.OutputTokens,
		Output:         resp.Text,
	}, nil
}

// HashPrompt returns the sha256 hex digest of the given prompt
// content. Exported so callers computing prompt IDs outside an
// Invoke call (e.g. UI code showing "you're about to run this
// version") get the same hash the envelope will record.
func HashPrompt(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// StripMarkdownFences returns s with a wrapping markdown code
// fence removed, if one is present. Handles both the plain
// ```...``` form and the ```json ... ``` (language-tagged) form.
// LLMs frequently wrap JSON output in fences despite explicit
// prompt instructions asking for raw JSON; stripping here keeps
// downstream json.Unmarshal / json.RawMessage consumers robust.
//
// Behaviour:
//   - Input without fences returns unchanged (trim-only).
//   - Input with a leading ```<lang>? and trailing ``` (with
//     optional whitespace around either) returns the inner
//     content trimmed.
//   - Malformed or partially-fenced input returns unchanged
//     rather than guessing — better a downstream parse error
//     the caller can log than silent corruption.
func StripMarkdownFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	// Drop opening fence + optional language tag on its own line.
	// Split off the first line; keep the rest.
	_, inner, ok := strings.Cut(trimmed, "\n")
	if !ok {
		return trimmed
	}
	// Drop trailing fence if present.
	if idx := strings.LastIndex(inner, "```"); idx >= 0 {
		inner = inner[:idx]
	}
	return strings.TrimSpace(inner)
}
