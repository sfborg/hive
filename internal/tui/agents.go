package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	hive "github.com/sfborg/hive/pkg"
)

// agentsModel edits the sfga role tables (creator, contact, editor,
// contributor, publisher) as a sub-mode of the metadata screen —
// mirrors what the WUI does with <sfga-agent-section> below the
// metadata dl. A curator opens the pane with `a` from the metadata
// read view; Esc returns to the read view.
//
// Sub-modes:
//   agentsModeBrowse — pick a role (Tab), pick an agent (↑/↓),
//                      Enter=edit, n=new, d=delete, c=copy, Esc=exit
//   agentsModeForm   — Tab through fields, ctrl+s save, Esc cancel
//   agentsModeCopy   — role picker for the quick-copy affordance
type agentsModel struct {
	a        *hive.Archive
	actor    string
	editable bool

	mode agentsMode

	// Browse state.
	role       hive.Role
	agents     []hive.Agent
	selected   int
	err        error
	loading    bool
	// Per-role issue severity indexed by (role, id). Populated
	// lazily each time a role is loaded.
	issueSev map[agentIssueKey]string

	// Form state.
	inputs     [afCount]textinput.Model
	focused    int
	saving     bool
	saveError  string
	editingRow *hive.Agent // nil = create-mode
	// pendingCopy is the target role when a copy or move is in flight;
	// on completion the pane hops to that role so the curator sees
	// the newly-created (or relocated) row immediately.
	pendingCopy hive.Role
	// moving distinguishes the copy-or-move picker's action: false
	// duplicates the source into the target role; true relocates and
	// deletes the source. Set when entering agentsModeCopy via `c`
	// or `m`.
	moving bool
}

type agentsMode int

const (
	agentsModeBrowse agentsMode = iota
	agentsModeForm
	agentsModeCopy
)

// agentIssueKey pairs a role with a record id so validation glyphs
// can be shown next to the right card without extra lookups.
type agentIssueKey struct {
	Role hive.Role
	ID   int
}

// Form field order. Given/Family first (usually required), then the
// identifiers, then affiliation, then contact. Note last — most
// records leave it empty and it's the largest input.
const (
	afGiven = iota
	afFamily
	afOrcid
	afOrganisation
	afRorID
	afDepartment
	afCity
	afState
	afCountry
	afEmail
	afURL
	afNote
	afCount
)

var afLabels = [afCount]string{
	afGiven:        "Given name",
	afFamily:       "Family name",
	afOrcid:        "ORCID iD",
	afOrganisation: "Organisation",
	afRorID:        "ROR ID",
	afDepartment:   "Department",
	afCity:         "City",
	afState:        "State/Region",
	afCountry:      "Country",
	afEmail:        "Email",
	afURL:          "URL",
	afNote:         "Role / contribution note",
}

// Brand colors for the identifier bullets. Matches the WUI's
// vendored ORCID/ROR logos so a curator recognizes the marks
// across frontends.
var (
	orcidBullet = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6CE39")).Bold(true).Render("●")
	rorBullet   = lipgloss.NewStyle().Foreground(lipgloss.Color("#53BEBB")).Bold(true).Render("●")
)

// newAgentsModel constructs a fresh model. Inputs are pre-created
// so entering form mode is instant.
func newAgentsModel(a *hive.Archive, editable bool, actor string) agentsModel {
	m := agentsModel{
		a:        a,
		editable: editable,
		actor:    actor,
		role:     hive.RoleCreator,
		issueSev: map[agentIssueKey]string{},
	}
	for i := range m.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 4000
		m.inputs[i] = ti
	}
	return m
}

// Msgs for async work.
type agentsLoadedMsg struct {
	role   hive.Role
	agents []hive.Agent
	issues map[agentIssueKey]string
	err    error
}
type agentsSavedMsg struct {
	role hive.Role
	err  error
}

// Load fetches the current role's agents plus per-agent issue
// severities. Returned cmd runs when the pane becomes visible.
func (m agentsModel) Load() tea.Cmd {
	role := m.role
	a := m.a
	return func() tea.Msg {
		ctx := context.Background()
		list, err := a.ListAgents(ctx, role)
		if err != nil {
			return agentsLoadedMsg{role: role, err: err}
		}
		iss, _ := loadAgentIssueSeverities(ctx, a, role, list)
		return agentsLoadedMsg{role: role, agents: list, issues: iss}
	}
}

// loadAgentIssueSeverities builds a (role, id) → highest-severity
// map for the loaded agents by scanning the __gsvalidator_results
// cache via ListIssues. Best-effort: failure leaves the map empty
// and rows render without the badge.
func loadAgentIssueSeverities(
	ctx context.Context, a *hive.Archive, role hive.Role, list []hive.Agent,
) (map[agentIssueKey]string, error) {
	out := map[agentIssueKey]string{}
	if len(list) == 0 {
		return out, nil
	}
	issues, _, err := a.ListIssues(ctx, hive.IssueFilter{
		TableName:        string(role),
		HideAcknowledged: false,
	}, 500, 0)
	if err != nil {
		return out, err
	}
	// Index by record id → highest severity.
	byID := map[int]string{}
	for _, is := range issues {
		id := 0
		fmt.Sscanf(is.RecordID, "%d", &id)
		if id == 0 {
			continue
		}
		if sevRank(is.Severity) > sevRank(byID[id]) {
			byID[id] = is.Severity
		}
	}
	for _, ag := range list {
		if sev, ok := byID[ag.ID]; ok {
			out[agentIssueKey{Role: role, ID: ag.ID}] = sev
		}
	}
	return out, nil
}

func sevRank(sev string) int {
	switch strings.ToLower(sev) {
	case "error":
		return 4
	case "warn":
		return 3
	case "info":
		return 2
	case "debug":
		return 1
	}
	return 0
}

// Update handles all keys and async messages for the agents pane.
// Returns quickly with (m, nil) when the pane isn't active.
func (m agentsModel) Update(msg tea.Msg) (agentsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case agentsLoadedMsg:
		if msg.role != m.role {
			return m, nil
		}
		m.loading = false
		m.err = msg.err
		m.agents = msg.agents
		if msg.issues != nil {
			for k, v := range msg.issues {
				m.issueSev[k] = v
			}
		}
		if m.selected >= len(m.agents) {
			m.selected = maxInt(0, len(m.agents)-1)
		}
		return m, nil

	case agentsSavedMsg:
		m.saving = false
		if msg.err != nil {
			m.saveError = formatCoreError(msg.err)
			return m, nil
		}
		m.mode = agentsModeBrowse
		m.editingRow = nil
		// If the save was a cross-role copy, hop to the target role
		// so the curator sees the new row immediately.
		if m.pendingCopy != "" {
			m.role = m.pendingCopy
			m.pendingCopy = ""
		}
		m.loading = true
		return m, m.Load()

	case tea.KeyMsg:
		switch m.mode {
		case agentsModeBrowse:
			return m.updateBrowse(msg)
		case agentsModeForm:
			return m.updateForm(msg)
		case agentsModeCopy:
			return m.updateCopy(msg)
		}
	}
	return m, nil
}

func (m agentsModel) updateBrowse(msg tea.KeyMsg) (agentsModel, tea.Cmd) {
	switch msg.String() {
	case "tab":
		m.role = nextRole(m.role)
		m.selected = 0
		m.loading = true
		return m, m.Load()
	case "shift+tab":
		m.role = prevRole(m.role)
		m.selected = 0
		m.loading = true
		return m, m.Load()
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil
	case "down", "j":
		if m.selected < len(m.agents)-1 {
			m.selected++
		}
		return m, nil
	case "enter", "e":
		if !m.editable || len(m.agents) == 0 {
			return m, nil
		}
		row := m.agents[m.selected]
		m.editingRow = &row
		m.seedForm(&row)
		m.mode = agentsModeForm
		return m, m.focusField(0)
	case "n":
		if !m.editable {
			return m, nil
		}
		m.editingRow = nil
		m.seedForm(nil)
		m.mode = agentsModeForm
		return m, m.focusField(0)
	case "d":
		if !m.editable || len(m.agents) == 0 {
			return m, nil
		}
		row := m.agents[m.selected]
		role := m.role
		id := row.ID
		a := m.a
		actor := m.actor
		return m, func() tea.Msg {
			ctx := contextWithActor(actor)
			err := a.WithTx(ctx, func(tx *hive.Tx) error {
				return tx.DeleteAgent(role, id)
			})
			return agentsSavedMsg{role: role, err: err}
		}
	case "c":
		if !m.editable || len(m.agents) == 0 {
			return m, nil
		}
		m.moving = false
		m.mode = agentsModeCopy
		return m, nil
	case "M":
		// Uppercase M for Move — lowercase `m` is reserved for the
		// alt+m screen switch elsewhere in the shell and shouldn't
		// double as a destructive per-row action.
		if !m.editable || len(m.agents) == 0 {
			return m, nil
		}
		m.moving = true
		m.mode = agentsModeCopy
		return m, nil
	}
	return m, nil
}

func (m agentsModel) updateForm(msg tea.KeyMsg) (agentsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = agentsModeBrowse
		m.saveError = ""
		return m, nil
	case "tab":
		return m, m.focusField((m.focused + 1) % afCount)
	case "shift+tab":
		return m, m.focusField((m.focused - 1 + afCount) % afCount)
	case "ctrl+s":
		return m.saveForm()
	}
	if m.focused >= 0 && m.focused < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m agentsModel) updateCopy(msg tea.KeyMsg) (agentsModel, tea.Cmd) {
	// Digit keys 1..4 pick from the other roles list (source role
	// excluded). Esc cancels. The `moving` flag (set when the
	// picker was opened via `M` rather than `c`) picks between
	// Tx.CopyAgentToRole and Tx.MoveAgentToRole.
	if msg.String() == "esc" {
		m.mode = agentsModeBrowse
		return m, nil
	}
	targets := otherRoles(m.role)
	for i, r := range targets {
		key := fmt.Sprintf("%d", i+1)
		if msg.String() == key {
			target := r
			src := m.role
			id := m.agents[m.selected].ID
			actor := m.actor
			a := m.a
			moving := m.moving
			m.pendingCopy = target
			m.saving = true
			return m, func() tea.Msg {
				ctx := contextWithActor(actor)
				err := a.WithTx(ctx, func(tx *hive.Tx) error {
					if moving {
						_, err := tx.MoveAgentToRole(src, id, target, true)
						return err
					}
					_, err := tx.CopyAgentToRole(src, id, target, true)
					return err
				})
				return agentsSavedMsg{role: target, err: err}
			}
		}
	}
	return m, nil
}

// seedForm populates the input widgets from row (nil = create-mode
// defaults). Called on entering form mode so the fields display the
// existing values immediately.
func (m *agentsModel) seedForm(row *hive.Agent) {
	if row == nil {
		for i := range m.inputs {
			m.inputs[i].SetValue("")
		}
		m.saveError = ""
		return
	}
	m.inputs[afGiven].SetValue(row.Given)
	m.inputs[afFamily].SetValue(row.Family)
	m.inputs[afOrcid].SetValue(row.Orcid)
	m.inputs[afOrganisation].SetValue(row.Organisation)
	m.inputs[afRorID].SetValue(row.RorID)
	m.inputs[afDepartment].SetValue(row.Department)
	m.inputs[afCity].SetValue(row.City)
	m.inputs[afState].SetValue(row.State)
	m.inputs[afCountry].SetValue(row.Country)
	m.inputs[afEmail].SetValue(row.Email)
	m.inputs[afURL].SetValue(row.URL)
	m.inputs[afNote].SetValue(row.Note)
	m.saveError = ""
}

// saveForm collects the input values and writes them via the
// archive. Async — the tea.Cmd returns agentsSavedMsg.
func (m agentsModel) saveForm() (agentsModel, tea.Cmd) {
	ag := hive.Agent{
		Role:         m.role,
		Given:        m.inputs[afGiven].Value(),
		Family:       m.inputs[afFamily].Value(),
		Orcid:        m.inputs[afOrcid].Value(),
		Organisation: m.inputs[afOrganisation].Value(),
		RorID:        m.inputs[afRorID].Value(),
		Department:   m.inputs[afDepartment].Value(),
		City:         m.inputs[afCity].Value(),
		State:        m.inputs[afState].Value(),
		Country:      m.inputs[afCountry].Value(),
		Email:        m.inputs[afEmail].Value(),
		URL:          m.inputs[afURL].Value(),
		Note:         m.inputs[afNote].Value(),
	}
	if m.editingRow != nil {
		ag.ID = m.editingRow.ID
	}
	role := m.role
	a := m.a
	actor := m.actor
	m.saving = true
	return m, func() tea.Msg {
		ctx := contextWithActor(actor)
		err := a.WithTx(ctx, func(tx *hive.Tx) error {
			if ag.ID == 0 {
				_, err := tx.CreateAgent(ag)
				return err
			}
			return tx.UpdateAgent(ag)
		})
		return agentsSavedMsg{role: role, err: err}
	}
}

func (m *agentsModel) focusField(idx int) tea.Cmd {
	if m.focused >= 0 && m.focused < len(m.inputs) {
		m.inputs[m.focused].Blur()
	}
	m.focused = idx
	if idx >= 0 && idx < len(m.inputs) {
		return m.inputs[idx].Focus()
	}
	return nil
}

// Mode reports whether the agents pane owns the current view.
func (m agentsModel) Active() bool {
	return m.mode != agentsModeBrowse ||
		m.role != "" && len(m.agents) >= 0
}

// InForm reports whether an edit form is currently open — used by
// the shell to route ctrl+s / esc to the form.
func (m agentsModel) InForm() bool { return m.mode == agentsModeForm }

// InCopy reports whether the copy-role picker is up.
func (m agentsModel) InCopy() bool { return m.mode == agentsModeCopy }

// View renders the whole pane: the role tabs, the agent list, and
// (in form/copy mode) the form / picker overlay below.
func (m agentsModel) View() string {
	var b strings.Builder
	b.WriteString(m.renderRoleTabs())
	b.WriteByte('\n')
	if m.err != nil {
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
		b.WriteByte('\n')
	}
	if m.loading {
		b.WriteString(dimStyle.Render("loading…\n"))
	}
	switch m.mode {
	case agentsModeBrowse:
		b.WriteString(m.renderBrowse())
	case agentsModeForm:
		b.WriteString(m.renderForm())
	case agentsModeCopy:
		b.WriteString(m.renderBrowse())
		b.WriteString("\n\n")
		b.WriteString(m.renderCopyPicker())
	}
	return b.String()
}

func (m agentsModel) renderRoleTabs() string {
	parts := make([]string, 0, len(hive.AllRoles))
	for _, r := range hive.AllRoles {
		label := titleCase(string(r)) + "s"
		if r == m.role {
			parts = append(parts, focusedStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, dimStyle.Render(" "+label+" "))
		}
	}
	return strings.Join(parts, "  ")
}

func (m agentsModel) renderBrowse() string {
	var b strings.Builder
	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("No %ss.", m.role)))
	} else {
		for i, ag := range m.agents {
			prefix := "  "
			if i == m.selected {
				prefix = focusedStyle.Render("▶ ")
			}
			b.WriteString(prefix)
			b.WriteString(renderAgentLine(ag, m.issueSev[agentIssueKey{Role: m.role, ID: ag.ID}]))
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	if m.editable {
		b.WriteString(dimStyle.Render(
			"[tab] role   [↑↓] select   [enter/e] edit   [n] new   [c] copy   [M] move   [d] delete   [esc] back",
		))
	} else {
		b.WriteString(dimStyle.Render("[tab] role   [↑↓] select   [esc] back"))
	}
	return b.String()
}

// renderAgentLine formats one row for the browse list. Colored
// bullet before the identifier stands in for the ORCID/ROR logo
// the WUI shows — same brand hue so a curator recognizes it.
func renderAgentLine(ag hive.Agent, sev string) string {
	name := strings.TrimSpace(strings.Join(
		[]string{ag.Family, ag.Given}, ", "))
	if name == ", " || name == "" {
		if ag.Organisation != "" {
			name = ag.Organisation
		} else {
			name = "(unnamed)"
		}
	}
	parts := []string{headerStyle.Render(name)}
	if ag.Orcid != "" {
		parts = append(parts, orcidBullet+" "+ag.Orcid)
	}
	if ag.RorID != "" {
		parts = append(parts, rorBullet+" "+ag.RorID)
	}
	if ag.Email != "" {
		parts = append(parts, dimStyle.Render(ag.Email))
	}
	line := strings.Join(parts, "   ")
	if sev != "" {
		line = severityBulletTUI(sev) + " " + line
	}
	return line
}

func severityBulletTUI(sev string) string {
	switch strings.ToLower(sev) {
	case "error":
		return sevErrorStyle.Render("✕")
	case "warn":
		return sevWarnStyle.Render("⚠")
	case "info":
		return sevInfoStyle.Render("ⓘ")
	case "debug":
		return sevDebugStyle.Render("🐛")
	}
	return " "
}

func (m agentsModel) renderForm() string {
	var b strings.Builder
	title := "Edit"
	if m.editingRow == nil {
		title = "Add"
	}
	b.WriteString(sectionHeaderStyle.Render(fmt.Sprintf("── %s %s ──", title, titleCase(string(m.role)))))
	b.WriteByte('\n')
	for i := 0; i < afCount; i++ {
		req := ""
		if isRequired(m.role, i) {
			req = errStyle.Render(" *")
		}
		label := labelStyle.Render(fmt.Sprintf("%-24s", afLabels[i]+":")) + req
		b.WriteString(label)
		b.WriteByte(' ')
		b.WriteString(m.inputs[i].View())
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if m.saveError != "" {
		b.WriteString(errStyle.Render(m.saveError))
		b.WriteString("\n\n")
	}
	if m.saving {
		b.WriteString(dimStyle.Render("saving…"))
	} else {
		b.WriteString(dimStyle.Render("[tab] next   [ctrl+s] save   [esc] cancel"))
	}
	return b.String()
}

func (m agentsModel) renderCopyPicker() string {
	var b strings.Builder
	verb := "Copy"
	if m.moving {
		verb = "Move"
	}
	b.WriteString(sectionHeaderStyle.Render(fmt.Sprintf("── %s to which role? ──", verb)))
	b.WriteByte('\n')
	for i, r := range otherRoles(m.role) {
		b.WriteString(fmt.Sprintf("  [%d] %s\n", i+1, titleCase(string(r))+"s"))
	}
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(fmt.Sprintf("[digit] %s   [esc] cancel", strings.ToLower(verb))))
	b.WriteString("\n")
	if m.moving {
		b.WriteString(dimStyle.Render("(source row will be deleted; role/contribution note blanked)"))
	} else {
		b.WriteString(dimStyle.Render("(source row stays in place; role/contribution note blanked)"))
	}
	return b.String()
}

// isRequired mirrors the schema's NOT NULL fields per role.
//
//	creator / contact / editor / contributor — given, family required
//	contact — email additionally required
//	publisher — nothing required at the person-name level
func isRequired(role hive.Role, field int) bool {
	if role == hive.RolePublisher {
		return false
	}
	if field == afGiven || field == afFamily {
		return true
	}
	if role == hive.RoleContact && field == afEmail {
		return true
	}
	return false
}

// nextRole / prevRole walk the canonical role order so Tab / Shift-
// Tab cycles through the sections predictably.
func nextRole(r hive.Role) hive.Role {
	for i, rr := range hive.AllRoles {
		if rr == r {
			return hive.AllRoles[(i+1)%len(hive.AllRoles)]
		}
	}
	return hive.AllRoles[0]
}
func prevRole(r hive.Role) hive.Role {
	for i, rr := range hive.AllRoles {
		if rr == r {
			return hive.AllRoles[(i-1+len(hive.AllRoles))%len(hive.AllRoles)]
		}
	}
	return hive.AllRoles[0]
}

func otherRoles(r hive.Role) []hive.Role {
	out := make([]hive.Role, 0, len(hive.AllRoles)-1)
	for _, rr := range hive.AllRoles {
		if rr != r {
			out = append(out, rr)
		}
	}
	return out
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

