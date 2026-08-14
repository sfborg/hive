package hive

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// External reference-data vocabularies — ISO 3166-1 country codes,
// ISO 639-3 language codes, and the rich sex vocabulary. Loaded from
// JSON files embedded at build time so the served list matches every
// hive installation regardless of what an archive happens to
// populate its own tiny sex table with. Sourced from ChecklistBank:
//
//   https://api.checklistbank.org/vocab/country
//   https://api.checklistbank.org/vocab/language
//   https://api.checklistbank.org/vocab/sex
//
// A short generator note lives in tools/mkisovocab (planned) so a
// refresh is one command; the current files were captured from the
// live endpoints and can be regenerated the same way.

//go:embed iso3166.json
var iso3166JSON []byte

//go:embed iso639.json
var iso639JSON []byte

//go:embed sex_vocab.json
var sexVocabJSON []byte

// Country is one ISO 3166-1 entry surfaced by /api/vocab/countries.
// `ID` carries the alpha-2 code (matching sfga's
// vernacular.col__country storage convention). Alpha3 and Continent
// are extra context the frontend can render in dropdown rows.
type Country struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Alpha3    string `json:"alpha3,omitempty"`
	Continent string `json:"continent,omitempty"`
}

// Language is one ISO 639-3 entry surfaced by /api/vocab/languages.
// `ID` is the three-letter code (matching sfga's
// vernacular.col__language storage convention).
type Language struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SexTerm is one entry in the rich sex vocabulary — same three
// values sfga's `sex` table carries (FEMALE / MALE / HERMAPHRODITE)
// plus the ChecklistBank-sourced human-facing name, glyph symbol,
// and definition. The plain /api/vocab bundle still ships the flat
// sfga vocab; this endpoint serves the enriched form pickers use.
type SexTerm struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Symbol      string `json:"symbol,omitempty"`
	Description string `json:"description,omitempty"`
}

var (
	countriesOnce sync.Once
	countriesData []Country
	countriesErr  error

	languagesOnce sync.Once
	languagesData []Language
	languagesErr  error

	sexTermsOnce sync.Once
	sexTermsData []SexTerm
	sexTermsErr  error
)

// Countries returns the ISO 3166-1 alpha-2 catalog, alphabetized by
// name. Parsed from the embedded JSON on first call and cached for
// the process lifetime — the data is immutable.
func Countries() ([]Country, error) {
	countriesOnce.Do(func() {
		var wrap struct {
			Items []Country `json:"items"`
		}
		if err := json.Unmarshal(iso3166JSON, &wrap); err != nil {
			countriesErr = fmt.Errorf("core: parse iso3166.json: %w", err)
			return
		}
		countriesData = wrap.Items
	})
	return countriesData, countriesErr
}

// Languages returns the ISO 639-3 catalog, alphabetized by name.
// Parsed from the embedded JSON on first call.
func Languages() ([]Language, error) {
	languagesOnce.Do(func() {
		var wrap struct {
			Items []Language `json:"items"`
		}
		if err := json.Unmarshal(iso639JSON, &wrap); err != nil {
			languagesErr = fmt.Errorf("core: parse iso639.json: %w", err)
			return
		}
		languagesData = wrap.Items
	})
	return languagesData, languagesErr
}

// SexTerms returns the enriched sex vocabulary (symbol + description
// per term) — same set as sfga's flat `sex` table but suitable for a
// picker that wants to show the ♀ / ♂ / ⚥ glyph and hover-tooltip
// definition.
func SexTerms() ([]SexTerm, error) {
	sexTermsOnce.Do(func() {
		var wrap struct {
			Items []SexTerm `json:"items"`
		}
		if err := json.Unmarshal(sexVocabJSON, &wrap); err != nil {
			sexTermsErr = fmt.Errorf("core: parse sex_vocab.json: %w", err)
			return
		}
		sexTermsData = wrap.Items
	})
	return sexTermsData, sexTermsErr
}
