package orcid

import "strings"

// Normalize accepts any of "0000-0002-1825-0097",
// "https://orcid.org/0000-0002-1825-0097", "0000000218250097", or
// mixed-case URLs, and returns the canonical dashed form.
//
// Returns [*InvalidIDError] (which matches [ErrInvalidID] via
// [errors.Is]) when the length, character set, or MOD 11-2 checksum
// is wrong.
func Normalize(s string) (string, error) {
	raw := s
	s = strings.TrimSpace(s)
	for _, prefix := range []string{
		"https://orcid.org/", "http://orcid.org/",
		"https://sandbox.orcid.org/", "http://sandbox.orcid.org/",
		"orcid.org/",
	} {
		if len(s) >= len(prefix) &&
			strings.EqualFold(s[:len(prefix)], prefix) {
			s = s[len(prefix):]
			break
		}
	}
	digits := strings.ReplaceAll(s, "-", "")
	if len(digits) != 16 {
		return "", &InvalidIDError{Input: raw, Reason: "must be 16 characters"}
	}
	for i, r := range digits {
		switch {
		case r >= '0' && r <= '9':
		case (r == 'X' || r == 'x') && i == 15:
			digits = digits[:15] + "X"
		default:
			return "", &InvalidIDError{Input: raw, Reason: "non-digit body / misplaced X"}
		}
	}
	if !validChecksum(digits) {
		return "", &InvalidIDError{Input: raw, Reason: "checksum mismatch"}
	}
	return digits[0:4] + "-" + digits[4:8] + "-" + digits[8:12] + "-" + digits[12:16], nil
}

// validChecksum verifies the MOD 11-2 (ISO 7064) check digit ORCID
// uses. The last character of digits is the expected check digit; the
// first 15 are the base.
func validChecksum(digits string) bool {
	total := 0
	for i := range 15 {
		total = (total + int(digits[i]-'0')) * 2
	}
	result := (12 - total%11) % 11
	want := byte('0' + result)
	if result == 10 {
		want = 'X'
	}
	return digits[15] == want
}

// SanitizeSearchTerm strips characters that ORCID's Solr-backed search
// grammar treats as operators (`+ - && || ! ( ) { } [ ] ^ " ~ * ? : \ /`)
// from an untrusted string, making it safe to splice into a
// [Client.Search] query without letting a caller inject additional
// clauses.
//
// ORCID's search does not document a per-character escape mechanism
// with wide client-library support, so injection safety here relies on
// stripping rather than escaping — a user-supplied literal ":" cannot
// be represented inside a query term.
func SanitizeSearchTerm(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '+', '-', '!', '(', ')', '{', '}', '[', ']',
			'^', '"', '~', '*', '?', ':', '\\', '/', '&', '|':
			return ' '
		}
		return r
	}, s)
}
