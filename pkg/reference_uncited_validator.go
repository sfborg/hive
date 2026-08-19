package hive

import (
	"fmt"

	"github.com/gdower/gsvalidator/domain"
)

// referenceUncitedValidator fires when a reference row is not cited by
// any of the tables tracked in referenceCitationTables. Info severity —
// an uncited reference isn't broken data; the curator may have added it
// intentionally to cite later, or the previously-citing record was
// deleted. The rule surfaces the row in the Issues view so the curator
// can decide whether to delete it or keep it around for future use.
//
// See feedback_inclusive_terminology for the language choice: "uncited"
// rather than the previous convention "orphaned" reference — clearer
// technically (the reference itself is fine, it just has no incoming
// citations) and avoids the metaphor.
type referenceUncitedValidator struct{}

func newReferenceUncitedValidator() *referenceUncitedValidator {
	return &referenceUncitedValidator{}
}

func (v *referenceUncitedValidator) Name() string {
	return "hive.reference_uncited"
}

func (v *referenceUncitedValidator) CanAutoFix() bool { return false }

func (v *referenceUncitedValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *referenceUncitedValidator) Validate(
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
		Severity:    domain.SeverityInfo,
	}
	if ctx.RecordID == "" {
		return pass, nil
	}
	// Sum citations across every table that FKs into reference. First
	// non-zero count short-circuits — no need to know the exact total,
	// just whether any citation exists.
	for _, dep := range referenceCitationTables {
		var count int
		q := "SELECT COUNT(*) FROM " + dep.table + " WHERE " + dep.col + " = ?"
		if err := ctx.DB.QueryRowContext(ctx.Ctx, q, ctx.RecordID).Scan(&count); err != nil {
			return nil, fmt.Errorf("reference_uncited: count %s.%s: %w", dep.table, dep.col, err)
		}
		if count > 0 {
			return pass, nil
		}
	}
	msg := rule.WarningMessage
	if msg == "" {
		msg = "This reference is not yet cited by any record. Kept in the archive — it may be useful for a future citation."
	}
	return &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      false,
		Enforcement: domain.EnforcementSoft,
		Severity:    domain.SeverityInfo,
		Message:     msg,
	}, nil
}
