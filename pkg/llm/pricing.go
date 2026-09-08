package llm

// Pricing describes the USD cost of a specific model per million
// tokens. Kept as a per-model record rather than per-provider so
// the same OpenAI-compat adapter can point at different endpoints
// (OpenAI proper vs. an OpenAI-compat gateway) without changing
// how cost is computed for a given model identifier.
type Pricing struct {
	// InputPerMillion is USD per 1,000,000 input tokens (prompt).
	InputPerMillion float64

	// OutputPerMillion is USD per 1,000,000 output tokens
	// (completion). Typically 3-5x InputPerMillion.
	OutputPerMillion float64

	// CacheReadPerMillion is the discounted rate charged for
	// cache-hit input tokens on providers that support prompt
	// caching (Anthropic ephemeral cache). Zero when the provider
	// doesn't offer prompt caching — callers should treat zero as
	// "no cache pricing" and fall back to InputPerMillion.
	CacheReadPerMillion float64
}

// pricingTable is the authoritative per-model USD pricing hive
// uses for cost estimates. Keys are the model identifiers that
// callers pass to Request.Model (or that live in
// ProviderConfig.DefaultModel).
//
// Prices DO change (usually downward). Update this table when a
// provider announces new pricing. Sources:
//   - Anthropic: https://www.anthropic.com/pricing
//   - OpenAI:    https://openai.com/api/pricing
//   - Google:    https://ai.google.dev/pricing
//
// Models not in this table return known=false from LookupPricing;
// callers should surface "cost unknown for this model" in the UI
// rather than assume $0 (which would silently mislead a curator
// running an Opus-tier model at flagship prices).
var pricingTable = map[string]Pricing{
	// Anthropic Claude 4.x (native /v1/messages adapter)
	"claude-opus-4-7":   {InputPerMillion: 15.0, OutputPerMillion: 75.0, CacheReadPerMillion: 1.50},
	"claude-opus-4-6":   {InputPerMillion: 15.0, OutputPerMillion: 75.0, CacheReadPerMillion: 1.50},
	"claude-sonnet-4-6": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.30},
	"claude-sonnet-4-5": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.30},
	"claude-haiku-4-5":  {InputPerMillion: 1.0, OutputPerMillion: 5.0, CacheReadPerMillion: 0.10},

	// Anthropic Claude 3.x (older; still available on the API)
	"claude-3-5-sonnet-20241022": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.30},
	"claude-3-5-haiku-20241022":  {InputPerMillion: 0.80, OutputPerMillion: 4.0, CacheReadPerMillion: 0.08},

	// OpenAI GPT-4x (openai_compat adapter → api.openai.com)
	"gpt-4o":      {InputPerMillion: 2.50, OutputPerMillion: 10.0},
	"gpt-4o-mini": {InputPerMillion: 0.15, OutputPerMillion: 0.60},
	"gpt-4-turbo": {InputPerMillion: 10.0, OutputPerMillion: 30.0},
	"o1":          {InputPerMillion: 15.0, OutputPerMillion: 60.0},
	"o3-mini":     {InputPerMillion: 1.10, OutputPerMillion: 4.40},

	// Google Gemini 2.x (openai_compat adapter → Gemini OpenAI-compat endpoint)
	"gemini-2.5-pro":        {InputPerMillion: 1.25, OutputPerMillion: 5.0},
	"gemini-2.5-flash":      {InputPerMillion: 0.30, OutputPerMillion: 2.50},
	"gemini-2.5-flash-lite": {InputPerMillion: 0.10, OutputPerMillion: 0.40},
}

// LookupPricing returns the pricing entry for the given model.
// Second return is false when the model is not in the table.
func LookupPricing(model string) (Pricing, bool) {
	p, ok := pricingTable[model]
	return p, ok
}

// EstimateTokens approximates the token count of s using the
// ~4-chars-per-token rule of thumb, which matches BPE tokenizers
// on English / Latin text within roughly ±20%. Not exact — token
// counts differ per model and per language (CJK text tokenizes
// closer to 1-2 chars/token) — but good enough for the "roughly
// what will this cost?" display next to a submit button. Where
// exact billing matters, use the provider's reported usage from
// the response.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	// Round up so a small prompt never reports zero.
	return (len(s) + 3) / 4
}

// EstimateCost returns the estimated USD cost of an invocation.
// inputTokens covers system + user content combined; outputTokens
// is typically the requested MaxTokens (worst-case cost, since the
// model may generate fewer). Returns known=false when no pricing
// entry exists for model — callers should surface that rather
// than showing $0.00.
func EstimateCost(model string, inputTokens, outputTokens int) (usd float64, known bool) {
	p, ok := LookupPricing(model)
	if !ok {
		return 0, false
	}
	in := float64(inputTokens) / 1_000_000 * p.InputPerMillion
	out := float64(outputTokens) / 1_000_000 * p.OutputPerMillion
	return in + out, true
}

// ConfirmThresholdUSD is the recommended cost above which a
// curator-facing invocation UI should require explicit
// confirmation before submitting. Exposed as a constant (not a
// per-user tunable) so every WUI/CLI/TUI surface applies the same
// threshold; raise it here to lift the bar globally.
const ConfirmThresholdUSD = 1.00
