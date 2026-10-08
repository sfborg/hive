package server

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/refmanager/casing"
	"github.com/sfborg/hive/pkg/taxonomy/mentions"
	"github.com/sfborg/hive/pkg/assembler"
	"github.com/sfborg/hive/pkg/correction"
	"github.com/sfborg/hive/pkg/llm"
	"github.com/sfborg/hive/pkg/refmanager/bibtex"
	"github.com/sfborg/hive/pkg/refmanager/figextract"
	"github.com/sfborg/hive/pkg/refmanager/grobid"
	"github.com/sfborg/hive/pkg/refmanager/jatsconv"
	"github.com/sfborg/hive/pkg/refmanager/labels"
	"github.com/sfborg/hive/pkg/refmanager/ocr"
	"github.com/sfborg/hive/pkg/refmanager/openalex"
	"github.com/sfborg/hive/pkg/refmanager/pandoc"
	"github.com/sfborg/hive/pkg/refmanager/pdftext"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/hive/pkg/taxonomy/annotator"
	"github.com/sfborg/hive/pkg/refmanager/bhlnames"
	"github.com/sfborg/hive/pkg/ui"
	"github.com/sfborg/sflib/pkg/coldp"
)

// server is the HTTP handler context. Owns the archive handle plus any
// shared state (soon: actor context, cached enums, keymap, form specs).
// Handlers are methods on this type so they can reach dependencies without
// package-level globals.
type server struct {
	a           *hive.Archive
	archivePath string
	// grobid is the client for the optional GROBID service (PDF →
	// TEI conversion). nil when HIVE_GROBID_URL is unset — the
	// upload handler skips the conversion pass and stores only the
	// raw source; a curator can run GROBID later by bringing the
	// service up and re-triggering the pipeline. See
	// pkg/grobid/grobid.go and REFERENCE_PDF_PLAN.md.
	grobid *grobid.Client
	// ocr is the client for the optional OCR sidecar service. nil
	// when HIVE_OCR_URL is unset — image-only PDFs then land in the
	// library but GROBID returns empty output. When set, the
	// upload handler retries GROBID after an OCR pass to give
	// scanned papers a text layer.
	ocr *ocr.Client
	// pandoc is the subprocess adapter for EPUB → JATS conversion.
	// nil when the pandoc binary is unavailable at startup — EPUB
	// uploads then land with metadata only and the reader shows a
	// "no augmented XML" state for the EPUB sha; a PDF companion,
	// if present, still drives the reader. When set, EPUB uploads
	// run through the same downstream JATS annotation pipeline as
	// PDF and publisher-XML uploads.
	pandoc *pandoc.Client
	// annotator wraps GNfinder + GNverifier. Loaded lazily on the
	// first upload so a hive process that never sees a PDF doesn't
	// pay the several-MB dictionary + Bayes-weight load cost. One
	// instance per server (thread-safe under the sync.Once inside).
	annotator *annotator.Annotator
	// bhlnames is the shared HTTP client for the BHLnames service.
	// Constructed once at Run(); hits the public production endpoint
	// by default. DOI resolution + free-text search go through
	// s.a.Lookup instead — BHLnames is scientific-name-scoped and
	// keeps its concrete-typed accessor until the shared name-lookup
	// interface has more than one implementation to shape it.
	bhlnames *bhlnames.Client
	// llmRunner invokes shipped-library prompts (ner_v5, compose_v1,
	// future translation) against curator-configured providers. nil
	// when the user config declares no llm_providers — the
	// /api/llm/invoke endpoint then returns 400 rather than a
	// misleading 500.
	llmRunner *llm.Runner
	// llmProviders is the raw config-declared provider list, kept so
	// GET /api/llm/providers can describe what's configured (name,
	// kind, default model) without extending the Provider interface
	// or reloading config. Credentials never appear in the API
	// response — the CredentialConfig fields are ignored on read.
	llmProviders []llm.ProviderConfig
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Debug pprof — enabled when HIVE_DEBUG_PPROF=1 in the
	// environment. Off by default so it doesn't leak diagnostics
	// on a shared instance. Endpoints under /debug/pprof/ include
	// /goroutine (stack dump), /profile (30s CPU), /heap.
	if os.Getenv("HIVE_DEBUG_PPROF") == "1" {
		mux.HandleFunc("GET /debug/pprof/", httppprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", httppprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", httppprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", httppprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", httppprof.Trace)
		mux.HandleFunc("GET /debug/pprof/goroutine", httppprof.Handler("goroutine").ServeHTTP)
		mux.HandleFunc("GET /debug/pprof/heap", httppprof.Handler("heap").ServeHTTP)
		mux.HandleFunc("GET /debug/pprof/allocs", httppprof.Handler("allocs").ServeHTTP)
		mux.HandleFunc("GET /debug/pprof/block", httppprof.Handler("block").ServeHTTP)
		mux.HandleFunc("GET /debug/pprof/mutex", httppprof.Handler("mutex").ServeHTTP)
	}

	// OpenAPI spec + Docs UI. The spec is regenerated from swaggo
	// annotations by `just openapi`. /docs boots the vendored RapiDoc
	// component against /api/openapi.yaml.
	mux.HandleFunc("GET /api/openapi.yaml", s.handleOpenAPIYAML)
	mux.HandleFunc("GET /api/openapi.json", s.handleOpenAPIJSON)
	mux.HandleFunc("GET /docs", s.handleDocs)

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/services", s.handleServices)
	mux.HandleFunc("GET /api/archive", s.handleArchive)
	mux.HandleFunc("POST /api/archive/reassemble", s.handleReassembleArchive)
	mux.HandleFunc("GET /api/archive/assembler-nodes", s.handleAssemblerNodes)
	mux.HandleFunc("GET /api/reference/{id}/compose-tree", s.handleExportComposeTree)
	mux.HandleFunc("GET /api/taxon/{id}/overlays", s.handleListOverlaysForTaxon)
	mux.HandleFunc("POST /api/taxon/{id}/overlays", s.handleCreateOverlay)
	mux.HandleFunc("GET /api/overlay/{id}", s.handleGetOverlay)
	mux.HandleFunc("DELETE /api/overlay/{id}", s.handleRetireOverlay)
	mux.HandleFunc("PATCH /api/overlay/{id}/citation", s.handleSetOverlayCitation)
	mux.HandleFunc("GET /api/vocab", s.handleVocab)
	mux.HandleFunc("GET /api/vocab/nomen", s.handleNomenVocab)
	mux.HandleFunc("GET /api/vocab/countries", s.handleCountriesVocab)
	mux.HandleFunc("GET /api/vocab/languages", s.handleLanguagesVocab)
	mux.HandleFunc("GET /api/vocab/sex", s.handleSexVocab)
	// Rich per-vocab CRUD for the vocab editor. Currently scoped to
	// species_interaction_type; grows to other editable vocabs as
	// they get schema support for description / URI (see
	// DEFERRED.md § Rich vocab detail columns for flat vocabs).
	mux.HandleFunc("GET /api/vocab/species_interaction_type/full",
		s.handleListSpeciesInteractionTypes)
	mux.HandleFunc("POST /api/vocab/species_interaction_type",
		s.handleAddSpeciesInteractionType)
	mux.HandleFunc("PATCH /api/vocab/species_interaction_type/{id}",
		s.handlePatchSpeciesInteractionType)
	mux.HandleFunc("DELETE /api/vocab/species_interaction_type/{id}",
		s.handleDeleteSpeciesInteractionType)
	mux.HandleFunc("GET /api/keymap", s.handleKeymap)

	mux.HandleFunc("GET /api/metadata", s.handleGetMetadata)
	mux.HandleFunc("PATCH /api/metadata", s.handlePatchMetadata)

	// URLs are singular to match ChecklistBank's convention. See CLAUDE.md
	// § HTTP API shape. Every SFBorg-adjacent API follows the same pattern
	// so cross-API muscle memory stays cheap.
	mux.HandleFunc("GET /api/taxon/roots", s.handleRoots)
	mux.HandleFunc("GET /api/taxon/search", s.handleTaxonSearch)
	mux.HandleFunc("GET /api/taxon/{id}", s.handleGetTaxon)
	mux.HandleFunc("GET /api/taxon/{id}/children", s.handleChildren)
	mux.HandleFunc("GET /api/taxon/{id}/synonyms", s.handleSynonyms)
	mux.HandleFunc("GET /api/taxon/{id}/nomenclatural-history", s.handleNomenclaturalHistory)
	mux.HandleFunc("GET /api/taxon/{id}/vernaculars", s.handleListVernaculars)
	mux.HandleFunc("POST /api/taxon/{id}/vernaculars", s.handleCreateVernacular)
	mux.HandleFunc("PATCH /api/vernacular/{id}", s.handlePatchVernacular)
	mux.HandleFunc("DELETE /api/vernacular/{id}", s.handleDeleteVernacular)
	mux.HandleFunc("GET /api/taxon/{id}/distributions", s.handleListDistributions)
	mux.HandleFunc("POST /api/taxon/{id}/distributions", s.handleCreateDistribution)
	mux.HandleFunc("PATCH /api/distribution/{id}", s.handlePatchDistribution)
	mux.HandleFunc("DELETE /api/distribution/{id}", s.handleDeleteDistribution)
	mux.HandleFunc("GET /api/taxon/{id}/species-interactions", s.handleListSpeciesInteractions)
	mux.HandleFunc("POST /api/taxon/{id}/species-interactions", s.handleCreateSpeciesInteraction)
	mux.HandleFunc("PATCH /api/species-interaction/{id}", s.handlePatchSpeciesInteraction)
	mux.HandleFunc("DELETE /api/species-interaction/{id}", s.handleDeleteSpeciesInteraction)
	mux.HandleFunc("GET /api/name/{id}/type-materials", s.handleListTypeMaterials)
	mux.HandleFunc("POST /api/name/{id}/type-materials", s.handleCreateTypeMaterial)
	mux.HandleFunc("PATCH /api/type-material/{id}", s.handlePatchTypeMaterial)
	mux.HandleFunc("DELETE /api/type-material/{id}", s.handleDeleteTypeMaterial)
	mux.HandleFunc("GET /api/name/{id}/relations", s.handleListNameRelations)
	mux.HandleFunc("POST /api/name/{id}/relations", s.handleCreateNameRelation)
	mux.HandleFunc("DELETE /api/name-relation/{id}", s.handleDeleteNameRelation)
	mux.HandleFunc("GET /api/taxon/{id}/ancestors", s.handleAncestors)
	mux.HandleFunc("GET /api/taxon/{id}/classification", s.handleClassification)
	mux.HandleFunc("POST /api/taxon/{id}/move", s.handleMoveTaxon)
	mux.HandleFunc("POST /api/taxon/{id}/basionym", s.handleAddBasionym)
	mux.HandleFunc("POST /api/taxon/{id}/synonym", s.handleAddSynonym)
	mux.HandleFunc("DELETE /api/synonym/{id}", s.handleDeleteSynonym)
	mux.HandleFunc("POST /api/synonym/{id}/move", s.handleMoveSynonym)
	mux.HandleFunc("GET /api/name/{id}/dependencies", s.handleNameDependencies)
	mux.HandleFunc("POST /api/taxon", s.handleCreateTaxon)
	mux.HandleFunc("DELETE /api/taxon/{id}", s.handleDeleteTaxon)
	mux.HandleFunc("GET /api/taxon/{id}/delete-preview", s.handleDeletePreview)
	mux.HandleFunc("POST /api/taxon/{id}/delete-reparent", s.handleDeleteReparent)
	mux.HandleFunc("POST /api/taxon/{id}/delete-cascade", s.handleDeleteCascade)
	mux.HandleFunc("GET /api/taxon/{id}/code-default", s.handleCodeDefault)
	mux.HandleFunc("GET /api/taxon/{id}/create-name-prefix", s.handleCreateNamePrefix)
	mux.HandleFunc("GET /api/taxon/{id}/child-ranks", s.handleChildRanks)

	mux.HandleFunc("GET /api/name/search", s.handleNameSearch)
	mux.HandleFunc("POST /api/name/parse", s.handleParseName)
	mux.HandleFunc("GET /api/name/{id}", s.handleGetName)

	mux.HandleFunc("GET /api/reference", s.handleReferences)
	mux.HandleFunc("GET /api/reference/dashboard", s.handleReferenceDashboard)
	mux.HandleFunc("GET /api/reference/search", s.handleReferenceSearch)
	mux.HandleFunc("GET /api/name-mentions/taxa", s.handleColTaxa)
	mux.HandleFunc("GET /api/name-mentions/tree", s.handleTaxonTree)
	mux.HandleFunc("POST /api/reference", s.handleCreateReference)
	mux.HandleFunc("POST /api/reference/regenerate-citations", s.handleRegenerateCitations)
	mux.HandleFunc("POST /api/reference/{id}/attach-pdf", s.handleAttachReferencePDF)
	mux.HandleFunc("GET /api/reference/{id}/license", s.handleGetReferenceLicense)
	mux.HandleFunc("PUT /api/reference/{id}/license", s.handleSetReferenceLicense)
	mux.HandleFunc("DELETE /api/reference/{id}/attached-pdf/{sha}", s.handleDetachReferencePDF)
	// Corrections — bbox-add, text-tag, xref-fix, chip-feedback.
	// Slice 1 supports bbox-add for figures/tables only. See
	// DIGITIZE_CORRECTIONS_PLAN.md § Slicing.
	mux.HandleFunc("POST /api/reference/correction", s.handleCreateCorrection)
	mux.HandleFunc("POST /api/reference/correction/preview", s.handlePreviewCorrection)
	mux.HandleFunc("DELETE /api/reference/correction/{id}", s.handleDeleteCorrection)
	mux.HandleFunc("GET /api/reference/{id}/corrections", s.handleListCorrections)
	// Add-reference modal helpers. All three return *unsaved* previews
	// — the modal shows a form the curator confirms, then POSTs to
	// /api/reference to actually write.
	mux.HandleFunc("GET /api/reference/resolve-doi", s.handleResolveDOI)
	mux.HandleFunc("GET /api/reference/lookup-openalex", s.handleLookupOpenAlex)
	mux.HandleFunc("POST /api/reference/lookup-bhlnames", s.handleLookupBHLnames)
	mux.HandleFunc("POST /api/reference/parse-bibtex", s.handleParseBibTeX)
	mux.HandleFunc("GET /api/reference/{id}", s.handleGetReference)
	mux.HandleFunc("GET /api/reference/{id}/casing", s.handleReferenceCasing)
	mux.HandleFunc("PATCH /api/reference/{id}", s.handlePatchReference)
	mux.HandleFunc("DELETE /api/reference/{id}", s.handleDeleteReference)
	// POST /api/reference/upload accepts a multipart file
	// (PDF / EPUB / HTML / JATS), stores it in the archive's
	// sidecar directory content-addressed on sha512, and returns
	// the metadata (sha, ext, size, already_existed). Downstream
	// processing (GROBID → TEI → JATS, DOI extraction) happens in
	// follow-up handlers so a curator can upload without paying
	// the full pipeline cost synchronously.
	mux.HandleFunc("POST /api/reference/upload", s.handleReferenceUpload)
	// GET /api/reference/library/{sha}/source streams a sidecar file
	// back to the browser so the WUI can render the PDF (or other
	// format) inline via an <iframe> next to the reference-edit
	// form. Content-Type is derived from the extension recorded in
	// the sidecar's meta.json.
	mux.HandleFunc("GET /api/reference/library/{sha}/source", s.handleReferenceLibrarySource)
	mux.HandleFunc("POST /api/reference/library/{sha}/reannotate", s.handleReferenceLibraryReannotate)
	mux.HandleFunc("GET /api/reference/library/{sha}/taxpub", s.handleReferenceLibraryTaxpub)
	mux.HandleFunc("GET /api/reference/library/{sha}/figures/{id}", s.handleReferenceLibraryFigure)
	mux.HandleFunc("GET /api/reference/library/{sha}/regions", s.handleReferenceLibraryRegions)

	mux.HandleFunc("PATCH /api/taxon/{id}", s.handlePatchTaxon)
	mux.HandleFunc("PATCH /api/name/{id}", s.handlePatchName)

	mux.HandleFunc("POST /api/reindex/validation", s.handleReindexValidation)

	mux.HandleFunc("GET /api/llm/providers", s.handleListLLMProviders)
	mux.HandleFunc("POST /api/llm/providers", s.handleCreateLLMProvider)
	mux.HandleFunc("PATCH /api/llm/providers/{name}", s.handlePatchLLMProvider)
	mux.HandleFunc("DELETE /api/llm/providers/{name}", s.handleDeleteLLMProvider)
	mux.HandleFunc("GET /api/llm/providers/{name}/models", s.handleListLLMProviderModels)
	mux.HandleFunc("POST /api/llm/estimate", s.handleEstimateLLM)
	mux.HandleFunc("POST /api/llm/invoke", s.handleInvokeLLM)
	mux.HandleFunc("POST /api/llm/pipeline", s.handleRunLLMPipeline)
	mux.HandleFunc("GET /api/reference/{id}/llm-runs", s.handleListReferenceLLMRuns)
	mux.HandleFunc("GET /api/reference/{id}/assertions", s.handleListReferenceAssertions)
	mux.HandleFunc("PATCH /api/assertion/{id}/status", s.handlePatchAssertionStatus)
	mux.HandleFunc("POST /api/assertion/{id}/correction", s.handlePostAssertionCorrection)
	mux.HandleFunc("GET /api/taxon/{id}/row-sources", s.handleGetTaxonRowSources)
	mux.HandleFunc("GET /api/reference/{id}/paper-import", s.handleListPaperImportMappings)
	mux.HandleFunc("POST /api/reference/{id}/paper-import", s.handleUpsertPaperImportMapping)
	mux.HandleFunc("DELETE /api/reference/{id}/paper-import/{key}", s.handleDeletePaperImportMapping)
	mux.HandleFunc("POST /api/reference/{id}/paper-import/execute", s.handleExecutePaperImport)
	mux.HandleFunc("GET /api/prompt", s.handleListPrompts)
	mux.HandleFunc("GET /api/prompt/{id}", s.handleGetPrompt)
	mux.HandleFunc("POST /api/prompt", s.handleCreatePrompt)
	mux.HandleFunc("PATCH /api/prompt/{id}", s.handlePatchPrompt)
	mux.HandleFunc("DELETE /api/prompt/{id}", s.handleDeletePrompt)
	mux.HandleFunc("GET /api/test-set", s.handleListTestSet)
	mux.HandleFunc("GET /api/test-set/{id}", s.handleGetTestSetStatus)
	mux.HandleFunc("POST /api/test-set/{id}", s.handleAddToTestSet)
	mux.HandleFunc("DELETE /api/test-set/{id}", s.handleRemoveFromTestSet)
	mux.HandleFunc("GET /api/llm/run/{id}", s.handleGetLLMRun)
	mux.HandleFunc("DELETE /api/llm/run/{id}", s.handleSoftDeleteLLMRun)
	mux.HandleFunc("POST /api/llm/run/{id}/restore", s.handleRestoreLLMRun)
	mux.HandleFunc("POST /api/llm/run/{id}/gold-standard", s.handleSetLLMRunGoldStandard)

	mux.HandleFunc("GET /api/issue/summary", s.handleIssueSummary)
	mux.HandleFunc("GET /api/issue", s.handleIssueList)

	// Role-table CRUD. The {role} parameter selects which sfga table
	// (creator/contact/editor/contributor/publisher); rows share an
	// identical column shape so one handler set covers all five.
	mux.HandleFunc("GET /api/agent/{role}", s.handleListAgents)
	mux.HandleFunc("POST /api/agent/{role}", s.handleCreateAgent)
	mux.HandleFunc("GET /api/agent/{role}/{id}", s.handleGetAgent)
	mux.HandleFunc("PATCH /api/agent/{role}/{id}", s.handlePatchAgent)
	mux.HandleFunc("DELETE /api/agent/{role}/{id}", s.handleDeleteAgent)
	mux.HandleFunc("POST /api/agent/{role}/{id}/copy", s.handleCopyAgent)
	mux.HandleFunc("POST /api/agent/{role}/{id}/move", s.handleMoveAgent)

	return mux
}

// handleHealth is the trivial liveness probe. Returns 200 with a static body
// so orchestrators can distinguish "server up" from "server hung."
//
// @Summary      Liveness probe
// @Description  Static 200 response for orchestrator health checks.
// @Tags         system
// @Produce      json
// @Success      200 {object} map[string]string
// @Router       /health [get]
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Service statuses reported by GET /api/services. They use the same
// words as the upload response's tei_status.
const (
	serviceOK            = "ok"
	serviceNotConfigured = "not_configured"
	serviceUnavailable   = "unavailable"
)

// servicePingTimeout bounds each GROBID / OCR reachability check, so
// a service that is down cannot stall the request.
const servicePingTimeout = 3 * time.Second

// handleServices reports whether the optional document-processing
// services are available. PDF conversion needs GROBID, which runs in
// the Docker stack; without it, uploads are stored unconverted. The
// WUI and `hive ingest` use this to warn before an upload.
//
// @Summary      Document-processing services
// @Description  Status of the optional services used to process uploaded papers:
// @Description  "ok", "not_configured" (not set up) or "unavailable" (set up
// @Description  but not responding). PDF conversion needs GROBID.
// @Tags         system
// @Produce      json
// @Success      200 {object} apiServices
// @Router       /services [get]
func (s *server) handleServices(w http.ResponseWriter, r *http.Request) {
	out := apiServices{
		GROBID: serviceNotConfigured,
		OCR:    serviceNotConfigured,
		Pandoc: serviceUnavailable,
	}
	var wg sync.WaitGroup
	if s.grobid != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), servicePingTimeout)
			defer cancel()
			out.GROBID = pingStatus(s.grobid.Ping(ctx))
		}()
	}
	if s.ocr != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), servicePingTimeout)
			defer cancel()
			out.OCR = pingStatus(s.ocr.Ping(ctx))
		}()
	}
	if s.pandoc != nil {
		out.Pandoc = serviceOK
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

func pingStatus(err error) string {
	if err != nil {
		return serviceUnavailable
	}
	return serviceOK
}

// handleVocab returns the controlled-vocabulary bundle. Callers cache this
// once at boot; the tables are read-only for the process lifetime so a
// long Cache-Control keeps browsers from re-fetching. Bundle-endpoint
// URL stays plural-ish (`/api/vocab` is treated as a mass noun) because
// it isn't a "get one resource by id" lookup.
//
// @Summary      Vocabulary bundle
// @Description  All controlled vocabularies (rank, code, status, etc)
// @Description  loaded once per session. Cached for one hour.
// @Tags         vocab
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} problem
// @Router       /vocab [get]
func (s *server) handleVocab(w http.ResponseWriter, r *http.Request) {
	v, err := s.a.Vocabulary(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Vocabularies don't change during a session — a curator adds one via
	// a schema migration, not during editing — so caching is safe. The
	// browser still revalidates cross-session in case of a hive upgrade.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, v)
}

// handleListSpeciesInteractionTypes returns every row in the
// species_interaction_type vocab with the rich fields the editor
// needs (description / obo / inverse / symmetrical / superTypes).
// The picker's /api/vocab bundle continues to ship the slim
// (id, name) shape.
//
// @Summary      List species interaction types
// @Description  Rich vocabulary rows for the species_interaction_type editor.
// @Tags         vocab
// @Produce      json
// @Success      200 {object} apiPage[apiVocabTermDetail]
// @Failure      500 {object} problem
// @Router       /vocab/species_interaction_type/full [get]
func (s *server) handleListSpeciesInteractionTypes(w http.ResponseWriter, r *http.Request) {
	terms, err := s.a.ListSpeciesInteractionTypes(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiVocabTermDetail, 0, len(terms))
	for _, t := range terms {
		items = append(items, speciesInteractionTypeToAPI(t))
	}
	writeJSON(w, http.StatusOK, apiPage[apiVocabTermDetail]{Items: items})
}

// handleAddSpeciesInteractionType writes a new term. Path is
// /api/vocab/species_interaction_type; body is apiVocabTermDetail
// (id required). Uniqueness on the id column is enforced by sfga's
// PK — a duplicate returns 409 via ErrConflict.
//
// @Summary      Add species interaction type
// @Description  Insert a new species_interaction_type vocab row.
// @Tags         vocab
// @Accept       json
// @Produce      json
// @Param        body body apiVocabTermDetail true "New vocab row"
// @Success      201 {object} apiVocabTermDetail
// @Failure      400 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /vocab/species_interaction_type [post]
func (s *server) handleAddSpeciesInteractionType(w http.ResponseWriter, r *http.Request) {
	var body apiVocabTermDetail
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		writeBadRequest(w, r, "id is required")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeBadRequest(w, r, "name is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.AddSpeciesInteractionType(apiVocabTermDetailToCore(body))
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

// handlePatchSpeciesInteractionType updates every editable column of
// the term at {id}. The id itself isn't editable — renaming an id
// is delete + re-add on a new id (also rare — other rows reference
// it via FK).
//
// @Summary      Update species interaction type
// @Description  Replace editable columns of a species_interaction_type term.
// @Tags         vocab
// @Accept       json
// @Produce      json
// @Param        id path string true "Vocab term ID"
// @Param        body body apiVocabTermDetail true "Updated columns"
// @Success      200 {object} apiVocabTermDetail
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /vocab/species_interaction_type/{id} [patch]
func (s *server) handlePatchSpeciesInteractionType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body apiVocabTermDetail
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.ID = id // path wins over body
	if strings.TrimSpace(body.Name) == "" {
		writeBadRequest(w, r, "name is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.UpdateSpeciesInteractionType(apiVocabTermDetailToCore(body))
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// handleDeleteSpeciesInteractionType removes a term. sfga's FK from
// species_interaction.col__type_id blocks the delete when any row
// still cites the term — returns 409 (ErrConflict) so the caller
// can prompt the curator to reassign the affected rows first.
//
// @Summary      Delete species interaction type
// @Description  Delete a species_interaction_type term. Returns 409 if any row still references it.
// @Tags         vocab
// @Param        id path string true "Vocab term ID"
// @Success      204 "No Content"
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /vocab/species_interaction_type/{id} [delete]
func (s *server) handleDeleteSpeciesInteractionType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteSpeciesInteractionType(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// speciesInteractionTypeToAPI / apiVocabTermDetailToCore ship the
// core VocabTermDetail across the wire boundary.
func speciesInteractionTypeToAPI(t hive.VocabTermDetail) apiVocabTermDetail {
	return apiVocabTermDetail{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Obo:         t.Obo,
		Inverse:     t.Inverse,
		Symmetrical: t.Symmetrical,
		SuperTypes:  t.SuperTypes,
	}
}

func apiVocabTermDetailToCore(a apiVocabTermDetail) hive.VocabTermDetail {
	return hive.VocabTermDetail{
		ID:          a.ID,
		Name:        a.Name,
		Description: a.Description,
		Obo:         a.Obo,
		Inverse:     a.Inverse,
		Symmetrical: a.Symmetrical,
		SuperTypes:  a.SuperTypes,
	}
}

// handleNomenVocab returns the NOMEN ontology's class list — the
// controlled vocabulary of nomenclatural statuses under ICZN, ICN,
// ICNP, ICVCN, ICNCP, and ICPN. Served separately from the sfga
// vocabs (/api/vocab) because it comes from an embedded OWL file and
// the frontend fetches it only when the status picker is opened.
// See CLAUDE.md § Nomenclatural status vocabulary (NOMEN).
//
// @Summary      NOMEN vocabulary
// @Description  NOMEN ontology status terms served from an embedded OWL file. Cached for one hour.
// @Tags         vocab
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} problem
// @Router       /vocab/nomen [get]
func (s *server) handleNomenVocab(w http.ResponseWriter, r *http.Request) {
	terms, err := hive.Nomen()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Immutable for the process lifetime — safe to cache aggressively.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": terms})
}

// handleCountriesVocab returns the ISO 3166-1 alpha-2 catalog for
// the vernacular col__country picker (and any other 2-letter
// country lookup). Sourced from ChecklistBank so hive picks
// countries from the same list as CoLDP tooling — see
// hive.Countries().
//
// @Summary      Countries vocabulary
// @Description  ISO 3166-1 alpha-2 country catalog. Cached for one hour.
// @Tags         vocab
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} problem
// @Router       /vocab/countries [get]
func (s *server) handleCountriesVocab(w http.ResponseWriter, r *http.Request) {
	items, err := hive.Countries()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Immutable at build time — long cache is safe.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleLanguagesVocab returns the ISO 639-3 catalog for the
// vernacular col__language picker (and any other 3-letter language
// lookup). Ships ~7900 entries; frontends should fetch on-demand
// (when the picker first opens) rather than at boot.
//
// @Summary      Languages vocabulary
// @Description  ISO 639-3 language catalog. Cached for one hour.
// @Tags         vocab
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} problem
// @Router       /vocab/languages [get]
func (s *server) handleLanguagesVocab(w http.ResponseWriter, r *http.Request) {
	items, err := hive.Languages()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleSexVocab returns the enriched sex vocabulary (name +
// glyph symbol + definition) for the vernacular col__sex_id
// picker. The plain sfga vocab bundle at /api/vocab still ships
// the flat form (id-only from the archive's own `sex` table); the
// enriched form here is drawn from ChecklistBank for pickers that
// want to show the ♀ / ♂ / ⚥ symbol and hover-tooltip.
//
// @Summary      Sex vocabulary
// @Description  Enriched sex terms (name + glyph + definition). Cached for one hour.
// @Tags         vocab
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} problem
// @Router       /vocab/sex [get]
func (s *server) handleSexVocab(w http.ResponseWriter, r *http.Request) {
	items, err := hive.SexTerms()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleKeymap returns the canonical shortcut list from pkg/ui.
// Served whole so the WUI fetches once at boot and holds it in the
// module-level cache alongside the vocab / NOMEN bundles.
//
// The WUI filters client-side to WUI-available bindings; returning
// the full list (including TUI-only rows) keeps the endpoint useful
// to other consumers — future automation tooling, documentation
// generators, or the eventual customization layer.
//
// @Summary      Keymap
// @Description  Canonical shortcut list shared between TUI and WUI. Cached for one hour.
// @Tags         system
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /keymap [get]
func (s *server) handleKeymap(w http.ResponseWriter, r *http.Request) {
	// Same immutable-for-process-lifetime story as vocab / NOMEN.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": ui.Keymap()})
}

// handleGetMetadata returns the dataset metadata row (title,
// description, license, …). Missing metadata is a 404 — legacy
// archives without a seeded row can PATCH one in.
//
// @Summary      Get metadata
// @Description  Fetch the dataset metadata row.
// @Tags         metadata
// @Produce      json
// @Success      200 {object} apiMetadata
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /metadata [get]
func (s *server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
	m, err := s.a.GetMetadata(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	body := metadataToAPI(m)
	body.Warnings = metadataWarnings(s.a, r.Context(), m.ID)
	writeJSON(w, http.StatusOK, body)
}

// handlePatchMetadata applies a partial update to the metadata row.
// Missing row → UpdateMetadata inserts one on the fly, so a PATCH
// against a legacy archive without seeded metadata also serves as the
// initial seed (as long as the patch supplies Title).
//
// @Summary      Update metadata
// @Description  Partially update the dataset metadata row. Seeds a missing row on first PATCH.
// @Tags         metadata
// @Accept       json
// @Produce      json
// @Param        body body apiMetadataPatch true "Fields to update"
// @Success      200 {object} apiMetadata
// @Failure      400 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /metadata [patch]
func (s *server) handlePatchMetadata(w http.ResponseWriter, r *http.Request) {
	var patch apiMetadataPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetMetadata(r.Context())
		if err != nil {
			// Legacy archive without seeded metadata — seed on first
			// PATCH, defaulting Title if the patch didn't supply it
			// (schema requires it NOT NULL).
			current = &hive.Metadata{Title: "Untitled archive"}
		}
		applyMetadataPatch(current, patch)
		return tx.UpdateMetadata(*current)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetMetadata(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, metadataToAPI(fresh))
}

// handleArchive returns the archive's identity: path, schema version, and
// read-only mode. Cheap enough to be called on every WUI boot to display
// the current dataset.
//
// @Summary      Archive identity
// @Description  Report the archive path, schema version, and read-only mode.
// @Tags         system
// @Produce      json
// @Success      200 {object} apiArchive
// @Failure      500 {object} problem
// @Router       /archive [get]
func (s *server) handleArchive(w http.ResponseWriter, r *http.Request) {
	version, err := s.a.SchemaVersion(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apiArchive{
		Path:          s.archivePath,
		SchemaVersion: version,
		ReadOnly:      s.a.IsReadOnly(),
	})
}

// apiReassembleReport is the JSON projection of
// hive.ReassembleReport returned by POST /api/archive/reassemble.
type apiReassembleReport struct {
	PapersLoaded          int                `json:"papers_loaded"`
	PapersSkipped         int                `json:"papers_skipped"`
	Warnings              []string           `json:"warnings,omitempty"`
	Matched               int                `json:"matched"`
	TaxaCreated           int                `json:"taxa_created"`
	SynonymsCreated       int                `json:"synonyms_created"`
	BracketedCreated      int                `json:"bracketed_created"`
	ConceptRelationsAdded int                `json:"concept_relations_added"`
	ConceptFieldsSet      int                `json:"concept_fields_set"`
	TypeMaterialInserted  int                `json:"type_material_inserted"`
	DistributionInserted  int                `json:"distribution_inserted"`
	VernacularInserted    int                `json:"vernacular_inserted"`
	ExtensionRowsReplaced int                `json:"extension_rows_replaced"`
	Flags                 []apiAssemblerFlag `json:"flags,omitempty"`
}

// apiAssemblerFlag mirrors assembler.FlagAction in the response.
type apiAssemblerFlag struct {
	Kind       string   `json:"kind"`
	Message    string   `json:"message"`
	Candidates []string `json:"candidates,omitempty"`
}

// handleReassembleArchive runs the deterministic assembler across
// every compose extraction run in the archive and materializes
// Layer 2 into the taxon / synonym / name / taxon_concept_relation
// tables. See docs/2026-09-12_LITERATURE_VIEW.md § Slice 1.
//
// @Summary      Reassemble literature view from Layer 1 assertions
// @Description  Runs the deterministic assembler across every
// @Description  compose run in the archive. Materializes matched /
// @Description  created taxa, synonyms, [bracket]ed species, and
// @Description  sensu-derived concept relations. Returns per-paper
// @Description  and per-action counters plus any curator-actionable
// @Description  flags.
// @Tags         archive
// @Produce      json
// @Success      200 {object} apiReassembleReport
// @Failure      500 {object} problem
// @Router       /archive/reassemble [post]
func (s *server) handleReassembleArchive(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	var report *hive.ReassembleReport
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		out, err := tx.ExecuteReassemble(assembler.DefaultRules())
		report = out
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reassembleReportToAPI(report))
}

// composeTreeExportEnvelope mirrors the shape of the git-committed
// fixture at testdata/assimilate_demo/compose_run.json so a
// downloaded file can be dropped in as a fixture with no wrapping
// step. Keep this struct in sync with composeRunFixture in
// tools/mkassimilatedemo — they're the same on-disk contract.
type composeTreeExportEnvelope struct {
	RunID       string          `json:"run_id"`
	ReferenceID string          `json:"reference_id"`
	Purpose     string          `json:"purpose"`
	SourceKind  string          `json:"source_kind"`
	SourceID    string          `json:"source_id"`
	Notes       string          `json:"notes,omitempty"`
	CreatedAt   string          `json:"created_at,omitempty"`
	RawOutput   json.RawMessage `json:"raw_output" swaggertype:"object"`
}

// handleExportComposeTree returns the envelope-wrapped compose
// tree for a reference's most recent compose extraction run,
// downloadable as a file with Content-Disposition: attachment.
// Same shape as testdata/assimilate_demo/compose_run.json so the
// download can seed a fresh fixture in place.
//
// Optional query param ?run_id= picks a specific compose run.
// Without it, the most recent compose run for the reference is
// selected. 404 when the reference has no compose run at all
// (or the named run_id isn't a compose run against this
// reference).
//
// Two motivations for having this: a curator inspecting a specific
// run's raw output without spelunking SQLite, and a hedge against
// docker ghost-inode losses (the browser reads through the running
// container, so a ghost inode's contents come out even when host
// tooling only sees a pristine DB).
//
// @Summary      Export a reference's compose tree as JSON
// @Tags         references
// @Produce      json
// @Param        id     path  string true  "Reference ID"
// @Param        run_id query string false "Specific run to export (defaults to most recent compose run)"
// @Success      200 {object} composeTreeExportEnvelope
// @Failure      404 {object} problem
// @Router       /reference/{id}/compose-tree [get]
func (s *server) handleExportComposeTree(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id is required")
		return
	}
	wantRunID := r.URL.Query().Get("run_id")
	runs, err := s.a.ListExtractionRunsByReference(r.Context(), refID, false)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var picked *hive.ExtractionRun
	for i := range runs {
		if runs[i].Purpose != "compose" {
			continue
		}
		if wantRunID != "" && runs[i].RunID != wantRunID {
			continue
		}
		picked = &runs[i]
		break
	}
	if picked == nil {
		msg := fmt.Sprintf("no compose extraction run for reference %q", refID)
		if wantRunID != "" {
			msg = fmt.Sprintf("no compose extraction run %q for reference %q",
				wantRunID, refID)
		}
		writeProblem(w, r, fmt.Errorf("%s: %w", msg, hive.ErrNotFound))
		return
	}
	raw := json.RawMessage(picked.RawOutputJSON)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	env := composeTreeExportEnvelope{
		RunID:       picked.RunID,
		ReferenceID: picked.ReferenceID,
		Purpose:     picked.Purpose,
		SourceKind:  picked.SourceKind,
		SourceID:    picked.SourceID,
		Notes:       picked.Notes,
		CreatedAt:   picked.CreatedAt.Format(time.RFC3339Nano),
		RawOutput:   raw,
	}
	// Include reference id (short prefix) + timestamp in the
	// filename so downloads from different references / runs don't
	// collide in the browser's downloads folder.
	shortRef := refID
	if len(shortRef) > 8 {
		shortRef = shortRef[:8]
	}
	filename := fmt.Sprintf("compose_run_%s_%s.json",
		shortRef, picked.CreatedAt.Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		// Response headers may already be flushed; a log entry is
		// the best we can do at this point.
		_ = err
	}
}

// apiAssemblerNode is the JSON projection of hive.AssemblerNode
// returned by GET /api/archive/assembler-nodes.
type apiAssemblerNode struct {
	TaxonID       string             `json:"taxon_id"`
	NameID        string             `json:"name_id"`
	ParentTaxonID string             `json:"parent_taxon_id,omitempty"`
	Canonical     string             `json:"canonical"`
	Authorship    string             `json:"authorship,omitempty"`
	Rank          string             `json:"rank,omitempty"`
	Status        string             `json:"status,omitempty"`
	Color         string             `json:"color"`
	HasOverlay    bool               `json:"has_overlay"`
	Flags         []apiAssemblerFlag `json:"flags,omitempty"`
}

// handleAssemblerNodes lists every Layer 2 taxon with per-node
// color state derived from Layer 3 overlays and hive__assembler_flag
// rows. Feeds the WUI's assimilate view.
//
// @Summary      List Layer 2 taxa with per-node color state
// @Description  Returns every Layer 2 taxon projected to
// @Description  {taxon_id, canonical, authorship, rank,
// @Description  parent_taxon_id, color, has_overlay, flags[]}.
// @Description  Color derivation: blue > red > amber > green.
// @Description  Consumed by the assimilate view for the four-color
// @Description  tree.
// @Tags         archive
// @Produce      json
// @Success      200 {array} apiAssemblerNode
// @Failure      500 {object} problem
// @Router       /archive/assembler-nodes [get]
func (s *server) handleAssemblerNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.a.ListAssemblerNodes(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := make([]apiAssemblerNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, assemblerNodeToAPI(n))
	}
	writeJSON(w, http.StatusOK, out)
}

func assemblerNodeToAPI(n hive.AssemblerNode) apiAssemblerNode {
	flags := make([]apiAssemblerFlag, 0, len(n.Flags))
	for _, f := range n.Flags {
		flags = append(flags, apiAssemblerFlag{
			Kind:       f.Kind,
			Message:    f.Message,
			Candidates: f.Candidates,
		})
	}
	return apiAssemblerNode{
		TaxonID:       n.TaxonID,
		NameID:        n.NameID,
		ParentTaxonID: n.ParentTaxonID,
		Canonical:     n.Canonical,
		Authorship:    n.Authorship,
		Rank:          n.Rank,
		Status:        n.Status,
		Color:         n.Color,
		HasOverlay:    n.HasOverlay,
		Flags:         flags,
	}
}

// apiLayer3Overlay is the JSON projection of hive.Layer3Overlay
// used by the overlay CRUD endpoints.
type apiLayer3Overlay struct {
	ID                  string `json:"id"`
	TaxonID             string `json:"taxon_id"`
	OverlayType         string `json:"overlay_type"`
	Claim               any    `json:"claim,omitempty"`
	Notes               string `json:"notes,omitempty"`
	CitationReferenceID string `json:"citation_reference_id,omitempty"`
	RetiredAt           string `json:"retired_at,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	CreatedBy           string `json:"created_by,omitempty"`
}

// apiCreateOverlayReq is the POST body for creating an overlay.
// Claim is passed as a decoded object so callers don't have to
// double-encode JSON; the server re-marshals it into claim_json.
type apiCreateOverlayReq struct {
	OverlayType string `json:"overlay_type"`
	Claim       any    `json:"claim"`
	Notes       string `json:"notes,omitempty"`
}

// apiSetOverlayCitationReq is the PATCH body for
// SetOverlayCitation.
type apiSetOverlayCitationReq struct {
	CitationReferenceID string `json:"citation_reference_id"`
}

func overlayToAPI(o hive.Layer3Overlay) apiLayer3Overlay {
	out := apiLayer3Overlay{
		ID:                  o.ID,
		TaxonID:             o.TaxonID,
		OverlayType:         o.OverlayType,
		Notes:               o.Notes,
		CitationReferenceID: o.CitationReferenceID,
		CreatedBy:           o.CreatedBy,
	}
	if !o.CreatedAt.IsZero() {
		out.CreatedAt = o.CreatedAt.Format(time.RFC3339Nano)
	}
	if !o.UpdatedAt.IsZero() {
		out.UpdatedAt = o.UpdatedAt.Format(time.RFC3339Nano)
	}
	if !o.RetiredAt.IsZero() {
		out.RetiredAt = o.RetiredAt.Format(time.RFC3339Nano)
	}
	if o.ClaimJSON != "" {
		var claim any
		if err := json.Unmarshal([]byte(o.ClaimJSON), &claim); err == nil {
			out.Claim = claim
		}
	}
	return out
}

// handleListOverlaysForTaxon returns the active Layer 3 overlays
// anchored to the given taxon id.
//
// @Summary      List Layer 3 overlays for a taxon
// @Tags         overlays
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {array} apiLayer3Overlay
// @Router       /taxon/{id}/overlays [get]
func (s *server) handleListOverlaysForTaxon(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	list, err := s.a.ListOverlaysForTaxon(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := make([]apiLayer3Overlay, 0, len(list))
	for _, o := range list {
		out = append(out, overlayToAPI(o))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateOverlay adds a new Layer 3 overlay.
//
// @Summary      Create a Layer 3 overlay for a taxon
// @Tags         overlays
// @Accept       json
// @Produce      json
// @Param        id   path string             true "Taxon ID"
// @Param        body body apiCreateOverlayReq true "Overlay request"
// @Success      201 {object} apiLayer3Overlay
// @Failure      400 {object} problem
// @Router       /taxon/{id}/overlays [post]
func (s *server) handleCreateOverlay(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	taxonID := r.PathValue("id")
	var body apiCreateOverlayReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.OverlayType) == "" {
		writeBadRequest(w, r, "overlay_type is required")
		return
	}
	claimJSON := "{}"
	if body.Claim != nil {
		b, err := json.Marshal(body.Claim)
		if err != nil {
			writeBadRequest(w, r, "claim payload cannot be marshaled")
			return
		}
		claimJSON = string(b)
	}
	var newID string
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.AddOverlay(hive.Layer3Overlay{
			TaxonID:     taxonID,
			OverlayType: body.OverlayType,
			ClaimJSON:   claimJSON,
			Notes:       body.Notes,
		})
		newID = id
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetOverlay(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, overlayToAPI(*fresh))
}

// handleGetOverlay returns one overlay by id — active OR retired.
//
// @Summary      Get a Layer 3 overlay
// @Tags         overlays
// @Produce      json
// @Param        id path string true "Overlay ID"
// @Success      200 {object} apiLayer3Overlay
// @Failure      404 {object} problem
// @Router       /overlay/{id} [get]
func (s *server) handleGetOverlay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	o, err := s.a.GetOverlay(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overlayToAPI(*o))
}

// handleRetireOverlay stamps retired_at on an active overlay.
// Idempotent-adjacent: a second call on the same overlay returns
// 404 because the WHERE clause excludes retired rows.
//
// @Summary      Retire a Layer 3 overlay
// @Tags         overlays
// @Param        id path string true "Overlay ID"
// @Success      204
// @Failure      404 {object} problem
// @Router       /overlay/{id} [delete]
func (s *server) handleRetireOverlay(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	id := r.PathValue("id")
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.RetireOverlay(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetOverlayCitation attaches (or clears) the citation
// reference id on an active overlay.
//
// @Summary      Set the citation reference on an overlay
// @Tags         overlays
// @Accept       json
// @Param        id   path string                    true "Overlay ID"
// @Param        body body apiSetOverlayCitationReq true "Citation reference"
// @Success      200 {object} apiLayer3Overlay
// @Failure      404 {object} problem
// @Router       /overlay/{id}/citation [patch]
func (s *server) handleSetOverlayCitation(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	id := r.PathValue("id")
	var body apiSetOverlayCitationReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.SetOverlayCitation(id, body.CitationReferenceID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetOverlay(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overlayToAPI(*fresh))
}

func reassembleReportToAPI(r *hive.ReassembleReport) apiReassembleReport {
	out := apiReassembleReport{
		PapersLoaded:          r.PapersLoaded,
		PapersSkipped:         r.PapersSkipped,
		Warnings:              r.Warnings,
		Matched:               r.AssemblerReport.Matched,
		TaxaCreated:           r.AssemblerReport.TaxaCreated,
		SynonymsCreated:       r.AssemblerReport.SynonymsCreated,
		BracketedCreated:      r.AssemblerReport.BracketedCreated,
		ConceptRelationsAdded: r.AssemblerReport.ConceptRelationsInserted,
		ConceptFieldsSet:      r.AssemblerReport.ConceptFieldsSet,
		TypeMaterialInserted:  r.TypeMaterialInserted,
		DistributionInserted:  r.DistributionInserted,
		VernacularInserted:    r.VernacularInserted,
		ExtensionRowsReplaced: r.ExtensionRowsReplaced,
	}
	for _, f := range r.AssemblerReport.Flags {
		out.Flags = append(out.Flags, apiAssemblerFlag{
			Kind:       f.Kind.String(),
			Message:    f.Message,
			Candidates: f.Candidates,
		})
	}
	return out
}

// handleRoots is a paginated projection of root-level taxa. Alias of
// /api/taxon/{id}/children with an empty parent ID; kept as a dedicated
// endpoint so WUI/TUI code that wants the top of the tree doesn't need to
// invent a magic sentinel.
//
// @Summary      List root taxa
// @Description  Paginated list of root-level taxa.
// @Tags         taxa
// @Produce      json
// @Param        limit query int false "Page size"
// @Param        offset query int false "Row offset"
// @Success      200 {object} apiPage[apiTaxonHit]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/roots [get]
func (s *server) handleRoots(w http.ResponseWriter, r *http.Request) {
	s.writeChildren(w, r, "")
}

// handleGetTaxon returns one taxon by ID.
//
// @Summary      Get taxon
// @Description  Fetch a taxon by ID. The response ETag carries col__modified
// @Description  for optimistic concurrency; pass it back as If-Match on PATCH.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiTaxon
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id} [get]
func (s *server) handleGetTaxon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// ETag = col__modified so PATCH callers can round-trip it in If-Match.
	if t.Modified != "" {
		w.Header().Set("ETag", t.Modified)
	}
	body := taxonToAPI(t)
	// Own label — canonical + authorship + italics based on rank.
	// One extra query for the rank field on the associated name row.
	if ref, err := s.a.TaxonRef(r.Context(), t.ID); err == nil {
		body.Label = apiLabel{Text: ref.Label.Text, HTML: ref.Label.HTML}
	}
	// Resolved parent — id + label. Missing parent leaves body.Parent nil.
	if t.ParentID != "" {
		if ref, err := s.a.TaxonRef(r.Context(), t.ParentID); err == nil {
			body.Parent = &apiRef{
				ID:    ref.ID,
				Label: apiLabel{Text: ref.Label.Text, HTML: ref.Label.HTML},
			}
		}
	}
	body.Warnings = validationWarnings(s.a, r.Context(), t.ID, t.NameID)
	writeJSON(w, http.StatusOK, body)
}

// handleChildren returns a paginated list of the given taxon's children.
//
// @Summary      List taxon children
// @Description  Paginated list of children under the given parent taxon.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Parent taxon ID"
// @Param        limit query int false "Page size"
// @Param        offset query int false "Row offset"
// @Success      200 {object} apiPage[apiTaxonHit]
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/children [get]
func (s *server) handleChildren(w http.ResponseWriter, r *http.Request) {
	s.writeChildren(w, r, r.PathValue("id"))
}

// writeChildren is the shared paginated-children implementation used by both
// /api/taxon/roots and /api/taxon/{id}/children.
func (s *server) writeChildren(w http.ResponseWriter, r *http.Request, parentID string) {
	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	hits, total, err := s.a.ListChildrenPage(r.Context(), parentID, limit, offset)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	page := apiPage[apiTaxonHit]{Items: make([]apiTaxonHit, 0, len(hits))}
	for _, h := range hits {
		page.Items = append(page.Items, hitToAPI(h))
	}
	page.Total = &total
	// Advance cursor only when we returned a full page AND there's more data.
	if len(hits) == limit && offset+limit < total {
		page.NextCursor = encodeCursor(offset + limit)
	}
	writeJSON(w, http.StatusOK, page)
}

// handleCodeDefault returns the nomenclatural code the new-taxon form
// should seed the code picker with — the code_id from the given parent
// taxon's associated name row, or "" if none is set. Frontends call
// this when opening the create form under a selected parent.
//
// @Summary      Default code for parent
// @Description  Seed value for the code picker on a new child of the given parent.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Parent taxon ID"
// @Success      200 {object} map[string]string
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/code-default [get]
func (s *server) handleCodeDefault(w http.ResponseWriter, r *http.Request) {
	parentID := r.PathValue("id")
	code, err := s.a.CodeForParent(r.Context(), parentID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"code": code})
}

// handleCreateNamePrefix returns the string a new child of parentID
// should have pre-populated in its scientific-name field, ending with
// a space so the curator's cursor lands ready to type the new epithet.
// Empty prefix ("" — child is a fresh uninomial) is a valid response.
// See hive.CreateNamePrefix for the exact rules.
//
// @Summary      Create-name prefix
// @Description  Pre-populated scientific-name prefix for a new child of the parent taxon.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Parent taxon ID"
// @Success      200 {object} map[string]string
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/create-name-prefix [get]
func (s *server) handleCreateNamePrefix(w http.ResponseWriter, r *http.Request) {
	parentID := r.PathValue("id")
	prefix, err := s.a.CreateNamePrefix(r.Context(), parentID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"prefix": prefix})
}

// handleChildRanks returns the ranks valid as children of the given
// parent, per the TW-derived rank hierarchy filtered by the parent's
// own rank + code. Each item is {"id": ..., "typical_use": bool}.
// Empty items array means "no filter" — front-ends should show the
// full rank vocab for that case. Typical_use lets the picker default
// to the common ranks; the curator can widen via search or an
// explicit "show all" toggle.
// See hive.Archive.ValidChildRanks for filter logic.
//
// @Summary      Valid child ranks
// @Description  Ranks valid as children of the given parent taxon.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Parent taxon ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/child-ranks [get]
func (s *server) handleChildRanks(w http.ResponseWriter, r *http.Request) {
	parentID := r.PathValue("id")
	ranks, err := s.a.ValidChildRanks(r.Context(), parentID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if ranks == nil {
		ranks = []ui.ChildRank{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ranks})
}

// handleClassification returns the taxon's full ancestor chain in
// root-down order, INCLUDING the taxon itself as the last element.
// Each item is an apiTaxonHit — id + display name + rank + status —
// which is enough for the WUI to render breadcrumbs and to drive a
// single-round-trip tree reveal (fetch classification, then fire all
// per-ancestor children requests in parallel instead of sequentially).
//
// Empty items array means the taxon id is unknown.
//
// @Summary      Classification chain
// @Description  Full ancestor chain in root-down order, including the taxon itself as the last element.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiPage[apiTaxonHit]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/classification [get]
func (s *server) handleClassification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hits, err := s.a.Classification(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiTaxonHit, 0, len(hits))
	for _, h := range hits {
		items = append(items, hitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiTaxonHit]{Items: items})
}

// handleAncestors returns the taxon's parent chain in root-down order
// (excluding the taxon itself). Front-ends use this to expand the tree
// down to a moved taxon so it appears in its new location without a full
// reload.
//
// Kept alongside handleClassification for two reasons: any caller that
// only needs the id chain doesn't have to pay for the name+rank join,
// and the existing WUI shell code paths that just want the chain don't
// have to be rewritten in the same change that adds classification.
//
// @Summary      Ancestor id chain
// @Description  Parent-id chain in root-down order, excluding the taxon itself.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/ancestors [get]
func (s *server) handleAncestors(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ids, err := s.a.Ancestors(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if ids == nil {
		ids = []string{} // JSON [] rather than null for a clean wire shape
	}
	writeJSON(w, http.StatusOK, map[string]any{"ids": ids})
}

// handleNomenclaturalHistory returns the taxon's multi-cluster
// basionym-anchored nomenclatural history. Backs the "Nomenclatural
// history" section on the WUI/TUI detail page — see DESIGN.md.
//
// Every hydrated name has its ReferenceID → ReferenceLabel resolved
// here so the frontend footnote accumulator can render citations
// inline without a per-row round trip. Same treatment the synonym
// endpoint just picked up.
//
// @Summary      Nomenclatural history
// @Description  Basionym-anchored nomenclatural history for a taxon, grouped into original-combination clusters.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiNomenHistory
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/nomenclatural-history [get]
func (s *server) handleNomenclaturalHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hist, err := s.a.NomenclaturalHistory(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := apiNomenHistory{Clusters: make([]apiNomenCluster, 0, len(hist.Clusters))}
	for _, c := range hist.Clusters {
		outC := apiNomenCluster{
			Role:  c.Role,
			Names: make([]apiNomenName, 0, len(c.Names)),
		}
		for _, n := range c.Names {
			row := apiNomenName{
				NameID:                    n.NameID,
				Label:                     apiLabel{Text: n.Label.Text, HTML: n.Label.HTML},
				Authorship:                n.Authorship,
				Rank:                      n.Rank,
				Year:                      n.Year,
				IsBasionym:                n.IsBasionym,
				Involvement:               n.Involvement,
				SynonymID:                 n.SynonymID,
				ReferenceID:               n.ReferenceID,
				BasionymAuthorship:        n.BasionymAuthorship,
				BasionymAuthorshipYear:    n.BasionymAuthorshipYear,
				CombinationAuthorship:     n.CombinationAuthorship,
				CombinationAuthorshipYear: n.CombinationAuthorshipYear,
				IssueCount:                n.IssueCount,
				MaxSeverity:               n.MaxSeverity,
			}
			row.ReferenceLabel = s.referenceLabel(r.Context(), firstCSVID(row.ReferenceID))
			outC.Names = append(outC.Names, row)
		}
		out.Clusters = append(out.Clusters, outC)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSynonyms returns the synonym rows attached to the given taxon.
//
// @Summary      List taxon synonyms
// @Description  Synonym rows for a taxon, each with a server-rendered display label and resolved reference label.
// @Tags         synonyms
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiPage[apiSynonym]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/synonyms [get]
func (s *server) handleSynonyms(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	// ListSynonymHits joins each synonym to its name so we can ship a
	// display string on the wire — front-ends stop needing per-synonym
	// name fetches.
	hits, err := s.a.ListSynonymHits(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiSynonym, 0, len(hits))
	for _, h := range hits {
		item := synonymHitToAPI(h)
		// Resolve the reference label so the frontend footnote
		// accumulator can render inline citations without a per-row
		// round trip. sfga stores synonym.col__reference_id as a
		// comma-separated list; the first id is the primary
		// citation for label rendering. Frontend re-splits when it
		// needs to number every id separately.
		item.ReferenceLabel = s.referenceLabel(r.Context(), firstCSVID(item.ReferenceID))
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, apiPage[apiSynonym]{Items: items})
}

// firstCSVID returns the first non-empty comma-separated fragment of
// s, trimmed. Applies to sfga columns that hold multiple ids as CSV
// (synonym.col__reference_id and its cousins).
func firstCSVID(s string) string {
	if s == "" {
		return ""
	}
	head, _, _ := strings.Cut(s, ",")
	return strings.TrimSpace(head)
}

// handleListVernaculars returns every vernacular row attached to
// the given taxon, ordered preferred-first per language. The rowid
// handle is stringified into `id` on the wire so front-ends address
// individual rows against PATCH/DELETE /api/vernacular/{id}.
//
// @Summary      List vernaculars
// @Description  Vernacular-name rows attached to a taxon, preferred-first per language.
// @Tags         vernaculars
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiPage[apiVernacular]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/vernaculars [get]
func (s *server) handleListVernaculars(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	hits, err := s.a.ListVernaculars(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiVernacular, 0, len(hits))
	for _, h := range hits {
		items = append(items, vernacularHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiVernacular]{Items: items})
}

// handleCreateVernacular writes a new vernacular row attached to
// the {id} taxon and returns the freshly-hydrated apiVernacular so
// the caller can splice it into local state without a follow-up
// list refresh. TaxonID is taken from the path, not the body —
// consistent with POST /api/taxon/{id}/basionym.
//
// @Summary      Create vernacular
// @Description  Add a vernacular-name row to the given taxon.
// @Tags         vernaculars
// @Accept       json
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Param        body body apiVernacular true "New vernacular row"
// @Success      201 {object} apiVernacular
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/vernaculars [post]
func (s *server) handleCreateVernacular(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	var body apiVernacular
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.TaxonID = taxonID // path wins over body
	if strings.TrimSpace(body.Name) == "" {
		writeBadRequest(w, r, "name is required")
		return
	}
	var newID int64
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.AddVernacular(coldp.Vernacular{
			TaxonID:         body.TaxonID,
			SourceID:        body.SourceID,
			Name:            body.Name,
			Transliteration: body.Transliteration,
			Language:        body.Language,
			Preferred:       ptrBoolToNull(body.Preferred),
			Country:         body.Country,
			Area:            body.Area,
			Sex:             coldp.NewSex(body.Sex),
			ReferenceID:     body.ReferenceID,
			Remarks:         body.Remarks,
		})
		newID = id
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetVernacular(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, vernacularHitToAPI(*fresh))
}

// handlePatchVernacular applies a partial update. Nil fields on the
// patch mean "leave alone"; a set pointer to zero-value clears the
// field. TaxonID is not editable — reparent a vernacular by
// delete+add on the new taxon.
//
// @Summary      Update vernacular
// @Description  Partially update a vernacular row. TaxonID is not editable.
// @Tags         vernaculars
// @Accept       json
// @Produce      json
// @Param        id path string true "Vernacular rowid"
// @Param        body body apiVernacularPatch true "Fields to update"
// @Success      200 {object} apiVernacular
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /vernacular/{id} [patch]
func (s *server) handlePatchVernacular(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseVernacularRowID(w, r)
	if !ok {
		return
	}
	var patch apiVernacularPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetVernacular(r.Context(), rowid)
		if err != nil {
			return err
		}
		merged := coldp.Vernacular{
			TaxonID:         current.TaxonID,
			SourceID:        current.SourceID,
			Name:            current.Name,
			Transliteration: current.Transliteration,
			Language:        current.Language,
			Preferred:       current.Preferred,
			Country:         current.Country,
			Area:            current.Area,
			Sex:             current.Sex,
			ReferenceID:     current.ReferenceID,
			Remarks:         current.Remarks,
		}
		applyVernacularPatch(&merged, patch)
		return tx.UpdateVernacular(rowid, merged)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetVernacular(r.Context(), rowid)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vernacularHitToAPI(*fresh))
}

// handleDeleteVernacular removes the row at the given rowid.
// Unknown row → 404 via ErrNotFound.
//
// @Summary      Delete vernacular
// @Description  Delete a vernacular row by rowid.
// @Tags         vernaculars
// @Param        id path string true "Vernacular rowid"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /vernacular/{id} [delete]
func (s *server) handleDeleteVernacular(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseVernacularRowID(w, r)
	if !ok {
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteVernacular(rowid)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseVernacularRowID pulls the {id} path parameter and parses it
// as an int64. On failure it writes a 400 and returns false so the
// caller can early-return.
func parseVernacularRowID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeBadRequest(w, r, "invalid vernacular id: "+raw)
		return 0, false
	}
	return n, true
}

// applyVernacularPatch layers the pointer-optional patch onto the
// merged coldp.Vernacular. Nil pointer → leave alone; set pointer
// (even to zero value) → overwrite. Preferred is *bool → sql.NullBool
// via ptrBoolToNull so an explicit false round-trips.
func applyVernacularPatch(v *coldp.Vernacular, p apiVernacularPatch) {
	if p.Name != nil {
		v.Name = *p.Name
	}
	if p.Transliteration != nil {
		v.Transliteration = *p.Transliteration
	}
	if p.Language != nil {
		v.Language = *p.Language
	}
	if p.Preferred != nil {
		v.Preferred = ptrBoolToNull(p.Preferred)
	}
	if p.Country != nil {
		v.Country = *p.Country
	}
	if p.Area != nil {
		v.Area = *p.Area
	}
	if p.Sex != nil {
		v.Sex = coldp.NewSex(*p.Sex)
	}
	if p.SourceID != nil {
		v.SourceID = *p.SourceID
	}
	if p.ReferenceID != nil {
		v.ReferenceID = *p.ReferenceID
	}
	if p.Remarks != nil {
		v.Remarks = *p.Remarks
	}
}

// ptrBoolToNull converts a wire *bool into sql.NullBool: nil → invalid,
// set → valid with the given value. Mirror of nullBoolToPtr used on
// the read side.
func ptrBoolToNull(p *bool) sql.NullBool {
	if p == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *p, Valid: true}
}

// handleListDistributions returns every distribution row attached
// to the given taxon, ordered by gazetteer + area. Same rowid-as-
// string handle pattern as vernaculars — front-ends address rows
// via PATCH/DELETE /api/distribution/{id}.
//
// @Summary      List distributions
// @Description  Distribution rows attached to a taxon, ordered by gazetteer + area.
// @Tags         distributions
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiPage[apiDistribution]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/distributions [get]
func (s *server) handleListDistributions(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	hits, err := s.a.ListDistributions(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiDistribution, 0, len(hits))
	for _, h := range hits {
		items = append(items, distributionHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiDistribution]{Items: items})
}

// handleCreateDistribution writes a new distribution row attached
// to the {id} taxon and returns the freshly-hydrated
// apiDistribution so the caller can splice it into local state
// without a follow-up list refresh. TaxonID is taken from the
// path, not the body — consistent with the vernacular create.
//
// @Summary      Create distribution
// @Description  Add a distribution row to the given taxon.
// @Tags         distributions
// @Accept       json
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Param        body body apiDistribution true "New distribution row"
// @Success      201 {object} apiDistribution
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/distributions [post]
func (s *server) handleCreateDistribution(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	var body apiDistribution
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.TaxonID = taxonID
	var newID int64
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.AddDistribution(coldp.Distribution{
			TaxonID:     body.TaxonID,
			SourceID:    body.SourceID,
			Area:        body.Area,
			AreaID:      body.AreaID,
			Gazetteer:   coldp.NewGazetteerEnt(body.Gazetteer),
			Status:      coldp.NewDistrStatus(body.Status),
			ReferenceID: body.ReferenceID,
			Remarks:     body.Remarks,
		})
		newID = id
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetDistribution(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, distributionHitToAPI(*fresh))
}

// handlePatchDistribution applies a partial update. Nil fields on
// the patch mean "leave alone"; a set pointer to zero-value
// clears the field. TaxonID is not editable — reparent via
// delete+add on the new taxon.
//
// @Summary      Update distribution
// @Description  Partially update a distribution row. TaxonID is not editable.
// @Tags         distributions
// @Accept       json
// @Produce      json
// @Param        id path string true "Distribution rowid"
// @Param        body body apiDistributionPatch true "Fields to update"
// @Success      200 {object} apiDistribution
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /distribution/{id} [patch]
func (s *server) handlePatchDistribution(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseDistributionRowID(w, r)
	if !ok {
		return
	}
	var patch apiDistributionPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetDistribution(r.Context(), rowid)
		if err != nil {
			return err
		}
		merged := coldp.Distribution{
			TaxonID:     current.TaxonID,
			SourceID:    current.SourceID,
			Area:        current.Area,
			AreaID:      current.AreaID,
			Gazetteer:   current.Gazetteer,
			Status:      current.Status,
			ReferenceID: current.ReferenceID,
			Remarks:     current.Remarks,
		}
		applyDistributionPatch(&merged, patch)
		return tx.UpdateDistribution(rowid, merged)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetDistribution(r.Context(), rowid)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, distributionHitToAPI(*fresh))
}

// handleDeleteDistribution removes the row at the given rowid.
// Unknown row → 404 via ErrNotFound.
//
// @Summary      Delete distribution
// @Description  Delete a distribution row by rowid.
// @Tags         distributions
// @Param        id path string true "Distribution rowid"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /distribution/{id} [delete]
func (s *server) handleDeleteDistribution(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseDistributionRowID(w, r)
	if !ok {
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteDistribution(rowid)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseDistributionRowID pulls the {id} path parameter and parses
// it as an int64. Mirrors parseVernacularRowID.
func parseDistributionRowID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeBadRequest(w, r, "invalid distribution id: "+raw)
		return 0, false
	}
	return n, true
}

// applyDistributionPatch layers the pointer-optional patch onto
// the merged coldp.Distribution. Enum fields (gazetteer, status)
// go through coldp constructors so empty-string means "clear the
// enum" cleanly.
func applyDistributionPatch(d *coldp.Distribution, p apiDistributionPatch) {
	if p.Area != nil {
		d.Area = *p.Area
	}
	if p.AreaID != nil {
		d.AreaID = *p.AreaID
	}
	if p.Gazetteer != nil {
		d.Gazetteer = coldp.NewGazetteerEnt(*p.Gazetteer)
	}
	if p.Status != nil {
		d.Status = coldp.NewDistrStatus(*p.Status)
	}
	if p.SourceID != nil {
		d.SourceID = *p.SourceID
	}
	if p.ReferenceID != nil {
		d.ReferenceID = *p.ReferenceID
	}
	if p.Remarks != nil {
		d.Remarks = *p.Remarks
	}
}

// handleListSpeciesInteractions returns every interaction row
// where the given taxon is the subject (col__taxon_id). Same
// rowid-as-string handle pattern as vernacular / distribution.
// Each row carries a server-resolved related-taxon label so the
// front-end row can render without a follow-up fetch.
//
// @Summary      List species interactions
// @Description  Species-interaction rows where the given taxon is the subject.
// @Tags         species-interactions
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} apiPage[apiSpeciesInteraction]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/species-interactions [get]
func (s *server) handleListSpeciesInteractions(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	hits, err := s.a.ListSpeciesInteractions(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiSpeciesInteraction, 0, len(hits))
	for _, h := range hits {
		items = append(items, speciesInteractionHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiSpeciesInteraction]{Items: items})
}

// handleCreateSpeciesInteraction writes a new interaction row
// attached to the {id} taxon and returns the freshly-hydrated
// apiSpeciesInteraction so the caller can splice it into local
// state without a follow-up list refresh. TaxonID from path
// wins over body.
//
// @Summary      Create species interaction
// @Description  Add a species-interaction row where the given taxon is the subject.
// @Tags         species-interactions
// @Accept       json
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Param        body body apiSpeciesInteraction true "New interaction row"
// @Success      201 {object} apiSpeciesInteraction
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/species-interactions [post]
func (s *server) handleCreateSpeciesInteraction(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	var body apiSpeciesInteraction
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.TaxonID = taxonID
	var newID int64
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.AddSpeciesInteraction(coldp.SpeciesInteraction{
			TaxonID:                    body.TaxonID,
			RelatedTaxonID:             body.RelatedTaxonID,
			RelatedTaxonScientificName: body.RelatedTaxonScientificName,
			SourceID:                   body.SourceID,
			Type:                       coldp.NewSpInteractionType(body.Type),
			ReferenceID:                body.ReferenceID,
			Remarks:                    body.Remarks,
		}, body.Type)
		newID = id
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetSpeciesInteraction(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, speciesInteractionHitToAPI(*fresh))
}

// handlePatchSpeciesInteraction applies a partial update. Nil
// fields on the patch mean "leave alone"; a set pointer to
// zero-value clears the field. TaxonID is not editable —
// reparent via delete+add on the new taxon.
//
// @Summary      Update species interaction
// @Description  Partially update a species-interaction row. TaxonID is not editable.
// @Tags         species-interactions
// @Accept       json
// @Produce      json
// @Param        id path string true "Species interaction rowid"
// @Param        body body apiSpeciesInteractionPatch true "Fields to update"
// @Success      200 {object} apiSpeciesInteraction
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /species-interaction/{id} [patch]
func (s *server) handlePatchSpeciesInteraction(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseSpeciesInteractionRowID(w, r)
	if !ok {
		return
	}
	var patch apiSpeciesInteractionPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetSpeciesInteraction(r.Context(), rowid)
		if err != nil {
			return err
		}
		merged := coldp.SpeciesInteraction{
			TaxonID:                    current.TaxonID,
			RelatedTaxonID:             current.RelatedTaxonID,
			RelatedTaxonScientificName: current.RelatedTaxonScientificName,
			SourceID:                   current.SourceID,
			Type:                       current.Type,
			ReferenceID:                current.ReferenceID,
			Remarks:                    current.Remarks,
		}
		applySpeciesInteractionPatch(&merged, patch)
		// Preserve the raw type string across the round-trip. When the
		// patch touched Type, use its literal value (curator's choice —
		// controlled or freeform). Otherwise carry forward the raw
		// value from the current row so unknown-vocab types don't get
		// silently zeroed by the enum coercion.
		typeRaw := current.TypeRaw
		if patch.Type != nil {
			typeRaw = *patch.Type
		}
		return tx.UpdateSpeciesInteraction(rowid, merged, typeRaw)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetSpeciesInteraction(r.Context(), rowid)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, speciesInteractionHitToAPI(*fresh))
}

// handleDeleteSpeciesInteraction removes the row at the given
// rowid. Unknown row → 404 via ErrNotFound.
//
// @Summary      Delete species interaction
// @Description  Delete a species-interaction row by rowid.
// @Tags         species-interactions
// @Param        id path string true "Species interaction rowid"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /species-interaction/{id} [delete]
func (s *server) handleDeleteSpeciesInteraction(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseSpeciesInteractionRowID(w, r)
	if !ok {
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteSpeciesInteraction(rowid)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseSpeciesInteractionRowID pulls the {id} path parameter and
// parses it as an int64. Mirrors parseVernacularRowID /
// parseDistributionRowID.
func parseSpeciesInteractionRowID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeBadRequest(w, r, "invalid species-interaction id: "+raw)
		return 0, false
	}
	return n, true
}

// handleListTypeMaterials returns every type_material row attached
// to the given name, ordered by status. The rowid handle is
// stringified into `id` on the wire so front-ends address rows via
// PATCH/DELETE /api/type-material/{id}.
//
// @Summary      List type materials
// @Description  Type-material rows attached to a name, ordered by status.
// @Tags         type-materials
// @Produce      json
// @Param        id path string true "Name ID"
// @Success      200 {object} apiPage[apiTypeMaterial]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id}/type-materials [get]
func (s *server) handleListTypeMaterials(w http.ResponseWriter, r *http.Request) {
	nameID := r.PathValue("id")
	hits, err := s.a.ListTypeMaterials(r.Context(), nameID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiTypeMaterial, 0, len(hits))
	for _, h := range hits {
		items = append(items, typeMaterialHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiTypeMaterial]{Items: items})
}

// handleCreateTypeMaterial writes a new type_material row attached
// to the {id} name and returns the freshly-hydrated apiTypeMaterial
// so the caller can splice it into local state without a follow-up
// list refresh. NameID is taken from the path, not the body.
//
// @Summary      Create type material
// @Description  Add a type-material row to the given name.
// @Tags         type-materials
// @Accept       json
// @Produce      json
// @Param        id path string true "Name ID"
// @Param        body body apiTypeMaterial true "New type-material row"
// @Success      201 {object} apiTypeMaterial
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id}/type-materials [post]
func (s *server) handleCreateTypeMaterial(w http.ResponseWriter, r *http.Request) {
	nameID := r.PathValue("id")
	var body apiTypeMaterial
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.NameID = nameID // path wins over body
	var newID int64
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.AddTypeMaterial(coldp.TypeMaterial{
			ID:                  body.SpecimenID,
			NameID:              body.NameID,
			SourceID:            body.SourceID,
			Citation:            body.Citation,
			Status:              coldp.NewTypeStatus(body.Status),
			InstitutionCode:     body.InstitutionCode,
			CatalogNumber:       body.CatalogNumber,
			ReferenceID:         body.ReferenceID,
			Locality:            body.Locality,
			Country:             body.Country,
			Latitude:            ptrFloat64ToNull(body.Latitude),
			Longitude:           ptrFloat64ToNull(body.Longitude),
			Altitude:            ptrIntToNull(body.Altitude),
			Host:                body.Host,
			Sex:                 coldp.NewSex(body.Sex),
			Date:                body.Date,
			Collector:           body.Collector,
			AssociatedSequences: body.AssociatedSequences,
			Link:                body.Link,
			Remarks:             body.Remarks,
		})
		newID = id
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetTypeMaterial(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, typeMaterialHitToAPI(*fresh))
}

// handlePatchTypeMaterial applies a partial update. Nil fields on the
// patch mean "leave alone"; a set pointer overwrites (including with
// zero-value strings / coordinates). NameID isn't editable — reparent
// a type_material row by delete+add on the new name.
//
// @Summary      Update type material
// @Description  Partially update a type-material row. NameID is not editable.
// @Tags         type-materials
// @Accept       json
// @Produce      json
// @Param        id path string true "Type-material rowid"
// @Param        body body apiTypeMaterialPatch true "Fields to update"
// @Success      200 {object} apiTypeMaterial
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /type-material/{id} [patch]
func (s *server) handlePatchTypeMaterial(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseTypeMaterialRowID(w, r)
	if !ok {
		return
	}
	var patch apiTypeMaterialPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetTypeMaterial(r.Context(), rowid)
		if err != nil {
			return err
		}
		merged := coldp.TypeMaterial{
			ID:                  current.SpecimenID,
			NameID:              current.NameID,
			SourceID:            current.SourceID,
			Citation:            current.Citation,
			Status:              current.Status,
			InstitutionCode:     current.InstitutionCode,
			CatalogNumber:       current.CatalogNumber,
			ReferenceID:         current.ReferenceID,
			Locality:            current.Locality,
			Country:             current.Country,
			Latitude:            current.Latitude,
			Longitude:           current.Longitude,
			Altitude:            current.Altitude,
			Host:                current.Host,
			Sex:                 current.Sex,
			Date:                current.Date,
			Collector:           current.Collector,
			AssociatedSequences: current.AssociatedSequences,
			Link:                current.Link,
			Remarks:             current.Remarks,
		}
		applyTypeMaterialPatch(&merged, patch)
		return tx.UpdateTypeMaterial(rowid, merged)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetTypeMaterial(r.Context(), rowid)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, typeMaterialHitToAPI(*fresh))
}

// handleDeleteTypeMaterial removes the row at the given rowid.
// Unknown row → 404 via ErrNotFound.
//
// @Summary      Delete type material
// @Description  Delete a type-material row by rowid.
// @Tags         type-materials
// @Param        id path string true "Type-material rowid"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /type-material/{id} [delete]
func (s *server) handleDeleteTypeMaterial(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseTypeMaterialRowID(w, r)
	if !ok {
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteTypeMaterial(rowid)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseTypeMaterialRowID pulls the {id} path parameter and parses it
// as an int64. Mirrors parseVernacularRowID.
func parseTypeMaterialRowID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeBadRequest(w, r, "invalid type-material id: "+raw)
		return 0, false
	}
	return n, true
}

// applyTypeMaterialPatch layers the pointer-optional patch onto the
// merged coldp.TypeMaterial. Nil pointer → leave alone; set pointer
// → overwrite. Coordinates use ptrFloat64ToNull / ptrIntToNull so
// an explicit numeric round-trips as sql.NullFloat64{Valid:true}.
func applyTypeMaterialPatch(m *coldp.TypeMaterial, p apiTypeMaterialPatch) {
	if p.SpecimenID != nil {
		m.ID = *p.SpecimenID
	}
	if p.Citation != nil {
		m.Citation = *p.Citation
	}
	if p.Status != nil {
		m.Status = coldp.NewTypeStatus(*p.Status)
	}
	if p.InstitutionCode != nil {
		m.InstitutionCode = *p.InstitutionCode
	}
	if p.CatalogNumber != nil {
		m.CatalogNumber = *p.CatalogNumber
	}
	if p.ReferenceID != nil {
		m.ReferenceID = *p.ReferenceID
	}
	if p.Locality != nil {
		m.Locality = *p.Locality
	}
	if p.Country != nil {
		m.Country = *p.Country
	}
	if p.Latitude != nil {
		m.Latitude = ptrFloat64ToNull(p.Latitude)
	}
	if p.Longitude != nil {
		m.Longitude = ptrFloat64ToNull(p.Longitude)
	}
	if p.Altitude != nil {
		m.Altitude = ptrIntToNull(p.Altitude)
	}
	if p.Host != nil {
		m.Host = *p.Host
	}
	if p.Sex != nil {
		m.Sex = coldp.NewSex(*p.Sex)
	}
	if p.Date != nil {
		m.Date = *p.Date
	}
	if p.Collector != nil {
		m.Collector = *p.Collector
	}
	if p.AssociatedSequences != nil {
		m.AssociatedSequences = *p.AssociatedSequences
	}
	if p.Link != nil {
		m.Link = *p.Link
	}
	if p.SourceID != nil {
		m.SourceID = *p.SourceID
	}
	if p.Remarks != nil {
		m.Remarks = *p.Remarks
	}
}

// ptrFloat64ToNull converts a wire *float64 into sql.NullFloat64:
// nil → invalid, set → valid with the given value. Mirror of
// nullFloat64ToPtr used on the read side.
func ptrFloat64ToNull(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

// ptrIntToNull converts a wire *int into sql.NullInt64: nil →
// invalid, set → valid. Mirror of nullInt64ToPtr.
func ptrIntToNull(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

// handleListNameRelations returns every name_relation row where the
// {id} name is either the subject or the object. Each row's
// related_name is resolved to a full apiRef (id + rendered label) so
// the caller renders the counterpart's canonical + authorship without
// a per-row fetch. Reference label is populated when the row cites
// one.
//
// @Summary      List name relations
// @Description  Name-relation rows where the given name is subject or object, with resolved counterpart labels.
// @Tags         name-relations
// @Produce      json
// @Param        id path string true "Name ID"
// @Success      200 {object} apiPage[apiNameRelation]
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id}/relations [get]
func (s *server) handleListNameRelations(w http.ResponseWriter, r *http.Request) {
	nameID := r.PathValue("id")
	hits, err := s.a.ListNameRelations(r.Context(), nameID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiNameRelation, 0, len(hits))
	for _, h := range hits {
		items = append(items, s.nameRelationHitToAPI(r.Context(), nameID, h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiNameRelation]{Items: items})
}

// handleCreateNameRelation writes a new name_relation row. The subject
// name comes from the path ({id}); the body carries related_name_id +
// type + optional reference / page / remarks. Returns the freshly
// hydrated apiNameRelation so callers can splice into local state
// without a follow-up list refresh.
//
// @Summary      Create name relation
// @Description  Link a name to a related name with a nom_rel_type relation.
// @Tags         name-relations
// @Accept       json
// @Produce      json
// @Param        id path string true "Subject name ID"
// @Param        body body apiNameRelationCreate true "Relation payload"
// @Success      201 {object} apiNameRelation
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id}/relations [post]
func (s *server) handleCreateNameRelation(w http.ResponseWriter, r *http.Request) {
	nameID := r.PathValue("id")
	var body apiNameRelationCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.RelatedNameID) == "" {
		writeBadRequest(w, r, "related_name_id is required")
		return
	}
	if strings.TrimSpace(body.Type) == "" {
		writeBadRequest(w, r, "type is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.LinkNameRelation(coldp.NameRelation{
			NameID:        nameID,
			RelatedNameID: body.RelatedNameID,
			Type:          coldp.NewNomRelType(body.Type),
			ReferenceID:   body.ReferenceID,
			Page:          body.Page,
			Remarks:       body.Remarks,
		})
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Re-list to find the row we just wrote (name_relation has no
	// insert-time id to return). Match on the composite key —
	// (name_id, related_name_id, type_id) is unique enough in practice
	// that the first matching row is the freshly-added one.
	hits, err := s.a.ListNameRelations(r.Context(), nameID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	for _, h := range hits {
		if h.Direction == "outgoing" &&
			h.CounterpartID == body.RelatedNameID &&
			h.Type == body.Type {
			writeJSON(w, http.StatusCreated, s.nameRelationHitToAPI(r.Context(), nameID, h))
			return
		}
	}
	// Row was written but the re-list didn't find it (should not
	// happen). Return a minimal shape so the client can refresh.
	writeJSON(w, http.StatusCreated, apiNameRelation{
		NameID: nameID,
		Type:   body.Type,
		RelatedName: apiRef{
			ID: body.RelatedNameID,
		},
		Direction:   "outgoing",
		ReferenceID: body.ReferenceID,
		Page:        body.Page,
		Remarks:     body.Remarks,
	})
}

// handleDeleteNameRelation removes the row at the given rowid.
// Unknown row → 404 via ErrNotFound.
//
// @Summary      Delete name relation
// @Description  Delete a name-relation row by rowid.
// @Tags         name-relations
// @Param        id path string true "Name relation rowid"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /name-relation/{id} [delete]
func (s *server) handleDeleteNameRelation(w http.ResponseWriter, r *http.Request) {
	rowid, ok := parseNameRelationRowID(w, r)
	if !ok {
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.UnlinkNameRelation(rowid)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseNameRelationRowID mirrors parseVernacularRowID.
func parseNameRelationRowID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeBadRequest(w, r, "invalid name-relation id: "+raw)
		return 0, false
	}
	return n, true
}

// nameRelationHitToAPI resolves a NameRelationHit into its wire
// projection: stringifies the rowid handle, fetches the counterpart
// name's label via NameRef, and populates reference_label when a
// reference is cited.
func (s *server) nameRelationHitToAPI(ctx context.Context, nameID string, h hive.NameRelationHit) apiNameRelation {
	out := apiNameRelation{
		ID:          strconv.FormatInt(h.RowID, 10),
		NameID:      nameID,
		Type:        h.Type,
		Direction:   h.Direction,
		ReferenceID: h.ReferenceID,
		Page:        h.Page,
		Remarks:     h.Remarks,
	}
	if h.ReferenceID != "" {
		out.ReferenceLabel = s.referenceLabel(ctx, h.ReferenceID)
	}
	ref, err := s.a.NameRef(ctx, h.CounterpartID)
	if err != nil || ref.ID == "" {
		out.RelatedName = apiRef{ID: h.CounterpartID}
	} else {
		out.RelatedName = apiRef{
			ID: ref.ID,
			Label: apiLabel{
				Text: ref.Label.Text,
				HTML: ref.Label.HTML,
			},
		}
	}
	return out
}

// applySpeciesInteractionPatch layers the pointer-optional patch
// onto the merged coldp.SpeciesInteraction. Type goes through the
// coldp constructor so empty-string means "clear the enum" cleanly.
func applySpeciesInteractionPatch(s *coldp.SpeciesInteraction, p apiSpeciesInteractionPatch) {
	if p.RelatedTaxonID != nil {
		s.RelatedTaxonID = *p.RelatedTaxonID
	}
	if p.RelatedTaxonScientificName != nil {
		s.RelatedTaxonScientificName = *p.RelatedTaxonScientificName
	}
	if p.Type != nil {
		s.Type = coldp.NewSpInteractionType(*p.Type)
	}
	if p.SourceID != nil {
		s.SourceID = *p.SourceID
	}
	if p.ReferenceID != nil {
		s.ReferenceID = *p.ReferenceID
	}
	if p.Remarks != nil {
		s.Remarks = *p.Remarks
	}
}

// handleGetName returns one name by ID.
//
// @Summary      Get name
// @Description  Fetch a name by ID. Includes resolved reference label and basionym reference when present.
// @Tags         names
// @Produce      json
// @Param        id path string true "Name ID"
// @Success      200 {object} apiName
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id} [get]
func (s *server) handleGetName(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := s.a.GetName(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if n.Modified != "" {
		w.Header().Set("ETag", n.Modified)
	}
	body := nameToAPI(n)
	body.ReferenceLabel = s.referenceLabel(r.Context(), body.ReferenceID)
	body.Basionym = s.resolveBasionym(r.Context(), id)
	writeJSON(w, http.StatusOK, body)
}

// resolveBasionym returns an apiRef pointing at the linked basionym
// name — the row related to id via a name_relation of type BASIONYM
// where id is the subject. Returns nil when there's no basionym
// linkage (which is the common case for original combinations and
// for any name whose curator hasn't added a basionym record yet).
//
// Resolution errors are swallowed: a broken relation shouldn't fail
// the whole name-detail response. Worst case the row shows without
// the "Basionym" line and the curator can re-link.
func (s *server) resolveBasionym(ctx context.Context, id string) *apiRef {
	if id == "" {
		return nil
	}
	hits, err := s.a.ListNameRelations(ctx, id)
	if err != nil {
		return nil
	}
	for _, h := range hits {
		if h.Type != "BASIONYM" || h.Direction != "outgoing" {
			continue
		}
		// Resolve the counterpart's display label via TaxonRef-style
		// build. The basionym is a Name, not a Taxon, so use NameRef
		// which the core layer exposes for exactly this case.
		ref, err := s.a.NameRef(ctx, h.CounterpartID)
		if err != nil {
			return &apiRef{ID: h.CounterpartID}
		}
		return &apiRef{
			ID: ref.ID,
			Label: apiLabel{
				Text: ref.Label.Text,
				HTML: ref.Label.HTML,
			},
		}
	}
	return nil
}

// referenceLabel resolves a reference id → "Author (Year) Title" for
// the wire ReferenceLabel field. Empty id → empty label. Any
// resolution error is swallowed (label just stays empty) so a broken
// link doesn't fail the whole name-detail response — the frontend
// still shows the ID and the picker can be used to re-link.
func (s *server) referenceLabel(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	ref, err := s.a.GetReference(ctx, id)
	if err != nil {
		return ""
	}
	return hive.ReferenceLabel(ref)
}

// handleTaxonSearch returns taxa whose name matches q as a
// case-insensitive prefix. Backs the WUI top-bar combobox and any
// parent-picker use case; also useful as a generic tree-navigation
// aid.
//
// include_synonyms=true (default false) extends results with accepted
// taxa reached via matching synonyms — see hive.SearchTaxa. When set,
// each apiTaxonHit carries is_synonym (true when the match came via
// a synonym) and matched_name (the string that actually matched).
//
// @Summary      Search taxa
// @Description  Search taxa by name. Supports prefix, partial, and fuzzy modes; optional synonym expansion.
// @Tags         taxa
// @Produce      json
// @Param        q query string true "Query string"
// @Param        limit query int false "Page size"
// @Param        mode query string false "Match mode: prefix|partial|fuzzy"
// @Param        include_synonyms query bool false "Extend hits with accepted taxa reached via matching synonyms"
// @Success      200 {object} apiPage[apiTaxonHit]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/search [get]
func (s *server) handleTaxonSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeBadRequest(w, r, "query parameter 'q' is required")
		return
	}
	limit := defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeBadRequest(w, r, "invalid 'limit' value")
			return
		}
		limit = clampPageSize(n)
	}
	includeSynonyms := parseBoolParam(r.URL.Query().Get("include_synonyms"))
	mode, err := parseSearchMode(r.URL.Query().Get("mode"))
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	hits, err := s.a.SearchTaxa(r.Context(), q, hive.SearchOpts{
		Mode:            mode,
		IncludeSynonyms: includeSynonyms,
		Limit:           limit,
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiTaxonHit, 0, len(hits))
	for _, h := range hits {
		item := hitToAPI(h)
		if !includeSynonyms {
			// Suppress the synonym-only fields when the caller didn't
			// opt in so the default response shape stays identical to
			// pre-feature. Core still populates MatchedName (== Name)
			// for accepted matches, which would otherwise leak onto
			// the wire.
			item.MatchedName = ""
			item.IsSynonym = false
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, apiPage[apiTaxonHit]{Items: items})
}

// parseSearchMode maps the `mode` query-string value to a
// hive.SearchMode. Empty or missing → prefix (the default,
// backward-compatible with pre-partial callers). Unknown values
// return an error the handler surfaces as 400.
func parseSearchMode(v string) (hive.SearchMode, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "prefix":
		return hive.SearchModePrefix, nil
	case "partial":
		return hive.SearchModePartial, nil
	case "fuzzy":
		return hive.SearchModeFuzzy, nil
	}
	return "", fmt.Errorf("invalid 'mode' value %q (expected prefix, partial, or fuzzy)", v)
}

// parseBoolParam interprets a query-string flag. Accepts the common
// truthy forms browsers and CLIs emit; anything else (including empty
// / absent) is false so include_synonyms preserves its default-off
// contract when the param is missing.
func parseBoolParam(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	}
	return false
}

// handleMoveTaxon reparents a taxon. Body is {"new_parent_id": "..."} —
// empty string moves the taxon to root level. Optimistic concurrency via
// If-Match on the taxon's current col__modified. Cycle attempts, self-parent
// attempts, and unknown-parent errors surface from core with the usual
// sentinel-to-status mapping (ErrValidation → 422, ErrNotFound → 404,
// ErrConflict → 409).
//
// v0 does NOT refresh denormalized classification columns after the move —
// see CLAUDE.md § pkg/ package conventions. A future Reclassify endpoint
// will rebuild them.
//
// @Summary      Move taxon
// @Description  Reparent a taxon. Empty new_parent_id moves it to root. Optimistic concurrency via If-Match.
// @Tags         taxa
// @Accept       json
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Param        If-Match header string false "col__modified value for optimistic concurrency"
// @Param        body body object true "New parent id" schema-example({"new_parent_id":"..."})
// @Success      200 {object} apiTaxon
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/move [post]
func (s *server) handleMoveTaxon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		NewParentID string `json:"new_parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ifMatch := r.Header.Get("If-Match")

	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		// If the client sent an If-Match, verify inside the tx so a
		// concurrent edit between the client's GET and this move surfaces
		// as ErrConflict. Empty If-Match skips the check.
		if ifMatch != "" {
			current, err := s.a.GetTaxon(r.Context(), id)
			if err != nil {
				return err
			}
			if current.Modified != ifMatch {
				return fmt.Errorf("core: move taxon %s: %w: If-Match mismatch (have %q, want %q)",
					id, hive.ErrConflict, current.Modified, ifMatch)
			}
		}
		return tx.MoveTaxon(id, body.NewParentID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}

	fresh, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", fresh.Modified)
	writeJSON(w, http.StatusOK, taxonToAPI(fresh))
}

// handleAddBasionym creates the original combination (basionym) for the
// taxon identified in the path. All three writes happen atomically in
// one WithTx:
//
//  1. CreateName for the basionym — takes the same body shape the new-
//     taxon endpoint accepts (verbatim + code required; every atomized
//     field optional and fill-from-parsed on write).
//  2. AddSynonym linking the new basionym name to the taxon whose id
//     is in the path (i.e. the current-combination taxon). Any pre-
//     existing synonym pointing that name at that taxon is a duplicate
//     insert — AddSynonym surfaces the DB unique-constraint error as
//     ErrExists / ErrConflict.
//  3. LinkNameRelation with the current combination's name as the
//     subject, the new basionym as the object, and type BASIONYM.
//     sfga's nom_rel_type collapses botanical "basionym" and
//     zoological "original combination" into one enum value; UI can
//     present code-scoped labels.
//
// Returns the newly created basionym as apiName so the client can
// display / pick it. The parent taxon's row is untouched; the client
// stays on its current view.
//
// @Summary      Add basionym
// @Description  Create the basionym name for the current-combination taxon and link the BASIONYM relation atomically.
// @Tags         names
// @Accept       json
// @Produce      json
// @Param        id path string true "Current combination taxon ID"
// @Param        body body createTaxonBody true "New basionym name payload"
// @Success      201 {object} apiName
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/basionym [post]
func (s *server) handleAddBasionym(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	if taxonID == "" {
		writeBadRequest(w, r, "taxon id is required in path")
		return
	}
	var body createTaxonBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.ScientificName) == "" {
		writeBadRequest(w, r, "scientific_name is required")
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeBadRequest(w, r, "code is required")
		return
	}
	// Fetch the current combination's taxon to grab the name id we're
	// pointing the BASIONYM relation at.
	currentTaxon, err := s.a.GetTaxon(r.Context(), taxonID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if currentTaxon.NameID == "" {
		writeBadRequest(w, r, "current taxon has no attached name; cannot add basionym")
		return
	}
	currentNameID := currentTaxon.NameID

	var newBasionymID string
	err = s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		nameID, err := tx.CreateName(body.toColdpName())
		if err != nil {
			return err
		}
		newBasionymID = nameID

		if _, err := tx.AddSynonym(coldp.Synonym{
			TaxonID: taxonID,
			NameID:  nameID,
			// AddSynonym stamps col__modified / col__modified_by from
			// the tx actor; status defaults to SYNONYM.
		}); err != nil {
			return err
		}

		if err := tx.LinkNameRelation(coldp.NameRelation{
			NameID:        currentNameID,
			RelatedNameID: nameID,
			Type:          coldp.NewNomRelType("BASIONYM"),
		}); err != nil {
			return err
		}
		// Backfill both names' atomized year fields from the citation
		// graph. Order matters: fill the basionym first (originals
		// path — populates its own basionym_year from its own
		// reference) so when we then backfill the current combination
		// (recomb path — needs the basionym's year for the sanity
		// gate), the plausibility check has a value to compare against.
		if err := tx.BackfillAuthorshipYears(nameID); err != nil {
			return err
		}
		return tx.BackfillAuthorshipYears(currentNameID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetName(r.Context(), newBasionymID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/name/"+newBasionymID)
	writeJSON(w, http.StatusCreated, nameToAPI(fresh))
}

// handleAddSynonym creates a new name and links it as a synonym of the
// taxon identified in the path. Two writes in one WithTx:
//
//  1. CreateName from the same createTaxonBody shape the new-taxon
//     endpoint takes.
//  2. AddSynonym linking the fresh name id to the taxon in the path.
//
// Simpler than handleAddBasionym — no BASIONYM name-relation because a
// synonym is not, in general, homotypic with the accepted name (curators
// use the basionym endpoint when the incoming name IS the accepted
// name's original combination).
//
// Returns the newly created name (apiName) so the frontend can display
// it. The parent taxon's row is untouched; the client refreshes the
// taxon detail to pick the new synonym up in the Nomenclatural history
// section.
//
// @Summary      Add synonym
// @Description  Create a name and link it as a synonym of the given taxon. Optionally links a basionym in the same transaction.
// @Tags         synonyms
// @Accept       json
// @Produce      json
// @Param        id path string true "Accepted taxon ID"
// @Param        body body createTaxonBody true "New synonym name payload"
// @Success      201 {object} apiName
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/synonym [post]
func (s *server) handleAddSynonym(w http.ResponseWriter, r *http.Request) {
	taxonID := r.PathValue("id")
	if taxonID == "" {
		writeBadRequest(w, r, "taxon id is required in path")
		return
	}
	var body createTaxonBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.ScientificName) == "" {
		writeBadRequest(w, r, "scientific_name is required")
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeBadRequest(w, r, "code is required")
		return
	}
	if body.BasionymNameID != "" && body.Basionym != nil {
		writeBadRequest(w, r,
			"set exactly one of basionym_name_id (link existing) or basionym (create new)")
		return
	}
	if body.Basionym != nil {
		if strings.TrimSpace(body.Basionym.ScientificName) == "" {
			writeBadRequest(w, r, "basionym.scientific_name is required")
			return
		}
		if strings.TrimSpace(body.Basionym.Code) == "" {
			writeBadRequest(w, r, "basionym.code is required")
			return
		}
	}
	var newNameID string
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		// Create the basionym first (when supplied) so we have a
		// name id to hang the BASIONYM relation off before writing
		// the primary. Curator-supplied atomized fields on the
		// basionym payload survive; empty ones get gnparser fill-
		// from-parse inside CreateName.
		basionymID := body.BasionymNameID
		if body.Basionym != nil {
			id, err := tx.CreateName(body.Basionym.toColdpName())
			if err != nil {
				return err
			}
			basionymID = id
		}
		nameID, err := tx.CreateName(body.toColdpName())
		if err != nil {
			return err
		}
		newNameID = nameID
		if _, err = tx.AddSynonym(coldp.Synonym{
			TaxonID: taxonID,
			NameID:  nameID,
			Status:  coldp.NewTaxonomicStatus(body.SynonymStatus),
			// modified / modified_by come from the tx actor. Empty
			// SynonymStatus → coldp.NewTaxonomicStatus returns the
			// zero value; AddSynonym then defaults to SYNONYM.
		}); err != nil {
			return err
		}
		if basionymID != "" {
			if err := tx.LinkNameRelation(coldp.NameRelation{
				NameID:        nameID,
				RelatedNameID: basionymID,
				Type:          coldp.NewNomRelType("BASIONYM"),
			}); err != nil {
				return err
			}
			// Backfill the basionym first (originals path — its own
			// reference year fills basionym_authorship_year), so the
			// primary's recomb-path backfill has a value for the
			// sanity gate.
			if err := tx.BackfillAuthorshipYears(basionymID); err != nil {
				return err
			}
		}
		return tx.BackfillAuthorshipYears(nameID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetName(r.Context(), newNameID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/name/"+newNameID)
	writeJSON(w, http.StatusCreated, nameToAPI(fresh))
}

// handleNameDependencies returns a count-projection of what still
// references the given name — taxa (accepted-name link), synonyms,
// and name_relation rows on either side. Frontend uses this to decide
// whether to offer a "cascade name" option on synonym delete:
// only if all three counts are zero AFTER the synonym being deleted
// has been removed (i.e., synonym_count = 1 today and it's the target
// synonym, and every other count = 0).
//
// @Summary      Name dependency counts
// @Description  Count of taxa, synonyms, and name-relations that reference a name.
// @Tags         names
// @Produce      json
// @Param        id path string true "Name ID"
// @Success      200 {object} map[string]int
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id}/dependencies [get]
func (s *server) handleNameDependencies(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deps, err := s.a.NameDependencies(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"taxon_count":         deps.TaxonCount,
		"synonym_count":       deps.SynonymCount,
		"name_relation_count": deps.NameRelationCount,
	})
}

// handleMoveSynonym reassigns a synonym row to a new accepted taxon.
// Body: {"new_taxon_id": "..."}. Returns 204 on success. The synonym's
// col__id stays stable — this is an in-place taxon-id update, not a
// delete-and-recreate, so any external reference to the synonym id
// (audit log, undo) survives.
//
// @Summary      Move synonym
// @Description  Reassign a synonym row to a new accepted taxon in place.
// @Tags         synonyms
// @Accept       json
// @Param        id path string true "Synonym ID"
// @Param        body body object true "New accepted taxon id"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /synonym/{id}/move [post]
func (s *server) handleMoveSynonym(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "synonym id is required in path")
		return
	}
	var body struct {
		NewTaxonID string `json:"new_taxon_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.NewTaxonID) == "" {
		writeBadRequest(w, r, "new_taxon_id is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.MoveSynonym(id, body.NewTaxonID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSynonym removes a single synonym row and, optionally,
// the underlying name row if the query param cascade_name=true is set.
// Cascade only succeeds when no OTHER row references the name — the
// underlying DeleteName op refuses with ErrConflict when a taxon,
// another synonym, or a name_relation still points at the name. Both
// deletes execute in one WithTx so the archive never lands in a
// synonym-gone-but-name-still-there half-state on cascade failure.
//
// Returns 204 on success.
//
// @Summary      Delete synonym
// @Description  Delete a synonym row; optionally cascade the underlying name row when no other row references it.
// @Tags         synonyms
// @Param        id path string true "Synonym ID"
// @Param        cascade_name query bool false "Also delete the underlying name row if it becomes orphaned"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /synonym/{id} [delete]
func (s *server) handleDeleteSynonym(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "synonym id is required in path")
		return
	}
	cascade := parseBoolParam(r.URL.Query().Get("cascade_name"))
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		_, nameID, err := tx.RemoveSynonymByID(id)
		if err != nil {
			return err
		}
		if cascade {
			// DeleteName does its own dependency check inside the tx —
			// no need to double-guard here. If a curator's request races
			// with another that just added a new synonym pointing at the
			// same name, DeleteName returns ErrConflict and rolls back
			// the whole tx (synonym removal included). Correct
			// consistency; the client can retry or drop the cascade.
			if err := tx.DeleteName(nameID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateTaxon composes a new (name, taxon) pair in one WithTx and
// returns the resulting taxon detail (same shape as GET /api/taxon/{id}).
//
// Body is an apiName-shaped subset (every atomized field the create
// modal's preview form can send) plus parent_id + name_phrase. Only
// scientific_name and code are required; everything else is optional
// and, when omitted, falls to hive.CreateName's fill-from-parse
// (gnparser-derived defaults). Curator-supplied atomized values (e.g.
// a genus override for a name gnparser mis-atomizes) always win over
// the parse.
//
// parent_id may be empty (creates a root-level taxon).
//
// @Summary      Create taxon
// @Description  Create a name and its associated taxon in one transaction. Optionally links a basionym.
// @Tags         taxa
// @Accept       json
// @Produce      json
// @Param        body body createTaxonBody true "New taxon payload"
// @Success      201 {object} apiTaxon
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon [post]
func (s *server) handleCreateTaxon(w http.ResponseWriter, r *http.Request) {
	var body createTaxonBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.ScientificName) == "" {
		writeBadRequest(w, r, "scientific_name is required")
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeBadRequest(w, r, "code is required")
		return
	}
	if body.BasionymNameID != "" && body.Basionym != nil {
		writeBadRequest(w, r,
			"set exactly one of basionym_name_id (link existing) or basionym (create new)")
		return
	}
	if body.Basionym != nil {
		if strings.TrimSpace(body.Basionym.ScientificName) == "" {
			writeBadRequest(w, r, "basionym.scientific_name is required")
			return
		}
		if strings.TrimSpace(body.Basionym.Code) == "" {
			writeBadRequest(w, r, "basionym.code is required")
			return
		}
	}

	var newID, newNameID string
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		// Create the basionym first (when supplied) so we have a
		// name id available before writing the primary name — lets us
		// LinkNameRelation in the same tx after the primary lands.
		basionymID := body.BasionymNameID
		if body.Basionym != nil {
			id, err := tx.CreateName(body.Basionym.toColdpName())
			if err != nil {
				return err
			}
			basionymID = id
		}
		nameID, err := tx.CreateName(body.toColdpName())
		if err != nil {
			return err
		}
		id, err := tx.CreateTaxon(coldp.Taxon{
			NameID:     nameID,
			ParentID:   body.ParentID,
			NamePhrase: body.NamePhrase,
		})
		if err != nil {
			return err
		}
		newID = id
		newNameID = nameID
		if basionymID != "" {
			if err := tx.LinkNameRelation(coldp.NameRelation{
				NameID:        nameID,
				RelatedNameID: basionymID,
				Type:          coldp.NewNomRelType("BASIONYM"),
			}); err != nil {
				return err
			}
			// Basionym first (originals path) so the primary's
			// recomb-path backfill sees a value for the sanity gate.
			if err := tx.BackfillAuthorshipYears(basionymID); err != nil {
				return err
			}
		}
		return tx.BackfillAuthorshipYears(nameID)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}

	// Fetch the fresh taxon (+ label + parent ref) so the caller can
	// splice it into local state without a second round-trip.
	t, err := s.a.GetTaxon(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	resp := taxonToAPI(t)
	if ref, refErr := s.a.TaxonRef(r.Context(), t.ID); refErr == nil {
		resp.Label = apiLabel{Text: ref.Label.Text, HTML: ref.Label.HTML}
	}
	if t.ParentID != "" {
		if ref, refErr := s.a.TaxonRef(r.Context(), t.ParentID); refErr == nil {
			resp.Parent = &apiRef{
				ID:    ref.ID,
				Label: apiLabel{Text: ref.Label.Text, HTML: ref.Label.HTML},
			}
		}
	}
	// Attach soft warnings from gsvalidator to the response. Runs
	// post-commit — this slice only surfaces warnings, doesn't
	// block save on hard errors yet.
	resp.Warnings = validationWarnings(s.a, r.Context(), t.ID, newNameID)
	w.Header().Set("ETag", t.Modified)
	w.Header().Set("Location", "/api/taxon/"+t.ID)
	writeJSON(w, http.StatusCreated, resp)
}

// validationWarnings adapts hive.NameWarnings + hive.TaxonWarnings
// into the wire type used by create/update/get taxon responses. Core
// does the actual filtering; the shape mirrors it 1:1. Both sources
// are folded into a single list so the per-record banner surfaces
// name-scoped and taxon-scoped rules together — a curator viewing a
// taxon detail doesn't care which side of the taxon↔name pair a
// warning was attached to.
func validationWarnings(a *hive.Archive, ctx context.Context, taxonID, nameID string) []apiValidationWarning {
	ws := a.NameWarnings(ctx, nameID)
	if taxonID != "" {
		ws = append(ws, a.TaxonWarnings(ctx, taxonID)...)
	}
	if len(ws) == 0 {
		return nil
	}
	out := make([]apiValidationWarning, len(ws))
	for i, w := range ws {
		out[i] = apiValidationWarning{
			RuleID:    w.RuleID,
			RuleName:  w.RuleName,
			FieldName: w.FieldName,
			Severity:  w.Severity,
			Message:   w.Message,
		}
	}
	return out
}

// metadataWarnings adapts hive.MetadataWarnings for the metadata GET
// response. Kept as a distinct helper because metadata is
// singleton-scoped (int id, not a taxon+name pair).
func metadataWarnings(a *hive.Archive, ctx context.Context, id int) []apiValidationWarning {
	ws := a.MetadataWarnings(ctx, id)
	if len(ws) == 0 {
		return nil
	}
	out := make([]apiValidationWarning, len(ws))
	for i, w := range ws {
		out[i] = apiValidationWarning{
			RuleID:    w.RuleID,
			RuleName:  w.RuleName,
			FieldName: w.FieldName,
			Severity:  w.Severity,
			Message:   w.Message,
		}
	}
	return out
}

// handleDeleteTaxon removes the taxon and its per-taxon associations.
// Refuses (409) when the taxon has children — the curator must reparent
// or delete them first. The response body carries the parent_id so
// front-ends can reveal the tree at the parent's location.
//
// @Summary      Delete taxon
// @Description  Delete a taxon and its per-taxon associations. Refuses when the taxon still has children.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} map[string]string
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id} [delete]
func (s *server) handleDeleteTaxon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Capture parent_id before the delete — the row is gone afterward.
	t, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	parentID := t.ParentID

	err = s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteTaxon(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"deleted_id": id,
		"parent_id":  parentID,
	})
}

// handleDeletePreview returns the summary a curator sees before
// confirming a cascade delete: direct-child count (drives the "leaf
// vs parent" UI branch), descendant count, and per-association-table
// counts across the descendant set.
//
// @Summary      Delete preview
// @Description  Descendant counts and per-association counts for a proposed cascade delete.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/delete-preview [get]
func (s *server) handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.a.TaxonDeletePreview(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"direct_child_count":           p.DirectChildCount,
		"descendant_count":             p.DescendantCount,
		"synonym_count":                p.SynonymCount,
		"vernacular_count":             p.VernacularCount,
		"distribution_count":           p.DistributionCount,
		"media_count":                  p.MediaCount,
		"treatment_count":              p.TreatmentCount,
		"species_estimate_count":       p.SpeciesEstimateCount,
		"taxon_property_count":         p.TaxonPropertyCount,
		"species_interaction_count":    p.SpeciesInteractionCount,
		"taxon_concept_relation_count": p.TaxonConceptRelationCount,
		"parent_id":                    p.ParentID,
	})
}

// handleDeleteReparent moves the taxon's direct children up one level
// then leaf-deletes the taxon. Response body carries the destination
// parent_id (empty when children became new roots) so the client can
// reveal the tree at the parent's position.
//
// @Summary      Delete with reparent
// @Description  Move direct children up one level, then leaf-delete the taxon.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} map[string]string
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/delete-reparent [post]
func (s *server) handleDeleteReparent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	t, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	parentID := t.ParentID

	err = s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.ReparentAndDeleteTaxon(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"deleted_id": id,
		"parent_id":  parentID,
	})
}

// handleDeleteCascade removes the taxon, every descendant, and every
// per-taxon association attached to any member of that set. Response
// body carries the deleted-root's parent_id so the client can reveal
// the tree at the surviving parent (empty = tree returned to roots-
// only view).
//
// @Summary      Delete cascade
// @Description  Delete the taxon, every descendant, and every per-taxon association attached to the set.
// @Tags         taxa
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Success      200 {object} map[string]string
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id}/delete-cascade [post]
func (s *server) handleDeleteCascade(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	t, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	parentID := t.ParentID

	err = s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.CascadeDeleteTaxon(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"deleted_id": id,
		"parent_id":  parentID,
	})
}

// handleReferences returns a paginated list of references, ordered
// by author + year + title. Same shape as the taxon list endpoints so
// clients can render either aggregate uniformly.
//
// @Summary      List references
// @Description  Paginated list of references. Sort by author (default) or recent.
// @Tags         references
// @Produce      json
// @Param        limit query int false "Page size"
// @Param        offset query int false "Row offset"
// @Param        sort query string false "Sort: author|recent|year"
// @Param        filter query string false "Comma-separated workflow filters, all of which must hold: runs, assertions, pending, bibliographic, digitized, imported, and no-bibliographic, no-digitized, no-assertions, no-imported"
// @Param        col_parent query string false "Papers mentioning names the Catalogue of Life places in this higher taxon"
// @Param        ncbi_parent query string false "Papers mentioning names NCBI Taxonomy places in this higher taxon"
// @Param        min_mentions query int false "Count only names mentioned at least this many times toward col_parent / ncbi_parent"
// @Success      200 {object} apiPage[apiReferenceHit]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference [get]
func (s *server) handleReferences(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	sort := hive.ReferenceSortAuthor
	switch r.URL.Query().Get("sort") {
	case "recent":
		sort = hive.ReferenceSortRecent
	case "year":
		sort = hive.ReferenceSortYear
	}
	rq, msg := parseReferenceQuery(r)
	if msg != "" {
		writeBadRequest(w, r, msg)
		return
	}
	hits, total, err := s.a.ListReferencesPage(r.Context(), limit, offset, sort, rq)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	page := apiPage[apiReferenceHit]{Items: make([]apiReferenceHit, 0, len(hits))}
	for _, h := range hits {
		page.Items = append(page.Items, referenceHitToAPI(h))
	}
	page.Total = &total
	if len(hits) == limit && offset+limit < total {
		page.NextCursor = encodeCursor(offset + limit)
	}
	writeJSON(w, http.StatusOK, page)
}

// apiColTaxon is one higher taxon from the name-mention index.
type apiColTaxon struct {
	Name   string `json:"name"`
	Rank   string `json:"rank"`
	Papers int    `json:"papers"`
}

// handleColTaxa returns higher taxa from one source of the
// name-mention index whose names start with q, most-mentioned first.
// Backs autocomplete for the col_parent / ncbi_parent reference
// filters, whose values must use that source's names.
//
// @Summary      Look up higher taxa in the name-mention index
// @Description  Ranked higher taxa (kingdom to genus) from one verifier source's classification of names mentioned in indexed papers, with the number of papers mentioning names in each. Discovery data only.
// @Tags         references
// @Produce      json
// @Param        source query string false "Source: ncbi (default) or col"
// @Param        q query string false "Name prefix"
// @Param        limit query int false "Maximum results (default 20)"
// @Success      200 {object} apiPage[apiColTaxon]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /name-mentions/taxa [get]
func (s *server) handleColTaxa(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeBadRequest(w, r, "invalid 'limit' value")
			return
		}
		limit = clampPageSize(n)
	}
	key := r.URL.Query().Get("source")
	if key == "" {
		key = mentions.Sources[0].Key
	}
	src, ok := mentions.SourceByKey(key)
	if !ok {
		writeBadRequest(w, r, "invalid 'source' value (ncbi | col)")
		return
	}
	taxa, err := s.a.ColTaxa(r.Context(), src.ID, r.URL.Query().Get("q"), limit)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiColTaxon, 0, len(taxa))
	for _, t := range taxa {
		items = append(items, apiColTaxon{Name: t.Name, Rank: t.Rank, Papers: t.Papers})
	}
	writeJSON(w, http.StatusOK, apiPage[apiColTaxon]{Items: items})
}

// apiTaxonTreeNode is one taxon in a classification-tree response.
type apiTaxonTreeNode struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Rank     string `json:"rank,omitempty"`
	Papers   int    `json:"papers"`
	Children int    `json:"children"`
	Match    bool   `json:"match,omitempty"`
}

// handleTaxonTree returns part of one source's classification of the
// names mentioned in indexed papers, for browsing the higher-taxon
// reference filter.
//
// @Summary      Browse the classification of mentioned names
// @Description  Nodes of one verifier source's classification of names mentioned in indexed papers, in depth-first order, each with the number of papers mentioning names in it. Without q, returns the descendants of path down to depth branching levels (a node with a single child does not count as a level). With q, returns the taxa whose names start with q and their ancestors. Discovery data only.
// @Tags         references
// @Produce      json
// @Param        source query string false "Source: ncbi (default) or col"
// @Param        path query string false "Classification path such as |Eukaryota|Sar|; default the top"
// @Param        depth query int false "Branching levels below path (default 1, at most 5)"
// @Param        q query string false "Name prefix to search for"
// @Param        min_mentions query int false "Count a paper only when it mentions a name in the taxon at least this often"
// @Param        limit query int false "Maximum q matches (default 30)"
// @Success      200 {object} apiPage[apiTaxonTreeNode]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /name-mentions/tree [get]
func (s *server) handleTaxonTree(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	key := qv.Get("source")
	if key == "" {
		key = mentions.Sources[0].Key
	}
	src, ok := mentions.SourceByKey(key)
	if !ok {
		writeBadRequest(w, r, "invalid 'source' value (ncbi | col)")
		return
	}
	tq := hive.TaxonTreeQuery{SourceID: src.ID, Path: qv.Get("path"), Q: qv.Get("q")}
	for _, p := range []struct {
		name string
		dst  *int
		max  int
	}{{"depth", &tq.Depth, 5}, {"min_mentions", &tq.MinMentions, 1000}, {"limit", &tq.Limit, 100}} {
		raw := qv.Get(p.name)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeBadRequest(w, r, "invalid '"+p.name+"' value")
			return
		}
		*p.dst = min(n, p.max)
	}
	nodes, err := s.a.TaxonTree(r.Context(), tq)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiTaxonTreeNode, 0, len(nodes))
	for _, n := range nodes {
		items = append(items, apiTaxonTreeNode{Path: n.Path, Name: n.Name, Rank: n.Rank,
			Papers: n.Papers, Children: n.Children, Match: n.Match})
	}
	writeJSON(w, http.StatusOK, apiPage[apiTaxonTreeNode]{Items: items})
}

// parseReferenceQuery reads the reference-list narrowing parameters
// shared by GET /api/reference and /api/reference/search. It returns
// a client-facing message for invalid values.
func parseReferenceQuery(r *http.Request) (hive.ReferenceQuery, string) {
	qv := r.URL.Query()
	var rq hive.ReferenceQuery
	if raw := qv.Get("filter"); raw != "" {
		for _, v := range strings.Split(raw, ",") {
			f, ok := hive.ParseReferenceFilter(strings.TrimSpace(v))
			if !ok {
				return hive.ReferenceQuery{}, fmt.Sprintf("invalid 'filter' value %q", v)
			}
			rq.Filters = append(rq.Filters, f)
		}
	}
	for _, src := range mentions.Sources {
		if v := strings.TrimSpace(qv.Get(src.Key + "_parent")); v != "" {
			rq.Parents = append(rq.Parents, hive.IndexParent{SourceID: src.ID, Name: v})
		}
	}
	if raw := qv.Get("min_mentions"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return hive.ReferenceQuery{}, "invalid 'min_mentions' value (a positive integer)"
		}
		rq.MinMentions = n
	}
	return rq, ""
}

// handleReferenceSearch returns references whose author, title,
// citation, or DOI matches q as a case-insensitive substring, or
// whose ID starts with q.
//
// @Summary      Search references
// @Description  Case-insensitive substring search across author, title, citation, and DOI; queries of 8+ characters also match an ID prefix.
// @Tags         references
// @Produce      json
// @Param        q query string true "Query string"
// @Param        limit query int false "Page size"
// @Param        filter query string false "Comma-separated workflow filters, all of which must hold: runs, assertions, pending, bibliographic, digitized, imported, and no-bibliographic, no-digitized, no-assertions, no-imported"
// @Param        col_parent query string false "Papers mentioning names the Catalogue of Life places in this higher taxon"
// @Param        ncbi_parent query string false "Papers mentioning names NCBI Taxonomy places in this higher taxon"
// @Param        min_mentions query int false "Count only names mentioned at least this many times toward col_parent / ncbi_parent"
// @Success      200 {object} apiPage[apiReferenceHit]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/search [get]
func (s *server) handleReferenceSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeBadRequest(w, r, "query parameter 'q' is required")
		return
	}
	limit := defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeBadRequest(w, r, "invalid 'limit' value")
			return
		}
		limit = clampPageSize(n)
	}
	rq, msg := parseReferenceQuery(r)
	if msg != "" {
		writeBadRequest(w, r, msg)
		return
	}
	hits, err := s.a.SearchReferences(r.Context(), q, limit, rq)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiReferenceHit, 0, len(hits))
	for _, h := range hits {
		items = append(items, referenceHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiReferenceHit]{Items: items})
}

// handleAttachReferencePDF associates a sidecar-library sha512
// with an existing reference. The sha comes from a prior
// POST /api/reference/upload response. Curator use case: the
// reference was created without a PDF, and later they want to
// upload one and link it.
//
// Body: {"sha512": "..."}. Idempotent — a repeat attach with
// the same sha is a no-op.
//
// @Summary      Attach PDF to reference
// @Description  Associate a sidecar-library sha512 with an existing reference. Idempotent.
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        id path string true "Reference ID"
// @Param        body body object true "SHA-512 hex payload"
// @Success      200 {object} map[string]string
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/attach-pdf [post]
func (s *server) handleAttachReferencePDF(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		SHA512 string `json:"sha512"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	body.SHA512 = strings.TrimSpace(body.SHA512)
	if body.SHA512 == "" {
		writeBadRequest(w, r, "sha512 required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.AttachReferencePDF(id, body.SHA512)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reference_id": id, "sha512": body.SHA512})
}

// handleDetachReferencePDF removes the (reference, sha) association
// and cascades cleanup:
//
//  1. Delete the hive__reference_pdf row.
//  2. Delete correction rows attached to this (reference, sha) —
//     keeping them would silently re-apply on re-attach later.
//  3. If no other reference still points at this sha (IsOrphanSHA),
//     delete every correction row still targeting the sha across
//     the archive and remove sidecar files on disk
//     (pdf, tei.xml, jats.xml, figures/, meta.json).
//
// Shared PDFs (rare — same content-addressed sha attached to
// multiple references): the sidecar stays, other references keep
// working. Only the calling reference's association is severed.
//
// @Summary      Detach PDF from reference
// @Description  Break the (reference, sha) association. Cascades correction cleanup; wipes sidecar files when the sha becomes orphaned.
// @Tags         references
// @Produce      json
// @Param        id path string true "Reference ID"
// @Param        sha path string true "SHA-512 hex"
// @Success      200 {object} apiDetachPDFResponse
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/attached-pdf/{sha} [delete]
func (s *server) handleDetachReferencePDF(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	sha := r.PathValue("sha")
	if refID == "" || sha == "" {
		writeBadRequest(w, r, "reference id and sha required in path")
		return
	}
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DetachReferencePDF(refID, sha)
	}); err != nil {
		writeProblem(w, r, err)
		return
	}
	orphan, err := s.a.IsOrphanSHA(r.Context(), sha)
	if err != nil {
		// Detach itself succeeded — surface the orphan-check error
		// as a warning rather than a hard failure. The DB rows are
		// gone; on-disk cleanup can be retried later.
		writeJSON(w, http.StatusOK, apiDetachPDFResponse{
			Deleted:      true,
			Warnings:     []string{"orphan check: " + err.Error()},
			SidecarWiped: false,
		})
		return
	}
	if !orphan {
		writeJSON(w, http.StatusOK, apiDetachPDFResponse{
			Deleted:      true,
			SidecarWiped: false,
		})
		return
	}
	// True orphan — cascade delete correction rows for this sha
	// across the archive (should be zero after the per-ref delete
	// above, but this handles multi-ref-shared cases where another
	// reference's corrections still lingered), then wipe sidecar
	// files on disk.
	var warnings []string
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteCorrectionsForSHA(sha)
	}); err != nil {
		warnings = append(warnings, "cascade corrections: "+err.Error())
	}
	sidecarDir := sidecar.Dir(s.archivePath)
	// Glob every file that shares this sha's prefix — captures the
	// raw source (pdf/epub/xml/jats), every derived artifact
	// (tei.xml, jats.xml), and meta.json in one pass, so an
	// added upload format never leaves an orphan file behind.
	// The .figures directory needs a separate RemoveAll.
	matches, _ := filepath.Glob(filepath.Join(sidecarDir, sha+".*"))
	for _, path := range matches {
		info, err := os.Lstat(path)
		if err != nil {
			warnings = append(warnings, "stat "+filepath.Base(path)+": "+err.Error())
			continue
		}
		if info.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				warnings = append(warnings, "remove "+filepath.Base(path)+"/: "+err.Error())
			}
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			warnings = append(warnings, "remove "+filepath.Base(path)+": "+err.Error())
		}
	}
	writeJSON(w, http.StatusOK, apiDetachPDFResponse{
		Deleted:      true,
		SidecarWiped: true,
		Warnings:     warnings,
	})
}

type apiDetachPDFResponse struct {
	Deleted      bool     `json:"deleted"`
	SidecarWiped bool     `json:"sidecar_wiped"`
	Warnings     []string `json:"warnings,omitempty"`
}

// handleDeleteReference removes a reference row. Fails with 409
// when downstream aggregates still cite the reference — DeleteReference
// refuses rather than orphaning them. Curator's UI shows the error
// so they can un-cite the reference first.
//
// @Summary      Delete reference
// @Description  Delete a reference row. Refuses with 409 when downstream aggregates still cite it.
// @Tags         references
// @Param        id path string true "Reference ID"
// @Success      204 "No Content"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id} [delete]
func (s *server) handleDeleteReference(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "reference id is required in path")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteReference(id)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReferenceDashboard returns the aggregated progress view
// shown as the landing pane of the references section: per-step
// done/total counts across the 4-step ingest workflow and a hot
// list of the next-action buckets. See REFERENCES_DASHBOARD_PLAN
// for the workflow definition and JSON shape.
//
// One SQL round-trip. Cheap to call on every landing; no caching.
//
// @Summary      Reference dashboard
// @Description  Per-step done/total counts across the 4-step ingest workflow plus next-action buckets.
// @Tags         references
// @Produce      json
// @Success      200 {object} apiReferenceDashboard
// @Failure      500 {object} problem
// @Router       /reference/dashboard [get]
func (s *server) handleReferenceDashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.a.ReferenceDashboard(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := apiReferenceDashboard{
		Total: d.Total,
		Steps: apiReferenceDashboardSteps{
			Bibliographic:       apiStepCount{Done: d.Steps.Bibliographic.Done, Total: d.Steps.Bibliographic.Total},
			Digitized:           apiStepCount{Done: d.Steps.Digitized.Done, Total: d.Steps.Digitized.Total},
			AssertionsExtracted: apiStepCount{Done: d.Steps.AssertionsExtracted.Done, Total: d.Steps.AssertionsExtracted.Total},
			Imported:            apiStepCount{Done: d.Steps.Imported.Done, Total: d.Steps.Imported.Total},
		},
	}
	out.Attention = make([]apiAttentionRow, len(d.Attention))
	for i, a := range d.Attention {
		out.Attention[i] = apiAttentionRow{
			Step:        a.Step,
			Description: a.Description,
			Count:       a.Count,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiReferenceDashboard mirrors the JSON shape documented in
// REFERENCES_DASHBOARD_PLAN § Step counters. Keys use snake_case
// to match the rest of hive's API surface.
type apiReferenceDashboard struct {
	Total     int                        `json:"total"`
	Steps     apiReferenceDashboardSteps `json:"steps"`
	Attention []apiAttentionRow          `json:"attention"`
}

type apiReferenceDashboardSteps struct {
	Bibliographic       apiStepCount `json:"bibliographic"`
	Digitized           apiStepCount `json:"digitized"`
	AssertionsExtracted apiStepCount `json:"assertions_extracted"`
	Imported            apiStepCount `json:"imported"`
}

type apiStepCount struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type apiAttentionRow struct {
	Step        int    `json:"step"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

// handleCreateCorrection accepts a new bbox-add correction, writes
// the row inside a transaction, applies the mutation to the JATS
// sidecar, and re-runs figextract on the new region so the PNG is
// ready when the reader re-fetches. Body shape: apiCorrection.
//
// Slice 1 supports content_type = 'figure' | 'table' only. Other
// content types (abstract, section, paragraph) land in slice 3
// (cross-surface placement) with their own apply paths.
//
// Returns 201 + the created correction (with server-assigned id and
// timestamps). On JATS mutation or figextract failure the DB row
// stays (so the training-signal record isn't lost) but the response
// carries a warning field so the UI can surface the partial success.
//
// @Summary      Create a reference correction
// @Description  Insert a new correction row (bbox-add, text-tag, ref-merge, etc), apply the mutation to the JATS sidecar, and re-run figure extraction on the new region.
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        body body apiCorrection true "Correction body"
// @Success      201 {object} apiCorrectionResponse
// @Failure      400 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/correction [post]
func (s *server) handleCreateCorrection(w http.ResponseWriter, r *http.Request) {
	var body apiCorrection
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.ReferenceID == "" || body.SidecarSHA == "" {
		writeBadRequest(w, r, "reference_id and sidecar_sha required")
		return
	}
	if body.Kind == "" {
		body.Kind = string(correction.KindBBoxAdd)
	}
	// Kind-scoped validation: bbox-* kinds require figure/table
	// content types; ref-merge scopes to references (content_type
	// = "reference") and needs only a target_element_id (a <ref>
	// id in the JATS ref-list — no bbox involved).
	switch body.Kind {
	case string(correction.KindBBoxAdd),
		string(correction.KindBBoxModify),
		string(correction.KindBBoxRemove):
		if body.ContentType != "figure" && body.ContentType != "table" {
			writeBadRequest(w, r, "content_type must be 'figure' or 'table' for bbox-* corrections")
			return
		}
	case string(correction.KindRefMerge):
		if body.TargetElementID == "" {
			writeBadRequest(w, r, "target_element_id required for ref-merge")
			return
		}
		if body.ContentType == "" {
			body.ContentType = "reference"
		}
	case string(correction.KindRefEdit):
		if body.TargetElementID == "" {
			writeBadRequest(w, r, "target_element_id required for ref-edit")
			return
		}
		if body.Payload == "" || body.Payload == "{}" {
			writeBadRequest(w, r, "payload with corrected fields required for ref-edit")
			return
		}
		if body.ContentType == "" {
			body.ContentType = "reference"
		}
	case string(correction.KindTextRemove):
		if body.Payload == "" || body.Payload == "{}" {
			writeBadRequest(w, r, "payload with text required for text-remove")
			return
		}
		if body.ContentType == "" {
			body.ContentType = "text"
		}
	case string(correction.KindTextTag):
		tp, err := correction.ParseTextTagPayload(body.Payload)
		if err != nil {
			writeBadRequest(w, r, "text-tag "+err.Error())
			return
		}
		if tp.Action == correction.TextTagMoveToCaption && body.TargetElementID == "" {
			writeBadRequest(w, r, "target_element_id required for move-to-caption")
			return
		}
		if body.ContentType == "" {
			body.ContentType = "text"
		}
	case string(correction.KindTextRestore):
		if body.Payload == "" || body.Payload == "{}" {
			writeBadRequest(w, r, "payload with remove_correction_id required for text-restore")
			return
		}
		if body.ContentType == "" {
			body.ContentType = "text"
		}
	default:
		writeBadRequest(w, r, "unsupported correction kind: "+body.Kind)
		return
	}
	if body.Kind == string(correction.KindBBoxAdd) {
		if body.BBox == nil || body.BBoxPage <= 0 {
			writeBadRequest(w, r, "bbox and bbox_page > 0 required for bbox-add")
			return
		}
	}
	if body.Kind == string(correction.KindBBoxModify) {
		if body.TargetElementID == "" {
			writeBadRequest(w, r, "target_element_id required for bbox-modify")
			return
		}
		if body.Payload == "" {
			writeBadRequest(w, r, "payload with original+corrected required for bbox-modify")
			return
		}
	}
	if body.Kind == string(correction.KindBBoxRemove) {
		if body.TargetElementID == "" {
			writeBadRequest(w, r, "target_element_id required for bbox-remove")
			return
		}
		// Snapshot the current GROBID state of the target element
		// so the training-corpus export has the false-positive
		// example even after re-derive drops the element. Without
		// this, the correction records only "this id doesn't
		// exist" — useless for training a better extractor.
		if body.Payload == "" || body.Payload == "{}" {
			if jats, err := sidecar.Read(s.archivePath, body.SidecarSHA, "jats.xml"); err == nil && jats != nil {
				if regions, err := labels.ExtractRegions(jats); err == nil {
					for _, rg := range regions {
						if rg.ID == body.TargetElementID {
							snapshot := map[string]any{
								"original": map[string]any{
									"kind":  rg.Kind,
									"label": rg.Text,
									"page":  rg.Page,
								},
							}
							if rg.BBox != nil {
								snapshot["original"].(map[string]any)["bbox"] = map[string]float64{
									"x": rg.BBox.X, "y": rg.BBox.Y,
									"w": rg.BBox.W, "h": rg.BBox.H,
								}
							}
							if b, err := json.Marshal(snapshot); err == nil {
								body.Payload = string(b)
							}
							break
						}
					}
				}
			}
		}
	}

	c := &correction.Correction{
		ReferenceID:     body.ReferenceID,
		SidecarSHA:      body.SidecarSHA,
		Kind:            correction.Kind(body.Kind),
		ContentType:     body.ContentType,
		BBoxPage:        body.BBoxPage,
		Label:           body.Label,
		TargetElementID: body.TargetElementID,
		Payload:         body.Payload,
	}
	if body.BBox != nil {
		c.BBox = &correction.BBox{X: body.BBox.X, Y: body.BBox.Y, W: body.BBox.W, H: body.BBox.H}
	}
	// For bbox-add of a table, extract the region's text with
	// pdftotext -layout and parse into rows/columns. Populates the
	// payload so BBoxAddFigureJATSElement emits a real <table>
	// child, which lets the reader modal show structured HTML in
	// the Digitized tab instead of a PNG-only view.
	//
	// Heuristic parsing only — works well for simple tables like
	// column-aligned data; a proper table-structuring UI (Pattern B
	// from DIGITIZE_CORRECTIONS_PLAN.md) is deferred.
	if c.Kind == correction.KindBBoxAdd && c.ContentType == "table" && c.BBox != nil {
		pdfPath := sidecar.Path(s.archivePath, c.SidecarSHA, "pdf")
		if _, err := os.Stat(pdfPath); err == nil {
			rows, err := pdftext.ExtractTable(pdfPath, c.BBoxPage,
				c.BBox.X, c.BBox.Y, c.BBox.W, c.BBox.H)
			if err == nil && len(rows) > 0 {
				var payload correction.BBoxAddPayload
				if c.Payload != "" && c.Payload != "{}" {
					_ = json.Unmarshal([]byte(c.Payload), &payload)
				}
				payload.TableRows = rows
				if b, err := json.Marshal(payload); err == nil {
					c.Payload = string(b)
				}
			}
		}
	}
	// Same table re-parse for bbox-modify on a table region — a
	// resize can uncover missing rows, so the row grid needs
	// re-extraction over the new bbox. Row output is merged into
	// payload.corrected.table_rows so ApplyBBoxModifyToJATS can
	// splice a fresh <table> into the target <table-wrap>.
	if c.Kind == correction.KindBBoxModify && c.ContentType == "table" {
		var mp correction.BBoxModifyPayload
		if c.Payload != "" && c.Payload != "{}" {
			_ = json.Unmarshal([]byte(c.Payload), &mp)
		}
		if mp.Corrected.BBox != nil {
			page := mp.Corrected.Page
			if page == 0 {
				page = mp.Original.Page
			}
			if page > 0 {
				pdfPath := sidecar.Path(s.archivePath, c.SidecarSHA, "pdf")
				if _, err := os.Stat(pdfPath); err == nil {
					rows, err := pdftext.ExtractTable(pdfPath, page,
						mp.Corrected.BBox.X, mp.Corrected.BBox.Y,
						mp.Corrected.BBox.W, mp.Corrected.BBox.H)
					if err == nil && len(rows) > 0 {
						mp.Corrected.TableRows = rows
						if b, err := json.Marshal(mp); err == nil {
							c.Payload = string(b)
						}
					}
				}
			}
		}
	}
	if c.Payload == "" {
		c.Payload = "{}"
	}
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.CreateCorrection(c)
	}); err != nil {
		writeProblem(w, r, err)
		return
	}

	// Apply to the sidecar. Failures don't roll the DB back — the
	// correction is still a valid training signal; the UI shows
	// warnings if the JATS didn't mutate cleanly and a retry can
	// re-attempt the apply.
	warnings, applyErr := s.applyBBoxCorrectionToSidecar(c)
	// A text correction that could not be applied (its text was not
	// found, or the result would not be well-formed) is marked
	// reverted so a later rebuild of the sidecar does not apply it
	// unexpectedly.
	if applyErr != nil && strings.HasPrefix(string(c.Kind), "text-") {
		reason := applyErr.Error()
		if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
			return tx.RevertCorrection(c.ID, reason)
		}); err == nil {
			c.Status = correction.StatusReverted
			c.RevertedReason = reason
		}
	}

	// Re-run reference-scoped validators so hive_citation_coverage
	// et al. reflect the mutated sidecar — a bbox-add for the
	// missing Table 1 should drop the "cited Figure/Table not
	// extracted" warning immediately, not on the next reindex.
	_ = s.a.SyncReferenceIssues(r.Context(), c.ReferenceID)

	writeJSON(w, http.StatusCreated, apiCorrectionResponse{
		Correction: apiCorrectionFromCore(c),
		Warnings:   warnings,
	})
}

// applyBBoxCorrectionToSidecar reads the current JATS sidecar,
// applies the correction to it (splice a new element for bbox-add,
// patch an existing element for bbox-modify), writes it back, then
// re-runs figextract so PNG crops stay in sync. Best-effort: each
// step returns a warning string on failure rather than aborting.
//
// applyErr is non-nil when the correction itself could not be
// applied to the JATS, as opposed to a sidecar I/O or figure
// extraction problem.
func (s *server) applyBBoxCorrectionToSidecar(c *correction.Correction) (warnings []string, applyErr error) {
	jats, err := sidecar.Read(s.archivePath, c.SidecarSHA, "jats.xml")
	if err != nil || jats == nil {
		return []string{"jats sidecar not found for sha " + c.SidecarSHA}, nil
	}
	mutated, err := s.a.ApplyCorrection(jats, c)
	if err != nil {
		return []string{"apply " + string(c.Kind) + ": " + err.Error()}, err
	}
	if err := sidecar.Store(s.archivePath, c.SidecarSHA, "jats.xml", mutated); err != nil {
		return []string{"write jats sidecar: " + err.Error()}, nil
	}
	// Figure extraction: pdftocairo cropping the new region. Only
	// runs when the source PDF is on disk (the common case; a
	// reference could in principle have lost its PDF after upload).
	pdfPath := sidecar.Path(s.archivePath, c.SidecarSHA, "pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		warnings = append(warnings, "source pdf missing; skipped figextract")
		return warnings, nil
	}
	outDir := filepath.Join(sidecar.Dir(s.archivePath), c.SidecarSHA+".figures")
	if _, err := figextract.Extract(mutated, pdfPath, outDir); err != nil {
		warnings = append(warnings, "figextract: "+err.Error())
	}
	return warnings, nil
}

// handlePreviewCorrection dry-runs a bbox: crops the region to
// PNG, extracts text from it via pdftotext, and returns both the
// PNG (as a data URL) and any recognized "Figure N" / "Table N"
// hint from the extracted text. Nothing is persisted — this is
// the form's "what will I get if I save this bbox?" preview.
//
// Body: {sidecar_sha, page, bbox: {x, y, w, h}} in PDF points.
// Response: {png_data_url, extracted_text, detected_type,
//
//	detected_label, existing_labels: [{label, page, kind, id}]}.
//
// existing_labels is returned inline so the form only needs one
// round-trip after the bbox is drawn.
//
// @Summary      Preview a correction
// @Description  Dry-run a bbox: crop the region to PNG, extract text via pdftotext, and return the PNG data URL plus any detected fig/table label. Nothing is persisted.
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        body body apiPreviewCorrectionRequest true "Preview request"
// @Success      200 {object} apiPreviewCorrectionResponse
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/correction/preview [post]
func (s *server) handlePreviewCorrection(w http.ResponseWriter, r *http.Request) {
	var body apiPreviewCorrectionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if body.SidecarSHA == "" {
		writeBadRequest(w, r, "sidecar_sha required")
		return
	}
	if body.BBox == nil || body.Page <= 0 {
		writeBadRequest(w, r, "bbox and page > 0 required")
		return
	}
	pdfPath := sidecar.Path(s.archivePath, body.SidecarSHA, "pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		writeBadRequest(w, r, "source pdf not found for sha")
		return
	}
	// PNG crop for the visual preview. Absent bytes are OK (means
	// pdftocairo not installed) — form still shows the detected
	// text and existing labels.
	png, err := figextract.PreviewRegion(pdfPath, body.Page,
		body.BBox.X, body.BBox.Y, body.BBox.W, body.BBox.H)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := apiPreviewCorrectionResponse{}
	if png != nil {
		out.PNGDataURL = "data:image/png;base64," +
			base64.StdEncoding.EncodeToString(png)
	}
	// Text extraction + fig/table detection. Silent skip when
	// pdftotext isn't installed OR the PDF is image-only — form
	// falls back to curator-typed values.
	det, err := pdftext.ExtractRegion(pdfPath, body.Page,
		body.BBox.X, body.BBox.Y, body.BBox.W, body.BBox.H)
	if err == nil && det != nil {
		out.ExtractedText = det.FullText
		out.DetectedType = det.ContentType
		out.DetectedLabel = det.Label
	}
	// Existing labels from the JATS sidecar so the form can show
	// what figs/tables the paper already has, ordered by page.
	if jats, err := sidecar.Read(s.archivePath, body.SidecarSHA, "jats.xml"); err == nil && jats != nil {
		if extracted, err := labels.Extract(jats); err == nil {
			out.ExistingLabels = make([]apiExtractedLabel, len(extracted))
			for i, l := range extracted {
				out.ExistingLabels[i] = apiExtractedLabel{
					Label: l.Text,
					Kind:  l.Kind,
					Page:  l.Page,
					ID:    l.ID,
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleReferenceLibraryRegions returns every fig/table-wrap
// region in the sidecar's JATS with bbox coords, so the PDF
// viewer can render all detected regions as overlays — GROBID-
// extracted ones in one color, curator-added ones in another.
// Powers the "see everything the extractor found" affordance on
// the Digitize tab.
//
// @Summary      List sidecar regions
// @Description  Every fig/table-wrap region in the sidecar JATS with bbox coords and origin (GROBID / curator).
// @Tags         references
// @Produce      json
// @Param        sha path string true "Sidecar SHA-512 hex"
// @Success      200 {object} apiRegionList
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/library/{sha}/regions [get]
func (s *server) handleReferenceLibraryRegions(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	if sha == "" {
		writeBadRequest(w, r, "sha required")
		return
	}
	jats, err := sidecar.Read(s.archivePath, sha, "jats.xml")
	if err != nil || jats == nil {
		http.NotFound(w, r)
		return
	}
	regions, err := labels.ExtractRegions(jats)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := apiRegionList{Items: make([]apiRegion, 0, len(regions))}
	for _, rg := range regions {
		item := apiRegion{
			ID:     rg.ID,
			Label:  rg.Text,
			Kind:   rg.Kind,
			Page:    rg.Page,
			Origin:  rg.Origin,
			Caption: rg.Caption,
		}
		if rg.BBox != nil {
			item.BBox = &apiBBox{X: rg.BBox.X, Y: rg.BBox.Y, W: rg.BBox.W, H: rg.BBox.H}
		}
		out.Items = append(out.Items, item)
	}
	writeJSON(w, http.StatusOK, out)
}

type apiRegionList struct {
	Items []apiRegion `json:"items"`
}

type apiRegion struct {
	ID      string   `json:"id"`
	Label   string   `json:"label,omitempty"`
	Kind    string   `json:"kind"`
	Page    int      `json:"page,omitempty"`
	Origin  string   `json:"origin"`
	BBox    *apiBBox `json:"bbox,omitempty"`
	Caption string   `json:"caption,omitempty"`
}

// apiPreviewCorrectionRequest is the wire shape for a dry-run
// bbox preview.
type apiPreviewCorrectionRequest struct {
	SidecarSHA string   `json:"sidecar_sha"`
	Page       int      `json:"page"`
	BBox       *apiBBox `json:"bbox"`
}

type apiPreviewCorrectionResponse struct {
	PNGDataURL     string              `json:"png_data_url,omitempty"`
	ExtractedText  string              `json:"extracted_text,omitempty"`
	DetectedType   string              `json:"detected_type,omitempty"`
	DetectedLabel  string              `json:"detected_label,omitempty"`
	ExistingLabels []apiExtractedLabel `json:"existing_labels,omitempty"`
}

type apiExtractedLabel struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Page  int    `json:"page,omitempty"`
	ID    string `json:"id,omitempty"`
}

// handleListCorrections returns every correction attached to the
// given reference, both active and reverted. Used by the Digitize
// tab's chip strip on load.
//
// @Summary      List corrections for a reference
// @Description  Every correction attached to the given reference, both active and reverted.
// @Tags         references
// @Produce      json
// @Param        id path string true "Reference ID"
// @Success      200 {object} apiCorrectionList
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id}/corrections [get]
func (s *server) handleListCorrections(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	if refID == "" {
		writeBadRequest(w, r, "reference id required")
		return
	}
	list, err := s.a.ListCorrectionsForReference(r.Context(), refID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiCorrection, len(list))
	for i, c := range list {
		items[i] = apiCorrectionFromCore(&c)
	}
	writeJSON(w, http.StatusOK, apiCorrectionList{Items: items})
}

// handleDeleteCorrection removes the correction row and re-derives
// the sidecar JATS from the remaining active corrections. Slice 1
// simplification: since bbox-add is additive, deletion re-applies
// every remaining active correction on top of the pre-correction
// baseline. Later slices with non-additive corrections may need a
// more sophisticated re-derive path.
//
// For slice 1 we take the simple approach: delete the row, then
// re-run jatsconv from the source TEI and re-apply every remaining
// active correction. Baseline is always regenerable, so no data
// loss risk.
//
// @Summary      Delete a correction
// @Description  Remove the correction row and re-derive the sidecar JATS from the remaining active corrections.
// @Tags         references
// @Produce      json
// @Param        id path string true "Correction ID"
// @Success      200 {object} apiCorrectionDeleteResponse
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/correction/{id} [delete]
func (s *server) handleDeleteCorrection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeBadRequest(w, r, "correction id required")
		return
	}
	c, err := s.a.GetCorrection(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.DeleteCorrection(id)
	}); err != nil {
		writeProblem(w, r, err)
		return
	}
	warnings := s.redoBBoxCorrectionsForReference(c.ReferenceID, c.SidecarSHA)
	// Same re-validation trigger as the create path — sidecar
	// mutation from a delete could re-introduce a citation-coverage
	// warning (removed the only Table 1 → cited Table 1 no longer
	// exists in the JATS).
	_ = s.a.SyncReferenceIssues(r.Context(), c.ReferenceID)
	writeJSON(w, http.StatusOK, apiCorrectionDeleteResponse{
		Deleted:  true,
		Warnings: warnings,
	})
}

// redoBBoxCorrectionsForReference rebuilds the JATS from the
// original TEI, then re-applies every remaining active bbox-add
// correction. Used after a correction deletion.
//
// TEI-based baseline: hive keeps the raw GROBID TEI on disk
// alongside the JATS, so re-deriving is (a) safe and (b) doesn't
// require another GROBID call. jatsconv is fast (microseconds per
// paragraph); the whole re-derive completes in tens of ms.
func (s *server) redoBBoxCorrectionsForReference(refID, sha string) []string {
	var warnings []string
	tei, err := sidecar.Read(s.archivePath, sha, "tei.xml")
	if err != nil || tei == nil {
		return []string{"tei sidecar missing; skip re-derive"}
	}
	// Same word-page extraction as tryAnnotate so re-derived JATS
	// keeps its page-break markers after a correction delete.
	var jatsOpts jatsconv.Options
	pdfPath := sidecar.Path(s.archivePath, sha, "pdf")
	if _, statErr := os.Stat(pdfPath); statErr == nil {
		if words, err := pdftext.WordPages(pdfPath); err == nil && len(words) > 0 {
			jatsOpts.WordPages = words
			if journalPage1 := pdftext.JournalPageOfPDFPage1(words); journalPage1 > 1 {
				jatsOpts.PageOffset = journalPage1 - 1
			}
		}
	}
	jats, err := jatsconv.TEIToJATSOpts(tei, jatsOpts)
	if err != nil {
		return []string{"re-derive jats: " + err.Error()}
	}
	// Re-run the annotator so tp:taxon-name chips (GNfinder /
	// GNverifier output) survive the delete + re-derive cycle.
	// Skipping this step here was silently stripping every chip
	// from the sidecar whenever a curator deleted a correction.
	if s.annotator != nil {
		if augmented, annErr := s.annotator.Annotate(context.Background(), jats); annErr == nil {
			jats = augmented
		} else {
			warnings = append(warnings, "annotate: "+annErr.Error())
		}
	}
	remaining, err := s.a.ListCorrectionsForReference(context.Background(), refID)
	if err != nil {
		return []string{"list remaining corrections: " + err.Error()}
	}
	for i := range remaining {
		c := &remaining[i]
		if c.Status != correction.StatusActive {
			continue
		}
		// Defensive filter: bbox-* kinds require figure/table
		// content type. Skip rows with mismatched content_type
		// (shouldn't exist post-validation, but old rows may).
		if correction.IsBBoxKind(c.Kind) &&
			c.ContentType != "figure" && c.ContentType != "table" {
			continue
		}
		mutated, err := s.a.ApplyCorrection(jats, c)
		if err != nil {
			warnings = append(warnings, "reapply "+c.ID+": "+err.Error())
			continue
		}
		jats = mutated
	}
	if err := sidecar.Store(s.archivePath, sha, "jats.xml", jats); err != nil {
		warnings = append(warnings, "write sidecar: "+err.Error())
		return warnings
	}
	// Re-run figextract so PNGs for remaining corrections stay in sync.
	if _, err := os.Stat(pdfPath); err == nil {
		outDir := filepath.Join(sidecar.Dir(s.archivePath), sha+".figures")
		if _, err := figextract.Extract(jats, pdfPath, outDir); err != nil {
			warnings = append(warnings, "figextract: "+err.Error())
		}
	}
	return warnings
}

// apiCorrection is the wire shape of a correction row. Optional
// fields use pointers so the JSON round-trips distinguish "empty"
// from "not present".
type apiCorrection struct {
	ID              string     `json:"id,omitempty"`
	ReferenceID     string     `json:"reference_id"`
	SidecarSHA      string     `json:"sidecar_sha"`
	Kind            string     `json:"kind"`
	ContentType     string     `json:"content_type"`
	BBoxPage        int        `json:"bbox_page,omitempty"`
	BBox            *apiBBox   `json:"bbox,omitempty"`
	Label           string     `json:"label,omitempty"`
	AnchorPtr       *apiAnchor `json:"anchor_ptr,omitempty"`
	TargetElementID string     `json:"target_element_id,omitempty"`
	Payload         string     `json:"payload,omitempty"`
	Status          string     `json:"status,omitempty"`
	RevertedReason  string     `json:"reverted_reason,omitempty"`
	CreatedAt       string     `json:"created_at,omitempty"`
	CreatedBy       string     `json:"created_by,omitempty"`
	// FigureID is the JATS element id the correction's fig/table
	// element ends up with — used by the client to build the PNG
	// URL (/api/reference/library/<sha>/figures/<id>).
	FigureID string `json:"figure_id,omitempty"`
}

type apiBBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type apiAnchor struct {
	ElementID string `json:"element_id"`
	Position  string `json:"position"`
}

type apiCorrectionResponse struct {
	Correction apiCorrection `json:"correction"`
	Warnings   []string      `json:"warnings,omitempty"`
}

type apiCorrectionList struct {
	Items []apiCorrection `json:"items"`
}

type apiCorrectionDeleteResponse struct {
	Deleted  bool     `json:"deleted"`
	Warnings []string `json:"warnings,omitempty"`
}

// apiCorrectionFromCore maps a core Correction to the wire shape.
// Populates FigureID for bbox-add corrections of figure/table type
// so the client can construct the extracted-PNG URL.
func apiCorrectionFromCore(c *correction.Correction) apiCorrection {
	out := apiCorrection{
		ID:              c.ID,
		ReferenceID:     c.ReferenceID,
		SidecarSHA:      c.SidecarSHA,
		Kind:            string(c.Kind),
		ContentType:     c.ContentType,
		BBoxPage:        c.BBoxPage,
		Label:           c.Label,
		TargetElementID: c.TargetElementID,
		Payload:         c.Payload,
		Status:          string(c.Status),
		RevertedReason:  c.RevertedReason,
		CreatedBy:       c.CreatedBy,
	}
	if !c.CreatedAt.IsZero() {
		out.CreatedAt = c.CreatedAt.Format(time.RFC3339)
	}
	if c.BBox != nil {
		out.BBox = &apiBBox{X: c.BBox.X, Y: c.BBox.Y, W: c.BBox.W, H: c.BBox.H}
	}
	if c.AnchorPtr != nil {
		out.AnchorPtr = &apiAnchor{
			ElementID: c.AnchorPtr.ElementID,
			Position:  c.AnchorPtr.Position,
		}
	}
	if c.Kind == correction.KindBBoxAdd &&
		(c.ContentType == "figure" || c.ContentType == "table") {
		out.FigureID = correction.CurationElementID(c.ID)
	}
	if c.Kind == correction.KindBBoxModify {
		out.FigureID = c.TargetElementID
	}
	return out
}

// handleRegenerateCitations rewrites col__citation on reference
// rows using ComposeCitation over each row's current structured
// fields. Query param overwrite=true replaces every row's citation
// with the freshly-composed one (for normalizing an archive
// imported from a source with poorly-formatted citations); default
// (or overwrite=false) only fills currently-empty citations,
// preserving import citations that came with their own format.
//
// Returns the number of rows actually updated.
//
// @Summary      Regenerate reference citations
// @Description  Rewrite col__citation using ComposeCitation over each row's structured fields. overwrite=true replaces every row; default only fills empty citations.
// @Tags         references
// @Produce      json
// @Param        overwrite query bool false "Replace non-empty citations too"
// @Success      200 {object} map[string]int
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/regenerate-citations [post]
func (s *server) handleRegenerateCitations(w http.ResponseWriter, r *http.Request) {
	overwrite := r.URL.Query().Get("overwrite") == "true"
	var updated int
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		n, err := tx.RegenerateCitations(overwrite)
		updated = n
		return err
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// handleCreateReference writes a new reference from the JSON body and
// returns the freshly-fetched row (fully hydrated with modified /
// modified_by). Body accepts every apiReference field; ID is optional
// — an empty ID triggers a UUID inside hive.CreateReference. Callers
// that want to preserve an upstream id (e.g. a legacy CoLDP-string
// identifier) can send it verbatim.
//
// @Summary      Create a reference
// @Description  Insert a new reference row and return the hydrated record. Optional attach_pdf_sha query param links a previously-uploaded sidecar PDF in the same tx.
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        attach_pdf_sha query string false "Sidecar SHA-512 to attach in the same tx"
// @Param        body body apiReference true "Reference body"
// @Success      201 {object} apiReference
// @Failure      400 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /reference [post]
func (s *server) handleCreateReference(w http.ResponseWriter, r *http.Request) {
	var body apiReference
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ref := apiReferenceToColdp(body)
	// Optional ?attach_pdf_sha=... query param associates a
	// sidecar-library sha512 with the newly-created reference in
	// the same transaction. Populated by the reference-add modal
	// when a PDF was uploaded and the resulting sha needs to be
	// linked to the record. Empty is fine — plenty of references
	// have no attached PDF.
	attachSha := strings.TrimSpace(r.URL.Query().Get("attach_pdf_sha"))

	var newID string
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		id, err := tx.CreateReference(ref)
		if err != nil {
			return err
		}
		newID = id
		if attachSha != "" {
			if err := tx.AttachReferencePDF(id, attachSha); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetReference(r.Context(), newID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := referenceToAPI(fresh)
	if attachSha != "" {
		out.AttachedPDFShas = []string{attachSha}
		out.AttachedFiles = s.buildAttachedFiles([]string{attachSha})
		out.HasSourceDoc = true
	}
	if fresh.Modified != "" {
		w.Header().Set("ETag", fresh.Modified)
	}
	w.Header().Set("Location", "/api/reference/"+newID)
	writeJSON(w, http.StatusCreated, out)
}

// handleResolveDOI fetches an OpenAlex Work for the DOI in the `doi`
// query param and returns the resulting coldp.Reference — *unsaved*.
// The modal shows this as a preview form; on curator confirm the WUI
// POSTs to /api/reference to persist. Returns 404 with a distinguishing
// problem type when OpenAlex has no match, 502 on upstream errors.
//
// @Summary      Resolve a DOI to a reference preview
// @Description  Fetch a reference by DOI from the configured resolver. Returns an unsaved apiReference preview (empty id); the curator confirms and POSTs to /api/reference to persist.
// @Tags         references
// @Produce      json
// @Param        doi query string true "DOI (with or without doi.org prefix)"
// @Success      200 {object} apiReference
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      501 {object} problem
// @Failure      502 {object} problem
// @Router       /reference/resolve-doi [get]
func (s *server) handleResolveDOI(w http.ResponseWriter, r *http.Request) {
	doi := strings.TrimSpace(r.URL.Query().Get("doi"))
	if doi == "" {
		writeBadRequest(w, r, "query parameter 'doi' is required")
		return
	}
	resolver := s.a.Lookup.DOIResolver()
	if resolver == nil {
		writeProblemDetail(w, r, http.StatusNotImplemented,
			"urn:hive:error:no-doi-resolver", "no DOI resolver configured",
			"this archive has no registered DOI resolver")
		return
	}
	ref, err := resolver.ResolveDOI(r.Context(), doi)
	if err != nil {
		if errors.Is(err, openalex.ErrNotFound) {
			writeProblemDetail(w, r, http.StatusNotFound,
				"urn:hive:error:doi-not-found", "not found",
				resolver.Name()+" has no work for DOI "+doi)
			return
		}
		writeProblemDetail(w, r, http.StatusBadGateway,
			"urn:hive:error:doi-lookup", resolver.Name()+" lookup failed", err.Error())
		return
	}
	// Clear the generated UUID from ResolveDOI — this is a preview, not
	// a saved row. The WUI leaves ID empty on the POST so hive.CreateReference
	// mints a fresh one at write time. Sending a UUID here would be
	// misleading (the curator might think it's already saved).
	ref.ID = ""
	writeJSON(w, http.StatusOK, referenceToAPI(ref))
}

// handleLookupOpenAlex searches OpenAlex for references matching the
// supplied metadata, useful when a PDF has no DOI printed on it but
// might have had one retroactively assigned by a publisher backfile
// digitization program (Taylor & Francis, Wiley, Springer, etc.) or
// indexed by DataCite (BHL scans). Returns up to 5 candidates; the
// frontend renders them as suggestions the curator can pick from —
// picking triggers the standard DOI-resolve + conflict-picker flow.
//
// Query params: title (required), author (optional first-author
// surname, sharpens the OpenAlex ranking), year (optional
// publication year for the same reason).
//
// @Summary      OpenAlex free-text lookup
// @Description  Search OpenAlex for references matching title (+ optional author, year). Returns up to 5 unsaved apiReference previews.
// @Tags         references
// @Produce      json
// @Param        title query string true "Title text"
// @Param        author query string false "First-author surname"
// @Param        year query string false "Publication year"
// @Success      200 {object} apiPage[apiReference]
// @Failure      400 {object} problem
// @Failure      501 {object} problem
// @Failure      502 {object} problem
// @Router       /reference/lookup-openalex [get]
func (s *server) handleLookupOpenAlex(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if title == "" {
		writeBadRequest(w, r, "title required")
		return
	}
	author := strings.TrimSpace(r.URL.Query().Get("author"))
	year := strings.TrimSpace(r.URL.Query().Get("year"))
	// Compose the search query. OpenAlex's cross-field ranker weights
	// title matches strongly; appending author + year narrows without
	// requiring the caller to know OpenAlex filter syntax and without
	// over-filtering (a typo in the extracted author surname would
	// otherwise wipe out real matches).
	q := title
	if author != "" {
		q += " " + author
	}
	if year != "" {
		q += " " + year
	}
	searcher := s.a.Lookup.FreeTextSearcher()
	if searcher == nil {
		writeProblemDetail(w, r, http.StatusNotImplemented,
			"urn:hive:error:no-free-text-searcher", "no free-text searcher configured",
			"this archive has no registered free-text reference searcher")
		return
	}
	hits, err := searcher.Search(r.Context(), q, 5)
	if err != nil {
		writeProblemDetail(w, r, http.StatusBadGateway,
			"urn:hive:error:free-text-search", searcher.Name()+" lookup failed", err.Error())
		return
	}
	items := make([]apiReference, 0, len(hits))
	for i := range hits {
		// Clear the generated UUID — these are preview candidates,
		// not saved rows (mirrors handleResolveDOI).
		hits[i].ID = ""
		items = append(items, referenceToAPI(&hits[i]))
	}
	writeJSON(w, http.StatusOK, apiPage[apiReference]{Items: items})
}

// handleLookupBHLnames runs a BHLnames match for the given scientific
// name and returns hits ordered as BHLnames returned them (newest-first
// by default; see bhlnames.Client.LookupName). Each hit carries an
// unsaved preview reference plus BHLnames's own quality/score signals so
// the modal can render "auto-suggest confidently" vs "here if you want
// it" tiers.
//
// Body: {canonical, authors, year, nomen_event, limit}. `canonical` is
// required; the rest are optional but scores improve dramatically when
// authorship + year are supplied.
//
// @Summary      BHLnames scientific-name lookup
// @Description  Match a scientific name against BHLnames and return hits (each with an unsaved apiReference preview and BHLnames quality signals).
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        body body object true "Lookup request {canonical, authors, year, nomen_event, limit}"
// @Success      200 {object} apiPage[apiBHLnameHit]
// @Failure      400 {object} problem
// @Failure      502 {object} problem
// @Router       /reference/lookup-bhlnames [post]
func (s *server) handleLookupBHLnames(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Canonical  string `json:"canonical"`
		Authors    string `json:"authors"`
		Year       int    `json:"year"`
		NomenEvent bool   `json:"nomen_event"`
		Limit      int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Canonical) == "" {
		writeBadRequest(w, r, "canonical name is required")
		return
	}
	hits, err := s.bhlnames.LookupName(r.Context(),
		body.Canonical, body.Authors, body.Year,
		bhlnames.LookupOpts{
			RefsLimit:  body.Limit,
			NomenEvent: body.NomenEvent,
		})
	if err != nil {
		writeProblemDetail(w, r, http.StatusBadGateway,
			"urn:hive:error:bhlnames", "bhlnames lookup failed", err.Error())
		return
	}
	items := make([]apiBHLnameHit, 0, len(hits))
	for _, h := range hits {
		// Same "no id on preview" convention as resolve-doi.
		h.Reference.ID = ""
		items = append(items, bhlnameHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiBHLnameHit]{Items: items})
}

// handleParseBibTeX parses the BibTeX entry in the request body and
// returns the resulting coldp.Reference — unsaved. Accepts either
// application/x-bibtex (raw entry as the body) or application/json
// ({"text":"@article{..."}) for callers that prefer to keep everything
// on the JSON path.
//
// @Summary      Parse a BibTeX entry
// @Description  Parse a single BibTeX entry (raw body or {"text":"..."} JSON) and return an unsaved apiReference preview.
// @Tags         references
// @Accept       json
// @Accept       plain
// @Produce      json
// @Param        body body string true "BibTeX entry text"
// @Success      200 {object} apiReference
// @Failure      400 {object} problem
// @Failure      422 {object} problem
// @Router       /reference/parse-bibtex [post]
func (s *server) handleParseBibTeX(w http.ResponseWriter, r *http.Request) {
	var text string
	ct := r.Header.Get("Content-Type")
	// Split on ';' so "application/json; charset=utf-8" still matches.
	mediatype := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	switch mediatype {
	case "application/json":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeBadRequest(w, r, "invalid JSON body: "+err.Error())
			return
		}
		text = body.Text
	default:
		// Treat everything else (text/plain, application/x-bibtex,
		// empty CT) as a raw BibTeX body — the parser rejects garbage
		// with ErrBibTeXParse anyway.
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeBadRequest(w, r, "read body: "+err.Error())
			return
		}
		text = string(raw)
	}
	ref, err := bibtex.Parse(text)
	if err != nil {
		if errors.Is(err, bibtex.ErrParse) {
			writeProblemDetail(w, r, http.StatusUnprocessableEntity,
				"urn:hive:error:bibtex", "bibtex parse failed", err.Error())
			return
		}
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, referenceToAPI(ref))
}

// handleGetReference returns the full reference detail.
//
// @Summary      Get a reference
// @Description  Full reference detail, including attached sidecar PDF shas.
// @Tags         references
// @Produce      json
// @Param        id path string true "Reference ID"
// @Success      200 {object} apiReference
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id} [get]
func (s *server) handleGetReference(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ref, err := s.a.GetReference(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if ref.Modified != "" {
		w.Header().Set("ETag", ref.Modified)
	}
	out := referenceToAPI(ref)
	// Attach sidecar-PDF associations from hive__reference_pdf so
	// the references-screen reader pane knows which sha (if any)
	// to fetch. Empty slice is elided by omitempty.
	if shas, err := s.a.ReferencePDFs(r.Context(), id); err == nil && len(shas) > 0 {
		out.AttachedFiles = s.buildAttachedFiles(shas)
		// Re-derive the legacy attached_pdf_shas from the sorted
		// AttachedFiles so downstream consumers (reader iframe
		// picker) default to the PDF sha before falling back to
		// non-PDF attachments.
		out.AttachedPDFShas = make([]string, 0, len(out.AttachedFiles))
		for _, af := range out.AttachedFiles {
			out.AttachedPDFShas = append(out.AttachedPDFShas, af.SHA512)
		}
		out.HasSourceDoc = true
	}
	if wf, err := s.a.ReferenceWorkflowFor(r.Context(), id); err == nil {
		out.Workflow = referenceWorkflowToAPI(wf)
	}
	writeJSON(w, http.StatusOK, out)
}

// apiCasingVocabulary maps a lower-case word to the form the
// reference's own text writes it in (see package casing).
type apiCasingVocabulary struct {
	Words map[string]string `json:"words"`
}

// handleReferenceCasing returns the casing vocabulary learned from the
// text of the reference's attached documents. The WUI uses it to
// restore titles and other fields supplied in all capitals. Empty when
// no document is attached.
//
// @Summary      Casing vocabulary of a reference's documents
// @Description  Words the reference's attached documents write with capitals mid-sentence (proper nouns, genus and higher names, gene names, acronyms), keyed by lower case.
// @Tags         references
// @Produce      json
// @Param        id path string true "Reference ID"
// @Success      200 {object} apiCasingVocabulary
// @Failure      500 {object} problem
// @Router       /reference/{id}/casing [get]
func (s *server) handleReferenceCasing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	shas, err := s.a.ReferencePDFs(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var segs []string
	for _, sha := range shas {
		jats, err := sidecar.Read(s.archivePath, sha, "jats.xml")
		if err != nil || jats == nil {
			continue
		}
		if ss, err := casing.JATSSegments(jats); err == nil {
			segs = append(segs, ss...)
		}
	}
	writeJSON(w, http.StatusOK, apiCasingVocabulary{Words: casing.Vocabulary(segs)})
}

// buildAttachedFiles enriches a bare sha slice with the primary
// upload extension for each sha (read from the sidecar's
// meta.json). Sorts the result so PDFs come first — hive's WUI
// reader defaults to the first sha, and a paper's PDF is almost
// always the more useful default view than its publisher JATS
// or EPUB for bibliography verification and digitization work.
//
// Missing meta.json falls back to the raw sha with empty ext so
// the sha still surfaces in the WUI even when the sidecar is
// incomplete (e.g., an older upload predating meta.json).
func (s *server) buildAttachedFiles(shas []string) []apiAttachedFile {
	out := make([]apiAttachedFile, 0, len(shas))
	for _, sha := range shas {
		af := apiAttachedFile{SHA512: sha}
		if m, err := sidecar.ReadMeta(s.archivePath, sha); err == nil && m != nil {
			af.Ext = m.Ext
		}
		out = append(out, af)
	}
	// Stable ordering: PDF first, then everything else by original
	// order. Using a rank helper keeps the sort deterministic even
	// when multiple non-PDF formats are present.
	rank := func(ext string) int {
		if ext == "pdf" {
			return 0
		}
		return 1
	}
	for i := 1; i < len(out); i++ {
		if rank(out[i].Ext) < rank(out[i-1].Ext) {
			out[i], out[i-1] = out[i-1], out[i]
			// One-pass insertion is enough because we're only
			// promoting a single "pdf" past non-pdfs.
			for j := i; j > 1 && rank(out[j].Ext) < rank(out[j-1].Ext); j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
	}
	return out
}

// handlePatchReference applies a partial update to a reference row.
// Mirrors the handlePatchName / handlePatchTaxon shape: pointer-
// optional fields (omit vs. clear), If-Match optimistic concurrency,
// re-stamps col__modified / col__modified_by inside the tx. Wraps
// Tx.UpdateReference — the core primitive that carries the actual
// write.
//
// Backs the reference-quick-fix modal (Slice D) that opens over the
// create/edit taxon pane when a reference has a validation warning
// on missing structured metadata. See feedback_no_side_quests for
// the UX principle motivating in-context fixes.
//
// @Summary      Patch a reference
// @Description  Partial update to a reference row. Pointer-optional fields distinguish clear vs leave-alone; supports If-Match optimistic concurrency.
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        id path string true "Reference ID"
// @Param        If-Match header string false "col__modified ETag for optimistic concurrency"
// @Param        body body apiReferencePatch true "Reference patch body"
// @Success      200 {object} apiReference
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/{id} [patch]
func (s *server) handlePatchReference(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch apiReferencePatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ifMatch := r.Header.Get("If-Match")

	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetReference(r.Context(), id)
		if err != nil {
			return err
		}
		applyReferencePatch(current, patch)
		current.Modified = ifMatch
		return tx.UpdateReference(*current)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}

	fresh, err := s.a.GetReference(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if fresh.Modified != "" {
		w.Header().Set("ETag", fresh.Modified)
	}
	writeJSON(w, http.StatusOK, referenceToAPI(fresh))
}

// handleReferenceUpload accepts a multipart file upload (PDF /
// EPUB / HTML / JATS) and stores it in the archive's sidecar
// library (<archive>.library/) content-addressed on sha512.
// Idempotent per hash — uploading the same bytes twice returns
// the same sha with already_existed=true.
//
// Downstream steps (GROBID → TEI → JATS, DOI extraction, GNfinder,
// etc.) run in follow-up handlers so this upload path stays fast
// and doesn't block on the pipeline. A curator dragging a PDF
// into the reference modal gets an immediate "stored" signal;
// conversion happens asynchronously and the UI polls or waits on
// its own.
//
// Max upload size is limited to 100 MB. Scientific PDFs are
// almost always under that; the cap protects against a curator
// accidentally uploading a book scan or a huge sequencing dataset.
//
// @Summary      Upload a reference source file
// @Description  Multipart upload of PDF / EPUB / HTML / JATS. Stored content-addressed on sha512; PDFs trigger GROBID + annotation follow-ups synchronously.
// @Tags         references
// @Accept       mpfd
// @Produce      json
// @Param        file formData file true "Source file (pdf/epub/html/xml/jats)"
// @Param        source_url formData string false "Optional origin URL for the upload"
// @Success      201 {object} apiReferenceUpload
// @Failure      400 {object} problem
// @Failure      413 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/upload [post]
func (s *server) handleReferenceUpload(w http.ResponseWriter, r *http.Request) {
	const maxUploadBytes = 100 << 20 // 100 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeBadRequest(w, r, "invalid multipart body: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeBadRequest(w, r, "missing 'file' field in multipart body")
		return
	}
	defer file.Close()

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(header.Filename)), ".")
	if ext == "" {
		writeBadRequest(w, r, "upload filename has no extension — hive needs it to know how to process the file")
		return
	}
	// Whitelist known formats. Unknown extensions land in the
	// sidecar but the pipeline won't know how to convert them —
	// better to reject up front with a clear message.
	switch ext {
	case "pdf", "epub", "xml", "jats":
		// OK — supported input formats. xml + jats treated as JATS.
	default:
		writeBadRequest(w, r,
			"unsupported file type '"+ext+"' — hive accepts pdf, epub, jats/xml")
		return
	}
	mime := header.Header.Get("Content-Type")
	sourceURL := r.FormValue("source_url")

	res, err := sidecar.Ingest(s.archivePath, file, ext, mime, sourceURL)
	if err != nil {
		writeProblem(w, r, err)
		return
	}

	// Downstream TEI conversion via GROBID. Only PDFs go through
	// GROBID; other formats (EPUB / HTML / JATS) have their own
	// conversion paths that aren't wired yet. Skip re-processing
	// when the file was a dedup hit AND a TEI already exists on
	// disk from a prior upload. When TEI lands, extract both the
	// DOI (for auto-hop to the DOI tab) and the full header
	// metadata (for the Manual-tab fallback).
	teiStatus, doi, meta := s.tryGrobidConvert(r.Context(), res, ext)
	// GROBID handles PDF only. For JATS uploads the DOI + basic
	// metadata live in the file itself as XML — pull them out
	// with a lightweight parse so DOI-based dedup works for
	// JATS-first ingestion (multi-format workflows drop the
	// publisher-native XML before the PDF).
	if ext == "xml" || ext == "jats" {
		if xdoi, xmeta := extractJATSMeta(res.Path); xdoi != "" || xmeta != nil {
			if doi == "" {
				doi = xdoi
			}
			if meta == nil {
				meta = xmeta
			}
		}
	}
	// EPUB carries the same bibliographic fields as JATS but in
	// its container.xml → content.opf Dublin Core block. Parse
	// it so an EPUB-only upload lands as a properly-titled
	// reference row instead of "Untitled (bulk ingest)".
	if ext == "epub" {
		if edoi, emeta := extractEPUBMeta(res.Path); edoi != "" || emeta != nil {
			if doi == "" {
				doi = edoi
			}
			if meta == nil {
				meta = emeta
			}
		}
	}
	annotStatus, annotCount := s.tryAnnotate(r.Context(), res, teiStatus)

	// Enumerate any references already attached to this sha so
	// the WUI can short-circuit the bibliographic-entry flow with
	// a "you already have this PDF" prompt instead of letting the
	// curator waste time filling out fields.
	attached := s.attachedReferencesForSHA(r.Context(), res.SHA512)

	// Look up references by extracted DOI. When the DOI matches
	// an existing reference row but the sha is new, this is the
	// multi-format case (JATS after PDF, EPUB after PDF, etc.).
	// Clients use MatchedByDOI to offer "attach to existing" so
	// each paper stays as one reference with N format sidecars.
	matched := s.matchedByDOI(r.Context(), doi, attached)

	writeJSON(w, http.StatusCreated, apiReferenceUpload{
		SHA512:              res.SHA512,
		Ext:                 res.Ext,
		Size:                res.Size,
		AlreadyExisted:      res.AlreadyExisted,
		TEIStatus:           teiStatus,
		DOI:                 doi,
		Metadata:            meta,
		AnnotationStatus:    annotStatus,
		AnnotationNameCount: annotCount,
		AttachedReferences:  attached,
		MatchedByDOI:        matched,
	})
}

// apiReferenceLicense is the JSON projection of
// hive.ReferenceLicense used by the license endpoints.
type apiReferenceLicense struct {
	ReferenceID string `json:"reference_id"`
	License     string `json:"license"`
	SourceURL   string `json:"source_url,omitempty"`
	VerifiedAt  string `json:"verified_at,omitempty"`
	VerifiedBy  string `json:"verified_by,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// apiSetLicenseReq is the PUT body.
type apiSetLicenseReq struct {
	License   string `json:"license"`
	SourceURL string `json:"source_url,omitempty"`
}

func referenceLicenseToAPI(rl hive.ReferenceLicense) apiReferenceLicense {
	out := apiReferenceLicense{
		ReferenceID: rl.ReferenceID,
		License:     rl.License,
		SourceURL:   rl.SourceURL,
		VerifiedBy:  rl.VerifiedBy,
	}
	if !rl.VerifiedAt.IsZero() {
		out.VerifiedAt = rl.VerifiedAt.Format(time.RFC3339Nano)
	}
	if !rl.UpdatedAt.IsZero() {
		out.UpdatedAt = rl.UpdatedAt.Format(time.RFC3339Nano)
	}
	return out
}

// handleGetReferenceLicense returns the license row for a
// reference. Missing rows return a synthetic "unknown" row so
// clients don't have to distinguish "no row yet" from "explicitly
// unknown" when rendering.
//
// @Summary      Get the verified license of a reference
// @Tags         references
// @Produce      json
// @Param        id path string true "Reference ID"
// @Success      200 {object} apiReferenceLicense
// @Router       /reference/{id}/license [get]
func (s *server) handleGetReferenceLicense(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("id")
	rl, err := s.a.GetReferenceLicense(r.Context(), refID)
	if err != nil {
		if errors.Is(err, hive.ErrNotFound) {
			writeJSON(w, http.StatusOK, apiReferenceLicense{
				ReferenceID: refID,
				License:     hive.LicenseUnknown,
			})
			return
		}
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, referenceLicenseToAPI(*rl))
}

// handleSetReferenceLicense upserts the license for a reference.
//
// @Summary      Record the verified license of a reference
// @Tags         references
// @Accept       json
// @Produce      json
// @Param        id   path string             true "Reference ID"
// @Param        body body apiSetLicenseReq   true "License payload"
// @Success      200 {object} apiReferenceLicense
// @Failure      400 {object} problem
// @Router       /reference/{id}/license [put]
func (s *server) handleSetReferenceLicense(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	refID := r.PathValue("id")
	var body apiSetLicenseReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.License) == "" {
		writeBadRequest(w, r, "license is required")
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		return tx.SetReferenceLicense(hive.ReferenceLicense{
			ReferenceID: refID,
			License:     body.License,
			SourceURL:   body.SourceURL,
		})
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	fresh, err := s.a.GetReferenceLicense(r.Context(), refID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, referenceLicenseToAPI(*fresh))
}

// extractJATSMeta pulls DOI + basic bibliographic metadata out
// of a JATS XML file for the reference-upload path. GROBID
// handles PDFs; JATS carries the same fields natively and
// doesn't need a converter. Returns empty DOI + nil metadata
// when the file can't be parsed (malformed XML, non-JATS shape,
// etc.) — callers treat it as "no metadata extracted" without
// failing the upload.
//
// Deliberately lightweight — reads the whole file (JATS papers
// are usually 100-500 KB; the 100 MB upload cap enforces the
// worst case) and walks the DOM to pull four fields. Not a
// general JATS parser; hive has richer parsers in
// pkg/refmanager/jatsconv for the ingestion pipeline itself.
func extractJATSMeta(path string) (string, *apiUploadBibHeader) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var (
		doi           string
		title         string
		firstAuthor   string
		year          string
		inTitleGroup  bool
		inArticleMeta bool
		inSurname     bool
		inGivenNames  bool
		inYear        bool
		wantAuthor    bool
		curSurname    string
		curGiven      string
		captureID     bool
	)
	for {
		tok, err := dec.Token()
		if tok == nil || errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Malformed XML — return whatever we have.
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "article-meta", "book-meta":
				inArticleMeta = true
			case "title-group":
				if inArticleMeta {
					inTitleGroup = true
				}
			case "article-id":
				pubType := ""
				for _, a := range t.Attr {
					if a.Name.Local == "pub-id-type" {
						pubType = a.Value
					}
				}
				if pubType == "doi" {
					captureID = true
				}
			case "article-title":
				if inTitleGroup && title == "" {
					captureID = false
					title = ""
					// use a small helper collector
				}
			case "contrib":
				if firstAuthor == "" {
					wantAuthor = true
				}
			case "surname":
				if wantAuthor {
					inSurname = true
				}
			case "given-names":
				if wantAuthor {
					inGivenNames = true
				}
			case "year":
				if year == "" {
					inYear = true
				}
			}
			// Grab title inline — treated as chardata below via the
			// captureTitle state.
			if t.Name.Local == "article-title" && inTitleGroup {
				captureTitleStart(&title, dec)
				inTitleGroup = false
			}
		case xml.CharData:
			if captureID {
				doi = strings.TrimSpace(string(t))
				captureID = false
			}
			if inSurname {
				curSurname = strings.TrimSpace(string(t))
			}
			if inGivenNames {
				curGiven = strings.TrimSpace(string(t))
			}
			if inYear {
				year = strings.TrimSpace(string(t))
				inYear = false
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "article-meta", "book-meta":
				inArticleMeta = false
			case "title-group":
				inTitleGroup = false
			case "surname":
				inSurname = false
			case "given-names":
				inGivenNames = false
			case "contrib":
				if wantAuthor && curSurname != "" {
					if curGiven != "" {
						firstAuthor = curSurname + ", " + curGiven
					} else {
						firstAuthor = curSurname
					}
					wantAuthor = false
					curSurname = ""
					curGiven = ""
				}
			}
		}
		if doi != "" && title != "" && firstAuthor != "" && year != "" {
			break
		}
	}
	if doi == "" && title == "" && firstAuthor == "" && year == "" {
		return "", nil
	}
	return doi, &apiUploadBibHeader{
		Title:  title,
		Author: firstAuthor,
		Issued: year,
	}
}

// extractEPUBMeta pulls DOI + basic bibliographic metadata out
// of an EPUB file for the reference-upload path. EPUBs are zip
// archives whose META-INF/container.xml points at a content.opf
// carrying standard Dublin Core metadata (title, creator, date,
// identifier, publisher). Returns empty DOI + nil metadata when
// the archive is malformed or missing the OPF — callers treat
// it as "no metadata extracted" without failing the upload.
//
// EPUB identifiers may carry a DOI in several conventions:
//   - "doi:10.1234/xyz"
//   - "urn:doi:10.1234/xyz"
//   - a plain "10.1234/xyz" when scheme metadata says DOI
// Any of those get normalized to the bare DOI.
func extractEPUBMeta(path string) (string, *apiUploadBibHeader) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", nil
	}
	defer zr.Close()

	// Step 1: read META-INF/container.xml to find the OPF path.
	// EPUB spec fixes this location, so any archive that doesn't
	// have it is not a valid EPUB — treat as no metadata.
	var opfPath string
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", nil
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", nil
			}
			// The rootfile is inside <rootfiles><rootfile full-path="..." ... />
			dec := xml.NewDecoder(bytes.NewReader(data))
			for {
				tok, err := dec.Token()
				if tok == nil || err != nil {
					break
				}
				if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "rootfile" {
					for _, a := range se.Attr {
						if a.Name.Local == "full-path" {
							opfPath = a.Value
							break
						}
					}
					break
				}
			}
			break
		}
	}
	if opfPath == "" {
		return "", nil
	}

	// Step 2: read the OPF file and pull Dublin Core metadata.
	var opfBytes []byte
	for _, f := range zr.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return "", nil
			}
			opfBytes, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", nil
			}
			break
		}
	}
	if len(opfBytes) == 0 {
		return "", nil
	}

	// Walk the OPF's metadata block. Match by local name so
	// namespace-prefix variation ("dc:" vs default namespace) both
	// work. Not a full OPF parser; four fields is plenty here.
	dec := xml.NewDecoder(bytes.NewReader(opfBytes))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var (
		doi         string
		title       string
		firstAuthor string
		year        string
		publisher   string
	)
	// Track the scheme attribute on the current <dc:identifier> so
	// we can pick a DOI even when the value has no doi: prefix.
	var currentIDScheme string
	for {
		tok, err := dec.Token()
		if tok == nil || err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "identifier":
			currentIDScheme = ""
			for _, a := range se.Attr {
				if a.Name.Local == "scheme" {
					currentIDScheme = strings.ToLower(a.Value)
				}
			}
			text := readCharData(dec, se.Name.Local)
			if doi == "" {
				doi = normalizeEPUBDOI(text, currentIDScheme)
			}
		case "title":
			text := readCharData(dec, se.Name.Local)
			if title == "" {
				title = strings.TrimSpace(text)
			}
		case "creator":
			text := readCharData(dec, se.Name.Local)
			if firstAuthor == "" {
				firstAuthor = strings.TrimSpace(text)
			}
		case "date":
			text := readCharData(dec, se.Name.Local)
			if year == "" {
				// dc:date can be "2026", "2026-09", "2026-09-16",
				// or a longer ISO 8601 form. Take the leading
				// 4-digit year.
				t := strings.TrimSpace(text)
				if len(t) >= 4 {
					year = t[:4]
				}
			}
		case "publisher":
			text := readCharData(dec, se.Name.Local)
			if publisher == "" {
				publisher = strings.TrimSpace(text)
			}
		}
	}
	if doi == "" && title == "" && firstAuthor == "" && year == "" && publisher == "" {
		return "", nil
	}
	return doi, &apiUploadBibHeader{
		Title:     title,
		Author:    firstAuthor,
		Issued:    year,
		Publisher: publisher,
	}
}

// readCharData collects character data up to the closing tag of
// the given element local name and returns it as a string. Used
// by extractEPUBMeta to consume <dc:title>text</dc:title> without
// pulling the full DOM.
func readCharData(dec *xml.Decoder, localName string) string {
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if tok == nil || err != nil {
			return b.String()
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name.Local == localName {
				return b.String()
			}
		}
	}
}

// normalizeEPUBDOI extracts a DOI from a dc:identifier value.
// EPUBs write DOIs in several conventions; peel them all back to
// the bare "10.NNNN/rest" form. Returns "" when the value is not
// a DOI.
func normalizeEPUBDOI(value, scheme string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(lower, "urn:doi:"):
		v = v[len("urn:doi:"):]
	case strings.HasPrefix(lower, "doi:"):
		v = v[len("doi:"):]
	case strings.HasPrefix(lower, "https://doi.org/"):
		v = v[len("https://doi.org/"):]
	case strings.HasPrefix(lower, "http://doi.org/"):
		v = v[len("http://doi.org/"):]
	case scheme == "doi":
		// Bare value + explicit scheme — trust it.
	default:
		// No DOI marker — accept only if it looks like a DOI.
		if !strings.HasPrefix(v, "10.") || !strings.Contains(v, "/") {
			return ""
		}
	}
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "10.") && strings.Contains(v, "/") {
		return v
	}
	return ""
}

// captureTitleStart reads text content up to the closing
// </article-title> and writes it into *dst. Skips inline
// markup elements (italic, bold, xref, etc.) — only their
// character data enters the title string.
func captureTitleStart(dst *string, dec *xml.Decoder) {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if tok == nil || err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			b.Write(t)
		}
	}
	*dst = strings.Join(strings.Fields(b.String()), " ")
}

// matchedByDOI returns compact reference summaries for every row
// whose col__doi matches the given DOI, excluding entries that
// are already in the same-sha `attached` list (they would appear
// there anyway).
func (s *server) matchedByDOI(ctx context.Context, doi string, attached []apiAttachedReference) []apiAttachedReference {
	if strings.TrimSpace(doi) == "" {
		return nil
	}
	refs, err := s.a.FindReferencesByDOI(ctx, doi)
	if err != nil || len(refs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(attached))
	for _, a := range attached {
		seen[a.ID] = struct{}{}
	}
	out := make([]apiAttachedReference, 0, len(refs))
	for _, r := range refs {
		if _, dup := seen[r.ID]; dup {
			continue
		}
		out = append(out, apiAttachedReference{
			ID:       r.ID,
			Citation: r.Citation,
			Title:    r.Title,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// attachedReferencesForSHA returns compact summaries of every
// reference row currently linked to the given sha via
// hive__reference_pdf. Empty slice when nothing points at the sha
// (fresh upload, orphan sidecar from a prior detach, etc.).
func (s *server) attachedReferencesForSHA(ctx context.Context, sha string) []apiAttachedReference {
	rows, err := s.a.DB().QueryContext(ctx,
		`SELECT r.col__id,
		        COALESCE(r.col__citation, ''),
		        COALESCE(r.col__title, '')
		 FROM hive__reference_pdf p
		 JOIN reference r ON r.col__id = p.reference_id
		 WHERE p.sha512 = ?
		 ORDER BY p.attached_at`,
		sha,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []apiAttachedReference
	for rows.Next() {
		var item apiAttachedReference
		if err := rows.Scan(&item.ID, &item.Citation, &item.Title); err != nil {
			continue
		}
		out = append(out, item)
	}
	return out
}

// tryAnnotate converts the just-generated (or previously-cached)
// GROBID TEI into JATS via pkg/jatsconv, runs the GNfinder +
// GNverifier annotation pass over that JATS, and stores the
// resulting TaxPub-augmented JATS at <sha>.jats.xml. Runs
// synchronously after tryGrobidConvert; the GROBID pass upstream
// dominates the latency budget, and having the augmented JATS on
// disk unblocks every downstream reader / feedback / corpus-query
// path without on-demand work.
//
// Skips when there's no TEI (non-PDF upload, GROBID unavailable),
// dedups when the augmented JATS already exists on disk. Errors
// don't propagate to the upload response — the raw source and TEI
// are still stored, and the curator can retry the annotation pass
// separately.
// apiReannotateResult is the JSON response for
// POST /api/reference/library/{sha}/reannotate.
type apiReannotateResult struct {
	SHA512              string `json:"sha512"`
	Status              string `json:"status"`
	AnnotationNameCount int    `json:"annotation_name_count,omitempty"`
	Error               string `json:"error,omitempty"`
}

// handleReferenceLibraryReannotate re-runs the TEI → JATS +
// GNfinder annotation pass for an existing sidecar sha. Fixes
// the case where the initial upload's annotation step failed
// transiently (container pressure, brief timeout) and left the
// sha with a TEI but no augmented JATS — the user sees a red
// "no augmented XML" banner in the reader.
//
// Requires the sha's .tei.xml to already exist on disk (from
// the initial GROBID pass). Returns an error otherwise —
// callers can re-ingest the source PDF to reproduce the TEI.
//
// @Summary      Re-run TEI → JATS annotation for a sidecar sha
// @Tags         references
// @Produce      json
// @Param        sha path string true "sha512 of the sidecar attachment"
// @Success      200 {object} apiReannotateResult
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/library/{sha}/reannotate [post]
func (s *server) handleReferenceLibraryReannotate(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	sha := r.PathValue("sha")
	if sha == "" {
		writeBadRequest(w, r, "sha required")
		return
	}
	if s.annotator == nil {
		writeJSON(w, http.StatusOK, apiReannotateResult{
			SHA512: sha, Status: "annotator_unconfigured",
		})
		return
	}
	// Two input paths: PDF (via TEI) vs. raw JATS/XML upload.
	// Publisher JATS is often higher-quality than GROBID output
	// and deserves the same GNfinder annotation pass as
	// PDF-derived JATS. Check the sidecar meta.json to learn
	// which one we're dealing with.
	var jats []byte
	var jatsOpts jatsconv.Options
	meta, _ := sidecar.ReadMeta(s.archivePath, sha)
	primaryExt := ""
	if meta != nil {
		primaryExt = meta.Ext
	}
	switch primaryExt {
	case "xml", "jats":
		raw, err := sidecar.Read(s.archivePath, sha, primaryExt)
		if err != nil || raw == nil {
			writeJSON(w, http.StatusOK, apiReannotateResult{
				SHA512: sha, Status: "no_input",
				Error: "sidecar has no " + primaryExt + " content",
			})
			return
		}
		jats = raw
	case "epub":
		if s.pandoc == nil {
			writeJSON(w, http.StatusOK, apiReannotateResult{
				SHA512: sha, Status: "pandoc_unconfigured",
				Error: "pandoc is not installed — install it in the hive container to enable EPUB → JATS conversion",
			})
			return
		}
		epubPath := sidecar.Path(s.archivePath, sha, "epub")
		if _, statErr := os.Stat(epubPath); statErr != nil {
			writeJSON(w, http.StatusOK, apiReannotateResult{
				SHA512: sha, Status: "no_input",
				Error: "sidecar has no epub content",
			})
			return
		}
		out, err := s.pandoc.EpubToJATS(r.Context(), epubPath)
		if err != nil {
			writeJSON(w, http.StatusOK, apiReannotateResult{
				SHA512: sha, Status: "failed",
				Error: "pandoc EPUB→JATS: " + err.Error(),
			})
			return
		}
		jats = out
	default:
		// PDF (or unknown ext falling back to the TEI path).
		tei, err := sidecar.Read(s.archivePath, sha, "tei.xml")
		if err != nil || tei == nil {
			writeJSON(w, http.StatusOK, apiReannotateResult{
				SHA512: sha, Status: "no_tei",
				Error: "sidecar has no tei.xml — re-ingest the source PDF to produce it",
			})
			return
		}
		pdfPath := sidecar.Path(s.archivePath, sha, "pdf")
		if _, statErr := os.Stat(pdfPath); statErr == nil {
			if words, err := pdftext.WordPages(pdfPath); err == nil && len(words) > 0 {
				jatsOpts.WordPages = words
				if journalPage1 := pdftext.JournalPageOfPDFPage1(words); journalPage1 > 1 {
					jatsOpts.PageOffset = journalPage1 - 1
				}
			}
		}
		jats, err = jatsconv.TEIToJATSOpts(tei, jatsOpts)
		if err != nil {
			writeJSON(w, http.StatusOK, apiReannotateResult{
				SHA512: sha, Status: "failed", Error: "TEI→JATS: " + err.Error(),
			})
			return
		}
	}
	augmented, err := s.annotator.Annotate(r.Context(), jats)
	if err != nil {
		// The page-marker splicing bug that motivated the earlier
		// retry-without-page-markers fallback was fixed at the
		// source in pkg/refmanager/jatsconv. Any remaining errors
		// here are real (malformed publisher JATS, GNfinder
		// upstream issues) and worth surfacing directly.
		writeJSON(w, http.StatusOK, apiReannotateResult{
			SHA512: sha, Status: "failed",
			Error: "annotate: " + err.Error(),
		})
		return
	}
	augmented = s.reapplyCorrectionsForSHA(r.Context(), sha, augmented)
	if err := sidecar.Store(s.archivePath, sha, "jats.xml", augmented); err != nil {
		writeJSON(w, http.StatusOK, apiReannotateResult{
			SHA512: sha, Status: "failed", Error: "store: " + err.Error(),
		})
		return
	}
	s.tryExtractFigures(sha, augmented)
	writeJSON(w, http.StatusOK, apiReannotateResult{
		SHA512:              sha,
		Status:              "annotated",
		AnnotationNameCount: countTaxonNameElements(augmented),
	})
}

func (s *server) tryAnnotate(ctx context.Context, res *sidecar.IngestResult, teiStatus string) (string, int) {
	if s.annotator == nil {
		return "skipped", 0
	}
	if existing, err := sidecar.Read(s.archivePath, res.SHA512, "jats.xml"); err == nil && existing != nil {
		return "already_exists", countTaxonNameElements(existing)
	}
	// Publisher JATS / raw XML uploads bypass GROBID + TEI entirely
	// but still deserve GNfinder annotation — running GNfinder over
	// authoritative publisher structure often produces better
	// taxonomic markup than the PDF-derived path. Detect and
	// annotate directly.
	if res.Ext == "xml" || res.Ext == "jats" {
		raw, err := sidecar.Read(s.archivePath, res.SHA512, res.Ext)
		if err != nil || raw == nil {
			slog.Warn("annotate: raw JATS input unreadable",
				"sha", res.SHA512[:12], "ext", res.Ext, "err", err)
			return "failed", 0
		}
		augmented, err := s.annotator.Annotate(ctx, raw)
		if err != nil {
			slog.Warn("annotate: GNfinder pass on raw JATS failed",
				"sha", res.SHA512[:12], "err", err)
			return "failed", 0
		}
		augmented = s.reapplyCorrectionsForSHA(ctx, res.SHA512, augmented)
		if err := sidecar.Store(s.archivePath, res.SHA512, "jats.xml", augmented); err != nil {
			slog.Warn("annotate: augmented JATS store failed",
				"sha", res.SHA512[:12], "err", err)
			return "failed", 0
		}
		return "annotated", countTaxonNameElements(augmented)
	}
	// EPUB path: pandoc converts the archive to JATS, then the
	// same annotator pass runs on the result. Requires the pandoc
	// adapter to be initialized at startup — when it is nil, EPUB
	// stays at metadata-only and the reader shows an "annotation
	// unavailable" state for the EPUB sha rather than a hard error.
	if res.Ext == "epub" {
		if s.pandoc == nil {
			return "skipped", 0
		}
		epubPath := sidecar.Path(s.archivePath, res.SHA512, "epub")
		if _, statErr := os.Stat(epubPath); statErr != nil {
			slog.Warn("annotate: EPUB sidecar missing",
				"sha", res.SHA512[:12], "err", statErr)
			return "failed", 0
		}
		jats, err := s.pandoc.EpubToJATS(ctx, epubPath)
		if err != nil {
			slog.Warn("annotate: pandoc EPUB→JATS failed",
				"sha", res.SHA512[:12], "err", err)
			return "failed", 0
		}
		augmented, err := s.annotator.Annotate(ctx, jats)
		if err != nil {
			slog.Warn("annotate: GNfinder pass on pandoc JATS failed",
				"sha", res.SHA512[:12], "err", err)
			return "failed", 0
		}
		augmented = s.reapplyCorrectionsForSHA(ctx, res.SHA512, augmented)
		if err := sidecar.Store(s.archivePath, res.SHA512, "jats.xml", augmented); err != nil {
			slog.Warn("annotate: augmented JATS store failed",
				"sha", res.SHA512[:12], "err", err)
			return "failed", 0
		}
		return "annotated", countTaxonNameElements(augmented)
	}
	if teiStatus != "stored" {
		return "skipped", 0
	}
	tei, err := sidecar.Read(s.archivePath, res.SHA512, "tei.xml")
	if err != nil || tei == nil {
		return "failed", 0
	}
	// Extract per-word (word, page) tuples from the source PDF so
	// jatsconv can splice page-break markers at the exact word
	// boundary where the source transitions from one page to the
	// next — enables citation-accurate page annotations for
	// nomenclatural acts (ZooBank, ICZN).
	//
	// Also extract the journal-labeled page number of PDF page 1
	// (e.g., "83" from a "83-93." page range in the running
	// header) so markers show journal pages, not PDF-index pages.
	//
	// Silent fall-through when pdftotext isn't installed or the
	// PDF isn't available — markers just don't appear in that case.
	var jatsOpts jatsconv.Options
	pdfPath := sidecar.Path(s.archivePath, res.SHA512, "pdf")
	if _, statErr := os.Stat(pdfPath); statErr == nil {
		if words, err := pdftext.WordPages(pdfPath); err == nil && len(words) > 0 {
			jatsOpts.WordPages = words
			if journalPage1 := pdftext.JournalPageOfPDFPage1(words); journalPage1 > 1 {
				jatsOpts.PageOffset = journalPage1 - 1
			}
		}
	}
	jats, err := jatsconv.TEIToJATSOpts(tei, jatsOpts)
	if err != nil {
		slog.Warn("annotate: TEI→JATS conversion failed",
			"sha", res.SHA512[:12], "err", err)
		return "failed", 0
	}
	augmented, err := s.annotator.Annotate(ctx, jats)
	if err != nil {
		slog.Warn("annotate: GNfinder pass failed",
			"sha", res.SHA512[:12], "err", err)
		return "failed", 0
	}
	// Re-apply active bbox-add corrections attached to this sha
	// before writing the sidecar. This preserves curator additions
	// (missed figures/tables drawn via the Digitize-tab bbox tool)
	// across a re-import + re-annotate cycle — otherwise the fresh
	// GROBID JATS overwrites the curator work with a clean slate.
	augmented = s.reapplyCorrectionsForSHA(ctx, res.SHA512, augmented)
	if err := sidecar.Store(s.archivePath, res.SHA512, "jats.xml", augmented); err != nil {
		slog.Warn("annotate: augmented JATS store failed",
			"sha", res.SHA512[:12], "err", err)
		return "failed", 0
	}
	// Extract figure/table image crops opportunistically. Failures
	// don't affect the annotation status — the JATS is still valid
	// without the images, curator just won't have visual crops in
	// the reader modal for figures/tables. Silent on missing
	// pdftocairo (Docker ships it; local Go-native runs skip).
	s.tryExtractFigures(res.SHA512, augmented)
	return "annotated", countTaxonNameElements(augmented)
}

// reapplyCorrectionsForSHA finds every active bbox-add correction
// attached to the given sidecar sha (across all references that
// might share that sha) and re-splices each into the freshly
// generated JATS. Called from tryAnnotate so curator-added
// figures/tables survive a re-import + re-annotate cycle.
//
// Non-fatal: any correction that fails to apply is skipped
// silently, matching the tryAnnotate best-effort tolerance model.
// A more visible failure path lands with slice 6 (re-digitize
// with per-correction status reporting).
func (s *server) reapplyCorrectionsForSHA(ctx context.Context, sha string, jats []byte) []byte {
	rows, err := s.a.DB().QueryContext(ctx,
		`SELECT DISTINCT correction_id
		 FROM hive__extraction_correction
		 WHERE sidecar_sha = ? AND status = 'active'
		 ORDER BY created_at`,
		sha,
	)
	if err != nil {
		return jats
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		c, err := s.a.GetCorrection(ctx, id)
		if err != nil || c == nil {
			continue
		}
		// Defensive filter: bbox-* kinds require figure/table
		// content type. Skip rows with mismatched content_type.
		if correction.IsBBoxKind(c.Kind) &&
			c.ContentType != "figure" && c.ContentType != "table" {
			continue
		}
		mutated, err := s.a.ApplyCorrection(jats, c)
		if err != nil {
			continue
		}
		jats = mutated
	}
	return jats
}

// tryExtractFigures renders each fig/table-wrap with usable coord
// attributes to a PNG sidecar under <sha>.figures/<id>.png.
// Non-fatal — same tolerance model as tryAnnotate. Runs
// synchronously after annotation; the pdftocairo call is fast
// (~200ms per figure on the typical scientific PDF).
func (s *server) tryExtractFigures(sha string, jats []byte) {
	pdfPath := filepath.Join(sidecar.Dir(s.archivePath), sha+".pdf")
	outDir := filepath.Join(sidecar.Dir(s.archivePath), sha+".figures")
	_, _ = figextract.Extract(jats, pdfPath, outDir)
}

// countTaxonNameElements returns the number of <tp:taxon-name>
// StartElement tokens in the augmented XML. Cheap — a single-pass
// token scan, no full DOM. Reported to the WUI so the upload
// response can hint at how much augmentation happened without the
// WUI having to fetch and parse the augmented XML.
func countTaxonNameElements(augmented []byte) int {
	dec := xml.NewDecoder(bytes.NewReader(augmented))
	n := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return n
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "taxon-name" {
			n++
		}
	}
}

// tryGrobidConvert runs the freshly-uploaded PDF through GROBID
// and stores the TEI response alongside the source. Returns the
// TEIStatus value that goes on the upload response — one of
// "stored" / "not_configured" / "unavailable" / "skipped" (see
// apiReferenceUpload for meaning) plus the first DOI extracted
// from the TEI (empty when GROBID didn't run or no DOI appears).
// Non-PDF formats are "skipped" for now (their conversion paths
// aren't wired).
//
// Deliberately doesn't propagate errors up — the upload itself
// succeeded, the TEI pass is a best-effort follow-on. Errors are
// captured in the status string and surface to the WUI.
func (s *server) tryGrobidConvert(ctx context.Context, res *sidecar.IngestResult, ext string) (string, string, *apiUploadBibHeader) {
	if ext != "pdf" {
		return "skipped", "", nil
	}
	if s.grobid == nil {
		return "not_configured", "", nil
	}
	// If TEI already exists on disk (dedup case + prior conversion),
	// don't re-run — deterministic pipeline, same input same output.
	if existing, err := sidecar.Read(s.archivePath, res.SHA512, "tei.xml"); err == nil && existing != nil {
		return "stored", grobid.ExtractDOI(existing), parseHeaderToAPI(existing)
	}
	pdf, err := os.Open(res.Path)
	if err != nil {
		return "unavailable", "", nil
	}
	tei, err := s.grobid.ProcessFulltextDocument(ctx, pdf, res.SHA512+".pdf")
	pdf.Close()
	if err != nil {
		return "unavailable", "", nil
	}
	// Image-only scans yield a TEI with an empty body. When the
	// OCR service is configured, retry: ocrmypdf the PDF, store
	// the OCR'd variant as a sidecar artifact, and re-run GROBID
	// on it. Skips the retry when OCR isn't wired up or when the
	// body already has substantive text.
	if s.ocr != nil && teiBodyIsEmpty(tei) {
		if ocrTEI, ok := s.retryWithOCR(ctx, res); ok {
			tei = ocrTEI
		}
	}
	if err := sidecar.Store(s.archivePath, res.SHA512, "tei.xml", tei); err != nil {
		return "unavailable", "", nil
	}
	return "stored", grobid.ExtractDOI(tei), parseHeaderToAPI(tei)
}

// retryWithOCR runs the raw PDF through ocrmypdf, stores the
// text-layered result as a sidecar artifact (<sha>.ocr.pdf), and
// re-runs GROBID on it. Returns the TEI and true on full success;
// on any failure returns (nil, false) so the caller falls back to
// the empty-body TEI.
func (s *server) retryWithOCR(ctx context.Context, res *sidecar.IngestResult) ([]byte, bool) {
	pdf, err := os.Open(res.Path)
	if err != nil {
		return nil, false
	}
	defer pdf.Close()
	ocrPDF, err := s.ocr.ProcessPDF(ctx, pdf, res.SHA512+".pdf")
	if err != nil {
		return nil, false
	}
	if err := sidecar.Store(s.archivePath, res.SHA512, "ocr.pdf", ocrPDF); err != nil {
		return nil, false
	}
	tei, err := s.grobid.ProcessFulltextDocument(ctx, bytes.NewReader(ocrPDF), res.SHA512+".ocr.pdf")
	if err != nil {
		return nil, false
	}
	return tei, true
}

// teiBodyIsEmpty reports whether GROBID's TEI body content is
// too sparse to be a real paper — the "image-only PDF" signal.
// Uses a 200-character threshold: even the shortest genuine
// letters-to-the-editor beat it, and empty-body output typically
// contains <100 chars of boilerplate. Cheap: streams the body
// text via an xml.Decoder rather than parsing the whole document.
func teiBodyIsEmpty(tei []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(tei))
	inBody := false
	total := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "body" {
				inBody = true
			}
		case xml.EndElement:
			if t.Name.Local == "body" {
				return total < 200
			}
		case xml.CharData:
			if inBody {
				total += len(bytes.TrimSpace(t))
				if total >= 200 {
					return false
				}
			}
		}
	}
	return total < 200
}

// parseHeaderToAPI runs grobid.ParseHeader over the TEI bytes and
// projects the result onto the wire type. Returns nil when parsing
// fails or the extracted header is entirely empty — the WUI treats
// a nil metadata field as "GROBID didn't get useful data" and
// stays on the file tab rather than hopping to Manual with a blank
// form. TEI errors are swallowed here (best-effort — the upload
// still succeeded); the tei_status field already indicates whether
// GROBID ran at all.
func parseHeaderToAPI(tei []byte) *apiUploadBibHeader {
	h, err := grobid.ParseHeader(tei)
	if err != nil || h == nil {
		return nil
	}
	// Skip entirely-empty headers so the WUI doesn't hop to
	// Manual with nothing to review.
	if h.Title == "" && h.Authors == "" && h.DOI == "" &&
		h.ContainerTitle == "" && h.Issued == "" &&
		h.Volume == "" && h.Issue == "" && h.Page == "" &&
		h.Publisher == "" {
		return nil
	}
	return &apiUploadBibHeader{
		Title:          h.Title,
		Author:         h.Authors,
		Issued:         h.Issued,
		ContainerTitle: h.ContainerTitle,
		Volume:         h.Volume,
		Issue:          h.Issue,
		Page:           h.Page,
		Publisher:      h.Publisher,
	}
}

// handleReferenceLibrarySource streams the sidecar source file
// (PDF / EPUB / HTML / JATS) so the WUI's iframe-based PDF viewer
// can render it side-by-side with the reference-edit form. The
// sha comes from the path; the extension is read from the
// sidecar's meta.json so the URL doesn't need to carry it and the
// same URL works for every format.
//
// Content-Type is set per extension so browsers render PDFs inline
// (application/pdf) rather than triggering a download. Unknown
// extensions fall back to a generic type — safer than guessing
// wrong and mis-labeling a JATS XML as HTML.
//
// @Summary      Stream sidecar source file
// @Description  Stream the raw uploaded source (PDF / EPUB / HTML / JATS) with the correct Content-Type for inline browser rendering.
// @Tags         references
// @Produce      octet-stream
// @Param        sha path string true "Sidecar SHA-512 hex"
// @Success      200 "Raw file bytes"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/library/{sha}/source [get]
func (s *server) handleReferenceLibrarySource(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	if sha == "" {
		writeBadRequest(w, r, "sha required")
		return
	}
	meta, err := sidecar.ReadMeta(s.archivePath, sha)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if meta == nil {
		http.NotFound(w, r)
		return
	}
	path := sidecar.Path(s.archivePath, sha, meta.Ext)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeProblem(w, r, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Explicit Content-Type keeps browsers from sniffing and
	// (worse) offering a download for a PDF. Extension → MIME
	// mapping matches what's accepted at upload time.
	switch strings.ToLower(meta.Ext) {
	case "pdf":
		w.Header().Set("Content-Type", "application/pdf")
	case "epub":
		w.Header().Set("Content-Type", "application/epub+zip")
	case "html", "htm":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case "xml", "jats":
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	// Content-Disposition inline so the browser renders instead of
	// downloading. filename uses the sha so a curator hitting
	// "download" gets a stable name.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`inline; filename="%s.%s"`, sha, meta.Ext))
	http.ServeContent(w, r, info.Name(), meta.UploadedAt, f)
}

// handleReferenceLibraryTaxpub streams the TaxPub-augmented JATS
// produced by the annotator (GNfinder + GNverifier) and stored
// alongside the raw source at <sha>.jats.xml. Consumers: the
// reader UI, corpus-scale analysis, curator eyeballing via curl.
// Returns 404 when no augmented XML exists (upload happened but
// annotation was skipped or failed).
//
// @Summary      Stream TaxPub-augmented JATS
// @Description  Return the sidecar's TaxPub-augmented JATS (with GNfinder + GNverifier taxon-name markup).
// @Tags         references
// @Produce      xml
// @Param        sha path string true "Sidecar SHA-512 hex"
// @Success      200 "JATS XML body"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/library/{sha}/taxpub [get]
func (s *server) handleReferenceLibraryTaxpub(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	if sha == "" {
		writeBadRequest(w, r, "sha required")
		return
	}
	data, err := sidecar.Read(s.archivePath, sha, "jats.xml")
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write(data)
}

// handleReferenceLibraryFigure streams a per-figure PNG crop
// rendered by figextract from the source PDF via GROBID's coord
// attributes. Path is /api/reference/library/{sha}/figures/{id}
// where {id} matches a JATS <fig>/<table-wrap>'s id attribute
// (fig_0, tab_1, etc.). Reader modal fetches these on demand
// when the curator clicks a figure or table.
//
// Returns 404 when the crop isn't on disk — either coord metadata
// was missing so figextract skipped that fig, or the extraction
// hasn't run yet. Client should degrade gracefully (show caption
// only, offer "Open in PDF" as fallback).
//
// @Summary      Stream a figure/table PNG crop
// @Description  PNG crop of a fig or table-wrap rendered from the source PDF via GROBID coordinates.
// @Tags         references
// @Produce      png
// @Param        sha path string true "Sidecar SHA-512 hex"
// @Param        id path string true "JATS fig/table id (e.g. fig_0, tab_1)"
// @Success      200 "PNG bytes"
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      500 {object} problem
// @Router       /reference/library/{sha}/figures/{id} [get]
func (s *server) handleReferenceLibraryFigure(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	id := r.PathValue("id")
	if sha == "" || id == "" {
		writeBadRequest(w, r, "sha and id required")
		return
	}
	// Path traversal guard: id comes straight from the URL, so any
	// "../" or absolute path would escape the figures dir. Restrict
	// to alphanumerics + underscore, which covers every id
	// jatsconv emits (fig_0, tab_1, ...).
	if !safeIDForFilename(id) {
		writeBadRequest(w, r, "invalid figure id")
		return
	}
	path := filepath.Join(sidecar.Dir(s.archivePath), sha+".figures", id+".png")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeProblem(w, r, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// safeIDForFilename accepts only the character set jatsconv emits
// for fig/table ids: ASCII letters, digits, underscore, hyphen.
// Rejects anything else so a URL-path fig id can't traverse into
// a parent directory or reach unintended files.
func safeIDForFilename(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// handleParseName runs gnparser on a verbatim scientific name and returns
// an apiName-shaped preview with atomized col__ fields + gn__* cache + a
// code-scoped rank guess. Nothing is written — the response's `id` is
// empty and `modified` is unset. Drives the two-step name-add form: the
// WUI/TUI collect the verbatim + code from the curator, POST here, then
// show the returned atomized breakdown as an editable form. On confirm
// the form POSTs to /api/taxon (accepted) or the synonym write path.
//
// Body:  {"scientific_name": "...", "code": "ZOOLOGICAL"}
// Empty scientific_name yields 400; code is optional (unset code skips
// the suffix-rule tier of RankGuess).
//
// @Summary      Parse a scientific name
// @Description  Run gnparser on a verbatim name and return an apiName-shaped preview with atomized col__ fields + gn__* cache + a code-scoped rank guess. Nothing is written.
// @Tags         names
// @Accept       json
// @Produce      json
// @Param        body body object true "Parse request {scientific_name, code}"
// @Success      200 {object} apiName
// @Failure      400 {object} problem
// @Router       /name/parse [post]
func (s *server) handleParseName(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ScientificName string `json:"scientific_name"`
		Code           string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.ScientificName) == "" {
		writeBadRequest(w, r, "scientific_name is required")
		return
	}
	preview, tail := s.a.ParseNamePreview(body.Code, body.ScientificName)
	resp := nameToAPI(preview)
	// Tail is preview-only diagnostic data — never persisted on the name
	// row, only surfaced through this endpoint so the create/edit form
	// can render the "unparsed tail" banner. Empty when the parse was
	// clean.
	resp.Tail = tail
	writeJSON(w, http.StatusOK, resp)
}

// handleNameSearch returns names whose canonical/scientific-name form
// matches q as a case-insensitive substring.
//
// @Summary      Search names
// @Description  Case-insensitive substring search over scientific-name forms.
// @Tags         names
// @Produce      json
// @Param        q query string true "Query string"
// @Param        limit query int false "Page size"
// @Success      200 {object} apiPage[apiNameHit]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /name/search [get]
func (s *server) handleNameSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeBadRequest(w, r, "query parameter 'q' is required")
		return
	}
	limit := defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeBadRequest(w, r, "invalid 'limit' value")
			return
		}
		limit = clampPageSize(n)
	}
	hits, err := s.a.SearchNames(r.Context(), q, limit)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiNameHit, 0, len(hits))
	for _, h := range hits {
		items = append(items, nameHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiNameHit]{Items: items})
}

// parseLimitOffset decodes the ?limit= and ?cursor= query params. Missing
// values fall back to the defaults; malformed values yield an error the
// caller reports as 400.
func parseLimitOffset(r *http.Request) (limit, offset int, err error) {
	limit = defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err2 := strconv.Atoi(raw)
		if err2 != nil {
			return 0, 0, err2
		}
		limit = clampPageSize(n)
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		off, err2 := decodeCursor(raw)
		if err2 != nil {
			return 0, 0, err2
		}
		offset = off
	}
	return limit, offset, nil
}

// writeJSON serializes v as application/json with the given status. On
// encoder failure it can't recover (headers already written) so we log
// silently — the client sees a truncated body which will fail its parse.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handlePatchTaxon applies a partial update to a taxon and returns the
// resulting representation with an ETag matching the new col__modified.
//
// Concurrency: If-Match on the request is compared against col__modified
// via hive.UpdateTaxon (the tx propagates it). Callers reading a taxon and
// wanting a safe update pattern do:
//  1. GET /api/taxon/{id} → note the response's ETag header
//  2. PATCH /api/taxon/{id} with `If-Match: <that etag>`
//
// A concurrent edit between (1) and (2) causes a 409 conflict and the
// client re-reads.
//
// @Summary      Patch a taxon
// @Description  Partial update to a taxon row. Pointer-optional fields distinguish clear vs leave-alone; supports If-Match optimistic concurrency.
// @Tags         taxa
// @Accept       json
// @Produce      json
// @Param        id path string true "Taxon ID"
// @Param        If-Match header string false "col__modified ETag for optimistic concurrency"
// @Param        body body apiTaxonPatch true "Taxon patch body"
// @Success      200 {object} apiTaxon
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /taxon/{id} [patch]
func (s *server) handlePatchTaxon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch apiTaxonPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ifMatch := r.Header.Get("If-Match")

	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetTaxon(r.Context(), id)
		if err != nil {
			return err
		}
		applyTaxonPatch(current, patch)
		// Propagate the If-Match token to UpdateTaxon so the concurrency
		// check happens under the tx's snapshot, not the pre-read window.
		current.Modified = ifMatch
		return tx.UpdateTaxon(*current)
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}

	// Re-fetch to publish the new modified timestamp as ETag.
	fresh, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", fresh.Modified)
	writeJSON(w, http.StatusOK, taxonToAPI(fresh))
}

// handlePatchName applies a partial update to a name. gn__* fields are
// regenerated by hive.UpdateName from the (possibly-patched) verbatim
// name string; callers cannot bypass this — see CLAUDE.md § gn__*.
//
// @Summary      Patch a name
// @Description  Partial update to a name row. gn__* fields are always re-derived server-side from the scientific-name string; supports If-Match optimistic concurrency.
// @Tags         names
// @Accept       json
// @Produce      json
// @Param        id path string true "Name ID"
// @Param        If-Match header string false "col__modified ETag for optimistic concurrency"
// @Param        body body apiNamePatch true "Name patch body"
// @Success      200 {object} apiName
// @Failure      400 {object} problem
// @Failure      404 {object} problem
// @Failure      409 {object} problem
// @Failure      422 {object} problem
// @Failure      500 {object} problem
// @Router       /name/{id} [patch]
func (s *server) handlePatchName(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch apiNamePatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ifMatch := r.Header.Get("If-Match")

	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		current, err := s.a.GetName(r.Context(), id)
		if err != nil {
			return err
		}
		applyNamePatch(current, patch)
		current.Modified = ifMatch
		if err := tx.UpdateName(*current); err != nil {
			return err
		}
		// Status is written separately via SetNameStatus so NOMEN URIs
		// (which sflib's NomStatus enum can't round-trip) survive
		// untouched. Only fire when the patch actually included Status
		// — otherwise skipping keeps col__status_id and its modified
		// stamp intact.
		if patch.Status != nil {
			if err := tx.SetNameStatus(id, *patch.Status); err != nil {
				return err
			}
		}
		// Reference change is the common trigger for backfill — if the
		// curator just picked the combination paper, the reference's
		// year can now populate col__combination_authorship_year (and
		// the linked basionym's year via the sanity check). Skip when
		// no reference field was touched; the two years won't move.
		if patch.ReferenceID != nil {
			if err := tx.BackfillAuthorshipYears(id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}

	fresh, err := s.a.GetName(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", fresh.Modified)
	body := nameToAPI(fresh)
	body.ReferenceLabel = s.referenceLabel(r.Context(), body.ReferenceID)
	body.Basionym = s.resolveBasionym(r.Context(), body.ID)
	writeJSON(w, http.StatusOK, body)
}

// handleReindexValidation walks every hive-validated table and rewrites
// __gsvalidator_results. Backs a "recompute all issues" button on the
// Issues screen and mirrors the `hive validate` CLI. Runs synchronously
// — the current rule set is small enough that a full pass returns in
// milliseconds for a typical archive. A streaming SSE variant is
// deferred until the long-running-ops plumbing lands (see PLANNING.md
// § Long-running operations).
//
// Refuses (409) on a read-only archive. Response body is a small
// summary; consumers can render a toast or refresh the issue list.
//
// @Summary      Reindex validation issues
// @Description  Walk every hive-validated table and rewrite __gsvalidator_results. Runs synchronously; returns a small summary.
// @Tags         issues
// @Produce      json
// @Success      200 {object} apiReindexResult
// @Failure      409 {object} problem
// @Failure      500 {object} problem
// @Router       /reindex/validation [post]
func (s *server) handleReindexValidation(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	var (
		count int
		table string
	)
	start := time.Now()
	err := s.a.ReindexValidation(r.Context(), func(p hive.ReindexProgress) {
		count = p.Done
		table = p.Table
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apiReindexResult{
		Table:     table,
		Records:   count,
		ElapsedMS: time.Since(start).Milliseconds(),
	})
}

// apiReindexResult is the wire shape of a reindex response. Small on
// purpose — the client already knows what triggered the reindex and
// just needs to confirm it finished and refresh whatever view was open.
type apiReindexResult struct {
	Table     string `json:"table"`
	Records   int    `json:"records"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// handleIssueSummary returns per-rule / per-severity counts across the
// archive so a dashboard can render totals broken down by both axes
// without paging through the issue list itself.
//
// @Summary      Issue counts summary
// @Description  Per-rule / per-severity counts across the archive.
// @Tags         issues
// @Produce      json
// @Success      200 {object} apiIssueSummary
// @Failure      500 {object} problem
// @Router       /issue/summary [get]
func (s *server) handleIssueSummary(w http.ResponseWriter, r *http.Request) {
	rows, err := s.a.IssueSummary(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	out := make([]apiIssueSummaryRow, len(rows))
	for i, row := range rows {
		out[i] = apiIssueSummaryRow{
			Table:    row.TableName,
			RuleID:   row.RuleID,
			RuleName: row.RuleName,
			Severity: row.Severity,
			Count:    row.Count,
		}
	}
	writeJSON(w, http.StatusOK, apiIssueSummary{Items: out})
}

// handleIssueList returns a page of issues matching the query filters.
// Recognised query params: table, rule_id, severity (repeatable),
// hide_acknowledged (bool), limit, offset. All optional.
//
// @Summary      List validation issues
// @Description  Paginated list of validation issues matching the given filters.
// @Tags         issues
// @Produce      json
// @Param        table query string false "Filter by table_name"
// @Param        record_id query string false "Filter by record id"
// @Param        rule_id query string false "Filter by rule id"
// @Param        severity query []string false "Filter by severity (repeatable: error|warn|info|debug)"
// @Param        hide_acknowledged query bool false "Hide acknowledged issues"
// @Param        limit query int false "Page size"
// @Param        cursor query string false "Pagination cursor"
// @Success      200 {object} apiPage[apiIssue]
// @Failure      400 {object} problem
// @Failure      500 {object} problem
// @Router       /issue [get]
func (s *server) handleIssueList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := hive.IssueFilter{
		TableName:        q.Get("table"),
		RecordID:         q.Get("record_id"),
		RuleID:           q.Get("rule_id"),
		Severities:       q["severity"],
		HideAcknowledged: q.Get("hide_acknowledged") == "true",
	}
	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	issues, total, err := s.a.ListIssues(r.Context(), f, limit, offset)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	items := make([]apiIssue, len(issues))
	for i, is := range issues {
		items[i] = apiIssue{
			ID:             is.ID,
			Table:          is.TableName,
			RecordID:       is.RecordID,
			RecordLabel:    is.RecordLabel,
			LinkTaxonID:    is.LinkTaxonID,
			RuleID:         is.RuleID,
			RuleName:       is.RuleName,
			FieldName:      is.FieldName,
			Severity:       is.Severity,
			Enforcement:    is.Enforcement,
			Message:        is.Message,
			ActualValue:    is.ActualValue,
			ExpectedValue:  is.ExpectedValue,
			CreatedAt:      is.CreatedAt,
			AcknowledgedBy: is.AcknowledgedBy,
			AcknowledgedAt: is.AcknowledgedAt,
		}
	}
	page := apiPage[apiIssue]{Items: items, Total: &total}
	if len(issues) == limit && offset+limit < total {
		page.NextCursor = encodeCursor(offset + limit)
	}
	writeJSON(w, http.StatusOK, page)
}

// apiIssueSummary is the wire envelope for /api/issue/summary. Items
// live under a key rather than being the top-level array so the shape
// can grow later (totals, timestamps) without breaking clients.
type apiIssueSummary struct {
	Items []apiIssueSummaryRow `json:"items"`
}

type apiIssueSummaryRow struct {
	Table    string `json:"table"`
	RuleID   string `json:"rule_id"`
	RuleName string `json:"rule_name,omitempty"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

// apiIssue is the wire shape of a single stored issue. Includes both
// the raw record_id and the pre-resolved record_label + link_taxon_id
// so the list renders without follow-up lookups.
type apiIssue struct {
	ID             string `json:"id"`
	Table          string `json:"table"`
	RecordID       string `json:"record_id"`
	RecordLabel    string `json:"record_label,omitempty"`
	LinkTaxonID    string `json:"link_taxon_id,omitempty"`
	RuleID         string `json:"rule_id"`
	RuleName       string `json:"rule_name,omitempty"`
	FieldName      string `json:"field_name,omitempty"`
	Severity       string `json:"severity"`
	Enforcement    string `json:"enforcement"`
	Message        string `json:"message"`
	ActualValue    string `json:"actual_value,omitempty"`
	ExpectedValue  string `json:"expected_value,omitempty"`
	CreatedAt      string `json:"created_at"`
	AcknowledgedBy string `json:"acknowledged_by,omitempty"`
	AcknowledgedAt string `json:"acknowledged_at,omitempty"`
}
