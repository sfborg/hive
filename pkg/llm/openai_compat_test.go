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

		resp := openaiResponse{Model: "gpt-x-2026-01"}
		resp.Choices = append(resp.Choices, struct {
			Message openaiMessage `json:"message"`
		}{Message: openaiMessage{Role: "assistant", Content: "reply text"}})
		resp.Usage.PromptTokens = 100
		resp.Usage.CompletionTokens = 20
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewOpenAICompat("test", srv.URL, "gpt-x", PlainCredential{Secret: "sk-test"})
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
		resp := openaiResponse{Model: "m"}
		resp.Choices = append(resp.Choices, struct {
			Message openaiMessage `json:"message"`
		}{Message: openaiMessage{Content: "ok"}})
		_ = json.NewEncoder(w).Encode(resp)
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
