// Package labeldate parses the taxonomic-community label-date
// format (DD-<roman>-YYYY, e.g. "14-viii-2017") into ISO 8601. The
// format is a deliberate community convention to sidestep the
// MM-DD-YYYY / DD-MM-YYYY localization ambiguity that plagues
// international specimen exchange — see the
// project_taxonomic_date_conventions memory for why.
//
// Deterministic parse (regex + 12-entry month table); no LLM
// tokens needed and no room for interpretation slippage. Used by
// the compose-output post-processing step to derive ISO dates
// from verbatim label transcriptions the LLM produced, so
// downstream sort / calendar / scoring queries operate on
// canonical dates while the verbatim label form stays available
// for display.
package labeldate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// romanMonths maps the lowercase Roman-numeral month tokens to
// their 1-12 integer month numbers.
var romanMonths = map[string]int{
	"i":    1,
	"ii":   2,
	"iii":  3,
	"iv":   4,
	"v":    5,
	"vi":   6,
	"vii":  7,
	"viii": 8,
	"ix":   9,
	"x":    10,
	"xi":   11,
	"xii":  12,
}

// labelDateRE matches DD-<roman>-YYYY (day optional). Case-
// insensitive; accepts `-`, `.`, or whitespace as separators
// since curators and transcribers vary. Roman numerals are
// listed longest-first so alternation matches greedily
// (`viii` before `vii`, etc.).
var labelDateRE = regexp.MustCompile(
	`(?i)^\s*(?:(\d{1,2})\s*[.\-\s]\s*)?(viii|vii|vi|iv|v|iii|ii|i|xii|xi|ix|x)\s*[.\-\s]\s*(\d{4})\s*$`,
)

// isoDateRE matches an already-ISO verbatim value. Publishers
// occasionally ignore the taxonomic-community Roman-numeral
// convention and print ISO dates directly (or curators paste ISO
// into forms). Accept those verbatim rather than dropping the ISO
// derivation on the floor.
var isoDateRE = regexp.MustCompile(
	`^\s*(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?\s*$`,
)

// Parse takes a verbatim-date string and returns its ISO 8601 form
// plus a bool indicating whether the input matched a known format.
// Two formats are recognized:
//
//  1. Taxonomic label-date convention (Roman-numeral month):
//     "14-viii-2017" → "2017-08-14", "viii-2017" → "2017-08".
//  2. Verbatim already in ISO form: "2017-08-14" / "2017-08" /
//     "2017" → returned unchanged after calendar validation.
//     Covers papers whose publishers don't follow the community
//     Roman-numeral convention.
//
// Returns:
//
//   - full "YYYY-MM-DD" when day + month + year resolve to a real
//     calendar date (Feb 30 → not ok);
//   - partial "YYYY-MM" when only month + year are present;
//   - "YYYY" when only year is present (from ISO input);
//   - "", false for anything that doesn't match either format or
//     that resolves to an impossible date.
//
// Deliberately strict on English / other-language month names —
// leaving ISO empty and letting the curator correct via the
// per-assertion correction flow is safer than a lax parser that
// silently accepts ambiguous input.
func Parse(s string) (string, bool) {
	if iso, ok := parseISO(s); ok {
		return iso, true
	}
	return parseRoman(s)
}

func parseRoman(s string) (string, bool) {
	m := labelDateRE.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	monthKey := strings.ToLower(m[2])
	month, ok := romanMonths[monthKey]
	if !ok {
		return "", false
	}
	year := m[3]
	if m[1] == "" {
		return fmt.Sprintf("%s-%02d", year, month), true
	}
	day, err := strconv.Atoi(m[1])
	if err != nil {
		return "", false
	}
	iso := fmt.Sprintf("%s-%02d-%02d", year, month, day)
	// time.Parse validates the calendar (Feb 30, month 13, ...).
	if _, err := time.Parse("2006-01-02", iso); err != nil {
		return "", false
	}
	return iso, true
}

func parseISO(s string) (string, bool) {
	m := isoDateRE.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	year := m[1]
	if m[2] == "" {
		return year, true
	}
	monthStr := m[2]
	if m[3] == "" {
		// Validate month is 01-12.
		month, err := strconv.Atoi(monthStr)
		if err != nil || month < 1 || month > 12 {
			return "", false
		}
		return year + "-" + monthStr, true
	}
	iso := year + "-" + monthStr + "-" + m[3]
	if _, err := time.Parse("2006-01-02", iso); err != nil {
		return "", false
	}
	return iso, true
}
