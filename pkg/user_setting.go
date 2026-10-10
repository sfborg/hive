package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxUserSettingBytes caps one setting's JSON value.
const MaxUserSettingBytes = 64 << 10

// UserSettings returns every setting stored for actor, keyed by
// setting name, with values as raw JSON. Project settings (the empty
// actor) are not included.
func (a *Archive) UserSettings(ctx context.Context, actor string) (map[string]json.RawMessage, error) {
	if actor == "" {
		return map[string]json.RawMessage{}, nil
	}
	rows, err := a.db.QueryContext(ctx,
		`SELECT key, value FROM hive__setting WHERE actor = ?`, actor)
	if err != nil {
		return nil, fmt.Errorf("core: list user settings: %w", err)
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("core: scan user setting: %w", err)
		}
		out[key] = json.RawMessage(value)
	}
	return out, rows.Err()
}

// projectActor is the actor project-wide settings are stored under.
const projectActor = ""

// ProjectSetting decodes the project-wide setting key into dst and
// reports whether it is set.
func (a *Archive) ProjectSetting(ctx context.Context, key string, dst any) (bool, error) {
	var value string
	err := a.db.QueryRowContext(ctx,
		`SELECT value FROM hive__setting WHERE actor = ? AND key = ?`, projectActor, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("core: read project setting %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(value), dst); err != nil {
		return false, fmt.Errorf("core: decode project setting %s: %w", key, err)
	}
	return true, nil
}

// SetProjectSetting stores a project-wide setting, encoded as JSON.
func (t *Tx) SetProjectSetting(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("core: encode project setting %s: %w", key, err)
	}
	return t.setSetting(projectActor, key, raw)
}

// SetUserSetting stores one setting for the transaction's actor.
// value must be valid JSON.
func (t *Tx) SetUserSetting(key string, value json.RawMessage) error {
	actor := ActorFromContext(t.ctx)
	if actor == projectActor {
		return fmt.Errorf("%w: user settings need an actor", ErrValidation)
	}
	return t.setSetting(actor, key, value)
}

func (t *Tx) setSetting(actor, key string, value json.RawMessage) error {
	if key == "" || len(key) > 256 {
		return fmt.Errorf("%w: setting key must be 1-256 characters", ErrValidation)
	}
	if len(value) > MaxUserSettingBytes {
		return fmt.Errorf("%w: setting value larger than %d bytes", ErrValidation, MaxUserSettingBytes)
	}
	if !json.Valid(value) {
		return fmt.Errorf("%w: setting value is not valid JSON", ErrValidation)
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO hive__setting (actor, key, value, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (actor, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		actor, key, string(value), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("core: set setting %s: %w", key, err)
	}
	return nil
}

// DeleteUserSetting removes one setting for the transaction's actor.
// Deleting a setting that does not exist is not an error.
func (t *Tx) DeleteUserSetting(key string) error {
	_, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__setting WHERE actor = ? AND key = ?`,
		ActorFromContext(t.ctx), key)
	if err != nil {
		return fmt.Errorf("core: delete user setting %s: %w", key, err)
	}
	return nil
}
