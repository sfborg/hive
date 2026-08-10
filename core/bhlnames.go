package core

import "github.com/sfborg/hive/pkg/bhlnames"

// Type aliases so existing callers of core.BHLnameHit /
// core.BHLnameLookupOpts keep compiling without an import change.
type (
	BHLnameHit        = bhlnames.Hit
	BHLnameLookupOpts = bhlnames.LookupOpts
)

// ErrBHLnamesNoMatch is re-exported so callers relying on the sentinel
// keep working.
var ErrBHLnamesNoMatch = bhlnames.ErrNoMatch

var bhlnamesInstance *bhlnames.Client

// BHLnames returns the shared process client, pointed at the
// production BHLnames endpoint. Cached across calls; construct a fresh
// client in tests via bhlnames.New(httptestURL).
func BHLnames() *bhlnames.Client {
	if bhlnamesInstance == nil {
		bhlnamesInstance = bhlnames.New(bhlnames.DefaultBaseURL)
	}
	return bhlnamesInstance
}
