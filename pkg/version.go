package hive

import "github.com/gnames/gnlib/ent/gnvers"

// Version and Build identify the binary. Release builds set them at
// link time with -ldflags "-X github.com/sfborg/hive/pkg.Version=<tag>
// -X github.com/sfborg/hive/pkg.Build=<commit>"; other builds use the
// defaults below. Update Version for each release.
var (
	Version = "v0.0.1"
	Build   = "n/a"
)

// GetVersion returns hive's version and build information.
func GetVersion() gnvers.Version {
	return gnvers.Version{Version: Version, Build: Build}
}
