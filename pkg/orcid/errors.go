package orcid

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors for cheap matching with [errors.Is]. The concrete
// error struct types (e.g. [NotFoundError]) carry structured detail;
// use [errors.As] to reach it.
var (
	ErrNotFound     = errors.New("orcid: not found")
	ErrInvalidID    = errors.New("orcid: invalid iD")
	ErrRateLimited  = errors.New("orcid: rate limited")
	ErrBadRequest   = errors.New("orcid: bad request")
	ErrUnauthorized = errors.New("orcid: unauthorized")
	ErrServer       = errors.New("orcid: server error")
	ErrConfig       = errors.New("orcid: config error")
)

// NotFoundError is returned for HTTP 404s from ORCID. It matches
// [ErrNotFound] via [errors.Is].
type NotFoundError struct {
	ID       string
	Endpoint string
}

// Error implements the error interface.
func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("orcid: %s not found: %s", e.Endpoint, e.ID)
	}
	return fmt.Sprintf("orcid: %s not found", e.Endpoint)
}

// Is reports whether target is [ErrNotFound].
func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// InvalidIDError is returned when an input string is not a
// syntactically valid ORCID iD (wrong length, wrong character set, or
// MOD 11-2 checksum mismatch). Matches [ErrInvalidID] via [errors.Is].
type InvalidIDError struct {
	Input  string
	Reason string
}

// Error implements the error interface.
func (e *InvalidIDError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("orcid: invalid iD %q: %s", e.Input, e.Reason)
	}
	return fmt.Sprintf("orcid: invalid iD %q", e.Input)
}

// Is reports whether target is [ErrInvalidID].
func (e *InvalidIDError) Is(target error) bool { return target == ErrInvalidID }

// RateLimitedError is returned when ORCID responds with 429.
// RetryAfter carries the parsed Retry-After header value (zero if
// absent or unparseable). Matches [ErrRateLimited] via [errors.Is].
type RateLimitedError struct {
	RetryAfter time.Duration
	Body       string
}

// Error implements the error interface.
func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("orcid: rate limited (retry after %s)", e.RetryAfter)
	}
	return "orcid: rate limited"
}

// Is reports whether target is [ErrRateLimited].
func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

// BadRequestError wraps a 400 from ORCID, usually caused by a
// malformed search query. Message holds the server-supplied
// explanation when present. Matches [ErrBadRequest] via [errors.Is].
type BadRequestError struct {
	Message  string
	Endpoint string
}

// Error implements the error interface.
func (e *BadRequestError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("orcid: bad request to %s: %s", e.Endpoint, e.Message)
	}
	return fmt.Sprintf("orcid: bad request to %s", e.Endpoint)
}

// Is reports whether target is [ErrBadRequest].
func (e *BadRequestError) Is(target error) bool { return target == ErrBadRequest }

// UnauthorizedError covers 401 and 403. The public API endpoints used
// by [Client.Lookup] and [Client.Search] do not require credentials,
// so this typically indicates a record whose visibility is restricted
// (401), or an IP-level block (403). Matches [ErrUnauthorized] via
// [errors.Is].
type UnauthorizedError struct {
	Message string
	Status  int
}

// Error implements the error interface.
func (e *UnauthorizedError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("orcid: unauthorized (%d): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("orcid: unauthorized (%d)", e.Status)
}

// Is reports whether target is [ErrUnauthorized].
func (e *UnauthorizedError) Is(target error) bool { return target == ErrUnauthorized }

// ServerError represents a 5xx from ORCID. Callers may retry with
// backoff. Matches [ErrServer] via [errors.Is].
type ServerError struct {
	Status int
	Body   string
}

// Error implements the error interface.
func (e *ServerError) Error() string {
	return fmt.Sprintf("orcid: server error %d", e.Status)
}

// Is reports whether target is [ErrServer].
func (e *ServerError) Is(target error) bool { return target == ErrServer }

// ConfigError is returned by client construction when its inputs don't
// add up (e.g., an http:// BaseURL without OptAllowInsecure). Matches
// [ErrConfig] via [errors.Is].
type ConfigError struct {
	Msg string
}

// Error implements the error interface.
func (e *ConfigError) Error() string { return "orcid: config: " + e.Msg }

// Is reports whether target is [ErrConfig].
func (e *ConfigError) Is(target error) bool { return target == ErrConfig }
