package hive

import "github.com/gnames/gnlib/ent/gnvers"

// Version is set at build time via -ldflags "-X ...Version=vX.Y.Z" or
// kept in sync manually until a release script lands. Build carries
// the commit SHA / date the same way.
var (
	Version = "v0.1.0"
	Build   = "n/a"
)

// GetVersion returns hive's version and build information.
func GetVersion() gnvers.Version {
	return gnvers.Version{Version: Version, Build: Build}
}
