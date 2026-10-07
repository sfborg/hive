package hive

import (
	"fmt"

	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/hiveext"
	"github.com/sfborg/hive/pkg/refmanager"
	"github.com/sfborg/hive/pkg/taxonomy"
)

// ApplyCorrection dispatches a correction to its kind-specific
// applier and returns the mutated JATS. The applier map is built
// once per Archive at Open() by walking every extension package's
// Appliers() slice — refmanager owns every JATS-touching kind today;
// a future taxonomy-side kind would union its own Appliers() into
// the same map.
//
// Callers that need to filter kinds by content-type (defensive
// figure/table check for bbox-* kinds) or status (skip reverted
// corrections) do so BEFORE calling — see correction.IsBBoxKind.
func (a *Archive) ApplyCorrection(jats []byte, c *correction.Correction) ([]byte, error) {
	app, ok := a.correctionAppliers[c.Kind]
	if !ok {
		return nil, fmt.Errorf("apply correction: unsupported kind: %s", c.Kind)
	}
	return app.Apply(jats, c)
}

// buildCorrectionAppliers assembles the kind → applier map from every
// extension package that contributes correction kinds. Kept in one
// place so the composition set is grep-able.
func buildCorrectionAppliers() map[correction.Kind]hiveext.CorrectionApplier {
	m := map[correction.Kind]hiveext.CorrectionApplier{}
	for _, app := range refmanager.Appliers() {
		m[app.Kind()] = app
	}
	for _, app := range taxonomy.Appliers() {
		m[app.Kind()] = app
	}
	return m
}
