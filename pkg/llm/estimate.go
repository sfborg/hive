package llm

// EstimateTokens approximates the token count of s using the
// ~4-chars-per-token rule of thumb, which matches BPE tokenizers
// on English / Latin text within roughly ±20%. Not exact — token
// counts differ per model and per language (CJK text tokenizes
// closer to 1-2 chars/token) — but good enough for the pre-submit
// size check against a model's context window. Where
// exact billing matters, use the provider's reported usage from
// the response.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	// Round up so a small prompt never reports zero.
	return (len(s) + 3) / 4
}
