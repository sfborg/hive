package view

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// commandModel drives hive's vim-style ex-mode: a status-bar prompt
// opened with `:` that accepts short commands like `:h` / `:help`.
// The subsystem is deliberately minimal today (help is the only wired
// command) but shaped so future commands — `:w` save, `:q` quit,
// `:reveal <id>`, `:reindex`, `:release` — can slot in as one-line
// additions to runCommand's switch.
//
// See PROJECTS.md for why the TUI uses `:h` over the more web-native
// `?` for help: `?` is vim's reverse-search binding and hive's TUI
// stays vim-adjacent so muscle memory transfers from editors curators
// already know.
type commandModel struct {
	active bool   // prompt is showing
	input  string // buffer of characters typed since `:` was pressed
	err    string // last error to render in the status bar
}

// commandExecuteMsg wraps whichever tea.Msg the executed command
// produced. runCommand returns one; the shell handles the payload by
// type-switch (openHelpMsg, commandErrMsg, …).
type commandExecuteMsg struct{ payload tea.Msg }

// openHelpMsg opens the help overlay. Emitted by `:h` / `:help`.
type openHelpMsg struct{}

// commandErrMsg carries a short error message to render briefly in
// the status bar (e.g. "unknown command: foo"). Cleared on the next
// keystroke; no timer needed for that.
type commandErrMsg struct{ msg string }

// Open puts the prompt into the active state with a fresh empty
// buffer. Called by the shell when `:` fires from view mode.
func (m *commandModel) Open() {
	m.active = true
	m.input = ""
	m.err = ""
}

// Close hides the prompt without executing. Called by Esc, or by
// runCommand once a command has been parsed.
func (m *commandModel) Close() {
	m.active = false
	m.input = ""
	m.err = ""
}

// Active reports whether the prompt is currently open. The shell
// checks this before routing key events elsewhere.
func (m *commandModel) Active() bool { return m.active }

// SetError posts a one-shot error to the prompt's status line. The
// shell clears it on the next unrelated key event.
func (m *commandModel) SetError(msg string) { m.err = msg }

// ClearError removes a lingering error. Called on the first key
// after an error is shown so the bar returns to normal.
func (m *commandModel) ClearError() { m.err = "" }

// Update owns every key event while active. Handles line editing
// (character insert / backspace), Enter to execute, and Esc to
// cancel. Returns a tea.Cmd emitting a commandExecuteMsg for the
// shell to dispatch on.
func (m commandModel) Update(msg tea.Msg, keys keyMap) (commandModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Cancel via the shared "cancel" binding — currently Esc.
	if key.Matches(km, keys.Cancel) {
		m.Close()
		return m, nil
	}
	switch km.String() {
	case "enter":
		payload := runCommand(m.input)
		m.Close()
		return m, func() tea.Msg { return commandExecuteMsg{payload: payload} }
	case "backspace":
		if len(m.input) > 0 {
			// Trim one rune, not one byte, so multibyte inputs (unlikely
			// in commands but possible in future :reveal arguments)
			// don't corrupt.
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	}
	// Regular character input — accept printable ASCII plus space.
	// Non-printable / control keys fall through as no-ops so bindings
	// like Tab or Ctrl+C don't accidentally land in the buffer.
	if len(km.Runes) == 1 {
		r := km.Runes[0]
		if r >= 32 && r != 127 {
			m.input += string(r)
		}
	}
	return m, nil
}

// runCommand parses one line of the command buffer and returns the
// tea.Msg the shell should dispatch on. Unknown commands produce a
// commandErrMsg; recognized ones produce their own payload
// (openHelpMsg, etc.). Kept as a small switch — adding a command is
// one case here plus a shell handler for its message type.
func runCommand(input string) tea.Msg {
	cmd := strings.TrimSpace(input)
	// Bare `:` with no command is a no-op — the curator canceled by
	// pressing Enter on an empty buffer.
	if cmd == "" {
		return nil
	}
	switch cmd {
	case "h", "help":
		return openHelpMsg{}
	}
	return commandErrMsg{msg: fmt.Sprintf("unknown command: %s", cmd)}
}

// View renders the prompt for the status bar. Returns "" when
// inactive so the caller can fall back to the normal status hints.
// Format matches vim: `:command_buffer` with a caret cursor.
func (m commandModel) View(width int) string {
	if !m.active {
		if m.err != "" {
			return statusStyle.Render(padRight(fmt.Sprintf("  %s", m.err), width))
		}
		return ""
	}
	line := fmt.Sprintf(" :%s", m.input)
	// Simple block cursor at the end of the input.
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	return statusStyle.Render(padRight(line+cursor, width))
}
