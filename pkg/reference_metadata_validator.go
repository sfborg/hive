package hive

import (
	"github.com/sfborg/gsvalidator/domain"
)

// referenceMetadataValidator flags reference rows whose free-text
// citation string is populated but whose structured atomized fields
// (author, issued) are empty. Common on CoL-derived data where the
// import path populated only the citation and left the atomized
// columns NULL. Blocks downstream flows that read structured metadata:
//
//   - citation-pick backfill in the WUI's create/edit taxon form
//     (basionym_year / combination_year pull from ref.author + ref.issued).
//   - hive_combination_matches_basionym rule and other author-year
//     comparisons.
//   - CoLDP export shape and any tool that reasons about publication
//     date or authorship independently of the free-text citation.
//
// Soft-warn only — some legacy corpora carry citation-only references
// intentionally and hive shouldn't block a save on the pattern.
// Curators fix them via the reference-quick-fix modal that opens
// from the citation picker's warning badge.
//
// Rule fires when col__citation is non-empty AND EITHER col__author
// is empty OR col__issued is empty. Both-empty means the reference is
// a completely-empty stub — a different problem, not this rule's
// scope. Both-populated means the reference is structurally complete.
type referenceMetadataValidator struct{}

func newReferenceMetadataValidator() *referenceMetadataValidator {
	return &referenceMetadataValidator{}
}

func (v *referenceMetadataValidator) Name() string {
	return "hive.reference_missing_structured_metadata"
}

func (v *referenceMetadataValidator) CanAutoFix() bool { return false }

func (v *referenceMetadataValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *referenceMetadataValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	citation, _ := ctx.GetFieldString("col__citation")
	author, _ := ctx.GetFieldString("col__author")
	issued, _ := ctx.GetFieldString("col__issued")

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

	// Nothing to check: empty citation OR both atomized fields populated.
	if citation == "" {
		return pass, nil
	}
	if author != "" && issued != "" {
		return pass, nil
	}

	msg := rule.WarningMessage
	if msg == "" {
		msg = "Reference has a citation but is missing structured metadata."
	}
	missing := "author + issued"
	switch {
	case author == "" && issued != "":
		missing = "author"
	case author != "" && issued == "":
		missing = "issued (year)"
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
		Message:     msg + " Missing: " + missing + ".",
		ActualValue: missing,
	}, nil
}
