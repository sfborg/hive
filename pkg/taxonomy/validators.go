package taxonomy

import (
	"github.com/gdower/gsvalidator/domain"
	"github.com/sfborg/hive/pkg/hiveext"
)

// hiveValidator is the local alias for the validator interface hive's
// registry expects. Keeping it here (rather than importing gsvalidator's
// registry package) means this file has to know nothing about how the
// registry is built — the pkg root loops the returned slice and hands
// each entry to registry.Register itself.
type hiveValidator interface {
	Name() string
	CanAutoFix() bool
	AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error
	Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error)
}

// Validators returns the taxonomy-specific validators hive wires into
// the shared gsvalidator registry. The pkg root calls this once at
// Archive-open time and registers each entry.
//
// Adding a new taxonomy validator is one line here + the constructor
// in a sibling file; the pkg root does not need to know its type.
func Validators(cfg hiveext.ValidatorConfig) []hiveValidator {
	return []hiveValidator{
		newParseTailValidator(),
		newCombinationMatchesBasionymValidator(),
		newInfraspecificMarkerValidator(),
		newSpeciesInteractionNeedsRelatedValidator(),
		newSpeciesInteractionNeedsTypeValidator(),
	}
}
