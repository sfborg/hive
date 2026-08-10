package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
)

// Pagination model: opaque base64-encoded offset strings. The value is
// deliberately opaque so we can migrate to keyset pagination later without
// changing the wire contract. Callers should treat `next_cursor` as a
// bag-of-bytes, never parse it.
//
// v0 payload: base64(text-form int). Encoding for the range 0..N is
// straightforward, and the check-vs-payload prevents callers from crafting
// enormous offsets that would DoS the DB.

const (
	defaultPageSize = 100
	maxPageSize     = 500
	maxOffset       = 1_000_000 // sanity cap — bigger than any realistic sfga tree branch
)

// decodeCursor turns a "next_cursor" query param back into the row offset it
// represents. Empty cursor means "start from row 0".
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("cursor is not valid base64url: %w", err)
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, fmt.Errorf("cursor payload is not a number: %w", err)
	}
	if n < 0 {
		return 0, errors.New("cursor payload is negative")
	}
	if n > maxOffset {
		return 0, fmt.Errorf("cursor payload exceeds max offset %d", maxOffset)
	}
	return n, nil
}

// encodeCursor produces the wire-form cursor for the given offset. The
// caller decides when a next cursor is meaningful (e.g., only when the
// current page filled all `limit` slots); if the caller passes 0 the
// helper returns "" so pagination is short-circuited at the boundary.
func encodeCursor(offset int) string {
	if offset <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// clampPageSize normalizes ?limit=. Zero or negative → defaultPageSize; too
// large → maxPageSize. This is a hive server-side policy, not a schema
// constraint.
func clampPageSize(v int) int {
	if v <= 0 {
		return defaultPageSize
	}
	if v > maxPageSize {
		return maxPageSize
	}
	return v
}
