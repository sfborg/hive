package view

import "github.com/charmbracelet/bubbles/key"

// keyMap holds every binding used by the view TUI. Bindings live in one
// place so the help overlay (added later) can render them from data and
// the PWA can eventually pull the same table via GET /api/keymap.
type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	Expand      key.Binding
	Collapse    key.Binding
	SwitchFocus key.Binding
	Quit        key.Binding // "q" or ctrl+c — disabled ("q" only) inside edit-mode forms
	HardQuit    key.Binding // ctrl+c always quits, even in edit mode
	Edit        key.Binding // "e" — enter edit mode on the current taxon
	Save        key.Binding // ctrl+s — save edits
	Cancel      key.Binding // esc — cancel edits, back to view mode
	Search      key.Binding // "/" — focus the search box above the tree
	New         key.Binding // "n" — new child taxon under the selected one
	Delete      key.Binding // "d" — delete the selected taxon (with confirm)
	LoadAll     key.Binding // "G" — vim-style: bottom of siblings (loads all if truncated)
	Top         key.Binding // "g" — vim-style: top of siblings
	ViewTaxa    key.Binding // "alt+t" — switch to the Taxa view
	ViewMeta    key.Binding // "alt+m" — switch to the Metadata view (Media, when it lands, takes alt+shift+m — every project has metadata; fewer have media)
	ViewRefs    key.Binding // "alt+r" — switch to the References view
	AddRef      key.Binding // "ctrl+a" — open the add-reference modal from the name edit form
	AddBasionym key.Binding // "ctrl+o" — from the create pane: save current + start adding the original combination
	Help        key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Expand: key.NewBinding(
			key.WithKeys("right", "l", "enter"),
			key.WithHelp("→/l/enter", "expand"),
		),
		Collapse: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "collapse"),
		),
		SwitchFocus: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch pane"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		HardQuit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Edit: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit"),
		),
		Save: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "save"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		New: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new child"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete"),
		),
		ViewTaxa: key.NewBinding(
			key.WithKeys("alt+t"),
			key.WithHelp("alt+t", "taxa view"),
		),
		// Every project has metadata; not every project has media.
		// Give metadata the cheaper unshifted chord and reserve
		// alt+shift+m for the future Media view. This diverges from
		// CLAUDE.md's original letter assignment; the doc gets
		// updated to match.
		ViewMeta: key.NewBinding(
			key.WithKeys("alt+m"),
			key.WithHelp("alt+m", "metadata view"),
		),
		ViewRefs: key.NewBinding(
			key.WithKeys("alt+r"),
			key.WithHelp("alt+r", "references view"),
		),
		AddRef: key.NewBinding(
			key.WithKeys("ctrl+a"),
			key.WithHelp("ctrl+a", "add reference (in name edit form)"),
		),
		AddBasionym: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "save + add original combination (in create pane)"),
		),
		LoadAll: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G / NG", "bottom of siblings / jump to Nth"),
		),
		Top: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g / Ng", "top of siblings / jump to Nth"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
	}
}
