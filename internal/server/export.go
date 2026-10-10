package server

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	hive "github.com/sfborg/hive/pkg"
)

// Table exports: the Search screen's taxon table and the reference list
// as CSV or SQLite, with the screen's filters, sort and visible
// columns. Every matching row is exported, not only the loaded page.
// The record identifiers come first so exported rows can be joined
// back to the archive.

// exportColumn is one exported column: its identifier (the screen's
// column id, used as the SQLite column name), heading, SQLite type and
// how to read it from a row.
type exportColumn[T any] struct {
	id, label, kind string
	value           func(T) any
}

func textCol[T any](id, label string, f func(T) string) exportColumn[T] {
	return exportColumn[T]{id, label, "TEXT", func(r T) any { return f(r) }}
}

func intCol[T any](id, label string, f func(T) int) exportColumn[T] {
	return exportColumn[T]{id, label, "INTEGER", func(r T) any { return f(r) }}
}

// joinList joins a cell's values for one CSV or SQLite field.
func joinList(v []string) string { return strings.Join(v, "; ") }

// severityCounts renders issue counts as "error 1; warn 2".
func severityCounts(m map[string]int) string {
	var parts []string
	for _, sev := range []string{"error", "warn", "info", "debug"} {
		if m[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", sev, m[sev]))
		}
	}
	return strings.Join(parts, "; ")
}

var taxonExportColumns = func() []exportColumn[apiTaxonTableRow] {
	type R = apiTaxonTableRow
	cols := []exportColumn[R]{
		textCol("name_id", "Name ID", func(r R) string { return r.NameID }),
		textCol("taxon_id", "Taxon ID", func(r R) string { return r.TaxonID }),
		textCol("synonym_id", "Synonym ID", func(r R) string { return r.SynonymID }),
		textCol("name", "Name", func(r R) string { return r.Name }),
		textCol("authorship", "Authorship", func(r R) string { return r.Authorship }),
		textCol("rank", "Rank", func(r R) string { return strings.ToLower(r.Rank) }),
		textCol("usage", "Usage", func(r R) string { return r.Usage }),
		textCol("related", "Parent / accepted", func(r R) string { return r.Related }),
		textCol("status", "Status", func(r R) string { return strings.ToLower(r.Status) }),
		textCol("code", "Code", func(r R) string { return strings.ToLower(r.Code) }),
		textCol("extinct", "Extinct", func(r R) string {
			if r.Extinct != nil && *r.Extinct {
				return "true"
			}
			return ""
		}),
		textCol("modified", "Modified", func(r R) string { return r.Modified }),
	}
	for _, rank := range hive.TaxonTableRanks {
		cols = append(cols, textCol(rank, strings.ToUpper(rank[:1])+rank[1:],
			func(r R) string { return r.Classification[rank] }))
	}
	return append(cols,
		textCol("vernaculars", "Vernacular names", func(r R) string { return joinList(r.Vernaculars) }),
		textCol("distribution", "Distribution", func(r R) string { return joinList(r.Distribution) }),
		textCol("interactions", "Interactions", func(r R) string { return joinList(r.Interactions) }),
		textCol("type_material", "Type material", func(r R) string { return joinList(r.TypeMaterial) }),
		textCol("media", "Media", func(r R) string { return joinList(r.Media) }),
		textCol("properties", "Properties", func(r R) string { return joinList(r.Properties) }),
		textCol("estimates", "Species estimates", func(r R) string { return joinList(r.Estimates) }),
		intCol("synonyms", "Synonyms", func(r R) int { return r.Synonyms }),
		intCol("name_relations", "Name relations", func(r R) int { return r.NameRelations }),
		textCol("issues", "Issues", func(r R) string { return severityCounts(r.Issues) }),
		textCol("issue_rules", "Issue types", func(r R) string { return joinList(r.IssueRules) }),
	)
}()

// taxonSummaryCell renders a classification summary as
// "Dinophyceae 256; Spirotrichea 134".
func taxonSummaryCell(ts *apiTaxonSummary) string {
	if ts == nil {
		return ""
	}
	parts := make([]string, len(ts.Top))
	for i, t := range ts.Top {
		parts[i] = fmt.Sprintf("%s %d", t.Name, t.Count)
	}
	return strings.Join(parts, "; ")
}

var referenceExportColumns = func() []exportColumn[apiReferenceHit] {
	type R = apiReferenceHit
	taxa := func(r R, rank string) *apiTaxonSummary {
		if ts, ok := r.Taxa[rank]; ok {
			return &ts
		}
		return nil
	}
	cols := []exportColumn[R]{
		textCol("id", "Reference ID", func(r R) string { return r.ID }),
		textCol("status", "Status", func(r R) string {
			if r.Workflow == nil {
				return ""
			}
			w := r.Workflow
			return fmt.Sprintf("bibliographic %s; digitized %s; assertions %s; imported %s",
				w.Bibliographic, w.Digitized, w.Assertions, w.Imported)
		}),
		textCol("author", "Authors", func(r R) string { return r.Author }),
		textCol("year", "Year", func(r R) string { return r.Year }),
		textCol("title", "Title", func(r R) string { return r.Title }),
		textCol("citation", "Citation", func(r R) string { return r.Citation }),
		textCol("name", "Names", func(r R) string { return taxonSummaryCell(taxa(r, "name")) }),
	}
	for _, rank := range hive.SummaryRanks {
		cols = append(cols, textCol(rank, strings.ToUpper(rank[:1])+rank[1:],
			func(r R) string { return taxonSummaryCell(taxa(r, rank)) }))
	}
	return append(cols,
		textCol("issues", "Issues", func(r R) string { return severityCounts(r.Issues) }),
		textCol("license", "License", func(r R) string { return r.License }),
		intCol("pages", "Pages", func(r R) int { return r.Pages }),
		intCol("words", "Words", func(r R) int { return r.Words }),
		intCol("characters", "Characters", func(r R) int { return r.Characters }),
		textCol("tokens", "Tokens used (provider-reported, input / output)", func(r R) string {
			if r.TokensIn == nil && r.TokensOut == nil {
				return ""
			}
			in, out := 0, 0
			if r.TokensIn != nil {
				in = *r.TokensIn
			}
			if r.TokensOut != nil {
				out = *r.TokensOut
			}
			return fmt.Sprintf("%d / %d", in, out)
		}),
		textCol("modified", "Modified", func(r R) string { return r.Modified }),
	)
}()

// pickColumns returns the identifier columns followed by the requested
// columns in the requested order; unknown ids are skipped and an empty
// request exports every column.
func pickColumns[T any](all []exportColumn[T], ids []string, idCols int) []exportColumn[T] {
	if len(ids) == 0 {
		return all
	}
	byID := map[string]exportColumn[T]{}
	for _, c := range all {
		byID[c.id] = c
	}
	out := append([]exportColumn[T]{}, all[:idCols]...)
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.id] = true
	}
	for _, id := range ids {
		if c, ok := byID[id]; ok && !seen[id] {
			out = append(out, c)
			seen[id] = true
		}
	}
	return out
}

// writeExport writes rows as CSV or as a SQLite file with one table,
// named table, plus export_info describing the export.
func writeExport[T any](w http.ResponseWriter, r *http.Request, format, base, table string,
	cols []exportColumn[T], rows []T, info map[string]string) {
	stamp := time.Now().Format("2006-01-02")
	switch format {
	case "", "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, base, stamp))
		if err := writeCSV(w, cols, rows); err != nil {
			writeProblem(w, r, err)
		}
	case "sqlite":
		sendSQLite(w, r, base+"-"+stamp+".sqlite", func(path string) error {
			return writeSQLite(r.Context(), path, table, cols, rows, info)
		})
	default:
		writeBadRequest(w, r, "format must be csv or sqlite")
	}
}

// sendSQLite builds a SQLite file with write in a temporary directory
// and sends it as an attachment named name.
func sendSQLite(w http.ResponseWriter, r *http.Request, name string, write func(path string) error) {
	dir, err := os.MkdirTemp("", "hive-export-")
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "export.sqlite")
	if err := write(path); err != nil {
		writeProblem(w, r, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	io.Copy(w, f)
}

func writeCSV[T any](w io.Writer, cols []exportColumn[T], rows []T) error {
	// A byte-order mark lets spreadsheet programs read the file as UTF-8.
	if _, err := io.WriteString(w, "\xef\xbb\xbf"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = c.label
	}
	if err := cw.Write(head); err != nil {
		return err
	}
	rec := make([]string, len(cols))
	for _, row := range rows {
		for i, c := range cols {
			switch v := c.value(row).(type) {
			case int:
				rec[i] = strconv.Itoa(v)
			case string:
				rec[i] = v
			default:
				rec[i] = fmt.Sprint(v)
			}
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func writeSQLite[T any](ctx context.Context, path, table string, cols []exportColumn[T], rows []T, info map[string]string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	defs := make([]string, len(cols))
	marks := make([]string, len(cols))
	for i, c := range cols {
		if !identifier.MatchString(c.id) {
			return fmt.Errorf("export: bad column id %q", c.id)
		}
		defs[i] = `"` + c.id + `" ` + c.kind
		marks[i] = "?"
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE "` + table + `" (` + strings.Join(defs, ", ") + `)`,
		`CREATE TABLE export_info (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE export_columns (ordinal INTEGER, column_name TEXT, label TEXT)`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("export: %w", err)
		}
	}
	for k, v := range info {
		if _, err := tx.ExecContext(ctx, `INSERT INTO export_info VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	for i, c := range cols {
		if _, err := tx.ExecContext(ctx, `INSERT INTO export_columns VALUES (?, ?, ?)`, i+1, c.id, c.label); err != nil {
			return err
		}
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO "`+table+`" VALUES (`+strings.Join(marks, ", ")+`)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	args := make([]any, len(cols))
	for _, row := range rows {
		for i, c := range cols {
			args[i] = c.value(row)
		}
		if _, err := ins.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("export: insert: %w", err)
		}
	}
	return tx.Commit()
}

// exportInfo describes an export for its export_info table.
func (s *server) exportInfo(r *http.Request, what string) map[string]string {
	info := map[string]string{
		"exported":    what,
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"query":       r.URL.RawQuery,
		"archive":     filepath.Base(s.archivePath),
	}
	if m, err := s.a.GetMetadata(r.Context()); err == nil {
		info["title"] = m.Title
	}
	return info
}

func splitColumns(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// handleTaxonTableExport exports the taxon table with the Search
// screen's filters, sort and columns.
//
// @Summary      Export the taxon table
// @Tags         taxa
// @Produce      text/csv
// @Param        format  query string false "csv (default), sqlite (one table) or sfga (the matching records and their related tables, with taxon_table_view)"
// @Param        columns query string false "comma-separated column ids, in order; identifiers always come first"
// @Param        validation query string false "sfga: 0 leaves out the validation results"
// @Success      200 {file} file
// @Router       /taxon/table/export [get]
func (s *server) handleTaxonTableExport(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := hive.TaxonTableQuery{Q: v.Get("q"), Usage: v.Get("usage"), Issue: v.Get("issue"),
		Within: v.Get("within"), Has: v.Get("has"), Missing: v.Get("missing"), DetailLimit: 1 << 20}
	if raw := strings.TrimSpace(v.Get("rank")); raw != "" {
		q.Ranks = strings.Split(raw, ",")
	}
	sort, err := hive.ParseTaxonTableSort(v.Get("sort"))
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	q.Sort, q.Limit = sort, -1
	sfga := v.Get("format") == "sfga"
	if sfga {
		q.DetailLimit = 0
	}
	found, _, err := s.a.TaxonTable(r.Context(), q)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if sfga {
		info := s.exportInfo(r, "taxon table")
		sendSQLite(w, r, "taxa-"+time.Now().Format("2006-01-02")+".sfga.sqlite", func(path string) error {
			return s.a.ExportTaxaSFGA(r.Context(), path, found, v.Get("validation") != "0", info)
		})
		return
	}
	rows := make([]apiTaxonTableRow, len(found))
	for i, row := range found {
		rows[i] = taxonTableRowToAPI(row)
	}
	cols := pickColumns(taxonExportColumns, splitColumns(v.Get("columns")), 3)
	writeExport(w, r, v.Get("format"), "taxa", "taxa", cols, rows, s.exportInfo(r, "taxon table"))
}

// handleReferenceExport exports the reference list with the screen's
// filters, sort and columns.
//
// @Summary      Export the reference list
// @Tags         references
// @Produce      text/csv
// @Param        format  query string false "csv (default) or sqlite"
// @Param        columns query string false "comma-separated column ids, in order; the reference id always comes first"
// @Success      200 {file} file
// @Router       /reference/export [get]
func (s *server) handleReferenceExport(w http.ResponseWriter, r *http.Request) {
	sortMode := hive.ReferenceSortAuthor
	switch r.URL.Query().Get("sort") {
	case "recent":
		sortMode = hive.ReferenceSortRecent
	case "year":
		sortMode = hive.ReferenceSortYear
	}
	sources, err := s.a.NameSources(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	rq, msg := parseReferenceQuery(r, sources)
	if msg != "" {
		writeBadRequest(w, r, msg)
		return
	}
	// With search text, export what the search shows.
	var hits []hive.ReferenceHit
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		hits, err = s.a.SearchReferences(r.Context(), q, 1<<20, rq)
	} else {
		hits, _, err = s.a.ListReferencesPage(r.Context(), 0, 0, sortMode, rq)
	}
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	rows := make([]apiReferenceHit, len(hits))
	for i, h := range hits {
		rows[i] = referenceHitToAPI(h)
	}
	cols := pickColumns(referenceExportColumns, splitColumns(r.URL.Query().Get("columns")), 1)
	writeExport(w, r, r.URL.Query().Get("format"), "references", "reference_list", cols, rows,
		s.exportInfo(r, "reference list"))
}
