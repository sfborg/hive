// Package serve implements `hive serve <archive.db>` — the HTTP + PWA
// frontend. Runs the read-only API alongside a Lit-based single-page shell.
//
// v0 is deliberately narrow: read-only endpoints, localhost bind, no
// authentication. Non-loopback binds without configured auth are refused
// (see CLAUDE.md § Authentication and collaboration). Editing endpoints
// and ORCID SSO land in follow-up slices.
package serve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sfborg/hive/core"
	"github.com/sfborg/hive/web"
)

const defaultPort = 2365

// Options configures Run without positional-argument sprawl.
type Options struct {
	// Bind is the "host:port" pair the server listens on. Empty →
	// 127.0.0.1:2365. Non-loopback binds require auth to be configured
	// (unimplemented in v0) and are refused with an explicit error.
	Bind string

	// AllowUnauthenticated is the escape hatch for non-loopback binds
	// without auth. Off by default; the caller must opt in explicitly.
	AllowUnauthenticated bool

	// ReadOnly opens the archive with core.ReadOnly, refusing writes at
	// the core layer. Default is read-write so hive serve can host both
	// browsing and editing. Set true for public-view deployments.
	ReadOnly bool

	// Actor is the ORCID iD (or other identifier) stamped into
	// col__modified_by on every mutation. Loaded from --orcid flag or
	// HIVE_ORCID env var by the main package; falls back to "local" when
	// nothing is configured (with a single-line warning at startup).
	// In hosted SSO mode this will be overwritten per-request by the
	// auth middleware; for v0 it's a process-wide stamp.
	Actor string
}

// Run opens the archive and blocks serving HTTP until the process receives
// SIGINT/SIGTERM (or the listener errors).
func Run(archivePath string, opts Options) error {
	openOpts := []core.OpenOption{}
	if opts.ReadOnly {
		openOpts = append(openOpts, core.ReadOnly())
	}
	a, err := core.Open(archivePath, openOpts...)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer a.Close()

	if opts.Actor == "" {
		opts.Actor = "local"
		fmt.Fprintln(os.Stderr,
			"warning: no --orcid or HIVE_ORCID set; writes will be attributed to \"local\"")
	}

	bind := opts.Bind
	if bind == "" {
		bind = fmt.Sprintf("127.0.0.1:%d", defaultPort)
	}
	if err := checkBind(bind, opts.AllowUnauthenticated); err != nil {
		return err
	}

	s := &server{a: a, archivePath: archivePath}
	mux := s.routes()

	// Static PWA served from web/dist embed. The API routes above win first
	// (their patterns are more specific than "GET /"); everything else falls
	// through to the file server.
	staticFS, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		return fmt.Errorf("web/dist embed: %w", err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))

	// Wrap the whole mux with a middleware that stamps the acting user into
	// each request's context. All mutation handlers pick it up via
	// core.ActorFromContext when they open a Tx. When ORCID SSO lands, this
	// middleware chain gets a session-decoding step before the actor-inject
	// step; core keeps knowing nothing about the source.
	handler := withActor(opts.Actor, mux)

	srv := &http.Server{
		Addr:              bind,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// No overall read/write timeouts yet — the SSE streaming endpoints
		// (import progress, reindex) that arrive with editing want to hold
		// connections open. When those land we'll add per-route timeouts.
	}

	// Signal handling: graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		mode := "read-write"
		if opts.ReadOnly {
			mode = "read-only"
		}
		fmt.Printf("hive serve on http://%s  archive=%s  mode=%s  actor=%s\n",
			bind, archivePath, mode, opts.Actor)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "\nshutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-serverErr:
		return err
	}
}

// checkBind refuses non-loopback binds unless the caller has explicitly
// opted out of auth. Serving on 0.0.0.0 without auth is easy to trip into
// accidentally and would expose an archive to the network with no
// gatekeeping — the check is the safe-default guard from CLAUDE.md
// § Authentication and collaboration.
func checkBind(addr string, allowUnauthenticated bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bind address %q is malformed: %w", addr, err)
	}
	if isLoopback(host) {
		return nil
	}
	if !allowUnauthenticated {
		return fmt.Errorf(
			"non-loopback bind %q requires configured auth (unimplemented in v0). "+
				"Re-run with --allow-unauthenticated to bypass this guard.",
			addr,
		)
	}
	return nil
}

// withActor wraps next with a middleware that injects `actor` into every
// request's context via core.WithActor. Every mutation handler downstream
// picks it up when it opens a Tx.
//
// Future: once ORCID SSO ships, this middleware moves after a session-
// decoding middleware that extracts the ORCID iD from the request. The
// signature and behavior stay the same — only the source of `actor`
// changes. Core stays ignorant of where the string came from.
func withActor(actor string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := core.WithActor(r.Context(), actor)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isLoopback reports whether the host portion of a bind address resolves to
// a loopback interface. Handles the common shorthands (empty, localhost,
// 127.*, ::1) without a DNS lookup.
func isLoopback(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	// Anything else — a hostname other than "localhost" — is treated as
	// non-loopback. Users who really mean loopback should bind to
	// 127.0.0.1 explicitly.
	return strings.HasSuffix(host, ".localhost")
}
