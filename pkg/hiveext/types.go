// Package hiveext defines the shared shapes hive's extension points
// pass around at wire-up time. It is a leaf package: it depends only
// on the standard library and gsvalidator, never on pkg/ root,
// pkg/refmanager/, or pkg/taxonomy/. This is what breaks the import
// cycle between the pkg root (which composes the validator registry)
// and the extension packages (which provide validators depending on
// state the pkg root owns).
//
// The rule of thumb: any type that both pkg/ root and an extension
// package need to reference lives here. Any type that only one side
// touches stays in that side's package.
package hiveext

import (
	"database/sql"
)

// ValidatorConfig carries the ambient state a hive extension package
// needs to construct its validators. It is populated once, at
// Archive-open time, and passed by value to each extension's
// Validators(cfg) constructor.
//
// New fields go here rather than becoming a per-extension parameter
// because a single struct is what makes wire-up read as plain Go
// composition — no bag-of-init()-side-effects, no context.Value fishing.
type ValidatorConfig struct {
	// DB is the archive's read/write handle. Validators that need
	// cross-table queries (e.g. count-across-tables checks) borrow it
	// via gsvalidator's ValidationContext, but the config also carries
	// it for validators that build their own prepared statements at
	// construction time.
	DB *sql.DB

	// ArchivePath is the on-disk path to the sqlite archive. Validators
	// that touch sibling files (e.g. the .pdf/ sidecar tree next to
	// the archive) resolve those paths relative to this.
	ArchivePath string

	// ReferenceFKSites lists every {table, column} pair whose column
	// FKs into reference(col__id). Populated by pkg/taxonomy (the
	// package that knows which taxonomy aggregates cite references).
	// The reference manager's uncited-reference validator walks this
	// list to answer "does any row cite this reference?"
	ReferenceFKSites []ReferenceFKSite
}

// ReferenceFKSite names one column that FKs into reference(col__id).
// The list is small (single-digit count today) and change-controlled —
// adding a citation site means teaching hive that a new aggregate can
// hold reference IDs, and both the reference-delete guard and the
// uncited-reference validator need to know.
type ReferenceFKSite struct {
	Table  string
	Column string
}
