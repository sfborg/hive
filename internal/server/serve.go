// Package server implements `hive serve <archive.db>` — the HTTP + WUI
// frontend. Runs the read-only API alongside a Lit-based single-page shell.
//
// v0 is deliberately narrow: read-only endpoints, localhost bind, no
// authentication. Non-loopback binds without configured auth are refused
// (see CLAUDE.md § Authentication and collaboration). Editing endpoints
// and ORCID SSO land in follow-up slices.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sfborg/hive/internal/wui"
	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/config"
	"github.com/sfborg/hive/pkg/llm"
	"github.com/sfborg/hive/pkg/prompts"
	"github.com/sfborg/hive/pkg/refmanager"
	"github.com/sfborg/hive/pkg/refmanager/bhlnames"
	"github.com/sfborg/hive/pkg/refmanager/grobid"
	"github.com/sfborg/hive/pkg/refmanager/ocr"
	"github.com/sfborg/hive/pkg/refmanager/pandoc"
	"github.com/sfborg/hive/pkg/taxonomy/annotator"
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

	// ReadOnly opens the archive with hive.ReadOnly, refusing writes at
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
//
// @title           hive API
// @version         0.1
// @description     REST API for the hive taxonomic editor. Reads and writes
// @description     an sfga SQLite archive of taxonomic names, references,
// @description     and supporting data. Regenerate this spec with `just openapi`.
// @termsOfService  https://github.com/sfborg/hive
//
// @contact.name    hive maintainers
// @contact.url     https://github.com/sfborg/hive
//
// @license.name    MIT
// @license.url     https://github.com/sfborg/hive/blob/main/LICENSE
//
// @host            localhost:2365
// @BasePath        /api
// @schemes         http
//
// @tag.name  system
// @tag.description  Health, archive metadata, keymap, shared vocabularies.
//
// @tag.name  taxa
// @tag.description  Read + write the taxon tree, ancestors, classifications, moves.
//
// @tag.name  names
// @tag.description  Scientific-name search, parse, patch, relations, dependencies.
//
// @tag.name  synonyms
// @tag.description  Synonym add/move/delete, pro-parte relationships.
//
// @tag.name  references
// @tag.description  Reference CRUD, DOI/OpenAlex/BHLnames/BibTeX lookup, PDF upload/library, JATS conversion, corrections.
//
// @tag.name  vernaculars
// @tag.description  Vernacular-name CRUD scoped to a taxon.
//
// @tag.name  distributions
// @tag.description  Geographic distribution CRUD scoped to a taxon.
//
// @tag.name  species-interactions
// @tag.description  Species-interaction CRUD (predator/host/pollinator/etc).
//
// @tag.name  type-materials
// @tag.description  Type-material CRUD scoped to a name.
//
// @tag.name  name-relations
// @tag.description  Name-relation CRUD (original combination, homonym, etc).
//
// @tag.name  agents
// @tag.description  Role-scoped agent CRUD (creator/contact/editor/contributor/publisher).
//
// @tag.name  metadata
// @tag.description  Archive-level metadata (title, description, license, logo).
//
// @tag.name  issues
// @tag.description  Validation issues (soft warnings) and rule reindex.
//
// @tag.name  vocab
// @tag.description  Controlled vocabularies (rank, code, status, NOMEN, countries, etc).
//
// @tag.name  llm
// @tag.description  Provider management + prompt invocation for LLM-assisted extraction.
//
// @tag.name  prompts
// @tag.description  Prompt library CRUD (system/user prompt templates for LLM runs).
//
// @tag.name  assertions
// @tag.description  Curator review of LLM-extracted assertions staged on references.
//
// @tag.name  test-set
// @tag.description  Gold-standard test-set membership for evaluation runs.
//
// @tag.name  docs
// @tag.description  OpenAPI spec + RapiDoc viewer.
func Run(archivePath string, opts Options) error {
	openOpts := []hive.OpenOption{
		hive.WithLookup(refmanager.LookupProviders(refmanager.LookupConfig{
			OpenAlexEmail: config.CurrentIdentity().OpenAlexEmail,
		})),
	}
	if opts.ReadOnly {
		openOpts = append(openOpts, hive.ReadOnly())
	}
	a, err := hive.Open(archivePath, openOpts...)
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

	s := &server{
		a:           a,
		archivePath: archivePath,
		// BHLnames stays a per-server field until a second scientific-
		// name-lookup provider (e.g. ChecklistBank) arrives and its
		// shape informs the shared interface. DOI + free-text search
		// already go through Archive.Lookup.
		bhlnames: bhlnames.New(bhlnames.DefaultBaseURL),
	}
	// GROBID client for the reference-ingest pipeline. Env var
	// HIVE_GROBID_URL activates it; unset means the upload handler
	// stores raw sources and skips the TEI conversion pass. Set to
	// e.g. http://grobid:8070 when running the docker-compose stack.
	if url := os.Getenv("HIVE_GROBID_URL"); url != "" {
		s.grobid = grobid.NewClient(url)
	} else {
		slog.Info("GROBID not configured; PDF uploads are stored without conversion",
			"hint", "run the Docker stack (just docker-up) or set HIVE_GROBID_URL")
	}
	// OCR fallback for image-only PDFs. Optional — when unset,
	// scanned papers land in the sidecar library but GROBID
	// extraction produces empty text. When set, image-only PDFs
	// get an ocrmypdf pass and a re-run through GROBID.
	if url := os.Getenv("HIVE_OCR_URL"); url != "" {
		s.ocr = ocr.NewClient(url)
	}
	// Pandoc adapter for EPUB → JATS conversion. Enabled when
	// the pandoc binary is invocable — probed once at startup so a
	// deployment without pandoc (local dev, minimal Docker image)
	// simply leaves EPUB uploads at metadata-only rather than
	// failing at first click. Set HIVE_DISABLE_PANDOC=1 to force
	// it off even when pandoc is on PATH (useful for tests that
	// don't want a real subprocess call in the path).
	if os.Getenv("HIVE_DISABLE_PANDOC") != "1" {
		pc := pandoc.NewClient()
		if err := pc.Ping(context.Background()); err == nil {
			s.pandoc = pc
		} else {
			slog.Info("pandoc unavailable; EPUB → JATS conversion disabled",
				"err", err)
		}
	}
	// Annotator (GNfinder + GNverifier). Cheap to construct; the
	// expensive dictionary + Bayes-weight load happens on the first
	// annotation call so a hive process that never receives a PDF
	// upload doesn't pay the cost.
	s.annotator = annotator.New()

	// LLM invocation runner. Assembled from the user config's
	// llm_providers entries; each entry becomes a Provider registered
	// under its declared name. Fails loud on any invalid entry so a
	// config typo surfaces at startup rather than at first invocation.
	// When no providers are configured, s.llmRunner stays nil and the
	// /api/llm/invoke endpoint returns 400 with a clear message.
	if cfg, cerr := config.Load(); cerr != nil {
		return fmt.Errorf("load user config: %w", cerr)
	} else if len(cfg.LLMProviders) > 0 {
		reg := llm.NewRegistry()
		for _, pc := range cfg.LLMProviders {
			p, err := llm.NewProvider(pc)
			if err != nil {
				return err
			}
			reg.Register(p)
		}
		s.llmRunner = &llm.Runner{
			Providers:  reg,
			LoadPrompt: prompts.Load,
		}
		s.llmProviders = cfg.LLMProviders
	}

	mux := s.routes()

	// Static WUI served from internal/wui/dist embed. The API routes above
	// win first (their patterns are more specific than "GET /"); everything
	// else falls through to the file server.
	staticFS, err := fs.Sub(wui.DistFS, "dist")
	if err != nil {
		return fmt.Errorf("wui/dist embed: %w", err)
	}
	// Cache-Control: no-cache tells browsers to revalidate with the server
	// on every request before using the cached copy, so a stale
	// reader.js is not served from cache after a container rebuild.
	// This matters most for iframe content, because Firefox caches
	// nested-iframe resources across parent-page reloads. The
	// revalidation is cheap (server-side embed
	// is in-memory) and correct behavior for a self-updating dev tool
	// where every restart may ship changed assets.
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))

	// Wrap the whole mux with a middleware that stamps the acting user into
	// each request's context. All mutation handlers pick it up via
	// hive.ActorFromContext when they open a Tx. When ORCID SSO lands, this
	// middleware chain gets a session-decoding step before the actor-inject
	// step; core keeps knowing nothing about the source.
	s.actor = newActorSource(opts.Actor)
	handler := withActor(s.actor, mux)

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

	// Time-based rule refresh: an hourly ticker keeps stale-metadata
	// (and future time-based rules) firing even during long-running
	// server sessions that don't restart. RefreshTimeBasedIssues is
	// cheap when nothing is due — one SELECT per rule against a small
	// state table. No-op on read-only archives.
	if !opts.ReadOnly {
		go refreshTicker(ctx, a)
		// Size counts and taxon summaries for papers processed before
		// they existed; later writes keep them current.
		go func() {
			if n, err := a.BackfillJATSStats(ctx); err != nil {
				slog.Warn("jats stats backfill stopped", "err", err, "filled", n)
			} else if n > 0 {
				slog.Info("jats stats backfilled", "files", n)
			}
			if n, err := a.BackfillTaxonSummaries(ctx); err != nil {
				slog.Warn("taxon summary backfill stopped", "err", err, "filled", n)
			} else if n > 0 {
				slog.Info("taxon summaries backfilled", "references", n)
			}
		}()
	}

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

// refreshTicker re-evaluates time-based rules once an hour for the
// server's lifetime. Covers long-running processes where openReadWrite's
// one-shot call is not enough to catch rules that come due mid-session.
// Exits when ctx is done (SIGINT/SIGTERM). No-op if the archive is
// read-only (RefreshTimeBasedIssues checks that internally too).
func refreshTicker(ctx context.Context, a *hive.Archive) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = a.RefreshTimeBasedIssues(context.Background())
		}
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

// withActor wraps next with a middleware that injects the current actor
// into every request's context via hive.WithActor. Every mutation handler downstream
// picks it up when it opens a Tx.
//
// Future: once ORCID SSO ships, this middleware moves after a session-
// decoding middleware that extracts the ORCID iD from the request. The
// signature and behavior stay the same — only the source of `actor`
// changes. Core stays ignorant of where the string came from.
func withActor(actor *actorSource, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := hive.WithActor(r.Context(), actor.get())
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
