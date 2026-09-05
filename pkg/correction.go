package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sfborg/hive/pkg/correction"
)

// CreateCorrection inserts a new correction row. Generates a UUID
// when Correction.ID is empty. Timestamps are stamped server-side;
// callers don't need to populate CreatedAt / UpdatedAt.
//
// Applying the correction to the sidecar (mutating the JATS,
// re-running figextract, etc.) is a separate step — see
// Archive.ApplyCorrection. This split lets the HTTP handler write
// the row inside a transaction and then run the potentially-slow
// sidecar mutation outside the DB lock.
func (t *Tx) CreateCorrection(c *correction.Correction) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = correction.StatusActive
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.CreatedAt, _ = time.Parse(time.RFC3339, now)
	c.UpdatedAt = c.CreatedAt
	if c.CreatedBy == "" {
		c.CreatedBy = ActorFromContext(t.ctx)
	}

	bboxJSON := sql.NullString{}
	if c.BBox != nil {
		b, err := json.Marshal(c.BBox)
		if err != nil {
			return fmt.Errorf("core: marshal bbox: %w", err)
		}
		bboxJSON = sql.NullString{String: string(b), Valid: true}
	}
	anchorJSON := sql.NullString{}
	if c.AnchorPtr != nil {
		b, err := json.Marshal(c.AnchorPtr)
		if err != nil {
			return fmt.Errorf("core: marshal anchor_ptr: %w", err)
		}
		anchorJSON = sql.NullString{String: string(b), Valid: true}
	}
	bboxPage := sql.NullInt64{}
	if c.BBoxPage > 0 {
		bboxPage = sql.NullInt64{Int64: int64(c.BBoxPage), Valid: true}
	}

	targetID := sql.NullString{}
	if c.TargetElementID != "" {
		targetID = sql.NullString{String: c.TargetElementID, Valid: true}
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO hive__extraction_correction
		 (correction_id, reference_id, sidecar_sha, correction_kind,
		  content_type, bbox_page, bbox_json, label, anchor_ptr,
		  target_element_id, payload, status,
		  created_at, updated_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ReferenceID, c.SidecarSHA, string(c.Kind),
		c.ContentType, bboxPage, bboxJSON, c.Label, anchorJSON,
		targetID, c.Payload, string(c.Status),
		now, now, c.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("core: insert correction: %w", err)
	}
	return nil
}

// ListCorrectionsForReference returns every correction attached to
// the given reference in creation order. Both active and reverted
// rows are returned so the UI can show a "corrections that stopped
// applying" section after a re-digitize.
func (a *Archive) ListCorrectionsForReference(ctx context.Context, referenceID string) ([]correction.Correction, error) {
	const q = `
		SELECT correction_id, reference_id, sidecar_sha, correction_kind,
		       content_type, bbox_page, bbox_json, label, anchor_ptr,
		       target_element_id, payload, status, COALESCE(reverted_reason, ''),
		       created_at, updated_at, created_by
		FROM hive__extraction_correction
		WHERE reference_id = ?
		ORDER BY created_at`
	rows, err := a.db.QueryContext(ctx, q, referenceID)
	if err != nil {
		return nil, fmt.Errorf("core: list corrections for %s: %w", referenceID, err)
	}
	defer rows.Close()
	var out []correction.Correction
	for rows.Next() {
		c, err := scanCorrection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCorrection fetches a single correction row by id. Returns
// hive.ErrNotFound when the id doesn't exist.
func (a *Archive) GetCorrection(ctx context.Context, id string) (*correction.Correction, error) {
	const q = `
		SELECT correction_id, reference_id, sidecar_sha, correction_kind,
		       content_type, bbox_page, bbox_json, label, anchor_ptr,
		       target_element_id, payload, status, COALESCE(reverted_reason, ''),
		       created_at, updated_at, created_by
		FROM hive__extraction_correction
		WHERE correction_id = ?`
	row := a.db.QueryRowContext(ctx, q, id)
	c, err := scanCorrection(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("core: get correction %s: %w", id, err)
	}
	return &c, nil
}

// scanCorrection consumes one row from either a *sql.Rows or a
// *sql.Row into a correction.Correction. Uses the small scanner
// interface both types satisfy so the read-list and read-single
// helpers can share this code.
type sqlScanner interface {
	Scan(dest ...any) error
}

func scanCorrection(sc sqlScanner) (correction.Correction, error) {
	var (
		c          correction.Correction
		kind       string
		status     string
		bboxPage   sql.NullInt64
		bboxJSON   sql.NullString
		anchorJSON sql.NullString
		targetID   sql.NullString
		createdAt  string
		updatedAt  string
	)
	if err := sc.Scan(
		&c.ID, &c.ReferenceID, &c.SidecarSHA, &kind,
		&c.ContentType, &bboxPage, &bboxJSON, &c.Label, &anchorJSON,
		&targetID, &c.Payload, &status, &c.RevertedReason,
		&createdAt, &updatedAt, &c.CreatedBy,
	); err != nil {
		return c, err
	}
	c.Kind = correction.Kind(kind)
	c.Status = correction.Status(status)
	if bboxPage.Valid {
		c.BBoxPage = int(bboxPage.Int64)
	}
	if bboxJSON.Valid {
		var b correction.BBox
		if err := json.Unmarshal([]byte(bboxJSON.String), &b); err == nil {
			c.BBox = &b
		}
	}
	if anchorJSON.Valid {
		var ap correction.AnchorPtr
		if err := json.Unmarshal([]byte(anchorJSON.String), &ap); err == nil {
			c.AnchorPtr = &ap
		}
	}
	if targetID.Valid {
		c.TargetElementID = targetID.String
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return c, nil
}

// DeleteCorrection removes a correction row entirely. Prefer this
// over marking status='reverted' when the curator explicitly wants
// the correction gone (as opposed to the system marking it reverted
// because a re-digitize invalidated it).
//
// Sidecar re-mutation to un-apply the correction is the caller's
// responsibility — DeleteCorrection only touches the DB row.
func (t *Tx) DeleteCorrection(id string) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__extraction_correction WHERE correction_id = ?`, id)
	if err != nil {
		return fmt.Errorf("core: delete correction %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
