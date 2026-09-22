package hive

import (
	"github.com/sfborg/gsvalidator/domain"
)

// speciesInteractionNeedsRelatedValidator fires when a
// species_interaction row has neither col__related_taxon_id nor
// col__related_taxon_scientific_name populated — the row identifies
// no counterpart at all, so the interaction is untethered.
//
// Backstop for FK-off imports (like 3i.db) that can plant rows the
// hive write path would otherwise reject. Fires at hard severity
// because a row with no related taxon and no free-text counterpart
// is unusable — it should either get a related taxon assigned or
// be deleted.
//
// The FK-required side (curators wanting to record only a freeform
// scientific name when the counterpart isn't in the archive) is
// blocked upstream by sfga's schema (col__related_taxon_id is
// NOT NULL with an FK). Once sfga relaxes that, this rule becomes
// the primary enforcement of "at least one identifier."
type speciesInteractionNeedsRelatedValidator struct{}

func newSpeciesInteractionNeedsRelatedValidator() *speciesInteractionNeedsRelatedValidator {
	return &speciesInteractionNeedsRelatedValidator{}
}

func (v *speciesInteractionNeedsRelatedValidator) Name() string {
	return "hive.species_interaction_needs_related"
}

func (v *speciesInteractionNeedsRelatedValidator) CanAutoFix() bool { return false }

func (v *speciesInteractionNeedsRelatedValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *speciesInteractionNeedsRelatedValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	relatedID, _ := ctx.GetFieldString("col__related_taxon_id")
	relatedName, _ := ctx.GetFieldString("col__related_taxon_scientific_name")
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
	if relatedID != "" || relatedName != "" {
		return pass, nil
	}
	msg := rule.WarningMessage
	if msg == "" {
		msg = "Species interaction has neither a related taxon id nor a related-taxon scientific name."
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
