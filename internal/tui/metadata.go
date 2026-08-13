package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	hive "github.com/sfborg/hive/pkg"
)

// Metadata edit-form fields, in tab order. Layout groups identity
// first (Title, Alias, Version, Issued, DOI, URL, Logo, Label),
// then content (Description, Keywords, Citation), then scope
// (Geographic / Taxonomic / Temporal), then quality (Confidence,
// Completeness), then License, then Private. Title is required by
// the sfga schema (NOT NULL); the rest are optional.
const (
	mfTitle = iota
	mfAlias
	mfVersion
	mfIssued
	mfDOI
	mfURL
	mfLogo
	mfLabel
	mfDescription
	mfKeywords
	mfCitation
	mfGeographic
	mfTaxonomic
	mfTemporal
	mfConfidence
	mfCompleteness
	mfLicense
	// Private is a tri-state (unset / public / private), rendered via
	// the same cycler pattern as extinct in detail.go.
	mfPrivate
	mfCount
)

var mfLabels = [mfCount]string{
	mfTitle:        "Title *",
	mfAlias:        "Alias",
	mfVersion:      "Version",
	mfIssued:       "Issued",
	mfDOI:          "DOI",
	mfURL:          "URL",
	mfLogo:         "Logo URL",
	mfLabel:        "Label",
	mfDescription:  "Description",
	mfKeywords:     "Keywords",
	mfCitation:     "Citation",
	mfGeographic:   "Geographic scope",
	mfTaxonomic:    "Taxonomic scope",
	mfTemporal:     "Temporal scope",
	mfConfidence:   "Confidence (1–5)",
	mfCompleteness: "Completeness (%)",
	mfLicense:      "License",
	mfPrivate:      "Private",
}

// metadataModel is the metadata screen. Mirrors detailModel's edit
// pattern: read view by default, ctrl+s save / esc cancel in edit.
//
// The metadata pane also hosts the agents sub-mode: pressing `a`
// from the read view flips inAgents=true and delegates rendering /
// key handling to `agents`. Esc from the agents browse pane returns
// here. Matches the WUI layout, which renders <sfga-agent-section>
// entries below the metadata dl.
type metadataModel struct {
	a        *hive.Archive
	editable bool
	actor    string

	metadata *hive.Metadata
	loading  bool
	err      error

	editing      bool
	inputs       [mfCount]textinput.Model
	focusedField int
	privateState int // 0=unset, 1=private, 2=public
	saving       bool
	saveError    string

	// Agents sub-mode. When inAgents is true, the metadata pane
	// hands rendering + input to `agents` until Esc or a mode
	// transition returns focus here.
	inAgents bool
	agents   agentsModel

	// perRoleAgents caches every role's rows so the read view can
	// render them inline (Contact / Creators / … as rows in the
	// metadata field grid) without a per-render fetch. Populated by
	// loadAllAgentsCmd after the initial metadata load and again
	// after every agent save/copy/move so the display stays fresh.
	perRoleAgents map[hive.Role][]hive.Agent
}

func newMetadataModel(a *hive.Archive, editable bool, actor string) metadataModel {
	m := metadataModel{a: a, editable: editable, actor: actor}
	for i := range m.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 4000
		m.inputs[i] = ti
	}
	m.agents = newAgentsModel(a, editable, actor)
	return m
}

// metadataLoadedMsg carries the fetched metadata back to the model.
type metadataLoadedMsg struct {
	metadata *hive.Metadata
	err      error
}

// metadataSavedMsg reports a Save round-trip result. On success the
// caller's cached metadata title is refreshed too.
type metadataSavedMsg struct {
	metadata *hive.Metadata
	err      error
}

// metadataAgentsLoadedMsg delivers the per-role agent lists used by
// the metadata read view's inline sections. Best-effort: an error
// leaves the previous cache intact.
type metadataAgentsLoadedMsg struct {
	perRole map[hive.Role][]hive.Agent
	err     error
}

// Load kicks off an async GetMetadata fetch. Returned cmd is fired by
// the shell when the metadata screen becomes visible.
func (m metadataModel) Load() tea.Cmd {
	a := m.a
	return tea.Batch(
		func() tea.Msg {
			md, err := a.GetMetadata(context.Background())
			return metadataLoadedMsg{metadata: md, err: err}
		},
		loadAllAgentsCmd(a),
	)
}

// loadAllAgentsCmd fetches every role's rows in one go so the read
// view can render inline agent sections without per-render round
// trips. Called from Load() at startup and again after any agent
// mutation (create / update / delete / copy / move).
func loadAllAgentsCmd(a *hive.Archive) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		out := make(map[hive.Role][]hive.Agent, len(hive.AllRoles))
		for _, r := range hive.AllRoles {
			list, err := a.ListAgents(ctx, r)
			if err != nil {
				return metadataAgentsLoadedMsg{err: err}
			}
			out[r] = list
		}
		return metadataAgentsLoadedMsg{perRole: out}
	}
}

// Editing reports whether the pane is in edit mode.
func (m metadataModel) Editing() bool { return m.editing }

// HasUnsavedChanges reports whether the edit form holds a draft
// that differs from the loaded row. Consulted by the shell before
// switching screens so a stray alt+t / alt+r / alt+i doesn't
// silently discard curator work.
//
// Compares each field's textinput value against the snapshot the
// form was seeded from. Any mismatch on a required field, or a
// change on the private tri-state, counts as unsaved.
func (m metadataModel) HasUnsavedChanges() bool {
	if !m.editing || m.metadata == nil {
		return false
	}
	md := m.metadata
	if strings.TrimSpace(m.inputs[mfTitle].Value()) != md.Title {
		return true
	}
	stringFields := []struct {
		idx int
		v   string
	}{
		{mfAlias, md.Alias},
		{mfVersion, md.Version},
		{mfIssued, md.Issued},
		{mfDOI, md.DOI},
		{mfURL, md.URL},
		{mfLogo, md.Logo},
		{mfLabel, md.Label},
		{mfDescription, md.Description},
		{mfKeywords, md.Keywords},
		{mfCitation, md.Citation},
		{mfGeographic, md.GeographicScope},
		{mfTaxonomic, md.TaxonomicScope},
		{mfTemporal, md.TemporalScope},
		{mfLicense, md.License},
	}
	for _, f := range stringFields {
		if m.inputs[f.idx].Value() != f.v {
			return true
		}
	}
	if inputInt(m.inputs[mfConfidence].Value()) != intPtrValue(md.Confidence) {
		return true
	}
	if inputInt(m.inputs[mfCompleteness].Value()) != intPtrValue(md.Completeness) {
		return true
	}
	var state int
	switch {
	case md.Private == nil:
		state = 0
	case *md.Private:
		state = 1
	default:
		state = 2
	}
	if m.privateState != state {
		return true
	}
	return false
}

// inputInt returns the number sentinel used by HasUnsavedChanges
// to compare a text input against a *int metadata field. Empty
// input → sentinel matches nil-pointer's sentinel; a real number
// returns itself. Parse failure is treated as "unchanged" — the
// form's Save path will surface the parse error if the curator
// actually submits.
func inputInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func intPtrValue(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// CanEdit reports whether entering edit mode is legal.
func (m metadataModel) CanEdit() bool {
	return m.editable && m.metadata != nil && !m.editing
}

// EnterEditModeCmd seeds the form from the current metadata and takes
// edit focus. Returns the initial-focus cmd.
func (m *metadataModel) EnterEditModeCmd() (tea.Cmd, bool) {
	if !m.CanEdit() {
		return nil, false
	}
	m.editing = true
	m.saveError = ""
	md := m.metadata
	m.inputs[mfTitle].SetValue(md.Title)
	m.inputs[mfAlias].SetValue(md.Alias)
	m.inputs[mfVersion].SetValue(md.Version)
	m.inputs[mfIssued].SetValue(md.Issued)
	m.inputs[mfDOI].SetValue(md.DOI)
	m.inputs[mfURL].SetValue(md.URL)
	m.inputs[mfLogo].SetValue(md.Logo)
	m.inputs[mfLabel].SetValue(md.Label)
	m.inputs[mfDescription].SetValue(md.Description)
	m.inputs[mfKeywords].SetValue(md.Keywords)
	m.inputs[mfCitation].SetValue(md.Citation)
	m.inputs[mfGeographic].SetValue(md.GeographicScope)
	m.inputs[mfTaxonomic].SetValue(md.TaxonomicScope)
	m.inputs[mfTemporal].SetValue(md.TemporalScope)
	if md.Confidence != nil {
		m.inputs[mfConfidence].SetValue(strconv.Itoa(*md.Confidence))
	} else {
		m.inputs[mfConfidence].SetValue("")
	}
	if md.Completeness != nil {
		m.inputs[mfCompleteness].SetValue(strconv.Itoa(*md.Completeness))
	} else {
		m.inputs[mfCompleteness].SetValue("")
	}
	m.inputs[mfLicense].SetValue(md.License)
	switch {
	case md.Private == nil:
		m.privateState = 0
	case *md.Private:
		m.privateState = 1
	default:
		m.privateState = 2
	}
	m.focusedField = 0
	return m.focusField(0), true
}

// ExitEditMode drops any unsaved changes and returns to view mode.
func (m *metadataModel) ExitEditMode() {
	m.editing = false
	m.saveError = ""
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
}

// Save commits the form. Returns a metadataSavedMsg; the shell
// re-caches the title on success.
func (m *metadataModel) Save() tea.Cmd {
	if m.metadata == nil {
		return nil
	}
	title := strings.TrimSpace(m.inputs[mfTitle].Value())
	if title == "" {
		m.saveError = "title is required"
		return nil
	}
	snapshot := *m.metadata
	snapshot.Title = title
	snapshot.Alias = m.inputs[mfAlias].Value()
	snapshot.Version = m.inputs[mfVersion].Value()
	snapshot.Issued = m.inputs[mfIssued].Value()
	snapshot.DOI = m.inputs[mfDOI].Value()
	snapshot.URL = m.inputs[mfURL].Value()
	snapshot.Logo = m.inputs[mfLogo].Value()
	snapshot.Label = m.inputs[mfLabel].Value()
	snapshot.Description = m.inputs[mfDescription].Value()
	snapshot.Keywords = m.inputs[mfKeywords].Value()
	snapshot.Citation = m.inputs[mfCitation].Value()
	snapshot.GeographicScope = m.inputs[mfGeographic].Value()
	snapshot.TaxonomicScope = m.inputs[mfTaxonomic].Value()
	snapshot.TemporalScope = m.inputs[mfTemporal].Value()
	snapshot.License = m.inputs[mfLicense].Value()
	// Optional numerics: empty stays nil (unset). Bad input becomes an
	// inline error so the curator can fix and re-submit without
	// losing the rest of the form.
	if v := strings.TrimSpace(m.inputs[mfConfidence].Value()); v == "" {
		snapshot.Confidence = nil
	} else {
		n, err := strconv.Atoi(v)
		if err != nil {
			m.saveError = "confidence must be a number (1–5) or empty"
			return nil
		}
		snapshot.Confidence = &n
	}
	if v := strings.TrimSpace(m.inputs[mfCompleteness].Value()); v == "" {
		snapshot.Completeness = nil
	} else {
		n, err := strconv.Atoi(v)
		if err != nil {
			m.saveError = "completeness must be a number (0–100) or empty"
			return nil
		}
		snapshot.Completeness = &n
	}
	switch m.privateState {
	case 1:
		v := true
		snapshot.Private = &v
	case 2:
		v := false
		snapshot.Private = &v
	default:
		snapshot.Private = nil
	}

	a := m.a
	actor := m.actor
	m.saving = true
	return func() tea.Msg {
		ctx := contextWithActor(actor)
		err := a.WithTx(ctx, func(tx *hive.Tx) error {
			return tx.UpdateMetadata(snapshot)
		})
		if err != nil {
			return metadataSavedMsg{err: err}
		}
		fresh, err := a.GetMetadata(ctx)
		if err != nil {
			return metadataSavedMsg{err: err}
		}
		return metadataSavedMsg{metadata: fresh}
	}
}

// Update handles metadata-screen messages: load result, save result,
// and key events (in edit mode).
func (m metadataModel) Update(msg tea.Msg) (metadataModel, tea.Cmd) {
	// Route async agent-pane messages to the sub-model always — a
	// save result may arrive after the curator has Esc'd out, and
	// we want the agents cache to stay coherent for the next visit.
	// agentsSavedMsg additionally triggers a per-role reload so the
	// metadata read view's inline sections reflect the change.
	switch msg := msg.(type) {
	case agentsLoadedMsg:
		var cmd tea.Cmd
		m.agents, cmd = m.agents.Update(msg)
		return m, cmd
	case agentsSavedMsg:
		var cmd tea.Cmd
		m.agents, cmd = m.agents.Update(msg)
		return m, tea.Batch(cmd, loadAllAgentsCmd(m.a))
	case metadataAgentsLoadedMsg:
		if msg.err == nil {
			m.perRoleAgents = msg.perRole
		}
		return m, nil
	}
	// While the agents pane owns the screen, forward keys to it.
	// Esc from browse mode returns focus to the metadata read view;
	// other modes (form / copy picker) consume Esc themselves.
	if m.inAgents {
		if km, ok := msg.(tea.KeyMsg); ok {
			if km.String() == "esc" && !m.agents.InForm() && !m.agents.InCopy() {
				m.inAgents = false
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.agents, cmd = m.agents.Update(msg)
		return m, cmd
	}
	switch msg := msg.(type) {
	case metadataLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.metadata = msg.metadata
		return m, nil

	case metadataSavedMsg:
		m.saving = false
		if msg.err != nil {
			m.saveError = formatCoreError(msg.err)
			return m, nil
		}
		m.metadata = msg.metadata
		m.editing = false
		for i := range m.inputs {
			m.inputs[i].Blur()
		}
		return m, nil

	case tea.KeyMsg:
		// `a` from the read view enters the agents sub-mode and
		// kicks off the initial load (creators first).
		if !m.editing && msg.String() == "a" && m.metadata != nil {
			m.inAgents = true
			return m, m.agents.Load()
		}
		if !m.editing {
			return m, nil
		}
		switch msg.String() {
		case "tab":
			return m, m.nextField()
		case "shift+tab":
			return m, m.prevField()
		case " ":
			if m.focusedField == mfPrivate {
				m.privateState = (m.privateState + 1) % 3
				return m, nil
			}
		}
		if m.focusedField == mfPrivate {
			return m, nil // cycler owns key handling; ignore other keys
		}
		if m.focusedField >= 0 && m.focusedField < len(m.inputs) {
			var cmd tea.Cmd
			m.inputs[m.focusedField], cmd = m.inputs[m.focusedField].Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *metadataModel) focusField(idx int) tea.Cmd {
	if m.focusedField >= 0 && m.focusedField < len(m.inputs) {
		m.inputs[m.focusedField].Blur()
	}
	m.focusedField = idx
	if idx >= 0 && idx < len(m.inputs) {
		return m.inputs[idx].Focus()
	}
	return nil
}

func (m *metadataModel) nextField() tea.Cmd {
	return m.focusField((m.focusedField + 1) % mfCount)
}
func (m *metadataModel) prevField() tea.Cmd {
	return m.focusField((m.focusedField - 1 + mfCount) % mfCount)
}

// View renders the metadata pane. Read view is a dl-style listing;
// edit mode is the labelled form; agents mode delegates to the
// agentsModel.
func (m metadataModel) View() string {
	if m.loading {
		return dimStyle.Render("loading…")
	}
	if m.err != nil {
		return errStyle.Render("error: " + m.err.Error())
	}
	if m.metadata == nil {
		return dimStyle.Render("(no metadata — press e to seed)")
	}
	if m.inAgents {
		return m.agents.View()
	}
	if m.editing {
		return m.renderForm()
	}
	return m.renderRead()
}

func (m metadataModel) renderRead() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(m.metadata.Title))
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(strings.Repeat("─", max(20, len([]rune(m.metadata.Title))))))
	b.WriteString("\n\n")

	push := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(labelStyle.Render(fmt.Sprintf("%-18s", k+":")))
		b.WriteByte(' ')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	md := m.metadata
	push("Alias", md.Alias)
	push("Version", md.Version)
	push("Issued", md.Issued)
	push("DOI", md.DOI)
	push("URL", md.URL)
	push("Logo", md.Logo)
	push("Label", md.Label)
	push("Description", md.Description)
	push("Keywords", md.Keywords)
	push("Citation", md.Citation)
	push("Geographic scope", md.GeographicScope)
	push("Taxonomic scope", md.TaxonomicScope)
	push("Temporal scope", md.TemporalScope)
	if md.Confidence != nil {
		push("Confidence", renderStars(*md.Confidence, 5))
	}
	if md.Completeness != nil {
		push("Completeness", strconv.Itoa(*md.Completeness)+"%")
	}
	push("License", md.License)
	if md.Private != nil {
		v := "public"
		if *md.Private {
			v = "private"
		}
		push("Access", v)
	}

	// Inline agent sections. Same order as the WUI so a curator
	// switching frontends sees the fields in the same place. Empty
	// sections render "(none)" so the reader knows the field exists
	// but is unfilled — mirrors how ChecklistBank surfaces empty
	// slots.
	b.WriteByte('\n')
	for _, sec := range agentReadSections {
		m.writeAgentSection(&b, sec.role, sec.label)
	}

	b.WriteByte('\n')
	if m.editable {
		b.WriteString(dimStyle.Render("[e] edit   [a] agents (creators / contacts / editors / …)"))
	} else {
		b.WriteString(dimStyle.Render("[a] agents"))
	}
	return b.String()
}

// agentReadSections is the display order for inline agent rows in
// the metadata read view. Matches SfgaMetadata's dl order: contact
// first (who to reach), then creators (primary authors), then
// editors / publishers / contributors.
var agentReadSections = []struct {
	role  hive.Role
	label string
}{
	{hive.RoleContact, "Contact"},
	{hive.RoleCreator, "Creators"},
	{hive.RoleEditor, "Editors"},
	{hive.RolePublisher, "Publishers"},
	{hive.RoleContributor, "Contributors"},
}

// writeAgentSection appends one label + N agent lines to b. First
// agent shares the label line; subsequent agents are indented to
// the value column so the block reads as a stacked list. Empty
// section renders "(none)" in dim style.
func (m metadataModel) writeAgentSection(b *strings.Builder, role hive.Role, label string) {
	list := m.perRoleAgents[role]
	labelCell := labelStyle.Render(fmt.Sprintf("%-18s", label+":"))
	indent := strings.Repeat(" ", 18)
	if len(list) == 0 {
		b.WriteString(labelCell)
		b.WriteByte(' ')
		b.WriteString(dimStyle.Render("(none)"))
		b.WriteByte('\n')
		return
	}
	for i, ag := range list {
		if i == 0 {
			b.WriteString(labelCell)
		} else {
			b.WriteString(indent)
		}
		b.WriteByte(' ')
		b.WriteString(renderAgentLine(ag, ""))
		b.WriteByte('\n')
	}
}

func (m metadataModel) renderForm() string {
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render("── Metadata ──"))
	b.WriteByte('\n')
	for i := range mfCount {
		b.WriteString(m.renderFormRow(i))
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
		b.WriteString(dimStyle.Render("[tab] next   [space] cycle private   [ctrl+s] save   [esc] cancel"))
	}
	return b.String()
}

func (m metadataModel) renderFormRow(i int) string {
	label := labelStyle.Render(fmt.Sprintf("%-18s", mfLabels[i]+":"))
	var rendered string
	if i == mfPrivate {
		opts := []string{"(unset)", "private", "public"}
		for j, opt := range opts {
			if j == m.privateState {
				opts[j] = focusedStyle.Render("[" + opt + "]")
			} else {
				opts[j] = dimStyle.Render(" " + opt + " ")
			}
		}
		rendered = strings.Join(opts, " ")
		if m.focusedField == mfPrivate {
			rendered += "  " + dimStyle.Render("(space to cycle)")
		}
	} else {
		rendered = m.inputs[i].View()
	}
	return label + " " + rendered
}

// Title returns the metadata title, or "" if not loaded. Used by the
// shell to refresh the status-bar cache after a save.
func (m metadataModel) Title() string {
	if m.metadata == nil {
		return ""
	}
	return m.metadata.Title
}

// renderStars produces "★★★★☆  4 / 5" with lipgloss colors — filled
// stars use the accent color, empty stars stay dim. Values outside the
// [0, max] range clamp. Used for the Confidence 1-5 rating in metadata
// view mode; Completeness stays as a plain percentage.
func renderStars(value, max int) string {
	if value < 0 {
		value = 0
	}
	if value > max {
		value = max
	}
	filled := starFilledStyle.Render(strings.Repeat("★", value))
	empty := starEmptyStyle.Render(strings.Repeat("☆", max-value))
	return filled + empty + "  " + dimStyle.Render(fmt.Sprintf("%d / %d", value, max))
}
