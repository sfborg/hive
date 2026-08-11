package sfgarules

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/gdower/gsvalidator/domain"
)

// ParentRankValidator validates that parent-child rank relationships are compatible.
// For example, a species must have a genus or subgenus as parent.
type ParentRankValidator struct{}

// Name returns the validator type identifier.
func (v *ParentRankValidator) Name() string {
	return "sfga_parent_rank"
}

// Validate checks if the parent taxon has an appropriate rank for this taxon's rank.
func (v *ParentRankValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)

	// Get current name ID
	nameID := ctx.RecordID

	// Query taxon table to find taxon for this name and get its parent_id
	// Note: A name may exist without a taxon if it hasn't been placed in the taxonomy yet
	query := `SELECT col__id, col__parent_id FROM taxon WHERE col__name_id = ? LIMIT 1`
	var taxonID sql.NullString
	var parentID sql.NullString
	err := ctx.DB.QueryRowContext(ctx.Ctx, query, nameID).Scan(&taxonID, &parentID)
	if err == sql.ErrNoRows {
		// This name doesn't have a taxon record yet - skip validation
		result.Passed = true
		result.Message = "Name not yet placed in taxonomy (no taxon record)"
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query taxon table: %w", err)
	}

	// Check if taxon has a parent
	if !parentID.Valid || parentID.String == "" {
		// No parent specified - this might be a root taxon (kingdom)
		// Check if current rank is allowed to have no parent
		currentRank, hasRank := ctx.GetFieldString("col__rank_id")
		if hasRank {
			rankHierarchy, err := v.parseRankHierarchy(rule.Parameters)
			if err != nil {
				return nil, fmt.Errorf("failed to parse rank_hierarchy parameter: %w", err)
			}

			allowedParents, rankExists := rankHierarchy[currentRank]
			if rankExists && len(allowedParents) == 0 {
				// Rank is allowed to have no parent (e.g., kingdom)
				result.Passed = true
				result.Message = fmt.Sprintf("Rank '%s' is allowed to have no parent", currentRank)
				return result, nil
			}
		}

		// Otherwise, missing parent is a failure
		result.Passed = false
		result.Message = "Parent taxon is required"
		result.ExpectedValue = "valid parent_id"
		return result, nil
	}

	// Use the parent ID from taxon table for subsequent validation
	parentIDStr := parentID.String

	// Get current taxon's rank
	currentRank, exists := ctx.GetFieldString("col__rank_id")
	if !exists || currentRank == "" {
		result.Passed = false
		result.Message = "Cannot validate parent rank: current rank is not specified"
		return result, nil
	}

	// Parse rank hierarchy from parameters
	rankHierarchy, err := v.parseRankHierarchy(rule.Parameters)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rank_hierarchy parameter: %w", err)
	}

	// Get allowed parent ranks for current rank
	allowedParentRanks, rankExists := rankHierarchy[currentRank]
	if !rankExists {
		result.Passed = false
		result.Message = fmt.Sprintf("Rank '%s' not found in rank hierarchy", currentRank)
		return result, nil
	}

	// Query parent taxon to get its rank
	parentRank, err := v.getParentRank(ctx, parentIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			result.Passed = false
			result.Message = fmt.Sprintf("Parent taxon with ID '%s' not found", parentIDStr)
			result.ActualValue = parentIDStr
			return result, nil
		}
		return nil, fmt.Errorf("failed to query parent taxon: %w", err)
	}

	// Check if parent rank is in the list of allowed parent ranks
	isAllowed := false
	for _, allowedRank := range allowedParentRanks {
		if parentRank == allowedRank {
			isAllowed = true
			break
		}
	}

	if isAllowed {
		result.Passed = true
		result.Message = fmt.Sprintf("Parent rank '%s' is compatible with child rank '%s'", parentRank, currentRank)
		result.ActualValue = parentRank
		result.ExpectedValue = allowedParentRanks
	} else {
		result.Passed = false
		result.Message = rule.Message()
		result.ActualValue = parentRank
		result.ExpectedValue = allowedParentRanks
	}

	return result, nil
}

// parseRankHierarchy parses the rank_hierarchy parameter from the rule.
// Expected format: {"rank_hierarchy": {"rank1": ["parent_rank1", "parent_rank2"], ...}}
func (v *ParentRankValidator) parseRankHierarchy(params map[string]interface{}) (map[string][]string, error) {
	rankHierarchyRaw, exists := params["rank_hierarchy"]
	if !exists {
		return nil, fmt.Errorf("rank_hierarchy parameter is required")
	}

	// Convert to JSON and back to get proper type
	jsonData, err := json.Marshal(rankHierarchyRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rank_hierarchy: %w", err)
	}

	var rankHierarchy map[string][]string
	if err := json.Unmarshal(jsonData, &rankHierarchy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rank_hierarchy: %w", err)
	}

	return rankHierarchy, nil
}

// getParentRank queries the database to get the rank of the parent taxon.
func (v *ParentRankValidator) getParentRank(ctx *domain.ValidationContext, parentID string) (string, error) {
	// Try to get from cache first
	cacheKey := fmt.Sprintf("parent_rank_%s", parentID)
	if cached, exists := ctx.RelatedRecords[cacheKey]; exists && len(cached) > 0 {
		if rank, ok := cached[0]["col__rank_id"].(string); ok {
			return rank, nil
		}
	}

	// Query the database
	// In SFGA, rank is stored in the name table, not the taxon table
	// We need to join taxon -> name to get the rank
	query := `
		SELECT n.col__rank_id
		FROM taxon t
		JOIN name n ON t.col__name_id = n.col__id
		WHERE t.col__id = ?
	`

	var parentRank string
	err := ctx.DB.QueryRowContext(ctx.Ctx, query, parentID).Scan(&parentRank)
	if err != nil {
		return "", err
	}

	// Cache the result
	ctx.SetRelatedRecords(cacheKey, []map[string]interface{}{
		{"col__rank_id": parentRank},
	})

	return parentRank, nil
}

// CanAutoFix returns false (parent rank cannot be auto-fixed).
func (v *ParentRankValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for parent rank validator.
func (v *ParentRankValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
