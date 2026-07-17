package core

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

// BHLnames client — looks up Biodiversity Heritage Library references
// for a scientific name + authorship. Used on the name edit form to
// suggest the original species description ("protologue") when hive
// only has the name and no reference yet. See task #75 for the UX and
// the wider add-reference modal.
//
// PROTOTYPE — LIFT-TO-SFLIB CANDIDATE. Every SFBorg tool that touches
// nomenclatural events benefits from this. Written self-contained so
// migration to sflib is a clean cut.
//
// BHLnames API is at bhlnames.globalnames.org/api/v1. The main
// endpoint is POST /name_refs which takes a name (+ optional
// authorship / year / reference context) and returns a scored list of
// BHL references that likely contain / are the original description.
// Full schema at bhlnames.globalnames.org/apidoc/index.html.

// BHLnamesClient is the HTTP client for BHLnames. Concurrent-safe.
// One instance per process via BHLnames(); tests can New a fresh one.
type BHLnamesClient struct {
	http    *http.Client
	baseURL string
}

var bhlnamesInstance *BHLnamesClient

// BHLnames returns the shared process client.
func BHLnames() *BHLnamesClient {
	if bhlnamesInstance == nil {
		bhlnamesInstance = NewBHLnames("https://bhlnames.globalnames.org/api/v1")
	}
	return bhlnamesInstance
}

// NewBHLnames constructs a client against baseURL (test hook — pass
// an httptest server URL for offline tests, the production URL
// otherwise). BHLnames has no polite-pool concept — no email header
// needed.
func NewBHLnames(baseURL string) *BHLnamesClient {
	return &BHLnamesClient{
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// BHLnameHit is one match from BHLnames, projected into hive-friendly
// shape. Reference is a preview coldp.Reference ready to hand to
// Tx.CreateReference; Quality (1-5) and Score are surface metadata
// the UI can use to sort / filter / annotate.
//
// Quality thresholds (from the BHLnames API doc):
//
//	1 — nothing found (usually filtered out by BHLnames itself)
//	2 — 15%   (Odds > 0.01)
//	3 — 50%   (Odds > 0.1)
//	4 — 80%   (Odds > 1)     ← reasonable "auto-suggest" threshold
//	5 — 98%   (Odds > 10)    ← very confident, feel free to prompt
//
// Hive uses Quality >= 4 as the "confident enough to surface without
// the curator asking" threshold. Lower-quality matches are still
// available on-demand.
type BHLnameHit struct {
	Reference   coldp.Reference
	Quality     int
	Score       int
	PageURL     string
	PageID      int
	MatchedName string
}

// BHLnameLookupOpts controls what BHLnames returns. Zero-value means
// "sensible defaults" — pull up to 5 matches, don't try to identify
// the nomenclatural event separately (which is slower + noisier for
// most callers). NomenEvent=true makes BHLnames try to isolate the
// original description specifically.
type BHLnameLookupOpts struct {
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
func (c *BHLnamesClient) LookupName(
	ctx context.Context, canonical, authors string, year int, opts BHLnameLookupOpts,
) ([]BHLnameHit, error) {
	if strings.TrimSpace(canonical) == "" {
		return nil, fmt.Errorf("bhlnames: canonical name required")
	}
	if opts.RefsLimit <= 0 {
		opts.RefsLimit = 5
	}
	body := bhlnamesInput{
		Name: bhlnamesInputName{
			Canonical: canonical,
			Authors:   authors,
			Year:      year,
		},
		Params: bhlnamesInputParams{
			RefsLimit:  opts.RefsLimit,
			NomenEvent: opts.NomenEvent,
			// Sort descending by year so the newest matches (usually
			// the ones a curator cares about) surface first. Original-
			// description hunting flips this via NomenEvent scoring
			// which promotes the earliest confident match anyway.
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
	var out bhlnamesRefsByName
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("bhlnames: parse response: %w", err)
	}
	hits := make([]BHLnameHit, 0, len(out.References))
	for _, r := range out.References {
		hits = append(hits, r.toHit(authors))
	}
	return hits, nil
}

// ErrBHLnamesNoMatch is returned by callers that treat an empty match
// list as an error; the client itself returns (nil, nil) for empty.
// Provided as a sentinel so higher layers can decide independently.
var ErrBHLnamesNoMatch = errors.New("bhlnames: no matching references")

// ---------- Wire types (subset of BHLnames API) ----------

type bhlnamesInput struct {
	Name bhlnamesInputName `json:"name"`
	// Params is always set by LookupName (RefsLimit defaults to 5), so
	// no omitempty needed — the whole struct always serializes.
	Params bhlnamesInputParams `json:"params"`
}

type bhlnamesInputName struct {
	Canonical string `json:"canonical,omitempty"`
	Authors   string `json:"authors,omitempty"`
	Year      int    `json:"year,omitempty"`
}

type bhlnamesInputParams struct {
	RefsLimit  int  `json:"refsLimit,omitempty"`
	NomenEvent bool `json:"nomenEvent,omitempty"`
	SortDesc   bool `json:"sortDesc,omitempty"`
}

type bhlnamesRefsByName struct {
	References []bhlnamesReferenceName `json:"references"`
}

type bhlnamesReferenceName struct {
	Name            bhlnamesNameData  `json:"name"`
	RefMatchQuality int               `json:"refMatchQuality"`
	Reference       bhlnamesReference `json:"reference"`
	Score           bhlnamesScore     `json:"score"`
}

type bhlnamesNameData struct {
	MatchName string `json:"matchName"`
	Name      string `json:"name"`
}

type bhlnamesScore struct {
	Total int `json:"total"`
}

type bhlnamesReference struct {
	DoiTitle       string        `json:"doiTitle"`
	ItemID         int           `json:"itemId"`
	PageID         int           `json:"pageId"`
	PageNum        int           `json:"pageNum"`
	Part           *bhlnamesPart `json:"part"`
	TitleID        int           `json:"titleId"`
	TitleName      string        `json:"titleName"`
	TitleYearStart int           `json:"titleYearStart"`
	TitleYearEnd   int           `json:"titleYearEnd"`
	URL            string        `json:"url"`
	Volume         string        `json:"volume"`
	YearAggr       int           `json:"yearAggr"`
	YearType       string        `json:"yearType"`
}

type bhlnamesPart struct {
	DOI   string `json:"doi"`
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Pages string `json:"pages"`
	Year  int    `json:"year"`
}

// toHit projects a single BHLnames match into hive's BHLnameHit shape.
// author (from the caller's input) is passed through since BHLnames
// doesn't echo it on the reference side — the input authorship is
// what belongs on the coldp.Reference anyway.
func (r bhlnamesReferenceName) toHit(author string) BHLnameHit {
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
	// Volume comes from the item metadata directly.
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

	return BHLnameHit{
		Reference:   ref,
		Quality:     r.RefMatchQuality,
		Score:       r.Score.Total,
		PageURL:     r.Reference.URL,
		PageID:      r.Reference.PageID,
		MatchedName: r.Name.MatchName,
	}
}
