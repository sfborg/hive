package core

import (
	"testing"

	"github.com/gnames/gnparser"
)

// TestRankGuess covers the three tiers of the guess strategy (marker,
// suffix, cardinality) across all four codes hive supports natively.
// Each case is a real-world name a curator might type; the expected
// guess is what the picker should pre-select.
func TestRankGuess(t *testing.T) {
	p := gnparser.New(gnparser.NewConfig(gnparser.OptWithDetails(true)))
	cases := []struct {
		code, name, want string
	}{
		// Explicit marker — highest confidence.
		{"ZOOLOGICAL", "Panthera onca subsp. goldmani", "SUBSPECIES"},
		{"BOTANICAL", "Rosa gallica var. officinalis", "VARIETY"},
		{"BOTANICAL", "Rosa gallica f. alba", "FORM"},

		// Zoology suffixes.
		{"ZOOLOGICAL", "Felidae", "FAMILY"},
		{"ZOOLOGICAL", "Felinae", "SUBFAMILY"},
		{"ZOOLOGICAL", "Feloidea", "SUPERFAMILY"},
		{"ZOOLOGICAL", "Pantherini", "TRIBE"},

		// Botany suffixes.
		{"BOTANICAL", "Rosaceae", "FAMILY"},
		{"BOTANICAL", "Rosoideae", "SUBFAMILY"},
		{"BOTANICAL", "Roseae", "TRIBE"},
		{"BOTANICAL", "Rosales", "ORDER"},

		// Virology suffixes.
		{"VIRUS", "Coronaviridae", "FAMILY"},
		{"VIRUS", "Orthocoronavirinae", "SUBFAMILY"},
		{"VIRUS", "Nidovirales", "ORDER"},

		// Cardinality fallback.
		{"ZOOLOGICAL", "Panthera", "GENUS"},         // sp==sp, no suffix → cardinality 1
		{"ZOOLOGICAL", "Panthera onca", "SPECIES"},  // gnparser marks rank="sp."
		{"ZOOLOGICAL", "Panthera onca goldmani", "SUBSPECIES"}, // no marker, cardinality 3

		// Code disambiguation: -aceae is a botanical family, not zoological.
		// A zoologist typing "Rosaceae" wouldn't (they'd be in ICN mode),
		// but if the code is set to ZOOLOGICAL the guess falls through to
		// cardinality — 1 → GENUS. Not "wrong" — just conservative.
		{"ZOOLOGICAL", "Rosaceae", "GENUS"},
	}
	for _, c := range cases {
		flat := p.ParseName(c.name).Flatten()
		got := RankGuess(c.code, flat, flat.CanonicalSimple)
		if got != c.want {
			t.Errorf("RankGuess(%q, %q) = %q, want %q",
				c.code, c.name, got, c.want)
		}
	}
}

// TestRankGuessEmpty covers the "no useful signal" paths — empty parse
// result and unknown code both return "" so the picker stays unset.
func TestRankGuessEmpty(t *testing.T) {
	p := gnparser.New(gnparser.NewConfig(gnparser.OptWithDetails(true)))

	// Empty name → cardinality 0 → no guess.
	flat := p.ParseName("").Flatten()
	if got := RankGuess("ZOOLOGICAL", flat, ""); got != "" {
		t.Errorf("empty name: got %q, want empty", got)
	}

	// Unknown code — suffix rules skipped, cardinality still applies.
	flat = p.ParseName("Panthera").Flatten()
	if got := RankGuess("UNKNOWN_CODE", flat, "Panthera"); got != "GENUS" {
		t.Errorf("unknown code + uninomial: got %q, want GENUS", got)
	}
}
