package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// anthropicAPIVersion is the API-version header value hive pins
// its requests to. Anthropic requires this header on every call;
// bumping it in one place shifts the whole client atomically.
const anthropicAPIVersion = "2023-06-01"

// anthropicDefaultMaxTokens is the fallback when a Request leaves
// MaxTokens at zero. Anthropic requires an explicit max_tokens on
// every /v1/messages call, so hive supplies a default rather than
// reject the request. Set to Haiku's per-request output ceiling so
// the fallback is usable across the current Claude 4.x line
// without truncating — Sonnet and Opus support more, but callers
// that want their full capacity should set Request.MaxTokens
// explicitly (the pipeline handler does this per-model). Anthropic
// bills only for actual output tokens, not for the max_tokens
// setting, so an over-generous cap has no cost impact.
const anthropicDefaultMaxTokens = 8192

// Anthropic is the native adapter for the Anthropic /v1/messages
// endpoint. Preferred over OpenAI-compat for Anthropic models
// because it unlocks prompt caching (the system prompt is marked
// cache_control: ephemeral, which typically wins ~90% off input
// tokens on repeated NER calls against the same prompt) and gives
// access to the full model surface.
type Anthropic struct {
	name         string
	baseURL      string
	defaultModel string
	cred         Credential
	client       *http.Client
}

// NewAnthropic builds the adapter. An empty baseURL falls back to
// the public API host so most callers can leave it unset.
func NewAnthropic(name, baseURL, defaultModel string, cred Credential) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &Anthropic{
		name:         name,
		baseURL:      strings.TrimRight(baseURL, "/"),
		defaultModel: defaultModel,
		cred:         cred,
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

// Name implements Provider.
func (a *Anthropic) Name() string { return a.name }

// Kind implements Provider.
func (a *Anthropic) Kind() string { return KindAnthropic }

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicRequest struct {
	Model       string                  `json:"model"`
	Messages    []anthropicMessage      `json:"messages"`
	System      []anthropicContentBlock `json:"system,omitempty"`
	MaxTokens   int                     `json:"max_tokens"`
	Temperature float64                 `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// Complete implements Provider. Wire format: POST {baseURL}/v1/messages
// with x-api-key + anthropic-version headers. System-prompt content
// is placed in the top-level `system` field with ephemeral
// cache_control so repeated invocations against the same prompt
// hit the cache.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	model := req.Model
	if model == "" {
		model = a.defaultModel
	}
	if model == "" {
		return Response{}, errors.New("anthropic: no model set (Request.Model empty and no DefaultModel configured)")
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = anthropicDefaultMaxTokens
	}

	body := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "text", Text: req.User},
				},
			},
		},
	}
	if req.System != "" {
		body.System = []anthropicContentBlock{
			{
				Type:         "text",
				Text:         req.System,
				CacheControl: &anthropicCacheControl{Type: "ephemeral"},
			},
		}
	}

	apiKey, err := a.cred.Reveal(ctx)
	if err != nil {
		return Response{}, err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("anthropic http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Response{}, fmt.Errorf("anthropic decode: %w", err)
	}
	if len(out.Content) == 0 {
		return Response{}, errors.New("anthropic: response had no content blocks")
	}

	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}

	return Response{
		Text:           text.String(),
		ModelVersionID: out.Model,
		InputTokens:    out.Usage.InputTokens + out.Usage.CacheReadInputTokens + out.Usage.CacheCreationInputTokens,
		OutputTokens:   out.Usage.OutputTokens,
	}, nil
}
