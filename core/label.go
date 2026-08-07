package core

import "html"

// Label carries both plain-text and HTML-styled forms of a display string.
// HTML is server-rendered with ICZN-appropriate italics + extinct dagger,
// so front-ends render consistently without duplicating the styling logic.
// Plain text has the dagger but no styling — the TUI uses this directly,
// or can re-apply italics via lipgloss using the same italicForRank rules.
type Label struct {
	Text string `json:"text,omitempty"`
	HTML string `json:"html,omitempty"`
}

// Ref is a lightweight pointer to another entity — id + rendered label.
// Used wherever a response would otherwise ship a raw UUID (parent taxa,
// name references, according-to links). Front-ends render the label
// directly; the id is present for follow-up navigation.
type Ref struct {
	ID    string `json:"id"`
	Label Label  `json:"label,omitzero"`
}

// BuildLabel produces the (text, html) pair for a name row. Called by
// TaxonRef, NameRef, and the synonym hit builder to ensure identical
// formatting across every endpoint.
//
// Formatting:
//   * Extinct taxa get a "† " prefix (both forms).
//   * The canonical part is wrapped in <i>…</i> when italicForRank(rank)
//     is true — genus group and below, plus infraspecific ranks.
//     Everything above the family group (family, order, class, …) stays
//     roman per ICZN convention.
//   * Authorship is never italicized.
//   * HTML output escapes canonical and authorship so weird data
//     (unlikely, but possible in legacy archives) can't inject markup.
//
// The rank ID is the raw sfga enum value ("SPECIES", "GENUS", "FAMILY",
// …). Unknown ranks default to non-italic — safe fallback that keeps
// the label rendering even for values italicForRank doesn't recognize.
func BuildLabel(canonical, authorship, rankID string, extinct bool) Label {
	daggerText := ""
	if extinct {
		daggerText = "† "
	}
	text := daggerText + canonical
	if authorship != "" {
		text = text + " " + authorship
	}

	escCanon := html.EscapeString(canonical)
	escAuthor := html.EscapeString(authorship)
	var htmlOut string
	if italicForRank(rankID) && canonical != "" {
		htmlOut = daggerText + "<i>" + escCanon + "</i>"
	} else {
		htmlOut = daggerText + escCanon
	}
	if authorship != "" {
		htmlOut = htmlOut + " " + escAuthor
	}
	return Label{Text: text, HTML: htmlOut}
}

// ItalicForRank is the exported alias that front-ends use to apply the
// same italicization rule as BuildLabel does for its HTML output. The
// TUI calls it to wrap canonical name spans in lipgloss italic before
// rendering, giving the terminal the same visual hierarchy as the
// WUI's HTML.
func ItalicForRank(rankID string) bool { return italicForRank(rankID) }

// italicForRank reports whether names at the given sfga rank should be
// italicized under standard nomenclature conventions.
//
// Rules (ICZN/ICN/ICNP consensus):
//   * Genus group (including subgenus, infragenus, sections, series): italic
//   * Species group and infraspecific ranks (variety, form, etc.): italic
//   * Family group and above (family, order, class, phylum, kingdom): roman
//   * Cultivar and cultivar-group: not italicized (ICNCP uses single
//     quotes instead — deferred; we render as roman for now)
//   * Rank markers ("subsp.", "var.") in trinomials: roman — handled
//     by gnparser's canonical form, which omits the marker
//
// The list is hand-curated against the sfga rank table. Missing ranks
// default to roman (safer than accidentally italicizing a family).
// A future refactor could load this from the rank table's boolean
// flags (col__genus_group, col__infraspecific, col__supraspecific).
func italicForRank(rankID string) bool {
	switch rankID {
	// Genus group
	case "SUPERGENUS", "GENUS", "SUBGENUS", "INFRAGENUS", "INFRAGENERIC_NAME":
		return true
	// Botanical rank between subgenus and species
	case "SUPERSECTION_BOTANY", "SECTION_BOTANY", "SUBSECTION_BOTANY",
		"SUPERSERIES", "SERIES", "SUBSERIES":
		return true
	// Species group
	case "SPECIES_AGGREGATE", "SPECIES":
		return true
	// Infraspecific ranks
	case "SUBSPECIES", "INFRASPECIFIC_NAME", "INFRASUBSPECIFIC_NAME",
		"SUPERVARIETY", "VARIETY", "SUBVARIETY",
		"SUPERFORM", "FORM", "SUBFORM":
		return true
	// Other infra-/intra-specific variants
	case "NATIO", "ABERRATION", "LUSUS", "PROLES", "MORPH",
		"KLEPTON", "GREX", "MUTATIO", "CONVARIETY":
		return true
	// Micro/varieties commonly used in bacteriology
	case "STRAIN", "PATHOVAR", "BIOVAR", "CHEMOVAR", "MORPHOVAR",
		"PHAGOVAR", "SEROVAR", "CHEMOFORM", "FORMA_SPECIALIS":
		return true
	}
	return false
}
