package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Prompt is one row of hive__prompt — a curator-authored fork of a
// shipped prompt (or a fork of another curator prompt). Shipped
// built-ins stay in pkg/prompts/ as embedded .md files and are NOT
// represented here; the server merges both sources when listing.
//
// See the schema comment on hive__prompt in hive_tables.go for
// column semantics.
type Prompt struct {
	// PromptID is a UUID. Populated by CreatePrompt when empty.
	PromptID string
	// Name is the curator-chosen slug. Unique per (name, purpose).
	Name string
	// Purpose routes the prompt to a pipeline stage: "ner" or
	// "compose".
	Purpose string
	// Content is the prompt body as the LLM will see it —
	// frontmatter and other metadata are up to the curator.
	Content string
	// ForkOf is the source prompt's stable ID at fork time.
	// For a fork of a shipped built-in, this is the built-in's
	// name ("ner_v6"). For a fork of another curator prompt, this
	// is that prompt's UUID. Non-empty by construction (v0 always
	// forks; no create-from-scratch flow yet).
	ForkOf string
	// IsActive false means soft-deleted. Row stays for provenance
	// (an extraction_run tagged with this prompt still resolves)
	// but hidden from default picker lists.
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// CreatedBy is the ORCID (or config fallback) of the curator
	// who forked. Stamped from context actor when unset.
	CreatedBy string
}

// CreatePrompt inserts a new curator prompt fork. If PromptID is
// empty a UUID is minted. CreatedAt/UpdatedAt are stamped from
// wall-clock time. CreatedBy is stamped from the context actor
// unless already set. Returns the assigned PromptID.
func (t *Tx) CreatePrompt(p Prompt) (string, error) {
	if strings.TrimSpace(p.Name) == "" {
		return "", errors.New("core: CreatePrompt: name is required")
	}
	if p.Purpose != "ner" && p.Purpose != "compose" {
		return "", fmt.Errorf(
			"core: CreatePrompt: purpose must be 'ner' or 'compose' (got %q)",
			p.Purpose,
		)
	}
	if strings.TrimSpace(p.Content) == "" {
		return "", errors.New("core: CreatePrompt: content is required")
	}
	if strings.TrimSpace(p.ForkOf) == "" {
		return "", errors.New("core: CreatePrompt: fork_of is required")
	}
	if p.PromptID == "" {
		p.PromptID = uuid.NewString()
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	if p.CreatedBy == "" {
		p.CreatedBy = ActorFromContext(t.ctx)
	}
	const q = `INSERT INTO hive__prompt
		(prompt_id, name, purpose, content, fork_of,
		 is_active, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)`
	_, err := t.tx.ExecContext(t.ctx, q,
		p.PromptID, p.Name, p.Purpose, p.Content, p.ForkOf,
		p.CreatedAt.Format(time.RFC3339Nano),
		p.UpdatedAt.Format(time.RFC3339Nano),
		p.CreatedBy,
	)
	if err != nil {
		return "", fmt.Errorf("core: insert hive__prompt: %w", err)
	}
	return p.PromptID, nil
}

// GetPrompt returns one prompt by ID. Returns [ErrNotFound] when
// nothing matches. Soft-deleted rows are returned so provenance
// lookups (an extraction_run pointing at a deleted prompt) still
// resolve.
func (a *Archive) GetPrompt(ctx context.Context, id string) (*Prompt, error) {
	row := a.db.QueryRowContext(ctx, promptSelectSQL+` WHERE prompt_id = ?`, id)
	p, err := scanPrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("core: prompt %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// PromptListFilter narrows [Archive.ListPrompts]. Empty filter
// returns every active prompt, newest first.
type PromptListFilter struct {
	// Purpose restricts to "ner" or "compose". Empty = both.
	Purpose string
	// IncludeInactive returns soft-deleted rows when true.
	// Default false — the picker never shows deleted prompts.
	IncludeInactive bool
}

// ListPrompts returns curator prompts matching the filter.
// Shipped built-ins are NOT included — merge with the pkg/prompts
// listing in the HTTP layer.
func (a *Archive) ListPrompts(ctx context.Context, f PromptListFilter) ([]Prompt, error) {
	q := promptSelectSQL + ` WHERE 1=1`
	var args []any
	if f.Purpose != "" {
		q += ` AND purpose = ?`
		args = append(args, f.Purpose)
	}
	if !f.IncludeInactive {
		q += ` AND is_active = 1`
	}
	q += ` ORDER BY created_at DESC, prompt_id ASC`
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: list prompts: %w", err)
	}
	defer rows.Close()
	var out []Prompt
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdatePromptContent overwrites the content of an existing
// curator prompt. Name and purpose are immutable post-create
// (renaming would break provenance lookups on extraction_runs
// tagged with the prompt). Bumps updated_at.
func (t *Tx) UpdatePromptContent(id, content string) error {
	if id == "" {
		return errors.New("core: UpdatePromptContent: id is required")
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("core: UpdatePromptContent: content is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__prompt SET content = ?, updated_at = ?
		 WHERE prompt_id = ? AND is_active = 1`,
		content, now, id,
	)
	if err != nil {
		return fmt.Errorf("core: update prompt %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: prompt %q: %w", id, ErrNotFound)
	}
	return nil
}

// SoftDeletePrompt flips is_active to 0. Row stays for provenance
// lookups but hidden from default picker lists. Reversible via
// RestorePrompt (deferred; not exposed in v0 UI).
func (t *Tx) SoftDeletePrompt(id string) error {
	if id == "" {
		return errors.New("core: SoftDeletePrompt: id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__prompt SET is_active = 0, updated_at = ?
		 WHERE prompt_id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("core: soft-delete prompt %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: prompt %q: %w", id, ErrNotFound)
	}
	return nil
}

const promptSelectSQL = `SELECT
	prompt_id, name, purpose, content, fork_of,
	is_active, created_at, updated_at, created_by
FROM hive__prompt`

func scanPrompt(r rowScanner) (*Prompt, error) {
	var (
		p                  Prompt
		isActive           int
		createdAt, updated string
	)
	if err := r.Scan(
		&p.PromptID, &p.Name, &p.Purpose, &p.Content, &p.ForkOf,
		&isActive, &createdAt, &updated, &p.CreatedBy,
	); err != nil {
		return nil, err
	}
	p.IsActive = isActive == 1
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		p.UpdatedAt = t
	}
	return &p, nil
}
