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
	"strconv"
	"strings"
	"time"

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

// ErrNotFound is returned when OpenAlex has no work for the given DOI.
// Distinct from network failures so calling code can tell "DOI is bad"
// from "OpenAlex is down."
var ErrNotFound = errors.New("openalex: work not found")

// ResolveDOI fetches the OpenAlex Work record for a DOI and maps it to
// a coldp.Reference. The DOI may be a bare identifier
// ("10.1234/abcd") or a full URL ("https://doi.org/10.1234/abcd").
// Returns ErrNotFound on 404.
//
// The returned Reference carries no ID; the caller assigns one before
// persisting. OpenAlex's own W-IDs aren't preserved into sfga; a
// curator wanting that linkage can add it to col__alternative_id.
func (c *Client) ResolveDOI(ctx context.Context, doi string) (*coldp.Reference, error) {
	doi = strings.TrimSpace(doi)
	doi = strings.TrimPrefix(doi, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")
	doi = strings.TrimPrefix(doi, "doi:")
	if doi == "" {
		return nil, fmt.Errorf("openalex: empty DOI")
	}
	path := "/works/doi:" + url.PathEscape(doi)
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

func (w work) toReference() *coldp.Reference {
	title := w.Title
	if title == "" {
		title = w.DisplayName
	}
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
	authorField := strings.Join(authors, "; ")
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
	doi := strings.TrimPrefix(w.DOI, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")

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
		DOI:            doi,
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
