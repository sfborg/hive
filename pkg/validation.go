package hive

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/gdower/gsvalidator/domain"
	"github.com/gdower/gsvalidator/usecase"
	"github.com/gdower/gsvalidator/usecase/validator"
	"github.com/sfborg/hive/pkg/sfgarules"
)

//go:embed hive_rules.json
var hiveRulesJSON []byte

// embeddedRuleLoader is a hive-local RuleLoader that reads its rule
// set from an in-memory byte slice (hive_rules.json, embedded at
// build time). Same semantics as gsvalidator's JSONRuleLoader but
// without the os.ReadFile boundary.
//
// Longer-term the ruleset lives in hive__validation_rules inside the
// archive (per PLANNING.md § Validation engine); this loader is the
// bootstrap seed for archives that don't have any rules yet.
type embeddedRuleLoader struct {
	raw   []byte
	rules []*domain.Rule
	byID  map[string]*domain.Rule
}

func newEmbeddedRuleLoader(raw []byte) *embeddedRuleLoader {
	return &embeddedRuleLoader{raw: raw, byID: map[string]*domain.Rule{}}
}

func (l *embeddedRuleLoader) load() error {
	if len(l.rules) > 0 {
		return nil
	}
	var rules []*domain.Rule
	if err := json.Unmarshal(l.raw, &rules); err != nil {
		var wrapper struct {
			Rules []*domain.Rule `json:"rules"`
		}
		if err2 := json.Unmarshal(l.raw, &wrapper); err2 != nil {
			return fmt.Errorf("hive rules: %w", err)
		}
		rules = wrapper.Rules
	}
	l.rules = rules
	for _, r := range rules {
		l.byID[r.ID] = r
	}
	return nil
}

func (l *embeddedRuleLoader) LoadRules(ctx context.Context) ([]*domain.Rule, error) {
	if err := l.load(); err != nil {
		return nil, err
	}
	return l.rules, nil
}

func (l *embeddedRuleLoader) LoadRuleByID(ctx context.Context, id string) (*domain.Rule, error) {
	if err := l.load(); err != nil {
		return nil, err
	}
	r, ok := l.byID[id]
	if !ok {
		return nil, fmt.Errorf("hive rules: rule %s not found", id)
	}
	return r, nil
}

func (l *embeddedRuleLoader) LoadRulesForTable(ctx context.Context, table string) ([]*domain.Rule, error) {
	if err := l.load(); err != nil {
		return nil, err
	}
	var out []*domain.Rule
	for _, r := range l.rules {
		if r.TableName == table {
			out = append(out, r)
		}
	}
	return out, nil
}

// newHiveValidator builds a gsvalidator use case pre-wired with
// hive's embedded rule set, the sfga schema mapper, and every
// validator hive currently uses (built-in generics from gsvalidator
// plus the sfga-specific set from pkg/sfgarules). One Archive gets
// one use case; per-record checks route through it. Also returns
// the rule loader so the caller can attach it to the Archive for
// time-based scheduling (which reads rule metadata directly rather
// than through the use case).
//
// The sfga-specific validators + SFGAMapper live in
// pkg/sfgarules — hive's temporary landing spot for logic that
// gsvalidator was carrying but doesn't belong upstream in a
// truly generic validation service. See SCHEMA_COMMONS_REFACTOR.md
// for the eventual JSON-decomposition plan.
func newHiveValidator(db *sql.DB) (*usecase.ValidateRecordUseCase, *embeddedRuleLoader) {
	registry := validator.NewRegistry()
	// Built-in generic validators from gsvalidator.
	registry.Register(&validator.PresenceValidator{})
	registry.Register(validator.NewRegexValidator())
	registry.Register(&validator.LengthValidator{})
	registry.Register(&validator.RangeValidator{})
	// SFGA-specific validators from hive's pkg/sfgarules.
	registry.Register(&sfgarules.ParentRankValidator{})
	registry.Register(&sfgarules.HomonymValidator{})
	registry.Register(&sfgarules.DuplicateValidator{})
	registry.Register(&sfgarules.CoordinatedNamesValidator{})
	registry.Register(&sfgarules.RelationshipValidator{})
	registry.Register(&sfgarules.SourceYearValidator{})
	registry.Register(&sfgarules.SourceAuthorValidator{})
	registry.Register(&sfgarules.TypeDesignationValidator{})
	registry.Register(&sfgarules.ParseQualityValidator{})

	loader := newEmbeddedRuleLoader(hiveRulesJSON)
	mapper := sfgarules.NewSFGAMapper()
	return usecase.NewValidateRecordUseCase(db, loader, mapper, registry), loader
}

// ValidateName runs every rule that applies to the name table
// against the row with the given id. Result includes both passes
// and failures — callers separate hard errors (Result.IsError) from
// soft warnings (Result.IsWarning). The first slice of the
// save-with-acknowledgment UX uses this to attach warnings to
// successful create/update responses; a follow-up hooks it into
// Tx.CreateName / Tx.UpdateName for pre-commit hard-fail rollback.
func (a *Archive) ValidateName(ctx context.Context, nameID string) ([]*domain.Result, error) {
	if a.validator == nil {
		return nil, nil
	}
	return a.validator.Execute(ctx, "name", nameID)
}

// ValidationWarning is the frontend-facing shape of a non-blocking
// validation result. Both TUI and WUI consume this so they can render
// warnings the same way without touching gsvalidator's domain package.
type ValidationWarning struct {
	RuleID    string
	RuleName  string
	FieldName string
	Severity  string // "warn" | "info" | "debug"
	Message   string
}

// NameWarnings / TaxonWarnings / MetadataWarnings return the persisted
// non-blocking issue set for a single record of the corresponding
// table. Backed by __gsvalidator_results; write paths keep the cache
// fresh via post-commit syncXIssues. Read path filters to warn/info
// severities — hard errors would surface via a different channel (RFC
// 7807 problem) and aren't attached to a successful GET response;
// debug is diagnostic-only and hidden unless the curator explicitly
// opts in from the Issues screen.
//
// Empty result set may mean "clean" or "not yet synced" (legacy row).
// The store makes no distinction; a hive validate reindex backfills.
func (a *Archive) NameWarnings(ctx context.Context, nameID string) []ValidationWarning {
	return filterWarnings(a.readNameIssues(ctx, nameID))
}

func (a *Archive) TaxonWarnings(ctx context.Context, taxonID string) []ValidationWarning {
	return filterWarnings(a.readTaxonIssues(ctx, taxonID))
}

func (a *Archive) MetadataWarnings(ctx context.Context, metadataID int) []ValidationWarning {
	return filterWarnings(a.readMetadataIssues(ctx, metadataID))
}

// filterWarnings keeps warn + info severities and drops the rest.
// Errors are not "warnings"; debug is diagnostic noise not meant for
// per-record banners.
func filterWarnings(rows []ValidationWarning, err error) []ValidationWarning {
	if err != nil {
		return nil
	}
	var out []ValidationWarning
	for _, r := range rows {
		switch r.Severity {
		case "warn", "info":
			out = append(out, r)
		}
	}
	return out
}
