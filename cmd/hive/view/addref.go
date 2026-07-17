package view

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sfborg/hive/core"
	"github.com/sfborg/sflib/pkg/coldp"
)

// The TUI counterpart of the PWA's <sfga-add-reference-modal>. Same four
// tabs, same in-process backing (core.OpenAlex / core.BHLnames /
// core.ParseBibTeX / Tx.CreateReference) — the TUI just skips the HTTP
// layer entirely because it already holds the *core.Archive.
//
// Tabs, in order:
//
//	0 Project — SearchReferences on the current archive; pick without write
//	1 BHLnames — auto-fires on entry using the current name's context;
//	             preview + "add & pick" creates the reference then closes
//	2 DOI      — text input; resolve via OpenAlex; preview; add & pick
//	3 BibTeX   — textarea; parse locally; preview; add & pick
//
// Key routing (while modal is active — the shell delegates everything):
//
//	esc                         close (no change)
//	ctrl+n / ctrl+p             cycle tabs (safe against text-input capture)
//	ctrl+1..4                   direct tab selection
//	up / down                   navigate hits list (project + bhlnames)
//	enter                       submit (search / resolve / parse) OR pick
//	                            highlighted hit / commit preview
//	ctrl+d                      discard preview (tabs 2 / 3)
//
// Focus model per tab:
//
//	Tab 0 always focuses the query input; up/down moves the hover in the
//	  hit list without leaving the input.
//	Tab 1 auto-runs on entry — there's no input to focus; up/down + enter
//	  drive the hit list. A "refresh" is possible via `ctrl+r`.
//	Tab 2 focuses the DOI input until a preview lands; then focus moves
//	  to the preview (Enter = add & pick, ctrl+d = discard).
//	Tab 3 same as tab 2 but with a textarea for the BibTeX body.

// addRefTab enumerates the four modal tabs.
type addRefTab int

const (
	addRefTabProject  addRefTab = 0
	addRefTabBHLnames addRefTab = 1
	addRefTabDOI      addRefTab = 2
	addRefTabBibTeX   addRefTab = 3
)

// addRefResult is dispatched by the modal to the parent (detailModel) when
// the curator picks or creates a reference. Empty ID means the modal was
// closed without a pick (esc).
type addRefResult struct {
	id    string
	label string
}

// Async message deliveries. Each carries a query token where relevant so
// the modal can drop stale replies from an earlier submission.
type projectSearchDone struct {
	query string
	hits  []core.ReferenceHit
	err   error
}
type bhlLookupDone struct {
	hits []core.BHLnameHit
	err  error
}
type doiResolveDone struct {
	ref *coldp.Reference
	err error
}
type bibtexParseDone struct {
	ref *coldp.Reference
	err error
}
type refCreatedDone struct {
	id    string
	label string
	err   error
}

// addRefModel owns the modal's state and layout. Instantiated by the
// detailModel on Ctrl+A while editing a name.
type addRefModel struct {
	a     *core.Archive
	actor string
	// Context propagated from the current name for the BHLnames tab.
	canonical string
	authors   string
	year      int

	tab  addRefTab
	busy bool
	err  string

	// Tab 0
	projectInput textinput.Model
	projectHits  []core.ReferenceHit
	projectHover int
	projectQuery string // last-fired query (race guard for projectSearchDone)

	// Tab 1
	bhlHits    []core.BHLnameHit
	bhlLoaded  bool
	bhlHover   int

	// Tab 2
	doiInput textinput.Model

	// Tab 3
	bibtexInput textarea.Model

	// Shared preview for tabs 1/2/3. When set, actions target the preview.
	preview *coldp.Reference

	// Result is set on close; the parent reads it via ConsumeResult.
	result   addRefResult
	finished bool
}

// newAddRefModel builds a fresh modal seeded with the given context. The
// context comes from the name currently being edited so the BHLnames tab
// can auto-run without asking the curator to re-type the scientific name.
func newAddRefModel(a *core.Archive, actor, canonical, authors string, year int) addRefModel {
	q := textinput.New()
	q.Placeholder = "author / title / citation…"
	q.Prompt = ""
	q.CharLimit = 200
	q.Focus()

	doi := textinput.New()
	doi.Placeholder = "10.1038/171737a0"
	doi.Prompt = ""
	doi.CharLimit = 200

	ta := textarea.New()
	ta.Placeholder = "@article{smith2020, title={…}, author={…}, journal={…}, year={2020}}"
	ta.CharLimit = 8000
	ta.ShowLineNumbers = false
	ta.SetHeight(6)

	return addRefModel{
		a:            a,
		actor:        actor,
		canonical:    canonical,
		authors:      authors,
		year:         year,
		tab:          addRefTabProject,
		projectInput: q,
		doiInput:     doi,
		bibtexInput:  ta,
	}
}

// Init returns the startup cmd — none required until the curator switches
// tabs or types. Kept for symmetry with other TUI sub-models.
func (m *addRefModel) Init() tea.Cmd { return nil }

// Finished reports whether the modal is done (curator picked, or closed).
// The parent reads ConsumeResult after this returns true.
func (m *addRefModel) Finished() bool { return m.finished }

// ConsumeResult returns the pick (if any) and marks the modal as read.
// Called by the parent after Finished() returns true.
func (m *addRefModel) ConsumeResult() addRefResult {
	r := m.result
	m.result = addRefResult{}
	return r
}

// selectTab switches tabs, clears transient error/preview state, and
// wires focus to whatever input is appropriate for the new tab.
func (m *addRefModel) selectTab(t addRefTab) tea.Cmd {
	m.tab = t
	m.err = ""
	m.preview = nil
	// Blur everything, then focus the right input.
	m.projectInput.Blur()
	m.doiInput.Blur()
	m.bibtexInput.Blur()
	switch t {
	case addRefTabProject:
		return m.projectInput.Focus()
	case addRefTabBHLnames:
		if !m.bhlLoaded && m.canonical != "" {
			return m.runBHLnamesCmd()
		}
		return nil
	case addRefTabDOI:
		return m.doiInput.Focus()
	case addRefTabBibTeX:
		return m.bibtexInput.Focus()
	}
	return nil
}

// Update handles all key events + async completion messages while the
// modal is active. The parent (detailModel) forwards everything here
// unfiltered; the modal owns focus.
func (m addRefModel) Update(msg tea.Msg) (addRefModel, tea.Cmd) {
	switch msg := msg.(type) {
	case projectSearchDone:
		// Race guard: drop stale replies from queries the curator has
		// since typed past.
		if msg.query != m.projectQuery {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.projectHits = msg.hits
		m.projectHover = 0
		return m, nil

	case bhlLookupDone:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.bhlHits = msg.hits
		m.bhlHover = 0
		return m, nil

	case doiResolveDone:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.preview = msg.ref
		return m, nil

	case bibtexParseDone:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.preview = msg.ref
		return m, nil

	case refCreatedDone:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.result = addRefResult{id: msg.id, label: msg.label}
		m.finished = true
		return m, nil

	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// updateKey routes a key to tab-switching, then tab-scoped handlers, then
// (as a fallthrough) the tab's active text input. Kept as one large
// switch so the control flow reads top-to-bottom rather than spread
// across small helpers.
func (m addRefModel) updateKey(msg tea.KeyMsg) (addRefModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Close without a pick. Parent detects m.finished + empty id.
		m.finished = true
		return m, nil
	case "ctrl+n":
		return m, m.selectTab((m.tab + 1) % 4)
	case "ctrl+p":
		return m, m.selectTab((m.tab + 3) % 4)
	case "ctrl+1":
		return m, m.selectTab(addRefTabProject)
	case "ctrl+2":
		return m, m.selectTab(addRefTabBHLnames)
	case "ctrl+3":
		return m, m.selectTab(addRefTabDOI)
	case "ctrl+4":
		return m, m.selectTab(addRefTabBibTeX)
	}
	switch m.tab {
	case addRefTabProject:
		return m.updateProject(msg)
	case addRefTabBHLnames:
		return m.updateBHLnames(msg)
	case addRefTabDOI:
		return m.updateDOI(msg)
	case addRefTabBibTeX:
		return m.updateBibTeX(msg)
	}
	return m, nil
}

// ---- Tab 0: Project search ----

func (m addRefModel) updateProject(msg tea.KeyMsg) (addRefModel, tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.projectHover > 0 {
			m.projectHover--
		}
		return m, nil
	case "down":
		if m.projectHover < len(m.projectHits)-1 {
			m.projectHover++
		}
		return m, nil
	case "enter":
		if len(m.projectHits) > m.projectHover && m.projectHover >= 0 {
			h := m.projectHits[m.projectHover]
			m.result = addRefResult{id: h.ID, label: referenceHitLabel(h)}
			m.finished = true
			return m, nil
		}
		// No hits — fire a fresh search from the current input.
		return m.fireProjectSearchFromInput()
	}
	// Otherwise route to the input; input change triggers a search cmd.
	var cmd tea.Cmd
	m.projectInput, cmd = m.projectInput.Update(msg)
	// Debounce would be nicer, but SearchReferences on local SQLite
	// is fast enough to fire on every keystroke without a lag.
	if q := strings.TrimSpace(m.projectInput.Value()); len(q) >= 2 && q != m.projectQuery {
		searchCmd := m.fireProjectSearch(q)
		return m, tea.Batch(cmd, searchCmd)
	}
	if strings.TrimSpace(m.projectInput.Value()) == "" {
		m.projectHits = nil
	}
	return m, cmd
}

func (m *addRefModel) fireProjectSearchFromInput() (addRefModel, tea.Cmd) {
	q := strings.TrimSpace(m.projectInput.Value())
	if q == "" {
		return *m, nil
	}
	return *m, m.fireProjectSearch(q)
}

func (m *addRefModel) fireProjectSearch(q string) tea.Cmd {
	m.projectQuery = q
	m.busy = true
	m.err = ""
	a := m.a
	return func() tea.Msg {
		hits, err := a.SearchReferences(context.Background(), q, 15)
		return projectSearchDone{query: q, hits: hits, err: err}
	}
}

// ---- Tab 1: BHLnames ----

func (m addRefModel) updateBHLnames(msg tea.KeyMsg) (addRefModel, tea.Cmd) {
	if m.preview != nil {
		return m.updatePreviewCommon(msg)
	}
	switch msg.String() {
	case "up":
		if m.bhlHover > 0 {
			m.bhlHover--
		}
		return m, nil
	case "down":
		if m.bhlHover < len(m.bhlHits)-1 {
			m.bhlHover++
		}
		return m, nil
	case "enter":
		if len(m.bhlHits) > m.bhlHover && m.bhlHover >= 0 {
			// Preview the selected hit — a second Enter (in preview mode)
			// adds & picks. Two-step so the curator can eyeball the
			// citation before committing a write.
			m.preview = &m.bhlHits[m.bhlHover].Reference
			return m, nil
		}
	case "ctrl+r":
		if m.canonical != "" {
			m.bhlLoaded = false
			return m, m.runBHLnamesCmd()
		}
	}
	return m, nil
}

func (m *addRefModel) runBHLnamesCmd() tea.Cmd {
	m.busy = true
	m.err = ""
	m.bhlLoaded = true
	canonical := m.canonical
	authors := m.authors
	year := m.year
	return func() tea.Msg {
		hits, err := core.BHLnames().LookupName(context.Background(),
			canonical, authors, year,
			core.BHLnameLookupOpts{
				RefsLimit:  5,
				NomenEvent: true,
			})
		return bhlLookupDone{hits: hits, err: err}
	}
}

// ---- Tab 2: DOI ----

func (m addRefModel) updateDOI(msg tea.KeyMsg) (addRefModel, tea.Cmd) {
	if m.preview != nil {
		return m.updatePreviewCommon(msg)
	}
	switch msg.String() {
	case "enter":
		doi := strings.TrimSpace(m.doiInput.Value())
		if doi == "" {
			return m, nil
		}
		m.busy = true
		m.err = ""
		return m, func() tea.Msg {
			ref, err := core.OpenAlex().ResolveDOI(context.Background(), doi)
			return doiResolveDone{ref: ref, err: err}
		}
	}
	var cmd tea.Cmd
	m.doiInput, cmd = m.doiInput.Update(msg)
	return m, cmd
}

// ---- Tab 3: BibTeX ----

func (m addRefModel) updateBibTeX(msg tea.KeyMsg) (addRefModel, tea.Cmd) {
	if m.preview != nil {
		return m.updatePreviewCommon(msg)
	}
	switch msg.String() {
	case "ctrl+enter", "alt+enter":
		// Explicit "submit" chord — plain Enter in a textarea is a
		// newline, so the parse trigger needs a modifier. Both work;
		// terminals differ on which they deliver.
		text := strings.TrimSpace(m.bibtexInput.Value())
		if text == "" {
			return m, nil
		}
		m.busy = true
		m.err = ""
		return m, func() tea.Msg {
			ref, err := core.ParseBibTeX(text)
			return bibtexParseDone{ref: ref, err: err}
		}
	}
	var cmd tea.Cmd
	m.bibtexInput, cmd = m.bibtexInput.Update(msg)
	return m, cmd
}

// updatePreviewCommon handles keys once a preview is showing on tabs
// 1 / 2 / 3. Enter commits (creates + picks), ctrl+d discards.
func (m addRefModel) updatePreviewCommon(msg tea.KeyMsg) (addRefModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.preview == nil {
			return m, nil
		}
		return m, m.createAndPickCmd(*m.preview)
	case "ctrl+d":
		m.preview = nil
		return m, nil
	}
	return m, nil
}

// createAndPickCmd writes the reference through core and dispatches the
// pick on success. Called from all three "resolve/parse" tabs — a write
// is a write regardless of where the preview came from.
func (m *addRefModel) createAndPickCmd(ref coldp.Reference) tea.Cmd {
	m.busy = true
	m.err = ""
	// Clear ID so core mints a fresh UUID — the previews from OpenAlex /
	// BHLnames / BibTeX may or may not carry one; hive's convention is
	// to generate on write.
	ref.ID = ""
	a := m.a
	actor := m.actor
	return func() tea.Msg {
		ctx := contextWithActor(actor)
		var newID string
		err := a.WithTx(ctx, func(tx *core.Tx) error {
			id, err := tx.CreateReference(ref)
			if err != nil {
				return err
			}
			newID = id
			return nil
		})
		if err != nil {
			return refCreatedDone{err: err}
		}
		fresh, err := a.GetReference(ctx, newID)
		if err != nil {
			// The write succeeded but the follow-up read failed —
			// hand back the id with a label built from the preview.
			return refCreatedDone{id: newID, label: core.ReferenceLabel(&ref)}
		}
		return refCreatedDone{id: newID, label: core.ReferenceLabel(fresh)}
	}
}

// -------- Rendering --------

func (m addRefModel) View() string {
	var b strings.Builder
	b.WriteString(addRefTitleStyle.Render("── Add reference ──"))
	b.WriteByte('\n')
	b.WriteString(m.renderTabs())
	b.WriteString("\n\n")
	switch m.tab {
	case addRefTabProject:
		b.WriteString(m.renderProject())
	case addRefTabBHLnames:
		b.WriteString(m.renderBHLnames())
	case addRefTabDOI:
		b.WriteString(m.renderDOI())
	case addRefTabBibTeX:
		b.WriteString(m.renderBibTeX())
	}
	if m.err != "" {
		b.WriteByte('\n')
		b.WriteString(errStyle.Render("error: " + m.err))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(
		"[ctrl+n/p] tabs   [ctrl+1..4] jump   [esc] close",
	))
	return b.String()
}

func (m addRefModel) renderTabs() string {
	labels := []string{"Project", "BHLnames", "DOI", "BibTeX"}
	var parts []string
	for i, l := range labels {
		lbl := fmt.Sprintf(" %d %s ", i+1, l)
		if addRefTab(i) == m.tab {
			parts = append(parts, focusedStyle.Render(lbl))
		} else {
			parts = append(parts, dimStyle.Render(lbl))
		}
	}
	return strings.Join(parts, "│")
}

func (m addRefModel) renderProject() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Search: "))
	b.WriteString(m.projectInput.View())
	b.WriteByte('\n')
	if m.busy {
		b.WriteString(dimStyle.Render("searching…\n"))
	}
	if len(m.projectHits) == 0 && strings.TrimSpace(m.projectInput.Value()) != "" && !m.busy {
		b.WriteString(dimStyle.Render("no matches\n"))
	}
	for i, h := range m.projectHits {
		row := formatRefHit(h)
		if i == m.projectHover {
			b.WriteString(focusedStyle.Render("▸ " + row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render("[↑/↓] pick   [enter] use / search   [ctrl+n] BHLnames tab"))
	return b.String()
}

func (m addRefModel) renderBHLnames() string {
	var b strings.Builder
	if m.canonical == "" {
		return dimStyle.Render("BHLnames needs a scientific name — this taxon doesn't have one yet.")
	}
	b.WriteString(dimStyle.Render(fmt.Sprintf(
		"Lookup: %s", m.contextLabel())))
	b.WriteByte('\n')
	if m.busy {
		b.WriteString(dimStyle.Render("querying BHLnames…\n"))
	} else if m.bhlLoaded && len(m.bhlHits) == 0 && m.err == "" {
		b.WriteString(dimStyle.Render("no BHLnames matches\n"))
	}
	for i, h := range m.bhlHits {
		row := formatBHLnameHit(h)
		if i == m.bhlHover {
			b.WriteString(focusedStyle.Render("▸ " + row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteByte('\n')
	}
	if m.preview != nil {
		b.WriteByte('\n')
		b.WriteString(m.renderPreview())
	} else if len(m.bhlHits) > 0 {
		b.WriteByte('\n')
		b.WriteString(dimStyle.Render("[↑/↓] pick   [enter] preview   [ctrl+r] re-run"))
	}
	return b.String()
}

func (m addRefModel) renderDOI() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("DOI: "))
	b.WriteString(m.doiInput.View())
	b.WriteByte('\n')
	if m.busy {
		b.WriteString(dimStyle.Render("resolving via OpenAlex…\n"))
	}
	if m.preview != nil {
		b.WriteByte('\n')
		b.WriteString(m.renderPreview())
	} else {
		b.WriteByte('\n')
		b.WriteString(dimStyle.Render("[enter] resolve"))
	}
	return b.String()
}

func (m addRefModel) renderBibTeX() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("BibTeX entry:"))
	b.WriteByte('\n')
	b.WriteString(m.bibtexInput.View())
	b.WriteByte('\n')
	if m.busy {
		b.WriteString(dimStyle.Render("parsing…\n"))
	}
	if m.preview != nil {
		b.WriteByte('\n')
		b.WriteString(m.renderPreview())
	} else {
		b.WriteByte('\n')
		b.WriteString(dimStyle.Render("[ctrl+enter] parse"))
	}
	return b.String()
}

// renderPreview draws the shared preview panel + action hints. Shows the
// same field set as the PWA preview so the two modals feel like the same
// tool.
func (m addRefModel) renderPreview() string {
	r := m.preview
	rows := [][2]string{
		{"Author", r.Author},
		{"Year", yearOnly(r.Issued)},
		{"Title", r.Title},
		{"Container", r.ContainerTitle},
		{"Volume/Issue", volIssue(r)},
		{"Page", r.Page},
		{"DOI", r.DOI},
		{"Type", r.Type.ID()},
	}
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render("── preview ──"))
	b.WriteByte('\n')
	for _, kv := range rows {
		if kv[1] == "" {
			continue
		}
		b.WriteString(labelStyle.Render(fmt.Sprintf("%-14s", kv[0]+":")))
		b.WriteByte(' ')
		b.WriteString(kv[1])
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render("[enter] add & pick   [ctrl+d] discard"))
	return b.String()
}

// -------- Helpers --------

// contextLabel builds the "Panthera leo Linnaeus (1758)" one-liner shown
// at the top of the BHLnames pane.
func (m addRefModel) contextLabel() string {
	parts := []string{m.canonical}
	if m.authors != "" {
		parts = append(parts, m.authors)
	}
	if m.year != 0 {
		parts = append(parts, fmt.Sprintf("(%d)", m.year))
	}
	return strings.Join(parts, " ")
}

func formatRefHit(h core.ReferenceHit) string {
	label := referenceHitLabel(h)
	if len(label) > 96 {
		label = label[:93] + "…"
	}
	return label
}

func formatBHLnameHit(h core.BHLnameHit) string {
	title := h.Reference.Title
	if title == "" {
		title = h.Reference.Citation
	}
	if title == "" {
		title = "(no title)"
	}
	if len(title) > 70 {
		title = title[:67] + "…"
	}
	year := yearOnly(h.Reference.Issued)
	yearPart := ""
	if year != "" {
		yearPart = " (" + year + ")"
	}
	author := h.Reference.Author
	if author != "" {
		author += " "
	}
	qual := ""
	if h.Quality > 0 {
		qual = fmt.Sprintf("  [q%d/5]", h.Quality)
	}
	return fmt.Sprintf("%s%s%s%s", author, title, yearPart, qual)
}

func yearOnly(issued string) string {
	if len(issued) >= 4 {
		return issued[:4]
	}
	return ""
}

func volIssue(r *coldp.Reference) string {
	if r.Volume == "" {
		return r.Issue
	}
	if r.Issue == "" {
		return r.Volume
	}
	return r.Volume + "(" + r.Issue + ")"
}

var addRefTitleStyle = lipgloss.NewStyle().Bold(true)
