// Package schema exposes hive's embedded XML schemas — currently
// the hive-jats overlay plus its vendored NCBI JATS Publishing 1.3
// base. Consumers extract the tree to disk with WriteTo so that
// external validators (xmllint) can resolve the RelaxNG
// <include href="…"> chain across the ~33 module files.
package schema

import "embed"

// FS holds every .rng file in this directory tree at compile time.
// The embed pattern intentionally covers both the overlay
// (hive-jats.rng at the root) and every module under vendor/ so
// xmllint's include resolution never hits a missing file.
//
//go:embed *.rng vendor/*.rng
var FS embed.FS
