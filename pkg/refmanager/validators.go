package refmanager

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

// Validators returns the reference-manager validators hive wires into
// the shared gsvalidator registry. The pkg root calls this once at
// Archive-open time and registers each entry.
//
// These rules reason over reference metadata, attached-PDF sidecars,
// and cross-table citation coverage — everything that's a first-class
// concern of the reference-manager surface, independent of taxonomy.
func Validators(cfg hiveext.ValidatorConfig) []hiveValidator {
	return []hiveValidator{
		newReferenceMetadataValidator(),
		newJATSValidationValidator(cfg.ArchivePath),
		newCitationCoverageValidator(cfg.ArchivePath),
		newReferenceUncitedValidator(cfg.ReferenceFKSites),
	}
}
