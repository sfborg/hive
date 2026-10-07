package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAICompat is the adapter for any provider that speaks the
// OpenAI /v1/chat/completions wire shape — OpenAI itself, Gemini
// through its OpenAI-compatible endpoint, Ollama, LiteLLM proxies,
// vLLM, LM Studio, and many others. The wire protocol is stable
// enough that a single adapter covers them all; provider quirks
// (which models exist, what headers they want) live in config,
// not code.
type OpenAICompat struct {
	name                string
	baseURL             string
	defaultModel        string
	cred                Credential
	client              *http.Client
	disableCacheControl bool
}

// OpenAICompatOptions carries adapter-tuning knobs that don't fit
// the four required NewOpenAICompat args. Zero value is fine — all
// fields default to the recommended behavior.
type OpenAICompatOptions struct {
	// DisableCacheControl turns off the cache_control:ephemeral
	// marker on the system prompt. Default (false) sends the
	// marker so OpenRouter routing to Anthropic keeps the
	// cache-read discount; other providers ignore the field. Set
	// to true only for strict OpenAI-compat endpoints that reject
	// unknown fields.
	DisableCacheControl bool
}

// NewOpenAICompat builds an OpenAI-compatible provider with default
// options. Kept for backward compatibility with call sites that
// don't need cache-control or other tuning.
func NewOpenAICompat(name, baseURL, defaultModel string, cred Credential) *OpenAICompat {
	return NewOpenAICompatWithOptions(name, baseURL, defaultModel, cred, OpenAICompatOptions{})
}

// NewOpenAICompatWithOptions is the options-taking constructor.
// baseURL is the API root (e.g. "https://api.openai.com/v1",
// "http://localhost:11434/v1" for Ollama). Trailing slashes are
// trimmed so callers don't have to be precise.
func NewOpenAICompatWithOptions(name, baseURL, defaultModel string, cred Credential, opts OpenAICompatOptions) *OpenAICompat {
	return &OpenAICompat{
		name:                name,
		baseURL:             strings.TrimRight(baseURL, "/"),
		defaultModel:        defaultModel,
		cred:                cred,
		disableCacheControl: opts.DisableCacheControl,
		// 20-minute HTTP timeout — see the matching comment in
		// anthropic.go for the reasoning. Larger models on
		// whole-paper NER regularly exceed 5 minutes even when the
		// call ultimately succeeds.
		client: &http.Client{Timeout: 20 * time.Minute},
	}
}

// Name implements Provider.
func (o *OpenAICompat) Name() string { return o.name }

// Kind implements Provider.
func (o *OpenAICompat) Kind() string { return KindOpenAICompat }

// openaiMessage carries a chat-completions message. Content is
// `any` because two shapes are valid on the wire:
//   - a plain string (the OpenAI default and the shape used when
//     cache_control is not requested)
//   - an array of content blocks (used when cache_control is
//     requested so the block-level "cache_control":"ephemeral"
//     marker attaches to the system prompt for OpenRouter's
//     Anthropic passthrough)
// The response-parsing path also has to accept both shapes for
// robustness — some gateways always echo content as blocks.
type openaiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// openaiContentBlock is one entry in the content-blocks array
// shape. type="text" is the only value this adapter emits;
// cache_control is Anthropic-compatible and understood by gateways
// like OpenRouter that pass it through to the underlying model.
type openaiContentBlock struct {
	Type         string                     `json:"type"`
	Text         string                     `json:"text,omitempty"`
	CacheControl *openaiBlockCacheControl   `json:"cache_control,omitempty"`
}

type openaiBlockCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type openaiRespFormat struct {
	Type string `json:"type"`
}

type openaiRequest struct {
	Model          string            `json:"model"`
	Messages       []openaiMessage   `json:"messages"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	Temperature    float64           `json:"temperature,omitempty"`
	ResponseFormat *openaiRespFormat `json:"response_format,omitempty"`
}

// openaiResponse maps the standard chat-completions response
// shape. Usage carries the OpenAI-style token counts plus optional
// sub-fields for prompt-caching detail (`cached_tokens` on OpenAI/
// DeepSeek/Cerebras, `cache_creation_input_tokens` and
// `cache_read_input_tokens` on Anthropic-through-OpenRouter). Both
// are optional so a gateway that reports neither still decodes
// cleanly.
type openaiResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// Complete implements Provider. Wire format: POST {baseURL}/chat/completions
// with the standard OpenAI chat-completions body. System is sent
// as a "system" role message when non-empty; User as a "user"
// role message.
func (o *OpenAICompat) Complete(ctx context.Context, req Request) (Response, error) {
	model := req.Model
	if model == "" {
		model = o.defaultModel
	}
	if model == "" {
		return Response{}, errors.New("openai_compat: no model set (Request.Model empty and no DefaultModel configured)")
	}

	msgs := make([]openaiMessage, 0, 2)
	if req.System != "" {
		if o.disableCacheControl {
			msgs = append(msgs, openaiMessage{Role: "system", Content: req.System})
		} else {
			// Content-blocks shape with a cache_control marker on
			// the system prompt. Default on so gateways that
			// support Anthropic-style prompt caching (OpenRouter
			// routing to Anthropic) get the cache-read discount
			// transparently. Providers that don't understand the
			// field ignore it; strict endpoints that reject
			// unknown fields opt out via DisableCacheControl.
			msgs = append(msgs, openaiMessage{
				Role: "system",
				Content: []openaiContentBlock{{
					Type:         "text",
					Text:         req.System,
					CacheControl: &openaiBlockCacheControl{Type: "ephemeral"},
				}},
			})
		}
	}
	msgs = append(msgs, openaiMessage{Role: "user", Content: req.User})

	body := openaiRequest{
		Model:       model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	if strings.EqualFold(req.ResponseFormat, "json") {
		body.ResponseFormat = &openaiRespFormat{Type: "json_object"}
	}

	apiKey, err := o.cred.Reveal(ctx)
	if err != nil {
		return Response{}, err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("openai_compat marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("openai_compat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai_compat http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return Response{}, &ProviderError{Adapter: KindOpenAICompat, Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}

	var out openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Response{}, fmt.Errorf("openai_compat decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("openai_compat: response had no choices")
	}

	text, err := decodeOpenAIContent(out.Choices[0].Message.Content)
	if err != nil {
		return Response{}, fmt.Errorf("openai_compat decode content: %w", err)
	}
	return Response{
		Text:           text,
		ModelVersionID: out.Model,
		InputTokens:    out.Usage.PromptTokens,
		OutputTokens:   out.Usage.CompletionTokens,
	}, nil
}

// decodeOpenAIContent accepts either the string content shape or
// the content-blocks array shape that some gateways echo back
// (especially when cache_control was used on the request). For
// the array shape, only text-typed blocks contribute to the
// combined string — images and other block types are ignored.
func decodeOpenAIContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// Try string first (the common case).
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}
	// Fall back to array of blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("content is neither string nor content-blocks array: %w", err)
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String(), nil
}

// openaiModelsResponse covers both the plain OpenAI /v1/models
// shape (each item has {id, ...}) and the OpenRouter shape
// (each item has {id, name, pricing:{prompt,completion},
// context_length, top_provider:{max_completion_tokens}}). Fields
// missing on plain OpenAI are left zero; fields missing on
// OpenRouter don't collide because the JSON tags map cleanly.
type openaiModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name,omitempty"`
		ContextLength int    `json:"context_length,omitempty"`
		Pricing       struct {
			// USD per token as decimal strings (OpenRouter).
			// Empty on providers that don't list prices.
			Prompt     string `json:"prompt,omitempty"`
			Completion string `json:"completion,omitempty"`
		} `json:"pricing,omitempty"`
		TopProvider struct {
			MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
		} `json:"top_provider,omitempty"`
	} `json:"data"`
}

// ListModels calls the provider's models-listing endpoint
// (GET {baseURL}/models per the OpenAI convention) and returns
// normalized [ModelInfo] entries. Context window and output cap are
// filled when the provider reports them (OpenRouter does; plain
// OpenAI and Ollama return IDs only). Listed per-token prices are
// passed through as [ListedPrice] when present.
func (o *OpenAICompat) ListModels(ctx context.Context) ([]ModelInfo, error) {
	apiKey, err := o.cred.Reveal(ctx)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openai_compat models request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai_compat models http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai_compat models http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out openaiModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai_compat models decode: %w", err)
	}
	models := make([]ModelInfo, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, ModelInfo{
			ID:              m.ID,
			DisplayName:     m.Name,
			ContextWindow:   m.ContextLength,
			MaxOutputTokens: m.TopProvider.MaxCompletionTokens,
			ListedPrice:     listedPrice(m.Pricing.Prompt, m.Pricing.Completion),
		})
	}
	return models, nil
}

// listedPrice returns the provider's listed rates when both are
// non-negative decimal numbers. OpenRouter lists "-1" for routers
// whose price depends on the model chosen at request time; those,
// and empty or malformed values, yield nil so the UI falls back to
// "see provider".
func listedPrice(input, output string) *ListedPrice {
	for _, v := range []string{input, output} {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return nil
		}
	}
	return &ListedPrice{InputPerToken: input, OutputPerToken: output}
}
