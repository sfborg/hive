package orcid

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Hit is one match from expanded-search. Institution carries the top
// affiliation ORCID returned (may be empty); a picker UI displays it
// alongside the name to disambiguate people who share names.
type Hit struct {
	ORCID       string
	GivenNames  string
	FamilyName  string
	CreditName  string
	Institution string
}

// SearchOptions controls what [Client.Search] returns. Zero-value is
// legal — Rows defaults to 20, Start to 0.
type SearchOptions struct {
	// Rows is the requested page size. Clamped to [1, 200]; zero uses
	// the default (20). ORCID currently caps expanded-search at 200
	// rows per request.
	Rows int
	// Start is the zero-based offset into the result set for
	// pagination. Combine with Rows to walk larger result sets.
	Start int
}

// Search runs the ORCID expanded-search against the free-text query and
// returns matching hits. The query grammar is Solr-style — fielded
// terms like "family-name:Smith AND given-names:Jane" are legal; an
// unqualified string matches across name, keyword, and email fields.
//
// Empty query returns (nil, nil) rather than "everyone." Passing a
// nil opts is equivalent to a zero-valued struct.
//
// For untrusted input inside a query term, wrap the term with
// [SanitizeSearchTerm] to strip Solr operator characters.
func (c *Client) Search(
	ctx context.Context, query string, opts *SearchOptions,
) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if opts == nil {
		opts = &SearchOptions{}
	}
	rows := opts.Rows
	if rows <= 0 {
		rows = 20
	}
	if rows > 200 {
		rows = 200
	}
	q := url.Values{
		"q":    {query},
		"rows": {strconv.Itoa(rows)},
	}
	if opts.Start > 0 {
		q.Set("start", strconv.Itoa(opts.Start))
	}
	var raw expandedSearch
	if err := c.get(ctx, "/expanded-search/", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(raw.Results))
	for _, r := range raw.Results {
		var inst string
		if len(r.InstitutionName) > 0 {
			inst = r.InstitutionName[0]
		}
		out = append(out, Hit{
			ORCID:       r.ORCIDID,
			GivenNames:  r.GivenNames,
			FamilyName:  r.FamilyNames,
			CreditName:  r.CreditName,
			Institution: inst,
		})
	}
	return out, nil
}

// expandedSearch matches GET /v3.0/expanded-search/?q=...
// Only the fields hive maps are decoded.
type expandedSearch struct {
	Results  []expandedResult `json:"expanded-result"`
	NumFound int              `json:"num-found"`
}

type expandedResult struct {
	ORCIDID         string   `json:"orcid-id"`
	GivenNames      string   `json:"given-names"`
	FamilyNames     string   `json:"family-names"`
	CreditName      string   `json:"credit-name"`
	InstitutionName []string `json:"institution-name"`
}
