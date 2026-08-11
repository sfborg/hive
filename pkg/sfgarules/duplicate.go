package sfgarules

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/gdower/gsvalidator/domain"
)

// DuplicateValidator detects duplicate names within a classification.
// This is different from homonyms - duplicates are exact matches with the same
// authorship and year, indicating potential data entry errors or improper synonymy.
type DuplicateValidator struct{}

// Name returns the validator type identifier.
func (v *DuplicateValidator) Name() string {
	return "sfga_duplicate"
}

// Validate checks if there are duplicate names in the classification.
func (v *DuplicateValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get the scientific name
	scientificName, exists := ctx.GetFieldString("col__scientific_name")
	if !exists || scientificName == "" {
		result.Passed = false
		result.Message = "Cannot check for duplicates: scientific name is missing"
		return result, nil
	}

	// Get authorship and year for more precise duplicate detection
	authorship, _ := ctx.GetFieldString("col__authorship")
	year, _ := ctx.GetFieldString("col__published_in_year")

	// Get scope from parameters (default: same_authorship_and_year)
	scope := "same_authorship_and_year"
	if s, exists := rule.Parameters["scope"].(string); exists {
		scope = s
	}

	// Check for duplicates
	duplicates, err := v.findDuplicates(ctx, scientificName, authorship, year, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to check for duplicates: %w", err)
	}

	if len(duplicates) > 0 {
		result.Passed = false

		// Build message with duplicate details
		duplicateInfo := make([]string, len(duplicates))
		for i, d := range duplicates {
			duplicateInfo[i] = fmt.Sprintf("%s %s %s (ID: %s, Status: %s)",
				d["col__scientific_name"],
				d["col__authorship"],
				d["col__published_in_year"],
				d["id"],
				d["col__status_id"],
			)
		}
		result.Message = fmt.Sprintf("%s. Found: %s", rule.Message(), strings.Join(duplicateInfo, "; "))
		result.ActualValue = fmt.Sprintf("Found %d duplicate(s)", len(duplicates))
		result.ExpectedValue = "unique name in classification"
	} else {
		result.Passed = true
		result.Message = "No duplicates detected"
		result.ActualValue = fmt.Sprintf("%s %s %s", scientificName, authorship, year)
	}

	return result, nil
}

// findDuplicates searches for duplicate names based on the specified scope.
func (v *DuplicateValidator) findDuplicates(
	ctx *domain.ValidationContext,
	scientificName string,
	authorship string,
	year string,
	scope string,
) ([]map[string]interface{}, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("duplicates_%s_%s_%s_%s", scientificName, authorship, year, scope)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists {
		return cached, nil
	}

	// Build query based on scope
	var query string
	var args []interface{}

	switch scope {
	case "same_authorship_and_year":
		// Exact match including authorship and year
		query = `
			SELECT id, col__scientific_name, col__authorship, col__published_in_year, col__status_id
			FROM name
			WHERE col__scientific_name = ?
			  AND col__authorship = ?
			  AND col__published_in_year = ?
			  AND id != ?
		`
		args = []interface{}{scientificName, authorship, year, ctx.RecordID}

	case "same_authorship":
		// Same name and authorship, any year
		query = `
			SELECT id, col__scientific_name, col__authorship, col__published_in_year, col__status_id
			FROM name
			WHERE col__scientific_name = ?
			  AND col__authorship = ?
			  AND id != ?
		`
		args = []interface{}{scientificName, authorship, ctx.RecordID}

	case "same_name_only":
		// Just same scientific name (most permissive)
		query = `
			SELECT id, col__scientific_name, col__authorship, col__published_in_year, col__status_id
			FROM name
			WHERE col__scientific_name = ?
			  AND id != ?
		`
		args = []interface{}{scientificName, ctx.RecordID}

	default:
		return nil, fmt.Errorf("unsupported duplicate scope: %s", scope)
	}

	// Execute query
	rows, err := ctx.DB.QueryContext(ctx.Ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect results
	var duplicates []map[string]interface{}
	for rows.Next() {
		var id, name, status string
		var authorshipVal, yearVal sql.NullString

		if err := rows.Scan(&id, &name, &authorshipVal, &yearVal, &status); err != nil {
			return nil, err
		}

		var auth, yr string
		if authorshipVal.Valid {
			auth = authorshipVal.String
		}
		if yearVal.Valid {
			yr = yearVal.String
		}

		duplicates = append(duplicates, map[string]interface{}{
			"id":                     id,
			"col__scientific_name":   name,
			"col__authorship":        auth,
			"col__published_in_year": yr,
			"col__status_id":         status,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cache results
	ctx.SetRelatedRecords(cacheKey, duplicates)

	return duplicates, nil
}

// CanAutoFix returns false (duplicates cannot be auto-fixed).
func (v *DuplicateValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for duplicate validator.
func (v *DuplicateValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
