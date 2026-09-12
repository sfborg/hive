// Package assembler produces hive's Layer 2 (literature view) as a
// deterministic function of Layer 1 (per-paper assertions) and a
// rule set.
//
// See docs/2026-09-12_LITERATURE_VIEW.md for the four-layer model.
// This package implements Slice 1 — assembler v1, manual trigger,
// no auto-recompute on paper import.
//
// Layering. The package splits into three concerns:
//
//   - Pure planning. [Plan] takes pre-loaded papers, a snapshot of
//     existing DB taxa, and a rule set, and returns a [Plan] whose
//     [Action] entries describe what to write. Deterministic and
//     side-effect-free — testable without a database.
//   - Persistence. [Apply] walks a [Plan] and invokes the [Writer]
//     for each action, delegating the actual SQL to the caller.
//   - Loading. Callers implement the [Reader] interface to hand
//     [Plan] a snapshot of the existing taxonomy; they implement
//     [Writer] to receive mutations.
//
// The assembler does not create its own transaction, does not open a
// SQL connection, and does not import github.com/sfborg/hive/pkg.
// Callers wire it through their own Tx type.
package assembler

import (
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/hive/pkg/conceptimport"
)

// PaperInput is one paper's assertions as fed into [Plan]. Callers
// materialize this from the compose extraction run for the paper.
type PaperInput struct {
	// ReferenceID is the CoLDP reference row this paper corresponds
	// to (col__id on the reference row).
	ReferenceID string

	// ExtractionRunID is the compose-purpose extraction run whose
	// raw_output_json produced the NameUsages. Used for provenance
	// and as the last-resort deterministic tiebreak.
	ExtractionRunID string

	// PaperCreatedAt is the paper's publication date (or the
	// extraction-run created_at if the publication date is missing
	// or unparseable). Primary tiebreak for most-recent-wins rules.
	PaperCreatedAt time.Time

	// NameUsages are the per-mention rows from the compose tree,
	// preserving parent/child ordering as authored by the paper.
	NameUsages []NameUsage
}

// NameUsage is one row of a paper's name_usages array, restricted
// to the fields the assembler consumes.
type NameUsage struct {
	// MentionID is the compose-run identifier for this row. Unique
	// within the paper, meaningless across papers. Used to resolve
	// ParentMentionID within the same paper.
	MentionID string

	// ParentMentionID identifies the parent mention within the same
	// paper. Empty for the paper's top-level anchor mention.
	ParentMentionID string

	// Status is the CoLDP taxonomic status ("accepted", "synonym",
	// "ambiguous synonym", "provisionally accepted", ...) as
	// asserted by the paper.
	Status string

	// Canonical is the parser-canonical form of the name (genus +
	// species + infraspecific for a species-group name; higher-taxon
	// names are their own canonical). Free of authorship, sensu
	// markers, and other tail annotations.
	Canonical string

	// Authorship is the parser-normalized authorship string (e.g.
	// "(Smith, 1850)" or "L., 1970"). Empty when the paper doesn't
	// carry authorship on this mention.
	Authorship string

	// Rank is the CoLDP rank id ("kingdom", "phylum", ...,
	// "species", "subspecies", ...). Required for correct nesting.
	Rank string

	// NamePhrase / AccordingTo / NomenclaturalNote feed straight
	// into [conceptimport.MentionConcept]; see that package for the
	// full semantics.
	NamePhrase        string
	AccordingTo       string
	NomenclaturalNote string
}

// mention returns the MentionConcept view of this name_usage.
func (n NameUsage) mention() conceptimport.MentionConcept {
	return conceptimport.MentionConcept{
		NamePhrase:        n.NamePhrase,
		AccordingTo:       n.AccordingTo,
		NomenclaturalNote: n.NomenclaturalNote,
	}
}

// Rules configures the assembler behavior. Zero value is safe and
// produces the default "high-purity" behavior described in
// LITERATURE_VIEW.md § Purity spectrum.
type Rules struct {
	// AutoMatchAuthorshipRequired makes the auto-match rule refuse
	// to bind a paper mention to an existing taxon when the paper
	// omits authorship. When false (default) the assembler binds on
	// canonical-only match if it's unambiguous.
	AutoMatchAuthorshipRequired bool

	// BracketFallback enables the [Aus] bus provisionally-accepted
	// fallback for species-group names whose genus can't be
	// resolved through cascade. When false, such names surface as
	// FlagUnresolvedAnchor actions instead. Default: true.
	BracketFallback bool

	// SynonymyWinnerByDate picks the most recent paper's synonymy
	// declaration when multiple papers disagree. When false, any
	// disagreement flags for curator review. Default: true.
	SynonymyWinnerByDate bool
}

// DefaultRules returns the high-purity default rule set.
func DefaultRules() Rules {
	return Rules{
		BracketFallback:      true,
		SynonymyWinnerByDate: true,
	}
}

// Existing is the snapshot of the archive's current taxonomy that
// [Plan] consults during auto-match and cascade. Loaded by the
// caller via [Reader] before Plan runs. Kept as a plain struct
// rather than an interface so tests can construct it directly.
type Existing struct {
	// Taxa is the flat list of every taxon row visible to the
	// assembler, keyed as convenient by callers. Order-independent;
	// [Plan] indexes it internally.
	Taxa []ExistingTaxon
}

// ExistingTaxon is the assembler-facing view of a taxon row.
type ExistingTaxon struct {
	// TaxonID is col__id.
	TaxonID string

	// NameID is col__name_id (the accepted-name row).
	NameID string

	// ParentTaxonID is col__parent_id ("" for a root).
	ParentTaxonID string

	// Canonical is the parser-canonical form. Empty is legal (some
	// higher taxa lack a canonical distinct from scientific name).
	Canonical string

	// Authorship is the normalized authorship on the accepted name.
	// Empty for names without an authorship string.
	Authorship string

	// Rank is the CoLDP rank id.
	Rank string
}

// Plan holds the ordered sequence of [Action]s the assembler
// intends to apply. [Apply] walks it top-to-bottom.
type Plan struct {
	Actions []Action
}

// ActionKind names the discriminated-union tag on [Action].
type ActionKind int

const (
	// ActionMatch records that a paper mention bound to an
	// existing taxon by canonical+authorship. No mutation needed;
	// kept in the plan so the report can count matches.
	ActionMatch ActionKind = iota

	// ActionCreateTaxon adds a new accepted taxon (and its name
	// row) to the archive.
	ActionCreateTaxon

	// ActionCreateSynonym adds a new synonym row against an
	// existing (or newly-created) accepted taxon.
	ActionCreateSynonym

	// ActionBracket applies the [Aus] bus convention: create a
	// species-group taxon under a fallback anchor, with bracketed
	// scientific-name / genus fields and status
	// "provisionally accepted".
	ActionBracket

	// ActionInsertConceptRel writes a taxon_concept_relation row
	// derived from a sensu / auct. / misapplication mention.
	ActionInsertConceptRel

	// ActionSetConceptFields sets col__name_phrase and/or
	// col__according_to_id on an existing taxon or synonym without
	// creating a new row.
	ActionSetConceptFields

	// ActionFlag surfaces a paper mention that the assembler
	// couldn't decide about (ambiguous auto-match, contradictory
	// synonymies, missing anchor with bracket fallback disabled).
	// Never a mutation; the [Writer] receives it as a report row.
	ActionFlag
)

// Action is one entry in a [Plan]. The Kind field discriminates
// which of the inline sub-structs is populated.
type Action struct {
	Kind ActionKind

	// SourcePaperReferenceID / SourceMentionID name the assertion
	// that produced this action (for provenance). Every action has
	// them; both empty is legal only for a synthetic action added
	// by the assembler itself.
	SourcePaperReferenceID string
	SourceMentionID        string

	// The following fields are populated according to Kind. Zero
	// values elsewhere.
	Match             MatchAction
	CreateTaxon       CreateTaxonAction
	CreateSynonym     CreateSynonymAction
	Bracket           BracketAction
	InsertConceptRel  conceptimport.ConceptRelation
	SetConceptFields  SetConceptFieldsAction
	Flag              FlagAction
}

// MatchAction records a canonical+authorship auto-match binding.
type MatchAction struct {
	// TaxonID is the existing archive taxon the mention bound to.
	TaxonID string

	// AuthorshipConfirmed is true when both canonical AND authorship
	// matched; false when only canonical matched.
	AuthorshipConfirmed bool
}

// CreateTaxonAction describes an accepted-taxon insert.
type CreateTaxonAction struct {
	// ParentTaxonID is the existing (or previously-created-in-plan)
	// anchor. Empty is only legal for a root taxon.
	ParentTaxonID string

	// Canonical, Authorship, Rank, Status feed the new taxon /
	// name rows. Status defaults to "accepted" when empty.
	Canonical  string
	Authorship string
	Rank       string
	Status     string
}

// CreateSynonymAction describes a synonym insert against an
// existing (or plan-created) accepted taxon.
type CreateSynonymAction struct {
	// AcceptedTaxonID identifies the accepted taxon the synonym
	// attaches to.
	AcceptedTaxonID string

	// Canonical / Authorship / Status describe the synonym.
	Canonical  string
	Authorship string
	Status     string
}

// BracketAction describes the [Aus] bus fallback: create a
// species-group taxon with a bracketed genus, status
// "provisionally accepted", attached to a fallback anchor.
type BracketAction struct {
	// AnchorTaxonID is the highest-in-chain existing taxon the
	// bracketed name attaches to. Typically a family or subfamily.
	AnchorTaxonID string

	// BracketedGenus is the original genus, with brackets applied:
	// e.g. "[Aus]". Written to col__genus.
	BracketedGenus string

	// Species / Authorship / Rank match the paper's mention.
	Species    string
	Authorship string
	Rank       string
}

// SetConceptFieldsAction writes name_phrase / according_to on an
// existing (or plan-created) row without creating a new row.
type SetConceptFieldsAction struct {
	// EntityKind is either "taxon" or "synonym". SynonymTaxonID is
	// required for synonyms (see conceptimport.Writer semantics).
	EntityKind      string
	TaxonID         string
	SynonymID       string
	SynonymTaxonID  string
	NamePhrase      string
	AccordingToRef  string
}

// FlagKind names the family of flag actions.
type FlagKind int

const (
	FlagAmbiguousMatch FlagKind = iota
	FlagUnresolvedAnchor
	FlagContradictorySynonymy
	FlagAmbiguousImplicitReference
)

// FlagAction records a curator-actionable condition the assembler
// couldn't resolve on its own.
type FlagAction struct {
	Kind    FlagKind
	Message string
	// Optional context references.
	Candidates []string // e.g. taxon ids for ambiguous match
}

// Reader is the read-side interface the caller implements against
// its Tx. [LoadExisting] uses it to build an [Existing] snapshot
// before [Plan] runs.
type Reader interface {
	// ListAllTaxaForAssembler returns every taxon row visible to
	// the assembler, projected to [ExistingTaxon]. Callers are
	// expected to filter by archive scope in their implementation.
	ListAllTaxaForAssembler() ([]ExistingTaxon, error)

	// FindImplicitReferenceTaxa mirrors conceptimport's implicit-
	// reference lookup: given a nameID and a namePhrase, return the
	// candidate taxa the phrase could refer to. See
	// [conceptimport.ResolveImplicitReference] for how the result
	// count is interpreted.
	FindImplicitReferenceTaxa(nameID, namePhrase string) ([]string, error)
}

// Writer is the write-side interface [Apply] invokes. Extends
// [conceptimport.Writer] with the row-creating operations the
// assembler needs.
type Writer interface {
	conceptimport.Writer

	// CreateTaxonForAssembler creates a new accepted taxon (+ name
	// row) matching the action and returns the new taxon id.
	// Implementations stamp provenance (referenceID as the source
	// paper) so the row is attributable to the assembler.
	CreateTaxonForAssembler(a CreateTaxonAction, referenceID string) (string, error)

	// CreateSynonymForAssembler creates a synonym row against the
	// accepted taxon in the action, stamped with the source
	// referenceID for provenance.
	CreateSynonymForAssembler(a CreateSynonymAction, referenceID string) (string, error)

	// CreateBracketedTaxonForAssembler creates a species-group taxon
	// with bracketed genus / scientific-name fields and status
	// "provisionally accepted".
	CreateBracketedTaxonForAssembler(a BracketAction, referenceID string) (string, error)

	// RecordAssemblerFlag persists a curator-actionable flag from
	// the assembler run. The implementation decides where the flag
	// lives (a hive__assembler_flag table, a warnings log, etc.);
	// Apply just delegates.
	RecordAssemblerFlag(f FlagAction, source ActionProvenance) error
}

// ActionProvenance carries the source-paper coordinates of a flag
// so the Writer can attach them to the persisted flag row.
type ActionProvenance struct {
	ReferenceID string
	MentionID   string
}

// Report is [Apply]'s summary output.
type Report struct {
	// Matched counts mentions bound to existing taxa.
	Matched int

	// TaxaCreated counts new accepted taxa the assembler added.
	TaxaCreated int

	// SynonymsCreated counts new synonym rows added.
	SynonymsCreated int

	// BracketedCreated counts [Aus] bus fallback taxa.
	BracketedCreated int

	// ConceptRelationsInserted counts taxon_concept_relation rows
	// added from sensu / auct. mentions.
	ConceptRelationsInserted int

	// ConceptFieldsSet counts existing rows whose namePhrase /
	// accordingTo the assembler populated.
	ConceptFieldsSet int

	// Flags collects the flag actions the assembler emitted.
	// Callers surface these to curators via the UI.
	Flags []FlagAction
}

// LoadExisting is a small convenience wrapper the caller can use to
// populate an [Existing] snapshot from a [Reader]. Kept in the
// package rather than every caller so the snapshot invariants
// (deduplication, indexing) stay in one place.
func LoadExisting(r Reader) (Existing, error) {
	if r == nil {
		return Existing{}, errors.New("assembler: LoadExisting: nil reader")
	}
	rows, err := r.ListAllTaxaForAssembler()
	if err != nil {
		return Existing{}, fmt.Errorf("assembler: load existing: %w", err)
	}
	return Existing{Taxa: rows}, nil
}

// Build computes the assembler action plan for the given paper set
// against the existing taxonomy snapshot. Pure function: same
// inputs, same output.
//
// TODO(slice-1): implement rule bodies. Current placeholder emits
// an empty plan so callers can wire the surface and iterate.
func Build(papers []PaperInput, existing Existing, rules Rules) Plan {
	_ = papers
	_ = existing
	_ = rules
	return Plan{}
}

// Apply walks a [Plan] and invokes the [Writer] for each action.
// Returns the [Report] summarizing what landed. Any Writer error
// aborts and is returned to the caller; the caller's transaction
// determines whether to commit the partial application.
func Apply(w Writer, r Reader, plan Plan) (Report, error) {
	if w == nil {
		return Report{}, errors.New("assembler: Apply: nil writer")
	}
	report := Report{}
	for _, act := range plan.Actions {
		prov := ActionProvenance{
			ReferenceID: act.SourcePaperReferenceID,
			MentionID:   act.SourceMentionID,
		}
		switch act.Kind {
		case ActionMatch:
			report.Matched++
		case ActionCreateTaxon:
			if _, err := w.CreateTaxonForAssembler(act.CreateTaxon, prov.ReferenceID); err != nil {
				return report, fmt.Errorf("assembler: create taxon: %w", err)
			}
			report.TaxaCreated++
		case ActionCreateSynonym:
			if _, err := w.CreateSynonymForAssembler(act.CreateSynonym, prov.ReferenceID); err != nil {
				return report, fmt.Errorf("assembler: create synonym: %w", err)
			}
			report.SynonymsCreated++
		case ActionBracket:
			if _, err := w.CreateBracketedTaxonForAssembler(act.Bracket, prov.ReferenceID); err != nil {
				return report, fmt.Errorf("assembler: bracket fallback: %w", err)
			}
			report.BracketedCreated++
		case ActionInsertConceptRel:
			if err := w.InsertConceptRelation(act.InsertConceptRel); err != nil {
				return report, fmt.Errorf("assembler: insert concept relation: %w", err)
			}
			report.ConceptRelationsInserted++
		case ActionSetConceptFields:
			if err := applySetConceptFields(w, act.SetConceptFields); err != nil {
				return report, err
			}
			report.ConceptFieldsSet++
		case ActionFlag:
			if err := w.RecordAssemblerFlag(act.Flag, prov); err != nil {
				return report, fmt.Errorf("assembler: record flag: %w", err)
			}
			report.Flags = append(report.Flags, act.Flag)
		default:
			return report, fmt.Errorf("assembler: unknown action kind %d", act.Kind)
		}
	}
	_ = r
	return report, nil
}

// applySetConceptFields dispatches an ActionSetConceptFields to the
// right pair of Writer methods depending on whether the target row
// is a taxon or a synonym.
func applySetConceptFields(w Writer, a SetConceptFieldsAction) error {
	switch a.EntityKind {
	case "taxon":
		if a.NamePhrase != "" {
			if err := w.SetTaxonNamePhrase(a.TaxonID, a.NamePhrase, false); err != nil {
				return fmt.Errorf("assembler: set taxon name phrase: %w", err)
			}
		}
		if a.AccordingToRef != "" {
			if err := w.SetTaxonAccordingTo(a.TaxonID, a.AccordingToRef, false); err != nil {
				return fmt.Errorf("assembler: set taxon according-to: %w", err)
			}
		}
	case "synonym":
		if a.NamePhrase != "" {
			if err := w.SetSynonymNamePhrase(a.SynonymID, a.SynonymTaxonID, a.NamePhrase, false); err != nil {
				return fmt.Errorf("assembler: set synonym name phrase: %w", err)
			}
		}
		if a.AccordingToRef != "" {
			if err := w.SetSynonymAccordingTo(a.SynonymID, a.SynonymTaxonID, a.AccordingToRef, false); err != nil {
				return fmt.Errorf("assembler: set synonym according-to: %w", err)
			}
		}
	default:
		return fmt.Errorf("assembler: unknown entity kind %q", a.EntityKind)
	}
	return nil
}
