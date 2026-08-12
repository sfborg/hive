package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	hive "github.com/sfborg/hive/pkg"
)

// issuesModel is the Alt+I "Issues" screen — the TUI counterpart to
// the WUI's SfgaIssues component. Two-pane layout matching the shared
// design: severity chips + rule list on the left, issue list on the
// right. Row selection uses tab to cross panes; Up/Down moves the
// cursor within the focused pane.
//
// The screen filters by severity (default error+warn, per CLAUDE.md §
// Validation) with a "d" chord to toggle debug + info visibility. "r"
// triggers a reindex (POST /api/reindex/validation counterpart —
// same underlying Archive.ReindexValidation). "Enter" on an issue
// with a resolvable link_taxon_id navigates: switches to the Taxa
// screen and reveals the flagged record.
//
// Ports-and-adapters note: this model reaches through hive.Archive
// only. Its network shape (severity multi-filter, pagination cursor)
// matches the HTTP endpoints exactly so the two frontends stay in
// step, but no HTTP client lives here — direct hive calls only.
type issuesModel struct {
	a *hive.Archive

	// Facet state.
	severities map[string]bool // severity → on
	showDiag   bool            // convenience: enables info + debug when true

	// Cached data.
	summary       []hive.IssueSummaryRow // full unfiltered
	rules         []hive.IssueSummaryRow // filtered by severities
	issues        []hive.Issue
	total         int
	offset        int
	pageSize      int
	selectedRule  string // rule_id filter — "" means "all"
	selectedTable string // table filter — matches the summary row's Table
	loaded        bool
	err           error
	statusMsg     string
	reindexing    bool

	// Focus state within the screen: which pane owns arrow keys.
	focus issuesFocus

	// Cursor positions per pane.
	rulesCursor  int
	issuesCursor int
}

type issuesFocus int

const (
	issuesFocusRules issuesFocus = iota
	issuesFocusList
)

// defaultIssueSeverities matches the WUI default: error + warn. Debug
// and info are diagnostic — they need explicit opt-in via the "d"
// toggle so a curator scanning the screen isn't nudged into "fixing"
// signals like parse-quality tier 2 that aren't curator-fixable.
func defaultIssueSeverities() map[string]bool {
	return map[string]bool{"error": true, "warn": true}
}

func newIssuesModel(a *hive.Archive) issuesModel {
	return issuesModel{
		a:          a,
		severities: defaultIssueSeverities(),
		pageSize:   50,
		focus:      issuesFocusRules,
	}
}

// ---- messages ----

type issuesSummaryMsg struct {
	rows []hive.IssueSummaryRow
	err  error
}

type issuesPageMsg struct {
	rows  []hive.Issue
	total int
	err   error
}

// issueReindexMsg carries the outcome of a reindex triggered from the
// screen. Presented as a status-bar message rather than a modal.
type issueReindexMsg struct {
	err error
}

// issueNavigateMsg is delivered when the curator hits Enter on an
// issue row that has a resolvable link_taxon_id. The shell catches
// this to switch to the Taxa screen and reveal the taxon.
type issueNavigateMsg struct {
	TaxonID string
}

// ---- commands ----

// Load kicks off the initial summary + first page fetch. Called by
// the shell's Init alongside the tree / metadata loads.
func (m issuesModel) Load() tea.Cmd {
	return tea.Batch(m.loadSummary(), m.loadPage())
}

func (m issuesModel) loadSummary() tea.Cmd {
	a := m.a
	return func() tea.Msg {
		rows, err := a.IssueSummary(context.Background())
		return issuesSummaryMsg{rows: rows, err: err}
	}
}

func (m issuesModel) loadPage() tea.Cmd {
	a := m.a
	filter := hive.IssueFilter{
		TableName:  m.selectedTable,
		RuleID:     m.selectedRule,
		Severities: m.activeSeverities(),
	}
	limit := m.pageSize
	offset := m.offset
	return func() tea.Msg {
		rows, total, err := a.ListIssues(context.Background(), filter, limit, offset)
		return issuesPageMsg{rows: rows, total: total, err: err}
	}
}

func (m issuesModel) reindexCmd() tea.Cmd {
	a := m.a
	return func() tea.Msg {
		err := a.ReindexValidation(context.Background(), nil)
		return issueReindexMsg{err: err}
	}
}

// ---- Update ----

// Update handles screen-scoped events. The shell only routes tea.KeyMsg
// here when the Issues screen is active; async messages (summary /
// page fetches, reindex completion) route unconditionally so a fetch
// that started before an alt+t away still lands.
func (m issuesModel) Update(msg tea.Msg, keys keyMap) (issuesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case issuesSummaryMsg:
		m.err = msg.err
		m.summary = msg.rows
		m.recomputeRules()
		m.loaded = true
		return m, nil

	case issuesPageMsg:
		m.err = msg.err
		m.issues = msg.rows
		m.total = msg.total
		if m.issuesCursor >= len(m.issues) {
			m.issuesCursor = 0
		}
		return m, nil

	case issueReindexMsg:
		m.reindexing = false
		if msg.err != nil {
			m.statusMsg = "reindex failed: " + msg.err.Error()
			return m, nil
		}
		m.statusMsg = "reindex complete"
		return m, tea.Batch(m.loadSummary(), m.loadPage())

	case tea.KeyMsg:
		return m.handleKey(msg, keys)
	}
	return m, nil
}

func (m issuesModel) handleKey(msg tea.KeyMsg, keys keyMap) (issuesModel, tea.Cmd) {
	// Any key clears a status banner so it doesn't linger past its
	// relevance.
	if m.statusMsg != "" && !key.Matches(msg, keys.SwitchFocus) {
		m.statusMsg = ""
	}

	switch msg.String() {
	case "1":
		return m.toggleSeverity("error"), m.loadPage()
	case "2":
		return m.toggleSeverity("warn"), m.loadPage()
	case "3":
		return m.toggleSeverity("info"), m.loadPage()
	case "4":
		return m.toggleSeverity("debug"), m.loadPage()
	case "d":
		// Convenience toggle for info + debug together — same effect as
		// pressing 3 and 4 in sequence but easier to reach for.
		m.showDiag = !m.showDiag
		if m.showDiag {
			m.severities["info"] = true
			m.severities["debug"] = true
		} else {
			delete(m.severities, "info")
			delete(m.severities, "debug")
		}
		m.offset = 0
		m.recomputeRules()
		return m, m.loadPage()
	case "r":
		if m.reindexing {
			return m, nil
		}
		m.reindexing = true
		m.statusMsg = "recomputing issues…"
		return m, m.reindexCmd()
	}

	switch {
	case key.Matches(msg, keys.SwitchFocus):
		if m.focus == issuesFocusRules {
			m.focus = issuesFocusList
		} else {
			m.focus = issuesFocusRules
		}
		return m, nil

	case key.Matches(msg, keys.Up):
		if m.focus == issuesFocusRules {
			if m.rulesCursor > 0 {
				m.rulesCursor--
			}
		} else if m.issuesCursor > 0 {
			m.issuesCursor--
		}
		return m, nil

	case key.Matches(msg, keys.Down):
		if m.focus == issuesFocusRules {
			if m.rulesCursor < len(m.rules)-1 {
				m.rulesCursor++
			}
		} else if m.issuesCursor < len(m.issues)-1 {
			m.issuesCursor++
		}
		return m, nil

	case msg.String() == "enter":
		if m.focus == issuesFocusRules {
			return m.applyRuleFilter(), m.loadPage()
		}
		return m, m.navigateFromCurrent()

	case msg.String() == "left" || msg.String() == "h":
		// Previous page in the list pane. In the rules pane this is a
		// no-op since the rules aren't paginated.
		if m.focus == issuesFocusList && m.offset > 0 {
			m.offset = maxInt(0, m.offset-m.pageSize)
			m.issuesCursor = 0
			return m, m.loadPage()
		}
	case msg.String() == "right" || msg.String() == "l":
		// Next page — same guard as WUI: only advance when a full
		// page's worth of rows remain past the current window.
		if m.focus == issuesFocusList && m.offset+len(m.issues) < m.total {
			m.offset += m.pageSize
			m.issuesCursor = 0
			return m, m.loadPage()
		}

	case msg.String() == "backspace" || msg.String() == "esc":
		// Clear the rule filter — companion to Enter-on-rule.
		if m.selectedRule != "" || m.selectedTable != "" {
			m.selectedRule = ""
			m.selectedTable = ""
			m.offset = 0
			m.rulesCursor = 0
			return m, m.loadPage()
		}
	}
	return m, nil
}

func (m issuesModel) toggleSeverity(sev string) issuesModel {
	if m.severities[sev] {
		delete(m.severities, sev)
	} else {
		m.severities[sev] = true
	}
	m.offset = 0
	m.recomputeRules()
	// Keep showDiag in sync so the "d" chord stays meaningful.
	m.showDiag = m.severities["info"] || m.severities["debug"]
	return m
}

// applyRuleFilter narrows the list to the rule currently under the
// rules-pane cursor. If that rule is already the active filter, this
// clears it — same "toggle by re-selecting" convention the WUI uses.
func (m issuesModel) applyRuleFilter() issuesModel {
	if m.rulesCursor >= len(m.rules) {
		return m
	}
	r := m.rules[m.rulesCursor]
	if m.selectedRule == r.RuleID && m.selectedTable == r.TableName {
		m.selectedRule = ""
		m.selectedTable = ""
	} else {
		m.selectedRule = r.RuleID
		m.selectedTable = r.TableName
	}
	m.offset = 0
	m.issuesCursor = 0
	return m
}

// navigateFromCurrent returns a Cmd that emits issueNavigateMsg with
// the current issue's link_taxon_id, when set. Returns nil for
// orphan rows so pressing Enter on them is a no-op.
func (m issuesModel) navigateFromCurrent() tea.Cmd {
	if m.issuesCursor >= len(m.issues) {
		return nil
	}
	issue := m.issues[m.issuesCursor]
	if issue.LinkTaxonID == "" {
		return nil
	}
	taxonID := issue.LinkTaxonID
	return func() tea.Msg {
		return issueNavigateMsg{TaxonID: taxonID}
	}
}

// activeSeverities returns the currently-selected severity set as a
// slice, in a stable order. Used to build the list-fetch filter.
func (m issuesModel) activeSeverities() []string {
	// Stable order: error, warn, info, debug (severity-desc, matches
	// the SQL ORDER-BY in hive.ListIssues).
	order := []string{"error", "warn", "info", "debug"}
	var out []string
	for _, s := range order {
		if m.severities[s] {
			out = append(out, s)
		}
	}
	return out
}

// recomputeRules rebuilds the rules list filtered by the active
// severity set. Sorted by count desc so the biggest bar is at the
// top; ties broken by rule id for stability.
func (m *issuesModel) recomputeRules() {
	filtered := make([]hive.IssueSummaryRow, 0, len(m.summary))
	for _, r := range m.summary {
		if m.severities[r.Severity] {
			filtered = append(filtered, r)
		}
	}
	// Simple descending sort by count using an insertion pass — few
	// enough rows (bounded by unique rule×severity combinations) that
	// avoiding sort.Slice keeps the file dependency-clean.
	for i := 1; i < len(filtered); i++ {
		for j := i; j > 0; j-- {
			a := filtered[j-1]
			b := filtered[j]
			if a.Count > b.Count || (a.Count == b.Count && a.RuleID <= b.RuleID) {
				break
			}
			filtered[j-1], filtered[j] = b, a
		}
	}
	m.rules = filtered
	if m.rulesCursor >= len(m.rules) {
		m.rulesCursor = 0
	}
}

// ---- View ----

func (m issuesModel) View(width, height int) string {
	if !m.loaded {
		return dimStyle.Render("loading…")
	}
	if m.err != nil {
		return errStyle.Render("error: " + m.err.Error())
	}
	// Header line: severity chips + status.
	header := m.renderHeader(width)
	if header != "" {
		header += "\n"
	}
	// Split remaining space between rules pane and list pane.
	remainingH := height - lipgloss.Height(header)
	if remainingH < 3 {
		remainingH = 3
	}
	rulesW := width * 2 / 5
	if rulesW < 24 {
		rulesW = 24
	}
	if rulesW > width-30 {
		rulesW = width - 30
	}
	listW := width - rulesW - 1

	rulesPane := m.renderRules(rulesW, remainingH)
	sep := ""
	if remainingH > 0 {
		sep = dimStyle.Render(strings.Repeat("│\n", remainingH-1) + "│")
	}
	listPane := m.renderIssueList(listW, remainingH)
	return header + lipgloss.JoinHorizontal(lipgloss.Top, rulesPane, sep, listPane)
}

func (m issuesModel) renderHeader(width int) string {
	chips := make([]string, 0, 4)
	for _, sev := range []string{"error", "warn", "info", "debug"} {
		chips = append(chips, m.renderSevChip(sev))
	}
	left := strings.Join(chips, " ")
	right := ""
	switch {
	case m.reindexing:
		right = dimStyle.Render("recomputing…")
	case m.statusMsg != "":
		right = dimStyle.Render(m.statusMsg)
	default:
		total := m.total
		right = dimStyle.Render(fmt.Sprintf(
			"%d issue%s · d: diagnostic · r: recompute · tab: switch pane · enter: filter/open",
			total, pluralIssues(total),
		))
	}
	// Compose: chips on the left, right-aligned status on the right.
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m issuesModel) renderSevChip(sev string) string {
	glyph, label := sevGlyphAndLabel(sev)
	body := fmt.Sprintf(" %s %s ", glyph, label)
	shortcut := sevChipShortcut(sev)
	style := lipgloss.NewStyle()
	if m.severities[sev] {
		style = severityChipStyleFor(sev).Reverse(true)
	} else {
		style = dimStyle
	}
	return style.Render(body) + dimStyle.Render(shortcut)
}

func (m issuesModel) renderRules(width, height int) string {
	var b strings.Builder
	title := fmt.Sprintf("Rules (%d)", len(m.rules))
	if m.focus == issuesFocusRules {
		b.WriteString(headerStyle.Render(title))
	} else {
		b.WriteString(dimStyle.Render(title))
	}
	b.WriteByte('\n')
	if len(m.rules) == 0 {
		b.WriteString(dimStyle.Render("(no issues match)"))
		return b.String()
	}
	viewH := height - 2
	if viewH <= 0 {
		viewH = len(m.rules)
	}
	start := 0
	if m.rulesCursor >= viewH {
		start = m.rulesCursor - viewH + 1
	}
	end := minInt(start+viewH, len(m.rules))
	for i := start; i < end; i++ {
		r := m.rules[i]
		label := r.RuleName
		if label == "" {
			label = r.RuleID
		}
		chip := severityChipStyle(r.Severity)
		counts := fmt.Sprintf("%d", r.Count)
		// Reserve room for chip + count + spacing.
		available := width - lipgloss.Width(chip) - len(counts) - 4
		if available < 8 {
			available = 8
		}
		if len([]rune(label)) > available {
			label = string([]rune(label)[:available-1]) + "…"
		}
		row := fmt.Sprintf(" %s %-*s %s", chip, available, label, counts)
		if i == m.rulesCursor && m.focus == issuesFocusRules {
			b.WriteString(lipgloss.NewStyle().Reverse(true).Render(row))
		} else {
			b.WriteString(row)
		}
		// Marker column: "*" prefix for the currently-selected filter.
		if m.selectedRule == r.RuleID && m.selectedTable == r.TableName {
			b.WriteString(dimStyle.Render(" ←"))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (m issuesModel) renderIssueList(width, height int) string {
	var b strings.Builder
	title := "All issues"
	if m.selectedRule != "" {
		for _, r := range m.rules {
			if r.RuleID == m.selectedRule && r.TableName == m.selectedTable {
				if r.RuleName != "" {
					title = r.RuleName
				} else {
					title = r.RuleID
				}
				break
			}
		}
	}
	from := 0
	if m.total > 0 {
		from = m.offset + 1
	}
	to := m.offset + len(m.issues)
	pos := ""
	if m.total > 0 {
		pos = fmt.Sprintf("  %d-%d of %d", from, to, m.total)
	}
	if m.focus == issuesFocusList {
		b.WriteString(headerStyle.Render(title) + dimStyle.Render(pos))
	} else {
		b.WriteString(dimStyle.Render(title + pos))
	}
	b.WriteByte('\n')
	if len(m.issues) == 0 {
		b.WriteString(dimStyle.Render("(no issues match)"))
		return b.String()
	}
	viewH := height - 2
	if viewH <= 0 {
		viewH = len(m.issues)
	}
	// Each issue row spans two lines (chip+label, then message), so
	// halve the viewport for the cursor window.
	rowsPerIssue := 2
	visible := maxInt(1, viewH/rowsPerIssue)
	start := 0
	if m.issuesCursor >= visible {
		start = m.issuesCursor - visible + 1
	}
	end := minInt(start+visible, len(m.issues))
	for i := start; i < end; i++ {
		issue := m.issues[i]
		chip := severityChipStyle(issue.Severity)
		label := issue.RecordLabel
		if label == "" {
			label = "(record " + shorten(issue.RecordID, 8) + "…)"
		}
		orphan := issue.LinkTaxonID == ""
		labelStyled := label
		if orphan {
			labelStyled = dimStyle.Render(label + "  (no owning taxon)")
		} else {
			labelStyled = headerStyle.Render(label)
		}
		// Truncate the visible label so row1 (indent + chip + label)
		// fits the pane width. Long taxon names would otherwise wrap
		// into the next line.
		row1Budget := width - lipgloss.Width(chip) - 2 // 1 leading space + 1 chip separator
		if row1Budget < 8 {
			row1Budget = 8
		}
		if len([]rune(label)) > row1Budget {
			label = string([]rune(label)[:row1Budget-1]) + "…"
			if orphan {
				labelStyled = dimStyle.Render(label + "  (no owning taxon)")
			} else {
				labelStyled = headerStyle.Render(label)
			}
		}
		row1 := fmt.Sprintf(" %s %s", chip, labelStyled)
		row2Rule := issue.RuleName
		if row2Rule == "" {
			row2Rule = issue.RuleID
		}
		if issue.FieldName != "" {
			row2Rule += " · " + issue.FieldName
		}
		row2Msg := issue.Message
		if row2Msg == "" {
			row2Msg = row2Rule
		}
		// The whole row2 (indent + rule label + ": " + message) must
		// stay under the pane width or the terminal wraps it and the
		// wrapped tail spills into whatever pane the horizontal-join
		// puts underneath. Compute the message budget by subtracting
		// the fixed prefix width from the pane width.
		row2Indent := "    "
		row2Prefix := row2Indent + row2Rule + ": "
		prefixLen := len([]rune(row2Prefix))
		maxTotal := width
		if maxTotal < 20 {
			maxTotal = 20
		}
		var row2 string
		if prefixLen >= maxTotal {
			// Even the prefix overflows — truncate the prefix itself
			// and drop the message; the row1 header already carries
			// the record label so the curator sees enough context.
			row2 = string([]rune(row2Prefix)[:maxTotal-1]) + "…"
		} else {
			availMsg := maxTotal - prefixLen
			if len([]rune(row2Msg)) > availMsg {
				row2Msg = string([]rune(row2Msg)[:availMsg-1]) + "…"
			}
			row2 = row2Indent + dimStyle.Render(row2Rule+": ") + row2Msg
		}
		if i == m.issuesCursor && m.focus == issuesFocusList {
			b.WriteString(lipgloss.NewStyle().Reverse(true).Render(row1))
			b.WriteByte('\n')
			b.WriteString(lipgloss.NewStyle().Reverse(true).Render(row2))
		} else {
			b.WriteString(row1)
			b.WriteByte('\n')
			b.WriteString(row2)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ---- helpers ----

func pluralIssues(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func sevGlyphAndLabel(sev string) (string, string) {
	switch strings.ToLower(sev) {
	case "error":
		return "✕", "error"
	case "info":
		return "ⓘ", "info"
	case "debug":
		return "🐛", "debug"
	default:
		return "⚠", "warn"
	}
}

func sevChipShortcut(sev string) string {
	switch sev {
	case "error":
		return " 1"
	case "warn":
		return " 2"
	case "info":
		return " 3"
	case "debug":
		return " 4"
	}
	return ""
}

// severityChipStyleFor returns the lipgloss style for the chip
// background when a severity toggle is active. Uses the same color
// per severity that severityChipStyle produces for the foreground.
func severityChipStyleFor(sev string) lipgloss.Style {
	switch strings.ToLower(sev) {
	case "error":
		return sevErrorStyle
	case "info":
		return sevInfoStyle
	case "debug":
		return sevDebugStyle
	default:
		return sevWarnStyle
	}
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
