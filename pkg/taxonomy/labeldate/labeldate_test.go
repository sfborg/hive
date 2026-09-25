package labeldate

import "testing"

func TestParseHappyPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		// The canonical form.
		{"14-viii-2017", "2017-08-14"},
		// Uppercase Roman numerals.
		{"14-VIII-2017", "2017-08-14"},
		{"14-Viii-2017", "2017-08-14"},
		// Every month, single-day.
		{"1-i-2020", "2020-01-01"},
		{"1-ii-2020", "2020-02-01"},
		{"1-iii-2020", "2020-03-01"},
		{"1-iv-2020", "2020-04-01"},
		{"1-v-2020", "2020-05-01"},
		{"1-vi-2020", "2020-06-01"},
		{"1-vii-2020", "2020-07-01"},
		{"1-viii-2020", "2020-08-01"},
		{"1-ix-2020", "2020-09-01"},
		{"1-x-2020", "2020-10-01"},
		{"1-xi-2020", "2020-11-01"},
		{"1-xii-2020", "2020-12-01"},
		// Space + dot separators (curator transcription variance).
		{"14 viii 2017", "2017-08-14"},
		{"14.viii.2017", "2017-08-14"},
		{"14 . viii . 2017", "2017-08-14"},
		// Padded whitespace.
		{"  14-viii-2017  ", "2017-08-14"},
		// Two-digit day (edge of range).
		{"31-xii-2020", "2020-12-31"},
		// Single-digit day, no padding.
		{"9-iii-1999", "1999-03-09"},
		// Month + year only (day omitted).
		{"viii-2017", "2017-08"},
		{"iii-1999", "1999-03"},
		// Verbatim already in ISO — dates printed without the
		// Roman-numeral convention still get an ISO derivation.
		{"2017-08-14", "2017-08-14"},
		{"2017-08", "2017-08"},
		{"2017", "2017"},
		{"  2017-08-14  ", "2017-08-14"},
	}
	for _, tc := range tests {
		got, ok := Parse(tc.in)
		if !ok {
			t.Errorf("Parse(%q): expected ok=true", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	// Inputs that shouldn't match. Downstream can fall back to
	// LLM output or curator entry; better to reject than to
	// guess wrong.
	rejects := []string{
		"",
		"random",
		"viii",              // month alone
		"14-08-2017",       // digit month (localization ambiguity — the whole reason the label format exists)
		"14-xiii-2017",     // invalid Roman
		"31-ii-2017",       // Feb 31
		"32-viii-2017",      // impossible day
		"0-viii-2017",       // zero day
		"14-viii-17",        // 2-digit year
		"14-viii-2017 foo",  // trailing garbage
		"foo 14-viii-2017",  // leading garbage
		"14/viii/2017",      // wrong separator
		"August 14, 2017",    // English month name (deliberately out-of-scope; curator can correct via edit flow)
		"2017-13-01",       // impossible month even in ISO form
		"2017-02-30",       // impossible day even in ISO form
		"2017-8-14",        // ISO requires zero-padded month/day
	}
	for _, in := range rejects {
		got, ok := Parse(in)
		if ok {
			t.Errorf("Parse(%q) accepted with %q, expected reject", in, got)
		}
	}
}

func TestParseCalendarValidation(t *testing.T) {
	// Real-calendar rejects: month lengths + leap years.
	rejects := []string{
		"29-ii-2019", // 2019 not a leap year
		"31-iv-2020", // April has 30 days
		"31-vi-2020", // June has 30
		"31-ix-2020", // September has 30
		"31-xi-2020", // November has 30
	}
	for _, in := range rejects {
		_, ok := Parse(in)
		if ok {
			t.Errorf("Parse(%q) accepted, expected reject on calendar validation", in)
		}
	}
	// Leap-year Feb 29 accepted.
	got, ok := Parse("29-ii-2020")
	if !ok || got != "2020-02-29" {
		t.Errorf("Parse(29-ii-2020) = %q, %v; want 2020-02-29, true", got, ok)
	}
}
