package hive

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/sfborg/gsvalidator/adapter/repository"
	"github.com/sfborg/gsvalidator/domain"
	"github.com/sfborg/gsvalidator/usecase"
	"github.com/sfborg/gsvalidator/usecase/joins"
	"github.com/sfborg/gsvalidator/usecase/validator"
	"github.com/sfborg/hive/pkg/hiveext"
	"github.com/sfborg/hive/pkg/refmanager"
	"github.com/sfborg/hive/pkg/taxonomy"
	"github.com/sfborg/hive/pkg/taxonomy/sfgarules"
)

//go:embed hive_rules.json
var hiveRulesJSON []byte

//go:embed clb_rules.json
var clbRulesJSON []byte

//go:embed tw_rules.json
var twRulesJSON []byte

//go:embed tw_iczn_rules.json
var twICZNRulesJSON []byte

//go:embed tw_icn_rules.json
var twICNRulesJSON []byte

//go:embed tw_icnp_rules.json
var twICNPRulesJSON []byte

//go:embed tw_icvcn_rules.json
var twICVCNRulesJSON []byte

//go:embed tw_ext_rules.json
var twExtRulesJSON []byte

//go:embed tw_ext_iczn_rules.json
var twExtICZNRulesJSON []byte

//go:embed tw_ext_icn_rules.json
var twExtICNRulesJSON []byte

//go:embed tw_ext_icnp_rules.json
var twExtICNPRulesJSON []byte

//go:embed tw_ext_icvcn_rules.json
var twExtICVCNRulesJSON []byte

// rulesetSource identifies where a bundle of rules came from —
// visible in rule_id prefixes (hive_ / clb_ / tw_) and used by
// per-ruleset enable/disable via hive__config_rulesets.
type rulesetSource struct {
	name    string
	bundle  []byte
	enabled bool
}

// rulesetSources lists every embedded bundle in load order. The
// TaxonWorks-derived rules are split by nomenclatural code so a
// curator can disable the rules for a code the archive does not
// curate; "tw" holds the TaxonWorks rules that apply to every code.
var rulesetSources = []rulesetSource{
	{name: "hive", bundle: hiveRulesJSON},
	{name: "clb", bundle: clbRulesJSON},
	{name: "tw", bundle: twRulesJSON},
	{name: "tw_iczn", bundle: twICZNRulesJSON},
	{name: "tw_icn", bundle: twICNRulesJSON},
	{name: "tw_icnp", bundle: twICNPRulesJSON},
	{name: "tw_icvcn", bundle: twICVCNRulesJSON},
	// The tw_ext bundles need the TaxonWorks round-trip columns; they
	// load only on an archive that has them (see RulesetSkip).
	{name: "tw_ext", bundle: twExtRulesJSON},
	{name: "tw_ext_iczn", bundle: twExtICZNRulesJSON},
	{name: "tw_ext_icn", bundle: twExtICNRulesJSON},
	{name: "tw_ext_icnp", bundle: twExtICNPRulesJSON},
	{name: "tw_ext_icvcn", bundle: twExtICVCNRulesJSON},
}

// RulesetSkip records a bundle that did not load because the archive
// lacks a table, column or relation the bundle's Requires block names.
// Its rules did not run.
type RulesetSkip struct {
	Ruleset          string
	Rules            int
	MissingTables    []string
	MissingColumns   []string
	MissingRelations []string
	TypeMismatches   []string
}

// Summary renders a skip as one line for a CLI or a log.
func (s RulesetSkip) Summary() string {
	var missing []string
	missing = append(missing, s.MissingTables...)
	missing = append(missing, s.MissingColumns...)
	missing = append(missing, s.MissingRelations...)
	missing = append(missing, s.TypeMismatches...)
	return fmt.Sprintf("ruleset %s: %d rules did not run; archive lacks %s",
		s.Ruleset, s.Rules, strings.Join(missing, ", "))
}

// bundleSupersedes reads a bundle's optional "supersedes" list: rule
// ids from other bundles that this bundle replaces. gsvalidator's
// loader ignores the field, so it is read from the raw JSON.
func bundleSupersedes(bundle []byte) ([]string, error) {
	var b struct {
		Supersedes []string `json:"supersedes"`
	}
	if err := json.Unmarshal(bundle, &b); err != nil {
		return nil, fmt.Errorf("read supersedes: %w", err)
	}
	return b.Supersedes, nil
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
// mergedRuleLoader wired into the use case. Bundles the archive
// cannot satisfy are reported as RulesetSkips rather than dropped
// silently.
func newHiveValidator(db *sql.DB, archivePath string) (
	*usecase.ValidateRecordUseCase, *repository.BundleLoader, []RulesetSkip, error,
) {
	sources := make([]rulesetSource, len(rulesetSources))
	for i, src := range rulesetSources {
		sources[i] = src
		sources[i].enabled = true
	}
	// Apply per-ruleset toggles from hive__config_rulesets — a
	// curator's explicit disable turns off a whole bundle before
	// its rules are even loaded.
	ctx := context.Background()
	rulesetOverrides, err := readRulesetOverrides(ctx, db)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read ruleset overrides: %w", err)
	}
	for i, src := range sources {
		if v, ok := rulesetOverrides[src.name]; ok {
			sources[i].enabled = v
		}
	}

	primaryLoader := repository.NewBytesBundleLoader(hiveRulesJSON)
	pkg, err := primaryLoader.LoadPackage(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("hive_sfga bundle: %w", err)
	}
	available := make(map[string]bool, len(pkg.Relations))
	for name := range pkg.Relations {
		available[name] = true
	}
	// Merge rules across enabled bundles. Relations come from the
	// primary (hive) bundle only.
	//
	// A bundle whose Requires block names tables or columns the
	// archive does not have is skipped, and the skip is recorded so
	// callers can say so out loud: the TaxonWorks round-trip bundles
	// need tw__ columns that are still a proposal against the sfga
	// schema, and a rename upstream has to surface as "these rules
	// did not run", not as silence.
	var mergedRules []*domain.Rule
	var skipped []RulesetSkip
	var superseded []string
	for _, src := range sources {
		if !src.enabled {
			continue
		}
		bl := repository.NewBytesBundleLoader(src.bundle)
		bundle, err := bl.LoadPackage(ctx)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s bundle: %w", src.name, err)
		}
		if err := usecase.CheckRequires(ctx, db, bundle.Requires, available); err != nil {
			var re *usecase.RequiresError
			if !errors.As(err, &re) {
				return nil, nil, nil, fmt.Errorf("%s bundle: %w", src.name, err)
			}
			skipped = append(skipped, RulesetSkip{
				Ruleset:          src.name,
				Rules:            len(bundle.Rules),
				MissingTables:    re.MissingTables,
				MissingColumns:   re.MissingColumns,
				MissingRelations: re.MissingRelations,
				TypeMismatches:   re.TypeMismatches,
			})
			slog.Warn("validation ruleset skipped: the archive lacks what it needs",
				"ruleset", src.name, "rules_not_run", len(bundle.Rules),
				"missing", re.Error())
			continue
		}
		// A bundle may supersede rules of another: the TaxonWorks
		// round-trip rules restate some core rules against the
		// columns TaxonWorks actually round-trips, and only one of
		// the pair should report.
		sup, err := bundleSupersedes(src.bundle)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s bundle: %w", src.name, err)
		}
		superseded = append(superseded, sup...)
		mergedRules = append(mergedRules, bundle.Rules...)
	}
	if len(superseded) > 0 {
		drop := make(map[string]bool, len(superseded))
		for _, id := range superseded {
			drop[id] = true
		}
		kept := mergedRules[:0]
		for _, r := range mergedRules {
			if !drop[r.ID] {
				kept = append(kept, r)
			}
		}
		mergedRules = kept
	}
	// Apply per-rule overrides from hive__config_rules — disable
	// specific rules and rewrite severities. Runs after the merge
	// so a curator can override rules from any bundle.
	mergedRules, err = applyRuleOverrides(ctx, db, mergedRules)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("apply rule overrides: %w", err)
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
	// Extension-package validators. Field-agnostic reference-manager
	// rules come from pkg/refmanager (reference metadata, JATS sidecar
	// validation, citation coverage, uncited-reference detection).
	// Taxonomy-specific rules come from pkg/taxonomy (rank/code/synonym
	// vocabulary). Each package owns its own constructors; the pkg root
	// only sees the returned slice.
	cfg := hiveext.ValidatorConfig{
		DB:               db,
		ArchivePath:      archivePath,
		ReferenceFKSites: taxonomy.ReferenceFKSites(),
	}
	for _, v := range refmanager.Validators(cfg) {
		registry.Register(v)
	}
	for _, v := range taxonomy.Validators(cfg) {
		registry.Register(v)
	}

	uc := usecase.NewValidateRecordUseCase(db, mergedLoader, mapper, registry)
	uc.SetRelationResolver(resolver)
	return uc, primaryLoader, skipped, nil
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

// SyncReferenceIssues re-runs every reference-scoped validator
// against the given reference row and refreshes __gsvalidator_results
// so read paths see the new state. Called from the corrections
// handler after a JATS-mutating operation so validators that read
// the sidecar (hive_citation_coverage, hive_jats_validation) reflect
// the current sidecar state without waiting for the next reindex.
//
// No-op when the archive has no validator wired.
func (a *Archive) SyncReferenceIssues(ctx context.Context, referenceID string) error {
	if a.validator == nil {
		return nil
	}
	return a.syncReferenceIssues(ctx, referenceID)
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
