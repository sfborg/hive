package hive

import (
	"fmt"
	"sync"

	"github.com/gdower/gsvalidator/domain"
	"github.com/gnames/gnparser"
	"github.com/gnames/gnparser/ent/parsed"
)

// parseTailValidator flags a name whose verbatim scientific name has
// an unparsed tail — gnparser's signal that some trailing text (junk,
// editorial markers, "sensu lato" annotations, bacterial strain
// identifiers, and similar) fell outside the grammar. The tail is
// re-derived at rule-eval time rather than persisted because the
// gn__* columns are a cache and adding a tail column would break the
// "sfga schema stays sfga's" rule (CLAUDE.md § Schema handling).
//
// Re-parsing per validation call is cheap (gnparser is microseconds
// per name) and keeps the tail message in sync with the currently-
// installed parser version — a curator who saves a name with a tail,
// upgrades hive months later, and re-syncs issues sees the updated
// tail text automatically.
//
// The validator owns its own gnparser instance rather than borrowing
// the Archive's shared one, because the validator registry is built
// once at newHiveValidator time and doesn't carry an Archive
// reference. gnparser initialization is fast; the extra instance
// costs a small amount of RAM per hive process, which is fine for the
// single-archive-per-process target.
type parseTailValidator struct {
	parser gnparser.GNparser
	mu     sync.Mutex
}

// newParseTailValidator constructs a validator with its own gnparser.
// The parser has no code option set here — Validate re-parses with a
// per-record code hint pulled from col__code_id so ICN vs. ICZN edge
// cases stay code-aware.
func newParseTailValidator() *parseTailValidator {
	return &parseTailValidator{
		parser: gnparser.New(gnparser.NewConfig()),
	}
}

func (v *parseTailValidator) Name() string { return "hive.parse_tail" }

func (v *parseTailValidator) CanAutoFix() bool { return false }

func (v *parseTailValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *parseTailValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	// Prefer the verbatim string that CreateName / UpdateName write
	// into gn__scientific_name_string (mirrored into
	// col__scientific_name_string). Fall back to col__scientific_name
	// for legacy rows or edge cases where only the display form is set.
	verbatim, _ := ctx.GetFieldString("col__scientific_name_string")
	if verbatim == "" {
		verbatim, _ = ctx.GetFieldString("col__scientific_name")
	}
	if verbatim == "" {
		return &domain.Result{
			RuleID:      rule.ID,
			RuleName:    rule.Name,
			RecordID:    ctx.RecordID,
			TableName:   ctx.TableName,
			FieldName:   rule.FieldName,
			Passed:      true,
			Enforcement: domain.EnforcementSoft,
			Severity:    domain.SeverityWarn,
		}, nil
	}

	code, _ := ctx.GetFieldString("col__code_id")
	p := v.parse(verbatim, code)
	if p.Tail == "" {
		return &domain.Result{
			RuleID:      rule.ID,
			RuleName:    rule.Name,
			RecordID:    ctx.RecordID,
			TableName:   ctx.TableName,
			FieldName:   rule.FieldName,
			Passed:      true,
			Enforcement: domain.EnforcementSoft,
			Severity:    domain.SeverityWarn,
		}, nil
	}

	msg := rule.WarningMessage
	if msg == "" {
		msg = rule.ErrorMessage
	}
	if msg == "" {
		msg = "gnparser could not fully parse this scientific name — an unparsed tail is present."
	}
	return &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      false,
		Enforcement: domain.EnforcementSoft,
		Severity:    domain.SeverityWarn,
		Message:     fmt.Sprintf("%s Unparsed tail: %q", msg, p.Tail),
		ActualValue: p.Tail,
	}, nil
}

// parse runs the internal gnparser with the code option applied.
// Serialized via mu since a single GNparser instance isn't documented
// as goroutine-safe.
func (v *parseTailValidator) parse(verbatim, codeID string) parsed.ParsedFlat {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.parser.
		ChangeConfig(gnparser.OptCode(parseCodeID(codeID))).
		ParseName(verbatim).
		Flatten()
}
