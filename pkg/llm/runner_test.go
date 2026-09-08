package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func newTestRunner(t *testing.T, p Provider, prompt string) *Runner {
	t.Helper()
	reg := NewRegistry()
	if p != nil {
		reg.Register(p)
	}
	return &Runner{
		Providers: reg,
		LoadPrompt: func(name string) (string, error) {
			if name == "missing" {
				return "", errors.New("no such prompt")
			}
			return prompt, nil
		},
	}
}

func TestRunnerRunAppliesOptionsAndStampsEnvelope(t *testing.T) {
	var gotReq Request
	p := &fakeProvider{
		name: "test",
		kind: KindAnthropic,
		fn: func(_ context.Context, req Request) (Response, error) {
			gotReq = req
			return Response{Text: `{"ok":true}`, ModelVersionID: "m-1"}, nil
		},
	}
	r := newTestRunner(t, p, "loaded prompt content")

	env, err := r.Run(context.Background(), "test", "some-prompt", "user input",
		WithModel("m-override"),
		WithMaxTokens(500),
		WithTemperature(0.1),
		WithResponseFormat("json"),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gotReq.System != "loaded prompt content" {
		t.Errorf("System = %q", gotReq.System)
	}
	if gotReq.User != "user input" {
		t.Errorf("User = %q", gotReq.User)
	}
	if gotReq.Model != "m-override" {
		t.Errorf("Model = %q", gotReq.Model)
	}
	if gotReq.MaxTokens != 500 || gotReq.Temperature != 0.1 {
		t.Errorf("MaxTokens/Temperature not applied: %d / %v", gotReq.MaxTokens, gotReq.Temperature)
	}
	if gotReq.ResponseFormat != "json" {
		t.Errorf("ResponseFormat = %q", gotReq.ResponseFormat)
	}
	if env.PromptName != "some-prompt" {
		t.Errorf("PromptName = %q", env.PromptName)
	}
	if env.PromptVersion != HashPrompt("loaded prompt content") {
		t.Errorf("PromptVersion did not hash the loaded prompt")
	}
	if env.Output != `{"ok":true}` {
		t.Errorf("Output = %q", env.Output)
	}
}

func TestRunnerRunSurfacesProviderLookupError(t *testing.T) {
	r := newTestRunner(t, nil, "prompt")
	_, err := r.Run(context.Background(), "nope", "p", "u")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected provider-lookup error, got %v", err)
	}
}

func TestRunnerRunSurfacesPromptLoadError(t *testing.T) {
	p := &fakeProvider{
		name: "test",
		kind: KindAnthropic,
		fn:   func(context.Context, Request) (Response, error) { return Response{}, nil },
	}
	r := newTestRunner(t, p, "prompt")
	_, err := r.Run(context.Background(), "test", "missing", "u")
	if err == nil || !strings.Contains(err.Error(), "no such prompt") {
		t.Fatalf("expected prompt-load error, got %v", err)
	}
}

func TestRunnerRunJSONParsesOutput(t *testing.T) {
	p := &fakeProvider{
		name: "test",
		kind: KindAnthropic,
		fn: func(context.Context, Request) (Response, error) {
			return Response{Text: `{"count":42,"label":"foo"}`, ModelVersionID: "m"}, nil
		},
	}
	r := newTestRunner(t, p, "prompt")

	var out struct {
		Count int    `json:"count"`
		Label string `json:"label"`
	}
	env, err := r.RunJSON(context.Background(), "test", "p", "u", &out)
	if err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	if out.Count != 42 || out.Label != "foo" {
		t.Errorf("unmarshaled = %+v", out)
	}
	if env.Output != `{"count":42,"label":"foo"}` {
		t.Errorf("Envelope.Output = %q", env.Output)
	}
}

func TestRunnerRunJSONReturnsEnvelopeOnParseFailure(t *testing.T) {
	p := &fakeProvider{
		name: "test",
		kind: KindAnthropic,
		fn: func(context.Context, Request) (Response, error) {
			return Response{Text: `not valid json`, ModelVersionID: "m"}, nil
		},
	}
	r := newTestRunner(t, p, "prompt")

	var out map[string]any
	env, err := r.RunJSON(context.Background(), "test", "p", "u", &out)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	// Envelope must survive parse failure so callers can log the
	// raw output for prompt-drift diagnostics.
	if env.Output != "not valid json" {
		t.Errorf("Envelope.Output = %q (expected raw model output)", env.Output)
	}
	if env.PromptName != "p" {
		t.Errorf("Envelope.PromptName = %q", env.PromptName)
	}
}
