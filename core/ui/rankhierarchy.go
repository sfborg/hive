// Rank-hierarchy filtering: given a parent taxon's code + rank,
// return the set of ranks valid as a child. Ported from TaxonWorks'
// NomenclaturalRank class hierarchy (MIT, same research group), with
// two hive-specific tightenings:
//
//  1. The parent's own rank is never a valid child of itself, and
//     ranks above the parent within its own group are excluded via
//     TW's truncateAtRank logic. TW's UI does this via
//     setParentAndRanks.js + truncateAtRank.js in the Vue store.
//  2. Ranks that declare a `valid_parents_override` are ALWAYS
//     honored — TW's UI ignores overrides at display time (it uses
//     group truncation + typical_use only), which is why TW's
//     picker offers subspecies under a genus. Hive treats the
//     override as authoritative so subspecies only shows under
//     species, variety only under species/subspecies, etc.
//
// Data shape per code:
//   * ranks — every rank valid under that code, each with:
//     - group: "higher" | "family" | "genus" | "species"
//     - parent_rank_id: this rank's own parent in the ordered chain
//     - valid_parents_override: exact set of valid parent rank IDs
//       when TW's Ruby source declares one (tighter than the group
//       default). Null when the rank inherits from the group.
//     - typical_use: whether the rank is commonly used (defaults to
//       true in TW's base class; less-common ranks opt out).
//
// Group defaults (from TW's *_group.rb `valid_parents` methods):
//   * higher:  parents in {higher}
//   * family:  parents in {higher, family}
//   * genus:   parents in {family, genus}
//   * species: parents in {genus, species}
//
// Group order (from TW's UI iteration and childOfParent helper) is
// higher → family → genus → species. Higher-index groups are
// "deeper" in the tree; children come from groups at or below the
// parent's group index.

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

// ChildRank is one entry in the child-rank list returned to callers.
// TypicalUse pairs with a picker-side "show all" affordance: default
// display shows only typical ranks; a curator can widen via search
// or an explicit toggle to see everything else.
type ChildRank struct {
	ID         string `json:"id"`
	TypicalUse bool   `json:"typical_use"`
}

// rankCodeData is the per-code decoded slice plus lookup structures
// built once at init.
type rankCodeData struct {
	Code               string             // TW code slug
	Ranks              []rankRow          // in file order (TW's own emission)
	byID               map[string]rankRow // rank_id → row for O(1) lookup
	idsByGroup         map[string][]string
	orderedChainByGroup map[string][]string // per-group top→bottom rank IDs
}

var rankHierarchy map[string]*rankCodeData

// groupOrder mirrors TW's Object.keys(ranks) iteration order in
// rankSelector.vue — higher-index groups are lower in the tree
// (closer to leaves). Used for the group-truncation filter.
var groupOrder = []string{"higher", "family", "genus", "species"}

func groupIndex(g string) int {
	for i, name := range groupOrder {
		if name == g {
			return i
		}
	}
	return -1
}

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
		data.orderedChainByGroup = data.buildOrderedChains()
		rankHierarchy[code] = data
	}
}

// buildOrderedChains walks each group's parent_rank_id links to
// produce a top→bottom ordered chain of rank IDs. Mirrors TW's
// NomenclaturalRank.ordered_ranks. Ranks whose parent points outside
// the group (or is empty) are the group's roots; the chain descends
// via ranks whose parent_rank_id points into the group.
//
// A rank chain is expected to be linear within a group (each rank
// has exactly one child in the group). If TW ever branches within a
// group, the walk picks one path — good enough for the truncation
// filter, which only needs to distinguish "above parent" from
// "below parent" in position.
func (c *rankCodeData) buildOrderedChains() map[string][]string {
	out := make(map[string][]string, len(c.idsByGroup))
	for group, ids := range c.idsByGroup {
		inGroup := make(map[string]bool, len(ids))
		for _, id := range ids {
			inGroup[id] = true
		}
		childrenByParent := make(map[string][]string, len(ids))
		var roots []string
		for _, id := range ids {
			r := c.byID[id]
			if r.ParentRankID == "" || !inGroup[r.ParentRankID] {
				roots = append(roots, id)
				continue
			}
			childrenByParent[r.ParentRankID] = append(childrenByParent[r.ParentRankID], id)
		}
		// Deterministic root order — TW files list them in a
		// specific order; alphabetical is a stable stand-in.
		sort.Strings(roots)
		var chain []string
		seen := make(map[string]bool, len(ids))
		var walk func(id string)
		walk = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			chain = append(chain, id)
			children := childrenByParent[id]
			sort.Strings(children)
			for _, child := range children {
				walk(child)
			}
		}
		for _, root := range roots {
			walk(root)
		}
		out[group] = chain
	}
	return out
}

// positionInGroup returns the index of rankID in its group's ordered
// chain, or -1 if not present. Lower index = higher (closer to root)
// in the group. Used by ValidChildRanks to implement TW's
// truncateAtRank semantics.
func (c *rankCodeData) positionInGroup(group, rankID string) int {
	chain := c.orderedChainByGroup[group]
	for i, id := range chain {
		if id == rankID {
			return i
		}
	}
	return -1
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

// canonicalizeSfgaRankID maps sfga's disambiguated rank IDs to the
// TW rank they correspond to inside a specific code. Sfga carries
// SECTION_* and SUBSECTION_* / SUPERSECTION_* variants because
// SECTION is used differently across codes; TW captures the
// semantic under each code's own SECTION / SUBSECTION rank without
// the suffix. Empty return means the sfga rank has no TW equivalent
// under this code (e.g., SECTION_BOTANY under ICZN).
func canonicalizeSfgaRankID(codeTW, sfgaRankID string) string {
	if _, ok := rankHierarchy[codeTW].byID[sfgaRankID]; ok {
		return sfgaRankID
	}
	for _, suffix := range []string{"_ZOOLOGY", "_BOTANY"} {
		if base, found := strings.CutSuffix(sfgaRankID, suffix); found {
			if _, ok := rankHierarchy[codeTW].byID[base]; ok {
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
// verbatim; otherwise fall back to the group default.
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

// ValidChildRanks returns the rank IDs valid as children of a taxon
// whose rank is parentRankID under the given nomenclatural code
// (sfga's nom_code value — ZOOLOGICAL / BOTANICAL / BACTERIAL /
// VIRUS). Each returned entry carries a typical_use flag so the
// picker can default to the common set and reveal the rest via
// search or an explicit "show all" affordance.
//
// Filter combines:
//   1. Group-level truncation (TW's truncateAtRank + isMajor):
//      exclude groups above the parent's group; within the parent's
//      own group exclude ranks at or above the parent's position.
//   2. valid_parents_override enforcement (hive tightening): a rank
//      with an explicit override list is only valid under those
//      parents, regardless of the group-truncation result. Prevents
//      e.g. subspecies-under-genus that TW's UI accidentally allows.
//
// Empty codeSfga, unmapped code, or an unresolvable parent rank all
// return nil, signaling "no filter" — callers show every rank in the
// vocab. Empty parentRankID (root-taxon create) returns every rank
// under the code, all marked typical_use per their own row.
func ValidChildRanks(codeSfga, parentRankID string) []ChildRank {
	codeTW := sfgaToTWCode[codeSfga]
	if codeTW == "" {
		return nil
	}
	code := rankHierarchy[codeTW]
	if code == nil {
		return nil
	}
	if parentRankID == "" {
		return code.allChildRanks()
	}
	parentCanonical := canonicalizeSfgaRankID(codeTW, parentRankID)
	if parentCanonical == "" {
		return code.allChildRanks()
	}
	parentRow, ok := code.byID[parentCanonical]
	if !ok {
		return code.allChildRanks()
	}
	parentGroupIdx := groupIndex(parentRow.Group)
	parentPos := code.positionInGroup(parentRow.Group, parentCanonical)

	out := []ChildRank{}
	for _, r := range code.Ranks {
		// (1a) Exclude groups above the parent's group.
		if groupIndex(r.Group) < parentGroupIdx {
			continue
		}
		// (1b) Within the parent's own group, exclude ranks at or
		// above the parent's position — TW's truncateAtRank keeps
		// only strictly-below entries.
		if r.Group == parentRow.Group {
			pos := code.positionInGroup(r.Group, r.RankID)
			if pos <= parentPos {
				continue
			}
		}
		// (2) Honor valid_parents_override strictly — hive is
		// stricter than TW here.
		parents := validParentIDs(codeTW, r)
		if !parents[parentCanonical] {
			continue
		}
		out = append(out, ChildRank{ID: r.RankID, TypicalUse: r.TypicalUse})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *rankCodeData) allChildRanks() []ChildRank {
	out := make([]ChildRank, 0, len(c.Ranks))
	for _, r := range c.Ranks {
		out = append(out, ChildRank{ID: r.RankID, TypicalUse: r.TypicalUse})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
