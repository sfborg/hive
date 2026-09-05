package refmanager

import (
	"github.com/sfborg/hive/pkg/hiveext"
	"github.com/sfborg/hive/pkg/refmanager/openalex"
)

// LookupConfig carries per-provider configuration for the
// refmanager-registered lookup providers. Fields grow as new
// providers land (CrossRefUserAgent, ChecklistBankToken, …). Zero-
// value fields mean "no config supplied" — providers that need one
// (e.g. OpenAlex's polite-pool email) still function, just with
// anonymous access.
type LookupConfig struct {
	// OpenAlexEmail is the "mailto=" identifier OpenAlex uses to
	// route requests to the polite pool. Recommended but not
	// required; unset falls back to anonymous quota.
	OpenAlexEmail string
}

// LookupProviders returns the refmanager-registered lookup providers,
// grouped by capability. Callers pass the result to hive.Open via
// hive.WithLookup so Archive.Lookup exposes the set to handlers.
//
// Today: OpenAlex provides both DOI resolution and free-text search.
// Adding CrossRef or ChecklistBank means constructing another client
// and appending to the appropriate slice(s); handlers do not change.
func LookupProviders(cfg LookupConfig) hiveext.LookupSet {
	oa := openalex.New(cfg.OpenAlexEmail)
	return hiveext.LookupSet{
		DOIResolvers:      []hiveext.DOIResolver{oa},
		FreeTextSearchers: []hiveext.FreeTextSearcher{oa},
	}
}
