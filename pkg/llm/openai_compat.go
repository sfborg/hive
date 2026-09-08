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
	name         string
	baseURL      string
	defaultModel string
	cred         Credential
	client       *http.Client
}

// NewOpenAICompat builds an OpenAI-compatible provider. baseURL is
// the API root (e.g. "https://api.openai.com/v1",
// "http://localhost:11434/v1" for Ollama). Trailing slashes are
// trimmed so callers don't have to be precise.
func NewOpenAICompat(name, baseURL, defaultModel string, cred Credential) *OpenAICompat {
	return &OpenAICompat{
		name:         name,
		baseURL:      strings.TrimRight(baseURL, "/"),
		defaultModel: defaultModel,
		cred:         cred,
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

// Name implements Provider.
func (o *OpenAICompat) Name() string { return o.name }

// Kind implements Provider.
func (o *OpenAICompat) Kind() string { return KindOpenAICompat }

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

type openaiResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message openaiMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
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
		msgs = append(msgs, openaiMessage{Role: "system", Content: req.System})
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
		return Response{}, fmt.Errorf("openai_compat http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Response{}, fmt.Errorf("openai_compat decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("openai_compat: response had no choices")
	}

	return Response{
		Text:           out.Choices[0].Message.Content,
		ModelVersionID: out.Model,
		InputTokens:    out.Usage.PromptTokens,
		OutputTokens:   out.Usage.CompletionTokens,
	}, nil
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
			// Per-token USD strings (OpenRouter). Empty
			// on providers that don't expose pricing.
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
// normalized [ModelInfo] entries. Prices are converted from
// per-token (OpenRouter's format) to per-million (hive's
// convention). Providers without pricing fields (plain OpenAI,
// Ollama, most self-hosted) return entries with zero prices;
// callers fall back to hive's static pricing table for those.
//
// Silent-fail semantics on individual field parses: a model
// with a malformed pricing string still lands with the other
// fields intact — an incomplete list is better than a broken
// picker for the whole provider.
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
		mi := ModelInfo{
			ID:              m.ID,
			DisplayName:     m.Name,
			ContextWindow:   m.ContextLength,
			MaxOutputTokens: m.TopProvider.MaxCompletionTokens,
		}
		// Per-token USD strings → per-million USD floats.
		// Skip silently on parse failure so the model still
		// lands, just without pricing info.
		if m.Pricing.Prompt != "" {
			if v, perr := strconv.ParseFloat(m.Pricing.Prompt, 64); perr == nil {
				mi.InputPricePerMillion = v * 1_000_000
			}
		}
		if m.Pricing.Completion != "" {
			if v, perr := strconv.ParseFloat(m.Pricing.Completion, 64); perr == nil {
				mi.OutputPricePerMillion = v * 1_000_000
			}
		}
		models = append(models, mi)
	}
	return models, nil
}
