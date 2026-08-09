package core

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/gdower/gsvalidator/adapter/gateway"
	"github.com/gdower/gsvalidator/domain"
	"github.com/gdower/gsvalidator/usecase"
	"github.com/gdower/gsvalidator/usecase/validator"
	"github.com/gdower/gsvalidator/usecase/validator/custom/sfga"
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
// validator hive currently uses (built-in plus the sfga-custom
// set). One Archive gets one use case; per-record checks route
// through it.
func newHiveValidator(db *sql.DB) *usecase.ValidateRecordUseCase {
	registry := validator.NewRegistry()
	// Built-in validators.
	registry.Register(&validator.PresenceValidator{})
	registry.Register(validator.NewRegexValidator())
	registry.Register(&validator.LengthValidator{})
	registry.Register(&validator.RangeValidator{})
	// SFGA-specific custom validators.
	registry.Register(&sfga.ParentRankValidator{})
	registry.Register(&sfga.HomonymValidator{})
	registry.Register(&sfga.DuplicateValidator{})
	registry.Register(&sfga.CoordinatedNamesValidator{})
	registry.Register(&sfga.RelationshipValidator{})
	registry.Register(&sfga.SourceYearValidator{})
	registry.Register(&sfga.SourceAuthorValidator{})
	registry.Register(&sfga.TypeDesignationValidator{})

	loader := newEmbeddedRuleLoader(hiveRulesJSON)
	mapper := gateway.NewSFGAMapper()
	return usecase.NewValidateRecordUseCase(db, loader, mapper, registry)
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
