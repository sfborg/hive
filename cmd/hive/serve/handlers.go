package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/sfborg/hive/core"
	"github.com/sfborg/hive/core/ui"
	"github.com/sfborg/sflib/pkg/coldp"
)

// server is the HTTP handler context. Owns the archive handle plus any
// shared state (soon: actor context, cached enums, keymap, form specs).
// Handlers are methods on this type so they can reach dependencies without
// package-level globals.
type server struct {
	a           *core.Archive
	archivePath string
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/archive", s.handleArchive)
	mux.HandleFunc("GET /api/vocab", s.handleVocab)
	mux.HandleFunc("GET /api/vocab/nomen", s.handleNomenVocab)
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
	mux.HandleFunc("GET /api/taxon/{id}/ancestors", s.handleAncestors)
	mux.HandleFunc("POST /api/taxon/{id}/move", s.handleMoveTaxon)
	mux.HandleFunc("POST /api/taxon/{id}/basionym", s.handleAddBasionym)
	mux.HandleFunc("POST /api/taxon", s.handleCreateTaxon)
	mux.HandleFunc("DELETE /api/taxon/{id}", s.handleDeleteTaxon)
	mux.HandleFunc("GET /api/taxon/{id}/code-default", s.handleCodeDefault)
	mux.HandleFunc("GET /api/taxon/{id}/create-name-prefix", s.handleCreateNamePrefix)
	mux.HandleFunc("GET /api/taxon/{id}/child-ranks", s.handleChildRanks)

	mux.HandleFunc("GET /api/name/search", s.handleNameSearch)
	mux.HandleFunc("POST /api/name/parse", s.handleParseName)
	mux.HandleFunc("GET /api/name/{id}", s.handleGetName)

	mux.HandleFunc("GET /api/reference", s.handleReferences)
	mux.HandleFunc("GET /api/reference/search", s.handleReferenceSearch)
	mux.HandleFunc("POST /api/reference", s.handleCreateReference)
	// Add-reference modal helpers. All three return *unsaved* previews
	// — the modal shows a form the curator confirms, then POSTs to
	// /api/reference to actually write.
	mux.HandleFunc("GET /api/reference/resolve-doi", s.handleResolveDOI)
	mux.HandleFunc("POST /api/reference/lookup-bhlnames", s.handleLookupBHLnames)
	mux.HandleFunc("POST /api/reference/parse-bibtex", s.handleParseBibTeX)
	mux.HandleFunc("GET /api/reference/{id}", s.handleGetReference)

	mux.HandleFunc("PATCH /api/taxon/{id}", s.handlePatchTaxon)
	mux.HandleFunc("PATCH /api/name/{id}", s.handlePatchName)

	return mux
}

// handleHealth is the trivial liveness probe. Returns 200 with a static body
// so orchestrators can distinguish "server up" from "server hung."
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVocab returns the controlled-vocabulary bundle. Callers cache this
// once at boot; the tables are read-only for the process lifetime so a
// long Cache-Control keeps browsers from re-fetching. Bundle-endpoint
// URL stays plural-ish (`/api/vocab` is treated as a mass noun) because
// it isn't a "get one resource by id" lookup.
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

// handleNomenVocab returns the NOMEN ontology's class list — the
// controlled vocabulary of nomenclatural statuses under ICZN, ICN,
// ICNP, ICVCN, ICNCP, and ICPN. Served separately from the sfga
// vocabs (/api/vocab) because it comes from an embedded OWL file and
// the frontend fetches it only when the status picker is opened.
// See CLAUDE.md § Nomenclatural status vocabulary (NOMEN).
func (s *server) handleNomenVocab(w http.ResponseWriter, r *http.Request) {
	terms, err := core.Nomen()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	// Immutable for the process lifetime — safe to cache aggressively.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": terms})
}

// handleKeymap returns the canonical shortcut list from core/ui.
// Served whole so the WUI fetches once at boot and holds it in the
// module-level cache alongside the vocab / NOMEN bundles.
//
// The WUI filters client-side to WUI-available bindings; returning
// the full list (including TUI-only rows) keeps the endpoint useful
// to other consumers — future automation tooling, documentation
// generators, or the eventual customization layer.
func (s *server) handleKeymap(w http.ResponseWriter, r *http.Request) {
	// Same immutable-for-process-lifetime story as vocab / NOMEN.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": ui.Keymap()})
}

// handleGetMetadata returns the dataset metadata row (title,
// description, license, …). Missing metadata is a 404 — legacy
// archives without a seeded row can PATCH one in.
func (s *server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
	m, err := s.a.GetMetadata(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, metadataToAPI(m))
}

// handlePatchMetadata applies a partial update to the metadata row.
// Missing row → UpdateMetadata inserts one on the fly, so a PATCH
// against a legacy archive without seeded metadata also serves as the
// initial seed (as long as the patch supplies Title).
func (s *server) handlePatchMetadata(w http.ResponseWriter, r *http.Request) {
	var patch apiMetadataPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	err := s.a.WithTx(r.Context(), func(tx *core.Tx) error {
		current, err := s.a.GetMetadata(r.Context())
		if err != nil {
			// Legacy archive without seeded metadata — seed on first
			// PATCH, defaulting Title if the patch didn't supply it
			// (schema requires it NOT NULL).
			current = &core.Metadata{Title: "Untitled archive"}
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

// handleRoots is a paginated projection of root-level taxa. Alias of
// /api/taxon/{id}/children with an empty parent ID; kept as a dedicated
// endpoint so WUI/TUI code that wants the top of the tree doesn't need to
// invent a magic sentinel.
func (s *server) handleRoots(w http.ResponseWriter, r *http.Request) {
	s.writeChildren(w, r, "")
}

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
	writeJSON(w, http.StatusOK, body)
}

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
// See core.CreateNamePrefix for the exact rules.
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
// See core.Archive.ValidChildRanks for filter logic.
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

// handleAncestors returns the taxon's parent chain in root-down order
// (excluding the taxon itself). Front-ends use this to expand the tree
// down to a moved taxon so it appears in its new location without a full
// reload.
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
		items = append(items, synonymHitToAPI(h))
	}
	writeJSON(w, http.StatusOK, apiPage[apiSynonym]{Items: items})
}

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
	return core.ReferenceLabel(ref)
}

// handleTaxonSearch returns taxa whose name matches q as a case-insensitive
// substring. Backs the WUI parent picker; also useful as a generic tree
// navigation aid.
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
	hits, err := s.a.SearchTaxa(r.Context(), q, limit)
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

// handleMoveTaxon reparents a taxon. Body is {"new_parent_id": "..."} —
// empty string moves the taxon to root level. Optimistic concurrency via
// If-Match on the taxon's current col__modified. Cycle attempts, self-parent
// attempts, and unknown-parent errors surface from core with the usual
// sentinel-to-status mapping (ErrValidation → 422, ErrNotFound → 404,
// ErrConflict → 409).
//
// v0 does NOT refresh denormalized classification columns after the move —
// see CLAUDE.md § core/ package conventions. A future Reclassify endpoint
// will rebuild them.
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

	err := s.a.WithTx(r.Context(), func(tx *core.Tx) error {
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
					id, core.ErrConflict, current.Modified, ifMatch)
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
	err = s.a.WithTx(r.Context(), func(tx *core.Tx) error {
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

		return tx.LinkNameRelation(coldp.NameRelation{
			NameID:        currentNameID,
			RelatedNameID: nameID,
			Type:          coldp.NewNomRelType("BASIONYM"),
		})
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

// handleCreateTaxon composes a new (name, taxon) pair in one WithTx and
// returns the resulting taxon detail (same shape as GET /api/taxon/{id}).
//
// Body is an apiName-shaped subset (every atomized field the create
// modal's preview form can send) plus parent_id + name_phrase. Only
// scientific_name and code are required; everything else is optional
// and, when omitted, falls to core.CreateName's fill-from-parse
// (gnparser-derived defaults). Curator-supplied atomized values (e.g.
// a genus override for a name gnparser mis-atomizes) always win over
// the parse.
//
// parent_id may be empty (creates a root-level taxon).
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

	var newID, newNameID string
	err := s.a.WithTx(r.Context(), func(tx *core.Tx) error {
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
		return nil
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
	resp.Warnings = validationWarnings(s.a, r.Context(), newNameID)
	w.Header().Set("ETag", t.Modified)
	w.Header().Set("Location", "/api/taxon/"+t.ID)
	writeJSON(w, http.StatusCreated, resp)
}

// validationWarnings runs gsvalidator against the given name row and
// returns the soft-warning results wrapped for the wire. Best-effort
// — a validator failure yields an empty list so the create response
// still succeeds; the caller doesn't lose data because a rule crashed.
func validationWarnings(a *core.Archive, ctx context.Context, nameID string) []apiValidationWarning {
	if nameID == "" {
		return nil
	}
	results, err := a.ValidateName(ctx, nameID)
	if err != nil {
		return nil
	}
	var out []apiValidationWarning
	for _, r := range results {
		// gsvalidator's Rule.ValidationType uses "hard" / "soft";
		// the sfga custom validators copy that value through to
		// Result.ValidationType unchanged, but Result.IsWarning()
		// only matches "warn". Accept either name-set here so
		// warnings surface regardless of which convention a rule
		// author picked.
		if r.Passed {
			continue
		}
		if r.ValidationType != "soft" && r.ValidationType != "warn" {
			continue
		}
		out = append(out, apiValidationWarning{
			RuleID:    r.RuleID,
			RuleName:  r.RuleName,
			FieldName: r.FieldName,
			Message:   r.Message,
		})
	}
	return out
}

// handleDeleteTaxon removes the taxon and its per-taxon associations.
// Refuses (409) when the taxon has children — the curator must reparent
// or delete them first. The response body carries the parent_id so
// front-ends can reveal the tree at the parent's location.
func (s *server) handleDeleteTaxon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Capture parent_id before the delete — the row is gone afterward.
	t, err := s.a.GetTaxon(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	parentID := t.ParentID

	err = s.a.WithTx(r.Context(), func(tx *core.Tx) error {
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

// handleReferences returns a paginated list of references, ordered
// by author + year + title. Same shape as the taxon list endpoints so
// clients can render either aggregate uniformly.
func (s *server) handleReferences(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeBadRequest(w, r, err.Error())
		return
	}
	hits, total, err := s.a.ListReferencesPage(r.Context(), limit, offset)
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

// handleReferenceSearch returns references whose author, title, or
// citation matches q as a case-insensitive substring.
func (s *server) handleReferenceSearch(w http.ResponseWriter, r *http.Request) {
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
	hits, err := s.a.SearchReferences(r.Context(), q, limit)
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

// handleCreateReference writes a new reference from the JSON body and
// returns the freshly-fetched row (fully hydrated with modified /
// modified_by). Body accepts every apiReference field; ID is optional
// — an empty ID triggers a UUID inside core.CreateReference. Callers
// that want to preserve an upstream id (e.g. a legacy CoLDP-string
// identifier) can send it verbatim.
func (s *server) handleCreateReference(w http.ResponseWriter, r *http.Request) {
	var body apiReference
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ref := apiReferenceToColdp(body)

	var newID string
	err := s.a.WithTx(r.Context(), func(tx *core.Tx) error {
		id, err := tx.CreateReference(ref)
		if err != nil {
			return err
		}
		newID = id
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
	if fresh.Modified != "" {
		w.Header().Set("ETag", fresh.Modified)
	}
	w.Header().Set("Location", "/api/reference/"+newID)
	writeJSON(w, http.StatusCreated, referenceToAPI(fresh))
}

// handleResolveDOI fetches an OpenAlex Work for the DOI in the `doi`
// query param and returns the resulting coldp.Reference — *unsaved*.
// The modal shows this as a preview form; on curator confirm the WUI
// POSTs to /api/reference to persist. Returns 404 with a distinguishing
// problem type when OpenAlex has no match, 502 on upstream errors.
func (s *server) handleResolveDOI(w http.ResponseWriter, r *http.Request) {
	doi := strings.TrimSpace(r.URL.Query().Get("doi"))
	if doi == "" {
		writeBadRequest(w, r, "query parameter 'doi' is required")
		return
	}
	ref, err := core.OpenAlex().ResolveDOI(r.Context(), doi)
	if err != nil {
		if errors.Is(err, core.ErrOpenAlexNotFound) {
			writeProblemDetail(w, r, http.StatusNotFound,
				"urn:hive:error:openalex-not-found", "not found",
				"OpenAlex has no Work for DOI "+doi)
			return
		}
		writeProblemDetail(w, r, http.StatusBadGateway,
			"urn:hive:error:openalex", "openalex lookup failed", err.Error())
		return
	}
	// Clear the generated UUID from ResolveDOI — this is a preview, not
	// a saved row. The WUI leaves ID empty on the POST so core.CreateReference
	// mints a fresh one at write time. Sending a UUID here would be
	// misleading (the curator might think it's already saved).
	ref.ID = ""
	writeJSON(w, http.StatusOK, referenceToAPI(ref))
}

// handleLookupBHLnames runs a BHLnames match for the given scientific
// name and returns hits ordered as BHLnames returned them (newest-first
// by default; see core.BHLnamesClient.LookupName). Each hit carries an
// unsaved preview reference plus BHLnames's own quality/score signals so
// the modal can render "auto-suggest confidently" vs "here if you want
// it" tiers.
//
// Body: {canonical, authors, year, nomen_event, limit}. `canonical` is
// required; the rest are optional but scores improve dramatically when
// authorship + year are supplied.
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
	hits, err := core.BHLnames().LookupName(r.Context(),
		body.Canonical, body.Authors, body.Year,
		core.BHLnameLookupOpts{
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
	ref, err := core.ParseBibTeX(text)
	if err != nil {
		if errors.Is(err, core.ErrBibTeXParse) {
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
	writeJSON(w, http.StatusOK, referenceToAPI(ref))
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
	preview := s.a.ParseNamePreview(body.Code, body.ScientificName)
	writeJSON(w, http.StatusOK, nameToAPI(preview))
}

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
// via core.UpdateTaxon (the tx propagates it). Callers reading a taxon and
// wanting a safe update pattern do:
//   1. GET /api/taxon/{id} → note the response's ETag header
//   2. PATCH /api/taxon/{id} with `If-Match: <that etag>`
// A concurrent edit between (1) and (2) causes a 409 conflict and the
// client re-reads.
func (s *server) handlePatchTaxon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch apiTaxonPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ifMatch := r.Header.Get("If-Match")

	err := s.a.WithTx(r.Context(), func(tx *core.Tx) error {
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
// regenerated by core.UpdateName from the (possibly-patched) verbatim
// name string; callers cannot bypass this — see CLAUDE.md § gn__*.
func (s *server) handlePatchName(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch apiNamePatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	ifMatch := r.Header.Get("If-Match")

	err := s.a.WithTx(r.Context(), func(tx *core.Tx) error {
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

