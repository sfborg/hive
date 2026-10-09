/**
 * hive WUI API client.
 *
 * The only place in the WUI that talks to /api/*. Components take data as
 * properties and dispatch CustomEvents for actions; a top-level controller
 * calls this module and re-renders on responses.
 *
 * The client throws Problem objects (RFC 7807) for non-2xx responses so
 * callers can pattern-match on `problem.type` (URN under the hive prefix)
 * without parsing the human-readable title.
 */

const problemMime = "application/problem+json";

/** Small typed error class for RFC 7807 payloads. */
export class Problem extends Error {
  constructor(payload, status) {
    super(payload.detail || payload.title || `HTTP ${status}`);
    this.type = payload.type || "";
    this.title = payload.title || "";
    this.status = payload.status || status;
    this.detail = payload.detail || "";
    this.instance = payload.instance || "";
    this.fields = payload.errors || [];
  }
}

async function j(method, url, body, headers = {}) {
  const opts = {
    method,
    headers: { Accept: "application/json", ...headers },
  };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(url, opts);
  if (!res.ok) {
    // Any response — problem+json or plain — is coerced into a Problem so
    // callers only have one error shape to handle.
    const mime = res.headers.get("content-type") || "";
    if (mime.includes(problemMime)) {
      throw new Problem(await res.json(), res.status);
    }
    throw new Problem({ title: res.statusText || "http error", status: res.status }, res.status);
  }
  if (res.status === 204) return null;
  // Read the body first so we can attach the ETag alongside it. Consumers
  // wanting the ETag reach for `.__etag`; wanting just the body use as-is.
  const body_ = await res.json();
  const etag = res.headers.get("etag");
  if (etag) {
    Object.defineProperty(body_, "__etag", { value: etag, enumerable: false });
  }
  return body_;
}

/** Query-string helper: skips undefined/null values. Arrays become
 * repeated params (?severity=warn&severity=error) so multi-select
 * filters like the issue endpoint's severity axis work naturally. */
function qs(params = {}) {
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    if (Array.isArray(v)) {
      for (const item of v) {
        if (item === undefined || item === null || item === "") continue;
        usp.append(k, String(item));
      }
      continue;
    }
    usp.set(k, String(v));
  }
  const s = usp.toString();
  return s ? `?${s}` : "";
}

// Resource groups are singular to match the URL convention.
// vocabCache holds the controlled-vocabulary bundle fetched once at boot.
// Components read from it synchronously via api.vocab.get(name) — the
// bundle is small (a few thousand terms across ~17 vocabularies) so
// keeping the whole thing in memory is fine.
let vocabCache = null;
// nomenCache holds the NOMEN ontology loaded from /api/vocab/nomen —
// fetched once, byURI is a URI→term lookup for label display.
let nomenCache = null;
let nomenByURI = null;
// ISO vocab caches populated on first picker open. Countries + sex are
// small; languages is ~7900 entries but fetched once and reused.
let countriesCache = null;
let languagesCache = null;
let sexCache = null;
// keymapCache holds the shared shortcut list loaded from /api/keymap.
// Populated once at boot; consumers read via api.keymap.all() or
// api.keymap.forScope(scope).
let keymapCache = null;

// sfga nom_code IDs (ZOOLOGICAL, BOTANICAL, …) → NOMEN label prefix
// (ICZN, ICN, …). Mirrors core.NomenCodePrefix on the server side.
const nomenCodePrefix = {
  ZOOLOGICAL: "ICZN",
  BOTANICAL: "ICN",
  BACTERIAL: "ICNP",
  VIRUS: "ICVCN",
  CULTIVARS: "ICNCP",
  PHYTOSOCIOLOGICAL: "ICPN",
};

export const api = {
  // Per-user interface settings (stored on the server per ORCID).
  settings: {
    list: () => j("GET", "/api/settings"),
    put: (key, value) => j("PUT", `/api/settings/${encodeURIComponent(key)}`, value),
    remove: (key) => j("DELETE", `/api/settings/${encodeURIComponent(key)}`),
  },
  health: () => j("GET", "/api/health"),
  // services reports whether GROBID, OCR and pandoc are available:
  // {grobid, ocr, pandoc}, each "ok" / "not_configured" / "unavailable".
  services: () => j("GET", "/api/services"),
  archive: () => j("GET", "/api/archive"),

  metadata: {
    get: () => j("GET", "/api/metadata"),
    patch: (patch) => j("PATCH", "/api/metadata", patch),
  },

  vocab: {
    // load fetches the full bundle from /api/vocab and caches it. Idempotent
    // — subsequent calls return the cached copy without a network round-trip.
    // Call once at boot before any component that needs vocab data renders.
    load: async () => {
      if (vocabCache) return vocabCache;
      vocabCache = await j("GET", "/api/vocab");
      return vocabCache;
    },
    // get returns the terms for a named vocabulary synchronously. Returns
    // [] if the bundle isn't loaded yet or the name is unknown; callers
    // don't crash on race conditions during boot.
    get: (name) => (vocabCache ? vocabCache[name] || [] : []),
    // isLoaded lets components skip rendering pickers until the bundle
    // arrives. Rarely needed since load() runs before mount.
    isLoaded: () => vocabCache !== null,
    // reload drops the cached bundle so the next load() re-fetches. Called
    // after vocab-editor writes so pickers reflect the new / edited /
    // deleted terms without a browser refresh.
    reload: async () => {
      vocabCache = null;
      return await api.vocab.load();
    },
    // listFull fetches the rich-field per-term list for editable vocabs.
    // Only vocabs with schema support for description / obo / etc. respond;
    // currently: species_interaction_type.
    listFull: (name) =>
      j("GET", `/api/vocab/${encodeURIComponent(name)}/full`),
    // add / patch / delete round-trip a single term via the vocab editor.
    // Post-write, callers should api.vocab.reload() to refresh cached
    // picker data.
    add: (name, body) =>
      j("POST", `/api/vocab/${encodeURIComponent(name)}`, body),
    patch: (name, id, body) =>
      j(
        "PATCH",
        `/api/vocab/${encodeURIComponent(name)}/${encodeURIComponent(id)}`,
        body,
      ),
    delete: (name, id) =>
      j(
        "DELETE",
        `/api/vocab/${encodeURIComponent(name)}/${encodeURIComponent(id)}`,
      ),
  },

  // ISO vocab bundles served on-demand — countries + languages are
  // large enough that fetching at boot would balloon the initial
  // payload for curators who never touch a vernacular. Each vocab
  // caches on first load, so opening the picker a second time is
  // instant. Cache-Control headers on the endpoints let the browser
  // serve subsequent visits from disk cache.
  countries: {
    load: async () => {
      if (countriesCache) return countriesCache;
      const resp = await j("GET", "/api/vocab/countries");
      countriesCache = resp.items || [];
      return countriesCache;
    },
    get: () => countriesCache || [],
    isLoaded: () => countriesCache !== null,
  },
  languages: {
    load: async () => {
      if (languagesCache) return languagesCache;
      const resp = await j("GET", "/api/vocab/languages");
      languagesCache = resp.items || [];
      return languagesCache;
    },
    get: () => languagesCache || [],
    isLoaded: () => languagesCache !== null,
  },
  sex: {
    load: async () => {
      if (sexCache) return sexCache;
      const resp = await j("GET", "/api/vocab/sex");
      sexCache = resp.items || [];
      return sexCache;
    },
    get: () => sexCache || [],
    isLoaded: () => sexCache !== null,
  },

  nomen: {
    // load fetches /api/vocab/nomen once and caches. Called from the
    // top-level shell in connectedCallback alongside vocab.load().
    load: async () => {
      if (nomenCache) return nomenCache;
      const resp = await j("GET", "/api/vocab/nomen");
      nomenCache = resp.items || [];
      nomenByURI = new Map(nomenCache.map((t) => [t.id, t]));
      return nomenCache;
    },
    // filterByCode returns terms whose Code matches the NOMEN prefix
    // for the given sfga nom_code ID. Also drops non-classification
    // terms (ranks, relationships, name-part descriptors) and the
    // Latinized subtree (gender / part-of-speech — TW-classified but
    // not nomenclatural statuses) so the picker only sees actual
    // statuses. Empty codeID → all classification terms, unfiltered
    // by code.
    //
    // Sorted popular-first (CoL-mapped roots at the top), then
    // alphabetical by label. Matches the TUI's nomenComboSource
    // ordering and mirrors TW's "popular statuses on the first tab"
    // UX — common choices land at the top without scrolling.
    filterByCode: (codeID) => {
      if (!nomenCache) return [];
      const prefix = nomenCodePrefix[codeID] || "";
      let out = nomenCache.filter(
        (t) =>
          t.kind === "classification" &&
          !(t.class || "").startsWith("TaxonNameClassification::Latinized"),
      );
      if (prefix) out = out.filter((t) => t.code === prefix);
      return [...out].sort((a, b) => {
        if (!!a.popular !== !!b.popular) return a.popular ? -1 : 1;
        return (a.label || "").localeCompare(b.label || "");
      });
    },
    // labelFor resolves a NOMEN URI → its human label, or "" if the
    // URI isn't in the ontology (legacy col__status_id values return
    // "" and callers fall back to their own display derivation).
    labelFor: (uri) => (nomenByURI ? nomenByURI.get(uri)?.label || "" : ""),
    isLoaded: () => nomenCache !== null,
  },

  keymap: {
    // load fetches the canonical shortcut list from /api/keymap once
    // and caches it in memory. Both frontends share the same source
    // (pkg/ui.Keymap()) so the WUI's key handlers and help modal
    // render from the same table.
    load: async () => {
      if (keymapCache) return keymapCache;
      const resp = await j("GET", "/api/keymap");
      // Filter to shortcuts with a WUI binding so callers don't have
      // to guard every access; TUI-only rows are useless to the WUI.
      const all = resp.items || [];
      keymapCache = all.filter((s) => (s.keys?.wui || []).length > 0);
      return keymapCache;
    },
    // all returns the cached, WUI-filtered list. [] before load() has
    // completed so first-render code paths don't crash on missing
    // data.
    all: () => keymapCache || [],
    // forScope returns the subset of the cache scoped to the given
    // pane ("global", "tree", "detail", "form"). Consumed by each
    // scope's key handler for data-driven dispatch.
    forScope: (scope) => (keymapCache || []).filter((s) => s.scope === scope),
    isLoaded: () => keymapCache !== null,
  },

  taxon: {
    roots: (opts) => j("GET", `/api/taxon/roots${qs(opts)}`),
    get: (id) => j("GET", `/api/taxon/${encodeURIComponent(id)}`),
    children: (id, opts) => j("GET", `/api/taxon/${encodeURIComponent(id)}/children${qs(opts)}`),
    synonyms: (id) => j("GET", `/api/taxon/${encodeURIComponent(id)}/synonyms`),
    ancestors: (id) => j("GET", `/api/taxon/${encodeURIComponent(id)}/ancestors`),
    // classification returns the full ancestor chain (id + name + rank
    // + status) in one call — root first, target taxon last. Backs the
    // breadcrumb strip on the taxon detail page and lets the tree
    // reveal() path fan out per-ancestor children requests in parallel
    // instead of walking the chain sequentially.
    classification: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/classification`),
    // nomenHistory returns the taxon's multi-cluster basionym-anchored
    // nomenclatural history — one cluster per basionym family. Backs the
    // Nomenclatural history section on the detail page; drives the
    // ≡ / = glyphs, cluster indentation, and per-name reference cites.
    // Shape: { clusters: [{ role, names: [{name_id, label, year,
    // is_basionym, involvement, reference_id, reference_label, ...}] }] }.
    // See core.NomenclaturalHistory for assembly semantics.
    nomenHistory: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/nomenclatural-history`),
    codeDefault: (parentId) =>
      j("GET", `/api/taxon/${encodeURIComponent(parentId)}/code-default`),
    createNamePrefix: (parentId) =>
      j("GET", `/api/taxon/${encodeURIComponent(parentId)}/create-name-prefix`),
    childRanks: (parentId) =>
      j("GET", `/api/taxon/${encodeURIComponent(parentId)}/child-ranks`),
    search: (opts) => j("GET", `/api/taxon/search${qs(opts)}`),
    // rowSources returns hive__row_source entries for the taxon's
    // extension rows (type_material, distribution, vernacular).
    // Backs the source badges on the detail-pane extension tables.
    rowSources: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/row-sources`),
    patch: (id, patch, ifMatch) =>
      j("PATCH", `/api/taxon/${encodeURIComponent(id)}`, patch, ifMatch ? { "If-Match": ifMatch } : {}),
    move: (id, newParentID, ifMatch) =>
      j(
        "POST",
        `/api/taxon/${encodeURIComponent(id)}/move`,
        { new_parent_id: newParentID },
        ifMatch ? { "If-Match": ifMatch } : {},
      ),
    create: (body) => j("POST", `/api/taxon`, body),
    // addBasionym: atomically creates the original combination for the
    // given taxon (Name + Synonym + BASIONYM relation, all in one tx).
    // Body is the same shape as create() but parent_id / name_phrase
    // are ignored — the basionym is a synonym of the given taxon, not
    // a new accepted taxon.
    addBasionym: (taxonID, body) =>
      j("POST", `/api/taxon/${encodeURIComponent(taxonID)}/basionym`, body),
    // addSynonym creates a name and links it as a synonym of the taxon.
    // Body shape mirrors addBasionym (createTaxonBody on the server) —
    // scientific_name + code required, atomized fields optional and
    // filled from gnparse on write. Returns the newly created name
    // (apiName) so callers can display it or chain further work.
    addSynonym: (taxonID, body) =>
      j("POST", `/api/taxon/${encodeURIComponent(taxonID)}/synonym`, body),
    delete: (id) => j("DELETE", `/api/taxon/${encodeURIComponent(id)}`),
    // Delete-preview returns descendant + per-association-table counts
    // used by the parent-delete modal to render the cascade summary
    // (see DESIGN.md § List-row actions / delete flow).
    deletePreview: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/delete-preview`),
    // ReparentAndDelete moves the taxon's direct children up one level
    // (to its parent — empty when the taxon was a root) then deletes
    // the taxon as a leaf.
    deleteReparent: (id) =>
      j("POST", `/api/taxon/${encodeURIComponent(id)}/delete-reparent`),
    // CascadeDelete removes the taxon, every descendant, and every
    // per-taxon association attached to any member of that set.
    deleteCascade: (id) =>
      j("POST", `/api/taxon/${encodeURIComponent(id)}/delete-cascade`),
    // vernaculars returns the taxon's vernacular names —
    // preferred-first, then by language, then name. Each item is an
    // apiVernacular {id, taxon_id, name, language, preferred, country,
    // area, sex, transliteration, source_id, reference_id, remarks,
    // modified, modified_by}.
    vernaculars: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/vernaculars`),
    createVernacular: (id, body) =>
      j("POST", `/api/taxon/${encodeURIComponent(id)}/vernaculars`, body),
    // distributions returns every distribution row attached to the
    // taxon — ordered by gazetteer then area for stable geographic
    // grouping. Each item is an apiDistribution {id, taxon_id, area,
    // area_id, gazetteer, status, source_id, reference_id, remarks,
    // modified, modified_by, issue_count}.
    distributions: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/distributions`),
    createDistribution: (id, body) =>
      j("POST", `/api/taxon/${encodeURIComponent(id)}/distributions`, body),
    // speciesInteractions returns the interactions where this taxon is
    // the SUBJECT, ordered by type then related-taxon name. Each item
    // is an apiSpeciesInteraction {id, taxon_id, related_taxon_id,
    // related_taxon_label {text, html}, related_taxon_scientific_name,
    // type, source_id, reference_id, remarks, modified, modified_by,
    // issue_count}. Rows where this taxon is the OBJECT belong on the
    // other taxon's page.
    speciesInteractions: (id) =>
      j("GET", `/api/taxon/${encodeURIComponent(id)}/species-interactions`),
    createSpeciesInteraction: (id, body) =>
      j("POST", `/api/taxon/${encodeURIComponent(id)}/species-interactions`, body),
  },

  // Vernacular row lives under its own path once created — PATCH and
  // DELETE address it by rowid (opaque string per CLAUDE.md's ID rule).
  // PATCH follows the standard hive pattern: nil pointer = leave
  // alone, set-to-zero-value = clear this field. Reparenting is not
  // supported (delete + re-add on the new taxon).
  vernacular: {
    patch: (id, patch) =>
      j("PATCH", `/api/vernacular/${encodeURIComponent(id)}`, patch),
    delete: (id) => j("DELETE", `/api/vernacular/${encodeURIComponent(id)}`),
  },

  // Same pattern as vernacular — distribution rows live at their own
  // path once created (rowid handle since sfga distribution has no
  // col__id). Reparenting is not supported (delete + re-add on the
  // new taxon).
  distribution: {
    patch: (id, patch) =>
      j("PATCH", `/api/distribution/${encodeURIComponent(id)}`, patch),
    delete: (id) => j("DELETE", `/api/distribution/${encodeURIComponent(id)}`),
  },

  // Same pattern as vernacular / distribution. Species interactions
  // live at their own path once created (rowid handle since sfga's
  // species_interaction has no col__id). Reparenting is not supported
  // (delete + re-add on the new subject taxon).
  speciesInteraction: {
    patch: (id, patch) =>
      j("PATCH", `/api/species-interaction/${encodeURIComponent(id)}`, patch),
    delete: (id) =>
      j("DELETE", `/api/species-interaction/${encodeURIComponent(id)}`),
  },

  name: {
    get: (id) => j("GET", `/api/name/${encodeURIComponent(id)}`),
    search: (opts) => j("GET", `/api/name/search${qs(opts)}`),
    patch: (id, patch, ifMatch) =>
      j("PATCH", `/api/name/${encodeURIComponent(id)}`, patch, ifMatch ? { "If-Match": ifMatch } : {}),
    // parse runs gnparser server-side and returns an unsaved apiName
    // preview — atomized col__ fields + gn__* cache + code-scoped rank
    // guess. Drives the two-step name-add form.
    parse: (scientific_name, code) =>
      j("POST", `/api/name/parse`, { scientific_name, code }),
    // dependencies returns counts of taxa / synonyms / name_relations
    // still pointing at the name. Zero across the board = the name is
    // a "bare name" and safe to cascade-delete. Used by the WUI
    // synonym-delete flow before offering the "delete name too" option.
    dependencies: (id) =>
      j("GET", `/api/name/${encodeURIComponent(id)}/dependencies`),
    // relations returns every name_relation row where {id} is the
    // subject or object. Each item is an apiNameRelation with a
    // rowid-stringified handle in `id`, the counterpart's rendered
    // label in `related_name`, and a `direction` marker so callers
    // know which side of the relation the URL name is on.
    relations: (id) =>
      j("GET", `/api/name/${encodeURIComponent(id)}/relations`),
    createRelation: (id, body) =>
      j("POST", `/api/name/${encodeURIComponent(id)}/relations`, body),
  },

  // Name-relation rows live at their own path once created. Delete-
  // only — the composite (name_id, related_name_id, type) is the row's
  // identity, so "changing the type" is delete + create rather than
  // an update.
  nameRelation: {
    delete: (id) =>
      j("DELETE", `/api/name-relation/${encodeURIComponent(id)}`),
  },

  // Synonym row lives under its own path once created. Delete-only
  // for now — synonym CREATE goes via /api/taxon/{id}/synonym (which
  // creates both the name and the synonym link atomically). The
  // cascade_name query param, when true, also DeleteNames the
  // underlying name row in the same tx — refuses (ErrConflict) if
  // any other row still references it.
  synonym: {
    delete: (id, opts = {}) => {
      const params = opts.cascadeName ? "?cascade_name=true" : "";
      return j("DELETE", `/api/synonym/${encodeURIComponent(id)}${params}`);
    },
    // move reassigns the synonym row to a different accepted taxon.
    // In-place update — synonym.col__id stays stable so an audit log
    // reference to this row survives. Curator's own taxon-detail view
    // may need to re-navigate if this was the only reason they were
    // viewing the source taxon; the caller decides.
    move: (id, newTaxonID) =>
      j("POST", `/api/synonym/${encodeURIComponent(id)}/move`, {
        new_taxon_id: newTaxonID,
      }),
  },

  // Issues (__gsvalidator_results). summary returns per-rule/severity
  // counts across the archive; list returns a paginated page of issues
  // with the flagged record's label + a link_taxon_id navigation hint
  // pre-resolved server-side so the row renders in one round-trip.
  //
  // reindex re-runs every rule and rewrites __gsvalidator_results.
  // Backfills legacy rows, repairs the cache after rule changes, and
  // prunes issues from removed rules. Synchronous — small archives
  // return in milliseconds. See PLANNING.md § Long-running operations
  // for the eventual SSE progress plan.
  issue: {
    summary: () => j("GET", "/api/issue/summary"),
    list: (opts) => j("GET", `/api/issue${qs(opts)}`),
    reindex: () => j("POST", "/api/reindex/validation"),
  },

  // Agent role tables. sfga stores dataset metadata people (creators,
  // contacts, editors, contributors, publishers) as five parallel
  // tables with identical column shape; hive treats them uniformly
  // and parameterizes on role. See pkg/role.go for the storage
  // layer and internal/server/agent.go for the handler set.
  agent: {
    list: (role) => j("GET", `/api/agent/${encodeURIComponent(role)}`),
    get: (role, id) =>
      j("GET", `/api/agent/${encodeURIComponent(role)}/${encodeURIComponent(id)}`),
    create: (role, body) =>
      j("POST", `/api/agent/${encodeURIComponent(role)}`, body),
    patch: (role, id, patch) =>
      j("PATCH", `/api/agent/${encodeURIComponent(role)}/${encodeURIComponent(id)}`, patch),
    delete: (role, id) =>
      j("DELETE", `/api/agent/${encodeURIComponent(role)}/${encodeURIComponent(id)}`),
    // copy creates a new agent in `toRole` from (fromRole, id).
    // blankNote defaults to true on the server; caller may pass
    // false to preserve the source note when it applies uniformly
    // across the two roles.
    copy: (fromRole, id, toRole, blankNote) =>
      j(
        "POST",
        `/api/agent/${encodeURIComponent(fromRole)}/${encodeURIComponent(id)}/copy`,
        { to_role: toRole, blank_note: blankNote },
      ),
    // move reassigns an agent to a different role atomically: creates
    // the row in toRole and deletes the source in one transaction.
    // Use when a person was filed under the wrong role from the start
    // (copy leaves the source in place; move relocates it).
    move: (fromRole, id, toRole, blankNote) =>
      j(
        "POST",
        `/api/agent/${encodeURIComponent(fromRole)}/${encodeURIComponent(id)}/move`,
        { to_role: toRole, blank_note: blankNote },
      ),
  },

  // LLM invocation + provider management. Curator configures
  // providers once (either by hand in ~/.config/sfborg/hive/config.yml
  // or via api.llm.providers.create() which stores the API key in
  // the OS keyring — Keychain / DPAPI / Secret Service), then any
  // number of prompt runs flow through /api/llm/invoke and get
  // persisted as hive__extraction_run rows for per-paper multi-model
  // comparison and cost accountability.
  llm: {
    providers: {
      list: () => j("GET", "/api/llm/providers"),
      // create pastes an API key to the server, which writes it to
      // the OS keyring and adds a {source: keyring, value: <name>}
      // credential entry to the user config. The key never touches
      // the config file — only the label (the provider name) does.
      // Response shape: apiLLMProvider {name, kind, default_model}.
      create: (body) => j("POST", "/api/llm/providers", body),
      // models fetches the provider's models-listing endpoint
      // (server-side proxy so the API key stays out of the
      // browser). Cached per-server for one hour so repeated
      // invoke-modal opens don't hammer the upstream. Returns
      // {items: [{id, display_name?, context_window?,
      // max_output_tokens?, listed_price?: {input_per_token,
      // output_per_token}}], pricing_source?}. listed_price is the
      // provider's own published rate, passed through verbatim;
      // pricing_source names the API host it came from. Pass
      // {fresh: 1} to bypass the server cache.
      // 501 when the underlying adapter doesn't support models
      // listing — WUI should fall back to a free-text override.
      models: (name, opts) =>
        j("GET", `/api/llm/providers/${encodeURIComponent(name)}/models${qs(opts)}`),
      // patch applies partial updates to a provider. Only
      // non-empty fields in the body take effect. Rules for
      // credential handling live in the server-side doc for
      // apiLLMProviderPatchRequest — TL;DR: swap source =
      // requires the new source's key material; keep source +
      // supply APIKey = rotate stored value; keep source + no
      // APIKey = leave credential alone.
      patch: (name, body) =>
        j("PATCH", `/api/llm/providers/${encodeURIComponent(name)}`, body),
      // delete removes the provider from config and deletes its
      // keyring entry. Idempotent — 204 whether the provider
      // existed or not.
      delete: (name) => j("DELETE", `/api/llm/providers/${encodeURIComponent(name)}`),
    },
    // estimate returns the approximate token count + context-window
    // check for a would-be invocation, so the WUI can disable submit
    // when the input would exceed the model's context window. Token
    // counts only; hive never computes dollar costs. See
    // apiLLMEstimateResponse on the server for the full field set.
    estimate: (body) => j("POST", "/api/llm/estimate", body),
    // invoke runs the prompt via the selected provider and persists
    // a hive__extraction_run row. Response shape:
    // {run_id, envelope, persist_error?}. persist_error is
    // populated when the LLM call succeeded but the run couldn't be
    // saved — the envelope is still returned so the curator sees
    // the paid-for result.
    invoke: (body) => j("POST", "/api/llm/invoke", body),
    // pipeline runs the two-step NER → compose flow in one call so
    // curators see it as a single "Run" action. Backend fetches
    // the paper's JATS body server-side from the reference's
    // attached PDF sidecar (curator never handles raw XML) and
    // orchestrates both steps, persisting a hive__extraction_run
    // row for each (compose linked to NER via parent_run_id).
    // Response: {ner_run, compose_run, tree, failed_stage?, error?}.
    pipeline: (body) => j("POST", "/api/llm/pipeline", body),
    run: {
      // listForReference returns every run associated with a
      // reference, most-recent first. includeDeleted brings back
      // soft-deleted rows for the cost dashboard.
      listForReference: (referenceID, opts = {}) =>
        j(
          "GET",
          `/api/reference/${encodeURIComponent(referenceID)}/llm-runs${qs({ include_deleted: opts.includeDeleted ? "1" : "" })}`,
        ),
      get: (id) => j("GET", `/api/llm/run/${encodeURIComponent(id)}`),
      // delete is soft — deleted_at gets stamped, row stays in DB
      // so cost dashboards keep visibility of paid-for spend.
      delete: (id) => j("DELETE", `/api/llm/run/${encodeURIComponent(id)}`),
      restore: (id) => j("POST", `/api/llm/run/${encodeURIComponent(id)}/restore`, {}),
      // setGoldStandard flips the run's ground-truth flag used by
      // sampling-based accuracy scoring. Gold runs anchor the
      // per-language F1 estimates.
      setGoldStandard: (id, gold) =>
        j("POST", `/api/llm/run/${encodeURIComponent(id)}/gold-standard`, { gold }),
    },
  },

  reference: {
    list: (opts) => j("GET", `/api/reference${qs(opts)}`),
    get: (id) => j("GET", `/api/reference/${encodeURIComponent(id)}`),
    // casing returns {words: {lower: form}}: how the reference's own
    // documents write capitalized words, for restoring fields supplied
    // in all capitals.
    casing: (id) => j("GET", `/api/reference/${encodeURIComponent(id)}/casing`),
    search: (opts) => j("GET", `/api/reference/search${qs(opts)}`),
    // colTaxa looks up higher taxa in the name-mention index for the
    // ncbi_parent / col_parent filters; opts.source is "ncbi" or
    // "col". Discovery data only. Returns {items: [{name, rank, papers}]}.
    colTaxa: (opts) => j("GET", `/api/name-mentions/taxa${qs(opts)}`),
    // taxonTree browses the same classification: opts {source, path,
    // depth, q, min_mentions}. Returns {items: [{path, name, rank,
    // papers, children, match}]} in depth-first order.
    taxonTree: (opts) => j("GET", `/api/name-mentions/tree${qs(opts)}`),
    // create optionally accepts an attach_pdf_sha which links the
    // newly-created reference to a sidecar-library sha in the same
    // transaction. Used by the reference-add modal when the curator
    // uploaded a PDF before completing the form.
    create: (body, attachPdfSha) =>
      j(
        "POST",
        `/api/reference${attachPdfSha ? qs({ attach_pdf_sha: attachPdfSha }) : ""}`,
        body,
      ),
    patch: (id, patch, ifMatch) =>
      j(
        "PATCH",
        `/api/reference/${encodeURIComponent(id)}`,
        patch,
        ifMatch ? { "If-Match": ifMatch } : {},
      ),
    resolveDOI: (doi) => j("GET", `/api/reference/resolve-doi${qs({ doi })}`),

    // Per-reference assertions — the staged per-fact rows
    // materialized from an LLM (or curator) extraction run.
    // Query filters: status[], source_run_id, asserted_by.
    // Backs the Assertions-tab list and the paper-level
    // comparison view. Empty items array when no runs have
    // landed yet — WUI shows an empty-state call-to-action.
    assertions: (id, opts = {}) =>
      j("GET", `/api/reference/${encodeURIComponent(id)}/assertions${qs(opts)}`),

    // Test-set membership — the "is this paper part of the
    // evaluation set?" per-reference flag. Presence of a
    // hive__reference_test_set row = in the set; absence = not.
    // Response shape for getTestSet: {in_test_set, added_at?, added_by?, notes?}.
    // Backs the toolbar checkbox on the reference detail page.
    //
    // Path is /api/test-set/{ref_id} (not /api/reference/{id}/test-set)
    // to avoid stdlib mux ambiguity with existing correction routes.
    getTestSet: (id) =>
      j("GET", `/api/test-set/${encodeURIComponent(id)}`),
    addToTestSet: (id, notes) =>
      j("POST", `/api/test-set/${encodeURIComponent(id)}`,
        notes ? { notes } : {}),
    removeFromTestSet: (id) =>
      j("DELETE", `/api/test-set/${encodeURIComponent(id)}`),
    delete: (id) => j("DELETE", `/api/reference/${encodeURIComponent(id)}`),
    attachPDF: (id, sha512) =>
      j("POST", `/api/reference/${encodeURIComponent(id)}/attach-pdf`, { sha512 }),
    regenerateCitations: (overwrite) =>
      j("POST", `/api/reference/regenerate-citations${qs({ overwrite: overwrite ? "true" : "" })}`, {}),
    // lookupOpenAlex searches OpenAlex by extracted metadata (title +
    // optional author surname + optional year) for a paper that has
    // no DOI printed on it. Used to surface publisher-backfill and
    // BHL-DataCite DOIs that got assigned after the copy the curator
    // has was printed.
    lookupOpenAlex: (opts) =>
      j("GET", `/api/reference/lookup-openalex${qs(opts)}`),

    // Paper-import assembly mappings — one row per paper mention
    // → target taxon UUID. Backs the Assembly workspace tab's
    // drag-and-drop / picker-based flow.
    listImportMappings: (referenceID) =>
      j("GET", `/api/reference/${encodeURIComponent(referenceID)}/paper-import`),
    upsertImportMapping: (referenceID, body) =>
      j("POST", `/api/reference/${encodeURIComponent(referenceID)}/paper-import`, body),
    deleteImportMapping: (referenceID, paperNameKey) =>
      j("DELETE", `/api/reference/${encodeURIComponent(referenceID)}/paper-import/${encodeURIComponent(paperNameKey)}`),
    executeImport: (referenceID, extractionRunID) =>
      j("POST",
        `/api/reference/${encodeURIComponent(referenceID)}/paper-import/execute`,
        { extraction_run_id: extractionRunID }),
    lookupBHLnames: (body) => j("POST", `/api/reference/lookup-bhlnames`, body),
    // parseBibTeX sends the raw entry with the vendor MIME type — the
    // handler accepts either form, and the raw path avoids a JSON escape
    // round-trip when curators paste multi-line entries with backslashes.
    parseBibTeX: async (text) => {
      const res = await fetch("/api/reference/parse-bibtex", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-bibtex",
        },
        body: text,
      });
      if (!res.ok) {
        const mime = res.headers.get("content-type") || "";
        if (mime.includes(problemMime)) {
          throw new Problem(await res.json(), res.status);
        }
        throw new Problem(
          { title: res.statusText || "http error", status: res.status },
          res.status,
        );
      }
      return res.json();
    },
    // upload streams a File object (from a drag-drop event or
    // <input type="file">) to /api/reference/upload as multipart
    // form data. Response shape: apiReferenceUpload
    // {sha512, ext, size, already_existed, tei_status, doi}.
    // Bypasses the shared j() helper because it constructs a JSON
    // body; multipart uploads need FormData without a
    // Content-Type override (the browser sets the boundary).
    upload: async (file) => {
      const fd = new FormData();
      fd.append("file", file);
      const res = await fetch("/api/reference/upload", {
        method: "POST",
        body: fd,
      });
      if (!res.ok) {
        const mime = res.headers.get("content-type") || "";
        if (mime.includes(problemMime)) {
          throw new Problem(await res.json(), res.status);
        }
        throw new Problem(
          { title: res.statusText || "http error", status: res.status },
          res.status,
        );
      }
      return res.json();
    },
  },

  // Prompt library — shipped built-ins from pkg/prompts/ merged
  // with curator forks stored in hive__prompt. list() returns
  // the merged list (built-ins + curator forks), get() fetches
  // content for one prompt (by shipped name or curator UUID),
  // create() forks, patch() edits a curator fork's content,
  // remove() soft-deletes.
  prompt: {
    // list options: {purpose?: "ner"|"compose", include_content?: bool}
    // Content is omitted by default so the picker payload stays
    // lean; opt in with include_content=true when the caller
    // wants to inline preview panes.
    list: (opts = {}) => {
      const params = { purpose: opts.purpose || "" };
      if (opts.include_content) params.include_content = "1";
      return j("GET", `/api/prompt${qs(params)}`);
    },
    get: (id) => j("GET", `/api/prompt/${encodeURIComponent(id)}`),
    create: (body) => j("POST", `/api/prompt`, body),
    patch: (id, content) =>
      j("PATCH", `/api/prompt/${encodeURIComponent(id)}`, { content }),
    remove: (id) =>
      j("DELETE", `/api/prompt/${encodeURIComponent(id)}`),
  },

  // Individual staged assertions. reference.assertions() lists them
  // in bulk; this namespace covers per-row curator actions.
  assertion: {
    // patchStatus flips one assertion's review status. Valid values:
    // pending | accepted | rejected | edited. reject_reason is
    // required by the backend when status === "rejected" — pass an
    // empty string for other transitions and the server ignores it.
    // Response echoes the updated row so callers can hydrate local
    // state without a re-list.
    patchStatus: (id, status, rejectReason = "") =>
      j(
        "PATCH",
        `/api/assertion/${encodeURIComponent(id)}/status`,
        { status, reject_reason: rejectReason },
      ),

    // correct records a curator fix to the NER value of one
    // assertion — additive, not destructive: the model's original
    // extraction stays on the assertion row; the correction lands
    // as a separate hive__extraction_correction record so both
    // signals are available as training data.
    //
    // corrected: the truth value the curator wants persisted
    // field:    the compose-output slot name this fix targets
    //           (e.g. "verbatimDate") — optional; blank when the
    //           assertion type unambiguously identifies the field
    // notes:    curator rationale — optional
    correct: (id, corrected, field = "", notes = "") =>
      j(
        "POST",
        `/api/assertion/${encodeURIComponent(id)}/correction`,
        { corrected, field, notes },
      ),
  },
};
