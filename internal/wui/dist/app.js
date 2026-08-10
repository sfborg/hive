/**
 * hive WUI — walking-skeleton entry point.
 *
 * Renders a two-pane layout: expandable taxon tree on the left, detail
 * view on the right. Data flows through /lib/api.js only — no fetch calls
 * anywhere in this file, and components take data as properties instead
 * of loading it themselves.
 *
 * Everything is defined as a Custom Element and registered with the
 * platform. No build step, no bundler; the browser resolves the imports.
 */

import { LitElement, html, css, svg, unsafeHTML } from "/vendor/lit-3.x.x.min.js";
import { api, Problem } from "/lib/api.js";

// ---------- icons ----------
// Inline SVG shapes hand-picked from Lucide (MIT license, lucide.dev).
// The `svg` template tag from Lit (not `html`) is required for these
// inner shapes so <rect>/<path>/<circle> are parsed in the SVG
// namespace — otherwise they render as HTMLUnknownElement and stay
// invisible inside the <svg> wrapper. Zero runtime deps, currentColor
// inherits theme, sharp at any size.
//
// When adding a view, pick a lucide icon at lucide.dev, grab the
// inner shapes from "Show as SVG", and add here using `svg` tag.
const iconPaths = {
  // network — nested nodes / hierarchy → Taxa tree
  network: svg`
    <rect x="16" y="16" width="6" height="6" rx="1" />
    <rect x="2" y="16" width="6" height="6" rx="1" />
    <rect x="9" y="2" width="6" height="6" rx="1" />
    <path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3" />
    <path d="M12 12V8" />
  `,
  // info — circled "i" → Metadata
  info: svg`
    <circle cx="12" cy="12" r="10" />
    <path d="M12 16v-4" />
    <path d="M12 8h.01" />
  `,
  // panel-left-close / panel-left-open — sidebar toggle affordances
  "panel-left-close": svg`
    <rect width="18" height="18" x="3" y="3" rx="2" />
    <path d="M9 3v18" />
    <path d="m16 15-3-3 3-3" />
  `,
  "panel-left-open": svg`
    <rect width="18" height="18" x="3" y="3" rx="2" />
    <path d="M9 3v18" />
    <path d="m14 9 3 3-3 3" />
  `,
  // sun / moon / monitor — theme toggle. Monitor icon indicates
  // "follows system"; sun and moon indicate explicit choices.
  sun: svg`
    <circle cx="12" cy="12" r="4" />
    <path d="M12 2v2" />
    <path d="M12 20v2" />
    <path d="m4.93 4.93 1.41 1.41" />
    <path d="m17.66 17.66 1.41 1.41" />
    <path d="M2 12h2" />
    <path d="M20 12h2" />
    <path d="m6.34 17.66-1.41 1.41" />
    <path d="m19.07 4.93-1.41 1.41" />
  `,
  moon: svg`
    <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z" />
  `,
  monitor: svg`
    <rect width="20" height="14" x="2" y="3" rx="2" />
    <line x1="8" x2="16" y1="21" y2="21" />
    <line x1="12" x2="12" y1="17" y2="21" />
  `,
  // book — open book silhouette → References
  book: svg`
    <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z" />
    <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z" />
  `,
  // help-circle — circled question mark → open the keyboard help modal
  "help-circle": svg`
    <circle cx="12" cy="12" r="10" />
    <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
    <path d="M12 17h.01" />
  `,
  // pencil — edit the selected taxon
  pencil: svg`
    <path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z" />
    <path d="m15 5 4 4" />
  `,
  // trash-2 — delete the selected taxon (with confirm)
  "trash-2": svg`
    <path d="M3 6h18" />
    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
    <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
    <line x1="10" x2="10" y1="11" y2="17" />
    <line x1="14" x2="14" y1="11" y2="17" />
  `,
};

// iconPathsFilled — icons that render with fill instead of stroke.
// Sourced from TaxonWorks (SpeciesFileGroup/taxonworks, MIT license,
// same research group). Kept in a separate map from iconPaths so the
// Lucide stroke set stays a single-style family; renderIcon picks the
// right wrapper per icon.
//
// Paths are transcribed verbatim from taxonworks/app/assets/images
// with only cosmetic changes: the original `fill="#FFFFFF"` gets
// dropped so `currentColor` on the outer <svg> flows through. Each
// entry declares its own viewBox because TW's icons ship in a mix of
// authoring scales (create_* is 111.32×111.32; w_pencil is 12×12).
//
// The viewBox for the create_* icons is expanded ~13% beyond the
// original 0 0 111.32 111.32 authoring bounds to add inner padding.
// TW authored these to fill their canvas; when rendered next to
// Lucide stroke icons at the same pixel size they read as visually
// heavier. Adding whitespace inside the viewBox shrinks the drawn
// content relative to the button footprint, evening out the weight.
const iconPathsFilled = {
  // create_child_icon.svg — parent circle top-left, child circle
  // bottom-right, plus sign top-right, L-shape connector.
  // Represents "add a new child under this taxon."
  "tree-child-plus": {
    viewBox: "-14 -14 139.32 139.32",
    paths: svg`
      <circle cx="23.73" cy="23.38" r="23.38" />
      <circle cx="87.44" cy="87.94" r="23.38" />
      <path d="M82.56,46.75V29.87H65.54V18.21h17.02V1.33H93.9v16.88h17.06v11.66H93.9v16.88H82.56z" />
      <path d="M57.72,79.68H31.27V53.28c-2.42,0.61-4.93,0.97-7.54,0.97c-2.71,0-5.33-0.39-7.84-1.04v41.85h41.53
        c-0.54-2.29-0.86-4.66-0.86-7.12C56.56,85.07,56.98,82.31,57.72,79.68z" />
    `,
  },
  // create_sister_icon.svg — left vertical trunk, two circles stacked
  // to its right with horizontal connectors, plus sign far right.
  // Represents "add a new sister at the same tree level."
  "tree-sister-plus": {
    viewBox: "-14 -14 139.32 139.32",
    paths: svg`
      <circle cx="49.44" cy="24.95" r="20.95" />
      <circle cx="49.44" cy="83.49" r="20.95" />
      <path d="M85.64,74.39V59.26H70.39V48.81h15.25V33.68h10.17v15.13h15.29v10.45H95.81v15.13H85.64z" />
      <rect x="0.21" y="17.68" width="13.79" height="72.7" />
      <path d="M21.57,24.95c0-2.52,0.36-4.95,0.99-7.27H0.21v13.79h22.15C21.87,29.37,21.57,27.2,21.57,24.95z" />
      <path d="M22.46,76.6H0.21v13.79h22.25c-0.56-2.21-0.89-4.51-0.89-6.89C21.57,81.11,21.9,78.81,22.46,76.6z" />
    `,
  },
};

// readAtomizedPref / writeAtomizedPref persist the "show atomized
// fields" toggle across taxa and sessions. Some curators always want
// to see the parser's atomization to verify edge cases; forcing them
// to re-toggle it on every navigation is user-hostile. Single boolean
// key covers both the edit form and the create form's step-1 preview
// since a curator wanting one usually wants the other.
function readAtomizedPref() {
  return localStorage.getItem("hive-show-atomized") === "true";
}
function writeAtomizedPref(v) {
  localStorage.setItem("hive-show-atomized", v ? "true" : "false");
}

// matchesKey reports whether a DOM KeyboardEvent matches one of the
// key strings from the shared keymap (pkg/ui.Shortcut.Keys["wui"]).
// Format is either a bare KeyboardEvent.key value ("ArrowUp", "g",
// "?") or a "modifier+key" combination ("alt+t", "ctrl+s"). Shift
// is implicit in the key value itself (KeyboardEvent.key already
// returns the shifted character for shifted letters like "G" and
// "?"), so we only check alt/ctrl/meta explicitly. Kept as a
// module-level helper because both the app shell (global dispatch)
// and the tree component (tree-scoped dispatch) need it.
function matchesKey(e, keyStr) {
  const parts = keyStr.split("+");
  const key = parts[parts.length - 1];
  const mods = new Set(parts.slice(0, -1));
  if (e.key !== key) return false;
  if (mods.has("alt") !== !!e.altKey) return false;
  if (mods.has("ctrl") !== !!e.ctrlKey) return false;
  if (mods.has("meta") !== !!e.metaKey) return false;
  return true;
}

// actionForEvent walks a scope-filtered shortcut list and returns
// the Action string of the first entry whose WUI keys match the
// event, or null if none. The shared keymap lives in memory (loaded
// by api.keymap.load() at boot) so this is a cheap in-process match.
function actionForEvent(shortcuts, e) {
  for (const s of shortcuts) {
    const keys = s.keys?.wui || [];
    for (const k of keys) {
      if (matchesKey(e, k)) return s.action;
    }
  }
  return null;
}

// renderIcon wraps a named icon in a properly-sized <svg>. Size is a
// number (rendered as square width/height). currentColor lets the CSS
// context set the color, so theme flips just work.
//
// Two families of icons: Lucide stroke set (iconPaths) rendered with
// fill=none stroke=currentColor, and the TaxonWorks-sourced filled
// set (iconPathsFilled) rendered with fill=currentColor. The filled
// set declares its own viewBox because TW authored at various scales;
// stroke icons all share the standard Lucide 24×24 grid.
function renderIcon(name, size = 20) {
  const filled = iconPathsFilled[name];
  if (filled) {
    return html`
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width=${size}
        height=${size}
        viewBox=${filled.viewBox}
        fill="currentColor"
      >
        ${filled.paths}
      </svg>
    `;
  }
  const inner = iconPaths[name];
  if (!inner) return "";
  return html`
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width=${size}
      height=${size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      ${inner}
    </svg>
  `;
}

// renderLabel returns the appropriate lit template for a server-rendered
// Label ({text, html}) — using unsafeHTML when the html form is present so
// dagger + italics render, falling back to the plain text. The server
// escapes user-supplied strings inside label.html (see core.BuildLabel),
// so unsafeHTML is safe here as long as label originates from our API.
function renderLabel(label, fallback = "") {
  if (!label) return fallback;
  if (label.html) return html`${unsafeHTML(label.html)}`;
  return label.text || fallback;
}

// severityChip renders a small pill for a gsvalidator result severity.
// Color comes from --sev-* CSS custom properties (traffic-light default,
// swap via html[data-severity-palette="cvd"]). A leading glyph carries
// the signal too so severity is legible under color loss.
const SEV_META = {
  error: { glyph: "✕",  label: "error" },  // ✕
  warn:  { glyph: "⚠",  label: "warn"  },  // ⚠
  info:  { glyph: "ⓘ",  label: "info"  },  // ⓘ
  debug: { glyph: "🐛", label: "debug" },  // 🐛
};
function severityChip(severity) {
  const key = (severity || "warn").toLowerCase();
  const meta = SEV_META[key] || SEV_META.warn;
  return html`<span class="sev-chip sev-${key}"
    ><span class="sev-glyph">${meta.glyph}</span>${meta.label}</span
  >`;
}

// orcidLink turns a bare ORCID iD (0000-0002-1825-0097) into an anchor to
// orcid.org. Non-ORCID scrutinizer strings fall through unchanged.
const orcidRE = /^\d{4}-\d{4}-\d{4}-\d{3}[\dX]$/;
function orcidLink(value) {
  if (!value) return "";
  if (!orcidRE.test(value)) return value;
  return html`<a href="https://orcid.org/${value}" target="_blank" rel="noopener">${value}</a>`;
}

// renderStars produces a filled/empty star row plus a numeric readout.
// Used for the metadata Confidence 1-5 rating. Values clamp into
// [0, max]. CSS class .star-filled gets the accent color; .star-empty
// stays dim. Unicode chars — no icon library.
function renderStars(value, max) {
  const v = Math.max(0, Math.min(max, Number(value) || 0));
  return html`<span class="stars"
    ><span class="star-filled">${"★".repeat(v)}</span
    ><span class="star-empty">${"☆".repeat(max - v)}</span>
    <span class="star-count">${v} / ${max}</span></span
  >`;
}

// ---------- combobox source adapters ----------
// The <sfga-combobox> component takes a `source(query) → results[]` function.
// These helpers build sources for the two flavors of picker hive uses:
//   * `vocabSource("rank")` — filters the cached api.vocab bundle in memory.
//   * `taxonSource`         — hits /api/taxon/search with a debounce (the
//                             combobox itself debounces server-search sources).
// Both return items shaped {id, name} matching what the combobox expects.

// vocabSource(name) returns a source function that filters the named
// controlled vocabulary. Empty query → all terms (select-like UX for
// small vocabs like nom_code / gender).
function vocabSource(name) {
  return (q) => {
    const terms = api.vocab.get(name);
    const needle = (q || "").toLowerCase().trim();
    const filtered = needle
      ? terms.filter(
          (t) =>
            (t.name || "").toLowerCase().includes(needle) ||
            (t.id || "").toLowerCase().includes(needle),
        )
      : terms;
    return filtered.map((t) => ({
      id: t.id,
      name: t.name || t.id || "(unset)",
    }));
  };
}

// vocabResolver(name) resolves a vocab ID → display name synchronously
// (Promise wrap-around for a uniform combobox interface).
function vocabResolver(name) {
  return async (id) => {
    const terms = api.vocab.get(name);
    const t = terms.find((t) => t.id === id);
    return t ? t.name || t.id : id;
  };
}

// childRankSource restricts the rank combobox to the ranks
// pkg/ui.ValidChildRanks says are valid children of the current
// create's parent. Behavior mirrors the TUI's childRankComboSource:
//   - null / empty allow list → fall through to vocabSource("rank"),
//     i.e. no filter (root taxon or code unknown).
//   - Empty query → return only typical_use ranks (subgenus,
//     species, etc.).
//   - Non-empty query → return every allowed rank whose ID or name
//     matches. Typing widens without needing a "show all" button.
//
// allowed is a list of {id, typical_use} objects as returned by
// /api/taxon/{id}/child-ranks.
function childRankSource(allowed) {
  const base = vocabSource("rank");
  if (!allowed || allowed.length === 0) return base;
  const allowSet = new Set(allowed.map((r) => r.id));
  const typicalSet = new Set(allowed.filter((r) => r.typical_use).map((r) => r.id));
  return async (q) => {
    const all = await base(q);
    const needle = (q || "").toLowerCase().trim();
    return all.filter((t) => {
      if (!allowSet.has(t.id)) return false;
      if (!needle) return typicalSet.has(t.id);
      return true;
    });
  };
}

// nomenSource(codeID) returns a combobox source that filters NOMEN
// terms by the current name's nomenclatural code. Empty codeID → all
// terms. Matches against label or short local identifier.
function nomenSource(codeID) {
  return (q) => {
    const scoped = api.nomen.filterByCode(codeID);
    const needle = (q || "").toLowerCase().trim();
    const filtered = needle
      ? scoped.filter(
          (t) =>
            (t.label || "").toLowerCase().includes(needle) ||
            (t.local || "").toLowerCase().includes(needle),
        )
      : scoped;
    return filtered.map((t) => ({ id: t.id, name: t.label }));
  };
}

// nomenResolver looks up a stored NOMEN URI's label. Legacy CoLDP-
// generalized values (ESTABLISHED, ACCEPTABLE, …) resolve via the
// nom_status vocab fallback.
async function nomenResolver(uri) {
  const label = api.nomen.labelFor(uri);
  if (label) return label;
  return vocabResolver("nom_status")(uri);
}

// referenceSource — combobox source that hits /api/reference/search
// (server-side substring across author/title/citation). Each match
// renders as "Author (Year) Title" so the picker line reads like a
// citation. Empty query returns [] to skip flashing the whole list.
async function referenceSource(q) {
  if (!q || q.length < 2) return [];
  try {
    const page = await api.reference.search({ q, limit: 20 });
    return (page.items || []).map((h) => ({
      id: h.id,
      name: referenceHitLabel(h),
    }));
  } catch (_) {
    return [];
  }
}

// referenceResolver — id → display label. The name-detail response
// server-populates `reference_label` alongside `reference_id`, so most
// callers can skip this and hydrate the combobox from the already-
// present label. Kept as a fallback for cases where only the id is
// available.
async function referenceResolver(id) {
  if (!id) return "";
  try {
    const r = await api.reference.get(id);
    return referenceHitLabel({
      author: r.author,
      year: r.issued ? String(r.issued).slice(0, 4) : "",
      title: r.title,
      citation: r.citation,
    });
  } catch (_) {
    return id;
  }
}

// referenceHitLabel composes the "Author (Year) Title" line used by
// both the picker source and the resolver. Falls back to citation or
// id when structured fields are missing.
// referenceLabelFor returns the reference-picker label text on the
// name-add form, sharpened to name what the curator's atomized
// authorship fields say the reference is for. Combination author or
// year set → "Reference for the combination (X, YYYY)". Basionym set
// but no combination → "Reference (basionym: X, YYYY)" (nudging the
// curator toward the basionym flow, since the ref is really for a
// separate name row). Neither → plain "Reference".
//
// The label points at whatever the curator just typed, so it's hard
// to mistake which combination the reference is being attached to.
function referenceLabelFor(d) {
  const combA = (d.combination_authorship || "").trim();
  const combY = (d.combination_authorship_year || "").trim();
  const basA = (d.basionym_authorship || "").trim();
  const basY = (d.basionym_authorship_year || "").trim();
  const fmt = (a, y) =>
    a && y ? `${a}, ${y}` : a || y || "";
  if (combA || combY) {
    const who = fmt(combA, combY);
    return `Reference for the combination${who ? ` (${who})` : ""}`;
  }
  if (basA || basY) {
    const who = fmt(basA, basY);
    return `Reference${who ? ` (basionym: ${who})` : ""}`;
  }
  return "Reference";
}

function referenceHitLabel(h) {
  const parts = [];
  if (h.author) parts.push(h.author);
  // Hits from /api/reference/search carry `year`; full apiReference
  // objects (from GET/POST /api/reference) carry `issued` in YYYY /
  // YYYY-MM / YYYY-MM-DD form. Accept either so callers don't need
  // to reshape before labeling.
  const year = h.year || (h.issued ? h.issued.slice(0, 4) : "");
  if (year) parts.push(`(${year})`);
  if (h.title) parts.push(h.title);
  if (parts.length === 0) return h.citation || h.id || "";
  return parts.join(" ");
}

// taxonSource hits the server. Uses the server-rendered label.text so
// dagger + authorship formatting stays consistent with the tree.
async function taxonSource(q) {
  if (!q || q.length < 2) return [];
  try {
    const page = await api.taxon.search({ q, limit: 20 });
    return (page.items || []).map((hit) => ({
      id: hit.id,
      name: hit.label?.text || hit.name,
    }));
  } catch (_) {
    return [];
  }
}

// taxonResolver fetches a taxon and returns its server-rendered label text.
// The label already includes dagger + authorship; combobox uses plain text
// for the input value so no HTML in the field.
async function taxonResolver(id) {
  try {
    const t = await api.taxon.get(id);
    return t.label?.text || id;
  } catch (_) {
    return "(lookup failed)";
  }
}

// ---------- <sfga-app> ----------
// Root shell. Owns the selected-taxon ID, propagates it into the detail
// pane, and holds the archive header. Delegates rendering of each pane to
// its dedicated element.

class SfgaApp extends LitElement {
  static properties = {
    archive: { state: true },
    metadata: { state: true },
    selectedId: { state: true },
    error: { state: true },
    theme: { state: true },
    screen: { state: true },
    sidebarCollapsed: { state: true },
    helpOpen: { state: true },
    // Warnings from the most recent create/update, if any. Cleared
    // when the selection moves to a different taxon. See _onMoved.
    _pendingWarnings: { state: true },
  };

  // View list — matches CLAUDE.md § keybinding conventions and the
  // TUI's screen enum. New views append here; the sidebar and shortcut
  // handler pick them up automatically. Icon is a Lucide icon name
  // registered in the iconPaths map above.
  static views = [
    { id: "taxa", label: "Taxa", icon: "network", key: "t" },
    { id: "metadata", label: "Metadata", icon: "info", key: "m" },
    { id: "references", label: "References", icon: "book", key: "r" },
  ];

  static styles = css`
    :host {
      display: grid;
      grid-template-rows: auto 1fr;
      height: 100vh;
      color: var(--fg);
      background: var(--bg);
    }
    header {
      border-bottom: 1px solid var(--border);
      padding: 0.5rem 0.75rem;
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 1rem;
    }
    header .title {
      font-weight: 600;
    }
    header .meta {
      color: var(--dim);
      font-size: 0.9em;
      font-family: var(--font-mono);
    }
    header .header-buttons {
      display: inline-flex;
      align-items: center;
      gap: 0.35rem;
    }
    button.theme-toggle {
      background: transparent;
      color: var(--fg);
      border: 1px solid var(--border);
      padding: 0.25rem;
      font-family: inherit;
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      line-height: 1;
    }
    button.theme-toggle:hover {
      border-color: var(--accent);
      color: var(--accent);
    }
    button.theme-toggle svg { display: block; }
    main {
      display: grid;
      grid-template-columns: auto 1fr;
      overflow: hidden;
    }
    /* Sidebar — icon list on the far left, collapsible to icons only.
       Expanded is wide enough for the labels; collapsed is a narrow
       strip of icons. Toggle sits at the bottom so it stays out of
       the way. */
    nav.sidebar {
      display: grid;
      grid-template-rows: 1fr auto;
      border-right: 1px solid var(--border);
      overflow: hidden;
      background: color-mix(in oklab, var(--bg) 96%, var(--fg));
    }
    nav.sidebar.expanded { width: 10rem; }
    nav.sidebar.collapsed { width: 3rem; }
    nav.sidebar ul {
      list-style: none;
      margin: 0;
      padding: 0.5rem 0;
      overflow-y: auto;
    }
    nav.sidebar li {
      padding: 0.35rem 0.75rem;
      display: flex;
      align-items: center;
      gap: 0.6rem;
      cursor: pointer;
      color: var(--fg);
      white-space: nowrap;
    }
    nav.sidebar li:hover {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    nav.sidebar li.active {
      background: var(--accent);
      color: var(--accent-fg);
    }
    nav.sidebar .icon {
      display: inline-flex;
      justify-content: center;
      align-items: center;
      width: 1.5rem;
      height: 1.5rem;
      flex-shrink: 0;
      color: currentColor;
    }
    nav.sidebar .icon svg {
      display: block;
    }
    nav.sidebar .label { font-size: 0.95em; }
    nav.sidebar.collapsed .label { display: none; }
    nav.sidebar .toggle {
      padding: 0.5rem;
      border-top: 1px solid var(--border);
      color: var(--dim);
      cursor: pointer;
      display: flex;
      justify-content: center;
      user-select: none;
    }
    nav.sidebar .toggle:hover { color: var(--fg); }
    nav.sidebar .toggle svg { display: block; }
    /* Screen container is one grid track wide; each view's own layout
       lives inside. The Taxa view keeps its search+tree | detail
       split; the Metadata view is a single form. */
    .screen {
      display: grid;
      overflow: hidden;
    }
    .screen.taxa {
      grid-template-columns: minmax(20rem, 40%) 1fr;
    }
    .screen.metadata {
      grid-template-columns: 1fr;
      padding: 1rem;
      overflow: auto;
    }
    .screen.references {
      grid-template-columns: minmax(20rem, 40%) 1fr;
    }
    aside,
    section {
      overflow: auto;
      padding: 0.5rem;
    }
    aside {
      border-right: 1px solid var(--border);
      display: grid;
      grid-template-rows: auto 1fr;
      gap: 0.5rem;
      overflow: hidden;
    }
    aside .tree-scroll {
      overflow: auto;
      /* Focus ring wraps the whole tree area below the search box and
         fills to the bottom of the pane — so a short tree doesn't get
         a half-height content-sized ring. :focus-within reaches
         through the sfga-tree shadow boundary and matches when the
         inner <ul> has focus, so keyboard-nav mode is signaled by the
         whole pane lighting up. See PARITY.md § Focus semantics. */
      border: 2px solid transparent;
      border-radius: 2px;
    }
    aside .tree-scroll:focus-within {
      border-color: var(--accent);
    }
    .error {
      color: var(--error);
      font-family: var(--font-mono);
    }
  `;

  constructor() {
    super();
    this.archive = null;
    this.metadata = null;
    this.selectedId = "";
    this.error = "";
    this.theme = localStorage.getItem("hive-theme") || "auto";
    this.screen = "taxa";
    this.sidebarCollapsed = localStorage.getItem("hive-sidebar") === "collapsed";
    this.helpOpen = false;
    this._pendingWarnings = null;
    this._applyTheme();
    this._onGlobalKey = this._onGlobalKey.bind(this);
  }

  async connectedCallback() {
    super.connectedCallback();
    // Alt+letter view switches fire globally so the shortcut works
    // regardless of which input has focus. Bound at document level;
    // torn down in disconnectedCallback.
    window.addEventListener("keydown", this._onGlobalKey);
    // URL fragment routing. Back/forward buttons update the hash;
    // hashchange feeds it back into our state. Initial hash is read
    // before data loads so the tree reveal fires with the right id.
    this._onHashChange = this._onHashChange.bind(this);
    window.addEventListener("hashchange", this._onHashChange);
    this._syncFromHash();
    try {
      // Load in parallel: archive info + dataset metadata for the
      // header, vocabulary bundle for edit-form dropdowns, NOMEN
      // ontology for the nomenclatural-status picker, keymap for the
      // help modal and data-driven key dispatch. All cheap and
      // independent; no reason to serialize.
      const [archive, metadata] = await Promise.all([
        api.archive(),
        api.metadata.get().catch(() => null), // legacy archive w/o seeded metadata → null header
        api.vocab.load(),
        api.nomen.load(),
        api.keymap.load(),
      ]);
      this.archive = archive;
      this.metadata = metadata;
      // Reveal the URL-supplied taxon (if any) after the tree component
      // has had a chance to render. updateComplete waits for one Lit
      // render cycle; the tree loads its roots on connectedCallback and
      // is ready to accept reveal() by then.
      if (this.screen === "taxa" && this.selectedId) {
        await this.updateComplete;
        await this._revealInTree(this.selectedId);
      }
    } catch (err) {
      this.error = this._formatError(err);
    }
  }

  disconnectedCallback() {
    window.removeEventListener("keydown", this._onGlobalKey);
    window.removeEventListener("hashchange", this._onHashChange);
    super.disconnectedCallback();
  }

  // updated pushes state changes back into the URL fragment so the
  // location bar tracks the current view + selected taxon. Skipped
  // once (per _syncFromHash call) when the change originated from
  // the hash — the flag is consumed here so the state ↔ hash echo
  // loop doesn't self-perpetuate, but subsequent user-driven changes
  // still propagate to the URL.
  updated(changed) {
    if (this._syncingFromHash) {
      this._syncingFromHash = false;
      return;
    }
    if (changed.has("screen") || changed.has("selectedId")) {
      this._syncToHash();
    }
  }

  // _onHashChange fires when the browser navigates history (back /
  // forward buttons) or when the user pastes a new hash into the URL
  // bar. Feeds the new hash into state; the updated() guard prevents
  // the state → hash update from re-firing hashchange.
  async _onHashChange() {
    const before = { screen: this.screen, selectedId: this.selectedId };
    this._syncFromHash();
    await this.updateComplete;
    // Reveal the newly-selected taxon in the tree if the hash change
    // introduced a fresh selection on the taxa screen.
    if (
      this.screen === "taxa" &&
      this.selectedId &&
      (before.screen !== "taxa" || before.selectedId !== this.selectedId)
    ) {
      await this._revealInTree(this.selectedId);
    }
  }

  // _syncFromHash parses location.hash into screen + selectedId.
  // Recognized paths:
  //   #/                    → taxa screen, no selection
  //   #/taxon/{id}          → taxa screen, taxon revealed + selected
  //   #/metadata            → metadata screen
  //   #/references          → references screen
  // Unknown paths fall back to the taxa screen — safer than leaving
  // the UI in a broken state when someone shares an old-schema link.
  _syncFromHash() {
    const raw = location.hash.startsWith("#") ? location.hash.slice(1) : location.hash;
    const parts = raw.split("/").filter(Boolean);
    this._syncingFromHash = true;
    if (parts.length === 0) {
      this.screen = "taxa";
      // Preserve any existing selection when the hash is just #/ —
      // arriving at this branch during a screen switch (e.g. from
      // metadata → taxa via alt+t) shouldn't discard the current
      // taxon. Only clear when we're explicitly navigating to root.
    } else if (parts[0] === "taxon") {
      this.screen = "taxa";
      this.selectedId = parts[1] || "";
    } else if (SfgaApp.views.some((v) => v.id === parts[0])) {
      this.screen = parts[0];
    } else {
      this.screen = "taxa";
    }
    // Flag stays true until updated() consumes it on the next render
    // cycle — see the guard there. No microtask needed.
  }

  // _syncToHash reflects the current state into location.hash. Uses
  // pushState so back/forward navigate between visited states.
  _syncToHash() {
    let wanted;
    if (this.screen === "taxa") {
      wanted = this.selectedId ? `#/taxon/${this.selectedId}` : "#/";
    } else {
      wanted = `#/${this.screen}`;
    }
    if (location.hash === wanted) return;
    if (location.hash === "" && wanted === "#/") return;
    // pushState here doesn't fire hashchange (spec quirk) — no guard
    // needed for the return trip. Back/forward will fire it, which
    // routes through _onHashChange with the _syncingFromHash flag set
    // by _syncFromHash.
    history.pushState({}, "", wanted);
  }

  // _onGlobalKey handles shortcuts that fire regardless of which
  // shadow tree currently owns focus. Dispatch is data-driven from
  // the shared keymap (pkg/ui.Keymap()) so this handler and the
  // TUI stay in sync — adding a global binding is one edit to
  // pkg/ui/keymap.go plus a case here for its Action.
  //
  // Global keys are deliberately kept small (PARITY.md § Focus
  // semantics — global keys must be extremely obvious in intent).
  // Anything more nuanced belongs on the focused component's own
  // keydown handler (see SfgaTree._onKeyDown).
  _onGlobalKey(e) {
    // Detail-scope shortcuts (e / n / c / s / d) — bare letters, so
    // gate universally on "curator isn't typing." Fire only on the
    // Taxa screen and only when a taxon is selected (except new-child,
    // which allows empty-tree root creation like the TUI's `n`).
    // Handled before the global-scope switch so detail bindings win
    // over anything that shares a letter, and so the return here
    // short-circuits the rest.
    if (this.screen === "taxa" && !this._isTypingInInput(e)) {
      const detailAction = actionForEvent(api.keymap.forScope("detail"), e);
      if (detailAction) {
        const detail = this.renderRoot.querySelector("sfga-detail");
        if (detail) {
          switch (detailAction) {
            case "detail-edit":
              if (this.selectedId && this.archive && !this.archive.read_only) {
                e.preventDefault();
                detail._startEdit();
              }
              return;
            case "detail-new-child":
              if (this.archive && !this.archive.read_only) {
                e.preventDefault();
                detail._openCreate();
              }
              return;
            case "detail-new-sister":
              if (this.selectedId && this.archive && !this.archive.read_only) {
                e.preventDefault();
                detail._openCreateSister();
              }
              return;
            case "detail-delete":
              if (this.selectedId && this.archive && !this.archive.read_only) {
                e.preventDefault();
                detail._askDelete();
              }
              return;
          }
        }
      }
    }

    const action = actionForEvent(api.keymap.forScope("global"), e);
    if (!action) return;

    // Actions that would swallow ordinary typing are gated on
    // "curator isn't already in an input." "cancel" (Escape) is
    // never gated — Escape inside an input should still close an
    // overlay. Alt+letter view switches don't collide with typing
    // (Alt-modified keys don't emit printable characters) so they
    // aren't gated either.
    const gateForInput = new Set(["search-focus", "help-open"]);
    if (gateForInput.has(action) && this._isTypingInInput(e)) return;

    switch (action) {
      case "view-taxa":
      case "view-metadata":
      case "view-references": {
        const wanted = action.slice("view-".length); // "taxa" / "metadata" / "references"
        e.preventDefault();
        this.screen = wanted;
        return;
      }
      case "search-focus": {
        if (this.screen !== "taxa") return;
        const input = this._searchInput();
        if (!input) return;
        e.preventDefault();
        input.focus();
        input.select();
        return;
      }
      case "help-open": {
        e.preventDefault();
        this.helpOpen = true;
        return;
      }
      case "cancel": {
        // Escape closes the help overlay from anywhere without
        // pre-empting cancel behavior inside inputs / modals owned
        // by other components.
        if (this.helpOpen) {
          e.preventDefault();
          this.helpOpen = false;
          return;
        }
        // Escape while typing in the search combobox returns focus
        // to the tree so the curator can start arrow-nav without
        // Tab-cycling out. Detected via composedPath so we don't
        // accidentally hijack Escape inside other inputs (edit
        // forms, add-reference modal — they own their own Esc).
        const path = e.composedPath ? e.composedPath() : [];
        const inSearchCombo = path.some(
          (node) =>
            node instanceof HTMLElement &&
            node.tagName === "SFGA-COMBOBOX" &&
            node.classList.contains("search"),
        );
        if (inSearchCombo && this.screen === "taxa") {
          e.preventDefault();
          const combo = this.renderRoot.querySelector("sfga-combobox.search");
          if (combo) {
            combo.value = "";
            combo.valueName = "";
            const input = combo.renderRoot?.querySelector("input");
            if (input) input.blur();
          }
          const tree = this.renderRoot.querySelector("sfga-tree");
          if (tree && typeof tree.focus === "function") tree.focus();
        }
        return;
      }
    }
  }

  // _isTypingInInput returns true when the innermost focus target
  // (across shadow boundaries) is a text-entry element — <input>,
  // <textarea>, or contentEditable. Uses composedPath so a keydown
  // targeting an input inside a nested shadow root is still detected.
  _isTypingInInput(e) {
    const path = e.composedPath ? e.composedPath() : [];
    for (const node of path) {
      if (!(node instanceof HTMLElement)) continue;
      const tag = node.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA") return true;
      if (node.isContentEditable) return true;
    }
    return false;
  }

  // _searchInput reaches through the shadow tree to the search
  // combobox's inner <input>. Ships as a helper so `/` and any future
  // "focus search" affordance route through one place.
  _searchInput() {
    const combo = this.renderRoot.querySelector("sfga-combobox.search");
    return combo?.renderRoot?.querySelector("input") ?? null;
  }

  _toggleSidebar() {
    this.sidebarCollapsed = !this.sidebarCollapsed;
    localStorage.setItem("hive-sidebar", this.sidebarCollapsed ? "collapsed" : "expanded");
  }

  _applyTheme() {
    // "auto" → clear the attribute so prefers-color-scheme takes over.
    if (this.theme === "auto") {
      document.documentElement.removeAttribute("data-theme");
    } else {
      document.documentElement.setAttribute("data-theme", this.theme);
    }
  }

  _cycleTheme() {
    const order = ["auto", "light", "dark"];
    const next = order[(order.indexOf(this.theme) + 1) % order.length];
    this.theme = next;
    localStorage.setItem("hive-theme", next);
    this._applyTheme();
  }

  // _themeIcon maps the current theme setting to a Lucide icon name.
  // Monitor = "follows the system"; sun / moon are the explicit
  // choices. Clicking the button cycles through auto → light → dark.
  _themeIcon() {
    switch (this.theme) {
      case "light":
        return "sun";
      case "dark":
        return "moon";
      default:
        return "monitor";
    }
  }

  _onSelected(e) {
    // User clicked a different taxon — drop any create/update
    // warning that was hanging around from the previous save.
    this._pendingWarnings = null;
    this.selectedId = e.detail.id;
  }

  // Detail pane fires taxon-moved after a successful reparent (or a
  // fresh create); forward it to the tree so the visible tree state
  // reflects the new location, and set selectedId so the detail pane
  // loads the new taxon (essential for "add child, add child, add
  // child" workflows — otherwise the next create would still attach
  // under the previous parent).
  async _onMoved(e) {
    // Cache the create/update warnings so the detail pane can render
    // them as a banner. Keyed on the taxon id — a subsequent
    // selection change to another taxon clears the banner naturally
    // because pendingWarnings.taxonId no longer matches selectedId.
    const warnings = e.detail.warnings || [];
    this._pendingWarnings = warnings.length ? { taxonId: e.detail.id, warnings } : null;
    this.selectedId = e.detail.id;
    await this._revealInTree(e.detail.id);
  }

  // Detail pane fires taxon-deleted after a successful delete. Reveal
  // the parent so the tree stays anchored at the surviving ancestor;
  // reset selectedId to the parent so the detail pane loads it.
  async _onDeleted(e) {
    const parent = e.detail.parent_id;
    if (parent) {
      this.selectedId = parent;
      await this._revealInTree(parent);
    } else {
      // Deleted a root — clear selection and reload roots. Reuse the
      // tree's own reload so pagination sentinels are handled the same
      // way as on initial load.
      this.selectedId = "";
      const tree = this.renderRoot.querySelector("sfga-tree");
      if (tree && typeof tree.reloadRoots === "function") {
        await tree.reloadRoots();
      }
    }
  }

  // Search box above the tree picks a taxon; reveal it in the tree AND
  // open its detail pane. Empty id (× clear) is a no-op — we don't want
  // to collapse the tree just because the search input was cleared.
  async _onSearchPick(e) {
    const id = e.detail.id;
    if (!id) return;
    this.selectedId = id;
    await this._revealInTree(id);
    // Clear the input so the next search starts empty; the tree
    // selection persists via selectedId.
    const combo = this.renderRoot.querySelector("sfga-combobox.search");
    if (combo) {
      combo.value = "";
      combo.valueName = "";
    }
  }

  async _revealInTree(id) {
    const tree = this.renderRoot.querySelector("sfga-tree");
    if (tree && typeof tree.reveal === "function") {
      await tree.reveal(id);
    }
  }

  _formatError(err) {
    if (err instanceof Problem) {
      return `${err.title}: ${err.detail || err.message}`;
    }
    return String(err);
  }

  render() {
    // Header title comes from the dataset metadata when present, else
    // falls back to the literal "hive" (legacy archive without a seeded
    // metadata row). Path / schema / mode stay in the secondary meta
    // strip for operator-visible context.
    const title = this.metadata?.title || "hive";
    const meta = this.archive
      ? html`${this.archive.path} · schema ${this.archive.schema_version} ·
          ${this.archive.read_only ? "read-only" : "read-write"}`
      : "loading…";
    return html`
      <header>
        <div class="title">${title}</div>
        <div class="meta">${meta}</div>
        <div class="header-buttons">
          <button
            class="theme-toggle"
            @click=${() => (this.helpOpen = true)}
            title="keyboard shortcuts  (?)"
            aria-label="keyboard shortcuts"
          >
            ${renderIcon("help-circle", 18)}
          </button>
          <button
            class="theme-toggle"
            @click=${() => this._cycleTheme()}
            title="theme: ${this.theme}  (click to cycle)"
            aria-label="theme: ${this.theme}"
          >
            ${renderIcon(this._themeIcon(), 18)}
          </button>
        </div>
      </header>
      <main>
        ${this._renderSidebar()}
        ${this._renderScreen()}
      </main>
      ${this.helpOpen
        ? html`<sfga-help-modal
            @close=${() => (this.helpOpen = false)}
          ></sfga-help-modal>`
        : ""}
    `;
  }

  _renderSidebar() {
    const cls = this.sidebarCollapsed ? "collapsed" : "expanded";
    return html`
      <nav class="sidebar ${cls}">
        <ul>
          ${SfgaApp.views.map(
            (v) => html`
              <li
                class=${v.id === this.screen ? "active" : ""}
                @click=${() => (this.screen = v.id)}
                title=${v.label + "  (alt+" + v.key + ")"}
              >
                <span class="icon">${renderIcon(v.icon)}</span>
                <span class="label">${v.label}</span>
              </li>
            `,
          )}
        </ul>
        <div
          class="toggle"
          @click=${() => this._toggleSidebar()}
          title=${this.sidebarCollapsed ? "expand sidebar" : "collapse sidebar"}
        >
          ${renderIcon(
            this.sidebarCollapsed ? "panel-left-open" : "panel-left-close",
            18,
          )}
        </div>
      </nav>
    `;
  }

  _renderScreen() {
    switch (this.screen) {
      case "metadata":
        return html`
          <div class="screen metadata">
            <sfga-metadata
              .editable=${this.archive ? !this.archive.read_only : false}
              @metadata-changed=${(e) => (this.metadata = e.detail.metadata)}
            ></sfga-metadata>
          </div>
        `;
      case "references":
        return html`
          <div class="screen references">
            <sfga-references></sfga-references>
          </div>
        `;
      default:
        return html`
          <div class="screen taxa">
            <aside>
              <sfga-combobox
                class="search"
                min-search-chars="2"
                placeholder="search taxa…"
                .source=${taxonSource}
                .resolver=${taxonResolver}
                @pick=${(e) => this._onSearchPick(e)}
              ></sfga-combobox>
              <div class="tree-scroll">
                ${this.error ? html`<div class="error">${this.error}</div>` : ""}
                <sfga-tree @taxon-selected=${(e) => this._onSelected(e)}></sfga-tree>
              </div>
            </aside>
            <section>
              <sfga-detail
                .taxonId=${this.selectedId}
                .editable=${this.archive ? !this.archive.read_only : false}
                .pendingWarnings=${
                  this._pendingWarnings && this._pendingWarnings.taxonId === this.selectedId
                    ? this._pendingWarnings.warnings
                    : []
                }
                @taxon-moved=${(e) => this._onMoved(e)}
                @taxon-deleted=${(e) => this._onDeleted(e)}
              ></sfga-detail>
            </section>
          </div>
        `;
    }
  }
}

// ---------- <sfga-tree> ----------
// Flat-rendered expandable tree. Loads roots on connect, fetches children
// on expand. Selection dispatches a `taxon-selected` event; the shell
// forwards it to the detail pane.

class SfgaTree extends LitElement {
  static properties = {
    nodes: { state: true },
    selectedId: { state: true },
    error: { state: true },
  };

  static styles = css`
    :host {
      display: block;
      font-family: var(--font-mono);
      font-size: 0.95em;
    }
    ul {
      list-style: none;
      margin: 0;
      padding: 0;
      /* Suppress the default focus outline on the <ul>; the pane-level
         ring lives on the .tree-scroll wrapper in the app shell so it
         fills the whole tree area (not just the content-sized <ul>).
         See PARITY.md § Focus semantics. */
      outline: none;
    }
    li {
      padding: 0.15rem 0.35rem;
      cursor: pointer;
      white-space: nowrap;
      color: var(--fg);
    }
    li:hover {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    li.selected {
      background: var(--accent);
      color: var(--accent-fg);
    }
    li.sentinel {
      color: var(--dim);
      font-style: italic;
    }
    li.sentinel:hover {
      color: var(--fg);
    }
    .caret {
      display: inline-block;
      width: 1em;
      color: var(--dim);
    }
    li.selected .caret {
      color: var(--accent-fg);
    }
    .error {
      color: var(--error);
    }
  `;

  constructor() {
    super();
    this.nodes = [];
    this.selectedId = "";
    this.error = "";
    // Page size matches the TUI's treePageSize constant — 200 rows per
    // fetch, sentinel below if more remain. Kept as a field so tests /
    // future config can override.
    this._pageSize = 200;
    // Vim-style count prefix: digits typed before g/G accumulate here
    // and are consumed by the next jump. Any other key resets it so
    // stale counts don't leak into unrelated actions.
    this._pendingCount = 0;
  }

  async connectedCallback() {
    super.connectedCallback();
    await this.reloadRoots();
    // Auto-focus the tree once the initial roots have rendered so a
    // keyboard-first curator can start arrow-nav without clicking or
    // Tab-cycling past the search box. Deferred to updateComplete so
    // Lit has actually mounted the <ul>.
    await this.updateComplete;
    this.focus();
  }

  // focus places keyboard focus on the tree's <ul>, making the tree
  // the active pane for arrow / vim shortcuts. Public so the shell can
  // call it (e.g. Esc in the search input returns focus here).
  focus() {
    const ul = this.renderRoot.querySelector("ul");
    if (ul) ul.focus({ preventScroll: true });
  }

  // reloadRoots re-fetches the top-level page and replaces the whole
  // visible tree. Public so the shell can call it after operations that
  // invalidate the root set (root-level delete, project switch).
  async reloadRoots() {
    try {
      const page = await api.taxon.roots({ limit: this._pageSize });
      this.nodes = this._pageToNodes(page, 0, "");
    } catch (err) {
      this.error = String(err);
    }
  }

  // _pageToNodes maps an /api/taxon/... children response into the
  // flat-list shape the tree renders. Appends a sentinel row when the
  // response carries a next_cursor so the curator sees the truncation
  // and can page down with a click.
  _pageToNodes(page, depth, parentId) {
    const items = (page.items || []).map((h) => ({
      ...h,
      depth,
      expanded: false,
    }));
    if (page.next_cursor) {
      const remaining = Math.max((page.total ?? 0) - items.length, 0);
      items.push({
        sentinel: true,
        parent_id: parentId,
        depth,
        cursor: page.next_cursor,
        remaining,
      });
    }
    return items;
  }

  _select(node) {
    if (node.sentinel) return;
    if (this.selectedId === node.id) return;
    this.selectedId = node.id;
    this.dispatchEvent(
      new CustomEvent("taxon-selected", {
        detail: { id: node.id },
        bubbles: true,
        composed: true,
      }),
    );
  }

  // _cursorIndex returns the index of the currently selected node in
  // this.nodes, or -1 if selection is unset / no longer visible. Used
  // by every keyboard motion as the anchor for its relative move.
  _cursorIndex() {
    if (!this.selectedId) return -1;
    return this.nodes.findIndex((n) => !n.sentinel && n.id === this.selectedId);
  }

  // _moveTo selects the taxon at nodes[idx] (skipping sentinels) and
  // scrolls it into view. Called by every arrow / vim jump so scroll
  // and detail-pane load stay coupled to cursor position.
  async _moveTo(idx) {
    if (idx < 0 || idx >= this.nodes.length) return;
    const node = this.nodes[idx];
    if (node.sentinel) return;
    this._select(node);
    await this.updateComplete;
    const el = this.renderRoot.querySelector("li.selected");
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "nearest" });
    }
  }

  async _toggle(node) {
    if (node.sentinel) {
      await this._loadMore(node);
      return;
    }
    if (!node.has_children) return;
    if (node.expanded) {
      this._collapse(node);
    } else {
      await this._expand(node);
    }
  }

  async _expand(node) {
    try {
      const page = await api.taxon.children(node.id, { limit: this._pageSize });
      const idx = this.nodes.indexOf(node);
      if (idx < 0) return;
      const children = this._pageToNodes(page, node.depth + 1, node.id);
      const newNodes = [...this.nodes];
      newNodes[idx] = { ...node, expanded: true };
      newNodes.splice(idx + 1, 0, ...children);
      this.nodes = newNodes;
    } catch (err) {
      this.error = String(err);
    }
  }

  // _loadMore fetches the next page of siblings for a sentinel row,
  // splices the new rows in above the sentinel, and either updates the
  // sentinel's cursor/remaining or drops it if the parent is fully
  // loaded now. Matches the TUI's expand-on-sentinel behavior.
  async _loadMore(sentinel) {
    try {
      const opts = { limit: this._pageSize, cursor: sentinel.cursor };
      const page = sentinel.parent_id
        ? await api.taxon.children(sentinel.parent_id, opts)
        : await api.taxon.roots(opts);
      const idx = this.nodes.indexOf(sentinel);
      if (idx < 0) return;
      const fresh = this._pageToNodes(page, sentinel.depth, sentinel.parent_id);
      const newNodes = [...this.nodes];
      // Replace the old sentinel with the freshly-loaded rows (which
      // themselves end in a new sentinel iff there's still more).
      newNodes.splice(idx, 1, ...fresh);
      this.nodes = newNodes;
    } catch (err) {
      this.error = String(err);
    }
  }

  _collapse(node) {
    const idx = this.nodes.indexOf(node);
    if (idx < 0) return;
    let end = idx + 1;
    while (end < this.nodes.length && this.nodes[end].depth > node.depth) {
      end++;
    }
    const newNodes = [...this.nodes];
    newNodes.splice(idx + 1, end - idx - 1);
    newNodes[idx] = { ...node, expanded: false };
    this.nodes = newNodes;
  }

  // reveal loads the ancestor chain for id and expands the tree along it
  // so the target appears in its correct location. Called after a move so
  // the curator doesn't have to hunt for the reparented taxon. Also
  // handles the case where an ancestor is above any currently-visible
  // root by reloading roots first.
  async reveal(id) {
    if (!id) return;
    try {
      const { ids: chain } = await api.taxon.ancestors(id);
      // Reload roots so a taxon moved to root level (or whose new root
      // ancestor was previously off-screen) shows up. Rebuild fresh via
      // the paging helper so an oversized root list carries its sentinel.
      const rootsPage = await api.taxon.roots({ limit: this._pageSize });
      this.nodes = this._pageToNodes(rootsPage, 0, "");
      // Expand each ancestor in order. _expand mutates this.nodes in place
      // so the next ancestor lookup finds the freshly-inserted row.
      for (const ancestorID of chain) {
        const node = this.nodes.find((n) => n.id === ancestorID);
        if (!node || node.expanded) continue;
        await this._expand(node);
      }
      // Select the target (highlights the row so the curator sees where
      // it landed). If the target is off-screen the browser scrolls it in.
      this.selectedId = id;
      await this.updateComplete;
      const el = this.renderRoot.querySelector("li.selected");
      if (el && typeof el.scrollIntoView === "function") {
        el.scrollIntoView({ block: "nearest", behavior: "smooth" });
      }
    } catch (err) {
      this.error = String(err);
    }
  }

  render() {
    if (this.error) return html`<div class="error">${this.error}</div>`;
    // tabindex="0" makes the tree Tab-reachable and a valid focus target.
    // mousedown promotes focus to the <ul> before the <li> click fires,
    // so a click into the tree lands focus here (browsers don't focus
    // non-input elements on click by default). The pane earns focus
    // explicitly (PARITY.md § Focus semantics) so keyboard shortcuts
    // don't fire while the curator is typing into an unrelated input.
    return html`
      <ul
        tabindex="0"
        @mousedown=${(e) => e.currentTarget.focus()}
        @keydown=${(e) => this._onKeyDown(e)}
      >
        ${this.nodes.map((n) => this._renderNode(n))}
      </ul>
    `;
  }

  // _onKeyDown handles tree-scoped shortcuts. Runs only when the <ul>
  // has focus — outside that scope the keys have no effect on the tree.
  // Bindings mirror the TUI (see internal/tui/keys.go) so muscle
  // memory transfers between frontends.
  //
  //   Escape           blur — return to pure mouse UX
  //   ↑ / k            move cursor up one taxon
  //   ↓ / j            move cursor down one taxon
  //   → / l / Enter    expand current, or step into first child if
  //                    already expanded (or trigger sentinel load)
  //   ← / h            collapse current, or step out to parent if not
  //                    expanded
  //   0-9              accumulate into pending count for the next g/G
  //   g / Ng           first / Nth sibling in the current group
  //   G / NG           last loaded sibling (or trigger load-more if
  //                    the group is truncated) / Nth
  //
  // Dispatch is data-driven from api.keymap.forScope("tree") so the
  // shared source in pkg/ui/keymap.go is the single place a binding
  // is edited. async so the loop bodies below can await
  // _moveCursor / _expandOrEnter / _collapseOrParent — each of those
  // may issue an API fetch (child page load, sentinel expansion).
  async _onKeyDown(e) {
    // Digit prefix — accumulate into pendingCount, consumed by g/G.
    // Not a shortcut (no Action); handled before dispatch. Bare digits
    // only; modifier+digit is reserved for future bindings.
    const k = e.key;
    if (k.length === 1 && k >= "0" && k <= "9" && !e.altKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      this._pendingCount = this._pendingCount * 10 + (k.charCodeAt(0) - 48);
      return;
    }

    // Match against tree-scope + cancel (Escape/blur). Cancel lives
    // in the global scope of the shared keymap, so include it here
    // explicitly rather than pulling all global bindings into tree
    // dispatch — the tree's cancel means "blur," which is different
    // from the app's cancel handling.
    const treeAction = actionForEvent(api.keymap.forScope("tree"), e);
    const cancelKeys = api.keymap.forScope("global").filter((s) => s.action === "cancel");
    const isCancel = actionForEvent(cancelKeys, e) === "cancel";
    const action = treeAction || (isCancel ? "cancel" : null);

    if (!action) {
      // Anything unmapped clears any pending count so a stray keypress
      // doesn't linger into the next g/G.
      this._pendingCount = 0;
      return;
    }

    e.preventDefault();

    // Every tree action consumes the pending count. Jump actions
    // (g/G) read it as a group-index target; motion actions
    // (j/k/l/h) treat it as a repeat count. cancel ignores it.
    // Cap loops at 10000 as a paranoia guard against a stray 100000j
    // that would otherwise block the event loop.
    const count = Math.max(1, Math.min(this._pendingCount || 1, 10000));
    this._pendingCount = 0;

    switch (action) {
      case "cancel":
        e.currentTarget.blur();
        return;
      case "tree-up":
        for (let i = 0; i < count; i++) {
          if (!(await this._moveCursor(-1))) break;
        }
        return;
      case "tree-down":
        for (let i = 0; i < count; i++) {
          if (!(await this._moveCursor(1))) break;
        }
        return;
      case "tree-expand":
        for (let i = 0; i < count; i++) {
          if (!(await this._expandOrEnter())) break;
        }
        return;
      case "tree-collapse":
        for (let i = 0; i < count; i++) {
          if (!(await this._collapseOrParent())) break;
        }
        return;
      case "tree-first-sibling":
        this._jumpInSiblings(true, count);
        return;
      case "tree-last-sibling":
        this._jumpInSiblings(false, count);
        return;
    }
  }

  // _moveCursor walks the visible taxa in this.nodes by `dir` steps
  // (typically ±1), skipping sentinel rows. When moving down past the
  // last taxon into a sentinel, triggers a load-more so the sentinel
  // page-in happens without a separate keypress — mirrors the TUI's
  // autoLoad-on-sentinel behavior. Returns true when the cursor
  // actually moved, false at the edges — lets count-prefix loops
  // break early once they run out of visible taxa.
  async _moveCursor(dir) {
    if (this.nodes.length === 0) return false;
    let idx = this._cursorIndex();
    if (idx < 0) {
      // Nothing selected yet — land on the first / last taxon.
      idx = dir > 0 ? -1 : this.nodes.length;
    }
    let next = idx + dir;
    while (next >= 0 && next < this.nodes.length && this.nodes[next].sentinel) {
      next += dir;
    }
    if (next < 0 || next >= this.nodes.length) {
      // Off the ends. If we ran off the bottom and the last node is a
      // sentinel, trigger a load-more so subsequent presses can walk
      // into the newly-loaded rows.
      if (dir > 0) {
        const last = this.nodes[this.nodes.length - 1];
        if (last && last.sentinel) await this._loadMore(last);
      }
      return false;
    }
    await this._moveTo(next);
    return true;
  }

  // _expandOrEnter implements the →/l/Enter contract:
  //   - Leaf             → no-op (nothing to descend into).
  //   - Collapsed parent → expand AND step cursor into first child.
  //   - Expanded parent  → step cursor into first child.
  //
  // "Always descend when there's somewhere to descend to" — expanding
  // a node is almost always followed by wanting to look at what's
  // inside, so folding the two into one press means `l l l l` walks
  // the leftmost path efficiently and Nl descends N levels in one
  // action. Costs one extra press to move to a sibling of the first
  // child (down-arrow after), which is a fair trade.
  //
  // Returns true when the cursor advanced (used by the count-prefix
  // loop to break early at a leaf).
  async _expandOrEnter() {
    const idx = this._cursorIndex();
    if (idx < 0) return false;
    const node = this.nodes[idx];
    if (!node.has_children) return false;
    if (!node.expanded) {
      await this._expand(node);
    }
    // Re-check position — _expand splices children after this row
    // without moving this row, so idx is still valid, but read fresh
    // to be defensive against future refactors.
    const currentIdx = this._cursorIndex();
    if (currentIdx < 0) return false;
    const child = this.nodes[currentIdx + 1];
    if (child && !child.sentinel && child.depth === this.nodes[currentIdx].depth + 1) {
      await this._moveTo(currentIdx + 1);
      return true;
    }
    return false;
  }

  // _collapseOrParent implements the ←/h contract, mirror-symmetric
  // with →/l's _expandOrEnter:
  //   - Expanded node → collapse AND move cursor to parent in one press.
  //   - Leaf / collapsed / root → move cursor to parent (or no-op at depth 0).
  //
  // "Always ascend when there's somewhere to ascend to" — collapsing
  // a subtree means the curator is done browsing it, so folding the
  // two into one press means `h h h h` walks the ancestor chain
  // efficiently and Nh ascends N levels in one action. "Collapse
  // without moving cursor" is not available; if it becomes wanted
  // later, a separate binding (z / -) can carry it.
  //
  // Returns true when the cursor moved or a subtree collapsed (used
  // by the count-prefix loop to stop looping at an already-collapsed
  // root).
  async _collapseOrParent() {
    const idx = this._cursorIndex();
    if (idx < 0) return false;
    const node = this.nodes[idx];
    if (node.expanded) {
      this._collapse(node);
      // Now this node is collapsed with cursor still on it. Fall
      // through to the ascend step below.
    }
    if (node.depth === 0) return false;
    // Walk back through the flat list until we find the row at the
    // parent's depth — that's the parent (siblings share depth, so
    // the first shallower row above must be an ancestor at depth-1).
    for (let i = idx - 1; i >= 0; i--) {
      if (this.nodes[i].sentinel) continue;
      if (this.nodes[i].depth === node.depth - 1) {
        await this._moveTo(i);
        return true;
      }
    }
    return false;
  }

  // _jumpInSiblings handles g / G / Ng / NG. The sibling group is the
  // contiguous run of nodes at the current cursor's depth sharing the
  // same parent — matches the TUI's siblingRange semantics so g/G feel
  // group-local rather than file-wide.
  //   g       → first sibling
  //   G       → last loaded sibling (load more if group is truncated)
  //   Ng / NG → Nth (1-based); loads more if N > loaded and group is
  //             truncated, then lands on the sentinel (subsequent G
  //             takes you to the new last row).
  //
  // `count` comes from the vim-style pending-count prefix — 1 when
  // unspecified, otherwise the accumulated digit sequence.
  async _jumpInSiblings(top, count) {
    // Bare g/G (no count) is signaled by count === 1 from the
    // dispatcher; internally we treat count === 1 as "no explicit
    // target" so the top vs. bottom branches below pick their
    // defaults. Callers wanting to jump to sibling 1 explicitly
    // achieve it with plain g, which is already position 1.
    if (count === 1) count = 0;
    const idx = this._cursorIndex();
    if (idx < 0) {
      // No cursor yet — g/G at start land on first / last visible taxon.
      const target = top ? 0 : this.nodes.length - 1;
      let t = target;
      while (t >= 0 && t < this.nodes.length && this.nodes[t].sentinel) {
        t += top ? 1 : -1;
      }
      if (t >= 0 && t < this.nodes.length) await this._moveTo(t);
      return;
    }
    const [start, end] = this._siblingRange(idx);
    const loaded = end - start;
    const sentinelIdx = this._sentinelIndex(this.nodes[start].parent_id, this.nodes[start].depth);
    const truncated = sentinelIdx >= 0;

    // Target position within the group (1-based). -1 means "last."
    let target;
    if (count > 0) {
      target = count;
    } else if (top) {
      target = 1;
    } else {
      target = -1;
    }

    if (target === 1) {
      await this._moveTo(start);
      return;
    }
    if (target > 0 && target <= loaded) {
      await this._moveTo(start + target - 1);
      return;
    }
    if (target === -1 && !truncated) {
      await this._moveTo(end - 1);
      return;
    }
    // Beyond the loaded window — trigger load-more on the group's
    // sentinel. Cursor stays put; the freshly-loaded rows are then
    // reachable by arrow-down or a second G.
    if (truncated) {
      await this._loadMore(this.nodes[sentinelIdx]);
    }
  }

  // _siblingRange returns [start, end) — the contiguous run of nodes
  // at nodes[idx]'s depth + parent. Mirrors internal/tui/tree.go's
  // siblingRange: "contiguous" is the operative word — if a preceding
  // sibling has expanded children (depth+1 rows breaking the run),
  // the range covers only the current visible run, not the full
  // sibling set. Keeps g/G group-local rather than file-wide.
  _siblingRange(idx) {
    const cur = this.nodes[idx];
    const depth = cur.depth;
    const parent = cur.parent_id || "";
    let start = idx;
    while (start > 0) {
      const p = this.nodes[start - 1];
      if (p.sentinel) break;
      if (p.depth !== depth) break;
      if ((p.parent_id || "") !== parent) break;
      start--;
    }
    let end = idx + 1;
    while (end < this.nodes.length) {
      const n = this.nodes[end];
      if (n.sentinel) break;
      if (n.depth !== depth) break;
      if ((n.parent_id || "") !== parent) break;
      end++;
    }
    return [start, end];
  }

  // _sentinelIndex returns the index of the sentinel row that paginates
  // the given (parentID, depth) sibling group, or -1 if the group isn't
  // truncated. The sentinel row is emitted by _pageToNodes when the API
  // response carries a next_cursor.
  _sentinelIndex(parentID, depth) {
    const parent = parentID || "";
    for (let i = 0; i < this.nodes.length; i++) {
      const n = this.nodes[i];
      if (n.sentinel && n.depth === depth && (n.parent_id || "") === parent) {
        return i;
      }
    }
    return -1;
  }

  _renderNode(n) {
    if (n.sentinel) {
      const label = n.remaining
        ? `⋯ ${n.remaining} more  (click to load)`
        : `⋯ load more`;
      return html`
        <li
          class="sentinel"
          style="padding-left: ${0.35 + n.depth * 1.1}rem"
          @click=${() => this._loadMore(n)}
        >
          <span class="caret"> </span>
          ${label}
        </li>
      `;
    }
    return html`
      <li
        class=${n.id === this.selectedId ? "selected" : ""}
        style="padding-left: ${0.35 + n.depth * 1.1}rem"
        @click=${() => {
          this._select(n);
          this._toggle(n);
        }}
      >
        <span class="caret">
          ${n.has_children ? (n.expanded ? "▼" : "▶") : " "}
        </span>
        ${renderLabel(n.label, n.name)}
      </li>
    `;
  }
}

// ---------- <sfga-detail> ----------
// Right pane. Rerender is driven by the `.taxonId` property; a change
// triggers a fetch on the next update. Renders identity + name fields;
// synonym / vernacular / distribution tabs land in follow-ups.

class SfgaDetail extends LitElement {
  static properties = {
    taxonId: { type: String, attribute: false },
    editable: { type: Boolean, attribute: false },
    // Soft warnings passed in from the shell after a successful
    // create/update, keyed to this.taxonId. Rendered as a banner
    // above the field list. Empty array = no banner.
    pendingWarnings: { attribute: false },
    _taxon: { state: true },
    _name: { state: true },
    _etag: { state: true },
    _nameEtag: { state: true },
    _synonyms: { state: true },
    _error: { state: true },
    _loading: { state: true },
    // Edit-mode state — taxon and name drafts are tracked separately so
    // the PATCH round-trip only touches an aggregate whose fields changed.
    _editing: { state: true },
    _draft: { state: true }, // taxon patch payload
    _nameDraft: { state: true }, // name patch payload
    _saving: { state: true },
    _saveError: { state: true },
    // Create-child modal state. Two-step flow:
    //   step 0 — verbatim scientific name + code picker; server-side
    //            parse fires on "next" to compute the atomized breakdown.
    //   step 1 — atomized preview: every col__ field editable, rank
    //            pre-selected from RankGuess. Curator confirms and saves.
    _creating: { state: true },
    _createStep: { state: true },
    _createDraft: { state: true },
    _createBusy: { state: true },
    _createError: { state: true },
    // Progressive disclosure toggle for step 1: collapsed shows just
    // scientific name + rank + verbatim authorship + reference + status
    // + notes; expanded reveals the atomized name (uninomial/genus/…)
    // and atomized authorship (basionym + combination pairs). Parsing
    // runs regardless — the toggle only affects visibility. Power users
    // who want to verify the parse crack it open; curators who trust
    // the parse leave it collapsed. gsvalidator's parse-mismatch rule
    // catches the poorly-parsed cases in either mode.
    _createShowAtomized: { state: true },
    // When set, the create pane's Save writes to the "add basionym"
    // endpoint (POST /api/taxon/{X}/basionym) instead of POST /api/taxon.
    // The value is the current-combination taxon id — set by the
    // "Create + add original combination" flow after the accepted-taxon
    // half of the write succeeds. Null = normal accepted-name create.
    _creatingBasionymFor: { state: true },
    // Display name for the header row while creating a basionym so the
    // curator sees which combination they're entering the original for.
    _creatingBasionymForName: { state: true },
    // Parent id + label the pending create attaches to. Distinguishes
    // "new child" (id = current taxon) from "new sister" (id = current
    // taxon's parent). Stored at open time so _submitCreate has a
    // stable target even if the tree selection moves underneath.
    _createParentID: { state: true },
    _createParentLabel: { state: true },
    // Ranks valid as a child of the intended parent (from
    // pkg/ui.ValidChildRanks via GET /api/taxon/{id}/child-ranks).
    // null = "no filter" (root taxon, or code unknown); array of
    // {id, typical_use} = allow only these. Step-1 rank picker
    // uses childRankSource to display typical ranks by default and
    // widen on search.
    _createChildRanks: { state: true },
    // Delete-confirmation modal state.
    _confirmDelete: { state: true },
    _deleteError: { state: true },
    // Add-reference modal state. The modal is a self-contained component
    // (<sfga-add-reference-modal>) — this flag just toggles rendering,
    // and _pickedReferenceLabel carries the label into the combobox
    // after a successful pick so the display updates immediately without
    // waiting on the resolver's second fetch.
    _addingReference: { state: true },
    _pickedReferenceLabel: { state: true },
    // Edit-mode progressive-disclosure toggle for the atomized name +
    // atomized authorship blocks. Mirrors _createShowAtomized on the
    // create form; edit and create both surface the same widget set
    // so curators learn one form.
    _editShowAtomized: { state: true },
  };

  static styles = css`
    :host {
      display: block;
      font-family: var(--font-body);
    }
    h2 {
      margin: 0 0 0.25rem 0;
      font-family: var(--font-mono);
      font-size: 1.15em;
    }
    .authorship {
      color: var(--dim);
    }
    hr {
      border: 0;
      border-top: 1px solid var(--border);
      margin: 0.5rem 0;
    }
    dl {
      margin: 0;
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.15rem 0.75rem;
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    dt {
      color: var(--dim);
    }
    dd {
      margin: 0;
      overflow-wrap: anywhere;
    }
    section.synonyms {
      margin-top: 1rem;
    }
    section.synonyms h3 {
      margin: 0 0 0.25rem 0;
      font-size: 0.95em;
      color: var(--dim);
    }
    section.synonyms ul {
      list-style: none;
      margin: 0;
      padding: 0;
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    section.synonyms li {
      padding: 0.15rem 0;
    }
    .error {
      color: var(--error);
      font-family: var(--font-mono);
    }
    .empty {
      color: var(--dim);
      font-style: italic;
    }
    /* Soft-validation banner shown at the top of the detail pane after
       a save that produced non-blocking results from gsvalidator. The
       banner itself is neutral; each item carries its own severity chip
       so a mixed batch (warn + info) still ranks visually. */
    .warning-banner {
      border: 1px solid var(--border);
      background: color-mix(in oklab, var(--fg) 4%, var(--bg));
      color: var(--fg);
      padding: 0.5rem 0.75rem;
      border-radius: 3px;
      margin: 0.5rem 0 0.75rem 0;
      font-size: 0.95em;
    }
    .warning-banner ul {
      list-style: none;
      margin: 0.4rem 0 0 0;
      padding: 0;
    }
    .warning-banner li {
      margin: 0.25rem 0;
      display: grid;
      grid-template-columns: auto 1fr;
      gap: 0.5rem;
      align-items: baseline;
    }
    .warning-banner .warning-rule {
      font-weight: 600;
      color: var(--fg);
    }
    /* Severity chip: colored glyph + label. Color always pairs with the
       glyph so severity is readable under color loss. Backgrounds are
       tinted at ~12% opacity so the chip stays legible in both themes.
       See styles.css for --sev-* palette (traffic-light default, CVD
       alternate via html[data-severity-palette="cvd"]). */
    .sev-chip {
      display: inline-flex;
      align-items: center;
      gap: 0.25rem;
      padding: 0.1rem 0.4rem;
      border-radius: 999px;
      font-size: 0.8em;
      font-weight: 600;
      line-height: 1;
      border: 1px solid currentColor;
      white-space: nowrap;
    }
    .sev-chip.sev-error { color: var(--sev-error); background: var(--sev-error-bg); }
    .sev-chip.sev-warn  { color: var(--sev-warn);  background: var(--sev-warn-bg);  }
    .sev-chip.sev-info  { color: var(--sev-info);  background: var(--sev-info-bg);  }
    .sev-chip.sev-debug { color: var(--sev-debug); background: var(--sev-debug-bg); }
    .sev-chip .sev-glyph { font-weight: 700; }
    /* Detail-pane header: taxon name on the left, action icons on
       the right. Matches TaxonWorks's convention of putting edit /
       new / delete inline with the record heading so scrolling the
       field list doesn't hide the primary actions. */
    .detail-header {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: 1rem;
      margin-bottom: 0.25rem;
    }
    .detail-header h2 {
      margin: 0;
    }
    .header-actions {
      display: inline-flex;
      gap: 0.25rem;
      flex-shrink: 0;
    }
    button.icon-btn {
      background: transparent;
      color: var(--fg);
      border: 1px solid var(--border);
      padding: 0.25rem;
      font-family: inherit;
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      line-height: 1;
    }
    button.icon-btn:hover {
      border-color: var(--accent);
      color: var(--accent);
    }
    button.icon-btn:disabled {
      opacity: 0.4;
      cursor: not-allowed;
    }
    button.icon-btn:disabled:hover {
      border-color: var(--border);
      color: var(--fg);
    }
    button.icon-btn.icon-btn-danger:hover {
      border-color: var(--error);
      color: var(--error);
    }
    button.icon-btn svg { display: block; }
    /* Edit-mode UI. Deliberately plain: platform inputs, single-column,
       no fancy grid — the walking skeleton proves the wire flow, not
       visual polish. */
    .toolbar {
      display: flex;
      gap: 0.5rem;
      align-items: center;
      margin-top: 0.5rem;
    }
    button {
      background: transparent;
      color: var(--fg);
      border: 1px solid var(--border);
      padding: 0.2rem 0.6rem;
      font-family: inherit;
      cursor: pointer;
    }
    button:hover {
      border-color: var(--accent);
    }
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    button[disabled] {
      cursor: not-allowed;
      opacity: 0.5;
    }
    .modal-backdrop {
      position: fixed;
      inset: 0;
      background: color-mix(in oklab, var(--bg) 60%, transparent);
      display: grid;
      place-items: center;
      z-index: 10;
    }
    .modal {
      background: var(--bg);
      border: 1px solid var(--border);
      padding: 1rem 1.25rem;
      min-width: 24rem;
      max-width: 32rem;
      display: grid;
      gap: 0.5rem;
    }
    .modal h3 {
      margin: 0 0 0.25rem 0;
      font-family: var(--font-body);
      font-size: 1.05em;
    }
    .modal p {
      margin: 0;
      color: var(--dim);
      font-size: 0.95em;
    }
    .modal .error {
      color: var(--error);
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    .modal .req {
      color: var(--error);
    }
    form {
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.5rem 0.75rem;
      margin-top: 0.5rem;
      align-items: center;
    }
    form label {
      color: var(--dim);
      font-family: var(--font-mono);
      font-size: 0.9em;
      text-align: right;
    }
    /* Inputs share styles across the edit-form <form> and the
       create-pane <div>. Without width:100% + box-sizing, <input>
       uses HTML-default content-driven sizing and grows as the
       curator types — pushing sibling fields around unpredictably. */
    form input[type="text"],
    form input[type="date"],
    form textarea,
    .create-pane input[type="text"],
    .create-pane input[type="date"],
    .create-pane textarea {
      color: var(--fg);
      background: var(--bg);
      border: 1px solid var(--border);
      padding: 0.3rem 0.4rem;
      font-family: inherit;
      font-size: 1em;
      width: 100%;
      box-sizing: border-box;
      min-width: 0; /* let grid/flex parents shrink us properly */
    }
    form input[type="text"]:focus,
    form input[type="date"]:focus,
    form textarea:focus,
    form select:focus,
    .create-pane input[type="text"]:focus,
    .create-pane input[type="date"]:focus,
    .create-pane textarea:focus,
    .create-pane select:focus {
      outline: none;
      border-color: var(--accent);
    }
    form textarea,
    .create-pane textarea {
      min-height: 4rem;
      font-family: var(--font-body);
    }
    form select {
      color: var(--fg);
      background: var(--bg);
      border: 1px solid var(--border);
      padding: 0.3rem 0.4rem;
      font-family: inherit;
      font-size: 1em;
    }
    .save-error {
      grid-column: 1 / -1;
      color: var(--error);
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    /* Shared fieldset / atomized-toggle / authorship-pair styles used
       by both the create pane and the inline edit form. Scoped to
       :host so they don't leak globally but apply anywhere inside the
       detail component. */
    fieldset {
      border: 1px solid var(--border);
      padding: 0.5rem 0.75rem;
      margin: 0;
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.4rem 0.75rem;
      align-items: center;
    }
    fieldset legend {
      color: var(--dim);
      font-size: 0.85em;
      padding: 0 0.3rem;
    }
    .atomized-toggle {
      display: flex;
      align-items: center;
      gap: 0.4rem;
      font-family: var(--font-body);
      cursor: pointer;
      user-select: none;
    }
    .atomized-toggle .hint {
      color: var(--dim);
      font-size: 0.85em;
    }
    .authorship-pair {
      display: flex;
      gap: 0.75rem;
      flex-wrap: wrap;
    }
    .authorship-pair > fieldset {
      flex: 1 1 16rem;
    }

    /* The create pane takes over the whole right-pane while active.
       Selector-scope its layout to .create-pane so the atomized-preview
       fieldsets don't leak into other panes. Modal wrappers were
       removed — clicking outside used to lose curator work, and a
       pane-native form has no such risk. */
    .create-pane {
      display: grid;
      gap: 0.5rem;
    }
    .create-pane .step-indicator {
      color: var(--dim);
      font-weight: normal;
      font-size: 0.85em;
      margin-left: 0.5rem;
    }
    .create-pane .preview-verbatim {
      font-family: var(--font-mono);
      font-size: 0.9em;
      color: var(--dim);
      border-bottom: 1px solid var(--border);
      padding-bottom: 0.3rem;
    }
    .create-pane .preview-verbatim span {
      color: var(--fg);
    }
    .create-pane .toolbar {
      justify-content: flex-end;
    }
    /* Row wrapping a combobox + adjacent action button so the button
       shrinks and the picker gets the remaining width. */
    .picker-row {
      display: flex;
      gap: 0.3rem;
      align-items: stretch;
    }
    .picker-row > sfga-combobox {
      flex: 1;
      min-width: 0;
    }
    .picker-row > button {
      flex: 0 0 auto;
    }
  `;

  constructor() {
    super();
    this.taxonId = "";
    this.editable = false;
    this._taxon = null;
    this._name = null;
    this._etag = "";
    this._nameEtag = "";
    this._synonyms = [];
    this._error = "";
    this._loading = false;
    this._editing = false;
    this._draft = {};
    this._nameDraft = {};
    // Parent moves are a separate operation (POST /api/taxon/{id}/move),
    // not a field patch. Tracked here as the desired new parent ID (empty
    // means "root"; null/undefined means "no change requested").
    this._parentDraft = null;
    this._parentDraftName = ""; // display name captured from the picker
    this._saving = false;
    this._saveError = "";
    this._creating = false;
    this._createStep = 0;
    this._createDraft = {};
    this._createBusy = false;
    this._createError = "";
    // Both atomized toggles seed from localStorage so a curator who
    // wants to see the parser's atomization gets that view immediately
    // on every taxon they open (not just the first). Persists across
    // sessions too.
    this._createShowAtomized = readAtomizedPref();
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = null;
    this._editShowAtomized = readAtomizedPref();
    this._confirmDelete = false;
    this._deleteError = "";
    this.pendingWarnings = [];
  }

  updated(changed) {
    if (changed.has("taxonId")) {
      // Discard any in-flight edit or in-flight create when the
      // selection changes. Same rule as the TUI's SetCurrent.
      this._editing = false;
      this._draft = {};
      this._nameDraft = {};
      this._saveError = "";
      this._creating = false;
      this._createStep = 0;
      this._createDraft = {};
      this._createError = "";
      this._createShowAtomized = readAtomizedPref();
      this._creatingBasionymFor = null;
      this._creatingBasionymForName = "";
      this._load();
    }
  }

  async _load() {
    if (!this.taxonId) {
      this._taxon = this._name = null;
      this._etag = "";
      this._nameEtag = "";
      this._synonyms = [];
      return;
    }
    // Snapshot the requested ID so stale replies are discarded.
    const requested = this.taxonId;
    this._loading = true;
    this._error = "";
    try {
      const taxon = await api.taxon.get(requested);
      if (this.taxonId !== requested) return;
      this._taxon = taxon;
      // The api client stashes the ETag on the response object; capture it
      // so PATCH can round-trip via If-Match. See lib/api.js.
      this._etag = taxon.__etag || "";

      let name = null;
      let nameEtag = "";
      if (taxon.name_id) {
        try {
          name = await api.name.get(taxon.name_id);
          nameEtag = name.__etag || "";
        } catch (_) {
          // The name might be inaccessible for legacy archives — surface
          // just the taxon rather than propagating the failure.
        }
      }
      if (this.taxonId !== requested) return;
      this._name = name;
      this._nameEtag = nameEtag;

      const syn = await api.taxon.synonyms(requested);
      if (this.taxonId !== requested) return;
      this._synonyms = syn.items || [];
    } catch (err) {
      if (this.taxonId !== requested) return;
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      if (this.taxonId === requested) this._loading = false;
    }
  }

  _startEdit() {
    this._draft = {};
    this._nameDraft = {};
    this._parentDraft = null;
    this._parentDraftName = "";
    this._saveError = "";
    this._editing = true;
  }

  _cancelEdit() {
    this._draft = {};
    this._nameDraft = {};
    this._parentDraft = null;
    this._parentDraftName = "";
    this._saveError = "";
    this._editing = false;
  }

  // _openCreate opens the create pane with the current taxon as the
  // parent — the "new child" flow.
  async _openCreate() {
    return this._openCreateWithParent(
      this._taxon?.id || "",
      this._taxon?.label?.text || this._taxon?.id || "(root)",
    );
  }

  // _openCreateSister opens the create pane with the current taxon's
  // parent as the parent — so the new taxon slots in as a sibling of
  // the current one. If the current taxon is itself a root, the sister
  // is also a root.
  async _openCreateSister() {
    const parentID = this._taxon?.parent_id || "";
    const parentLabel = this._taxon?.parent
      ? this._taxon.parent.label?.text || this._taxon.parent.id || "(root)"
      : "(root)";
    return this._openCreateWithParent(parentID, parentLabel);
  }

  // _openCreateWithParent is the shared open path. Records the intended
  // parent id + label so _submitCreate can attach to the right parent
  // and the pane's heading names it correctly. In parallel it fetches:
  //   - code default (parent's nom_code) so the code picker on step 1
  //     is pre-seeded and ICZN work stays ICZN.
  //   - scientific-name prefix (parent's sci-name + " " when the child
  //     will be a compound name) so the curator only types the new
  //     epithet — "Felis " → curator adds "catus (L., 1758)".
  // Both fetches are best-effort: failures leave the corresponding
  // field empty and the curator types manually.
  async _openCreateWithParent(parentID, parentLabel) {
    this._createDraft = { scientific_name: "", code: "" };
    this._createStep = 0;
    this._createError = "";
    this._createBusy = false;
    this._createParentID = parentID;
    this._createParentLabel = parentLabel;
    this._creating = true;
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    if (parentID) {
      try {
        const [codeResp, prefixResp, childRanksResp] = await Promise.all([
          api.taxon.codeDefault(parentID).catch(() => ({ code: "" })),
          api.taxon.createNamePrefix(parentID).catch(() => ({ prefix: "" })),
          api.taxon.childRanks(parentID).catch(() => ({ items: [] })),
        ]);
        if (this._creating) {
          this._createDraft = {
            ...this._createDraft,
            code: this._createDraft.code || codeResp.code || "",
            scientific_name: this._createDraft.scientific_name || prefixResp.prefix || "",
          };
          // items === [] and items === null both signal "no filter" —
          // an empty list means every rank is invalid, which we treat
          // as "the server has no opinion" per handleChildRanks.
          const items = childRanksResp.items || [];
          this._createChildRanks = items.length > 0 ? items : null;
        }
      } catch (_) {
        /* leave defaults empty; curator will fill */
      }
    }
    // Park the cursor at the end of the pre-filled sci-name so a
    // curator hitting `c` immediately types the epithet after (say)
    // "Felis catus " without having to click or arrow-right. Awaits
    // updateComplete so the input actually exists in the DOM; runs
    // even for empty-prefix cases (autofocus + cursor at 0) so the
    // flow is uniform.
    await this.updateComplete;
    if (this._creating && this._createStep === 0) {
      const input = this.renderRoot.querySelector(".create-pane input[type='text']");
      if (input) {
        input.focus();
        const end = input.value.length;
        input.setSelectionRange(end, end);
      }
    }
  }

  _cancelCreate() {
    this._creating = false;
    this._createStep = 0;
    this._createError = "";
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
  }

  // _advanceToPreview fires the server-side parse (POST /api/name/parse)
  // and pre-fills the atomized preview form with the result. Step 0 →
  // step 1. Fills only fields the curator hasn't already touched — same
  // "caller-supplied wins" rule the server uses on write.
  async _advanceToPreview() {
    const sci = (this._createDraft.scientific_name || "").trim();
    if (!sci) {
      this._createError = "scientific name is required";
      return;
    }
    // Code is optional here — pre-fetched from CodeForParent for the
    // common inherit-from-parent case, empty for root taxa. Passing
    // "" to /api/name/parse just skips the suffix-rule tier of
    // RankGuess. Curator can set/override on step 1's bottom row.
    this._createBusy = true;
    this._createError = "";
    try {
      const preview = await api.name.parse(sci, this._createDraft.code);
      // Merge: preview values fill fields the curator hasn't set. The
      // verbatim + code the curator entered on step 0 always win.
      const merged = { ...preview };
      merged.scientific_name = sci;
      merged.scientific_name_string = sci;
      merged.code = this._createDraft.code;
      // Strip fields we don't want to send back on POST /api/taxon
      // (server-authored on write).
      delete merged.id;
      delete merged.modified;
      delete merged.modified_by;
      delete merged.parse_quality;
      delete merged.cardinality;
      delete merged.gn_id;
      delete merged.authors;
      delete merged.canonical_simple;
      delete merged.canonical_full;
      delete merged.canonical_stemmed;
      delete merged.reference_label;
      this._createDraft = merged;
      this._createStep = 1;
    } catch (err) {
      this._createError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    } finally {
      this._createBusy = false;
    }
  }

  _backToVerbatim() {
    // Preserve the verbatim + code so the curator doesn't lose their
    // typing. Everything else is discarded — re-parsing the same
    // verbatim from step 0 will re-fill it.
    const keep = {
      scientific_name: this._createDraft.scientific_name || "",
      code: this._createDraft.code || "",
    };
    this._createDraft = keep;
    this._createStep = 0;
    this._createError = "";
  }

  _createFieldChange(field, value) {
    this._createDraft = { ...this._createDraft, [field]: value };
  }

  async _submitCreate() {
    this._createBusy = true;
    this._createError = "";
    try {
      if (this._creatingBasionymFor) {
        // Basionym write path — POST /api/taxon/{X}/basionym creates
        // Name + Synonym + BASIONYM name_relation atomically. Reveals
        // the current-combination taxon (not the basionym; the basionym
        // is a synonym, not an accepted taxon in the tree).
        const revealID = this._creatingBasionymFor;
        await api.taxon.addBasionym(this._creatingBasionymFor, this._createDraft);
        this._cancelCreate();
        this.dispatchEvent(
          new CustomEvent("taxon-moved", {
            detail: { id: revealID },
            bubbles: true,
            composed: true,
          }),
        );
      } else {
        // Normal accepted-name create. parent_id is fixed at open
        // time so new-child and new-sister route to the right parent
        // regardless of tree state.
        const body = {
          ...this._createDraft,
          parent_id: this._createParentID || "",
        };
        const created = await api.taxon.create(body);
        this._cancelCreate();
        this.dispatchEvent(
          new CustomEvent("taxon-moved", {
            detail: { id: created.id, warnings: created.warnings || [] },
            bubbles: true,
            composed: true,
          }),
        );
      }
    } catch (err) {
      this._createError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    } finally {
      this._createBusy = false;
    }
  }

  // _submitCreateThenBasionym is the "Create + add original combination"
  // path. Saves the current combination first (POST /api/taxon), then
  // transitions the same pane into basionym-add mode targeting the
  // just-created taxon. The curator lands on step 0 with a fresh
  // scientific-name input; when they save that, the basionym write
  // (POST /api/taxon/{X}/basionym) fires and the pane closes.
  //
  // Two-step commit — either half can fail independently. If the
  // accepted-name save fails, the pane stays put and shows the error.
  // If it succeeds and the basionym half is then abandoned (curator
  // hits Cancel), the accepted taxon is still saved. That's the
  // expected semantics: adding a basionym is optional; the accepted
  // name is the primary commit.
  async _submitCreateThenBasionym() {
    this._createBusy = true;
    this._createError = "";
    try {
      const body = {
        ...this._createDraft,
        parent_id: this._taxon?.id || "",
      };
      const created = await api.taxon.create(body);
      // Reset the form for the basionym half. Keep the code (usually
      // matches the current combination's) but blank everything else
      // so the curator types the original name fresh.
      const keepCode = this._createDraft.code || "";
      this._createDraft = { scientific_name: "", code: keepCode };
      this._createStep = 0;
      this._createShowAtomized = readAtomizedPref();
      this._creatingBasionymFor = created.id;
      this._creatingBasionymForName =
        created.label?.text || body.scientific_name || "the current combination";
    } catch (err) {
      this._createError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    } finally {
      this._createBusy = false;
    }
  }

  _askDelete() {
    this._deleteError = "";
    this._confirmDelete = true;
  }

  _cancelDelete() {
    this._confirmDelete = false;
    this._deleteError = "";
  }

  async _submitDelete() {
    if (!this._taxon) return;
    try {
      const res = await api.taxon.delete(this._taxon.id);
      this._confirmDelete = false;
      // Reveal the parent (or clear selection if the deleted taxon was
      // a root) via the shell's move-handler pipeline.
      this.dispatchEvent(
        new CustomEvent("taxon-deleted", {
          detail: { deleted_id: res.deleted_id, parent_id: res.parent_id },
          bubbles: true,
          composed: true,
        }),
      );
    } catch (err) {
      this._deleteError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    }
  }

  _renderCreatePane() {
    // Parent label was captured at open time (new-child vs new-sister)
    // so the heading names the right ancestor regardless of any tree
    // selection changes since.
    const parentLabel = this._createParentLabel || "(root)";
    const heading = this._creatingBasionymFor
      ? html`Add original combination for
          <em>${this._creatingBasionymForName}</em>`
      : html`New taxon under ${parentLabel}`;
    return html`
      <div class="create-pane">
        <h2>
          ${heading}
          <span class="step-indicator">
            step ${this._createStep + 1} / 2 —
            ${this._createStep === 0 ? "verbatim" : "atomized preview"}
          </span>
        </h2>
        <hr />
        ${this._createError
          ? html`<div class="error">${this._createError}</div>`
          : ""}
        ${this._createStep === 0
          ? this._renderCreateStep0()
          : this._renderCreateStep1()}
      </div>
    `;
  }

  // Step 0: just the verbatim scientific name. Code has moved to the
  // bottom of step 1 so the common inherit-from-parent path never
  // requires focusing it. The pre-fetched default from
  // api.taxon.codeDefault(parentId) still flows into ParseName's
  // rank-guess so suffix rules work invisibly.
  _renderCreateStep0() {
    return html`
      <label>Scientific name + authorship <span class="req">*</span></label>
      <input
        type="text"
        .value=${this._createDraft.scientific_name || ""}
        placeholder="e.g. Panthera onca (Linnaeus, 1758)"
        @input=${(e) => this._createFieldChange("scientific_name", e.target.value)}
        @keydown=${(e) => {
          if (e.key === "Enter") this._advanceToPreview();
          if (e.key === "Escape") this._cancelCreate();
        }}
        autofocus
      />
      <div class="toolbar">
        <button
          class="primary"
          @click=${() => this._advanceToPreview()}
          ?disabled=${this._createBusy}
        >
          ${this._createBusy ? "parsing…" : "next → preview"}
        </button>
        <button @click=${() => this._cancelCreate()}>cancel</button>
      </div>
    `;
  }

  // Step 1: atomized preview. Curator sees the essentials up front
  // (name recap, rank, verbatim authorship, reference, status, notes)
  // and can expand a "show atomized fields" toggle to reveal + edit
  // the individual col__ columns gnparser derived. Parsing runs
  // regardless — the toggle only affects visibility. gsvalidator will
  // flag parse-mismatch cases regardless of whether the curator ever
  // opened the expanded view, so trust-the-parse and verify-the-parse
  // paths both stay safe.
  _renderCreateStep1() {
    const d = this._createDraft;
    const set = (f) => (e) => this._createFieldChange(f, e.target.value);
    return html`
      <div class="preview-verbatim">
        Verbatim: <span>${d.scientific_name}</span>
      </div>

      <fieldset>
        <legend>name</legend>
        <label>Rank</label>
        <sfga-combobox
          min-search-chars="0"
          placeholder="rank…"
          .source=${childRankSource(this._createChildRanks)}
          .resolver=${vocabResolver("rank")}
          .value=${d.rank || ""}
          @pick=${(e) => this._createFieldChange("rank", e.detail.id)}
        ></sfga-combobox>

        <label>Verbatim authorship</label>
        <input type="text" .value=${d.authorship || ""} @input=${set("authorship")} />
      </fieldset>

      <label class="atomized-toggle">
        <input
          type="checkbox"
          .checked=${this._createShowAtomized}
          @change=${(e) => {
            this._createShowAtomized = e.target.checked;
            this._editShowAtomized = e.target.checked;
            writeAtomizedPref(e.target.checked);
          }}
        />
        show atomized fields
        <span class="hint">
          (verify or override the parse; hive parses in the background
          regardless)
        </span>
      </label>

      ${this._createShowAtomized ? this._renderAtomizedFieldset(d, set) : ""}

      <fieldset>
        <legend>publication</legend>
        <label>${referenceLabelFor(d)}</label>
        <sfga-combobox
          min-search-chars="2"
          placeholder="search references…"
          .source=${referenceSource}
          .resolver=${referenceResolver}
          .value=${d.reference_id || ""}
          @pick=${(e) => this._createFieldChange("reference_id", e.detail.id)}
        ></sfga-combobox>
        <label>Published in page</label>
        <input type="text" .value=${d.published_in_page || ""} @input=${set("published_in_page")} />
      </fieldset>

      <fieldset>
        <legend>metadata</legend>
        <label>Nom status</label>
        <sfga-combobox
          placeholder="nomenclatural status…"
          .source=${nomenSource(d.code)}
          .resolver=${nomenResolver}
          .value=${d.status || ""}
          @pick=${(e) => this._createFieldChange("status", e.detail.id)}
        ></sfga-combobox>
        <label>Etymology</label>
        <input type="text" .value=${d.etymology || ""} @input=${set("etymology")} />
        <label>Remarks</label>
        <textarea .value=${d.remarks || ""} @input=${set("remarks")}></textarea>
      </fieldset>

      <!-- Nomenclatural code lives at the very bottom.
           CodeForParent pre-fills it from the parent's name so the
           common path never focuses this row; the affordance is here
           for the exceptions (root taxa, deliberate mixed-code
           subtrees like protists). Matches the TUI's placement. -->
      <fieldset>
        <legend>nomenclatural code</legend>
        <label>Code</label>
        <sfga-combobox
          min-search-chars="0"
          placeholder="nomenclatural code…"
          .source=${vocabSource("nom_code")}
          .resolver=${vocabResolver("nom_code")}
          .value=${d.code || ""}
          @pick=${(e) => this._createFieldChange("code", e.detail.id)}
        ></sfga-combobox>
      </fieldset>

      <div class="toolbar">
        <button @click=${() => this._backToVerbatim()}>← back</button>
        <button
          class="primary"
          @click=${() => this._submitCreate()}
          ?disabled=${this._createBusy}
        >
          ${this._createBusy
            ? "creating…"
            : this._creatingBasionymFor
              ? "add basionym"
              : "create"}
        </button>
        ${this._shouldOfferBasionymAfterCreate(d)
          ? html`
              <button
                @click=${() => this._submitCreateThenBasionym()}
                ?disabled=${this._createBusy}
                title="Save the current combination, then enter the original combination as its basionym"
              >
                create + add original combination →
              </button>
            `
          : ""}
        <button @click=${() => this._cancelCreate()}>cancel</button>
      </div>
    `;
  }

  // _shouldOfferBasionymAfterCreate returns true when the current draft
  // looks like a subsequent combination — parenthetical author in the
  // verbatim, or an atomized basionym_authorship value from the parse.
  // Only offered on the accepted-name create path; the basionym-add
  // path itself hides the button to avoid infinite regress.
  _shouldOfferBasionymAfterCreate(d) {
    if (this._creatingBasionymFor) return false;
    if ((d.scientific_name || "").includes("(")) return true;
    if ((d.basionym_authorship || "").trim()) return true;
    if ((d.basionym_authorship_year || "").trim()) return true;
    return false;
  }

  // _renderAtomizedFieldset is the expanded "atomized fields" section
  // hidden behind the toggle. Two blocks:
  //   1. Name atomization (uninomial / genus / subgenus / species /
  //      infraspecies / cultivar) — one grid.
  //   2. Authorship atomization: Basionym (original) and Combination
  //      (current) rendered side-by-side. Botanists use both fields
  //      routinely ("Aus bus (L.) Smith" → basionym=L., combination=Smith).
  //      Zoologists often leave combination blank; the layout is the
  //      same either way. CoLDP maps 1-to-1.
  _renderAtomizedFieldset(d, set) {
    return html`
      <fieldset>
        <legend>atomized name</legend>
        <label>Uninomial</label>
        <input type="text" .value=${d.uninomial || ""} @input=${set("uninomial")} />
        <label>Genus</label>
        <input type="text" .value=${d.genus || ""} @input=${set("genus")} />
        <label>Subgenus</label>
        <input type="text" .value=${d.infrageneric_epithet || ""} @input=${set("infrageneric_epithet")} />
        <label>Specific epithet</label>
        <input type="text" .value=${d.specific_epithet || ""} @input=${set("specific_epithet")} />
        <label>Infraspecific epithet</label>
        <input type="text" .value=${d.infraspecific_epithet || ""} @input=${set("infraspecific_epithet")} />
        <label>Cultivar epithet</label>
        <input type="text" .value=${d.cultivar_epithet || ""} @input=${set("cultivar_epithet")} />
      </fieldset>

      <div class="authorship-pair">
        <fieldset>
          <legend>basionym (original)</legend>
          <label>Author</label>
          <input type="text" .value=${d.basionym_authorship || ""} @input=${set("basionym_authorship")} />
          <label>Year</label>
          <input type="text" .value=${d.basionym_authorship_year || ""} @input=${set("basionym_authorship_year")} />
        </fieldset>
        <fieldset>
          <legend>combination (current)</legend>
          <label>Author</label>
          <input type="text" .value=${d.combination_authorship || ""} @input=${set("combination_authorship")} />
          <label>Year</label>
          <input type="text" .value=${d.combination_authorship_year || ""} @input=${set("combination_authorship_year")} />
        </fieldset>
      </div>
    `;
  }

  _renderAddReferenceModal() {
    // Context for the BHLnames tab: canonical + authorship + year.
    // Read from the *current* form values so a curator who has been
    // editing the scientific name gets a BHLnames match on what
    // they've typed, not the pre-edit form.
    const canonical =
      this._name?.canonical_simple ||
      this._nameFieldValue("scientific_name_string") ||
      this._name?.scientific_name ||
      "";
    const authors = this._name?.authors || "";
    const year = parseInt(this._name?.published_in_year || "", 10) || 0;
    return html`
      <sfga-add-reference-modal
        .contextCanonical=${canonical}
        .contextAuthors=${authors}
        .contextYear=${year}
        @reference-picked=${(e) => this._onReferencePicked(e)}
        @close=${() => (this._addingReference = false)}
      ></sfga-add-reference-modal>
    `;
  }

  _onReferencePicked(e) {
    // Modal supplies {id, label}. Update the draft and stash the label
    // so the combobox displays it immediately (its resolver would
    // otherwise fire a second GET before the label appears).
    const { id, label } = e.detail;
    this._nameFieldChange("reference_id", id);
    this._pickedReferenceLabel = label || "";
    this._addingReference = false;
  }

  _renderDeleteModal() {
    const label = this._taxon?.label?.text || this._taxon?.id || "(unknown)";
    return html`
      <div class="modal-backdrop" @click=${() => this._cancelDelete()}>
        <div class="modal" @click=${(e) => e.stopPropagation()}>
          <h3>Delete ${label}?</h3>
          ${this._deleteError
            ? html`<div class="error">${this._deleteError}</div>`
            : html`<p>
                Deleting removes the taxon and its per-taxon associations
                (synonyms, vernaculars, distributions). Names are shared and
                remain intact. Taxa with children are refused — reparent or
                delete descendants first.
              </p>`}
          <div class="toolbar">
            <button class="primary" @click=${() => this._submitDelete()}>delete</button>
            <button @click=${() => this._cancelDelete()}>cancel</button>
          </div>
        </div>
      </div>
    `;
  }

  _onParentPick(e) {
    // The picker emits {id, name}; empty id means "move to root".
    this._parentDraft = e.detail.id;
    this._parentDraftName = e.detail.name;
  }

  _fieldChange(field, value) {
    this._draft = { ...this._draft, [field]: value };
  }

  _nameFieldChange(field, value) {
    this._nameDraft = { ...this._nameDraft, [field]: value };
  }

  async _save() {
    if (!this._taxon) return;
    const hasTaxonEdits = Object.keys(this._draft).length > 0;
    const hasNameEdits = Object.keys(this._nameDraft).length > 0 && this._name;
    const hasParentMove =
      this._parentDraft !== null && this._parentDraft !== (this._taxon.parent_id ?? "");
    if (!hasTaxonEdits && !hasNameEdits && !hasParentMove) {
      this._editing = false;
      return;
    }
    this._saving = true;
    this._saveError = "";
    try {
      // Order: move first, then patch. Rationale: a move refreshes the
      // taxon's col__modified, so doing patch first and then move would
      // burn the patch's ETag on the subsequent move call. Move first,
      // capture its new ETag, then patch against that.
      //
      // Still NOT atomic across the three operations. A future batch
      // endpoint (POST /api/taxon/{id}/apply with a plan body) would fix it.
      let parentMoved = false;
      if (hasParentMove) {
        const moved = await api.taxon.move(this._taxon.id, this._parentDraft, this._etag);
        this._taxon = moved;
        this._etag = moved.__etag || "";
        this._parentDraft = null;
        this._parentDraftName = "";
        parentMoved = true;
      }
      if (hasTaxonEdits) {
        const updated = await api.taxon.patch(this._taxon.id, this._draft, this._etag);
        this._taxon = updated;
        this._etag = updated.__etag || "";
        this._draft = {};
      }
      if (hasNameEdits) {
        const updated = await api.name.patch(this._name.id, this._nameDraft, this._nameEtag);
        this._name = updated;
        this._nameEtag = updated.__etag || "";
        this._nameDraft = {};
      }
      this._editing = false;
      if (parentMoved) {
        // Tell the shell to reveal the taxon in its new tree location.
        // The tree pane fetches ancestors, expands the chain, and scrolls
        // the moved row into view so the curator sees the result of their
        // reparent without a full page reload.
        this.dispatchEvent(
          new CustomEvent("taxon-moved", {
            detail: { id: this._taxon.id },
            bubbles: true,
            composed: true,
          }),
        );
      }
    } catch (err) {
      // 409 (stale If-Match) is the common case; render its detail so
      // curators know why their save didn't land. A future refinement
      // could offer a "reload and retry" button.
      if (err instanceof Problem) {
        this._saveError = `${err.title}: ${err.detail || err.message}`;
      } else {
        this._saveError = String(err);
      }
    } finally {
      this._saving = false;
    }
  }

  // _fieldValue returns the current in-form value for a field: the draft
  // override if the user has touched it, otherwise the current taxon value.
  // Handles booleans (Extinct) specially — the draft may explicitly set
  // false/true, which are both legal draft values.
  _fieldValue(field) {
    if (Object.hasOwn(this._draft, field)) return this._draft[field];
    return this._taxon[field] ?? "";
  }

  // Parallel of _fieldValue for the name aggregate.
  _nameFieldValue(field) {
    if (Object.hasOwn(this._nameDraft, field)) return this._nameDraft[field];
    return this._name?.[field] ?? "";
  }

  render() {
    if (!this.taxonId) {
      return html`<div class="empty">Select a taxon to see its details.</div>`;
    }
    if (this._error) {
      return html`<div class="error">${this._error}</div>`;
    }
    if (this._loading && !this._taxon) {
      return html`<div class="empty">loading…</div>`;
    }
    if (!this._taxon) return html``;

    // Create mode takes over the whole pane — there's no existing taxon
    // to render behind it, and the modal-in-overlay pattern's
    // click-outside-loses-work risk was real. Curator uses the toolbar
    // buttons (or Esc) to cancel back to view mode.
    if (this._creating) {
      return this._renderCreatePane();
    }

    // Server-rendered label: text + html forms. html carries dagger and
    // italics per rank (BuildLabel). Fall back to canonical / (no name)
    // if the server didn't attach a label (legacy or missing name row).
    const heading = this._taxon.label?.html
      ? html`${unsafeHTML(this._taxon.label.html)}`
      : this._name?.canonical_simple ||
        this._name?.scientific_name ||
        "(no name)";

    return html`
      <div class="detail-header">
        <h2>${heading}</h2>
        ${this.editable && !this._editing ? this._renderHeaderActions() : ""}
      </div>
      <hr />
      ${this._renderPendingWarnings()}
      ${this._editing ? this._renderEditForm() : this._renderViewFields()}
      ${this._renderSynonyms()}
    `;
  }

  // _renderPendingWarnings shows the gsvalidator soft warnings for the
  // currently displayed taxon. Prefers shell-supplied `pendingWarnings`
  // (freshest — set by the just-completed create/update round-trip);
  // falls back to `_taxon.warnings` from the GET response so the banner
  // reappears when a curator returns to the record later. Hidden while
  // the edit form is open so it doesn't fight the form for attention.
  _renderPendingWarnings() {
    const fresh = (this.pendingWarnings && this.pendingWarnings.length)
      ? this.pendingWarnings
      : (this._taxon?.warnings || []);
    if (fresh.length === 0 || this._editing) return "";
    const warnings = fresh;
    const heading = `${warnings.length} open issue${warnings.length > 1 ? "s" : ""}:`;
    return html`
      <div class="warning-banner">
        <strong>${heading}</strong>
        <ul>
          ${warnings.map(
            (w) => html`<li>
              ${severityChip(w.severity)}
              <span>
                <span class="warning-rule">${w.rule_name || w.rule_id}</span>:
                ${w.message}
              </span>
            </li>`,
          )}
        </ul>
      </div>
    `;
  }

  // _renderHeaderActions is the compact icon-button strip floated to
  // the right of the taxon name in the detail-pane header. Kept out of
  // the field list at the bottom so scrolling long records doesn't
  // hide the primary actions.
  //
  // Matches the TaxonWorks convention of surfacing edit / new-child /
  // new-sister / delete inline with the taxon name. Icons + tooltips
  // rather than text — a curator picking up the pattern learns four
  // symbols once and gets a much more scannable pane forever after.
  _renderHeaderActions() {
    return html`
      <div class="header-actions">
        <button
          class="icon-btn"
          @click=${() => this._startEdit()}
          title="edit (e)"
          aria-label="edit"
        >
          ${renderIcon("pencil", 18)}
        </button>
        <button
          class="icon-btn"
          @click=${() => this._openCreate()}
          title="new child (n) — adds under this taxon"
          aria-label="new child"
        >
          ${renderIcon("tree-child-plus", 18)}
        </button>
        <button
          class="icon-btn"
          @click=${() => this._openCreateSister()}
          title="new sister — adds at the same level"
          aria-label="new sister"
          ?disabled=${!this._taxon}
        >
          ${renderIcon("tree-sister-plus", 18)}
        </button>
        <button
          class="icon-btn icon-btn-danger"
          @click=${() => this._askDelete()}
          title="delete (d)"
          aria-label="delete"
        >
          ${renderIcon("trash-2", 18)}
        </button>
      </div>
    `;
  }

  _renderViewFields() {
    // Row helper: skip empty values so the pane stays scannable. Modified
    // fields always show since even "unset" is meaningful for auditing.
    const t = this._taxon;
    const n = this._name;
    const row = (label, value) =>
      value === undefined || value === null || value === ""
        ? ""
        : html`<dt>${label}</dt>
            <dd>${value}</dd>`;
    const rowLink = (label, value) =>
      !value
        ? ""
        : html`<dt>${label}</dt>
            <dd><a href=${value}>${value}</a></dd>`;

    // Parent is a resolved reference — render the label when the server
    // supplied one, fall back to the raw id if the parent row is missing.
    const parentDisplay = t.parent
      ? renderLabel(t.parent.label, t.parent.id)
      : t.parent_id || "";

    return html`
      <dl>
        ${row("ID", t.id)} ${row("Parent", parentDisplay)}
        ${row("Rank", n?.rank ? n.rank.toLowerCase() : "")}
        <dt>Status</dt>
        <dd>${t.status || "accepted"}</dd>
        ${t.extinct !== undefined
          ? html`<dt>Extinct</dt>
              <dd>${t.extinct ? "yes" : "no"}</dd>`
          : ""}
        <!-- Name-derived fields — only render when a name is attached. -->
        ${n ? row("Authorship", n.authorship) : ""}
        ${n ? row("Nom code", n.code) : ""}
        ${n ? row("Nom status", api.nomen.labelFor(n.status) || n.status) : ""}
        <!-- Atomized authorship — only render when populated so the
             record stays scannable. Botanical records typically have
             both; zoological records often just have basionym. -->
        ${n && n.basionym
          ? html`<dt>Basionym</dt>
              <dd>${renderLabel(n.basionym.label, n.basionym.id)}</dd>`
          : ""}
        ${n && (n.basionym_authorship || n.basionym_authorship_year)
          ? row(
              "Basionym authorship",
              [n.basionym_authorship, n.basionym_authorship_year].filter((x) => x).join(", "),
            )
          : ""}
        ${n && (n.combination_authorship || n.combination_authorship_year)
          ? row(
              "Combination authorship",
              [n.combination_authorship, n.combination_authorship_year].filter((x) => x).join(", "),
            )
          : ""}
        ${n ? row("Reference", n.reference_label || n.reference_id) : ""}
        ${n ? row("Published year", n.published_in_year) : ""}
        ${n ? row("Etymology", n.etymology) : ""}
        ${n ? rowLink("Name link", n.link) : ""}
        ${n ? row("Name remarks", n.remarks) : ""}
        <!-- Taxon extras -->
        ${row("Name phrase", t.name_phrase)}
        ${t.scrutinizer
          ? html`<dt>Scrutinizer</dt>
              <dd>${t.scrutinizer}${t.scrutinizer_id ? html` (${orcidLink(t.scrutinizer_id)})` : ""}</dd>`
          : ""}
        ${rowLink("Link", t.link)} ${row("Remarks", t.remarks)}
        <!-- Audit trail -->
        <dt>Modified</dt>
        <dd>${t.modified}</dd>
        ${row("By", orcidLink(t.modified_by))}
        ${n && n.modified && n.modified !== t.modified
          ? html`<dt>Name modified</dt>
                <dd>${n.modified}</dd>
                ${row("Name mod. by", orcidLink(n.modified_by))}`
          : ""}
      </dl>
      ${this._confirmDelete ? this._renderDeleteModal() : ""}
    `;
  }

  _renderEditForm() {
    // Extinct is a tri-state (unknown / yes / no) — represented as an
    // empty-string / "true" / "false" select. On save, "" is treated as
    // "leave alone" (draft doesn't include the field); the other two are
    // sent as bool.
    const extinctValue =
      this._draft.extinct === undefined
        ? this._taxon.extinct === undefined
          ? ""
          : this._taxon.extinct
            ? "true"
            : "false"
        : this._draft.extinct === true
          ? "true"
          : "false";

    // Parent picker: reads either the draft (if the user picked) or the
    // current taxon.parent_id. Empty means "root."
    const currentParentID = this._taxon.parent_id ?? "";
    const parentID =
      this._parentDraft !== null ? this._parentDraft : currentParentID;

    return html`
      <form @submit=${(e) => e.preventDefault()}>
        <label>Parent</label>
        <sfga-combobox
          min-search-chars="2"
          placeholder="type to search taxa…"
          .source=${taxonSource}
          .resolver=${taxonResolver}
          .value=${parentID}
          .valueName=${this._parentDraft !== null ? this._parentDraftName : ""}
          @pick=${(e) => this._onParentPick(e)}
        ></sfga-combobox>

        <label for="edit-name-phrase">Name phrase</label>
        <input
          id="edit-name-phrase"
          type="text"
          .value=${this._fieldValue("name_phrase")}
          @input=${(e) => this._fieldChange("name_phrase", e.target.value)}
        />

        <label for="edit-scrutinizer">Scrutinizer</label>
        <input
          id="edit-scrutinizer"
          type="text"
          .value=${this._fieldValue("scrutinizer")}
          @input=${(e) => this._fieldChange("scrutinizer", e.target.value)}
        />

        <label for="edit-scrutinizer-id">Scrutinizer ID</label>
        <input
          id="edit-scrutinizer-id"
          type="text"
          placeholder="ORCID or other identifier"
          .value=${this._fieldValue("scrutinizer_id")}
          @input=${(e) => this._fieldChange("scrutinizer_id", e.target.value)}
        />

        <label for="edit-scrutinizer-date">Scrutinizer date</label>
        <input
          id="edit-scrutinizer-date"
          type="date"
          .value=${this._fieldValue("scrutinizer_date")}
          @input=${(e) => this._fieldChange("scrutinizer_date", e.target.value)}
        />

        <label for="edit-extinct">Extinct</label>
        <select
          id="edit-extinct"
          .value=${extinctValue}
          @change=${(e) => {
            const v = e.target.value;
            if (v === "") {
              // Remove from draft — send nothing for this field.
              const { extinct, ...rest } = this._draft;
              this._draft = rest;
              this.requestUpdate();
            } else {
              this._fieldChange("extinct", v === "true");
            }
          }}
        >
          <option value="">(unset)</option>
          <option value="true">yes</option>
          <option value="false">no</option>
        </select>

        <label for="edit-link">Link</label>
        <input
          id="edit-link"
          type="text"
          .value=${this._fieldValue("link")}
          @input=${(e) => this._fieldChange("link", e.target.value)}
        />

        <label for="edit-remarks">Taxon remarks</label>
        <textarea
          id="edit-remarks"
          .value=${this._fieldValue("remarks")}
          @input=${(e) => this._fieldChange("remarks", e.target.value)}
        ></textarea>

        ${this._name ? this._renderNameSection() : ""}

        ${this._saveError ? html`<div class="save-error">${this._saveError}</div>` : ""}

        <div class="toolbar" style="grid-column: 1 / -1">
          <button class="primary" @click=${() => this._save()} ?disabled=${this._saving}>
            ${this._saving ? "saving…" : "save"}
          </button>
          <button @click=${() => this._cancelEdit()} ?disabled=${this._saving}>
            cancel
          </button>
        </div>
      </form>
    `;
  }

  // _renderNameSection renders the name-editing block. Hidden when the
  // taxon has no attached name. Mirrors the create pane's step-1 form
  // structure — same field set, same progressive-disclosure toggle for
  // atomized fields, same dynamic reference label. Curators learn one
  // form and use it for both create and edit.
  _renderNameSection() {
    // Merge the current name + any in-flight draft so referenceLabelFor
    // sees whatever atomized authorship values the curator has typed.
    const merged = { ...(this._name || {}), ...this._nameDraft };
    return html`
      <h3 class="section" style="grid-column: 1 / -1; margin: 0.75rem 0 0 0; color: var(--dim); font-size: 0.95em;">
        ── Name ──
      </h3>

      <label for="edit-scientific">Scientific name</label>
      <input
        id="edit-scientific"
        type="text"
        placeholder="verbatim string with authorship"
        .value=${this._nameFieldValue("scientific_name_string")}
        @input=${(e) => this._nameFieldChange("scientific_name_string", e.target.value)}
      />

      <label>Rank</label>
      <sfga-combobox
        placeholder="rank…"
        .source=${vocabSource("rank")}
        .resolver=${vocabResolver("rank")}
        .value=${this._nameFieldValue("rank")}
        @pick=${(e) => this._nameFieldChange("rank", e.detail.id)}
      ></sfga-combobox>

      <label>Code</label>
      <sfga-combobox
        placeholder="nomenclatural code…"
        .source=${vocabSource("nom_code")}
        .resolver=${vocabResolver("nom_code")}
        .value=${this._nameFieldValue("code")}
        @pick=${(e) => this._nameFieldChange("code", e.detail.id)}
      ></sfga-combobox>

      <label>Verbatim authorship</label>
      <input
        type="text"
        .value=${this._nameFieldValue("authorship")}
        @input=${(e) => this._nameFieldChange("authorship", e.target.value)}
      />

      <label class="atomized-toggle" style="grid-column: 1 / -1">
        <input
          type="checkbox"
          .checked=${this._editShowAtomized}
          @change=${(e) => {
            this._editShowAtomized = e.target.checked;
            this._createShowAtomized = e.target.checked;
            writeAtomizedPref(e.target.checked);
          }}
        />
        show atomized fields
        <span class="hint">
          (verify or override the parse — hive parses in the background
          regardless)
        </span>
      </label>

      ${this._editShowAtomized ? this._renderAtomizedEditFields() : ""}

      <label>Nom status</label>
      <sfga-combobox
        placeholder="nomenclatural status…"
        .source=${nomenSource(this._nameFieldValue("code"))}
        .resolver=${nomenResolver}
        .value=${this._nameFieldValue("status")}
        @pick=${(e) => this._nameFieldChange("status", e.detail.id)}
      ></sfga-combobox>

      <label>${referenceLabelFor(merged)}</label>
      <div class="picker-row">
        <sfga-combobox
          min-search-chars="2"
          placeholder="search references…"
          .source=${referenceSource}
          .resolver=${referenceResolver}
          .value=${this._nameFieldValue("reference_id")}
          .valueName=${this._pickedReferenceLabel !== undefined
            ? this._pickedReferenceLabel
            : Object.hasOwn(this._nameDraft, "reference_id")
              ? ""
              : this._name?.reference_label || ""}
          @pick=${(e) => this._nameFieldChange("reference_id", e.detail.id)}
        ></sfga-combobox>
        <button
          type="button"
          title="add a reference from BHLnames / DOI / BibTeX"
          @click=${() => (this._addingReference = true)}
        >
          add…
        </button>
      </div>
      ${this._addingReference ? this._renderAddReferenceModal() : ""}

      <label for="edit-etymology">Etymology</label>
      <input
        id="edit-etymology"
        type="text"
        .value=${this._nameFieldValue("etymology")}
        @input=${(e) => this._nameFieldChange("etymology", e.target.value)}
      />

      <label for="edit-name-remarks">Name remarks</label>
      <textarea
        id="edit-name-remarks"
        .value=${this._nameFieldValue("remarks")}
        @input=${(e) => this._nameFieldChange("remarks", e.target.value)}
      ></textarea>
    `;
  }

  // _renderAtomizedEditFields is the collapsed section revealed by the
  // "show atomized fields" toggle on the edit form. Shape matches the
  // create pane's atomized fieldset (name grid + basionym/combination
  // pair) so curators see the same widget in both contexts.
  _renderAtomizedEditFields() {
    const v = (f) => this._nameFieldValue(f);
    const set = (f) => (e) => this._nameFieldChange(f, e.target.value);
    return html`
      <fieldset style="grid-column: 1 / -1">
        <legend>atomized name</legend>
        <label>Uninomial</label>
        <input type="text" .value=${v("uninomial")} @input=${set("uninomial")} />
        <label>Genus</label>
        <input type="text" .value=${v("genus")} @input=${set("genus")} />
        <label>Subgenus</label>
        <input type="text" .value=${v("infrageneric_epithet")} @input=${set("infrageneric_epithet")} />
        <label>Specific epithet</label>
        <input type="text" .value=${v("specific_epithet")} @input=${set("specific_epithet")} />
        <label>Infraspecific epithet</label>
        <input type="text" .value=${v("infraspecific_epithet")} @input=${set("infraspecific_epithet")} />
        <label>Cultivar epithet</label>
        <input type="text" .value=${v("cultivar_epithet")} @input=${set("cultivar_epithet")} />
      </fieldset>
      <div class="authorship-pair" style="grid-column: 1 / -1">
        <fieldset>
          <legend>basionym (original)</legend>
          <label>Author</label>
          <input type="text" .value=${v("basionym_authorship")} @input=${set("basionym_authorship")} />
          <label>Year</label>
          <input type="text" .value=${v("basionym_authorship_year")} @input=${set("basionym_authorship_year")} />
        </fieldset>
        <fieldset>
          <legend>combination (current)</legend>
          <label>Author</label>
          <input type="text" .value=${v("combination_authorship")} @input=${set("combination_authorship")} />
          <label>Year</label>
          <input type="text" .value=${v("combination_authorship_year")} @input=${set("combination_authorship_year")} />
        </fieldset>
      </div>
    `;
  }

  _renderSynonyms() {
    if (!this._synonyms.length) return "";
    return html`
      <section class="synonyms">
        <h3>Synonyms (${this._synonyms.length})</h3>
        <ul>
          ${this._synonyms.map(
            (s) => html`<li>
              ${renderLabel(s.label, s.name_id)}${s.remarks
                ? html` — <span class="empty">${s.remarks}</span>`
                : ""}
            </li>`,
          )}
        </ul>
      </section>
    `;
  }
}

// ---------- <sfga-add-reference-modal> ----------
// Four-tab modal for adding a reference from an outside source, then
// picking it into the name edit form. Tabs, in order:
//
//   0 Project — search references already in this archive (fuzzy on
//     author/title/citation). This is the "did I already add this?"
//     tab and the default landing pane; picking here just returns the
//     existing id + label, no write.
//   1 BHLnames — pre-runs a lookup against the current scientific
//     name (via context props) and shows scored hits. Selecting a hit
//     POSTs to /api/reference to persist an unsaved preview.
//   2 DOI — text input; on submit, resolves via OpenAlex and shows a
//     preview form; save-and-pick POSTs to /api/reference.
//   3 BibTeX — textarea; on submit, parses via core.ParseBibTeX and
//     shows a preview form; save-and-pick POSTs to /api/reference.
//
// Events:
//   reference-picked  { id, label }  — modal closes; caller updates form.
//   close                             — modal closes; no change.
class SfgaAddReferenceModal extends LitElement {
  static properties = {
    contextCanonical: { attribute: false },
    contextAuthors: { attribute: false },
    contextYear: { attribute: false },
    _tab: { state: true }, // 0..3
    _busy: { state: true },
    _error: { state: true },
    // Tab 0 (project)
    _projectQuery: { state: true },
    _projectHits: { state: true },
    // Tab 1 (BHLnames)
    _bhlHits: { state: true },
    _bhlLoaded: { state: true },
    // Tab 2 (DOI)
    _doiInput: { state: true },
    // Tab 3 (BibTeX)
    _bibtexInput: { state: true },
    // Preview (shared by tabs 1/2/3 once a candidate resolves).
    _preview: { state: true },
  };

  static styles = css`
    :host {
      display: block;
    }
    .backdrop {
      position: fixed;
      inset: 0;
      background: color-mix(in oklab, var(--bg) 60%, transparent);
      display: grid;
      place-items: center;
      z-index: 10;
    }
    .modal {
      background: var(--bg);
      border: 1px solid var(--border);
      padding: 1rem 1.25rem;
      width: min(48rem, 95vw);
      max-height: 90vh;
      overflow: auto;
      display: grid;
      gap: 0.5rem;
      font-family: var(--font-body);
    }
    h3 {
      margin: 0;
      font-size: 1.05em;
    }
    .tabs {
      display: flex;
      gap: 0.25rem;
      border-bottom: 1px solid var(--border);
    }
    .tabs button {
      background: transparent;
      color: var(--fg);
      border: 1px solid transparent;
      border-bottom: none;
      padding: 0.3rem 0.7rem;
      cursor: pointer;
      font: inherit;
    }
    .tabs button[aria-selected="true"] {
      border-color: var(--border);
      background: var(--bg);
      position: relative;
      top: 1px;
    }
    .pane {
      min-height: 12rem;
      display: grid;
      gap: 0.5rem;
    }
    input[type="text"],
    textarea {
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      padding: 0.3rem 0.4rem;
      font: inherit;
    }
    textarea {
      font-family: var(--font-mono);
      font-size: 0.9em;
      min-height: 8rem;
    }
    .hit {
      border: 1px solid var(--border);
      padding: 0.5rem;
      display: grid;
      gap: 0.2rem;
    }
    .hit .title {
      font-weight: 500;
    }
    .hit .meta {
      color: var(--dim);
      font-size: 0.85em;
    }
    .hit .actions {
      display: flex;
      gap: 0.3rem;
      justify-content: flex-end;
    }
    .quality {
      display: inline-block;
      padding: 0 0.3rem;
      border: 1px solid var(--border);
      border-radius: 3px;
      font-family: var(--font-mono);
      font-size: 0.75em;
      color: var(--dim);
    }
    .quality.q5 {
      color: var(--accent);
      border-color: var(--accent);
    }
    .quality.q4 {
      color: var(--fg);
      border-color: var(--fg);
    }
    .preview dl {
      margin: 0;
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.15rem 0.75rem;
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    .preview dt {
      color: var(--dim);
    }
    .preview dd {
      margin: 0;
      overflow-wrap: anywhere;
    }
    .toolbar {
      display: flex;
      justify-content: flex-end;
      gap: 0.3rem;
      margin-top: 0.5rem;
    }
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    .error {
      color: var(--error);
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    .empty {
      color: var(--dim);
      font-style: italic;
    }
  `;

  constructor() {
    super();
    this.contextCanonical = "";
    this.contextAuthors = "";
    this.contextYear = 0;
    this._tab = 0;
    this._busy = false;
    this._error = "";
    this._projectQuery = "";
    this._projectHits = [];
    this._bhlHits = [];
    this._bhlLoaded = false;
    this._doiInput = "";
    this._bibtexInput = "";
    this._preview = null;
  }

  updated(changed) {
    // Auto-fire BHLnames lookup the first time the user switches to
    // the BHLnames tab (or on open if it's the default). Cache the
    // result — the modal instance is short-lived so a single lookup
    // per open is fine.
    if (
      changed.has("_tab") &&
      this._tab === 1 &&
      !this._bhlLoaded &&
      this.contextCanonical
    ) {
      this._runBHLnames();
    }
  }

  _selectTab(t) {
    this._tab = t;
    this._error = "";
    // Preview is per-tab — leaving the tab clears any half-completed
    // resolve/parse work so switching back is a clean slate.
    this._preview = null;
  }

  _close() {
    this.dispatchEvent(new CustomEvent("close", { bubbles: true, composed: true }));
  }

  _pick(id, label) {
    this.dispatchEvent(
      new CustomEvent("reference-picked", {
        detail: { id, label },
        bubbles: true,
        composed: true,
      }),
    );
  }

  render() {
    return html`
      <div class="backdrop" @click=${() => this._close()}>
        <div class="modal" @click=${(e) => e.stopPropagation()}>
          <h3>Add reference</h3>
          <div class="tabs" role="tablist">
            <button role="tab" aria-selected=${this._tab === 0} @click=${() => this._selectTab(0)}>
              Project
            </button>
            <button role="tab" aria-selected=${this._tab === 1} @click=${() => this._selectTab(1)}>
              BHLnames
            </button>
            <button role="tab" aria-selected=${this._tab === 2} @click=${() => this._selectTab(2)}>
              DOI
            </button>
            <button role="tab" aria-selected=${this._tab === 3} @click=${() => this._selectTab(3)}>
              BibTeX
            </button>
          </div>
          <div class="pane">
            ${this._tab === 0 ? this._renderProjectPane() : ""}
            ${this._tab === 1 ? this._renderBHLnamesPane() : ""}
            ${this._tab === 2 ? this._renderDOIPane() : ""}
            ${this._tab === 3 ? this._renderBibTeXPane() : ""}
          </div>
          <div class="toolbar">
            <button @click=${() => this._close()}>close</button>
          </div>
        </div>
      </div>
    `;
  }

  // ---------- Tab 0: Project ----------
  _renderProjectPane() {
    return html`
      <p class="empty">
        Search references already in this archive. Picking one just
        links the name to it — nothing new is written.
      </p>
      <input
        type="text"
        placeholder="author / title / citation…"
        .value=${this._projectQuery}
        @input=${(e) => this._onProjectInput(e.target.value)}
        autofocus
      />
      ${this._busy ? html`<div class="empty">searching…</div>` : ""}
      ${this._projectHits.length === 0 && this._projectQuery.length >= 2 && !this._busy
        ? html`<div class="empty">no matches</div>`
        : ""}
      ${this._projectHits.map(
        (h) => html`
          <div class="hit">
            <div class="title">${h.title || h.citation || h.id}</div>
            <div class="meta">
              ${h.author || ""}${h.year ? ` (${h.year})` : ""}
              ${h.type ? html` · <span>${h.type}</span>` : ""}
            </div>
            <div class="actions">
              <button
                class="primary"
                @click=${() => this._pick(h.id, referenceHitLabel(h))}
              >
                pick
              </button>
            </div>
          </div>
        `,
      )}
    `;
  }

  async _onProjectInput(q) {
    this._projectQuery = q;
    if (q.length < 2) {
      this._projectHits = [];
      return;
    }
    // Trailing-edge debounce so a burst of typing doesn't fire 8
    // requests. 200 ms matches the combobox debounce.
    clearTimeout(this._projectDebounce);
    this._projectDebounce = setTimeout(async () => {
      this._busy = true;
      try {
        const page = await api.reference.search({ q, limit: 15 });
        this._projectHits = page.items || [];
      } catch (err) {
        this._error = err.message || String(err);
      } finally {
        this._busy = false;
      }
    }, 200);
  }

  // ---------- Tab 1: BHLnames ----------
  _renderBHLnamesPane() {
    if (!this.contextCanonical) {
      return html`
        <p class="empty">
          BHLnames needs a scientific name — this taxon doesn't have one yet.
        </p>
      `;
    }
    return html`
      <p class="empty">
        Looking up
        <em>${this.contextCanonical}</em>${this.contextAuthors ? ` ${this.contextAuthors}` : ""}${this.contextYear ? ` (${this.contextYear})` : ""}
        in the Biodiversity Heritage Library index.
      </p>
      ${this._busy ? html`<div class="empty">searching BHLnames…</div>` : ""}
      ${this._error ? html`<div class="error">${this._error}</div>` : ""}
      ${this._bhlLoaded && this._bhlHits.length === 0 && !this._busy
        ? html`<div class="empty">no BHLnames matches</div>`
        : ""}
      ${this._bhlHits.map(
        (h) => html`
          <div class="hit">
            <div class="title">${h.reference.title || h.reference.citation || "(no title)"}</div>
            <div class="meta">
              ${h.reference.author || ""}
              ${h.reference.issued ? ` (${h.reference.issued.slice(0, 4)})` : ""}
              ${h.reference.container_title ? ` · ${h.reference.container_title}` : ""}
              ${h.quality
                ? html` · <span class="quality q${h.quality}">quality ${h.quality}/5</span>`
                : ""}
            </div>
            ${h.page_url
              ? html`<div class="meta">
                  <a href=${h.page_url} target="_blank" rel="noopener">view page</a>
                </div>`
              : ""}
            <div class="actions">
              <button @click=${() => (this._preview = h.reference)}>preview</button>
              <button class="primary" @click=${() => this._createAndPick(h.reference)}>
                add & pick
              </button>
            </div>
          </div>
        `,
      )}
      ${this._preview ? this._renderPreview() : ""}
    `;
  }

  async _runBHLnames() {
    this._busy = true;
    this._error = "";
    this._bhlLoaded = true;
    try {
      const page = await api.reference.lookupBHLnames({
        canonical: this.contextCanonical,
        authors: this.contextAuthors,
        year: this.contextYear,
        nomen_event: true,
        limit: 5,
      });
      this._bhlHits = page.items || [];
    } catch (err) {
      this._error = err.detail || err.message || String(err);
    } finally {
      this._busy = false;
    }
  }

  // ---------- Tab 2: DOI ----------
  _renderDOIPane() {
    return html`
      <p class="empty">
        Paste a DOI (bare or as a doi.org URL). Hive resolves it via OpenAlex
        and shows a preview before writing.
      </p>
      <input
        type="text"
        placeholder="10.1038/171737a0"
        .value=${this._doiInput}
        @input=${(e) => (this._doiInput = e.target.value)}
        @keydown=${(e) => e.key === "Enter" && this._resolveDOI()}
        autofocus
      />
      <div class="toolbar" style="justify-content: flex-start">
        <button @click=${() => this._resolveDOI()} ?disabled=${!this._doiInput.trim() || this._busy}>
          ${this._busy ? "resolving…" : "resolve"}
        </button>
      </div>
      ${this._error ? html`<div class="error">${this._error}</div>` : ""}
      ${this._preview ? this._renderPreview() : ""}
    `;
  }

  async _resolveDOI() {
    const doi = this._doiInput.trim();
    if (!doi) return;
    this._busy = true;
    this._error = "";
    this._preview = null;
    try {
      const ref = await api.reference.resolveDOI(doi);
      this._preview = ref;
    } catch (err) {
      this._error = err.detail || err.message || String(err);
    } finally {
      this._busy = false;
    }
  }

  // ---------- Tab 3: BibTeX ----------
  _renderBibTeXPane() {
    return html`
      <p class="empty">
        Paste a BibTeX entry. Multi-line is fine; the parser handles nested
        braces and the standard field aliases.
      </p>
      <textarea
        placeholder="@article{smith2020, title={…}, author={…}, journal={…}, year={2020}}"
        .value=${this._bibtexInput}
        @input=${(e) => (this._bibtexInput = e.target.value)}
        autofocus
      ></textarea>
      <div class="toolbar" style="justify-content: flex-start">
        <button @click=${() => this._parseBibTeX()} ?disabled=${!this._bibtexInput.trim() || this._busy}>
          ${this._busy ? "parsing…" : "parse"}
        </button>
      </div>
      ${this._error ? html`<div class="error">${this._error}</div>` : ""}
      ${this._preview ? this._renderPreview() : ""}
    `;
  }

  async _parseBibTeX() {
    const text = this._bibtexInput.trim();
    if (!text) return;
    this._busy = true;
    this._error = "";
    this._preview = null;
    try {
      const ref = await api.reference.parseBibTeX(text);
      this._preview = ref;
    } catch (err) {
      this._error = err.detail || err.message || String(err);
    } finally {
      this._busy = false;
    }
  }

  // ---------- Shared preview panel ----------
  _renderPreview() {
    const r = this._preview;
    const rows = [
      ["Author", r.author],
      ["Year", r.issued ? r.issued.slice(0, 4) : ""],
      ["Title", r.title],
      ["Container", r.container_title],
      ["Volume/Issue", r.volume ? `${r.volume}${r.issue ? "(" + r.issue + ")" : ""}` : ""],
      ["Page", r.page],
      ["DOI", r.doi],
      ["Type", r.type],
    ].filter(([, v]) => v);
    return html`
      <div class="preview" style="border-top: 1px solid var(--border); padding-top: 0.5rem">
        <dl>
          ${rows.map(([k, v]) => html`<dt>${k}</dt><dd>${v}</dd>`)}
        </dl>
        <div class="toolbar">
          <button @click=${() => (this._preview = null)}>discard</button>
          <button class="primary" @click=${() => this._createAndPick(r)}>
            ${this._busy ? "saving…" : "add & pick"}
          </button>
        </div>
      </div>
    `;
  }

  async _createAndPick(ref) {
    this._busy = true;
    this._error = "";
    try {
      // Strip id so core.CreateReference mints a fresh UUID.
      const body = { ...ref, id: "" };
      const saved = await api.reference.create(body);
      this._pick(saved.id, referenceHitLabel(saved));
    } catch (err) {
      this._error = err.detail || err.message || String(err);
    } finally {
      this._busy = false;
    }
  }
}

// ---------- <sfga-combobox> ----------
// Unified picker used for both vocab-driven fields (rank, code, status —
// backed by the cached api.vocab bundle) AND server-search fields
// (parent — backed by /api/taxon/search). The data source is pluggable:
// callers pass a `source(query) → results[]` function that returns
// {id, name, extra?} items. A companion `resolver(id) → name` lets the
// component fill in the display label when only an id was provided.
//
// UX: always-editable text input with an overlaid × on the right when
// non-empty. Type to filter; Arrow keys / Enter / Esc navigate; blur
// without picking reverts to the last committed value. minSearchChars=0
// makes it behave like a native select (focus with empty input shows
// all options); minSearchChars=2 (parent picker) requires the user to
// type before results appear.
//
// Retired: the old SfgaVocabSelect (native <select>) and SfgaTaxonPicker
// (display+change/clear buttons). Same wire contract — callers still bind
// `.value` and listen for `pick` events.

class SfgaCombobox extends LitElement {
  static properties = {
    value: { type: String },
    valueName: { type: String, attribute: "value-name" },
    placeholder: { type: String },
    minSearchChars: { type: Number, attribute: "min-search-chars" },
    source: { attribute: false }, // async (q) => [{id, name, ...extras}]
    resolver: { attribute: false }, // async (id) => name (optional)
    _input: { state: true },
    _results: { state: true },
    _open: { state: true },
    _loading: { state: true },
    _hover: { state: true },
  };

  static styles = css`
    :host {
      display: block;
      position: relative;
      width: 100%;
    }
    .wrap {
      position: relative;
      width: 100%;
    }
    input {
      color: var(--fg);
      background: var(--bg);
      border: 1px solid var(--border);
      /* room on the right for the × button, plus a bit extra so text
         doesn't butt up against it. */
      padding: 0.3rem 1.8rem 0.3rem 0.4rem;
      font-family: inherit;
      font-size: 1em;
      width: 100%;
      box-sizing: border-box;
    }
    input::placeholder {
      color: var(--dim);
      font-style: italic;
    }
    input:focus {
      outline: none;
      border-color: var(--accent);
    }
    button.clear {
      position: absolute;
      right: 0.35rem;
      top: 50%;
      transform: translateY(-50%);
      background: transparent;
      color: var(--dim);
      border: 0;
      padding: 0 0.35rem;
      font-family: inherit;
      font-size: 1.2em;
      line-height: 1;
      cursor: pointer;
    }
    button.clear:hover {
      color: var(--fg);
    }
    .results {
      position: absolute;
      left: 0;
      right: 0;
      top: 100%;
      z-index: 20;
      max-height: 14rem;
      overflow: auto;
      background: var(--bg);
      border: 1px solid var(--border);
      border-top: none;
      margin: 0;
      padding: 0;
      list-style: none;
    }
    .results li {
      padding: 0.25rem 0.5rem;
      cursor: pointer;
      font-family: var(--font-mono);
      font-size: 0.9em;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .results li.hover {
      background: var(--accent);
      color: var(--accent-fg);
    }
    .results .empty {
      color: var(--dim);
      font-style: italic;
      cursor: default;
    }
    .results li.empty:hover {
      background: var(--bg);
      color: var(--dim);
    }
  `;

  constructor() {
    super();
    this.value = "";
    this.valueName = "";
    this.placeholder = "type to search…";
    this.minSearchChars = 0;
    this.source = async () => [];
    this.resolver = null;
    this._input = "";
    this._results = [];
    this._open = false;
    this._loading = false;
    this._hover = -1;
    this._debounceHandle = 0;
    this._lastQuery = null;
    this._focused = false;
    this._resolvingFor = ""; // id we're currently resolving to avoid duplicate work
  }

  async updated(changed) {
    // Sync internal input with valueName when the parent supplies it.
    if (changed.has("valueName") && !this._focused) {
      this._input = this.valueName || "";
    }
    // If value is set but no valueName, ask the resolver to fill it in.
    if (
      changed.has("value") &&
      this.value &&
      !this.valueName &&
      this.resolver &&
      this._resolvingFor !== this.value
    ) {
      this._resolvingFor = this.value;
      try {
        const name = await this.resolver(this.value);
        // Race guard: value may have changed while we were fetching.
        if (this._resolvingFor === this.value) {
          this.valueName = name || "";
          if (!this._focused) this._input = this.valueName;
        }
      } catch (_) {
        if (this._resolvingFor === this.value) {
          this.valueName = "(lookup failed)";
          if (!this._focused) this._input = this.valueName;
        }
      }
    }
  }

  _onFocus() {
    this._focused = true;
    // Empty input + minSearchChars=0 → show all options (select-like UX
    // for small vocabs). Otherwise wait for the user to type.
    if (this._input.length === 0 && this.minSearchChars === 0) {
      this._runSearch();
      this._open = true;
    }
  }

  _onBlur() {
    // Delay so click-on-result / click-on-× fires before we drop focus
    // and revert. Also handled by @mousedown+preventDefault on those
    // elements as a belt-and-braces measure.
    setTimeout(() => {
      this._focused = false;
      this._open = false;
      // If the user typed something and didn't pick, revert to the last
      // committed value's name so the input never shows a "phantom" state.
      if (this._input !== (this.valueName || "")) {
        this._input = this.valueName || "";
      }
    }, 150);
  }

  _onInput(e) {
    this._input = e.target.value;
    clearTimeout(this._debounceHandle);
    if (this._input.length < this.minSearchChars) {
      this._results = [];
      this._loading = false;
      this._open = this._input.length === 0 && this.minSearchChars === 0;
      if (this._open) this._runSearch();
      return;
    }
    this._loading = true;
    this._open = true;
    // Vocab-driven searches are synchronous — no benefit to debouncing,
    // and 300ms of latency feels sluggish. Server-search sources get the
    // debounce. Detect by minSearchChars >= 1 as a proxy — a taxon
    // picker needs 2 chars, a vocab picker needs 0.
    const debounce = this.minSearchChars >= 2 ? 300 : 0;
    if (debounce === 0) {
      this._runSearch();
    } else {
      this._debounceHandle = setTimeout(() => this._runSearch(), debounce);
    }
  }

  async _runSearch() {
    const q = this._input;
    this._lastQuery = q;
    try {
      const results = await Promise.resolve(this.source(q));
      if (q === this._lastQuery) {
        this._results = results || [];
        this._hover = this._results.length ? 0 : -1;
        this._loading = false;
      }
    } catch (_) {
      if (q === this._lastQuery) {
        this._results = [];
        this._loading = false;
      }
    }
  }

  _onKeyDown(e) {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (!this._open) {
          this._open = true;
          if (!this._results.length) this._runSearch();
        } else if (this._results.length) {
          this._hover = (this._hover + 1) % this._results.length;
        }
        break;
      case "ArrowUp":
        e.preventDefault();
        if (this._results.length) {
          this._hover = (this._hover - 1 + this._results.length) % this._results.length;
        }
        break;
      case "Enter":
        e.preventDefault();
        if (this._hover >= 0 && this._hover < this._results.length) {
          this._pick(this._results[this._hover]);
        }
        break;
      case "Escape":
        e.preventDefault();
        this._input = this.valueName || "";
        this._open = false;
        break;
    }
  }

  _pick(item) {
    this.value = item.id;
    this.valueName = item.name;
    this._input = item.name;
    this._open = false;
    this._results = [];
    this.dispatchEvent(
      new CustomEvent("pick", {
        detail: { id: item.id, name: item.name, item },
        bubbles: true,
        composed: true,
      }),
    );
  }

  _clear(e) {
    // Called from a mousedown handler; preventDefault keeps focus on the
    // input so the user can immediately type without a second click.
    if (e) e.preventDefault();
    this.value = "";
    this.valueName = "";
    this._input = "";
    this._results = [];
    this.dispatchEvent(
      new CustomEvent("pick", {
        detail: { id: "", name: "", item: null },
        bubbles: true,
        composed: true,
      }),
    );
    // Focus and re-open the dropdown (empty-query behavior handled by
    // minSearchChars logic in _onFocus).
    this.updateComplete.then(() => {
      const el = this.renderRoot.querySelector("input");
      if (el) {
        el.focus();
        // Re-trigger the focus logic explicitly since focus() on an
        // already-focused element doesn't fire the handler.
        this._onFocus();
      }
    });
  }

  render() {
    let dropdown;
    if (this._loading) {
      dropdown = html`<li class="empty">searching…</li>`;
    } else if (
      !this._results.length &&
      this._input.length >= this.minSearchChars
    ) {
      dropdown = html`<li class="empty">no matches</li>`;
    } else {
      dropdown = this._results.map(
        (r, i) => html`
          <li
            class=${i === this._hover ? "hover" : ""}
            @mousedown=${(e) => {
              e.preventDefault();
              this._pick(r);
            }}
            @mouseenter=${() => (this._hover = i)}
          >
            ${r.name || "(unset)"}
          </li>
        `,
      );
    }

    return html`
      <div class="wrap">
        <input
          type="text"
          placeholder=${this.placeholder}
          .value=${this._input}
          @input=${(e) => this._onInput(e)}
          @focus=${() => this._onFocus()}
          @blur=${() => this._onBlur()}
          @keydown=${(e) => this._onKeyDown(e)}
        />
        ${this._input
          ? html`<button
              class="clear"
              tabindex="-1"
              title="clear"
              @mousedown=${(e) => this._clear(e)}
            >
              ×
            </button>`
          : ""}
        ${this._open ? html`<ul class="results">${dropdown}</ul>` : ""}
      </div>
    `;
  }
}


// ---------- <sfga-metadata> ----------
// Dataset-metadata screen — dl-style read view + form-based edit
// mode. Fetches its own copy on connect so the component can render
// independently; dispatches `metadata-changed` after a save so the
// shell can refresh the header title.

class SfgaMetadata extends LitElement {
  static properties = {
    editable: { type: Boolean, attribute: false },
    _metadata: { state: true },
    _loading: { state: true },
    _error: { state: true },
    _editing: { state: true },
    _draft: { state: true },
    _saving: { state: true },
    _saveError: { state: true },
  };

  static styles = css`
    :host {
      display: block;
      font-family: var(--font-body);
      color: var(--fg);
      padding: 1rem 1.25rem;
    }
    h2 {
      margin: 0 0 0.25rem 0;
      font-size: 1.25em;
    }
    hr {
      border: 0;
      border-top: 1px solid var(--border);
      margin: 0.5rem 0 1rem 0;
    }
    dl {
      margin: 0;
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.25rem 1rem;
      font-family: var(--font-mono);
      font-size: 0.95em;
      max-width: 60rem;
    }
    dt {
      color: var(--dim);
    }
    dd {
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
    }
    form {
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.5rem 1rem;
      max-width: 60rem;
      margin-top: 0.5rem;
    }
    form label {
      color: var(--dim);
      align-self: center;
      font-family: var(--font-mono);
      font-size: 0.95em;
    }
    form input,
    form textarea,
    form select {
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      padding: 0.25rem 0.4rem;
      font-family: inherit;
      font-size: 0.95em;
    }
    form textarea {
      min-height: 4rem;
      resize: vertical;
      font-family: var(--font-body);
    }
    form input:focus,
    form textarea:focus,
    form select:focus {
      outline: none;
      border-color: var(--accent);
    }
    .req {
      color: var(--error);
    }
    .toolbar {
      grid-column: 1 / -1;
      display: flex;
      gap: 0.5rem;
      margin-top: 0.5rem;
    }
    button {
      background: transparent;
      color: var(--fg);
      border: 1px solid var(--border);
      padding: 0.2rem 0.6rem;
      font-family: inherit;
      cursor: pointer;
    }
    button:hover {
      border-color: var(--accent);
    }
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    .error {
      color: var(--error);
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    .empty {
      color: var(--dim);
    }
    .stars {
      display: inline-flex;
      align-items: baseline;
      gap: 0.4rem;
      font-family: var(--font-mono);
    }
    .star-filled {
      color: gold;
    }
    .star-empty {
      color: var(--dim);
    }
    .star-count {
      color: var(--dim);
      font-size: 0.85em;
    }
  `;

  constructor() {
    super();
    this.editable = false;
    this._metadata = null;
    this._loading = false;
    this._error = "";
    this._editing = false;
    this._draft = {};
    this._saving = false;
    this._saveError = "";
  }

  async connectedCallback() {
    super.connectedCallback();
    this._loading = true;
    try {
      this._metadata = await api.metadata.get();
    } catch (err) {
      // Legacy archive without a seeded row — treat as "empty".
      if (err instanceof Problem && err.status === 404) {
        this._metadata = null;
      } else {
        this._error = err instanceof Problem ? err.detail || err.title : String(err);
      }
    } finally {
      this._loading = false;
    }
  }

  _startEdit() {
    this._draft = {};
    this._saveError = "";
    this._editing = true;
  }

  _cancelEdit() {
    this._draft = {};
    this._saveError = "";
    this._editing = false;
  }

  _change(field, value) {
    this._draft = { ...this._draft, [field]: value };
  }

  _fieldValue(field) {
    if (Object.hasOwn(this._draft, field)) return this._draft[field];
    return this._metadata?.[field] ?? "";
  }

  async _save() {
    if (Object.keys(this._draft).length === 0) {
      this._editing = false;
      return;
    }
    // Coerce numerics from string inputs. Empty stays null (unset).
    const patch = { ...this._draft };
    for (const k of ["confidence", "completeness"]) {
      if (Object.hasOwn(patch, k)) {
        const v = (patch[k] ?? "").toString().trim();
        if (v === "") {
          patch[k] = null;
        } else {
          const n = parseInt(v, 10);
          if (Number.isNaN(n)) {
            this._saveError = `${k} must be a whole number`;
            return;
          }
          patch[k] = n;
        }
      }
    }
    if (Object.hasOwn(patch, "private")) {
      const v = patch.private;
      if (v === "" || v === null) patch.private = null;
      else patch.private = v === "true" || v === true;
    }
    this._saving = true;
    this._saveError = "";
    try {
      const updated = await api.metadata.patch(patch);
      this._metadata = updated;
      this._draft = {};
      this._editing = false;
      // Notify the shell so the header title stays in sync.
      this.dispatchEvent(
        new CustomEvent("metadata-changed", {
          detail: { metadata: updated },
          bubbles: true,
          composed: true,
        }),
      );
    } catch (err) {
      this._saveError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    } finally {
      this._saving = false;
    }
  }

  render() {
    if (this._loading && !this._metadata) {
      return html`<div class="empty">loading…</div>`;
    }
    if (this._error) {
      return html`<div class="error">${this._error}</div>`;
    }
    if (!this._metadata) {
      // Legacy archive without seeded metadata — offer to seed via
      // edit mode. PATCH with a Title creates the row.
      if (!this._editing) {
        return html`
          <h2>Metadata</h2>
          <hr />
          <p class="empty">
            This archive doesn't have metadata yet.
            ${this.editable
              ? html`<button @click=${() => this._startEdit()}>seed metadata</button>`
              : ""}
          </p>
        `;
      }
      // Fall through to edit form for the seed case.
    }
    if (this._editing) return this._renderEdit();
    return this._renderRead();
  }

  _renderRead() {
    const m = this._metadata;
    const row = (label, value) =>
      value === undefined || value === null || value === ""
        ? ""
        : html`<dt>${label}</dt>
            <dd>${value}</dd>`;
    return html`
      <h2>${m.title || "(untitled)"}</h2>
      <hr />
      <dl>
        ${row("Alias", m.alias)} ${row("Version", m.version)}
        ${row("Issued", m.issued)} ${row("DOI", m.doi)} ${row("URL", m.url)}
        ${row("Logo", m.logo)} ${row("Label", m.label)}
        ${row("Description", m.description)} ${row("Keywords", m.keywords)}
        ${row("Citation", m.citation)}
        ${row("Geographic scope", m.geographic_scope)}
        ${row("Taxonomic scope", m.taxonomic_scope)}
        ${row("Temporal scope", m.temporal_scope)}
        ${m.confidence !== undefined && m.confidence !== null
          ? html`<dt>Confidence</dt>
              <dd>${renderStars(m.confidence, 5)}</dd>`
          : ""}
        ${m.completeness !== undefined && m.completeness !== null
          ? row("Completeness", m.completeness + "%")
          : ""}
        ${row("License", m.license)}
        ${m.private !== undefined && m.private !== null
          ? row("Access", m.private ? "private" : "public")
          : ""}
      </dl>
      ${this.editable
        ? html`<div class="toolbar" style="margin-top:1rem">
            <button @click=${() => this._startEdit()}>edit</button>
          </div>`
        : ""}
    `;
  }

  _renderEdit() {
    // Private is a tri-state; represented as "" / "true" / "false" so
    // the empty-string value carries "leave unset".
    const priv = Object.hasOwn(this._draft, "private")
      ? this._draft.private === null
        ? ""
        : this._draft.private === true
          ? "true"
          : "false"
      : this._metadata?.private === undefined || this._metadata?.private === null
        ? ""
        : this._metadata.private
          ? "true"
          : "false";
    return html`
      <h2>Edit metadata</h2>
      <hr />
      <form @submit=${(e) => e.preventDefault()}>
        <label>Title <span class="req">*</span></label>
        <input
          type="text"
          .value=${this._fieldValue("title")}
          @input=${(e) => this._change("title", e.target.value)}
          required
        />
        <label>Alias</label>
        <input
          type="text"
          .value=${this._fieldValue("alias")}
          @input=${(e) => this._change("alias", e.target.value)}
        />
        <label>Version</label>
        <input
          type="text"
          .value=${this._fieldValue("version")}
          @input=${(e) => this._change("version", e.target.value)}
        />
        <label>Issued</label>
        <input
          type="text"
          placeholder="YYYY-MM-DD"
          .value=${this._fieldValue("issued")}
          @input=${(e) => this._change("issued", e.target.value)}
        />
        <label>DOI</label>
        <input
          type="text"
          .value=${this._fieldValue("doi")}
          @input=${(e) => this._change("doi", e.target.value)}
        />
        <label>URL</label>
        <input
          type="url"
          .value=${this._fieldValue("url")}
          @input=${(e) => this._change("url", e.target.value)}
        />
        <label>Logo</label>
        <input
          type="url"
          .value=${this._fieldValue("logo")}
          @input=${(e) => this._change("logo", e.target.value)}
        />
        <label>Label</label>
        <input
          type="text"
          .value=${this._fieldValue("label")}
          @input=${(e) => this._change("label", e.target.value)}
        />
        <label>Description</label>
        <textarea
          .value=${this._fieldValue("description")}
          @input=${(e) => this._change("description", e.target.value)}
        ></textarea>
        <label>Keywords</label>
        <input
          type="text"
          .value=${this._fieldValue("keywords")}
          @input=${(e) => this._change("keywords", e.target.value)}
        />
        <label>Citation</label>
        <textarea
          .value=${this._fieldValue("citation")}
          @input=${(e) => this._change("citation", e.target.value)}
        ></textarea>
        <label>Geographic scope</label>
        <input
          type="text"
          .value=${this._fieldValue("geographic_scope")}
          @input=${(e) => this._change("geographic_scope", e.target.value)}
        />
        <label>Taxonomic scope</label>
        <input
          type="text"
          .value=${this._fieldValue("taxonomic_scope")}
          @input=${(e) => this._change("taxonomic_scope", e.target.value)}
        />
        <label>Temporal scope</label>
        <input
          type="text"
          .value=${this._fieldValue("temporal_scope")}
          @input=${(e) => this._change("temporal_scope", e.target.value)}
        />
        <label>Confidence (1–5)</label>
        <input
          type="number"
          min="1"
          max="5"
          .value=${this._fieldValue("confidence")}
          @input=${(e) => this._change("confidence", e.target.value)}
        />
        <label>Completeness (%)</label>
        <input
          type="number"
          min="0"
          max="100"
          .value=${this._fieldValue("completeness")}
          @input=${(e) => this._change("completeness", e.target.value)}
        />
        <label>License</label>
        <input
          type="text"
          list="hive-license-suggestions"
          placeholder="e.g. CC-BY-4.0"
          .value=${this._fieldValue("license")}
          @input=${(e) => this._change("license", e.target.value)}
        />
        <datalist id="hive-license-suggestions">
          ${api.vocab.get("licenses").map(
            (l) => html`<option value=${l.id}>${l.name || l.id}</option>`,
          )}
        </datalist>
        <label>Access</label>
        <select
          .value=${priv}
          @change=${(e) => this._change("private", e.target.value === "" ? null : e.target.value === "true")}
        >
          <option value="">(unset)</option>
          <option value="false">public</option>
          <option value="true">private</option>
        </select>
        <div class="toolbar">
          <button class="primary" @click=${() => this._save()} ?disabled=${this._saving}>
            save
          </button>
          <button @click=${() => this._cancelEdit()} ?disabled=${this._saving}>
            cancel
          </button>
          ${this._saveError
            ? html`<span class="error">${this._saveError}</span>`
            : ""}
        </div>
      </form>
    `;
  }
}

// ---------- <sfga-references> ----------
// References screen — flat paginated list on the left, full reference
// detail on the right. Read-only in this slice; add-by-DOI / add-by-
// BibTeX and edit come later. Fetches its own data on connect.

class SfgaReferences extends LitElement {
  static properties = {
    _hits: { state: true },
    _selectedId: { state: true },
    _current: { state: true },
    _loading: { state: true },
    _error: { state: true },
  };

  static styles = css`
    :host {
      display: grid;
      grid-template-columns: minmax(18rem, 40%) 1fr;
      overflow: hidden;
      height: 100%;
    }
    aside,
    section {
      overflow: auto;
      padding: 0.5rem;
    }
    aside {
      border-right: 1px solid var(--border);
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    ul.list {
      list-style: none;
      margin: 0;
      padding: 0;
    }
    ul.list li {
      padding: 0.35rem 0.5rem;
      cursor: pointer;
      color: var(--fg);
      border-bottom: 1px solid color-mix(in oklab, var(--border) 60%, transparent);
    }
    ul.list li:hover {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    ul.list li.selected {
      background: var(--accent);
      color: var(--accent-fg);
    }
    .author { font-weight: 600; }
    .year { color: var(--dim); margin-left: 0.4rem; }
    .title { display: block; color: var(--dim); font-weight: normal; margin-top: 0.15rem; }
    ul.list li.selected .year,
    ul.list li.selected .title { color: var(--accent-fg); }
    section h2 {
      margin: 0 0 0.25rem 0;
      font-size: 1.1em;
    }
    section hr {
      border: 0;
      border-top: 1px solid var(--border);
      margin: 0.5rem 0;
    }
    section dl {
      margin: 0;
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 0.25rem 1rem;
      font-family: var(--font-mono);
      font-size: 0.9em;
    }
    section dt { color: var(--dim); }
    section dd { margin: 0; word-break: break-word; }
    .citation {
      margin-top: 1rem;
      color: var(--dim);
      font-family: var(--font-body);
      line-height: 1.4;
    }
    .empty { color: var(--dim); padding: 0.5rem; }
    .error { color: var(--error); font-family: var(--font-mono); padding: 0.5rem; }
  `;

  constructor() {
    super();
    this._hits = [];
    this._selectedId = "";
    this._current = null;
    this._loading = false;
    this._error = "";
  }

  async connectedCallback() {
    super.connectedCallback();
    this._loading = true;
    try {
      const page = await api.reference.list({ limit: 200 });
      this._hits = page.items || [];
      if (this._hits.length > 0) {
        this._select(this._hits[0].id);
      }
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      this._loading = false;
    }
  }

  async _select(id) {
    if (!id || id === this._selectedId) return;
    this._selectedId = id;
    this._current = null;
    try {
      this._current = await api.reference.get(id);
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    }
  }

  render() {
    if (this._loading && this._hits.length === 0) {
      return html`<div class="empty">loading…</div>`;
    }
    if (this._error) {
      return html`<div class="error">${this._error}</div>`;
    }
    if (this._hits.length === 0) {
      return html`<div class="empty">(no references)</div>`;
    }
    return html`
      <aside>
        <ul class="list">
          ${this._hits.map(
            (h) => html`
              <li
                class=${h.id === this._selectedId ? "selected" : ""}
                @click=${() => this._select(h.id)}
              >
                <span class="author">${h.author || "(no author)"}</span
                ><span class="year">${h.year || ""}</span>
                <span class="title">${h.title || h.citation || ""}</span>
              </li>
            `,
          )}
        </ul>
      </aside>
      <section>${this._renderDetail()}</section>
    `;
  }

  _renderDetail() {
    if (!this._current) return html`<div class="empty">loading…</div>`;
    const r = this._current;
    const row = (label, value) =>
      value === undefined || value === null || value === ""
        ? ""
        : html`<dt>${label}</dt>
            <dd>${value}</dd>`;
    const linkRow = (label, value) =>
      !value
        ? ""
        : html`<dt>${label}</dt>
            <dd><a href=${value} target="_blank" rel="noopener">${value}</a></dd>`;
    return html`
      <h2>${r.title || r.citation || r.id}</h2>
      <hr />
      <dl>
        ${row("Author", r.author)} ${row("Issued", r.issued)}
        ${row("Type", r.type)} ${row("Container", r.container_title)}
        ${row("Volume", r.volume)} ${row("Issue", r.issue)}
        ${row("Page", r.page)} ${row("Publisher", r.publisher)}
        ${row("Place", r.publisher_place)}
        ${r.doi
          ? html`<dt>DOI</dt>
              <dd><a href="https://doi.org/${r.doi}" target="_blank" rel="noopener">${r.doi}</a></dd>`
          : ""}
        ${row("ISBN", r.isbn)} ${row("ISSN", r.issn)}
        ${linkRow("Link", r.link)} ${row("Editor", r.editor)}
        ${row("Remarks", r.remarks)}
      </dl>
      ${r.citation && r.citation !== r.title
        ? html`<div class="citation">${r.citation}</div>`
        : ""}
    `;
  }
}

// ---------- <sfga-help-modal> ----------
// Keyboard shortcut reference. Rendered when the shell's helpOpen
// state is true; fires a "close" CustomEvent when the user dismisses.
// The shell owns open/close state so the ? global shortcut and the
// header help button both flow through the same path.
//
// Content is rendered from api.keymap.forScope(scope) for each scope
// in scopeOrder — data-driven so an edit to pkg/ui/keymap.go
// automatically shows here without touching this component.
class SfgaHelpModal extends LitElement {
  // scopeOrder controls rendered section order and labels. Global
  // first (view-switch + orient-yourself bindings), then per-pane.
  static scopeOrder = [
    { scope: "global", label: "Global" },
    { scope: "tree", label: "Taxon tree" },
    { scope: "detail", label: "Detail pane" },
    { scope: "form", label: "Edit forms" },
  ];

  static styles = css`
    :host {
      display: block;
    }
    .backdrop {
      position: fixed;
      inset: 0;
      background: color-mix(in oklab, var(--bg) 60%, transparent);
      display: grid;
      place-items: center;
      z-index: 10;
    }
    .modal {
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--accent);
      padding: 1rem 1.5rem;
      width: min(38rem, 95vw);
      max-height: 90vh;
      overflow: auto;
      display: grid;
      gap: 0.75rem;
      font-family: var(--font-body);
    }
    header {
      display: flex;
      justify-content: space-between;
      align-items: center;
    }
    header h3 {
      margin: 0;
      font-size: 1.1em;
      color: var(--accent);
    }
    button.close {
      background: transparent;
      color: var(--dim);
      border: 0;
      padding: 0 0.35rem;
      font: inherit;
      font-size: 1.4em;
      line-height: 1;
      cursor: pointer;
    }
    button.close:hover {
      color: var(--fg);
    }
    .hint {
      color: var(--dim);
      font-style: italic;
      font-size: 0.9em;
    }
    section {
      display: grid;
      gap: 0.25rem;
    }
    section h4 {
      margin: 0 0 0.15rem 0;
      font-size: 0.95em;
      border-bottom: 1px solid var(--border);
      padding-bottom: 0.15rem;
    }
    dl {
      margin: 0;
      display: grid;
      grid-template-columns: minmax(6rem, auto) 1fr;
      column-gap: 1rem;
      row-gap: 0.15rem;
      align-items: baseline;
    }
    dt {
      font-family: var(--font-mono);
      color: var(--accent);
      white-space: nowrap;
    }
    dd {
      margin: 0;
    }
  `;

  render() {
    return html`
      <div
        class="backdrop"
        @click=${(e) => {
          // Click on the backdrop (not the modal) closes.
          if (e.target === e.currentTarget) this._close();
        }}
      >
        <div class="modal" role="dialog" aria-label="Keyboard shortcuts">
          <header>
            <h3>Keyboard shortcuts</h3>
            <button class="close" @click=${() => this._close()} title="close">
              ×
            </button>
          </header>
          ${SfgaHelpModal.scopeOrder.map((so) => this._renderSection(so))}
          <div class="hint">
            Esc or click outside to close.
          </div>
        </div>
      </div>
    `;
  }

  _renderSection({ scope, label }) {
    const entries = api.keymap.forScope(scope);
    if (entries.length === 0) return "";
    return html`
      <section>
        <h4>${label}</h4>
        <dl>
          ${entries.map(
            (s) => html`<dt>${s.display}</dt>
              <dd>${s.description}</dd>`,
          )}
        </dl>
      </section>
    `;
  }

  _close() {
    this.dispatchEvent(
      new CustomEvent("close", { bubbles: true, composed: true }),
    );
  }
}

customElements.define("sfga-app", SfgaApp);
customElements.define("sfga-tree", SfgaTree);
customElements.define("sfga-detail", SfgaDetail);
customElements.define("sfga-metadata", SfgaMetadata);
customElements.define("sfga-references", SfgaReferences);
customElements.define("sfga-combobox", SfgaCombobox);
customElements.define("sfga-add-reference-modal", SfgaAddReferenceModal);
customElements.define("sfga-help-modal", SfgaHelpModal);
