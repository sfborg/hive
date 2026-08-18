package hive

import (
	"context"
	"fmt"
	"time"

	"github.com/sfborg/sflib/pkg/coldp"
)

// name_relation is a pure link table: (name_id, related_name_id,
// type_id) plus reference / page / remarks / audit. No surrogate ID
// column — a "relation" is identified by its two endpoints + type.
// Writes therefore accept a coldp.NameRelation and return no id.
//
// Semantics per CoLDP: NameID is the subject; RelatedNameID is the
// object; Type describes the relationship the subject has TO the
// object. For a subsequent combination "Panthera onca" whose basionym
// is "Felis onca":
//
//   NameID        = the "Panthera onca" name row
//   RelatedNameID = the "Felis onca" name row (basionym)
//   Type          = BASIONYM
//
// sfga's nom_rel_type enum uses BASIONYM as the umbrella term for both
// botanical "basionym" and zoological "original combination" — the UI
// can present code-scoped labels while the storage stays uniform.

// LinkNameRelation writes a name_relation row. Fields on n:
//
//   - NameID (required): the subject name.
//   - RelatedNameID (required): the object name.
//   - Type (required): a NomRelType enum value. Empty ID is rejected —
//     an untyped relation has no meaning.
//   - ReferenceID (optional): the reference that describes this
//     nomenclatural act.
//   - Page, Remarks, SourceID: optional.
//
// col__modified is stamped with the current time; col__modified_by
// comes from the tx's actor context (WithActor).
//
// Callers wanting a whole basionym-add flow (create the basionym name +
// link it as a synonym of the current accepted taxon + write the
// relation) should do all three inside a single WithTx so partial
// failure rolls back cleanly.
func (t *Tx) LinkNameRelation(n coldp.NameRelation) error {
	if n.NameID == "" {
		return fmt.Errorf("core: link name relation: %w: NameID required", ErrValidation)
	}
	if n.RelatedNameID == "" {
		return fmt.Errorf("core: link name relation: %w: RelatedNameID required", ErrValidation)
	}
	if n.Type.ID() == "" {
		return fmt.Errorf("core: link name relation: %w: Type required", ErrValidation)
	}
	if n.NameID == n.RelatedNameID {
		// Self-relations are nonsensical for every current NomRelType
		// and almost always a caller bug. Reject early with a clear
		// message instead of writing a row nobody wants.
		return fmt.Errorf("core: link name relation: %w: NameID and RelatedNameID must differ",
			ErrValidation)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	const insert = `INSERT INTO name_relation (
		col__name_id, col__related_name_id, col__source_id,
		col__type_id, tw__name_relationship_type,
		col__page, col__reference_id,
		col__remarks, col__modified, col__modified_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := t.tx.ExecContext(t.ctx, insert,
		n.NameID, n.RelatedNameID, nullIfEmpty(n.SourceID),
		n.Type.ID(), n.TwNameRelationshipType,
		n.Page, nullIfEmpty(n.ReferenceID),
		n.Remarks, now, t.actor,
	)
	if err != nil {
		return fmt.Errorf("core: insert name_relation %s→%s: %w",
			n.NameID, n.RelatedNameID, err)
	}
	return nil
}

// ListNameRelations returns every name_relation row where the given
// name is either the subject or the object. Used by the detail-view
// pane to surface "basionym: X" and "has-basionym-of: Y" links.
//
// The projection carries the counterpart name id (whichever side isn't
// `id`) plus a `direction` marker so callers can render the relation
// naturally: "→" when `id` is the subject, "←" when it's the object.
// RowID is the addressable handle for update / delete calls — sfga's
// name_relation has no col__id column, so hive uses SQLite's implicit
// rowid (same pattern as vernacular / distribution).
func (a *Archive) ListNameRelations(ctx context.Context, id string) ([]NameRelationHit, error) {
	const q = `SELECT
		rowid,
		col__name_id, col__related_name_id, col__type_id,
		COALESCE(col__reference_id, ''), col__page, col__remarks
	FROM name_relation
	WHERE col__name_id = ? OR col__related_name_id = ?
	ORDER BY col__type_id`
	rows, err := a.db.QueryContext(ctx, q, id, id)
	if err != nil {
		return nil, fmt.Errorf("core: list name_relation for %s: %w", id, err)
	}
	defer rows.Close()

	var out []NameRelationHit
	for rows.Next() {
		var (
			rowid                                           int64
			nameID, relatedID, typeID, refID, page, remarks string
		)
		if err := rows.Scan(&rowid, &nameID, &relatedID, &typeID, &refID, &page, &remarks); err != nil {
			return nil, fmt.Errorf("core: scan name_relation: %w", err)
		}
		hit := NameRelationHit{
			RowID:       rowid,
			Type:        typeID,
			ReferenceID: refID,
			Page:        page,
			Remarks:     remarks,
		}
		if nameID == id {
			hit.CounterpartID = relatedID
			hit.Direction = "outgoing"
		} else {
			hit.CounterpartID = nameID
			hit.Direction = "incoming"
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("core: iterate name_relation: %w", err)
	}
	return out, nil
}

// UnlinkNameRelation removes the row at rowid. Unknown rowid →
// ErrNotFound so the handler surfaces a 404 rather than silently
// no-op'ing. Mirrors the vernacular / distribution delete shape;
// callers wanting to "change the type of an existing relation" do
// unlink + Link (the row's identity IS its type + endpoints, so
// updating any of those is semantically a new row).
func (t *Tx) UnlinkNameRelation(rowid int64) error {
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM name_relation WHERE rowid = ?`, rowid,
	)
	if err != nil {
		return fmt.Errorf("core: unlink name_relation %d: %w", rowid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("core: unlink name_relation %d rows affected: %w", rowid, err)
	}
	if n == 0 {
		return fmt.Errorf("core: unlink name_relation %d: %w", rowid, ErrNotFound)
	}
	return nil
}

// NameRelationHit is the projection returned by ListNameRelations. It
// carries the counterpart name id (never `id` itself) plus a
// direction marker so the caller can render "this name → basionym X"
// vs. "this name ← recombined-into Y" without another query. RowID is
// the opaque handle for update / delete calls (see UnlinkNameRelation).
type NameRelationHit struct {
	RowID         int64
	CounterpartID string
	Type          string // nom_rel_type ID (BASIONYM, SPELLING_CORRECTION, …)
	Direction     string // "outgoing" (id is subject) or "incoming" (id is object)
	ReferenceID   string
	Page          string
	Remarks       string
}
