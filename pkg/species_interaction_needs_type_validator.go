package hive

import (
	"github.com/sfborg/gsvalidator/domain"
)

// speciesInteractionNeedsTypeValidator fires when a species_interaction
// row has an empty col__type_id. An interaction with no type identifies
// no meaningful relationship — the row is essentially "Focus X — ??? —
// Related Y", which costs more to interpret later than it's worth to
// keep. Backstops FK-off imports (like 3i.db pre-cleanup) that plant
// rows the hive write path would otherwise reject.
//
// Hard severity: the row is unusable as-is. Curators either pick a
// type (from the vocab, adding a new term via the vocab editor if
// needed) or delete the row.
//
// Companion to speciesInteractionNeedsRelatedValidator — one enforces
// "who is the counterpart," the other enforces "what is the
// relationship."
type speciesInteractionNeedsTypeValidator struct{}

func newSpeciesInteractionNeedsTypeValidator() *speciesInteractionNeedsTypeValidator {
	return &speciesInteractionNeedsTypeValidator{}
}

func (v *speciesInteractionNeedsTypeValidator) Name() string {
	return "hive.species_interaction_needs_type"
}

func (v *speciesInteractionNeedsTypeValidator) CanAutoFix() bool { return false }

func (v *speciesInteractionNeedsTypeValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *speciesInteractionNeedsTypeValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	typeID, _ := ctx.GetFieldString("col__type_id")
	pass := &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      true,
		Enforcement: domain.EnforcementHard,
		Severity:    domain.SeverityError,
	}
	if typeID != "" {
		return pass, nil
	}
	msg := rule.WarningMessage
	if msg == "" {
		msg = "Species interaction row has no interaction type — the relationship is undefined."
	}
	return &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      false,
		Enforcement: domain.EnforcementHard,
		Severity:    domain.SeverityError,
		Message:     msg,
	}, nil
}
