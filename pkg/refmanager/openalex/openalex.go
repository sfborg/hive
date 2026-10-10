// Package openalex is a thin HTTP client for api.openalex.org that
// resolves DOIs and runs cross-field search, mapping the returned Work
// records into coldp.Reference values.
//
// LIFT-TO-SFLIB CANDIDATE. Every SFBorg tool that touches references
// wants this. Kept self-contained (no reach into hive-internal state,
// no singleton) so migration to sflib is a clean copy.
//
// Polite pool etiquette: OpenAlex asks clients to identify themselves
// via User-Agent or a mailto=<email> query param. Identified requests
// land in a higher-priority pool. Callers thread the curator email
// through New(email); empty is legal (anonymous pool, slower).
package openalex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sfborg/hive/pkg/refmanager/doi"
	"github.com/sfborg/sflib/pkg/coldp"
)

// Client is concurrent-safe (net/http.Client is). One instance per
// process is plenty.
type Client struct {
	http    *http.Client
	baseURL string
	email   string
}

// New constructs a client with an explicit email for the polite pool.
func New(email string) *Client {
	return &Client{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL: "https://api.openalex.org",
		email:   email,
	}
}

// Name implements hiveext.DOIResolver / FreeTextSearcher. Used in
// wire responses and logging to identify which provider handled a
// request.
func (c *Client) Name() string { return "openalex" }

// ErrNotFound is returned when OpenAlex has no work for the given DOI.
// Distinct from network failures so calling code can tell "DOI is bad"
// from "OpenAlex is down."
var ErrNotFound = errors.New("openalex: work not found")

// ErrRateLimited is returned when OpenAlex refuses a request for rate
// or daily-budget limits (HTTP 429). errors.As a *RateLimitError for
// how long OpenAlex asked to wait.
var ErrRateLimited = errors.New("openalex: rate limited")

// RateLimitError is a 429 response. RetryAfter is OpenAlex's
// Retry-After value, zero when it gave none.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("openalex: rate limited; retry after %s", e.RetryAfter)
	}
	return "openalex: rate limited"
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// maxRetryWait bounds the pause before the single retry after a 429.
// Longer waits (a spent daily budget) are returned to the caller.
const maxRetryWait = 5 * time.Second

// ResolveDOI fetches the OpenAlex Work record for a DOI and maps it to
// a coldp.Reference. The DOI may be a bare identifier
// ("10.1234/abcd") or a full URL ("https://doi.org/10.1234/abcd").
// Returns ErrNotFound on 404.
//
// The returned Reference carries no ID; the caller assigns one before
// persisting. OpenAlex's own W-IDs aren't preserved into sfga; a
// curator wanting that linkage can add it to col__alternative_id.
func (c *Client) ResolveDOI(ctx context.Context, value string) (*coldp.Reference, error) {
	id := doi.Normalize(value)
	if id == "" {
		return nil, fmt.Errorf("openalex: empty DOI")
	}
	path := "/works/doi:" + url.PathEscape(id)
	var raw work
	if err := c.get(ctx, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw.toReference(), nil
}

// Search runs OpenAlex's cross-field search (title / abstract / author)
// and returns up to `limit` results mapped to coldp.Reference. Search
// is fuzzy — OpenAlex handles typos and word ordering itself.
//
// Empty query returns nil rather than "everything"; callers filtering
// by name+year should compose a query string themselves.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]coldp.Reference, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	q := url.Values{
		"search":   {query},
		"per_page": {strconv.Itoa(limit)},
	}
	var raw worksPage
	if err := c.get(ctx, "/works", q, &raw); err != nil {
		return nil, err
	}
	out := make([]coldp.Reference, 0, len(raw.Results))
	for _, w := range raw.Results {
		out = append(out, *w.toReference())
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, path string, extra url.Values, out any) error {
	if extra == nil {
		extra = url.Values{}
	}
	if c.email != "" {
		extra.Set("mailto", c.email)
	}
	full := c.baseURL + path
	if len(extra) > 0 {
		full += "?" + extra.Encode()
	}
	// One retry after a short 429 pause; a long one is the caller's.
	var resp *http.Response
	var body []byte
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return fmt.Errorf("openalex: build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent())
		resp, err = c.http.Do(req)
		if err != nil {
			return fmt.Errorf("openalex: %s: %w", path, err)
		}
		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("openalex: read %s: %w", path, err)
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		wait := retryAfter(resp.Header.Get("Retry-After"))
		if attempt > 0 || wait > maxRetryWait {
			return &RateLimitError{RetryAfter: wait}
		}
		if wait == 0 {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("openalex: %s: HTTP %d: %s",
			path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("openalex: parse %s: %w", path, err)
	}
	return nil
}

// retryAfter parses a Retry-After header given in seconds or as an
// HTTP date; zero when absent or unparseable.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) userAgent() string {
	if c.email != "" {
		return "hive/0.1 (mailto:" + c.email + ")"
	}
	return "hive/0.1 (+https://github.com/sfborg/hive)"
}

// ---------- OpenAlex JSON shapes (partial) ----------
//
// Only the fields hive maps into coldp.Reference are decoded. Extra
// JSON keys are silently ignored by encoding/json.

type worksPage struct {
	Results []work `json:"results"`
}

type work struct {
	ID              string    `json:"id"`
	DOI             string    `json:"doi"`
	Title           string    `json:"title"`
	DisplayName     string    `json:"display_name"`
	PublicationYear int       `json:"publication_year"`
	PublicationDate string    `json:"publication_date"`
	Type            string    `json:"type"`
	Authorships     []author  `json:"authorships"`
	PrimaryLocation *location `json:"primary_location"`
	Biblio          *biblio   `json:"biblio"`
	Abstract        string    `json:"abstract"`
}

type author struct {
	RawAuthorName string  `json:"raw_author_name"`
	Author        *person `json:"author"`
}

type person struct {
	DisplayName string `json:"display_name"`
	ORCID       string `json:"orcid"`
}

type location struct {
	Source         *source `json:"source"`
	LandingPageURL string  `json:"landing_page_url"`
}

type source struct {
	DisplayName string   `json:"display_name"`
	Type        string   `json:"type"`
	ISSN        []string `json:"issn"`
	ISSN_L      string   `json:"issn_l"`
	Publisher   string   `json:"host_organization_name"`
}

type biblio struct {
	Volume    string `json:"volume"`
	Issue     string `json:"issue"`
	FirstPage string `json:"first_page"`
	LastPage  string `json:"last_page"`
}

// italicTagBoundaryAfter / italicTagBoundaryBefore match the tag-
// boundary positions where JATS-to-string conversion at some
// publishers drops the surrounding space. Only italic tags are
// targeted — <sup>/<sub> legitimately attach to adjacent characters
// (chemistry, math) and would be wrong to space out.
//
// \p{L}\p{N} covers non-ASCII letters common in older natural-history
// titles (umlauts, diacritics).
var (
	italicTagBoundaryAfter  = regexp.MustCompile(`(</(?:i|italic|em)>)([\p{L}\p{N}])`)
	italicTagBoundaryBefore = regexp.MustCompile(`([\p{L}\p{N}])(<(?:i|italic|em)>)`)
)

// repairTagBoundarySpacing inserts a single space between an italic
// tag boundary and an adjacent word character in either direction.
// Fixes a common JATS-to-string bug where publisher-supplied metadata
// arrives as "</i>Coleoptera<i>" instead of "</i> Coleoptera <i>".
// Idempotent: running on already-clean input is a no-op.
//
// Applied to title-like fields at the OpenAlex boundary so the tag-
// stripped display and the tag-preserving stored value both read
// correctly. Semantic markup (roman-inverted taxa in italicized
// titles) is preserved.
func repairTagBoundarySpacing(s string) string {
	if !strings.ContainsAny(s, "<>") {
		return s
	}
	s = italicTagBoundaryAfter.ReplaceAllString(s, "$1 $2")
	s = italicTagBoundaryBefore.ReplaceAllString(s, "$1 $2")
	return s
}

// lowerParticles are the surname / given-name particles that
// conventionally stay lowercase in a title-cased name — "van der
// Berg", "de la Cruz", "y Cajal". Applied only when they appear
// after the first word (a leading "Van" is more likely to be an
// English given name than a Dutch particle).
var lowerParticles = map[string]bool{
	"de": true, "del": true, "la": true, "le": true, "les": true,
	"du": true, "des": true, "den": true, "der": true, "das": true,
	"dos": true, "di": true, "da": true, "do": true, "van": true,
	"von": true, "y": true, "e": true, "of": true, "the": true, "el": true,
}

// titleCaseIfShouting fixes publisher-uppercased author names
// ("DOUGLASS R. MILLER" → "Douglass R. Miller") while leaving
// already-mixed-case input alone. The trigger is strict: any
// lowercase letter in the input means "leave as-is", so a name
// that's genuinely mixed-case (including edge cases like "John
// De Olso" where "De" is genuinely capitalized for that person)
// doesn't get downcased.
//
// Particle-aware — "JOHN VAN DER BERG" resolves to "John van der
// Berg" rather than the naive titlecase "John Van Der Berg" —
// though this is wrong for the rare "Sean Van Buren"-as-given-
// name case. The taxonomic-author convention favors particle
// lowercasing so we skew that way.
//
// Mc-prefix handling: "MCDONALD" becomes "McDonald". Mac is left
// as-is (Mac* words split ambiguously — "Machel" isn't a Mc-name
// but shares the prefix).
func titleCaseIfShouting(s string) string {
	if !isShouting(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && lowerParticles[strings.TrimRight(w, ".,;")] {
			continue
		}
		words[i] = capitalizeAuthorWord(w)
	}
	return strings.Join(words, " ")
}

// isShouting reports whether a string has letters but no
// lowercase letters — the "publisher SHOUTED THE AUTHOR NAMES"
// signal. Punctuation, digits, and whitespace don't count either
// way; the presence of any lowercase letter defeats the trigger.
func isShouting(s string) bool {
	hasUpper := false
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			hasUpper = true
		}
	}
	return hasUpper
}

// capitalizeAuthorWord titlecases a single lowercased word with
// small allowances for Mc-prefix names. Punctuation trailing the
// alphabetic part (a period after an initial) survives untouched.
func capitalizeAuthorWord(w string) string {
	if w == "" {
		return w
	}
	if strings.HasPrefix(w, "mc") && len(w) > 2 {
		r, _ := utf8.DecodeRuneInString(w[2:])
		if unicode.IsLetter(r) {
			return "Mc" + string(unicode.ToUpper(r)) + w[2+utf8.RuneLen(r):]
		}
	}
	r, size := utf8.DecodeRuneInString(w)
	return string(unicode.ToUpper(r)) + w[size:]
}

func (w work) toReference() *coldp.Reference {
	title := w.Title
	if title == "" {
		title = w.DisplayName
	}
	title = repairTagBoundarySpacing(title)
	var authors []string
	for _, a := range w.Authorships {
		name := a.RawAuthorName
		if name == "" && a.Author != nil {
			name = a.Author.DisplayName
		}
		if name != "" {
			authors = append(authors, titleCaseIfShouting(name))
		}
	}
	authorField := strings.Join(authors, "; ")
	var (
		container, containerType, publisher string
		issns                               []string
	)
	if w.PrimaryLocation != nil && w.PrimaryLocation.Source != nil {
		src := w.PrimaryLocation.Source
		container = repairTagBoundarySpacing(src.DisplayName)
		containerType = src.Type
		publisher = src.Publisher
		// Preserve every ISSN (print + electronic when a journal
		// has both) so downstream serial lookups can match on
		// either — TaxonWorks and other systems may index serials
		// by only one medium's ISSN. The comma-joined value is
		// safe for check-digit validation because the ISSN rule
		// carries a `separator: ","` param and validates per-token.
		if src.ISSN_L != "" {
			issns = append(issns, src.ISSN_L)
		}
		for _, i := range src.ISSN {
			if i != src.ISSN_L {
				issns = append(issns, i)
			}
		}
	}
	var landing string
	if w.PrimaryLocation != nil {
		landing = w.PrimaryLocation.LandingPageURL
	}
	var volume, issue, page string
	if w.Biblio != nil {
		volume = w.Biblio.Volume
		issue = w.Biblio.Issue
		switch {
		case w.Biblio.FirstPage != "" && w.Biblio.LastPage != "":
			page = w.Biblio.FirstPage + "–" + w.Biblio.LastPage
		case w.Biblio.FirstPage != "":
			page = w.Biblio.FirstPage
		}
	}
	issued := w.PublicationDate
	if issued == "" && w.PublicationYear != 0 {
		issued = strconv.Itoa(w.PublicationYear)
	}
	workDOI := doi.Normalize(w.DOI)

	return &coldp.Reference{
		Citation:       "",
		Type:           coldp.NewReferenceType(mapType(w.Type, containerType)),
		Author:         authorField,
		Title:          title,
		ContainerTitle: container,
		Volume:         volume,
		Issue:          issue,
		Page:           page,
		Publisher:      publisher,
		Issued:         issued,
		ISSN:           strings.Join(issns, ", "),
		DOI:            workDOI,
		Link:           landing,
	}
}

// mapType translates OpenAlex's `type` vocabulary into sfga
// reference_type enum values. containerType disambiguates: "article"
// in a "journal" → ARTICLE_JOURNAL; "article" elsewhere → ARTICLE.
func mapType(oaType, containerType string) string {
	switch oaType {
	case "article":
		if containerType == "journal" {
			return "ARTICLE_JOURNAL"
		}
		return "ARTICLE"
	case "book-chapter":
		return "CHAPTER"
	case "book":
		return "BOOK"
	case "dissertation":
		return "THESIS"
	case "dataset":
		return "DATASET"
	case "report":
		return "REPORT"
	case "reference-entry":
		return "ENTRY"
	case "review":
		return "REVIEW"
	case "conference-paper", "proceedings-article":
		return "PAPER_CONFERENCE"
	case "monograph":
		return "BOOK"
	case "preprint":
		return "ARTICLE"
	case "editorial", "erratum", "letter", "paratext":
		return "ARTICLE_JOURNAL"
	}
	return ""
}
