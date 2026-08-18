package hive

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/gdower/gsvalidator/adapter/repository"
	"github.com/gdower/gsvalidator/domain"
	"github.com/gdower/gsvalidator/usecase"
	"github.com/gdower/gsvalidator/usecase/joins"
	"github.com/gdower/gsvalidator/usecase/validator"
	"github.com/sfborg/hive/pkg/sfgarules"
)

//go:embed hive_rules.json
var hiveRulesJSON []byte

//go:embed clb_rules.json
var clbRulesJSON []byte

//go:embed tw_rules.json
var twRulesJSON []byte

// rulesetSource identifies where a bundle of rules came from —
// visible in rule_id prefixes (hive_ / clb_ / tw_) and used by
// per-ruleset enable/disable when that surface lands. Currently
// all three sources load unconditionally; a curator-facing toggle
// is a follow-up.
type rulesetSource struct {
	name    string
	bundle  []byte
	enabled bool
}

// newHiveValidator builds a gsvalidator use case pre-wired with
// hive's embedded rule bundles (hive-native + CLB-derived + TW-
// derived), the sfga schema mapper, and every validator hive
// currently uses.
//
// Each bundle is loaded eagerly so a malformed bundle fails at
// Archive open rather than on the first validation call. Relations
// live in the hive_rules.json bundle as shared infrastructure —
// clb and tw bundles reference them by name without redeclaring.
//
// Returns the primary (hive) bundle loader so callers can inspect
// rule metadata (Trigger, RecheckDays) for time-based scheduling.
// Rules from all enabled bundles are merged into a single
// mergedRuleLoader wired into the use case.
func newHiveValidator(db *sql.DB) (*usecase.ValidateRecordUseCase, *repository.BundleLoader, error) {
	sources := []rulesetSource{
		{name: "hive", bundle: hiveRulesJSON, enabled: true},
		{name: "clb", bundle: clbRulesJSON, enabled: true},
		{name: "tw", bundle: twRulesJSON, enabled: true},
	}
	// Apply per-ruleset toggles from hive__config_rulesets — a
	// curator's explicit disable turns off a whole bundle before
	// its rules are even loaded.
	ctx := context.Background()
	rulesetOverrides, err := readRulesetOverrides(ctx, db)
	if err != nil {
		return nil, nil, fmt.Errorf("read ruleset overrides: %w", err)
	}
	for i, src := range sources {
		if v, ok := rulesetOverrides[src.name]; ok {
			sources[i].enabled = v
		}
	}

	primaryLoader := repository.NewBytesBundleLoader(hiveRulesJSON)
	pkg, err := primaryLoader.LoadPackage(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("hive_sfga bundle: %w", err)
	}
	// Merge rules across enabled bundles. Relations come from the
	// primary (hive) bundle only.
	var mergedRules []*domain.Rule
	for _, src := range sources {
		if !src.enabled {
			continue
		}
		bl := repository.NewBytesBundleLoader(src.bundle)
		bundle, err := bl.LoadPackage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("%s bundle: %w", src.name, err)
		}
		mergedRules = append(mergedRules, bundle.Rules...)
	}
	// Apply per-rule overrides from hive__config_rules — disable
	// specific rules and rewrite severities. Runs after the merge
	// so a curator can override rules from any bundle.
	mergedRules, err = applyRuleOverrides(ctx, db, mergedRules)
	if err != nil {
		return nil, nil, fmt.Errorf("apply rule overrides: %w", err)
	}
	mergedLoader := &mergedRuleLoader{rules: mergedRules}
	mapper := sfgarules.NewSFGAMapper()
	resolver := joins.NewRelationResolver(pkg.Relations, mapper)

	registry := validator.NewRegistry()
	// Built-in generic validators from gsvalidator.
	registry.Register(&validator.PresenceValidator{})
	registry.Register(&validator.AbsenceValidator{})
	registry.Register(validator.NewRegexValidator())
	registry.Register(&validator.LengthValidator{})
	registry.Register(&validator.RangeValidator{})
	// Generic mechanisms that traverse the bundle's relations.
	registry.Register(validator.NewRelatedFieldEqualsValidator(resolver))
	registry.Register(validator.NewRelatedFieldNotEqualsValidator(resolver))
	registry.Register(validator.NewRelatedFieldInSetValidator(resolver))
	// Generic cross-record aggregate — count rows matching predicates,
	// range-check the count. Uses mapper's PK-per-table for
	// exclude_self.
	registry.Register(validator.NewCountAcrossValidator(mapper))
	// Cycle detection over a self-referencing parent column.
	registry.Register(validator.NewNoSelfCycleValidator(mapper))
	// Walk parent chain and check for an ancestor matching a predicate.
	registry.Register(validator.NewAncestorExistsValidator(mapper))
	// Walk parent chain, find matching ancestor, compare a field on
	// self against a field on that ancestor.
	registry.Register(validator.NewAncestorFieldCheckValidator(mapper))
	// FK column resolves via a declared relation (skips on empty).
	registry.Register(validator.NewForeignKeyExistsValidator(resolver))
	// Parent's rank must be higher than self's rank per per-code
	// ordered lists (data injected via inline rule params, kept in
	// sync with pkg/ui/rank_hierarchy.json by tools/mkrankorder).
	registry.Register(validator.NewParentRankHigherValidator(mapper))
	// Check-digit verification for common identifier formats
	// (orcid, issn, isbn10, isbn13, luhn).
	registry.Register(validator.NewCheckDigitValidator())
	// Hive-native: re-parse the name at rule-eval time and flag any
	// unparsed tail gnparser rejected. Not a generic validator — owns
	// its own gnparser instance because the registry has no Archive
	// reference to borrow the shared parser.
	registry.Register(newParseTailValidator())
	// Hive-native: soft-warn when the atomized combination_* pair
	// exactly matches the basionym_* pair on the same row — almost
	// always a data-entry mistake. See DEFERRED.md → moved-here-now.
	registry.Register(newCombinationMatchesBasionymValidator())
	// Hive-native: soft-warn when a reference has a free-text
	// citation but is missing structured author or issued (year).
	// Surfaces the data-quality gap that breaks the WUI citation-pick
	// backfill on CoL-derived data. See feedback_no_side_quests for
	// the inline-quick-fix UX that resolves flagged rows without a
	// side quest to the References screen.
	registry.Register(newReferenceMetadataValidator())
	// Hive-native: soft-warn when an infraspecific name lacks its
	// rank marker (var., f., subsp., subvar., subf.) in the scientific
	// name string. Code-aware: skips ICZN + SUBSPECIES (marker is
	// optional under ICZN) and ICVCN (no formal subspecies rank).
	// Fires for ICN / ICNP / empty-code on any covered infraspecific
	// rank and for ICZN on variety/subvariety/form/subform ranks
	// (historical rows in synonymy).
	registry.Register(newInfraspecificMarkerValidator())

	uc := usecase.NewValidateRecordUseCase(db, mergedLoader, mapper, registry)
	uc.SetRelationResolver(resolver)
	return uc, primaryLoader, nil
}

// mergedRuleLoader satisfies usecase.RuleLoader by returning a
// pre-computed rule slice merged from every enabled bundle. Kept
// in-memory because bundles are embedded (no I/O to re-do), and
// the merge cost is trivial (~42 rules today).
type mergedRuleLoader struct {
	rules []*domain.Rule
}

func (l *mergedRuleLoader) LoadRules(ctx context.Context) ([]*domain.Rule, error) {
	return l.rules, nil
}

func (l *mergedRuleLoader) LoadRuleByID(ctx context.Context, ruleID string) (*domain.Rule, error) {
	for _, r := range l.rules {
		if r.ID == ruleID {
			return r, nil
		}
	}
	return nil, nil
}

func (l *mergedRuleLoader) LoadRulesForTable(ctx context.Context, tableName string) ([]*domain.Rule, error) {
	var out []*domain.Rule
	for _, r := range l.rules {
		if r.TableName == tableName {
			out = append(out, r)
		}
	}
	return out, nil
}

// readRulesetOverrides fetches every hive__config_rulesets row into
// a map. Missing entries → use the bundle's shipped default.
func readRulesetOverrides(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ruleset_name, enabled FROM hive__config_rulesets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var name string
		var enabled int
		if err := rows.Scan(&name, &enabled); err != nil {
			return nil, err
		}
		out[name] = enabled == 1
	}
	return out, rows.Err()
}

// applyRuleOverrides consumes the merged rule list and rewrites it
// per hive__config_rules — dropping rules a curator disabled and
// swapping the severity of rules with a severity_override. Rules
// without a config row pass through untouched.
func applyRuleOverrides(ctx context.Context, db *sql.DB, rules []*domain.Rule) ([]*domain.Rule, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT rule_id, enabled, severity_override FROM hive__config_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type override struct {
		enabled          sql.NullBool
		severityOverride sql.NullString
	}
	overrides := make(map[string]override)
	for rows.Next() {
		var id string
		var ov override
		if err := rows.Scan(&id, &ov.enabled, &ov.severityOverride); err != nil {
			return nil, err
		}
		overrides[id] = ov
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(overrides) == 0 {
		return rules, nil
	}
	out := make([]*domain.Rule, 0, len(rules))
	for _, r := range rules {
		ov, hasOv := overrides[r.ID]
		if hasOv && ov.enabled.Valid && !ov.enabled.Bool {
			continue // curator disabled — drop
		}
		if hasOv && ov.severityOverride.Valid && ov.severityOverride.String != "" {
			// Shallow copy so the override doesn't leak back into
			// the bundle's cached rule pointer.
			cp := *r
			cp.Severity = domain.Severity(ov.severityOverride.String)
			out = append(out, &cp)
			continue
		}
		out = append(out, r)
	}
	return out, nil
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
