package hive

import (
	"sort"
	"strings"
	"unicode"
)

// rerankHits applies composite ranking to search hits from the
// FTS-backed modes (partial, fuzzy). SQL selects a candidate pool
// ordered by FTS bm25; this pass reorders that pool using
// taxonomy-aware signals so the intended target ranks first even
// when bm25 alone would prefer a longer / trigram-richer noise row
// (the canonical case: "Cerpolastes" query, "Herpolasia" outranks
// "Ceroplastes" by pure trigram overlap; edit-distance re-scoring
// flips it back).
//
// Prefix mode skips reranking — its SQL order (alphabetical by
// matched_name) is already the right UX for anchored matches.
//
// Signals composed into one score per hit (higher = better):
//
//   * +8 prefix anchor — matched_name starts with the query
//   * +6 epithet position — last space-separated token of matched_name
//        starts with the query, and query is all-lowercase (the
//        curator was searching by epithet, not by genus)
//   * +3 exact epithet — the last token EQUALS the query (not just
//        prefix-match) — surfaces "Ceroplastes rusci" over
//        "Alyxia ruscifolia" for q="rusci"
//   * +4 capitalization hint — query starts uppercase and matched_name
//        starts with the same character (curator is honoring
//        taxonomic convention: Genus)
//   * +0..4 length ratio — shorter matches are tighter fits; a hit
//        whose matched_name is exactly the query length scores +4,
//        one twice as long scores +2
//   * -2 synonym penalty — accepted-name matches float above synonym
//        matches for otherwise-tied candidates. Dedup already handles
//        the same-taxon case; this handles distinct taxa competing
//        for a slot.
//   * +3 authorship prefix — gnparser extracted an authorship from
//        the query and it prefix-matches the hit's col__authorship
//   * -0.5 per Levenshtein edit distance point (fuzzy mode only) —
//        rewards typo-corrected matches over trigram-lucky noise
//
// Ties are broken alphabetically by matched_name for stable UX
// across repeated identical searches.
func (a *Archive) rerankHits(q string, mode SearchMode, hits []TaxonHit) []TaxonHit {
	if len(hits) < 2 {
		return hits
	}
	sig := a.buildScoreSignals(q, mode)
	type scored struct {
		hit   TaxonHit
		score float64
	}
	pool := make([]scored, len(hits))
	for i, h := range hits {
		pool[i] = scored{h, sig.score(h)}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].score != pool[j].score {
			return pool[i].score > pool[j].score
		}
		return pool[i].hit.MatchedName < pool[j].hit.MatchedName
	})
	out := make([]TaxonHit, len(pool))
	for i, s := range pool {
		out[i] = s.hit
	}
	return out
}

// scoreSignals captures the query-derived state a single score()
// call needs, precomputed once per SearchTaxa invocation so the
// per-hit loop stays hot.
//
// query / lowerQuery are the *matchable* portion — for a fully-
// cited paste like "Panthera leo (Linnaeus, 1758)" gnparser
// extracts the canonical ("Panthera leo") into query, and the
// authorship goes into parsedAuthorship for its own boost signal.
// Using the canonical (not the raw input) for prefix, epithet, and
// length signals keeps a citation-carrying query from looking
// "long" to the length-ratio comparison against short matches.
type scoreSignals struct {
	query            string
	lowerQuery       string
	parsedAuthorship string
	queryStartsUpper bool
	queryIsLower     bool
	mode             SearchMode
}

func (a *Archive) buildScoreSignals(q string, mode SearchMode) scoreSignals {
	// gnparser also runs inside buildFTSMatch / buildFTSTrigramMatch
	// during arm construction — parsing again here is a few
	// microseconds and avoids threading the parse result through
	// the SearchTaxa switch.
	a.parserMu.Lock()
	parsed := a.parser.ParseName(q).Flatten()
	a.parserMu.Unlock()
	// Prefer the parser's canonical when it recognized a multi-token
	// name, so the length/prefix/epithet comparisons use just the
	// name part and not the authorship citation.
	matchable := q
	if parsed.Cardinality >= 2 && parsed.CanonicalSimple != "" {
		matchable = parsed.CanonicalSimple
	}
	sig := scoreSignals{
		query:            matchable,
		lowerQuery:       strings.ToLower(matchable),
		parsedAuthorship: parsed.Authorship,
		mode:             mode,
	}
	sig.queryIsLower = matchable == sig.lowerQuery
	for _, r := range matchable {
		sig.queryStartsUpper = unicode.IsUpper(r)
		break
	}
	return sig
}

func (s scoreSignals) score(h TaxonHit) float64 {
	matched := h.MatchedName
	if matched == "" {
		matched = h.Name
	}
	lower := strings.ToLower(matched)

	score := 0.0

	// Prefix anchor. Matched_name begins with the query — the
	// strongest single signal for "this is what the curator meant".
	if strings.HasPrefix(lower, s.lowerQuery) {
		score += 8
	}

	// Epithet-position vs capitalization-hint are mutually exclusive:
	// a curator typing lowercase is looking for any word starting
	// with their query (usually the epithet); a curator typing
	// uppercase is signaling "genus" and doesn't want epithet hits
	// crowding the top. Both cost a full token boost.
	if s.queryStartsUpper {
		// Uppercase → boost matches that begin with the same
		// character. Composes with the prefix anchor above when the
		// query is a genus prefix like "Cerop" matching "Ceroplastes".
		if firstChar(matched) == firstChar(s.query) {
			score += 4
		}
	} else if s.queryIsLower {
		// Lowercase → boost when the last space-separated token of
		// matched_name begins with the query. Fires for the "type
		// epithet, find the binomial" flow: q="rusci", matched
		// "Ceroplastes rusci", last token "rusci" starts with q.
		//
		// An additional bonus when the last token EQUALS the query
		// exactly separates the desired hit ("Ceroplastes rusci")
		// from a prefix-of-longer-epithet hit ("Alyxia ruscifolia")
		// — both get the +6 prefix boost, only the exact match
		// gets the extra.
		if tokens := strings.Fields(lower); len(tokens) > 0 {
			last := tokens[len(tokens)-1]
			if strings.HasPrefix(last, s.lowerQuery) {
				score += 6
				if last == s.lowerQuery {
					score += 3
				}
			}
		}
	}

	// Length ratio. Tighter fit (query and match closer in length)
	// scores higher. Symmetric — a match shorter than the query is
	// as bad a fit as one much longer, since either direction means
	// characters are unaccounted for. min/max keeps the ratio in
	// [0, 1] regardless of which side is longer.
	if qn, mn := len(s.query), len(matched); qn > 0 && mn > 0 {
		shorter, longer := qn, mn
		if shorter > longer {
			shorter, longer = longer, shorter
		}
		score += float64(shorter) / float64(longer) * 4
	}

	// Synonym penalty. For otherwise-equivalent candidates, the
	// accepted-name match ranks first. Dedup already collapses the
	// same-taxon case; this handles distinct taxa competing for a
	// slot.
	if h.IsSynonym {
		score -= 2
	}

	// Authorship boost. When the curator pasted a fully-cited name
	// ("Panthera leo (Linnaeus, 1758)"), gnparser extracts the
	// authorship into parsedAuthorship. Candidates whose stored
	// authorship prefix-matches get a bump — pasting the exact
	// citation should surface the exact taxon.
	if s.parsedAuthorship != "" && h.Authorship != "" {
		if strings.HasPrefix(
			strings.ToLower(h.Authorship),
			strings.ToLower(s.parsedAuthorship),
		) {
			score += 3
		}
	}

	// Fuzzy-mode edit distance. bm25 alone lets a trigram-rich noise
	// row outrank the intended target (Herpolasia has more trigram
	// overlap with "Cerpolastes" than Ceroplastes does); subtracting
	// Levenshtein distance flips those cases back.
	if s.mode == SearchModeFuzzy {
		d := levenshtein(s.lowerQuery, lower)
		score -= float64(d) * 0.5
	}

	return score
}

// firstChar returns the first rune of s as a string, or "" for
// empty input. Small helper to make the capitalization check
// readable at the callsite.
func firstChar(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

// levenshtein computes the edit distance between two strings using
// the standard two-row dynamic programming table. Rune-aware so
// diacritics and multi-byte characters count as one edit.
//
// Adequate for the ~200-candidate rerank pool at combobox latency;
// no need for the more elaborate Wagner-Fischer with early
// termination that a bulk fuzzy-matching pipeline would want.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(min(prev[j]+1, curr[j-1]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}
