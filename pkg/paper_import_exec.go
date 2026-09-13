package hive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/hive/pkg/conceptimport"
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

	// Concept-import counters. NamePhraseWritten counts target
	// taxa whose col__name_phrase was populated (safe-mode: only
	// when previously empty). ConceptRelationsInserted counts
	// taxon_concept_relation rows added from bare
	// sensu-lato / sensu-stricto mentions whose implicit reference
	// concept was resolved to exactly one candidate taxon.
	// ImplicitReferenceMissing / Ambiguous count the two skip
	// outcomes so curators can see where the archive needs
	// additional context.
	NamePhraseWritten          int
	ConceptRelationsInserted   int
	ImplicitReferenceMissing   int
	ImplicitReferenceAmbiguous int

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

	if err := t.attachExtensionsForReference(
		ctx, referenceID, extractionRunID, &tree, targetByKey, result,
	); err != nil {
		return nil, err
	}

	// Concept-import: walk name_usages, populate namePhrase on
	// mapped target taxa, and insert taxon_concept_relation rows
	// for bare sensu-lato / sensu-stricto assertions whose
	// implicit reference concept resolves to exactly one candidate.
	//
	// Safe-mode: overwrite=false throughout so existing curator-set
	// concept fields are preserved. Named-author concept relations
	// (sensu <author>, auct. non <author>) require reference-row
	// resolution and are not written here — deferred to the
	// assimilate UI where the curator can confirm the reference.
	if err := t.importConceptFields(ctx, referenceID, targetByKey, tree.NameUsages, result); err != nil {
		return nil, err
	}

	if err := t.MarkPaperImportSynced(referenceID, time.Now().UTC()); err != nil {
		return nil, err
	}

	return result, nil
}

// importConceptFields walks the compose tree's name_usages and
// populates concept-alignment fields on mapped target taxa. Called
// from ExecutePaperImport after the extension-array walks so
// existing target taxa are already known to exist and haven't been
// swept by the delete-then-reinsert cycle (which only touches
// extension tables).
//
// Behavior per name_usage:
//
//   - No mapping in hive__paper_import → skip (no warning; the
//     extension-array walk already warned about the same mention
//     if it produced extensions).
//   - Empty namePhrase and no derivable relation → nothing to
//     write; skip silently.
//   - Non-empty namePhrase → SetTaxonNamePhrase with
//     overwrite=false. Silently preserves any existing curator-set
//     value.
//   - Derivable concept relation with a named author → skip for now
//     (reference-row resolution isn't wired yet); log for curator
//     follow-up via the assimilate UI.
//   - Derivable concept relation without a named author (bare
//     sensu lato / stricto) → look up implicit reference taxa via
//     FindImplicitReferenceTaxa. Zero candidates or many candidates
//     count in the result but insert no row. Exactly one candidate
//     inserts a taxon_concept_relation row.
func (t *Tx) importConceptFields(
	ctx context.Context,
	referenceID string,
	targetByKey map[string]string,
	nameUsages []composeNameUsage,
	result *ExecutePaperImportResult,
) error {
	for _, nu := range nameUsages {
		targetTaxonID, ok := targetByKey[nu.ID]
		if !ok {
			continue
		}
		phrase := nu.NamePhrase.stringVal()
		mc := conceptimport.MentionConcept{
			NamePhrase:        phrase,
			AccordingTo:       nu.accordingTo(),
			NomenclaturalNote: nu.NomenclaturalNote.stringVal(),
		}

		// Write the namePhrase onto the target taxon. Reference
		// resolution isn't wired yet, so pass empty referenceID —
		// WriteMention will skip the according-to write in that
		// case.
		if err := conceptimport.WriteMention(t, targetTaxonID, mc, "", false); err != nil {
			return err
		}
		if phrase != "" {
			result.NamePhraseWritten++
		}

		// Derive the implied concept relation and attempt implicit
		// resolution for bare sensu markers.
		derived := conceptimport.Derive(mc)
		if !derived.HasRelation() || derived.AgainstNamedAuthor {
			// Nothing more to do here — named-author relations
			// need reference resolution and are deferred to the
			// assimilate UI.
			continue
		}

		nameID, err := t.resolveTaxonNameID(ctx, targetTaxonID)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("concept relation for %q → taxon %q: %v",
					nu.ID, targetTaxonID, err))
			continue
		}
		candidates, err := t.FindImplicitReferenceTaxa(nameID, phrase)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("implicit reference lookup for %q: %v", nu.ID, err))
			continue
		}
		// The subject taxon itself might match the lookup (same
		// name, empty phrase); exclude it so we don't relate the
		// taxon to itself.
		filtered := candidates[:0]
		for _, id := range candidates {
			if id != targetTaxonID {
				filtered = append(filtered, id)
			}
		}
		var singleID string
		if len(filtered) == 1 {
			singleID = filtered[0]
		}
		outcome, rel := conceptimport.ResolveImplicitReference(
			derived, targetTaxonID, referenceID, "",
			len(filtered), singleID,
		)
		switch outcome {
		case conceptimport.ImplicitReferenceResolved:
			if err := t.InsertConceptRelation(rel); err != nil {
				return err
			}
			result.ConceptRelationsInserted++
		case conceptimport.ImplicitReferenceMissing:
			result.ImplicitReferenceMissing++
		case conceptimport.ImplicitReferenceAmbiguous:
			result.ImplicitReferenceAmbiguous++
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("concept relation for %q → ambiguous (%d candidates); skipped",
					nu.ID, len(filtered)))
		}
	}
	return nil
}

// composeTreeShape mirrors the compose_v2+ output structure just
// enough to walk the three extension arrays and the name_usages
// array (the latter added for compose_v4's concept-import fields:
// namePhrase, accordingTo, nomenclaturalNote). Deliberately loose
// on the wrapper fields — every {value, sources} pair is decoded
// as composeValue so unwrap is uniform.
type composeTreeShape struct {
	NameUsages     []composeNameUsage    `json:"name_usages"`
	TypeMaterial   []composeTypeMaterial `json:"type_material"`
	Distribution   []composeDistribution `json:"distribution"`
	VernacularName []composeVernacular   `json:"vernacular_name"`
}

// composeNameUsage is the compose-v4 name_usage shape restricted
// to the fields concept-import cares about. Other fields on the
// row (scientificName, authorship, rank, etc.) are handled by the
// backbone-write path when a curator creates the taxon/synonym;
// this struct exists so paper_import can walk name_usages for
// concept metadata without re-materializing the whole compose
// shape.
//
// AccordingToAuthor is the compose_v3 field name for what
// compose_v4 renamed to AccordingTo. Both are decoded so archives
// with a mix of v3 and v4 runs behave uniformly through the
// concept-import pipeline — accordingTo() returns whichever the
// producer populated. New callers should prefer accordingTo() over
// touching the fields directly.
type composeNameUsage struct {
	ID                string       `json:"id"`
	ParentID          string       `json:"parentID"`
	Status            composeValue `json:"status"`
	ScientificName    composeValue `json:"scientificName"`
	Authorship        composeValue `json:"authorship"`
	Rank              composeValue `json:"rank"`
	NamePhrase        composeValue `json:"namePhrase"`
	AccordingTo       composeValue `json:"accordingTo"`
	AccordingToAuthor composeValue `json:"accordingToAuthor"`
	NomenclaturalNote composeValue `json:"nomenclaturalNote"`
}

// accordingTo returns the compose_v4 accordingTo value when
// present, falling back to compose_v3's accordingToAuthor.
// Never both — compose emits one or the other per prompt version.
func (n composeNameUsage) accordingTo() string {
	if v := n.AccordingTo.stringVal(); v != "" {
		return v
	}
	return n.AccordingToAuthor.stringVal()
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

// attachExtensionsForReference is the shared extension-attachment
// step used by both ExecutePaperImport (per-paper import button)
// and ExecuteReassemble (project-level reassemble). Given the
// paper's compose tree and a mention → target-taxon map, it:
//
//  1. Sweeps prior hive__row_source rows for the reference and
//     deletes the corresponding extension rows (delete-then-
//     reinsert cycle, so re-runs are idempotent).
//  2. Walks the compose tree's type_material array, resolves each
//     mention to its target taxon's name_id, and inserts a
//     type_material row.
//  3. Walks the distribution array; each row attaches to the
//     mapped target taxon directly.
//  4. Walks the vernacular_name array; same shape as distribution.
//  5. Stamps hive__row_source rows for every insert so the
//     provenance chain (reference_id + extraction_run_id) is
//     preserved.
//
// Skips silently when a mention has no mapping — the caller
// (per-paper or project-level) decides whether that's a warning
// or expected behavior; both push the message into result.Warnings.
func (t *Tx) attachExtensionsForReference(
	ctx context.Context,
	referenceID, extractionRunID string,
	tree *composeTreeShape,
	targetByKey map[string]string,
	result *ExecutePaperImportResult,
) error {
	prior, err := t.DeleteRowSourcesForReference(referenceID)
	if err != nil {
		return err
	}
	for _, pair := range prior {
		table, id := pair[0], pair[1]
		if err := t.deleteExtensionRow(table, id); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("prior %s row %s: %v", table, id, err))
			continue
		}
		result.Removed++
	}

	actor := ActorFromContext(t.ctx)

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
			return err
		}
		result.TypeMaterialInserted++
	}

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
			return err
		}
		result.DistributionInserted++
	}

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
			return err
		}
		result.VernacularInserted++
	}

	return nil
}
