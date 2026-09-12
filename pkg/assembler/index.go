package assembler

import "strings"

// nameKey normalizes a (canonical, authorship) pair to the string
// used as an index key. Both fields are trimmed and lowercased so
// case-insensitive matching works; empty authorship is preserved
// as an empty suffix so the canonical-only key and the
// canonical+authorship key are always distinct.
//
// Format: "<canonical>||<authorship>". The double-pipe is a delimiter
// that can't legally appear in a parsed name or authorship.
func nameKey(canonical, authorship string) string {
	c := strings.ToLower(strings.TrimSpace(canonical))
	a := strings.ToLower(strings.TrimSpace(authorship))
	return c + "||" + a
}

// canonicalOnlyKey drops the authorship for the ambiguous-canonical
// lookup path. Used when the paper mention has no authorship — the
// planner queries this index for candidates and flags on multiple
// hits.
func canonicalOnlyKey(canonical string) string {
	return strings.ToLower(strings.TrimSpace(canonical))
}

// taxonIndex holds fast lookups by (canonical, authorship) and by
// canonical-only across the Existing snapshot plus any taxa the
// planner has virtually added so far in the same run.
type taxonIndex struct {
	// byFull maps nameKey(canonical, authorship) → the taxon id
	// bound to that name. Multiple taxa can share a canonical +
	// authorship pair in theory (primary homonyms across authors,
	// dataset merges); when they do, the value is the sentinel
	// "" and the ids live in byFullMulti[key].
	byFull      map[string]string
	byFullMulti map[string][]string

	// byCanonical maps a canonical-only key to every taxon id
	// carrying that canonical, regardless of authorship. Used by
	// the mention-has-no-authorship branch of the auto-match rule.
	byCanonical map[string][]string
}

// newTaxonIndex builds an index from an existing-taxa snapshot.
func newTaxonIndex(taxa []ExistingTaxon) *taxonIndex {
	idx := &taxonIndex{
		byFull:      make(map[string]string, len(taxa)),
		byFullMulti: make(map[string][]string),
		byCanonical: make(map[string][]string, len(taxa)),
	}
	for _, t := range taxa {
		idx.add(t.TaxonID, t.Canonical, t.Authorship)
	}
	return idx
}

// add inserts a (taxonID, canonical, authorship) row into the
// index. Called during snapshot load and again as the planner
// virtualizes new-taxon creations.
func (i *taxonIndex) add(taxonID, canonical, authorship string) {
	if canonical == "" {
		return
	}
	full := nameKey(canonical, authorship)
	if existing, ok := i.byFull[full]; ok {
		// Second and subsequent hits — promote to multi.
		if _, promoted := i.byFullMulti[full]; !promoted {
			i.byFullMulti[full] = []string{existing}
		}
		i.byFullMulti[full] = append(i.byFullMulti[full], taxonID)
		i.byFull[full] = "" // sentinel: use byFullMulti
	} else {
		i.byFull[full] = taxonID
	}
	canonKey := canonicalOnlyKey(canonical)
	i.byCanonical[canonKey] = append(i.byCanonical[canonKey], taxonID)
}

// matchOutcome is the internal auto-match verdict.
type matchOutcome int

const (
	matchNone       matchOutcome = iota // no candidate found
	matchUnique                         // exactly one candidate
	matchAmbiguous                      // multiple candidates
)

// matchResult carries the auto-match verdict plus the candidate
// taxon ids (populated for both matchUnique — one id — and
// matchAmbiguous — the full list).
type matchResult struct {
	outcome    matchOutcome
	taxonID    string
	candidates []string
	// authorshipConfirmed is true when the mention carried
	// authorship and it matched. False for canonical-only matches.
	authorshipConfirmed bool
}

// lookup runs auto-match against the index. Semantics:
//
//   - Mention has canonical + authorship: prefer an exact
//     canonical+authorship match. If found and unique, return
//     matchUnique with authorshipConfirmed=true. If found and
//     ambiguous, return matchAmbiguous. If not found at all, fall
//     through to the canonical-only branch.
//   - Mention has canonical only, or authorship-required is off and
//     the full match missed: look up by canonical only. Unique →
//     matchUnique with authorshipConfirmed=false. Multiple →
//     matchAmbiguous. Zero → matchNone.
//
// The authorship-required rule (rejecting canonical-only matches) is
// enforced by the caller — this function reports both branches; the
// caller decides which to accept.
func (i *taxonIndex) lookup(canonical, authorship string) matchResult {
	if canonical == "" {
		return matchResult{outcome: matchNone}
	}
	if authorship != "" {
		full := nameKey(canonical, authorship)
		if id, ok := i.byFull[full]; ok {
			if id != "" {
				return matchResult{
					outcome:             matchUnique,
					taxonID:             id,
					authorshipConfirmed: true,
				}
			}
			// Ambiguous full-match (multiple taxa share the pair).
			return matchResult{
				outcome:             matchAmbiguous,
				candidates:          append([]string(nil), i.byFullMulti[full]...),
				authorshipConfirmed: true,
			}
		}
		// Fall through to canonical-only.
	}
	canonKey := canonicalOnlyKey(canonical)
	ids := i.byCanonical[canonKey]
	switch len(ids) {
	case 0:
		return matchResult{outcome: matchNone}
	case 1:
		return matchResult{outcome: matchUnique, taxonID: ids[0]}
	default:
		return matchResult{
			outcome:    matchAmbiguous,
			candidates: append([]string(nil), ids...),
		}
	}
}
