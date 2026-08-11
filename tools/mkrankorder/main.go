// mkrankorder regenerates the rank_order parameter block in
// pkg/hive_rules.json by walking pkg/ui/rank_hierarchy.json (the
// TW-derived rank data hive already uses for its child-rank picker).
//
// This is the codegen backing the "put ranks in rules, keep DRY via
// tooling" trade — pkg/hive_rules.json stays self-contained (an
// external gsvalidator consumer can load it and validate against it
// without needing hive's internals), and this tool keeps its
// rank_order in sync with the rank data that already lives in
// pkg/ui/.
//
// Usage: go run ./tools/mkrankorder
//
// Design note: only the single `"rank_order": {...}` block on the
// hive_parent_rank_higher rule is rewritten. The rest of
// pkg/hive_rules.json is preserved byte-for-byte so hand-authored
// key ordering, indentation, and inline formatting survive.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Per-rank fields we consume from rank_hierarchy.json — group and
// parent_rank_id define each rank's position within its group's
// ordered chain. Other fields (typical_use,
// valid_parents_override) are the picker's business, not ours.
type rankRow struct {
	RankID       string `json:"rank_id"`
	Group        string `json:"group"`
	ParentRankID string `json:"parent_rank_id"`
}

type codeData struct {
	Ranks []rankRow `json:"ranks"`
}

// TW's group iteration in rankSelector.vue — higher groups sit
// closer to the tree root; lower groups (species) sit at the
// leaves. Concatenating each group's ordered chain in this order
// produces one top→bottom per-code list.
var groupOrder = []string{"higher", "family", "genus", "species"}

// sfga's nom_code IDs mapped from TW's per-code file keys. The
// parent_rank_higher rule keys rank_order by the sfga code ID.
var twToSfga = map[string]string{
	"iczn":  "ZOOLOGICAL",
	"icn":   "BOTANICAL",
	"icnp":  "BACTERIAL",
	"icvcn": "VIRUS",
}

func main() {
	hierarchyPath := "pkg/ui/rank_hierarchy.json"
	rulesPath := "pkg/hive_rules.json"

	rankOrder, err := loadRankOrder(hierarchyPath)
	if err != nil {
		die(fmt.Errorf("load rank hierarchy: %w", err))
	}
	if err := patchRulesFile(rulesPath, rankOrder); err != nil {
		die(fmt.Errorf("patch rules: %w", err))
	}
	fmt.Printf("mkrankorder: updated %s (%d codes, %d rank entries total)\n",
		rulesPath, len(rankOrder), totalEntries(rankOrder))
}

// loadRankOrder reads rank_hierarchy.json and returns the per-sfga-code
// ordered rank lists, higher→lower.
func loadRankOrder(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var byTWCode map[string]codeData
	if err := json.Unmarshal(raw, &byTWCode); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(twToSfga))
	for twCode, sfgaCode := range twToSfga {
		cd, ok := byTWCode[twCode]
		if !ok {
			return nil, fmt.Errorf("rank_hierarchy.json missing code %q", twCode)
		}
		var full []string
		for _, group := range groupOrder {
			full = append(full, buildOrderedChain(cd.Ranks, group)...)
		}
		out[sfgaCode] = full
	}
	return out, nil
}

// buildOrderedChain reproduces pkg/ui/rankhierarchy.go's
// buildOrderedChains logic for a single group — walk each root
// through its parent_rank_id children, alphabetically where the
// chain branches.
func buildOrderedChain(ranks []rankRow, group string) []string {
	inGroup := make(map[string]bool)
	for _, r := range ranks {
		if r.Group == group {
			inGroup[r.RankID] = true
		}
	}
	childrenByParent := make(map[string][]string)
	var roots []string
	for _, r := range ranks {
		if r.Group != group {
			continue
		}
		if r.ParentRankID == "" || !inGroup[r.ParentRankID] {
			roots = append(roots, r.RankID)
			continue
		}
		childrenByParent[r.ParentRankID] = append(childrenByParent[r.ParentRankID], r.RankID)
	}
	sort.Strings(roots)
	var chain []string
	seen := make(map[string]bool)
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		chain = append(chain, id)
		kids := childrenByParent[id]
		sort.Strings(kids)
		for _, k := range kids {
			walk(k)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return chain
}

// patchRulesFile does byte-level surgery on the `"rank_order":`
// value of the hive_parent_rank_higher rule. Every other byte in
// the file is preserved.
//
// The file is scanned once to find:
//  1. The `"rule_id": "hive_parent_rank_higher"` marker.
//  2. The nearest following `"rank_order":` key on the same rule.
//  3. The `{` opening the rank_order value, and its matching `}`.
//
// Then the [rank_order-open, rank_order-close] byte range is
// replaced with a freshly-emitted block. Since rank_order values are
// flat (map of string → array of strings), naive brace counting is
// safe.
func patchRulesFile(path string, rankOrder map[string][]string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	ruleMarker := []byte(`"rule_id": "hive_parent_rank_higher"`)
	ruleIdx := bytes.Index(src, ruleMarker)
	if ruleIdx < 0 {
		return fmt.Errorf(`no %q found`, string(ruleMarker))
	}
	keyMarker := []byte(`"rank_order":`)
	rel := bytes.Index(src[ruleIdx:], keyMarker)
	if rel < 0 {
		return fmt.Errorf("no %q key after rule marker", string(keyMarker))
	}
	keyPos := ruleIdx + rel
	// Value indent — spaces at the start of the line containing the key.
	lineStart := bytes.LastIndexByte(src[:keyPos], '\n') + 1
	indentEnd := lineStart
	for indentEnd < keyPos && src[indentEnd] == ' ' {
		indentEnd++
	}
	keyIndent := string(src[lineStart:indentEnd])
	// Locate the opening `{`.
	openPos := keyPos + len(keyMarker)
	for openPos < len(src) && (src[openPos] == ' ' || src[openPos] == '\t' || src[openPos] == '\n') {
		openPos++
	}
	if openPos >= len(src) || src[openPos] != '{' {
		return fmt.Errorf("rank_order value must be an object; got %q at %d", src[openPos:min(openPos+10, len(src))], openPos)
	}
	// Naive brace count. rank_order is a flat map of string → []string,
	// so nested `{` inside strings is nonsensical and we can ignore it.
	depth := 0
	closePos := -1
	for i := openPos; i < len(src); i++ {
		if src[i] == '{' {
			depth++
		} else if src[i] == '}' {
			depth--
			if depth == 0 {
				closePos = i
				break
			}
		}
	}
	if closePos < 0 {
		return fmt.Errorf("unbalanced braces starting at %d", openPos)
	}

	newBlock := renderRankOrder(rankOrder, keyIndent)

	var out bytes.Buffer
	out.Grow(len(src) + len(newBlock))
	out.Write(src[:openPos])
	out.WriteString(newBlock)
	out.Write(src[closePos+1:])
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// renderRankOrder emits the rank_order value in a stable, terse
// shape: the outer object spans multiple lines with each code's
// list on a single line. Sorted by code for determinism.
func renderRankOrder(m map[string][]string, keyIndent string) string {
	codes := make([]string, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	entryIndent := keyIndent + "  "
	var sb strings.Builder
	sb.WriteString("{\n")
	for i, code := range codes {
		sb.WriteString(entryIndent)
		sb.WriteString(quoteJSONString(code))
		sb.WriteString(": [")
		for j, r := range m[code] {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(quoteJSONString(r))
		}
		sb.WriteString("]")
		if i < len(codes)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(keyIndent)
	sb.WriteString("}")
	return sb.String()
}

// quoteJSONString wraps a plain identifier-style value in a JSON
// string. Rank IDs and code IDs from rank_hierarchy.json are all
// simple ASCII words with underscores, so escaping is not needed —
// but we route through encoding/json to keep the tool safe against
// future data containing awkward characters.
func quoteJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func totalEntries(m map[string][]string) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "mkrankorder:", err)
	os.Exit(1)
}
