// Package bhlnames is an HTTP client for the BHLnames service
// (bhlnames.globalnames.org/api/v1). It looks up Biodiversity Heritage
// Library references for a scientific name + authorship and projects
// each match into a coldp.Reference preview plus surface metadata
// (quality, score, page URL) that a UI can use to sort or annotate.
//
// LIFT-TO-SFLIB CANDIDATE. Every SFBorg tool that touches
// nomenclatural events benefits from this. Written self-contained (no
// reach into hive-internal state, no singleton) so migration to sflib
// is a clean copy.
//
// The main endpoint is POST /name_refs which takes a name (+ optional
// authorship / year / reference context) and returns a scored list of
// BHL references that likely contain or are the original description.
// Full schema at bhlnames.globalnames.org/apidoc/index.html.
package bhlnames

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// DefaultBaseURL is the production endpoint. Tests can pass an
// httptest URL to New instead.
const DefaultBaseURL = "https://bhlnames.globalnames.org/api/v1"

// Client is the HTTP client for BHLnames. Concurrent-safe.
type Client struct {
	http    *http.Client
	baseURL string
}

// New constructs a client against baseURL. Pass DefaultBaseURL for
// production; pass an httptest server URL for offline tests.
// BHLnames has no polite-pool concept — no email header needed.
func New(baseURL string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// Hit is one match from BHLnames, projected into a hive-friendly shape.
// Reference is a preview coldp.Reference ready to hand to CreateReference;
// Quality (1-5) and Score are surface metadata a UI can use to sort,
// filter, or annotate.
//
// Quality thresholds (from the BHLnames API doc):
//
//	1 — nothing found (usually filtered out by BHLnames itself)
//	2 — 15%   (Odds > 0.01)
//	3 — 50%   (Odds > 0.1)
//	4 — 80%   (Odds > 1)     ← reasonable "auto-suggest" threshold
//	5 — 98%   (Odds > 10)    ← very confident
type Hit struct {
	Reference   coldp.Reference
	Quality     int
	Score       int
	PageURL     string
	PageID      int
	MatchedName string
}

// LookupOpts controls what BHLnames returns. Zero-value means "sensible
// defaults" — pull up to 5 matches, don't try to identify the
// nomenclatural event separately. NomenEvent=true makes BHLnames try
// to isolate the original description specifically.
type LookupOpts struct {
	// RefsLimit — how many matches to return. Default 5 (client-side
	// cap when zero). BHLnames itself supports up to a few hundred.
	RefsLimit int
	// NomenEvent asks BHLnames to focus on identifying the original
	// nomenclatural act (protologue). Slower + more targeted. Useful
	// when the caller specifically wants "where was this name
	// established," less useful for "any BHL page containing this
	// name."
	NomenEvent bool
}

// LookupName runs the POST /name_refs query for a scientific name.
// canonical is the name without authorship ("Panthera leo"); authors
// is the authorship string ("Linnaeus"); year is the publication year
// or 0 for unknown. All three are optional per the BHLnames docs but
// scores improve dramatically when authorship + year are provided.
//
// Returns an empty slice (no error) when BHLnames finds no matches.
func (c *Client) LookupName(
	ctx context.Context, canonical, authors string, year int, opts LookupOpts,
) ([]Hit, error) {
	if strings.TrimSpace(canonical) == "" {
		return nil, fmt.Errorf("bhlnames: canonical name required")
	}
	if opts.RefsLimit <= 0 {
		opts.RefsLimit = 5
	}
	body := input{
		Name: inputName{
			Canonical: canonical,
			Authors:   authors,
			Year:      year,
		},
		Params: inputParams{
			RefsLimit:  opts.RefsLimit,
			NomenEvent: opts.NomenEvent,
			// Sort descending by year so the newest matches (usually the
			// ones a curator cares about) surface first. Original-
			// description hunting flips this via NomenEvent scoring which
			// promotes the earliest confident match anyway.
			SortDesc: true,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("bhlnames: marshal input: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/name_refs", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("bhlnames: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hive/0.1 (+https://github.com/sfborg/hive)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bhlnames: POST /name_refs: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bhlnames: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bhlnames: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var out refsByName
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("bhlnames: parse response: %w", err)
	}
	hits := make([]Hit, 0, len(out.References))
	for _, r := range out.References {
		hits = append(hits, r.toHit(authors))
	}
	return hits, nil
}

// ErrNoMatch is a sentinel for callers that want to treat an empty
// match list as an error; the client itself returns (nil, nil) for
// empty.
var ErrNoMatch = errors.New("bhlnames: no matching references")

// ---------- Wire types (subset of BHLnames API) ----------

type input struct {
	Name inputName `json:"name"`
	// Params is always set by LookupName (RefsLimit defaults to 5), so
	// no omitempty needed — the whole struct always serializes.
	Params inputParams `json:"params"`
}

type inputName struct {
	Canonical string `json:"canonical,omitempty"`
	Authors   string `json:"authors,omitempty"`
	Year      int    `json:"year,omitempty"`
}

type inputParams struct {
	RefsLimit  int  `json:"refsLimit,omitempty"`
	NomenEvent bool `json:"nomenEvent,omitempty"`
	SortDesc   bool `json:"sortDesc,omitempty"`
}

type refsByName struct {
	References []referenceName `json:"references"`
}

type referenceName struct {
	Name            nameData      `json:"name"`
	RefMatchQuality int           `json:"refMatchQuality"`
	Reference       referenceData `json:"reference"`
	Score           scoreData     `json:"score"`
}

type nameData struct {
	MatchName string `json:"matchName"`
	Name      string `json:"name"`
}

type scoreData struct {
	Total int `json:"total"`
}

type referenceData struct {
	DoiTitle       string    `json:"doiTitle"`
	ItemID         int       `json:"itemId"`
	PageID         int       `json:"pageId"`
	PageNum        int       `json:"pageNum"`
	Part           *partData `json:"part"`
	TitleID        int       `json:"titleId"`
	TitleName      string    `json:"titleName"`
	TitleYearStart int       `json:"titleYearStart"`
	TitleYearEnd   int       `json:"titleYearEnd"`
	URL            string    `json:"url"`
	Volume         string    `json:"volume"`
	YearAggr       int       `json:"yearAggr"`
	YearType       string    `json:"yearType"`
}

type partData struct {
	DOI   string `json:"doi"`
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Pages string `json:"pages"`
	Year  int    `json:"year"`
}

// toHit projects a single BHLnames match into hive's Hit shape.
// author (from the caller's input) is passed through since BHLnames
// doesn't echo it on the reference side — the input authorship is
// what belongs on the coldp.Reference anyway.
func (r referenceName) toHit(author string) Hit {
	ref := coldp.Reference{
		Author: author,
	}
	// Title / container / page depend on whether BHLnames identified a
	// specific "part" (a paper within a book/journal) vs. just the
	// containing item.
	if r.Reference.Part != nil && r.Reference.Part.Name != "" {
		ref.Title = r.Reference.Part.Name
		ref.ContainerTitle = r.Reference.TitleName
		ref.Page = r.Reference.Part.Pages
		if r.Reference.Part.DOI != "" {
			ref.DOI = r.Reference.Part.DOI
		}
		ref.Type = coldp.NewReferenceType("ARTICLE_JOURNAL")
	} else {
		// No specific part → treat the whole item (book / journal
		// volume) as the reference. Curator can refine later.
		ref.Title = r.Reference.TitleName
		ref.Type = coldp.NewReferenceType("BOOK")
		if r.Reference.PageNum > 0 {
			ref.Page = strconv.Itoa(r.Reference.PageNum)
		}
	}
	// DOI: prefer the specific part's DOI; fall back to the title's
	// (book / journal) DOI.
	if ref.DOI == "" && r.Reference.DoiTitle != "" {
		ref.DOI = r.Reference.DoiTitle
	}
	ref.Volume = r.Reference.Volume
	// Issued: prefer the part year, then the aggregated year for the
	// reference. YearAggr is BHLnames's best-effort answer.
	switch {
	case r.Reference.Part != nil && r.Reference.Part.Year != 0:
		ref.Issued = strconv.Itoa(r.Reference.Part.Year)
	case r.Reference.YearAggr != 0:
		ref.Issued = strconv.Itoa(r.Reference.YearAggr)
	}
	// Link — BHL page URL. Always useful even when no DOI (BHL scans
	// are the primary artifact for older literature).
	ref.Link = r.Reference.URL

	return Hit{
		Reference:   ref,
		Quality:     r.RefMatchQuality,
		Score:       r.Score.Total,
		PageURL:     r.Reference.URL,
		PageID:      r.Reference.PageID,
		MatchedName: r.Name.MatchName,
	}
}
