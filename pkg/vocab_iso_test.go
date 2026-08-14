package hive

import "testing"

// TestCountriesLoaded — the embedded ISO 3166-1 catalog parses and
// contains sanity-check entries. Guards against silently shipping an
// empty or malformed data file.
func TestCountriesLoaded(t *testing.T) {
	cs, err := Countries()
	if err != nil {
		t.Fatalf("Countries: %v", err)
	}
	if len(cs) < 240 {
		t.Errorf("expected ≥ 240 countries, got %d", len(cs))
	}
	byID := make(map[string]Country, len(cs))
	for _, c := range cs {
		byID[c.ID] = c
	}
	for _, want := range []struct{ id, name string }{
		{"US", "United States of America"},
		{"FR", "France"},
		{"JP", "Japan"},
	} {
		c, ok := byID[want.id]
		if !ok {
			t.Errorf("missing %q in countries catalog", want.id)
			continue
		}
		if c.Name != want.name {
			t.Errorf("%s name = %q, want %q", want.id, c.Name, want.name)
		}
	}
}

// TestLanguagesLoaded — the embedded ISO 639-3 catalog parses and
// contains the common-case entries.
func TestLanguagesLoaded(t *testing.T) {
	ls, err := Languages()
	if err != nil {
		t.Fatalf("Languages: %v", err)
	}
	if len(ls) < 7000 {
		t.Errorf("expected ≥ 7000 languages, got %d", len(ls))
	}
	byID := make(map[string]Language, len(ls))
	for _, l := range ls {
		byID[l.ID] = l
	}
	for _, want := range []struct{ id, name string }{
		{"eng", "English"},
		{"fra", "French"},
		{"deu", "German"},
		{"zho", "Chinese"},
	} {
		l, ok := byID[want.id]
		if !ok {
			t.Errorf("missing %q in language catalog", want.id)
			continue
		}
		if l.Name != want.name {
			t.Errorf("%s name = %q, want %q", want.id, l.Name, want.name)
		}
	}
}

// TestSexTermsLoaded — the enriched sex vocab has the three sfga
// terms and carries the ♀ / ♂ / ⚥ symbols.
func TestSexTermsLoaded(t *testing.T) {
	terms, err := SexTerms()
	if err != nil {
		t.Fatalf("SexTerms: %v", err)
	}
	if len(terms) < 3 {
		t.Fatalf("expected ≥ 3 sex terms, got %d", len(terms))
	}
	byID := make(map[string]SexTerm, len(terms))
	for _, s := range terms {
		byID[s.ID] = s
	}
	for _, id := range []string{"FEMALE", "MALE", "HERMAPHRODITE"} {
		s, ok := byID[id]
		if !ok {
			t.Errorf("missing %q in sex vocab", id)
			continue
		}
		if s.Symbol == "" {
			t.Errorf("%s missing symbol", id)
		}
		if s.Description == "" {
			t.Errorf("%s missing description", id)
		}
	}
}
