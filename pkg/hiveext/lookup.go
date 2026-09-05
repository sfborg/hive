package hiveext

import (
	"context"

	"github.com/sfborg/sflib/pkg/coldp"
)

// DOIResolver looks up a reference by DOI. Implementations today:
// openalex. Planned additions: crossref, checklistbank. All return
// a *coldp.Reference on success and a package-specific "not found"
// error the caller can errors.Is-check.
type DOIResolver interface {
	// Name identifies the provider for logging + wire responses
	// ("openalex", "crossref", "checklistbank"). Lower-case; used as
	// a URL-slug and JSON key.
	Name() string
	ResolveDOI(ctx context.Context, doi string) (*coldp.Reference, error)
}

// FreeTextSearcher finds references matching a free-text query
// (author, title, citation words). Implementations today: openalex.
// Planned additions: crossref, checklistbank. Returned slice is
// ordered by provider-assigned relevance; callers may re-rank.
type FreeTextSearcher interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]coldp.Reference, error)
}

// LookupSet groups every registered provider by capability. Built at
// Archive-open time from refmanager.LookupProviders(cfg) so the pkg
// root doesn't have to know which providers ship. Handlers reach it
// via Archive.Lookup.
//
// Zero-value LookupSet is valid — DOIResolver() and FreeTextSearcher()
// return nil, and handlers can short-circuit with a 501 or a "no
// provider configured" error.
type LookupSet struct {
	DOIResolvers      []DOIResolver
	FreeTextSearchers []FreeTextSearcher
}

// DOIResolver returns the primary registered DOI resolver, or nil if
// none is registered. Callers wanting fan-out semantics iterate
// DOIResolvers directly.
func (s LookupSet) DOIResolver() DOIResolver {
	if len(s.DOIResolvers) == 0 {
		return nil
	}
	return s.DOIResolvers[0]
}

// FreeTextSearcher returns the primary registered free-text
// searcher, or nil if none is registered.
func (s LookupSet) FreeTextSearcher() FreeTextSearcher {
	if len(s.FreeTextSearchers) == 0 {
		return nil
	}
	return s.FreeTextSearchers[0]
}
