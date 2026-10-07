package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicWireFormat(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion, gotContentType string
	var gotBody anthropicRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)

		writeAnthropicSSE(w, "claude-opus-4-7-20260101",
			anthropicUsage{InputTokens: 40, CacheReadInputTokens: 60, CacheCreationInputTokens: 5, OutputTokens: 10},
			sseBlock{"text", "hello "}, sseBlock{"text", "world"})
	}))
	defer srv.Close()

	p := NewAnthropic("test", srv.URL, "claude-opus-4-7", PlainCredential{Secret: "sk-ant-test"})
	got, err := p.Complete(context.Background(), Request{
		System: "system prompt",
		User:   "user input",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if gotPath != "/v1/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAPIKey != "sk-ant-test" {
		t.Errorf("x-api-key = %q", gotAPIKey)
	}
	if gotVersion != anthropicAPIVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, anthropicAPIVersion)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotBody.Model != "claude-opus-4-7" {
		t.Errorf("body.Model = %q", gotBody.Model)
	}
	if gotBody.MaxTokens != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens = %d, want default %d", gotBody.MaxTokens, anthropicDefaultMaxTokens)
	}
	if len(gotBody.System) != 1 || gotBody.System[0].Text != "system prompt" {
		t.Fatalf("system = %+v", gotBody.System)
	}
	if gotBody.System[0].CacheControl == nil || gotBody.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("expected ephemeral cache_control on system block, got %+v", gotBody.System[0].CacheControl)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v", gotBody.Messages)
	}
	if len(gotBody.Messages[0].Content) != 1 || gotBody.Messages[0].Content[0].Text != "user input" {
		t.Errorf("user content = %+v", gotBody.Messages[0].Content)
	}

	if got.Text != "hello world" {
		t.Errorf("Text = %q (should concatenate text blocks in order)", got.Text)
	}
	if got.ModelVersionID != "claude-opus-4-7-20260101" {
		t.Errorf("ModelVersionID = %q", got.ModelVersionID)
	}
	// Input tokens should sum non-cache + cache-read + cache-creation
	// so downstream cost accounting sees the real input footprint.
	if got.InputTokens != 40+60+5 {
		t.Errorf("InputTokens = %d, want 105 (input + cache_read + cache_creation)", got.InputTokens)
	}
	if got.OutputTokens != 10 {
		t.Errorf("OutputTokens = %d", got.OutputTokens)
	}
}

func TestAnthropicDefaultsBaseURL(t *testing.T) {
	p := NewAnthropic("t", "", "claude-x", PlainCredential{Secret: "k"})
	if p.baseURL != "https://api.anthropic.com" {
		t.Errorf("baseURL = %q", p.baseURL)
	}
}

func TestAnthropicOmitsSystemWhenEmpty(t *testing.T) {
	var gotBody anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		writeAnthropicSSE(w, "m", anthropicUsage{}, sseBlock{"text", "ok"})
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	if _, err := p.Complete(context.Background(), Request{User: "u"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(gotBody.System) != 0 {
		t.Errorf("expected empty system block, got %+v", gotBody.System)
	}
}

func TestAnthropicRespectsMaxTokensOverride(t *testing.T) {
	var gotBody anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		writeAnthropicSSE(w, "m", anthropicUsage{}, sseBlock{"text", "ok"})
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	if _, err := p.Complete(context.Background(), Request{User: "u", MaxTokens: 8192}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotBody.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want 8192", gotBody.MaxTokens)
	}
}

func TestAnthropicEffort(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = nil
		_ = json.Unmarshal(b, &raw)
		writeAnthropicSSE(w, "m", anthropicUsage{}, sseBlock{"text", "ok"})
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})

	env, err := Invoke(context.Background(), p, "ner_test", Request{User: "u", Effort: "low"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	oc, ok := raw["output_config"].(map[string]any)
	if !ok || oc["effort"] != "low" {
		t.Errorf("output_config = %v, want effort low", raw["output_config"])
	}
	if env.Effort != "low" {
		t.Errorf("envelope effort = %q, want low", env.Effort)
	}

	if _, err := p.Complete(context.Background(), Request{User: "u"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, present := raw["output_config"]; present {
		t.Errorf("output_config sent without effort: %v", raw["output_config"])
	}
}

func TestValidEffort(t *testing.T) {
	for _, e := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		if !ValidEffort(e) {
			t.Errorf("ValidEffort(%q) = false", e)
		}
	}
	for _, e := range []string{"none", "LOW", "minimal"} {
		if ValidEffort(e) {
			t.Errorf("ValidEffort(%q) = true", e)
		}
	}
}

func TestAnthropicSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	_, err := p.Complete(context.Background(), Request{User: "u"})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Fatalf("expected 401 + body in error, got %v", err)
	}
}

type sseBlock struct{ typ, text string }

// writeAnthropicSSE writes a streaming /v1/messages response in the
// server-sent event format the API uses.
func writeAnthropicSSE(w http.ResponseWriter, model string, usage anthropicUsage, blocks ...sseBlock) {
	w.Header().Set("Content-Type", "text/event-stream")
	ev := func(name string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	}
	start := usage
	start.OutputTokens = 1
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"model": model, "usage": start}})
	ev("ping", map[string]any{"type": "ping"})
	for i, b := range blocks {
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": b.typ}})
		delta := map[string]any{"type": "text_delta", "text": b.text}
		if b.typ == "thinking" {
			delta = map[string]any{"type": "thinking_delta", "thinking": b.text}
		}
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": delta})
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": usage.OutputTokens}})
	ev("message_stop", map[string]any{"type": "message_stop"})
}

func TestAnthropicStreamsAndRequestsStream(t *testing.T) {
	var raw map[string]any
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &raw)
		writeAnthropicSSE(w, "m", anthropicUsage{OutputTokens: 3}, sseBlock{"text", "ok"})
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	got, err := p.Complete(context.Background(), Request{User: "u"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if raw["stream"] != true {
		t.Errorf("stream = %v, want true", raw["stream"])
	}
	if accept != "text/event-stream" {
		t.Errorf("Accept = %q", accept)
	}
	if _, present := raw["thinking"]; present {
		t.Errorf("thinking sent without a summary request: %v", raw["thinking"])
	}
	if got.Text != "ok" || got.OutputTokens != 3 || got.Thinking != "" {
		t.Errorf("got %+v", got)
	}
}

func TestAnthropicThinkingSummary(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &raw)
		writeAnthropicSSE(w, "claude-sonnet-5-5", anthropicUsage{OutputTokens: 9},
			sseBlock{"thinking", "first pass"}, sseBlock{"thinking", "second pass"},
			sseBlock{"text", `{"assertions":[]}`})
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "claude-sonnet-5-5", PlainCredential{Secret: "k"})
	env, err := Invoke(context.Background(), p, "ner_test", Request{User: "u", ThinkingSummary: true})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	th, ok := raw["thinking"].(map[string]any)
	if !ok || th["type"] != "adaptive" || th["display"] != "summarized" {
		t.Errorf("thinking = %v, want adaptive + summarized", raw["thinking"])
	}
	if env.Thinking != "first pass\n\nsecond pass" {
		t.Errorf("envelope thinking = %q", env.Thinking)
	}
	if env.Output != `{"assertions":[]}` {
		t.Errorf("output = %q (thinking must not leak into output)", env.Output)
	}
}

func TestAnthropicThinkingSummaryRefusedWhenItWouldAddCost(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "claude-haiku-4-5", PlainCredential{Secret: "k"})
	_, err := p.Complete(context.Background(), Request{User: "u", ThinkingSummary: true})
	if err == nil || !strings.Contains(err.Error(), "increase cost") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if called {
		t.Error("request was sent to the provider")
	}
}

func TestThinkingByDefault(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-sonnet-5-5": true, "claude-sonnet-5": true, "claude-opus-5-5": true,
		"claude-opus-5": true, "claude-fable-5-1": true,
		"claude-opus-4-8": false, "claude-sonnet-4-6": false, "claude-haiku-4-5": false,
	} {
		if got := ThinkingByDefault(model); got != want {
			t.Errorf("ThinkingByDefault(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestAnthropicStreamEndedEarly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"{\\\"partial\"}}\n\n")
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	_, err := p.Complete(context.Background(), Request{User: "u"})
	if err == nil || !strings.Contains(err.Error(), "before message_stop") {
		t.Fatalf("err = %v, want early-end error", err)
	}
}

func TestAnthropicMidStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\"}}\n\n")
		fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	_, err := p.Complete(context.Background(), Request{User: "u"})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != 529 || !strings.Contains(pe.Body, "Overloaded") {
		t.Fatalf("err = %v, want *ProviderError 529", err)
	}
}

func TestAnthropicIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\"}}\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	p.idleTimeout = 100 * time.Millisecond
	_, err := p.Complete(context.Background(), Request{User: "u"})
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("err = %v, want ErrIdleTimeout", err)
	}
}

func TestAnthropicSlowStreamWithinIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\"}}\n\n")
		w.(http.Flusher).Flush()
		// Total duration exceeds the idle timeout, but no single gap does.
		for range 5 {
			time.Sleep(60 * time.Millisecond)
			fmt.Fprint(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	p.idleTimeout = 150 * time.Millisecond
	got, err := p.Complete(context.Background(), Request{User: "u"})
	if err != nil || got.Text != "done" {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestAnthropicListModels(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "k" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","max_input_tokens":1000000,"max_tokens":128000}],"has_more":true,"last_id":"claude-sonnet-5-5"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-haiku-4-5","display_name":"Claude Haiku 4.5","max_input_tokens":200000,"max_tokens":64000}],"has_more":false,"last_id":"claude-haiku-4-5"}`))
	}))
	defer srv.Close()

	p := NewAnthropic("t", srv.URL, "m", PlainCredential{Secret: "k"})
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || len(queries) != 2 {
		t.Fatalf("models = %+v, queries = %v", models, queries)
	}
	if m := models[0]; m.ID != "claude-sonnet-5-5" || m.ContextWindow != 1_000_000 || m.MaxOutputTokens != 128_000 {
		t.Errorf("models[0] = %+v", m)
	}
	if !strings.Contains(queries[1], "after_id=claude-sonnet-5-5") {
		t.Errorf("second page query = %q", queries[1])
	}
}
