// Package config holds the [Config] and its functional [Option] setters
// used to construct an orcid Client.
package config

import (
	"log/slog"
	"net/http"
	"time"
)

// Config controls how the orcid Client talks to pub.orcid.org.
//
// Construct with [New], usually via the option helpers (OptBaseURL,
// OptTimeout, ...); direct struct literals work too if you prefer.
type Config struct {
	// BaseURL overrides the ORCID host — useful for tests
	// (httptest.NewServer) or the sandbox environment
	// (https://pub.sandbox.orcid.org/v3.0). Defaults to
	// https://pub.orcid.org/v3.0.
	BaseURL string

	// Transport lets the caller wrap the HTTP round-trip — the usual
	// use case is instrumentation (tracing spans, metrics, structured
	// logs). Custom TLS or proxy config also plug in here. When nil,
	// http.DefaultTransport is used.
	//
	// Client-level safety features (redirect policy, timeout) are
	// preserved regardless.
	Transport http.RoundTripper

	// UserAgent overrides the default UA string. The default carries
	// the library name, version, and repo URL.
	UserAgent string

	// Logger receives warnings. Defaults to slog.Default().
	Logger *slog.Logger

	// Timeout is applied to the constructed HTTP client. Zero uses the
	// default (10s).
	Timeout time.Duration

	// MaxResponseSize caps the number of bytes the driver will read
	// from any single response before failing with an error. Guards
	// against unbounded reads from a hostile or misbehaving server.
	// Zero means the default (16 MiB — ORCID records are small); a
	// negative value disables the cap entirely.
	MaxResponseSize int64

	// AllowInsecure permits a non-HTTPS BaseURL. By default, [New]
	// refuses to construct a Client if BaseURL uses http:// (except
	// for localhost / loopback addresses, which are allowed for tests).
	AllowInsecure bool
}

// Option applies a single setting to a [Config]. Used with [New].
type Option func(*Config)

// New returns a Config with defaults applied, then each option run in
// order. Later options override earlier ones.
func New(opts ...Option) Config {
	c := Config{
		BaseURL:         "https://pub.orcid.org/v3.0",
		Timeout:         10 * time.Second,
		MaxResponseSize: 16 << 20,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// OptBaseURL overrides the ORCID host. Primarily for testing and for
// pointing at the ORCID sandbox.
func OptBaseURL(s string) Option { return func(c *Config) { c.BaseURL = s } }

// OptTransport wraps the HTTP round-trip. The most common use is
// tracing / metrics / logging instrumentation:
//
//	config.OptTransport(otelhttp.NewTransport(http.DefaultTransport))
//
// Also usable for custom TLS or proxy configuration by passing a
// tuned *http.Transport. Client-layer safety (timeout, redirect
// policy) is preserved regardless.
func OptTransport(t http.RoundTripper) Option {
	return func(c *Config) { c.Transport = t }
}

// OptUserAgent overrides the default User-Agent header entirely.
func OptUserAgent(s string) Option { return func(c *Config) { c.UserAgent = s } }

// OptLogger swaps the default slog.Logger.
func OptLogger(l *slog.Logger) Option { return func(c *Config) { c.Logger = l } }

// OptTimeout sets the total per-request timeout on the HTTP client.
func OptTimeout(d time.Duration) Option { return func(c *Config) { c.Timeout = d } }

// OptMaxResponseSize caps the number of bytes the driver reads from a
// single response before failing. Guards against unbounded memory use
// from a hostile server. Zero uses the default; negative disables the
// cap.
func OptMaxResponseSize(n int64) Option {
	return func(c *Config) { c.MaxResponseSize = n }
}

// OptAllowInsecure permits BaseURL to use http:// instead of https://.
// By default the client refuses non-HTTPS URLs (except loopback
// addresses, which are always allowed for tests). Enable only when
// pointing at a private mirror on a trusted network.
func OptAllowInsecure() Option {
	return func(c *Config) { c.AllowInsecure = true }
}
