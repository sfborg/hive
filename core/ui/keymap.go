// Package ui holds hive's shared UI descriptions — data that both the
// TUI and the WUI render from, so a single edit propagates to both
// frontends. See CLAUDE.md § Shared UI descriptions for the design
// intent.
//
// The keymap lives here rather than in cmd/hive/view so the HTTP
// layer can serve it via GET /api/keymap without pulling in Bubble
// Tea types.
package ui

// Scope identifies which pane / focus context a shortcut is active
// under. The help modal groups shortcuts by scope so curators can see
// at a glance which pane a binding needs focus on.
type Scope string

const (
	// ScopeGlobal — binding fires from anywhere. Reserved for keys
	// with clear, non-surprising intent (view switch, search focus,
	// help). See PARITY.md § Focus semantics.
	ScopeGlobal Scope = "global"
	// ScopeTree — binding fires only when the taxon tree has focus.
	ScopeTree Scope = "tree"
	// ScopeDetail — binding fires from the taxon detail pane in
	// view mode (edit, new, delete).
	ScopeDetail Scope = "detail"
	// ScopeForm — binding fires inside an edit form (save, cancel,
	// modal-open shortcuts).
	ScopeForm Scope = "form"
)

// Frontend target identifiers used as keys in Shortcut.Keys.
const (
	// FrontendTUI — the Bubble Tea terminal frontend. Key strings
	// use Bubble Tea's notation ("up", "esc", "alt+t").
	FrontendTUI = "tui"
	// FrontendWUI — the browser-hosted web user interface (the Lit
	// app under web/dist). Key strings use KeyboardEvent.key values
	// with a "modifier+" prefix convention matching Bubble Tea
	// ("ArrowUp", "Escape", "alt+t") so both frontends can share the
	// same parsing convention. The "WUI" spelling is deliberately
	// symmetric with TUI: hive calls its frontends by their medium
	// (terminal / web) rather than by implementation detail (Bubble
	// Tea / Lit). See CLAUDE.md for the reasoning; the app directory
	// still lives at web/ pending a wider rename.
	FrontendWUI = "wui"
)

// User overrides are deliberately not modeled as a Frontend value —
// they are per-frontend (a user might rebind a web key without
// affecting the TUI, or vice versa) and will layer server-side into
// the effective keymap when the customization mechanism ships. Keep
// this map two-valued (tui, web) so consumers can rely on that shape.

// Shortcut describes one keyboard binding. Populated once in Keymap()
// and consumed by both TUI keymap construction and the WUI via the
// /api/keymap endpoint.
//
// Keys is a per-frontend map. Absence of a frontend key means the
// binding is not available there — used for browser-preempted
// combinations (Ctrl+S/A/O), TUI-only affordances (Tab, :, q), and
// WUI-only affordances (? for help).
type Shortcut struct {
	Action      string              `json:"action"`
	Description string              `json:"description"`
	Display     string              `json:"display"`
	Scope       Scope               `json:"scope"`
	Keys        map[string][]string `json:"keys"`
}

// Keymap returns the canonical hive keymap. Slice order is the display
// order for the help modal within each scope; scope grouping is done
// by the renderer.
func Keymap() []Shortcut {
	tuiOnly := func(keys ...string) map[string][]string {
		return map[string][]string{FrontendTUI: keys}
	}
	wuiOnly := func(keys ...string) map[string][]string {
		return map[string][]string{FrontendWUI: keys}
	}
	both := func(tui, wui []string) map[string][]string {
		return map[string][]string{FrontendTUI: tui, FrontendWUI: wui}
	}

	return []Shortcut{
		// ---- global ----
		{
			Action:      "view-taxa",
			Description: "switch to Taxa view",
			Display:     "Alt+T",
			Scope:       ScopeGlobal,
			Keys:        both([]string{"alt+t"}, []string{"alt+t"}),
		},
		{
			Action:      "view-metadata",
			Description: "switch to Metadata view",
			Display:     "Alt+M",
			Scope:       ScopeGlobal,
			Keys:        both([]string{"alt+m"}, []string{"alt+m"}),
		},
		{
			Action:      "view-references",
			Description: "switch to References view",
			Display:     "Alt+R",
			Scope:       ScopeGlobal,
			Keys:        both([]string{"alt+r"}, []string{"alt+r"}),
		},
		{
			Action:      "search-focus",
			Description: "focus the taxon search box",
			Display:     "/",
			Scope:       ScopeGlobal,
			Keys:        both([]string{"/"}, []string{"/"}),
		},
		{
			Action:      "help-open",
			Description: "open keyboard shortcut help",
			Display:     "?",
			Scope:       ScopeGlobal,
			// TUI opens help via the command mode (:h / :help) rather
			// than the bare ? key, since ? is vim's reverse-search
			// binding and hive's TUI is deliberately vim-adjacent.
			// The command-open row below carries that affordance.
			Keys: wuiOnly("?"),
		},
		{
			Action:      "command-open",
			Description: "open command mode (try :help)",
			Display:     ":",
			Scope:       ScopeGlobal,
			// WUI has no command mode; discovery happens via the ?
			// header button + shortcut instead.
			Keys: tuiOnly(":"),
		},
		{
			Action:      "cancel",
			Description: "cancel / close overlay / blur pane",
			Display:     "Esc",
			Scope:       ScopeGlobal,
			Keys:        both([]string{"esc"}, []string{"Escape"}),
		},
		{
			Action:      "switch-pane",
			Description: "switch focus between panes",
			Display:     "Tab",
			Scope:       ScopeGlobal,
			// WUI relies on browser-native Tab focus traversal; a
			// bespoke pane-switch binding would fight it.
			Keys: tuiOnly("tab"),
		},
		{
			Action:      "quit",
			Description: "quit hive",
			Display:     "q",
			Scope:       ScopeGlobal,
			// WUI has no quit — closing the tab is the browser's job.
			Keys: tuiOnly("q", "ctrl+c"),
		},

		// ---- tree ----
		{
			Action:      "tree-up",
			Description: "move cursor up one taxon",
			Display:     "↑ / k",
			Scope:       ScopeTree,
			Keys:        both([]string{"up", "k"}, []string{"ArrowUp", "k"}),
		},
		{
			Action:      "tree-down",
			Description: "move cursor down one taxon",
			Display:     "↓ / j",
			Scope:       ScopeTree,
			Keys:        both([]string{"down", "j"}, []string{"ArrowDown", "j"}),
		},
		{
			Action:      "tree-expand",
			Description: "expand and step into first child (Nl descends N)",
			Display:     "→ / l / Enter",
			Scope:       ScopeTree,
			Keys:        both([]string{"right", "l", "enter"}, []string{"ArrowRight", "l", "Enter"}),
		},
		{
			Action:      "tree-collapse",
			Description: "collapse node, or step out to parent",
			Display:     "← / h",
			Scope:       ScopeTree,
			Keys:        both([]string{"left", "h"}, []string{"ArrowLeft", "h"}),
		},
		{
			Action:      "tree-first-sibling",
			Description: "first sibling in group (prefix with count for Nth)",
			Display:     "g / Ng",
			Scope:       ScopeTree,
			Keys:        both([]string{"g"}, []string{"g"}),
		},
		{
			Action:      "tree-last-sibling",
			Description: "last loaded sibling (loads more if truncated)",
			Display:     "G / NG",
			Scope:       ScopeTree,
			Keys:        both([]string{"G"}, []string{"G"}),
		},

		// ---- detail ----
		{
			Action:      "detail-edit",
			Description: "edit the selected taxon",
			Display:     "e",
			Scope:       ScopeDetail,
			// WUI uses the on-screen Edit button.
			Keys: tuiOnly("e"),
		},
		{
			Action:      "detail-new-child",
			Description: "new child taxon under the selection",
			Display:     "n",
			Scope:       ScopeDetail,
			Keys:        tuiOnly("n"),
		},
		{
			Action:      "detail-delete",
			Description: "delete the selected taxon",
			Display:     "d",
			Scope:       ScopeDetail,
			Keys:        tuiOnly("d"),
		},

		// ---- form ----
		{
			Action:      "form-save",
			Description: "save edits",
			Display:     "Ctrl+S",
			Scope:       ScopeForm,
			// WUI uses on-screen Save; Ctrl+S is browser Save Page As.
			Keys: tuiOnly("ctrl+s"),
		},
		{
			Action:      "form-add-ref",
			Description: "add reference (in name edit form)",
			Display:     "Ctrl+A",
			Scope:       ScopeForm,
			// WUI uses on-screen Add Reference; Ctrl+A is browser Select All.
			Keys: tuiOnly("ctrl+a"),
		},
		{
			Action:      "form-add-basionym",
			Description: "save + start adding original combination",
			Display:     "Ctrl+O",
			Scope:       ScopeForm,
			// WUI uses on-screen affordance; Ctrl+O is browser Open File.
			Keys: tuiOnly("ctrl+o"),
		},
	}
}

// ForFrontend returns the subset of Keymap() available in the given
// frontend (FrontendTUI or FrontendWUI). Both frontends' help
// renderers use this to filter — a curator using the WUI shouldn't
// be shown TUI-only bindings that can't fire in the browser.
func ForFrontend(frontend string) []Shortcut {
	all := Keymap()
	out := make([]Shortcut, 0, len(all))
	for _, s := range all {
		if len(s.Keys[frontend]) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// TUIKeysFor returns the TUI key strings for the given action, or nil
// if the action has no TUI binding. Convenience wrapper used by the
// TUI's Bubble Tea keymap construction.
func TUIKeysFor(action string) []string {
	for _, s := range Keymap() {
		if s.Action == action {
			return s.Keys[FrontendTUI]
		}
	}
	return nil
}
