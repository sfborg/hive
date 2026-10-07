package llm

// Limits describes the per-model size envelope hive checks against
// before invoking. A provider's own models listing takes precedence
// when available; this table is the fallback for providers without
// one.
type Limits struct {
	// ContextWindow is the total token budget (input + output) the
	// model accepts in a single call. Exceeding it returns HTTP 400
	// from the provider — no silent truncation.
	ContextWindow int

	// MaxOutputTokens is the hard per-request output cap the
	// provider enforces regardless of the max_tokens value in the
	// request body. Callers should treat this as an upper bound on
	// Request.MaxTokens.
	MaxOutputTokens int
}

// limitsTable is the fallback per-model size envelope, used when the
// provider's models listing is unavailable or omits the model. Bump
// when a provider announces changes. Models absent from the table return
// known=false from [LookupLimits]; callers should surface "size
// limits unknown for this model" and (defensively) skip the size
// check rather than assume infinite context.
var limitsTable = map[string]Limits{
	// Anthropic Claude Sonnet 5.5 — 1M context, 128K output per
	// Anthropic's model documentation. Reasoning tokens count
	// against max_tokens.
	"claude-sonnet-5-5": {ContextWindow: 1_000_000, MaxOutputTokens: 128_000},

	// Anthropic Claude 5 — 200K context standard tier. Output caps
	// mirror the 4.x family (Sonnet 64K, Opus 32K) pending
	// authoritative verification against Anthropic's per-model
	// documentation; update if the 5-series changes them.
	"claude-opus-5":   {ContextWindow: 200_000, MaxOutputTokens: 32_000},
	"claude-sonnet-5": {ContextWindow: 200_000, MaxOutputTokens: 64_000},
	"claude-fable-5-1": {ContextWindow: 200_000, MaxOutputTokens: 32_000},

	// Anthropic Claude 4.x — 200K context standard tier
	"claude-opus-4-7":   {ContextWindow: 200_000, MaxOutputTokens: 32_000},
	"claude-opus-4-6":   {ContextWindow: 200_000, MaxOutputTokens: 32_000},
	"claude-sonnet-4-6": {ContextWindow: 200_000, MaxOutputTokens: 64_000},
	"claude-sonnet-4-5": {ContextWindow: 200_000, MaxOutputTokens: 64_000},
	"claude-haiku-4-5":  {ContextWindow: 200_000, MaxOutputTokens: 8_192},
	// Dated variant returned by the API for `claude-haiku-4-5`.
	"claude-haiku-4-5-20251001": {ContextWindow: 200_000, MaxOutputTokens: 8_192},

	// Anthropic Claude 3.x
	"claude-3-5-sonnet-20241022": {ContextWindow: 200_000, MaxOutputTokens: 8_192},
	"claude-3-5-haiku-20241022":  {ContextWindow: 200_000, MaxOutputTokens: 8_192},

	// OpenAI GPT-4x
	"gpt-4o":      {ContextWindow: 128_000, MaxOutputTokens: 16_384},
	"gpt-4o-mini": {ContextWindow: 128_000, MaxOutputTokens: 16_384},
	"gpt-4-turbo": {ContextWindow: 128_000, MaxOutputTokens: 4_096},
	"o1":          {ContextWindow: 200_000, MaxOutputTokens: 100_000},
	"o3-mini":     {ContextWindow: 200_000, MaxOutputTokens: 100_000},

	// Google Gemini 2.x — significantly larger context than the
	// others, which makes it the practical choice for monograph-
	// scale papers until hive grows a chunking pipeline. Output caps
	// updated to Google's actual 65_536 per Gemini 2.5 docs; the
	// prior 8_192 was a stale conservative default.
	"gemini-2.5-pro":        {ContextWindow: 2_000_000, MaxOutputTokens: 65_536},
	"gemini-2.5-flash":      {ContextWindow: 1_000_000, MaxOutputTokens: 65_536},
	"gemini-2.5-flash-lite": {ContextWindow: 1_000_000, MaxOutputTokens: 65_536},

	// OpenRouter model IDs — the same underlying models exposed
	// under provider-prefixed slugs. Duplicating rather than
	// aliasing so hive-side lookups match whatever the caller
	// actually passed as --model, and so a future provider-specific
	// tweak (an OpenRouter-only override) has somewhere to land.
	"google/gemini-2.5-pro":        {ContextWindow: 2_000_000, MaxOutputTokens: 65_536},
	"google/gemini-2.5-flash":      {ContextWindow: 1_000_000, MaxOutputTokens: 65_536},
	"google/gemini-2.5-flash-lite": {ContextWindow: 1_000_000, MaxOutputTokens: 65_536},
	"google/gemini-flash-latest":   {ContextWindow: 1_000_000, MaxOutputTokens: 65_536},
	"google/gemini-pro-latest":     {ContextWindow: 2_000_000, MaxOutputTokens: 65_536},

	"anthropic/claude-opus-5":    {ContextWindow: 200_000, MaxOutputTokens: 32_000},
	"anthropic/claude-sonnet-5":  {ContextWindow: 200_000, MaxOutputTokens: 64_000},
	"anthropic/claude-haiku-4-5": {ContextWindow: 200_000, MaxOutputTokens: 8_192},

	"openai/gpt-4o":      {ContextWindow: 128_000, MaxOutputTokens: 16_384},
	"openai/gpt-4o-mini": {ContextWindow: 128_000, MaxOutputTokens: 16_384},
	"openai/o1":          {ContextWindow: 200_000, MaxOutputTokens: 100_000},
	"openai/o3-mini":     {ContextWindow: 200_000, MaxOutputTokens: 100_000},

	// DeepSeek V3 (deepseek-chat) — 8K output cap without beta
	// output extension. If the beta long-output header is
	// wired later, bump this.
	"deepseek/deepseek-chat":     {ContextWindow: 128_000, MaxOutputTokens: 8_192},
	"deepseek/deepseek-reasoner": {ContextWindow: 128_000, MaxOutputTokens: 32_768},
}

// LookupLimits returns the [Limits] entry for model. Second return
// is false when the model is not in the table — callers should
// treat that as "unknown; can't enforce size checks."
func LookupLimits(model string) (Limits, bool) {
	l, ok := limitsTable[model]
	return l, ok
}

// WouldExceedContext reports whether an invocation with the given
// input token count and requested output cap would exceed the
// model's context window. Returns false when the model is unknown
// (defensive — don't block on missing info; the WUI should surface
// "limits unknown" separately). ok=false lets callers distinguish
// "fits" from "can't tell."
func WouldExceedContext(model string, inputTokens, maxOutputTokens int) (exceeds bool, ok bool) {
	l, known := LookupLimits(model)
	if !known {
		return false, false
	}
	return inputTokens+maxOutputTokens > l.ContextWindow, true
}
