package server

import (
	"net/http"
	"strconv"
	"strings"

	hive "github.com/sfborg/hive/pkg"
)

// apiTaxonTableRow is one name usage in the taxon table. Supplemental
// lists hold at most 20 values each.
type apiTaxonTableRow struct {
	NameID         string            `json:"name_id"`
	TaxonID        string            `json:"taxon_id"`
	SynonymID      string            `json:"synonym_id,omitempty"`
	Usage          string            `json:"usage"`
	Status         string            `json:"status,omitempty"`
	Name           string            `json:"name"`
	Authorship     string            `json:"authorship,omitempty"`
	Rank           string            `json:"rank,omitempty"`
	Code           string            `json:"code,omitempty"`
	Related        string            `json:"related,omitempty"`
	Classification map[string]string `json:"classification,omitempty"`
	Extinct        *bool             `json:"extinct,omitempty"`
	Modified       string            `json:"modified,omitempty"`
	Vernaculars    []string          `json:"vernaculars,omitempty"`
	Distribution   []string          `json:"distribution,omitempty"`
	Interactions   []string          `json:"interactions,omitempty"`
	TypeMaterial   []string          `json:"type_material,omitempty"`
	Media          []string          `json:"media,omitempty"`
	Properties     []string          `json:"properties,omitempty"`
	Estimates      []string          `json:"estimates,omitempty"`
	Synonyms       int               `json:"synonyms,omitempty"`
	NameRelations  int               `json:"name_relations,omitempty"`
	Issues         map[string]int    `json:"issues,omitempty"`
	IssueRules     []string          `json:"issue_rules,omitempty"`
}

// handleTaxonTable returns a page of the taxon table: every name usage
// (accepted taxa and synonyms) with classification, supplemental data
// and open validation issues.
//
// @Summary      Taxon table
// @Tags         taxa
// @Produce      json
// @Param        q      query string false "name contains"
// @Param        usage  query string false "accepted | synonym"
// @Param        rank   query string false "comma-separated rank IDs"
// @Param        issue  query string false "any | error | warn | info | debug | rule:<rule_id>"
// @Param        within query string false "taxon ID: keep it and the taxa below it"
// @Param        has     query string false "keep rows with this data: vernaculars, distribution, interactions, media, properties, estimates, synonyms, type_material, name_relations"
// @Param        missing query string false "keep rows lacking this data (same kinds as has)"
// @Param        sort   query string false "col:asc|desc, comma-separated"
// @Param        lineage query string false "comma-separated lowercase rank IDs to fill the classification with (default kingdom,phylum,class,order,family,genus)"
// @Param        limit  query int    false "page size (default 100, max 500)"
// @Param        offset query int    false "rows to skip"
// @Success      200 {object} map[string]any
// @Failure      400 {object} problem
// @Router       /taxon/table [get]
func (s *server) handleTaxonTable(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := hive.TaxonTableQuery{Q: v.Get("q"), Usage: v.Get("usage"), Issue: v.Get("issue"),
		Within: v.Get("within"), Has: v.Get("has"), Missing: v.Get("missing")}
	if raw := strings.TrimSpace(v.Get("rank")); raw != "" {
		q.Ranks = strings.Split(raw, ",")
	}
	sort, err := hive.ParseTaxonTableSort(v.Get("sort"))
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	q.Sort = sort
	if v.Has("lineage") {
		q.Lineage = []string{}
		if raw := strings.TrimSpace(v.Get("lineage")); raw != "" {
			q.Lineage = strings.Split(raw, ",")
		}
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	q.Offset, _ = strconv.Atoi(v.Get("offset"))
	rows, total, err := s.a.TaxonTable(r.Context(), q)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiTaxonTableRow, len(rows))
	for i, row := range rows {
		items[i] = taxonTableRowToAPI(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// handleTaxonTableRanks lists the archive's ranks in hierarchy order,
// with how many taxa each has, for the table's classification columns.
//
// @Summary      Ranks for classification columns
// @Tags         taxa
// @Produce      json
// @Success      200 {object} map[string]any
// @Router       /taxon/table/ranks [get]
func (s *server) handleTaxonTableRanks(w http.ResponseWriter, r *http.Request) {
	ranks, err := s.a.TaxonRanks(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]map[string]any, len(ranks))
	for i, rk := range ranks {
		items[i] = map[string]any{"id": rk.ID, "name": rk.Name, "linnean": rk.Linnean, "taxa": rk.Taxa}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleTaxonTableIssueRules lists the validation rules with open
// issues on names, taxa or synonyms, for the table's issue filter.
//
// @Summary      Issue rules found on names
// @Tags         taxa
// @Produce      json
// @Success      200 {object} map[string]any
// @Router       /taxon/table/issue-rules [get]
func (s *server) handleTaxonTableIssueRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.a.TaxonIssueRules(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	type apiRule struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Severity string `json:"severity"`
		Count    int    `json:"count"`
	}
	items := make([]apiRule, len(rules))
	for i, rule := range rules {
		items[i] = apiRule{rule.ID, rule.Name, rule.Severity, rule.Count}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func taxonTableRowToAPI(row hive.TaxonTableRow) apiTaxonTableRow {
	return apiTaxonTableRow{
		NameID: row.NameID, TaxonID: row.TaxonID, SynonymID: row.SynonymID,
		Usage: row.Usage, Status: row.Status, Name: row.Name, Authorship: row.Authorship,
		Rank: row.Rank, Code: row.Code, Related: row.Related,
		Classification: row.Classification, Extinct: row.Extinct, Modified: row.Modified,
		Vernaculars: row.Vernaculars, Distribution: row.Distribution,
		Interactions: row.Interactions, TypeMaterial: row.TypeMaterial,
		Media: row.Media, Properties: row.Properties, Estimates: row.Estimates,
		Synonyms: row.Synonyms, NameRelations: row.NameRelations,
		Issues: row.IssuesBySeverity, IssueRules: row.IssueRules,
	}
}
