package hive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// License vocabulary — matches the CHECK constraint on
// hive__reference_license.license. Curators pick from this
// controlled set; "unknown" is the safe default when the
// license genuinely can't be verified.
const (
	LicenseCCBY           = "CC-BY"
	LicenseCCBYSA         = "CC-BY-SA"
	LicenseCCBYNC         = "CC-BY-NC"
	LicenseCC0            = "CC0"
	LicensePD             = "PD"
	LicensePDBHL          = "PD-BHL"
	LicenseOtherVerified  = "other-verified"
	LicenseUnknown        = "unknown"
)

// ReferenceLicense is one row of hive__reference_license — the
// verified open-access license for a reference, with the
// verification's source URL and audit-trail metadata.
type ReferenceLicense struct {
	ReferenceID string
	License     string
	SourceURL   string
	VerifiedAt  time.Time
	VerifiedBy  string
	UpdatedAt   time.Time
}

// validLicense reports whether s is a recognized license
// vocabulary value.
func validLicense(s string) bool {
	switch s {
	case LicenseCCBY, LicenseCCBYSA, LicenseCCBYNC,
		LicenseCC0, LicensePD, LicensePDBHL,
		LicenseOtherVerified, LicenseUnknown:
		return true
	}
	return false
}

// SetReferenceLicense upserts the license row for a reference.
// license must be one of the controlled-vocabulary values.
// verified_at is stamped now when license != "unknown"; a
// re-verify with the same license bumps updated_at but preserves
// verified_at (the first time the license was affirmed).
func (t *Tx) SetReferenceLicense(rl ReferenceLicense) error {
	if rl.ReferenceID == "" {
		return errors.New("core: SetReferenceLicense: reference_id required")
	}
	if rl.License == "" {
		rl.License = LicenseUnknown
	}
	if !validLicense(rl.License) {
		return fmt.Errorf("core: SetReferenceLicense: %w: unknown license %q",
			ErrValidation, rl.License)
	}
	if rl.VerifiedBy == "" {
		rl.VerifiedBy = ActorFromContext(t.ctx)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var verifiedAt any
	if rl.License != LicenseUnknown {
		if rl.VerifiedAt.IsZero() {
			verifiedAt = now
		} else {
			verifiedAt = rl.VerifiedAt.Format(time.RFC3339Nano)
		}
	}
	// ON CONFLICT preserves verified_at on the DO UPDATE branch
	// so the audit-trail carries the first affirmation timestamp,
	// not the most recent bump. updated_at reflects the most
	// recent write.
	const q = `INSERT INTO hive__reference_license
	    (reference_id, license, source_url, verified_at, verified_by, updated_at)
	    VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(reference_id) DO UPDATE SET
	    license     = excluded.license,
	    source_url  = excluded.source_url,
	    verified_by = excluded.verified_by,
	    updated_at  = excluded.updated_at`
	if _, err := t.tx.ExecContext(t.ctx, q,
		rl.ReferenceID, rl.License, rl.SourceURL, verifiedAt, rl.VerifiedBy, now,
	); err != nil {
		return fmt.Errorf("core: upsert hive__reference_license: %w", err)
	}
	return nil
}

// GetReferenceLicense returns the license row for a reference, or
// [ErrNotFound] when no row exists (which the caller should
// treat as "unknown").
func (a *Archive) GetReferenceLicense(ctx context.Context, referenceID string) (*ReferenceLicense, error) {
	if referenceID == "" {
		return nil, errors.New("core: GetReferenceLicense: reference_id required")
	}
	row := a.db.QueryRowContext(ctx, `SELECT
	    reference_id, license, source_url,
	    COALESCE(verified_at, ''), verified_by, updated_at
	    FROM hive__reference_license WHERE reference_id = ?`, referenceID)
	rl, err := scanReferenceLicense(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("core: license for reference %q: %w", referenceID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return rl, nil
}

// ListReferenceLicenses returns every recorded license. Used by
// the OA-corpus dashboard to show license coverage across the
// archive.
func (a *Archive) ListReferenceLicenses(ctx context.Context) ([]ReferenceLicense, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT
	    reference_id, license, source_url,
	    COALESCE(verified_at, ''), verified_by, updated_at
	    FROM hive__reference_license
	    ORDER BY reference_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("core: list reference licenses: %w", err)
	}
	defer rows.Close()
	var out []ReferenceLicense
	for rows.Next() {
		rl, err := scanReferenceLicense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rl)
	}
	return out, rows.Err()
}

func scanReferenceLicense(r rowScanner) (*ReferenceLicense, error) {
	var rl ReferenceLicense
	var verifiedAt, updatedAt string
	if err := r.Scan(
		&rl.ReferenceID, &rl.License, &rl.SourceURL,
		&verifiedAt, &rl.VerifiedBy, &updatedAt,
	); err != nil {
		return nil, err
	}
	if verifiedAt != "" {
		rl.VerifiedAt, _ = time.Parse(time.RFC3339Nano, verifiedAt)
	}
	rl.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &rl, nil
}
