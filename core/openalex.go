package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// OpenAlex client — resolves DOIs and does fuzzy title/author search
// against the OpenAlex Works API. Used by the future add-reference
// modal (see task #75) for the DOI and search tabs.
//
// PROTOTYPE — LIFT-TO-SFLIB CANDIDATE. Every SFBorg tool that touches
// references will want this. Kept self-contained here (no reach into
// hive-internal state beyond CurrentIdentity for the polite-pool
// email) so migration to sflib is a clean cut.
//
// Polite pool etiquette: OpenAlex asks clients to identify themselves
// via User-Agent or mailto=<email> param. Higher-priority pool for
// identified requests, anonymous pool for the rest. Hive threads the
// per-curator email from config (see core/config.go) rather than
// baking in a project-wide address — real accountability, not a shared
// contact.

// OpenAlexClient is a thin HTTP client for api.openalex.org.
// Concurrent-safe (net/http.Client is). One instance per process is
// plenty; the OpenAlex() constructor caches a singleton.
type OpenAlexClient struct {
	http    *http.Client
	baseURL string
	// email lands in User-Agent and mailto= query param for the polite
	// pool. Empty is legal (anonymous pool, slower).
	email string
}

var (
	openAlexInstance *OpenAlexClient
)

// OpenAlex returns a shared OpenAlexClient built from the current
// process identity's OpenAlexEmail (see core.CurrentIdentity, set by
// main.go after resolving flag > env > config). Cached — repeated
// calls return the same client. Change requires a process restart.
func OpenAlex() *OpenAlexClient {
	if openAlexInstance == nil {
		openAlexInstance = NewOpenAlex(CurrentIdentity().OpenAlexEmail)
	}
	return openAlexInstance
}

// NewOpenAlex constructs a client with an explicit email. Intended
// for tests / non-default callers; production code uses OpenAlex().
func NewOpenAlex(email string) *OpenAlexClient {
	return &OpenAlexClient{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL: "https://api.openalex.org",
		email:   email,
	}
}

// ErrOpenAlexNotFound is returned when OpenAlex has no work for the
// given DOI. Distinct from network failures so the calling UI can
// distinguish "DOI is bad" from "OpenAlex is down."
var ErrOpenAlexNotFound = errors.New("openalex: work not found")

// ResolveDOI fetches the OpenAlex Work record for a DOI and maps it
// to a coldp.Reference. The DOI may be a bare identifier
// ("10.1234/abcd") or a full URL ("https://doi.org/10.1234/abcd") —
// both work. Returns ErrOpenAlexNotFound on 404.
//
// The returned Reference has a hive-generated ID (UUID) — OpenAlex's
// own IDs (W...) aren't preserved into sfga; if a curator wants that
// linkage, they can put it in col__alternative_id later.
func (c *OpenAlexClient) ResolveDOI(ctx context.Context, doi string) (*coldp.Reference, error) {
	// OpenAlex accepts "doi:<x>" or the full URL form; normalize.
	doi = strings.TrimSpace(doi)
	doi = strings.TrimPrefix(doi, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")
	doi = strings.TrimPrefix(doi, "doi:")
	if doi == "" {
		return nil, fmt.Errorf("openalex: empty DOI")
	}
	path := "/works/doi:" + url.PathEscape(doi)
	var raw openAlexWork
	if err := c.get(ctx, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw.toReference(), nil
}

// Search runs OpenAlex's cross-field search (title / abstract / author)
// and returns up to `limit` results mapped to coldp.Reference. Search
// is fuzzy — OpenAlex handles typos and word ordering itself.
//
// Empty query returns nil (nothing) rather than "everything"; callers
// filtering by name+year should compose a query string themselves.
func (c *OpenAlexClient) Search(ctx context.Context, query string, limit int) ([]coldp.Reference, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200 // OpenAlex per_page cap
	}
	q := url.Values{
		"search":   {query},
		"per_page": {strconv.Itoa(limit)},
	}
	var raw openAlexWorksPage
	if err := c.get(ctx, "/works", q, &raw); err != nil {
		return nil, err
	}
	out := make([]coldp.Reference, 0, len(raw.Results))
	for _, w := range raw.Results {
		out = append(out, *w.toReference())
	}
	return out, nil
}

// get is the shared HTTP driver: sets User-Agent + mailto polite-pool
// params, dispatches the request, maps status to hive errors, and
// decodes JSON into `out`.
func (c *OpenAlexClient) get(ctx context.Context, path string, extra url.Values, out any) error {
	if extra == nil {
		extra = url.Values{}
	}
	if c.email != "" {
		// mailto param is OpenAlex's documented polite-pool signal;
		// the User-Agent header is best-practice etiquette. Set both.
		extra.Set("mailto", c.email)
	}
	full := c.baseURL + path
	if len(extra) > 0 {
		full += "?" + extra.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("openalex: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openalex: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("openalex: read %s: %w", path, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrOpenAlexNotFound
	default:
		return fmt.Errorf("openalex: %s: HTTP %d: %s",
			path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("openalex: parse %s: %w", path, err)
	}
	return nil
}

func (c *OpenAlexClient) userAgent() string {
	if c.email != "" {
		return "hive/0.1 (mailto:" + c.email + ")"
	}
	return "hive/0.1 (+https://github.com/sfborg/hive)"
}

// ---------- OpenAlex JSON shapes (partial) ----------
//
// Only the fields hive maps into coldp.Reference are decoded. The full
// Work schema is large; adding fields is additive — extra JSON keys
// are silently ignored by encoding/json.

type openAlexWorksPage struct {
	Results []openAlexWork `json:"results"`
}

type openAlexWork struct {
	ID              string             `json:"id"`
	DOI             string             `json:"doi"`
	Title           string             `json:"title"`
	DisplayName     string             `json:"display_name"`
	PublicationYear int                `json:"publication_year"`
	PublicationDate string             `json:"publication_date"`
	Type            string             `json:"type"`
	Authorships     []openAlexAuthor   `json:"authorships"`
	PrimaryLocation *openAlexLocation  `json:"primary_location"`
	Biblio          *openAlexBiblio    `json:"biblio"`
	Abstract        string             `json:"abstract"`
}

type openAlexAuthor struct {
	RawAuthorName string       `json:"raw_author_name"`
	Author        *openAlexRef `json:"author"`
}

type openAlexRef struct {
	DisplayName string `json:"display_name"`
	ORCID       string `json:"orcid"`
}

type openAlexLocation struct {
	Source          *openAlexSource `json:"source"`
	LandingPageURL  string          `json:"landing_page_url"`
}

type openAlexSource struct {
	DisplayName string   `json:"display_name"`
	Type        string   `json:"type"` // journal / repository / conference / book series
	ISSN        []string `json:"issn"`
	ISSN_L      string   `json:"issn_l"`
	Publisher   string   `json:"host_organization_name"`
}

type openAlexBiblio struct {
	Volume    string `json:"volume"`
	Issue     string `json:"issue"`
	FirstPage string `json:"first_page"`
	LastPage  string `json:"last_page"`
}

// toReference maps an OpenAlex Work into a coldp.Reference. Best-effort
// — missing fields on the OpenAlex side land as empty strings.
func (w openAlexWork) toReference() *coldp.Reference {
	// Title: OpenAlex often duplicates title into display_name; pick
	// whichever is non-empty.
	title := w.Title
	if title == "" {
		title = w.DisplayName
	}
	// Authors: OpenAlex ships an authorship list; hive collapses to
	// coldp's "Family, Given; Family, Given" convention. If OpenAlex
	// gives raw_author_name we prefer that (already formatted); fall
	// back to display_name.
	var authors []string
	for _, a := range w.Authorships {
		name := a.RawAuthorName
		if name == "" && a.Author != nil {
			name = a.Author.DisplayName
		}
		if name != "" {
			authors = append(authors, name)
		}
	}
	author := strings.Join(authors, "; ")
	// Container (journal / book) title + issn come from primary_location.
	var (
		container, containerType, publisher string
		issns                               []string
	)
	if w.PrimaryLocation != nil && w.PrimaryLocation.Source != nil {
		container = w.PrimaryLocation.Source.DisplayName
		containerType = w.PrimaryLocation.Source.Type
		publisher = w.PrimaryLocation.Source.Publisher
		issns = w.PrimaryLocation.Source.ISSN
	}
	var landing string
	if w.PrimaryLocation != nil {
		landing = w.PrimaryLocation.LandingPageURL
	}
	// Volume / issue / pages: OpenAlex ships first_page + last_page
	// separately; coldp packs them into col__page as "first–last".
	var (
		volume, issue, page string
	)
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
	// Issued: prefer full date; fall back to year.
	issued := w.PublicationDate
	if issued == "" && w.PublicationYear != 0 {
		issued = strconv.Itoa(w.PublicationYear)
	}
	// DOI: OpenAlex sends the full URL; sfga stores bare identifier.
	doi := strings.TrimPrefix(w.DOI, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")

	return &coldp.Reference{
		Citation:        "", // curator can compose; leave blank to trigger the col__author + col__title + col__issued fallback
		Type:            coldp.NewReferenceType(mapOpenAlexType(w.Type, containerType)),
		Author:          author,
		Title:           title,
		ContainerTitle:  container,
		Volume:          volume,
		Issue:           issue,
		Page:            page,
		Publisher:       publisher,
		Issued:          issued,
		ISSN:            strings.Join(issns, ", "),
		DOI:             doi,
		Link:            landing,
	}
}

// mapOpenAlexType translates OpenAlex's `type` vocabulary into the
// sfga reference_type enum values. OpenAlex uses "article",
// "book-chapter", "book", etc.; sfga uses "ARTICLE", "CHAPTER",
// "BOOK". containerType helps disambiguate ("article" in a "journal"
// → ARTICLE_JOURNAL; "article" in a "repository" → ARTICLE).
func mapOpenAlexType(oaType, containerType string) string {
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
		return "ARTICLE" // sfga doesn't have a distinct preprint type
	case "editorial", "erratum", "letter", "paratext":
		return "ARTICLE_JOURNAL"
	}
	// Unknown → leave empty so nothing gets written; curator can pick.
	return ""
}
