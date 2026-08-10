package hive

import "github.com/sfborg/sflib/pkg/coldp"

// sflib's coldp enum constructors silently map the empty string to a
// non-empty default: NewNomStatus("") → Established, NewRank("") →
// Unranked. That's convenient for user-typed input where "leave me
// alone" is ambiguous, but harmful for hive's read/write flow — a taxon
// with NULL col__status_id round-trips into "ESTABLISHED" and gets
// silently persisted, poisoning the row.
//
// These wrappers preserve the empty-in / empty-out invariant so hive's
// DB reads, HTTP patches, and edit-form writes never introduce a
// phantom default. Callers should route every string→enum conversion
// through them.

// ParseRank returns coldp.NewRank(s) when s is non-empty, and the
// zero-value Rank (ID="") otherwise. Use for every place hive
// materializes a Rank from a database cell, HTTP field, or picker id.
func ParseRank(s string) coldp.Rank {
	if s == "" {
		var zero coldp.Rank
		return zero
	}
	return coldp.NewRank(s)
}

// ParseNomStatus mirrors ParseRank for the nomenclatural-status enum.
// Empty stays empty; a name whose col__status_id is NULL stays NULL on
// round-trip.
func ParseNomStatus(s string) coldp.NomStatus {
	if s == "" {
		var zero coldp.NomStatus
		return zero
	}
	return coldp.NewNomStatus(s)
}

// NomenCodePrefix maps a sfga nom_code enum ID (ZOOLOGICAL, BOTANICAL,
// BACTERIAL, VIRUS, CULTIVARS, PHYTOSOCIOLOGICAL) to the code
// abbreviation NOMEN labels use (ICZN, ICN, ICNP, ICVCN, ICNCP, ICPN).
// The status picker filters NOMEN terms by this prefix so a name under
// zoological code only sees ICZN options. Unknown / empty input → "".
func NomenCodePrefix(sfgaCodeID string) string {
	switch sfgaCodeID {
	case "ZOOLOGICAL":
		return "ICZN"
	case "BOTANICAL":
		// Historically covers algae, fungi, and plants — all under ICN.
		return "ICN"
	case "BACTERIAL":
		return "ICNP"
	case "VIRUS":
		return "ICVCN"
	case "CULTIVARS":
		return "ICNCP"
	case "PHYTOSOCIOLOGICAL":
		return "ICPN"
	}
	return ""
}
