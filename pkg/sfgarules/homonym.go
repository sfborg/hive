package sfgarules

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/gdower/gsvalidator/domain"
)

// HomonymValidator detects homonyms - when the same name is used for different taxa.
// This is important in nomenclature as homonyms can cause confusion and may require
// replacement names.
type HomonymValidator struct{}

// Name returns the validator type identifier.
func (v *HomonymValidator) Name() string {
	return "sfga_homonym"
}

// Validate checks if this name is a homonym (same name exists with different authorship).
func (v *HomonymValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get the scientific name
	scientificName, exists := ctx.GetFieldString("col__scientific_name")
	if !exists || scientificName == "" {
		result.Passed = false
		result.Message = "Cannot check for homonyms: scientific name is missing"
		return result, nil
	}

	// Get the rank and code for filtering
	rank, _ := ctx.GetFieldString("col__rank_id")
	code, _ := ctx.GetFieldString("col__code_id")

	// Get homonym scope from parameters (default: same_code_and_rank)
	scope := "same_code_and_rank"
	if s, exists := rule.Parameters["scope"].(string); exists {
		scope = s
	}

	// Check for homonyms
	homonyms, err := v.findHomonyms(ctx, scientificName, rank, code, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to check for homonyms: %w", err)
	}

	if len(homonyms) > 0 {
		result.Passed = false

		// Build message with homonym details
		homonymInfo := make([]string, len(homonyms))
		for i, h := range homonyms {
			homonymInfo[i] = fmt.Sprintf("%s %s (ID: %s)", h["col__scientific_name"], h["col__authorship"], h["id"])
		}
		result.Message = fmt.Sprintf("%s. Found: %s", rule.Message(), strings.Join(homonymInfo, "; "))
		result.ActualValue = fmt.Sprintf("Found %d potential homonym(s)", len(homonyms))
		result.ExpectedValue = "unique name within scope"
	} else {
		result.Passed = true
		result.Message = "No homonyms detected"
		result.ActualValue = scientificName
	}

	return result, nil
}

// findHomonyms searches for names with the same scientific name but different records.
func (v *HomonymValidator) findHomonyms(
	ctx *domain.ValidationContext,
	scientificName string,
	rank string,
	code string,
	scope string,
) ([]map[string]interface{}, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("homonyms_%s_%s_%s_%s", scientificName, rank, code, scope)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists {
		return cached, nil
	}

	// Build query based on scope
	var query string
	var args []interface{}

	switch scope {
	case "same_code_and_rank":
		// Most strict: same name, rank, and nomenclatural code
		query = `
			SELECT id, col__scientific_name, col__authorship, col__rank_id, col__code_id
			FROM name
			WHERE col__scientific_name = ?
			  AND col__rank_id = ?
			  AND col__code_id = ?
			  AND id != ?
		`
		args = []interface{}{scientificName, rank, code, ctx.RecordID}

	case "same_code":
		// Same name and code, any rank
		query = `
			SELECT id, col__scientific_name, col__authorship, col__rank_id, col__code_id
			FROM name
			WHERE col__scientific_name = ?
			  AND col__code_id = ?
			  AND id != ?
		`
		args = []interface{}{scientificName, code, ctx.RecordID}

	case "any":
		// Most permissive: just same name
		query = `
			SELECT id, col__scientific_name, col__authorship, col__rank_id, col__code_id
			FROM name
			WHERE col__scientific_name = ?
			  AND id != ?
		`
		args = []interface{}{scientificName, ctx.RecordID}

	default:
		return nil, fmt.Errorf("unsupported homonym scope: %s", scope)
	}

	// Execute query
	rows, err := ctx.DB.QueryContext(ctx.Ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect results
	var homonyms []map[string]interface{}
	for rows.Next() {
		var id, name, authorship, rankID, codeID string
		var authorshipNullable sql.NullString

		if err := rows.Scan(&id, &name, &authorshipNullable, &rankID, &codeID); err != nil {
			return nil, err
		}

		if authorshipNullable.Valid {
			authorship = authorshipNullable.String
		} else {
			authorship = ""
		}

		homonyms = append(homonyms, map[string]interface{}{
			"id":                   id,
			"col__scientific_name": name,
			"col__authorship":      authorship,
			"col__rank_id":         rankID,
			"col__code_id":         codeID,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cache results
	ctx.SetRelatedRecords(cacheKey, homonyms)

	return homonyms, nil
}

// CanAutoFix returns false (homonyms cannot be auto-fixed).
func (v *HomonymValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for homonym validator.
func (v *HomonymValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
