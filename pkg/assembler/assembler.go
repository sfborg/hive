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
	"sort"
	"strings"
	"time"

	"github.com/sfborg/hive/pkg/conceptimport"
)

// sortStrings is sort.Strings under a package-local name so the
// synonymy pass's small comparators sit alongside their callers.
func sortStrings(v []string) { sort.Strings(v) }

// sortSynonymyCandidatesDesc orders candidates newest-first:
// PaperCreatedAt desc, then ExtractionRunID desc as a
// deterministic tiebreak.
func sortSynonymyCandidatesDesc(v []synonymyCandidate) {
	sort.SliceStable(v, func(i, j int) bool {
		ti := v[i].Paper.PaperCreatedAt
		tj := v[j].Paper.PaperCreatedAt
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return v[i].Paper.ExtractionRunID > v[j].Paper.ExtractionRunID
	})
}

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
	// LocalID is a plan-scoped identifier the planner assigns to
	// this to-be-created taxon so subsequent actions in the same
	// plan can reference it as a parent. Format is "plan:<n>".
	// Empty for actions the planner doesn't need to reference again.
	LocalID string

	// ParentTaxonID is the anchor for the new taxon. Either a real
	// archive taxon id, or a "plan:<n>" reference to a
	// [CreateTaxonAction.LocalID] earlier in the same plan. Apply
	// substitutes the local reference for the real id at write
	// time. Empty is only legal for a root taxon.
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
// Papers are processed in the order supplied. Callers are
// responsible for sorting (typically by paper publication date
// ascending, with extraction_run_id as a deterministic tiebreak)
// so the result is invariant to storage order — see
// LITERATURE_VIEW.md § Determinism guarantees.
//
// Two passes:
//
//  1. Non-synonym pass. Every accepted / provisionally-accepted /
//     bare mention flows through resolveMention: auto-match,
//     cascade, bracket, create-taxon.
//  2. Synonymy pass. Synonym-status mentions are grouped across
//     papers by (canonical, authorship). Most-recent-wins picks
//     the surviving assertion per group; disagreements without a
//     tiebreak surface as FlagContradictorySynonymy.
func Build(papers []PaperInput, existing Existing, rules Rules) Plan {
	st := newPlanState(existing, rules)
	for _, paper := range papers {
		st.processPaper(paper)
	}
	st.processSynonymies(papers)
	return Plan{Actions: st.actions}
}

// planState holds the mutable per-Build working state: the growing
// taxon index (existing + planned), per-paper mention indexes, the
// resolved-mention memo, a plan-local id counter, and the
// accumulated action list.
type planState struct {
	rules Rules
	idx   *taxonIndex

	// resolved memoizes each processed mention's resolution so a
	// child's cascade lookup doesn't re-run the parent's rules.
	// Key: paper.ReferenceID + "|" + mentionID.
	resolved map[string]mentionResolution

	// paperMentions holds the current paper's name_usages indexed by
	// MentionID for parent-chain traversal. Reset per paper.
	paperMentions map[string]NameUsage

	// currentPaper is the paper being processed. Used by the
	// mention-key helper.
	currentPaper PaperInput

	// nextLocalID is the ordinal for the next "plan:<n>" id minted.
	nextLocalID int

	// actions is the growing plan.
	actions []Action
}

// mentionResolution is what a mention resolved to. Populated when
// planState first processes the mention, memoized so subsequent
// cascade lookups skip the work.
type mentionResolution struct {
	kind resolutionKind
	// anchorID is what the mention resolves to as a parent for its
	// own children. Real taxon id for matched; "plan:<n>" for
	// created; "" for failed.
	anchorID string
}

type resolutionKind int

const (
	resolvedNone     resolutionKind = iota // couldn't resolve; failed
	resolvedMatched                        // bound to an existing taxon
	resolvedCreated                        // created via cascade / create rule
)

// newPlanState constructs the working state for a Build call.
func newPlanState(existing Existing, rules Rules) *planState {
	return &planState{
		rules:    rules,
		idx:      newTaxonIndex(existing.Taxa),
		resolved: make(map[string]mentionResolution),
	}
}

// processPaper is the per-paper entry point: build the mention
// index and process every non-synonym name_usage. Synonym-status
// mentions are handled by [processSynonymies] in a second pass so
// most-recent-wins can compare across papers.
func (s *planState) processPaper(paper PaperInput) {
	s.currentPaper = paper
	s.paperMentions = make(map[string]NameUsage, len(paper.NameUsages))
	for _, nu := range paper.NameUsages {
		s.paperMentions[nu.MentionID] = nu
	}
	for _, nu := range paper.NameUsages {
		if isSynonymStatus(nu.Status) {
			continue
		}
		s.resolveMention(nu.MentionID)
	}
}

// isSynonymStatus reports whether a mention's paper-asserted status
// puts it in the "synonym" family (heterotypic, homotypic,
// ambiguous, misapplied). Case-insensitive substring match.
func isSynonymStatus(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "synonym") || strings.Contains(s, "misapplied")
}

// mentionKey builds the resolved-memo key for a mention in the
// current paper.
func (s *planState) mentionKey(mentionID string) string {
	return s.currentPaper.ReferenceID + "|" + mentionID
}

// nextPlanLocalID mints and returns the next plan-local id string.
func (s *planState) nextPlanLocalID() string {
	s.nextLocalID++
	return fmt.Sprintf("plan:%d", s.nextLocalID)
}

// resolveMention runs the per-mention rule cascade. Idempotent per
// (paper, mention) via the memo. Returns the resolution.
func (s *planState) resolveMention(mentionID string) mentionResolution {
	key := s.mentionKey(mentionID)
	if r, ok := s.resolved[key]; ok {
		return r
	}
	// Reserve the slot with a failed sentinel so a cycle (which
	// shouldn't happen in a well-formed compose tree, but is worth
	// guarding) terminates.
	s.resolved[key] = mentionResolution{kind: resolvedNone}

	nu, ok := s.paperMentions[mentionID]
	if !ok {
		return s.resolved[key]
	}
	if nu.Canonical == "" {
		// Nothing to match; leave for a later rule (or curator).
		return s.resolved[key]
	}
	res := s.idx.lookup(nu.Canonical, nu.Authorship)
	prov := actionProv(s.currentPaper, nu)
	switch res.outcome {
	case matchUnique:
		if s.rules.AutoMatchAuthorshipRequired && !res.authorshipConfirmed {
			s.actions = append(s.actions, flag(prov, FlagUnresolvedAnchor,
				"canonical-only match rejected (authorship required)",
				[]string{res.taxonID}))
			return s.resolved[key]
		}
		s.actions = append(s.actions, Action{
			Kind:                   ActionMatch,
			SourcePaperReferenceID: prov.ReferenceID,
			SourceMentionID:        prov.MentionID,
			Match: MatchAction{
				TaxonID:             res.taxonID,
				AuthorshipConfirmed: res.authorshipConfirmed,
			},
		})
		r := mentionResolution{kind: resolvedMatched, anchorID: res.taxonID}
		s.resolved[key] = r
		return r
	case matchAmbiguous:
		s.actions = append(s.actions, flag(prov, FlagAmbiguousMatch,
			"canonical+authorship matched multiple existing taxa",
			res.candidates))
		return s.resolved[key]
	}

	// matchNone → cascade for an anchor by walking up the paper's
	// own parent chain. First ancestor with a successful
	// resolution (matched or created) becomes the parent for a
	// new-taxon insert.
	parent := s.cascadeParent(nu)
	if parent == "" {
		s.actions = append(s.actions, flag(prov, FlagUnresolvedAnchor,
			"no existing taxon matched, and no cascade anchor found",
			nil))
		return s.resolved[key]
	}

	// Bracket-fallback rule: species-group mention whose immediate
	// paper-asserted parent is above genus rank (i.e., the paper
	// jumped over the genus level, e.g. Order → Species) means the
	// paper is genuinely uncertain about the genus. Rather than
	// fabricate one, apply the [Aus] bus convention and mark
	// "provisionally accepted".
	if s.rules.BracketFallback && isSpeciesGroupRank(nu.Rank) &&
		s.paperParentIsAboveGenus(nu) {
		genus, species := splitBinomial(nu.Canonical)
		if genus != "" && species != "" {
			s.actions = append(s.actions, Action{
				Kind:                   ActionBracket,
				SourcePaperReferenceID: prov.ReferenceID,
				SourceMentionID:        prov.MentionID,
				Bracket: BracketAction{
					AnchorTaxonID:  parent,
					BracketedGenus: "[" + genus + "]",
					Species:        species,
					Authorship:     nu.Authorship,
					Rank:           nu.Rank,
				},
			})
			// Don't virtualize a bracketed taxon into the index;
			// its identity is deliberately unresolved and future
			// mentions shouldn't auto-match to it.
			r := mentionResolution{kind: resolvedNone}
			s.resolved[key] = r
			return r
		}
	}

	localID := s.nextPlanLocalID()
	s.actions = append(s.actions, Action{
		Kind:                   ActionCreateTaxon,
		SourcePaperReferenceID: prov.ReferenceID,
		SourceMentionID:        prov.MentionID,
		CreateTaxon: CreateTaxonAction{
			LocalID:       localID,
			ParentTaxonID: parent,
			Canonical:     nu.Canonical,
			Authorship:    nu.Authorship,
			Rank:          nu.Rank,
			Status:        defaultStatus(nu.Status),
		},
	})
	// Virtualize the new taxon into the index so subsequent
	// mentions in the same or later paper can auto-match it. The
	// LocalID is used as the "taxon id" in the index because Apply
	// will remap it.
	s.idx.add(localID, nu.Canonical, nu.Authorship)
	r := mentionResolution{kind: resolvedCreated, anchorID: localID}
	s.resolved[key] = r
	return r
}

// isSpeciesGroupRank reports whether a CoLDP rank string names a
// species-group rank (per the ICZN + ICN Codes). Case-insensitive.
func isSpeciesGroupRank(rank string) bool {
	switch strings.ToLower(strings.TrimSpace(rank)) {
	case "species", "subspecies", "variety", "subvariety",
		"form", "subform", "aggregate",
		"section", "subsection": // rare botanical species-group ranks
		return true
	}
	return false
}

// paperParentIsAboveGenus checks whether the mention's immediate
// paper-asserted parent (if any) sits at a rank above genus. When
// the parent mention is missing or has no rank, that also counts
// as "above genus" for the bracket rule — no genus context means
// no genus context.
func (s *planState) paperParentIsAboveGenus(nu NameUsage) bool {
	if nu.ParentMentionID == "" {
		return true
	}
	parent, ok := s.paperMentions[nu.ParentMentionID]
	if !ok {
		return true
	}
	pr := strings.ToLower(strings.TrimSpace(parent.Rank))
	if pr == "" {
		return true
	}
	// Anything explicitly at genus or below is a valid genus
	// context; anything else counts as above-genus for the
	// bracket rule.
	switch pr {
	case "genus", "subgenus", "section", "subsection":
		return false
	}
	return true
}

// processSynonymies is the pass-2 entry point. It walks every
// paper again, collects synonym-status mentions grouped by
// (canonical, authorship), and picks the winner per group.
//
// Most-recent-wins ordering (default with SynonymyWinnerByDate):
// group entries sort by PaperCreatedAt desc, then
// ExtractionRunID desc as tiebreak; the first entry after sort
// wins.
//
// Contradictions (multiple entries with distinct accepted anchors
// and identical or missing PaperCreatedAt) surface as
// FlagContradictorySynonymy so the curator can adjudicate.
func (s *planState) processSynonymies(papers []PaperInput) {
	groups := s.groupSynonymies(papers)
	// Deterministic iteration order over the group map: sort by
	// key so the resulting Plan is stable regardless of map
	// iteration order.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		s.emitSynonymyForGroup(groups[k])
	}
}

// synonymyCandidate is one paper's assertion of a synonym → accepted
// pair, restricted to what the pass-2 winner picker needs.
type synonymyCandidate struct {
	Paper          PaperInput
	Mention        NameUsage
	AcceptedAnchor string // real taxon id or "plan:<n>"
}

// synonymyGroup collects every candidate assertion for one
// (synonym canonical, authorship) key.
type synonymyGroup struct {
	Canonical  string
	Authorship string
	Candidates []synonymyCandidate
}

// groupSynonymies walks all papers and gathers candidate synonym
// assertions grouped by (canonical, authorship). Skips any
// mention whose accepted-parent didn't resolve (that's a separate
// concern surfaced by the accepted-taxon pass).
func (s *planState) groupSynonymies(papers []PaperInput) map[string]*synonymyGroup {
	out := make(map[string]*synonymyGroup)
	for _, paper := range papers {
		s.currentPaper = paper
		s.paperMentions = make(map[string]NameUsage, len(paper.NameUsages))
		for _, nu := range paper.NameUsages {
			s.paperMentions[nu.MentionID] = nu
		}
		for _, nu := range paper.NameUsages {
			if !isSynonymStatus(nu.Status) {
				continue
			}
			if nu.Canonical == "" || nu.ParentMentionID == "" {
				// Missing subject or accepted anchor — the paper
				// didn't give enough info to plan a synonym row.
				continue
			}
			parentRes := s.resolveMention(nu.ParentMentionID)
			if parentRes.anchorID == "" {
				continue
			}
			key := nameKey(nu.Canonical, nu.Authorship)
			g, ok := out[key]
			if !ok {
				g = &synonymyGroup{
					Canonical:  nu.Canonical,
					Authorship: nu.Authorship,
				}
				out[key] = g
			}
			g.Candidates = append(g.Candidates, synonymyCandidate{
				Paper:          paper,
				Mention:        nu,
				AcceptedAnchor: parentRes.anchorID,
			})
		}
	}
	return out
}

// emitSynonymyForGroup runs winner selection on one group and
// appends the corresponding Action to the plan.
func (s *planState) emitSynonymyForGroup(g *synonymyGroup) {
	if len(g.Candidates) == 0 {
		return
	}
	// Distinct anchors decide whether we have a real contradiction.
	distinct := make(map[string]int)
	for _, c := range g.Candidates {
		distinct[c.AcceptedAnchor]++
	}
	if len(distinct) == 1 {
		// Everyone agrees; emit the winning ActionCreateSynonym
		// once. Provenance comes from the first candidate for
		// stable output.
		s.emitCreateSynonymFromCandidate(g.Candidates[0])
		return
	}
	if !s.rules.SynonymyWinnerByDate {
		// Rule off: surface every disagreement as a flag.
		s.emitFlagContradictorySynonymy(g)
		return
	}
	winner, ok := pickSynonymyWinner(g.Candidates)
	if !ok {
		s.emitFlagContradictorySynonymy(g)
		return
	}
	s.emitCreateSynonymFromCandidate(winner)
}

// pickSynonymyWinner sorts candidates by paper date desc, run id
// desc, and returns the first entry — provided the top-two
// candidates aren't tied on both fields. A true tie (same date,
// same run id) or a top entry with a zero paper date returns
// ok=false so the caller can flag the group instead.
func pickSynonymyWinner(cands []synonymyCandidate) (synonymyCandidate, bool) {
	if len(cands) == 0 {
		return synonymyCandidate{}, false
	}
	sorted := make([]synonymyCandidate, len(cands))
	copy(sorted, cands)
	sortSynonymyCandidatesDesc(sorted)
	top := sorted[0]
	if top.Paper.PaperCreatedAt.IsZero() {
		return synonymyCandidate{}, false
	}
	if len(sorted) > 1 {
		second := sorted[1]
		if second.AcceptedAnchor != top.AcceptedAnchor &&
			second.Paper.PaperCreatedAt.Equal(top.Paper.PaperCreatedAt) &&
			second.Paper.ExtractionRunID == top.Paper.ExtractionRunID {
			return synonymyCandidate{}, false
		}
	}
	return top, true
}

func (s *planState) emitCreateSynonymFromCandidate(c synonymyCandidate) {
	prov := actionProv(c.Paper, c.Mention)
	s.actions = append(s.actions, Action{
		Kind:                   ActionCreateSynonym,
		SourcePaperReferenceID: prov.ReferenceID,
		SourceMentionID:        prov.MentionID,
		CreateSynonym: CreateSynonymAction{
			AcceptedTaxonID: c.AcceptedAnchor,
			Canonical:       c.Mention.Canonical,
			Authorship:      c.Mention.Authorship,
			Status:          defaultSynonymStatus(c.Mention.Status),
		},
	})
}

func (s *planState) emitFlagContradictorySynonymy(g *synonymyGroup) {
	// Pick a stable provenance from the first candidate; include
	// every distinct accepted anchor in Candidates so the curator
	// can see the disagreement.
	first := g.Candidates[0]
	prov := actionProv(first.Paper, first.Mention)
	anchors := make(map[string]struct{})
	for _, c := range g.Candidates {
		anchors[c.AcceptedAnchor] = struct{}{}
	}
	keys := make([]string, 0, len(anchors))
	for k := range anchors {
		keys = append(keys, k)
	}
	sortStrings(keys)
	s.actions = append(s.actions, flag(prov, FlagContradictorySynonymy,
		fmt.Sprintf("papers disagree on the accepted taxon for %s %s",
			g.Canonical, g.Authorship),
		keys))
}

// defaultSynonymStatus normalizes empty status → "synonym" so
// the Writer always sees a non-empty CoLDP status value.
func defaultSynonymStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return "synonym"
	}
	return status
}

// splitBinomial extracts (genus, species) from a canonical
// binomial. Returns ("", "") if the canonical isn't at least
// two space-separated words.
func splitBinomial(canonical string) (string, string) {
	parts := strings.Fields(canonical)
	if len(parts) < 2 {
		return "", ""
	}
	// Species-group binomial or trinomial: first word is genus,
	// remainder is species (or species + subspecific epithet the
	// bracket rule treats as one string).
	return parts[0], strings.Join(parts[1:], " ")
}

// cascadeParent walks the paper's parent chain for the given
// mention and returns the anchor id (real taxon id or "plan:<n>")
// of the first ancestor with a successful resolution. Empty when
// no ancestor resolves — the caller handles that as a
// missing-anchor condition (bracket fallback or flag).
func (s *planState) cascadeParent(nu NameUsage) string {
	pid := nu.ParentMentionID
	for pid != "" {
		res := s.resolveMention(pid)
		if res.kind != resolvedNone && res.anchorID != "" {
			return res.anchorID
		}
		parentNU, ok := s.paperMentions[pid]
		if !ok {
			break
		}
		pid = parentNU.ParentMentionID
	}
	return ""
}

// defaultStatus applies the "accepted" default when the paper
// mention didn't carry a status. Any explicit status the paper
// asserted (synonym, provisionally accepted, etc.) passes
// through unchanged.
func defaultStatus(s string) string {
	if s == "" {
		return "accepted"
	}
	return s
}

// actionProv builds the ActionProvenance triple used by every
// action produced by the planner.
func actionProv(paper PaperInput, nu NameUsage) ActionProvenance {
	return ActionProvenance{
		ReferenceID: paper.ReferenceID,
		MentionID:   nu.MentionID,
	}
}

// flag is a small constructor for ActionFlag entries. Keeps the
// noisy field-population out of the per-rule bodies.
func flag(prov ActionProvenance, kind FlagKind, msg string, candidates []string) Action {
	return Action{
		Kind:                   ActionFlag,
		SourcePaperReferenceID: prov.ReferenceID,
		SourceMentionID:        prov.MentionID,
		Flag: FlagAction{
			Kind:       kind,
			Message:    msg,
			Candidates: candidates,
		},
	}
}

// Apply walks a [Plan] and invokes the [Writer] for each action.
// Returns the [Report] summarizing what landed. Any Writer error
// aborts and is returned to the caller; the caller's transaction
// determines whether to commit the partial application.
//
// Plan-local ids ("plan:<n>" strings) on CreateTaxonAction.LocalID
// and other fields (ParentTaxonID, AcceptedTaxonID, AnchorTaxonID)
// are substituted with the real ids returned by the Writer as the
// plan is walked. An action that references an unknown local id
// returns an error.
func Apply(w Writer, r Reader, plan Plan) (Report, error) {
	if w == nil {
		return Report{}, errors.New("assembler: Apply: nil writer")
	}
	report := Report{}
	remap := make(map[string]string)
	for _, act := range plan.Actions {
		prov := ActionProvenance{
			ReferenceID: act.SourcePaperReferenceID,
			MentionID:   act.SourceMentionID,
		}
		switch act.Kind {
		case ActionMatch:
			report.Matched++
		case ActionCreateTaxon:
			ct := act.CreateTaxon
			parent, err := resolveLocal(ct.ParentTaxonID, remap)
			if err != nil {
				return report, err
			}
			ct.ParentTaxonID = parent
			newID, err := w.CreateTaxonForAssembler(ct, prov.ReferenceID)
			if err != nil {
				return report, fmt.Errorf("assembler: create taxon: %w", err)
			}
			if ct.LocalID != "" {
				remap[ct.LocalID] = newID
			}
			report.TaxaCreated++
		case ActionCreateSynonym:
			cs := act.CreateSynonym
			acc, err := resolveLocal(cs.AcceptedTaxonID, remap)
			if err != nil {
				return report, err
			}
			cs.AcceptedTaxonID = acc
			if _, err := w.CreateSynonymForAssembler(cs, prov.ReferenceID); err != nil {
				return report, fmt.Errorf("assembler: create synonym: %w", err)
			}
			report.SynonymsCreated++
		case ActionBracket:
			br := act.Bracket
			anchor, err := resolveLocal(br.AnchorTaxonID, remap)
			if err != nil {
				return report, err
			}
			br.AnchorTaxonID = anchor
			if _, err := w.CreateBracketedTaxonForAssembler(br, prov.ReferenceID); err != nil {
				return report, fmt.Errorf("assembler: bracket fallback: %w", err)
			}
			report.BracketedCreated++
		case ActionInsertConceptRel:
			rel := act.InsertConceptRel
			subj, err := resolveLocal(rel.TaxonID, remap)
			if err != nil {
				return report, err
			}
			obj, err := resolveLocal(rel.RelatedTaxonID, remap)
			if err != nil {
				return report, err
			}
			rel.TaxonID = subj
			rel.RelatedTaxonID = obj
			if err := w.InsertConceptRelation(rel); err != nil {
				return report, fmt.Errorf("assembler: insert concept relation: %w", err)
			}
			report.ConceptRelationsInserted++
		case ActionSetConceptFields:
			scf := act.SetConceptFields
			t, err := resolveLocal(scf.TaxonID, remap)
			if err != nil {
				return report, err
			}
			st, err := resolveLocal(scf.SynonymTaxonID, remap)
			if err != nil {
				return report, err
			}
			scf.TaxonID = t
			scf.SynonymTaxonID = st
			if err := applySetConceptFields(w, scf); err != nil {
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

// resolveLocal remaps a "plan:<n>" reference into the real id the
// Writer returned when the corresponding CreateTaxon action fired.
// Non-plan-local ids pass through unchanged; an empty id also
// passes through (root taxon case).
func resolveLocal(id string, remap map[string]string) (string, error) {
	if id == "" {
		return "", nil
	}
	if !strings.HasPrefix(id, "plan:") {
		return id, nil
	}
	real, ok := remap[id]
	if !ok {
		return "", fmt.Errorf("assembler: unresolved plan-local id %q", id)
	}
	return real, nil
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
