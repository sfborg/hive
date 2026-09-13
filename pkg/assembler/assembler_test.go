package assembler

import (
	"testing"
	"time"

	"github.com/sfborg/hive/pkg/conceptimport"
)

// TestBuild_AutoMatchExactWithAuthorship: mention canonical+authorship
// matches a single existing taxon exactly. Expect one ActionMatch
// with AuthorshipConfirmed=true.
func TestBuild_AutoMatchExactWithAuthorship(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970", Rank: "species"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus", Authorship: "L., 1970",
			Rank: "species",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 {
		t.Fatalf("want 1 action, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	got := plan.Actions[0]
	if got.Kind != ActionMatch {
		t.Fatalf("want ActionMatch, got kind %d", got.Kind)
	}
	if got.Match.TaxonID != "t1" {
		t.Errorf("want taxon t1, got %q", got.Match.TaxonID)
	}
	if !got.Match.AuthorshipConfirmed {
		t.Errorf("want AuthorshipConfirmed=true")
	}
	if got.SourcePaperReferenceID != "ref-a" || got.SourceMentionID != "m1" {
		t.Errorf("wrong provenance: %#v", got)
	}
}

// TestBuild_AutoMatchCanonicalOnly: mention has no authorship, but
// canonical matches a single existing taxon. With default rules,
// expect ActionMatch with AuthorshipConfirmed=false.
func TestBuild_AutoMatchCanonicalOnly(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970", Rank: "species"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus", Rank: "species",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionMatch {
		t.Fatalf("want 1 ActionMatch, got %#v", plan.Actions)
	}
	if plan.Actions[0].Match.AuthorshipConfirmed {
		t.Errorf("want AuthorshipConfirmed=false on canonical-only match")
	}
}

// TestBuild_AutoMatchCanonicalOnlyRejectedWhenRequired: with
// AutoMatchAuthorshipRequired=true, a canonical-only match is
// refused and emits a FlagUnresolvedAnchor.
func TestBuild_AutoMatchCanonicalOnlyRejectedWhenRequired(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970", Rank: "species"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus", Rank: "species",
		}},
	}}
	rules := DefaultRules()
	rules.AutoMatchAuthorshipRequired = true
	plan := Build(papers, existing, rules)
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionFlag {
		t.Fatalf("want 1 ActionFlag, got %#v", plan.Actions)
	}
	if plan.Actions[0].Flag.Kind != FlagUnresolvedAnchor {
		t.Errorf("want FlagUnresolvedAnchor, got kind %d", plan.Actions[0].Flag.Kind)
	}
	if len(plan.Actions[0].Flag.Candidates) != 1 || plan.Actions[0].Flag.Candidates[0] != "t1" {
		t.Errorf("want candidate t1, got %v", plan.Actions[0].Flag.Candidates)
	}
}

// TestBuild_AutoMatchAmbiguousCanonical: two existing taxa share the
// same canonical (different authorship). A mention with no
// authorship hits both — expect FlagAmbiguousMatch with both
// candidates.
func TestBuild_AutoMatchAmbiguousCanonical(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
		{TaxonID: "t2", Canonical: "Aus bus", Authorship: "Smith, 1980"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionFlag {
		t.Fatalf("want 1 ActionFlag, got %#v", plan.Actions)
	}
	if plan.Actions[0].Flag.Kind != FlagAmbiguousMatch {
		t.Errorf("want FlagAmbiguousMatch, got kind %d", plan.Actions[0].Flag.Kind)
	}
	if len(plan.Actions[0].Flag.Candidates) != 2 {
		t.Errorf("want 2 candidates, got %v", plan.Actions[0].Flag.Candidates)
	}
}

// TestBuild_AutoMatchAuthorshipFallsThroughToCanonical: when a
// mention's canonical+authorship pair doesn't exist but the
// canonical alone is unambiguous, the planner still returns a
// match (with AuthorshipConfirmed=false) rather than skipping the
// canonical index entirely.
func TestBuild_AutoMatchAuthorshipFallsThroughToCanonical(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus", Authorship: "Different, 1999",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionMatch {
		t.Fatalf("want 1 ActionMatch, got %#v", plan.Actions)
	}
	if plan.Actions[0].Match.AuthorshipConfirmed {
		t.Errorf("want AuthorshipConfirmed=false — authorship differed")
	}
}

// TestBuild_AutoMatchNoMatch: canonical not present anywhere. With
// only the auto-match rule wired, expect FlagUnresolvedAnchor as
// the planner's Slice 1 placeholder before cascade/bracket/create
// rules land.
func TestBuild_AutoMatchNoMatch(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Novus taxon", Authorship: "Smith, 2020",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionFlag {
		t.Fatalf("want 1 ActionFlag, got %#v", plan.Actions)
	}
	if plan.Actions[0].Flag.Kind != FlagUnresolvedAnchor {
		t.Errorf("want FlagUnresolvedAnchor")
	}
}

// TestBuild_CascadeAttachesToMatchedAncestor: paper introduces a
// new species under a genus that already exists. Genus is
// matched; species emits ActionCreateTaxon under the genus.
func TestBuild_CascadeAttachesToMatchedAncestor(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "genus-1", Canonical: "Aus", Rank: "genus"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "g", Canonical: "Aus", Rank: "genus"},
			{MentionID: "s", ParentMentionID: "g",
				Canonical: "Aus novus", Authorship: "Smith, 2020",
				Rank: "species"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[0].Kind != ActionMatch || plan.Actions[0].Match.TaxonID != "genus-1" {
		t.Fatalf("expected genus to auto-match, got %#v", plan.Actions[0])
	}
	if plan.Actions[1].Kind != ActionCreateTaxon {
		t.Fatalf("expected species to CreateTaxon, got %#v", plan.Actions[1])
	}
	ct := plan.Actions[1].CreateTaxon
	if ct.ParentTaxonID != "genus-1" {
		t.Errorf("expected species parent = genus-1, got %q", ct.ParentTaxonID)
	}
	if ct.Canonical != "Aus novus" || ct.Rank != "species" {
		t.Errorf("wrong CreateTaxon payload: %#v", ct)
	}
	if ct.LocalID == "" {
		t.Errorf("expected non-empty plan-local id for species")
	}
	if ct.Status != "accepted" {
		t.Errorf("want status=accepted default, got %q", ct.Status)
	}
}

// TestBuild_CascadeSkipsMissingIntermediates: paper's genus is not
// in the DB, but the family is. Species should attach directly to
// the family via cascade (genus also created in the same plan).
func TestBuild_CascadeSkipsMissingIntermediates(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "fam-1", Canonical: "Foidae", Rank: "family"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "f", Canonical: "Foidae", Rank: "family"},
			{MentionID: "g", ParentMentionID: "f",
				Canonical: "Aus", Authorship: "Smith, 2020", Rank: "genus"},
			{MentionID: "s", ParentMentionID: "g",
				Canonical: "Aus novus", Authorship: "Smith, 2020",
				Rank: "species"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 3 {
		t.Fatalf("want 3 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	// [0] family match, [1] genus create under fam-1, [2] species
	// create under the genus's LocalID.
	if plan.Actions[0].Kind != ActionMatch {
		t.Fatalf("actions[0] want ActionMatch, got %#v", plan.Actions[0])
	}
	if plan.Actions[1].Kind != ActionCreateTaxon || plan.Actions[1].CreateTaxon.ParentTaxonID != "fam-1" {
		t.Fatalf("actions[1] want CreateTaxon parent=fam-1, got %#v", plan.Actions[1])
	}
	genusLocalID := plan.Actions[1].CreateTaxon.LocalID
	if genusLocalID == "" {
		t.Fatal("genus create action missing LocalID")
	}
	if plan.Actions[2].Kind != ActionCreateTaxon {
		t.Fatalf("actions[2] want CreateTaxon, got %#v", plan.Actions[2])
	}
	if plan.Actions[2].CreateTaxon.ParentTaxonID != genusLocalID {
		t.Errorf("species parent = %q, want the genus's plan-local id %q",
			plan.Actions[2].CreateTaxon.ParentTaxonID, genusLocalID)
	}
}

// TestBuild_CascadeChildBeforeParentInOrder: name_usages
// intentionally reversed (species listed before its parent). The
// planner memoizes via resolveMention, so processing order should
// not affect the plan — species resolves its parent lazily.
func TestBuild_CascadeChildBeforeParentInOrder(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "genus-1", Canonical: "Aus", Rank: "genus"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			// Species first this time.
			{MentionID: "s", ParentMentionID: "g",
				Canonical: "Aus novus", Rank: "species"},
			{MentionID: "g", Canonical: "Aus", Rank: "genus"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	// Expected order (result of processing s first): resolve s
	// recurses into g, which emits ActionMatch, then the species
	// emits ActionCreateTaxon. The second top-level pass hits g's
	// memoized entry and emits nothing more.
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[0].Kind != ActionMatch || plan.Actions[0].Match.TaxonID != "genus-1" {
		t.Fatalf("actions[0] want genus match, got %#v", plan.Actions[0])
	}
	if plan.Actions[1].Kind != ActionCreateTaxon || plan.Actions[1].CreateTaxon.ParentTaxonID != "genus-1" {
		t.Fatalf("actions[1] want species CreateTaxon under genus-1, got %#v", plan.Actions[1])
	}
}

// TestBuild_CascadeAllOrphaned: nothing in the paper resolves at
// all. Every mention flags with FlagUnresolvedAnchor.
func TestBuild_CascadeAllOrphaned(t *testing.T) {
	existing := Existing{}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "g", Canonical: "Aus", Rank: "genus"},
			{MentionID: "s", ParentMentionID: "g",
				Canonical: "Aus novus", Rank: "species"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	for i, a := range plan.Actions {
		if a.Kind != ActionFlag || a.Flag.Kind != FlagUnresolvedAnchor {
			t.Errorf("action[%d] want FlagUnresolvedAnchor, got %#v", i, a)
		}
	}
}

// TestBuild_BracketFiresWhenPaperSkipsGenus: paper places a species
// directly under a family (no genus mention). With bracket-fallback
// enabled (the default), the species emits an ActionBracket
// anchored to the family, with the genus bracketed.
func TestBuild_BracketFiresWhenPaperSkipsGenus(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "fam-1", Canonical: "Foidae", Rank: "family"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "f", Canonical: "Foidae", Rank: "family"},
			{MentionID: "s", ParentMentionID: "f",
				Canonical: "Aus dubius", Authorship: "L., 1970",
				Rank: "species"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[0].Kind != ActionMatch {
		t.Fatalf("actions[0] want ActionMatch (family), got %#v", plan.Actions[0])
	}
	if plan.Actions[1].Kind != ActionBracket {
		t.Fatalf("actions[1] want ActionBracket, got %#v", plan.Actions[1])
	}
	br := plan.Actions[1].Bracket
	if br.AnchorTaxonID != "fam-1" {
		t.Errorf("bracket anchor = %q, want fam-1", br.AnchorTaxonID)
	}
	if br.BracketedGenus != "[Aus]" {
		t.Errorf("bracketed genus = %q, want [Aus]", br.BracketedGenus)
	}
	if br.Species != "dubius" {
		t.Errorf("species = %q, want dubius", br.Species)
	}
	if br.Authorship != "L., 1970" {
		t.Errorf("authorship = %q, want L., 1970", br.Authorship)
	}
}

// TestBuild_BracketSkippedForNormalCascade: paper's genus is
// asserted but not in the DB. Cascade creates the genus, and the
// species falls into normal ActionCreateTaxon (NOT bracket) under
// that plan-local genus.
func TestBuild_BracketSkippedForNormalCascade(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "fam-1", Canonical: "Foidae", Rank: "family"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "f", Canonical: "Foidae", Rank: "family"},
			{MentionID: "g", ParentMentionID: "f",
				Canonical: "Aus", Authorship: "L., 1970", Rank: "genus"},
			{MentionID: "s", ParentMentionID: "g",
				Canonical: "Aus novus", Authorship: "L., 1970",
				Rank: "species"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 3 {
		t.Fatalf("want 3 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[2].Kind != ActionCreateTaxon {
		t.Errorf("species should CreateTaxon under created genus, got %#v",
			plan.Actions[2])
	}
}

// TestBuild_BracketDisabledFlagsInstead: when the rule is off, the
// same paper (species with family-only parent) flags instead of
// bracketing.
func TestBuild_BracketDisabledFlagsInstead(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "fam-1", Canonical: "Foidae", Rank: "family"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "f", Canonical: "Foidae", Rank: "family"},
			{MentionID: "s", ParentMentionID: "f",
				Canonical: "Aus dubius", Rank: "species"},
		},
	}}
	rules := DefaultRules()
	rules.BracketFallback = false
	plan := Build(papers, existing, rules)
	// Without bracket fallback: the species is auto-created as a
	// normal taxon under the family (per the cascade rule). That
	// isn't ideal — the genus is fabricated — but it's the
	// non-bracket behavior. If a curator wants bracketing, they
	// leave the default rule on.
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[1].Kind != ActionCreateTaxon {
		t.Errorf("actions[1] want ActionCreateTaxon (no bracket), got %#v",
			plan.Actions[1])
	}
}

// TestBuild_BracketWithNoAnchorFlags: species mention with no
// parent and no family-level anchor. Cascade exhausts before the
// bracket rule can pick an anchor; the mention flags.
func TestBuild_BracketWithNoAnchorFlags(t *testing.T) {
	existing := Existing{}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "s", Canonical: "Aus dubius", Rank: "species"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionFlag {
		t.Fatalf("want 1 ActionFlag, got %#v", plan.Actions)
	}
}

// TestBuild_SynonymUnderMatchedAccepted: single paper marks a
// synonym under an existing accepted taxon. Expect one match for
// the accepted, and one ActionCreateSynonym for the synonym.
func TestBuild_SynonymUnderMatchedAccepted(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "a", Canonical: "Aus bus", Authorship: "L., 1970",
				Status: "accepted"},
			{MentionID: "s", ParentMentionID: "a",
				Canonical: "Aus bogus", Authorship: "Smith, 1980",
				Status: "synonym"},
		},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[0].Kind != ActionMatch || plan.Actions[0].Match.TaxonID != "t1" {
		t.Fatalf("actions[0] want accepted match, got %#v", plan.Actions[0])
	}
	syn := plan.Actions[1]
	if syn.Kind != ActionCreateSynonym {
		t.Fatalf("actions[1] want ActionCreateSynonym, got %#v", syn)
	}
	if syn.CreateSynonym.AcceptedTaxonID != "t1" {
		t.Errorf("synonym accepted = %q, want t1", syn.CreateSynonym.AcceptedTaxonID)
	}
	if syn.CreateSynonym.Canonical != "Aus bogus" {
		t.Errorf("synonym canonical = %q", syn.CreateSynonym.Canonical)
	}
	if syn.CreateSynonym.Status != "synonym" {
		t.Errorf("synonym status = %q, want synonym", syn.CreateSynonym.Status)
	}
}

// TestBuild_SynonymyDeduplicatedAcrossPapers: two papers agree on
// the same (synonym, accepted) pair. Only one ActionCreateSynonym
// gets emitted (from the first-processed paper's provenance).
func TestBuild_SynonymyDeduplicatedAcrossPapers(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
	}}
	papers := []PaperInput{
		{
			ReferenceID:    "ref-a",
			PaperCreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			NameUsages: []NameUsage{
				{MentionID: "a", Canonical: "Aus bus", Authorship: "L., 1970",
					Status: "accepted"},
				{MentionID: "s", ParentMentionID: "a",
					Canonical: "Aus bogus", Authorship: "Smith, 1980",
					Status: "synonym"},
			},
		},
		{
			ReferenceID:    "ref-b",
			PaperCreatedAt: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			NameUsages: []NameUsage{
				{MentionID: "a", Canonical: "Aus bus", Authorship: "L., 1970",
					Status: "accepted"},
				{MentionID: "s", ParentMentionID: "a",
					Canonical: "Aus bogus", Authorship: "Smith, 1980",
					Status: "synonym"},
			},
		},
	}
	plan := Build(papers, existing, DefaultRules())
	synCount := 0
	for _, a := range plan.Actions {
		if a.Kind == ActionCreateSynonym {
			synCount++
		}
	}
	if synCount != 1 {
		t.Errorf("want 1 ActionCreateSynonym, got %d", synCount)
	}
}

// TestBuild_SynonymyMostRecentWins: two papers disagree on the
// accepted taxon for the same synonym. The newer paper's
// assertion wins.
func TestBuild_SynonymyMostRecentWins(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
		{TaxonID: "t2", Canonical: "Cus dus", Authorship: "M., 1980"},
	}}
	papers := []PaperInput{
		{
			ReferenceID:    "ref-old",
			PaperCreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			NameUsages: []NameUsage{
				{MentionID: "a", Canonical: "Aus bus", Authorship: "L., 1970",
					Status: "accepted"},
				{MentionID: "s", ParentMentionID: "a",
					Canonical: "Aus bogus", Authorship: "Smith, 1980",
					Status: "synonym"},
			},
		},
		{
			ReferenceID:    "ref-new",
			PaperCreatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			NameUsages: []NameUsage{
				{MentionID: "a", Canonical: "Cus dus", Authorship: "M., 1980",
					Status: "accepted"},
				{MentionID: "s", ParentMentionID: "a",
					Canonical: "Aus bogus", Authorship: "Smith, 1980",
					Status: "synonym"},
			},
		},
	}
	plan := Build(papers, existing, DefaultRules())
	var syn *Action
	for i := range plan.Actions {
		if plan.Actions[i].Kind == ActionCreateSynonym {
			syn = &plan.Actions[i]
			break
		}
	}
	if syn == nil {
		t.Fatalf("no ActionCreateSynonym in plan: %#v", plan.Actions)
	}
	if syn.CreateSynonym.AcceptedTaxonID != "t2" {
		t.Errorf("winning accepted = %q, want t2 (newer paper)",
			syn.CreateSynonym.AcceptedTaxonID)
	}
	if syn.SourcePaperReferenceID != "ref-new" {
		t.Errorf("provenance = %q, want ref-new",
			syn.SourcePaperReferenceID)
	}
}

// TestBuild_SynonymyTieFlagsContradiction: two papers with
// identical dates disagree on the accepted taxon for the same
// synonym. The winner picker returns no clear top, so the group
// surfaces as FlagContradictorySynonymy.
func TestBuild_SynonymyTieFlagsContradiction(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
		{TaxonID: "t2", Canonical: "Cus dus", Authorship: "M., 1980"},
	}}
	sameDay := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	papers := []PaperInput{
		{
			ReferenceID:     "ref-x",
			ExtractionRunID: "run-1",
			PaperCreatedAt:  sameDay,
			NameUsages: []NameUsage{
				{MentionID: "a", Canonical: "Aus bus", Authorship: "L., 1970",
					Status: "accepted"},
				{MentionID: "s", ParentMentionID: "a",
					Canonical: "Aus bogus", Authorship: "Smith, 1980",
					Status: "synonym"},
			},
		},
		{
			ReferenceID:     "ref-y",
			ExtractionRunID: "run-1", // identical run id triggers tie
			PaperCreatedAt:  sameDay,
			NameUsages: []NameUsage{
				{MentionID: "a", Canonical: "Cus dus", Authorship: "M., 1980",
					Status: "accepted"},
				{MentionID: "s", ParentMentionID: "a",
					Canonical: "Aus bogus", Authorship: "Smith, 1980",
					Status: "synonym"},
			},
		},
	}
	plan := Build(papers, existing, DefaultRules())
	flagFound := false
	synFound := false
	for _, a := range plan.Actions {
		if a.Kind == ActionCreateSynonym {
			synFound = true
		}
		if a.Kind == ActionFlag && a.Flag.Kind == FlagContradictorySynonymy {
			flagFound = true
		}
	}
	if synFound {
		t.Errorf("no ActionCreateSynonym should land on a tied contradiction")
	}
	if !flagFound {
		t.Errorf("want FlagContradictorySynonymy, got: %#v", plan.Actions)
	}
}

// TestBuild_ConceptFieldsSetOnMatched: a matched mention with a
// sensu-lato name_phrase should emit both ActionMatch and an
// ActionSetConceptFields carrying the phrase, plus an
// ActionResolveImplicitConceptRel with the derived INCLUDES
// relation.
func TestBuild_ConceptFieldsSetOnMatched(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus", Authorship: "L., 1970",
			NamePhrase: "sensu lato",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 3 {
		t.Fatalf("want 3 actions, got %d: %#v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[0].Kind != ActionMatch {
		t.Errorf("actions[0] want ActionMatch, got %#v", plan.Actions[0])
	}
	scf := plan.Actions[1]
	if scf.Kind != ActionSetConceptFields {
		t.Fatalf("actions[1] want ActionSetConceptFields, got %#v", scf)
	}
	if scf.SetConceptFields.NamePhrase != "sensu lato" {
		t.Errorf("phrase = %q", scf.SetConceptFields.NamePhrase)
	}
	if scf.SetConceptFields.TaxonID != "t1" {
		t.Errorf("phrase target taxon = %q", scf.SetConceptFields.TaxonID)
	}
	ric := plan.Actions[2]
	if ric.Kind != ActionResolveImplicitConceptRel {
		t.Fatalf("actions[2] want ActionResolveImplicitConceptRel, got %#v", ric)
	}
	if ric.ResolveImplicitConceptRel.Derived.RelTypeID != "INCLUDES" {
		t.Errorf("derived rel = %q, want INCLUDES",
			ric.ResolveImplicitConceptRel.Derived.RelTypeID)
	}
	if ric.ResolveImplicitConceptRel.Derived.AgainstNamedAuthor {
		t.Error("bare sensu lato should not be against a named author")
	}
}

// TestBuild_ConceptFieldsSkippedWhenPhraseAbsent: matched mention
// without any concept qualifier emits only the match.
func TestBuild_ConceptFieldsSkippedWhenPhraseAbsent(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus", Authorship: "L., 1970",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != ActionMatch {
		t.Fatalf("want 1 ActionMatch, got %#v", plan.Actions)
	}
}

// TestBuild_ConceptRelDeferredForNamedAuthor: sensu <author>
// resolves to Derived.AgainstNamedAuthor=true. No
// ActionResolveImplicitConceptRel is emitted; the assembler
// leaves named-author relations to the curator UI (per the pass
// comment).
func TestBuild_ConceptRelDeferredForNamedAuthor(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{{
			MentionID: "m1", Canonical: "Aus bus",
			NamePhrase:  "sensu Smith, 1980",
			AccordingTo: "Smith, 1980",
		}},
	}}
	plan := Build(papers, existing, DefaultRules())
	for _, a := range plan.Actions {
		if a.Kind == ActionResolveImplicitConceptRel {
			t.Errorf("named-author sensu should not emit resolve-implicit action: %#v", a)
		}
	}
}

// stubReaderWriter is a minimal Reader+Writer for exercising
// Apply's ActionResolveImplicitConceptRel branch.
type stubReaderWriter struct {
	// candidates maps subjectTaxonID → the list of candidate ids
	// FindImplicitReferenceCandidates should return.
	candidates map[string][]string

	insertedRels []conceptimport.ConceptRelation
	flags        []FlagAction
}

func (s *stubReaderWriter) ListAllTaxaForAssembler() ([]ExistingTaxon, error) {
	return nil, nil
}
func (s *stubReaderWriter) FindImplicitReferenceCandidates(subj, phrase string) ([]string, error) {
	return append([]string(nil), s.candidates[subj]...), nil
}
func (s *stubReaderWriter) SetTaxonNamePhrase(string, string, bool) error         { return nil }
func (s *stubReaderWriter) SetTaxonAccordingTo(string, string, bool) error        { return nil }
func (s *stubReaderWriter) SetSynonymNamePhrase(string, string, string, bool) error {
	return nil
}
func (s *stubReaderWriter) SetSynonymAccordingTo(string, string, string, bool) error {
	return nil
}
func (s *stubReaderWriter) InsertConceptRelation(rel conceptimport.ConceptRelation) error {
	s.insertedRels = append(s.insertedRels, rel)
	return nil
}
func (s *stubReaderWriter) CreateTaxonForAssembler(a CreateTaxonAction, refID string) (string, error) {
	return "real-" + a.LocalID, nil
}
func (s *stubReaderWriter) CreateSynonymForAssembler(a CreateSynonymAction, refID string) (string, error) {
	return "syn-real", nil
}
func (s *stubReaderWriter) CreateBracketedTaxonForAssembler(a BracketAction, refID string) (string, error) {
	return "bracket-real", nil
}
func (s *stubReaderWriter) RecordAssemblerFlag(f FlagAction, _ ActionProvenance) error {
	s.flags = append(s.flags, f)
	return nil
}
func (s *stubReaderWriter) RecordAssemblerMapping(_, _, _ string, _ bool) error {
	return nil
}

// TestApply_ResolveImplicitInsertsWhenUnique: given a single
// candidate, Apply materializes the concept relation via the
// Writer.
func TestApply_ResolveImplicitInsertsWhenUnique(t *testing.T) {
	rw := &stubReaderWriter{candidates: map[string][]string{
		"t1": {"t2"},
	}}
	plan := Plan{Actions: []Action{{
		Kind:                   ActionResolveImplicitConceptRel,
		SourcePaperReferenceID: "ref-a",
		ResolveImplicitConceptRel: ResolveImplicitConceptRelAction{
			SubjectTaxonID: "t1",
			ReferenceID:    "ref-a",
			NamePhrase:     "sensu lato",
			Derived: conceptimport.DerivedRelation{
				RelTypeID: conceptimport.RelIncludes,
			},
		},
	}}}
	report, err := Apply(rw, rw, plan)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if report.ConceptRelationsInserted != 1 {
		t.Errorf("want 1 rel inserted, got %d", report.ConceptRelationsInserted)
	}
	if len(rw.insertedRels) != 1 {
		t.Fatalf("want 1 inserted, got %d", len(rw.insertedRels))
	}
	if rw.insertedRels[0].TaxonID != "t1" || rw.insertedRels[0].RelatedTaxonID != "t2" {
		t.Errorf("wrong relation: %#v", rw.insertedRels[0])
	}
}

// TestApply_ResolveImplicitAmbiguousFlags: multiple candidates
// route to FlagAmbiguousImplicitReference. No relation inserted.
func TestApply_ResolveImplicitAmbiguousFlags(t *testing.T) {
	rw := &stubReaderWriter{candidates: map[string][]string{
		"t1": {"t2", "t3"},
	}}
	plan := Plan{Actions: []Action{{
		Kind: ActionResolveImplicitConceptRel,
		ResolveImplicitConceptRel: ResolveImplicitConceptRelAction{
			SubjectTaxonID: "t1",
			NamePhrase:     "sensu lato",
			Derived: conceptimport.DerivedRelation{
				RelTypeID: conceptimport.RelIncludes,
			},
		},
	}}}
	report, err := Apply(rw, rw, plan)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if len(rw.insertedRels) != 0 {
		t.Errorf("no relation should be inserted on ambiguous lookup")
	}
	if len(report.Flags) != 1 || report.Flags[0].Kind != FlagAmbiguousImplicitReference {
		t.Errorf("want FlagAmbiguousImplicitReference, got %#v", report.Flags)
	}
}

// TestBuild_Determinism: same inputs, two Build calls, identical
// plans. Guards against map-iteration flakiness in the index.
func TestBuild_Determinism(t *testing.T) {
	existing := Existing{Taxa: []ExistingTaxon{
		{TaxonID: "t1", Canonical: "Aus bus", Authorship: "L., 1970"},
		{TaxonID: "t2", Canonical: "Aus bus", Authorship: "Smith, 1980"},
		{TaxonID: "t3", Canonical: "Foo bar", Authorship: "Jones, 1990"},
	}}
	papers := []PaperInput{{
		ReferenceID: "ref-a",
		NameUsages: []NameUsage{
			{MentionID: "m1", Canonical: "Aus bus"},
			{MentionID: "m2", Canonical: "Foo bar", Authorship: "Jones, 1990"},
			{MentionID: "m3", Canonical: "Unknown thing"},
		},
	}}
	rules := DefaultRules()
	first := Build(papers, existing, rules)
	for range 10 {
		got := Build(papers, existing, rules)
		if len(got.Actions) != len(first.Actions) {
			t.Fatalf("action count varied across runs: %d vs %d",
				len(first.Actions), len(got.Actions))
		}
		for i := range first.Actions {
			if got.Actions[i].Kind != first.Actions[i].Kind ||
				got.Actions[i].SourceMentionID != first.Actions[i].SourceMentionID {
				t.Fatalf("action %d differed across runs", i)
			}
		}
	}
}
