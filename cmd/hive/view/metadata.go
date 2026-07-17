package view

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sfborg/hive/core"
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
type metadataModel struct {
	a        *core.Archive
	editable bool
	actor    string

	metadata *core.Metadata
	loading  bool
	err      error

	editing      bool
	inputs       [mfCount]textinput.Model
	focusedField int
	privateState int // 0=unset, 1=private, 2=public
	saving       bool
	saveError    string
}

func newMetadataModel(a *core.Archive, editable bool, actor string) metadataModel {
	m := metadataModel{a: a, editable: editable, actor: actor}
	for i := range m.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 4000
		m.inputs[i] = ti
	}
	return m
}

// metadataLoadedMsg carries the fetched metadata back to the model.
type metadataLoadedMsg struct {
	metadata *core.Metadata
	err      error
}

// metadataSavedMsg reports a Save round-trip result. On success the
// caller's cached metadata title is refreshed too.
type metadataSavedMsg struct {
	metadata *core.Metadata
	err      error
}

// Load kicks off an async GetMetadata fetch. Returned cmd is fired by
// the shell when the metadata screen becomes visible.
func (m metadataModel) Load() tea.Cmd {
	a := m.a
	return func() tea.Msg {
		md, err := a.GetMetadata(context.Background())
		return metadataLoadedMsg{metadata: md, err: err}
	}
}

// Editing reports whether the pane is in edit mode.
func (m metadataModel) Editing() bool { return m.editing }

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
		err := a.WithTx(ctx, func(tx *core.Tx) error {
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
// edit mode is the labelled form.
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

	if m.editable {
		b.WriteByte('\n')
		b.WriteString(dimStyle.Render("[e] edit"))
	}
	return b.String()
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
