package view

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/sfborg/hive/core/ui"
)

// keyMap holds every Bubble Tea binding used by the view TUI. Field
// values are derived from core/ui.Keymap() at construction time via
// defaultKeys(), so the shared keymap is the single source of truth
// — the WUI reads the same list via GET /api/keymap and both frontends
// stay in sync.
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
	New         key.Binding // "n" / "c" — new child taxon under the selected one
	NewSister   key.Binding // "s" — new sister taxon at the same tree level
	Delete      key.Binding // "d" — delete the selected taxon (with confirm)
	LoadAll     key.Binding // "G" — vim-style: bottom of siblings (loads all if truncated)
	Top         key.Binding // "g" — vim-style: top of siblings
	ViewTaxa    key.Binding // "alt+t" — switch to the Taxa view
	ViewMeta    key.Binding // "alt+m" — switch to the Metadata view
	ViewRefs    key.Binding // "alt+r" — switch to the References view
	AddRef      key.Binding // "ctrl+a" — open the add-reference modal from the name edit form
	AddBasionym key.Binding // "ctrl+o" — from the create pane: save current + start adding the original combination
	Command     key.Binding // ":" — open command mode (opens the status-bar prompt)
	Help        key.Binding // synthetic: dispatched from the :h / :help command
}

// bindingFor looks up a Shortcut in the shared keymap by action, then
// builds a Bubble Tea key.Binding from its TUI keys and description.
// Panics on unknown action or missing TUI keys — every field in keyMap
// must correspond to a real entry in core/ui.Keymap(), and any TUI
// binding must have a non-empty TuiKeys slice. Both drift signals we
// want to catch at process start rather than silently at runtime.
func bindingFor(action string) key.Binding {
	for _, s := range ui.Keymap() {
		if s.Action != action {
			continue
		}
		keys := s.Keys[ui.FrontendTUI]
		if len(keys) == 0 {
			panic(fmt.Sprintf("core/ui: action %q has no TUI keys but is referenced by the TUI keymap", action))
		}
		return key.NewBinding(
			key.WithKeys(keys...),
			key.WithHelp(s.Display, s.Description),
		)
	}
	panic(fmt.Sprintf("core/ui: unknown action %q referenced by the TUI keymap", action))
}

func defaultKeys() keyMap {
	// HardQuit is a synthetic sub-binding of the shared "quit" action.
	// Bubble Tea consults it inside edit-mode forms where "q" is a
	// valid character but ctrl+c should still bail. Constructed
	// separately so its Keys set is just ctrl+c.
	hardQuit := key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("Ctrl+C", "quit"),
	)

	return keyMap{
		Up:          bindingFor("tree-up"),
		Down:        bindingFor("tree-down"),
		Expand:      bindingFor("tree-expand"),
		Collapse:    bindingFor("tree-collapse"),
		SwitchFocus: bindingFor("switch-pane"),
		Quit:        bindingFor("quit"),
		HardQuit:    hardQuit,
		Edit:        bindingFor("detail-edit"),
		Save:        bindingFor("form-save"),
		Cancel:      bindingFor("cancel"),
		Search:      bindingFor("search-focus"),
		New:         bindingFor("detail-new-child"),
		NewSister:   bindingFor("detail-new-sister"),
		Delete:      bindingFor("detail-delete"),
		LoadAll:     bindingFor("tree-last-sibling"),
		Top:         bindingFor("tree-first-sibling"),
		ViewTaxa:    bindingFor("view-taxa"),
		ViewMeta:    bindingFor("view-metadata"),
		ViewRefs:    bindingFor("view-references"),
		AddRef:      bindingFor("form-add-ref"),
		AddBasionym: bindingFor("form-add-basionym"),
		Command:     bindingFor("command-open"),
		// Help is intentionally not bound to a bare key — the shared
		// keymap marks it web-only ("?"). The TUI reaches the help
		// overlay via :h / :help through the command-mode subsystem.
		Help: key.NewBinding(),
	}
}
