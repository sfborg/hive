package conceptimport

import "testing"

func TestDerive(t *testing.T) {
	cases := []struct {
		name    string
		in      MentionConcept
		wantRel string
		wantAgainstAuthor bool
	}{
		// Bare sensu-family markers → implicit reference concept.
		{"sensu lato", MentionConcept{NamePhrase: "sensu lato"}, RelIncludes, false},
		{"sensu lato mixed case", MentionConcept{NamePhrase: "Sensu Lato"}, RelIncludes, false},
		{"sensu stricto", MentionConcept{NamePhrase: "sensu stricto"}, RelIncludedIn, false},

		// sensu <author> → EQUALS against the named author.
		{"sensu smith 1850", MentionConcept{NamePhrase: "sensu Smith, 1850", AccordingTo: "Smith, 1850"}, RelEquals, true},
		{"sensu multi-author", MentionConcept{NamePhrase: "sensu Smith & Jones 1900", AccordingTo: "Smith & Jones 1900"}, RelEquals, true},

		// auct. / sensu auct. / pro parte → no single clean relation.
		{"bare auct.", MentionConcept{NamePhrase: "auct."}, "", false},
		{"sensu auct.", MentionConcept{NamePhrase: "sensu auct."}, "", false},
		{"pro parte", MentionConcept{NamePhrase: "pro parte"}, "", false},
		{"pro parte pp abbrev", MentionConcept{NamePhrase: "p.p."}, "", false},

		// auct. non <author> → EXCLUDES the named author.
		{"auct. non smith", MentionConcept{NamePhrase: "auct. non Smith, 1850", AccordingTo: "Smith, 1850", NomenclaturalNote: NoteMisapplication}, RelExcludes, true},
		{"auct. non without note", MentionConcept{NamePhrase: "auct. non Jones", AccordingTo: "Jones"}, RelExcludes, true},

		// Standalone nomenclaturalNote fallback.
		{"note only misapplication with author", MentionConcept{NomenclaturalNote: NoteMisapplication, AccordingTo: "Ericksmith, 1974"}, RelExcludes, true},
		{"note only misapplication no author", MentionConcept{NomenclaturalNote: NoteMisapplication}, RelExcludes, false},
		{"note primary homonym alone", MentionConcept{NomenclaturalNote: NotePrimaryHomonym}, "", false},
		{"note ambiguous synonym alone", MentionConcept{NomenclaturalNote: NoteAmbiguousSynonym}, "", false},

		// Empty input.
		{"empty", MentionConcept{}, "", false},
		{"whitespace only phrase", MentionConcept{NamePhrase: "   "}, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Derive(c.in)
			if got.RelTypeID != c.wantRel {
				t.Errorf("RelTypeID: got %q, want %q (input: %+v)", got.RelTypeID, c.wantRel, c.in)
			}
			if got.AgainstNamedAuthor != c.wantAgainstAuthor {
				t.Errorf("AgainstNamedAuthor: got %v, want %v (input: %+v)", got.AgainstNamedAuthor, c.wantAgainstAuthor, c.in)
			}
		})
	}
}

func TestDeriveHasRelation(t *testing.T) {
	if !Derive(MentionConcept{NamePhrase: "sensu lato"}).HasRelation() {
		t.Error("sensu lato should have a relation")
	}
	if Derive(MentionConcept{}).HasRelation() {
		t.Error("empty should not have a relation")
	}
}

func TestResolveImplicitReference(t *testing.T) {
	cases := []struct {
		name             string
		derived          DerivedRelation
		candidateCount   int
		wantOutcome      ImplicitReferenceOutcome
		wantRelInserted  bool
		wantRelType      string
	}{
		{
			"named-author derivation returns none",
			DerivedRelation{RelTypeID: RelEquals, AgainstNamedAuthor: true},
			1, ImplicitReferenceNone, false, "",
		},
		{
			"no relation derived returns none",
			DerivedRelation{RelTypeID: "", AgainstNamedAuthor: false},
			5, ImplicitReferenceNone, false, "",
		},
		{
			"implicit sensu-lato, zero candidates → missing",
			DerivedRelation{RelTypeID: RelIncludes, AgainstNamedAuthor: false},
			0, ImplicitReferenceMissing, false, "",
		},
		{
			"implicit sensu-lato, one candidate → resolved",
			DerivedRelation{RelTypeID: RelIncludes, AgainstNamedAuthor: false},
			1, ImplicitReferenceResolved, true, RelIncludes,
		},
		{
			"implicit sensu-stricto, one candidate → resolved with INCLUDED_IN",
			DerivedRelation{RelTypeID: RelIncludedIn, AgainstNamedAuthor: false},
			1, ImplicitReferenceResolved, true, RelIncludedIn,
		},
		{
			"implicit sensu-lato, three candidates → ambiguous",
			DerivedRelation{RelTypeID: RelIncludes, AgainstNamedAuthor: false},
			3, ImplicitReferenceAmbiguous, false, "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcome, rel := ResolveImplicitReference(
				c.derived, "t-subject", "r-source", "note", c.candidateCount, "t-candidate",
			)
			if outcome != c.wantOutcome {
				t.Errorf("outcome = %v, want %v", outcome, c.wantOutcome)
			}
			if c.wantRelInserted {
				if rel.TaxonID != "t-subject" {
					t.Errorf("TaxonID = %q, want t-subject", rel.TaxonID)
				}
				if rel.RelatedTaxonID != "t-candidate" {
					t.Errorf("RelatedTaxonID = %q, want t-candidate", rel.RelatedTaxonID)
				}
				if rel.RelTypeID != c.wantRelType {
					t.Errorf("RelTypeID = %q, want %q", rel.RelTypeID, c.wantRelType)
				}
			}
		})
	}
}

func TestConceptRelationValidate(t *testing.T) {
	cases := []struct {
		name string
		in   ConceptRelation
		wantErr bool
	}{
		{"ok", ConceptRelation{TaxonID: "a", RelatedTaxonID: "b", RelTypeID: RelEquals}, false},
		{"missing taxon", ConceptRelation{RelatedTaxonID: "b", RelTypeID: RelEquals}, true},
		{"missing related", ConceptRelation{TaxonID: "a", RelTypeID: RelEquals}, true},
		{"same taxon", ConceptRelation{TaxonID: "a", RelatedTaxonID: "a", RelTypeID: RelEquals}, true},
		{"missing reltype", ConceptRelation{TaxonID: "a", RelatedTaxonID: "b"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("got err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}
