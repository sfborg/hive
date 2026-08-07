package view

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sfborg/hive/core"
)

// treePageSize caps how many children we load per parent. Matches the
// WUI's default (`limit=200` on /api/taxon/{id}/children) so both
// frontends handle wide-fanout taxa (Cladocera, some spider families
// with cryptic species, undescribed-genus grab-bags) consistently.
// Above the limit, a sentinel row hints that "use / search" is the way
// to jump to a specific child.
const treePageSize = 200

// treeNode is a flattened row in the visible tree. The full tree is not
// materialized; only the currently-expanded slice is. `depth` drives
// indentation, `expanded` distinguishes ▶ from ▼, `hasChildren` decides
// whether the caret is drawn at all.
//
// The display parts (canonical / authorship / rank / extinct) are kept
// separate rather than pre-rendered so the row style (italic on canonical
// per rank, selection highlight, dim) can compose at draw time without
// mid-line SGR resets fighting each other.
//
// truncatedRemaining > 0 marks a *sentinel* row that stands in for the
// N unloaded siblings under a page-limited parent. Sentinels have empty
// id/canonical, are not returned by SelectedID, and cursor navigation
// skips over them. View renders them as a dim "⋯ N more" hint.
type treeNode struct {
	id                 string
	parentID           string
	nameID             string
	canonical          string
	authorship         string
	rank               string
	extinct            bool
	depth              int
	hasChildren        bool
	expanded           bool
	truncatedRemaining int
}

// isSentinel reports whether this row is a truncation marker rather than
// a real taxon. Selection, expand, and detail-load code skip these.
func (n treeNode) isSentinel() bool { return n.truncatedRemaining > 0 }

// treeModel is the left pane. It owns a slice of visible nodes, a cursor
// index, and the archive handle used to load children on expand. Loading
// is deferred until a node is expanded — the tree does not eagerly walk.
//
// pendingCount is a vim-style prefix accumulator: typing "1", "0", "0"
// leaves it at 100; the next g/G consumes it as "jump to Nth sibling."
// Any non-digit non-counted key resets it. 0 = no pending count.
type treeModel struct {
	a            *core.Archive
	nodes        []treeNode
	cursor       int
	width        int
	height       int
	focused      bool
	err          error
	pendingCount int
	// pendingDescend is the number of "l" steps still to walk after
	// an async child-page load. Bubble Tea's Update can't await mid-
	// call, so a count-prefixed descent (5l, 10l) that crosses
	// collapsed nodes has to survive across treeChildrenMsg cycles:
	// each async load's applyChildren consumes one step from this
	// counter and issues the next expandCurrent until we hit zero or
	// a leaf. Not touched by fully-loaded synchronous descent — those
	// consume their count inside a single Update.
	pendingDescend int
}

func newTreeModel(a *core.Archive) treeModel {
	return treeModel{a: a, focused: true}
}

// treeChildrenMsg carries children fetched from core in a background command.
// parentID == "" means the roots were loaded. offset > 0 means this is a
// "load more" fetch — the applier splices the new rows in ahead of the
// existing sentinel instead of after the parent. remaining is how many
// children are still unloaded after this batch (0 means fully loaded).
//
// cursorHint controls where the cursor lands after the splice. When
// cursorHintSet is false (zero-value default), the cursor moves to the
// first newly-loaded row — matches the auto-scroll UX. When set, the
// hint is a literal offset into the newly-loaded block: 0 = first, N =
// Nth (0-indexed), len-1 = last (used by G / NG).
type treeChildrenMsg struct {
	parentID      string
	depth         int
	offset        int
	remaining     int
	nodes         []treeNode
	cursorHint    int
	cursorHintSet bool
	// descendAfterLoad asks applyChildren to move the cursor to the
	// first newly-loaded child after a first-page expand. Set by
	// expandCurrent so the `l` / →/Enter binding lands one row into
	// the parent it just expanded — one press, one level down. No
	// effect on load-more (offset>0) or roots (parentID=="") paths.
	descendAfterLoad bool
	err              error
}

// loadChildren returns a tea.Cmd that fetches a page of children under
// parentID (offset=0 for the initial page, >0 to load more). Kept
// synchronous inside the cmd — SQLite is local, latency is in the
// hundreds of microseconds. The resulting msg.nodes are just the taxon
// rows; the applier is responsible for inserting a sentinel afterwards
// based on msg.remaining.
func (m treeModel) loadChildren(parentID string, depth, offset int) tea.Cmd {
	return m.loadChildrenOpts(parentID, depth, offset, false)
}

// loadChildrenOpts is the full-arg variant; loadChildren wraps it with
// descendAfterLoad=false for the paths that don't want cursor movement
// (roots, load-more). expandCurrent uses the descendAfterLoad=true form
// so `l` on a collapsed parent lands the cursor on the first child once
// the fetch returns — one press, one level down.
func (m treeModel) loadChildrenOpts(parentID string, depth, offset int, descendAfterLoad bool) tea.Cmd {
	return func() tea.Msg {
		hits, total, err := m.a.ListChildrenPage(context.Background(), parentID, treePageSize, offset)
		if err != nil {
			return treeChildrenMsg{parentID: parentID, depth: depth, offset: offset, err: err}
		}
		nodes := make([]treeNode, len(hits))
		for i, h := range hits {
			nodes[i] = hitToTreeNode(h, depth)
		}
		return treeChildrenMsg{
			parentID:         parentID,
			depth:            depth,
			offset:           offset,
			remaining:        total - (offset + len(hits)),
			nodes:            nodes,
			descendAfterLoad: descendAfterLoad,
		}
	}
}

// truncationSentinel builds a placeholder row that stands in for
// unloaded siblings under a page-limited parent. parentID identifies
// which parent's paging window this sentinel belongs to (needed for the
// load-more cmd). Cursor CAN land on sentinels — pressing Enter loads
// the next page. SelectedID returns "" for them so the detail pane
// isn't disturbed while paging.
func truncationSentinel(parentID string, depth, remaining int) treeNode {
	return treeNode{
		parentID:           parentID,
		depth:              depth,
		truncatedRemaining: remaining,
	}
}

// hitToTreeNode maps a TaxonHit onto the tree row shape, keeping the
// display parts separate so View can compose italic + selection styles
// at draw time without mid-line SGR collisions.
func hitToTreeNode(h core.TaxonHit, depth int) treeNode {
	return treeNode{
		id:          h.ID,
		parentID:    h.ParentID,
		nameID:      h.NameID,
		canonical:   h.Name,
		authorship:  h.Authorship,
		rank:        h.Rank,
		extinct:     h.Extinct.Valid && h.Extinct.Bool,
		depth:       depth,
		hasChildren: h.HasChildren,
	}
}

// Init loads the roots.
func (m treeModel) Init() tea.Cmd {
	return m.loadChildren("", 0, 0)
}

// treeRevealedMsg carries a fully-expanded node list plus the target ID
// the cursor should land on. Produced by RevealCmd after a taxon move so
// the tree pane reflects the new location without a cascade of expand
// commands.
type treeRevealedMsg struct {
	nodes    []treeNode
	targetID string
	err      error
}

// RevealCmd computes a node list containing every ancestor of id (root
// down, siblings inclusive) with each ancestor marked expanded. The
// resulting slice replaces the whole visible tree in one atomic apply.
// One goroutine, one message — no expand cascade across events.
//
// Sibling loads at each level are capped at treePageSize; a sentinel is
// appended per level when the parent has more children than we loaded.
// The target itself is always guaranteed to be in the loaded window
// (search-and-reveal skips the paging problem for jumping to a leaf).
func (m treeModel) RevealCmd(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	a := m.a
	return func() tea.Msg {
		ctx := context.Background()
		chain, err := a.Ancestors(ctx, id)
		if err != nil {
			return treeRevealedMsg{targetID: id, err: err}
		}
		// expandedSet: ancestor IDs whose children should be inserted
		// nested below them (rendered as ▼ rather than ▶).
		expandedSet := make(map[string]bool, len(chain))
		for _, aID := range chain {
			expandedSet[aID] = true
		}
		convert := func(hits []core.TaxonHit, parentID string, depth, remaining int) []treeNode {
			out := make([]treeNode, 0, len(hits)+1)
			for _, h := range hits {
				n := hitToTreeNode(h, depth)
				n.expanded = expandedSet[h.ID]
				out = append(out, n)
			}
			if remaining > 0 {
				out = append(out, truncationSentinel(parentID, depth, remaining))
			}
			return out
		}
		// Roots first.
		hits, total, err := a.ListChildrenPage(ctx, "", treePageSize, 0)
		if err != nil {
			return treeRevealedMsg{targetID: id, err: err}
		}
		nodes := convert(hits, "", 0, total-len(hits))
		// For each ancestor in root→leaf order, insert its children
		// (siblings-inclusive) right after the ancestor's position in
		// the growing slice. That keeps every visible level properly
		// nested under its parent.
		for depth, ancestorID := range chain {
			pos := -1
			for i, n := range nodes {
				if n.id == ancestorID {
					pos = i
					break
				}
			}
			if pos < 0 {
				continue
			}
			hits, total, err := a.ListChildrenPage(ctx, ancestorID, treePageSize, 0)
			if err != nil {
				return treeRevealedMsg{targetID: id, err: err}
			}
			children := convert(hits, ancestorID, depth+1, total-len(hits))
			// Insert children after pos.
			nodes = append(nodes[:pos+1], append(children, nodes[pos+1:]...)...)
		}
		return treeRevealedMsg{nodes: nodes, targetID: id}
	}
}

func (m treeModel) Update(msg tea.Msg, keys keyMap) (treeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case treeChildrenMsg:
		m = m.applyChildren(msg)
		// Chain the vim-count descent through async loads: after each
		// child-page load commits and applyChildren has moved the
		// cursor to the first newly-loaded row, kick off the next
		// expand tick if there's remaining descent budget. Guarded on
		// descendAfterLoad so ordinary loads (roots, sentinel expand)
		// don't accidentally start walking down the tree.
		if msg.descendAfterLoad && m.pendingDescend > 0 {
			return m.descendSteps(m.pendingDescend)
		}
		return m, nil
	case treeRevealedMsg:
		return m.applyReveal(msg), nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		// Vim-style count prefix: single digits 0-9 accumulate into
		// pendingCount, consumed by the next g/G. Leading zero is
		// treated as a normal digit — "00" and "0" both mean "no
		// count" for hive's purposes since sibling indices are 1-based.
		if isDigit(msg.String()) {
			d := int(msg.String()[0] - '0')
			m.pendingCount = m.pendingCount*10 + d
			return m, nil
		}
		// All motion / expand / collapse actions consume the pending
		// count as a repeat multiplier (10j → down ten, 10l → descend
		// ten levels via the leftmost path). g/G still consume it as
		// a group-index target inside jumpInSiblings. Cap at 10000 as
		// a paranoia guard against a runaway Nj.
		count := m.pendingCount
		if count < 1 {
			count = 1
		}
		if count > 10000 {
			count = 10000
		}
		switch {
		case key.Matches(msg, keys.Up):
			m.pendingCount = 0
			for i := 0; i < count; i++ {
				if m.cursor <= 0 {
					break
				}
				m.cursor--
			}
			return m, m.autoLoadCmd()
		case key.Matches(msg, keys.Down):
			m.pendingCount = 0
			for i := 0; i < count; i++ {
				if m.cursor >= len(m.nodes)-1 {
					break
				}
				m.cursor++
			}
			return m, m.autoLoadCmd()
		case key.Matches(msg, keys.Expand):
			// descendSteps walks the count synchronously, and — when
			// it hits a collapsed node — issues the async child load
			// and stashes the remaining count on the model so
			// applyChildren can chain the next step once the load
			// commits. This makes 10l descend 10 levels even when
			// crossing not-yet-loaded child pages, matching what the
			// WUI does with await.
			m.pendingCount = 0
			return m.descendSteps(count)
		case key.Matches(msg, keys.Collapse):
			m.pendingCount = 0
			for i := 0; i < count; i++ {
				m = m.collapseCurrent()
			}
			return m, nil
		case key.Matches(msg, keys.Top):
			return m.jumpInSiblings(true)
		case key.Matches(msg, keys.LoadAll):
			return m.jumpInSiblings(false)
		}
		// Any other key resets the pending count so it doesn't linger
		// across unrelated actions.
		m.pendingCount = 0
	}
	return m, nil
}

// isDigit reports whether s is a single ASCII digit character. Used to
// gate the vim-style count-prefix accumulator.
func isDigit(s string) bool {
	return len(s) == 1 && s[0] >= '0' && s[0] <= '9'
}

// jumpInSiblings handles vim-style g / G / Ng / NG:
//   - g          → first sibling of the current group
//   - G          → last sibling (loads all if truncated)
//   - Ng / NG    → Nth sibling (1-indexed); loads more if needed
//
// The two motions are symmetric on hive because the sibling group is
// always a contiguous run at a single depth — "top" and "bottom" both
// mean group-local extremes, not file-wide. `top` is true for g, false
// for G; only matters when there's no pending count.
func (m treeModel) jumpInSiblings(top bool) (treeModel, tea.Cmd) {
	count := m.pendingCount
	m.pendingCount = 0
	if len(m.nodes) == 0 {
		return m, nil
	}
	start, end, ok := m.siblingRange(m.cursor)
	if !ok {
		return m, nil
	}
	loaded := end - start
	parent := m.nodes[start].parentID
	depth := m.nodes[start].depth
	sentinelIdx := m.sentinelIndex(parent, depth)
	truncated := sentinelIdx >= 0

	// Decide target position (1-based within the group). -1 means "last."
	var targetIdx int
	switch {
	case count > 0:
		targetIdx = count
	case top:
		targetIdx = 1
	default:
		targetIdx = -1 // G with no count → last
	}

	// Fast paths — target is inside the currently-loaded window:
	//   * top / small N always is
	//   * "last" is when the group isn't truncated
	if targetIdx == 1 {
		m.cursor = start
		return m, nil
	}
	if targetIdx > 0 && targetIdx <= loaded {
		m.cursor = start + targetIdx - 1
		return m, nil
	}
	if targetIdx == -1 && !truncated {
		m.cursor = end - 1
		return m, nil
	}

	// Otherwise we need the whole group. Move cursor onto the sentinel
	// first so loadAllRemainingCmd's sentinelForCursor picks the right
	// paging window.
	m.cursor = sentinelIdx
	after := -1
	if targetIdx > 0 {
		after = targetIdx - 1
	}
	return m, m.loadAllRemainingCmd(after)
}

// siblingRange returns [start, end) — the slice of m.nodes forming the
// contiguous sibling group that contains position idx (same depth and
// parentID). The sentinel, if any, immediately follows end. Returns
// ok=false when idx is out of range.
func (m treeModel) siblingRange(idx int) (int, int, bool) {
	if idx < 0 || idx >= len(m.nodes) {
		return 0, 0, false
	}
	cur := m.nodes[idx]
	// A sentinel's group is the run of siblings above it at the same
	// depth+parent. Treat the sentinel-cursor case by pointing at the
	// last real sibling just above.
	if cur.isSentinel() {
		if idx == 0 {
			return 0, 0, false
		}
		cur = m.nodes[idx-1]
		idx--
	}
	depth := cur.depth
	parent := cur.parentID
	start := idx
	for start > 0 {
		p := m.nodes[start-1]
		if p.depth != depth || p.parentID != parent || p.isSentinel() {
			break
		}
		start--
	}
	end := idx + 1
	for end < len(m.nodes) {
		n := m.nodes[end]
		if n.isSentinel() && n.depth == depth && n.parentID == parent {
			break
		}
		if n.depth != depth || n.parentID != parent {
			break
		}
		end++
	}
	return start, end, true
}

// loadAllRemainingCmd returns a fetch that pulls every remaining sibling
// under the parent whose window the cursor is currently inside (or on).
// Bypasses the pagination cap by passing limit=0 to core, so a `G` on a
// 10k-child parent completes in one round-trip rather than many.
//
// cursorAfterLoad selects where the cursor lands within the *entire*
// sibling group after the fetch — a value < 0 means "last row" (the G
// default); a value >= 0 is a literal 0-indexed offset into the group
// (used by NG to jump to sibling N). If the target already lies within
// the currently-loaded siblings, the caller shouldn't call this — a
// pure cursor move suffices.
func (m treeModel) loadAllRemainingCmd(cursorAfterLoad int) tea.Cmd {
	sentinel, ok := m.sentinelForCursor()
	if !ok {
		return nil
	}
	parentID := sentinel.parentID
	depth := sentinel.depth
	offset := m.siblingsBefore(m.sentinelIndex(parentID, depth))
	a := m.a
	return func() tea.Msg {
		// limit=0 → no LIMIT clause in core.ListChildrenPage. Uncapped
		// is fine for local SQLite; the user explicitly asked for the
		// whole group with g/G/NG.
		hits, total, err := a.ListChildrenPage(context.Background(), parentID, 0, offset)
		if err != nil {
			return treeChildrenMsg{parentID: parentID, depth: depth, offset: offset, err: err}
		}
		nodes := make([]treeNode, len(hits))
		for i, h := range hits {
			nodes[i] = hitToTreeNode(h, depth)
		}
		// Translate a group-wide index into an offset within the
		// newly-loaded block: subtract the number of already-loaded
		// rows. Clamp at the extremes.
		hint := len(nodes) - 1 // default: last row (G)
		if cursorAfterLoad >= 0 {
			hint = cursorAfterLoad - offset
			if hint < 0 {
				hint = 0
			}
		}
		return treeChildrenMsg{
			parentID:      parentID,
			depth:         depth,
			offset:        offset,
			remaining:     total - (offset + len(hits)),
			nodes:         nodes,
			cursorHint:    hint,
			cursorHintSet: true,
		}
	}
}

// sentinelForCursor finds the sentinel that belongs to the sibling group
// the cursor is currently in — either the cursor is on the sentinel
// itself, or the cursor is on a sibling with a sentinel below at the
// same depth+parent.
func (m treeModel) sentinelForCursor() (treeNode, bool) {
	if len(m.nodes) == 0 {
		return treeNode{}, false
	}
	cur := m.nodes[m.cursor]
	if cur.isSentinel() {
		return cur, true
	}
	// Search downward for a sentinel at the same depth + parentID.
	for i := m.cursor + 1; i < len(m.nodes); i++ {
		n := m.nodes[i]
		if n.depth < cur.depth {
			break
		}
		if n.isSentinel() && n.depth == cur.depth && n.parentID == cur.parentID {
			return n, true
		}
	}
	return treeNode{}, false
}

// sentinelIndex returns the position of the sentinel matching parentID
// and depth, or -1. Used to compute offset for load-all.
func (m treeModel) sentinelIndex(parentID string, depth int) int {
	for i, n := range m.nodes {
		if n.isSentinel() && n.parentID == parentID && n.depth == depth {
			return i
		}
	}
	return -1
}

// autoLoadCmd fires a load-more fetch when the cursor lands on a
// truncation sentinel. Local SQLite is fast enough that a fetch per
// nav-onto-sentinel is a blip, and there's at most one sentinel per
// parent's window — so no runaway loop is possible. Returns nil when
// the cursor is on a regular row.
func (m treeModel) autoLoadCmd() tea.Cmd {
	if len(m.nodes) == 0 {
		return nil
	}
	cur := m.nodes[m.cursor]
	if !cur.isSentinel() {
		return nil
	}
	return m.loadChildren(cur.parentID, cur.depth, m.siblingsBefore(m.cursor))
}

// applyChildren inserts a fetched child slice at the right place. Three
// shapes:
//   - offset==0 and parentID=="": initial roots load; replace the whole
//     visible slice (plus a sentinel when the roots list is truncated).
//   - offset==0 and parentID!="": first-page expand of a real parent;
//     insert after the parent, append a sentinel if truncated.
//   - offset>0: "load more" driven by expanding a sentinel; splice the
//     new rows in *before* the existing sentinel and refresh its
//     remaining count (or remove it when we've fully loaded).
func (m treeModel) applyChildren(msg treeChildrenMsg) treeModel {
	if msg.err != nil {
		m.err = msg.err
		return m
	}

	// "Load more" path: find the sentinel for this parent + depth and
	// splice new nodes in before it.
	if msg.offset > 0 {
		for i, n := range m.nodes {
			if !n.isSentinel() || n.parentID != msg.parentID || n.depth != msg.depth {
				continue
			}
			suffix := append([]treeNode{}, m.nodes[i:]...) // includes the old sentinel
			m.nodes = append(m.nodes[:i], msg.nodes...)
			// Rebuild sentinel or drop it if fully loaded now.
			if msg.remaining > 0 {
				m.nodes = append(m.nodes, truncationSentinel(msg.parentID, msg.depth, msg.remaining))
			}
			m.nodes = append(m.nodes, suffix[1:]...) // skip the old sentinel
			// Cursor placement: default = first newly-loaded row (so
			// scroll-and-load feels continuous). cursorHintSet with
			// cursorHint = k lands on the kth newly-loaded row (used
			// by g/G/NG for vim-style jumps).
			if len(msg.nodes) > 0 {
				target := i
				if msg.cursorHintSet {
					k := msg.cursorHint
					if k < 0 {
						k = 0
					}
					if k >= len(msg.nodes) {
						k = len(msg.nodes) - 1
					}
					target = i + k
				}
				m.cursor = target
			}
			return m
		}
		return m // sentinel gone (concurrent collapse) — silently drop
	}

	// Roots load.
	if msg.parentID == "" {
		m.nodes = append([]treeNode{}, msg.nodes...)
		if msg.remaining > 0 {
			m.nodes = append(m.nodes, truncationSentinel("", msg.depth, msg.remaining))
		}
		return m
	}

	// First-page expand of a real parent.
	for i, n := range m.nodes {
		if n.id != msg.parentID {
			continue
		}
		m.nodes[i].expanded = true
		suffix := append([]treeNode{}, m.nodes[i+1:]...)
		inserted := append([]treeNode{}, msg.nodes...)
		if msg.remaining > 0 {
			inserted = append(inserted, truncationSentinel(msg.parentID, msg.depth, msg.remaining))
		}
		m.nodes = append(m.nodes[:i+1], inserted...)
		m.nodes = append(m.nodes, suffix...)
		// Descend-on-load: expandCurrent asked us to land on the
		// first newly-loaded child so `l` on a collapsed parent
		// completes as one press = one level down.
		if msg.descendAfterLoad && len(msg.nodes) > 0 {
			m.cursor = i + 1
		}
		return m
	}
	return m
}

// applyReveal replaces the whole visible tree with a fresh, ancestor-
// expanded slice and moves the cursor to the target row.
func (m treeModel) applyReveal(msg treeRevealedMsg) treeModel {
	if msg.err != nil {
		m.err = msg.err
		return m
	}
	m.nodes = msg.nodes
	for i, n := range m.nodes {
		if n.id == msg.targetID {
			m.cursor = i
			break
		}
	}
	return m
}

// descendSteps walks `count` levels down the leftmost path,
// consuming pending descent tickets. Runs synchronously through
// already-expanded chains; when it hits a collapsed node it stores
// the remaining count on pendingDescend and returns the async
// child-load cmd. applyChildren then calls back into descendSteps
// after the load commits (see the treeChildrenMsg case in Update),
// chaining the descent across as many async loads as needed.
//
// Also stops early on a leaf (or any position where expandCurrent
// can't advance the cursor) so a stray 100l doesn't keep chasing
// no-op expands.
func (m treeModel) descendSteps(count int) (treeModel, tea.Cmd) {
	for count > 0 {
		before := m.cursor
		var cmd tea.Cmd
		m, cmd = m.expandCurrent()
		if cmd != nil {
			// Async load — save remainder for the next chain link.
			// applyChildren will decrement one more once the load
			// commits and calls back here.
			m.pendingDescend = count - 1
			return m, cmd
		}
		if m.cursor == before {
			// Nothing left to descend into (leaf, empty child list,
			// or a broken invariant). Bail cleanly.
			m.pendingDescend = 0
			return m, nil
		}
		count--
	}
	m.pendingDescend = 0
	return m, nil
}

// expandCurrent implements the → / l / Enter contract, matching the
// WUI's _expandOrEnter:
//   - Sentinel row     → load more (cursor stays on the sentinel).
//   - Leaf             → no-op.
//   - Collapsed parent → load children AND move cursor to first child
//                        once the load returns (async, via applyChildren
//                        honoring descendAfterLoad).
//   - Expanded parent  → move cursor to first child immediately.
//
// Folding expand + descend into one press means `l l l l` walks the
// leftmost path efficiently and Nl descends N levels in one action.
func (m treeModel) expandCurrent() (treeModel, tea.Cmd) {
	if len(m.nodes) == 0 {
		return m, nil
	}
	cur := &m.nodes[m.cursor]
	// Sentinel row → load the next page for the parent whose window
	// this sentinel represents. Offset is however many siblings we've
	// already loaded at this depth immediately above the sentinel.
	if cur.isSentinel() {
		offset := m.siblingsBefore(m.cursor)
		return m, m.loadChildren(cur.parentID, cur.depth, offset)
	}
	if !cur.hasChildren {
		return m, nil
	}
	if cur.expanded {
		// Already loaded — first child is the next row, unless the
		// children were collapsed underneath (impossible; collapse
		// removes them from the flat list). Move cursor to it.
		if m.cursor+1 < len(m.nodes) {
			child := m.nodes[m.cursor+1]
			if !child.isSentinel() && child.depth == cur.depth+1 {
				m.cursor = m.cursor + 1
			}
		}
		return m, nil
	}
	// Collapsed — trigger a load and ask applyChildren to move the
	// cursor to the first newly-loaded row when the fetch returns.
	return m, m.loadChildrenOpts(cur.id, cur.depth+1, 0, true)
}

// siblingsBefore counts the taxa at nodes[idx].depth that appear
// immediately before nodes[idx] and share the same parentID. Used to
// compute the offset for a load-more fetch — the number of already-
// loaded rows above the sentinel.
func (m treeModel) siblingsBefore(idx int) int {
	if idx <= 0 {
		return 0
	}
	depth := m.nodes[idx].depth
	parent := m.nodes[idx].parentID
	count := 0
	for i := idx - 1; i >= 0; i-- {
		n := m.nodes[i]
		if n.depth < depth {
			break
		}
		if n.depth == depth && n.parentID == parent && !n.isSentinel() {
			count++
		}
	}
	return count
}

// collapseCurrent implements the ← / h contract, mirror-symmetric
// with expandCurrent:
//   - Expanded node → collapse AND move cursor to parent in one press.
//   - Leaf / collapsed / root → move cursor to parent (or no-op at depth 0).
//
// Folding collapse + ascend into one press means `h h h h` walks the
// ancestor chain efficiently and Nh ascends N levels in one action —
// matching the semantics of the new `l`. "Collapse in place without
// moving cursor" isn't available; add a distinct binding (z / -) if
// it becomes wanted.
func (m treeModel) collapseCurrent() treeModel {
	if len(m.nodes) == 0 {
		return m
	}
	cur := &m.nodes[m.cursor]
	// Collapse first when expanded, so descendants disappear before
	// we walk up looking for the parent row.
	if cur.expanded {
		end := m.cursor + 1
		for end < len(m.nodes) && m.nodes[end].depth > cur.depth {
			end++
		}
		m.nodes = append(m.nodes[:m.cursor+1], m.nodes[end:]...)
		cur.expanded = false
	}
	// Ascend one level.
	targetDepth := cur.depth - 1
	if targetDepth < 0 {
		return m
	}
	for i := m.cursor - 1; i >= 0; i-- {
		if m.nodes[i].depth == targetDepth {
			m.cursor = i
			return m
		}
	}
	return m
}

// SelectedID returns the ID of the currently highlighted taxon, or "" if
// the tree is empty or the cursor sits on a truncation sentinel. Callers
// that update side-panes on selection change should treat "" as "keep
// showing what was there" — the shell does this so a cursor pass over a
// sentinel doesn't blank the detail pane.
func (m treeModel) SelectedID() string {
	if len(m.nodes) == 0 {
		return ""
	}
	n := m.nodes[m.cursor]
	if n.isSentinel() {
		return ""
	}
	return n.id
}

// SelectedLabel returns a plain-text display for the highlighted taxon —
// canonical + authorship, with a dagger for extinct taxa. Used by the
// create-form header ("New taxon under …") so the curator sees where a
// new child will land. No SGR codes; the caller styles as needed.
// Returns "" for sentinels.
func (m treeModel) SelectedLabel() string {
	if len(m.nodes) == 0 {
		return ""
	}
	n := m.nodes[m.cursor]
	if n.isSentinel() {
		return ""
	}
	label := n.canonical
	if n.authorship != "" {
		label = label + " " + n.authorship
	}
	if n.extinct {
		label = "† " + label
	}
	return label
}

// View renders the tree pane. Very small styling for the walking skeleton;
// theme.go can grow later.
func (m treeModel) View() string {
	if m.err != nil {
		return errStyle.Render("error: " + m.err.Error())
	}
	if len(m.nodes) == 0 {
		return dimStyle.Render("(archive has no taxa)")
	}
	var b strings.Builder
	// Clamp visible window to height. Simple scroll: keep cursor in view.
	viewH := m.height
	if viewH <= 0 {
		viewH = len(m.nodes)
	}
	start := 0
	if m.cursor >= viewH {
		start = m.cursor - viewH + 1
	}
	end := min(start+viewH, len(m.nodes))
	for i := start; i < end; i++ {
		n := m.nodes[i]

		// Choose the outer style for this row (selection/dim/default).
		outer := lipgloss.NewStyle()
		switch {
		case i == m.cursor && m.focused:
			outer = outer.Reverse(true)
		case i == m.cursor:
			outer = outer.Faint(true).Reverse(true)
		}

		// Sentinel: dim "⋯ N more" — load fires automatically when the
		// cursor navigates onto this row, so no hint text is needed.
		if n.isSentinel() {
			indent := strings.Repeat("  ", n.depth)
			msg := fmt.Sprintf("%s⋯ %d more", indent, n.truncatedRemaining)
			style := outer.Faint(true)
			b.WriteString(style.Render(msg))
			b.WriteByte('\n')
			continue
		}

		caret := " "
		if n.hasChildren {
			if n.expanded {
				caret = "▼"
			} else {
				caret = "▶"
			}
		}
		indent := strings.Repeat("  ", n.depth)
		prefix := fmt.Sprintf("%s%s ", indent, caret)

		dagger := ""
		if n.extinct {
			dagger = "† "
		}
		canonStyle := outer
		if core.ItalicForRank(n.rank) && n.canonical != "" {
			canonStyle = outer.Italic(true)
		}
		// Render each segment with its own composed style — no nested
		// lipgloss.Render, so the outer effects (Reverse/Faint) survive
		// intact across the italic boundary.
		b.WriteString(outer.Render(prefix + dagger))
		b.WriteString(canonStyle.Render(n.canonical))
		if n.authorship != "" {
			b.WriteString(outer.Render(" " + n.authorship))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// styles kept in this file for the walking skeleton; a theme.go can absorb
// them once palette variations arrive. Selection/reverse styling is built
// inline in View — it needs to merge with per-name italic modifiers, so
// keeping it as a static var would just mean pulling it apart again.
var (
	dimStyle = lipgloss.NewStyle().Faint(true)
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)
