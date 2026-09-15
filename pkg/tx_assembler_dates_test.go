package hive

import (
	"testing"
	"time"
)

// TestParsePublicationDate covers the col__issued shapes the
// assembler encounters in the wild: bare year (BHL, most legacy
// data), year-month, ISO date, and full RFC3339 timestamp.
func TestParsePublicationDate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Time
		ok   bool
	}{
		{"year only", "2026",
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"year-month", "2026-09",
			time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), true},
		{"iso date", "2026-09-15",
			time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), true},
		{"rfc3339", "2026-09-15T14:30:00Z",
			time.Date(2026, 9, 15, 14, 30, 0, 0, time.UTC), true},
		{"empty", "", time.Time{}, false},
		{"whitespace", "   ", time.Time{}, false},
		{"garbage", "circa 1830", time.Time{}, false},
		{"partial year", "26", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePublicationDate(tc.in)
			if ok != tc.ok {
				t.Fatalf("parsePublicationDate(%q) ok = %v, want %v",
					tc.in, ok, tc.ok)
			}
			if ok && !got.Equal(tc.want) {
				t.Errorf("parsePublicationDate(%q) = %v, want %v",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestParsePublicationDate_YearOnlySortsFirst confirms the
// conservative "year-only sorts before any dated entry in the
// same year" behavior — important for the assembler's
// most-recent-wins tiebreak when one paper carries just the year
// and another carries a full date.
func TestParsePublicationDate_YearOnlySortsFirst(t *testing.T) {
	yearOnly, ok := parsePublicationDate("2026")
	if !ok {
		t.Fatal("year-only failed to parse")
	}
	fullDate, ok := parsePublicationDate("2026-09-15")
	if !ok {
		t.Fatal("full date failed to parse")
	}
	if !yearOnly.Before(fullDate) {
		t.Errorf("year-only (%v) should sort before dated (%v)",
			yearOnly, fullDate)
	}
}
