package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatWireFormat(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody openaiRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)

		_, _ = w.Write([]byte(`{
			"model": "gpt-x-2026-01",
			"choices": [{"message": {"role": "assistant", "content": "reply text"}}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 20}
		}`))
	}))
	defer srv.Close()

	// DisableCacheControl for this baseline test so the wire body
	// stays in the plain-string form the assertions below check.
	p := NewOpenAICompatWithOptions("test", srv.URL, "gpt-x",
		PlainCredential{Secret: "sk-test"},
		OpenAICompatOptions{DisableCacheControl: true})
	got, err := p.Complete(context.Background(), Request{
		System:         "system prompt",
		User:           "user input",
		MaxTokens:      512,
		Temperature:    0.2,
		ResponseFormat: "json",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotBody.Model != "gpt-x" {
		t.Errorf("body.Model = %q", gotBody.Model)
	}
	if len(gotBody.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(gotBody.Messages))
	}
	if gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "system prompt" {
		t.Errorf("system message wrong: %+v", gotBody.Messages[0])
	}
	if gotBody.Messages[1].Role != "user" || gotBody.Messages[1].Content != "user input" {
		t.Errorf("user message wrong: %+v", gotBody.Messages[1])
	}
	if gotBody.MaxTokens != 512 {
		t.Errorf("max_tokens = %d", gotBody.MaxTokens)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_object" {
		t.Errorf("response_format = %+v", gotBody.ResponseFormat)
	}

	if got.Text != "reply text" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.ModelVersionID != "gpt-x-2026-01" {
		t.Errorf("ModelVersionID = %q", got.ModelVersionID)
	}
	if got.InputTokens != 100 || got.OutputTokens != 20 {
		t.Errorf("tokens = %d/%d", got.InputTokens, got.OutputTokens)
	}
}

func TestOpenAICompatOmitsSystemWhenEmpty(t *testing.T) {
	var gotBody openaiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{
			"model": "m",
			"choices": [{"message": {"role": "assistant", "content": "ok"}}],
			"usage": {"prompt_tokens": 5, "completion_tokens": 2}
		}`))
	}))
	defer srv.Close()

	p := NewOpenAICompat("t", srv.URL, "m", PlainCredential{Secret: "k"})
	if _, err := p.Complete(context.Background(), Request{User: "u"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" {
		t.Errorf("messages = %+v, want single user message", gotBody.Messages)
	}
}

func TestOpenAICompatRejectsMissingModel(t *testing.T) {
	p := NewOpenAICompat("t", "http://ignored", "", PlainCredential{Secret: "k"})
	_, err := p.Complete(context.Background(), Request{User: "u"})
	if err == nil || !strings.Contains(err.Error(), "no model set") {
		t.Fatalf("expected model-missing error, got %v", err)
	}
}

// TestOpenAICompatCacheControlDefault confirms the system prompt
// goes on the wire as a content-blocks array with a
// cache_control:ephemeral marker when the adapter is used with
// its default options — this is the flag that lets OpenRouter
// keep Anthropic caching working transparently. The user asked
// for this to be on by default so a curator running --provider
// openrouter --model anthropic/claude-... doesn't silently lose
// the cache-read discount they'd have gotten with a direct
// Anthropic provider entry.
func TestOpenAICompatCacheControlDefault(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	p := NewOpenAICompat("t", srv.URL, "m", PlainCredential{Secret: "k"})
	if _, err := p.Complete(context.Background(), Request{
		System: "sys prompt content",
		User:   "u",
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("missing messages: %+v", gotBody)
	}
	sys, ok := msgs[0].(map[string]any)
	if !ok || sys["role"] != "system" {
		t.Fatalf("first message not a system role: %+v", msgs[0])
	}
	blocks, ok := sys["content"].([]any)
	if !ok {
		t.Fatalf("system content is not an array (should be blocks when cache_control is on): %T = %v", sys["content"], sys["content"])
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(blocks))
	}
	block, _ := blocks[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "sys prompt content" {
		t.Errorf("block text or type wrong: %+v", block)
	}
	cache, ok := block["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("missing cache_control on block: %+v", block)
	}
	if cache["type"] != "ephemeral" {
		t.Errorf("cache_control.type = %v, want ephemeral", cache["type"])
	}
}

// TestOpenAICompatCacheControlDisabled verifies the escape hatch:
// when DisableCacheControl is set (for strict endpoints that
// reject unknown fields), the system prompt goes on the wire as a
// plain string and no cache_control markers appear.
func TestOpenAICompatCacheControlDisabled(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatWithOptions("t", srv.URL, "m",
		PlainCredential{Secret: "k"},
		OpenAICompatOptions{DisableCacheControl: true})
	if _, err := p.Complete(context.Background(), Request{
		System: "sys prompt content",
		User:   "u",
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs := gotBody["messages"].([]any)
	sys := msgs[0].(map[string]any)
	if _, isString := sys["content"].(string); !isString {
		t.Fatalf("system content is not a string (DisableCacheControl should force plain-string form): %T = %v", sys["content"], sys["content"])
	}
	if b, _ := json.Marshal(gotBody); strings.Contains(string(b), "cache_control") {
		t.Errorf("cache_control leaked into disabled request:\n%s", b)
	}
}

func TestOpenAICompatSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad model"}`))
	}))
	defer srv.Close()

	p := NewOpenAICompat("t", srv.URL, "m", PlainCredential{Secret: "k"})
	_, err := p.Complete(context.Background(), Request{User: "u"})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("expected 400 + body in error, got %v", err)
	}
}
