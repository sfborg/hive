package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// NomenclaturalHistory is the multi-cluster projection returned by
// Archive.NomenclaturalHistory. A taxon's history is grouped by
// **basionym anchor**: every name in a cluster shares an original
// combination (either explicitly via name_relation type=BASIONYM, or
// implicitly by BEING the basionym itself). This mirrors how
// taxonomists actually read a synonymy — original name first, then
// subsequent combinations of the same root.
//
// Clusters carry a role:
//
//   - "accepted" — the cluster contains the taxon's currently
//     accepted name. There is exactly one such cluster per response.
//   - "synonym" — the cluster contains only synonyms of the taxon
//     (and any recombinations we discovered via name_relation that
//     aren't themselves linked to this taxon).
//
// Names within a cluster are ordered basionym-first, then by
// authorship year ascending, then alphabetical. That's the read
// order for a synonymy paragraph: original combination, then each
// recombination chronologically.
type NomenclaturalHistory struct {
	Clusters []NomenCluster
}

// NomenCluster is one basionym-anchored family of names surfaced on
// a taxon's nomenclatural-history section.
type NomenCluster struct {
	Role  string // "accepted" or "synonym"
	Names []NomenName
}

// NomenName is one name entry in a nomenclatural-history cluster.
// Involvement records how this row relates to the taxon we're
// viewing: "accepted" (this IS the taxon's accepted name),
// "synonym" (linked as a synonym; SynonymID is populated), or
// "unlinked" (a sibling recombination discovered via
// name_relation but not currently attached to this taxon — useful
// context, not something the curator needs to act on).
type NomenName struct {
	NameID      string
	Label       Label
	Authorship  string
	Rank        string
	Year        string
	IsBasionym  bool
	Involvement string
	SynonymID   string
	ReferenceID string
	// Atomized authorship — surfaced alongside the pre-formatted
	// Authorship string so the WUI's "Standardized authorship" render
	// can compose the hybrid "(basionym_author, basionym_year)
	// combination_author, combination_year" form neither ICZN nor ICN
	// produces on its own. Empty fields fall back to whichever pieces
	// exist. Same source as apiName's equivalent fields — comes from
	// gnparser's atomization on write (or curator override).
	BasionymAuthorship        string
	BasionymAuthorshipYear    string
	CombinationAuthorship     string
	CombinationAuthorshipYear string
	// IssueCount is the number of __gsvalidator_results rows currently
	// open against this name row (table_name='name', any severity).
	// Populated per-name by a single batched query in
	// Archive.NomenclaturalHistory so the frontend can render a warn
	// icon on synonym / basionym rows that need attention without a
	// per-row round trip.
	IssueCount int
	// MaxSeverity is the highest severity among the open issues on
	// this name ("error" > "warn" > "info" > "debug"). Empty when
	// IssueCount is 0. Drives the WUI badge color via the shared
	// validationSeverityBadge helper.
	MaxSeverity string
}

// NomenclaturalHistory returns the basionym-anchored cluster
// projection for a taxon. Reads only — safe to call from any
// request path. Returns an empty (non-nil) NomenclaturalHistory
// with zero clusters when the taxon has no accepted name row and
// no synonyms; ErrNotFound when the taxon id itself doesn't exist.
//
// Cost is bounded by (synonym count + basionym cluster size) name
// rows to fetch. Two batched-IN queries (one for outgoing
// BASIONYM, one for incoming) plus one to hydrate every name's
// display fields — cheap on typical taxa (a handful of names) and
// linear in cluster size on the rare monotypic-genus case (dozens
// of recombinations).
func (a *Archive) NomenclaturalHistory(ctx context.Context, taxonID string) (*NomenclaturalHistory, error) {
	if taxonID == "" {
		return nil, ErrNotFound
	}
	// Step 1: accepted name id.
	var acceptedNameID string
	err := a.db.QueryRowContext(ctx,
		`SELECT col__name_id FROM taxon WHERE col__id = ?`, taxonID,
	).Scan(&acceptedNameID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: nomenclatural history %s: %w", taxonID, ErrNotFound)
		}
		return nil, fmt.Errorf("core: nomen history accepted name %s: %w", taxonID, err)
	}

	// Step 2: synonyms — (col__id, col__name_id) pairs.
	type synLink struct{ id, nameID string }
	var syns []synLink
	rows, err := a.db.QueryContext(ctx,
		`SELECT COALESCE(col__id, ''), col__name_id FROM synonym WHERE col__taxon_id = ?`,
		taxonID,
	)
	if err != nil {
		return nil, fmt.Errorf("core: nomen history synonyms %s: %w", taxonID, err)
	}
	for rows.Next() {
		var s synLink
		if err := rows.Scan(&s.id, &s.nameID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("core: nomen history scan synonym: %w", err)
		}
		syns = append(syns, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("core: nomen history iterate synonyms: %w", err)
	}

	// involved[nameID] records how each involved name relates to the
	// taxon. A name may appear as both accepted and synonym in
	// pathological archives — accepted wins.
	type involvement struct {
		role      string // "accepted" or "synonym"
		synonymID string
	}
	involved := make(map[string]involvement, len(syns)+1)
	if acceptedNameID != "" {
		involved[acceptedNameID] = involvement{role: "accepted"}
	}
	for _, s := range syns {
		if _, exists := involved[s.nameID]; exists {
			continue
		}
		involved[s.nameID] = involvement{role: "synonym", synonymID: s.id}
	}
	if len(involved) == 0 {
		return &NomenclaturalHistory{}, nil
	}

	// Step 3: find each involved name's basionym anchor — either the
	// outgoing BASIONYM target, or the name itself when it IS the
	// basionym.
	anchors, err := a.batchBasionymAnchors(ctx, keysOf(involved))
	if err != nil {
		return nil, err
	}
	for nameID := range involved {
		if _, ok := anchors[nameID]; !ok {
			anchors[nameID] = nameID // self-anchor when no outgoing BASIONYM
		}
	}

	// Step 4: for every distinct anchor, find all recombinations
	// (names X where X.BASIONYM = anchor). Union with the involved
	// names sharing that anchor to get the full cluster membership.
	anchorSet := make(map[string]bool, len(involved))
	for _, a := range anchors {
		anchorSet[a] = true
	}
	recomb, err := a.batchBasionymRecombinations(ctx, keysOfBool(anchorSet))
	if err != nil {
		return nil, err
	}

	// membership[anchor] = set of nameIDs in that cluster.
	membership := make(map[string]map[string]bool, len(anchorSet))
	for anchor := range anchorSet {
		set := map[string]bool{anchor: true}
		for _, r := range recomb[anchor] {
			set[r] = true
		}
		membership[anchor] = set
	}
	for nameID, anchor := range anchors {
		membership[anchor][nameID] = true
	}

	// Step 5: hydrate every mentioned name (anchors + recombinations
	// + involved) in one batched SELECT.
	allNames := make(map[string]bool, len(membership)*4)
	for _, set := range membership {
		for id := range set {
			allNames[id] = true
		}
	}
	rowsByID, err := a.batchNomenNameRows(ctx, keysOfBool(allNames))
	if err != nil {
		return nil, err
	}

	// Step 5b: batch issue summaries (count + max severity) for
	// every name in play. Single query over __gsvalidator_results
	// so the frontend can drape a severity-colored warn icon on
	// rows that need attention (see the WUI row-actions block).
	// Cheap — indexed lookup on table_name + record_id.
	issueSummaries, err := a.batchIssueSummaries(ctx, "name", keysOfBool(allNames))
	if err != nil {
		return nil, err
	}

	// Step 6: assemble clusters.
	out := &NomenclaturalHistory{Clusters: make([]NomenCluster, 0, len(membership))}
	for anchor, set := range membership {
		cluster := NomenCluster{}
		for id := range set {
			row, ok := rowsByID[id]
			if !ok {
				// Referenced by name_relation but row missing — skip
				// rather than surface a phantom entry.
				continue
			}
			entry := NomenName{
				NameID:                    id,
				Authorship:                row.authorship,
				Rank:                      row.rank,
				Year:                      row.year,
				IsBasionym:                id == anchor,
				ReferenceID:               row.referenceID,
				BasionymAuthorship:        row.basionymAuthor,
				BasionymAuthorshipYear:    row.basionymAuthorYear,
				CombinationAuthorship:     row.combinationAuthor,
				CombinationAuthorshipYear: row.combinationAuthYear,
				IssueCount:                issueSummaries[id].Count,
				MaxSeverity:               issueSummaries[id].MaxSeverity,
				Label:                     BuildLabel(row.canonical, row.authorship, row.rank, false),
			}
			if inv, ok := involved[id]; ok {
				entry.Involvement = inv.role
				entry.SynonymID = inv.synonymID
				if inv.role == "accepted" {
					cluster.Role = "accepted"
				}
			} else {
				entry.Involvement = "unlinked"
			}
			cluster.Names = append(cluster.Names, entry)
		}
		if cluster.Role == "" {
			cluster.Role = "synonym"
		}
		sortNomenNames(cluster.Names)
		out.Clusters = append(out.Clusters, cluster)
	}
	sortNomenClusters(out.Clusters)
	return out, nil
}

// nomenNameRow is the raw column tuple hydrated by
// batchNomenNameRows. Kept private to this file — the exported
// NomenName carries the rendered Label.
type nomenNameRow struct {
	canonical           string
	authorship          string
	rank                string
	year                string
	referenceID         string
	basionymAuthor      string
	basionymAuthorYear  string
	combinationAuthor   string
	combinationAuthYear string
}

// batchBasionymAnchors returns a map nameID → basionym-target-nameID
// for every input id that carries an outgoing BASIONYM relation.
// Ids without such a relation are absent from the map; the caller
// treats absence as "the name is its own anchor" (i.e., no explicit
// basionym recorded).
func (a *Archive) batchBasionymAnchors(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT col__name_id, col__related_name_id
		FROM name_relation
		WHERE col__type_id = 'BASIONYM' AND col__name_id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: nomen history basionym anchors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var subj, obj string
		if err := rows.Scan(&subj, &obj); err != nil {
			return nil, fmt.Errorf("core: nomen history scan anchor: %w", err)
		}
		out[subj] = obj
	}
	return out, rows.Err()
}

// batchBasionymRecombinations returns basionymNameID → []nameID of
// every recombination (name whose outgoing BASIONYM points at the
// given basionym). Empty result for a basionym without recorded
// recombinations.
func (a *Archive) batchBasionymRecombinations(ctx context.Context, basionymIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(basionymIDs))
	if len(basionymIDs) == 0 {
		return out, nil
	}
	q := `SELECT col__name_id, col__related_name_id
		FROM name_relation
		WHERE col__type_id = 'BASIONYM' AND col__related_name_id IN (` + placeholders(len(basionymIDs)) + `)`
	args := make([]any, len(basionymIDs))
	for i, id := range basionymIDs {
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: nomen history recombinations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var subj, obj string
		if err := rows.Scan(&subj, &obj); err != nil {
			return nil, fmt.Errorf("core: nomen history scan recomb: %w", err)
		}
		out[obj] = append(out[obj], subj)
	}
	return out, rows.Err()
}

// batchNomenNameRows fetches the display columns for every id in
// one round trip.
func (a *Archive) batchNomenNameRows(ctx context.Context, ids []string) (map[string]nomenNameRow, error) {
	out := make(map[string]nomenNameRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT
		col__id,
		COALESCE(NULLIF(gn__canonical_simple, ''), col__scientific_name, ''),
		COALESCE(col__authorship, ''),
		COALESCE(col__rank_id, ''),
		COALESCE(NULLIF(col__combination_authorship_year, ''),
		         NULLIF(col__basionym_authorship_year, ''),
		         NULLIF(col__published_in_year, ''), ''),
		COALESCE(col__reference_id, ''),
		COALESCE(col__basionym_authorship, ''),
		COALESCE(col__basionym_authorship_year, ''),
		COALESCE(col__combination_authorship, ''),
		COALESCE(col__combination_authorship_year, '')
	FROM name WHERE col__id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: nomen history hydrate names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			r  nomenNameRow
		)
		if err := rows.Scan(&id, &r.canonical, &r.authorship, &r.rank, &r.year,
			&r.referenceID, &r.basionymAuthor, &r.basionymAuthorYear,
			&r.combinationAuthor, &r.combinationAuthYear); err != nil {
			return nil, fmt.Errorf("core: nomen history scan name row: %w", err)
		}
		out[id] = r
	}
	return out, rows.Err()
}

// batchNomenIssueCounts is retained as a thin wrapper for callers
// that only need the count (no severity). New code should call
// batchIssueSummaries directly for count + max severity in one pass.
func (a *Archive) batchNomenIssueCounts(ctx context.Context, ids []string) (map[string]int, error) {
	summaries, err := a.batchIssueSummaries(ctx, "name", ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(summaries))
	for id, s := range summaries {
		out[id] = s.Count
	}
	return out, nil
}

// sortNomenNames orders names within a cluster: basionym first
// (there's at most one per cluster), then chronologically by year,
// then alphabetical for stability. Empty years sort AFTER known
// years so undated recombinations don't crowd the top.
func sortNomenNames(names []NomenName) {
	sort.SliceStable(names, func(i, j int) bool {
		if names[i].IsBasionym != names[j].IsBasionym {
			return names[i].IsBasionym
		}
		yi, yj := names[i].Year, names[j].Year
		if (yi == "") != (yj == "") {
			return yi != ""
		}
		if yi != yj {
			return yi < yj
		}
		return names[i].Label.Text < names[j].Label.Text
	})
}

// sortNomenClusters orders clusters: accepted first, then synonym
// clusters by earliest-year of their basionym, then alphabetical.
func sortNomenClusters(clusters []NomenCluster) {
	sort.SliceStable(clusters, func(i, j int) bool {
		if (clusters[i].Role == "accepted") != (clusters[j].Role == "accepted") {
			return clusters[i].Role == "accepted"
		}
		ai := clusterAnchor(clusters[i])
		aj := clusterAnchor(clusters[j])
		yi, yj := ai.Year, aj.Year
		if (yi == "") != (yj == "") {
			return yi != ""
		}
		if yi != yj {
			return yi < yj
		}
		return ai.Label.Text < aj.Label.Text
	})
}

// clusterAnchor returns the basionym entry of a cluster, or the
// first entry when no name is flagged as basionym (a robustness
// guard — the sort above walked basionym-first, so the first entry
// is a reasonable fallback).
func clusterAnchor(c NomenCluster) NomenName {
	for _, n := range c.Names {
		if n.IsBasionym {
			return n
		}
	}
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return NomenName{}
}

// placeholders returns "?, ?, ?" for n ≥ 1 — used to build
// batched IN clauses. Zero n returns "" so callers can skip the
// SQL entirely.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?, ", n-1) + "?"
}

// keysOf / keysOfBool: tiny generic-ish helpers so the caller
// doesn't have to allocate a slice inline every time. Go 1.21+
// has maps.Keys but returns an iterator, not a slice.
func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
