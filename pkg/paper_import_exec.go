package hive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// ExecutePaperImportResult reports what a single execute call did
// — how many extension rows landed for each aggregate, and any
// per-mapping problems (missing target, missing name on target,
// unresolved payload). Curator sees the summary; the WUI can drill
// into failed mappings if needed.
type ExecutePaperImportResult struct {
	// Counts of newly-inserted extension rows.
	TypeMaterialInserted int
	DistributionInserted int
	VernacularInserted   int
	// Prior-import rows removed on the delete-then-reinsert cycle.
	// Non-zero on re-syncs; zero on first import for the paper.
	Removed int
	// Warnings collects non-fatal per-mapping issues so the
	// executor doesn't fail the whole transaction on a single
	// dangling mapping. Curator resolves via the assembly UI.
	Warnings []string
}

// ExecutePaperImport walks the compose tree from the given
// extraction run and materializes the paper's extension rows
// (type_material, distribution, vernacular_name) onto the target
// taxa named in hive__paper_import for the reference.
//
// Semantics (per project_assembly_interface_plan):
//   - Additive fact-only: never creates or modifies backbone
//     (taxon, name, synonymy). Curator's responsibility.
//   - Delete-then-reinsert: drops every prior extension row
//     hive__row_source attributes to this reference, then
//     inserts fresh. Curator's per-assertion corrections
//     persist because they live in a different table
//     (hive__extraction_correction). Loss of curator corrections
//     to imported ROWS themselves is a known v0 limitation —
//     deferred until bulk-re-sync UI lands.
//   - Skips paper-mentions with no mapping (warning), skips
//     mappings pointing at deleted taxa (warning).
//   - Provenance stamped on every insert via hive__row_source.
func (t *Tx) ExecutePaperImport(
	ctx context.Context,
	referenceID string,
	extractionRunID string,
	composeTreeJSON string,
) (*ExecutePaperImportResult, error) {
	if referenceID == "" {
		return nil, errors.New("core: ExecutePaperImport: reference_id required")
	}
	if composeTreeJSON == "" {
		return nil, errors.New("core: ExecutePaperImport: compose tree is empty (nothing to import)")
	}

	result := &ExecutePaperImportResult{}

	// Parse the compose tree. We only need the extension arrays
	// keyed by nameID / taxonID.
	var tree composeTreeShape
	if err := json.Unmarshal([]byte(composeTreeJSON), &tree); err != nil {
		return nil, fmt.Errorf("core: parse compose tree: %w", err)
	}

	// Load mappings and build (paper_name_key → target_taxon_id).
	// composeTreeShape's name_usages use their compose ID as the
	// mention key, which is what hive__paper_import stores in
	// paper_name_key. Both name_usages[].id and extension rows
	// carry that identifier (nameID on type_material; taxonID
	// on distribution / vernacular).
	mappings, err := t.listPaperImportMappings(ctx, referenceID)
	if err != nil {
		return nil, err
	}
	if len(mappings) == 0 {
		return nil, errors.New("core: no mappings to import — assign target taxa first")
	}
	targetByKey := make(map[string]string, len(mappings))
	for _, m := range mappings {
		targetByKey[m.PaperNameKey] = m.TargetTaxonID
	}

	// Sweep prior imports for this reference before re-inserting.
	// The sweep also returns the (table, id) pairs so we can
	// delete the SFGA rows themselves.
	prior, err := t.DeleteRowSourcesForReference(referenceID)
	if err != nil {
		return nil, err
	}
	for _, pair := range prior {
		table, id := pair[0], pair[1]
		if err := t.deleteExtensionRow(table, id); err != nil {
			// Log-only — a missing prior row shouldn't block the
			// fresh import.
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("prior %s row %s: %v", table, id, err))
			continue
		}
		result.Removed++
	}

	actor := ActorFromContext(t.ctx)

	// Type material: keyed by nameID → look up target taxon's
	// name_id (type material belongs to a NAME, not a taxon).
	for _, tm := range tree.TypeMaterial {
		targetTaxonID, ok := targetByKey[tm.NameID]
		if !ok {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("type_material for %q: no mapping (skipped)", tm.NameID))
			continue
		}
		nameID, err := t.resolveTaxonNameID(ctx, targetTaxonID)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("type_material for %q → taxon %q: %v",
					tm.NameID, targetTaxonID, err))
			continue
		}
		rowid, err := t.insertTypeMaterialFromCompose(nameID, referenceID, tm)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("insert type_material for %q: %v", tm.NameID, err))
			continue
		}
		if err := t.InsertRowSource(RowSource{
			EntityTable:     "type_material",
			EntityID:        fmt.Sprintf("%d", rowid),
			ReferenceID:     referenceID,
			ExtractionRunID: extractionRunID,
			CreatedBy:       actor,
		}); err != nil {
			return nil, err
		}
		result.TypeMaterialInserted++
	}

	// Distributions: keyed by taxonID directly (attach to taxon).
	for _, d := range tree.Distribution {
		targetTaxonID, ok := targetByKey[d.TaxonID.v]
		if !ok {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("distribution for %q: no mapping (skipped)", d.TaxonID))
			continue
		}
		if _, err := t.getTaxonExists(ctx, targetTaxonID); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("distribution for %q → taxon %q: %v",
					d.TaxonID, targetTaxonID, err))
			continue
		}
		rowid, err := t.insertDistributionFromCompose(targetTaxonID, referenceID, d)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("insert distribution for %q: %v", d.TaxonID, err))
			continue
		}
		if err := t.InsertRowSource(RowSource{
			EntityTable:     "distribution",
			EntityID:        fmt.Sprintf("%d", rowid),
			ReferenceID:     referenceID,
			ExtractionRunID: extractionRunID,
			CreatedBy:       actor,
		}); err != nil {
			return nil, err
		}
		result.DistributionInserted++
	}

	// Vernaculars: also taxon-keyed.
	for _, v := range tree.VernacularName {
		targetTaxonID, ok := targetByKey[v.TaxonID.v]
		if !ok {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("vernacular for %q: no mapping (skipped)", v.TaxonID))
			continue
		}
		if _, err := t.getTaxonExists(ctx, targetTaxonID); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("vernacular for %q → taxon %q: %v",
					v.TaxonID, targetTaxonID, err))
			continue
		}
		rowid, err := t.insertVernacularFromCompose(targetTaxonID, referenceID, v)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("insert vernacular for %q: %v", v.TaxonID, err))
			continue
		}
		if err := t.InsertRowSource(RowSource{
			EntityTable:     "vernacular",
			EntityID:        fmt.Sprintf("%d", rowid),
			ReferenceID:     referenceID,
			ExtractionRunID: extractionRunID,
			CreatedBy:       actor,
		}); err != nil {
			return nil, err
		}
		result.VernacularInserted++
	}

	if err := t.MarkPaperImportSynced(referenceID, time.Now().UTC()); err != nil {
		return nil, err
	}

	return result, nil
}

// composeTreeShape mirrors the compose_v2 output structure just
// enough to walk the three extension arrays. Deliberately loose
// on the wrapper fields — every {value, sources} pair is decoded
// as composeValue so unwrap is uniform.
type composeTreeShape struct {
	TypeMaterial   []composeTypeMaterial `json:"type_material"`
	Distribution   []composeDistribution `json:"distribution"`
	VernacularName []composeVernacular   `json:"vernacular_name"`
}

type composeValue struct {
	Value   any      `json:"value,omitempty"`
	Sources []string `json:"sources,omitempty"`
}

// stringVal extracts the value as a string. compose emits numeric
// values (year, altitude) as either strings or numbers depending
// on the model; both cases collapse to the string form here.
func (c composeValue) stringVal() string {
	if c.Value == nil {
		return ""
	}
	switch v := c.Value.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type composeTypeMaterial struct {
	NameID          string       `json:"nameID"`
	Status          composeValue `json:"status"`
	Sex             composeValue `json:"sex"`
	InstitutionCode composeValue `json:"institutionCode"`
	InstitutionName composeValue `json:"institutionName"`
	CatalogNumber   composeValue `json:"catalogNumber"`
	Country         composeValue `json:"country"`
	Locality        composeValue `json:"locality"`
	Latitude        composeValue `json:"latitude"`
	Longitude       composeValue `json:"longitude"`
	Altitude        composeValue `json:"altitude"`
	Date            composeValue `json:"date"`
	VerbatimDate    composeValue `json:"verbatimDate"`
	Collector       composeValue `json:"collector"`
	Host            composeValue `json:"host"`
	Remarks         composeValue `json:"remarks"`
}

type composeDistribution struct {
	TaxonID composeString `json:"taxonID"`
	Area    composeValue  `json:"area"`
	AreaID  composeValue  `json:"areaID"`
	Status  composeValue  `json:"status"`
}

type composeVernacular struct {
	TaxonID  composeString `json:"taxonID"`
	Name     composeValue  `json:"name"`
	Language composeValue  `json:"language"`
	Country  composeValue  `json:"country"`
}

// composeString accepts either a bare string ("taxonID": "hive:foo")
// or a wrapped {value, sources} object, since prompts sometimes
// wrap the routing field and sometimes leave it bare.
type composeString struct{ v string }

func (s *composeString) UnmarshalJSON(b []byte) error {
	// Try bare string first.
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		s.v = str
		return nil
	}
	// Fall through to wrapped shape.
	var wrapped composeValue
	if err := json.Unmarshal(b, &wrapped); err != nil {
		return err
	}
	s.v = wrapped.stringVal()
	return nil
}

// TaxonID / NameID as strings when accessed by the compose walker.
func (d composeDistribution) key() string { return d.TaxonID.v }
func (v composeVernacular) key() string   { return v.TaxonID.v }

// Method-based access for the maps built above — mirrors what the
// executor does inline. Kept as functions for future refactoring.
func (t *Tx) insertTypeMaterialFromCompose(
	nameID, referenceID string, tm composeTypeMaterial,
) (int64, error) {
	m := coldp.TypeMaterial{
		NameID:          nameID,
		ReferenceID:     referenceID,
		InstitutionCode: tm.InstitutionCode.stringVal(),
		CatalogNumber:   tm.CatalogNumber.stringVal(),
		Country:         tm.Country.stringVal(),
		Locality:        tm.Locality.stringVal(),
		Date:            firstNonEmpty(tm.VerbatimDate.stringVal(), tm.Date.stringVal()),
		Collector:       tm.Collector.stringVal(),
		Host:            tm.Host.stringVal(),
		Remarks:         tm.Remarks.stringVal(),
	}
	if v := tm.Status.stringVal(); v != "" {
		m.Status = coldp.NewTypeStatus(v)
	}
	if v := tm.Sex.stringVal(); v != "" {
		m.Sex = coldp.NewSex(v)
	}
	return t.AddTypeMaterial(m)
}

func (t *Tx) insertDistributionFromCompose(
	taxonID, referenceID string, d composeDistribution,
) (int64, error) {
	dist := coldp.Distribution{
		TaxonID:     taxonID,
		ReferenceID: referenceID,
		Area:        d.Area.stringVal(),
		AreaID:      d.AreaID.stringVal(),
	}
	return t.AddDistribution(dist)
}

func (t *Tx) insertVernacularFromCompose(
	taxonID, referenceID string, v composeVernacular,
) (int64, error) {
	vn := coldp.Vernacular{
		TaxonID:     taxonID,
		ReferenceID: referenceID,
		Name:        v.Name.stringVal(),
		Language:    v.Language.stringVal(),
		Country:     v.Country.stringVal(),
	}
	return t.AddVernacular(vn)
}

func (t *Tx) deleteExtensionRow(table, id string) error {
	// Rowids are ints; SQLite accepts them as strings in the WHERE
	// clause but be explicit to avoid confusion. Only whitelisted
	// tables to prevent injection via a hive__row_source row that
	// somehow carried a bad table name.
	switch table {
	case "type_material":
		_, err := t.tx.ExecContext(t.ctx,
			`DELETE FROM type_material WHERE rowid = ?`, id)
		return err
	case "distribution":
		_, err := t.tx.ExecContext(t.ctx,
			`DELETE FROM distribution WHERE rowid = ?`, id)
		return err
	case "vernacular":
		_, err := t.tx.ExecContext(t.ctx,
			`DELETE FROM vernacular WHERE rowid = ?`, id)
		return err
	default:
		return fmt.Errorf("unknown extension table %q", table)
	}
}

// resolveTaxonNameID looks up the name_id (accepted name row) for
// a taxon UUID. Type material attaches to a NAME row, not a taxon,
// so the executor needs this hop.
func (t *Tx) resolveTaxonNameID(ctx context.Context, taxonID string) (string, error) {
	tx, err := t.tx.QueryContext(ctx,
		`SELECT col__name_id FROM taxon WHERE col__id = ?`, taxonID)
	if err != nil {
		return "", fmt.Errorf("resolve name for taxon %s: %w", taxonID, err)
	}
	defer tx.Close()
	if !tx.Next() {
		return "", fmt.Errorf("taxon %s not found", taxonID)
	}
	var nameID string
	if err := tx.Scan(&nameID); err != nil {
		return "", err
	}
	if nameID == "" {
		return "", fmt.Errorf("taxon %s has no col__name_id", taxonID)
	}
	return nameID, nil
}

// getTaxonExists is a lightweight existence check.
func (t *Tx) getTaxonExists(ctx context.Context, taxonID string) (bool, error) {
	row := t.tx.QueryRowContext(ctx,
		`SELECT 1 FROM taxon WHERE col__id = ?`, taxonID)
	var one int
	err := row.Scan(&one)
	if err != nil {
		return false, fmt.Errorf("taxon %s not found", taxonID)
	}
	return true, nil
}

// listPaperImportMappings is the tx-scoped variant of
// Archive.ListPaperImportMappings (which uses the outer db handle).
// The executor needs to see rows visible within its own tx.
func (t *Tx) listPaperImportMappings(
	ctx context.Context, referenceID string,
) ([]PaperImportMapping, error) {
	const q = `SELECT
		reference_id, paper_name_key, target_taxon_id,
		match_mode, COALESCE(match_confidence, 0),
		COALESCE(last_synced_at, ''),
		created_at, updated_at, created_by
	FROM hive__paper_import
	WHERE reference_id = ?
	ORDER BY created_at ASC, paper_name_key ASC`
	rows, err := t.tx.QueryContext(ctx, q, referenceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PaperImportMapping
	for rows.Next() {
		m, err := scanPaperImportMapping(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
