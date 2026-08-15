package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	hive "github.com/sfborg/hive/pkg"
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
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/archive", s.handleArchive)
	mux.HandleFunc("GET /api/vocab", s.handleVocab)
	mux.HandleFunc("GET /api/vocab/nomen", s.handleNomenVocab)
	mux.HandleFunc("GET /api/vocab/countries", s.handleCountriesVocab)
	mux.HandleFunc("GET /api/vocab/languages", s.handleLanguagesVocab)
	mux.HandleFunc("GET /api/vocab/sex", s.handleSexVocab)
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

	mux.HandleFunc("POST /api/reindex/validation", s.handleReindexValidation)

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
	body := metadataToAPI(m)
	body.Warnings = metadataWarnings(s.a, r.Context(), m.ID)
	writeJSON(w, http.StatusOK, body)
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
	body.Warnings = validationWarnings(s.a, r.Context(), t.ID, t.NameID)
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
// See hive.CreateNamePrefix for the exact rules.
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
				NameID:      n.NameID,
				Label:       apiLabel{Text: n.Label.Text, HTML: n.Label.HTML},
				Authorship:  n.Authorship,
				Rank:        n.Rank,
				Year:        n.Year,
				IsBasionym:  n.IsBasionym,
				Involvement: n.Involvement,
				SynonymID:   n.SynonymID,
				ReferenceID: n.ReferenceID,
				IssueCount:  n.IssueCount,
			}
			row.ReferenceLabel = s.referenceLabel(r.Context(), firstCSVID(row.ReferenceID))
			outC.Names = append(outC.Names, row)
		}
		out.Clusters = append(out.Clusters, outC)
	}
	writeJSON(w, http.StatusOK, out)
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
		})
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
		return tx.UpdateSpeciesInteraction(rowid, merged)
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
	var newNameID string
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
		nameID, err := tx.CreateName(body.toColdpName())
		if err != nil {
			return err
		}
		newNameID = nameID
		_, err = tx.AddSynonym(coldp.Synonym{
			TaxonID: taxonID,
			NameID:  nameID,
			// Status defaults to SYNONYM inside AddSynonym; modified /
			// modified_by come from the tx actor.
		})
		return err
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
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
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
func (s *server) handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.a.TaxonDeletePreview(r.Context(), id)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"direct_child_count":            p.DirectChildCount,
		"descendant_count":              p.DescendantCount,
		"synonym_count":                 p.SynonymCount,
		"vernacular_count":              p.VernacularCount,
		"distribution_count":            p.DistributionCount,
		"media_count":                   p.MediaCount,
		"treatment_count":               p.TreatmentCount,
		"species_estimate_count":        p.SpeciesEstimateCount,
		"taxon_property_count":          p.TaxonPropertyCount,
		"species_interaction_count":     p.SpeciesInteractionCount,
		"taxon_concept_relation_count":  p.TaxonConceptRelationCount,
		"parent_id":                     p.ParentID,
	})
}

// handleDeleteReparent moves the taxon's direct children up one level
// then leaf-deletes the taxon. Response body carries the destination
// parent_id (empty when children became new roots) so the client can
// reveal the tree at the parent's position.
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
// — an empty ID triggers a UUID inside hive.CreateReference. Callers
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
	err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error {
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
	ref, err := hive.OpenAlex().ResolveDOI(r.Context(), doi)
	if err != nil {
		if errors.Is(err, hive.ErrOpenAlexNotFound) {
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
	// a saved row. The WUI leaves ID empty on the POST so hive.CreateReference
	// mints a fresh one at write time. Sending a UUID here would be
	// misleading (the curator might think it's already saved).
	ref.ID = ""
	writeJSON(w, http.StatusOK, referenceToAPI(ref))
}

// handleLookupBHLnames runs a BHLnames match for the given scientific
// name and returns hits ordered as BHLnames returned them (newest-first
// by default; see hive.BHLnamesClient.LookupName). Each hit carries an
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
	hits, err := hive.BHLnames().LookupName(r.Context(),
		body.Canonical, body.Authors, body.Year,
		hive.BHLnameLookupOpts{
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
	ref, err := hive.ParseBibTeX(text)
	if err != nil {
		if errors.Is(err, hive.ErrBibTeXParse) {
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
// via hive.UpdateTaxon (the tx propagates it). Callers reading a taxon and
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

