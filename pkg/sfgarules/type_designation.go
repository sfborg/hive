package sfgarules

import (
	"database/sql"
	"fmt"

	"github.com/gdower/gsvalidator/domain"
)

// TypeDesignationValidator validates that taxonomic names have required type designations.
// - Genus-group names should have a type species
// - Family-group names should have a type genus
// - Species-group names should have type material (holotype, lectotype, etc.)
type TypeDesignationValidator struct{}

// Name returns the validator type identifier.
func (v *TypeDesignationValidator) Name() string {
	return "sfga_type_designation"
}

// Validate checks if the name has the required type designation.
func (v *TypeDesignationValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get required type from parameters
	requiredType, exists := rule.Parameters["required_type"].(string)
	if !exists {
		return nil, fmt.Errorf("required_type parameter is required")
	}

	// Get type table name from parameters (default: type_material)
	typeTable := "type_material"
	if tt, exists := rule.Parameters["type_table"].(string); exists {
		typeTable = tt
	}

	// Get the name_id to check for type designation
	nameID, exists := ctx.GetFieldString("col__name_id")
	if !exists || nameID == "" {
		// Try to use the record ID if it's the name table
		if ctx.TableName == "name" {
			nameID = ctx.RecordID
		} else {
			result.Passed = false
			result.Message = "Cannot validate type designation: name_id not found"
			return result, nil
		}
	}

	// Check if type designation exists
	hasType, typeInfo, err := v.hasTypeDesignation(ctx, nameID, requiredType, typeTable, rule.Parameters)
	if err != nil {
		return nil, fmt.Errorf("failed to check type designation: %w", err)
	}

	if hasType {
		result.Passed = true
		result.Message = fmt.Sprintf("Has required %s designation: %s", requiredType, typeInfo)
		result.ActualValue = typeInfo
	} else {
		result.Passed = false
		result.Message = rule.Message()
		result.ExpectedValue = fmt.Sprintf("%s designation", requiredType)
	}

	return result, nil
}

// hasTypeDesignation checks if a type designation exists for the given name.
func (v *TypeDesignationValidator) hasTypeDesignation(
	ctx *domain.ValidationContext,
	nameID string,
	requiredType string,
	typeTable string,
	params map[string]interface{},
) (bool, string, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("type_designation_%s_%s", nameID, requiredType)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists && len(cached) > 0 {
		if info, ok := cached[0]["type_info"].(string); ok {
			return true, info, nil
		}
	}

	// Build query based on required type
	var query string
	var args []interface{}

	switch requiredType {
	case "type_species":
		// Check if genus has a designated type species
		query = `
			SELECT tm.col__type_of_type, n.col__scientific_name
			FROM type_material tm
			JOIN name n ON tm.col__type_id = n.id
			WHERE tm.col__name_id = ?
			  AND tm.col__type_of_type IN ('type species', 'type_species')
			LIMIT 1
		`
		args = []interface{}{nameID}

	case "type_genus":
		// Check if family has a designated type genus
		query = `
			SELECT tm.col__type_of_type, n.col__scientific_name
			FROM type_material tm
			JOIN name n ON tm.col__type_id = n.id
			WHERE tm.col__name_id = ?
			  AND tm.col__type_of_type IN ('type genus', 'type_genus')
			LIMIT 1
		`
		args = []interface{}{nameID}

	case "type_specimen":
		// Check if species has type material (holotype, lectotype, etc.)
		acceptedTypes := []string{"holotype", "lectotype", "neotype", "syntype"}
		if at, exists := params["accepted_types"]; exists {
			if types, ok := at.([]interface{}); ok {
				acceptedTypes = make([]string, len(types))
				for i, t := range types {
					acceptedTypes[i] = t.(string)
				}
			}
		}

		// Build IN clause
		placeholders := ""
		for i := range acceptedTypes {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
			args = append(args, acceptedTypes[i])
		}

		query = fmt.Sprintf(`
			SELECT col__type_of_type, col__catalog_number
			FROM type_material
			WHERE col__name_id = ?
			  AND col__type_of_type IN (%s)
			LIMIT 1
		`, placeholders)
		args = append([]interface{}{nameID}, args...)

	default:
		return false, "", fmt.Errorf("unsupported required_type: %s", requiredType)
	}

	// Execute query
	var typeOfType, typeInfo string
	err := ctx.DB.QueryRowContext(ctx.Ctx, query, args...).Scan(&typeOfType, &typeInfo)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, "", nil
		}
		return false, "", err
	}

	// Cache result
	ctx.SetRelatedRecords(cacheKey, []map[string]interface{}{
		{"type_info": fmt.Sprintf("%s: %s", typeOfType, typeInfo)},
	})

	return true, fmt.Sprintf("%s: %s", typeOfType, typeInfo), nil
}

// CanAutoFix returns false (type designation cannot be auto-fixed).
func (v *TypeDesignationValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for type designation validator.
func (v *TypeDesignationValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
