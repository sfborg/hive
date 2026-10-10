package hive

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// TaxonTreeQuery selects part of one verifier source's classification
// of the names mentioned in indexed papers.
type TaxonTreeQuery struct {
	SourceID int
	// MinMentions counts a paper toward a taxon only when it mentions
	// one of the taxon's names at least this often.
	MinMentions int
	// Path is the classification path ("|Eukaryota|Sar|") whose
	// descendants are returned; "" or "|" is the top. Ignored when Q is
	// set.
	Path string
	// Depth is how many branching levels below Path to return (default
	// 1). A node with a single child does not branch: its child is
	// returned with it, at no cost to the depth.
	Depth int
	// Q, when set, returns the taxa whose names start with it
	// (case-insensitive), with all their ancestors.
	Q string
	// Limit caps the number of Q matches (default 30).
	Limit int
}

// TaxonTreeNode is one taxon in a [TaxonTreeQuery] result.
type TaxonTreeNode struct {
	// Path is the node's classification path, ending with its own name.
	Path string
	Name string
	// Rank is the node's rank where the source ranks it, or "".
	Rank string
	// Papers is the number of indexed papers mentioning names in the
	// taxon.
	Papers int
	// Children is the number of child taxa in the full tree.
	Children int
	// Match marks the nodes that matched Q.
	Match bool
}

type taxonTreeNode struct {
	name     string
	rank     string
	papers   map[string]struct{}
	children map[string]*taxonTreeNode
}

// TaxonTree returns part of the classification of mentioned names, as
// a flat list of nodes in depth-first order, children by descending
// paper count. It is discovery data and never feeds project tables.
func (a *Archive) TaxonTree(ctx context.Context, q TaxonTreeQuery) ([]TaxonTreeNode, error) {
	root, err := a.loadTaxonTree(ctx, q.SourceID, max(q.MinMentions, 1))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(q.Q) != "" {
		return searchTaxonTree(root, strings.TrimSpace(q.Q), q.Limit), nil
	}
	node := root
	path := "|"
	for _, name := range strings.Split(strings.Trim(q.Path, "|"), "|") {
		if name == "" {
			continue
		}
		next, ok := node.children[name]
		if !ok {
			return nil, nil
		}
		node = next
		path += name + "|"
	}
	var out []TaxonTreeNode
	walkTaxonTree(node, path, max(q.Depth, 1), &out)
	return out, nil
}

func (a *Archive) loadTaxonTree(ctx context.Context, sourceID, minMentions int) (*taxonTreeNode, error) {
	cols := make([]string, len(colTaxonRanks))
	for i, r := range colTaxonRanks {
		cols[i] = "COALESCE(t." + rankColumns[r] + ", '')"
	}
	rows, err := a.db.QueryContext(ctx, `SELECT m.reference_id, t.raw__path, `+strings.Join(cols, ", ")+`
		FROM hive__name_mention m
		JOIN raw__taxon t ON t.raw__source = m.raw__source AND t.col__id = m.raw_taxon_id
		WHERE m.raw__source LIKE ? AND m.mention_count >= ? AND t.raw__path != ''
			AND `+discoveryMatch("m."),
		mentions.TagPrefix(sourceID)+"%", minMentions)
	if err != nil {
		return nil, fmt.Errorf("core: taxon tree: %w", err)
	}
	defer rows.Close()
	root := &taxonTreeNode{children: map[string]*taxonTreeNode{}}
	ranked := make([]string, len(colTaxonRanks))
	dest := make([]any, 2+len(ranked))
	var refID, path string
	dest[0], dest[1] = &refID, &path
	for i := range ranked {
		dest[2+i] = &ranked[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		rankOf := make(map[string]string, len(ranked))
		for i, n := range ranked {
			if n != "" {
				rankOf[n] = colTaxonRanks[i]
			}
		}
		node := root
		for _, name := range strings.Split(strings.Trim(path, "|"), "|") {
			if name == "" {
				continue
			}
			child, ok := node.children[name]
			if !ok {
				child = &taxonTreeNode{name: name, papers: map[string]struct{}{},
					children: map[string]*taxonTreeNode{}}
				node.children[name] = child
			}
			if child.rank == "" {
				child.rank = rankOf[name]
			}
			child.papers[refID] = struct{}{}
			node = child
		}
	}
	return root, rows.Err()
}

// sortedChildren returns n's children by descending paper count, then
// name.
func (n *taxonTreeNode) sortedChildren() []*taxonTreeNode {
	out := make([]*taxonTreeNode, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].papers) != len(out[j].papers) {
			return len(out[i].papers) > len(out[j].papers)
		}
		return out[i].name < out[j].name
	})
	return out
}

func (n *taxonTreeNode) node(path string) TaxonTreeNode {
	return TaxonTreeNode{Path: path, Name: n.name, Rank: n.rank,
		Papers: len(n.papers), Children: len(n.children)}
}

// walkTaxonTree appends the descendants of n (whose path is path) down
// to depth branching levels.
func walkTaxonTree(n *taxonTreeNode, path string, depth int, out *[]TaxonTreeNode) {
	if depth <= 0 {
		return
	}
	for _, c := range n.sortedChildren() {
		p := path + c.name + "|"
		*out = append(*out, c.node(p))
		d := depth - 1
		if len(c.children) == 1 {
			d = depth
		}
		walkTaxonTree(c, p, d, out)
	}
}

// searchTaxonTree returns the nodes whose names start with q, most
// papers first up to limit, together with their ancestors, in
// depth-first order.
func searchTaxonTree(root *taxonTreeNode, q string, limit int) []TaxonTreeNode {
	if limit <= 0 {
		limit = 30
	}
	q = strings.ToLower(q)
	type hit struct {
		path []*taxonTreeNode
	}
	var hits []hit
	var visit func(n *taxonTreeNode, anc []*taxonTreeNode)
	visit = func(n *taxonTreeNode, anc []*taxonTreeNode) {
		for _, c := range n.children {
			p := append(anc[:len(anc):len(anc)], c)
			if strings.HasPrefix(strings.ToLower(c.name), q) {
				hits = append(hits, hit{p})
			}
			visit(c, p)
		}
	}
	visit(root, nil)
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i].path[len(hits[i].path)-1], hits[j].path[len(hits[j].path)-1]
		if len(a.papers) != len(b.papers) {
			return len(a.papers) > len(b.papers)
		}
		return a.name < b.name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	// Keep the matches and their ancestors, then emit them depth-first.
	keep := map[*taxonTreeNode]bool{}
	match := map[*taxonTreeNode]bool{}
	for _, h := range hits {
		for _, n := range h.path {
			keep[n] = true
		}
		match[h.path[len(h.path)-1]] = true
	}
	var out []TaxonTreeNode
	var emit func(n *taxonTreeNode, path string)
	emit = func(n *taxonTreeNode, path string) {
		for _, c := range n.sortedChildren() {
			if !keep[c] {
				continue
			}
			p := path + c.name + "|"
			tn := c.node(p)
			tn.Match = match[c]
			out = append(out, tn)
			emit(c, p)
		}
	}
	emit(root, "|")
	return out
}
