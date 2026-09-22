package hive

import (
	"fmt"

	"github.com/sfborg/gsvalidator/domain"
)

// combinationMatchesBasionymValidator fires when a name row has
// identical (basionym_authorship, basionym_authorship_year) and
// (combination_authorship, combination_authorship_year) pairs — the
// two describe the original author and the recombiner respectively,
// so matching values almost always mean the fields were duplicated by
// mistake (curator copied one pair to the other, or an import
// populated both from the same source).
//
// Rare legitimate case: same author republishes their own species
// into a different genus in the same year. Kept soft-warn so those
// records surface for a curator glance but don't block anything.
//
// Both pairs must be non-empty to fire — empty fields are the
// original combination's normal shape (no recombiner cited) and
// shouldn't count as a match.
//
// Design note: the WUI's citation-pick backfill uses the same
// author+year gate to avoid materializing this pattern in the first
// place (see _backfillCombinationFromCitation in
// internal/wui/dist/app.js). This validator catches legacy rows and
// any import path that skips the WUI.
type combinationMatchesBasionymValidator struct{}

func newCombinationMatchesBasionymValidator() *combinationMatchesBasionymValidator {
	return &combinationMatchesBasionymValidator{}
}

func (v *combinationMatchesBasionymValidator) Name() string {
	return "hive.combination_matches_basionym"
}

func (v *combinationMatchesBasionymValidator) CanAutoFix() bool { return false }

func (v *combinationMatchesBasionymValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *combinationMatchesBasionymValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	combAuthor, _ := ctx.GetFieldString("col__combination_authorship")
	basAuthor, _ := ctx.GetFieldString("col__basionym_authorship")
	combYear, _ := ctx.GetFieldString("col__combination_authorship_year")
	basYear, _ := ctx.GetFieldString("col__basionym_authorship_year")

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

	// Empty-either-side is the original-combination normal — skip.
	if combAuthor == "" || basAuthor == "" {
		return pass, nil
	}
	if combYear == "" || basYear == "" {
		return pass, nil
	}
	if combAuthor != basAuthor || combYear != basYear {
		return pass, nil
	}

	msg := rule.WarningMessage
	if msg == "" {
		msg = "Combination authorship matches basionym authorship."
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
			"%s Both fields hold %q, %s — usually a data-entry mistake (the combination pair describes the recombiner, not the original author).",
			msg, combAuthor, combYear,
		),
		ActualValue: fmt.Sprintf("%s, %s", combAuthor, combYear),
	}, nil
}
