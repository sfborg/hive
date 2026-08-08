package view

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sfborg/hive/core"
)

// The TUI counterpart of the WUI's <sfga-combobox>: a text input with an
// inline dropdown of filtered results. Same UX as the web version:
//   * type to filter
//   * ↓/↑ navigate the dropdown
//   * Enter picks the highlighted term
//   * Esc closes the dropdown (keeps focus in the input)
//   * blur (focus leaves) reverts the input to the last committed value
//     so partial typing never becomes a phantom state
//
// Two data sources are provided at the bottom of this file:
//   * vocabComboSource(vocab, name) — synchronous filter over a cached
//     vocabulary bundle (rank, nom_code, nom_status, …)
//   * taxonComboSource(archive)      — async SearchTaxa (parent picker)
//
// Both fire via tea.Cmd → comboboxResultsMsg. Cmds keep Update() non-blocking
// so a slow query never freezes the whole TUI.

// term is one picker option: display label + underlying id.
type term struct {
	ID    string
	Label string
}

// comboboxSource returns a tea.Cmd that produces a comboboxResultsMsg for
// the given query. Sync sources wrap the filter in a self-firing cmd;
// async sources run the fetch in a goroutine.
type comboboxSource func(query string) tea.Cmd

// comboboxResultsMsg carries fetched results back to a combobox. The
// query field is the race guard — a combobox that receives a message
// for a query other than the one it last fired ignores it, so stale
// async results don't overwrite fresher ones.
type comboboxResultsMsg struct {
	query   string
	results []term
}

// visibleDropdownRows caps how many result lines the combobox renders
// inline. Beyond this we show "(+N more…)" so the form doesn't blow up
// vertically on a rank search that returns 30 candidates.
const visibleDropdownRows = 8

// combobox is a single picker instance. Owned by the parent model
// (detail.go); Update forwards messages to it when it's focused.
type combobox struct {
	input   textinput.Model
	source  comboboxSource
	results []term
	hover   int
	open    bool
	focused bool

	committedID   string
	committedName string

	// justPicked is set to true on the Update tick when Enter committed a
	// new selection. Parents check ConsumeJustPicked() to react to a pick
	// exactly once (e.g. fire a tree reveal). Cleared on read.
	justPicked bool

	// lastQuery is the race-guard reference for async sources. When a
	// results message arrives, we compare msg.query against lastQuery
	// and drop the message if the user has since kept typing.
	lastQuery string
}

func newCombobox(source comboboxSource, placeholder string) combobox {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = ""
	ti.CharLimit = 200
	return combobox{input: ti, source: source}
}

// SetValue seeds the picker with an existing selection (id + display name).
// Called from EnterEditMode to populate the form from the current taxon.
func (c *combobox) SetValue(id, name string) {
	c.committedID = id
	c.committedName = name
	c.input.SetValue(name)
}

// SelectedID returns the current committed selection's id — read by Save
// to build the write payload.
func (c *combobox) SelectedID() string { return c.committedID }

// SelectedName returns the display name for the current committed
// selection. Rarely needed externally; kept for symmetry with SelectedID.
func (c *combobox) SelectedName() string { return c.committedName }

// Focus enters the input and, for zero-min-chars vocab sources, triggers
// an immediate search so the dropdown shows all options right away.
func (c *combobox) Focus() tea.Cmd {
	c.focused = true
	c.open = true
	return tea.Batch(c.input.Focus(), c.triggerSearch())
}

// Blur exits input mode. If the user typed something without picking, the
// input reverts to the last committed name so no phantom state persists.
func (c *combobox) Blur() {
	c.focused = false
	c.open = false
	c.input.Blur()
	if c.input.Value() != c.committedName {
		c.input.SetValue(c.committedName)
	}
}

// Update handles key input and result messages. Called from the parent
// model's Update when this combobox is the focused form field.
func (c combobox) Update(msg tea.Msg) (combobox, tea.Cmd) {
	switch msg := msg.(type) {
	case comboboxResultsMsg:
		if msg.query != c.lastQuery {
			return c, nil // stale
		}
		c.results = msg.results
		if c.hover >= len(c.results) {
			c.hover = 0
		}
		if c.focused {
			c.open = true
		}
		return c, nil

	case tea.KeyMsg:
		if !c.focused {
			return c, nil
		}
		switch msg.String() {
		case "down":
			if !c.open {
				c.open = true
				return c, c.triggerSearch()
			}
			if len(c.results) > 0 {
				c.hover = (c.hover + 1) % len(c.results)
			}
			return c, nil
		case "up":
			if len(c.results) > 0 {
				c.hover = (c.hover - 1 + len(c.results)) % len(c.results)
			}
			return c, nil
		case "enter":
			if c.open && c.hover >= 0 && c.hover < len(c.results) {
				sel := c.results[c.hover]
				c.committedID = sel.ID
				c.committedName = sel.Label
				c.input.SetValue(sel.Label)
				c.open = false
				c.justPicked = true
				return c, nil
			}
			return c, nil
		case "esc":
			// Close dropdown without committing. Parent's Esc-handling
			// (cancel-edit) fires only when the combobox is closed.
			if c.open {
				c.open = false
				return c, nil
			}
			return c, nil
		}
		// Any other key: pass to the underlying textinput, then re-trigger
		// a search on the new query. Sync sources return results
		// immediately; async sources return a cmd we compose with the
		// input's own cmd.
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return c, tea.Batch(cmd, c.triggerSearch())
	}
	return c, nil
}

// triggerSearch fires the current source with the current input value.
// Records the query as `lastQuery` so a stale async response can be
// discarded when it arrives.
func (c *combobox) triggerSearch() tea.Cmd {
	q := c.input.Value()
	c.lastQuery = q
	return c.source(q)
}

// View renders the input line followed by the dropdown (when open and
// focused). Return value is a single string with embedded newlines —
// the parent model concatenates it into the wider form layout.
func (c combobox) View() string {
	var b strings.Builder
	b.WriteString(c.input.View())
	if !c.focused || !c.open || len(c.results) == 0 {
		return b.String()
	}
	visible := c.results
	overflow := 0
	if len(visible) > visibleDropdownRows {
		visible = visible[:visibleDropdownRows]
		overflow = len(c.results) - visibleDropdownRows
	}
	for i, r := range visible {
		b.WriteByte('\n')
		prefix := "  "
		style := dimStyle
		if i == c.hover {
			prefix = "▶ "
			style = focusedStyle
		}
		b.WriteString(style.Render(prefix + r.Label))
	}
	if overflow > 0 {
		b.WriteByte('\n')
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (+%d more…)", overflow)))
	}
	return b.String()
}

// IsOpen reports whether the dropdown is currently expanded. Parents use
// this to decide whether Esc should close the dropdown vs. cancel the
// whole edit form.
func (c combobox) IsOpen() bool { return c.open }

// ConsumeJustPicked returns true exactly once after Enter commits a
// selection. Parents call this after each Update to detect a fresh pick
// without polling committedID (which stays sticky across ticks). The
// flag is cleared on read so subsequent ticks don't misfire.
func (c *combobox) ConsumeJustPicked() bool {
	if c.justPicked {
		c.justPicked = false
		return true
	}
	return false
}

// Reset clears input, results, and committed selection so the picker is
// ready for a fresh search. Called after a search pick so the next `/`
// starts on an empty box.
func (c *combobox) Reset() {
	c.input.SetValue("")
	c.results = nil
	c.hover = 0
	c.open = false
	c.committedID = ""
	c.committedName = ""
	c.lastQuery = ""
}

// ---------- sources ----------

// vocabComboSource makes a source that filters the named controlled
// vocabulary in memory. Empty query returns all terms — same "select-like
// behavior on empty focus" as the WUI combobox for min-search-chars=0.
func vocabComboSource(vocab *core.Vocabulary, name string) comboboxSource {
	return func(q string) tea.Cmd {
		return func() tea.Msg {
			terms := vocabTerms(vocab, name)
			needle := strings.ToLower(strings.TrimSpace(q))
			var results []term
			for _, t := range terms {
				if needle != "" &&
					!strings.Contains(strings.ToLower(t.Name), needle) &&
					!strings.Contains(strings.ToLower(t.ID), needle) {
					continue
				}
				results = append(results, term{
					ID:    t.ID,
					Label: vocabLabel(t),
				})
			}
			return comboboxResultsMsg{query: q, results: results}
		}
	}
}

// vocabComboSourceIn is vocabComboSource restricted to a specific ID
// allowlist. Used by the create form's rank picker to hide ranks
// that aren't valid children of the current parent (per
// core/ui.ValidChildRankIDs). Empty / nil allow list disables
// filtering — the full vocab flows through.
func vocabComboSourceIn(vocab *core.Vocabulary, name string, allowed []string) comboboxSource {
	if len(allowed) == 0 {
		return vocabComboSource(vocab, name)
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		allowSet[id] = true
	}
	return func(q string) tea.Cmd {
		return func() tea.Msg {
			terms := vocabTerms(vocab, name)
			needle := strings.ToLower(strings.TrimSpace(q))
			var results []term
			for _, t := range terms {
				if !allowSet[t.ID] {
					continue
				}
				if needle != "" &&
					!strings.Contains(strings.ToLower(t.Name), needle) &&
					!strings.Contains(strings.ToLower(t.ID), needle) {
					continue
				}
				results = append(results, term{
					ID:    t.ID,
					Label: vocabLabel(t),
				})
			}
			return comboboxResultsMsg{query: q, results: results}
		}
	}
}

// vocabLabel picks the human-facing label for a vocab term, falling back
// to the id when the schema didn't ship a col__name and finally to
// "(unset)" for the empty-string seed row.
func vocabLabel(t core.VocabTerm) string {
	if t.Name != "" {
		return t.Name
	}
	if t.ID != "" {
		return t.ID
	}
	return "(unset)"
}

// nomenComboSource filters the NOMEN vocabulary by the current name's
// nomenclatural code (via core.NomenCodePrefix) so a name under
// ZOOLOGICAL only sees ICZN terms. Empty codeID → show all terms.
// Terms match against label or short-local (NOMEN_XXXXXXX) so a curator
// who knows the identifier can jump straight to it.
//
// Ordering: popular terms (the CoL-mapped roots) float to the top
// regardless of query, then everything else alphabetical by label.
// Matches TW's "popular statuses on the first tab" pattern — the
// common 90%-of-cases choices land in front of the curator without
// needing to scroll or type.
func nomenComboSource(codeID string) comboboxSource {
	prefix := core.NomenCodePrefix(codeID)
	return func(q string) tea.Cmd {
		return func() tea.Msg {
			terms, err := core.Nomen()
			if err != nil {
				return comboboxResultsMsg{query: q, results: nil}
			}
			needle := strings.ToLower(strings.TrimSpace(q))
			type row struct {
				t    term
				pop  bool
				name string
			}
			var picks []row
			for _, t := range terms {
				// Filter to TW-classified statuses only — drops ranks,
				// name-part descriptors, and relationship classes that
				// happen to share the NOMEN namespace but don't belong
				// in the name-status picker. Also drops the Latinized
				// subtree (gender / part-of-speech name-part
				// annotations) — TW models these as classifications
				// but they're not nomenclatural statuses.
				if t.Kind != "classification" {
					continue
				}
				if strings.HasPrefix(t.Class, "TaxonNameClassification::Latinized") {
					continue
				}
				if prefix != "" && t.Code != prefix {
					continue
				}
				if needle != "" &&
					!strings.Contains(strings.ToLower(t.Label), needle) &&
					!strings.Contains(strings.ToLower(t.Local), needle) {
					continue
				}
				picks = append(picks, row{
					t:    term{ID: t.ID, Label: t.Label},
					pop:  t.Popular,
					name: strings.ToLower(t.Label),
				})
			}
			sort.SliceStable(picks, func(i, j int) bool {
				if picks[i].pop != picks[j].pop {
					return picks[i].pop
				}
				return picks[i].name < picks[j].name
			})
			results := make([]term, len(picks))
			for i, p := range picks {
				results[i] = p.t
			}
			return comboboxResultsMsg{query: q, results: results}
		}
	}
}

// referenceComboSource returns a source that searches the archive for
// references matching q, via core.Archive.SearchReferences. Same async
// pattern as taxonComboSource. Requires at least 2 chars.
func referenceComboSource(a *core.Archive) comboboxSource {
	return func(q string) tea.Cmd {
		return func() tea.Msg {
			trimmed := strings.TrimSpace(q)
			if len(trimmed) < 2 {
				return comboboxResultsMsg{query: q, results: nil}
			}
			hits, err := a.SearchReferences(context.Background(), trimmed, 20)
			if err != nil {
				return comboboxResultsMsg{query: q, results: nil}
			}
			results := make([]term, 0, len(hits))
			for _, h := range hits {
				results = append(results, term{ID: h.ID, Label: referenceHitLabel(h)})
			}
			return comboboxResultsMsg{query: q, results: results}
		}
	}
}

// referenceHitLabel mirrors the WUI's composeReferenceLabel — "Author
// (Year) Title", falling back to citation or id when structured fields
// are absent.
func referenceHitLabel(h core.ReferenceHit) string {
	parts := make([]string, 0, 3)
	if h.Author != "" {
		parts = append(parts, h.Author)
	}
	if h.Year != "" {
		parts = append(parts, "("+h.Year+")")
	}
	if h.Title != "" {
		parts = append(parts, h.Title)
	}
	if len(parts) == 0 {
		if h.Citation != "" {
			return h.Citation
		}
		return h.ID
	}
	return strings.Join(parts, " ")
}

// taxonComboSource returns a source that searches the archive for taxa
// matching q, via core.Archive.SearchTaxa. Runs in a goroutine (SQLite
// is fast but "fast" in Bubble Tea means "measured in fractions of a
// display frame" — anything requiring I/O goes through tea.Cmd).
//
// Requires at least 2 chars to fire; short queries produce empty results
// so the dropdown doesn't flash "no matches" between keystrokes.
func taxonComboSource(a *core.Archive) comboboxSource {
	return func(q string) tea.Cmd {
		return func() tea.Msg {
			trimmed := strings.TrimSpace(q)
			if len(trimmed) < 2 {
				return comboboxResultsMsg{query: q, results: nil}
			}
			hits, err := a.SearchTaxa(context.Background(), trimmed, 20)
			if err != nil {
				return comboboxResultsMsg{query: q, results: nil}
			}
			results := make([]term, 0, len(hits))
			for _, h := range hits {
				results = append(results, term{ID: h.ID, Label: h.Label.Text})
			}
			return comboboxResultsMsg{query: q, results: results}
		}
	}
}
