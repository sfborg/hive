package hiveext

import (
	"github.com/sfborg/hive/pkg/correction"
)

// CorrectionApplier is the extension surface an extension package
// implements to teach hive how to apply a given correction kind to a
// JATS sidecar. Each applier owns exactly one kind — a package that
// contributes multiple kinds returns one applier per kind from its
// Appliers() accessor.
//
// The pkg root builds a map[correction.Kind]CorrectionApplier at
// Archive-open time by walking every registered extension's Appliers()
// slice. Archive.ApplyCorrection dispatches to the map entry matching
// the correction's Kind.
//
// Adding a new correction kind is: add the constant to
// pkg/correction, implement CorrectionApplier in the owning extension
// package (typically pkg/refmanager for JATS-touching kinds), and
// list it in that package's Appliers() slice. The pkg root does not
// need to know the new kind exists.
type CorrectionApplier interface {
	// Kind identifies which correction kind this applier handles.
	// The dispatch map uses the return value as its key.
	Kind() correction.Kind

	// Apply mutates jats per the correction and returns the new
	// bytes. Idempotency, error semantics, and any content_type
	// filtering are the applier's own contract — see pkg/refmanager
	// for the current shapes.
	Apply(jats []byte, c *correction.Correction) ([]byte, error)
}
