package main

import (
	"encoding/json"
	"fmt"
	"strings"

	gnparser "github.com/gnames/gnparser"
)

func main() {
	cfg := gnparser.NewConfig(gnparser.OptWithDetails(true))
	p := gnparser.New(cfg)

	tests := []string{
		"Aus bus Smith, 1850",
		"Aus bus sensu lato Smith, 1850",
		"Aus bus sensu stricto Smith, 1850",
		"Aus bus s.l. Smith, 1850",
		"Aus bus s.s. Smith, 1850",
		"Aus bus s. l.",
		"Aus bus s. str.",
		"Aus bus sensu Smith, 1850",
		"Aus bus sensu auct.",
		"Aus bus auct. non Smith, 1850",
		"Aus bus (Smith, 1850) sensu lato",
		"Aus bus sensu lato",
		"Aus bus s.lat.",
		"Homo sapiens sensu stricto",
		"Quercus robur L. s. l.",
	}

	for _, name := range tests {
		fmt.Println(strings.Repeat("=", 78))
		fmt.Printf("INPUT: %q\n", name)
		parsed := p.ParseName(name)
		fmt.Printf("  parsed:      %v\n", parsed.Parsed)
		fmt.Printf("  quality:     %d\n", parsed.ParseQuality)
		if parsed.Canonical != nil {
			fmt.Printf("  canonical:   simple=%q  full=%q  stemmed=%q\n",
				parsed.Canonical.Simple, parsed.Canonical.Full, parsed.Canonical.Stemmed)
		}
		if parsed.Authorship != nil {
			fmt.Printf("  authorship:  verbatim=%q  normalized=%q\n",
				parsed.Authorship.Verbatim, parsed.Authorship.Normalized)
		}
		fmt.Printf("  cardinality: %d\n", parsed.Cardinality)
		if parsed.Tail != "" {
			fmt.Printf("  tail:        %q\n", parsed.Tail)
		}
		if len(parsed.QualityWarnings) > 0 {
			for _, w := range parsed.QualityWarnings {
				fmt.Printf("  warning:     [q=%d] %s\n", w.Quality, w.Warning)
			}
		}
		if parsed.Virus {
			fmt.Printf("  virus:       true\n")
		}
		if parsed.Hybrid != nil {
			fmt.Printf("  hybrid:      %v\n", *parsed.Hybrid)
		}
		if parsed.Surrogate != nil {
			fmt.Printf("  surrogate:   %v\n", *parsed.Surrogate)
		}
		if strings.Contains(name, "sensu lato Smith") ||
			strings.Contains(name, "auct. non") {
			if b, err := json.MarshalIndent(parsed, "", "  "); err == nil {
				fmt.Printf("  FULL JSON:\n%s\n", string(b))
			}
		}
	}
}
