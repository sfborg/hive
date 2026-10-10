package hive

import (
	"context"
	"fmt"
)

// FocalGroup is one of the archive's focal taxonomic groups: a taxon of
// the archive the dataset is about. Groups are picked from the
// archive's own taxa, so names, ranks and vernacular names are always
// read through the taxon and stay current. An exporter can expand each
// into an EML taxonomicClassification with this archive as the
// taxonId provider.
type FocalGroup struct {
	TaxonID     string
	Name        string // scientific name, without authorship
	Authorship  string
	Rank        string
	Vernaculars []FocalVernacular
}

// FocalVernacular is a vernacular name of a focal group.
type FocalVernacular struct {
	Name     string
	Language string
}

// FocalTaxonomicGroups returns the archive's focal taxonomic groups in
// their saved order. Groups whose taxon no longer exists are skipped.
func (a *Archive) FocalTaxonomicGroups(ctx context.Context) ([]FocalGroup, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT f.taxon_id, COALESCE(n.col__scientific_name, ''), COALESCE(n.col__authorship, ''),
			COALESCE(n.col__rank_id, '')
		FROM hive__focal_taxonomic_group f
		JOIN taxon t ON t.col__id = f.taxon_id
		LEFT JOIN name n ON n.col__id = t.col__name_id
		ORDER BY f.ordinal, f.taxon_id`)
	if err != nil {
		return nil, fmt.Errorf("core: focal taxonomic groups: %w", err)
	}
	var out []FocalGroup
	for rows.Next() {
		var g FocalGroup
		if err := rows.Scan(&g.TaxonID, &g.Name, &g.Authorship, &g.Rank); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		vrows, err := a.db.QueryContext(ctx, `
			SELECT col__name, COALESCE(col__language, '') FROM vernacular
			WHERE col__taxon_id = ? ORDER BY col__preferred DESC, col__language, col__name`,
			out[i].TaxonID)
		if err != nil {
			return nil, fmt.Errorf("core: focal group vernaculars: %w", err)
		}
		for vrows.Next() {
			var v FocalVernacular
			if err := vrows.Scan(&v.Name, &v.Language); err != nil {
				vrows.Close()
				return nil, err
			}
			out[i].Vernaculars = append(out[i].Vernaculars, v)
		}
		vrows.Close()
	}
	return out, nil
}

// SetFocalTaxonomicGroups replaces the focal taxonomic groups with the
// given taxa, in order. Every ID must be a taxon of the archive. The
// reference taxon summaries depend on the groups (see
// chooseHomonymRows), so they are cleared for recomputation.
func (t *Tx) SetFocalTaxonomicGroups(taxonIDs []string) error {
	metadataID := 1
	if err := t.tx.QueryRowContext(t.ctx,
		`SELECT col__id FROM metadata ORDER BY col__id LIMIT 1`).Scan(&metadataID); err != nil {
		return fmt.Errorf("core: focal groups: %w: archive has no metadata", ErrValidation)
	}
	seen := map[string]bool{}
	for _, id := range taxonIDs {
		if seen[id] {
			return fmt.Errorf("core: focal groups: %w: taxon %s listed twice", ErrValidation, id)
		}
		seen[id] = true
		var n int
		if err := t.tx.QueryRowContext(t.ctx,
			`SELECT COUNT(*) FROM taxon WHERE col__id = ?`, id).Scan(&n); err != nil {
			return fmt.Errorf("core: focal groups: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("core: focal groups: %w: no taxon %s", ErrValidation, id)
		}
	}
	if _, err := t.tx.ExecContext(t.ctx, `DELETE FROM hive__focal_taxonomic_group`); err != nil {
		return fmt.Errorf("core: clear focal groups: %w", err)
	}
	for i, id := range taxonIDs {
		if _, err := t.tx.ExecContext(t.ctx, `
			INSERT INTO hive__focal_taxonomic_group (col__metadata_id, taxon_id, ordinal)
			VALUES (?, ?, ?)`, metadataID, id, i); err != nil {
			return fmt.Errorf("core: store focal group %s: %w", id, err)
		}
	}
	if _, err := t.tx.ExecContext(t.ctx, `DELETE FROM hive__reference_taxon_summary`); err != nil {
		return fmt.Errorf("core: clear taxon summaries: %w", err)
	}
	if err := touchMetadata(t.ctx, t.tx, metadataID); err != nil {
		return err
	}
	t.markMetadataDirty(metadataID)
	return nil
}

// focalNames returns the names that identify the focal groups in a
// name source's classification: each group's scientific name and the
// names of its synonyms, as canonical forms where parsed.
func focalNames(tx *Tx) (map[string]bool, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name)
		FROM hive__focal_taxonomic_group f
		JOIN taxon t ON t.col__id = f.taxon_id
		JOIN name n ON n.col__id = t.col__name_id
		UNION
		SELECT COALESCE(NULLIF(n.gn__canonical_simple, ''), n.col__scientific_name)
		FROM hive__focal_taxonomic_group f
		JOIN synonym s ON s.col__taxon_id = f.taxon_id
		JOIN name n ON n.col__id = s.col__name_id`)
	if err != nil {
		return nil, fmt.Errorf("core: focal names: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name != "" {
			out[name] = true
		}
	}
	return out, rows.Err()
}
