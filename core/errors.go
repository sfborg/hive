package core

import "errors"

var (
	// ErrNotFound is returned when a lookup fails because no row matches.
	// HTTP handlers map this to 404.
	ErrNotFound = errors.New("core: not found")

	// ErrValidation is returned for user-visible validation failures.
	// HTTP handlers map this to 422 with per-field details when available.
	ErrValidation = errors.New("core: validation failed")

	// ErrConflict is returned for optimistic-concurrency mismatches
	// (If-Match / col__modified) and unique-constraint violations.
	// HTTP handlers map this to 409.
	ErrConflict = errors.New("core: conflict")

	// ErrReadOnly is returned when a write operation is attempted against
	// an archive opened with the ReadOnly option (or one whose underlying
	// file is read-only on disk). HTTP handlers map this to 403.
	ErrReadOnly = errors.New("core: archive is read-only")

	// ErrExists is returned by Create when the target path already exists.
	ErrExists = errors.New("core: archive already exists")
)
