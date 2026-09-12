package llm

// Limits describes the per-model size envelope hive checks against
// before invoking. Kept separate from [Pricing] because context
// windows and prices change at different cadences and for
// different reasons — separating them keeps each table small and
// makes the intent of a per-model edit obvious.
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

// limitsTable is the authoritative per-model size envelope. Same
// maintenance discipline as [pricingTable] — bump when a provider
// announces changes. Models absent from the table return
// known=false from [LookupLimits]; callers should surface "size
// limits unknown for this model" and (defensively) skip the size
// check rather than assume infinite context.
var limitsTable = map[string]Limits{
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
	// scale papers until hive grows a chunking pipeline.
	"gemini-2.5-pro":        {ContextWindow: 2_000_000, MaxOutputTokens: 8_192},
	"gemini-2.5-flash":      {ContextWindow: 1_000_000, MaxOutputTokens: 8_192},
	"gemini-2.5-flash-lite": {ContextWindow: 1_000_000, MaxOutputTokens: 8_192},
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
