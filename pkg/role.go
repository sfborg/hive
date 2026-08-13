package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/sfborg/sflib/pkg/coldp"
)

// Role identifies which of the sfga role tables (creator, contact,
// editor, contributor, publisher) a hive.Agent lives in. sfga models
// each role as its own table with an identical column layout, so
// hive treats them uniformly and parameterizes CRUD on the role
// value. Persistence stays local to a single table per role — no
// join table, no shared "person" abstraction, matching the sfga
// data model exactly.
type Role string

const (
	RoleCreator     Role = "creator"
	RoleContact     Role = "contact"
	RoleEditor      Role = "editor"
	RoleContributor Role = "contributor"
	RolePublisher   Role = "publisher"
)

// AllRoles is the canonical order in which the WUI/TUI render agent
// sections. Kept in one place so a new role (if sfga ever adds one)
// is a one-file change here + a UI addition; nothing loops over
// the tables directly.
var AllRoles = []Role{
	RoleCreator,
	RoleContact,
	RoleEditor,
	RoleContributor,
	RolePublisher,
}

// ParseRole validates a role string. Returns an empty Role and
// ErrValidation when the input isn't one of the known role names.
// Used by the HTTP layer where the role arrives as a URL path
// parameter.
func ParseRole(s string) (Role, error) {
	for _, r := range AllRoles {
		if string(r) == s {
			return r, nil
		}
	}
	return "", fmt.Errorf("core: role %q: %w", s, ErrValidation)
}

// Agent mirrors sflib's coldp.Actor plus the sfga row id and the
// role that identifies which table the row lives in. All 5 sfga
// role tables share this exact column shape; carrying Role in the
// value keeps CRUD polymorphic without a table string sneaking
// into the HTTP wire format.
//
// Note: sfga names the column col__note (singular). Curators use
// it for a role/contribution note ("primary curator", "database
// contact"); the WUI/TUI label reflects that even though the sfga
// column name stays as-is.
type Agent struct {
	ID           int
	Role         Role
	Orcid        string
	Given        string
	Family       string
	RorID        string
	Department   string
	Organisation string
	City         string
	State        string
	Country      string
	Email        string
	URL          string
	Note         string
}

// FromActor copies a coldp.Actor into an Agent under the given role.
// Convenience for callers that already have an Actor (e.g. from an
// import flow) and want to write it via the hive edit API. ID is
// left zero so CreateAgent generates one.
func FromActor(role Role, a coldp.Actor) Agent {
	return Agent{
		Role:         role,
		Orcid:        a.Orcid,
		Given:        a.Given,
		Family:       a.Family,
		RorID:        a.RorID,
		Department:   a.Department,
		Organisation: a.Organization,
		City:         a.City,
		State:        a.State,
		Country:      a.Country,
		Email:        a.Email,
		URL:          a.URL,
		Note:         a.Note,
	}
}

// ToActor materializes a coldp.Actor from this Agent — useful for
// round-tripping into sflib's export path without leaking hive's
// Role wrapper.
func (a Agent) ToActor() coldp.Actor {
	return coldp.Actor{
		Orcid:        a.Orcid,
		Given:        a.Given,
		Family:       a.Family,
		RorID:        a.RorID,
		Department:   a.Department,
		Organization: a.Organisation,
		City:         a.City,
		State:        a.State,
		Country:      a.Country,
		Email:        a.Email,
		URL:          a.URL,
		Note:         a.Note,
	}
}

const agentColumns = `col__id,
	col__orcid, col__given, col__family, col__rorid,
	col__department, col__organisation,
	col__city, col__state, col__country,
	col__email, col__url, col__note`

// ListAgents returns every row from the role's table, ordered by
// family then given so the WUI/TUI card list is stable across
// reloads. sfga role tables are small (dataset agents, not
// taxonomic records) — pagination isn't needed at v0.
func (a *Archive) ListAgents(ctx context.Context, role Role) ([]Agent, error) {
	if _, err := ParseRole(string(role)); err != nil {
		return nil, err
	}
	q := `SELECT ` + agentColumns + ` FROM ` + string(role) + `
		ORDER BY col__family, col__given, col__id`
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("core: list %s agents: %w", role, err)
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		ag, err := scanAgent(rows, role)
		if err != nil {
			return nil, err
		}
		out = append(out, ag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("core: iterate %s agents: %w", role, err)
	}
	return out, nil
}

// GetAgent returns a single agent by role + id. ErrNotFound when the
// row is missing so the HTTP layer maps cleanly to 404.
func (a *Archive) GetAgent(ctx context.Context, role Role, id int) (*Agent, error) {
	if _, err := ParseRole(string(role)); err != nil {
		return nil, err
	}
	q := `SELECT ` + agentColumns + ` FROM ` + string(role) + `
		WHERE col__id = ?`
	row := a.db.QueryRowContext(ctx, q, id)
	ag, err := scanAgent(row, role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: %s agent %d: %w", role, id, ErrNotFound)
		}
		return nil, err
	}
	return &ag, nil
}

// agentScanner is either *sql.Row or *sql.Rows — both expose Scan.
type agentScanner interface {
	Scan(dest ...any) error
}

func scanAgent(s agentScanner, role Role) (Agent, error) {
	var ag Agent
	err := s.Scan(
		&ag.ID,
		&ag.Orcid, &ag.Given, &ag.Family, &ag.RorID,
		&ag.Department, &ag.Organisation,
		&ag.City, &ag.State, &ag.Country,
		&ag.Email, &ag.URL, &ag.Note,
	)
	if err != nil {
		return Agent{}, err
	}
	ag.Role = role
	return ag, nil
}

// validateAgent enforces the sfga NOT NULL constraints on a role
// table before hitting the DB. Errors mention the role so the
// curator-visible message is actionable ("contact requires
// email" vs a generic constraint failure).
//
// Rules (from schema.sql):
//   - creator / contact / editor / contributor require col__given
//     and col__family
//   - contact additionally requires col__email
//   - publisher has no required person-name fields (an organisation
//     with no individual is a valid publisher)
func validateAgent(ag Agent) error {
	if _, err := ParseRole(string(ag.Role)); err != nil {
		return err
	}
	if ag.Role != RolePublisher {
		if ag.Given == "" {
			return fmt.Errorf("core: %s agent: %w: given name required",
				ag.Role, ErrValidation)
		}
		if ag.Family == "" {
			return fmt.Errorf("core: %s agent: %w: family name required",
				ag.Role, ErrValidation)
		}
	}
	if ag.Role == RoleContact && ag.Email == "" {
		return fmt.Errorf("core: contact agent: %w: email required",
			ErrValidation)
	}
	return nil
}

// CreateAgent inserts a new row into the role's table. Returns the
// new col__id assigned by SQLite (AUTOINCREMENT).
//
//   - ag.Role must be a valid role.
//   - Per-role NOT NULL fields (see validateAgent) must be non-empty.
//   - col__metadata_id defaults to 1 via the schema; hive assumes
//     the singleton metadata convention and does not override it.
func (t *Tx) CreateAgent(ag Agent) (int, error) {
	if err := validateAgent(ag); err != nil {
		return 0, err
	}
	q := `INSERT INTO ` + string(ag.Role) + ` (
		col__orcid, col__given, col__family, col__rorid,
		col__department, col__organisation,
		col__city, col__state, col__country,
		col__email, col__url, col__note
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := t.tx.ExecContext(t.ctx, q,
		ag.Orcid, ag.Given, ag.Family, ag.RorID,
		ag.Department, ag.Organisation,
		ag.City, ag.State, ag.Country,
		ag.Email, ag.URL, ag.Note,
	)
	if err != nil {
		return 0, fmt.Errorf("core: insert %s agent: %w", ag.Role, err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("core: %s agent last id: %w", ag.Role, err)
	}
	t.markAgentDirty(ag.Role, int(newID))
	return int(newID), nil
}

// UpdateAgent overwrites the row at (ag.Role, ag.ID) with ag's field
// values. Missing row → ErrNotFound.
func (t *Tx) UpdateAgent(ag Agent) error {
	if err := validateAgent(ag); err != nil {
		return err
	}
	if ag.ID == 0 {
		return fmt.Errorf("core: update %s agent: %w: id required",
			ag.Role, ErrValidation)
	}
	preSnapshot, _ := t.snapshotAgent(ag.Role, ag.ID)
	q := `UPDATE ` + string(ag.Role) + ` SET
		col__orcid = ?, col__given = ?, col__family = ?, col__rorid = ?,
		col__department = ?, col__organisation = ?,
		col__city = ?, col__state = ?, col__country = ?,
		col__email = ?, col__url = ?, col__note = ?
		WHERE col__id = ?`
	res, err := t.tx.ExecContext(t.ctx, q,
		ag.Orcid, ag.Given, ag.Family, ag.RorID,
		ag.Department, ag.Organisation,
		ag.City, ag.State, ag.Country,
		ag.Email, ag.URL, ag.Note,
		ag.ID,
	)
	if err != nil {
		return fmt.Errorf("core: update %s agent %d: %w", ag.Role, ag.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update %s agent %d rows affected: %w",
			ag.Role, ag.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("core: update %s agent %d: %w",
			ag.Role, ag.ID, ErrNotFound)
	}
	t.markAgentDirtyWithPre(ag.Role, ag.ID, preSnapshot)
	return nil
}

// DeleteAgent removes a role row. Missing row → ErrNotFound.
//
// Role tables have no dependents that FK back to them, so unlike
// DeleteReference this doesn't need to guard against downstream
// citations — the row can always be removed cleanly.
func (t *Tx) DeleteAgent(role Role, id int) error {
	if _, err := ParseRole(string(role)); err != nil {
		return err
	}
	if id == 0 {
		return fmt.Errorf("core: delete %s agent: %w: id required",
			role, ErrValidation)
	}
	preSnapshot, _ := t.snapshotAgent(role, id)
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM `+string(role)+` WHERE col__id = ?`, id)
	if err != nil {
		return fmt.Errorf("core: delete %s agent %d: %w", role, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: delete %s agent %d rows affected: %w",
			role, id, err)
	}
	if n == 0 {
		return fmt.Errorf("core: delete %s agent %d: %w",
			role, id, ErrNotFound)
	}
	t.markAgentDeleted(role, id, preSnapshot)
	return nil
}

// CopyAgentToRole creates a new agent in toRole from the source
// (fromRole, id). Returns the new id.
//
// blankNote controls whether the source agent's col__note carries
// over. The note field is role-specific in curator practice (e.g.
// "primary curator" for a creator doesn't survive a copy to
// publisher), so the WUI/TUI default is to blank it on copy and let
// the curator author a fresh role-appropriate note.
func (t *Tx) CopyAgentToRole(
	fromRole Role, id int, toRole Role, blankNote bool,
) (int, error) {
	src, err := t.archive.GetAgent(t.ctx, fromRole, id)
	if err != nil {
		return 0, err
	}
	dst := *src
	dst.ID = 0
	dst.Role = toRole
	if blankNote {
		dst.Note = ""
	}
	return t.CreateAgent(dst)
}

// MoveAgentToRole reassigns an agent from one role to another,
// atomically: creates the row in toRole and deletes it from
// fromRole in the same transaction. The source id is gone after
// the call; the returned id is the newly-created target row.
//
// Same blankNote semantics as CopyAgentToRole — the caller decides
// whether to keep the source's col__note or clear it. Move is
// typically triggered when a curator realises a person was filed
// under the wrong role from the start; clearing the note is a
// sensible default for that flow.
func (t *Tx) MoveAgentToRole(
	fromRole Role, id int, toRole Role, blankNote bool,
) (int, error) {
	if fromRole == toRole {
		return 0, fmt.Errorf("core: move %s agent %d: %w: source and target roles are the same",
			fromRole, id, ErrValidation)
	}
	newID, err := t.CopyAgentToRole(fromRole, id, toRole, blankNote)
	if err != nil {
		return 0, err
	}
	if err := t.DeleteAgent(fromRole, id); err != nil {
		return 0, err
	}
	return newID, nil
}

// snapshotAgent captures a role row as a map keyed by column name.
// Used by the update / delete paths to feed the post-commit
// validation sync loop, which needs both old- and new-side field
// values for aggregate rules that count across a table.
func (t *Tx) snapshotAgent(role Role, id int) (map[string]interface{}, error) {
	if _, err := ParseRole(string(role)); err != nil {
		return nil, err
	}
	q := `SELECT ` + agentColumns + ` FROM ` + string(role) + `
		WHERE col__id = ?`
	ag, err := scanAgent(t.tx.QueryRowContext(t.ctx, q, id), role)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"col__id":           ag.ID,
		"col__orcid":        ag.Orcid,
		"col__given":        ag.Given,
		"col__family":       ag.Family,
		"col__rorid":        ag.RorID,
		"col__department":   ag.Department,
		"col__organisation": ag.Organisation,
		"col__city":         ag.City,
		"col__state":        ag.State,
		"col__country":      ag.Country,
		"col__email":        ag.Email,
		"col__url":          ag.URL,
		"col__note":         ag.Note,
	}, nil
}

// agentKey identifies one row across all 5 role tables. Used as the
// map key in dirtyAgents so a single post-commit loop can drive the
// validation sync for every role without one map per table.
type agentKey struct {
	Role Role
	ID   int
}

func (t *Tx) markAgentDirty(role Role, id int) {
	t.markAgentDirtyWithPre(role, id, nil)
}

func (t *Tx) markAgentDirtyWithPre(role Role, id int, pre map[string]interface{}) {
	if id == 0 {
		return
	}
	if t.dirtyAgents == nil {
		t.dirtyAgents = make(map[agentKey]dirtyEntry)
	}
	k := agentKey{Role: role, ID: id}
	existing, seen := t.dirtyAgents[k]
	if seen && existing.preRecord != nil {
		pre = existing.preRecord
	}
	t.dirtyAgents[k] = dirtyEntry{preRecord: pre, deleted: existing.deleted}
}

func (t *Tx) markAgentDeleted(role Role, id int, pre map[string]interface{}) {
	if id == 0 {
		return
	}
	if t.dirtyAgents == nil {
		t.dirtyAgents = make(map[agentKey]dirtyEntry)
	}
	t.dirtyAgents[agentKey{Role: role, ID: id}] = dirtyEntry{
		preRecord: pre, deleted: true,
	}
}

// syncAgentIssuesWithSnapshot routes to the generic per-table sync
// function used by every aggregate. Agent tables share the string
// record-id convention the validator store uses.
func (a *Archive) syncAgentIssuesWithSnapshot(
	ctx context.Context, role Role, id int, entry dirtyEntry,
) error {
	return a.syncIssuesWithSnapshot(ctx, string(role), strconv.Itoa(id), entry)
}
