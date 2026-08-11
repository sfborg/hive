package sfgarules

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/gdower/gsvalidator/domain"
)

// SourceYearValidator validates that the publication year matches the year in the reference.
type SourceYearValidator struct{}

// Name returns the validator type identifier.
func (v *SourceYearValidator) Name() string {
	return "sfga_source_year"
}

// Validate checks if the year matches the source reference year.
func (v *SourceYearValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get the publication year from the record
	pubYear, exists := ctx.GetFieldString("col__published_in_year")
	if !exists || pubYear == "" {
		result.Passed = true
		result.Message = "No publication year specified"
		return result, nil
	}

	// Get the reference ID
	refID, exists := ctx.GetFieldString("col__published_in_id")
	if !exists || refID == "" {
		result.Passed = true
		result.Message = "No reference specified, cannot verify year"
		return result, nil
	}

	// Query the reference to get its year
	sourceYear, err := v.getSourceYear(ctx, refID)
	if err != nil {
		if err == sql.ErrNoRows {
			result.Passed = false
			result.Message = fmt.Sprintf("Reference with ID '%s' not found", refID)
			return result, nil
		}
		return nil, fmt.Errorf("failed to query reference year: %w", err)
	}

	// Compare years
	if pubYear == sourceYear {
		result.Passed = true
		result.Message = "Publication year matches source reference"
		result.ActualValue = pubYear
	} else {
		result.Passed = false
		result.Message = rule.Message()
		result.ActualValue = pubYear
		result.ExpectedValue = sourceYear
	}

	return result, nil
}

// getSourceYear retrieves the year from the reference table.
func (v *SourceYearValidator) getSourceYear(ctx *domain.ValidationContext, refID string) (string, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("source_year_%s", refID)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists && len(cached) > 0 {
		if year, ok := cached[0]["col__year"].(string); ok {
			return year, nil
		}
	}

	// Query the reference table
	query := `SELECT col__year FROM reference WHERE col__id = ?`
	var year sql.NullString
	err := ctx.DB.QueryRowContext(ctx.Ctx, query, refID).Scan(&year)
	if err != nil {
		return "", err
	}

	var yearStr string
	if year.Valid {
		yearStr = year.String
	}

	// Cache result
	ctx.SetRelatedRecords(cacheKey, []map[string]interface{}{
		{"col__year": yearStr},
	})

	return yearStr, nil
}

// CanAutoFix returns false (source year cannot be auto-fixed).
func (v *SourceYearValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for source year validator.
func (v *SourceYearValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}

// SourceAuthorValidator validates that the authorship matches the author in the reference.
type SourceAuthorValidator struct{}

// Name returns the validator type identifier.
func (v *SourceAuthorValidator) Name() string {
	return "sfga_source_author"
}

// Validate checks if the authorship matches the source reference author.
func (v *SourceAuthorValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get the authorship from the record
	authorship, exists := ctx.GetFieldString("col__authorship")
	if !exists || authorship == "" {
		result.Passed = true
		result.Message = "No authorship specified"
		return result, nil
	}

	// Get the reference ID
	refID, exists := ctx.GetFieldString("col__published_in_id")
	if !exists || refID == "" {
		result.Passed = true
		result.Message = "No reference specified, cannot verify authorship"
		return result, nil
	}

	// Query the reference to get its author
	sourceAuthor, err := v.getSourceAuthor(ctx, refID)
	if err != nil {
		if err == sql.ErrNoRows {
			result.Passed = false
			result.Message = fmt.Sprintf("Reference with ID '%s' not found", refID)
			return result, nil
		}
		return nil, fmt.Errorf("failed to query reference author: %w", err)
	}

	// Compare authors (case-insensitive, trim whitespace)
	// This is a soft comparison as author formats can vary
	if v.normalizeAuthor(authorship) == v.normalizeAuthor(sourceAuthor) {
		result.Passed = true
		result.Message = "Authorship matches source reference"
		result.ActualValue = authorship
	} else {
		result.Passed = false
		result.Message = rule.Message()
		result.ActualValue = authorship
		result.ExpectedValue = sourceAuthor
	}

	return result, nil
}

// getSourceAuthor retrieves the author from the reference table.
func (v *SourceAuthorValidator) getSourceAuthor(ctx *domain.ValidationContext, refID string) (string, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("source_author_%s", refID)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists && len(cached) > 0 {
		if author, ok := cached[0]["col__author"].(string); ok {
			return author, nil
		}
	}

	// Query the reference table
	query := `SELECT col__author FROM reference WHERE col__id = ?`
	var author sql.NullString
	err := ctx.DB.QueryRowContext(ctx.Ctx, query, refID).Scan(&author)
	if err != nil {
		return "", err
	}

	var authorStr string
	if author.Valid {
		authorStr = author.String
	}

	// Cache result
	ctx.SetRelatedRecords(cacheKey, []map[string]interface{}{
		{"col__author": authorStr},
	})

	return authorStr, nil
}

// normalizeAuthor normalizes author strings for comparison.
func (v *SourceAuthorValidator) normalizeAuthor(author string) string {
	// Simple normalization: trim and lowercase
	// More sophisticated normalization could handle abbreviations, etc.
	return strings.TrimSpace(strings.ToLower(author))
}

// CanAutoFix returns false (source author cannot be auto-fixed).
func (v *SourceAuthorValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for source author validator.
func (v *SourceAuthorValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
