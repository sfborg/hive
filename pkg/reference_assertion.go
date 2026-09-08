package hive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReferenceAssertion is one row of hive__reference_assertion — a
// per-fact staging record awaiting curator review before promotion
// to canonical SFGA rows. Populated either by LLM output
// materialization (via [Tx.MaterializeAssertionsFromRun]) or by
// curator entry.
//
// The pair (source_run_id + asserted_by) is what makes multi-view
// tree rendering + comparison work — see [[assertions-as-source-of-truth]]
// and [[extraction-runs-multi-model-schema]] memories.
type ReferenceAssertion struct {
	AssertionID   string
	ReferenceID   string
	AssertionType string
	Payload       string // JSON blob
	SourcePtr     string // JSON blob (anchor triple, char offsets, ...)
	Origin        string // 'model' | 'curator' (matches existing CHECK)
	Status        string // 'pending' | 'accepted' | 'rejected' | 'edited'
	RejectReason  string
	ImportedAt    time.Time // non-zero when promoted to SFGA rows
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CreatedBy     string // ORCID or synthetic (e.g., "llm-run:<id>")
	SourceRunID   string // link to hive__extraction_run.run_id (empty for curator)
	AssertedBy    string // view filter (see memory)
}

// CreateAssertion inserts a row. Mints AssertionID + stamps
// CreatedAt / UpdatedAt when unset.
func (t *Tx) CreateAssertion(a ReferenceAssertion) (string, error) {
	if a.ReferenceID == "" {
		return "", errors.New("core: CreateAssertion: reference_id is required")
	}
	if a.AssertionType == "" {
		return "", errors.New("core: CreateAssertion: assertion_type is required")
	}
	if a.Origin == "" {
		return "", errors.New("core: CreateAssertion: origin is required (model | curator)")
	}
	if a.Payload == "" {
		a.Payload = "{}"
	}
	if a.Status == "" {
		a.Status = "pending"
	}
	if a.AssertionID == "" {
		a.AssertionID = uuid.NewString()
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}
	const q = `INSERT INTO hive__reference_assertion
		(assertion_id, reference_id, assertion_type, payload,
		 source_ptr, origin, status, reject_reason, imported_at,
		 created_at, updated_at, created_by, source_run_id, asserted_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := t.tx.ExecContext(t.ctx, q,
		a.AssertionID, a.ReferenceID, a.AssertionType, a.Payload,
		a.SourcePtr, a.Origin, a.Status,
		nullableText(a.RejectReason),
		nullableTime(a.ImportedAt),
		a.CreatedAt.Format(time.RFC3339Nano),
		a.UpdatedAt.Format(time.RFC3339Nano),
		a.CreatedBy,
		nullableText(a.SourceRunID),
		nullableText(a.AssertedBy),
	)
	if err != nil {
		return "", fmt.Errorf("core: insert hive__reference_assertion: %w", err)
	}
	return a.AssertionID, nil
}

// GetAssertion returns one row by ID. Returns [ErrNotFound] when
// nothing matches.
func (a *Archive) GetAssertion(ctx context.Context, id string) (*ReferenceAssertion, error) {
	row := a.db.QueryRowContext(ctx, assertionSelectSQL+` WHERE assertion_id = ?`, id)
	ra, err := scanAssertion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("core: assertion %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return ra, nil
}

// AssertionListFilter narrows [Archive.ListAssertions]. Zero-value
// fields are ignored — an empty filter returns everything for the
// reference, oldest first.
type AssertionListFilter struct {
	ReferenceID string   // required — assertions are always ref-scoped
	Status      []string // when set, restricts to matching statuses
	SourceRunID string   // when set, restricts to one run
	AssertedBy  string   // when set, restricts to one asserter (view filter)
}

// ListAssertions returns rows matching the filter, oldest first
// (so curator review flows through assertions in the order they
// were extracted, matching source order in the paper).
func (a *Archive) ListAssertions(ctx context.Context, f AssertionListFilter) ([]ReferenceAssertion, error) {
	if f.ReferenceID == "" {
		return nil, errors.New("core: ListAssertions: reference_id filter is required")
	}
	q := assertionSelectSQL + ` WHERE reference_id = ?`
	args := []any{f.ReferenceID}
	if len(f.Status) > 0 {
		q += ` AND status IN (` + strings.Repeat("?,", len(f.Status)-1) + `?)`
		for _, s := range f.Status {
			args = append(args, s)
		}
	}
	if f.SourceRunID != "" {
		q += ` AND source_run_id = ?`
		args = append(args, f.SourceRunID)
	}
	if f.AssertedBy != "" {
		q += ` AND asserted_by = ?`
		args = append(args, f.AssertedBy)
	}
	q += ` ORDER BY created_at ASC, assertion_id ASC`
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("core: list assertions: %w", err)
	}
	defer rows.Close()
	var out []ReferenceAssertion
	for rows.Next() {
		ra, err := scanAssertion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ra)
	}
	return out, rows.Err()
}

// UpdateAssertionStatus flips an assertion's status (accept /
// reject / edit) with an optional reject_reason. Stamps
// updated_at. Used by the curator review UI.
func (t *Tx) UpdateAssertionStatus(id, status, rejectReason string) error {
	if id == "" {
		return errors.New("core: UpdateAssertionStatus: id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE hive__reference_assertion
		 SET status = ?, reject_reason = ?, updated_at = ?
		 WHERE assertion_id = ?`,
		status, nullableText(rejectReason), now, id,
	)
	if err != nil {
		return fmt.Errorf("core: update assertion status %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("core: assertion %q: %w", id, ErrNotFound)
	}
	return nil
}

// MaterializeAssertionsFromRun parses the run's raw_output_json
// and inserts one hive__reference_assertion row per extracted
// fact, tagged with source_run_id = run.RunID and asserted_by =
// "llm-run:<run.RunID>". Returns the count of inserted rows.
//
// Currently supports purpose = "ner" (parses ner_v5 output shape:
// {assertions: [...], tag_reviews: [...]}). Compose materialization
// is deferred — compose emits a tree structure with per-field
// {value, sources} provenance rather than individual assertion
// records; folding that into hive__reference_assertion is its own
// design conversation.
//
// Idempotent by (source_run_id): callers can safely re-run
// materialization; existing assertions for the run are cleared and
// re-inserted so a re-materialize after prompt tuning doesn't
// leave orphans. Curator status edits ARE lost on re-materialize
// — callers should warn before that.
func (t *Tx) MaterializeAssertionsFromRun(ctx context.Context, run ExtractionRun) (int, error) {
	if run.RunID == "" {
		return 0, errors.New("core: MaterializeAssertionsFromRun: run_id is required")
	}
	if run.Purpose != "ner" {
		// Compose materialization not implemented yet — see doc.
		return 0, nil
	}
	if run.ReferenceID == "" {
		// Ad-hoc runs without a paper have no place to file
		// assertions. Skip silently rather than error — the run
		// still persists with its raw output.
		return 0, nil
	}
	if run.RawOutputJSON == "" {
		return 0, nil
	}

	// Clear any previous assertions from this run so re-materialize
	// is a straight-line replace (see idempotency note).
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM hive__reference_assertion WHERE source_run_id = ?`,
		run.RunID,
	); err != nil {
		return 0, fmt.Errorf("core: clear prior run assertions: %w", err)
	}

	var out nerOutput
	if err := json.Unmarshal([]byte(run.RawOutputJSON), &out); err != nil {
		return 0, fmt.Errorf("core: parse NER output: %w", err)
	}

	assertedBy := "llm-run:" + run.RunID
	createdBy := assertedBy
	inserted := 0
	for _, a := range out.Assertions {
		payload, err := json.Marshal(a)
		if err != nil {
			return inserted, fmt.Errorf("core: marshal assertion payload: %w", err)
		}
		var sourcePtr string
		if a.Anchor != nil {
			b, _ := json.Marshal(a.Anchor)
			sourcePtr = string(b)
		}
		if _, err := t.CreateAssertion(ReferenceAssertion{
			ReferenceID:   run.ReferenceID,
			AssertionType: coalesceStr(a.Type, "unknown"),
			Payload:       string(payload),
			SourcePtr:     sourcePtr,
			Origin:        "model",
			Status:        "pending",
			CreatedBy:     createdBy,
			SourceRunID:   run.RunID,
			AssertedBy:    assertedBy,
		}); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

// nerOutput mirrors the shape ner_v5 emits: an assertions array
// plus tag_reviews. Only assertions become hive__reference_assertion
// rows — tag_reviews are advisory (marks the curator sees on names
// GNfinder flagged); they don't become facts.
type nerOutput struct {
	Assertions []nerAssertion `json:"assertions"`
	// TagReviews are intentionally captured but not materialized —
	// they're display metadata, not extracted facts.
	TagReviews []map[string]any `json:"tag_reviews,omitempty"`
}

type nerAssertion struct {
	ID         string          `json:"id,omitempty"`
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Subject    string          `json:"subject,omitempty"`
	Evidence   string          `json:"evidence,omitempty"`
	Confidence string          `json:"confidence,omitempty"`
	Scope      string          `json:"scope,omitempty"`
	Anchor     json.RawMessage `json:"anchor,omitempty"`
	Notes      string          `json:"notes,omitempty"`
	// Passthrough for anything ner_v5 emits beyond the fields we
	// name — kept as raw JSON so a prompt evolution can add fields
	// without a code change.
	Extra map[string]any `json:"-"`
}

const assertionSelectSQL = `SELECT
	assertion_id, reference_id, assertion_type, payload,
	source_ptr, origin, status,
	COALESCE(reject_reason, ''),
	COALESCE(imported_at, ''),
	created_at, updated_at, created_by,
	COALESCE(source_run_id, ''),
	COALESCE(asserted_by, '')
FROM hive__reference_assertion`

func scanAssertion(r rowScanner) (*ReferenceAssertion, error) {
	var (
		ra                       ReferenceAssertion
		imported, created, updated string
	)
	if err := r.Scan(
		&ra.AssertionID, &ra.ReferenceID, &ra.AssertionType, &ra.Payload,
		&ra.SourcePtr, &ra.Origin, &ra.Status, &ra.RejectReason,
		&imported, &created, &updated, &ra.CreatedBy,
		&ra.SourceRunID, &ra.AssertedBy,
	); err != nil {
		return nil, err
	}
	if imported != "" {
		if t, err := time.Parse(time.RFC3339Nano, imported); err == nil {
			ra.ImportedAt = t
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		ra.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		ra.UpdatedAt = t
	}
	return &ra, nil
}

// nullableTime returns nil for zero times, ISO8601 string otherwise.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

// coalesceStr picks the first non-empty of the two strings.
func coalesceStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
