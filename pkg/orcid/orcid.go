// Package orcid is a Go client for the ORCID public API
// (https://pub.orcid.org/v3.0). It resolves ORCID iDs to person
// records and searches by name for a picker UI.
//
// Example:
//
//	c, _ := orcid.New()
//	p, err := c.Lookup(ctx, "0000-0002-1825-0097")
//
// See package config for construction options and the top-level
// [Normalize] and [SanitizeSearchTerm] helpers.
//
// Auth: the two endpoints this package uses (personal-details,
// expanded-search) are on the public API host and require no
// credentials. If per-app rate limits are ever needed, a client
// credential can be added via a future Option without changing call
// sites — user OAuth is not required for read-public data.
//
// LIFT-TO-SFLIB / STANDALONE CANDIDATE. This package is written
// self-contained (no reach into hive-internal state, no singleton) so
// it can migrate into sflib or graduate to its own repository as an
// independent ORCID client library.
package orcid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfborg/hive/pkg/orcid/config"
)

// Client is the ORCID client. Concurrent-safe. Instantiate with [New].
type Client struct {
	cfg  config.Config
	http *http.Client
	log  *slog.Logger

	rlMu sync.Mutex
	rl   RateLimit
}

// RateLimit is the most recent rate-limit snapshot the server sent
// back. Zero-valued until a request completes. ORCID's public API
// does not currently expose the granular X-RateLimit-* headers that
// some other services do; when the server omits them, the fields
// stay zero.
type RateLimit struct {
	Limit     int
	Remaining int
	ResetAt   time.Time
	// Observed is the wall-clock time at which this snapshot was taken.
	Observed time.Time
}

// New constructs a Client from the supplied options. Returns
// [*ConfigError] (matching [ErrConfig]) when the resolved BaseURL
// uses http:// on a non-loopback host without OptAllowInsecure.
func New(opts ...config.Option) (*Client, error) {
	cfg := config.New(opts...)
	if err := checkInsecureBaseURL(cfg.BaseURL, cfg.AllowInsecure); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
		log: log,
	}, nil
}

// LastRateLimit returns the most recent rate-limit snapshot from the
// server. Zero-valued until the first successful request completes.
func (c *Client) LastRateLimit() RateLimit {
	c.rlMu.Lock()
	defer c.rlMu.Unlock()
	return c.rl
}

// get performs a GET against path with query params extra and decodes
// the JSON response into out. Errors are mapped to the package's
// structured error types.
func (c *Client) get(ctx context.Context, path string, extra url.Values, out any) error {
	full := c.cfg.BaseURL + path
	if len(extra) > 0 {
		full += "?" + extra.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("orcid: build request: %w", err)
	}
	// vnd.orcid+json is the documented Accept header; plain
	// application/json also works but is deprecated in ORCID docs.
	req.Header.Set("Accept", "application/vnd.orcid+json")
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("orcid: %s: %w", path, err)
	}
	defer resp.Body.Close()

	c.captureRateLimit(resp)

	body, err := readCapped(resp.Body, c.cfg.MaxResponseSize)
	if err != nil {
		return fmt.Errorf("orcid: read %s: %w", path, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return &NotFoundError{Endpoint: path}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &RateLimitedError{
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Body:       string(body),
		}
	case resp.StatusCode == http.StatusBadRequest:
		return &BadRequestError{Endpoint: path, Message: string(body)}
	case resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden:
		return &UnauthorizedError{Status: resp.StatusCode, Message: string(body)}
	case resp.StatusCode >= 500:
		return &ServerError{Status: resp.StatusCode, Body: string(body)}
	default:
		return fmt.Errorf("orcid: %s: HTTP %d: %s",
			path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("orcid: parse %s: %w", path, err)
	}
	return nil
}

func (c *Client) userAgent() string {
	if c.cfg.UserAgent != "" {
		return c.cfg.UserAgent
	}
	return "hive-orcid/0.1 (+https://github.com/sfborg/hive)"
}

// captureRateLimit reads any X-RateLimit-* / X-Rate-Limit-* headers the
// server sends and stores the snapshot. ORCID does not currently
// publish these on the public API; the code tolerates them being
// absent (fields stay zero).
func (c *Client) captureRateLimit(resp *http.Response) {
	snap := RateLimit{Observed: time.Now()}
	// Try both the Foo-Bar and Foo-Rate-Limit variants — the ORCID
	// server has historically shipped a mix.
	if v := firstHeader(resp, "X-RateLimit-Limit", "X-Rate-Limit-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			snap.Limit = n
		}
	}
	if v := firstHeader(resp, "X-RateLimit-Remaining", "X-Rate-Limit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			snap.Remaining = n
		}
	}
	if v := firstHeader(resp, "X-RateLimit-Reset", "X-Rate-Limit-Reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			snap.ResetAt = time.Unix(n, 0)
		}
	}
	if snap.Limit == 0 && snap.Remaining == 0 && snap.ResetAt.IsZero() {
		return
	}
	c.rlMu.Lock()
	c.rl = snap
	c.rlMu.Unlock()
}

func firstHeader(resp *http.Response, names ...string) string {
	for _, n := range names {
		if v := resp.Header.Get(n); v != "" {
			return v
		}
	}
	return ""
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// readCapped reads at most cap+1 bytes; if the response overflows,
// returns an error. Negative cap disables the limit.
func readCapped(r io.Reader, cap int64) ([]byte, error) {
	if cap < 0 {
		return io.ReadAll(r)
	}
	if cap == 0 {
		cap = 16 << 20
	}
	buf, err := io.ReadAll(io.LimitReader(r, cap+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > cap {
		return nil, fmt.Errorf("response exceeds max size %d", cap)
	}
	return buf, nil
}

// checkInsecureBaseURL rejects http:// BaseURLs unless allowInsecure
// is true or the host is a loopback address. Loopback carve-out
// exists so tests using httptest.NewServer don't need extra ceremony.
func checkInsecureBaseURL(baseURL string, allowInsecure bool) error {
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return &ConfigError{Msg: "invalid BaseURL: " + err.Error()}
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme != "http" {
		return &ConfigError{Msg: "BaseURL scheme must be http or https, got " + u.Scheme}
	}
	if allowInsecure || isLoopback(u.Hostname()) {
		return nil
	}
	return &ConfigError{
		Msg: "BaseURL uses http:// on a non-loopback host. " +
			"Use https:// or pass config.OptAllowInsecure() to override.",
	}
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

