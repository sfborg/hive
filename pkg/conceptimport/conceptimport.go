// Package conceptimport writes taxonomic-concept fields into a
// CoLDP-shaped archive. Given a MentionConcept (the compose-shaped
// subset of a name_usage that carries a sensu qualifier, a
// concept-reference author, or a nomenclatural note), the package
// derives the implied CoLDP taxon_concept_rel_type relation and
// persists the concept fields onto taxon / synonym rows.
//
// LIFT-TO-SFLIB candidate. The package is deliberately narrow-
// interfaced and does not import github.com/sfborg/hive/pkg —
// callers pass a Writer implementation. When TaxonWorks integration
// arrives (via TCS-shaped input) it will consume the same Writer
// surface, so lifting into sflib is a `git mv` + import-path update.
//
// Scope. This package handles:
//
//   - Pure derivation of the implied CoLDP concept relation from a
//     MentionConcept. See [Derive].
//   - Writing name_phrase / according_to_id onto taxon and synonym
//     rows via the [Writer] interface.
//   - Inserting taxon_concept_relation rows for materialized
//     concept relations.
//
// This package does NOT:
//
//   - Create new taxon or synonym rows. That's the curator's
//     assimilate-flow decision, handled elsewhere; this package
//     enriches existing rows.
//   - Resolve compose's author-year strings to reference row IDs.
//     Callers do that lookup before calling; the AccordingToRefID
//     field on ConceptRelation is a resolved id, not a string.
//   - Silently overwrite curator-set values. Write* functions
//     accept an OverwriteExisting flag; default (false) leaves
//     existing values alone.
package conceptimport

import (
	"errors"
	"strings"
)

// CoLDP taxon_concept_rel_type.col__id values. Constants rather
// than raw strings so callers get compile-time protection against
// typos, and so the mapping table has one place to update if CoLDP
// ever renames.
const (
	RelEquals     = "EQUALS"      // ==  (isCongruentWith)
	RelIncludes   = "INCLUDES"    // >   (includes)
	RelIncludedIn = "INCLUDED_IN" // <   (isIncludedIn)
	RelOverlaps   = "OVERLAPS"    // ><  (partiallyOverlaps)
	RelExcludes   = "EXCLUDES"    // |   (isDisjointFrom)
)

// Recognized nomenclaturalNote values that compose_v4 emits.
// Kept as string constants for symmetry with the relation IDs.
const (
	NoteMisapplication   = "misapplication"
	NotePrimaryHomonym   = "primary homonym"
	NoteAmbiguousSynonym = "ambiguous synonym"
)

// MentionConcept is the compose-shaped subset of a name_usage that
// concept-import needs. Format-neutral: compose_v4 output is one
// producer; a future TCS2 JSON-LD importer is another. The
// consumer of this package extracts these fields from whatever
// source-shape it works with and calls [Derive] or the Write*
// functions.
type MentionConcept struct {
	// NamePhrase carries the sensu-family qualifier stripped off
	// scientificName during compose. One of "sensu lato",
	// "sensu stricto", "sensu <author>", "auct.",
	// "auct. non <author>", "pro parte". May be empty.
	NamePhrase string

	// AccordingTo carries the concept-reference author-year cited
	// by the sensu marker. Present for "sensu <author>" and
	// "auct. non <author>"; empty for bare sensu lato / stricto /
	// auct. / pro parte.
	AccordingTo string

	// NomenclaturalNote carries misapplication / primary-homonym /
	// ambiguous-synonym markers. Empty for the common cases.
	NomenclaturalNote string
}

// DerivedRelation is the CoLDP concept relation implied by a
// MentionConcept. Pure function output; no side effects.
type DerivedRelation struct {
	// RelTypeID is the CoLDP taxon_concept_rel_type.col__id value,
	// e.g. "EQUALS", "INCLUDES", "INCLUDED_IN", "OVERLAPS",
	// "EXCLUDES". Empty when the mention doesn't imply a single
	// clean relation (e.g. bare "auct.", or "pro parte" without
	// additional context).
	RelTypeID string

	// AgainstNamedAuthor is true when the relation is to the
	// concept of a specifically cited author (from "sensu <author>"
	// or "auct. non <author>"). False when the reference concept
	// is implicit (sensu lato / stricto without a cited author) —
	// in that case the caller needs to identify the implicit
	// reference concept (usually the type-restricted form of the
	// same name) before inserting a taxon_concept_relation row.
	AgainstNamedAuthor bool
}

// HasRelation reports whether a relation was derived.
func (d DerivedRelation) HasRelation() bool { return d.RelTypeID != "" }

// Derive computes the CoLDP concept relation implied by a
// MentionConcept. Pure function.
//
// Mapping table (matches compose_v4.md § Handling sensu markers):
//
//	NamePhrase                    | Nomenclatural note   | RelTypeID   | AgainstNamedAuthor
//	------------------------------+----------------------+-------------+-------------------
//	sensu lato                    | (any)                | INCLUDES    | false
//	sensu stricto                 | (any)                | INCLUDED_IN | false
//	sensu <author>                | (any)                | EQUALS      | true
//	auct.                         | (any)                | ""          | false
//	auct. non <author>            | misapplication       | EXCLUDES    | true
//	auct. non <author>            | (any other or none)  | EXCLUDES    | true
//	pro parte                     | (any)                | ""          | false
//	(any other / empty NamePhrase)| misapplication       | EXCLUDES    | (see AccordingTo)
//	(empty NamePhrase, empty note)| ""                   | ""          | false
//
// The final row handles standalone nomenclaturalNote =
// "misapplication" with no sensu marker: still yields EXCLUDES, and
// AgainstNamedAuthor is true iff AccordingTo is non-empty. This is
// defensive — some future prompt or curator flow may emit the note
// without the auct. non marker.
func Derive(mc MentionConcept) DerivedRelation {
	phrase := strings.TrimSpace(mc.NamePhrase)
	// Lowercase for case-insensitive matching of the leading
	// keyword; the original NamePhrase is preserved on the row.
	lower := strings.ToLower(phrase)

	switch {
	case lower == "sensu lato":
		return DerivedRelation{RelTypeID: RelIncludes, AgainstNamedAuthor: false}
	case lower == "sensu stricto":
		return DerivedRelation{RelTypeID: RelIncludedIn, AgainstNamedAuthor: false}
	case strings.HasPrefix(lower, "sensu ") && !strings.HasPrefix(lower, "sensu auct"):
		// "sensu <author>" or "sensu <author, year>" — everything
		// after the sensu keyword that isn't the reserved
		// "sensu auct." pattern.
		return DerivedRelation{RelTypeID: RelEquals, AgainstNamedAuthor: true}
	case lower == "auct." || lower == "auct":
		return DerivedRelation{RelTypeID: "", AgainstNamedAuthor: false}
	case strings.HasPrefix(lower, "auct. non ") || strings.HasPrefix(lower, "auct non "):
		return DerivedRelation{RelTypeID: RelExcludes, AgainstNamedAuthor: true}
	case lower == "sensu auct." || lower == "sensu auct":
		return DerivedRelation{RelTypeID: "", AgainstNamedAuthor: false}
	case lower == "pro parte" || lower == "p.p." || lower == "pro p.":
		return DerivedRelation{RelTypeID: "", AgainstNamedAuthor: false}
	}

	// No sensu marker matched. Fall back to nomenclaturalNote-only
	// signals — currently just misapplication.
	if mc.NomenclaturalNote == NoteMisapplication {
		return DerivedRelation{
			RelTypeID:          RelExcludes,
			AgainstNamedAuthor: mc.AccordingTo != "",
		}
	}

	return DerivedRelation{}
}

// Writer is the narrow persistence surface [Write*] and
// [InsertConceptRelation] need. Callers implement this on their
// transaction type; the interface stays small and format-agnostic
// so lifting the package into sflib is a mechanical import-path
// update.
//
// All Writer methods run within a single caller-owned transaction —
// this package does not open transactions or manage commit
// boundaries. The Writer implementation is responsible for
// stamping col__modified / col__modified_by; conceptimport does
// not pass an actor because the calling transaction already owns
// one.
type Writer interface {
	// SetTaxonNamePhrase writes taxon.col__name_phrase for the
	// given taxon id. Implementations should preserve
	// existing values unless overwrite is true.
	SetTaxonNamePhrase(taxonID, namePhrase string, overwrite bool) error

	// SetTaxonAccordingTo writes taxon.col__according_to_id for the
	// given taxon id. Empty referenceID clears the field only when
	// overwrite is true; otherwise no-op on empty referenceID.
	SetTaxonAccordingTo(taxonID, referenceID string, overwrite bool) error

	// SetSynonymNamePhrase writes synonym.col__name_phrase for the
	// synonym identified by (col__id, col__taxon_id). Pro-parte
	// synonyms have multiple rows sharing col__id, and the
	// namePhrase applies to a specific (synonym, taxon) pair, so
	// the taxonID is required in addition to the synonymID.
	SetSynonymNamePhrase(synonymID, taxonID, namePhrase string, overwrite bool) error

	// SetSynonymAccordingTo writes synonym.col__according_to_id for
	// (col__id, col__taxon_id). Same overwrite semantics as
	// SetTaxonAccordingTo.
	SetSynonymAccordingTo(synonymID, taxonID, referenceID string, overwrite bool) error

	// InsertConceptRelation adds a row to taxon_concept_relation.
	// Idempotency (whether a duplicate row on the same triple is
	// rejected or accepted) is left to the implementation — the
	// CoLDP schema doesn't declare uniqueness on this table, so
	// implementations often silently accept duplicates.
	InsertConceptRelation(rel ConceptRelation) error
}

// ConceptRelation is a materialized taxon_concept_relation row.
type ConceptRelation struct {
	// TaxonID is the subject of the relation (the taxon this row
	// concerns). Required.
	TaxonID string

	// RelatedTaxonID is the object of the relation (the other
	// taxon this row asserts a concept relationship to). Required.
	RelatedTaxonID string

	// RelTypeID is a CoLDP taxon_concept_rel_type.col__id — one of
	// the constants at the top of this package (RelEquals etc.) or
	// any other value the target vocab table accepts.
	RelTypeID string

	// ReferenceID identifies the source (paper) that authored the
	// concept relation. Optional but strongly recommended for
	// provenance. Resolves to reference.col__id.
	ReferenceID string

	// Remarks is free-text curator or importer commentary.
	Remarks string
}

// Validate reports invalid ConceptRelation shapes before write.
func (r ConceptRelation) Validate() error {
	if r.TaxonID == "" {
		return errors.New("conceptimport: ConceptRelation.TaxonID is required")
	}
	if r.RelatedTaxonID == "" {
		return errors.New("conceptimport: ConceptRelation.RelatedTaxonID is required")
	}
	if r.TaxonID == r.RelatedTaxonID {
		return errors.New("conceptimport: ConceptRelation cannot relate a taxon to itself")
	}
	if r.RelTypeID == "" {
		return errors.New("conceptimport: ConceptRelation.RelTypeID is required")
	}
	return nil
}

// ImplicitReferenceOutcome describes the resolution outcome for a
// bare sensu-lato / sensu-stricto assertion whose reference concept
// is implicit (not a cited author). The caller runs the archive
// lookup and reports what it found; this package doesn't touch the
// DB from the pure layer.
type ImplicitReferenceOutcome int

const (
	// ImplicitReferenceNone means the phrase doesn't imply an
	// implicit reference concept (sensu <author>, auct. non
	// <author>, and everything else land here — the reference is
	// explicit, or there is no relation to record).
	ImplicitReferenceNone ImplicitReferenceOutcome = iota

	// ImplicitReferenceMissing means the phrase does imply an
	// implicit reference (bare sensu lato / stricto) but no
	// candidate taxon exists in the archive to relate to. The
	// caller should populate namePhrase on the subject taxon and
	// skip inserting a taxon_concept_relation row.
	ImplicitReferenceMissing

	// ImplicitReferenceResolved means exactly one candidate taxon
	// was found. The caller should insert a taxon_concept_relation
	// row against it.
	ImplicitReferenceResolved

	// ImplicitReferenceAmbiguous means multiple candidate taxa
	// were found. The caller should skip inserting a relation and
	// log for curator review — this package won't guess which
	// candidate to pick.
	ImplicitReferenceAmbiguous
)

// ResolveImplicitReference combines the derivation output with the
// caller's archive lookup and returns a materializable
// ConceptRelation when the lookup produced exactly one candidate.
// Pure function — the archive lookup happens in the caller via
// the Writer / Resolver surface, and the caller passes the result
// count and (when count=1) the resolved taxon id.
//
// Only bare sensu lato / sensu stricto phrases resolve through
// this path. Named-author phrases (sensu <author>, auct. non
// <author>) reference concepts identified explicitly by the
// author string; caller resolves those via reference-based lookup
// (author-year -> reference row -> taxa citing that reference).
func ResolveImplicitReference(
	derived DerivedRelation,
	subjectTaxonID string,
	referenceID string,
	remarks string,
	candidateCount int,
	singleCandidateTaxonID string,
) (ImplicitReferenceOutcome, ConceptRelation) {
	// Named-author derivations aren't in scope for implicit
	// resolution; caller handles them via a different code path.
	if derived.AgainstNamedAuthor {
		return ImplicitReferenceNone, ConceptRelation{}
	}
	// No derivable relation from a bare namePhrase → no implicit
	// reference to resolve.
	if !derived.HasRelation() {
		return ImplicitReferenceNone, ConceptRelation{}
	}
	switch candidateCount {
	case 0:
		return ImplicitReferenceMissing, ConceptRelation{}
	case 1:
		return ImplicitReferenceResolved, ConceptRelation{
			TaxonID:        subjectTaxonID,
			RelatedTaxonID: singleCandidateTaxonID,
			RelTypeID:      derived.RelTypeID,
			ReferenceID:    referenceID,
			Remarks:        remarks,
		}
	default:
		return ImplicitReferenceAmbiguous, ConceptRelation{}
	}
}

// WriteMention applies the concept fields from a MentionConcept
// onto an existing taxon row. Convenience over calling the
// individual Writer methods.
//
// referenceID is the reference row that AccordingTo resolves to
// (caller does the string -> id lookup before calling). Empty
// referenceID skips the according_to write.
//
// overwrite=false is the safe default: existing curator-set values
// on the row are preserved. Pass true only when the caller has
// specific reason to clobber (bulk re-import with authoritative
// source, curator explicit override, etc.).
func WriteMention(w Writer, taxonID string, mc MentionConcept, referenceID string, overwrite bool) error {
	if taxonID == "" {
		return errors.New("conceptimport: WriteMention: taxonID is required")
	}
	if mc.NamePhrase != "" {
		if err := w.SetTaxonNamePhrase(taxonID, mc.NamePhrase, overwrite); err != nil {
			return err
		}
	}
	if referenceID != "" {
		if err := w.SetTaxonAccordingTo(taxonID, referenceID, overwrite); err != nil {
			return err
		}
	}
	return nil
}

// WriteMentionSynonym is the synonym-row equivalent of WriteMention.
// synonymID is the shared col__id (multiple synonym rows can share
// it for pro-parte cases); taxonID identifies which specific
// (synonym, taxon) row to update.
func WriteMentionSynonym(w Writer, synonymID, taxonID string, mc MentionConcept, referenceID string, overwrite bool) error {
	if synonymID == "" {
		return errors.New("conceptimport: WriteMentionSynonym: synonymID is required")
	}
	if taxonID == "" {
		return errors.New("conceptimport: WriteMentionSynonym: taxonID is required")
	}
	if mc.NamePhrase != "" {
		if err := w.SetSynonymNamePhrase(synonymID, taxonID, mc.NamePhrase, overwrite); err != nil {
			return err
		}
	}
	if referenceID != "" {
		if err := w.SetSynonymAccordingTo(synonymID, taxonID, referenceID, overwrite); err != nil {
			return err
		}
	}
	return nil
}
