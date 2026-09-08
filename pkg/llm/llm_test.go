package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeProvider is a Provider stub for testing the Invoke wrapper
// and Registry without hitting the network. Behaviour is controlled
// by the closure fields.
type fakeProvider struct {
	name string
	kind string
	fn   func(ctx context.Context, req Request) (Response, error)
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Kind() string { return f.kind }
func (f *fakeProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return f.fn(ctx, req)
}

func TestHashPromptDeterministic(t *testing.T) {
	a := HashPrompt("some prompt content")
	b := HashPrompt("some prompt content")
	if a != b {
		t.Fatalf("hash not deterministic: %q vs %q", a, b)
	}
	c := HashPrompt("some prompt content ")
	if a == c {
		t.Fatalf("hash collided across differing inputs")
	}
	if len(a) != 64 {
		t.Fatalf("expected sha256 hex len 64, got %d", len(a))
	}
}

func TestInvokeStampsEnvelope(t *testing.T) {
	p := &fakeProvider{
		name: "test-provider",
		kind: KindOpenAICompat,
		fn: func(ctx context.Context, req Request) (Response, error) {
			return Response{
				Text:           "hello",
				ModelVersionID: "test-model-2026-01-01",
				InputTokens:    42,
				OutputTokens:   3,
			}, nil
		},
	}
	env, err := Invoke(context.Background(), p, "compose_v1", Request{
		Model:  "test-model",
		System: "prompt text",
		User:   "input json",
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if env.PromptName != "compose_v1" {
		t.Errorf("PromptName = %q", env.PromptName)
	}
	if env.PromptVersion != HashPrompt("prompt text") {
		t.Errorf("PromptVersion not hashed from System field")
	}
	if env.Provider != "test-provider" {
		t.Errorf("Provider = %q", env.Provider)
	}
	if env.ProviderKind != KindOpenAICompat {
		t.Errorf("ProviderKind = %q", env.ProviderKind)
	}
	if env.Model != "test-model" {
		t.Errorf("Model = %q", env.Model)
	}
	if env.ModelVersionID != "test-model-2026-01-01" {
		t.Errorf("ModelVersionID = %q", env.ModelVersionID)
	}
	if env.Output != "hello" {
		t.Errorf("Output = %q", env.Output)
	}
	if env.InputTokens != 42 || env.OutputTokens != 3 {
		t.Errorf("token counts = %d/%d", env.InputTokens, env.OutputTokens)
	}
	if env.InvokedAt.IsZero() {
		t.Errorf("InvokedAt not stamped")
	}
}

func TestInvokePropagatesProviderError(t *testing.T) {
	sentinel := errors.New("boom")
	p := &fakeProvider{
		name: "test",
		kind: KindAnthropic,
		fn: func(ctx context.Context, req Request) (Response, error) {
			return Response{}, sentinel
		},
	}
	_, err := Invoke(context.Background(), p, "ner_v5", Request{System: "p", User: "u"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("fresh registry non-empty: %v", names)
	}
	if _, err := r.Get("missing"); err == nil {
		t.Fatalf("Get on missing name should error")
	}
	p1 := &fakeProvider{name: "b", kind: KindAnthropic}
	p2 := &fakeProvider{name: "a", kind: KindOpenAICompat}
	r.Register(p1)
	r.Register(p2)
	got, err := r.Get("a")
	if err != nil || got.Name() != "a" {
		t.Fatalf("Get(a) = %v, %v", got, err)
	}
	names := r.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("Names not sorted: %v", names)
	}
	// Overwrite semantics.
	p2b := &fakeProvider{name: "a", kind: KindAnthropic}
	r.Register(p2b)
	got, _ = r.Get("a")
	if got.Kind() != KindAnthropic {
		t.Fatalf("overwrite did not take: kind = %q", got.Kind())
	}
}

func TestEnvCredential(t *testing.T) {
	t.Setenv("HIVE_LLM_TEST_KEY", "shhh")
	c := EnvCredential{VarName: "HIVE_LLM_TEST_KEY"}
	got, err := c.Reveal(context.Background())
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if got != "shhh" {
		t.Fatalf("Reveal = %q", got)
	}

	missing := EnvCredential{VarName: "HIVE_LLM_TEST_KEY_UNSET"}
	if _, err := missing.Reveal(context.Background()); err == nil {
		t.Fatalf("expected error for unset var")
	}
}

func TestPlainCredential(t *testing.T) {
	if _, err := (PlainCredential{}).Reveal(context.Background()); err == nil {
		t.Fatalf("empty plaintext should error")
	}
	got, err := (PlainCredential{Secret: "x"}).Reveal(context.Background())
	if err != nil || got != "x" {
		t.Fatalf("Reveal = %q, %v", got, err)
	}
}

func TestNewCredentialDiscriminator(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CredentialConfig
		wantErr string
	}{
		{"env ok", CredentialConfig{Source: "env", EnvVar: "X"}, ""},
		{"env missing var", CredentialConfig{Source: "env"}, "env_var"},
		{"plaintext ok", CredentialConfig{Source: "plaintext", Value: "x"}, ""},
		{"keyring ok", CredentialConfig{Source: "keyring", Value: "some-label"}, ""},
		{"keyring missing label", CredentialConfig{Source: "keyring"}, "label"},
		{"unknown source", CredentialConfig{Source: "bogus"}, "unknown source"},
		{"empty source", CredentialConfig{}, "source is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCredential(tc.cfg)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q missing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewProviderRoutesByKind(t *testing.T) {
	t.Setenv("HIVE_LLM_TEST_KEY", "shhh")
	base := CredentialConfig{Source: "env", EnvVar: "HIVE_LLM_TEST_KEY"}

	p, err := NewProvider(ProviderConfig{
		Name: "openai-compat-test", Kind: KindOpenAICompat,
		BaseURL: "https://example.invalid/v1", DefaultModel: "gpt-x",
		Credential: base,
	})
	if err != nil {
		t.Fatalf("openai_compat: %v", err)
	}
	if p.Kind() != KindOpenAICompat {
		t.Errorf("got kind %q", p.Kind())
	}

	p, err = NewProvider(ProviderConfig{
		Name: "anthropic-test", Kind: KindAnthropic,
		DefaultModel: "claude-x",
		Credential:   base,
	})
	if err != nil {
		t.Fatalf("anthropic: %v", err)
	}
	if p.Kind() != KindAnthropic {
		t.Errorf("got kind %q", p.Kind())
	}

	if _, err := NewProvider(ProviderConfig{Name: "x", Kind: "unknown", Credential: base}); err == nil {
		t.Fatalf("expected error on unknown kind")
	}
	if _, err := NewProvider(ProviderConfig{Kind: KindAnthropic, Credential: base}); err == nil {
		t.Fatalf("expected error on missing name")
	}
	if _, err := NewProvider(ProviderConfig{Name: "x", Kind: KindOpenAICompat, Credential: base}); err == nil {
		t.Fatalf("expected error on missing base_url for openai_compat")
	}
}
