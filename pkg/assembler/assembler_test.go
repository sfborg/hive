package assembler

import (
	"testing"
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
