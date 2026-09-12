package hive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AssemblerNode is one row of the assimilate-view feed: a Layer 2
// taxon plus per-node color state derived from Layer 3 overlays
// and any hive__assembler_flag rows anchored to it.
//
// Emitted by [Archive.ListAssemblerNodes]. Consumed by the WUI's
// assimilate view (Slice 2) to render the four-color tree.
type AssemblerNode struct {
	TaxonID       string
	NameID        string
	ParentTaxonID string
	Canonical     string
	Authorship    string
	Rank          string
	Status        string

	// Color is the highest-priority visual state derived from
	// Overlays + Flags. Values match the vocabulary from
	// docs/2026-09-12_LITERATURE_VIEW.md § Color-coded node state:
	// "green", "amber", "red", "blue". Priority: blue > red > amber
	// > green.
	Color string

	// HasOverlay is true when at least one active Layer 3 overlay
	// anchors to this taxon. Used by the UI to show the override
	// affordance and (via Color) to paint the node blue.
	HasOverlay bool

	// Flags is every hive__assembler_flag row anchored to this
	// taxon in the most recent reassemble run.
	Flags []AssemblerNodeFlag
}

// AssemblerNodeFlag is the per-node projection of one
// hive__assembler_flag row.
type AssemblerNodeFlag struct {
	Kind        string
	Message     string
	ReferenceID string
	MentionID   string
	Candidates  []string
}

// ListAssemblerNodes returns every Layer 2 taxon (excluding
// soft-deleted) projected into [AssemblerNode] shape, with color
// state pre-computed and flags attached.
//
// The reader does one taxon-side JOIN plus one hive__assembler_flag
// scan plus one hive__layer3_overlay scan, then folds the results
// per taxon id. All work happens in a single connection with no
// N+1 queries.
func (a *Archive) ListAssemblerNodes(ctx context.Context) ([]AssemblerNode, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT
		    t.col__id, t.col__name_id, COALESCE(t.col__parent_id, ''),
		    COALESCE(n.gn__canonical_full, ''),
		    COALESCE(n.col__authorship, ''),
		    COALESCE(n.col__rank_id, ''),
		    COALESCE(t.col__status_id, '')
		FROM taxon t
		LEFT JOIN name n ON n.col__id = t.col__name_id
		ORDER BY t.col__id ASC`)
	if err != nil {
		return nil, fmt.Errorf("core: assembler nodes select: %w", err)
	}
	nodesByID := make(map[string]*AssemblerNode)
	var order []string
	for rows.Next() {
		var n AssemblerNode
		if err := rows.Scan(
			&n.TaxonID, &n.NameID, &n.ParentTaxonID,
			&n.Canonical, &n.Authorship, &n.Rank, &n.Status,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("core: assembler nodes scan: %w", err)
		}
		nodesByID[n.TaxonID] = &n
		order = append(order, n.TaxonID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fold in flags per taxon.
	if err := a.loadFlagsForNodes(ctx, nodesByID); err != nil {
		return nil, err
	}
	// Fold in overlay presence per taxon.
	if err := a.loadOverlayPresenceForNodes(ctx, nodesByID); err != nil {
		return nil, err
	}

	out := make([]AssemblerNode, 0, len(order))
	for _, id := range order {
		n := nodesByID[id]
		n.Color = deriveNodeColor(n.HasOverlay, n.Flags)
		out = append(out, *n)
	}
	return out, nil
}

func (a *Archive) loadFlagsForNodes(ctx context.Context, nodesByID map[string]*AssemblerNode) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT kind, message,
		       COALESCE(reference_id, ''),
		       COALESCE(mention_id, ''),
		       COALESCE(taxon_id, ''),
		       candidates_json
		FROM hive__assembler_flag
		WHERE taxon_id IS NOT NULL AND taxon_id != ''`)
	if err != nil {
		return fmt.Errorf("core: assembler flags select: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, msg, refID, mentionID, taxonID, candJSON string
		if err := rows.Scan(&kind, &msg, &refID, &mentionID, &taxonID, &candJSON); err != nil {
			return fmt.Errorf("core: assembler flags scan: %w", err)
		}
		var cands []string
		if candJSON != "" && candJSON != "[]" {
			_ = json.Unmarshal([]byte(candJSON), &cands)
		}
		node, ok := nodesByID[taxonID]
		if !ok {
			// Flag anchors to a taxon we don't know about (e.g., a
			// taxon deleted after the reassemble). Skip — the flag
			// still exists in hive__assembler_flag for auditing.
			continue
		}
		node.Flags = append(node.Flags, AssemblerNodeFlag{
			Kind:        kind,
			Message:     msg,
			ReferenceID: refID,
			MentionID:   mentionID,
			Candidates:  cands,
		})
	}
	return rows.Err()
}

func (a *Archive) loadOverlayPresenceForNodes(ctx context.Context, nodesByID map[string]*AssemblerNode) error {
	rows, err := a.db.QueryContext(ctx,
		`SELECT DISTINCT taxon_id
		   FROM hive__layer3_overlay
		  WHERE retired_at IS NULL`)
	if err != nil {
		return fmt.Errorf("core: overlay presence select: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return fmt.Errorf("core: overlay presence scan: %w", err)
		}
		if n, ok := nodesByID[tid]; ok {
			n.HasOverlay = true
		}
	}
	return rows.Err()
}

// deriveNodeColor applies the priority: blue > red > amber > green.
func deriveNodeColor(hasOverlay bool, flags []AssemblerNodeFlag) string {
	if hasOverlay {
		return "blue"
	}
	color := "green"
	for _, f := range flags {
		switch strings.ToLower(f.Kind) {
		case "unresolved_anchor", "ambiguous_match", "orphaned_overlay":
			return "red"
		case "contradictory_synonymy", "ambiguous_implicit_reference",
			"missing_implicit_reference", "overlay_matched_by_publication":
			color = "amber"
		}
	}
	return color
}
