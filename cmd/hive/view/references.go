package view

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sfborg/hive/core"
	"github.com/sfborg/sflib/pkg/coldp"
)

// referencesModel is the References screen — a two-pane layout matching
// the Taxa screen: scrollable list of references on the left, full
// detail on the right. Read-only in this slice; edit lands later.
//
// The list loads on Init (Load()) and re-loads if the archive changes.
// Cursor movement on the list updates the highlighted reference and
// triggers a detail fetch.
type referencesModel struct {
	a *core.Archive

	hits   []core.ReferenceHit
	cursor int
	total  int
	loaded bool
	err    error

	// Currently highlighted reference's full detail. Nil while
	// loading; the pane shows a "loading…" placeholder.
	current   *coldp.Reference
	currentID string
	detailErr error
}

func newReferencesModel(a *core.Archive) referencesModel {
	return referencesModel{a: a}
}

// referencesLoadedMsg carries a list-fetch result.
type referencesLoadedMsg struct {
	hits  []core.ReferenceHit
	total int
	err   error
}

// referenceDetailMsg carries a single-reference fetch result.
type referenceDetailMsg struct {
	id  string
	ref *coldp.Reference
	err error
}

// Load kicks off the initial list fetch. Called by the shell's Init
// alongside the tree + metadata loads so alt+r is instant.
func (m referencesModel) Load() tea.Cmd {
	a := m.a
	return func() tea.Msg {
		hits, total, err := a.ListReferencesPage(context.Background(), 200, 0)
		return referencesLoadedMsg{hits: hits, total: total, err: err}
	}
}

// loadDetail fires an async fetch for a single reference's full data.
func (m referencesModel) loadDetail(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	a := m.a
	return func() tea.Msg {
		ref, err := a.GetReference(context.Background(), id)
		return referenceDetailMsg{id: id, ref: ref, err: err}
	}
}

// Update handles list-load, detail-load, and cursor-movement events.
// The shell routes tea.KeyMsg events here only when the References
// screen is active.
func (m referencesModel) Update(msg tea.Msg, keys keyMap) (referencesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case referencesLoadedMsg:
		m.err = msg.err
		m.hits = msg.hits
		m.total = msg.total
		m.loaded = true
		if len(m.hits) > 0 && m.currentID == "" {
			m.currentID = m.hits[0].ID
			return m, m.loadDetail(m.currentID)
		}
		return m, nil

	case referenceDetailMsg:
		if msg.id != m.currentID {
			return m, nil // stale reply — cursor moved before this arrived
		}
		m.detailErr = msg.err
		m.current = msg.ref
		return m, nil

	case tea.KeyMsg:
		if len(m.hits) == 0 {
			return m, nil
		}
		switch {
		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, keys.Down):
			if m.cursor < len(m.hits)-1 {
				m.cursor++
			}
		default:
			return m, nil
		}
		newID := m.hits[m.cursor].ID
		if newID != m.currentID {
			m.currentID = newID
			m.current = nil
			m.detailErr = nil
			return m, m.loadDetail(newID)
		}
		return m, nil
	}
	return m, nil
}

// View renders the two-pane layout inside a caller-provided width/
// height. The shell wraps this in the outer bordered pane; here we
// join the list and detail horizontally.
func (m referencesModel) View(width, height int) string {
	if !m.loaded {
		return dimStyle.Render("loading…")
	}
	if m.err != nil {
		return errStyle.Render("error: " + m.err.Error())
	}
	if len(m.hits) == 0 {
		return dimStyle.Render("(no references)")
	}
	listW := width * 2 / 5
	if listW < 24 {
		listW = 24
	}
	if listW > width-30 {
		listW = width - 30
	}
	detailW := width - listW - 1

	list := m.renderList(listW, height)
	// Vertical separator: exactly `height` lines with no trailing
	// newline. A trailing "\n" would push lipgloss.Height to
	// height+1, and JoinHorizontal would then pad the whole row to
	// that inflated height — overflowing the outer bordered pane and
	// scrolling the menu bar off the top of the terminal.
	sep := ""
	if height > 0 {
		sep = dimStyle.Render(strings.Repeat("│\n", height-1) + "│")
	}
	detail := m.renderDetail(detailW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, sep, detail)
}

func (m referencesModel) renderList(width, height int) string {
	var b strings.Builder
	viewH := height
	if viewH <= 0 {
		viewH = len(m.hits)
	}
	start := 0
	if m.cursor >= viewH {
		start = m.cursor - viewH + 1
	}
	end := min(start+viewH, len(m.hits))
	for i := start; i < end; i++ {
		h := m.hits[i]
		author := h.Author
		if len(author) > 24 {
			author = author[:23] + "…"
		}
		title := h.Title
		if title == "" {
			title = h.Citation
		}
		maxTitle := width - len(author) - 10
		if maxTitle < 10 {
			maxTitle = 10
		}
		if len([]rune(title)) > maxTitle {
			title = string([]rune(title)[:maxTitle-1]) + "…"
		}
		row := fmt.Sprintf(" %-24s %s %s", author, h.Year, title)
		if i == m.cursor {
			b.WriteString(lipgloss.NewStyle().Reverse(true).Render(row))
		} else {
			b.WriteString(row)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (m referencesModel) renderDetail(width, height int) string {
	if m.detailErr != nil {
		return errStyle.Render("error: " + m.detailErr.Error())
	}
	if m.current == nil {
		return dimStyle.Render("loading…")
	}
	r := m.current
	var b strings.Builder
	// Header: title (bold) or citation as fallback.
	head := r.Title
	if head == "" {
		head = r.Citation
	}
	if head == "" {
		head = r.ID
	}
	b.WriteString(headerStyle.Render(head))
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(strings.Repeat("─", min(len([]rune(head)), width))))
	b.WriteString("\n\n")

	push := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(labelStyle.Render(fmt.Sprintf("%-14s", k+":")))
		b.WriteByte(' ')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	push("Author", r.Author)
	push("Issued", r.Issued)
	push("Type", r.Type.ID())
	push("Container", r.ContainerTitle)
	push("Volume", r.Volume)
	push("Issue", r.Issue)
	push("Page", r.Page)
	push("Publisher", r.Publisher)
	push("Place", r.PublisherPlace)
	push("DOI", r.DOI)
	push("ISBN", r.ISBN)
	push("ISSN", r.ISSN)
	push("Link", r.Link)
	push("Editor", r.Editor)
	push("Remarks", r.Remarks)
	if r.Citation != "" && r.Citation != r.Title {
		b.WriteByte('\n')
		b.WriteString(labelStyle.Render("Citation:"))
		b.WriteByte('\n')
		b.WriteString(r.Citation)
		b.WriteByte('\n')
	}
	return b.String()
}
