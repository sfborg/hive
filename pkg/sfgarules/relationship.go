package sfgarules

import (
	"database/sql"
	"fmt"

	"github.com/gdower/gsvalidator/domain"
)

// RelationshipValidator validates that required name relationships exist.
// For example, a replacement name should have a relationship to the replaced name,
// a new combination should reference the basionym, etc.
type RelationshipValidator struct{}

// Name returns the validator type identifier.
func (v *RelationshipValidator) Name() string {
	return "sfga_relationship"
}

// Validate checks if the required relationship exists.
func (v *RelationshipValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get required relationship type from parameters
	requiredRelType, exists := rule.Parameters["required_relationship"].(string)
	if !exists {
		return nil, fmt.Errorf("required_relationship parameter is required")
	}

	// Get the name ID to check
	nameID := ctx.RecordID
	if ctx.TableName != "name" {
		if nid, exists := ctx.GetFieldString("col__name_id"); exists {
			nameID = nid
		}
	}

	// Check if name status requires this relationship
	nameStatus, _ := ctx.GetFieldString("col__status_id")

	// Only check if status indicates this relationship is required
	requiresCheck := false
	switch requiredRelType {
	case "replaced_name":
		// Replacement names need this relationship
		requiresCheck = (nameStatus == "replacement name" || nameStatus == "nomen novum")
	case "basionym":
		// New combinations need this relationship
		requiresCheck = (nameStatus == "new combination" || nameStatus == "subsequent combination")
	case "original_genus":
		// Species moved to different genus need this relationship
		requiresCheck = (nameStatus == "new combination" || nameStatus == "subsequent combination")
	default:
		// For other relationships, always check
		requiresCheck = true
	}

	if !requiresCheck {
		result.Passed = true
		result.Message = fmt.Sprintf("Relationship %s not required for this name status", requiredRelType)
		return result, nil
	}

	// Check if relationship exists
	hasRelationship, relatedName, err := v.hasRelationship(ctx, nameID, requiredRelType)
	if err != nil {
		return nil, fmt.Errorf("failed to check relationship: %w", err)
	}

	if hasRelationship {
		result.Passed = true
		result.Message = fmt.Sprintf("Has required %s relationship: %s", requiredRelType, relatedName)
		result.ActualValue = relatedName
	} else {
		result.Passed = false
		result.Message = rule.Message()
		result.ExpectedValue = fmt.Sprintf("%s relationship", requiredRelType)
	}

	return result, nil
}

// hasRelationship checks if a specific relationship exists for the name.
func (v *RelationshipValidator) hasRelationship(
	ctx *domain.ValidationContext,
	nameID string,
	relType string,
) (bool, string, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("relationship_%s_%s", nameID, relType)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists && len(cached) > 0 {
		if relName, ok := cached[0]["related_name"].(string); ok {
			return true, relName, nil
		}
	}

	// Query the name_relation table
	// SFGA uses name_relation table to track relationships between names
	query := `
		SELECT nr.col__type_id, n.col__scientific_name
		FROM name_relation nr
		JOIN name n ON nr.col__related_name_id = n.id
		WHERE nr.col__name_id = ?
		  AND nr.col__type_id = ?
		LIMIT 1
	`

	var relTypeDB, relatedName string
	err := ctx.DB.QueryRowContext(ctx.Ctx, query, nameID, relType).Scan(&relTypeDB, &relatedName)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, "", nil
		}
		return false, "", err
	}

	// Cache result
	ctx.SetRelatedRecords(cacheKey, []map[string]interface{}{
		{"related_name": relatedName},
	})

	return true, relatedName, nil
}

// CanAutoFix returns false (relationships cannot be auto-fixed).
func (v *RelationshipValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for relationship validator.
func (v *RelationshipValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
