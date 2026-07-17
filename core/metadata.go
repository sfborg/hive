package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Metadata is the dataset-level metadata row (title, description,
// license, logo, private flag, …). Hive-authored because sflib doesn't
// expose a coldp.Metadata type — the sfga `metadata` table is a
// hive-facing concept for now.
//
// Sfga models it as an INTEGER-primary-key table that other tables
// reference via col__metadata_id (defaulting to 1). Convention across
// the ecosystem is one row per archive; hive follows that and treats
// the metadata row as a singleton — GetMetadata returns the id=1 row
// (or the sole row present, if any). Seeded on Create so the default
// FK references from taxon / name / etc. resolve cleanly.
//
// Confidence / Completeness map SQL NULL into an *int on the Go side
// so unset (never scored) stays distinguishable from 0 (worst score).
// Private is a tri-state (nil / true / false) for the same reason.
type Metadata struct {
	ID              int
	DOI             string
	Title           string
	Alias           string
	Description     string
	Issued          string
	Version         string
	Keywords        string
	GeographicScope string
	TaxonomicScope  string
	TemporalScope   string
	Confidence      *int
	Completeness    *int
	License         string
	URL             string
	Logo            string
	Label           string
	Citation        string
	Private         *bool
}

// GetMetadata returns the archive's single metadata row (id=1 by
// convention). Returns ErrNotFound when no row exists — callers on
// legacy archives should treat that as "prompt curator to seed one."
// Every hive-created archive is seeded on Create so fresh archives
// always have a row.
func (a *Archive) GetMetadata(ctx context.Context) (*Metadata, error) {
	const q = `SELECT
		col__id, col__doi, col__title, col__alias, col__description,
		col__issued, col__version, col__keywords,
		col__geographic_scope, col__taxonomic_scope, col__temporal_scope,
		col__confidence, col__completeness,
		col__license, col__url, col__logo, col__label, col__citation,
		col__private
	FROM metadata
	ORDER BY col__id
	LIMIT 1`

	var (
		m         Metadata
		confidenc sql.NullInt64
		completes sql.NullInt64
		private   sql.NullBool
	)
	err := a.db.QueryRowContext(ctx, q).Scan(
		&m.ID, &m.DOI, &m.Title, &m.Alias, &m.Description,
		&m.Issued, &m.Version, &m.Keywords,
		&m.GeographicScope, &m.TaxonomicScope, &m.TemporalScope,
		&confidenc, &completes,
		&m.License, &m.URL, &m.Logo, &m.Label, &m.Citation,
		&private,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("core: metadata: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("core: get metadata: %w", err)
	}
	if confidenc.Valid {
		v := int(confidenc.Int64)
		m.Confidence = &v
	}
	if completes.Valid {
		v := int(completes.Int64)
		m.Completeness = &v
	}
	if private.Valid {
		v := private.Bool
		m.Private = &v
	}
	return &m, nil
}

// UpdateMetadata writes a metadata row. If m.ID is 0 the row is either
// inserted (when no rows exist) or the existing sole row's fields are
// replaced (when one exists) — callers using the singleton convention
// don't need to know the id.
//
// Title is required by the sfga schema (NOT NULL); an empty title
// returns ErrValidation.
func (t *Tx) UpdateMetadata(m Metadata) error {
	if m.Title == "" {
		return fmt.Errorf("core: update metadata: %w: title is required", ErrValidation)
	}
	// Resolve target id: caller-supplied wins; else use existing single
	// row's id; else INSERT a new one.
	targetID := m.ID
	if targetID == 0 {
		if err := t.tx.QueryRowContext(t.ctx,
			"SELECT col__id FROM metadata ORDER BY col__id LIMIT 1").Scan(&targetID); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("core: metadata target id: %w", err)
			}
			// No row yet — INSERT.
			return t.insertMetadata(m)
		}
	}
	const update = `UPDATE metadata SET
		col__doi = ?, col__title = ?, col__alias = ?, col__description = ?,
		col__issued = ?, col__version = ?, col__keywords = ?,
		col__geographic_scope = ?, col__taxonomic_scope = ?, col__temporal_scope = ?,
		col__confidence = ?, col__completeness = ?,
		col__license = ?, col__url = ?, col__logo = ?, col__label = ?, col__citation = ?,
		col__private = ?
	WHERE col__id = ?`
	res, err := t.tx.ExecContext(t.ctx, update,
		m.DOI, m.Title, m.Alias, m.Description,
		m.Issued, m.Version, m.Keywords,
		m.GeographicScope, m.TaxonomicScope, m.TemporalScope,
		nullableIntPtr(m.Confidence), nullableIntPtr(m.Completeness),
		m.License, m.URL, m.Logo, m.Label, m.Citation,
		nullableBoolPtr(m.Private),
		targetID,
	)
	if err != nil {
		return fmt.Errorf("core: update metadata: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: update metadata rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("core: update metadata id=%d: %w", targetID, ErrNotFound)
	}
	return nil
}

func (t *Tx) insertMetadata(m Metadata) error {
	const insert = `INSERT INTO metadata (
		col__doi, col__title, col__alias, col__description,
		col__issued, col__version, col__keywords,
		col__geographic_scope, col__taxonomic_scope, col__temporal_scope,
		col__confidence, col__completeness,
		col__license, col__url, col__logo, col__label, col__citation,
		col__private
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := t.tx.ExecContext(t.ctx, insert,
		m.DOI, m.Title, m.Alias, m.Description,
		m.Issued, m.Version, m.Keywords,
		m.GeographicScope, m.TaxonomicScope, m.TemporalScope,
		nullableIntPtr(m.Confidence), nullableIntPtr(m.Completeness),
		m.License, m.URL, m.Logo, m.Label, m.Citation,
		nullableBoolPtr(m.Private),
	)
	if err != nil {
		return fmt.Errorf("core: insert metadata: %w", err)
	}
	return nil
}

// seedMetadata inserts a placeholder metadata row on hive Create so the
// col__metadata_id=1 FK defaults on taxon/name/etc. resolve. Called
// from Create() only — Open() never touches this. The placeholder
// title is "Untitled archive" and can be edited by the curator; the
// row's id will be 1 as long as no other rows have been added.
func seedMetadata(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata").Scan(&count); err != nil {
		return fmt.Errorf("core: seed metadata: count: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO metadata (col__title) VALUES (?)",
		"Untitled archive",
	)
	if err != nil {
		return fmt.Errorf("core: seed metadata: insert: %w", err)
	}
	return nil
}

// nullableIntPtr / nullableBoolPtr translate *T → sql-friendly
// nullable value. Non-nil pointer writes the value; nil writes NULL.
func nullableIntPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableBoolPtr(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
