package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// idleTimeout aborts a streaming call when the provider sends
	// nothing (not even a ping) for this long. There is no limit on
	// total duration.
	idleTimeout time.Duration
}

// anthropicIdleTimeout is the default [Anthropic.idleTimeout]. The
// API sends ping events while a response is being generated, so a
// healthy stream does not go silent for anywhere near this long;
// the timeout only catches dead connections.
const anthropicIdleTimeout = 15 * time.Minute

// anthropicModelsTimeout bounds a models-listing call.
const anthropicModelsTimeout = time.Minute

// ErrIdleTimeout is returned (wrapped) when a provider stops sending
// data mid-call for longer than the adapter's idle timeout.
var ErrIdleTimeout = errors.New("provider sent no data within the idle timeout")

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
		// No total timeout: responses are streamed and a long run is
		// cut off only when the connection goes idle. See
		// anthropicIdleTimeout.
		client:      &http.Client{},
		idleTimeout: anthropicIdleTimeout,
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
	Model        string                  `json:"model"`
	Messages     []anthropicMessage      `json:"messages"`
	System       []anthropicContentBlock `json:"system,omitempty"`
	MaxTokens    int                     `json:"max_tokens"`
	Temperature  float64                 `json:"temperature,omitempty"`
	OutputConfig *anthropicOutputConfig  `json:"output_config,omitempty"`
	Thinking     *anthropicThinking      `json:"thinking,omitempty"`
	Stream       bool                    `json:"stream"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type anthropicThinking struct {
	Type    string `json:"type"`
	Display string `json:"display,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// anthropicStreamEvent is the union of the server-sent event payloads
// hive reads from a streaming /v1/messages response. Fields not
// relevant to an event type stay zero.
type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Message struct {
		Model string         `json:"model"`
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
	Usage *anthropicUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ThinkingByDefault reports whether model runs extended thinking
// without being asked to. Requesting a thinking summary on these
// models changes only what is returned, not what is billed; on
// other models it would turn thinking on and increase output
// tokens.
func ThinkingByDefault(model string) bool {
	for _, p := range []string{
		"claude-sonnet-5", "claude-opus-5", "claude-fable-5", "claude-mythos-5",
	} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

// Complete implements Provider. Wire format: POST {baseURL}/v1/messages
// with x-api-key + anthropic-version headers and stream: true.
// System-prompt content is placed in the top-level `system` field
// with ephemeral cache_control so repeated invocations against the
// same prompt hit the cache.
//
// The response is read as a server-sent event stream. The call has
// no total time limit; it fails with [ErrIdleTimeout] only when the
// provider sends nothing for the adapter's idle timeout. A stream
// that ends before message_stop is an error, so a partial output is
// never returned as complete.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	model := req.Model
	if model == "" {
		model = a.defaultModel
	}
	if model == "" {
		return Response{}, errors.New("anthropic: no model set (Request.Model empty and no DefaultModel configured)")
	}
	if req.ThinkingSummary && !ThinkingByDefault(model) {
		return Response{}, fmt.Errorf("anthropic: thinking summary not requested for %s: the model does not think by default, so enabling it would increase cost", model)
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = anthropicDefaultMaxTokens
	}

	body := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      true,
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "text", Text: req.User},
				},
			},
		},
	}
	if req.Effort != "" {
		body.OutputConfig = &anthropicOutputConfig{Effort: req.Effort}
	}
	if req.ThinkingSummary {
		body.Thinking = &anthropicThinking{Type: "adaptive", Display: "summarized"}
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

	// The idle watchdog cancels the request when no bytes arrive for
	// idleTimeout, covering both the wait for response headers and
	// gaps between stream events.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	idle := time.AfterFunc(a.idleTimeout, func() { cancel(ErrIdleTimeout) })
	defer idle.Stop()
	wrapErr := func(stage string, err error) error {
		if cause := context.Cause(ctx); errors.Is(cause, ErrIdleTimeout) {
			return fmt.Errorf("anthropic %s: %w (%s)", stage, ErrIdleTimeout, a.idleTimeout)
		}
		return fmt.Errorf("anthropic %s: %w", stage, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return Response{}, wrapErr("http", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return Response{}, &ProviderError{Adapter: KindAnthropic, Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}

	var (
		out      Response
		text     strings.Builder
		thinking strings.Builder
		blocks   int
		usage    anthropicUsage
		stopped  bool
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		idle.Reset(a.idleTimeout)
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return Response{}, fmt.Errorf("anthropic decode event: %w", err)
		}
		switch ev.Type {
		case "message_start":
			out.ModelVersionID = ev.Message.Model
			usage = ev.Message.Usage
		case "content_block_start":
			blocks++
			if ev.ContentBlock.Type == "thinking" && thinking.Len() > 0 {
				thinking.WriteString("\n\n")
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
			case "thinking_delta":
				thinking.WriteString(ev.Delta.Thinking)
			}
		case "message_delta":
			// output_tokens here is cumulative for the message.
			if ev.Usage != nil {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			stopped = true
		case "error":
			return Response{}, &ProviderError{
				Adapter: KindAnthropic,
				Status:  anthropicStreamErrorStatus(ev.Error),
				Body:    data,
			}
		}
		if stopped {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, wrapErr("stream", err)
	}
	if !stopped {
		if cause := context.Cause(ctx); errors.Is(cause, ErrIdleTimeout) {
			return Response{}, wrapErr("stream", cause)
		}
		return Response{}, errors.New("anthropic: stream ended before message_stop")
	}
	if blocks == 0 {
		return Response{}, errors.New("anthropic: response had no content blocks")
	}

	out.Text = text.String()
	out.Thinking = thinking.String()
	out.InputTokens = usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	out.OutputTokens = usage.OutputTokens
	out.Effort = req.Effort
	return out, nil
}

// anthropicStreamErrorStatus maps an error event sent mid-stream to
// the HTTP status the same error carries when returned before the
// stream starts, so [AccountLimit] and retry logic treat both alike.
func anthropicStreamErrorStatus(e *struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}) int {
	if e == nil {
		return http.StatusInternalServerError
	}
	switch e.Type {
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "overloaded_error":
		return 529
	}
	return http.StatusInternalServerError
}

type anthropicModelsResponse struct {
	Data []struct {
		ID             string `json:"id"`
		DisplayName    string `json:"display_name"`
		MaxInputTokens int    `json:"max_input_tokens"`
		MaxTokens      int    `json:"max_tokens"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// ListModels implements [ModelLister] via GET {baseURL}/v1/models,
// following pagination. Context window and output cap come from the
// API's max_input_tokens and max_tokens fields.
func (a *Anthropic) ListModels(ctx context.Context) ([]ModelInfo, error) {
	apiKey, err := a.cred.Reveal(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, anthropicModelsTimeout)
	defer cancel()

	var models []ModelInfo
	afterID := ""
	for {
		q := url.Values{"limit": {"1000"}}
		if afterID != "" {
			q.Set("after_id", afterID)
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/v1/models?"+q.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("anthropic models request: %w", err)
		}
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
		resp, err := a.client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("anthropic models http: %w", err)
		}
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, &ProviderError{Adapter: KindAnthropic, Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
		}
		var page anthropicModelsResponse
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("anthropic models decode: %w", err)
		}
		for _, m := range page.Data {
			models = append(models, ModelInfo{
				ID:              m.ID,
				DisplayName:     m.DisplayName,
				ContextWindow:   m.MaxInputTokens,
				MaxOutputTokens: m.MaxTokens,
			})
		}
		if !page.HasMore || page.LastID == "" {
			return models, nil
		}
		afterID = page.LastID
	}
}
