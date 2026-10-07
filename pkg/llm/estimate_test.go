package llm

import (
	"strings"
	"testing"
)

func TestEstimateTokensRoughFourCharsPerToken(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},        // ceil(1/4) = 1
		{"abcd", 1},     // exactly 4 chars → 1 token
		{"abcde", 2},    // ceil(5/4) = 2
		{"abcdefgh", 2}, // exactly 8 chars → 2 tokens
	}
	for _, tc := range tests {
		if got := EstimateTokens(tc.in); got != tc.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	// A 100KB-ish input (order of magnitude for a 25-page JATS body)
	// should land in the 20K-30K token range.
	big := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2000)
	got := EstimateTokens(big)
	if got < 20_000 || got > 35_000 {
		t.Errorf("large text estimate = %d (expected 20k..35k for %d chars)", got, len(big))
	}
}
