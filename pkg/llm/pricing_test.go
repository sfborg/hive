package llm

import (
	"math"
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

func TestLookupPricingCoversNamedModels(t *testing.T) {
	// Spot-check that the models hive most cares about have
	// pricing entries — catches accidental table pruning.
	must := []string{
		"claude-opus-4-7",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
		"gpt-4o",
		"gpt-4o-mini",
	}
	for _, m := range must {
		if _, ok := LookupPricing(m); !ok {
			t.Errorf("LookupPricing(%q) missing from pricing table", m)
		}
	}

	// Unknown model must return known=false, not $0 silently.
	if _, ok := LookupPricing("not-a-real-model"); ok {
		t.Errorf("LookupPricing on unknown model should return ok=false")
	}
}

func TestEstimateCostArithmetic(t *testing.T) {
	// Sonnet 4.6: $3/M input, $15/M output.
	// 100K input + 4K output = 0.3 + 0.06 = $0.36
	cost, ok := EstimateCost("claude-sonnet-4-6", 100_000, 4_000)
	if !ok {
		t.Fatalf("pricing lookup failed")
	}
	want := 0.30 + 0.06
	if math.Abs(cost-want) > 0.0001 {
		t.Errorf("Sonnet cost = %.4f, want %.4f", cost, want)
	}

	// Opus 4.7: $15/M input, $75/M output.
	// Same call: 1.5 + 0.30 = $1.80 — should exceed the confirm
	// threshold and thus trigger the confirm-on-submit path.
	cost, _ = EstimateCost("claude-opus-4-7", 100_000, 4_000)
	if cost <= ConfirmThresholdUSD {
		t.Errorf("Opus 100K/4K should exceed $%.2f threshold, got $%.4f", ConfirmThresholdUSD, cost)
	}

	// Haiku on the same call is cheap enough that even a 700-page
	// paper (~28x input) stays comfortably under the threshold.
	// Sanity check: raw 100K/4K well under $1.
	cost, _ = EstimateCost("claude-haiku-4-5", 100_000, 4_000)
	if cost >= ConfirmThresholdUSD {
		t.Errorf("Haiku 100K/4K should be well under $%.2f, got $%.4f", ConfirmThresholdUSD, cost)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	cost, known := EstimateCost("nope-not-real", 1_000_000, 1_000_000)
	if known {
		t.Errorf("expected known=false for unknown model")
	}
	if cost != 0 {
		t.Errorf("unknown model cost should be 0 (with known=false), got %v", cost)
	}
}
