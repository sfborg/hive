// Rank-hierarchy filtering: given a parent taxon's code + rank,
// return the set of ranks valid as a child. Ported from TaxonWorks'
// NomenclaturalRank class hierarchy (MIT, same research group).
//
// The four codes model rank position independently — a section under
// a genus makes sense in ICN but not ICZN; a variety is a valid child
// of a species in ICN but not in ICZN. TaxonWorks captures this via
// per-code Ruby class hierarchies with `valid_parents` methods. Hive
// captures the same data as a JSON table (rank_hierarchy.json,
// extracted from the TW source) and evaluates the same semantics in
// Go.
//
// Data shape per code:
//   * ranks — every rank valid under that code, with:
//     - group: "higher" | "family" | "genus" | "species"
//     - parent_rank_id: default parent in the ordered chain (for
//       display ordering — not enforced as the only valid parent)
//     - valid_parents_override: when non-empty, the exact set of
//       valid parent rank IDs (tighter than the group default)
//
// Group-level defaults (matching TW's *_group.rb):
//   * higher:  parents in {higher}
//   * family:  parents in {higher, family}
//   * genus:   parents in {family, genus}
//   * species: parents in {genus, species}
//
// The set of valid children of a given parent is derived by inverting:
// for each rank R under the code, look up its valid_parents (override
// if present, else group default), and include R iff the parent's
// rank ID is in that set.

package ui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed rank_hierarchy.json
var rankHierarchyJSON []byte

// rankRow mirrors one entry in rank_hierarchy.json.
type rankRow struct {
	RankID               string   `json:"rank_id"`
	Group                string   `json:"group"`
	ParentRankID         string   `json:"parent_rank_id"`
	ValidParentsOverride []string `json:"valid_parents_override"`
	TypicalUse           bool     `json:"typical_use"`
}

// rankCodeData is the per-code decoded slice plus lookup structures
// built once at init.
type rankCodeData struct {
	Code             string             // TW code slug: "iczn" / "icn" / "icnp" / "icvcn"
	Ranks            []rankRow          // in file order (TW's own emission)
	byID             map[string]rankRow // rank_id → row for O(1) lookup
	idsByGroup       map[string][]string
}

var rankHierarchy map[string]*rankCodeData

func init() {
	var raw map[string]struct {
		Ranks []rankRow `json:"ranks"`
	}
	if err := json.Unmarshal(rankHierarchyJSON, &raw); err != nil {
		panic(fmt.Sprintf("core/ui: rank_hierarchy.json: %v", err))
	}
	rankHierarchy = make(map[string]*rankCodeData, len(raw))
	for code, payload := range raw {
		data := &rankCodeData{
			Code:       code,
			Ranks:      payload.Ranks,
			byID:       make(map[string]rankRow, len(payload.Ranks)),
			idsByGroup: make(map[string][]string, 4),
		}
		for _, r := range payload.Ranks {
			data.byID[r.RankID] = r
			data.idsByGroup[r.Group] = append(data.idsByGroup[r.Group], r.RankID)
		}
		rankHierarchy[code] = data
	}
}

// sfgaToTWCode maps hive/sfga's nom_code ID to TaxonWorks' code slug.
// Codes we don't model (CULTIVARS, PHYTOSOCIOLOGICAL) get an empty
// return; callers treat that as "no rank filter available."
var sfgaToTWCode = map[string]string{
	"ZOOLOGICAL": "iczn",
	"BOTANICAL":  "icn",
	"BACTERIAL":  "icnp",
	"VIRUS":      "icvcn",
}

// sfgaRankAliases maps sfga's disambiguated rank IDs to the TW rank
// they correspond to inside a specific code. Sfga carries SECTION_*
// and SUBSECTION_* / SUPERSECTION_* variants because SECTION is used
// differently across codes; TW captures the semantic under each
// code's own SECTION / SUBSECTION rank without the suffix. Empty
// return means the sfga rank has no TW equivalent under this code
// (e.g., SECTION_BOTANY under ICZN — sections don't exist in
// zoological nomenclature).
func canonicalizeSfgaRankID(codeTW, sfgaRankID string) string {
	// Fast path — most rank IDs match TW's spelling verbatim.
	if _, ok := rankHierarchy[codeTW].byID[sfgaRankID]; ok {
		return sfgaRankID
	}
	// Suffix-stripped aliases for the SECTION family.
	for _, suffix := range []string{"_ZOOLOGY", "_BOTANY"} {
		if strings.HasSuffix(sfgaRankID, suffix) {
			base := strings.TrimSuffix(sfgaRankID, suffix)
			if _, ok := rankHierarchy[codeTW].byID[base]; ok {
				// Zoology suffix under ICN or vice versa is spurious;
				// the base must actually exist under this code.
				return base
			}
		}
	}
	return ""
}

// groupDefaultParents returns the group IDs whose ranks are the
// default valid parents for a given rank group — matching the TW
// `valid_parents` methods on each *_group.rb.
func groupDefaultParents(group string) []string {
	switch group {
	case "higher":
		return []string{"higher"}
	case "family":
		return []string{"higher", "family"}
	case "genus":
		return []string{"family", "genus"}
	case "species":
		return []string{"genus", "species"}
	}
	return nil
}

// validParentIDs resolves a rank's effective valid-parents set. When
// the rank declares an explicit override in TW's Ruby source, use it
// verbatim; otherwise fall back to the group default. Returns rank
// IDs local to the given code.
func validParentIDs(codeTW string, r rankRow) map[string]bool {
	out := map[string]bool{}
	if len(r.ValidParentsOverride) > 0 {
		for _, id := range r.ValidParentsOverride {
			out[id] = true
		}
		return out
	}
	code := rankHierarchy[codeTW]
	for _, g := range groupDefaultParents(r.Group) {
		for _, id := range code.idsByGroup[g] {
			out[id] = true
		}
	}
	return out
}

// ValidChildRankIDs returns the rank IDs valid as children of a taxon
// whose rank is parentRankID under the given nomenclatural code
// (sfga's nom_code value — ZOOLOGICAL / BOTANICAL / BACTERIAL /
// VIRUS). Empty codeSfga or an unmapped code returns nil to signal
// "no filter" — callers show all ranks.
//
// The returned list is sorted alphabetically. TypicalUse-marked ranks
// (subspecies in ICZN, etc.) are not surfaced separately here; a
// future picker enhancement could pin them at the top.
//
// If parentRankID is empty (root-taxon create), returns every rank
// available under the code — a curator building a fresh tree can
// legitimately pick any rank for their root.
func ValidChildRankIDs(codeSfga, parentRankID string) []string {
	codeTW := sfgaToTWCode[codeSfga]
	if codeTW == "" {
		return nil
	}
	code := rankHierarchy[codeTW]
	if code == nil {
		return nil
	}
	if parentRankID == "" {
		return code.allRankIDsSorted()
	}
	parentCanonical := canonicalizeSfgaRankID(codeTW, parentRankID)
	if parentCanonical == "" {
		return code.allRankIDsSorted()
	}
	var out []string
	for _, r := range code.Ranks {
		parents := validParentIDs(codeTW, r)
		if parents[parentCanonical] {
			out = append(out, r.RankID)
		}
	}
	sort.Strings(out)
	return out
}

func (c *rankCodeData) allRankIDsSorted() []string {
	out := make([]string, 0, len(c.Ranks))
	for _, r := range c.Ranks {
		out = append(out, r.RankID)
	}
	sort.Strings(out)
	return out
}
