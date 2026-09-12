package hive

import (
	"errors"
	"fmt"
	"time"

	"github.com/sfborg/hive/pkg/conceptimport"
)

// This file implements the conceptimport.Writer interface on
// *Tx so callers can hand the transaction directly to
// pkg/conceptimport helpers. Kept narrow — each method touches
// exactly the columns conceptimport needs and stamps
// col__modified / col__modified_by from the transaction's actor.
//
// See pkg/conceptimport for the interface contract and semantics
// (overwrite flag, empty-referenceID handling, per-mention
// synonym addressing via (col__id, col__taxon_id)).

// Compile-time assertion that *Tx satisfies the Writer contract.
// Fails the build if either surface drifts, catching desync at
// build time instead of runtime.
var _ conceptimport.Writer = (*Tx)(nil)

// SetTaxonNamePhrase writes taxon.col__name_phrase for taxonID.
// See conceptimport.Writer for overwrite semantics.
func (t *Tx) SetTaxonNamePhrase(taxonID, namePhrase string, overwrite bool) error {
	if taxonID == "" {
		return errors.New("core: SetTaxonNamePhrase: taxonID required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := `UPDATE taxon
	         SET col__name_phrase = ?,
	             col__modified = ?, col__modified_by = ?
	       WHERE col__id = ?`
	if !overwrite {
		q += ` AND (col__name_phrase IS NULL OR col__name_phrase = '')`
	}
	res, err := t.tx.ExecContext(t.ctx, q, namePhrase, now, t.actor, taxonID)
	if err != nil {
		return fmt.Errorf("core: SetTaxonNamePhrase(%s): %w", taxonID, err)
	}
	// A zero-row result with overwrite=false is expected (existing
	// value preserved) — not an error. A zero-row result with
	// overwrite=true means the taxon doesn't exist.
	if overwrite {
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("core: SetTaxonNamePhrase(%s): %w", taxonID, ErrNotFound)
		}
	}
	t.markTaxonDirty(taxonID)
	return nil
}

// SetTaxonAccordingTo writes taxon.col__according_to_id for
// taxonID. Empty referenceID with overwrite=false is a no-op.
func (t *Tx) SetTaxonAccordingTo(taxonID, referenceID string, overwrite bool) error {
	if taxonID == "" {
		return errors.New("core: SetTaxonAccordingTo: taxonID required")
	}
	if referenceID == "" && !overwrite {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := `UPDATE taxon
	         SET col__according_to_id = ?,
	             col__modified = ?, col__modified_by = ?
	       WHERE col__id = ?`
	if !overwrite {
		q += ` AND (col__according_to_id IS NULL OR col__according_to_id = '')`
	}
	res, err := t.tx.ExecContext(t.ctx, q,
		nullIfEmpty(referenceID), now, t.actor, taxonID)
	if err != nil {
		return fmt.Errorf("core: SetTaxonAccordingTo(%s): %w", taxonID, err)
	}
	if overwrite {
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("core: SetTaxonAccordingTo(%s): %w", taxonID, ErrNotFound)
		}
	}
	t.markTaxonDirty(taxonID)
	return nil
}

// SetSynonymNamePhrase writes synonym.col__name_phrase for the
// (col__id, col__taxon_id) pair — pro-parte synonyms have
// multiple rows sharing col__id, and the phrase applies to a
// specific synonym-under-taxon usage, so both keys are required.
func (t *Tx) SetSynonymNamePhrase(synonymID, taxonID, namePhrase string, overwrite bool) error {
	if synonymID == "" || taxonID == "" {
		return errors.New("core: SetSynonymNamePhrase: synonymID and taxonID required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := `UPDATE synonym
	         SET col__name_phrase = ?,
	             col__modified = ?, col__modified_by = ?
	       WHERE col__id = ? AND col__taxon_id = ?`
	if !overwrite {
		q += ` AND (col__name_phrase IS NULL OR col__name_phrase = '')`
	}
	res, err := t.tx.ExecContext(t.ctx, q,
		namePhrase, now, t.actor, synonymID, taxonID)
	if err != nil {
		return fmt.Errorf("core: SetSynonymNamePhrase(%s,%s): %w", synonymID, taxonID, err)
	}
	if overwrite {
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("core: SetSynonymNamePhrase(%s,%s): %w", synonymID, taxonID, ErrNotFound)
		}
	}
	return nil
}

// SetSynonymAccordingTo mirrors SetTaxonAccordingTo for the
// (col__id, col__taxon_id) synonym row.
func (t *Tx) SetSynonymAccordingTo(synonymID, taxonID, referenceID string, overwrite bool) error {
	if synonymID == "" || taxonID == "" {
		return errors.New("core: SetSynonymAccordingTo: synonymID and taxonID required")
	}
	if referenceID == "" && !overwrite {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := `UPDATE synonym
	         SET col__according_to_id = ?,
	             col__modified = ?, col__modified_by = ?
	       WHERE col__id = ? AND col__taxon_id = ?`
	if !overwrite {
		q += ` AND (col__according_to_id IS NULL OR col__according_to_id = '')`
	}
	res, err := t.tx.ExecContext(t.ctx, q,
		nullIfEmpty(referenceID), now, t.actor, synonymID, taxonID)
	if err != nil {
		return fmt.Errorf("core: SetSynonymAccordingTo(%s,%s): %w", synonymID, taxonID, err)
	}
	if overwrite {
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("core: SetSynonymAccordingTo(%s,%s): %w", synonymID, taxonID, ErrNotFound)
		}
	}
	return nil
}

// FindImplicitReferenceTaxa returns taxon ids that could serve as
// the reference concept for a sensu-lato / sensu-stricto assertion
// on a name. When the paper says "Aus bus sensu lato" without
// naming an author, the *implicit* reference concept is "the type-
// restricted (sensu stricto) form of the same name" — this method
// finds existing taxa in the archive that match that role.
//
// Semantics per the sensu-handler design:
//
//   - phrase = "sensu lato": look for accepted taxa with the same
//     nameID and (name_phrase = "" OR name_phrase = "sensu stricto").
//     Return candidates in taxon.col__id form.
//   - phrase = "sensu stricto": symmetric — same nameID and
//     (name_phrase = "" OR name_phrase = "sensu lato").
//   - other phrases: returns empty. Only bare sensu-lato / stricto
//     without a cited author has an implicit reference; sensu
//     <author> and auct. non <author> reference an explicit
//     concept the caller resolves via other means.
//
// The zero/one/many count is what the caller uses to decide what
// to do next — zero means no implicit reference to relate to,
// one is the clean case (auto-insert taxon_concept_relation),
// many means ambiguous (skip and log for curator review). This
// function makes no assumptions about that decision.
//
// Deleted taxa are excluded via the standard soft-delete filter.
func (t *Tx) FindImplicitReferenceTaxa(nameID, phrase string) ([]string, error) {
	if nameID == "" {
		return nil, errors.New("core: FindImplicitReferenceTaxa: nameID required")
	}
	var complementPhrase string
	switch phrase {
	case "sensu lato":
		complementPhrase = "sensu stricto"
	case "sensu stricto":
		complementPhrase = "sensu lato"
	default:
		return nil, nil
	}
	const q = `SELECT col__id
	             FROM taxon
	            WHERE col__name_id = ?
	              AND (col__name_phrase = '' OR col__name_phrase = ?)`
	rows, err := t.tx.QueryContext(t.ctx, q, nameID, complementPhrase)
	if err != nil {
		return nil, fmt.Errorf("core: FindImplicitReferenceTaxa(%s,%s): %w", nameID, phrase, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("core: FindImplicitReferenceTaxa scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// InsertConceptRelation adds a taxon_concept_relation row. The
// CoLDP schema doesn't declare uniqueness on this table so this
// implementation permits duplicate triples; deduplication belongs
// to callers with archive-level context (e.g., import pipelines
// that clear prior rows for a reference before re-inserting).
func (t *Tx) InsertConceptRelation(rel conceptimport.ConceptRelation) error {
	if err := rel.Validate(); err != nil {
		return fmt.Errorf("core: InsertConceptRelation: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `INSERT INTO taxon_concept_relation (
	    col__taxon_id, col__related_taxon_id, col__type_id,
	    col__reference_id, col__remarks,
	    col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := t.tx.ExecContext(t.ctx, q,
		rel.TaxonID, rel.RelatedTaxonID, rel.RelTypeID,
		nullIfEmpty(rel.ReferenceID), rel.Remarks,
		now, t.actor,
	); err != nil {
		return fmt.Errorf("core: InsertConceptRelation(%s,%s,%s): %w",
			rel.TaxonID, rel.RelatedTaxonID, rel.RelTypeID, err)
	}
	t.markTaxonDirty(rel.TaxonID)
	t.markTaxonDirty(rel.RelatedTaxonID)
	return nil
}
