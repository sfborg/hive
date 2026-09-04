// Package taxonomy owns the taxonomy-specific extension surface hive
// exposes to its shared core. It provides:
//
//   - ReferenceFKSites, the list of taxonomy tables whose columns FK
//     into reference(col__id). The pkg root uses this list to guard
//     DeleteReference; the reference manager's uncited-reference
//     validator uses it to answer "does anything cite this reference?"
//   - Validators (see validators.go), the taxonomy-specific rules
//     wired into the shared gsvalidator registry.
//
// This package imports only pkg/hiveext (shared shapes) and external
// deps — never pkg/ root. That is what lets it compose with the pkg
// root without an import cycle.
package taxonomy

import (
	"github.com/sfborg/hive/pkg/hiveext"
)

// ReferenceFKSites returns every {table, column} pair in the taxonomy
// aggregate set whose column FKs into reference(col__id). Ordering is
// significant to callers only in that the reference-delete guard
// reports the first-matching table in its error message; keep name
// first (the most common citation site) so the error stays actionable.
//
// Adding a new citation site means teaching hive that a new taxonomy
// aggregate can hold reference IDs — both the delete guard and the
// uncited-reference validator pick up the change automatically.
func ReferenceFKSites() []hiveext.ReferenceFKSite {
	return []hiveext.ReferenceFKSite{
		// name.col__reference_id is the primary citation path (a name's
		// publication reference).
		{Table: "name", Column: "col__reference_id"},
		{Table: "taxon", Column: "col__according_to_id"},
		{Table: "synonym", Column: "col__according_to_id"},
		{Table: "vernacular", Column: "col__reference_id"},
		{Table: "name_relation", Column: "col__reference_id"},
		{Table: "type_material", Column: "col__reference_id"},
		{Table: "distribution", Column: "col__reference_id"},
		{Table: "species_estimate", Column: "col__reference_id"},
		{Table: "taxon_property", Column: "col__reference_id"},
		{Table: "species_interaction", Column: "col__reference_id"},
		{Table: "taxon_concept_relation", Column: "col__reference_id"},
	}
}
