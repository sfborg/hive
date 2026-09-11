package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	hive "github.com/sfborg/hive/pkg"
)

// apiRowSource is the wire shape for a single row_source lookup:
// which extension row (entity_table + rowid), what reference /
// extraction_run / assertion produced it, and enough reference
// metadata for the WUI badge to render a compact citation link
// without a second fetch per source.
type apiRowSource struct {
	EntityTable         string    `json:"entity_table"`
	EntityID            string    `json:"entity_id"`
	ReferenceID         string    `json:"reference_id"`
	ReferenceCitation   string    `json:"reference_citation,omitempty"`
	ExtractionRunID     string    `json:"extraction_run_id,omitempty"`
	AssertionID         string    `json:"assertion_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	CreatedBy           string    `json:"created_by,omitempty"`
}

type apiRowSourceList struct {
	Items []apiRowSource `json:"items"`
}

// handleGetTaxonRowSources returns row_source entries for every
// extension row attached to the given taxon:
//
//   - type_material rows via the taxon's col__name_id
//   - distribution rows directly via col__taxon_id
//   - vernacular_name rows directly via col__taxon_id
//
// Backs the detail-pane source badges (one badge per extension
// row) and, in aggregate, could feed a tree-node "N papers"
// count later. Response includes a short reference citation
// string so the badge doesn't need a second fetch.
//
// @Summary      List row sources for a taxon
// @Description  Every hive__row_source row attached to the taxon's type_material, distribution, and vernacular children, plus a short citation for each unique reference.
// @Tags         assertions
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiRowSourceList
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/row-sources [get]
func (s *server) handleGetTaxonRowSources(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	if taxonID == "" {
		writeBadRequest(w, r, "taxon id is required")
		return
	}
	// Resolve the taxon's accepted name id — type_material rows
	// attach to name, not taxon, so we need both.
	nameID, err := s.lookupTaxonNameID(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Query row_source joined against each extension table so we
	// filter down to rows attached to THIS taxon (or its name).
	rows, err := s.queryRowSourcesForTaxon(r.Context(), taxonID, nameID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Enrich with a short citation per unique reference so the
	// WUI badge has display text without a second fetch.
	byRefID := s.batchReferenceCitations(r.Context(), uniqueReferenceIDs(rows))
	for i := range rows {
		if c, ok := byRefID[rows[i].ReferenceID]; ok {
			rows[i].ReferenceCitation = c
		}
	}
	writeJSON(w, http.StatusOK, apiRowSourceList{Items: rows})
}

// lookupTaxonNameID reads col__name_id from the taxon table.
// Returns empty string (not error) when the taxon exists but has
// no name — the caller still runs the direct-attach queries.
// Returns ErrNotFound when the taxon doesn't exist at all.
func (s *server) lookupTaxonNameID(ctx context.Context, taxonID string) (string, error) {
	row := s.a.DB().QueryRowContext(ctx,
		`SELECT COALESCE(col__name_id, '') FROM taxon WHERE col__id = ?`, taxonID)
	var nameID string
	err := row.Scan(&nameID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("taxon %q: %w", taxonID, hive.ErrNotFound)
	}
	if err != nil {
		return "", err
	}
	return nameID, nil
}

// queryRowSourcesForTaxon runs three parallel SELECTs against
// hive__row_source, filtered by entity_id membership in the
// respective extension tables scoped to this taxon (or its name
// for type_material). Union-style aggregation in Go rather than
// SQL because sqlite's UNION treats the WHERE clauses per-branch
// and the three tables have different filter columns.
func (s *server) queryRowSourcesForTaxon(
	ctx context.Context, taxonID, nameID string,
) ([]apiRowSource, error) {
	var out []apiRowSource
	if nameID != "" {
		tm, err := s.queryRowSourceForTable(ctx,
			"type_material",
			`SELECT rowid FROM type_material WHERE col__name_id = ?`,
			nameID,
		)
		if err != nil {
			return nil, err
		}
		out = append(out, tm...)
	}
	dist, err := s.queryRowSourceForTable(ctx,
		"distribution",
		`SELECT rowid FROM distribution WHERE col__taxon_id = ?`,
		taxonID,
	)
	if err != nil {
		return nil, err
	}
	out = append(out, dist...)
	vern, err := s.queryRowSourceForTable(ctx,
		"vernacular",
		`SELECT rowid FROM vernacular WHERE col__taxon_id = ?`,
		taxonID,
	)
	if err != nil {
		return nil, err
	}
	out = append(out, vern...)
	return out, nil
}

// queryRowSourceForTable fetches row_source rows for entity_ids
// matching a per-table selector. Uses IN(...) with a subquery so
// SQLite plans it efficiently.
func (s *server) queryRowSourceForTable(
	ctx context.Context, entityTable, subquery string, filterArg any,
) ([]apiRowSource, error) {
	q := `SELECT entity_table, entity_id, reference_id,
	         COALESCE(extraction_run_id, ''),
	         COALESCE(assertion_id, ''),
	         created_at, created_by
	  FROM hive__row_source
	  WHERE entity_table = ?
	    AND CAST(entity_id AS INTEGER) IN (` + subquery + `)`
	rows, err := s.a.DB().QueryContext(ctx, q, entityTable, filterArg)
	if err != nil {
		return nil, fmt.Errorf("query row_source for %s: %w", entityTable, err)
	}
	defer rows.Close()
	var out []apiRowSource
	for rows.Next() {
		var (
			rs        apiRowSource
			createdAt string
		)
		if err := rows.Scan(
			&rs.EntityTable, &rs.EntityID, &rs.ReferenceID,
			&rs.ExtractionRunID, &rs.AssertionID,
			&createdAt, &rs.CreatedBy,
		); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			rs.CreatedAt = t
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

func uniqueReferenceIDs(rows []apiRowSource) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.ReferenceID != "" && !seen[r.ReferenceID] {
			seen[r.ReferenceID] = true
			out = append(out, r.ReferenceID)
		}
	}
	return out
}

// batchReferenceCitations fetches each reference once and returns
// a map of {reference_id → short citation string}. Errors on
// individual references are swallowed (badge falls back to
// showing the raw ID) — a missing citation shouldn't block the
// whole detail-pane render.
func (s *server) batchReferenceCitations(
	ctx context.Context, refIDs []string,
) map[string]string {
	out := make(map[string]string, len(refIDs))
	for _, id := range refIDs {
		ref, err := s.a.GetReference(ctx, id)
		if err != nil {
			continue
		}
		out[id] = shortCitation(ref)
	}
	return out
}

// shortCitation returns "Author (Year)" — enough for a badge
// label. Falls back to Title when Author is empty, or to the
// full compose citation when both are empty.
func shortCitation(ref interface {
	// Interface method set matches coldp.Reference fields we
	// need — avoids importing coldp here directly.
}) string {
	// Best-effort reflection-lite via JSON. Simpler than importing
	// coldp; we're just extracting Author + Issued year.
	b, err := json.Marshal(ref)
	if err != nil {
		return ""
	}
	var r struct {
		Author string `json:"author"`
		Title  string `json:"title"`
		Issued string `json:"issued"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return ""
	}
	year := ""
	if len(r.Issued) >= 4 {
		year = r.Issued[:4]
	}
	if r.Author != "" && year != "" {
		return fmt.Sprintf("%s (%s)", r.Author, year)
	}
	if r.Author != "" {
		return r.Author
	}
	if r.Title != "" {
		return r.Title
	}
	return ""
}
