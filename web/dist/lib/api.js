/**
 * hive PWA API client.
 *
 * The only place in the PWA that talks to /api/*. Components take data as
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

/** Query-string helper: skips undefined/null values. */
function qs(params = {}) {
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
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
  health: () => j("GET", "/api/health"),
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

  taxon: {
    roots: (opts) => j("GET", `/api/taxon/roots${qs(opts)}`),
    get: (id) => j("GET", `/api/taxon/${encodeURIComponent(id)}`),
    children: (id, opts) => j("GET", `/api/taxon/${encodeURIComponent(id)}/children${qs(opts)}`),
    synonyms: (id) => j("GET", `/api/taxon/${encodeURIComponent(id)}/synonyms`),
    ancestors: (id) => j("GET", `/api/taxon/${encodeURIComponent(id)}/ancestors`),
    codeDefault: (parentId) =>
      j("GET", `/api/taxon/${encodeURIComponent(parentId)}/code-default`),
    search: (opts) => j("GET", `/api/taxon/search${qs(opts)}`),
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
    delete: (id) => j("DELETE", `/api/taxon/${encodeURIComponent(id)}`),
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
  },

  reference: {
    list: (opts) => j("GET", `/api/reference${qs(opts)}`),
    get: (id) => j("GET", `/api/reference/${encodeURIComponent(id)}`),
    search: (opts) => j("GET", `/api/reference/search${qs(opts)}`),
    create: (body) => j("POST", `/api/reference`, body),
    resolveDOI: (doi) => j("GET", `/api/reference/resolve-doi${qs({ doi })}`),
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
  },
};
