// Package view implements the Bubble Tea TUI shared by `hive view` and
// `hive edit`. The two subcommands differ only in whether the archive is
// opened read-only and whether the detail pane accepts edits — every other
// piece (tree navigation, detail rendering, keymap, layout, status bar) is
// identical. Keeping one code path here means both modes evolve together.
//
// Package name is `view` for historical reasons — kept to avoid a churny
// rename now that main.go and callers already reference it. A future
// rename to `tui` would be mechanical.
package view

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sfborg/hive/core"
)

// Options configures Run.
type Options struct {
	// Editable opens the archive read-write and enables the detail pane's
	// edit form. False → read-only, no edit affordances.
	Editable bool

	// Actor is the identity stamped into col__modified_by on every write.
	// Only used when Editable is true. Empty falls back to "local".
	Actor string
}

// Run opens the archive (read-only unless opts.Editable) and starts the
// Bubble Tea program. Returns only after the program exits (or fails to
// open the archive).
//
// Belt-and-braces cleanup: Bubble Tea handles its own terminal restoration
// on normal exit and on the panics it catches inside the event loop.
// A panic BEFORE tea.Program.Run() (or SIGKILL) can bypass that. The
// deferred restoreTerminal below fires unconditionally, so even a rogue
// exit path leaves the user with a usable terminal.
func Run(archivePath string, opts Options) error {
	// Ordered defers: recover runs first (LIFO), sees any panic, records
	// it, restores the terminal, then re-raises so the operator still
	// gets the stack trace. restoreTerminal is idempotent — Bubble Tea's
	// own cleanup running before us is harmless.
	defer restoreTerminal()
	defer func() {
		if r := recover(); r != nil {
			restoreTerminal()
			panic(r) // re-raise so the runtime prints the stack
		}
	}()

	openOpts := []core.OpenOption{}
	if !opts.Editable {
		openOpts = append(openOpts, core.ReadOnly())
	}
	a, err := core.Open(archivePath, openOpts...)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer a.Close()

	actor := opts.Actor
	if actor == "" && opts.Editable {
		actor = "local"
		fmt.Fprintln(os.Stderr,
			"warning: no --orcid or HIVE_ORCID set; writes will be attributed to \"local\"")
	}

	// Actor lives on the model so WithTx calls from edit mode pick it up
	// via the same core.WithActor(ctx, ...) plumbing the HTTP server uses.
	m := newModel(a, archivePath, opts.Editable, actor)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// restoreTerminal puts the controlling TTY back into a sane cooked state.
// Called unconditionally on Run exit so a crashed or force-killed TUI
// doesn't leave the operator with a terminal in raw + alt-screen mode
// (symptom: log output stair-steps, Ctrl+C echoes as ^C instead of
// generating SIGINT). Safe to call when there's no TTY (redirected I/O)
// or when the terminal was already sane — the operations are no-ops in
// those cases.
func restoreTerminal() {
	// `stty sane` re-enables ONLCR, ICANON, ISIG, ECHO — the usual cooked
	// terminal semantics. Shell out because Go's stdlib doesn't ship a
	// terminal-mode API and `stty` is universally available on Unix.
	if cmd := exec.Command("stty", "sane"); cmd != nil {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run() // best-effort — if stty is missing or stdin isn't a TTY, skip
	}
	// Extra ANSI belt-and-braces: exit alt-screen (\e[?1049l), show cursor
	// (\e[?25h), carriage return. Some terminals hold onto the alt-screen
	// buffer even after `stty sane`.
	fmt.Fprint(os.Stderr, "\x1b[?1049l\x1b[?25h\r")
}

// contextWithActor is a small helper the detail pane uses when opening a
// Tx from within a Bubble Tea command. Kept package-visible so tests can
// override it if needed.
func contextWithActor(actor string) context.Context {
	return core.WithActor(context.Background(), actor)
}

type focused int

const (
	focusTree focused = iota
	focusDetail
	focusSearch
)

// view identifies the top-level screen the menu bar switches between.
// Matches CLAUDE.md § keybinding conventions — the Alt-letter chords
// map onto these. viewTaxa is the default and holds the tree+detail
// split that hive shipped with; viewMetadata surfaces the dataset
// metadata form. Additional views (Names, References, Vernaculars, …)
// slot in as the corresponding aggregates come online.
type screen int

const (
	viewTaxa screen = iota
	viewMetadata
	viewReferences
)

// model is the top-level Bubble Tea model. It owns the two panes, the
// keymap, focus state, and the current terminal dimensions.
type model struct {
	a           *core.Archive
	archivePath string
	editable    bool
	actor       string
	tree        treeModel
	detail      detailModel
	metadata    metadataModel
	references  referencesModel
	search      combobox
	keys        keyMap
	focus       focused
	width       int
	height      int
	err         error

	// Delete-confirmation state. When confirming is true the status bar
	// shows a y/N prompt and view mode intercepts the next key.
	confirming        bool
	confirmTargetID   string
	confirmTargetName string
	confirmError      string

	// Cached metadata title for the status bar. Loaded once at model
	// construction; not refreshed live since edits go through the
	// (future) metadata screen which will invalidate this itself.
	metaTitle string

	// Which top-level screen is showing. Menu bar reflects this; keys
	// route into the corresponding sub-model.
	screen screen

	// Command mode (`:` opens an ex-style prompt) and help overlay
	// (opened by :h / :help). Both are shell-level modal states —
	// when either is active, key events are routed to it first and
	// other bindings are suppressed until it closes.
	command commandModel
	help    helpModel
}

// metadataTitle returns the cached dataset-metadata title, or "" if
// unavailable (legacy archive without a seeded metadata row).
func (m *model) metadataTitle() string { return m.metaTitle }

func newModel(a *core.Archive, archivePath string, editable bool, actor string) *model {
	// Load the metadata title synchronously — one row lookup, sub-ms
	// on local SQLite, so it doesn't need to go through tea.Cmd. Fail
	// silently on legacy archives without a seeded row; the status bar
	// falls back to the archive path.
	var title string
	if md, err := a.GetMetadata(context.Background()); err == nil {
		title = md.Title
	}
	return &model{
		a:           a,
		archivePath: archivePath,
		editable:    editable,
		actor:       actor,
		tree:        newTreeModel(a),
		detail:      newDetailModel(a, editable, actor),
		metadata:    newMetadataModel(a, editable, actor),
		references:  newReferencesModel(a),
		search:      newCombobox(taxonComboSource(a), "/ to search taxa…"),
		keys:        defaultKeys(),
		focus:       focusTree,
		metaTitle:   title,
		help:        newHelpModel(),
	}
}

// Init loads the tree roots and the metadata screen's data in
// parallel. Metadata is small (one row) so pre-loading it costs nothing
// and makes alt+m instant. The detail pane waits until a taxon is
// highlighted before fetching anything.
func (m *model) Init() tea.Cmd {
	return tea.Batch(m.tree.Init(), m.metadata.Load(), m.references.Load())
}

// Update dispatches on message type. Key events flow to the focused pane;
// pane-agnostic events (window resize, quit) are handled here.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizePanes()
		return m, nil

	case tea.KeyMsg:
		// ctrl+c always quits, regardless of which pane / mode has focus.
		// Guards against a runaway text input that would otherwise trap
		// the user (e.g. an unresponsive search box).
		if key.Matches(msg, m.keys.HardQuit) {
			return m, tea.Quit
		}
		// Help overlay owns every key while showing — it is read-only,
		// and any keypress that isn't Esc/q is swallowed so mashing
		// keys to leave doesn't trigger the underlying view.
		if m.help.Active() {
			var cmd tea.Cmd
			m.help, cmd = m.help.Update(msg, m.keys)
			return m, cmd
		}
		// Command-mode prompt (`:` ex-mode) owns every key while
		// active. Enter executes; Esc cancels; other keys populate
		// the buffer. The dispatched payload lands as
		// commandExecuteMsg further down the switch.
		if m.command.Active() {
			var cmd tea.Cmd
			m.command, cmd = m.command.Update(msg, m.keys)
			return m, cmd
		}
		// Clear a lingering command-mode error on the next key so the
		// status bar returns to normal without needing a timer.
		m.command.ClearError()
		// `:` opens command mode from view mode. Handled here so it
		// wins over Alt-letter / Cancel / everything below.
		if key.Matches(msg, m.keys.Command) {
			m.command.Open()
			return m, nil
		}
		// Alt-letter view switches fire at the top level so they work
		// from any focus / mode (except the delete-confirm prompt,
		// which intercepts first). Editing an edit form and pressing
		// alt+M jumps you to metadata; the edit-form state stays put
		// underneath so alt+T returns to the same form.
		switch {
		case key.Matches(msg, m.keys.ViewTaxa):
			m.screen = viewTaxa
			return m, nil
		case key.Matches(msg, m.keys.ViewMeta):
			m.screen = viewMetadata
			return m, nil
		case key.Matches(msg, m.keys.ViewRefs):
			m.screen = viewReferences
			return m, nil
		}
		// Any key clears a lingering delete error banner and is consumed
		// so a stray keypress can't accidentally trigger another action.
		if m.confirmError != "" {
			m.confirmError = ""
			return m, nil
		}
		// Delete-confirmation intercept: while a "delete X? [y/N]" prompt
		// is up, only y / n / esc are meaningful. Y-run deletes; anything
		// else clears the prompt.
		if m.confirming {
			switch msg.String() {
			case "y", "Y":
				id := m.confirmTargetID
				m.confirming = false
				m.confirmTargetID = ""
				m.confirmTargetName = ""
				return m, m.deleteTaxonCmd(id)
			default:
				m.confirming = false
				m.confirmTargetID = ""
				m.confirmTargetName = ""
				return m, nil
			}
		}
		// Metadata screen key routing: edit-mode save/cancel take
		// priority, then form key events go through the metadata
		// model. `e` in view mode enters edit. Handled before the
		// generic edit-mode block below so alt+m from inside a
		// taxon edit form doesn't try to save the taxon.
		if m.screen == viewMetadata {
			if m.metadata.Editing() {
				switch {
				case key.Matches(msg, m.keys.Save):
					return m, m.metadata.Save()
				case key.Matches(msg, m.keys.Cancel):
					m.metadata.ExitEditMode()
					return m, nil
				}
				var cmd tea.Cmd
				m.metadata, cmd = m.metadata.Update(msg)
				return m, cmd
			}
			if key.Matches(msg, m.keys.Edit) {
				if cmd, ok := m.metadata.EnterEditModeCmd(); ok {
					return m, cmd
				}
				return m, nil
			}
			if key.Matches(msg, m.keys.Quit) {
				return m, tea.Quit
			}
			return m, nil
		}
		// References screen: read-only for now — arrow keys move the
		// list cursor, everything else falls through to the top-level
		// bindings. Save/Edit hooks are wired for the future
		// reference-edit slice.
		if m.screen == viewReferences {
			if key.Matches(msg, m.keys.Quit) {
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.references, cmd = m.references.Update(msg, m.keys)
			return m, cmd
		}
		// Add-reference modal owns every key while it's open — including
		// esc (the modal treats it as "close without a pick") and
		// ctrl+s (which we deliberately swallow so a stray save chord
		// mid-modal doesn't commit half-done edits). All routing goes
		// through detail.Update; the modal reports Finished() and the
		// detail model applies any pick itself.
		if m.detail.AddingReference() {
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
		// Edit-mode key handling: save/cancel take priority over every
		// other binding so text input can't steal them; otherwise the
		// key is forwarded to the detail pane's form (which routes to
		// the focused input / handles tab / etc.).
		if m.detail.Editing() {
			switch {
			case key.Matches(msg, m.keys.Save):
				return m, m.detail.Save()
			case key.Matches(msg, m.keys.Cancel):
				m.detail.ExitEditMode()
				return m, nil
			case key.Matches(msg, m.keys.AddRef):
				// Only offer add-reference from inside the name edit
				// form. The modal seeds itself from the current name;
				// with no name attached, OpenAddReference no-ops.
				return m, m.detail.OpenAddReference()
			}
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
		// Create-mode: same priority model as edit — Save commits, Esc
		// discards, everything else routes to the two-field form.
		if m.detail.Creating() {
			switch {
			case key.Matches(msg, m.keys.Save):
				return m, m.detail.CreateSave()
			case key.Matches(msg, m.keys.AddBasionym):
				// Only fires on step 1 of an accepted-name create;
				// CreateSaveThenBasionym returns nil in other states
				// and the key falls through harmlessly.
				if cmd := m.detail.CreateSaveThenBasionym(); cmd != nil {
					return m, cmd
				}
			case key.Matches(msg, m.keys.Cancel):
				m.detail.ExitCreateMode()
				return m, nil
			}
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
		// Search-focused view mode: keys go to the combobox. Esc
		// unfocuses; Enter (handled inside combobox) commits a pick,
		// which the shell then turns into a reveal. 'q' is a search
		// character while typing, not a quit shortcut — HardQuit above
		// covers ctrl+c so the user is never trapped.
		if m.focus == focusSearch {
			if key.Matches(msg, m.keys.Cancel) {
				m.search.Blur()
				m.search.Reset()
				m.focus = focusTree
				m.tree.focused = true
				return m, nil
			}
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			if id := m.consumeSearchPick(); id != "" {
				m.focus = focusTree
				m.tree.focused = true
				m.search.Blur()
				m.search.Reset()
				return m, tea.Batch(cmd, m.tree.RevealCmd(id))
			}
			return m, cmd
		}

		// View mode.
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Search):
			m.focus = focusSearch
			m.tree.focused = false
			return m, m.search.Focus()
		case key.Matches(msg, m.keys.Edit):
			if cmd, ok := m.detail.EnterEditModeCmd(); ok {
				return m, cmd
			}
		case key.Matches(msg, m.keys.New):
			// New child under the current tree selection. Empty selection
			// (empty tree) means a root-level taxon; core accepts an empty
			// parent id for that case.
			parentID := m.tree.SelectedID()
			parentLabel := m.tree.SelectedLabel()
			if cmd, ok := m.detail.EnterCreateModeCmd(parentID, parentLabel); ok {
				m.focus = focusDetail
				m.tree.focused = false
				return m, cmd
			}
		case key.Matches(msg, m.keys.NewSister):
			// New sister — attach to the current taxon's parent, so the
			// new taxon slots in as a sibling. If the current taxon is a
			// root (or the tree is empty), fall through to a root-level
			// create the same as `n` on an empty tree.
			parentID := m.tree.SelectedParentID()
			parentLabel := m.tree.SelectedParentLabel()
			if parentLabel == "" {
				parentLabel = "(root)"
			}
			if cmd, ok := m.detail.EnterCreateModeCmd(parentID, parentLabel); ok {
				m.focus = focusDetail
				m.tree.focused = false
				return m, cmd
			}
		case key.Matches(msg, m.keys.Delete):
			if m.editable {
				id := m.tree.SelectedID()
				if id != "" {
					m.confirming = true
					m.confirmTargetID = id
					m.confirmTargetName = m.tree.SelectedLabel()
					m.confirmError = ""
					return m, nil
				}
			}
		case key.Matches(msg, m.keys.SwitchFocus):
			m.toggleFocus()
			return m, nil
		}
		if m.focus == focusTree {
			prevSelected := m.tree.SelectedID()
			var cmd tea.Cmd
			m.tree, cmd = m.tree.Update(msg, m.keys)
			// Empty new selection (cursor on a sentinel or an empty
			// tree) shouldn't blank the detail pane — keep showing the
			// last real taxon while the user pages through siblings.
			if newSelected := m.tree.SelectedID(); newSelected != "" && newSelected != prevSelected {
				m.detail.SetCurrent(newSelected)
				return m, tea.Batch(cmd, m.detail.Load(newSelected))
			}
			return m, cmd
		}
		return m, nil

	case treeChildrenMsg, treeRevealedMsg:
		var cmd tea.Cmd
		prevSelected := m.tree.SelectedID()
		m.tree, cmd = m.tree.Update(msg, m.keys)
		if newSelected := m.tree.SelectedID(); newSelected != prevSelected && newSelected != "" {
			m.detail.SetCurrent(newSelected)
			return m, tea.Batch(cmd, m.detail.Load(newSelected))
		}
		return m, cmd

	case savedMsg:
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		// A reparent leaves the taxon at its old tree location — reveal
		// it under the new parent so the curator sees where it landed.
		if msg.err == nil && msg.parentMoved && msg.taxon != nil {
			cmd = tea.Batch(cmd, m.tree.RevealCmd(msg.taxon.ID))
		}
		return m, cmd

	case deletedMsg:
		if msg.err != nil {
			m.confirmError = formatCoreError(msg.err)
			return m, nil
		}
		// After delete, reveal the parent (or reload roots if the deleted
		// taxon was itself a root). The reveal replaces the whole tree
		// slice, so no stale reference to the deleted row is left behind.
		if msg.parentID != "" {
			m.detail.SetCurrent(msg.parentID)
			return m, tea.Batch(m.tree.RevealCmd(msg.parentID), m.detail.Load(msg.parentID))
		}
		m.detail.SetCurrent("")
		return m, m.tree.Init()

	case comboboxResultsMsg:
		// A combobox result can be for the tree search or for one of
		// the edit-form pickers. Route to both — the race-guarded
		// lastQuery check on each combobox drops messages that aren't
		// theirs, so the extra hop is cheap.
		var searchCmd tea.Cmd
		m.search, searchCmd = m.search.Update(msg)
		var detailCmd tea.Cmd
		m.detail, detailCmd = m.detail.Update(msg)
		return m, tea.Batch(searchCmd, detailCmd)

	case detailLoadedMsg, parentResolvedMsg, parsePreviewMsg, createdForBasionymMsg:
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd

	case metadataLoadedMsg, metadataSavedMsg:
		var cmd tea.Cmd
		m.metadata, cmd = m.metadata.Update(msg)
		// Refresh the cached status-bar title on a successful save so
		// the header immediately reflects the new dataset name.
		if t := m.metadata.Title(); t != "" {
			m.metaTitle = t
		}
		return m, cmd

	case referencesLoadedMsg, referenceDetailMsg:
		var cmd tea.Cmd
		m.references, cmd = m.references.Update(msg, m.keys)
		return m, cmd

	case commandExecuteMsg:
		// Fan out the parsed command's payload. Each recognized payload
		// type gets one case here; unknown payloads (nil, from an
		// empty `:` Enter) are dropped silently.
		switch p := msg.payload.(type) {
		case openHelpMsg:
			m.help.Open()
			return m, nil
		case commandErrMsg:
			m.command.SetError(p.msg)
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

// consumeSearchPick returns the id the user just picked from the tree
// search box, or "" if no pick happened this tick. Wraps combobox state
// so the shell doesn't reach into internals.
func (m *model) consumeSearchPick() string {
	if !m.search.ConsumeJustPicked() {
		return ""
	}
	return m.search.SelectedID()
}

// deletedMsg is delivered when a DeleteTaxon call finishes. parentID is
// the deleted taxon's parent (empty if it was a root), used to reveal
// the tree around where the taxon used to be.
type deletedMsg struct {
	deletedID string
	parentID  string
	err       error
}

// deleteTaxonCmd returns a tea.Cmd that runs DeleteTaxon in a WithTx and
// reports the outcome via deletedMsg. The parent lookup runs BEFORE the
// delete so we still have a valid ancestor even after the row is gone.
func (m *model) deleteTaxonCmd(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	a := m.a
	actor := m.actor
	return func() tea.Msg {
		ctx := contextWithActor(actor)
		t, err := a.GetTaxon(ctx, id)
		if err != nil {
			return deletedMsg{deletedID: id, err: err}
		}
		parentID := t.ParentID
		err = a.WithTx(ctx, func(tx *core.Tx) error {
			return tx.DeleteTaxon(id)
		})
		if err != nil {
			return deletedMsg{deletedID: id, err: err}
		}
		return deletedMsg{deletedID: id, parentID: parentID}
	}
}

// View renders the menu bar, the active screen (Taxa is the two-pane
// tree+detail; Metadata will be a single-pane form), and a status bar
// underneath. The tree pane has a search combobox pinned above it —
// same UX as the WUI.
func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	// Help overlay takes over the whole viewport. Simpler than
	// compositing over the tree/detail split and matches how vim
	// renders `:help` — full-window, Esc to return.
	if m.help.Active() {
		return m.help.View(m.width, m.height)
	}

	menu := m.renderMenuBar()
	menuH := lipgloss.Height(menu)
	statusH := 1
	// -1 for a blank separator row below the menu.
	contentH := max(m.height-menuH-statusH, 1)

	var content string
	switch m.screen {
	case viewMetadata:
		content = m.renderMetadataView(contentH)
	case viewReferences:
		content = m.renderReferencesView(contentH)
	default: // viewTaxa
		content = m.renderTaxaView(contentH)
	}

	status := m.statusBar()
	return lipgloss.JoinVertical(lipgloss.Left, menu, content, status)
}

// renderTaxaView is the classic tree+detail split, factored out of View
// so future screens can plug in as peers.
func (m *model) renderTaxaView(contentH int) string {
	treeW, detailW := m.paneWidths()

	treeBorder := treePaneStyle
	detailBorder := detailPaneStyle
	switch m.focus {
	case focusTree, focusSearch:
		treeBorder = focusedBorderStyle
	case focusDetail:
		detailBorder = focusedBorderStyle
	}

	// Search + tree stacked in the left pane. The search line takes one
	// row when idle; the dropdown grows below it when focused, at which
	// point we cap the tree view's height so it stays inside the border.
	searchLine := m.renderSearch(treeW - 4)
	searchH := lipgloss.Height(searchLine)
	treeInnerH := max(contentH-2-searchH, 1)
	m.tree.height = treeInnerH
	leftContent := lipgloss.JoinVertical(lipgloss.Left, searchLine, m.tree.View())

	treePane := treeBorder.
		Width(treeW - 2).
		Height(contentH - 2).
		Render(leftContent)
	detailPane := detailBorder.
		Width(detailW - 2).
		Height(contentH - 2).
		Render(m.detail.View())

	return lipgloss.JoinHorizontal(lipgloss.Top, treePane, detailPane)
}

// renderMetadataView delegates to the metadataModel. Same border style
// as the taxa panes so the two screens feel consistent.
func (m *model) renderMetadataView(contentH int) string {
	border := detailPaneStyle
	if m.screen == viewMetadata {
		border = focusedBorderStyle
	}
	return border.
		Width(m.width - 2).
		Height(contentH - 2).
		Render(m.metadata.View())
}

// renderReferencesView delegates to the referencesModel. Passes the
// inner width/height (border-adjusted) so the list+detail split can
// size itself.
func (m *model) renderReferencesView(contentH int) string {
	border := detailPaneStyle
	if m.screen == viewReferences {
		border = focusedBorderStyle
	}
	inner := m.references.View(m.width-4, contentH-2)
	return border.
		Width(m.width - 2).
		Height(contentH - 2).
		Render(inner)
}

// renderMenuBar draws the borgtui-style top strip: view names with the
// mnemonic letter underlined, active view highlighted. Screen-switch
// keys are Alt-mnemonic; the underline hints at which letter binds.
func (m *model) renderMenuBar() string {
	items := []struct {
		before, letter, after string
		active                bool
	}{
		{"", "T", "axa", m.screen == viewTaxa},
		// Metadata gets alt+m (every project has metadata; not every
		// project has media). Media, when it lands, takes alt+shift+m.
		{"", "M", "etadata", m.screen == viewMetadata},
		{"", "R", "eferences", m.screen == viewReferences},
	}
	var parts []string
	for _, it := range items {
		base := lipgloss.NewStyle()
		if it.active {
			base = base.Bold(true)
		}
		mnemonic := base.Underline(true).Render(it.letter)
		label := base.Render(it.before) + mnemonic + base.Render(it.after)
		parts = append(parts, label)
	}
	line := " " + strings.Join(parts, "   ") + " "
	// Trailing separator so the menu is visually distinct from the
	// content below without borrowing another row.
	sep := dimStyle.Render(strings.Repeat("─", max(m.width, 1)))
	return lipgloss.JoinVertical(lipgloss.Left, line, sep)
}

// renderSearch draws the search combobox line with a compact indicator
// so users notice the "/" hint. Width is the inner pane width (border
// removed by the caller).
func (m *model) renderSearch(width int) string {
	var view string
	if m.focus == focusSearch {
		view = m.search.View()
	} else {
		view = dimStyle.Render("/ search taxa")
	}
	// Trailing separator so the tree content below is visually distinct.
	sep := dimStyle.Render(strings.Repeat("─", max(width, 1)))
	return lipgloss.JoinVertical(lipgloss.Left, view, sep)
}

// paneWidths splits the terminal roughly 40/60 between tree and detail. A
// minimum tree width of 20 keeps names from truncating too aggressively on
// tiny terminals.
func (m *model) paneWidths() (tree, detail int) {
	tree = min(max(m.width*2/5, 20), m.width-20)
	detail = m.width - tree
	return tree, detail
}

// resizePanes tells the sub-models the current pane dimensions so their
// scrolling stays sane. Called after every WindowSizeMsg.
//
// Total vertical budget consumed by chrome: menu bar (1 row) + menu
// separator (1 row) + status bar (1 row) = 3 rows. Subtract that
// before handing the remaining height to the panes.
func (m *model) resizePanes() {
	treeW, detailW := m.paneWidths()
	paneH := max(m.height-3, 1)
	// Panes' inner content area is width/height minus 2 each for the border.
	m.tree.width = treeW - 4
	m.tree.height = paneH - 2
	m.detail.width = detailW - 4
	m.detail.height = paneH - 2
}

func (m *model) toggleFocus() {
	if m.focus == focusTree {
		m.focus = focusDetail
		m.tree.focused = false
	} else {
		m.focus = focusTree
		m.tree.focused = true
	}
}

func (m *model) statusBar() string {
	// Command-mode prompt or trailing error takes over the bar when
	// active. Checked first so `:` is unambiguous — the user can
	// always see what they've typed even mid-confirm.
	if s := m.command.View(m.width); s != "" {
		return s
	}
	// Confirm-delete takes over the whole bar so the y/N prompt is
	// unmissable. Same for delete errors — they replace the hints until
	// the next key clears them.
	if m.confirming {
		msg := fmt.Sprintf("  delete %q?  [y] confirm  [any other key] cancel  ",
			m.confirmTargetName)
		return statusStyle.Render(padRight(msg, m.width))
	}
	if m.confirmError != "" {
		msg := fmt.Sprintf("  delete failed: %s  [any key to dismiss]  ", m.confirmError)
		return statusStyle.Render(padRight(msg, m.width))
	}

	mode := "[read-only]"
	if m.editable {
		mode = "[edit]"
	}
	// Prefer the dataset metadata title over the raw path so the
	// status bar carries a curator-facing name instead of a filesystem
	// path. Path stays visible in the tooltip-less alternative — a
	// dedicated metadata screen will surface both fully once #58
	// lands. Legacy archives without a seeded title fall through to
	// the path.
	label := m.archivePath
	if title := m.metadataTitle(); title != "" {
		label = title
	}
	left := fmt.Sprintf(" %s  %s", mode, label)
	if m.editable && m.actor != "" {
		left += "  " + m.actor
	}
	var right string
	switch {
	case m.detail.Creating():
		right = "  tab field  ctrl+s save  esc cancel  "
	case m.detail.Editing():
		right = "  tab/shift-tab field  space cycle extinct  ctrl+s save  esc cancel  "
	case m.focus == focusSearch:
		right = "  type to search  ↑↓ nav  enter pick  esc cancel  "
	case m.editable:
		right = "  ↑↓ nav  → expand  g/G top/bot  / search  e edit  n/c new child  s new sister  d delete  :help  q quit  "
	default:
		right = "  ↑↓ nav  → expand  ← collapse  g/G top/bot  / search  tab switch  :help  q quit  "
	}

	// Pad between left and right so the bar spans the full width.
	pad := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	line := left + fmt.Sprintf("%*s", pad, "") + right
	return statusStyle.Render(line)
}

// padRight pads s with spaces to width so the reverse-styled status bar
// covers the whole terminal row.
func padRight(s string, width int) string {
	pad := max(width-lipgloss.Width(s), 0)
	return s + fmt.Sprintf("%*s", pad, "")
}

var (
	treePaneStyle      = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).BorderForeground(lipgloss.Color("240"))
	detailPaneStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).BorderForeground(lipgloss.Color("240"))
	focusedBorderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).BorderForeground(lipgloss.Color("6"))
	statusStyle        = lipgloss.NewStyle().Reverse(true)
)
