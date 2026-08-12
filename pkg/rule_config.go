package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RuleConfig captures a curator's overrides for one rule. A nil
// Enabled means "use ruleset default"; likewise for
// SeverityOverride. Zero value = "no override at all" (rule uses
// bundle defaults).
type RuleConfig struct {
	RuleID           string
	Enabled          *bool
	SeverityOverride string // "error" | "warn" | "info" | "debug" | "" for none
	UpdatedAt        string
	UpdatedBy        string
}

// RulesetConfig is a coarser toggle: whole bundle on or off. Only
// records rows for rulesets a curator has explicitly changed;
// missing row means "use default (enabled)".
type RulesetConfig struct {
	RulesetName string // "hive" | "clb" | "tw"
	Enabled     bool
	UpdatedAt   string
	UpdatedBy   string
}

// validSeverities are the only values severity_override may hold.
// Enforced both by the DB CHECK constraint and by the accessor —
// belt and braces.
var validSeverities = map[string]bool{
	"error": true, "warn": true, "info": true, "debug": true,
}

// GetRuleConfig returns the override row for a rule, if one exists.
// Returns nil (no error) when the rule has no override — caller
// treats that as "use bundle defaults."
func (a *Archive) GetRuleConfig(ctx context.Context, ruleID string) (*RuleConfig, error) {
	if ruleID == "" {
		return nil, fmt.Errorf("core: get rule config: rule id required")
	}
	row := a.db.QueryRowContext(ctx,
		`SELECT rule_id, enabled, severity_override, updated_at, updated_by
		 FROM hive__config_rules WHERE rule_id = ?`, ruleID,
	)
	var cfg RuleConfig
	var enabled sql.NullBool
	var sev sql.NullString
	if err := row.Scan(&cfg.RuleID, &enabled, &sev, &cfg.UpdatedAt, &cfg.UpdatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("core: get rule config %s: %w", ruleID, err)
	}
	if enabled.Valid {
		b := enabled.Bool
		cfg.Enabled = &b
	}
	if sev.Valid {
		cfg.SeverityOverride = sev.String
	}
	return &cfg, nil
}

// ListRuleConfigs returns every rule override the archive holds,
// ordered by rule id for deterministic display.
func (a *Archive) ListRuleConfigs(ctx context.Context) ([]RuleConfig, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT rule_id, enabled, severity_override, updated_at, updated_by
		 FROM hive__config_rules ORDER BY rule_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("core: list rule configs: %w", err)
	}
	defer rows.Close()
	var out []RuleConfig
	for rows.Next() {
		var cfg RuleConfig
		var enabled sql.NullBool
		var sev sql.NullString
		if err := rows.Scan(&cfg.RuleID, &enabled, &sev, &cfg.UpdatedAt, &cfg.UpdatedBy); err != nil {
			return nil, fmt.Errorf("core: scan rule config: %w", err)
		}
		if enabled.Valid {
			b := enabled.Bool
			cfg.Enabled = &b
		}
		if sev.Valid {
			cfg.SeverityOverride = sev.String
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// SetRuleConfig upserts a rule's override. Both Enabled and
// SeverityOverride are optional — nil / empty means "clear that
// dimension of the override, use bundle default." When both are
// nil/empty the accessor deletes the row entirely (no-op override
// takes no space).
func (a *Archive) SetRuleConfig(ctx context.Context, cfg RuleConfig) error {
	if a.readOnly {
		return ErrReadOnly
	}
	if cfg.RuleID == "" {
		return fmt.Errorf("core: set rule config: %w: rule id required", ErrValidation)
	}
	if cfg.SeverityOverride != "" && !validSeverities[cfg.SeverityOverride] {
		return fmt.Errorf("core: set rule config: %w: severity_override must be one of error|warn|info|debug (got %q)",
			ErrValidation, cfg.SeverityOverride)
	}
	if cfg.Enabled == nil && cfg.SeverityOverride == "" {
		return a.ClearRuleConfig(ctx, cfg.RuleID)
	}
	actor := ActorFromContext(ctx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var enabledArg interface{}
	if cfg.Enabled != nil {
		v := 0
		if *cfg.Enabled {
			v = 1
		}
		enabledArg = v
	}
	var sevArg interface{}
	if cfg.SeverityOverride != "" {
		sevArg = cfg.SeverityOverride
	}
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO hive__config_rules (rule_id, enabled, severity_override, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (rule_id) DO UPDATE SET
			enabled = excluded.enabled,
			severity_override = excluded.severity_override,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		cfg.RuleID, enabledArg, sevArg, now, actor,
	)
	if err != nil {
		return fmt.Errorf("core: set rule config %s: %w", cfg.RuleID, err)
	}
	return nil
}

// ClearRuleConfig removes a rule's override entirely. Silent no-op
// when the row doesn't exist.
func (a *Archive) ClearRuleConfig(ctx context.Context, ruleID string) error {
	if a.readOnly {
		return ErrReadOnly
	}
	if ruleID == "" {
		return fmt.Errorf("core: clear rule config: %w: rule id required", ErrValidation)
	}
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM hive__config_rules WHERE rule_id = ?`, ruleID)
	if err != nil {
		return fmt.Errorf("core: clear rule config %s: %w", ruleID, err)
	}
	return nil
}

// GetRulesetConfig returns the toggle state for a ruleset. Nil
// (no error) when no override row exists — caller treats that as
// "use default (enabled)."
func (a *Archive) GetRulesetConfig(ctx context.Context, name string) (*RulesetConfig, error) {
	if name == "" {
		return nil, fmt.Errorf("core: get ruleset config: name required")
	}
	row := a.db.QueryRowContext(ctx,
		`SELECT ruleset_name, enabled, updated_at, updated_by
		 FROM hive__config_rulesets WHERE ruleset_name = ?`, name,
	)
	var cfg RulesetConfig
	var enabled int
	if err := row.Scan(&cfg.RulesetName, &enabled, &cfg.UpdatedAt, &cfg.UpdatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("core: get ruleset config %s: %w", name, err)
	}
	cfg.Enabled = enabled == 1
	return &cfg, nil
}

// ListRulesetConfigs returns every ruleset toggle the archive holds.
func (a *Archive) ListRulesetConfigs(ctx context.Context) ([]RulesetConfig, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT ruleset_name, enabled, updated_at, updated_by
		 FROM hive__config_rulesets ORDER BY ruleset_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("core: list ruleset configs: %w", err)
	}
	defer rows.Close()
	var out []RulesetConfig
	for rows.Next() {
		var cfg RulesetConfig
		var enabled int
		if err := rows.Scan(&cfg.RulesetName, &enabled, &cfg.UpdatedAt, &cfg.UpdatedBy); err != nil {
			return nil, fmt.Errorf("core: scan ruleset config: %w", err)
		}
		cfg.Enabled = enabled == 1
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// SetRulesetConfig upserts a ruleset toggle. name must be one of
// the known bundle names ("hive" | "clb" | "tw") — enforced here
// to avoid orphan rows for typos of nonexistent bundles.
func (a *Archive) SetRulesetConfig(ctx context.Context, name string, enabled bool) error {
	if a.readOnly {
		return ErrReadOnly
	}
	if !isKnownRuleset(name) {
		return fmt.Errorf("core: set ruleset config: %w: unknown ruleset %q (must be one of hive|clb|tw)",
			ErrValidation, name)
	}
	actor := ActorFromContext(ctx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	v := 0
	if enabled {
		v = 1
	}
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO hive__config_rulesets (ruleset_name, enabled, updated_at, updated_by)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (ruleset_name) DO UPDATE SET
			enabled = excluded.enabled,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		name, v, now, actor,
	)
	if err != nil {
		return fmt.Errorf("core: set ruleset config %s: %w", name, err)
	}
	return nil
}

// ClearRulesetConfig removes a ruleset override. Silent no-op when
// the row doesn't exist.
func (a *Archive) ClearRulesetConfig(ctx context.Context, name string) error {
	if a.readOnly {
		return ErrReadOnly
	}
	if name == "" {
		return fmt.Errorf("core: clear ruleset config: %w: name required", ErrValidation)
	}
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM hive__config_rulesets WHERE ruleset_name = ?`, name)
	if err != nil {
		return fmt.Errorf("core: clear ruleset config %s: %w", name, err)
	}
	return nil
}

// isKnownRuleset gates SetRulesetConfig — typos in ruleset names
// shouldn't silently persist as inert rows. Kept in sync with the
// bundle list in newHiveValidator.
func isKnownRuleset(name string) bool {
	switch name {
	case "hive", "clb", "tw":
		return true
	}
	return false
}
