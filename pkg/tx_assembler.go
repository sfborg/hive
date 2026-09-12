package hive

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sfborg/hive/pkg/assembler"
	"github.com/sfborg/sflib/pkg/coldp"
)

// This file implements the assembler.Reader and assembler.Writer
// interfaces on *Tx so the reassemble orchestrator can hand the
// transaction directly to pkg/assembler.
//
// See pkg/assembler for the interface contracts. The bulk of the
// interesting SQL lives in existing helpers (CreateName,
// CreateTaxon, AddSynonym, FindImplicitReferenceTaxa) — this file
// mostly adapts them to the assembler's action shape.

// Compile-time assertions that *Tx satisfies both surfaces.
var (
	_ assembler.Reader = (*Tx)(nil)
	_ assembler.Writer = (*Tx)(nil)
)

// ListAllTaxaForAssembler projects every taxon row (excluding
// soft-deleted) into the assembler's ExistingTaxon shape.
func (t *Tx) ListAllTaxaForAssembler() ([]assembler.ExistingTaxon, error) {
	const q = `SELECT
		    t.col__id, t.col__name_id, COALESCE(t.col__parent_id, ''),
		    COALESCE(n.gn__canonical_full, ''),
		    COALESCE(n.col__authorship, ''),
		    COALESCE(n.col__rank_id, '')
		FROM taxon t
		LEFT JOIN name n ON n.col__id = t.col__name_id`
	rows, err := t.tx.QueryContext(t.ctx, q)
	if err != nil {
		return nil, fmt.Errorf("core: assembler list taxa: %w", err)
	}
	defer rows.Close()
	var out []assembler.ExistingTaxon
	for rows.Next() {
		var e assembler.ExistingTaxon
		if err := rows.Scan(
			&e.TaxonID, &e.NameID, &e.ParentTaxonID,
			&e.Canonical, &e.Authorship, &e.Rank,
		); err != nil {
			return nil, fmt.Errorf("core: assembler scan taxon: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindImplicitReferenceCandidates resolves the subject taxon's
// name_id and delegates to FindImplicitReferenceTaxa, matching the
// same complementary-phrase lookup conceptimport uses.
func (t *Tx) FindImplicitReferenceCandidates(subjectTaxonID, phrase string) ([]string, error) {
	if subjectTaxonID == "" {
		return nil, errors.New("core: FindImplicitReferenceCandidates: subjectTaxonID required")
	}
	nameID, err := t.resolveTaxonNameID(t.ctx, subjectTaxonID)
	if err != nil {
		return nil, err
	}
	return t.FindImplicitReferenceTaxa(nameID, phrase)
}

// CreateTaxonForAssembler mints a name row from the action's
// (canonical, authorship, rank) triple, then a taxon row referring
// to it under the given parent. Provenance columns
// (col__source_id, col__reference_id) carry the source paper id
// so the taxon is attributable to the assembler run.
func (t *Tx) CreateTaxonForAssembler(a assembler.CreateTaxonAction, referenceID string) (string, error) {
	if a.Canonical == "" {
		return "", errors.New("core: CreateTaxonForAssembler: canonical required")
	}
	sci := buildScientificName(a.Canonical, a.Authorship)
	nameID, err := t.CreateName(coldp.Name{
		ScientificName: sci,
		Rank:           ParseRank(a.Rank),
	})
	if err != nil {
		return "", fmt.Errorf("core: assembler create name: %w", err)
	}
	tx := coldp.Taxon{
		NameID:      nameID,
		ParentID:    a.ParentTaxonID,
		Provisional: provisionalFromStatus(a.Status),
		ReferenceID: referenceID,
	}
	return t.CreateTaxon(tx)
}

// CreateSynonymForAssembler mints a name row for the synonym and
// links it to the accepted taxon.
func (t *Tx) CreateSynonymForAssembler(a assembler.CreateSynonymAction, referenceID string) (string, error) {
	if a.AcceptedTaxonID == "" || a.Canonical == "" {
		return "", errors.New("core: CreateSynonymForAssembler: acceptedTaxonID + canonical required")
	}
	sci := buildScientificName(a.Canonical, a.Authorship)
	nameID, err := t.CreateName(coldp.Name{ScientificName: sci})
	if err != nil {
		return "", fmt.Errorf("core: assembler create synonym name: %w", err)
	}
	syn := coldp.Synonym{
		TaxonID:     a.AcceptedTaxonID,
		NameID:      nameID,
		Status:      coldp.NewTaxonomicStatus(a.Status),
		ReferenceID: referenceID,
	}
	return t.AddSynonym(syn)
}

// CreateBracketedTaxonForAssembler applies the `[Aus] bus`
// convention: builds a scientific name with the bracketed genus,
// creates the name + taxon rows, and marks the taxon
// provisionally accepted.
func (t *Tx) CreateBracketedTaxonForAssembler(a assembler.BracketAction, referenceID string) (string, error) {
	if a.AnchorTaxonID == "" || a.BracketedGenus == "" || a.Species == "" {
		return "", errors.New("core: CreateBracketedTaxonForAssembler: anchor / genus / species required")
	}
	canonical := a.BracketedGenus + " " + a.Species
	sci := buildScientificName(canonical, a.Authorship)
	nameID, err := t.CreateName(coldp.Name{
		ScientificName: sci,
		Rank:           ParseRank(a.Rank),
	})
	if err != nil {
		return "", fmt.Errorf("core: assembler create bracket name: %w", err)
	}
	tx := coldp.Taxon{
		NameID:      nameID,
		ParentID:    a.AnchorTaxonID,
		Provisional: sql.NullBool{Valid: true, Bool: true},
		ReferenceID: referenceID,
	}
	return t.CreateTaxon(tx)
}

// RecordAssemblerFlag logs the flag for now. Persistent storage
// (hive__assembler_flag) is planned but not yet in place — the
// AssembleReport returned by ExecuteReassemble already carries the
// full flag list, so callers can surface them to curators via the
// HTTP / CLI response.
func (t *Tx) RecordAssemblerFlag(f assembler.FlagAction, prov assembler.ActionProvenance) error {
	slog.Info("assembler.flag",
		"kind", f.Kind,
		"message", f.Message,
		"reference_id", prov.ReferenceID,
		"mention_id", prov.MentionID,
		"candidates", f.Candidates,
	)
	return nil
}

// buildScientificName concatenates canonical and authorship with
// a single space between when both are non-empty. Trims stray
// whitespace so `Aus bus  L., 1970` never appears.
func buildScientificName(canonical, authorship string) string {
	c := strings.TrimSpace(canonical)
	a := strings.TrimSpace(authorship)
	if a == "" {
		return c
	}
	return c + " " + a
}

// provisionalFromStatus maps the assembler's status string
// (usually "accepted", occasionally "provisionally accepted") to
// the CoLDP Provisional bool. Anything containing "provisional"
// is treated as provisional; everything else is regular accepted.
func provisionalFromStatus(status string) sql.NullBool {
	if strings.Contains(strings.ToLower(status), "provisional") {
		return sql.NullBool{Valid: true, Bool: true}
	}
	return sql.NullBool{Valid: true, Bool: false}
}

// ReassembleReport is the result of ExecuteReassemble: the raw
// assembler.Report plus per-orchestrator counts (papers loaded,
// compose runs skipped, JSON parse errors).
type ReassembleReport struct {
	// PapersLoaded is the count of compose runs that produced a
	// PaperInput. Runs with empty raw_output_json or a missing
	// reference_id are skipped.
	PapersLoaded int

	// PapersSkipped counts compose runs that couldn't feed the
	// assembler (empty output, missing reference, JSON parse
	// error). Warnings carry the details.
	PapersSkipped int

	// Warnings collects orchestrator-level messages (parse
	// failures, missing references). Assembler-level flags land
	// in AssemblerReport.Flags.
	Warnings []string

	// AssemblerReport is the pkg/assembler.Report returned by
	// Apply.
	AssemblerReport assembler.Report
}

// ExecuteReassemble is the archive-wide entry point for Slice 1:
// load every compose extraction run in the archive, materialize
// PaperInputs, run assembler.Build, and apply the plan.
//
// Papers sort ascending by extraction_run.created_at with run_id
// as tiebreak, matching LITERATURE_VIEW.md § Determinism
// guarantees.
func (t *Tx) ExecuteReassemble(rules assembler.Rules) (*ReassembleReport, error) {
	papers, report, err := t.loadPapersForAssembler()
	if err != nil {
		return nil, err
	}
	existing, err := assembler.LoadExisting(t)
	if err != nil {
		return nil, err
	}
	plan := assembler.Build(papers, existing, rules)
	ar, err := assembler.Apply(t, t, plan)
	if err != nil {
		return nil, err
	}
	report.AssemblerReport = ar
	return report, nil
}

// loadPapersForAssembler walks hive__extraction_run for every
// compose run, parses the raw output for name_usages, and
// materializes PaperInputs.
func (t *Tx) loadPapersForAssembler() ([]assembler.PaperInput, *ReassembleReport, error) {
	report := &ReassembleReport{}
	const q = `SELECT run_id, COALESCE(reference_id, ''),
	                  COALESCE(raw_output_json, ''), created_at
	             FROM hive__extraction_run
	            WHERE purpose = 'compose'
	         ORDER BY created_at ASC, run_id ASC`
	rows, err := t.tx.QueryContext(t.ctx, q)
	if err != nil {
		return nil, nil, fmt.Errorf("core: load compose runs: %w", err)
	}
	defer rows.Close()
	var out []assembler.PaperInput
	for rows.Next() {
		var runID, refID, rawJSON, createdAtStr string
		if err := rows.Scan(&runID, &refID, &rawJSON, &createdAtStr); err != nil {
			return nil, nil, fmt.Errorf("core: scan compose run: %w", err)
		}
		if refID == "" || rawJSON == "" {
			report.PapersSkipped++
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("compose run %s skipped (no reference or output)", runID))
			continue
		}
		nus, err := decodeComposeNameUsages(rawJSON)
		if err != nil {
			report.PapersSkipped++
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("compose run %s: parse: %v", runID, err))
			continue
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, createdAtStr)
		out = append(out, assembler.PaperInput{
			ReferenceID:     refID,
			ExtractionRunID: runID,
			PaperCreatedAt:  createdAt,
			NameUsages:      nus,
		})
		report.PapersLoaded++
	}
	return out, report, rows.Err()
}

// decodeComposeNameUsages parses a compose-v4 raw_output_json blob
// and returns the assembler-shaped NameUsage slice. Reuses the
// composeTreeShape / composeNameUsage decoders already used by
// paper_import_exec so a single source of truth handles compose
// wrapper quirks.
//
// compose_v4 emits `scientificName` as the canonical name (sensu
// markers already stripped into `namePhrase` per the prompt), so
// no gnparser round-trip is needed to feed the assembler.
func decodeComposeNameUsages(rawJSON string) ([]assembler.NameUsage, error) {
	var tree composeTreeShape
	if err := json.Unmarshal([]byte(rawJSON), &tree); err != nil {
		return nil, err
	}
	out := make([]assembler.NameUsage, 0, len(tree.NameUsages))
	for _, nu := range tree.NameUsages {
		out = append(out, assembler.NameUsage{
			MentionID:         nu.ID,
			ParentMentionID:   nu.ParentID,
			Status:            nu.Status.stringVal(),
			Canonical:         nu.ScientificName.stringVal(),
			Authorship:        nu.Authorship.stringVal(),
			Rank:              nu.Rank.stringVal(),
			NamePhrase:        nu.NamePhrase.stringVal(),
			AccordingTo:       nu.accordingTo(),
			NomenclaturalNote: nu.NomenclaturalNote.stringVal(),
		})
	}
	return out, nil
}
