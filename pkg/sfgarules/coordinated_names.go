package sfgarules

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/gdower/gsvalidator/domain"
)

// CoordinatedNamesValidator validates consistency between coordinated names.
// Coordinated names are names at different ranks derived from the same root
// (e.g., Felidae, Felinae, Felini from genus Felis).
// They should share the same author, year, and other metadata.
type CoordinatedNamesValidator struct{}

// Name returns the validator type identifier.
func (v *CoordinatedNamesValidator) Name() string {
	return "sfga_coordinated_names"
}

// Validate checks if coordinated names have consistent metadata.
func (v *CoordinatedNamesValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get the field to check from parameters
	fieldToCheck := "col__authorship"
	if f, exists := rule.Parameters["field"].(string); exists {
		fieldToCheck = f
	}

	// Get the scientific name and rank
	scientificName, exists := ctx.GetFieldString("col__scientific_name")
	if !exists || scientificName == "" {
		result.Passed = true
		result.Message = "Cannot check coordinated names: scientific name is missing"
		return result, nil
	}

	rank, exists := ctx.GetFieldString("col__rank_id")
	if !exists || rank == "" {
		result.Passed = true
		result.Message = "Cannot check coordinated names: rank is missing"
		return result, nil
	}

	// Only check for family-group names
	familyGroupRanks := []string{"superfamily", "family", "subfamily", "tribe", "subtribe"}
	isFamilyGroup := false
	for _, r := range familyGroupRanks {
		if rank == r {
			isFamilyGroup = true
			break
		}
	}

	if !isFamilyGroup {
		result.Passed = true
		result.Message = "Coordinated names check only applies to family-group names"
		return result, nil
	}

	// Extract the stem (base form) of the family-group name
	stem := v.extractFamilyGroupStem(scientificName, rank)
	if stem == "" {
		result.Passed = true
		result.Message = "Cannot extract stem from family-group name"
		return result, nil
	}

	// Get the value of the field being checked
	currentValue, exists := ctx.GetFieldString(fieldToCheck)
	if !exists {
		// If field doesn't exist, we can't check consistency
		result.Passed = true
		result.Message = fmt.Sprintf("Field %s not found, skipping coordinated names check", fieldToCheck)
		return result, nil
	}

	// Find coordinated names (same stem, different ranks)
	coordinatedNames, err := v.findCoordinatedNames(ctx, stem, rank, fieldToCheck)
	if err != nil {
		return nil, fmt.Errorf("failed to find coordinated names: %w", err)
	}

	if len(coordinatedNames) == 0 {
		result.Passed = true
		result.Message = "No coordinated names found for comparison"
		return result, nil
	}

	// Check if all coordinated names have the same value for the field
	inconsistencies := []string{}
	for _, coordinated := range coordinatedNames {
		coordValue, _ := coordinated[fieldToCheck].(string)
		if coordValue != currentValue {
			inconsistencies = append(inconsistencies,
				fmt.Sprintf("%s (%s): %s=%s",
					coordinated["col__scientific_name"],
					coordinated["col__rank_id"],
					fieldToCheck,
					coordValue,
				),
			)
		}
	}

	if len(inconsistencies) > 0 {
		result.Passed = false
		result.Message = fmt.Sprintf("%s. Inconsistent values: %s", rule.Message(), strings.Join(inconsistencies, "; "))
		result.ActualValue = currentValue
		result.ExpectedValue = fmt.Sprintf("consistent %s across coordinated names", fieldToCheck)
	} else {
		result.Passed = true
		result.Message = fmt.Sprintf("Field %s is consistent across %d coordinated name(s)", fieldToCheck, len(coordinatedNames))
		result.ActualValue = currentValue
	}

	return result, nil
}

// extractFamilyGroupStem extracts the stem from a family-group name.
// For example:
// - Felidae -> Fel
// - Felinae -> Fel
// - Felini -> Fel
func (v *CoordinatedNamesValidator) extractFamilyGroupStem(name string, rank string) string {
	switch rank {
	case "superfamily":
		// Remove -oidea suffix
		return strings.TrimSuffix(name, "oidea")
	case "family":
		// Remove -idae suffix
		return strings.TrimSuffix(name, "idae")
	case "subfamily":
		// Remove -inae suffix
		return strings.TrimSuffix(name, "inae")
	case "tribe":
		// Remove -ini suffix
		return strings.TrimSuffix(name, "ini")
	case "subtribe":
		// Remove -ina suffix
		return strings.TrimSuffix(name, "ina")
	default:
		return ""
	}
}

// findCoordinatedNames finds names with the same stem at different family-group ranks.
func (v *CoordinatedNamesValidator) findCoordinatedNames(
	ctx *domain.ValidationContext,
	stem string,
	currentRank string,
	fieldToCheck string,
) ([]map[string]interface{}, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("coordinated_names_%s_%s", stem, fieldToCheck)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists {
		return cached, nil
	}

	// Build query to find names with the same stem
	// Check for all family-group suffixes
	query := `
		SELECT id, col__scientific_name, col__rank_id, ` + fieldToCheck + `
		FROM name
		WHERE (
			col__scientific_name LIKE ? OR
			col__scientific_name LIKE ? OR
			col__scientific_name LIKE ? OR
			col__scientific_name LIKE ? OR
			col__scientific_name LIKE ?
		)
		AND col__rank_id IN ('superfamily', 'family', 'subfamily', 'tribe', 'subtribe')
		AND col__rank_id != ?
		AND id != ?
	`

	args := []interface{}{
		stem + "oidea", // superfamily
		stem + "idae",  // family
		stem + "inae",  // subfamily
		stem + "ini",   // tribe
		stem + "ina",   // subtribe
		currentRank,
		ctx.RecordID,
	}

	// Execute query
	rows, err := ctx.DB.QueryContext(ctx.Ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect results
	var coordinatedNames []map[string]interface{}
	for rows.Next() {
		var id, name, rank string
		var fieldValue sql.NullString

		if err := rows.Scan(&id, &name, &rank, &fieldValue); err != nil {
			return nil, err
		}

		var fieldVal string
		if fieldValue.Valid {
			fieldVal = fieldValue.String
		}

		coordinatedNames = append(coordinatedNames, map[string]interface{}{
			"id":                   id,
			"col__scientific_name": name,
			"col__rank_id":         rank,
			fieldToCheck:           fieldVal,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cache results
	ctx.SetRelatedRecords(cacheKey, coordinatedNames)

	return coordinatedNames, nil
}

// CanAutoFix returns false (coordinated names cannot be auto-fixed).
func (v *CoordinatedNamesValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for coordinated names validator.
func (v *CoordinatedNamesValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
