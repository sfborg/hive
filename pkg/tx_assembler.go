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

// RecordAssemblerFlag persists a flag into hive__assembler_flag.
// [ExecuteReassemble] clears the table at the start of each run so
// the persisted rows always reflect the current Layer 2 state.
//
// The flag also lands in the returned AssembleReport so callers
// that want to react immediately (HTTP/CLI) don't need a round
// trip through the table.
func (t *Tx) RecordAssemblerFlag(f assembler.FlagAction, prov assembler.ActionProvenance) error {
	candidatesJSON := "[]"
	if len(f.Candidates) > 0 {
		b, err := json.Marshal(f.Candidates)
		if err != nil {
			return fmt.Errorf("core: marshal assembler flag candidates: %w", err)
		}
		candidatesJSON = string(b)
	}
	// Debug-log flags too so operators tailing the server logs still
	// see them alongside the HTTP response.
	slog.Debug("assembler.flag",
		"kind", f.Kind.String(),
		"message", f.Message,
		"reference_id", prov.ReferenceID,
		"mention_id", prov.MentionID,
		"taxon_id", f.TaxonID,
		"candidates", f.Candidates,
	)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `INSERT INTO hive__assembler_flag (
	    kind, message, reference_id, mention_id, taxon_id,
	    candidates_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := t.tx.ExecContext(t.ctx, q,
		f.Kind.String(),
		f.Message,
		nullIfEmpty(prov.ReferenceID),
		nullIfEmpty(prov.MentionID),
		nullIfEmpty(f.TaxonID),
		candidatesJSON, now,
	); err != nil {
		return fmt.Errorf("core: insert hive__assembler_flag: %w", err)
	}
	return nil
}

// RecordAssemblerMapping writes a hive__paper_import row for an
// assembler-decided mention → taxon binding so downstream flows
// (per-paper extension attachment, the paper-level Assembly tab)
// don't need to re-derive the mapping. Curator-picked mappings
// take precedence: this method skips silently if a
// curator-picked row already exists for the same (referenceID,
// mentionID).
//
// authorshipConfirmed selects the match_mode:
//   true  → "auto-canonical-auth" (canonical + authorship match)
//   false → "auto-canonical" (canonical-only or authorship unknown)
func (t *Tx) RecordAssemblerMapping(
	referenceID, mentionID, targetTaxonID string,
	authorshipConfirmed bool,
) error {
	if referenceID == "" || mentionID == "" || targetTaxonID == "" {
		return errors.New("core: RecordAssemblerMapping: referenceID / mentionID / targetTaxonID all required")
	}
	matchMode := "auto-canonical"
	if authorshipConfirmed {
		matchMode = "auto-canonical-auth"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// ON CONFLICT clause preserves any curator-picked row; only
	// upgrades an existing auto-* row or inserts fresh. The WHERE
	// clause on the DO UPDATE branch is what enforces the "never
	// overwrite curator-picked" rule — SQLite runs the WHERE
	// after resolving the conflict.
	const q = `INSERT INTO hive__paper_import (
	    reference_id, paper_name_key, target_taxon_id,
	    match_mode, match_confidence, last_synced_at,
	    created_at, updated_at, created_by
	) VALUES (?, ?, ?, ?, NULL, NULL, ?, ?, ?)
	ON CONFLICT (reference_id, paper_name_key) DO UPDATE SET
	    target_taxon_id = excluded.target_taxon_id,
	    match_mode      = excluded.match_mode,
	    updated_at      = excluded.updated_at
	WHERE hive__paper_import.match_mode != 'curator-picked'`
	if _, err := t.tx.ExecContext(t.ctx, q,
		referenceID, mentionID, targetTaxonID,
		matchMode, now, now, t.actor,
	); err != nil {
		return fmt.Errorf("core: RecordAssemblerMapping(%s,%s): %w",
			referenceID, mentionID, err)
	}
	return nil
}

// DeleteAllAssemblerFlags clears hive__assembler_flag. Called at
// the start of each reassemble so the persisted table matches the
// current Layer 2 state — no history is kept here (extraction runs
// and Layer 1 assertions carry the durable audit trail).
func (t *Tx) DeleteAllAssemblerFlags() error {
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__assembler_flag`); err != nil {
		return fmt.Errorf("core: clear hive__assembler_flag: %w", err)
	}
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
	// Sweep prior flag rows so the persisted table matches the
	// current Layer 2 state — Apply's RecordAssemblerFlag calls
	// re-populate it as the run walks the plan.
	if err := t.DeleteAllAssemblerFlags(); err != nil {
		return nil, err
	}
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
	// Layer 3 diff pass. For each active overlay, compare its
	// claim against the just-materialized Layer 2 state and emit
	// FlagOrphanedOverlay / FlagOverlayMatchedByPublication as
	// appropriate. Flags land in both the persisted table and the
	// report so the HTTP response carries them without a
	// round-trip.
	l3Flags, err := t.emitLayer3DiffFlags()
	if err != nil {
		return nil, err
	}
	ar.Flags = append(ar.Flags, l3Flags...)
	report.AssemblerReport = ar
	return report, nil
}

// emitLayer3DiffFlags is the pass-3 (Layer 3 diff) helper. Loads
// active overlays and, for each one, either emits an orphan flag
// (taxon vanished) or a matched-by-publication flag (Layer 2 now
// agrees with the overlay's claim). Returns the flags emitted so
// the caller can append them to the assembler report.
func (t *Tx) emitLayer3DiffFlags() ([]assembler.FlagAction, error) {
	overlays, err := t.listActiveOverlays()
	if err != nil {
		return nil, err
	}
	var out []assembler.FlagAction
	for _, o := range overlays {
		flag, err := t.diffOverlay(o)
		if err != nil {
			return nil, err
		}
		if flag == nil {
			continue
		}
		if err := t.RecordAssemblerFlag(*flag, assembler.ActionProvenance{
			ReferenceID: flag.TaxonID, // no paper anchor here
		}); err != nil {
			return nil, err
		}
		out = append(out, *flag)
	}
	return out, nil
}

// diffOverlay applies the per-overlay-type diff and returns a
// flag (or nil for a no-op). Kept small — each overlay type gets
// its own small helper so future types graft on cleanly.
func (t *Tx) diffOverlay(o Layer3Overlay) (*assembler.FlagAction, error) {
	// Existence check first: a Layer 2 recompute may have removed
	// the taxon the overlay pointed at.
	exists, err := t.taxonExistsBool(o.TaxonID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &assembler.FlagAction{
			Kind: assembler.FlagOrphanedOverlay,
			Message: fmt.Sprintf("Layer 3 overlay %s references missing taxon %s",
				o.ID, o.TaxonID),
			TaxonID:    o.TaxonID,
			Candidates: []string{o.ID},
		}, nil
	}
	switch o.OverlayType {
	case OverlayTypeSynonymyOverride:
		return t.diffSynonymyOverride(o)
	case OverlayTypePlacementOverride:
		return t.diffPlacementOverride(o)
	case OverlayTypeConceptOverride:
		// TODO(slice-2 follow-up): concept-relation diff. Concept
		// overrides need a taxon_concept_relation lookup with
		// (taxon_id, related_taxon_id, rel_type_id) matching; the
		// reference id on the row gives the citation candidate.
		return nil, nil
	}
	return nil, nil
}

// diffSynonymyOverride checks a "keep as accepted" style overlay.
// Claim shape: {"status": "accepted"}. Agreement means the L2
// taxon's status is currently accepted (or provisionally
// accepted). Candidate reference is the taxon's own
// col__reference_id when populated.
func (t *Tx) diffSynonymyOverride(o Layer3Overlay) (*assembler.FlagAction, error) {
	var claim struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(o.ClaimJSON), &claim); err != nil {
		// Malformed claim → no diff, no flag. Curator can inspect
		// via GetOverlay.
		return nil, nil
	}
	if strings.ToLower(claim.Status) != "accepted" {
		return nil, nil
	}
	status, refID, err := t.readTaxonStatusAndRef(o.TaxonID)
	if err != nil {
		return nil, err
	}
	s := strings.ToUpper(status)
	if s == "" || s == "ACCEPTED" || s == "PROVISIONALLY_ACCEPTED" ||
		s == "VALID" || s == "PROVISIONALLY_VALID" {
		return &assembler.FlagAction{
			Kind: assembler.FlagOverlayMatchedByPublication,
			Message: fmt.Sprintf("Layer 2 now agrees the taxon is accepted (overlay %s)",
				o.ID),
			TaxonID:    o.TaxonID,
			Candidates: candidateRefs(o.ID, refID),
		}, nil
	}
	return nil, nil
}

// diffPlacementOverride checks a "belongs under parent X" style
// overlay. Claim shape: {"parent_taxon_id": "..."}. Agreement
// means the L2 taxon's col__parent_id == the claim's target.
func (t *Tx) diffPlacementOverride(o Layer3Overlay) (*assembler.FlagAction, error) {
	var claim struct {
		ParentTaxonID string `json:"parent_taxon_id"`
	}
	if err := json.Unmarshal([]byte(o.ClaimJSON), &claim); err != nil {
		return nil, nil
	}
	if claim.ParentTaxonID == "" {
		return nil, nil
	}
	parentID, refID, err := t.readTaxonParentAndRef(o.TaxonID)
	if err != nil {
		return nil, err
	}
	if parentID != claim.ParentTaxonID {
		return nil, nil
	}
	return &assembler.FlagAction{
		Kind: assembler.FlagOverlayMatchedByPublication,
		Message: fmt.Sprintf("Layer 2 now places the taxon under %s (overlay %s)",
			claim.ParentTaxonID, o.ID),
		TaxonID:    o.TaxonID,
		Candidates: candidateRefs(o.ID, refID),
	}, nil
}

// taxonExistsBool wraps getTaxonExists so a "not found" error
// becomes a false-return rather than propagating.
func (t *Tx) taxonExistsBool(taxonID string) (bool, error) {
	row := t.tx.QueryRowContext(t.ctx,
		`SELECT 1 FROM taxon WHERE col__id = ?`, taxonID)
	var one int
	if err := row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("core: taxonExistsBool(%s): %w", taxonID, err)
	}
	return true, nil
}

func (t *Tx) readTaxonStatusAndRef(taxonID string) (string, string, error) {
	row := t.tx.QueryRowContext(t.ctx,
		`SELECT COALESCE(col__status_id, ''), COALESCE(col__reference_id, '')
		   FROM taxon WHERE col__id = ?`, taxonID)
	var status, refID string
	if err := row.Scan(&status, &refID); err != nil {
		return "", "", fmt.Errorf("core: readTaxonStatusAndRef(%s): %w", taxonID, err)
	}
	return status, refID, nil
}

func (t *Tx) readTaxonParentAndRef(taxonID string) (string, string, error) {
	row := t.tx.QueryRowContext(t.ctx,
		`SELECT COALESCE(col__parent_id, ''), COALESCE(col__reference_id, '')
		   FROM taxon WHERE col__id = ?`, taxonID)
	var parentID, refID string
	if err := row.Scan(&parentID, &refID); err != nil {
		return "", "", fmt.Errorf("core: readTaxonParentAndRef(%s): %w", taxonID, err)
	}
	return parentID, refID, nil
}

// candidateRefs builds the flag's Candidates slice: [overlayID]
// alone when no reference id is available, [overlayID, refID]
// otherwise. The overlay id always leads so the UI can key on
// element 0 to identify the affected overlay.
func candidateRefs(overlayID, refID string) []string {
	if refID == "" {
		return []string{overlayID}
	}
	return []string{overlayID, refID}
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
