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

		resp := anthropicResponse{Model: "claude-opus-4-7-20260101"}
		resp.Content = append(resp.Content, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: "hello "}, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: "world"})
		resp.Usage.InputTokens = 40
		resp.Usage.CacheReadInputTokens = 60
		resp.Usage.CacheCreationInputTokens = 5
		resp.Usage.OutputTokens = 10
		_ = json.NewEncoder(w).Encode(resp)
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
		resp := anthropicResponse{Model: "m"}
		resp.Content = append(resp.Content, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: "ok"})
		_ = json.NewEncoder(w).Encode(resp)
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
		resp := anthropicResponse{Model: "m"}
		resp.Content = append(resp.Content, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: "ok"})
		_ = json.NewEncoder(w).Encode(resp)
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
