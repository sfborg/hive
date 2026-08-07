package view

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sfborg/hive/core/ui"
)

// helpModel renders hive's shared keymap grouped by scope. Opened by
// the `:h` / `:help` ex-mode command; closed by Esc or `q`. Reads
// core/ui.ForFrontend("tui") once at construction; the shared list is
// static for the process lifetime so no refresh path is needed.
type helpModel struct {
	active   bool
	sections []helpSection
}

// helpSection is one scope-group in the rendered modal. Grouping the
// display slice at construction time lets View be a straight loop —
// avoids re-grouping on every render.
type helpSection struct {
	scope    ui.Scope
	label    string
	entries  []ui.Shortcut
	maxWidth int // widest Display column, for column alignment
}

// scopeOrder controls the rendered order of the help groups. Global
// keys come first — a curator opening the help is usually orienting
// themselves, and the global bindings tell them how to move between
// panes and screens. Per-pane bindings follow.
var scopeOrder = []struct {
	scope ui.Scope
	label string
}{
	{ui.ScopeGlobal, "Global"},
	{ui.ScopeTree, "Taxon tree"},
	{ui.ScopeDetail, "Detail pane"},
	{ui.ScopeForm, "Edit forms"},
}

func newHelpModel() helpModel {
	all := ui.ForFrontend(ui.FrontendTUI)
	sections := make([]helpSection, 0, len(scopeOrder))
	for _, so := range scopeOrder {
		var entries []ui.Shortcut
		w := 0
		for _, s := range all {
			if s.Scope != so.scope {
				continue
			}
			entries = append(entries, s)
			if lipgloss.Width(s.Display) > w {
				w = lipgloss.Width(s.Display)
			}
		}
		if len(entries) == 0 {
			continue
		}
		sections = append(sections, helpSection{
			scope:    so.scope,
			label:    so.label,
			entries:  entries,
			maxWidth: w,
		})
	}
	return helpModel{sections: sections}
}

// Open shows the overlay. Called by the shell when it receives
// openHelpMsg from the command-mode dispatch.
func (m *helpModel) Open() { m.active = true }

// Close hides the overlay. Called by Esc / `q` inside Update.
func (m *helpModel) Close() { m.active = false }

// Active reports whether the overlay is showing. The shell checks
// this before routing any other key event.
func (m *helpModel) Active() bool { return m.active }

// Update owns every key event while the overlay is active. Any key
// closes it — the help view is read-only.
func (m helpModel) Update(msg tea.Msg, keys keyMap) (helpModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Esc or q — both close. Other keys are swallowed so a curator
	// mashing keys to leave the overlay doesn't accidentally fire an
	// action on the underlying view.
	if key.Matches(km, keys.Cancel) || km.String() == "q" {
		m.Close()
	}
	return m, nil
}

// View renders the overlay filling the given viewport. Content is
// centered horizontally; scrollable content would need more work but
// the current keymap fits on any reasonable terminal.
func (m helpModel) View(width, height int) string {
	if !m.active {
		return ""
	}
	title := helpTitleStyle.Render("Keyboard shortcuts")
	hint := helpHintStyle.Render("Esc or q to close  ·  :help to reopen")

	var body strings.Builder
	for i, sec := range m.sections {
		if i > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(helpSectionStyle.Render(sec.label))
		body.WriteString("\n")
		body.WriteString(helpRuleStyle.Render(strings.Repeat("─", lipgloss.Width(sec.label))))
		body.WriteString("\n")
		for _, e := range sec.entries {
			display := helpKeyStyle.Render(padRight(e.Display, sec.maxWidth))
			desc := helpDescStyle.Render(e.Description)
			body.WriteString("  " + display + "   " + desc + "\n")
		}
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		body.String(),
		"",
		hint,
	)
	boxed := helpBoxStyle.Render(content)
	// Center the box in the viewport. Place returns a string of
	// exactly width × height so the shell can render it in place of
	// the normal view without composition arithmetic.
	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		boxed,
		lipgloss.WithWhitespaceChars(" "),
	)
}

var (
	helpBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), true).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 3)
	helpTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6"))
	helpSectionStyle = lipgloss.NewStyle().
				Bold(true)
	helpRuleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
	helpKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))
	helpDescStyle = lipgloss.NewStyle()
	helpHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)
)
