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
