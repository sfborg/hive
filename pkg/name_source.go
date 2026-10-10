package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// nameSourcesSetting is the project setting holding the GNverifier data
// sources the name-mention index uses, in preference order.
const nameSourcesSetting = "name_sources"

// storedSource is a name source as kept in the project setting.
type storedSource struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
	Short string `json:"short,omitempty"`
}

// NameSources returns the GNverifier data sources the archive's
// name-mention index uses, in preference order: the curator's choice,
// or mentions.DefaultSources when none is stored.
func (a *Archive) NameSources(ctx context.Context) ([]mentions.Source, error) {
	return readNameSources(ctx, a.db)
}

func readNameSources(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) ([]mentions.Source, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT value FROM hive__setting WHERE actor = ? AND key = ?`,
		projectActor, nameSourcesSetting).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return mentions.DefaultSources, nil
	}
	if err != nil {
		return nil, fmt.Errorf("core: read name sources: %w", err)
	}
	var stored []storedSource
	if err := json.Unmarshal([]byte(value), &stored); err != nil || len(stored) == 0 {
		return mentions.DefaultSources, nil
	}
	out := make([]mentions.Source, len(stored))
	for i, s := range stored {
		out[i] = mentions.Source{ID: s.ID, Key: mentions.KeyFor(s.ID), Label: s.Label, Short: s.Short}
	}
	return out, nil
}

// SetNameSources stores the data sources the name-mention index uses,
// in preference order. The index keeps rows from earlier sources until
// it is rebuilt (see IndexNameMentions).
func (t *Tx) SetNameSources(sources []mentions.Source) error {
	if len(sources) == 0 {
		return fmt.Errorf("%w: pick at least one name source", ErrValidation)
	}
	seen := map[int]bool{}
	stored := make([]storedSource, 0, len(sources))
	for _, s := range sources {
		if s.ID <= 0 {
			return fmt.Errorf("%w: invalid data source ID %d", ErrValidation, s.ID)
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		label := s.Label
		if label == "" {
			label = fmt.Sprintf("Data source %d", s.ID)
		}
		stored = append(stored, storedSource{ID: s.ID, Label: label, Short: s.Short})
	}
	return t.SetProjectSetting(nameSourcesSetting, stored)
}
