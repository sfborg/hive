package hive

import (
	"fmt"
	"sync"

	"github.com/gnames/gnparser"
	"github.com/gnames/gnparser/ent/parsed"
	"github.com/sfborg/gsvalidator/domain"
)

// infraspecificMarkerValidator flags an infraspecific name whose
// scientific-name string lacks the expected rank marker (var., f.,
// subsp., subvar., subf.). Detection runs the verbatim name through
// gnparser and checks whether the parser recognized any infraspecific
// rank marker; if col__rank_id says the name is infraspecific but
// gnparser found no marker, the rule fires.
//
// Fire matrix (per user guidance):
//
//   - ICN, ICNP, and empty code: any infraspecific rank without a
//     marker fires. ICN/ICNP treat variety, subvariety, form, subform,
//     subspecies as coordinate ranks — the marker is what tells the
//     reader which coordinate level applies.
//   - ICZN + variety/subvariety/form/subform without a marker: fires.
//     ICZN eliminated those ranks in accepted usage, but names described
//     at them survive in synonymy and their historical rank belongs on
//     the record for reference.
//   - ICZN + SUBSPECIES: never fires. Under ICZN, subspecies is the
//     only surviving infraspecific rank, so a bare trinomial is
//     unambiguously a subspecies — the marker is optional.
//   - ICVCN: skipped entirely (no formal subspecies rank).
//
// Detection deliberately goes through gnparser instead of a regex on
// col__scientific_name so all marker variants normalize the same way
// (fm./forma/form → f., ssp. → subsp.) and gnparser's case-sensitive
// filius-vs-forma disambiguation ("f. Smith" → filius author suffix,
// "f. smith" → forma marker) doesn't produce false negatives here.
//
// Auto-fix is deliberately not implemented — hive doesn't currently
// have an auto-fix invocation path (no existing hive validator wires
// one up; gsvalidator's Validator interface defines AutoFix but the
// use-case layer doesn't call it). When that infrastructure lands, the
// natural fix here rebuilds col__scientific_name from atomized epithets
// with the rank-derived marker spliced in between species and
// infraspecific epithets. Manual curator edits via the name form are
// the workaround until then.
//
// The validator owns its own gnparser instance — matches
// parseTailValidator's shape and rationale (see that file).
type infraspecificMarkerValidator struct {
	parser gnparser.GNparser
	mu     sync.Mutex
}

func newInfraspecificMarkerValidator() *infraspecificMarkerValidator {
	return &infraspecificMarkerValidator{
		parser: gnparser.New(gnparser.NewConfig()),
	}
}

func (v *infraspecificMarkerValidator) Name() string {
	return "hive.infraspecific_marker_missing"
}

func (v *infraspecificMarkerValidator) CanAutoFix() bool { return false }

func (v *infraspecificMarkerValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *infraspecificMarkerValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	pass := &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      true,
		Enforcement: domain.EnforcementSoft,
		Severity:    domain.SeverityWarn,
	}

	rank, _ := ctx.GetFieldString("col__rank_id")
	expected := expectedMarkerForRank(rank)
	if expected == "" {
		// Rank isn't infraspecific (or is one of the deferred
		// historical ranks like ABERRATION / RACE / GREX where marker
		// enforcement is out of scope).
		return pass, nil
	}

	code, _ := ctx.GetFieldString("col__code_id")
	if !markerRuleFiresFor(code, rank) {
		return pass, nil
	}

	verbatim, _ := ctx.GetFieldString("col__scientific_name_string")
	if verbatim == "" {
		verbatim, _ = ctx.GetFieldString("col__scientific_name")
	}
	if verbatim == "" {
		// Nothing to check — a name with no scientific string surfaces
		// via other rules (e.g. presence).
		return pass, nil
	}

	p := v.parse(verbatim, code)
	if p.Rank != "" {
		// gnparser recognized a marker — the name has one. Mismatch
		// between the parsed marker and col__rank_id is a separate
		// concern (a follow-up validator could compare the two).
		return pass, nil
	}

	msg := rule.WarningMessage
	if msg == "" {
		msg = "Infraspecific name is missing its rank marker."
	}
	return &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      false,
		Enforcement: domain.EnforcementSoft,
		Severity:    domain.SeverityWarn,
		Message: fmt.Sprintf(
			"%s Expected %q between species and infraspecific epithet for rank %s.",
			msg, expected, rank,
		),
		ActualValue:   verbatim,
		ExpectedValue: expected,
	}, nil
}

// expectedMarkerForRank returns the standardized marker gnparser
// normalizes to for a given sfga rank id. Empty return means "not an
// infraspecific rank this validator covers." The list intentionally
// omits ABERRATION / RACE / GREX / NATIO / MORPH / KLEPTON and other
// historical ranks; marker enforcement for them is deferred. Add
// entries as those ranks become worth enforcing.
func expectedMarkerForRank(rankID string) string {
	switch rankID {
	case "SUBSPECIES":
		return "subsp."
	case "VARIETY":
		return "var."
	case "SUBVARIETY":
		return "subvar."
	case "FORM":
		return "f."
	case "SUBFORM":
		return "subf."
	}
	return ""
}

// markerRuleFiresFor returns true when the (code, rank) pair should
// trigger the missing-marker rule. Skips VIRUS (ICVCN) entirely and
// skips ZOOLOGICAL (ICZN) + SUBSPECIES (only surviving infraspecific
// rank under ICZN; the marker is optional). Everything else fires so
// a missing marker gets flagged for curator attention.
//
// Code IDs match sfga's col__code_id vocab — the sfga stringification
// of gnlib's nomcode.Code (Botanical → "BOTANICAL", Zoological →
// "ZOOLOGICAL", Virus → "VIRUS", Bacterial → "BACTERIAL"), NOT the
// familiar "ICN" / "ICZN" abbreviations.
func markerRuleFiresFor(codeID, rankID string) bool {
	switch codeID {
	case "VIRUS":
		return false
	case "ZOOLOGICAL":
		// var. / f. / subvar. / subf. survive in synonymy and should
		// still carry their historical marker. SUBSPECIES doesn't need
		// one under ICZN.
		return rankID != "SUBSPECIES"
	}
	// BOTANICAL, BACTERIAL, and empty (unknown) code all fire on any
	// infraspecific rank we recognize.
	return true
}

// parse mirrors parseTailValidator.parse — a single serialized entry
// point to the shared gnparser instance with the code option applied
// per-call so ICN vs. ICZN tuning takes effect.
func (v *infraspecificMarkerValidator) parse(verbatim, codeID string) parsed.ParsedFlat {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.parser.
		ChangeConfig(gnparser.OptCode(parseCodeID(codeID))).
		ParseName(verbatim).
		Flatten()
}
