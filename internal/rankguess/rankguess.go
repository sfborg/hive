// Package rankguess suggests a sfga rank ID for a name based on
// nom_code, the gnparser flattened parse result, and the canonical
// form. Used to pre-select the rank picker in the name-add form.
package rankguess

import (
	"strings"

	"github.com/gnames/gnparser/ent/parsed"
)

// Guess returns a suggested sfga rank ID for a name, given its
// nom_code (ZOOLOGICAL / BOTANICAL / BACTERIAL / VIRUS / CULTIVARS /
// PHYTOSOCIOLOGICAL), the gnparser flattened parse result, and a
// canonical form used for suffix matching.
//
// The guess is a *hint* meant to pre-select the rank picker in the
// name-add form — curators can override it before committing. The
// resolution order is:
//
//  1. Explicit rank marker from gnparser (e.g. "subsp.", "var.") →
//     mapped to the corresponding sfga rank. Highest confidence.
//  2. Family-group suffix rules, scoped by code (-idae, -inae, -oidea
//     in zoology; -aceae, -oideae, -eae in botany; -viridae, -virinae
//     in virology). High confidence when the code+suffix combo is a
//     well-established convention.
//  3. Cardinality fallback: 1 → GENUS, 2 → SPECIES, 3 → SUBSPECIES.
//     Low confidence — uninomials could be any suprageneric rank.
//
// Returns "" when nothing above matches (e.g. an unparsed name with
// cardinality 0). Callers should treat "" as "curator picks manually."
func Guess(codeID string, p parsed.ParsedFlat, canonical string) string {
	// 1. Explicit rank marker in the parsed name — most authoritative.
	if r := rankFromMarker(p.Rank); r != "" {
		return r
	}

	// 2. Suffix rules — code-scoped so "-idae" doesn't cross-suggest
	//    (family in zoology vs. subclass in some botanical traditions).
	last := lastWord(canonical)
	if last != "" {
		if r := rankFromSuffix(codeID, last); r != "" {
			return r
		}
	}

	// 3. Cardinality fallback — pure structural inference from how many
	//    words the parser saw.
	switch p.Cardinality {
	case 1:
		return "GENUS"
	case 2:
		return "SPECIES"
	case 3:
		// SUBSPECIES is the safe zoological default; botany often uses
		// VARIETY / FORM without a marker but there's no way to tell
		// from cardinality alone, so pick the most common.
		return "SUBSPECIES"
	}
	return ""
}

// rankFromMarker maps gnparser's short rank marker (as returned in
// ParsedFlat.Rank) to a sfga rank ID. gnparser normalizes marker
// spellings so "ssp." and "subsp." both come through as "subsp.".
func rankFromMarker(marker string) string {
	switch strings.ToLower(strings.TrimSpace(marker)) {
	case "sp.":
		return "SPECIES"
	case "subsp.", "ssp.":
		return "SUBSPECIES"
	case "var.":
		return "VARIETY"
	case "subvar.":
		return "SUBVARIETY"
	case "f.", "forma":
		return "FORM"
	case "subf.":
		return "SUBFORM"
	case "cv.":
		return "CULTIVAR"
	case "sect.":
		return "SECTION_BOTANY"
	case "subsect.":
		return "SUBSECTION_BOTANY"
	case "ser.":
		return "SERIES"
	case "subgen.":
		return "SUBGENUS"
	case "grex":
		return "GREX"
	case "morph":
		return "MORPH"
	case "prol.", "proles":
		return "PROLES"
	case "aberr.", "ab.":
		return "ABERRATION"
	}
	return ""
}

// rankFromSuffix applies well-known family-group suffix conventions
// under each nomenclatural code. Order within each block matters:
// longer suffixes are checked first so "-oideae" doesn't fall through
// to "-eae" or "-idae".
func rankFromSuffix(codeID, word string) string {
	w := strings.ToLower(word)
	switch codeID {
	case "ZOOLOGICAL":
		switch {
		case strings.HasSuffix(w, "oidea"):
			return "SUPERFAMILY"
		case strings.HasSuffix(w, "idae"):
			return "FAMILY"
		case strings.HasSuffix(w, "inae"):
			return "SUBFAMILY"
		case strings.HasSuffix(w, "ini"):
			return "TRIBE"
		case strings.HasSuffix(w, "ina"):
			return "SUBTRIBE"
		}
	case "BOTANICAL", "PHYTOSOCIOLOGICAL":
		switch {
		case strings.HasSuffix(w, "oideae"):
			return "SUBFAMILY"
		case strings.HasSuffix(w, "aceae"):
			return "FAMILY"
		case strings.HasSuffix(w, "phyta"):
			return "PHYLUM"
		case strings.HasSuffix(w, "opsida"):
			return "CLASS"
		case strings.HasSuffix(w, "ales"):
			return "ORDER"
		case strings.HasSuffix(w, "inae"):
			return "SUBTRIBE"
		case strings.HasSuffix(w, "eae"):
			return "TRIBE"
		}
	case "BACTERIAL":
		switch {
		case strings.HasSuffix(w, "aceae"):
			return "FAMILY"
		case strings.HasSuffix(w, "oideae"):
			return "SUBFAMILY"
		case strings.HasSuffix(w, "ales"):
			return "ORDER"
		case strings.HasSuffix(w, "eae"):
			return "TRIBE"
		}
	case "VIRUS":
		switch {
		case strings.HasSuffix(w, "virales"):
			return "ORDER"
		case strings.HasSuffix(w, "viridae"):
			return "FAMILY"
		case strings.HasSuffix(w, "virinae"):
			return "SUBFAMILY"
		case strings.HasSuffix(w, "virus"):
			return "GENUS"
		}
	}
	return ""
}

// lastWord returns the trailing space-delimited token from s — used by
// the suffix rules to grab the operative epithet from a canonical form
// ("Rosa alba" → "alba"). Multi-word canonicals only come from
// binomials / trinomials; suffix rules apply to the last word.
func lastWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		return s[i+1:]
	}
	return s
}
