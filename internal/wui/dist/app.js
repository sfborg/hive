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

import { LitElement, html, css, svg, unsafeHTML, nothing } from "/vendor/lit-3.x.x.min.js";
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
  // plus — generic "add" affordance (Lucide). Used in combobox
  // pinned actions and any future generic add button that isn't
  // one of the more specific taxonomic add icons.
  plus: svg`
    <path d="M5 12h14" />
    <path d="M12 5v14" />
  `,
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
  // star — five-pointed star → "reference is fully solid" badge on
  // the reference picker (structured metadata + JATS sidecar
  // available for annotation). Lucide's star. Rendered filled via
  // the .variant-solid CSS treatment.
  star: svg`
    <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
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
  // triangle-alert — warning triangle → Issues screen. Neutral hue
  // in the sidebar; the screen itself carries the severity color via
  // per-row chips. Lucide's triangle-alert.
  "triangle-alert": svg`
    <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" />
    <path d="M12 9v4" />
    <path d="M12 17h.01" />
  `,
  // refresh-cw — clockwise circular arrow → Recompute button
  "refresh-cw": svg`
    <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
    <path d="M21 3v5h-5" />
    <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
    <path d="M8 16H3v5" />
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

// ---------- modal stack ----------
// Shared registry of currently-open modals so nested modals can hide
// their parents (visibility: hidden — DOM state preserved so draft
// values survive the trip). Only the topmost modal renders visibly;
// everything below is inert. Escape / close on the topmost pops it
// and restores whatever was underneath.
//
// Every modal-owning component (SfgaAddReferenceModal / SfgaHelpModal
// / SfgaAgentModal / SfgaConfirmModal — all dedicated components —
// plus SfgaDetail for its inline modals) obtains an opaque id via
// openModal() when its backdrop mounts, calls closeModal(id) when the
// backdrop unmounts, and applies the `is-covered` CSS class to its
// backdrop when isTopModal(id) is false. Stack changes fire
// subscribeModalStack callbacks so open modals re-render.
//
// See feedback_unbounded_modal_nesting — depth is intentionally
// unbounded; reload-window is the escape hatch.
const _modalStack = [];
const _modalStackListeners = new Set();

function openModal() {
  const id = Symbol("modal");
  _modalStack.push(id);
  _notifyModalStack();
  return id;
}

function closeModal(id) {
  const i = _modalStack.indexOf(id);
  if (i < 0) return;
  _modalStack.splice(i, 1);
  _notifyModalStack();
}

function isTopModal(id) {
  return (
    id != null && _modalStack[_modalStack.length - 1] === id
  );
}

function subscribeModalStack(fn) {
  _modalStackListeners.add(fn);
  return () => _modalStackListeners.delete(fn);
}

function _notifyModalStack() {
  for (const fn of _modalStackListeners) {
    try {
      fn();
    } catch (_) {
      // A listener throwing shouldn't stop the rest from getting
      // the notification — a stuck subscribed component would
      // otherwise wedge the whole stack.
    }
  }
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

// nomActFieldLabel returns the reference-field label for any name-
// model form (create pane, taxon-edit name section, pencil-edit name
// editor). Prefixes the label with "Original" or "Subsequent" based
// on multiple recomb signals so the label flips correctly even when
// the curator authored the name via the atomized fields directly
// (bypassing the parenthetical-verbatim shortcut). Empty draft → no
// prefix.
//
// Recomb signals (any one triggers "Subsequent"):
//   1. Verbatim authorship starts with `(` — the standard cross-code
//      convention.
//   2. basionym_authorship and combination_authorship are BOTH set
//      AND differ — curator explicitly said the recombining author
//      differs from the original. (COL redundantly populates both
//      fields with the SAME author on originals — Sarg./Sarg. —
//      which is why we require a difference, not just presence.)
//   3. basionym_authorship_year and combination_authorship_year are
//      BOTH set AND differ — same signal via years for archives that
//      atomize years without duplicating them.
//
// Terminology is nomenclatural-code-neutral per CLAUDE.md § PWA
// (web/) conventions: "original combination" and "subsequent
// combination" read correctly to curators from either code, and the
// label itself is self-explanatory enough that we don't add a hint
// paragraph beneath the input.
// formatBytes turns a byte count into a short human-readable
// string ("42 B", "3.4 KB", "1.2 MB"). Used by the file-tab
// upload result to give curators a size hint on the file that
// just landed in the library. Base-1024 math with a compact
// two-digit precision — scientific PDFs mostly land in the
// 1-10 MB range where "3.4 MB" reads better than "3423.6 KB".
function formatBytes(n) {
  if (typeof n !== "number" || n < 0) return "";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n / 1024;
  for (let i = 0; i < units.length; i++) {
    if (v < 1024 || i === units.length - 1) {
      return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
    }
    v = v / 1024;
  }
  return `${n} B`;
}

function nomActFieldLabel(draft) {
  const auth = (draft?.authorship || "").trim();
  const basA = (draft?.basionym_authorship || "").trim();
  const cA = (draft?.combination_authorship || "").trim();
  const basY = (draft?.basionym_authorship_year || "").trim();
  const cY = (draft?.combination_authorship_year || "").trim();
  if (!auth && !basA && !cA && !basY && !cY) {
    return "Nomenclatural act citation";
  }
  const isRecomb =
    auth.startsWith("(") ||
    (basA && cA && basA !== cA) ||
    (basY && cY && basY !== cY);
  if (isRecomb) return "Subsequent nomenclatural act citation";
  return "Original nomenclatural act citation";
}

// stripAuthorshipFromLabelHTML returns the label HTML with the
// trailing authorship string removed — so the caller can compose a
// different authorship rendering next to the italicized canonical.
// Server's BuildLabel formats as "<i>Canonical</i> Authorship" for
// italicized ranks and "Canonical Authorship" for higher ranks; both
// have the raw authorship at the tail. We try the raw form first,
// then the HTML-escaped form (BuildLabel escapes `&`/`<`/`>` in the
// html field), then give up — if nothing matches, return the HTML
// unchanged so the caller falls back to renderLabel(label).
function stripAuthorshipFromLabelHTML(labelHTML, authorship) {
  if (!labelHTML) return "";
  const suffix = (authorship || "").trim();
  if (!suffix) return labelHTML;
  const candidates = [suffix];
  const escaped = htmlEscape(suffix);
  if (escaped !== suffix) candidates.push(escaped);
  for (const s of candidates) {
    const trailing = " " + s;
    if (labelHTML.endsWith(trailing)) {
      return labelHTML.slice(0, -trailing.length);
    }
  }
  return labelHTML;
}

function htmlEscape(s) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// normalizeAuthorField translates a pipe-separated author list (COL's
// storage convention for multi-author atomized authorship fields) into
// the display form "A & B & C". Single-author values pass through
// unchanged. Empty in → empty out.
function normalizeAuthorField(s) {
  return (s || "")
    .split("|")
    .map((x) => x.trim())
    .filter(Boolean)
    .join(" & ");
}

// severityChip renders a small pill for a gsvalidator result severity.
// Color comes from --sev-* CSS custom properties (traffic-light default,
// swap via html[data-severity-palette="cvd"]). A leading glyph carries
// the signal too so severity is legible under color loss.
// Same glyph for every severity — a warning triangle. Severity is
// conveyed by the chip color (via var(--sev-*)) and the label text;
// keeping the icon consistent avoids the mixed-iconography look
// of a per-severity glyph set (bug, X, i, triangle) and lets the
// color palette carry the semantic weight it's designed for.
const SEV_META = {
  error: { glyph: "⚠", label: "Error" },
  warn:  { glyph: "⚠", label: "Warn"  },
  info:  { glyph: "⚠", label: "Info"  },
  debug: { glyph: "⚠", label: "Debug" },
};
function severityChip(severity) {
  const key = (severity || "warn").toLowerCase();
  const meta = SEV_META[key] || SEV_META.warn;
  return html`<span class="sev-chip sev-${key}"
    ><span class="sev-glyph">${meta.glyph}</span>${meta.label}</span
  >`;
}

// Shared CSS for severityChip's rendered markup. Included in every
// component that hosts a severity chip via `static styles = [...]` —
// Lit shadow-DOM scoping means each host has to import the block
// itself; a global rule in styles.css wouldn't cross the boundary.
// See severityChip() for the markup this dresses up.
const severityChipStyles = css`
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
`;

// Shared button vocabulary. Included in every component that renders
// buttons via `static styles = [buttonStyles, ...]`. Variants per
// DESIGN.md § Buttons: default (secondary/neutral), .primary (main
// action), .danger (outlined destructive in a toolbar), .danger-primary
// (filled destructive that IS the primary intent — e.g. confirm-modal
// Delete), .icon-btn (square icon-only; requires aria-label + title),
// .icon-btn.subtle (borderless icon button for header nav / peripheral
// controls; hover reveals a background tint instead of a border),
// .close-x (bare glyph for modal dismissal only). Additions require a
// design-system extension per DESIGN.md § Extending this system.
const buttonStyles = css`
  button {
    background: transparent;
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--sp-1) var(--sp-3);
    font: inherit;
    cursor: pointer;
  }
  button:hover:not(:disabled) {
    border-color: var(--accent);
  }
  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }
  button.primary {
    background: var(--accent);
    color: var(--accent-fg);
    border-color: var(--accent);
  }
  button.danger {
    color: var(--error);
    border-color: var(--error);
  }
  button.danger:hover:not(:disabled) {
    border-color: var(--error);
    background: color-mix(in oklab, var(--error) 12%, transparent);
  }
  button.danger-primary {
    background: var(--error);
    color: var(--bg);
    border-color: var(--error);
  }
  button.danger-primary:hover:not(:disabled) {
    background: color-mix(in oklab, var(--error) 82%, black);
    border-color: color-mix(in oklab, var(--error) 82%, black);
  }
  button.icon-btn {
    padding: var(--sp-1);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }
  button.icon-btn:hover:not(:disabled) {
    border-color: var(--accent);
    color: var(--accent);
  }
  button.icon-btn svg {
    display: block;
  }
  /* Subtle variant for header navigation / peripheral controls where a
     visible border would compete with the data. Hover reveals a soft
     background tint instead. */
  button.icon-btn.subtle {
    border-color: transparent;
  }
  button.icon-btn.subtle:hover:not(:disabled) {
    border-color: transparent;
    background: color-mix(in oklab, var(--fg) 8%, transparent);
    color: var(--fg);
  }
  /* Subtle + danger: destructive header actions (trash / delete) reuse
     the borderless treatment but shift the hover tint to error so the
     icon reads as destructive on interaction, not just at rest. */
  button.icon-btn.subtle.danger {
    color: var(--fg);
    border-color: transparent;
  }
  button.icon-btn.subtle.danger:hover:not(:disabled) {
    border-color: transparent;
    color: var(--error);
    background: color-mix(in oklab, var(--error) 8%, transparent);
  }
  button.close-x {
    background: transparent;
    color: var(--dim);
    border: none;
    border-radius: 0;
    padding: 0;
    font-size: var(--fs-xl);
    line-height: 1;
  }
  button.close-x:hover:not(:disabled) {
    color: var(--fg);
    border-color: transparent;
  }
`;

// Shared form-field vocabulary. Included in every component that renders
// a form via `static styles = [formFieldStyles, ...]`. Covers inputs,
// textareas, selects, labels (right-aligned dim mono at --fs-sm), the
// .req marker (adds " *" in --error), and the focus behavior (accent
// border). Component-local form layout (grid columns, label alignment
// overrides, custom widgets) still lives in the component's own styles.
const formFieldStyles = css`
  input,
  textarea,
  select {
    background: var(--bg);
    color: var(--fg);
    border: 1px solid var(--border);
    padding: var(--sp-1) var(--sp-2);
    font-family: inherit;
    font-size: var(--fs-md);
    min-width: 0;
  }
  textarea {
    min-height: 4rem;
    resize: vertical;
    font-family: var(--font-body);
  }
  input:focus,
  textarea:focus,
  select:focus {
    outline: none;
    border-color: var(--accent);
  }
  /* Placeholder sits between --dim and --bg so a hint reads as
     clearly unfilled — distinguishes empty inputs from pre-
     populated ones at a glance. Derived from existing tokens via
     color-mix; no new palette values. Opacity override handles
     Firefox's 0.54 default. */
  input::placeholder,
  textarea::placeholder {
    color: color-mix(in oklab, var(--dim) 55%, var(--bg));
    opacity: 1;
  }
  label {
    color: var(--dim);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  label.req::after {
    content: " *";
    color: var(--error);
  }
  .req {
    color: var(--error);
  }
`;

// warningBannerStyles renders the shared open-issues banner used at
// the top of the taxon detail pane, per-row edit modals, and any
// dedicated modal component that surfaces gsvalidator issues (e.g.
// sfga-add-reference-modal's edit mode). Consumers apply via
// `static styles = [warningBannerStyles, ...]`. Neutral chrome with
// per-item severity chips so a mixed batch (warn + info + error)
// still ranks visually. Kept as a shared module so every surface
// shows the same visual language regardless of which shadow root
// it renders in — matches curator recognition parity.
const warningBannerStyles = css`
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
  /* Fixed first-column width so the issue text lines up across rows
     regardless of the severity chip's natural width. */
  .warning-banner li {
    margin: 0.25rem 0;
    display: grid;
    grid-template-columns: 4.5rem 1fr;
    gap: 0.5rem;
    align-items: baseline;
  }
  .warning-banner li > .sev-chip {
    justify-self: start;
  }
  .warning-banner .warning-rule {
    font-weight: 600;
    color: var(--fg);
  }
`;

// trapFocus keeps keyboard focus inside a modal container. Call it in
// connectedCallback with the root element to trap in (usually the
// component's shadow root or the .modal child), and invoke the returned
// release() in disconnectedCallback. Also focuses the initial target
// (opts.initialFocus, a selector or element) and restores focus to
// whichever element had focus before trap install when released — so
// dismissing a modal returns the curator to whatever they clicked to
// open it. Works across shadow-DOM boundaries by reading activeElement
// from the trapped element's own root node.
function trapFocus(container, opts = {}) {
  const root = container.getRootNode();
  const previouslyFocused = root.activeElement;

  const focusables = () =>
    Array.from(
      container.querySelectorAll(
        'button:not([disabled]), a[href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    );

  const onKeyDown = (e) => {
    if (e.key !== "Tab") return;
    const items = focusables();
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    // getRootNode().activeElement gives the active element within the
    // same shadow root as the container — document.activeElement would
    // return the host element (sfga-app) instead of the focused button.
    const active = root.activeElement;
    if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  };

  container.addEventListener("keydown", onKeyDown);

  // Defer initial focus so Lit finishes rendering the target element.
  Promise.resolve().then(() => {
    let target = null;
    if (opts.initialFocus) {
      target =
        typeof opts.initialFocus === "string"
          ? container.querySelector(opts.initialFocus)
          : opts.initialFocus;
    }
    if (!target) target = focusables()[0];
    if (target) target.focus();
  });

  return () => {
    container.removeEventListener("keydown", onKeyDown);
    if (previouslyFocused && typeof previouslyFocused.focus === "function") {
      previouslyFocused.focus();
    }
  };
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
// [0, max]. Both filled and empty use ★ so the glyph metrics match —
// the visual distinction comes from color (.star-filled uses the accent
// color, .star-empty uses --border for a faint outline effect). Using ☆
// for empty produced a size/baseline mismatch in most system fonts.
function renderStars(value, max) {
  const v = Math.max(0, Math.min(max, Number(value) || 0));
  return html`<span class="stars"
    ><span class="star-filled">${"★".repeat(v)}</span
    ><span class="star-empty">${"★".repeat(max - v)}</span>
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

// synonymStatusSource restricts the taxonomic_status vocab to the
// three values that describe a SYNONYM row's relationship to its
// accepted taxon. sfga's taxonomic_status vocab mixes accepted-side
// (ACCEPTED, PROVISIONALLY_ACCEPTED, VALID, PROVISIONALLY_VALID,
// BARE_NAME) and synonym-side (SYNONYM, AMBIGUOUS_SYNONYM,
// MISAPPLIED) terms together — the synonym form only ever wants the
// synonym-side subset, per CoLDP practice. See CLAUDE.md
// § Nomenclatural-code-neutral copy for the reasoning: BARE_NAME is
// inferred from "name row with no synonym or taxon link," not
// something curators pick.
function synonymStatusSource() {
  const allowed = new Set(["SYNONYM", "AMBIGUOUS_SYNONYM", "MISAPPLIED"]);
  return (q) => {
    const terms = api.vocab.get("taxonomic_status");
    const needle = (q || "").toLowerCase().trim();
    return terms
      .filter((t) => allowed.has(t.id))
      .filter(
        (t) =>
          !needle ||
          (t.name || "").toLowerCase().includes(needle) ||
          (t.id || "").toLowerCase().includes(needle),
      )
      .map((t) => ({ id: t.id, name: t.name || t.id || "(unset)" }));
  };
}

// isoSource / isoResolver back the ISO-vocab pickers (countries,
// languages, sex). Each entry renders as "<name> - <primary code>[ -
// <secondary code>]" so the curator can search either by human name
// or by ISO code. `id` on the committed value is the primary code
// (US, eng, MALE); the display string carries the full
// context. Fetches the vocab from /api/vocab/{name} on first call
// and caches for the process lifetime — the ISO catalogs are
// immutable at build time.
//
// `pri` and `sec` are the field names on the vocab item for the
// primary code (== id) and an optional secondary code (e.g., alpha-3
// for countries). `symbol` (used by the sex vocab) appends a glyph
// after the name.
function isoSource(cache, pri, sec, symbolField) {
  return async (q) => {
    if (!cache.isLoaded()) await cache.load();
    const items = cache.get() || [];
    const needle = (q || "").toLowerCase().trim();
    const matches = needle
      ? items.filter((t) => {
          const hay = `${t.name || ""} ${t.id || ""} ${t[sec] || ""}`
            .toLowerCase();
          return hay.includes(needle);
        })
      : items;
    return matches.map((t) => ({
      id: t.id,
      name: isoDisplayLabel(t, pri, sec, symbolField),
    }));
  };
}

function isoResolver(cache, pri, sec, symbolField) {
  return async (id) => {
    if (!id) return "";
    if (!cache.isLoaded()) await cache.load();
    const items = cache.get() || [];
    const t = items.find((t) => t.id === id);
    return t ? isoDisplayLabel(t, pri, sec, symbolField) : id;
  };
}

function isoDisplayLabel(t, pri, sec, symbolField) {
  const parts = [t.name || t.id || ""];
  if (t[pri]) parts.push(t[pri]);
  if (sec && t[sec] && t[sec] !== t[pri]) parts.push(t[sec]);
  const base = parts.join(" - ");
  const sym = symbolField ? t[symbolField] : "";
  return sym ? `${base} ${sym}` : base;
}

// Prebuilt source/resolver pairs for the three ISO vocabs.
const countrySource = isoSource(api.countries, "id", "alpha3");
const countryResolver = isoResolver(api.countries, "id", "alpha3");
const languageSource = isoSource(api.languages, "id", null);
const languageResolver = isoResolver(api.languages, "id", null);
const sexSource = isoSource(api.sex, "id", null, "symbol");
const sexResolver = isoResolver(api.sex, "id", null, "symbol");

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

// NOMEN terms hidden from the picker. Two categories, both driven
// by "these should never be a positive curator pick because the
// natural default carries the same signal":
//
//   Redundant "valid" markers — blank col__status_id already means
//   "valid" across all four codes. NOMEN-OWL round-tripping into
//   ChecklistBank once mapped "ICZN valid" onto CoLDP's
//   POTENTIALLY_VALID, which read as "not valid yet" and upset
//   zoological taxonomists. Convention since then: leave status
//   empty for valid names.
//
//   Fossil markers — CoLDP models fossil-ness as a taxon-level
//   `extinct` flag, not a name-level status. A name-level fossil
//   pick overlaps with taxon.extinct and produces duplicate /
//   inconsistent signal; hive routes curators to taxon.extinct
//   instead. NOMEN_0000206 ("ICZN based on fossil genus formula")
//   is NOT filtered — it describes how the name was constructed,
//   not whether the taxon is fossil.
//
// Legacy rows that already carry a filtered URI still resolve to
// their label via nomenResolver — picker-only filtering; the
// display path is untouched.
const NOMEN_HIDDEN_URIS = new Set([
  // Valid — redundant with blank.
  "http://purl.obolibrary.org/obo/NOMEN_0000007", // ICN validly published name
  "http://purl.obolibrary.org/obo/NOMEN_0000084", // ICNP validly published name
  "http://purl.obolibrary.org/obo/NOMEN_0000125", // ICVCN valid
  "http://purl.obolibrary.org/obo/NOMEN_0000224", // ICZN valid
  // Fossil — belongs on taxon.extinct, not name.status.
  "http://purl.obolibrary.org/obo/NOMEN_0000055", // ICZN fossil
  "http://purl.obolibrary.org/obo/NOMEN_0000057", // ICN fossil
]);

// nomenSource(codeID) returns a combobox source that filters NOMEN
// terms by the current name's nomenclatural code. Empty codeID → all
// terms. Matches against label or short local identifier. Terms in
// NOMEN_HIDDEN_URIS are dropped from the picker.
function nomenSource(codeID) {
  return (q) => {
    const scoped = api.nomen
      .filterByCode(codeID)
      .filter((t) => !NOMEN_HIDDEN_URIS.has(t.id));
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
// (server-side substring across author/title/citation/doi). Each
// match renders as "Author (Year) Title" so the picker line reads
// like a citation. Empty query returns [] to skip flashing the whole
// list.
//
// DOI normalisation: hive stores DOIs in bare 10.NNNN/… form, but
// curators paste them in a variety of shapes (doi.org URL, dx.doi.org
// URL, doi: CURIE). Detect the DOI shape and search by the bare form
// so the LIKE predicate matches. Non-DOI queries pass through
// unchanged.
async function referenceSource(q) {
  if (!q || q.length < 2) return [];
  const doi = extractDOI(q);
  const searchQ = doi || q;
  try {
    const page = await api.reference.search({ q: searchQ, limit: 20 });
    return (page.items || []).map((h) => ({
      id: h.id,
      name: referenceHitLabel(h),
      // Badge is always attached for references — the picker never
      // looks non-interactive for a resolved reference. Icon +
      // color depend on whether the row needs curator attention;
      // referenceIssueCount combines the server's persisted
      // __gsvalidator_results count with an inline structured-
      // metadata check that runs on already-fetched fields (no
      // per-row sync required). This lets the warning surface
      // instantly on large archives that haven't been through a
      // full validation reindex. has_source_doc drives the gold-
      // star tier when the JATS sidecar is attached (Slice 3 of
      // REFERENCE_PDF_PLAN.md; falsy today until PDF ingest lands).
      // max_severity from the server's batched issue-summary lookup
      // colors the triangle when count > 0 (via validationSeverityBadge).
      badge: referenceEditBadge(
        referenceIssueCount(h),
        !!h.has_source_doc,
        h.max_severity,
      ),
    }));
  } catch (_) {
    return [];
  }
}

// referenceIssueCount folds together the persisted gsvalidator
// count (from __gsvalidator_results, batched into apiReferenceHit /
// apiReference) with an inline "missing structured metadata" check
// on the reference's own fields. Either signal on its own is enough
// to mark the reference as needing attention.
//
// The inline check mirrors hive.reference_missing_structured_metadata
// (server-side validator). Duplicated on purpose: the server rule
// ships the persistent Issues-view row, the inline check lets the
// picker badge surface WITHOUT waiting for a validation reindex
// (which can take hours on large archives like CoL). If either
// says "not clean", the badge shows.
function referenceIssueCount(ref) {
  const persisted = ref.issue_count || 0;
  const citation = (ref.citation || "").trim();
  const author = (ref.author || "").trim();
  // Search hits carry `year` (derived from `issued`); detail rows
  // carry `issued` directly. Accept either.
  const year = (ref.year || ref.issued || "").trim();
  const inlineGap = citation !== "" && (author === "" || year === "");
  return persisted + (inlineGap ? 1 : 0);
}

// validationSeverityBadge builds the "record has open issues" badge
// used by every picker / list surface (reference picker, name picker,
// Nomen History rows, any future one). One helper → one visual
// language: triangle-alert icon, color = highest severity present,
// tooltip = short curator-facing summary, badge-click dispatches
// `kind` so the parent's handler routes to the right fix flow.
//
// Severity mapping:
//
//   error → red   (--sev-error)     hard-blocker or seriously wrong data
//   warn  → amber (--sev-warn)      curator should look
//   info  → green (--sev-info)      informational; no action forced
//   debug → dim                     diagnostic-only; rarely rendered
//
// Unknown / missing severity falls back to "warn" so a badge still
// surfaces — better to render an amber triangle than to hide the
// issue because the wire projection didn't include severity.
//
// tooltip override: pass opts.tooltip when the surface has a more
// specific short line (e.g., "This reference has an open issue —
// click to fix"). Default is a short generic phrasing.
function validationSeverityBadge(count, maxSeverity, kind, opts = {}) {
  const n = count || 0;
  const sev = normalizeSeverity(maxSeverity);
  let tooltip = opts.tooltip;
  if (!tooltip) {
    const plural = n === 1 ? "" : "s";
    tooltip = `${n} open ${sev} issue${plural} — click to view`;
  }
  return {
    icon: "triangle-alert",
    tooltip,
    kind: kind || "record-issue",
    variant: `sev-${sev}`,
  };
}

function normalizeSeverity(s) {
  switch (s) {
    case "error":
    case "warn":
    case "info":
    case "debug":
      return s;
    default:
      return "warn";
  }
}

// referenceEditBadge builds the always-present affordance the
// sfga-combobox renders next to a reference row (in dropdown or
// in-input). Three visual states, one click target — all open the
// same reference-edit modal via badge-click:
//
//   * count > 0                    → triangle-alert (colored by
//                                    maxSeverity; see
//                                    validationSeverityBadge).
//                                    "Click to fix issues."
//   * count == 0, no source doc    → book (dim color).
//                                    "Click to view or edit."
//   * count == 0, source doc ready → star (gold, filled).
//                                    "This reference is solid —
//                                     structured + source attached."
//
// Gold-star gamifies the "make this reference solid" workflow:
// curators start with all books / warnings, learn the click flow,
// and watch the pane fill with stars as they upgrade references
// with structured metadata + ingested source PDFs.
//
// Design rationale for the icons: pencil was rejected because it
// reads as "edit the picker" rather than "edit the referenced
// object"; domain-typed icons (book for references, star for
// solid/complete) are clearer signal-per-glance.
function referenceEditBadge(count, hasSourceDoc, maxSeverity) {
  const n = count || 0;
  if (n > 0) {
    const plural = n === 1 ? "" : "s";
    return validationSeverityBadge(n, maxSeverity, "reference-issue", {
      tooltip:
        n > 1
          ? `${n} open issue${plural} on this reference — click to fix`
          : "This reference has an open issue — click to fix",
    });
  }
  if (hasSourceDoc) {
    return {
      icon: "star",
      tooltip:
        "Reference is solid — structured metadata + source document attached. Click to view or edit.",
      kind: "reference-solid",
      variant: "solid",
    };
  }
  return {
    icon: "book",
    tooltip: "View or edit this reference",
    kind: "reference-edit",
    variant: "info",
  };
}

// referenceResolver — id → display label. The name-detail response
// server-populates `reference_label` alongside `reference_id`, so most
// callers can skip this and hydrate the combobox from the already-
// present label. Kept as a fallback for cases where only the id is
// available.
async function referenceResolver(id) {
  if (!id) return "";
  // Same policy as nameResolver: throw on fetch failure. Swallowing
  // and returning the id makes the combobox cache the id as its
  // display value, which then sticks past the transient failure.
  //
  // Always returns {name, badge} for references — the picker never
  // looks non-interactive for a resolved reference. Badge state
  // (warning vs. book) follows referenceIssueCount, which combines
  // persisted __gsvalidator_results with an inline structured-
  // metadata check on the fetched row.
  const [r, issues] = await Promise.all([
    api.reference.get(id),
    api.issue
      .list({ table: "reference", record_id: id, limit: 20 })
      .catch(() => ({ items: [] })),
  ]);
  const name = referenceHitLabel({
    author: r.author,
    year: r.issued ? String(r.issued).slice(0, 4) : "",
    title: r.title,
    citation: r.citation,
  });
  const persistedCount = (issues.items || []).length;
  const merged = referenceIssueCount({
    ...r,
    issue_count: persistedCount,
  });
  // Compute max severity from the fetched issues list — same signal
  // the batched issue-summary sends on search hits, computed here
  // for the in-input case where we already had the full list.
  const maxSev = maxSeverityOf(issues.items || []);
  return {
    name,
    badge: referenceEditBadge(merged, !!r.has_source_doc, maxSev),
  };
}

// maxSeverityOf returns the highest severity in an issues array,
// using the same error > warn > info > debug ordering as the server.
// Returns empty string on an empty array.
function maxSeverityOf(issues) {
  const rank = { error: 3, warn: 2, info: 1, debug: 0 };
  let bestRank = -1;
  let best = "";
  for (const i of issues) {
    const r = rank[i.severity] ?? -1;
    if (r > bestRank) {
      bestRank = r;
      best = i.severity;
    }
  }
  return best;
}

// refCitationAuthorAndYear returns (author, year) for a reference,
// preferring the atomized author + issued columns and falling back
// to parsing the citation string when those are empty. Many
// CoL-derived archives populate only the free-text citation field
// (e.g., "Johnson, J. Y. (1863). Description of a new species...")
// and leave author / issued NULL; the fallback pulls what it can so
// the citation-pick backfill still produces useful values.
//
// Year extraction: first 4-digit run in the range 1600-2099.
// Author extraction: first token when the citation starts with a
// surname-shaped word (capitalized, letters only, followed by an
// initial or a comma). Skips citations that lead with a title or
// abbreviation ("Suppl. Johnson's Gard. Dict.: 1015 (1882)" → no
// author extracted). Fallback is heuristic; fill-empty-only
// semantics limit the blast radius when it guesses wrong.
function refCitationAuthorAndYear(ref) {
  let author = refFirstAuthor(ref.author || "");
  let year = (ref.issued || "").slice(0, 4);
  const citation = (ref.citation || "").trim();

  if (!year && citation) {
    const m = /\b(1[6-9]\d{2}|20\d{2})\b/.exec(citation);
    if (m) year = m[1];
  }

  if (!author && citation) {
    // Two shapes we support:
    //   "Johnson, J. Y. (1863)..."        surname , initials
    //   "HÁVA J. 2009..."             surname initials
    //   "Biscaccianti A. B., Esser J..."  surname initials, ...
    // Reject leading tokens with digits, dots (abbreviations), or
    // lowercase — those signal title text like "Suppl." or
    // "ed. 2." leading a bare-title citation.
    const m = /^([A-ZÀ-Ž][A-Za-zÀ-ž'-]+)(?:,|\s+(?:[A-Z]\.?|[A-Z][a-z]))/.exec(
      citation,
    );
    if (m) author = m[1];
  }

  return { author, year };
}

// emptyManualReference is the seed for the add-reference modal's
// Manual tab in add-mode. All fields present as empty strings so
// the form doesn't get bit by undefined-vs-empty conditionals.
function emptyManualReference() {
  return {
    author: "",
    editor: "",
    title: "",
    title_short: "",
    container_title: "",
    container_title_short: "",
    container_author: "",
    issued: "",
    volume: "",
    issue: "",
    edition: "",
    page: "",
    publisher: "",
    publisher_place: "",
    isbn: "",
    issn: "",
    doi: "",
    link: "",
    type: "",
    remarks: "",
    citation: "",
  };
}

// manualFromReference hydrates the Manual tab's draft from a loaded
// apiReference. Symmetric with emptyManualReference — every field
// present, empty strings for unset values.
function manualFromReference(r) {
  return {
    author: r.author || "",
    editor: r.editor || "",
    title: r.title || "",
    title_short: r.title_short || "",
    container_title: r.container_title || "",
    container_title_short: r.container_title_short || "",
    container_author: r.container_author || "",
    issued: r.issued || "",
    volume: r.volume || "",
    issue: r.issue || "",
    edition: r.edition || "",
    page: r.page || "",
    publisher: r.publisher || "",
    publisher_place: r.publisher_place || "",
    isbn: r.isbn || "",
    issn: r.issn || "",
    doi: r.doi || "",
    link: r.link || "",
    type: r.type || "",
    remarks: r.remarks || "",
    citation: r.citation || "",
  };
}

// manualFromMetadata hydrates the Manual tab's draft from GROBID's
// extracted header (shape: apiUploadBibHeader). The subset of
// fields GROBID reliably extracts (title / author / issued /
// container_title / volume / issue / page / publisher / doi) maps
// directly onto apiReference. Every apiReference field not covered
// by GROBID (editor, container_author, isbn, issn, link, etc.)
// stays at the empty-string default from emptyManualReference so
// the curator sees a complete form ready for review.
//
// Called from _uploadFile as the Manual-tab fallback when the DOI
// hop isn't available (no DOI in the paper, or OpenAlex miss).
function manualFromMetadata(m) {
  const base = emptyManualReference();
  if (!m) return base;
  return {
    ...base,
    title: m.title || "",
    author: m.author || "",
    issued: m.issued || "",
    container_title: m.container_title || "",
    volume: m.volume || "",
    issue: m.issue || "",
    page: m.page || "",
    publisher: m.publisher || "",
  };
}

// refFirstAuthor extracts a single surname-ish token from a
// reference's author string, for the citation-pick backfill of
// combination_authorship. CoLDP references store the author field in
// a variety of shapes across datasets:
//
//   "Linnaeus, C."          (surname-first, CoL style)
//   "Sturm, H."             (surname-first)
//   "Smith, J.; Jones, K."  (multi-author, semicolon-separated)
//   "Smith, J., Jones, K."  (multi-author, comma-separated with initials)
//   "Smith, J. & Jones, K." (multi-author, ampersand)
//   "J.W.Sturm"             (initials-first, single token — some CoL rows)
//   "Sturm|Ker"             (pipe-separated, ChecklistBank export)
//
// The heuristic: take the first author unit (split on ; | or & or
// " and "), then extract the surname — the piece before the first
// comma, or the whole token when there's no comma. Falls back to
// the raw first-unit when the token has no obvious surname
// structure. Empty in → empty out.
//
// This is best-effort — the atomized authorship convention is
// surname-only ("Linnaeus", "Smith"), but reference formats vary
// wildly. Fill-empty-only semantics limit the blast radius: curator
// can override anything that looks wrong.
function refFirstAuthor(raw) {
  const s = (raw || "").trim();
  if (!s) return "";
  const first = s.split(/;|\||\s&\s|\sand\s/)[0].trim();
  if (!first) return "";
  // Surname-first "Linnaeus, C." → "Linnaeus". Keep the comma-first
  // token; drop trailing initials and commas.
  const beforeComma = first.split(",")[0].trim();
  return beforeComma || first;
}

// referenceHitLabel composes the "Author (Year) Title" line used by
// both the picker source and the resolver. Falls back to citation or
// id when structured fields are missing.
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

// taxonSource hits /api/taxon/search and normalises hits for the
// combobox. Options:
//   * include_synonyms — extend results with accepted taxa reached
//     via a matching synonym.
//   * mode — "prefix" (default, backend-implicit), "partial", or
//     "fuzzy" (see backend rollout; unknown modes fall back to
//     prefix server-side, so passing "partial" before backend step 2
//     lands is a graceful no-op).
//
// The row shape the combobox expects:
//   { id, name, isSynonym, matched, parent? }
// where `id` is always the accepted taxon (backend contract — synonym
// hits resolve to their accepted taxon's id), and `parent` is a
// {id, label, rank} object elided for root taxa. The combobox uses
// parent for synonym-hint and homonym-disambiguation rendering (see
// DESIGN.md § Search combobox result-row hints).
async function taxonSource(q, opts = {}) {
  if (!q || q.length < 2) return [];
  const includeSynonyms = opts.includeSynonyms !== false; // default on
  const mode = opts.mode || "prefix";
  try {
    const params = { q, limit: 20, include_synonyms: includeSynonyms };
    if (mode && mode !== "prefix") params.mode = mode;
    const page = await api.taxon.search(params);
    return (page.items || []).map((hit) => ({
      id: hit.id,
      name: hit.label?.text || hit.name,
      isSynonym: !!hit.is_synonym,
      matched: hit.matched_name || "",
      parent: hit.parent || null,
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

// nameSource hits /api/name/search. Backs the "link existing original
// combination" picker in the create pane's basionym section (and any
// future name-picker use case). Returns rows shaped {id, name} where
// `name` reads like a citation line — canonical + authorship. The
// backend orders by canonical; ties break by authorship.
async function nameSource(q) {
  if (!q || q.length < 2) return [];
  try {
    const page = await api.name.search({ q, limit: 20 });
    return (page.items || []).map((h) => ({
      id: h.id,
      name: [h.scientific || h.full, h.authorship].filter(Boolean).join(" "),
    }));
  } catch (_) {
    return [];
  }
}

async function nameResolver(id) {
  if (!id) return "";
  // Don't swallow errors here — a transient fetch failure (server
  // restart, connection blip) that "falls back to the id" produces
  // a stuck picker: the combobox caches the id as valueName, its
  // `!valueName` re-attempt guard is false forever, and the curator
  // sees the raw id where they expected a name. Let the combobox
  // handle failure (renders "(lookup failed)" + retries on focus).
  const n = await api.name.get(id);
  return [
    n.scientific_name || n.canonical_simple || n.canonical_full,
    n.authorship,
  ]
    .filter(Boolean)
    .join(" ");
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
    // Reference id to pre-select on the References screen — set by
    // _onIssueNavigate when a reference-scoped issue is opened, so
    // the References component focuses that row on first render.
    _pendingReferenceId: { state: true },
    // Which workspace tab (bibliography / digitize / assertions)
    // to activate when SfgaReferences mounts or a new reference is
    // selected. Synced with location.hash so reloads land back on
    // the tab the curator was on.
    _pendingWorkspaceTab: { state: true },
    // Omnibox filter state — persists to localStorage under
    // "hive-search-filters". See DESIGN.md § Search combobox filter
    // chips. `mode` is "prefix" (default) / "partial" / "fuzzy" —
    // matches the /api/taxon/search backend contract. `synonyms` is
    // the affirmative form of the "Accepted only" chip (checked =
    // synonyms off = accepted-only). Kept as an affirmative here so
    // the API param maps directly: include_synonyms=this.synonyms.
    _searchFilters: { state: true },
  };

  // View list — matches CLAUDE.md § keybinding conventions and the
  // TUI's screen enum. New views append here; the sidebar and shortcut
  // handler pick them up automatically. Icon is a Lucide icon name
  // registered in the iconPaths map above.
  // Sidebar order puts Metadata first so a curator scanning the nav is
  // reminded that the archive has editable metadata. Taxa remains the
  // default screen on open (see the `screen` state default) because
  // that's where editing time actually gets spent; the sidebar is a
  // menu, not a startup route.
  static views = [
    { id: "metadata", label: "Metadata", icon: "info", key: "m" },
    { id: "taxa", label: "Taxa", icon: "network", key: "t" },
    { id: "references", label: "References", icon: "book", key: "r" },
    { id: "issues", label: "Issues", icon: "triangle-alert", key: "i" },
  ];

  static styles = [
    buttonStyles,
    css`
    :host {
      display: grid;
      grid-template-rows: auto 1fr;
      height: 100vh;
      color: var(--fg);
      background: var(--bg);
    }
    /* Skip-to-main link — visually hidden until focused, then anchors
       to the top-left with the accent-fill so keyboard/screen-reader
       users can bypass the sidebar and header. Activating it moves
       focus into <main> (see _skipToMain). */
    .skip-link {
      position: absolute;
      top: var(--sp-2);
      left: var(--sp-2);
      background: var(--accent);
      color: var(--accent-fg);
      padding: var(--sp-1) var(--sp-3);
      border-radius: var(--radius-md);
      text-decoration: none;
      font: inherit;
      z-index: var(--z-popover);
      transform: translateX(-200%);
    }
    .skip-link:focus {
      transform: translateX(0);
      outline: 2px solid var(--fg);
      outline-offset: 2px;
    }
    /* main must be focusable (tabindex="-1") so _skipToMain can move
       focus there. Suppress its default focus outline — the
       tab-stop is a jump-target, not a visible focus destination. */
    main:focus {
      outline: none;
    }
    /* Three-column header: left is reserved (future hamburger menu for
       load-database / share / sign-in-out); center holds the archive
       title with schema version inline; right holds peripheral controls
       (help, theme). The 1fr/auto/1fr split keeps the title truly
       centered independent of what lands in the left slot. */
    header {
      border-bottom: 1px solid var(--border);
      padding: var(--sp-2) var(--sp-3);
      display: grid;
      grid-template-columns: 1fr auto 1fr;
      align-items: center;
      gap: var(--sp-4);
    }
    header .title {
      font-weight: 600;
      text-align: center;
    }
    header .title .schema {
      color: var(--dim);
      font-weight: normal;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
      margin-left: var(--sp-1);
    }
    header .header-buttons {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      justify-self: end;
    }
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
    /* Taxa screen splits the viewport 50/50 between tree and detail
       — the classification is a first-class artifact, not a nav
       sidebar. The minmax lower bound preserves a legible tree on
       narrow windows before the columns collapse to equal shares. */
    .screen.taxa {
      grid-template-columns: minmax(20rem, 1fr) 1fr;
    }
    .screen.metadata {
      grid-template-columns: 1fr;
      padding: 1rem;
      overflow: auto;
    }
    /* Single grid track — the sfga-references custom element owns
       its own list / detail / reader multi-column layout inside
       its shadow root. The taxa screen's two-child pattern (aside
       + section as direct children of the outer grid) doesn't fit
       here because the component is one custom element. */
    .screen.references {
      grid-template-columns: 1fr;
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
  `,
  ];

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
    this._pendingReferenceId = "";
    this._pendingWorkspaceTab = "";
    this._searchFilters = this._loadSearchFilters();
    // Bind the top-bar source once so the combobox reference is
    // stable across renders; the closure reads `this._searchFilters`
    // live, so filter toggles pick up on the next _runSearch tick
    // without needing to rebuild the source.
    this._taxonSearchSource = (q) =>
      taxonSource(q, {
        mode: this._searchFilters.mode,
        includeSynonyms: this._searchFilters.synonyms,
      });
    this._applyTheme();
    this._onGlobalKey = this._onGlobalKey.bind(this);
  }

  // _loadSearchFilters reads persisted omnibox toggles from
  // localStorage; returns the defaults from § Search combobox filter
  // chips (prefix mode, synonyms on) for a fresh curator or any
  // parse failure. Guarded against schema drift by validating each
  // field before accepting it.
  _loadSearchFilters() {
    const defaults = { mode: "prefix", synonyms: true };
    try {
      const raw = localStorage.getItem("hive-search-filters");
      if (!raw) return defaults;
      const parsed = JSON.parse(raw);
      const mode = ["prefix", "partial", "fuzzy"].includes(parsed.mode)
        ? parsed.mode
        : "prefix";
      const synonyms =
        typeof parsed.synonyms === "boolean" ? parsed.synonyms : true;
      return { mode, synonyms };
    } catch (_) {
      return defaults;
    }
  }

  _saveSearchFilters() {
    try {
      localStorage.setItem(
        "hive-search-filters",
        JSON.stringify(this._searchFilters),
      );
    } catch (_) {
      // localStorage unavailable (private mode, quota) — filters
      // still work for the session, just don't persist.
    }
  }

  // _onSearchFilterChange handles a `filter-change` event from the
  // top-bar combobox. Chip keys map to filter state as follows:
  //   * "partial"  → mode = partial   (toggling off restores prefix)
  //   * "fuzzy"    → mode = fuzzy     (toggling off restores prefix)
  //   * "accepted" → synonyms = !value (chip is affirmative
  //                                     "accepted only", state is
  //                                     affirmative "synonyms on")
  // Partial and fuzzy are mutually exclusive at the mode level; the
  // chip UI presents them as independent toggles so curators can
  // switch between the two without an intermediate "clear" step.
  _onSearchFilterChange(e) {
    const { key, value } = e.detail;
    const next = { ...this._searchFilters };
    if (key === "partial") {
      next.mode = value ? "partial" : "prefix";
    } else if (key === "fuzzy") {
      next.mode = value ? "fuzzy" : "prefix";
    } else if (key === "accepted") {
      next.synonyms = !value;
    }
    this._searchFilters = next;
    this._saveSearchFilters();
  }

  async connectedCallback() {
    super.connectedCallback();
    // Alt+letter view switches fire globally so the shortcut works
    // regardless of which input has focus. Bound at document level;
    // torn down in disconnectedCallback.
    window.addEventListener("keydown", this._onGlobalKey);
    // Screen-level action buttons live in the app header rather than
    // in each pane, so panes keep full horizontal room for their data
    // (long scientific names + authorships in taxa; long descriptions
    // in metadata). Screens dispatch "screen-actions-changed" when the
    // set of relevant actions changes (edit mode toggled, selection
    // changed, data loaded). See DESIGN.md § Screen actions.
    this._onScreenActionsChanged = () => this.requestUpdate();
    this.addEventListener(
      "screen-actions-changed",
      this._onScreenActionsChanged,
    );
    // Tree rows dispatch taxon-action {id, action} when the curator
    // hits one of the row-level buttons (edit / new-child /
    // new-sister / delete). If the target row isn't already selected
    // we select it first, then wait for SfgaDetail's load to
    // complete before invoking the action so the action fires
    // against fully-loaded state. See DESIGN.md § List-row actions.
    this._onTaxonAction = (e) => this._handleTaxonAction(e.detail);
    this.addEventListener("taxon-action", this._onTaxonAction);
    // Empty-archive affordance: SfgaTree dispatches taxon-create-root
    // when the curator clicks "Add first taxon" on a tree that has
    // no root taxa yet. Route into SfgaDetail's create pane with an
    // empty parent (see startCreateRoot).
    this._onTaxonCreateRoot = () => this._handleTaxonCreateRoot();
    this.addEventListener("taxon-create-root", this._onTaxonCreateRoot);
    // URL fragment routing. Back/forward buttons update the hash;
    // hashchange feeds it back into our state. Initial hash is read
    // before data loads so the tree reveal fires with the right id.
    this._onHashChange = this._onHashChange.bind(this);
    window.addEventListener("hashchange", this._onHashChange);
    this._syncFromHash();
    // Guard against losing unsaved metadata edits to a tab close,
    // reload, or navigation. Browsers show a generic confirm dialog
    // when preventDefault + returnValue is set; the specific text
    // is not shown to the user (spec: sanitized to a stock message).
    this._onBeforeUnload = (e) => {
      if (this._hasUnsavedMetadata()) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", this._onBeforeUnload);
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
    if (this._onBeforeUnload) {
      window.removeEventListener("beforeunload", this._onBeforeUnload);
    }
    if (this._onScreenActionsChanged) {
      this.removeEventListener(
        "screen-actions-changed",
        this._onScreenActionsChanged,
      );
    }
    if (this._onTaxonAction) {
      this.removeEventListener("taxon-action", this._onTaxonAction);
    }
    if (this._onTaxonCreateRoot) {
      this.removeEventListener("taxon-create-root", this._onTaxonCreateRoot);
    }
    super.disconnectedCallback();
  }

  // _skipToMain moves keyboard focus into the <main> region, bypassing
  // the header and sidebar. Prefers the first focusable element inside
  // <main> — on the taxa screen this lands on the tree <ul> (which is
  // where SfgaTree auto-focuses on mount anyway), on other screens it
  // lands on whichever interactive element renders first. Falls back
  // to focusing <main> itself when no focusable descendant exists.
  _skipToMain(e) {
    e.preventDefault();
    const main = this.renderRoot?.querySelector("main");
    if (!main) return;
    const focusable = main.querySelector(
      'button:not([disabled]), a[href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    if (focusable) focusable.focus();
    else main.focus();
  }

  // _handleTaxonAction processes a taxon-action dispatched by a tree
  // row. If the target isn't the currently-selected taxon, updates
  // selection so SfgaDetail loads the target, then awaits the load
  // via performAction() (which awaits its own _loadPromise) before
  // running the action. Single-click UX for the tree-row buttons on
  // desktop; the same code path handles the tap-then-button flow on
  // touch (where the row was already selected by the tap).
  async _handleTaxonAction({ id, action }) {
    if (!id || !action) return;
    if (this.selectedId !== id) {
      this.selectedId = id;
      await this.updateComplete;
    }
    const detail = this.renderRoot?.querySelector("sfga-detail");
    if (!detail || typeof detail.performAction !== "function") return;
    await detail.performAction(action);
  }

  // _handleTaxonCreateRoot opens the create pane for a new root-level
  // taxon. Fired by SfgaTree's "Add first taxon" affordance when the
  // archive is empty. Guarded on the archive being writable so the
  // click no-ops silently on read-only archives (the button shouldn't
  // render there either, but this belt-and-suspenders keeps the
  // affordance from doing anything harmful if it does).
  async _handleTaxonCreateRoot() {
    if (this.screen !== "taxa") return;
    if (!this.archive || this.archive.read_only) return;
    await this.updateComplete;
    const detail = this.renderRoot?.querySelector("sfga-detail");
    if (!detail || typeof detail.startCreateRoot !== "function") return;
    await detail.startCreateRoot();
  }

  // _renderScreenActions asks the currently-mounted screen component
  // for the buttons it wants projected into the app header. Screens
  // implement renderHeaderActions() returning an html template or "";
  // they dispatch "screen-actions-changed" (see connectedCallback)
  // when the output would change so this method's result stays fresh.
  _renderScreenActions() {
    const selector = {
      metadata: "sfga-metadata",
      taxa: "sfga-detail",
    }[this.screen];
    if (!selector) return "";
    const el = this.renderRoot?.querySelector(selector);
    return el?.renderHeaderActions?.() ?? "";
  }

  // _hasUnsavedMetadata reports whether the mounted <sfga-metadata>
  // has an in-progress edit. Returns false when the metadata screen
  // isn't rendered (component absent from the shadow tree).
  _hasUnsavedMetadata() {
    const md = this.renderRoot?.querySelector("sfga-metadata");
    return !!md && typeof md.hasUnsavedChanges === "function" &&
      md.hasUnsavedChanges();
  }

  // _requestScreenChange gates every screen switch through the
  // unsaved-edits guard. Called from both the sidebar click handler
  // and the alt+letter global shortcut so no navigation path can
  // bypass the confirm. Returns true iff the switch proceeded.
  async _requestScreenChange(target) {
    if (target === this.screen) return true;
    if (this.screen === "metadata" && this._hasUnsavedMetadata()) {
      const md = this.renderRoot?.querySelector("sfga-metadata");
      const choice = await confirmDirty({
        heading: "Unsaved metadata edits",
        message:
          "You have unsaved changes to metadata. Save them, discard them, or keep editing?",
        canSave: true,
      });
      if (choice === "cancel") return false;
      if (choice === "save") {
        if (!md || !(await md.save())) return false;
      } else if (choice === "discard") {
        if (md && typeof md.discardChanges === "function") md.discardChanges();
      }
    }
    this.screen = target;
    return true;
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
    if (
      changed.has("screen") ||
      changed.has("selectedId") ||
      changed.has("_pendingReferenceId") ||
      changed.has("_pendingWorkspaceTab")
    ) {
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
  //   #/                                → taxa screen, no selection
  //   #/taxon/{id}                      → taxa screen, taxon revealed + selected
  //   #/metadata                        → metadata screen
  //   #/references                      → references screen, no selection
  //   #/reference/{id}                  → references screen with ref selected
  //   #/reference/{id}/{tab}            → references screen with ref selected
  //                                       and a specific workspace tab active
  //                                       (bibliography / digitize / assertions)
  // Unknown paths fall back to the taxa screen — safer than leaving
  // the UI in a broken state when someone shares an old-schema link.
  _syncFromHash() {
    const raw = location.hash.startsWith("#") ? location.hash.slice(1) : location.hash;
    const parts = raw.split("/").filter(Boolean);
    this._syncingFromHash = true;
    if (parts.length === 0) {
      this.screen = "taxa";
    } else if (parts[0] === "taxon") {
      this.screen = "taxa";
      this.selectedId = parts[1] || "";
    } else if (parts[0] === "reference") {
      this.screen = "references";
      this._pendingReferenceId = parts[1] || "";
      // Third segment is the workspace tab. Only accept known
      // values so a stale link can't put the UI in a broken state.
      const tab = parts[2] || "";
      this._pendingWorkspaceTab =
        tab === "bibliography" || tab === "digitize" || tab === "assertions"
          ? tab
          : "";
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
    } else if (this.screen === "references") {
      if (!this._pendingReferenceId) {
        wanted = `#/references`;
      } else {
        // Include the workspace tab in the URL when the curator
        // has explicitly landed on one; omit for the default so
        // shared links stay clean. Empty _pendingWorkspaceTab
        // means "let the component pick its default."
        wanted = this._pendingWorkspaceTab
          ? `#/reference/${this._pendingReferenceId}/${this._pendingWorkspaceTab}`
          : `#/reference/${this._pendingReferenceId}`;
      }
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
        // WUI-only detail toggle: "a" flips the Standardized
        // Authorship render on the Nomenclatural History section.
        // Not in the shared keymap because the TUI hasn't picked up
        // the section yet — will graduate once the TUI has a
        // Nomen History equivalent. Bare letter, so gated by the
        // enclosing "not typing" check.
        if (this.selectedId && !e.altKey && !e.ctrlKey && !e.metaKey && e.key === "a") {
          const detail = this.renderRoot.querySelector("sfga-detail");
          if (detail && typeof detail.toggleStandardizedAuthorship === "function") {
            e.preventDefault();
            detail.toggleStandardizedAuthorship();
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
      case "view-references":
      case "view-issues": {
        const wanted = action.slice("view-".length); // "taxa" / "metadata" / "references" / "issues"
        e.preventDefault();
        this._requestScreenChange(wanted);
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

  // Row click from the Issues screen. Route to the natural edit
  // surface for the flagged record:
  //   - taxon-linked issues open the Taxa screen and reveal the row
  //   - reference-scoped issues open the References screen with the
  //     flagged reference pre-selected
  //   - metadata + role-table issues (creator/contact/contributor/
  //     editor/publisher) open the Metadata screen, where those
  //     agent lists are edited
  // Unknown tables with no taxon hint are silently ignored — the
  // Issues row was already rendered dim to signal that.
  async _onIssueNavigate(e) {
    const detail = e.detail || {};
    const roleTables = new Set([
      "metadata",
      "creator",
      "contact",
      "contributor",
      "editor",
      "publisher",
    ]);
    if (roleTables.has(detail.table)) {
      this.screen = "metadata";
      return;
    }
    if (detail.table === "reference") {
      this._pendingReferenceId = detail.record_id || "";
      this.screen = "references";
      return;
    }
    if (detail.taxon_id) {
      this.screen = "taxa";
      this.selectedId = detail.taxon_id;
      await this._revealInTree(detail.taxon_id);
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
    // Falls back to the literal "hive" for legacy archives without a
    // seeded metadata row. The schema version renders inline after the
    // title in a dimmed mono style; read-only mode appends a marker so
    // curators notice they're viewing a locked archive without needing
    // a separate strip. The DB filename is available via the title
    // tooltip.
    const title = this.metadata?.title || "hive";
    const schemaText = this.archive
      ? this.archive.read_only
        ? `(${this.archive.schema_version}, read-only)`
        : `(${this.archive.schema_version})`
      : "";
    const titleTooltip = this.archive ? this.archive.path : "";
    return html`
      <a class="skip-link" href="#main" @click=${(e) => this._skipToMain(e)}
        >Skip to main content</a
      >
      <header>
        <div class="header-left"></div>
        <div class="title" title=${titleTooltip}>
          ${title}${schemaText
            ? html`<span class="schema">${schemaText}</span>`
            : ""}
        </div>
        <div class="header-buttons">
          ${this._renderScreenActions()}
          <button
            class="icon-btn subtle"
            @click=${() => (this.helpOpen = true)}
            title="keyboard shortcuts  (?)"
            aria-label="keyboard shortcuts"
          >
            ${renderIcon("help-circle", 18)}
          </button>
          <button
            class="icon-btn subtle"
            @click=${() => this._cycleTheme()}
            title="theme: ${this.theme}  (click to cycle)"
            aria-label="theme: ${this.theme}"
          >
            ${renderIcon(this._themeIcon(), 18)}
          </button>
        </div>
      </header>
      <main id="main" tabindex="-1">
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
                @click=${() => this._requestScreenChange(v.id)}
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
            <sfga-references
              .selectId=${this._pendingReferenceId}
              .workspaceTab=${this._pendingWorkspaceTab}
              @reference-selected=${(e) => {
                // Track the current selection in the shell so the
                // hash can reflect it — reloading / bookmarking /
                // sharing a URL to a specific reference stays lossless.
                const id = (e && e.detail && e.detail.id) || "";
                if (this._pendingReferenceId !== id) {
                  this._pendingReferenceId = id;
                }
              }}
              @workspace-tab-changed=${(e) => {
                // Same story for the workspace tab — reload lands on
                // whichever pane the curator was working in.
                const tab = (e && e.detail && e.detail.tab) || "";
                if (this._pendingWorkspaceTab !== tab) {
                  this._pendingWorkspaceTab = tab;
                }
              }}
            ></sfga-references>
          </div>
        `;
      case "issues":
        return html`
          <div class="screen issues">
            <sfga-issues
              @issue-navigate=${(e) => this._onIssueNavigate(e)}
            ></sfga-issues>
          </div>
        `;
      default:
        return html`
          <div class="screen taxa">
            <aside>
              <sfga-combobox
                class="search"
                min-search-chars="2"
                placeholder="Search taxa…"
                .source=${this._taxonSearchSource}
                .resolver=${taxonResolver}
                .filters=${[
                  {
                    key: "partial",
                    label: "Partial",
                    value: this._searchFilters.mode === "partial",
                    description:
                      "Match epithets in any position (e.g., 'rusci' finds Ceroplastes rusci)",
                  },
                  {
                    key: "fuzzy",
                    label: "Fuzzy",
                    value: this._searchFilters.mode === "fuzzy",
                    description:
                      "Tolerate typos (e.g., 'Cerpolastes' finds Ceroplastes)",
                  },
                  {
                    key: "accepted",
                    label: "Accepted only",
                    value: !this._searchFilters.synonyms,
                    description:
                      "Skip synonym matches; only return accepted-name matches",
                  },
                ]}
                @pick=${(e) => this._onSearchPick(e)}
                @filter-change=${(e) => this._onSearchFilterChange(e)}
              ></sfga-combobox>
              <div class="tree-scroll">
                ${this.error ? html`<div class="error" role="alert">${this.error}</div>` : ""}
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
                @taxon-selected=${(e) => this._onSelected(e)}
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
    // _loaded flips true after the first reloadRoots resolves. Gates
    // the empty-state render so a slow initial fetch (COL-scale
    // archives take a moment for the roots query) doesn't flash the
    // "Add first taxon" affordance before the real roots arrive.
    _loaded: { state: true },
  };

  static styles = [
    buttonStyles,
    css`
      :host {
        display: block;
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
        /* Fixed row height so the sticky-ancestor waterfall's top
           offsets stack cleanly (each depth's ancestor sticks at
           row-height * depth). Value is in px (not rem) so every
           calc(row-height * depth) is a whole-pixel offset — a rem
           value at 14px body font produced 24.5px, which the browser
           rounded inconsistently and left 1px slivers of scrolled
           content bleeding through between stacked ancestor rows. */
        --tree-row-height: 24px;
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
        min-height: var(--tree-row-height);
        padding: 0 var(--sp-1);
        cursor: pointer;
        white-space: nowrap;
        color: var(--fg);
        display: flex;
        align-items: center;
        gap: var(--sp-1);
      }
      li:hover {
        background: color-mix(in oklab, var(--fg) 8%, transparent);
      }
      li.selected {
        background: var(--accent);
        color: var(--accent-fg);
      }
      /* Sticky ancestor waterfall (see DESIGN.md § Sticky ancestor
         rows in the tree pane). Only expanded parents stick — leaves
         and collapsed parents scroll normally so the tree doesn't
         "shimmer" as every row briefly pins itself on the way out.
         Each stuck row sits at row-height × depth from the top of
         the scroll region, so Animalia (depth 0) pins first at 0,
         Chordata (depth 1) below it at 1×row-height, etc. Background
         is opaque so scrolled content below doesn't bleed through.
         z-index inverted with depth keeps shallower ancestors on top
         when browsers stack overlapping stickies. Base pulled from
         --z-tree-sticky-base in styles.css so sticky rows always
         stack BELOW --z-popover (combobox dropdowns) — otherwise
         the tree search suggestions would render behind the sticky
         waterfall and be un-clickable. */
      li[data-sticky] {
        position: sticky;
        top: calc(var(--tree-row-height) * var(--depth, 0));
        background: var(--bg);
        /* Clamp to a positive value — depths beyond
           --z-tree-sticky-base would give a negative z-index, which
           can push the element behind the scroll container's stacking
           context in some browsers and make deep ancestor rows
           visually vanish. All sticky rows land in a small positive
           band (0 to base); DOM order breaks ties within it. */
        z-index: max(1, calc(var(--z-tree-sticky-base) - var(--depth, 0)));
      }
      /* Sticky rows need opaque backgrounds on state changes too —
         otherwise the transparent-mix hover / hover-over-scrolled-
         content combination bleeds through and looks broken. */
      li[data-sticky]:hover {
        background: color-mix(in oklab, var(--fg) 8%, var(--bg));
      }
      li[data-sticky].selected {
        background: var(--accent);
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
        flex: 0 0 auto;
      }
      li.selected .caret {
        color: var(--accent-fg);
      }
      /* Row label eats remaining width so the actions strip sits at
         the right edge; truncates with ellipsis when tight (curator
         hovers the row for the full name via title tooltip). */
      .label {
        flex: 1 1 auto;
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
      }
      /* List-row actions per DESIGN.md § List-row actions. Hidden on
         inactive rows by default; row hover / focus-within reveals
         them (allocates space via visibility so no layout shift).
         Active row (li.selected) always shows them so touch users
         can act without a hover state. Icons override the parent
         li's accent-fg text color when the row is selected so the
         icons remain legible on the accent background. */
      .row-actions {
        display: inline-flex;
        gap: 0;
        visibility: hidden;
        flex: 0 0 auto;
      }
      li:hover .row-actions,
      li:focus-within .row-actions,
      li.selected .row-actions {
        visibility: visible;
      }
      li.selected .row-actions button.icon-btn.subtle {
        color: var(--accent-fg);
      }
      li.selected .row-actions button.icon-btn.subtle:hover:not(:disabled) {
        background: color-mix(in oklab, var(--accent-fg) 18%, transparent);
        color: var(--accent-fg);
      }
      /* On the selected (accent-filled) row, the transparent-mix
         danger tint that works on inactive rows reads as purplish —
         the accent blue bleeds through. Switch to a solid --error
         fill (matching .danger-primary elsewhere) so the trash icon
         reads clearly as destructive on hover. */
      li.selected .row-actions button.icon-btn.subtle.danger:hover:not(:disabled) {
        background: var(--error);
        color: var(--bg);
      }
      .error {
        color: var(--error);
      }
      /* Empty-archive state: shown when the tree loads with zero
         root taxa. Centered CTA button that dispatches
         taxon-create-root; the shell opens SfgaDetail's create pane
         with an empty parent. */
      .empty-tree {
        display: flex;
        flex-direction: column;
        align-items: center;
        gap: var(--sp-3);
        padding: var(--sp-5) var(--sp-4);
        color: var(--dim);
        font-family: var(--font-body);
        font-size: var(--fs-md);
        text-align: center;
      }
      .empty-tree p {
        margin: 0;
      }
      button.add-first {
        background: var(--accent);
        color: var(--accent-fg);
        border-color: var(--accent);
      }
    `,
  ];

  constructor() {
    super();
    this.nodes = [];
    this.selectedId = "";
    this.error = "";
    this._loaded = false;
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
    } finally {
      this._loaded = true;
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
      // Look up by id, not by reference. Between the click that
      // captured `node` and this async continuation, this.nodes may
      // have been rebuilt (e.g., by a reveal fired from a search
      // pick that ran in the same tick). Reference identity would
      // silently fail — id lookup finds the row wherever it is now.
      const idx = this.nodes.findIndex((n) => !n.sentinel && n.id === node.id);
      if (idx < 0) return;
      const current = this.nodes[idx];
      const children = this._pageToNodes(page, current.depth + 1, current.id);
      const newNodes = [...this.nodes];
      newNodes[idx] = { ...current, expanded: true };
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
    // Id lookup (same rationale as _expand) so a stale reference
    // from a re-rendered array still resolves.
    const idx = this.nodes.findIndex((n) => !n.sentinel && n.id === node.id);
    if (idx < 0) return;
    const current = this.nodes[idx];
    let end = idx + 1;
    while (end < this.nodes.length && this.nodes[end].depth > current.depth) {
      end++;
    }
    const newNodes = [...this.nodes];
    newNodes.splice(idx + 1, end - idx - 1);
    newNodes[idx] = { ...current, expanded: false };
    this.nodes = newNodes;
  }

  // reveal loads the classification chain for id and expands the tree
  // along it so the target appears in its correct location. Called
  // after a move (so the curator doesn't have to hunt for the reparented
  // taxon) and after a search pick.
  //
  // Two round trips instead of O(depth):
  //   1. GET /api/taxon/{id}/classification — the full ancestor chain
  //      in one shot (root first, target last).
  //   2. GET /api/taxon/roots + GET /api/taxon/{ancestor}/children in
  //      parallel via Promise.all — every level's sibling page comes
  //      back concurrently.
  // The old path issued 1 (ancestors) + 1 (roots) + N (per-ancestor
  // children) sequential requests; on a 12-deep taxon in COL scale that
  // was 14 sequential round trips, observed as multi-second lag.
  async reveal(id) {
    if (!id) return;
    try {
      const { items: chain } = await api.taxon.classification(id);
      const ancestorIDs = (chain || []).slice(0, -1).map((h) => h.id);
      // Fan out roots + every ancestor's children page concurrently.
      // Each call is independent — we only need the parent id to fetch
      // its children — so parallelism is safe and drops wall time to
      // roughly the slowest single request.
      const opts = { limit: this._pageSize };
      const [rootsPage, ...childrenPages] = await Promise.all([
        api.taxon.roots(opts),
        ...ancestorIDs.map((aid) => api.taxon.children(aid, opts)),
      ]);
      // Rebuild the tree top-down. Start from roots, then splice each
      // ancestor's children page in below its parent row and mark the
      // parent expanded. Sequential in memory (no I/O) so the flat-list
      // invariant holds after each splice.
      let nodes = this._pageToNodes(rootsPage, 0, "");
      for (let i = 0; i < ancestorIDs.length; i++) {
        const ancestorID = ancestorIDs[i];
        const idx = nodes.findIndex((n) => !n.sentinel && n.id === ancestorID);
        if (idx < 0) continue;
        const parent = nodes[idx];
        const children = this._pageToNodes(
          childrenPages[i],
          parent.depth + 1,
          parent.id,
        );
        nodes = [
          ...nodes.slice(0, idx),
          { ...parent, expanded: true },
          ...children,
          ...nodes.slice(idx + 1),
        ];
      }
      this.nodes = nodes;
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
    if (this.error) return html`<div class="error" role="alert">${this.error}</div>`;
    // Empty-archive affordance: when the tree loads with zero root
    // taxa there's nothing to select, nothing to "add sister" from,
    // and no header pencil (which requires a selected taxon). Give
    // the curator a direct entry point. Dispatches taxon-create-root
    // for the shell to route into SfgaDetail's create pane; hidden
    // when the archive is read-only (button emits regardless — the
    // shell suppresses the action for viewers).
    //
    // Gated on _loaded so a slow initial roots-query (COL-scale
    // archives take a moment) doesn't flash the "Add first taxon"
    // affordance before the real roots arrive. Show a loading
    // placeholder in the interim instead.
    if (this.nodes.length === 0) {
      if (!this._loaded) {
        return html`<div class="empty-tree" role="status"><p>Loading…</p></div>`;
      }
      return html`
        <div class="empty-tree" role="status">
          <p>This archive has no taxa yet.</p>
          <button
            class="add-first"
            @click=${() => this._addFirstTaxon()}
          >
            Add first taxon
          </button>
        </div>
      `;
    }
    // tabindex="0" makes the tree Tab-reachable and a valid focus target.
    // mousedown promotes focus to the <ul> before the <li> click fires,
    // so a click into the tree lands focus here (browsers don't focus
    // non-input elements on click by default). The pane earns focus
    // explicitly (PARITY.md § Focus semantics) so keyboard shortcuts
    // don't fire while the curator is typing into an unrelated input.
    // role="tree" + role="treeitem" on children announces the widget
    // as a taxonomic tree to screen readers; aria-activedescendant
    // points at the currently selected row so screen readers know
    // which item has focus without moving DOM focus per keystroke.
    const activeId = this.selectedId
      ? `tree-item-${this.selectedId}`
      : undefined;
    return html`
      <ul
        role="tree"
        aria-label="Taxa"
        tabindex="0"
        aria-activedescendant=${activeId ?? nothing}
        @mousedown=${(e) => e.currentTarget.focus()}
        @keydown=${(e) => this._onKeyDown(e)}
      >
        ${this.nodes.map((n) => this._renderNode(n))}
      </ul>
    `;
  }

  _addFirstTaxon() {
    this.dispatchEvent(
      new CustomEvent("taxon-create-root", {
        bubbles: true,
        composed: true,
      }),
    );
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
        : `⋯ Load more`;
      // Sentinels are load-more affordances rather than tree items;
      // role="button" announces them accurately to screen readers.
      return html`
        <li
          role="button"
          class="sentinel"
          aria-label=${label}
          style="padding-left: ${0.35 + n.depth * 1.1}rem"
          @click=${() => this._loadMore(n)}
        >
          <span class="caret"> </span>
          <span class="label">${label}</span>
        </li>
      `;
    }
    const selected = n.id === this.selectedId;
    // aria-level is 1-indexed per the ARIA tree pattern; node.depth
    // is 0-indexed internally. aria-expanded is only meaningful on
    // parents — leaves omit it so screen readers don't announce an
    // expandable state that doesn't exist.
    //
    // data-sticky opts this row into the sticky-ancestor waterfall
    // (see :host CSS). Only expanded parents opt in — a stuck leaf
    // would flash-pin at the top as it scrolls out, which is noise
    // rather than signal. --depth drives the sticky top offset so
    // the waterfall stacks in classification order.
    const isAncestor = n.has_children && n.expanded;
    return html`
      <li
        id="tree-item-${n.id}"
        role="treeitem"
        class=${selected ? "selected" : ""}
        aria-selected=${selected ? "true" : "false"}
        aria-level=${n.depth + 1}
        aria-expanded=${n.has_children ? (n.expanded ? "true" : "false") : nothing}
        ?data-sticky=${isAncestor}
        style="padding-left: ${0.35 + n.depth * 1.1}rem; --depth: ${n.depth}"
        @click=${() => {
          this._select(n);
          this._toggle(n);
        }}
      >
        <span class="caret" aria-hidden="true">
          ${n.has_children ? (n.expanded ? "▼" : "▶") : " "}
        </span>
        <span class="label">${renderLabel(n.label, n.name)}</span>
        ${this._renderRowActions(n)}
      </li>
    `;
  }

  // _renderRowActions produces the per-row action strip (edit /
  // new-child / new-sister / delete). CSS hides it on inactive rows;
  // hover, focus-within, or selection reveals it. Buttons dispatch
  // taxon-action {id, action}; SfgaApp handles the select-then-act
  // flow. Click handlers stopPropagation so the row's own click
  // (select + expand) doesn't fire when a button is used. See
  // DESIGN.md § List-row actions.
  _renderRowActions(n) {
    const fire = (action, e) => {
      e.stopPropagation();
      this.dispatchEvent(
        new CustomEvent("taxon-action", {
          detail: { id: n.id, action },
          bubbles: true,
          composed: true,
        }),
      );
    };
    return html`
      <span class="row-actions">
        <button
          class="icon-btn subtle"
          title="edit"
          aria-label="edit"
          @click=${(e) => fire("edit", e)}
        >
          ${renderIcon("pencil", 14)}
        </button>
        <button
          class="icon-btn subtle"
          title="new child"
          aria-label="new child"
          @click=${(e) => fire("new-child", e)}
        >
          ${renderIcon("tree-child-plus", 14)}
        </button>
        <button
          class="icon-btn subtle"
          title="new sister"
          aria-label="new sister"
          @click=${(e) => fire("new-sister", e)}
        >
          ${renderIcon("tree-sister-plus", 14)}
        </button>
        <button
          class="icon-btn subtle danger"
          title="delete"
          aria-label="delete"
          @click=${(e) => fire("delete", e)}
        >
          ${renderIcon("trash-2", 14)}
        </button>
      </span>
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
    // Nomenclatural history from GET /api/taxon/{id}/nomenclatural-history.
    // Multi-cluster basionym-anchored projection — one cluster per
    // basionym family; the accepted cluster contains the accepted name,
    // its basionym, and every recombination sharing it. Backs the
    // Nomenclatural history section on the detail page.
    _nomenHistory: { state: true },
    // Vernacular names from GET /api/taxon/{id}/vernaculars.
    // Preferred-first, then by language, then name. Backs the
    // Vernacular names section on the detail page.
    _vernaculars: { state: true },
    // Modal state for the vernacular add / edit form. Null when the
    // modal is closed; an object shape {mode: "create" | "edit",
    // draft: {…}, id?: string, error?: string} when open. The draft
    // holds the in-progress field values so edits survive re-renders
    // and can be committed via PATCH / POST on save.
    _vernacularForm: { state: true },
    // Distribution list + modal state — same pattern as the
    // vernacular pair. Backend orders by gazetteer then area.
    _distributions: { state: true },
    _distributionForm: { state: true },
    // Species interaction list + modal state — same pattern as the
    // vernacular / distribution pair. Backend returns rows where this
    // taxon is the SUBJECT, ordered by type then related-taxon name.
    _speciesInteractions: { state: true },
    _speciesInteractionForm: { state: true },
    // Modal state for the synonym-delete flow. Null when closed;
    // {phase, synonymId, nameId, label, deps, cascade, confirmText,
    // busy, error} while open. See _renderSynonymDeleteModal.
    _synonymDelete: { state: true },
    // "Standardized authorship" toggle for the Nomenclatural History
    // section. When true, each row renders the hybrid form
    //   <canonical> (basionym_author, basionym_year) combination_author, combination_year
    // instead of the code-conventional pre-formatted Authorship string.
    // Persisted to localStorage as "hive-standardized-authorship" so
    // curators' preference carries across sessions. See
    // DESIGN.md § Standardized authorship.
    _standardizedAuthorship: { state: true },
    // Classification chain from GET /api/taxon/{id}/classification —
    // root-down list including the taxon itself as the last entry.
    // Backs the breadcrumb strip above the taxon heading.
    _classification: { state: true },
    _error: { state: true },
    _loading: { state: true },
    // Create-child pane state. Single-page form: scientific-name input
    // at the top of the name fieldset, Enter or blur re-parses via
    // POST /api/name/parse and re-fills the atomized fields below.
    // Curator-authored fields (reference, remarks, synonym_status,
    // basionym_name_id) survive re-parse untouched.
    _creating: { state: true },
    _createDraft: { state: true },
    _createBusy: { state: true },
    _createError: { state: true },
    // Guards the re-parse trigger so blur / repeat-Enter on an
    // unchanged scientific-name string doesn't fire redundant parses.
    // Cleared to "" on every _openCreate* so the first Enter/blur
    // always parses.
    _createLastParsedVerbatim: { state: true },
    // gnparser's parse_quality (0-4) for the current draft scientific
    // name. Number when a parse has completed, null before first parse
    // or when the current draft's verbatim has been edited past the
    // last-parsed value. Surfaced as an inline ✓/⚠/✗ glyph next to
    // the scientific-name input.
    _createParseQuality: { state: true },
    // Unparsed tail from the most recent parse of the draft's sci-name.
    //   null = no parse has run yet in this session
    //   ""   = clean parse (no tail)
    //   "…"  = parser rejected this trailing text
    // Drives the top-of-pane warning banner. Never persisted — the
    // backend re-derives the tail via the hive.parse_tail validator
    // and persists it as a __gsvalidator_results row so edit-mode
    // opens on old badly-parsed rows still show the diagnostic.
    _createParseTail: { state: true },
    // Persisted validation issues on the name being edited. Populated
    // by _openEditTaxon (parallel with the taxon load); empty in
    // create mode. Read by _renderCreateForm to surface the persisted
    // parse-tail issue when no live parse has run yet.
    _editingNameIssues: { state: true },
    // Progressive disclosure toggle inside the name fieldset: collapsed
    // hides the atomized breakdown (uninomial / genus / … + atomized
    // authorship pairs); expanded reveals them so curators can verify
    // or override the parse. Parsing runs regardless — the toggle only
    // affects visibility. gsvalidator's parse-mismatch rule catches the
    // poorly-parsed cases either way.
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
    // When set, the create pane's Save writes to the "add synonym"
    // endpoint (POST /api/taxon/{X}/synonym) instead of POST /api/taxon.
    // The value is the accepted taxon id the new name will be
    // linked to as a synonym. Null = normal accepted-name create /
    // basionym flow.
    _creatingSynonymFor: { state: true },
    // Display name of the accepted taxon for the header row while
    // creating a synonym so the curator sees which taxon they're
    // adding a synonym of.
    _creatingSynonymForName: { state: true },
    // Inline original-combination (basionym) subform inside the create
    // pane. Null when collapsed / not in use; {draft: {…}} when the
    // curator clicked "Create new original combination…" and is
    // filling in the sibling name that will be BASIONYM-linked to the
    // primary. On save, both records + the name_relation land in one
    // transaction. See CLAUDE.md § Nomenclatural-code-neutral copy.
    _createBasionymInline: { state: true },
    // Guards the re-parse trigger inside the inline basionym subform.
    // Same shape as _createLastParsedVerbatim but scoped to the
    // basionym draft's scientific-name field. Cleared on inline-open
    // and on subform cancel.
    _createBasionymInlineLastParsedVerbatim: { state: true },
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
    // Progressive-disclosure delete flow (see DESIGN.md § List-row
    // actions / delete flow). _deletePreview is the async summary
    // fetched when the modal opens (null while loading); it carries
    // descendant + per-association counts. _deleteMode picks the
    // path when the taxon has children — "" (unset), "reparent"
    // (move children up one level, then leaf-delete), or "cascade"
    // (recursive delete). _deleteConfirmText holds the typed value
    // that must literally equal "DELETE" to enable the cascade
    // Delete button.
    _deletePreview: { state: true },
    _deleteMode: { state: true },
    _deleteConfirmText: { state: true },
    // Add-reference modal state. The modal is a self-contained component
    // (<sfga-add-reference-modal>) — this flag just toggles rendering,
    // and _pickedCreateReferenceLabel carries the label into the
    // combobox display without waiting on the resolver's second fetch.
    // _addingReferenceFor tracks which form context requested the
    // add-reference modal — "create" / "vernacular" / "distribution" /
    // "section" — so _onReferencePicked routes the picked id back
    // into the right draft. Empty string means the modal is closed.
    _addingReferenceFor: { state: true },
    _pickedCreateReferenceLabel: { state: true },
    // Edit-mode-of-unified-form state. Non-empty _editingTaxonID means
    // the create pane is open as an editor for an existing taxon: reads
    // are hydrated into _createDraft on open; on save, deltas against
    // _editingOriginalTaxon / _editingOriginalName are PATCHed with
    // If-Match. Empty means the pane is either closed or in create mode.
    _editingTaxonID: { state: true },
    _editingTaxonEtag: { state: true },
    _editingNameEtag: { state: true },
    _editingOriginalTaxon: { state: true },
    _editingOriginalName: { state: true },
    // Parent-move draft while editing. `null` means "no change requested";
    // "" means "move to archive root"; non-empty string means "reparent
    // to this taxon id." Handled separately from PATCH since parent
    // moves route through POST /api/taxon/{id}/move.
    _editingParentDraft: { state: true },
    _editingParentDraftName: { state: true },
    // Synonym-edit mode of the unified form. When _editingSynonymID is
    // non-empty, the pane opens as a name-editor for a synonym row:
    // taxon-side fields are hidden (a synonym has no independent
    // taxon), an accepted-taxon picker replaces the parent picker for
    // move-synonym flows, and submit routes PATCH /api/name/{id} +
    // optional POST /api/synonym/{id}/move. _editingAcceptedTaxonID
    // remembers the original attachment for change detection.
    _editingSynonymID: { state: true },
    _editingAcceptedTaxonID: { state: true },
    _editingAcceptedTaxonDraft: { state: true },
    _editingAcceptedTaxonDraftName: { state: true },
    // Name relations state — the outgoing name_relation rows for the
    // name currently being edited. Loaded on _openEditTaxon /
    // _openEditSynonym, refreshed after add / delete. _nameRelations
    // is the persisted set; _nameRelationDraft is the in-progress
    // blank row (progressive disclosure — a new blank row appears
    // once related_name_id + type are picked and the row commits).
    _nameRelations: { state: true },
    _nameRelationDraft: { state: true },
    _nameRelationBusy: { state: true },
    // Vocab editor state. When non-empty, holds the vocab name being
    // edited; sfga-vocab-editor mounts as a nested modal over
    // whatever else is open. See VOCAB_EDITOR_ADAPTERS.
    _vocabEditor: { state: true },
    // Reference-quick-fix modal state. Opens over the create/edit
    // taxon pane when a curator clicks the book/warning badge on a
    // reference picker. Scoped to the fields most likely to need
    // fixing (author, issued, title, doi, type); full-fidelity
    // editing lives in the dedicated References screen.
    // See feedback_no_side_quests + REFERENCE_PDF_PLAN.md.
    // Non-empty when the reference-edit modal is open; the modal
    // (sfga-add-reference-modal in edit mode) owns all the working
    // state internally, so the pane only needs to know which
    // reference is being edited.
    _editRefID: { state: true },
  };

  static styles = [severityChipStyles, buttonStyles, formFieldStyles, css`
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
    /* Classification breadcrumbs on the line below the heading's
       horizontal rule. Small, dim, single line; wraps only when the
       pane is narrower than the full path. Reads as "this taxon lives
       here" without competing with the scientific name for the eye's
       first landing spot. Links stay real anchors (href="#/taxon/{id}")
       so right-click / open-in-new-tab work — see DESIGN.md
       § Navigation and links. */
    .breadcrumbs {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      gap: var(--sp-1);
      margin-bottom: var(--sp-3);
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
      color: var(--dim);
    }
    .breadcrumbs .crumb {
      color: var(--accent);
      text-decoration: none;
    }
    .breadcrumbs .crumb:hover {
      text-decoration: underline;
    }
    .breadcrumbs .crumb-sep {
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
    /* Shared header pattern for every taxon-detail section
       (Nomenclatural history, References, All fields). Each section
       renders with an <hr /> above and a headline row that holds the
       section label on the left and an optional + button on the right.
       Sections that don't offer a direct add flow omit the button;
       the label + hr still render so the visual rhythm stays uniform.
       See DESIGN.md § Add affordance. */
    /* Hide a section's leading <hr /> when it lands directly after
       the page's top hr (i.e., breadcrumbs and warnings both empty
       for this taxon). Prevents a double horizontal line at the
       first-section boundary. */
    hr + section > hr:first-child {
      display: none;
    }
    .section-header {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: var(--sp-2);
      margin: 0 0 var(--sp-2) 0;
    }
    .section-header h3 {
      margin: 0;
      font-size: var(--fs-sm);
      color: var(--dim);
      font-weight: normal;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }
    .section-header .add {
      /* Nudge the button up so its centre line aligns with the
         heading's baseline. Icon buttons don't share the text
         baseline naturally in a flex row. */
      align-self: center;
    }
    /* Nomenclatural history — the signature section. Accepted name
       flush left; basionyms and other synonyms rendered as compact
       monospace rows underneath, glyph-then-label. Layout matches
       how synonymy is laid out in a printed monograph so curators
       who scan taxonomic literature recognise it on sight. See
       DESIGN.md § Nomenclatural history section. */
    section.nomen-history {
      margin-bottom: var(--sp-4);
    }
    section.nomen-history ul {
      list-style: none;
      margin: 0;
      padding: 0;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    section.nomen-history li {
      padding: 1px 0;
    }
    /* Toolbar strip between the section header and the ul — holds
       the Standardized authorship chip. Small right-aligned row so
       it doesn't compete visually with the section title. */
    section.nomen-history .nomen-toolbar {
      display: flex;
      justify-content: flex-end;
      margin-bottom: var(--sp-2);
    }
    section.nomen-history .nomen-toolbar button.chip {
      display: inline-flex;
      align-items: baseline;
      gap: var(--sp-1);
      padding: 2px var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-pill);
      background: var(--bg);
      color: var(--dim);
      font: inherit;
      font-family: var(--font-body);
      font-size: var(--fs-sm);
      cursor: pointer;
      transition: background var(--transition-fast),
        color var(--transition-fast), border-color var(--transition-fast);
    }
    section.nomen-history .nomen-toolbar button.chip.on {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    section.nomen-history .nomen-toolbar button.chip:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
    }
    section.nomen-history .nomen-toolbar button.chip .chip-check {
      display: inline-flex;
      width: 0.9em;
      justify-content: center;
    }
    /* Every row (accepted, basionym, recombs) uses the same indented
       three-column grid so glyphs align in a single column, labels
       start at the same character position, and hover-reveal actions
       dock to the right edge. Reads as a nested block in monospace,
       matching how synonymy is laid out in a printed monograph. */
    section.nomen-history li.history {
      display: grid;
      grid-template-columns: 2ch 1fr auto;
      align-items: baseline;
      padding-left: 1ch;
    }
    /* Nested row — a recombination within a cluster, hanging off the
       basionym above it. Extra 2ch shifts the glyph column another
       character in so the visual hierarchy reads as
       "cluster anchor → its recombs." */
    section.nomen-history li.history.nested {
      padding-left: 3ch;
    }
    section.nomen-history li.history .glyph {
      color: var(--dim);
    }
    /* Accepted row's ✓ takes accent color to distinguish "the current
       name" from the ≡ / = history glyphs above and below without
       adding weight (bold on scientific names competes with the
       italic species / genus rendering). */
    section.nomen-history li.accepted .glyph {
      color: var(--accent);
    }
    /* Vernacular names — compact monospace table. Per DESIGN.md
       § Per-data-type sections. Row hover reveals edit/delete via
       the same .row-actions pattern used elsewhere. */
    section.vernaculars {
      margin-top: var(--sp-4);
    }
    section.vernaculars table {
      width: 100%;
      border-collapse: collapse;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    section.vernaculars th {
      text-align: left;
      color: var(--dim);
      font-weight: normal;
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid var(--border);
    }
    section.vernaculars td {
      padding: var(--sp-1) var(--sp-2);
      vertical-align: baseline;
    }
    /* Preferred marker column stays narrow — either ✓ or empty. */
    section.vernaculars th.pref,
    section.vernaculars td.pref {
      width: 1.5em;
      text-align: center;
      color: var(--accent);
    }
    /* Language / country / area columns are short — cap them so the
       name column claims the remaining space. */
    section.vernaculars th.lang,
    section.vernaculars td.lang,
    section.vernaculars th.country,
    section.vernaculars td.country {
      width: 4em;
    }
    section.vernaculars td.region {
      color: var(--dim);
    }
    section.vernaculars th.actions,
    section.vernaculars td.actions {
      width: 4.5em;
      text-align: right;
    }
    /* Match the nomen-history row-actions convention: hidden until
       row hover / focus-within to keep the table quiet. */
    section.vernaculars td.actions .row-actions {
      display: inline-flex;
      gap: 0;
      visibility: hidden;
    }
    section.vernaculars tr:hover td.actions .row-actions,
    section.vernaculars tr:focus-within td.actions .row-actions {
      visibility: visible;
    }
    /* Warn icon on a row with open validation issues stays visible
       even when the row isn't hovered — it's a "hey this needs
       attention" signal, not a subtle affordance. Same treatment as
       the nomen-history warn icon. */
    section.vernaculars td.actions .row-actions .warn {
      /* Default is warn-amber; variant-sev-* below overrides for
         higher- or lower-severity records. Same shared-badge pattern
         as nomen-history. */
      visibility: visible;
      color: var(--sev-warn);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-error {
      color: var(--sev-error);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-warn {
      color: var(--sev-warn);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-info {
      color: var(--sev-info);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-debug {
      color: var(--dim);
    }
    section.vernaculars td.actions .row-actions .warn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-error:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-error) 18%, transparent);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-info:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-info) 18%, transparent);
    }
    section.vernaculars td.actions .row-actions .warn.variant-sev-debug:hover:not(:disabled) {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    /* Distribution table — same shape as vernaculars, minus the
       preferred column and with a wider area column since gazetteer
       + status enum ids are short. */
    section.distributions {
      margin-top: var(--sp-4);
    }
    section.distributions table {
      width: 100%;
      border-collapse: collapse;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    section.distributions th {
      text-align: left;
      color: var(--dim);
      font-weight: normal;
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid var(--border);
    }
    section.distributions td {
      padding: var(--sp-1) var(--sp-2);
      vertical-align: baseline;
    }
    section.distributions th.gaz,
    section.distributions td.gaz,
    section.distributions th.status,
    section.distributions td.status {
      width: 8em;
    }
    section.distributions th.actions,
    section.distributions td.actions {
      width: 4.5em;
      text-align: right;
    }
    /* Area-code appendage — the machine-readable code shown after
       the human label when both are populated. Dim + slightly smaller
       so the primary label reads first. */
    section.distributions td.area .area-code {
      color: var(--dim);
      font-size: var(--fs-xs);
      margin-left: var(--sp-1);
    }
    section.distributions td.actions .row-actions {
      display: inline-flex;
      gap: 0;
      visibility: hidden;
    }
    section.distributions tr:hover td.actions .row-actions,
    section.distributions tr:focus-within td.actions .row-actions {
      visibility: visible;
    }
    section.distributions td.actions .row-actions .warn {
      /* Default warn-amber with variant-sev-* overrides — same shared
         severity-badge pattern as nomen-history and vernaculars. */
      visibility: visible;
      color: var(--sev-warn);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-error {
      color: var(--sev-error);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-warn {
      color: var(--sev-warn);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-info {
      color: var(--sev-info);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-debug {
      color: var(--dim);
    }
    section.distributions td.actions .row-actions .warn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-error:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-error) 18%, transparent);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-info:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-info) 18%, transparent);
    }
    section.distributions td.actions .row-actions .warn.variant-sev-debug:hover:not(:disabled) {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    /* Distribution modal form matches the vernacular form conventions
       (label/input grid inherited from the shared form rule; toolbar
       spans both columns). */
    .modal-backdrop .modal:has(> .distribution-form) {
      min-width: var(--modal-md);
      max-width: var(--modal-lg);
    }
    .distribution-form .toolbar {
      grid-column: 1 / -1;
      justify-content: flex-end;
    }
    /* Species interactions table — same shape as distributions.
       Type column narrower (enum ids are short); related-taxon column
       gets the rest of the row. */
    section.species-interactions {
      margin-top: var(--sp-4);
    }
    section.species-interactions table {
      width: 100%;
      border-collapse: collapse;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    section.species-interactions th {
      text-align: left;
      color: var(--dim);
      font-weight: normal;
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid var(--border);
    }
    section.species-interactions td {
      padding: var(--sp-1) var(--sp-2);
      vertical-align: baseline;
    }
    section.species-interactions th.type,
    section.species-interactions td.type {
      width: 10em;
    }
    section.species-interactions th.actions,
    section.species-interactions td.actions {
      width: 4.5em;
      text-align: right;
    }
    /* Divergent-cite annotation — shown when the source cited the
       related taxon under a different name than what's stored. Dim +
       body font so it reads as annotation rather than identity. */
    section.species-interactions td.related .annotation {
      color: var(--dim);
      font-family: var(--font-body);
      font-size: var(--fs-xs);
      margin-left: var(--sp-1);
    }
    section.species-interactions td.actions .row-actions {
      display: inline-flex;
      gap: 0;
      visibility: hidden;
    }
    section.species-interactions tr:hover td.actions .row-actions,
    section.species-interactions tr:focus-within td.actions .row-actions {
      visibility: visible;
    }
    section.species-interactions td.actions .row-actions .warn {
      /* Same shared-badge pattern as vernaculars / distributions /
         nomen-history. Default warn-amber with variant-sev-* overrides. */
      visibility: visible;
      color: var(--sev-warn);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-error {
      color: var(--sev-error);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-warn {
      color: var(--sev-warn);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-info {
      color: var(--sev-info);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-debug {
      color: var(--dim);
    }
    section.species-interactions td.actions .row-actions .warn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-error:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-error) 18%, transparent);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-info:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-info) 18%, transparent);
    }
    section.species-interactions td.actions .row-actions .warn.variant-sev-debug:hover:not(:disabled) {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    /* Species-interaction modal form — same conventions as the
       vernacular / distribution forms. */
    .modal-backdrop .modal:has(> .species-interaction-form) {
      min-width: var(--modal-md);
      max-width: var(--modal-lg);
    }
    .species-interaction-form .toolbar {
      grid-column: 1 / -1;
      justify-content: flex-end;
    }
    /* Non-Latin name (transliteration present) gets a small dim
       transliteration underneath the primary name. Body-font so
       curators reading the scientific literature can tell it apart
       from the row's identity text. */
    section.vernaculars td.name .translit {
      display: block;
      color: var(--dim);
      font-family: var(--font-body);
      font-size: var(--fs-xs);
      margin-top: 1px;
    }
    /* Vernacular add/edit form inside its modal. Inherits the
       label/input two-column grid from the shared form rule so
       labels right-align in col 1 and inputs fill col 2 — matches
       every other hive form. Widens the modal past the confirm
       default so the reference-picker combobox has room to breathe.
       Rows that need to span both columns (checkbox + toolbar) claim
       grid-column 1 / -1 so they don't consume a single cell and
       shift every row after them. */
    .modal-backdrop .modal:has(> .vernacular-form) {
      min-width: var(--modal-md);
      max-width: var(--modal-lg);
    }
    .vernacular-form label.checkbox-row {
      grid-column: 1 / -1;
      text-align: left;
      color: var(--fg);
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
    }
    .vernacular-form .toolbar {
      grid-column: 1 / -1;
      justify-content: flex-end;
    }
    /* Synonym-delete modal — two-option cascade choice with a type-
       to-confirm gate on the destructive path. Widens past the
       confirm default so the two labeled radio options don't crush
       their hint text. */
    .modal-backdrop .modal.synonym-delete {
      min-width: var(--modal-md);
      max-width: var(--modal-md);
    }
    .modal.synonym-delete .cascade-choice {
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-2);
      margin: 0;
      display: grid;
      /* Override the shared fieldset rule (max-content 1fr) — we want
         the two radio options stacked, not laid out side-by-side. */
      grid-template-columns: 1fr;
      gap: var(--sp-2);
    }
    .modal.synonym-delete .cascade-choice legend {
      color: var(--dim);
      font-size: var(--fs-sm);
      padding: 0 var(--sp-1);
    }
    .modal.synonym-delete .cascade-choice label {
      display: grid;
      grid-template-columns: auto 1fr;
      gap: var(--sp-2);
      align-items: start;
      color: var(--fg);
      cursor: pointer;
    }
    .modal.synonym-delete .cascade-choice label:has(input:disabled) {
      cursor: not-allowed;
      opacity: 0.65;
    }
    .modal.synonym-delete .cascade-choice input[type="radio"] {
      margin-top: 0.25em;
    }
    .modal.synonym-delete .cascade-choice strong {
      display: block;
      font-weight: 600;
    }
    .modal.synonym-delete .cascade-choice .hint {
      display: block;
      color: var(--dim);
      font-size: var(--fs-sm);
      font-family: var(--font-body);
      margin-top: 2px;
    }
    .modal.synonym-delete .cascade-choice .hint.warn {
      color: var(--sev-warn);
    }
    .modal.synonym-delete .type-to-confirm {
      display: grid;
      grid-template-columns: auto 1fr;
      align-items: center;
      gap: var(--sp-2);
      color: var(--fg);
    }
    .modal.synonym-delete .type-to-confirm code {
      font-family: var(--font-mono);
      background: color-mix(in oklab, var(--error) 12%, var(--bg));
      padding: 0 var(--sp-1);
      border-radius: var(--radius-sm);
      color: var(--error);
    }
    .modal.synonym-delete .toolbar {
      justify-content: flex-end;
    }
    /* Name-editor modal — inherits the label/input two-column grid
       from the shared form rule. Widens past confirm-default so the
       combobox pickers have room. */
    .modal-backdrop .modal.name-editor {
      min-width: var(--modal-md);
      max-width: var(--modal-lg);
    }
    .name-editor-form .toolbar {
      grid-column: 1 / -1;
      justify-content: flex-end;
    }
    /* Per-row hover actions — pencil (edit name), warning (open name
       editor at issues), delete (remove synonym link). Hidden until
       hover / focus-within, using visibility:hidden (not display:none)
       so the grid reserves the space and the label column width
       doesn't shift when the mouse enters the row. Matches the tree
       pane's row-actions pattern. See DESIGN.md § List-row actions.
       Accepted rows don't get these — the accepted taxon has
       edit/delete in the app header already. */
    section.nomen-history li.history .row-actions {
      display: inline-flex;
      gap: 0;
      visibility: hidden;
      flex: 0 0 auto;
      align-self: center;
    }
    section.nomen-history li.history:hover .row-actions,
    section.nomen-history li.history:focus-within .row-actions {
      visibility: visible;
    }
    section.nomen-history li.history .row-actions .warn {
      /* Warn icon breaks the row-actions hide-until-hover rule — a
         row with open validation issues is a "please look at this"
         signal, not a peripheral affordance. Kept visible at rest
         with the severity color so scanning the list surfaces every
         row that needs attention. Default color is warn-amber; the
         variant-sev-* classes below override for higher- or lower-
         severity records (max severity computed server-side; see
         validationSeverityBadge on the WUI side). */
      visibility: visible;
      color: var(--sev-warn);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-error {
      color: var(--sev-error);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-warn {
      color: var(--sev-warn);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-info {
      color: var(--sev-info);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-debug {
      color: var(--dim);
    }
    section.nomen-history li.history .row-actions .warn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-error:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-error) 18%, transparent);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-info:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-info) 18%, transparent);
    }
    section.nomen-history li.history .row-actions .warn.variant-sev-debug:hover:not(:disabled) {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    /* Inline citation superscripts — small numbered link that points
       at the References section at page bottom. Number is dim-accented
       so it reads as annotation rather than content; the link keeps
       standard underline-on-hover so it's obviously interactive. See
       DESIGN.md § Numbered references section. */
    a.cite {
      color: var(--accent);
      text-decoration: none;
      margin-left: 0.2em;
      font-size: 0.75em;
      vertical-align: super;
      line-height: 0;
    }
    a.cite:hover {
      text-decoration: underline;
    }
    /* Numbered References section — final block on the taxon detail
       page. Renders only when at least one row on the page cited a
       reference. Compact rows with the number aligned on the left. */
    section.references {
      margin-top: var(--sp-4);
    }
    section.references ol {
      list-style: none;
      margin: 0;
      padding: 0;
      font-family: var(--font-body);
      font-size: var(--fs-sm);
    }
    section.references li {
      display: grid;
      grid-template-columns: 2.5em 1fr;
      align-items: baseline;
      padding: 2px 0;
    }
    section.references li .refnum {
      color: var(--dim);
      text-align: right;
      padding-right: var(--sp-2);
    }
    section.references li:target {
      /* Highlight the entry after a click-jump from a superscript so
         curators know which row they landed on. Fades naturally as
         they read on. */
      background: color-mix(in oklab, var(--accent) 12%, var(--bg));
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
    /* Fixed first-column width so the issue text lines up across
       rows regardless of the severity chip's natural width (Info /
       Warn / Error / Debug render at slightly different pill widths).
       Each <li> is its own grid, so auto-sizing here would size col 1
       per-row and stagger the text left-edge. 4.5rem accommodates the
       widest chip. justify-self: start on the chip keeps it at its
       natural content width instead of stretching to fill the track. */
    .warning-banner li {
      margin: 0.25rem 0;
      display: grid;
      grid-template-columns: 4.5rem 1fr;
      gap: 0.5rem;
      align-items: baseline;
    }
    .warning-banner li > .sev-chip {
      justify-self: start;
    }
    .warning-banner .warning-rule {
      font-weight: 600;
      color: var(--fg);
    }
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
    /* Button and form-field styling comes from the shared buttonStyles
       and formFieldStyles modules (see top of app.js). Local rules
       below only cover layout (toolbar), state overrides that don't
       belong in the shared vocabulary, or form-input width/box-sizing
       constraints the shared module doesn't yet cover. */
    .toolbar {
      display: flex;
      gap: 0.5rem;
      align-items: center;
      margin-top: 0.5rem;
    }
    .modal-backdrop {
      position: fixed;
      inset: 0;
      /* fg-tinted overlay so the underlying pane visibly dims in
         light mode (fg is dark) and lightens in dark mode (fg is
         light) without either mode getting too muddy. 18% keeps the
         data below legible while signaling the modal is the active
         surface. */
      background: color-mix(in oklab, var(--fg) 18%, transparent);
      display: grid;
      place-items: center;
      z-index: 10;
    }
    /* Nested-modal hide: any backdrop that isn't currently the topmost
       modal is hidden via visibility (DOM + Lit state preserved so
       draft values survive the trip). See openModal / isTopModal in
       the modal-stack helpers. */
    .modal-backdrop.is-covered {
      visibility: hidden;
    }
    .modal {
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-4) var(--sp-5);
      min-width: var(--modal-sm);
      max-width: var(--modal-md);
      display: grid;
      gap: var(--sp-2);
    }
    .modal h3 {
      margin: 0 0 var(--sp-1) 0;
      font-family: var(--font-body);
      font-size: var(--fs-lg);
    }
    /* Modal header row — h3 title on the left, close-x on the right.
       Used by every form-shaped modal in SfgaDetail (name editor,
       vernacular, distribution, synonym delete). Confirms via
       Escape / Cancel button too — the × is the pointer-user
       counterpart of Escape. */
    .modal .modal-header {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: var(--sp-2);
    }
    .modal .modal-header h3 {
      margin: 0;
    }
    .modal p {
      margin: 0;
      color: var(--dim);
      font-size: var(--fs-sm);
    }
    .modal .error {
      color: var(--error);
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    .modal .req {
      color: var(--error);
    }
    /* Delete-modal: slightly wider than the default confirm because
       the parent case shows radio options + a cascade summary + typed-
       DELETE input. sr-only hides the fieldset legend visually while
       leaving it available to screen readers as "Choose how to handle
       children". */
    .delete-modal {
      min-width: 28rem;
      max-width: 38rem;
    }
    .sr-only {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
      border: 0;
    }
    .delete-options {
      display: grid;
      gap: var(--sp-2);
    }
    .delete-options label {
      display: flex;
      gap: var(--sp-2);
      align-items: baseline;
      cursor: pointer;
      color: var(--fg);
      font-family: var(--font-body);
      font-size: var(--fs-md);
      text-align: left;
    }
    .cascade-summary {
      margin-left: var(--sp-4);
      padding: var(--sp-2) var(--sp-3);
      background: color-mix(in oklab, var(--error) 8%, var(--bg));
      border: 1px solid color-mix(in oklab, var(--error) 30%, var(--border));
      border-radius: var(--radius-md);
      color: var(--fg);
      font-size: var(--fs-sm);
    }
    .cascade-summary .cascade-heading {
      margin: 0 0 var(--sp-1) 0;
      color: var(--error);
      font-weight: 600;
    }
    .cascade-summary ul {
      margin: 0 0 var(--sp-2) 0;
      padding-left: var(--sp-4);
    }
    .cascade-confirm {
      display: flex;
      gap: var(--sp-2);
      align-items: center;
      margin-top: var(--sp-2);
      font-family: var(--font-body);
    }
    .cascade-confirm input {
      flex: 1;
      min-width: 0;
      font-family: var(--font-mono);
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
      /* minmax(0, ...) on both tracks lets rows shrink below their
         content's intrinsic size — otherwise a long label like
         "Subsequent nomenclatural act citation" locks the label column
         to its full width and overflows the pane horizontally.
         Combined with min-width: 0 (fieldset defaults to
         min-width: min-content which blocks flex/grid shrinking). */
      grid-template-columns: minmax(0, max-content) minmax(0, 1fr);
      gap: 0.4rem 0.75rem;
      align-items: center;
      min-width: 0;
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
       Selector-scope its layout to .create-pane so the pane grid and
       fieldset styles don't leak into other panes. Modal wrappers were
       removed — clicking outside used to lose curator work, and a
       pane-native form has no such risk. */
    /* Both the pane and the form-within-it use a strict single-column
       grid. Without an explicit grid-template-columns, a child
       carrying grid-column: 1 / -1 (e.g. .save-error, or the
       atomized-toggle inside the name fieldset) can extend the
       implicit grid to multiple columns and cascade sibling
       fieldsets into a two-column layout that overflows the pane. */
    .create-pane {
      display: grid;
      grid-template-columns: minmax(0, 1fr);
      gap: 0.5rem;
      min-width: 0;
    }
    .create-pane .create-form {
      display: grid;
      grid-template-columns: minmax(0, 1fr);
      gap: 0.5rem;
      min-width: 0;
    }
    .create-pane .toolbar {
      justify-content: flex-end;
    }
    /* The name fieldset carries a nested atomized-fields fieldset and
       the authorship-pair div. Both need to span the parent grid's
       two columns (max-content 1fr) so they render full-width rather
       than squeezing into the value column. Same treatment for the
       toggle checkbox row inside the name fieldset. */
    .create-pane .name-fieldset > fieldset,
    .create-pane .name-fieldset > .authorship-pair {
      grid-column: 1 / -1;
      margin-top: 0.35rem;
    }
    .create-pane .name-fieldset > .atomized-toggle {
      margin-top: 0.25rem;
    }
    /* Scientific-name cell: the parse-quality glyph overlays the
       right edge of the input (matching the omnibox's inline clear-x
       treatment) rather than sitting outside it. Position: relative
       on the wrapper anchors the absolutely-positioned glyph;
       padding-right on the input reserves space so the caret and
       long names don't slide under it. */
    .create-pane .sci-input-cell {
      position: relative;
      display: block;
      min-width: 0;
    }
    .create-pane .sci-input-cell > .sci-input {
      padding-right: 1.75rem;
    }
    .create-pane .parse-quality {
      position: absolute;
      right: 0.4rem;
      top: 50%;
      transform: translateY(-50%);
      display: flex;
      align-items: center;
      justify-content: center;
      background: none;
      border: none;
      padding: 0.15rem;
      color: inherit;
      font-size: 1.05em;
      font-family: var(--font-mono);
      cursor: pointer;
      user-select: none;
      line-height: 1;
      border-radius: 3px;
    }
    .create-pane .parse-quality:hover {
      background: color-mix(in oklab, currentColor 12%, transparent);
    }
    .create-pane .parse-quality:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 1px;
    }
    .create-pane .parse-quality.pq-ok { color: var(--sev-info); }
    .create-pane .parse-quality.pq-warn { color: var(--sev-warn); }
    .create-pane .parse-quality.pq-err { color: var(--sev-error); }
    /* Stale = curator has edited the sci-name past the last-parsed
       value; the glyph dims to signal "hit Enter to refresh." */
    .create-pane .parse-quality.pq-stale {
      color: var(--dim);
      opacity: 0.5;
    }
    /* Unparsed-tail token in the top-of-pane warning banner. Monospace
       + subtle backdrop so the rejected text reads as raw parser
       output rather than prose. Wraps on narrow panes. */
    .create-pane .parse-tail-text {
      font-family: var(--font-mono);
      background: color-mix(in oklab, var(--sev-warn) 14%, transparent);
      padding: 0.05rem 0.3rem;
      border-radius: 2px;
      word-break: break-word;
    }
    /* Name relations fieldset — one row per relation with three
       picker columns (related name / type / reference) + a small
       action button. Persisted rows show the values with an unlink
       X; the trailing draft row is three empty pickers + a plus
       button. */
    fieldset.name-relations .name-relation-row {
      grid-column: 1 / -1;
      display: grid;
      grid-template-columns: 2fr 1fr 2fr auto;
      gap: 0.3rem;
      align-items: center;
      padding: 0.15rem 0;
    }
    fieldset.name-relations .name-relation-row.persisted .rel-type {
      color: var(--dim);
      font-family: var(--font-mono);
      font-size: var(--fs-xs);
      padding: 0.3rem 0.4rem;
    }
    fieldset.name-relations .name-relation-row.persisted .rel-name {
      color: var(--accent);
      text-decoration: none;
      padding: 0.3rem 0.4rem;
      min-width: 0;
      overflow-wrap: break-word;
    }
    fieldset.name-relations .name-relation-row.persisted .rel-name:hover {
      text-decoration: underline;
    }
    fieldset.name-relations .name-relation-row.persisted .rel-ref {
      color: var(--dim);
      font-size: var(--fs-xs);
      padding: 0.3rem 0.4rem;
      min-width: 0;
      overflow-wrap: break-word;
    }
    fieldset.name-relations .name-relation-row.draft sfga-combobox {
      min-width: 0;
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
  `];

  constructor() {
    super();
    this.taxonId = "";
    this.editable = false;
    this._taxon = null;
    this._name = null;
    this._etag = "";
    this._nameEtag = "";
    this._nomenHistory = null;
    this._vernaculars = [];
    this._vernacularForm = null;
    this._distributions = [];
    this._distributionForm = null;
    this._speciesInteractions = [];
    this._speciesInteractionForm = null;
    this._synonymDelete = null;
    this._standardizedAuthorship =
      localStorage.getItem("hive-standardized-authorship") === "true";
    this._error = "";
    this._loading = false;
    this._creating = false;
    this._createDraft = {};
    this._createBusy = false;
    this._createError = "";
    this._createLastParsedVerbatim = "";
    this._createParseQuality = null;
    this._createParseTail = null;
    // Both atomized toggles seed from localStorage so a curator who
    // wants to see the parser's atomization gets that view immediately
    // on every taxon they open (not just the first). Persists across
    // sessions too.
    this._createShowAtomized = readAtomizedPref();
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = null;
    this._editingTaxonID = "";
    this._editingTaxonEtag = "";
    this._editingNameEtag = "";
    this._editingOriginalTaxon = null;
    this._editingOriginalName = null;
    this._editingParentDraft = null;
    this._editingParentDraftName = "";
    this._editingNameIssues = [];
    this._editingSynonymID = "";
    this._editingAcceptedTaxonID = "";
    this._editingAcceptedTaxonDraft = null;
    this._editingAcceptedTaxonDraftName = "";
    this._nameRelations = [];
    this._nameRelationDraft = { related_name_id: "", type: "", reference_id: "" };
    this._nameRelationBusy = false;
    this._vocabEditor = "";
    this._editRefID = "";
    this._confirmDelete = false;
    this._deleteError = "";
    this._deletePreview = null;
    this._deleteMode = "";
    this._deleteConfirmText = "";
    this.pendingWarnings = [];
  }

  updated(changed) {
    if (changed.has("taxonId")) {
      // Discard any in-flight edit or in-flight create when the
      // selection changes. Same rule as the TUI's SetCurrent.
      this._creating = false;
      this._createDraft = {};
      this._createError = "";
      this._createLastParsedVerbatim = "";
      this._createParseQuality = null;
      this._createParseTail = null;
      this._createShowAtomized = readAtomizedPref();
      this._creatingBasionymFor = null;
      this._creatingBasionymForName = "";
      this._createBasionymInline = null;
      this._createBasionymInlineLastParsedVerbatim = "";
      this._editingTaxonID = "";
      this._editingTaxonEtag = "";
      this._editingNameEtag = "";
      this._editingOriginalTaxon = null;
      this._editingOriginalName = null;
      this._editingParentDraft = null;
      this._editingParentDraftName = "";
      this._editingNameIssues = [];
      this._editingSynonymID = "";
      this._editingAcceptedTaxonID = "";
      this._editingAcceptedTaxonDraft = null;
      this._editingAcceptedTaxonDraftName = "";
    this._editRefID = "";
      // Retain a handle to the in-flight load so callers of
      // performAction() can await the selection catching up before
      // firing the action against a partially-loaded state. See
      // DESIGN.md § List-row actions.
      this._loadPromise = this._load();
    }
    // Any state that changes which header actions this screen wants
    // in the app header → notify the shell so it re-renders the
    // header slot. See DESIGN.md § Screen actions.
    if (
      changed.has("_taxon") ||
      changed.has("_creating") ||
      changed.has("editable")
    ) {
      this.dispatchEvent(
        new CustomEvent("screen-actions-changed", {
          bubbles: true,
          composed: true,
        }),
      );
    }
    // Sync modal-stack registration for the inline modals SfgaDetail
    // renders (vernacular / distribution / species-interaction /
    // synonym-delete / delete-confirm). At most one inline modal is
    // open at a time, so one stack entry suffices. See openModal /
    // isTopModal helpers.
    const inlineOpen = this._hasInlineModal();
    if (inlineOpen && !this._inlineModalStackID) {
      this._inlineModalStackID = openModal();
      if (!this._unsubInlineModalStack) {
        this._unsubInlineModalStack = subscribeModalStack(() =>
          this.requestUpdate(),
        );
      }
    } else if (!inlineOpen && this._inlineModalStackID) {
      closeModal(this._inlineModalStackID);
      this._inlineModalStackID = null;
      if (this._unsubInlineModalStack) {
        this._unsubInlineModalStack();
        this._unsubInlineModalStack = null;
      }
    }
  }

  // _hasInlineModal reports whether any of SfgaDetail's own inline
  // modal-backdrop divs is currently in the render tree. Dedicated
  // modal components (sfga-add-reference-modal) register with the
  // stack on their own via connectedCallback — this only covers the
  // in-shadow-root inline modals.
  _hasInlineModal() {
    return !!(
      this._vernacularForm ||
      this._distributionForm ||
      this._speciesInteractionForm ||
      this._synonymDelete ||
      this._confirmDelete
    );
  }

  // _backdropClass returns the class string for SfgaDetail's inline
  // modal backdrops. Adds `is-covered` when a dedicated modal (opened
  // on top via openModal()) is now the topmost — so the inline
  // backdrop hides via CSS while its Lit state stays intact for the
  // return trip.
  _backdropClass() {
    const covered =
      this._inlineModalStackID && !isTopModal(this._inlineModalStackID);
    return "modal-backdrop" + (covered ? " is-covered" : "");
  }

  // _openVocabEditor mounts sfga-vocab-editor for the named vocab.
  // Vocabs without an entry in VOCAB_EDITOR_ADAPTERS silently do
  // nothing (the picker's action is gated on the same adapter, so
  // this should only fire for editable vocabs).
  _openVocabEditor(name) {
    if (!vocabEditorAdapter(name)) return;
    this._vocabEditor = name;
  }

  _closeVocabEditor() {
    this._vocabEditor = "";
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    // Belt-and-braces: if the component is torn down with an open
    // inline modal (e.g. selection reset mid-edit), release the stack
    // entry so a subsequent modal doesn't inherit the "covered" state.
    if (this._inlineModalStackID) {
      closeModal(this._inlineModalStackID);
      this._inlineModalStackID = null;
    }
    if (this._unsubInlineModalStack) {
      this._unsubInlineModalStack();
      this._unsubInlineModalStack = null;
    }
  }

  async _load() {
    if (!this.taxonId) {
      this._taxon = this._name = null;
      this._etag = "";
      this._nameEtag = "";
      this._nomenHistory = null;
      this._vernaculars = [];
      this._distributions = [];
      this._speciesInteractions = [];
      this._classification = [];
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

      // Fetch nomen-history + vernaculars + distributions +
      // species-interactions + classification in parallel — none depend
      // on the others and all are needed before the pane finishes
      // rendering. Per-section failure drops that one section; the rest
      // of the taxon still renders.
      const [nomen, vern, dist, sxi, cls] = await Promise.all([
        api.taxon.nomenHistory(requested).catch(() => ({ clusters: [] })),
        api.taxon.vernaculars(requested).catch(() => ({ items: [] })),
        api.taxon.distributions(requested).catch(() => ({ items: [] })),
        api.taxon.speciesInteractions(requested).catch(() => ({ items: [] })),
        api.taxon.classification(requested).catch(() => ({ items: [] })),
      ]);
      if (this.taxonId !== requested) return;
      this._nomenHistory = nomen;
      this._vernaculars = vern.items || [];
      this._distributions = dist.items || [];
      this._speciesInteractions = sxi.items || [];
      this._classification = cls.items || [];
    } catch (err) {
      if (this.taxonId !== requested) return;
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      if (this.taxonId === requested) this._loading = false;
    }
  }

  // startCreateRoot is the public entry the shell calls when the tree
  // is empty and the curator clicks "Add first taxon". Opens the
  // create pane with an empty parent so the new taxon is added at
  // the root of the archive. Also usable later for the "add a new
  // root to an already-populated archive" case, though that flow
  // typically routes through "add sister" on an existing root.
  async startCreateRoot() {
    return this._openCreateWithParent("", "(new top-level taxon)");
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
    this._createError = "";
    this._createBusy = false;
    this._createLastParsedVerbatim = "";
    this._createParseQuality = null;
    this._createParseTail = null;
    this._pickedCreateReferenceLabel = undefined;
    this._createParentID = parentID;
    this._createParentLabel = parentLabel;
    this._creating = true;
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
    this._editingTaxonID = "";
    this._editingTaxonEtag = "";
    this._editingNameEtag = "";
    this._editingOriginalTaxon = null;
    this._editingOriginalName = null;
    this._editingParentDraft = null;
    this._editingParentDraftName = "";
    this._editingNameIssues = [];
    this._editingSynonymID = "";
    this._editingAcceptedTaxonID = "";
    this._editingAcceptedTaxonDraft = null;
    this._editingAcceptedTaxonDraftName = "";
    this._nameRelations = [];
    this._nameRelationDraft = { related_name_id: "", type: "", reference_id: "" };
    this._nameRelationBusy = false;
    this._vocabEditor = "";
    this._editRefID = "";
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
    if (this._creating) {
      const input = this.renderRoot.querySelector(".create-pane .sci-input");
      if (input) {
        input.focus();
        const end = input.value.length;
        input.setSelectionRange(end, end);
      }
    }
  }

  // _openEditTaxon opens the unified pane in edit mode against the
  // currently-loaded taxon. Hydrates the draft with the existing
  // taxon + attached name fields so every input renders its current
  // value; on submit the pane diffs the draft against the loaded
  // originals and PATCHes only the changed slice on each aggregate
  // (with If-Match). See TAXON_EDITOR_PLAN.md § Unified create/edit
  // form.
  //
  // Parent changes route through POST /api/taxon/{id}/move (tracked
  // via _editingParentDraft) rather than a PATCH field, matching the
  // sfga model where a reparent isn't a plain column update.
  //
  // Requires _taxon (and, if present, _name) already loaded — the
  // pencil is only surfaced from renderHeaderActions after the taxon
  // detail loads, so this precondition holds at the call sites.
  async _openEditTaxon() {
    const t = this._taxon;
    if (!t) return;
    const n = this._name || null;
    // Seed the draft with every editable field. Missing values become
    // empty strings so re-parse's parser-derived-clobber path has a
    // consistent shape to diff against.
    const draft = {
      // Name-side (curator-authored + parser-derived — everything the
      // create form knows how to edit).
      scientific_name: n?.scientific_name || n?.scientific_name_string || "",
      scientific_name_string: n?.scientific_name_string || "",
      authorship: n?.authorship || "",
      rank: n?.rank || "",
      code: n?.code || "",
      status: n?.status || "",
      uninomial: n?.uninomial || "",
      genus: n?.genus || "",
      infrageneric_epithet: n?.infrageneric_epithet || "",
      specific_epithet: n?.specific_epithet || "",
      infraspecific_epithet: n?.infraspecific_epithet || "",
      cultivar_epithet: n?.cultivar_epithet || "",
      combination_authorship: n?.combination_authorship || "",
      combination_ex_authorship: n?.combination_ex_authorship || "",
      combination_authorship_year: n?.combination_authorship_year || "",
      basionym_authorship: n?.basionym_authorship || "",
      basionym_ex_authorship: n?.basionym_ex_authorship || "",
      basionym_authorship_year: n?.basionym_authorship_year || "",
      reference_id: n?.reference_id || "",
      published_in_year: n?.published_in_year || "",
      published_in_page: n?.published_in_page || "",
      published_in_page_link: n?.published_in_page_link || "",
      etymology: n?.etymology || "",
      // Name-side remarks — the create form's remarks textarea maps here.
      remarks: n?.remarks || "",
      // Taxon-side fields (edited via the "additional taxon fields"
      // fieldset that only renders in edit mode).
      name_phrase: t.name_phrase || "",
      scrutinizer: t.scrutinizer || "",
      scrutinizer_id: t.scrutinizer_id || "",
      scrutinizer_date: t.scrutinizer_date || "",
      extinct:
        t.extinct === undefined || t.extinct === null ? "" : String(t.extinct),
      link: t.link || "",
      taxon_remarks: t.remarks || "",
    };
    // Pre-populate the reference-label cache so the picker shows the
    // hydrated reference by name rather than a lookup-in-flight state.
    if (n?.reference_label) {
      this._pickedCreateReferenceLabel = n.reference_label;
    } else {
      this._pickedCreateReferenceLabel = undefined;
    }
    this._createDraft = draft;
    this._createError = "";
    this._createBusy = false;
    // Seed the re-parse guard with the current sci-name so a Tab-out
    // without editing doesn't fire a redundant parse.
    this._createLastParsedVerbatim = draft.scientific_name.trim();
    // Hydrate parse_quality from the loaded name so the glyph shows
    // meaningful state on open. Falls back to null when the row
    // predates the gn__parse_quality column (or when the loaded value
    // is missing).
    this._createParseQuality =
      typeof n?.parse_quality === "number" ? n.parse_quality : null;
    // _createParseTail stays null on open — the tail is discovered
    // either from the persisted parse-tail issue (loaded below) or
    // from a fresh in-session re-parse (curator edits + Enter).
    this._createParseTail = null;
    this._editingNameIssues = [];
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = null;
    this._creating = true;
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
    this._editingTaxonID = t.id;
    this._editingTaxonEtag = t.__etag || this._etag || "";
    this._editingNameEtag = n?.__etag || this._nameEtag || "";
    this._editingOriginalTaxon = t;
    this._editingOriginalName = n;
    this._editingParentDraft = null;
    this._editingParentDraftName = "";
    this._editingSynonymID = "";
    this._editingAcceptedTaxonID = "";
    this._editingAcceptedTaxonDraft = null;
    this._editingAcceptedTaxonDraftName = "";
    this._nameRelations = [];
    this._nameRelationDraft = { related_name_id: "", type: "", reference_id: "" };
    this._nameRelationBusy = false;
    this._vocabEditor = "";
    this._editRefID = "";
    // Fire an async fetch of persisted issues on this name so any
    // parse-tail (or other soft) issue renders in the top-of-pane
    // banner. Best-effort — a fetch failure leaves _editingNameIssues
    // empty and the banner just won't render.
    if (n?.id) {
      const nameID = n.id;
      api.issue
        .list({ table: "name", record_id: nameID, limit: 100 })
        .then((resp) => {
          if (this._editingTaxonID === t.id) {
            this._editingNameIssues = resp.items || [];
          }
        })
        .catch(() => {
          /* leave issues empty; banner just won't render */
        });
      this._loadNameRelations(nameID);
    }
    this._maybeBackfillOnOpen();
    await this.updateComplete;
    const input = this.renderRoot.querySelector(".create-pane .sci-input");
    if (input) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
  }

  // _maybeBackfillOnOpen runs the same citation-backfill pass that
  // fires on reference-pick, but against the reference the loaded
  // name already carries. Fixes the "open a name whose combination
  // author/year are empty but whose reference is set" case — no
  // pick event fires on open, so the backfill would otherwise never
  // run. Fill-empty-only semantics preserve any curator-typed values
  // that are already populated.
  async _maybeBackfillOnOpen() {
    const refID = (this._createDraft?.reference_id || "").trim();
    if (refID) {
      let ref;
      try {
        ref = await api.reference.get(refID);
      } catch (_) {
        /* fall through — still try the basionym backfill */
      }
      // Guard: bail if the pane closed or the reference changed while
      // the fetch was in flight.
      if (
        ref &&
        this._creating &&
        this._createDraft?.reference_id === refID
      ) {
        this._backfillCombinationFromCitation(ref);
      }
    }
    // Also backfill basionym_year on a recomb from the LINKED
    // basionym's own reference. The primary reference above is the
    // subseq citation; the original year lives on the basionym's
    // own reference row. Fires when the loaded name has a linked
    // basionym (via name_relation) OR the draft carries a
    // basionym_name_id (create-synonym flow via cluster-`+`).
    const linkedID =
      this._editingOriginalName?.basionym?.id ||
      (this._createDraft?.basionym_name_id || "").trim();
    if (linkedID) {
      await this._backfillPrimaryBasionymFromLinkedName(linkedID);
    }
  }

  // _onBasionymPickerPick handles a pick on the primary form's
  // basionym combobox. Sets basionym_name_id and — since the picked
  // name comes with its own reference (the original citation) —
  // also walks that reference to fill any empty basionym_authorship
  // / basionym_authorship_year on the primary draft. Handles the ICN
  // recomb case where curator picked the basionym after having
  // parsed a verbatim like `Aus bus (L.)` (basA populated, basY
  // empty) — the year materializes from the picked basionym's own
  // reference.
  async _onBasionymPickerPick(basionymID) {
    this._createFieldChange("basionym_name_id", basionymID);
    if (!basionymID) return;
    await this._backfillPrimaryBasionymFromLinkedName(basionymID);
  }

  // _backfillPrimaryBasionymFromLinkedName walks a linked basionym
  // name → its reference → extracts author + year, and fills the
  // primary draft's basionym_authorship / basionym_authorship_year
  // when empty. Handles the ICN subseq recomb case where the primary
  // reference is the SUBSEQUENT citation and the original year lives
  // on the basionym's own reference row.
  //
  // Fill-empty-only preserves anything gnparser already extracted
  // from the verbatim (usually author for ICN, sometimes year for
  // ICZN-style parenthetical `(L., 1758)`).
  async _backfillPrimaryBasionymFromLinkedName(basionymNameID) {
    if (!basionymNameID) return;
    let bName;
    try {
      bName = await api.name.get(basionymNameID);
    } catch (_) {
      return;
    }
    if (!this._creating) return;
    const bRefID = (bName?.reference_id || "").trim();
    if (!bRefID) return;
    let bRef;
    try {
      bRef = await api.reference.get(bRefID);
    } catch (_) {
      return;
    }
    if (!this._creating) return;
    const { author: refA, year: refY } = refCitationAuthorAndYear(bRef);
    if (!refA && !refY) return;
    const d = this._createDraft;
    const patch = {};
    if (!(d.basionym_authorship || "").trim() && refA) {
      patch.basionym_authorship = refA;
    }
    if (!(d.basionym_authorship_year || "").trim() && refY) {
      patch.basionym_authorship_year = refY;
    }
    if (Object.keys(patch).length > 0) {
      this._createDraft = { ...this._createDraft, ...patch };
    }
  }

  // _openEditSynonym opens the unified pane as a name-editor for a
  // synonym row (the pencil on Nomenclatural-history rows routes
  // here). Fetches the name row and any persisted issues, hydrates
  // _createDraft from the name, and enters synonym-edit mode:
  //
  //   * Header renders "Edit synonym <name>".
  //   * Taxon-fields fieldset (name_phrase / scrutinizer / extinct /
  //     link / taxon_remarks) is hidden — a synonym has no
  //     independent taxon row.
  //   * An accepted-taxon picker fieldset replaces the parent picker,
  //     so a curator can move the synonym to a different accepted
  //     taxon via POST /api/synonym/{id}/move on save.
  //   * Original-combination info renders read-only when the name has
  //     a linked basionym (same treatment as taxon-edit mode).
  //
  // Delegates identical name-side setup to _openEditTaxon's pattern —
  // hydration of _createDraft, parse_quality seeding, issue fetch —
  // so curators see one form regardless of whether they edit an
  // accepted taxon or a synonym.
  async _openEditSynonym(nameID, synonymID, currentTaxonID) {
    if (!nameID) return;
    // Two-phase fetch: name row + validation issues in parallel.
    // Issue fetch failure is silent (banner just won't render).
    this._creating = true;
    this._createError = "";
    this._createBusy = true;
    this._createLastParsedVerbatim = "";
    this._createParseQuality = null;
    this._createParseTail = null;
    this._createDraft = { scientific_name: "" };
    this._pickedCreateReferenceLabel = undefined;
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = null;
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
    this._editingTaxonID = "";
    this._editingTaxonEtag = "";
    this._editingOriginalTaxon = null;
    this._editingParentDraft = null;
    this._editingParentDraftName = "";
    this._editingNameIssues = [];
    this._editingSynonymID = synonymID || "";
    this._editingAcceptedTaxonID = currentTaxonID || "";
    this._editingAcceptedTaxonDraft = null;
    this._editingAcceptedTaxonDraftName = "";
    this._editRefID = "";
    this._editingNameEtag = "";
    this._editingOriginalName = null;
    try {
      const [name, issueResp] = await Promise.all([
        api.name.get(nameID),
        api.issue
          .list({ table: "name", record_id: nameID, limit: 100 })
          .catch(() => ({ items: [] })),
      ]);
      // Bail if the curator navigated away or opened a different
      // synonym while this fetch was in flight.
      if (this._editingSynonymID !== (synonymID || "")) return;
      const draft = {
        scientific_name: name.scientific_name || name.scientific_name_string || "",
        scientific_name_string: name.scientific_name_string || "",
        authorship: name.authorship || "",
        rank: name.rank || "",
        code: name.code || "",
        status: name.status || "",
        uninomial: name.uninomial || "",
        genus: name.genus || "",
        infrageneric_epithet: name.infrageneric_epithet || "",
        specific_epithet: name.specific_epithet || "",
        infraspecific_epithet: name.infraspecific_epithet || "",
        cultivar_epithet: name.cultivar_epithet || "",
        combination_authorship: name.combination_authorship || "",
        combination_ex_authorship: name.combination_ex_authorship || "",
        combination_authorship_year: name.combination_authorship_year || "",
        basionym_authorship: name.basionym_authorship || "",
        basionym_ex_authorship: name.basionym_ex_authorship || "",
        basionym_authorship_year: name.basionym_authorship_year || "",
        reference_id: name.reference_id || "",
        published_in_year: name.published_in_year || "",
        published_in_page: name.published_in_page || "",
        published_in_page_link: name.published_in_page_link || "",
        etymology: name.etymology || "",
        remarks: name.remarks || "",
      };
      if (name.reference_label) {
        this._pickedCreateReferenceLabel = name.reference_label;
      }
      this._createDraft = draft;
      this._createLastParsedVerbatim = draft.scientific_name.trim();
      this._createParseQuality =
        typeof name.parse_quality === "number" ? name.parse_quality : null;
      this._editingOriginalName = name;
      this._editingNameEtag = name.__etag || "";
      this._editingNameIssues = issueResp.items || [];
      this._loadNameRelations(name.id);
    } catch (err) {
      this._createError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._createBusy = false;
    }
    this._maybeBackfillOnOpen();
    await this.updateComplete;
    const input = this.renderRoot.querySelector(".create-pane .sci-input");
    if (input) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
  }

  _cancelCreate() {
    this._creating = false;
    this._createError = "";
    this._createLastParsedVerbatim = "";
    this._createParseQuality = null;
    this._createParseTail = null;
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
    this._editingTaxonID = "";
    this._editingTaxonEtag = "";
    this._editingNameEtag = "";
    this._editingOriginalTaxon = null;
    this._editingOriginalName = null;
    this._editingParentDraft = null;
    this._editingParentDraftName = "";
    this._editingNameIssues = [];
    this._editingSynonymID = "";
    this._editingAcceptedTaxonID = "";
    this._editingAcceptedTaxonDraft = null;
    this._editingAcceptedTaxonDraftName = "";
    this._nameRelations = [];
    this._nameRelationDraft = { related_name_id: "", type: "", reference_id: "" };
    this._nameRelationBusy = false;
    this._vocabEditor = "";
    this._editRefID = "";
  }

  // _openEditReference opens the unified reference modal
  // (sfga-add-reference-modal) in edit mode over the current
  // create/edit taxon pane. The modal owns all the working state
  // (load, form draft, PATCH round-trip); the pane only tracks
  // which reference is being edited via _editRefID so the render
  // knows to mount the modal. Save → the modal dispatches
  // reference-updated, caught by _onReferenceUpdated on the pane
  // to refresh picker badges + re-run backfill.
  _openEditReference(id) {
    if (!id) return;
    this._editRefID = id;
  }

  _cancelEditReference() {
    this._editRefID = "";
  }

  // _renderEditReferenceModal mounts the unified reference modal in
  // edit mode. Empty return when no id is set — modal only exists
  // in the render tree while the curator is actively editing.
  _renderEditReferenceModal() {
    if (!this._editRefID) return "";
    // Feed the BHLnames tab with the current taxon-form context so
    // "look up this reference in BHL" can search on the name the
    // curator was working on. Prefers the currently-open draft's
    // sci-name (curator may have edited it since the taxon loaded)
    // and falls back to the loaded name row.
    const draftSci =
      (this._createDraft?.scientific_name || "").trim() ||
      (this._createDraft?.scientific_name_string || "").trim();
    const canonical =
      this._name?.canonical_simple ||
      draftSci ||
      this._name?.scientific_name ||
      "";
    const authors =
      this._createDraft?.authorship ||
      this._name?.authors ||
      this._name?.authorship ||
      "";
    const rawYear =
      this._createDraft?.basionym_authorship_year ||
      this._createDraft?.combination_authorship_year ||
      this._name?.published_in_year ||
      "";
    const year = parseInt(rawYear, 10) || 0;
    // The modal's reference-updated event bubbles up to the pane
    // wrapper's @reference-updated handler (_onReferenceUpdated),
    // which refreshes picker badges and re-runs backfill. This
    // local listener just closes the modal — Lit event ordering
    // guarantees both fire in the same event tick.
    return html`
      <sfga-add-reference-modal
        .editID=${this._editRefID}
        .contextCanonical=${canonical}
        .contextAuthors=${authors}
        .contextYear=${year}
        @reference-updated=${() => this._cancelEditReference()}
        @reference-picked=${() => this._cancelEditReference()}
        @close=${() => this._cancelEditReference()}
      ></sfga-add-reference-modal>
    `;
  }

  // Fields cleared before each re-parse. Everything gnparser
  // (re-)derives from the verbatim string — canonical parts, atomized
  // authorship, and the umbrella `authorship`. Curator-authored
  // fields (reference_id, remarks, synonym_status, basionym_name_id,
  // code, rank) are preserved by the SURVIVES rule in _reparseVerbatim.
  static _PARSER_DERIVED_FIELDS = [
    "uninomial",
    "genus",
    "infrageneric_epithet",
    "specific_epithet",
    "infraspecific_epithet",
    "cultivar_epithet",
    "authorship",
    "basionym_authorship",
    "basionym_ex_authorship",
    "basionym_authorship_year",
    "combination_authorship",
    "combination_ex_authorship",
    "combination_authorship_year",
  ];

  // _reparseVerbatim fires the server-side parse (POST /api/name/parse)
  // and re-fills the atomized fields from the result. Called on Enter
  // or blur from the scientific-name input at the top of the create
  // form. Guarded so a repeat trigger on the same verbatim string does
  // nothing — Tab-out + Tab-in shouldn't refire.
  //
  // Re-parse always clobbers parser-derived fields (see
  // _PARSER_DERIVED_FIELDS) so a corrected verbatim string produces
  // fresh atomized values. Curator-authored fields (reference_id,
  // remarks, synonym_status, basionym_name_id, published_in_page,
  // etymology, status, parent_id, code, rank) survive untouched.
  //
  // Empty input is a no-op — validation of "scientific name is
  // required" happens at submit time so curators can still open the
  // form, browse the fieldsets, and enter data in any order.
  async _reparseVerbatim(rawSci) {
    const sci = (rawSci || "").trim();
    if (!sci) return;
    if (sci === this._createLastParsedVerbatim) return;
    this._createBusy = true;
    this._createError = "";
    try {
      const preview = await api.name.parse(sci, this._createDraft.code);
      // Start from the current draft to preserve curator-authored
      // fields, then overlay only the parser-derived slice.
      const merged = { ...this._createDraft };
      for (const f of SfgaDetail._PARSER_DERIVED_FIELDS) {
        merged[f] = preview[f] || "";
      }
      merged.scientific_name = sci;
      merged.scientific_name_string = sci;
      // Rank only fills when empty — the curator's explicit pick wins
      // over gnparser's guess. Same rule for code (which is separately
      // pre-seeded from CodeForParent and shouldn't be overwritten by
      // a parse guess).
      if (!merged.rank && preview.rank) merged.rank = preview.rank;
      this._createDraft = merged;
      this._createLastParsedVerbatim = sci;
      this._createParseQuality =
        typeof preview.parse_quality === "number" ? preview.parse_quality : null;
      // "" for a clean parse, non-empty when gnparser rejected trailing
      // text. Drives the top-of-pane warning banner. Live in-session
      // value overrides any persisted parse-tail issue.
      this._createParseTail =
        typeof preview.tail === "string" ? preview.tail : "";
      // After the parse-driven re-render lands, if the authorship
      // input is the currently-focused element (curator tab-blurred
      // out of the sci-name and Tab landed on Authorship), position
      // the caret at the end of the freshly-populated value so a
      // backspace deletes the last character rather than doing
      // nothing at position 0. No effect when authorship stayed
      // empty (empty inputs have position 0 either way) or when
      // focus is somewhere else entirely.
      await this.updateComplete;
      const auth = this.renderRoot.querySelector(
        ".create-pane .authorship-input",
      );
      if (
        auth &&
        this.shadowRoot?.activeElement === auth &&
        auth.value
      ) {
        auth.setSelectionRange(auth.value.length, auth.value.length);
      }
    } catch (err) {
      this._createError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
      // Parse failed → clear the quality indicator so the stale glyph
      // doesn't mislead. The atomized fields we cleared above stay
      // empty; the sci-input still shows what the curator typed.
      this._createParseQuality = null;
      this._createParseTail = null;
    } finally {
      this._createBusy = false;
    }
  }

  // Enter-key gate for the scientific-name input. Enter re-parses;
  // Escape cancels. Guard against IME composition so Enter to commit
  // a CJK candidate doesn't accidentally trigger a parse.
  _onSciInputKeydown(e) {
    if (e.isComposing) return;
    if (e.key === "Enter") {
      e.preventDefault();
      this._reparseVerbatim(e.target.value);
    } else if (e.key === "Escape") {
      e.preventDefault();
      this._cancelCreate();
    }
  }

  // _renderParseQualityGlyph draws the small inline indicator overlaid
  // on the right edge of the scientific-name input:
  //   * ✓ (green)         — parse_quality 1 (clean)
  //   * triangle-alert   — parse_quality 2-3 (imperfect, amber)
  //                         and parse_quality 4 (unparseable, red)
  //
  // Rendered as a <button> so clicking opens the atomized-fields view
  // — the natural next step when the curator wants to inspect what
  // gnparser produced. The triangle-alert shape matches the sygil hive
  // uses for validation issues elsewhere; on purpose the red case does
  // NOT look like an "×" so it doesn't get confused with the omnibox
  // clear-x directly below the input.
  //
  // Renders nothing when no parse has run yet. When the curator has
  // edited the sci-name past _createLastParsedVerbatim, the glyph
  // dims to signal "hit Enter to refresh" without hiding it entirely
  // — the previous quality is still useful context.
  //
  // gnparser's parse_quality scale: 1 = clean parse, 2 = imperfect
  // but recognizable, 3 = poor / recoverable, 4 = unparseable.
  // See github.com/gnames/gnparser docs for the full semantics.
  _renderParseQualityGlyph(currentSci) {
    const q = this._createParseQuality;
    if (q === null || q === undefined) return "";
    const stale =
      (currentSci || "").trim() !== (this._createLastParsedVerbatim || "");
    let content, cls, note;
    if (q === 4) {
      content = renderIcon("triangle-alert", 16);
      cls = "pq-err";
      note = "unparseable";
    } else if (q >= 2) {
      content = renderIcon("triangle-alert", 16);
      cls = "pq-warn";
      note = "imperfect parse";
    } else {
      content = html`✓`;
      cls = "pq-ok";
      note = "clean parse";
    }
    const staleCls = stale ? " pq-stale" : "";
    const title = stale
      ? `${note} (quality ${q}/4) — click to show atomized fields; press Enter in the input to re-parse`
      : `${note} (quality ${q}/4) — click to show atomized fields`;
    return html`<button
      type="button"
      class="parse-quality ${cls}${staleCls}"
      title=${title}
      aria-label=${title}
      @click=${() => this._showAtomizedFields()}
    >
      ${content}
    </button>`;
  }

  // _showAtomizedFields is the click target for the parse-quality
  // glyph. Opens the atomized-fields section (persisted per the
  // shared preference) so the curator can inspect / correct what
  // gnparser produced.
  _showAtomizedFields() {
    this._createShowAtomized = true;
    writeAtomizedPref(true);
  }

  _createFieldChange(field, value) {
    this._createDraft = { ...this._createDraft, [field]: value };
  }

  // _onCreateReferencePick handles a reference pick on the primary
  // form's publication combobox. Sets reference_id (same as the plain
  // handler) and — when the draft looks like a recomb and the picked
  // citation is NOT the basionym's own citation — backfills any empty
  // combination_authorship / combination_authorship_year from the
  // reference's author + year.
  //
  // Fill rule (matches the rest of hive's editor pattern):
  //   * Fill-empty-only. Curator-typed values in
  //     combination_authorship / _year survive.
  //   * Recomb-only. Original combinations don't get combination_*
  //     backfill; the parenthetical-authorship or populated-basionym-
  //     authorship signal gates the fill.
  //   * Skip when (citation author, year) matches (basionym author,
  //     year) — that's either the original citation being picked in
  //     error, or the rare same-author-same-year recomb; either way,
  //     don't materialize the (probably wrong) duplicate. Legacy
  //     rows already carrying the pattern surface via the
  //     hive_combination_matches_basionym validation rule.
  //
  // Fetches full reference detail on pick to see author+year (the
  // combobox pick event only carries id+label). One extra HTTP call
  // per pick; harmless. Best-effort — a fetch failure just no-ops.
  async _onCreateReferencePick(refID) {
    this._createFieldChange("reference_id", refID);
    if (!refID) return;
    let ref;
    try {
      ref = await api.reference.get(refID);
    } catch (_) {
      return;
    }
    // The pane may have closed / advanced by the time the fetch lands.
    if (!this._creating || this._createDraft.reference_id !== refID) return;
    this._backfillCombinationFromCitation(ref);
  }

  _backfillCombinationFromCitation(ref) {
    const d = this._createDraft;
    // Prefer atomized author/issued when set; fall back to parsing
    // the citation string for CoL-derived references that populate
    // only the free-text citation field.
    const { author: refA, year: refY } = refCitationAuthorAndYear(ref);
    if (!refA && !refY) return;

    const auth = (d.authorship || "").trim();
    const basA = (d.basionym_authorship || "").trim();
    const basY = (d.basionym_authorship_year || "").trim();
    const combA = (d.combination_authorship || "").trim();
    // Recomb: parenthetical authorship, OR combination_authorship
    // populated and differs from basionym_authorship. Bare-basionym
    // (basA populated, combA empty, no parens) is the ICN original
    // convention where CoL redundantly fills basionym_authorship —
    // treat as an original, not a recomb.
    const isRecomb =
      auth.startsWith("(") || (combA !== "" && combA !== basA);

    if (isRecomb) {
      // Subsequent-combination citation → fill combination_* pair.
      // Gate: skip when the citation matches the basionym pair (the
      // picker likely landed on the original citation, or the rare
      // same-author-same-year recomb — see
      // hive_combination_matches_basionym validator).
      if (refA && refY && refA === basA && refY === basY) return;
      const patch = {};
      if (!combA && refA) patch.combination_authorship = refA;
      if (!(d.combination_authorship_year || "").trim() && refY) {
        patch.combination_authorship_year = refY;
      }
      if (Object.keys(patch).length > 0) {
        this._createDraft = { ...this._createDraft, ...patch };
      }
      return;
    }

    // Original-combination citation → fill basionym_* pair. The
    // basionym pair on an original describes the name's own
    // establishment, so the citation's author + year map directly.
    // Fill-empty-only preserves anything the parse already extracted
    // from the verbatim.
    const patch = {};
    if (!basA && refA) patch.basionym_authorship = refA;
    if (!basY && refY) patch.basionym_authorship_year = refY;
    if (Object.keys(patch).length > 0) {
      this._createDraft = { ...this._createDraft, ...patch };
    }
  }

  async _submitCreate() {
    this._createBusy = true;
    this._createError = "";
    try {
      if (this._editingTaxonID) {
        await this._submitEdit();
        return;
      }
      if (this._editingSynonymID) {
        await this._submitEditSynonym();
        return;
      }
      if (this._creatingBasionymFor) {
        // Basionym write path — POST /api/taxon/{X}/basionym creates
        // Name + Synonym + BASIONYM name_relation atomically. Reveals
        // the current-combination taxon (not the basionym; the basionym
        // is a synonym, not an accepted taxon in the tree).
        const revealID = this._creatingBasionymFor;
        await api.taxon.addBasionym(this._creatingBasionymFor, this._createDraft);
        this._cancelCreate();
        // Refresh the local nomen history so the newly-added basionym
        // shows immediately. Same-id "taxon-moved" round-trips through
        // the shell without re-firing the detail pane's _load (Lit
        // skips reactive property updates when the value is unchanged),
        // so we refresh in-place before dispatching.
        await this._refreshNomenHistory();
        this.dispatchEvent(
          new CustomEvent("taxon-moved", {
            detail: { id: revealID },
            bubbles: true,
            composed: true,
          }),
        );
      } else if (this._creatingSynonymFor) {
        // Synonym write path — POST /api/taxon/{X}/synonym creates
        // Name + Synonym link atomically. Stay on the same accepted
        // taxon so the newly-added synonym appears in the refreshed
        // Nomenclatural history section. When the inline original-
        // combination subform is open OR a picker id is set, the
        // backend also creates/links the original combination in the
        // same transaction.
        const revealID = this._creatingSynonymFor;
        await api.taxon.addSynonym(
          this._creatingSynonymFor,
          this._buildCreateBody(),
        );
        this._cancelCreate();
        // Same-id dispatch below won't re-fire _load; refresh the
        // history locally so the newly-added synonym renders on the
        // next paint rather than leaving the section stale.
        await this._refreshNomenHistory();
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
          ...this._buildCreateBody(),
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

  // Field partition for the edit-mode diff. Every field in _createDraft
  // belongs to exactly one aggregate; the partition drives which PATCH
  // payload each delta lands in. `remarks` maps to the name aggregate
  // to match the create-form semantics; the taxon's own remarks column
  // is edited via _createDraft.taxon_remarks and translated back to
  // "remarks" on the taxon PATCH body.
  static _EDIT_TAXON_FIELDS = new Set([
    "name_phrase",
    "scrutinizer",
    "scrutinizer_id",
    "scrutinizer_date",
    "extinct",
    "link",
    "taxon_remarks",
  ]);
  static _EDIT_NAME_FIELDS = new Set([
    "scientific_name",
    "scientific_name_string",
    "authorship",
    "rank",
    "code",
    "status",
    "uninomial",
    "genus",
    "infrageneric_epithet",
    "specific_epithet",
    "infraspecific_epithet",
    "cultivar_epithet",
    "combination_authorship",
    "combination_ex_authorship",
    "combination_authorship_year",
    "basionym_authorship",
    "basionym_ex_authorship",
    "basionym_authorship_year",
    "reference_id",
    "published_in_year",
    "published_in_page",
    "published_in_page_link",
    "etymology",
    "remarks",
  ]);

  // _submitEdit computes the diff between _createDraft and the loaded
  // originals, partitions it into taxon-side and name-side patches,
  // fires an optional parent-move, then PATCHes each aggregate whose
  // slice changed (with If-Match against the etag captured at open).
  // Called from _submitCreate when _editingTaxonID is set.
  //
  // Order: move first (since move refreshes col__modified and would
  // invalidate the taxon-PATCH etag if run in the other order), then
  // PATCH taxon, then PATCH name. Each half can fail independently —
  // partial success leaves the archive consistent per aggregate.
  async _submitEdit() {
    const t = this._editingOriginalTaxon || {};
    const n = this._editingOriginalName || {};
    const d = this._createDraft;
    const taxonPatch = {};
    const namePatch = {};
    for (const [key, value] of Object.entries(d)) {
      if (SfgaDetail._EDIT_TAXON_FIELDS.has(key)) {
        const original =
          key === "taxon_remarks"
            ? t.remarks || ""
            : key === "extinct"
              ? t.extinct === undefined || t.extinct === null
                ? ""
                : String(t.extinct)
              : t[key] || "";
        if (value !== original) {
          if (key === "taxon_remarks") {
            taxonPatch.remarks = value;
          } else if (key === "extinct") {
            // Tri-state select: "" means "clear" (send null); "true" /
            // "false" become bool.
            if (value === "") taxonPatch.extinct = null;
            else taxonPatch.extinct = value === "true";
          } else {
            taxonPatch[key] = value;
          }
        }
      } else if (SfgaDetail._EDIT_NAME_FIELDS.has(key)) {
        const original = n[key] || "";
        if (value !== original) namePatch[key] = value;
      }
    }
    const hasTaxonEdits = Object.keys(taxonPatch).length > 0;
    const hasNameEdits = Object.keys(namePatch).length > 0 && n && n.id;
    const hasParentMove =
      this._editingParentDraft !== null &&
      this._editingParentDraft !== (t.parent_id ?? "");
    if (!hasTaxonEdits && !hasNameEdits && !hasParentMove) {
      this._cancelCreate();
      return;
    }
    let taxonEtag = this._editingTaxonEtag;
    let revealID = t.id;
    if (hasParentMove) {
      const moved = await api.taxon.move(
        t.id,
        this._editingParentDraft,
        taxonEtag,
      );
      taxonEtag = moved.__etag || taxonEtag;
      revealID = moved.id;
    }
    if (hasTaxonEdits) {
      const updated = await api.taxon.patch(t.id, taxonPatch, taxonEtag);
      taxonEtag = updated.__etag || taxonEtag;
    }
    if (hasNameEdits) {
      await api.name.patch(n.id, namePatch, this._editingNameEtag);
    }
    this._cancelCreate();
    this.dispatchEvent(
      new CustomEvent("taxon-moved", {
        detail: { id: revealID },
        bubbles: true,
        composed: true,
      }),
    );
  }

  // _submitEditSynonym is the synonym-edit-mode counterpart to
  // _submitEdit. Computes the name-side diff (same partition table
  // as _submitEdit's _EDIT_NAME_FIELDS), optionally moves the synonym
  // to a different accepted taxon, then PATCHes the name row with
  // If-Match. Order: move first (POST /api/synonym/{id}/move) so a
  // subsequent name-patch failure still leaves the synonym at the
  // curator's intended taxon.
  //
  // Reveal semantics: if the synonym was moved, jump to the NEW
  // accepted taxon so the moved synonym stays in view. Otherwise
  // stay on the current taxon and refresh the local nomen history
  // so the updated name renders on the next paint.
  async _submitEditSynonym() {
    const n = this._editingOriginalName || {};
    const d = this._createDraft;
    const namePatch = {};
    for (const [key, value] of Object.entries(d)) {
      if (SfgaDetail._EDIT_NAME_FIELDS.has(key)) {
        const original = n[key] || "";
        if (value !== original) namePatch[key] = value;
      }
    }
    const hasNameEdits = Object.keys(namePatch).length > 0 && n && n.id;
    const hasSynonymMove =
      this._editingAcceptedTaxonDraft !== null &&
      this._editingAcceptedTaxonDraft !== this._editingAcceptedTaxonID;
    if (!hasNameEdits && !hasSynonymMove) {
      this._cancelCreate();
      return;
    }
    let moved = false;
    let revealID = this._editingAcceptedTaxonID;
    if (hasSynonymMove) {
      await api.synonym.move(
        this._editingSynonymID,
        this._editingAcceptedTaxonDraft,
      );
      moved = true;
      revealID = this._editingAcceptedTaxonDraft;
    }
    if (hasNameEdits) {
      await api.name.patch(n.id, namePatch, this._editingNameEtag);
    }
    this._cancelCreate();
    if (moved) {
      // Synonym is no longer attached to the taxon the curator was
      // viewing — jump to the new accepted taxon so it stays in view.
      this.dispatchEvent(
        new CustomEvent("taxon-moved", {
          detail: { id: revealID },
          bubbles: true,
          composed: true,
        }),
      );
    } else {
      // Name-only change — refresh the local history so the updated
      // label renders in place without a full navigation.
      await this._refreshNomenHistory();
    }
  }

  // _buildCreateBody assembles the POST body for the create-taxon /
  // add-synonym / add-basionym endpoints. Adds the optional basionym
  // link fields (mutually exclusive):
  //   * basionym_name_id — set when the picker was used to link an
  //     existing name.
  //   * basionym — nested object when the inline subform is expanded
  //     for creating a new original-combination name in the same
  //     transaction.
  // Neither → today's behavior (no basionym link on write).
  _buildCreateBody() {
    const body = { ...this._createDraft };
    if (this._createBasionymInline) {
      // Inline-new wins over picker (mutually exclusive; UI clears
      // picker on inline-open, but be defensive).
      delete body.basionym_name_id;
      body.basionym = { ...this._createBasionymInline.draft };
    } else if (body.basionym_name_id) {
      // Picker set — send basionym_name_id verbatim.
    } else {
      delete body.basionym_name_id;
    }
    return body;
  }

  // _submitCreateThenBasionym is the "Create + add original combination"
  // path. Saves the current combination first (POST /api/taxon), then
  // transitions the same pane into basionym-add mode targeting the
  // just-created taxon. The curator lands on a fresh scientific-name
  // input; when they save that, the basionym write (POST
  // /api/taxon/{X}/basionym) fires and the pane closes.
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
      this._createLastParsedVerbatim = "";
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

  async _askDelete() {
    // Reset delete state, open the modal in a "loading" state, then
    // fetch the preview. The modal renders progressively — while the
    // preview is null we show a loading placeholder; once it arrives
    // we branch to either the leaf confirm or the parent's three-
    // option UI. See DESIGN.md § List-row actions / delete flow.
    this._deleteError = "";
    this._deleteMode = "";
    this._deleteConfirmText = "";
    this._deletePreview = null;
    this._confirmDelete = true;
    if (!this._taxon) return;
    try {
      this._deletePreview = await api.taxon.deletePreview(this._taxon.id);
    } catch (err) {
      this._deleteError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  _cancelDelete() {
    this._confirmDelete = false;
    this._deleteError = "";
    this._deletePreview = null;
    this._deleteMode = "";
    this._deleteConfirmText = "";
  }

  async _submitDelete() {
    if (!this._taxon) return;
    const preview = this._deletePreview;
    const isLeaf = preview && preview.direct_child_count === 0;
    try {
      let res;
      if (isLeaf || !preview) {
        // Leaf case (or preview never loaded — fall through to the
        // simple delete, which will 409 if the server sees children).
        res = await api.taxon.delete(this._taxon.id);
      } else if (this._deleteMode === "reparent") {
        res = await api.taxon.deleteReparent(this._taxon.id);
      } else if (this._deleteMode === "cascade") {
        // The Delete button only enables when this input equals
        // "DELETE" exactly, but guard here in case the button state
        // was bypassed (keyboard invocation of a disabled click).
        if (this._deleteConfirmText !== "DELETE") return;
        res = await api.taxon.deleteCascade(this._taxon.id);
      } else {
        return;
      }
      this._cancelDelete();
      // Reveal the parent (or clear selection if the deleted taxon
      // was a root) via the shell's move-handler pipeline.
      this.dispatchEvent(
        new CustomEvent("taxon-deleted", {
          detail: { deleted_id: res.deleted_id, parent_id: res.parent_id },
          bubbles: true,
          composed: true,
        }),
      );
    } catch (err) {
      this._deleteError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  _renderCreatePane() {
    // Parent label was captured at open time (new-child vs new-sister)
    // so the heading names the right ancestor regardless of any tree
    // selection changes since. Edit mode heads with the current
    // scientific-name so the curator sees which record they're
    // editing without needing to look at a separate context row.
    const parentLabel = this._createParentLabel || "(root)";
    const heading = this._editingSynonymID
      ? html`Edit synonym
          <em>
            ${this._editingOriginalName?.scientific_name ||
            this._editingSynonymID}
          </em>`
      : this._editingTaxonID
        ? html`Edit
            <em>
              ${this._editingOriginalName?.scientific_name ||
              this._editingOriginalTaxon?.label?.text ||
              this._editingTaxonID}
            </em>`
        : this._creatingBasionymFor
          ? html`Add original combination for
              <em>${this._creatingBasionymForName}</em>`
          : this._creatingSynonymFor
            ? html`Add synonym of
                <em>${this._creatingSynonymForName}</em>`
            : html`New taxon under ${parentLabel}`;
    return html`
      <div
        class="create-pane"
        @reference-updated=${(e) => this._onReferenceUpdated(e)}
      >
        <h2>${heading}</h2>
        <hr />
        ${this._createError
          ? html`<div class="error" role="alert">${this._createError}</div>`
          : ""}
        ${this._renderParseTailBanner()}
        ${this._renderEditingIssuesBanner()}
        ${this._renderCreateForm()}
      </div>
      ${this._renderEditReferenceModal()}
    `;
  }

  // _onReferenceUpdated fires when the reference-quick-fix modal
  // saves. Two things need refreshing:
  //   1. Every reference picker in the pane needs to re-resolve so
  //      its cached display + badge state reflect the fresh data.
  //      combobox.refresh() clears the resolver cache and triggers
  //      updated().
  //   2. Backfill re-runs so a freshly-filled author + issued
  //      populates the atomized combination / basionym fields on
  //      the current draft (the whole point of the inline fix).
  async _onReferenceUpdated(e) {
    const updatedID = e.detail?.id;
    if (!updatedID) return;
    // Refresh every combobox in this pane so any picker showing the
    // updated reference re-resolves. Cheap; no-ops for pickers with
    // a different value set.
    const boxes = this.renderRoot.querySelectorAll(
      ".create-pane sfga-combobox",
    );
    for (const box of boxes) {
      if (typeof box.refresh === "function" && box.value === updatedID) {
        box.refresh();
      }
    }
    // Re-run the backfill so newly-populated author + issued land
    // in the current draft.
    if (this._createDraft?.reference_id === updatedID) {
      try {
        const ref = await api.reference.get(updatedID);
        this._backfillCombinationFromCitation(ref);
      } catch (_) {
        /* silent */
      }
    }
    if (this._createBasionymInline?.draft?.reference_id === updatedID) {
      await this._onInlineBasionymReferencePick(updatedID);
    }
    // Basionym-side backfill if the updated reference belongs to the
    // linked basionym (basionym_name_id via cluster-`+` OR
    // _editingOriginalName.basionym.id in edit mode). Walks the
    // linked name's reference; refires when the ids match.
    const linkedID =
      this._editingOriginalName?.basionym?.id ||
      (this._createDraft?.basionym_name_id || "").trim();
    if (linkedID) {
      try {
        const linked = await api.name.get(linkedID);
        if (linked?.reference_id === updatedID) {
          await this._backfillPrimaryBasionymFromLinkedName(linkedID);
        }
      } catch (_) {
        /* silent */
      }
    }
  }

  // _renderParseTailBanner surfaces gnparser's "unparsed tail"
  // diagnostic at the top of the pane where curators can't miss it.
  // Two sources feed the banner and the fresher one wins:
  //
  //   1. Live: _createParseTail set from a re-parse this session.
  //      Non-null means "a parse ran"; "" means "clean parse, no
  //      tail" (banner suppressed); non-empty renders the banner.
  //
  //   2. Persisted: an existing hive_parse_tail row in
  //      __gsvalidator_results loaded on _openEditTaxon. Used only
  //      when no live parse has run (_createParseTail === null) so
  //      re-parsing a stale bad row instantly reflects the current
  //      gnparser's opinion.
  //
  // Placement at the top of the pane matches the name-editor modal's
  // issue-banner treatment and stays visible regardless of whether
  // the curator has expanded the atomized-fields section.
  _renderParseTailBanner() {
    let tail = "";
    let source = "";
    if (this._createParseTail !== null && this._createParseTail !== undefined) {
      if (this._createParseTail === "") return "";
      tail = this._createParseTail;
      source = "live";
    } else if (this._editingNameIssues && this._editingNameIssues.length > 0) {
      const persisted = this._editingNameIssues.find(
        (i) => i.rule_id === "hive_parse_tail",
      );
      if (!persisted) return "";
      // Persisted issues carry the tail on `actual_value` (set by
      // the hive.parse_tail validator's Result). Fall back to
      // extracting it from the message when actual_value is missing
      // for any reason.
      tail = persisted.actual_value || "";
      if (!tail) {
        const m = /Unparsed tail:\s*"([^"]*)"/.exec(persisted.message || "");
        tail = m ? m[1] : "";
      }
      source = "persisted";
    }
    if (!tail) return "";
    const note =
      source === "live"
        ? "gnparser could not fully parse this scientific name. Trailing text below wasn't recognized:"
        : "gnparser flagged this scientific name at save time. Trailing text below wasn't recognized:";
    return html`
      <div class="warning-banner" role="alert">
        <ul>
          <li>
            ${severityChip("warn")}
            <span>
              <span class="warning-rule">unparsed tail</span>:
              ${note}
              <code class="parse-tail-text">${tail}</code>
            </span>
          </li>
        </ul>
      </div>
    `;
  }

  // _renderEditingIssuesBanner surfaces every other persisted
  // validation issue on the name being edited (edit-taxon or
  // edit-synonym mode). The parse-tail issue is intentionally
  // excluded — it already has a dedicated banner just above with
  // richer formatting (the unparsed tail token gets its own
  // monospace pill).
  _renderEditingIssuesBanner() {
    if (!this._editingTaxonID && !this._editingSynonymID) return "";
    const all = this._editingNameIssues || [];
    const rest = all.filter((i) => i.rule_id !== "hive_parse_tail");
    if (rest.length === 0) return "";
    return html`
      <div class="warning-banner" role="status">
        <strong>
          ${rest.length} open
          issue${rest.length > 1 ? "s" : ""} on this name:
        </strong>
        <ul>
          ${rest.map(
            (i) => html`<li>
              ${severityChip(i.severity)}
              <span>
                <span class="warning-rule"
                  >${i.rule_name || i.rule_id}</span
                >:
                ${i.message}
                ${i.field_name
                  ? html` <span class="warning-rule"
                      >(${i.field_name})</span
                    >`
                  : ""}
              </span>
            </li>`,
          )}
        </ul>
      </div>
    `;
  }

  // _renderCreateForm renders the single-page create/add pane. The
  // scientific-name input sits at the top of the name fieldset; Enter
  // or blur triggers _reparseVerbatim which repopulates the atomized
  // fields nested inside the same fieldset. Curator-authored fields
  // (reference, remarks, synonym_status, basionym_name_id, code)
  // survive re-parse untouched — see _reparseVerbatim for the split.
  //
  // The form wraps its contents in a <form> so Enter in a plain text
  // input submits, matching the standard browser convention. Buttons
  // that shouldn't submit are marked type="button"; the primary Save
  // button is type="submit". The scientific-name input intercepts
  // Enter for re-parse instead. Textareas keep Enter=newline (default).
  _renderCreateForm() {
    const d = this._createDraft;
    const set = (f) => (e) => this._createFieldChange(f, e.target.value);
    return html`
      <form
        class="create-form"
        @submit=${(e) => {
          e.preventDefault();
          this._submitCreate();
        }}
      >
        <fieldset class="name-fieldset">
          <legend>Name</legend>
          <label>Scientific name <span class="req">*</span></label>
          <div class="sci-input-cell">
            <input
              class="sci-input"
              type="text"
              placeholder="e.g. Panthera onca (Linnaeus, 1758)"
              .value=${d.scientific_name || ""}
              @input=${(e) => this._createFieldChange("scientific_name", e.target.value)}
              @keydown=${(e) => this._onSciInputKeydown(e)}
              @blur=${(e) => this._reparseVerbatim(e.target.value)}
              autofocus
            />
            ${this._renderParseQualityGlyph(d.scientific_name || "")}
          </div>

          <!-- Verbatim authorship above Rank so Tab-blur from the
               scientific-name input lands here (the field most likely
               to hold a value the curator wants to review or edit
               after a parse). Rank is picker-only and typically stays
               correct once the parse populates it; keeping it below
               keeps the natural tab order aligned with review
               priority. -->
          <label>Verbatim authorship</label>
          <input
            class="authorship-input"
            type="text"
            .value=${d.authorship || ""}
            @input=${set("authorship")}
          />

          <label>Rank</label>
          <sfga-combobox
            min-search-chars="0"
            placeholder="Rank…"
            .source=${childRankSource(this._createChildRanks)}
            .resolver=${vocabResolver("rank")}
            .value=${d.rank || ""}
            @pick=${(e) => this._createFieldChange("rank", e.detail.id)}
          ></sfga-combobox>

          <label class="atomized-toggle" style="grid-column: 1 / -1">
            <input
              type="checkbox"
              .checked=${this._createShowAtomized}
              @change=${(e) => {
                this._createShowAtomized = e.target.checked;
                writeAtomizedPref(e.target.checked);
              }}
            />
            Show atomized fields
            <span class="hint">
              (verify or override the parse; hive parses in the
              background regardless)
            </span>
          </label>

          ${this._createShowAtomized
            ? this._renderAtomizedFieldset(d, set)
            : ""}
        </fieldset>

        ${this._creatingSynonymFor
          ? html`
              <fieldset>
                <legend>Synonym type</legend>
                <label>Type</label>
                <sfga-combobox
                  min-search-chars="0"
                  placeholder="synonym / ambiguous synonym / misapplied"
                  .source=${synonymStatusSource()}
                  .resolver=${vocabResolver("taxonomic_status")}
                  .value=${d.synonym_status || "SYNONYM"}
                  @pick=${(e) =>
                    this._createFieldChange("synonym_status", e.detail.id)}
                ></sfga-combobox>
              </fieldset>
            `
          : ""}

        <fieldset>
          <legend>Publication</legend>
          <label>${nomActFieldLabel(d)}</label>
          <sfga-combobox
            min-search-chars="2"
            placeholder="Search references…"
            .source=${referenceSource}
            .resolver=${referenceResolver}
            .value=${d.reference_id || ""}
            .valueName=${this._pickedCreateReferenceLabel !== undefined
              ? this._pickedCreateReferenceLabel
              : ""}
            .actions=${[
              {
                label: "Add new reference…",
                icon: "plus",
                handler: () => (this._addingReferenceFor = "create"),
              },
            ]}
            @pick=${(e) => this._onCreateReferencePick(e.detail.id)}
            @badge-click=${(e) =>
              this._openEditReference(e.detail.id || d.reference_id)}
          ></sfga-combobox>
          <label>Published in page</label>
          <input
            type="text"
            .value=${d.published_in_page || ""}
            @input=${set("published_in_page")}
          />
        </fieldset>

        <fieldset>
          <legend>Metadata</legend>
          <label>Nom status</label>
          <sfga-combobox
            placeholder="Nomenclatural status…"
            .source=${nomenSource(d.code)}
            .resolver=${nomenResolver}
            .value=${d.status || ""}
            @pick=${(e) => this._createFieldChange("status", e.detail.id)}
          ></sfga-combobox>
          <label>Etymology</label>
          <input
            type="text"
            .value=${d.etymology || ""}
            @input=${set("etymology")}
          />
          <label>Remarks</label>
          <textarea
            .value=${d.remarks || ""}
            @input=${set("remarks")}
          ></textarea>
        </fieldset>

        <!-- Nomenclatural code lives at the very bottom.
             CodeForParent pre-fills it from the parent's name so the
             common path never focuses this row; the affordance is here
             for the exceptions (root taxa, deliberate mixed-code
             subtrees like protists). Matches the TUI's placement. -->
        <fieldset>
          <legend>Nomenclatural code</legend>
          <label>Code</label>
          <sfga-combobox
            min-search-chars="0"
            placeholder="Nomenclatural code…"
            .source=${vocabSource("nom_code")}
            .resolver=${vocabResolver("nom_code")}
            .value=${d.code || ""}
            @pick=${(e) => this._createFieldChange("code", e.detail.id)}
          ></sfga-combobox>
        </fieldset>

        ${this._editingTaxonID ? this._renderEditTaxonFieldset(d, set) : ""}

        ${this._editingSynonymID ? this._renderEditSynonymFieldset() : ""}

        ${this._creatingBasionymFor
          ? ""
          : this._editingTaxonID || this._editingSynonymID
            ? this._renderNameRelationsFieldset()
            : this._renderOriginalCombinationSection(d, set)}

        <div class="toolbar">
          <button
            type="submit"
            class="primary"
            ?disabled=${this._createBusy}
          >
            ${this._createBusy
              ? this._editingTaxonID || this._editingSynonymID
                ? "Saving…"
                : "Creating…"
              : this._editingTaxonID || this._editingSynonymID
                ? "Save"
                : this._creatingBasionymFor
                  ? "Add basionym"
                  : this._creatingSynonymFor
                    ? "Add synonym"
                    : "Create"}
          </button>
          <button
            type="button"
            @click=${() => this._cancelCreate()}
            ?disabled=${this._createBusy}
          >
            Cancel
          </button>
        </div>
      </form>
      ${this._addingReferenceFor ? this._renderAddReferenceModal() : ""}
    `;
  }

  // _renderEditTaxonFieldset renders the taxon-side fields that only
  // appear in edit mode — parent picker, name-phrase, scrutinizer
  // trio, extinct, link, taxon-remarks. Values flow through
  // _createDraft (hydrated by _openEditTaxon); taxon-remarks lives at
  // draft.taxon_remarks to avoid colliding with the name-side
  // "remarks" field the create metadata fieldset owns.
  _renderEditTaxonFieldset(d, set) {
    const t = this._editingOriginalTaxon || {};
    const currentParentID = t.parent_id ?? "";
    const parentID =
      this._editingParentDraft !== null
        ? this._editingParentDraft
        : currentParentID;
    const extinctValue = d.extinct === undefined ? "" : d.extinct;
    return html`
      <fieldset>
        <legend>Taxon fields</legend>
        <label>Parent</label>
        <sfga-combobox
          min-search-chars="2"
          placeholder="Type to search, or leave empty for a top-level taxon"
          .source=${taxonSource}
          .resolver=${taxonResolver}
          .value=${parentID}
          .valueName=${this._editingParentDraft !== null
            ? this._editingParentDraftName
            : ""}
          @pick=${(e) => {
            this._editingParentDraft = e.detail.id;
            this._editingParentDraftName = e.detail.name;
          }}
        ></sfga-combobox>

        <label>Name phrase</label>
        <input
          type="text"
          .value=${d.name_phrase || ""}
          @input=${set("name_phrase")}
        />

        <label>Scrutinizer</label>
        <input
          type="text"
          .value=${d.scrutinizer || ""}
          @input=${set("scrutinizer")}
        />

        <label>Scrutinizer ID</label>
        <input
          type="text"
          placeholder="ORCID or other identifier"
          .value=${d.scrutinizer_id || ""}
          @input=${set("scrutinizer_id")}
        />

        <label>Scrutinizer date</label>
        <input
          type="date"
          .value=${d.scrutinizer_date || ""}
          @input=${set("scrutinizer_date")}
        />

        <label>Extinct</label>
        <select
          .value=${extinctValue}
          @change=${(e) => this._createFieldChange("extinct", e.target.value)}
        >
          <option value="">(unset)</option>
          <option value="true">yes</option>
          <option value="false">no</option>
        </select>

        <label>Link</label>
        <input
          type="text"
          .value=${d.link || ""}
          @input=${set("link")}
        />

        <label>Taxon remarks</label>
        <textarea
          .value=${d.taxon_remarks || ""}
          @input=${set("taxon_remarks")}
        ></textarea>
      </fieldset>
    `;
  }

  // _renderEditSynonymFieldset is the counterpart to
  // _renderEditTaxonFieldset for synonym-edit mode. Only surfaces the
  // synonym-only fields — currently just the accepted-taxon picker
  // for moving a synonym to a different accepted taxon (POST
  // /api/synonym/{id}/move on save). Empty picker keeps the current
  // attachment; picking a new taxon queues a move.
  //
  // taxonomic_status editing (SYNONYM / AMBIGUOUS_SYNONYM /
  // MISAPPLIED) is intentionally omitted from this MVP — the
  // apiNomenName projection doesn't carry the current value and no
  // PATCH /api/synonym endpoint exists. Curators wanting to change
  // the type today delete + re-add. When the backend gains synonym
  // PATCH, add the picker here (same shape as create-synonym mode
  // via synonymStatusSource()).
  _renderEditSynonymFieldset() {
    const currentTaxonID = this._editingAcceptedTaxonID || "";
    const draft = this._editingAcceptedTaxonDraft;
    const shown = draft !== null ? draft : currentTaxonID;
    return html`
      <fieldset>
        <legend>Synonym attachment</legend>
        <label>Accepted taxon</label>
        <sfga-combobox
          min-search-chars="2"
          placeholder="Search accepted taxa…"
          .source=${taxonSource}
          .resolver=${taxonResolver}
          .value=${shown}
          .valueName=${draft !== null ? this._editingAcceptedTaxonDraftName : ""}
          @pick=${(e) => {
            this._editingAcceptedTaxonDraft = e.detail.id;
            this._editingAcceptedTaxonDraftName = e.detail.name;
          }}
        ></sfga-combobox>
      </fieldset>
    `;
  }

  // _renderOriginalCombinationSection is the inline basionym-link
  // section at the bottom of the create form. Two paths through it:
  //   * Empty picker + inline form collapsed → save creates just the
  //     primary name (today's behavior).
  //   * Picker set → save links a BASIONYM relation to the picked
  //     name in the same tx.
  //   * "Create new" expanded → save creates the primary + the inline
  //     basionym name + the BASIONYM relation in the same tx.
  //
  // Hidden when we're already in basionym-add mode (avoid recursion —
  // a basionym doesn't get its own basionym via this pane).
  // Terminology: unified "original combination" for both codes; the
  // underlying sfga field is `name_relation.col__type_id = 'BASIONYM'`
  // — this is a display choice, not a schema choice.
  _renderOriginalCombinationSection(d, set) {
    const inline = this._createBasionymInline || null;
    const bDraft = inline?.draft || {};
    const bSet = (field) => (e) =>
      this._createBasionymInlineFieldChange(field, e.target.value);
    return html`
      <fieldset>
        <legend>Original combination (optional)</legend>
        ${inline
          ? html`
              <p class="hint" style="grid-column: 1 / -1; margin: 0 0 var(--sp-1) 0;">
                Creating a new original combination inline. Both records
                save together.
              </p>
              <label>Scientific name <span class="req">*</span></label>
              <input
                class="sci-input"
                type="text"
                placeholder="e.g. Aus bus L."
                .value=${bDraft.scientific_name || ""}
                @input=${bSet("scientific_name")}
                @keydown=${(e) => this._onInlineBasionymSciInputKeydown(e)}
                @blur=${(e) =>
                  this._reparseInlineBasionymVerbatim(e.target.value)}
              />
              <label>Verbatim authorship</label>
              <input
                type="text"
                .value=${bDraft.authorship || ""}
                @input=${bSet("authorship")}
              />
              <label>Rank</label>
              <sfga-combobox
                min-search-chars="0"
                placeholder="Rank…"
                .source=${vocabSource("rank")}
                .resolver=${vocabResolver("rank")}
                .value=${bDraft.rank || d.rank || ""}
                @pick=${(e) =>
                  this._createBasionymInlineFieldChange("rank", e.detail.id)}
              ></sfga-combobox>
              <label>Nom status</label>
              <sfga-combobox
                placeholder="Nomenclatural status…"
                .source=${nomenSource(bDraft.code || d.code)}
                .resolver=${nomenResolver}
                .value=${bDraft.status || ""}
                @pick=${(e) =>
                  this._createBasionymInlineFieldChange("status", e.detail.id)}
              ></sfga-combobox>
              <label>Original nomenclatural act citation</label>
              <sfga-combobox
                min-search-chars="2"
                placeholder="Search author / title / citation / DOI…"
                .source=${referenceSource}
                .resolver=${referenceResolver}
                .value=${bDraft.reference_id || ""}
                @pick=${(e) =>
                  this._onInlineBasionymReferencePick(e.detail.id)}
                @badge-click=${(e) =>
                  this._openEditReference(
                    e.detail.id || bDraft.reference_id,
                  )}
              ></sfga-combobox>
              <label>Published in page</label>
              <input
                type="text"
                .value=${bDraft.published_in_page || ""}
                @input=${bSet("published_in_page")}
              />
              <label>Etymology</label>
              <input
                type="text"
                .value=${bDraft.etymology || ""}
                @input=${bSet("etymology")}
              />
              <label>Remarks</label>
              <textarea
                .value=${bDraft.remarks || ""}
                @input=${bSet("remarks")}
              ></textarea>
              <label>Code</label>
              <sfga-combobox
                min-search-chars="0"
                placeholder="Nomenclatural code…"
                .source=${vocabSource("nom_code")}
                .resolver=${vocabResolver("nom_code")}
                .value=${bDraft.code || d.code || ""}
                @pick=${(e) =>
                  this._createBasionymInlineFieldChange("code", e.detail.id)}
              ></sfga-combobox>
              ${this._createShowAtomized
                ? this._renderAtomizedFieldset(bDraft, bSet)
                : ""}
              <div class="toolbar" style="grid-column: 1 / -1">
                <button
                  type="button"
                  @click=${() => this._cancelCreateBasionymInline()}
                >
                  Cancel this original combination
                </button>
              </div>
            `
          : html`
              <label>Link existing name</label>
              <sfga-combobox
                min-search-chars="2"
                placeholder="Search names in this archive…"
                .source=${nameSource}
                .resolver=${nameResolver}
                .value=${d.basionym_name_id || ""}
                .actions=${[
                  {
                    label: "Create new original combination…",
                    icon: "plus",
                    handler: () => this._openCreateBasionymInline(),
                  },
                ]}
                @pick=${(e) => this._onBasionymPickerPick(e.detail.id)}
              ></sfga-combobox>
              <p class="hint" style="grid-column: 1 / -1; margin: 0;">
                Leave empty if this name IS the original combination
                (no earlier basionym exists in this archive) or if you
                plan to add its original combination later.
              </p>
            `}
      </fieldset>
    `;
  }

  // _renderNameRelationsFieldset is the edit-mode counterpart to
  // _renderOriginalCombinationSection. Shows every outgoing
  // name_relation on the edited name as an unlink-able row and
  // offers a three-omnibox row (related name + type + optional
  // reference) to add a new relation. Sfga's name_relation has a
  // composite key (name_id + related_name_id + type_id), so
  // "changing" a relation's type is delete-then-add — this fieldset
  // enforces that via a bare unlink + fresh add rather than an
  // in-place edit path.
  //
  // Only OUTGOING relations render editing affordances — an incoming
  // relation is owned by the OTHER name's edit surface. The list
  // filters accordingly; the incoming set surfaces in the Nomen
  // History section instead.
  _renderNameRelationsFieldset() {
    const nameID = this._editingOriginalName?.id;
    if (!nameID) return "";
    const outgoing = (this._nameRelations || []).filter(
      (r) => r.direction === "outgoing",
    );
    const draft = this._nameRelationDraft || {};
    const canCommit =
      !!draft.related_name_id && !!draft.type && !this._nameRelationBusy;
    return html`
      <fieldset class="name-relations">
        <legend>Name relations</legend>
        ${outgoing.length === 0
          ? html`<p
              class="hint"
              style="grid-column: 1 / -1; margin: 0 0 var(--sp-2) 0;"
            >
              No relations recorded. Use the row below to add an
              original combination, replacement name, or other
              name-to-name link.
            </p>`
          : ""}
        ${outgoing.map((r) => this._renderNameRelationRow(r))}
        ${this._renderNameRelationDraftRow(draft, canCommit)}
      </fieldset>
    `;
  }

  // _renderNameRelationRow renders one persisted relation: type +
  // related name + optional reference + unlink X. All three fields
  // read-only (no in-place edit — see fieldset comment).
  _renderNameRelationRow(r) {
    const relatedID = r.related_name?.id || "";
    const relatedText =
      r.related_name?.label?.text || relatedID || "(missing name)";
    return html`
      <div class="name-relation-row persisted">
        <span class="rel-type" title="relation type">${r.type || ""}</span>
        <a
          class="rel-name"
          href="#/name/${relatedID}"
          @click=${(e) => this._onNameRelationRelatedClick(e, relatedID)}
          title="edit this name"
          >${relatedText}</a
        >
        <span class="rel-ref" title="citation">
          ${r.reference_label || (r.reference_id ? "(unresolved)" : "")}
        </span>
        <button
          class="icon-btn subtle danger"
          type="button"
          @click=${() => this._deleteNameRelation(r)}
          title="unlink this relation"
          aria-label="unlink relation"
        >
          ${renderIcon("x", 14)}
        </button>
      </div>
    `;
  }

  // _renderNameRelationDraftRow renders the three-omnibox row a
  // curator uses to add a new relation. Related name and type are
  // required; reference is optional. Progressive disclosure: the +
  // button lights up once both required fields are picked. On
  // commit, the row POSTs and a fresh blank draft replaces it —
  // "load another omnibox if it is populated" per the shared editor
  // pattern.
  _renderNameRelationDraftRow(draft, canCommit) {
    const refActions = [
      {
        label: "Add new reference",
        icon: "plus",
        handler: () => (this._addingReferenceFor = "name-relation"),
      },
    ];
    return html`
      <div class="name-relation-row draft">
        <sfga-combobox
          class="rel-name-picker"
          min-search-chars="2"
          placeholder="Related name…"
          .source=${nameSource}
          .resolver=${nameResolver}
          .value=${draft.related_name_id || ""}
          @pick=${(e) =>
            this._nameRelationDraftFieldChange(
              "related_name_id",
              e.detail.id,
            )}
        ></sfga-combobox>
        <sfga-combobox
          class="rel-type-picker"
          min-search-chars="0"
          placeholder="Type…"
          .source=${vocabSource("nom_rel_type")}
          .resolver=${vocabResolver("nom_rel_type")}
          .value=${draft.type || ""}
          @pick=${(e) =>
            this._nameRelationDraftFieldChange("type", e.detail.id)}
        ></sfga-combobox>
        <sfga-combobox
          class="rel-ref-picker"
          min-search-chars="2"
          placeholder="Citation (optional)…"
          .source=${referenceSource}
          .resolver=${referenceResolver}
          .value=${draft.reference_id || ""}
          .actions=${refActions}
          @pick=${(e) =>
            this._nameRelationDraftFieldChange(
              "reference_id",
              e.detail.id,
            )}
          @badge-click=${(e) =>
            this._openEditReference(e.detail.id || draft.reference_id)}
        ></sfga-combobox>
        <button
          class="icon-btn subtle"
          type="button"
          ?disabled=${!canCommit}
          @click=${() => this._commitNameRelationDraft()}
          title=${canCommit
            ? "add this relation"
            : "pick a related name and type first"}
          aria-label="add relation"
        >
          ${renderIcon("plus", 14)}
        </button>
      </div>
    `;
  }

  // _onNameRelationRelatedClick jumps to editing the related name.
  // Same behavior as the old _onLinkedBasionymClick — routes through
  // _openEditSynonym so the combined form opens with the related
  // name's fields hydrated.
  _onNameRelationRelatedClick(e, nameID) {
    e.preventDefault();
    if (!nameID) return;
    this._cancelCreate();
    this._openEditSynonym(nameID, "", this._taxon?.id || "");
  }

  // _loadNameRelations fetches the outgoing / incoming relation set
  // for the edited name. Silent on failure — the fieldset just
  // renders empty and the curator can retry by reopening the form.
  async _loadNameRelations(nameID) {
    if (!nameID) return;
    try {
      const resp = await api.name.relations(nameID);
      // Guard against a stale reply (curator switched to editing a
      // different name mid-flight).
      if (this._editingOriginalName?.id === nameID) {
        this._nameRelations = resp.items || [];
      }
    } catch (_) {
      /* leave list empty; curator can reopen to retry */
    }
  }

  _nameRelationDraftFieldChange(field, value) {
    this._nameRelationDraft = {
      ...(this._nameRelationDraft || {}),
      [field]: value,
    };
  }

  async _commitNameRelationDraft() {
    const nameID = this._editingOriginalName?.id;
    if (!nameID) return;
    const draft = this._nameRelationDraft || {};
    if (!draft.related_name_id || !draft.type) return;
    if (draft.related_name_id === nameID) {
      // Backend also rejects, but a client-side guard gives a
      // faster + clearer message.
      this._createError = "A name cannot have a relation to itself.";
      return;
    }
    this._nameRelationBusy = true;
    try {
      await api.name.createRelation(nameID, {
        related_name_id: draft.related_name_id,
        type: draft.type,
        reference_id: draft.reference_id || "",
      });
      this._nameRelationDraft = {
        related_name_id: "",
        type: "",
        reference_id: "",
      };
      await this._loadNameRelations(nameID);
    } catch (err) {
      this._createError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._nameRelationBusy = false;
    }
  }

  async _deleteNameRelation(r) {
    const nameID = this._editingOriginalName?.id;
    if (!nameID || !r?.id) return;
    const label =
      r.related_name?.label?.text || r.related_name?.id || "this relation";
    const ok = await confirmAction({
      heading: "Unlink relation?",
      message: `The "${r.type || "relation"}" link to "${label}" will be removed.`,
      actionLabel: "Unlink",
    });
    if (!ok) return;
    try {
      await api.nameRelation.delete(r.id);
      await this._loadNameRelations(nameID);
    } catch (err) {
      this._createError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  // _openCreateBasionymInline expands the inline basionym subform,
  // seeding it with the primary draft's code so the common ICN/ICZN
  // case doesn't need an extra pick. Clears any picker selection —
  // the two paths are mutually exclusive. Scientific-name stays
  // empty by default (see TAXON_EDITOR_PLAN.md § Non-goals — basionyms
  // usually live in a different genus than the recomb, so the primary
  // draft's prefix would fight the workflow more than it helps).
  _openCreateBasionymInline() {
    this._createBasionymInline = {
      draft: { code: this._createDraft?.code || "" },
    };
    this._createBasionymInlineLastParsedVerbatim = "";
    // Ensure the picker's basionym_name_id doesn't also submit.
    this._createFieldChange("basionym_name_id", "");
  }

  _cancelCreateBasionymInline() {
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
  }

  _createBasionymInlineFieldChange(field, value) {
    if (!this._createBasionymInline) return;
    this._createBasionymInline = {
      ...this._createBasionymInline,
      draft: { ...this._createBasionymInline.draft, [field]: value },
    };
  }

  // _onInlineBasionymReferencePick sets the inline basionym's
  // reference_id and backfills empty basionym_authorship /
  // basionym_authorship_year from the citation's author + year. The
  // inline subform IS an original combination — its citation is the
  // establishment reference, so the author + year map directly to
  // the basionym pair. Fill-empty-only preserves anything gnparser
  // already extracted from the verbatim.
  async _onInlineBasionymReferencePick(refID) {
    this._createBasionymInlineFieldChange("reference_id", refID);
    if (!refID) return;
    let ref;
    try {
      ref = await api.reference.get(refID);
    } catch (_) {
      return;
    }
    const inline = this._createBasionymInline;
    if (!inline || inline.draft.reference_id !== refID) return;
    const { author: refA, year: refY } = refCitationAuthorAndYear(ref);
    if (!refA && !refY) return;
    const d = inline.draft;
    const patch = {};
    if (!(d.basionym_authorship || "").trim() && refA) {
      patch.basionym_authorship = refA;
    }
    if (!(d.basionym_authorship_year || "").trim() && refY) {
      patch.basionym_authorship_year = refY;
    }
    if (Object.keys(patch).length > 0) {
      this._createBasionymInline = {
        ...inline,
        draft: { ...d, ...patch },
      };
    }
    // Mirror to the primary draft — the inline basionym IS this
    // primary recomb's original combination, so its author + year
    // fill the primary's basionym_* pair too (empty-only).
    const primary = this._createDraft;
    const primaryPatch = {};
    if (!(primary.basionym_authorship || "").trim() && refA) {
      primaryPatch.basionym_authorship = refA;
    }
    if (!(primary.basionym_authorship_year || "").trim() && refY) {
      primaryPatch.basionym_authorship_year = refY;
    }
    if (Object.keys(primaryPatch).length > 0) {
      this._createDraft = { ...primary, ...primaryPatch };
    }
  }

  // _reparseInlineBasionymVerbatim mirrors _reparseVerbatim but for
  // the inline original-combination subform. Same clear-derived /
  // preserve-authored contract, scoped to the basionym draft. Guarded
  // by _createBasionymInlineLastParsedVerbatim so blur/repeat-Enter
  // on unchanged text doesn't refire.
  async _reparseInlineBasionymVerbatim(rawSci) {
    if (!this._createBasionymInline) return;
    const sci = (rawSci || "").trim();
    if (!sci) return;
    if (sci === this._createBasionymInlineLastParsedVerbatim) return;
    this._createBusy = true;
    this._createError = "";
    try {
      const code = this._createBasionymInline.draft?.code || "";
      const preview = await api.name.parse(sci, code);
      const merged = { ...this._createBasionymInline.draft };
      for (const f of SfgaDetail._PARSER_DERIVED_FIELDS) {
        merged[f] = preview[f] || "";
      }
      merged.scientific_name = sci;
      if (!merged.rank && preview.rank) merged.rank = preview.rank;
      this._createBasionymInline = {
        ...this._createBasionymInline,
        draft: merged,
      };
      this._createBasionymInlineLastParsedVerbatim = sci;
    } catch (err) {
      this._createError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._createBusy = false;
    }
  }

  _onInlineBasionymSciInputKeydown(e) {
    if (e.isComposing) return;
    if (e.key === "Enter") {
      e.preventDefault();
      this._reparseInlineBasionymVerbatim(e.target.value);
    }
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
        <legend>Atomized name</legend>
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
          <legend>Basionym (original)</legend>
          <label>Author</label>
          <input type="text" .value=${d.basionym_authorship || ""} @input=${set("basionym_authorship")} />
          <label>Year</label>
          <input type="text" .value=${d.basionym_authorship_year || ""} @input=${set("basionym_authorship_year")} />
        </fieldset>
        <fieldset>
          <legend>Combination (current)</legend>
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
    // Read from the *current* form values when the create/edit pane
    // is open — a curator who has been typing the scientific name
    // gets a BHLnames match on what's on-screen, not the pre-edit
    // form. Falls back to the loaded name row for callers outside
    // the pane (Nomenclatural history rows, distribution/vernacular
    // modals).
    const draftSci =
      this._creating && this._createDraft
        ? this._createDraft.scientific_name ||
          this._createDraft.scientific_name_string ||
          ""
        : "";
    const canonical =
      this._name?.canonical_simple ||
      draftSci ||
      this._name?.scientific_name ||
      "";
    const authors = this._name?.authors || "";
    const year = parseInt(this._name?.published_in_year || "", 10) || 0;
    // Name-editor / taxon-create callers most often want to reach for
    // the protologue paper — Project search is the right first tab.
    // Non-name callers (vernacular row, section-level References +)
    // are usually adding a citing paper the curator has a DOI for;
    // land them on the DOI tab, or on the Project tab in DOI-aware
    // mode where a DOI-shaped query auto-falls-through to OpenAlex.
    // Non-name contexts (vernacular, distribution, section-level
    // References) default to the DOI tab because the curator most
    // likely has a paper reference to cite. Name-editing paths (the
    // taxon edit form's name section, taxon-create pane, and the
    // pencil-edit name-editor) default to Project search — for names
    // the protologue paper is often already in the archive.
    const nonName =
      this._addingReferenceFor === "section" ||
      this._addingReferenceFor === "vernacular" ||
      this._addingReferenceFor === "distribution" ||
      this._addingReferenceFor === "species-interaction" ||
      this._addingReferenceFor === "name-relation";
    return html`
      <sfga-add-reference-modal
        .contextCanonical=${canonical}
        .contextAuthors=${authors}
        .contextYear=${year}
        .defaultTab=${nonName ? "doi" : "project"}
        @reference-picked=${(e) => this._onReferencePicked(e)}
        @close=${() => (this._addingReferenceFor = "")}
      ></sfga-add-reference-modal>
    `;
  }

  _onReferencePicked(e) {
    // Modal supplies {id, label}. Call sites:
    //   * "create" — create/edit pane picked / added a reference
    //     (edit mode reuses the create-pane draft, so same target).
    //   * "vernacular" / "distribution" — per-row modals.
    //   * "section" — References section's + button; no form to route
    //     the pick into (the reference is already in the archive;
    //     it'll appear in the References section once some row cites
    //     it).
    const { id, label } = e.detail;
    const target = this._addingReferenceFor;
    if (target === "create") {
      this._pickedCreateReferenceLabel = label || "";
      // Route through _onCreateReferencePick so a freshly-added
      // reference gets the same combination-backfill treatment as a
      // picked existing one.
      this._onCreateReferencePick(id);
    } else if (target === "vernacular") {
      this._vernacularFieldChange("reference_id", id);
    } else if (target === "distribution") {
      this._distributionFieldChange("reference_id", id);
    } else if (target === "species-interaction") {
      this._speciesInteractionFieldChange("reference_id", id);
    } else if (target === "name-relation") {
      this._nameRelationDraftFieldChange("reference_id", id);
    }
    this._addingReferenceFor = "";
  }

  _renderDeleteModal() {
    const label = this._taxon?.label?.text || this._taxon?.id || "(unknown)";
    const preview = this._deletePreview;
    const isLeaf = preview && preview.direct_child_count === 0;
    const isParent = preview && preview.direct_child_count > 0;
    const isRoot = preview && !preview.parent_id;
    // The Delete button enables when: leaf, or reparent chosen, or
    // cascade chosen with the literal string "DELETE" typed. Guards
    // against accidental clicks on the destructive path.
    const canDelete =
      isLeaf ||
      (isParent && this._deleteMode === "reparent") ||
      (isParent &&
        this._deleteMode === "cascade" &&
        this._deleteConfirmText === "DELETE");
    return html`
      <div class=${this._backdropClass()} @click=${() => this._cancelDelete()}>
        <div
          class="modal delete-modal"
          role="alertdialog"
          aria-modal="true"
          aria-labelledby="delete-confirm-title"
          @click=${(e) => e.stopPropagation()}
        >
          <h3 id="delete-confirm-title">Delete ${label}?</h3>
          ${!preview && !this._deleteError
            ? html`<div class="empty" role="status">Loading…</div>`
            : ""}
          ${isLeaf
            ? html`<p>
                Deleting removes the taxon and its per-taxon associations
                (synonyms, vernaculars, distributions). Names are shared and
                remain intact.
              </p>`
            : ""}
          ${isParent ? this._renderDeleteParentOptions(preview, isRoot) : ""}
          ${this._deleteError
            ? html`<div class="error" role="alert">${this._deleteError}</div>`
            : ""}
          <div class="toolbar">
            <button @click=${() => this._cancelDelete()}>Cancel</button>
            <button
              class="danger"
              ?disabled=${!canDelete}
              @click=${() => this._submitDelete()}
            >
              Delete
            </button>
          </div>
        </div>
      </div>
    `;
  }

  // _renderDeleteParentOptions renders the three-option UI for the
  // "taxon has children" case: re-parent (safer, keeps children;
  // hidden as unavailable only visually if root — the option still
  // reads as "children become new top-level taxa") or cascade (with
  // typed-DELETE confirmation and per-table summary).
  _renderDeleteParentOptions(preview, isRoot) {
    const descendants = preview.descendant_count - 1;
    return html`
      <p>
        This taxon has ${preview.direct_child_count} direct
        ${preview.direct_child_count === 1 ? "child" : "children"}${descendants > preview.direct_child_count
          ? html` (${descendants} descendants total)`
          : ""}.
      </p>
      <div
        class="delete-options"
        role="radiogroup"
        aria-label="Choose how to handle children"
      >
        <label>
          <input
            type="radio"
            name="delete-mode"
            value="reparent"
            .checked=${this._deleteMode === "reparent"}
            @change=${() => (this._deleteMode = "reparent")}
          />
          <span>
            ${isRoot
              ? html`Move children up — they become new top-level taxa`
              : html`Re-parent children — they move up one level`}
          </span>
        </label>
        <label>
          <input
            type="radio"
            name="delete-mode"
            value="cascade"
            .checked=${this._deleteMode === "cascade"}
            @change=${() => (this._deleteMode = "cascade")}
          />
          <span>Delete this taxon and all descendants</span>
        </label>
        ${this._deleteMode === "cascade"
          ? this._renderDeleteCascadeConfirm(preview)
          : ""}
      </div>
    `;
  }

  _renderDeleteCascadeConfirm(preview) {
    // Only show non-zero rows so the summary reads as a real
    // inventory rather than a table of mostly-zeros.
    const rows = [
      ["taxa (this + descendants)", preview.descendant_count],
      ["distribution records", preview.distribution_count],
      ["vernacular names", preview.vernacular_count],
      ["synonyms", preview.synonym_count],
      ["media", preview.media_count],
      ["treatments", preview.treatment_count],
      ["species estimates", preview.species_estimate_count],
      ["taxon properties", preview.taxon_property_count],
      ["species interactions", preview.species_interaction_count],
      ["taxon-concept relations", preview.taxon_concept_relation_count],
    ].filter(([, n]) => n > 0);
    return html`
      <div class="cascade-summary">
        <p class="cascade-heading">⚠ Cascade will delete:</p>
        <ul>
          ${rows.map(
            ([lab, count]) => html`<li>${count} ${lab}</li>`,
          )}
        </ul>
        <label class="cascade-confirm">
          <span>Type <strong>DELETE</strong> to confirm:</span>
          <input
            type="text"
            .value=${this._deleteConfirmText}
            @input=${(e) => (this._deleteConfirmText = e.target.value)}
            autocomplete="off"
            spellcheck="false"
            aria-label="Type DELETE to confirm cascade delete"
          />
        </label>
      </div>
    `;
  }

  render() {
    // Create mode takes over the whole pane — hoisted above the
    // no-selection guard because the empty-archive flow needs to open
    // the create pane without any selected taxon (a curator with a
    // brand-new archive clicks "Add first taxon" and lands here).
    if (this._creating) {
      return this._renderCreatePane();
    }
    if (!this.taxonId) {
      return html`<div class="empty">Select a taxon to see its details.</div>`;
    }
    if (this._error) {
      return html`<div class="error" role="alert">${this._error}</div>`;
    }
    if (this._loading && !this._taxon) {
      return html`<div class="empty" role="status">Loading…</div>`;
    }
    if (!this._taxon) return html``;

    // Server-rendered label: text + html forms. html carries dagger and
    // italics per rank (BuildLabel). Fall back to canonical / (no name)
    // if the server didn't attach a label (legacy or missing name row).
    const heading = this._taxon.label?.html
      ? html`${unsafeHTML(this._taxon.label.html)}`
      : this._name?.canonical_simple ||
        this._name?.scientific_name ||
        "(No name)";

    // Taxon name gets the full pane width now that action buttons live
    // in the app header (see DESIGN.md § Screen actions). Long
    // scientific names + authorships wrap cleanly without an action
    // strip stealing horizontal room from them. Classification
    // breadcrumbs render on their own line below the horizontal rule
    // — separated from the name so neither has to compete with the
    // other for horizontal space, and the eye still lands on the
    // name first. See DESIGN.md § Breadcrumbs / classification path.
    //
    // Reference accumulator reset at the top of every render so
    // superscript numbers stay consistent with the References section
    // rendered at the bottom. Populated as _renderNomenclaturalHistory
    // (later: distribution / vernacular / interaction sections) walks
    // rows in visual order; _renderReferences reads the finished map.
    this._refCites = new Map();
    // Wrapping div carries the @reference-updated listener so a
    // curator opening the reference-quick-fix modal from a per-row
    // modal (vernacular / distribution / species-interaction / name
    // relation) still gets picker-badge + backfill refresh on save.
    // The event bubbles up from sfga-add-reference-modal through the
    // per-row modal to this element; the wrapper catches it either
    // way.
    return html`
      <div
        class="detail-root"
        @reference-updated=${(e) => this._onReferenceUpdated(e)}
      >
        <div class="detail-header">
          <h2>${heading}</h2>
        </div>
        <hr />
        ${this._renderBreadcrumbs()}
        ${this._renderPendingWarnings()}
        ${this._renderNomenclaturalHistory()}
        ${this._renderVernaculars()}
        ${this._renderDistributions()}
        ${this._renderSpeciesInteractions()}
        ${this._renderReferences()}
        ${this._renderViewFields()}
        ${this._vernacularForm ? this._renderVernacularModal() : ""}
        ${this._distributionForm ? this._renderDistributionModal() : ""}
        ${this._speciesInteractionForm ? this._renderSpeciesInteractionModal() : ""}
        ${this._synonymDelete ? this._renderSynonymDeleteModal() : ""}
        ${this._addingReferenceFor === "section" ||
        this._addingReferenceFor === "vernacular" ||
        this._addingReferenceFor === "distribution" ||
        this._addingReferenceFor === "species-interaction"
          ? this._renderAddReferenceModal()
          : ""}
        ${this._renderEditReferenceModal()}
        ${this._vocabEditor
          ? html`<sfga-vocab-editor
              .vocab=${this._vocabEditor}
              @close=${() => this._closeVocabEditor()}
            ></sfga-vocab-editor>`
          : ""}
      </div>
    `;
  }

  // _sectionHeader renders the shared "<hr /> + heading + optional +
  // button" pattern used above every taxon-detail section. `add` is
  // an optional {handler, title} — omit it to render just the heading
  // with no button. See DESIGN.md § Add affordance and the CSS in
  // this element's static styles.
  _sectionHeader(label, add) {
    return html`
      <hr />
      <div class="section-header">
        <h3>${label}</h3>
        ${add
          ? html`
              <button
                class="icon-btn subtle add"
                @click=${add.handler}
                title=${add.title}
                aria-label=${add.title}
              >
                ${renderIcon("plus", 16)}
              </button>
            `
          : ""}
      </div>
    `;
  }

  // _citeRef assigns a superscript number to a reference id — the next
  // integer in order of first appearance on the page. Idempotent: calling
  // it twice with the same id returns the same number. Returns a Lit
  // template of the `[N]` superscript ready to inline next to whatever
  // row cited the reference. Returns "" for empty ids so callers can
  // unconditionally sprinkle it into their row templates.
  //
  // reference_id may be a comma-separated list of ids per the sfga
  // convention; hive v1 cites just the first id (matches how the wire
  // format's reference_label resolves — see synonymHitToAPI /
  // taxonToAPI). Multi-reference citations track as a later slice.
  _citeRef(id, label) {
    if (!id) return "";
    const primary = id.split(",")[0].trim();
    if (!primary) return "";
    let entry = this._refCites.get(primary);
    if (!entry) {
      entry = { n: this._refCites.size + 1, label: label || primary };
      this._refCites.set(primary, entry);
    }
    return html`<a class="cite" href="#ref-${entry.n}" title=${entry.label}
      >[${entry.n}]</a
    >`;
  }

  // _renderReferences draws the numbered References section at page
  // bottom — one row per distinct reference cited above. Hidden entirely
  // when nothing was cited so pages with no reference-bearing rows
  // don't accumulate an empty heading.
  _renderReferences() {
    if (!this._refCites || this._refCites.size === 0) return "";
    const entries = Array.from(this._refCites.values()).sort(
      (a, b) => a.n - b.n,
    );
    return html`
      <section class="references">
        ${this._sectionHeader("References", {
          handler: () => (this._addingReferenceFor = "section"),
          title:
            "add reference to archive (appears here once cited)",
        })}
        <ol>
          ${entries.map(
            (e) => html`
              <li id="ref-${e.n}">
                <span class="refnum" aria-hidden="true">${e.n}.</span>
                <span>${e.label}</span>
              </li>
            `,
          )}
        </ol>
      </section>
    `;
  }

  // _renderBreadcrumbs draws the classification path above the taxon
  // heading — root down, excluding the taxon itself (which is the
  // heading). Each ancestor is a clickable link that dispatches
  // taxon-selected to navigate the shell to that taxon. Solves the
  // "I can't see my parents while scrolled deep in a large group"
  // problem from the detail-page side; complements the tree pane's
  // sticky-ancestor waterfall. See DESIGN.md § Breadcrumbs /
  // classification path.
  _renderBreadcrumbs() {
    const chain = this._classification || [];
    if (chain.length <= 1) return ""; // root taxon has no ancestors
    const ancestors = chain.slice(0, -1);
    return html`
      <nav class="breadcrumbs" aria-label="Classification">
        ${ancestors.map(
          (a, i) => html`
            <a
              class="crumb"
              href="#/taxon/${a.id}"
              @click=${(e) => this._onBreadcrumbClick(e, a.id)}
              title=${a.rank || ""}
              >${a.name}</a
            >${i < ancestors.length - 1
              ? html`<span class="crumb-sep" aria-hidden="true">▸</span>`
              : ""}
          `,
        )}
      </nav>
    `;
  }

  _onBreadcrumbClick(e, id) {
    // Left-click without modifiers navigates in-app via the shell's
    // taxon-selected event (same path as tree-row selection).
    // Modifier-clicks (Ctrl/Cmd/middle) fall through to the browser
    // so open-in-new-tab / open-in-new-window still work — the
    // href="#/taxon/{id}" resolves to a real URL the app routes
    // to on load.
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button === 1) return;
    e.preventDefault();
    this.dispatchEvent(
      new CustomEvent("taxon-selected", {
        detail: { id },
        bubbles: true,
        composed: true,
      }),
    );
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
    if (fresh.length === 0 || this._creating) return "";
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

  // renderHeaderActions is the public contract SfgaApp calls to project
  // this screen's actions into the app header (see DESIGN.md § Screen
  // actions). Returns the four-button strip (edit / new-child /
  // new-sister / delete) or "" when no taxon is loaded or the pane is
  // in edit/create mode. Classes use the "subtle" variant so they
  // inherit the borderless header treatment from SfgaApp's buttonStyles;
  // the rendered markup lives in SfgaApp's shadow DOM, so component-
  // local styles do not apply.
  renderHeaderActions() {
    if (!this.editable) return "";
    if (this._creating) return "";
    if (!this._taxon) return "";
    return html`
      <button
        class="icon-btn subtle"
        @click=${() => this._openEditTaxon()}
        title="edit (e)"
        aria-label="edit"
      >
        ${renderIcon("pencil", 18)}
      </button>
      <button
        class="icon-btn subtle"
        @click=${() => this._openCreate()}
        title="new child (n) — adds under this taxon"
        aria-label="new child"
      >
        ${renderIcon("tree-child-plus", 18)}
      </button>
      <button
        class="icon-btn subtle"
        @click=${() => this._openCreateSister()}
        title="new sister — adds at the same level"
        aria-label="new sister"
      >
        ${renderIcon("tree-sister-plus", 18)}
      </button>
      <button
        class="icon-btn subtle danger"
        @click=${() => this._askDelete()}
        title="delete (d)"
        aria-label="delete"
      >
        ${renderIcon("trash-2", 18)}
      </button>
    `;
  }

  // performAction is the public entry called by SfgaApp when the user
  // triggers an action from a tree-row button. Awaits any in-flight
  // load (set by updated() when taxonId changed) so the action fires
  // against fully-loaded taxon state rather than a partially-populated
  // component. See DESIGN.md § List-row actions.
  async performAction(action) {
    if (this._loadPromise) {
      try { await this._loadPromise; } catch (_) { /* fall through */ }
    }
    if (!this._taxon) return;
    switch (action) {
      case "edit": this._openEditTaxon(); break;
      case "new-child": this._openCreate(); break;
      case "new-sister": this._openCreateSister(); break;
      case "delete": this._askDelete(); break;
    }
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
      <section class="all-fields">
        ${this._sectionHeader("All fields")}
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
      </section>
      ${this._confirmDelete ? this._renderDeleteModal() : ""}
    `;
  }

  // _renderNomenclaturalHistory draws the taxon's naming history from
  // the multi-cluster projection at GET /api/taxon/{id}/nomenclatural-
  // history. See DESIGN.md § Nomenclatural history section.
  //
  // Cluster / glyph / indent rules:
  //   * Accepted cluster (exactly one per response):
  //       - Accepted name (involvement="accepted"): flush left, no glyph.
  //       - Basionym (is_basionym, not the accepted): indent 1, ≡ glyph.
  //       - Other members (recombs of the basionym): indent 2, = glyph.
  //         When accepted IS its own basionym (self-anchor, no separate
  //         basionym row), these bump to indent 1 so they still sit
  //         directly under the accepted rather than hanging off nothing.
  //   * Synonym cluster (one per distinct basionym family):
  //       - Basionym: indent 1, = glyph.
  //       - Other members (recombs of that basionym): indent 2, = glyph.
  //
  // Cluster ordering (accepted first, then synonym clusters by
  // basionym year, then alphabetical) and within-cluster ordering
  // (basionym-first, then chronological, then alphabetical) come from
  // the backend — no client-side re-sort.
  //
  // Every row's reference_id (the name's own publication citation)
  // feeds _citeRef so a [N] superscript renders inline and the row
  // shows up in the References section at page bottom.
  _renderNomenclaturalHistory() {
    const clusters = this._nomenHistory?.clusters || [];
    if (clusters.length === 0) return "";
    return html`
      <section class="nomen-history">
        ${this._sectionHeader("Nomenclatural history", {
          handler: () => this._addNomenEntry(),
          title: "add synonym",
        })}
        ${this._renderStandardizedAuthorshipToggle()}
        <ul>
          ${clusters.map((c) => this._renderNomenCluster(c))}
        </ul>
      </section>
    `;
  }

  // _renderStandardizedAuthorshipToggle draws the small chip that flips
  // between the code-conventional pre-formatted Authorship string and
  // the hybrid "(basionym_author, basionym_year) combination_author,
  // combination_year" render. Persist state to localStorage so the
  // preference carries across sessions. Hotkey is `a` when the taxa
  // screen has focus and the curator isn't typing — see SfgaApp's
  // _onGlobalKey.
  _renderStandardizedAuthorshipToggle() {
    const on = !!this._standardizedAuthorship;
    return html`
      <div class="nomen-toolbar">
        <button
          class=${"chip" + (on ? " on" : "")}
          role="switch"
          aria-checked=${on ? "true" : "false"}
          title=${"Standardized authorship — hybrid render that keeps both the basionym year (ICZN convention) and the combination author (ICN convention) on every row. Full nomenclatural-act context, code-agnostic. Toggle with `a`."}
          @click=${() => this.toggleStandardizedAuthorship()}
        >
          <span class="chip-check" aria-hidden="true">${on ? "✓" : "○"}</span>
          <span>Standardized authorship</span>
        </button>
      </div>
    `;
  }

  // toggleStandardizedAuthorship flips the standardized-authorship
  // preference. Public (unprefixed name) because SfgaApp calls it
  // from the global keydown handler when `a` is pressed on the taxa
  // screen.
  toggleStandardizedAuthorship() {
    this._standardizedAuthorship = !this._standardizedAuthorship;
    try {
      localStorage.setItem(
        "hive-standardized-authorship",
        this._standardizedAuthorship ? "true" : "false",
      );
    } catch (_) {
      // localStorage unavailable (private mode / quota) — preference
      // is session-only, which is fine.
    }
  }

  // _addNomenEntry handles the Nomenclatural History + button — opens
  // the create pane in synonym-add mode targeting the currently viewed
  // taxon as the accepted-name link.
  _addNomenEntry() {
    this._openCreateSynonym();
  }

  // _openCreateSynonym opens the create pane in synonym-add mode. Reuses
  // the same two-step (verbatim → atomized preview) form as new-taxon
  // create; on submit the pane calls api.taxon.addSynonym with the
  // accepted taxon id captured at open time. See DESIGN.md § Add
  // affordance.
  async _openCreateSynonym(opts = {}) {
    const taxonID = this._taxon?.id || "";
    if (!taxonID) return;
    const taxonLabel =
      this._taxon?.label?.text || this._taxon?.name || this._taxon?.id || "";
    // Seed the code from the accepted name so ICZN stays ICZN etc.
    // Curator can override on step 1 for the odd case (e.g. an ICN
    // name synonymised into a mixed-code project). Synonym type
    // defaults to SYNONYM (the overwhelming common case); curator
    // can flip to AMBIGUOUS_SYNONYM or MISAPPLIED on step 1.
    // basionymNameID (optional) pre-populates the original-combination
    // picker at the bottom of the form — the cluster-`+` shortcut in
    // Nomen History uses this to skip the picker step.
    const code = this._name?.code || "";
    const draft = {
      scientific_name: "",
      code,
      synonym_status: "SYNONYM",
    };
    if (opts.basionymNameID) {
      draft.basionym_name_id = opts.basionymNameID;
    }
    this._createDraft = draft;
    this._createError = "";
    this._createBusy = false;
    this._createLastParsedVerbatim = "";
    this._createParseQuality = null;
    this._createParseTail = null;
    this._pickedCreateReferenceLabel = undefined;
    // Parent doesn't apply to synonym mode; clear so the render's
    // parent-picker code path is bypassed cleanly.
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = [];
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = taxonID;
    this._creatingSynonymForName = taxonLabel;
    this._createBasionymInline = null;
    this._createBasionymInlineLastParsedVerbatim = "";
    this._editingTaxonID = "";
    this._editingTaxonEtag = "";
    this._editingNameEtag = "";
    this._editingOriginalTaxon = null;
    this._editingOriginalName = null;
    this._editingParentDraft = null;
    this._editingParentDraftName = "";
    this._editingNameIssues = [];
    this._editingSynonymID = "";
    this._editingAcceptedTaxonID = "";
    this._editingAcceptedTaxonDraft = null;
    this._editingAcceptedTaxonDraftName = "";
    this._nameRelations = [];
    this._nameRelationDraft = { related_name_id: "", type: "", reference_id: "" };
    this._nameRelationBusy = false;
    this._vocabEditor = "";
    this._editRefID = "";
    this._creating = true;
    // Fire the on-open backfill so a pre-populated basionym_name_id
    // (from the cluster-`+` shortcut in Nomen History) pulls
    // basionym_authorship_year from the linked basionym's own
    // reference. No primary reference yet in create mode, so the
    // reference-side branch inside _maybeBackfillOnOpen no-ops.
    this._maybeBackfillOnOpen();
  }

  _renderNomenCluster(cluster) {
    const isAccepted = cluster.role === "accepted";
    const names = cluster.names || [];
    // Detect whether the cluster has a separate basionym row from the
    // accepted (only meaningful for the accepted cluster; when accepted
    // is its own basionym, is_basionym and involvement="accepted"
    // coincide and there's no separate anchor).
    const hasSeparateBasionym = names.some(
      (n) => n.is_basionym && n.involvement !== "accepted",
    );
    return names.map((n) => {
      const isAcceptedName = isAccepted && n.involvement === "accepted";
      const isBasionymRow = n.is_basionym && !isAcceptedName;
      let cls, glyph;
      if (isAcceptedName) {
        // ✓ marks the currently accepted name — matches TaxonWorks and
        // signals "you land here" at a glance without breaking the
        // aligned column of glyphs above and below.
        cls = "history accepted";
        glyph = "✓";
      } else if (isBasionymRow) {
        // Cluster anchor (basionym). Homotypic-vs-heterotypic
        // distinction relative to the accepted:
        //   * Accepted cluster's basionym → homotypic with accepted → ≡
        //   * Any other cluster's basionym → heterotypic to accepted → =
        cls = "history";
        glyph = isAccepted ? "≡" : "=";
      } else {
        // Non-anchor, non-accepted row: a recombination sharing this
        // cluster's basionym → homotypic with the anchor above it → ≡.
        // Applies uniformly whether the cluster is the accepted cluster
        // or a heterotypic-synonym cluster — either way, "same basionym"
        // means "same type" means "homotypic." Indent 2 when there's a
        // separate anchor row above; indent 1 when the accepted is its
        // own basionym (no anchor line above to hang off).
        cls = hasSeparateBasionym ? "history nested" : "history";
        glyph = "≡";
      }
      const cite = this._citeRef(n.reference_id, n.reference_label);
      // Two render paths for the label:
      //   * default — the server-formatted code-conventional label
      //     (ICZN-style parenthetical for recombs; ICN-style two-author
      //     chain for basionym→recomb pairs).
      //   * standardized — hybrid canonical + (basionym, basionym_year)
      //     combination, combination_year that carries the full nom-act
      //     context regardless of code, falling back to whichever
      //     atomized pieces are populated.
      const nameCell = this._standardizedAuthorship
        ? this._renderStandardizedAuthorshipLabel(n)
        : renderLabel(n.label, n.name_id);
      return html`<li class=${cls}>
        <span class="glyph" aria-hidden="true">${glyph}</span>
        <span>${nameCell}${cite}</span>
        ${this._renderNomenRowActions(n, cluster, isAcceptedName)}
      </li>`;
    });
  }

  // _renderStandardizedAuthorshipLabel composes the hybrid render for
  // one NomenName. Formula:
  //
  //   <canonical HTML from BuildLabel>  ← italics preserved
  //   [ (basionym_author[, basionym_year]) ] [ combination_author[, combination_year] ]
  //
  // Empty pieces are elided so the render degrades gracefully on rows
  // where the archive only carries some of the atomized authorship.
  // The canonical portion is extracted from the server-supplied
  // label.html — BuildLabel renders "<i>Canonical</i> Authorship" for
  // ranks that italicize, so we strip the trailing " Authorship" and
  // keep only the italicized canonical span. When canonical HTML can't
  // be cleanly extracted (unusual), fall back to label.text minus its
  // authorship suffix, which is the same value the server used.
  _renderStandardizedAuthorshipLabel(n) {
    const label = n.label || {};
    const authorship = n.authorship || "";
    // Detect a subsequent combination: only recombs get the
    // "(basionym) combination" bracketing. Originals render as a
    // single author line — even when COL's data has both basionym_*
    // and combination_* populated (they're the same author, just
    // stored redundantly).
    const isRecomb = authorship.startsWith("(");
    const canonicalHTML = stripAuthorshipFromLabelHTML(label.html || "", authorship);
    const canonicalNode = canonicalHTML
      ? html`${unsafeHTML(canonicalHTML)}`
      : label.text || n.name_id;

    // COL stores multi-author atomized fields with pipe separators
    // (e.g. "E.J.Palmer|Steyerm."). Normalize to "A & B" display form.
    const bAuth = normalizeAuthorField(n.basionym_authorship);
    const bYear = (n.basionym_authorship_year || "").trim();
    const cAuth = normalizeAuthorField(n.combination_authorship);
    const cYear = (n.combination_authorship_year || "").trim();

    let suffix = "";
    if (isRecomb) {
      // Recomb: (basionym[, year]) combination[, year] — either half
      // may be empty and elides gracefully.
      const parts = [];
      if (bAuth || bYear) {
        parts.push(`(${[bAuth, bYear].filter(Boolean).join(", ")})`);
      }
      if (cAuth || cYear) {
        parts.push([cAuth, cYear].filter(Boolean).join(", "));
      }
      suffix = parts.join(" ");
    } else {
      // Original: single author line. Prefer basionym atomization
      // (that's the canonical author field); fall back to combination
      // atomization when basionym is empty. Year falls back the same
      // way. When both fields carry the same author (COL convention),
      // the picker still shows one, not two.
      const auth = bAuth || cAuth;
      const year = bYear || cYear;
      suffix = [auth, year].filter(Boolean).join(", ");
    }
    // Nothing atomized to render → fall back to the pre-formatted
    // code-conventional label so the row is never blank.
    if (!suffix) return renderLabel(label, n.name_id);
    return html`${canonicalNode} ${suffix}`;
  }

  // _renderNomenRowActions draws the hover-reveal action strip on
  // every Nomenclatural-history row. Buttons: + (add sibling
  // recomb), warn (open issues), pencil (edit name), trash (delete).
  //
  // Routing changes per row kind:
  //   * Accepted row → pencil opens the taxon-edit flow
  //     (_openEditTaxon) and trash opens the taxon-delete flow
  //     (_askDelete). The header's edit / delete affordances point
  //     at the same handlers.
  //   * Synonym / basionym / unlinked row → pencil opens the
  //     synonym-edit form (_openEditSynonym) and trash opens the
  //     synonym-delete modal.
  //
  // The + button surfaces on rows where hive can determine a
  // basionym anchor to seed a new subsequent combination:
  //   * Cluster-anchor rows (is_basionym: true) — uses this row's
  //     own name_id as the basionym.
  //   * The accepted row — uses the cluster's basionym anchor
  //     (accepted's name_id when accepted IS the basionym; the
  //     anchor row's name_id when accepted is itself a recomb).
  //
  // The warn icon uses the shared validationSeverityBadge so color
  // + tooltip stay in sync with pickers.
  _renderNomenRowActions(n, cluster, isAcceptedName) {
    const hasIssues = (n.issue_count || 0) > 0;
    const isClusterAnchor = !!n.is_basionym;
    // Basionym anchor id for the + button.
    //   * Cluster anchors → this row.
    //   * Accepted row → cluster's basionym anchor (may be this row
    //     if accepted IS the basionym, otherwise a sibling).
    //   * Everyone else → nothing (button hidden).
    let addBasionymNameID = "";
    if (isClusterAnchor) {
      addBasionymNameID = n.name_id || "";
    } else if (isAcceptedName) {
      const anchor = (cluster?.names || []).find((r) => r.is_basionym);
      addBasionymNameID = anchor?.name_id || n.name_id || "";
    }
    const showAdd = !!addBasionymNameID;

    const onEdit = isAcceptedName
      ? (e) => {
          e.stopPropagation();
          this._openEditTaxon();
        }
      : (e) => this._onNomenRowEdit(e, n);

    // Delete: accepted row → taxon-delete flow (_askDelete opens the
    // preview + confirm modal). Synonym rows → the existing synonym-
    // delete flow. Rows with no synonym id AND no accepted marker
    // (legacy / phantom entries) render a disabled trash button.
    const canDelete = isAcceptedName || !!n.synonym_id;
    const onDelete = isAcceptedName
      ? (e) => {
          e.stopPropagation();
          this._askDelete();
        }
      : (e) => this._onNomenRowDelete(e, n);
    const deleteTitle = isAcceptedName ? "delete taxon" : "delete synonym";

    return html`
      <span class="row-actions">
        ${showAdd
          ? html`<button
              class="icon-btn subtle"
              @click=${(e) => {
                e.stopPropagation();
                this._openCreateSynonym({
                  basionymNameID: addBasionymNameID,
                });
              }}
              title="add subsequent combination of this original combination"
              aria-label="add subsequent combination"
            >
              ${renderIcon("plus", 14)}
            </button>`
          : ""}
        ${hasIssues
          ? (() => {
              const badge = validationSeverityBadge(
                n.issue_count || 0,
                n.max_severity,
                "name-issue",
              );
              return html`<button
                class=${"icon-btn subtle warn variant-" + badge.variant}
                @click=${(e) => this._onNomenRowIssues(e, n)}
                title=${badge.tooltip}
                aria-label=${badge.tooltip}
              >
                ${renderIcon(badge.icon, 14)}
              </button>`;
            })()
          : ""}
        <button
          class="icon-btn subtle"
          @click=${onEdit}
          title="edit name"
          aria-label="edit name"
        >
          ${renderIcon("pencil", 14)}
        </button>
        <button
          class="icon-btn subtle danger"
          @click=${onDelete}
          title=${deleteTitle}
          aria-label=${deleteTitle}
          ?disabled=${!canDelete}
        >
          ${renderIcon("trash-2", 14)}
        </button>
      </span>
    `;
  }

  // _onNomenRowAddRecomb opens the create-synonym form with the
  // clicked row's name_id pre-populated as basionym_name_id — the
  // "add another recomb sharing this basionym" shortcut on cluster-
  // anchor rows in Nomen History. See DESIGN.md § Basionym cluster
  // + button.
  _onNomenRowAddRecomb(e, n) {
    e.stopPropagation();
    if (!n.name_id) return;
    this._openCreateSynonym({ basionymNameID: n.name_id });
  }

  _onNomenRowEdit(e, n) {
    e.stopPropagation();
    if (!n.name_id) return;
    // Routes through _openEditSynonym so the pencil opens the same
    // unified form as the taxon-edit pencil — one form shape for
    // both accepted-name and synonym-name editing. Falls through to
    // a name-only edit when the row has no synonym_id (legacy
    // archives where synonym.col__id wasn't set); the accepted-taxon
    // picker just renders empty in that case, harmless.
    this._openEditSynonym(n.name_id, n.synonym_id || "", this.taxonId || "");
  }

  _onNomenRowIssues(e, n) {
    // Same target as the pencil — the unified edit form fetches and
    // renders open issues at the top of the pane, so opening it "on
    // the issues" is just opening it.
    this._onNomenRowEdit(e, n);
  }

  _onNomenRowDelete(e, n) {
    e.stopPropagation();
    if (!n.synonym_id) return; // button disabled in render; belt-and-braces
    this._openSynonymDelete(n);
  }

  // _openSynonymDelete opens the two-option delete modal for a synonym.
  // Kicks off in "loading" phase while name dependencies are fetched;
  // on response transitions to "prompt" where the curator picks
  // "Delete synonym only" (safe — leaves a bare name if this was the
  // last reference) or "Delete synonym + name" (destructive — only
  // offered when the name would be bare after removal). Cascade path
  // gates behind typing DELETE. See DESIGN.md § List-row actions.
  async _openSynonymDelete(n) {
    this._synonymDelete = {
      phase: "loading",
      synonymId: n.synonym_id,
      nameId: n.name_id,
      label: n.label?.text || n.name_id,
      deps: null,
      cascade: false,
      confirmText: "",
      busy: false,
      error: "",
    };
    try {
      const deps = await api.name.dependencies(n.name_id);
      // Race guard: curator dismissed while we waited.
      if (!this._synonymDelete || this._synonymDelete.synonymId !== n.synonym_id) return;
      this._synonymDelete = { ...this._synonymDelete, phase: "prompt", deps };
    } catch (err) {
      if (!this._synonymDelete || this._synonymDelete.synonymId !== n.synonym_id) return;
      this._synonymDelete = {
        ...this._synonymDelete,
        phase: "prompt",
        deps: null,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  _cancelSynonymDelete() {
    this._synonymDelete = null;
  }

  _synonymDeleteSetCascade(cascade) {
    if (!this._synonymDelete) return;
    this._synonymDelete = {
      ...this._synonymDelete,
      cascade,
      // Reset the DELETE-typing guard when the choice flips so the
      // curator can't accidentally hit a stale-typed value.
      confirmText: "",
    };
  }

  _synonymDeleteSetConfirmText(v) {
    if (!this._synonymDelete) return;
    this._synonymDelete = { ...this._synonymDelete, confirmText: v };
  }

  async _submitSynonymDelete() {
    const d = this._synonymDelete;
    if (!d) return;
    // Cascade requires typing DELETE. Non-cascade doesn't (no name
    // is being removed; the synonym link is a safer op).
    if (d.cascade && d.confirmText.trim() !== "DELETE") {
      this._synonymDelete = {
        ...d,
        error: "Type DELETE to confirm cascade.",
      };
      return;
    }
    this._synonymDelete = { ...d, busy: true, error: "" };
    try {
      await api.synonym.delete(d.synonymId, { cascadeName: d.cascade });
      this._synonymDelete = null;
      // Refresh the whole nomen-history section — a cascade may also
      // affect related basionym relations, so a targeted refetch is
      // safest.
      await this._refreshNomenHistory();
    } catch (err) {
      this._synonymDelete = {
        ...this._synonymDelete,
        busy: false,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  async _refreshNomenHistory() {
    if (!this.taxonId) return;
    try {
      const resp = await api.taxon.nomenHistory(this.taxonId);
      this._nomenHistory = resp;
    } catch (_) {
      // Silent — the section was populated a moment ago; a transient
      // fetch failure leaves the stale rendering rather than clearing
      // it, which would surprise the curator.
    }
  }

  _renderSynonymDeleteModal() {
    const d = this._synonymDelete;
    if (!d) return "";
    if (d.phase === "loading") {
      return html`
        <div class=${this._backdropClass()} @click=${(e) => e.stopPropagation()}>
          <div
            class="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="synonym-delete-loading"
          >
            <h3 id="synonym-delete-loading">Delete synonym</h3>
            <p role="status">Checking what references this name…</p>
          </div>
        </div>
      `;
    }
    // Cascade is offered only when the name would be bare after
    // removal — i.e., no other synonyms point at it, no taxon claims
    // it, no name_relation touches it. The taxon_count could be > 0
    // if this synonym's name happens to also be an accepted name's
    // basionym or similar edge (rare); block cascade in that case.
    const deps = d.deps;
    // Post-removal counts: subtract 1 from synonym_count since this
    // row is being deleted. The other counts stay as-is.
    const otherSynonyms = deps ? Math.max(0, (deps.synonym_count || 0) - 1) : 0;
    const otherRefs = deps ? deps.taxon_count + otherSynonyms + deps.name_relation_count : 0;
    const canCascade = deps !== null && otherRefs === 0;
    const cascadeOK =
      d.cascade && (d.confirmText || "").trim() === "DELETE" && !d.busy;
    const deleteEnabled = d.cascade ? cascadeOK : !d.busy;
    return html`
      <div class=${this._backdropClass()} @click=${(e) => e.stopPropagation()}>
        <div
          class="modal synonym-delete"
          role="dialog"
          aria-modal="true"
          aria-labelledby="synonym-delete-heading"
          @keydown=${(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              this._cancelSynonymDelete();
            }
          }}
        >
          <h3 id="synonym-delete-heading">Delete synonym</h3>
          <p>
            <em>${d.label}</em> will be removed from this taxon.
          </p>
          ${d.error
            ? html`<div class="error" role="alert">${d.error}</div>`
            : ""}
          <fieldset class="cascade-choice">
            <legend>Also delete the name row?</legend>
            <label>
              <input
                type="radio"
                name="cascade"
                .checked=${!d.cascade}
                @change=${() => this._synonymDeleteSetCascade(false)}
              />
              <span>
                <strong>Keep the name</strong>
                <span class="hint">
                  Safe. The synonym link is removed; the name row stays in
                  the archive. If nothing else references it, it becomes a
                  <em>bare name</em> — still searchable and re-usable.
                </span>
              </span>
            </label>
            <label>
              <input
                type="radio"
                name="cascade"
                .checked=${d.cascade}
                ?disabled=${!canCascade}
                @change=${() => this._synonymDeleteSetCascade(true)}
              />
              <span>
                <strong>Delete synonym + name</strong>
                ${canCascade
                  ? html`<span class="hint">
                      Removes both rows. The name won't appear in any
                      list or search after this.
                    </span>`
                  : html`<span class="hint warn">
                      Not available — the name is still referenced elsewhere:
                      ${deps
                        ? this._renderNameDepBreakdown(deps, otherSynonyms)
                        : "unknown (dependency check failed)"}.
                    </span>`}
              </span>
            </label>
          </fieldset>
          ${d.cascade
            ? html`
                <label class="type-to-confirm">
                  Type <code>DELETE</code> to confirm cascade:
                  <input
                    type="text"
                    .value=${d.confirmText}
                    @input=${(e) =>
                      this._synonymDeleteSetConfirmText(e.target.value)}
                    autocomplete="off"
                    autofocus
                  />
                </label>
              `
            : ""}
          <div class="toolbar">
            <button
              type="button"
              @click=${() => this._cancelSynonymDelete()}
              ?disabled=${d.busy}
            >
              Cancel
            </button>
            <button
              type="button"
              class="danger-primary"
              ?disabled=${!deleteEnabled}
              @click=${() => this._submitSynonymDelete()}
            >
              ${d.busy
                ? "Deleting…"
                : d.cascade
                  ? "Delete synonym + name"
                  : "Delete synonym"}
            </button>
          </div>
        </div>
      </div>
    `;
  }

  _renderNameDepBreakdown(deps, otherSynonyms) {
    const parts = [];
    if (deps.taxon_count > 0) {
      parts.push(
        `${deps.taxon_count} taxon${deps.taxon_count > 1 ? "s" : ""}`,
      );
    }
    if (otherSynonyms > 0) {
      parts.push(
        `${otherSynonyms} other synonym${otherSynonyms > 1 ? "s" : ""}`,
      );
    }
    if (deps.name_relation_count > 0) {
      parts.push(
        `${deps.name_relation_count} name relation${deps.name_relation_count > 1 ? "s" : ""}`,
      );
    }
    return parts.join(", ");
  }

  // _renderVernaculars draws the vernacular-names table. Backed by
  // GET /api/taxon/{id}/vernaculars which returns rows preferred-first,
  // then by language, then name — no client-side re-sort needed.
  // Section elided entirely when there are no vernaculars (per
  // DESIGN.md § Per-data-type sections). Row hover reveals pencil +
  // delete via the same .row-actions pattern used in nomen history.
  _renderVernaculars() {
    const items = this._vernaculars || [];
    if (items.length === 0) return "";
    return html`
      <section class="vernaculars">
        ${this._sectionHeader("Vernacular names", {
          handler: () => this._openVernacularCreate(),
          title: "add vernacular name",
        })}
        <table>
          <thead>
            <tr>
              <th class="pref" aria-label="preferred"></th>
              <th class="name">Name</th>
              <th class="lang">Lang</th>
              <th class="country">Country</th>
              <th class="region">Area</th>
              <th class="actions" aria-label="actions"></th>
            </tr>
          </thead>
          <tbody>
            ${items.map((v) => this._renderVernacularRow(v))}
          </tbody>
        </table>
      </section>
    `;
  }

  _renderVernacularRow(v) {
    const cite = this._citeRef(v.reference_id, v.reference_label);
    return html`
      <tr>
        <td class="pref" aria-hidden=${v.preferred ? "false" : "true"}>
          ${v.preferred ? "✓" : ""}
        </td>
        <td class="name">
          ${v.name}${cite}${v.transliteration
            ? html`<span class="translit">${v.transliteration}</span>`
            : ""}
        </td>
        <td class="lang">${v.language || ""}</td>
        <td class="country">${v.country || ""}</td>
        <td class="region">${v.area || ""}</td>
        <td class="actions">
          <span class="row-actions">
            ${(v.issue_count || 0) > 0
              ? (() => {
                  const badge = validationSeverityBadge(
                    v.issue_count || 0,
                    v.max_severity,
                    "vernacular-issue",
                    { tooltip: "open editor at validation issues" },
                  );
                  return html`<button
                    class=${"icon-btn subtle warn variant-" + badge.variant}
                    @click=${() => this._openVernacularEdit(v)}
                    title=${badge.tooltip}
                    aria-label="validation issues on this vernacular"
                  >
                    ${renderIcon(badge.icon, 14)}
                  </button>`;
                })()
              : ""}
            <button
              class="icon-btn subtle"
              @click=${() => this._openVernacularEdit(v)}
              title="edit vernacular"
              aria-label="edit vernacular"
            >
              ${renderIcon("pencil", 14)}
            </button>
            <button
              class="icon-btn subtle danger"
              @click=${() => this._deleteVernacular(v)}
              title="delete vernacular"
              aria-label="delete vernacular"
            >
              ${renderIcon("trash-2", 14)}
            </button>
          </span>
        </td>
      </tr>
    `;
  }

  // _openVernacularCreate opens the modal in create mode with a blank
  // draft. Reference_id is left empty so the picker opens fresh; code
  // isn't a concept on vernaculars.
  _openVernacularCreate() {
    this._vernacularForm = {
      mode: "create",
      draft: { name: "", preferred: false },
      error: "",
    };
  }

  async _openVernacularEdit(v) {
    this._vernacularForm = {
      mode: "edit",
      id: v.id,
      draft: { ...v },
      issues: [],
      error: "",
    };
    // Fetch open validation issues for this row so the banner at the
    // top of the modal renders. Race-guarded via the id comparison
    // (the curator might close and reopen a different row).
    try {
      const resp = await api.issue.list({
        table: "vernacular",
        record_id: v.id,
        limit: 100,
      });
      if (this._vernacularForm && this._vernacularForm.id === v.id) {
        this._vernacularForm = {
          ...this._vernacularForm,
          issues: resp.items || [],
        };
      }
    } catch (_) {
      // Silent — banner just won't render on fetch failure.
    }
  }

  _cancelVernacularForm() {
    this._vernacularForm = null;
  }

  _vernacularFieldChange(field, value) {
    if (!this._vernacularForm) return;
    this._vernacularForm = {
      ...this._vernacularForm,
      draft: { ...this._vernacularForm.draft, [field]: value },
    };
  }

  async _submitVernacularForm() {
    const f = this._vernacularForm;
    if (!f) return;
    const name = (f.draft.name || "").trim();
    if (!name) {
      this._vernacularForm = { ...f, error: "name is required" };
      return;
    }
    try {
      if (f.mode === "create") {
        await api.taxon.createVernacular(this.taxonId, f.draft);
      } else {
        // PATCH body: send only fields that differ from the original
        // row (the modal's draft was seeded from the original, so any
        // field the curator hasn't touched matches; but we send them
        // all — the backend patch treats non-nil pointers as
        // "assign", which matches the round-trip we want). Simpler
        // than diffing; correctness is unchanged.
        await api.vernacular.patch(f.id, f.draft);
      }
      this._vernacularForm = null;
      await this._refreshVernaculars();
    } catch (err) {
      this._vernacularForm = {
        ...f,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  async _deleteVernacular(v) {
    const ok = await confirmAction({
      heading: "Delete vernacular name?",
      message: `"${v.name}" will be permanently deleted from this taxon.`,
      actionLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api.vernacular.delete(v.id);
      await this._refreshVernaculars();
    } catch (err) {
      // Surface via the taxon-pane error banner — same channel as
      // other detail-pane failures. Non-fatal for the rest of the pane.
      this._error =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  async _refreshVernaculars() {
    // Refetch just the vernaculars, not the whole pane — avoids a
    // full _load() and the visible flash of "loading" state.
    if (!this.taxonId) return;
    try {
      const resp = await api.taxon.vernaculars(this.taxonId);
      this._vernaculars = resp.items || [];
    } catch (_) {
      // Silent — the section was populated a moment ago; a transient
      // fetch failure leaves the stale list rather than flashing an
      // error into the section.
    }
  }

  // _renderVernacularModal is the add/edit form. Uses the modal
  // pattern from SfgaConfirmModal (backdrop + centered content +
  // trap focus) but with a bespoke form body.
  _renderVernacularModal() {
    const f = this._vernacularForm;
    if (!f) return "";
    const d = f.draft;
    const set = (field) => (e) =>
      this._vernacularFieldChange(field, e.target.value);
    const setCheck = (field) => (e) =>
      this._vernacularFieldChange(field, e.target.checked);
    const heading =
      f.mode === "edit" ? "Edit vernacular name" : "Add vernacular name";
    // Reference-picker pinned action opens the shared add-reference
    // modal. Route the pick back via the same _addingReferenceFor
    // machinery, targeting a new "vernacular" scope so the pick
    // updates this draft's reference_id.
    const refActions = [
      {
        label: "Add new reference",
        icon: "plus",
        handler: () => (this._addingReferenceFor = "vernacular"),
      },
    ];
    return html`
      <div
        class=${this._backdropClass()}
        @click=${(e) => {
          // Form modal — never dismiss on backdrop click (see DESIGN.md
          // § Modals). Curator uses Cancel or Escape.
          e.stopPropagation();
        }}
      >
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="vernacular-modal-heading"
          @keydown=${(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              this._cancelVernacularForm();
            }
          }}
        >
          <div class="modal-header">
            <h3 id="vernacular-modal-heading">${heading}</h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._cancelVernacularForm()}
              title="close"
              aria-label="close"
            >
              ×
            </button>
          </div>
          ${f.issues && f.issues.length > 0
            ? html`
                <div class="warning-banner">
                  <strong>
                    ${f.issues.length} open
                    issue${f.issues.length > 1 ? "s" : ""} on this
                    vernacular:
                  </strong>
                  <ul>
                    ${f.issues.map(
                      (i) => html`<li>
                        ${severityChip(i.severity)}
                        <span>
                          <span class="warning-rule"
                            >${i.rule_name || i.rule_id}</span
                          >:
                          ${i.message}
                          ${i.field_name
                            ? html` <span class="warning-rule"
                                >(${i.field_name})</span
                              >`
                            : ""}
                        </span>
                      </li>`,
                    )}
                  </ul>
                </div>
              `
            : ""}
          ${f.error
            ? html`<div class="error" role="alert">${f.error}</div>`
            : ""}
          <form
            class="vernacular-form"
            @submit=${(e) => {
              e.preventDefault();
              this._submitVernacularForm();
            }}
          >
            <label for="vern-name">Name <span class="req">*</span></label>
            <input
              id="vern-name"
              type="text"
              .value=${d.name || ""}
              @input=${set("name")}
              autofocus
            />

            <label class="checkbox-row">
              <input
                type="checkbox"
                .checked=${!!d.preferred}
                @change=${setCheck("preferred")}
              />
              Preferred common name for this taxon
            </label>

            <label>Language</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Type name or ISO 639-3 code…"
              allow-free-text
              .source=${languageSource}
              .resolver=${languageResolver}
              .value=${d.language || ""}
              @pick=${(e) =>
                this._vernacularFieldChange("language", e.detail.id)}
            ></sfga-combobox>

            <label>Country</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Type name or ISO 3166 code…"
              allow-free-text
              .source=${countrySource}
              .resolver=${countryResolver}
              .value=${d.country || ""}
              @pick=${(e) =>
                this._vernacularFieldChange("country", e.detail.id)}
            ></sfga-combobox>

            <label for="vern-area">Area / region</label>
            <input
              id="vern-area"
              type="text"
              placeholder="e.g. North America, Pacific Northwest"
              .value=${d.area || ""}
              @input=${set("area")}
            />

            <label>Sex</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Type name or code…"
              allow-free-text
              .source=${sexSource}
              .resolver=${sexResolver}
              .value=${d.sex || ""}
              @pick=${(e) => this._vernacularFieldChange("sex", e.detail.id)}
            ></sfga-combobox>

            <label for="vern-translit">Transliteration</label>
            <input
              id="vern-translit"
              type="text"
              placeholder="Latin-script rendering of a non-Latin name"
              .value=${d.transliteration || ""}
              @input=${set("transliteration")}
            />

            <label>Reference</label>
            <sfga-combobox
              min-search-chars="2"
              placeholder="Search references…"
              .source=${referenceSource}
              .resolver=${referenceResolver}
              .value=${d.reference_id || ""}
              .actions=${refActions}
              @pick=${(e) =>
                this._vernacularFieldChange("reference_id", e.detail.id)}
              @badge-click=${(e) =>
                this._openEditReference(e.detail.id || d.reference_id)}
            ></sfga-combobox>

            <label for="vern-remarks">Remarks</label>
            <textarea
              id="vern-remarks"
              rows="2"
              .value=${d.remarks || ""}
              @input=${set("remarks")}
            ></textarea>

            <div class="toolbar">
              <button
                type="button"
                @click=${() => this._cancelVernacularForm()}
              >
                Cancel
              </button>
              <button type="submit" class="primary">
                ${f.mode === "edit" ? "Save changes" : "Add"}
              </button>
            </div>
          </form>
        </div>
      </div>
    `;
  }

  // ---------- Distribution section ----------
  // Same shape as the vernacular section — table + row-actions +
  // add/edit modal. Backend ships apiDistribution with issue_count
  // baked in, so the warn-icon plumbing works out of the box. Area
  // gets the inline citation; other columns stay dense.

  _renderDistributions() {
    const items = this._distributions || [];
    if (items.length === 0) return "";
    return html`
      <section class="distributions">
        ${this._sectionHeader("Distributions", {
          handler: () => this._openDistributionCreate(),
          title: "add distribution",
        })}
        <table>
          <thead>
            <tr>
              <th class="area">Area</th>
              <th class="gaz">Gazetteer</th>
              <th class="status">Status</th>
              <th class="actions" aria-label="actions"></th>
            </tr>
          </thead>
          <tbody>
            ${items.map((d) => this._renderDistributionRow(d))}
          </tbody>
        </table>
      </section>
    `;
  }

  _renderDistributionRow(d) {
    const cite = this._citeRef(d.reference_id, d.reference_label);
    // Area prefers the human-readable label; falls back to area_id
    // for gazetteers where only the code was populated.
    const areaText = d.area || d.area_id || "(unset)";
    // Show area_id in dim when we have both the label AND the code —
    // gives curators the identifier reference without eating the row.
    const showCode = d.area && d.area_id;
    return html`
      <tr>
        <td class="area">
          ${areaText}${cite}${showCode
            ? html` <span class="area-code">${d.area_id}</span>`
            : ""}
        </td>
        <td class="gaz">${d.gazetteer || ""}</td>
        <td class="status">${d.status || ""}</td>
        <td class="actions">
          <span class="row-actions">
            ${(d.issue_count || 0) > 0
              ? (() => {
                  const badge = validationSeverityBadge(
                    d.issue_count || 0,
                    d.max_severity,
                    "distribution-issue",
                    { tooltip: "open editor at validation issues" },
                  );
                  return html`<button
                    class=${"icon-btn subtle warn variant-" + badge.variant}
                    @click=${() => this._openDistributionEdit(d)}
                    title=${badge.tooltip}
                    aria-label="validation issues on this distribution"
                  >
                    ${renderIcon(badge.icon, 14)}
                  </button>`;
                })()
              : ""}
            <button
              class="icon-btn subtle"
              @click=${() => this._openDistributionEdit(d)}
              title="edit distribution"
              aria-label="edit distribution"
            >
              ${renderIcon("pencil", 14)}
            </button>
            <button
              class="icon-btn subtle danger"
              @click=${() => this._deleteDistribution(d)}
              title="delete distribution"
              aria-label="delete distribution"
            >
              ${renderIcon("trash-2", 14)}
            </button>
          </span>
        </td>
      </tr>
    `;
  }

  _openDistributionCreate() {
    this._distributionForm = {
      mode: "create",
      draft: {},
      issues: [],
      error: "",
    };
  }

  async _openDistributionEdit(d) {
    this._distributionForm = {
      mode: "edit",
      id: d.id,
      draft: { ...d },
      issues: [],
      error: "",
    };
    try {
      const resp = await api.issue.list({
        table: "distribution",
        record_id: d.id,
        limit: 100,
      });
      if (this._distributionForm && this._distributionForm.id === d.id) {
        this._distributionForm = {
          ...this._distributionForm,
          issues: resp.items || [],
        };
      }
    } catch (_) {
      // Silent — banner just won't render on fetch failure.
    }
  }

  _cancelDistributionForm() {
    this._distributionForm = null;
  }

  _distributionFieldChange(field, value) {
    if (!this._distributionForm) return;
    this._distributionForm = {
      ...this._distributionForm,
      draft: { ...this._distributionForm.draft, [field]: value },
    };
  }

  async _submitDistributionForm() {
    const f = this._distributionForm;
    if (!f) return;
    // Distribution requires at least an area OR area_id — otherwise
    // there's no geography to record. Backend also validates but
    // catching here saves a round-trip and shows the error next to
    // the fields.
    const hasArea =
      (f.draft.area || "").trim() || (f.draft.area_id || "").trim();
    if (!hasArea) {
      this._distributionForm = {
        ...f,
        error: "area or area_id is required",
      };
      return;
    }
    try {
      if (f.mode === "create") {
        await api.taxon.createDistribution(this.taxonId, f.draft);
      } else {
        await api.distribution.patch(f.id, f.draft);
      }
      this._distributionForm = null;
      await this._refreshDistributions();
    } catch (err) {
      this._distributionForm = {
        ...f,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  async _deleteDistribution(d) {
    const ok = await confirmAction({
      heading: "Delete distribution?",
      message: `"${d.area || d.area_id || d.id}" will be permanently deleted from this taxon.`,
      actionLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api.distribution.delete(d.id);
      await this._refreshDistributions();
    } catch (err) {
      this._error =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  async _refreshDistributions() {
    if (!this.taxonId) return;
    try {
      const resp = await api.taxon.distributions(this.taxonId);
      this._distributions = resp.items || [];
    } catch (_) {
      // Silent — stale list beats a flashing error.
    }
  }

  _renderDistributionModal() {
    const f = this._distributionForm;
    if (!f) return "";
    const d = f.draft;
    const set = (field) => (e) =>
      this._distributionFieldChange(field, e.target.value);
    const heading =
      f.mode === "edit" ? "Edit distribution" : "Add distribution";
    const refActions = [
      {
        label: "Add new reference",
        icon: "plus",
        handler: () => (this._addingReferenceFor = "distribution"),
      },
    ];
    return html`
      <div class=${this._backdropClass()} @click=${(e) => e.stopPropagation()}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="distribution-modal-heading"
          @keydown=${(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              this._cancelDistributionForm();
            }
          }}
        >
          <div class="modal-header">
            <h3 id="distribution-modal-heading">${heading}</h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._cancelDistributionForm()}
              title="close"
              aria-label="close"
            >
              ×
            </button>
          </div>
          ${f.issues && f.issues.length > 0
            ? html`
                <div class="warning-banner">
                  <strong>
                    ${f.issues.length} open
                    issue${f.issues.length > 1 ? "s" : ""} on this
                    distribution:
                  </strong>
                  <ul>
                    ${f.issues.map(
                      (i) => html`<li>
                        ${severityChip(i.severity)}
                        <span>
                          <span class="warning-rule"
                            >${i.rule_name || i.rule_id}</span
                          >:
                          ${i.message}
                          ${i.field_name
                            ? html` <span class="warning-rule"
                                >(${i.field_name})</span
                              >`
                            : ""}
                        </span>
                      </li>`,
                    )}
                  </ul>
                </div>
              `
            : ""}
          ${f.error
            ? html`<div class="error" role="alert">${f.error}</div>`
            : ""}
          <form
            class="distribution-form"
            @submit=${(e) => {
              e.preventDefault();
              this._submitDistributionForm();
            }}
          >
            <label for="dist-area">Area</label>
            <input
              id="dist-area"
              type="text"
              placeholder="e.g. Australia: Western Australia"
              .value=${d.area || ""}
              @input=${set("area")}
              autofocus
            />

            <label for="dist-area-id">Area ID</label>
            <input
              id="dist-area-id"
              type="text"
              placeholder="Code within the gazetteer (e.g. AU-WA)"
              .value=${d.area_id || ""}
              @input=${set("area_id")}
            />

            <label>Gazetteer</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Gazetteer…"
              .source=${vocabSource("gazetteer")}
              .resolver=${vocabResolver("gazetteer")}
              .value=${d.gazetteer || ""}
              @pick=${(e) =>
                this._distributionFieldChange("gazetteer", e.detail.id)}
            ></sfga-combobox>

            <label>Status</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Distribution status…"
              .source=${vocabSource("distribution_status")}
              .resolver=${vocabResolver("distribution_status")}
              .value=${d.status || ""}
              @pick=${(e) =>
                this._distributionFieldChange("status", e.detail.id)}
            ></sfga-combobox>

            <label>Reference</label>
            <sfga-combobox
              min-search-chars="2"
              placeholder="Search author / title / citation / DOI…"
              .source=${referenceSource}
              .resolver=${referenceResolver}
              .value=${d.reference_id || ""}
              .actions=${refActions}
              @pick=${(e) =>
                this._distributionFieldChange("reference_id", e.detail.id)}
              @badge-click=${(e) =>
                this._openEditReference(e.detail.id || d.reference_id)}
            ></sfga-combobox>

            <label for="dist-remarks">Remarks</label>
            <textarea
              id="dist-remarks"
              rows="2"
              .value=${d.remarks || ""}
              @input=${set("remarks")}
            ></textarea>

            <div class="toolbar">
              <button
                type="button"
                @click=${() => this._cancelDistributionForm()}
              >
                Cancel
              </button>
              <button type="submit" class="primary">
                ${f.mode === "edit" ? "Save changes" : "Add"}
              </button>
            </div>
          </form>
        </div>
      </div>
    `;
  }

  // ---------- Species interactions section ----------
  // Same shape as vernaculars / distributions — table + row-actions +
  // add/edit modal. Backend ships apiSpeciesInteraction with
  // issue_count + max_severity baked in, so the shared warn-icon
  // plumbing works out of the box. Related taxon renders from the
  // server-resolved label so no per-row fetch is required.
  //
  // Sfga schema requires related_taxon_id (FK NOT NULL); the free-text
  // related_taxon_scientific_name is preserved as an annotation
  // alongside the FK but cannot stand alone. Direction: rows returned
  // here have this taxon as the SUBJECT — the OBJECT-side view is a
  // future enhancement (see pkg/species_interaction.go for the
  // two-sided rendering design note).

  _renderSpeciesInteractions() {
    const items = this._speciesInteractions || [];
    if (items.length === 0) return "";
    return html`
      <section class="species-interactions">
        ${this._sectionHeader("Species interactions", {
          handler: () => this._openSpeciesInteractionCreate(),
          title: "add species interaction",
        })}
        <table>
          <thead>
            <tr>
              <th class="type">Type</th>
              <th class="related">Related taxon</th>
              <th class="actions" aria-label="actions"></th>
            </tr>
          </thead>
          <tbody>
            ${items.map((si) => this._renderSpeciesInteractionRow(si))}
          </tbody>
        </table>
      </section>
    `;
  }

  _renderSpeciesInteractionRow(si) {
    const cite = this._citeRef(si.reference_id, si.reference_label);
    // Prefer the server-resolved label (canonical + authorship, HTML
    // for italics). Fall back to the free-text scientific-name
    // annotation when the resolved label is empty (shouldn't happen
    // with the FK-required contract, but a defensive fallback).
    const labelHTML =
      (si.related_taxon_label && si.related_taxon_label.html) ||
      (si.related_taxon_label && si.related_taxon_label.text) ||
      si.related_taxon_scientific_name ||
      si.related_taxon_id ||
      "(unset)";
    // If the source cited the taxon under a different name than what's
    // stored, show that annotation dimmed after the resolved label —
    // curators can see the divergence at a glance.
    const showAnnotation =
      si.related_taxon_scientific_name &&
      si.related_taxon_label &&
      si.related_taxon_label.text &&
      si.related_taxon_scientific_name !== si.related_taxon_label.text;
    return html`
      <tr>
        <td class="type">${si.type || ""}</td>
        <td class="related">
          <span .innerHTML=${labelHTML}></span>${cite}${showAnnotation
            ? html` <span class="annotation"
                >(as ${si.related_taxon_scientific_name})</span
              >`
            : ""}
        </td>
        <td class="actions">
          <span class="row-actions">
            ${(si.issue_count || 0) > 0
              ? (() => {
                  const badge = validationSeverityBadge(
                    si.issue_count || 0,
                    si.max_severity,
                    "species-interaction-issue",
                    { tooltip: "open editor at validation issues" },
                  );
                  return html`<button
                    class=${"icon-btn subtle warn variant-" + badge.variant}
                    @click=${() => this._openSpeciesInteractionEdit(si)}
                    title=${badge.tooltip}
                    aria-label="validation issues on this species interaction"
                  >
                    ${renderIcon(badge.icon, 14)}
                  </button>`;
                })()
              : ""}
            <button
              class="icon-btn subtle"
              @click=${() => this._openSpeciesInteractionEdit(si)}
              title="edit species interaction"
              aria-label="edit species interaction"
            >
              ${renderIcon("pencil", 14)}
            </button>
            <button
              class="icon-btn subtle danger"
              @click=${() => this._deleteSpeciesInteraction(si)}
              title="delete species interaction"
              aria-label="delete species interaction"
            >
              ${renderIcon("trash-2", 14)}
            </button>
          </span>
        </td>
      </tr>
    `;
  }

  _openSpeciesInteractionCreate() {
    this._speciesInteractionForm = {
      mode: "create",
      draft: {},
      issues: [],
      error: "",
    };
  }

  _openSpeciesInteractionEdit(si) {
    this._speciesInteractionForm = {
      mode: "edit",
      id: si.id,
      draft: { ...si },
      // Reuse the pattern from vernacular / distribution: pre-populate
      // the resolved label so the taxon picker shows the current
      // related taxon without a re-lookup.
      relatedLabel: si.related_taxon_label?.text || "",
      issues: [],
      error: "",
    };
  }

  _cancelSpeciesInteractionForm() {
    this._speciesInteractionForm = null;
  }

  _speciesInteractionFieldChange(field, value) {
    if (!this._speciesInteractionForm) return;
    this._speciesInteractionForm = {
      ...this._speciesInteractionForm,
      draft: { ...this._speciesInteractionForm.draft, [field]: value },
    };
  }

  async _submitSpeciesInteractionForm() {
    const f = this._speciesInteractionForm;
    if (!f) return;
    const relatedID = (f.draft.related_taxon_id || "").trim();
    if (!relatedID) {
      this._speciesInteractionForm = {
        ...f,
        error: "related taxon is required",
      };
      return;
    }
    try {
      if (f.mode === "create") {
        await api.taxon.createSpeciesInteraction(this.taxonId, f.draft);
      } else {
        await api.speciesInteraction.patch(f.id, f.draft);
      }
      this._speciesInteractionForm = null;
      await this._refreshSpeciesInteractions();
    } catch (err) {
      this._speciesInteractionForm = {
        ...f,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  async _deleteSpeciesInteraction(si) {
    const label =
      (si.related_taxon_label && si.related_taxon_label.text) ||
      si.related_taxon_scientific_name ||
      si.related_taxon_id ||
      si.id;
    const ok = await confirmAction({
      heading: "Delete species interaction?",
      message: `The "${si.type || "interaction"}" link to "${label}" will be permanently deleted.`,
      actionLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api.speciesInteraction.delete(si.id);
      await this._refreshSpeciesInteractions();
    } catch (err) {
      this._error =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  async _refreshSpeciesInteractions() {
    if (!this.taxonId) return;
    try {
      const resp = await api.taxon.speciesInteractions(this.taxonId);
      this._speciesInteractions = resp.items || [];
    } catch (_) {
      // Silent — stale list beats a flashing error.
    }
  }

  _renderSpeciesInteractionModal() {
    const f = this._speciesInteractionForm;
    if (!f) return "";
    const d = f.draft;
    const set = (field) => (e) =>
      this._speciesInteractionFieldChange(field, e.target.value);
    const heading =
      f.mode === "edit"
        ? "Edit species interaction"
        : "Add species interaction";
    const refActions = [
      {
        label: "Add new reference",
        icon: "plus",
        handler: () => (this._addingReferenceFor = "species-interaction"),
      },
    ];
    return html`
      <div class=${this._backdropClass()} @click=${(e) => e.stopPropagation()}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="species-interaction-modal-heading"
          @keydown=${(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              this._cancelSpeciesInteractionForm();
            }
          }}
        >
          <div class="modal-header">
            <h3 id="species-interaction-modal-heading">${heading}</h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._cancelSpeciesInteractionForm()}
              title="close"
              aria-label="close"
            >
              ×
            </button>
          </div>
          ${f.issues && f.issues.length > 0
            ? html`
                <div class="warning-banner">
                  <strong>
                    ${f.issues.length} open
                    issue${f.issues.length > 1 ? "s" : ""} on this
                    species interaction:
                  </strong>
                  <ul>
                    ${f.issues.map(
                      (i) => html`<li>
                        ${severityChip(i.severity)}
                        <span>
                          <span class="warning-rule"
                            >${i.rule_name || i.rule_id}</span
                          >:
                          ${i.message}
                          ${i.field_name
                            ? html` <span class="warning-rule"
                                >(${i.field_name})</span
                              >`
                            : ""}
                        </span>
                      </li>`,
                    )}
                  </ul>
                </div>
              `
            : ""}
          ${f.error
            ? html`<div class="error" role="alert">${f.error}</div>`
            : ""}
          <form
            class="species-interaction-form"
            @submit=${(e) => {
              e.preventDefault();
              this._submitSpeciesInteractionForm();
            }}
          >
            <label>Related taxon <span class="req">*</span></label>
            <sfga-combobox
              min-search-chars="2"
              placeholder="Search taxa…"
              .source=${taxonSource}
              .resolver=${taxonResolver}
              .value=${d.related_taxon_id || ""}
              @pick=${(e) =>
                this._speciesInteractionFieldChange(
                  "related_taxon_id",
                  e.detail.id,
                )}
            ></sfga-combobox>

            <label>Interaction type</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Interaction type…"
              .source=${vocabSource("species_interaction_type")}
              .resolver=${vocabResolver("species_interaction_type")}
              .value=${d.type || ""}
              .actions=${[
                {
                  label: "Edit interaction types…",
                  icon: "pencil",
                  handler: () =>
                    this._openVocabEditor("species_interaction_type"),
                },
              ]}
              @pick=${(e) =>
                this._speciesInteractionFieldChange("type", e.detail.id)}
            ></sfga-combobox>

            <label for="sxi-sci-name">Related taxon name (as cited)</label>
            <input
              id="sxi-sci-name"
              type="text"
              placeholder="Original spelling from the source, if different"
              .value=${d.related_taxon_scientific_name || ""}
              @input=${set("related_taxon_scientific_name")}
            />

            <label>Reference</label>
            <sfga-combobox
              min-search-chars="2"
              placeholder="Search references…"
              .source=${referenceSource}
              .resolver=${referenceResolver}
              .value=${d.reference_id || ""}
              .actions=${refActions}
              @pick=${(e) =>
                this._speciesInteractionFieldChange(
                  "reference_id",
                  e.detail.id,
                )}
              @badge-click=${(e) =>
                this._openEditReference(e.detail.id || d.reference_id)}
            ></sfga-combobox>

            <label for="sxi-remarks">Remarks</label>
            <textarea
              id="sxi-remarks"
              rows="2"
              .value=${d.remarks || ""}
              @input=${set("remarks")}
            ></textarea>

            <div class="toolbar">
              <button
                type="button"
                @click=${() => this._cancelSpeciesInteractionForm()}
              >
                Cancel
              </button>
              <button type="submit" class="primary">
                ${f.mode === "edit" ? "Save changes" : "Add"}
              </button>
            </div>
          </form>
        </div>
      </div>
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
// extractDOI normalises any DOI-shaped input into its bare form
// (10.NNNN/xxx). Accepts:
//   * bare DOI: "10.1234/abc.def"
//   * URL: "https://doi.org/10.1234/abc.def" or dx.doi.org
//   * CURIE: "doi:10.1234/abc.def"
// Returns "" if the input doesn't look like a DOI, so callers can
// treat a "found DOI" as the signal to fall through to OpenAlex.
function extractDOI(input) {
  const s = (input || "").trim();
  if (!s) return "";
  // Strip common prefixes; leave the DOI proper for the regex to
  // validate. `10.NNNN/…` is the DOI shape per doi.org standard.
  const cleaned = s.replace(
    /^(https?:\/\/(?:dx\.)?doi\.org\/|doi:)/i,
    "",
  );
  return /^10\.\d{4,9}\/\S+$/i.test(cleaned) ? cleaned : "";
}

// Events:
//   reference-picked  { id, label }  — modal closes; caller updates form.
//   close                             — modal closes; no change.
class SfgaAddReferenceModal extends LitElement {
  static properties = {
    contextCanonical: { attribute: false },
    contextAuthors: { attribute: false },
    contextYear: { attribute: false },
    // defaultTab lets the caller land the modal on a specific tab.
    // Values: "project" | "bhlnames" | "doi" | "bibtex" | "manual".
    // Callers with a name context (curator most likely adding the
    // protologue paper) typically want "project"; non-name contexts
    // (vernacular, distribution, section-level References) prefer
    // "doi" since the curator usually has a paper reference in hand.
    // Edit-mode (editID set) defaults to "manual". See DESIGN.md
    // § Reference-picker on every data-entry form.
    defaultTab: { attribute: false },
    // editID, when non-empty, opens the modal in edit mode: loads the
    // existing reference, defaults to the Manual tab, hydrates the
    // form with the loaded values, and PATCHes on save instead of
    // POST-creating a new row. Curator can still switch to any other
    // tab, though (for now) the metadata-lookup tabs in edit mode
    // still POST-create rather than PATCH-replace — Slice 2 wires
    // those. See REFERENCE_PDF_PLAN.md.
    editID: { attribute: false },
    // jatsRef, when set, puts the modal in "edit a JATS bibliography
    // reference" mode: seeds the Manual tab from the given fields
    // (no api.reference.get), routes save to POST a ref-edit
    // correction, and emits a "jats-ref-saved" event on success
    // instead of "reference-updated". Shape: { sha, refID, fields
    // (manualReference-like), suspect }.
    jatsRef: { attribute: false },
    // initialFile lets a caller (SfgaReferences drop handler)
    // hand a File object to the modal so the curator doesn't have
    // to re-pick it. Auto-uploaded in firstUpdated when set.
    initialFile: { attribute: false },
    _tab: { state: true }, // 0..4
    // Tab 0 (project) — inline DOI preview when the local search
    // finds nothing and the query looks like a DOI. Shows a save-
    // and-pick affordance so the curator doesn't need to switch tabs.
    _projectDoiPreview: { state: true },
    _projectDoiBusy: { state: true },
    _projectDoiError: { state: true },
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
    // _preview is the working copy — a curator's field picks in the
    // GROBID-vs-OpenAlex conflict UI mutate it in place. The saved
    // reference is built from _preview. _previewOpenAlex holds the
    // untouched OpenAlex response so the conflict picker can always
    // offer the OpenAlex value regardless of which side is currently
    // picked. Nil when the current preview didn't originate from an
    // OpenAlex fetch (BHLnames, BibTeX paths).
    _preview: { state: true },
    _previewOpenAlex: { state: true },
    // OpenAlex DOI-recovery candidates. Populated after an upload
    // where GROBID extracted usable header metadata but no DOI —
    // the search fires with title + first-author surname + year and
    // surfaces up to 5 matches. Publisher backfile programs and
    // DataCite (BHL) both assign DOIs retroactively to old papers,
    // so a PDF with no printed DOI often has one findable this way.
    _openalexCandidates: { state: true },
    _openalexCandidatesBusy: { state: true },
    _openalexCandidatesError: { state: true },
    // Tab 4 (File). Drag-and-drop / picker uploads a PDF (or
    // EPUB / HTML / JATS) into the archive's sidecar library and,
    // when GROBID is configured, converts to TEI. On success with
    // a DOI extracted from the TEI, auto-hops to the DOI tab and
    // fires the OpenAlex resolve so curators see the bibliographic
    // preview without a copy-paste step.
    _uploadBusy: { state: true },
    _uploadError: { state: true },
    _uploadResult: { state: true },
    // _rejectedDOI holds a DOI GROBID pulled from the TEI header
    // that resolved to an OpenAlex paper with a very different
    // title — typically a citation in the paper text (a database
    // credit, a "see also" reference) that GROBID misclassified as
    // this paper's own DOI. Surfaced as a hint on the File tab so
    // the curator sees why we didn't auto-hop to the DOI tab.
    _rejectedDOI: { state: true },
    _dragActive: { state: true },
    // Side-by-side PDF viewer mode. Auto-enables when an upload
    // lands with a sha (a PDF is available to display); the
    // curator can collapse via the "Hide PDF" toggle if they want
    // a narrower Manual form. Persists across tab switches so
    // hopping from Manual to DOI and back keeps the layout.
    _showPDF: { state: true },
    // Tab 5 (Manual). _manual holds the form draft object shaped like
    // apiReference; edit mode hydrates it from the loaded reference,
    // add mode starts empty. _manualOriginal is the loaded reference
    // in edit mode (used for If-Match etag). _manualBusy/_manualError
    // track the save round-trip.
    _manual: { state: true },
    _manualOriginal: { state: true },
    // Progressive disclosure for the Manual form. Add-mode with an
    // auto-fetched pre-fill (DOI resolve, BibTeX parse, upload) only
    // shows the essential fields + fields that have values; the
    // curator toggles the rest via "Show all fields". Edit mode
    // shows everything so existing values aren't hidden.
    _showAllFields: { state: true },
    _manualBusy: { state: true },
    _manualError: { state: true },
    // Persisted validation issues on the reference being edited.
    // Fetched alongside the reference in edit mode; rendered in a
    // banner between the modal header and the tab row so issues
    // stay visible while the curator switches tabs looking for the
    // right fix path. Empty in add mode.
    _issues: { state: true },
  };

  static styles = [
    buttonStyles,
    severityChipStyles,
    warningBannerStyles,
    css`
      :host {
        display: block;
      }
      .backdrop {
        position: fixed;
        inset: 0;
        background: color-mix(in oklab, var(--fg) 18%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
      }
      /* Nested-modal hide — see openModal / isTopModal helpers. */
      .backdrop.is-covered {
        visibility: hidden;
      }
      .modal {
        background: var(--bg);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-4) var(--sp-5);
        width: min(var(--modal-lg), 95vw);
        max-height: var(--modal-max-h);
        overflow: auto;
        display: grid;
        gap: var(--sp-2);
        font-family: var(--font-body);
      }
      /* Wide-mode expands the modal to give the PDF viewer room
         alongside the metadata form. Anchored at 95vw / 90vh (matches
         --modal-max-h already used for non-full modals), so we stay
         within the existing size tiers rather than jumping to a new
         --modal-full token that DESIGN.md reserves for the eventual
         workspace-shaped full-screen shell.
         Switches from grid to flex column so the .pane row can flex-
         grow to fill remaining space regardless of how many optional
         siblings (issues banner) are present. */
      .modal.wide {
        width: 95vw;
        height: var(--modal-max-h);
        display: flex;
        flex-direction: column;
        overflow: hidden;
      }
      .modal.wide > .pane {
        flex: 1;
        min-height: 0;
      }
      /* Side-by-side pane layout: PDF viewer on the left, tab body
         (metadata form) on the right. Two equal columns keep both
         panes readable; min-height: 0 lets the flex parent shrink us
         instead of overflowing. */
      .pane.side-by-side {
        grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
        min-height: 0;
        overflow: hidden;
      }
      .pane.side-by-side > .pane-body {
        overflow: auto;
        min-width: 0;
      }
      .pdf-viewer {
        display: grid;
        min-height: 0;
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        overflow: hidden;
        background: color-mix(in oklab, var(--fg) 3%, transparent);
      }
      .pdf-viewer iframe {
        width: 100%;
        height: 100%;
        border: 0;
      }
      /* Manual-tab form: two-column label+input grid, similar shape
         to the taxon edit form. Grid-column: 1 / -1 on textareas +
         the toolbar so they span the full width. */
      .manual-ref-form {
        display: grid;
        grid-template-columns: minmax(0, 10rem) minmax(0, 1fr);
        gap: var(--sp-1) var(--sp-2);
        align-items: center;
        min-width: 0;
      }
      .manual-ref-form label {
        text-align: right;
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      .manual-ref-form input[type="text"],
      .manual-ref-form textarea {
        color: var(--fg);
        background: var(--bg);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-1) var(--sp-2);
        font: inherit;
        font-size: var(--fs-md);
        width: 100%;
        box-sizing: border-box;
        min-width: 0;
      }
      .manual-ref-form textarea {
        min-height: 3rem;
      }
      /* Baseline align so rows with conflict chips (which take an
         extra text line) don't push their label off-center. */
      .manual-ref-form {
        align-items: baseline;
      }
      /* Column-2 cell that wraps input + inline conflict chips.
         Keeps the input at column width while allowing the chip
         to sit on its own line below without breaking the grid. */
      .manual-ref-form .input-cell {
        display: grid;
        gap: var(--sp-1);
        min-width: 0;
      }
      .manual-ref-form .field-conflict {
        display: flex;
        flex-wrap: wrap;
        gap: var(--sp-1);
      }
      /* Conflict chip: small, unobtrusive button below the input
         labelled with the source (GROBID / OpenAlex) and the
         alternative value. Clicking swaps the input's value. */
      .manual-ref-form .conflict-chip {
        display: inline-flex;
        align-items: baseline;
        gap: var(--sp-1);
        padding: 0.1rem var(--sp-1);
        border: 1px solid var(--border);
        border-radius: var(--radius-sm);
        background: color-mix(in oklab, var(--fg) 3%, transparent);
        color: var(--fg);
        cursor: pointer;
        font: inherit;
        font-size: var(--fs-xs);
        max-width: 100%;
        overflow: hidden;
      }
      .manual-ref-form .conflict-chip:hover {
        border-color: var(--accent);
      }
      .manual-ref-form .conflict-chip .source {
        color: var(--dim);
        text-transform: uppercase;
        letter-spacing: 0.03em;
      }
      .manual-ref-form .conflict-chip .value {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        max-width: 32ch;
      }
      .show-all-toggle {
        margin-top: var(--sp-2);
        display: flex;
        justify-content: flex-start;
      }
      h3 {
        margin: 0;
        font-size: var(--fs-lg);
      }
      .modal-header {
        display: flex;
        justify-content: space-between;
        align-items: center;
        gap: var(--sp-2);
      }
      .header-actions {
        display: flex;
        align-items: center;
        gap: var(--sp-1);
      }
      .tabs {
        display: flex;
        gap: var(--sp-1);
        border-bottom: 1px solid var(--border);
      }
      .tabs button {
        background: transparent;
        color: var(--fg);
        border: 1px solid transparent;
        border-bottom: none;
        border-radius: var(--radius-md) var(--radius-md) 0 0;
        padding: var(--sp-1) var(--sp-3);
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
        gap: var(--sp-2);
      }
      /* File-tab drop zone. Dashed outline signals "you can drop
         here"; hover / drag-active adds an accent tint to make the
         current target obvious. The modal's outer .drag-active
         mirror-class also highlights the whole modal when a file
         is being dragged in from outside, so a curator dropping on
         any tab sees a consistent "yes, I'm accepting this" cue. */
      .file-drop {
        border: 2px dashed var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-4);
        display: grid;
        gap: var(--sp-1);
        justify-items: center;
        color: var(--dim);
        cursor: pointer;
        transition: border-color 0.1s, background 0.1s;
      }
      .file-drop:hover,
      .file-drop.active,
      .modal.drag-active .file-drop {
        border-color: var(--accent);
        background: color-mix(in oklab, var(--accent) 8%, transparent);
        color: var(--fg);
      }
      .file-drop .hint {
        font-size: var(--fs-xs);
        color: var(--dim);
      }
      .upload-result {
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-2) var(--sp-3);
        display: grid;
        gap: var(--sp-1);
        font-size: var(--fs-sm);
      }
      .upload-result.dedupe {
        border-color: color-mix(in oklab, var(--sev-warn) 40%, transparent);
        background: color-mix(in oklab, var(--sev-warn) 10%, transparent);
      }
      .dedupe-title {
        font-weight: 600;
      }
      .dedupe-refs {
        list-style: none;
        margin: var(--sp-1) 0 0;
        padding: 0;
        display: grid;
        gap: var(--sp-1);
      }
      .dedupe-refs button.link {
        background: none;
        border: none;
        color: var(--link);
        cursor: pointer;
        text-align: left;
        padding: 0;
        font: inherit;
        text-decoration: underline;
      }
      .dedupe-refs button.link:hover {
        color: var(--link-hover, var(--link));
      }
      .upload-result code {
        font-family: var(--font-mono);
        font-size: var(--fs-xs);
        background: color-mix(in oklab, var(--fg) 5%, transparent);
        padding: 0.05rem 0.3rem;
        border-radius: 3px;
      }
      /* Local input styles cover the picker text fields and BibTeX
         textarea. Not composed from formFieldStyles because the
         textarea here wants a monospaced payload font at fs-sm and
         a taller min-height than the shared default. */
      input[type="text"],
      textarea {
        background: var(--bg);
        color: var(--fg);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-1) var(--sp-2);
        font: inherit;
      }
      textarea {
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
        min-height: 8rem;
      }
      .hit {
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-2);
        display: grid;
        gap: var(--sp-1);
      }
      .hit .title {
        font-weight: 500;
      }
      .hit .meta {
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      .hit .actions {
        display: flex;
        gap: var(--sp-1);
        justify-content: flex-end;
      }
      .quality {
        display: inline-block;
        padding: 0 var(--sp-1);
        border: 1px solid var(--border);
        border-radius: var(--radius-sm);
        font-family: var(--font-mono);
        font-size: var(--fs-xs);
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
      /* OpenAlex DOI-recovery suggestions panel above the Manual
         form. Only appears after an upload where GROBID pulled
         header metadata but no DOI resolved from the PDF text. Each
         candidate is a compact card; the "likely" match (normalized-
         title equality with GROBID's extracted title) gets an accent
         border to draw the eye. */
      .oa-candidates {
        display: grid;
        gap: var(--sp-2);
        padding: var(--sp-2);
        margin-bottom: var(--sp-2);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: color-mix(in oklab, var(--fg) 3%, transparent);
      }
      .oa-candidates .hint {
        margin: 0;
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      .oa-candidate {
        display: grid;
        gap: var(--sp-1);
        padding: var(--sp-2);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: var(--bg);
      }
      .oa-candidate.likely {
        border-color: var(--accent);
      }
      .oa-candidate .title {
        font-weight: 500;
      }
      .oa-candidate .meta {
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      .oa-candidate .meta code {
        font-family: var(--font-mono);
        font-size: var(--fs-xs);
      }
      .oa-candidate .actions {
        display: flex;
        justify-content: flex-end;
      }
      /* Conflict picker for GROBID-vs-OpenAlex disagreements in the
         resolved-DOI preview. Two labeled radio options stacked
         inside the dd cell; the picked side gets an accent border
         so the current choice reads at a glance. */
      .conflict-picker {
        display: grid;
        gap: var(--sp-1);
      }
      .conflict-picker .pick {
        display: grid;
        grid-template-columns: max-content max-content 1fr;
        gap: var(--sp-1) var(--sp-2);
        align-items: baseline;
        padding: var(--sp-1) var(--sp-2);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        cursor: pointer;
      }
      .conflict-picker .pick.picked {
        border-color: var(--accent);
        background: color-mix(in oklab, var(--accent) 6%, transparent);
      }
      .conflict-picker .source {
        color: var(--dim);
        font-size: var(--fs-xs);
        text-transform: uppercase;
        letter-spacing: 0.03em;
      }
      .conflict-picker .value {
        overflow-wrap: anywhere;
      }
      .conflict-picker em {
        color: var(--dim);
        font-style: italic;
      }
      .preview dl {
        margin: 0;
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: var(--sp-1) var(--sp-3);
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
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
        gap: var(--sp-1);
        margin-top: var(--sp-2);
      }
      .error {
        color: var(--error);
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
      }
      .empty {
        color: var(--dim);
        font-style: italic;
      }
    `,
  ];

  constructor() {
    super();
    this.contextCanonical = "";
    this.contextAuthors = "";
    this.contextYear = 0;
    this.defaultTab = "project";
    this.editID = "";
    this.jatsRef = null;
    this._tab = 0;
    this._busy = false;
    this._error = "";
    this._projectQuery = "";
    this._projectHits = [];
    this._projectDoiPreview = null;
    this._projectDoiBusy = false;
    this._projectDoiError = "";
    this._bhlHits = [];
    this._bhlLoaded = false;
    this._doiInput = "";
    this._bibtexInput = "";
    this._preview = null;
    this._previewOpenAlex = null;
    this._openalexCandidates = [];
    this._openalexCandidatesBusy = false;
    this._openalexCandidatesError = "";
    this._uploadBusy = false;
    this._uploadError = "";
    this._uploadResult = null;
    this._rejectedDOI = "";
    this._dragActive = false;
    this._showPDF = true;
    this._manual = null;
    this._manualOriginal = null;
    // Default to essentials-only in add mode; edit mode flips it on
    // in connectedCallback so existing values are always visible.
    this._showAllFields = false;
    this._manualBusy = false;
    this._manualError = "";
    this._issues = [];
  }

  async _loadForEdit(id) {
    this._manualBusy = true;
    try {
      const [ref, issueResp] = await Promise.all([
        api.reference.get(id),
        api.issue
          .list({ table: "reference", record_id: id, limit: 100 })
          .catch(() => ({ items: [] })),
      ]);
      // Guard: modal may have been closed / reopened on a different
      // id while the fetch was in flight.
      if (this.editID !== id) return;
      this._manualOriginal = ref;
      this._manual = manualFromReference(ref);
      const persisted = issueResp.items || [];
      // Mirror the picker's inline missing-structured-metadata check
      // so the banner agrees with the badge. The picker's
      // referenceIssueCount fires when citation is set but author or
      // issued is missing, even before the server-side
      // hive_reference_missing_structured_metadata rule has been
      // persisted via reindex. Without this parity, curators who
      // open the modal via the badge would see a clean-looking
      // modal despite the badge indicating an issue.
      const inline = this._syntheticMetadataIssue(ref, persisted);
      this._issues = inline ? [inline, ...persisted] : persisted;
    } catch (err) {
      if (this.editID !== id) return;
      this._manualError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._manualBusy = false;
    }
  }

  // _syntheticMetadataIssue returns a synthetic issue matching the
  // hive_reference_missing_structured_metadata rule when the loaded
  // reference has a citation string but is missing author or issued.
  // Returns null when the check doesn't apply, or when the same rule
  // is already in the persisted list (avoids double-listing).
  _syntheticMetadataIssue(ref, persisted) {
    const citation = (ref.citation || "").trim();
    const author = (ref.author || "").trim();
    const issued = (ref.issued || "").trim();
    if (!citation) return null;
    if (author && issued) return null;
    // Already surfaced by the persisted rule row — don't double-list.
    if (
      persisted.some(
        (i) => i.rule_id === "hive_reference_missing_structured_metadata",
      )
    ) {
      return null;
    }
    const missing = !author && !issued
      ? "author + issued"
      : !author
        ? "author"
        : "issued (year)";
    return {
      id: "synthetic-metadata-gap",
      rule_id: "hive_reference_missing_structured_metadata",
      rule_name: "Reference missing structured metadata",
      field_name: "col__citation",
      severity: "warn",
      message: `Reference has a citation but is missing structured ${missing}.`,
    };
  }

  static _tabIndex(name) {
    switch (name) {
      case "bhlnames":
        return 1;
      case "doi":
        return 2;
      case "bibtex":
        return 3;
      case "file":
        return 4;
      case "manual":
        return 5;
      default:
        return 0;
    }
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
    this._previewOpenAlex = null;
  }

  connectedCallback() {
    super.connectedCallback();
    // Edit-mode init: land on Manual so the curator sees loaded
    // fields immediately, kick off the record + issues fetch.
    // Non-edit mode: use the caller-supplied defaultTab.
    //
    // _manual is initialized synchronously with an empty template so
    // any curator interaction before _loadForEdit resolves produces
    // a complete field set (partial state → other inputs render
    // undefined → literal "undefined" text — the bug this init
    // prevents). _loadForEdit overwrites with the hydrated record
    // when the fetch lands.
    this._manual = emptyManualReference();
    if (this.editID) {
      // Edit mode: reveal all fields so existing values are always
      // visible (progressive disclosure applies only to add-mode
      // pre-fills where hiding empty inputs reduces noise).
      this._showAllFields = true;
      this._tab = SfgaAddReferenceModal._tabIndex("manual");
      this._loadForEdit(this.editID);
    } else if (this.jatsRef) {
      // JATS-ref edit mode: seed the Manual tab from the fields
      // the reader sent inline. No fetch — the reader already
      // has the parsed JATS ref data. All fields visible for the
      // same "curator can see everything at once" reason.
      this._showAllFields = true;
      this._tab = SfgaAddReferenceModal._tabIndex("manual");
      this._manual = { ...emptyManualReference(), ...(this.jatsRef.fields || {}) };
      // Store the original snapshot so _saveManual's diff logic
      // reuses the same code path as the sfga-reference edit case.
      this._manualOriginal = { ...this._manual };
    } else {
      this._tab = SfgaAddReferenceModal._tabIndex(this.defaultTab);
    }
    // Escape dismisses via the same guarded path as the × button and
    // the footer close, so a stray key doesn't discard a fetched
    // preview or a long BibTeX paste.
    this._onDocKey = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        this._requestClose();
      }
    };
    document.addEventListener("keydown", this._onDocKey);
    // Register with the shared modal stack so nested modals opened on
    // top of us hide us via is-covered, and so we hide any modal
    // opened underneath us. requestUpdate on stack changes so our
    // render sees the updated topmost state.
    this._modalStackID = openModal();
    this._unsubModalStack = subscribeModalStack(() => this.requestUpdate());
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    if (this._onDocKey) {
      document.removeEventListener("keydown", this._onDocKey);
      this._onDocKey = null;
    }
    if (this._releaseFocus) {
      this._releaseFocus();
      this._releaseFocus = null;
    }
    if (this._unsubModalStack) {
      this._unsubModalStack();
      this._unsubModalStack = null;
    }
    if (this._modalStackID) {
      closeModal(this._modalStackID);
      this._modalStackID = null;
    }
  }

  firstUpdated() {
    // Trap focus inside the modal and land initial focus in the first
    // input of the active tab (project search is the default tab).
    this._releaseFocus = trapFocus(this.renderRoot, {
      initialFocus: "input, textarea",
    });
    // Auto-upload a file the caller handed us via initialFile
    // (drag-and-drop path from SfgaReferences). Routes to the File
    // tab first so the progress + result render there, then
    // triggers the same upload path the tab's file picker uses.
    if (this.initialFile) {
      this._tab = SfgaAddReferenceModal._tabIndex("file");
      this._uploadFile(this.initialFile);
    }
  }

  _close() {
    this.dispatchEvent(new CustomEvent("close", { bubbles: true, composed: true }));
  }

  // _isDirty flags the state worth confirming before dismissal: a
  // typed DOI or BibTeX payload, or a fetched preview awaiting the
  // add-and-pick step. Project-tab search text is treated as
  // ephemeral filter state and doesn't gate the prompt.
  _isDirty() {
    return (
      (this._doiInput || "").trim().length > 0 ||
      (this._bibtexInput || "").trim().length > 0 ||
      this._preview != null
    );
  }

  async _requestClose() {
    if (this._isDirty()) {
      // Two-choice: there's no "save" path here — the affirmative
      // action for a pending reference is the add-and-pick button
      // on the tab itself.
      const choice = await confirmDirty({
        heading: "Discard pending reference",
        message:
          "This reference hasn't been added yet. Discard it?",
        canSave: false,
      });
      if (choice !== "discard") return;
    }
    this._close();
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
    const covered =
      this._modalStackID && !isTopModal(this._modalStackID)
        ? " is-covered"
        : "";
    // Wide-mode modal when a PDF upload is available AND the
    // curator hasn't collapsed the viewer. Takes over most of the
    // viewport so the side-by-side PDF + form layout has room.
    const wide =
      this._uploadResult && this._uploadResult.sha512 && this._showPDF
        ? " wide"
        : "";
    return html`
      <div class=${"backdrop" + covered}>
        <div
          class=${"modal" +
          (this._dragActive ? " drag-active" : "") +
          wide}
          role="dialog"
          aria-modal="true"
          aria-labelledby="add-ref-title"
          @dragenter=${(e) => this._onModalDragEnter(e)}
          @dragover=${(e) => this._onModalDragOver(e)}
          @dragleave=${(e) => this._onModalDragLeave(e)}
          @drop=${(e) => this._onModalDrop(e)}
        >
          <div class="modal-header">
            <h3 id="add-ref-title">
              ${this.editID ? "Edit reference" : "Add reference"}
            </h3>
            <div class="header-actions">
              ${this._uploadResult &&
              this._uploadResult.sha512 &&
              this._uploadResult.annotation_name_count > 0
                ? html`<button
                    type="button"
                    @click=${() =>
                      window.open(
                        `/read.html?sha=${encodeURIComponent(this._uploadResult.sha512)}`,
                        "_blank",
                      )}
                    title="Open the annotated reader in a new window (${this._uploadResult.annotation_name_count} names detected)"
                  >
                    Reader ↗
                  </button>`
                : ""}
              ${this._uploadResult && this._uploadResult.sha512
                ? html`<button
                    type="button"
                    @click=${() => this._togglePDF()}
                    title=${this._showPDF ? "hide PDF viewer" : "show PDF viewer"}
                  >
                    ${this._showPDF ? "Hide PDF" : "Show PDF"}
                  </button>`
                : ""}
              <button
                class="close-x"
                type="button"
                @click=${() => this._requestClose()}
                title="close"
                aria-label="close"
              >
                ×
              </button>
            </div>
          </div>
          ${this._renderIssuesBanner()}
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
            <button role="tab" aria-selected=${this._tab === 4} @click=${() => this._selectTab(4)}>
              File
            </button>
            <button role="tab" aria-selected=${this._tab === 5} @click=${() => this._selectTab(5)}>
              Manual
            </button>
          </div>
          <div class=${"pane" + (wide ? " side-by-side" : "")}>
            ${wide ? this._renderPDFViewer() : ""}
            <div class="pane-body">
              ${this._tab === 0 ? this._renderProjectPane() : ""}
              ${this._tab === 1 ? this._renderBHLnamesPane() : ""}
              ${this._tab === 2 ? this._renderDOIPane() : ""}
              ${this._tab === 3 ? this._renderBibTeXPane() : ""}
              ${this._tab === 4 ? this._renderFilePane() : ""}
              ${this._tab === 5 ? this._renderManualPane() : ""}
            </div>
          </div>
          <div class="toolbar">
            ${this._tab === 5
              ? html`<button
                    type="button"
                    class="primary"
                    ?disabled=${this._manualBusy}
                    @click=${() => this._saveManual()}
                  >
                    ${this._manualBusy
                      ? "Saving…"
                      : this.editID
                        ? "Save"
                        : "Add & pick"}
                  </button>`
              : ""}
            <button @click=${() => this._requestClose()}>Close</button>
          </div>
        </div>
      </div>
    `;
  }

  // ---------- Tab 0: Project ----------
  _renderProjectPane() {
    const isDOI = !!extractDOI(this._projectQuery);
    const emptyLocal =
      this._projectHits.length === 0 &&
      this._projectQuery.length >= 2 &&
      !this._busy;
    return html`
      <p class="empty">
        Search references already in this archive by author, title,
        citation, or DOI. Picking one links the name to it — nothing
        new is written. Type a DOI the archive doesn't have and hive
        will fetch it from OpenAlex for you.
      </p>
      <input
        type="text"
        placeholder="Author / title / citation / DOI…"
        .value=${this._projectQuery}
        @input=${(e) => this._onProjectInput(e.target.value)}
        autofocus
      />
      ${this._busy ? html`<div class="empty" role="status">Searching…</div>` : ""}
      ${emptyLocal && !isDOI
        ? html`<div class="empty" role="status">No matches</div>`
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
      ${emptyLocal && isDOI && this._projectDoiBusy
        ? html`<div class="empty" role="status">
            Not in archive — looking up on OpenAlex…
          </div>`
        : ""}
      ${this._projectDoiError
        ? html`<div class="error" role="alert">
            OpenAlex lookup failed: ${this._projectDoiError}
          </div>`
        : ""}
      ${this._projectDoiPreview
        ? html`<div class="hit doi-preview">
            <div class="title">
              ${this._projectDoiPreview.title ||
              this._projectDoiPreview.citation ||
              this._projectDoiPreview.doi}
            </div>
            <div class="meta">
              ${this._projectDoiPreview.author || ""}${this._projectDoiPreview
                .issued
                ? ` (${(this._projectDoiPreview.issued + "").slice(0, 4)})`
                : ""}
              · resolved from OpenAlex
            </div>
            <div class="actions">
              <button
                class="primary"
                ?disabled=${this._projectDoiBusy}
                @click=${() => this._saveProjectDoiPreview()}
              >
                ${this._projectDoiBusy ? "Saving…" : "Add to archive + pick"}
              </button>
            </div>
          </div>`
        : ""}
    `;
  }

  async _onProjectInput(q) {
    this._projectQuery = q;
    this._projectDoiPreview = null;
    this._projectDoiError = "";
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
      // DOI fallback — if the query looks like a DOI (bare, URL, or
      // curie form) AND the local search found nothing, auto-resolve
      // via OpenAlex so the curator sees a save-and-pick preview
      // inline. Detection guarded to well-formed DOIs only so a stray
      // author-name search doesn't hit OpenAlex.
      const doi = extractDOI(q);
      if (doi && this._projectHits.length === 0) {
        this._projectDoiBusy = true;
        try {
          const ref = await api.reference.resolveDOI(doi);
          if (this._projectQuery === q) this._projectDoiPreview = ref;
        } catch (err) {
          if (this._projectQuery === q) {
            this._projectDoiError =
              err.detail || err.message || String(err);
          }
        } finally {
          this._projectDoiBusy = false;
        }
      }
    }, 200);
  }

  // _saveProjectDoiPreview persists the OpenAlex-resolved preview to
  // the archive and picks it in one step, so the curator lands back
  // in the source form with the newly-added reference selected.
  async _saveProjectDoiPreview() {
    const preview = this._projectDoiPreview;
    if (!preview) return;
    this._projectDoiBusy = true;
    try {
      const saved = await api.reference.create(preview);
      this._pick(saved.id, referenceHitLabel(saved));
    } catch (err) {
      this._projectDoiError = err.detail || err.message || String(err);
    } finally {
      this._projectDoiBusy = false;
    }
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
      ${this._busy ? html`<div class="empty" role="status">Searching BHLnames…</div>` : ""}
      ${this._error ? html`<div class="error" role="alert">${this._error}</div>` : ""}
      ${this._bhlLoaded && this._bhlHits.length === 0 && !this._busy
        ? html`<div class="empty" role="status">No BHLnames matches</div>`
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
              <button @click=${() => { this._manual = manualFromReference(h.reference); this._previewOpenAlex = null; this._tab = SfgaAddReferenceModal._tabIndex("manual"); }}>Review & edit</button>
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
          ${this._busy ? "Resolving…" : "Resolve"}
        </button>
      </div>
      ${this._error ? html`<div class="error" role="alert">${this._error}</div>` : ""}
      ${this._preview ? this._renderPreview() : ""}
    `;
  }

  async _resolveDOI() {
    const doi = this._doiInput.trim();
    if (!doi) return;
    this._busy = true;
    this._error = "";
    this._preview = null;
    this._previewOpenAlex = null;
    try {
      const ref = await api.reference.resolveDOI(doi);
      // Land on the Manual tab with the resolved reference pre-
      // filled — the curator edits + saves from one place instead
      // of hopping between a read-only preview and the form. Keep
      // _previewOpenAlex around so the inline conflict picker can
      // still compare against GROBID metadata (when present).
      this._previewOpenAlex = ref;
      this._manual = manualFromReference(ref);
      this._tab = SfgaAddReferenceModal._tabIndex("manual");
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
          ${this._busy ? "Parsing…" : "Parse"}
        </button>
      </div>
      ${this._error ? html`<div class="error" role="alert">${this._error}</div>` : ""}
      ${this._preview ? this._renderPreview() : ""}
    `;
  }

  async _parseBibTeX() {
    const text = this._bibtexInput.trim();
    if (!text) return;
    this._busy = true;
    this._error = "";
    this._preview = null;
    this._previewOpenAlex = null;
    try {
      const ref = await api.reference.parseBibTeX(text);
      this._manual = manualFromReference(ref);
      this._tab = SfgaAddReferenceModal._tabIndex("manual");
    } catch (err) {
      this._error = err.detail || err.message || String(err);
    } finally {
      this._busy = false;
    }
  }

  // ---------- Shared preview panel ----------
  //
  // When both an OpenAlex-resolved reference and GROBID-extracted
  // header metadata are present (PDF upload → DOI extracted →
  // OpenAlex resolve), the preview shows a per-field conflict picker
  // wherever the two sources disagree. Curator picks per field; the
  // saved reference reflects the picks. Fields where only one source
  // has a value, or where both agree, render as plain rows.
  _renderPreview() {
    const r = this._preview;
    const grobid = this._uploadResult && this._uploadResult.metadata;
    const openalex = this._previewOpenAlex;
    // Field descriptors — [label, previewKey, projector].
    // projector maps a source object (openalex reference or GROBID
    // apiUploadBibHeader) to the display string for that field.
    const fields = [
      ["Author", "author", (s) => s.author || ""],
      ["Year", "issued", (s) => (s.issued ? String(s.issued).slice(0, 4) : "")],
      ["Title", "title", (s) => s.title || ""],
      ["Container", "container_title", (s) => s.container_title || ""],
      [
        "Volume/Issue",
        "__volume_issue",
        (s) => (s.volume ? `${s.volume}${s.issue ? "(" + s.issue + ")" : ""}` : ""),
      ],
      ["Page", "page", (s) => s.page || ""],
      ["DOI", "doi", (s) => s.doi || ""],
      ["Type", "type", (s) => s.type || ""],
    ];
    const rows = fields
      .map(([label, key, project]) => {
        const current = project(r);
        const oaValue = openalex ? project(openalex) : "";
        // GROBID's shape is apiUploadBibHeader — same keys except
        // container_title comes through, no doi (already handled
        // upstream) and no type. Project against it defensively.
        const grobidValue = grobid ? project(grobid) : "";
        return { label, key, current, oaValue, grobidValue };
      })
      .filter((row) => row.current || row.oaValue || row.grobidValue);
    const anyConflict = rows.some(
      (row) => this._isFieldConflict(row.oaValue, row.grobidValue),
    );
    return html`
      <div class="preview" style="border-top: 1px solid var(--border); padding-top: 0.5rem">
        ${anyConflict
          ? html`<p class="empty" style="margin: 0 0 var(--sp-1) 0">
              GROBID (from the PDF) and OpenAlex disagree on the
              highlighted fields. Pick the correct value per field;
              save when done.
            </p>`
          : ""}
        <dl>
          ${rows.map((row) => this._renderPreviewRow(row))}
        </dl>
        <div class="toolbar">
          <button @click=${() => this._discardPreview()}>Discard</button>
          <button class="primary" @click=${() => this._createAndPick(this._preview)}>
            ${this._busy ? "Saving…" : "Add & pick"}
          </button>
        </div>
      </div>
    `;
  }

  _renderPreviewRow(row) {
    const conflict = this._isFieldConflict(row.oaValue, row.grobidValue);
    if (!conflict) {
      return html`<dt>${row.label}</dt><dd>${row.current}</dd>`;
    }
    const pickedOA =
      this._normalizeForCompare(row.current) ===
      this._normalizeForCompare(row.oaValue);
    return html`
      <dt>${row.label}</dt>
      <dd>
        <div class="conflict-picker">
          <label class=${"pick" + (pickedOA ? " picked" : "")}>
            <input
              type="radio"
              name=${"pick-" + row.key}
              ?checked=${pickedOA}
              @change=${() => this._pickFieldValue(row.key, row.oaValue)}
            />
            <span class="source">OpenAlex</span>
            <span class="value">${row.oaValue || html`<em>(empty)</em>`}</span>
          </label>
          <label class=${"pick" + (!pickedOA ? " picked" : "")}>
            <input
              type="radio"
              name=${"pick-" + row.key}
              ?checked=${!pickedOA}
              @change=${() => this._pickFieldValue(row.key, row.grobidValue)}
            />
            <span class="source">GROBID (PDF)</span>
            <span class="value">${row.grobidValue || html`<em>(empty)</em>`}</span>
          </label>
        </div>
      </dd>
    `;
  }

  // _pickFieldValue mutates _preview to reflect the curator's choice
  // for a conflict field. The composite Volume/Issue key needs a
  // split back into vol + issue so the saved reference carries them
  // as separate columns; all other keys map 1:1.
  _pickFieldValue(key, value) {
    const next = { ...this._preview };
    if (key === "__volume_issue") {
      const m = /^([^(]*)(?:\(([^)]*)\))?$/.exec(value || "");
      next.volume = m ? m[1].trim() : "";
      next.issue = m && m[2] ? m[2].trim() : "";
    } else if (key === "issued") {
      // GROBID gives year only; OpenAlex may give full date. Preserve
      // whatever the source had rather than truncating.
      next.issued = value || "";
    } else {
      next[key] = value || "";
    }
    this._preview = next;
  }

  _discardPreview() {
    this._preview = null;
    this._previewOpenAlex = null;
  }

  // Field-conflict predicate. Both sides must have non-empty values;
  // if either is empty, treat as "one source missing" (not a conflict).
  // Normalization strips HTML tags, collapses whitespace, and
  // lower-cases so cosmetic differences (an italicized taxon name,
  // an en-dash vs a hyphen in a page range) don't trigger the picker.
  _isFieldConflict(a, b) {
    if (!a || !b) return false;
    return this._normalizeForCompare(a) !== this._normalizeForCompare(b);
  }

  _normalizeForCompare(s) {
    if (!s) return "";
    return String(s)
      .replace(/<[^>]+>/g, " ")
      .replace(/[–—]/g, "-")
      .replace(/\s+/g, " ")
      .trim()
      .toLowerCase();
  }

  async _createAndPick(ref) {
    this._busy = true;
    this._error = "";
    try {
      // Strip id so core.CreateReference mints a fresh UUID.
      const body = { ...ref, id: "" };
      // If a PDF was uploaded during this modal session, link its
      // sidecar sha to the newly-created reference so the reader
      // can find it later from the references screen.
      const attachSha =
        this._uploadResult && this._uploadResult.sha512
          ? this._uploadResult.sha512
          : "";
      const saved = await api.reference.create(body, attachSha);
      this._pick(saved.id, referenceHitLabel(saved));
    } catch (err) {
      this._error = err.detail || err.message || String(err);
    } finally {
      this._busy = false;
    }
  }

  // ---------- Tab 4: File ----------
  //
  // Drag-and-drop or click-to-pick a source document (PDF / EPUB /
  // HTML / JATS) into the archive's sidecar library. When GROBID
  // is configured on the server side, PDFs are converted to TEI
  // XML alongside the source. The server extracts the first DOI
  // it finds in the TEI and returns it in the upload response;
  // hive auto-hops to the DOI tab and fires the OpenAlex resolve
  // so the curator lands on a bibliographic preview without any
  // copy-paste. If no DOI surfaces, the tab stays put and shows a
  // "no DOI detected — try another tab" hint.
  //
  // Modal-wide dragover/drop handlers let a curator drop a file on
  // any tab; the drop always routes here.
  // _renderPDFViewer renders the left pane of the wide-mode layout:
  // an iframe pointing at the sidecar's source-stream endpoint. Only
  // called when this._uploadResult.sha512 is set (a file has been
  // ingested this session) and this._showPDF is true. Non-PDF formats
  // (EPUB, HTML, JATS) also render — the browser handles PDF and HTML
  // natively; other types download instead of previewing, which is
  // an acceptable degradation until per-format viewers land.
  _renderPDFViewer() {
    const sha = this._uploadResult && this._uploadResult.sha512;
    if (!sha) return "";
    // #page=1 is a hint to the browser's PDF viewer for the initial
    // page. Non-PDF viewers ignore the fragment.
    const src = `/api/reference/library/${sha}/source#page=1`;
    return html`
      <div class="pdf-viewer">
        <iframe
          src=${src}
          title="Uploaded source document"
        ></iframe>
      </div>
    `;
  }

  _togglePDF() {
    this._showPDF = !this._showPDF;
  }

  _renderFilePane() {
    const busy = this._uploadBusy;
    const err = this._uploadError;
    const res = this._uploadResult;
    return html`
      <p class="empty">
        Drop a PDF (or EPUB / HTML / JATS) anywhere on this modal,
        or click below to pick one. Hive stores it in the archive's
        library and, when GROBID is running, extracts bibliographic
        metadata + a DOI. If a DOI is found, you'll auto-hop to the
        DOI tab with the reference pre-populated.
      </p>
      <div
        class=${"file-drop" + (this._dragActive ? " active" : "")}
        @click=${() => this._openFilePicker()}
      >
        ${busy
          ? html`<span>Uploading${res && res.tei_status === "stored" ? " + processing with GROBID" : ""}…</span>`
          : html`
              <span>${renderIcon("plus", 16)} Drop a file here or click to pick</span>
              <span class="hint">pdf · epub · html · jats · xml</span>
            `}
      </div>
      <input
        type="file"
        id="file-picker"
        accept=".pdf,.epub,.html,.htm,.xml,.jats,application/pdf,application/epub+zip,text/html,application/xml"
        style="display: none"
        @change=${(e) => {
          const f = e.target.files && e.target.files[0];
          if (f) this._uploadFile(f);
          e.target.value = "";
        }}
      />
      ${err
        ? html`<div class="error" role="alert">${err}</div>`
        : ""}
      ${res && res.attached_references && res.attached_references.length > 0
        ? html`
            <div class="upload-result dedupe" role="status">
              <div class="dedupe-title">
                This PDF is already attached to
                ${res.attached_references.length === 1
                  ? "a reference"
                  : `${res.attached_references.length} references`}
              </div>
              <div class="hint">
                No need to fill out bibliographic info — jump to an
                existing reference or dismiss this dialog.
              </div>
              <ul class="dedupe-refs">
                ${res.attached_references.map(
                  (r) => html`<li>
                    <button
                      type="button"
                      class="link"
                      @click=${() => this._pickExistingReference(r.id)}
                      title="Open this reference"
                    >
                      ${r.title || r.citation || r.id}
                    </button>
                  </li>`,
                )}
              </ul>
            </div>
          `
        : res
          ? html`
              <div class="upload-result">
                <div><strong>${res.ext.toUpperCase()}</strong> stored (${formatBytes(res.size)})</div>
                <div class="hint">
                  ${res.already_existed
                    ? "identical file already in the library — deduplicated on hash"
                    : "added to the library"}
                </div>
                ${this._renderTEIStatusHint(res)}
              </div>
            `
          : ""}
    `;
  }

  // _pickExistingReference dispatches the standard reference-picked
  // event so the parent (SfgaReferences) selects the pre-existing
  // reference. Same wire as the normal pick flow.
  _pickExistingReference(id) {
    this.dispatchEvent(
      new CustomEvent("reference-picked", {
        detail: { id },
        bubbles: true,
        composed: true,
      }),
    );
    this._close();
  }

  // _renderTEIStatusHint explains what happened on the TEI /
  // GROBID pass in curator-facing language. Only shown after an
  // upload lands (res != null). The DOI-found case has already
  // triggered an auto-hop before render, so if we're still on the
  // file tab and status is "stored", the paper had no extractable
  // DOI.
  _renderTEIStatusHint(res) {
    switch (res.tei_status) {
      case "stored":
        if (res.doi && !this._rejectedDOI) {
          return html`<div class="hint">
            DOI <code>${res.doi}</code> extracted — switching to the DOI tab.
          </div>`;
        }
        if (this._rejectedDOI) {
          return html`<div class="hint">
            GROBID extracted DOI <code>${this._rejectedDOI}</code>,
            but it resolved to a paper whose title doesn't match
            this PDF — likely a citation in the paper text (e.g. a
            database credit) misclassified as this paper's own DOI.
            ${res.metadata
              ? "Falling back to the Manual tab with GROBID's header metadata; searching OpenAlex for a matching DOI."
              : "Please continue via the Project / BibTeX / Manual tabs."}
          </div>`;
        }
        if (res.metadata) {
          return html`<div class="hint">
            No DOI detected, but GROBID extracted header metadata
            (title, authors, journal). Switching to the Manual tab
            with fields pre-filled — please review before saving.
          </div>`;
        }
        return html`<div class="hint">
          TEI stored, but neither a DOI nor usable header metadata
          surfaced. Try the Project / BibTeX / Manual tabs.
        </div>`;
      case "not_configured":
        return html`<div class="hint">
          GROBID isn't configured, so hive stored the raw file
          without extracting metadata. Set
          <code>HIVE_GROBID_URL</code> or start the docker-compose
          stack (<code>just docker-up</code>) to enable automatic
          DOI extraction.
        </div>`;
      case "unavailable":
        return html`<div class="hint">
          GROBID is configured but didn't respond. The file is
          safely stored — the pipeline can retry later. See
          <code>docker compose logs grobid</code>.
        </div>`;
      case "skipped":
        return html`<div class="hint">
          ${res.ext.toUpperCase()} conversion isn't wired yet — file
          is stored, downstream steps (DOI extraction, taxonomic
          mining) don't run for this format in the current version.
        </div>`;
    }
    return "";
  }

  _openFilePicker() {
    const input = this.renderRoot.querySelector("#file-picker");
    if (input) input.click();
  }

  _onModalDragEnter(e) {
    if (!e.dataTransfer || !e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    this._dragActive = true;
  }
  _onModalDragOver(e) {
    if (!e.dataTransfer || !e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
  }
  _onModalDragLeave(e) {
    // Fires when the pointer moves between children — check
    // relatedTarget to skip the intra-modal transitions and only
    // clear when actually leaving the modal.
    if (
      e.relatedTarget &&
      e.currentTarget.contains &&
      e.currentTarget.contains(e.relatedTarget)
    ) {
      return;
    }
    this._dragActive = false;
  }
  _onModalDrop(e) {
    if (!e.dataTransfer || !e.dataTransfer.files || !e.dataTransfer.files.length) {
      return;
    }
    e.preventDefault();
    this._dragActive = false;
    // Belt-and-braces: also clear the document-level drop overlay
    // in case the window-level drop handler on References didn't
    // fire (some browsers stop bubbling when the topmost target
    // preventDefault-s a drop). Owned by SfgaReferences but safe
    // to clear from here — the attribute is document-global.
    document.body.removeAttribute("data-drop-active");
    const file = e.dataTransfer.files[0];
    // Route to the file tab so the progress + result render there,
    // then upload.
    this._tab = SfgaAddReferenceModal._tabIndex("file");
    this._uploadFile(file);
  }

  async _uploadFile(file) {
    if (!file || this._uploadBusy) return;
    this._uploadBusy = true;
    this._uploadError = "";
    this._uploadResult = null;
    this._rejectedDOI = "";
    try {
      const result = await api.reference.upload(file);
      this._uploadResult = result;
      // Dedupe short-circuit: the PDF is already attached to one or
      // more references. Show a "this PDF is already imported"
      // prompt in place of the normal DOI/manual flow — otherwise
      // curator wastes time filling out fields they'd end up
      // discarding when they realize (or don't realize) the
      // reference already exists.
      if (
        result.attached_references &&
        result.attached_references.length > 0
      ) {
        return;
      }
      // Post-upload routing:
      //   1. DOI extracted → resolve via OpenAlex, cross-check
      //      against GROBID's extracted title. If titles agree,
      //      hop to the DOI tab with the preview + conflict picker.
      //      If they disagree, GROBID likely grabbed a citation
      //      DOI (not the paper's own) — discard the DOI and fall
      //      through to case 2.
      //   2. No trusted DOI but GROBID pulled header metadata →
      //      Manual tab with fields pre-populated; fire the
      //      OpenAlex-by-metadata search in the background for
      //      DOI-recovery candidates.
      //   3. Nothing useful → stay on File tab with a hint.
      if (result.doi) {
        const accepted = await this._tryAcceptExtractedDOI(
          result.doi,
          result.metadata,
        );
        if (accepted) {
          // Land on Manual with the OpenAlex-resolved reference
          // pre-filled — GROBID's metadata is still in state, so
          // the inline conflict picker per input surfaces any
          // per-field disagreements between the two sources.
          this._manual = manualFromReference(this._previewOpenAlex);
          this._tab = SfgaAddReferenceModal._tabIndex("manual");
        } else if (result.metadata) {
          this._manual = manualFromMetadata(result.metadata);
          this._tab = SfgaAddReferenceModal._tabIndex("manual");
          this._lookupOpenAlexByMetadata(result.metadata);
        }
      } else if (result.metadata) {
        this._manual = manualFromMetadata(result.metadata);
        this._tab = SfgaAddReferenceModal._tabIndex("manual");
        // Fire OpenAlex search in the background — the paper may
        // have had a DOI retroactively assigned by a publisher
        // backfile program or DataCite (BHL) that isn't printed on
        // the copy the curator has. Candidates surface as a
        // suggestions panel above the Manual form when they arrive.
        this._lookupOpenAlexByMetadata(result.metadata);
      }
    } catch (err) {
      this._uploadError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._uploadBusy = false;
    }
  }

  // _tryAcceptExtractedDOI resolves the DOI GROBID pulled out of
  // the TEI header and validates it against GROBID's own title
  // extraction. GROBID sometimes assigns a DOI it found in the
  // paper text (a citation, a database credit) to the paper's own
  // metadata slot — the smoking gun is that the resolved paper's
  // title has no meaningful overlap with the extracted title. When
  // that happens, discard the DOI, remember it for the status hint,
  // and let the caller fall through to the DOI-recovery flow.
  //
  // Returns true when the DOI was accepted (preview populated,
  // ready to hop to DOI tab); false when rejected.
  async _tryAcceptExtractedDOI(doi, metadata) {
    this._doiInput = doi;
    try {
      const ref = await api.reference.resolveDOI(doi);
      const grobidTitle = metadata && metadata.title ? metadata.title : "";
      const score = this._titleMatchScore(grobidTitle, ref.title || "");
      // 0.4 threshold: allows for ~40% token overlap. In practice
      // right-DOI matches score >0.7 (title differs only in casing
      // / punctuation / markup); wrong-DOI matches — where GROBID
      // grabbed a citation — score <0.1.
      if (grobidTitle && score < 0.4) {
        this._rejectedDOI = doi;
        this._doiInput = "";
        this._preview = null;
        this._previewOpenAlex = null;
        return false;
      }
      this._preview = { ...ref };
      this._previewOpenAlex = ref;
      return true;
    } catch (err) {
      // Resolve failed (network, 404, whatever). Treat as rejection
      // so the flow falls through to the metadata-driven path.
      this._rejectedDOI = doi;
      this._doiInput = "";
      return false;
    }
  }

  // Token-set Jaccard on normalized titles. Word-order invariant;
  // tolerant of stopword mismatches because both titles usually
  // share most of the content-carrying tokens.
  _titleMatchScore(a, b) {
    const ta = new Set(
      this._normalizeForCompare(a).split(/\s+/).filter(Boolean),
    );
    const tb = new Set(
      this._normalizeForCompare(b).split(/\s+/).filter(Boolean),
    );
    if (!ta.size || !tb.size) return 0;
    let inter = 0;
    for (const t of ta) if (tb.has(t)) inter++;
    const union = ta.size + tb.size - inter;
    return inter / union;
  }

  // _lookupOpenAlexByMetadata searches OpenAlex using GROBID's
  // extracted title (required), first-author surname (parsed from
  // GROBID's "Given Surname; Given Surname; …" author field), and
  // publication year. Populates _openalexCandidates for the Manual
  // pane's suggestions panel to render. Errors are non-fatal — the
  // Manual pre-fill flow still works without OpenAlex.
  async _lookupOpenAlexByMetadata(meta) {
    if (!meta || !meta.title) return;
    this._openalexCandidatesBusy = true;
    this._openalexCandidatesError = "";
    this._openalexCandidates = [];
    try {
      let author = "";
      if (meta.author) {
        // "Given Surname; Given Surname; …" — take the first
        // author's last whitespace-separated token as the surname.
        const first = meta.author.split(";")[0].trim();
        const parts = first.split(/\s+/);
        author = parts[parts.length - 1] || "";
      }
      const res = await api.reference.lookupOpenAlex({
        title: meta.title,
        author,
        year: meta.issued || "",
      });
      this._openalexCandidates = (res && res.items) || [];
    } catch (err) {
      this._openalexCandidatesError =
        err instanceof Problem
          ? err.detail || err.message
          : String(err);
    } finally {
      this._openalexCandidatesBusy = false;
    }
  }

  // _useOpenAlexCandidate accepts a candidate the curator picked from
  // the DOI-recovery panel. Populates the preview state so the
  // standard conflict picker fires (GROBID metadata is still in
  // _uploadResult.metadata), hops to the DOI tab where the preview
  // renders, and clears the candidate list.
  _useOpenAlexCandidate(candidate) {
    if (!candidate || !candidate.doi) return;
    this._doiInput = candidate.doi;
    this._previewOpenAlex = candidate;
    this._manual = manualFromReference(candidate);
    this._tab = SfgaAddReferenceModal._tabIndex("manual");
    this._openalexCandidates = [];
  }

  // _renderOpenAlexCandidates renders the DOI-recovery suggestions
  // above the Manual form. Only shows candidates that have a DOI —
  // a match without a DOI isn't useful for recovery (and we already
  // have the metadata from GROBID). The one with a normalized-title
  // match against GROBID's extracted title is highlighted as the
  // likely match; others sit alongside as alternatives.
  _renderOpenAlexCandidates() {
    if (this._openalexCandidatesBusy) {
      return html`<p class="empty" role="status">
        Searching OpenAlex for a DOI that matches this PDF…
      </p>`;
    }
    if (this._openalexCandidatesError) {
      return html`<div class="error" role="alert">
        OpenAlex search: ${this._openalexCandidatesError}
      </div>`;
    }
    const grobidTitleRaw =
      this._uploadResult && this._uploadResult.metadata
        ? this._uploadResult.metadata.title || ""
        : "";
    const grobidTitle = this._normalizeForCompare(grobidTitleRaw);
    // Filter noisy hits: OpenAlex ranks by relevance regardless of
    // whether anything actually matches, so grey-literature papers
    // that Crossref doesn't index still return top-N by shared
    // stopwords or a single author-community token. Require a
    // meaningful title-overlap floor (~0.25 Jaccard on tokens);
    // below that the "match" is coincidence and showing it just
    // invites curators to pick the wrong DOI.
    const RELEVANT_JACCARD = 0.25;
    const withDOI = (this._openalexCandidates || [])
      .filter((c) => c.doi)
      .filter((c) => {
        if (!grobidTitleRaw) return true;
        return this._titleMatchScore(grobidTitleRaw, c.title || "") >= RELEVANT_JACCARD;
      });
    if (!withDOI.length) return "";
    return html`
      <div class="oa-candidates">
        <p class="hint">
          OpenAlex found ${withDOI.length}
          possible match${withDOI.length === 1 ? "" : "es"} for this PDF.
          Pick one to use its DOI (and compare against GROBID
          field-by-field), or keep filling out the Manual form.
        </p>
        ${withDOI.map((c) => {
          const likely =
            grobidTitle &&
            this._normalizeForCompare(c.title || "") === grobidTitle;
          return html`
            <div class=${"oa-candidate" + (likely ? " likely" : "")}>
              <div class="title">${c.title || html`<em>(no title)</em>`}</div>
              <div class="meta">
                ${c.author || "—"} ·
                ${c.issued ? String(c.issued).slice(0, 4) : "—"} ·
                ${c.container_title || "—"}
              </div>
              <div class="meta"><code>${c.doi}</code></div>
              <div class="actions">
                <button @click=${() => this._useOpenAlexCandidate(c)}>
                  ${likely ? "Use this DOI (likely match)" : "Use this DOI"}
                </button>
              </div>
            </div>
          `;
        })}
      </div>
    `;
  }

  // ---------- Tab 5: Manual ----------
  //
  // Full-fidelity apiReference form grouped by section. Type picker
  // at top drives per-type field emphasis (advisory only — nothing
  // hides). In edit mode, hydrates from the loaded reference and
  // PATCHes on save; in add mode, POSTs a fresh reference and picks
  // it (same as the other tabs).
  //
  // Rationale for a Manual tab even when Project/DOI/BibTeX exist:
  // curators sometimes have citation info that doesn't fit any of
  // the structured-lookup paths (unpublished works, private
  // communications, historical monographs BHL doesn't cover, papers
  // without DOIs). The other tabs solve the "find and import"
  // problem; Manual solves the "type it in" problem.
  _renderManualPane() {
    const d = this._manual || emptyManualReference();
    const set = (field) => (e) => this._manualFieldChange(field, e.target.value);
    if (this._manualBusy && !this._manualOriginal && this.editID) {
      return html`<p role="status" class="empty">Loading reference…</p>`;
    }
    return html`
      ${this._manualError
        ? html`<div class="error" role="alert">${this._manualError}</div>`
        : ""}
      ${this._rejectedDOI
        ? html`<p class="empty" style="margin: 0 0 var(--sp-2) 0">
            GROBID guessed DOI <code>${this._rejectedDOI}</code>,
            but it points to a different paper — probably a
            citation in the PDF text, not this paper's own DOI. It
            has been ignored; searching OpenAlex for other matches.
          </p>`
        : ""}
      ${this._renderOpenAlexCandidates()}
      <form
        class="manual-ref-form"
        @submit=${(e) => {
          e.preventDefault();
          this._saveManual();
        }}
      >
        <!--
          Field order: essentials first (Citation / Type / Author /
          Issued / Title / DOI — always shown), then descriptive
          + publication + identifier + free-form. Progressive
          disclosure hides non-essential rows that are empty and
          have no source-disagreement to surface, keeping the form
          short after an auto-fetch. Toggle at the bottom reveals
          the rest.
        -->
        <!--
          col__citation is a derived / cached field auto-composed
          from the structured fields at write time (see pkg/reference
          ComposeCitation). Removed from the Manual tab because a
          curator-typed value would get overwritten on save and
          that's a bad surprise. Curators shape the citation
          indirectly by editing the structured fields below.
        -->

        ${this._shouldShowManualField("type", true)
          ? html`
              <label>Type</label>
              <div class="input-cell">
                <sfga-combobox
                  min-search-chars="0"
                  placeholder="Reference type…"
                  .source=${vocabSource("reference_type")}
                  .resolver=${vocabResolver("reference_type")}
                  .value=${d.type || ""}
                  @pick=${(e) => this._manualFieldChange("type", e.detail.id)}
                ></sfga-combobox>
              </div>
            `
          : ""}

        ${this._renderManualTextField("author", "Author", {
          placeholder: "Surname, Initials; Surname, Initials",
          essential: true,
        })}
        ${this._renderManualTextField("issued", "Issued", {
          placeholder: "YYYY or YYYY-MM-DD",
          essential: true,
        })}
        ${this._renderManualTextField("title", "Title", { essential: true })}
        ${this._renderManualTextField("doi", "DOI", {
          placeholder: "10.xxxx/xxxxx",
          essential: true,
        })}
        ${this._renderManualTextField("container_title", "Container title", {
          placeholder: "Journal / book / series title",
        })}
        ${this._renderManualTextField("container_author", "Container author", {
          placeholder: "Book editor when this is a chapter",
        })}
        ${this._renderManualTextField("volume", "Volume")}
        ${this._renderManualTextField("issue", "Issue")}
        ${this._renderManualTextField("edition", "Edition")}
        ${this._renderManualTextField("page", "Page", {
          placeholder: "17-42 or e12345",
        })}
        ${this._renderManualTextField("publisher", "Publisher")}
        ${this._renderManualTextField("publisher_place", "Publisher place")}
        ${this._renderManualTextField("editor", "Editor")}
        ${this._renderManualTextField("isbn", "ISBN")}
        ${this._renderManualTextField("issn", "ISSN")}
        ${this._renderManualTextField("link", "Link", {
          placeholder: "https://…",
        })}

        ${this._shouldShowManualField("remarks", false)
          ? html`
              <label>Remarks</label>
              <div class="input-cell">
                <textarea
                  .value=${d.remarks || ""}
                  @input=${set("remarks")}
                ></textarea>
              </div>
            `
          : ""}

        <!--
          Hidden submit button so Enter in any field submits the
          form, but the visible Save button lives in the modal's
          outer toolbar next to Close (both on one row instead of
          two stacked toolbars).
        -->
        <button type="submit" hidden></button>
      </form>
      ${!this._showAllFields
        ? html`<div class="show-all-toggle">
            <button
              type="button"
              @click=${() => (this._showAllFields = true)}
              title="Reveal every apiReference field, including empty ones"
            >
              Show all fields
            </button>
          </div>`
        : ""}
    `;
  }

  // Manual-form progressive-disclosure gate. Essentials always
  // render; other fields render only when they have a value or
  // when the curator toggled "Show all fields", or when the two
  // sources (GROBID / OpenAlex) disagree on the field (so the
  // conflict chip has a place to live).
  _shouldShowManualField(key, essential) {
    if (this._showAllFields) return true;
    if (essential) return true;
    const v =
      this._manual && this._manual[key] != null ? this._manual[key] : "";
    if (v !== "") return true;
    return this._manualFieldHasConflict(key);
  }

  _manualFieldHasConflict(key) {
    const g =
      this._uploadResult && this._uploadResult.metadata
        ? this._uploadResult.metadata
        : null;
    const oa = this._previewOpenAlex;
    if (!g || !oa) return false;
    const gv = g[key] || "";
    const ov = oa[key] || "";
    if (!gv || !ov) return false;
    return this._normalizeForCompare(gv) !== this._normalizeForCompare(ov);
  }

  // _renderManualConflictChip shows a small "Use GROBID: <value>"
  // or "Use OpenAlex: <value>" button below the input when the two
  // sources disagree on this field AND the current value doesn't
  // already match that source. Clicking swaps the input's value.
  // No chip when sources agree or one is missing.
  _renderManualConflictChip(key) {
    if (!this._manualFieldHasConflict(key)) return "";
    const g = this._uploadResult.metadata[key] || "";
    const oa = this._previewOpenAlex[key] || "";
    const current =
      this._manual && this._manual[key] != null ? this._manual[key] : "";
    const cn = this._normalizeForCompare(current);
    const showG = cn !== this._normalizeForCompare(g);
    const showOA = cn !== this._normalizeForCompare(oa);
    if (!showG && !showOA) return "";
    return html`
      <div class="field-conflict">
        ${showG
          ? html`<button
              type="button"
              class="conflict-chip"
              title="Replace with GROBID's extracted value"
              @click=${() => this._manualFieldChange(key, g)}
            >
              <span class="source">GROBID</span>
              <span class="value">${g}</span>
            </button>`
          : ""}
        ${showOA
          ? html`<button
              type="button"
              class="conflict-chip"
              title="Replace with OpenAlex's value"
              @click=${() => this._manualFieldChange(key, oa)}
            >
              <span class="source">OpenAlex</span>
              <span class="value">${oa}</span>
            </button>`
          : ""}
      </div>
    `;
  }

  // _renderManualTextField renders one label + input + conflict-chip
  // triple inside the Manual form's grid. Progressive disclosure
  // hides the whole row when the field is empty, non-essential, and
  // has no conflict to surface.
  _renderManualTextField(key, label, opts = {}) {
    if (!this._shouldShowManualField(key, opts.essential)) return "";
    const d = this._manual || emptyManualReference();
    return html`
      <label>${label}</label>
      <div class="input-cell">
        <input
          type="text"
          placeholder=${opts.placeholder || ""}
          .value=${d[key] || ""}
          @input=${(e) => this._manualFieldChange(key, e.target.value)}
        />
        ${this._renderManualConflictChip(key)}
      </div>
    `;
  }

  _manualFieldChange(field, value) {
    // Merge into emptyManualReference when _manual is null so any
    // in-flight partial state still produces a complete field set —
    // defensive belt-and-braces alongside the synchronous init in
    // connectedCallback.
    const base = this._manual || emptyManualReference();
    this._manual = { ...base, [field]: value };
  }

  async _saveManual() {
    if (!this._manual) return;
    this._manualBusy = true;
    this._manualError = "";
    try {
      if (this.jatsRef) {
        // JATS-ref edit mode: POST a ref-edit correction with the
        // fields the curator changed. The correction row persists
        // through re-imports so the fix survives GROBID re-parses.
        await this._saveJATSRefEdit();
        return;
      }
      if (this.editID) {
        // Edit mode: PATCH the existing reference. Only send fields
        // the curator actually changed (compared against the loaded
        // original) so we don't clobber untouched fields with empty
        // strings the form initialized to "".
        const patch = {};
        const original = this._manualOriginal || {};
        for (const [k, v] of Object.entries(this._manual)) {
          if ((original[k] || "") !== v) patch[k] = v;
        }
        if (Object.keys(patch).length === 0) {
          // No changes — just close.
          this._close();
          return;
        }
        const etag = this._manualOriginal?.modified || "";
        await api.reference.patch(this.editID, patch, etag);
        // Fire the same reference-updated event the quick-fix modal
        // dispatched — parent's _onReferenceUpdated refreshes picker
        // badges and re-runs backfill.
        this.dispatchEvent(
          new CustomEvent("reference-updated", {
            detail: { id: this.editID },
            bubbles: true,
            composed: true,
          }),
        );
        this._close();
      } else {
        // Add mode: POST-create + pick, same path as _createAndPick.
        // Attach the upload sha (if any) so the reference gets its
        // sidecar-library association from the start.
        const body = { ...this._manual, id: "" };
        const attachSha =
          this._uploadResult && this._uploadResult.sha512
            ? this._uploadResult.sha512
            : "";
        const saved = await api.reference.create(body, attachSha);
        this._pick(saved.id, referenceHitLabel(saved));
      }
    } catch (err) {
      this._manualError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._manualBusy = false;
    }
  }

  // _saveJATSRefEdit posts a ref-edit correction with the diff of
  // the curator's form against the original JATS-ref fields. Only
  // changed fields go into the Corrected payload so the apply pass
  // touches only what the curator actually edited — untouched
  // fields stay as GROBID had them and benefit from any future
  // re-import that improves extraction.
  //
  // Emits a "jats-ref-saved" event on success so the shell can
  // refresh the reader iframe + corrections list.
  async _saveJATSRefEdit() {
    const desc = this.jatsRef;
    if (!desc) return;
    const original = this._manualOriginal || {};
    const corrected = {};
    for (const [k, v] of Object.entries(this._manual)) {
      if ((original[k] || "") !== v) {
        corrected[k] = v;
      }
    }
    if (Object.keys(corrected).length === 0) {
      // Nothing changed — just close.
      this._close();
      return;
    }
    // Build payload in the RefEditPayload shape the backend expects.
    // Backend keys are snake_case matching the RefEditFields Go struct.
    const payload = {
      original: this._toRefEditFields(original),
      corrected: this._toRefEditFields(corrected),
    };
    const referenceID = desc.referenceID || "";
    if (!referenceID) {
      this._manualError =
        "cannot save: missing reference context — close and reopen";
      this._manualBusy = false;
      return;
    }
    try {
      const r = await fetch("/api/reference/correction", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          reference_id: referenceID,
          sidecar_sha: desc.sha,
          kind: "ref-edit",
          content_type: "reference",
          target_element_id: desc.refID,
          payload: JSON.stringify(payload),
        }),
      });
      if (!r.ok) {
        throw new Error(`ref-edit save failed: HTTP ${r.status}`);
      }
      this.dispatchEvent(
        new CustomEvent("jats-ref-saved", {
          detail: { refID: desc.refID, sha: desc.sha },
          bubbles: true,
          composed: true,
        }),
      );
      this._close();
    } catch (err) {
      this._manualError =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._manualBusy = false;
    }
  }

  // _toRefEditFields maps a manualReference-shaped object to the
  // RefEditFields JSON the Go apply function expects. Only the
  // subset of fields ref-edit knows how to rewrite is included —
  // fields like editor, isbn, issn have no JATS element-citation
  // counterpart in our current mapping and are silently dropped
  // to keep the payload small.
  _toRefEditFields(src) {
    const out = {};
    if ("author" in src) out.author = src.author;
    if ("title" in src) out.title = src.title;
    if ("container_title" in src) out.container_title = src.container_title;
    if ("container_title_short" in src) out.container_title_short = src.container_title_short;
    if ("issued" in src) out.issued = src.issued;
    if ("volume" in src) out.volume = src.volume;
    if ("issue" in src) out.issue = src.issue;
    if ("page" in src) out.page = src.page;
    if ("publisher" in src) out.publisher = src.publisher;
    if ("publisher_place" in src) out.publisher_place = src.publisher_place;
    if ("doi" in src) out.doi = src.doi;
    if ("citation" in src) out.citation = src.citation;
    return out;
  }

  // Persistent issues banner between the modal header and the tab
  // row. Only renders in edit mode when the loaded reference has
  // open validation issues — curators see the same problem list
  // regardless of which tab they're on, which matches the "fix
  // in-context, don't send them elsewhere" principle
  // (feedback_no_side_quests).
  _renderIssuesBanner() {
    const items = this._issues || [];
    if (items.length === 0) return "";
    return html`
      <div class="warning-banner" role="status">
        <strong>
          ${items.length} open
          issue${items.length > 1 ? "s" : ""} on this reference:
        </strong>
        <ul>
          ${items.map(
            (i) => html`<li>
              ${severityChip(i.severity)}
              <span>
                <span class="warning-rule">${i.rule_name || i.rule_id}</span>:
                ${i.message}
                ${i.field_name
                  ? html` <span class="warning-rule"
                      >(${i.field_name})</span
                    >`
                  : ""}
              </span>
            </li>`,
          )}
        </ul>
      </div>
    `;
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
    // Pinned action items rendered at the top of the dropdown, always
    // visible even during search. Each entry:
    //   { label: string, icon?: string (renderIcon name), handler: () => void }
    // Selecting an action runs its handler and closes the dropdown;
    // it does NOT commit the input value as the picker's selection.
    // See DESIGN.md § Combobox pinned actions.
    actions: { attribute: false },
    // Filter chips rendered as a single compact row above actions and
    // results. Each entry:
    //   { key: string, label: string, value: boolean, description?: string }
    // Toggling a chip fires `filter-change` with detail {key, value}
    // and the combobox re-runs its source; the dropdown does NOT close.
    // Caller owns the state (persist / reset / defaults). See DESIGN.md
    // § Search combobox filter chips.
    filters: { attribute: false },
    // When true, the input accepts typed values that don't match any
    // source result. On blur (or Enter with no highlighted result),
    // the current input text commits verbatim as {id: text, name: text}
    // rather than reverting to the last committed value. Used by
    // vocab pickers where the ISO catalog is incomplete (historical
    // countries, curator-authored sex descriptors, etc.) — see
    // DESIGN.md § Combobox free-text mode.
    allowFreeText: { type: Boolean, attribute: "allow-free-text" },
    _input: { state: true },
    _results: { state: true },
    _open: { state: true },
    _loading: { state: true },
    _hover: { state: true },
    // Warning badge attached to the currently-picked/resolved value.
    // Shape: {icon, tooltip, kind} where kind is passed through in
    // the badge-click event so the parent can route (e.g. open the
    // reference-quick-fix modal for kind="reference-issue"). null =
    // no badge for the current value. See feedback_no_side_quests.
    _valueBadge: { state: true },
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
      border-radius: var(--radius-md);
      /* Room on the right for the × button, plus a bit extra so text
         doesn't butt up against it. Deliberately deviates from the
         formFieldStyles default padding — the combobox has to reserve
         space for its clear affordance. */
      padding: var(--sp-1) 1.8rem var(--sp-1) var(--sp-2);
      font-family: inherit;
      font-size: var(--fs-md);
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
      right: var(--sp-1);
      top: 50%;
      transform: translateY(-50%);
      background: transparent;
      color: var(--dim);
      border: 0;
      padding: 0 var(--sp-1);
      font-family: inherit;
      font-size: var(--fs-lg);
      line-height: 1;
      cursor: pointer;
    }
    button.clear:hover {
      color: var(--fg);
    }
    /* Value badge — small icon rendered inside the input alongside
       the picked value. Two variants: warn (open validation issue)
       and info (view/edit-anyway affordance for clean references).
       Both open the same fix modal on click. Always-present when
       the source/resolver attaches a badge — the picker never looks
       non-interactive for a resolved value (see feedback_no_side_quests). */
    button.value-badge {
      position: absolute;
      right: 1.9rem;
      top: 50%;
      transform: translateY(-50%);
      background: transparent;
      border: 0;
      padding: 0 var(--sp-1);
      display: flex;
      align-items: center;
      justify-content: center;
      line-height: 1;
      cursor: pointer;
    }
    /* Severity variants: validation-issue badge colored by the
       highest severity present on the record. Shared with .row-badge
       below so every surface (in-input + dropdown row) uses the
       same signal-per-color mapping. See validationSeverityBadge. */
    button.value-badge.variant-sev-error {
      color: var(--sev-error);
    }
    button.value-badge.variant-sev-error:hover {
      color: color-mix(in oklab, var(--sev-error) 70%, var(--fg));
    }
    button.value-badge.variant-sev-warn {
      color: var(--sev-warn);
    }
    button.value-badge.variant-sev-warn:hover {
      color: color-mix(in oklab, var(--sev-warn) 70%, var(--fg));
    }
    button.value-badge.variant-sev-info {
      color: var(--sev-info);
    }
    button.value-badge.variant-sev-info:hover {
      color: color-mix(in oklab, var(--sev-info) 70%, var(--fg));
    }
    button.value-badge.variant-sev-debug {
      color: var(--dim);
    }
    button.value-badge.variant-sev-debug:hover {
      color: var(--fg);
    }
    /* Non-severity affordances: book (view/edit) + solid (gold star). */
    button.value-badge.variant-info {
      color: var(--dim);
    }
    button.value-badge.variant-info:hover {
      color: var(--fg);
    }
    /* Gold star: reference has structured metadata AND a source
       document ready in the sidecar. Filled + gold to read at a
       glance as "you've upgraded this reference all the way".
       Star svg is a polygon; the svg selector below fills it via
       fill: currentColor. */
    button.value-badge.variant-solid {
      color: #d4a017;
    }
    button.value-badge.variant-solid svg {
      fill: currentColor;
      stroke: none;
    }
    button.value-badge.variant-solid:hover {
      color: #b58712;
    }
    /* Adjust input padding when a value-badge is present so text
       doesn't slide under it. */
    .wrap.has-value-badge input {
      padding-right: 3.4rem;
    }
    /* Per-result badge in the dropdown — rendered inline at the
       right end of the row. Two variants match the value-badge:
       warn (open issue) and info (view/edit affordance). */
    .results li .row-badge {
      background: transparent;
      border: 0;
      padding: 0 0 0 var(--sp-1);
      margin-left: auto;
      display: inline-flex;
      align-items: center;
      cursor: pointer;
      flex: 0 0 auto;
    }
    /* Severity variants mirror the value-badge above. */
    .results li .row-badge.variant-sev-error {
      color: var(--sev-error);
    }
    .results li.hover .row-badge.variant-sev-error {
      color: color-mix(in oklab, var(--sev-error) 60%, var(--accent-fg));
    }
    .results li .row-badge.variant-sev-warn {
      color: var(--sev-warn);
    }
    .results li.hover .row-badge.variant-sev-warn {
      color: color-mix(in oklab, var(--sev-warn) 60%, var(--accent-fg));
    }
    .results li .row-badge.variant-sev-info {
      color: var(--sev-info);
    }
    .results li.hover .row-badge.variant-sev-info {
      color: color-mix(in oklab, var(--sev-info) 60%, var(--accent-fg));
    }
    .results li .row-badge.variant-sev-debug {
      color: var(--dim);
    }
    .results li.hover .row-badge.variant-sev-debug {
      color: var(--accent-fg);
    }
    /* Non-severity affordances. */
    .results li .row-badge.variant-info {
      color: var(--dim);
    }
    .results li.hover .row-badge.variant-info {
      color: var(--accent-fg);
    }
    /* Gold star per-result — same fill treatment as the value-badge. */
    .results li .row-badge.variant-solid {
      color: #d4a017;
    }
    .results li .row-badge.variant-solid svg {
      fill: currentColor;
      stroke: none;
    }
    .results li.hover .row-badge.variant-solid {
      color: var(--accent-fg);
    }
    .results li.hover .row-badge.variant-solid svg {
      fill: currentColor;
    }
    /* Row layout tweak so the badge floats right of the primary
       text via margin-left: auto. */
    .results li.result {
      display: flex;
      align-items: baseline;
      gap: var(--sp-1);
    }
    .results {
      position: absolute;
      left: 0;
      right: 0;
      top: 100%;
      z-index: var(--z-popover);
      max-height: 14rem;
      overflow: auto;
      background: var(--bg);
      border: 1px solid var(--border);
      border-top: none;
      border-bottom-left-radius: var(--radius-md);
      border-bottom-right-radius: var(--radius-md);
      margin: 0;
      padding: 0;
      list-style: none;
    }
    .results li {
      padding: var(--sp-1) var(--sp-2);
      cursor: pointer;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
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
    /* Result-row hint line — the small dimmed second line that
       carries "via <synonym>" (synonym-matched hits) and/or
       "in <family> <name>" (parent context for homonym
       disambiguation). Displayed as a dedicated block under the
       primary label so scanning the accepted names stays cheap; the
       hint is context, not the row's identity. On hover the hint
       inverts to accent-fg with the primary label so both lines
       stay legible against the selection background. See DESIGN.md
       § Search combobox result-row hints. */
    .results li.result.has-hint {
      /* Row layout switches to block so the hint sits below the
         primary label instead of running off in one long ellipsised
         line. Left padding preserves the single-line rhythm for
         plain rows. */
      white-space: normal;
      line-height: 1.3;
      padding-top: var(--sp-1);
      padding-bottom: var(--sp-1);
    }
    .results li.result .row-primary {
      display: block;
    }
    .results li.result .row-hint {
      display: block;
      color: var(--dim);
      font-size: var(--fs-xs);
      font-family: var(--font-body);
      margin-top: 1px;
    }
    .results li.result.hover .row-hint {
      color: var(--accent-fg);
    }
    /* Filter chip row (see DESIGN.md § Search combobox filter chips).
       Single <li> holds all chips inline so the row stays compact.
       Sits at the very top of the dropdown; a border-bottom (via the
       .divider class on the last pinned row) separates the pinned
       zone from search results below. Non-mono body font signals
       "control, not data" — same rationale as .action rows. */
    .results li.filters {
      display: flex;
      align-items: center;
      flex-wrap: wrap;
      gap: var(--sp-1);
      padding: var(--sp-2);
      font-family: var(--font-body);
      background: color-mix(in oklab, var(--accent) 4%, var(--bg));
    }
    .results li.filters.divider {
      border-bottom: 1px solid var(--border);
    }
    .results li.filters button.chip {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      padding: var(--sp-1) var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-pill);
      background: var(--bg);
      color: var(--dim);
      font: inherit;
      font-size: var(--fs-sm);
      cursor: pointer;
      transition: background var(--transition-fast),
        color var(--transition-fast), border-color var(--transition-fast);
    }
    .results li.filters button.chip[aria-checked="true"] {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    .results li.filters button.chip:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
    }
    .results li.filters button.chip:hover {
      color: var(--fg);
    }
    .results li.filters button.chip[aria-checked="true"]:hover {
      color: var(--accent-fg);
    }
    .results li.filters .chip-check {
      display: inline-flex;
      width: 0.9em;
      justify-content: center;
    }
    /* Pinned action rows (see DESIGN.md § Combobox pinned actions).
       Three visual differentiators so curators don't miss the action
       or mistake it for a search result: (1) icon prefix; (2) subtle
       accent background tint; (3) border-bottom divider separating
       the action zone from the results zone below. Font matches the
       body font (not the mono result font) to further signal
       "action, not data". */
    .results li.action {
      display: flex;
      align-items: center;
      gap: var(--sp-2);
      font-family: var(--font-body);
      color: var(--fg);
      background: color-mix(in oklab, var(--accent) 8%, var(--bg));
    }
    .results li.action.divider {
      border-bottom: 1px solid var(--border);
    }
    .results li.action .action-icon {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      color: var(--accent);
      flex: 0 0 auto;
    }
    .results li.action .action-icon svg {
      display: block;
    }
    .results li.action.hover {
      background: var(--accent);
      color: var(--accent-fg);
    }
    .results li.action.hover .action-icon {
      color: var(--accent-fg);
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
    this.actions = [];
    this.filters = [];
    this.allowFreeText = false;
    this._input = "";
    this._results = [];
    this._open = false;
    this._loading = false;
    this._hover = -1;
    this._debounceHandle = 0;
    this._lastQuery = null;
    this._focused = false;
    this._resolvingFor = ""; // id we're currently resolving to avoid duplicate work
    this._valueBadge = null;
  }

  async updated(changed) {
    // Sync internal input with valueName when the parent supplies it.
    if (changed.has("valueName") && !this._focused) {
      this._input = this.valueName || "";
    }
    // If value is set, ask the resolver to fill in the display and
    // any attached badge. Runs on every value change — even when the
    // parent pre-populates valueName as a display optimization —
    // because the resolver is now the only path that hydrates the
    // value badge (e.g., reference "book / warning / gold star").
    // Pickers whose resolver returns just a string (rank, taxon,
    // name, vocab) get a redundant fetch on value-change, which is
    // an acceptable trade for consistent badge state.
    if (
      changed.has("value") &&
      this.value &&
      this.resolver &&
      this._resolvingFor !== this.value
    ) {
      this._resolvingFor = this.value;
      try {
        const resolved = await this.resolver(this.value);
        // Race guard: value may have changed while we were fetching.
        if (this._resolvingFor === this.value) {
          // Resolver may return a plain string (display name) or an
          // object {name, badge}. Object form lets the resolver carry
          // a warning badge (e.g. reference has open issues) that
          // renders inside the picker + emits a badge-click event.
          if (resolved && typeof resolved === "object") {
            this.valueName = resolved.name || "";
            this._valueBadge = resolved.badge || null;
          } else {
            this.valueName = resolved || "";
            this._valueBadge = null;
          }
          if (!this._focused) this._input = this.valueName;
        }
      } catch (_) {
        if (this._resolvingFor === this.value) {
          this.valueName = "(lookup failed)";
          if (!this._focused) this._input = this.valueName;
          // Clear _resolvingFor so a subsequent focus (or value
          // change back to this same id) re-attempts the fetch.
          // Otherwise a transient failure (server restart, connection
          // blip) leaves the picker permanently showing "(lookup
          // failed)" for the current session.
          this._resolvingFor = "";
        }
      }
    }
  }

  // refresh is the public API for parents to force the picker to
  // re-resolve its current value — used after the parent updates
  // the underlying record (e.g., reference-quick-fix modal saves).
  // Clears the resolver cache + valueName + valueBadge so the
  // updated() branch re-runs the resolver and fresh state lands
  // in the picker. No-op when no value is set.
  refresh() {
    if (!this.value || !this.resolver) return;
    this._resolvingFor = "";
    this._valueBadge = null;
    // Clearing valueName to "" triggers the updated() re-resolve
    // path because both valueName and value changes are tracked.
    // The visible input keeps its text (this._input) until the
    // resolver returns, avoiding a flash of empty content.
    this.valueName = "";
  }

  async _retryResolveIfStuck() {
    // Retry the resolver when the picker is in a stuck "value set,
    // no display name" state (typically after a transient failure).
    // Called from _onFocus so any curator interaction with the picker
    // triggers a fresh attempt without needing a page reload.
    if (
      !this.value ||
      this.valueName ||
      !this.resolver ||
      this._resolvingFor === this.value
    ) {
      return;
    }
    this._resolvingFor = this.value;
    try {
      const resolved = await this.resolver(this.value);
      if (this._resolvingFor === this.value) {
        if (resolved && typeof resolved === "object") {
          this.valueName = resolved.name || "";
          this._valueBadge = resolved.badge || null;
        } else {
          this.valueName = resolved || "";
          this._valueBadge = null;
        }
        if (!this._focused) this._input = this.valueName;
      }
    } catch (_) {
      this._resolvingFor = "";
    }
  }

  _onFocus() {
    this._focused = true;
    this._retryResolveIfStuck();
    // Open the dropdown on focus in four cases:
    //   1. Empty input + minSearchChars=0 (vocab picker; show all).
    //   2. Pinned actions exist (curator should see "Add new …"
    //      immediately without having to type first).
    //   3. Filter chips exist (curator should see current filter
    //      state and be able to flip a chip before typing).
    //   4. Input already has content above the search threshold
    //      (curator re-focusing a picker with a partial query — the
    //      existing results should re-appear).
    // Runs a search in cases (1) and (4) so results populate.
    const hasActions = (this.actions?.length || 0) > 0;
    const hasFilters = (this.filters?.length || 0) > 0;
    if (this._input.length === 0 && this.minSearchChars === 0) {
      this._runSearch();
      this._open = true;
    } else if (hasActions || hasFilters) {
      this._open = true;
      // If input meets the search threshold, refresh results too so
      // the dropdown shows current data alongside the pinned rows.
      if (this._input.length >= this.minSearchChars) {
        this._runSearch();
      } else if (hasActions) {
        // No results yet, but we still want the hover cursor on the
        // first action so Enter works immediately. Filters are not
        // in the hover cycle (they're focused via Tab / mouse), so
        // we only anchor hover onto the first action if actions exist.
        this._hover = 0;
      } else {
        this._hover = -1;
      }
    }
  }

  _onBlur() {
    // Delay so click-on-result / click-on-× fires before we drop focus
    // and revert. Also handled by @mousedown+preventDefault on those
    // elements as a belt-and-braces measure.
    setTimeout(() => {
      // If focus moved to a descendant of this combobox (e.g., a
      // filter chip clicked or Tab-navigated onto), keep the dropdown
      // open — the curator hasn't left the widget. Shadow DOM makes
      // this fiddly; we walk the deep active element up through any
      // shadow boundaries and see if we hit `this`.
      let el = this.renderRoot?.activeElement || document.activeElement;
      while (el) {
        if (el === this) return;
        el = el.parentNode || el.host || null;
      }
      this._focused = false;
      this._open = false;
      // If the user typed something and didn't pick:
      //   * allow-free-text: commit the raw typed value as-is
      //     (id === name === typed text). The caller stores whatever
      //     was typed; validation later can flag off-vocab values.
      //   * otherwise: revert to the last committed value's name so
      //     the input never shows a "phantom" state.
      if (this._input !== (this.valueName || "")) {
        if (this.allowFreeText) {
          const typed = this._input;
          this._pick({ id: typed, name: typed });
        } else {
          this._input = this.valueName || "";
        }
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

  // _totalHoverItems returns the count of keyboard-navigable dropdown
  // rows: pinned actions plus search results. Used by the arrow-key
  // handler and by Enter to route between action.handler and _pick.
  _totalHoverItems() {
    return (this.actions?.length || 0) + this._results.length;
  }

  async _runSearch() {
    const q = this._input;
    this._lastQuery = q;
    try {
      const results = await Promise.resolve(this.source(q));
      if (q === this._lastQuery) {
        this._results = results || [];
        // Default hover: first item overall (action if present, else
        // first result). Keeps Enter useful the moment the dropdown
        // opens.
        const total = this._totalHoverItems();
        this._hover = total > 0 ? 0 : -1;
        this._loading = false;
      }
    } catch (_) {
      if (q === this._lastQuery) {
        this._results = [];
        // Even with no results, actions may still be present; keep
        // hover on the first action so Enter works.
        this._hover = (this.actions?.length || 0) > 0 ? 0 : -1;
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
        } else {
          const total = this._totalHoverItems();
          if (total) this._hover = (this._hover + 1) % total;
        }
        break;
      case "ArrowUp":
        e.preventDefault();
        {
          const total = this._totalHoverItems();
          if (total) this._hover = (this._hover - 1 + total) % total;
        }
        break;
      case "Enter":
        e.preventDefault();
        {
          const nActions = this.actions?.length || 0;
          if (this._hover >= 0) {
            if (this._hover < nActions) {
              this._runAction(this.actions[this._hover]);
              break;
            } else if (this._hover - nActions < this._results.length) {
              this._pick(this._results[this._hover - nActions]);
              break;
            }
          }
          // No highlighted result to pick. In allow-free-text mode,
          // Enter commits whatever the curator typed (empty allowed —
          // maps to clearing the field). Otherwise Enter is a no-op
          // so the input stays as-is until the curator picks.
          if (this.allowFreeText) {
            const typed = this._input;
            this._pick({ id: typed, name: typed });
          }
        }
        break;
      case "Escape":
        e.preventDefault();
        this._input = this.valueName || "";
        this._open = false;
        break;
    }
  }

  _runAction(action) {
    if (!action || typeof action.handler !== "function") return;
    this._open = false;
    // Close the dropdown before running the handler so the action's
    // side effects (e.g., opening a modal) don't race with the
    // combobox's own render cycle.
    action.handler();
  }

  // _toggleFilter emits `filter-change` so the caller can update its
  // state, then re-runs the source so results reflect the new filter
  // set. Caller-owned state is the single source of truth: the chip's
  // rendered value comes from the caller's next-render filters prop,
  // never from a local mutation. Not optimistic on purpose — mutual-
  // exclusion cases (turning Partial on while Fuzzy is on) need the
  // parent to update *both* chip values consistently before the
  // combobox re-renders, otherwise both chips would briefly appear on.
  //
  // The dropdown stays open and the input keeps its value — flipping
  // filters is a refinement, not a selection.
  _toggleFilter(key, e) {
    if (e) e.preventDefault();
    const filter = (this.filters || []).find((f) => f.key === key);
    if (!filter) return;
    this.dispatchEvent(
      new CustomEvent("filter-change", {
        detail: { key, value: !filter.value },
        bubbles: true,
        composed: true,
      }),
    );
    // Refresh results with the new filter set. Only fire if the input
    // meets the search threshold — no point running an empty query.
    if (this._input.length >= this.minSearchChars) {
      this._runSearch();
    }
  }

  _pick(item) {
    this.value = item.id;
    this.valueName = item.name;
    this._input = item.name;
    // Carry the item's badge through so the in-input display shows
    // the same warning icon that was on the dropdown row. Cleared
    // when item has no badge (curator picked a clean row).
    this._valueBadge = item.badge || null;
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

  // _onBadgeClick emits a badge-click event carrying the item id and
  // the badge object. Parent decides what to do (typically open a
  // fix modal). Called from both the in-input badge and per-row
  // badges in the dropdown. Stops propagation so it doesn't also
  // fire pick / clear / focus behaviors.
  _onBadgeClick(e, id, badge) {
    if (e) {
      e.preventDefault();
      e.stopPropagation();
    }
    if (!badge) return;
    this.dispatchEvent(
      new CustomEvent("badge-click", {
        detail: { id, badge },
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
    this._valueBadge = null;
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
    const actions = this.actions || [];
    const nActions = actions.length;
    const filters = this.filters || [];
    const hasFilters = filters.length > 0;
    // Filters render as a single compact row at the very top of the
    // dropdown; the row carries `.divider` when no actions follow so
    // the visual partition sits directly below the filters. Chips are
    // real buttons — Tab moves focus onto them, Space/Enter (native
    // button behavior) toggles. mousedown+preventDefault keeps the
    // input focused for pointer users while still allowing Tab entry
    // for keyboard users (see _onBlur). See DESIGN.md § Search
    // combobox filter chips.
    const filterRow = hasFilters
      ? html`
          <li class=${"filters" + (nActions === 0 ? " divider" : "")}>
            ${filters.map(
              (f) => html`
                <button
                  type="button"
                  class="chip"
                  role="switch"
                  aria-checked=${f.value ? "true" : "false"}
                  title=${f.description || f.label}
                  @mousedown=${(e) => this._toggleFilter(f.key, e)}
                  @keydown=${(e) => {
                    if (e.key === " " || e.key === "Enter") {
                      e.preventDefault();
                      this._toggleFilter(f.key, null);
                    }
                  }}
                >
                  <span class="chip-check" aria-hidden="true"
                    >${f.value ? "✓" : "○"}</span
                  >
                  <span>${f.label}</span>
                </button>
              `,
            )}
          </li>
        `
      : "";
    // Actions render below filters, always visible (see DESIGN.md
    // § Combobox pinned actions). The last action carries `.divider`
    // when any result row follows so the border-bottom partitions the
    // pinned zone from the results zone cleanly. mousedown (not click)
    // so the dropdown's blur handler doesn't dismiss us before the
    // handler fires.
    const actionRows = actions.map((a, i) => {
      const cls = ["action"];
      if (i === this._hover) cls.push("hover");
      // Divider on the last action row iff any result content follows.
      if (i === nActions - 1) cls.push("divider");
      return html`
        <li
          class=${cls.join(" ")}
          @mousedown=${(e) => {
            e.preventDefault();
            this._runAction(a);
          }}
          @mouseenter=${() => (this._hover = i)}
        >
          ${a.icon
            ? html`<span class="action-icon">${renderIcon(a.icon, 14)}</span>`
            : ""}
          <span>${a.label}</span>
        </li>
      `;
    });

    let resultRows;
    if (this._loading) {
      resultRows = html`<li class="empty">Searching…</li>`;
    } else if (
      !this._results.length &&
      this._input.length >= this.minSearchChars
    ) {
      resultRows = html`<li class="empty">No matches</li>`;
    } else {
      // Homonym disambiguation: when the same accepted-name string
      // appears more than once in the current result set, every hit
      // with that name earns a parent-context hint so the curator can
      // tell them apart (see DESIGN.md § Search combobox result-row
      // hints). Detection is O(n²) over the small result set; a Map
      // avoids the quadratic when n grows.
      const nameCounts = new Map();
      for (const r of this._results) {
        nameCounts.set(r.name, (nameCounts.get(r.name) || 0) + 1);
      }
      resultRows = this._results.map((r, i) => {
        const cls = ["result"];
        if (nActions + i === this._hover) cls.push("hover");
        // Hint composition — up to two clauses on the second line:
        //   * "via <matched>" — set when the hit came via a synonym
        //     and the matched text differs from the accepted label
        //     (guards against a redundant "via X" when they match).
        //   * "in <family> <name>" — set when this row is a synonym
        //     match OR when the accepted-name string is duplicated in
        //     the result set (homonym disambiguation). Uses the
        //     server-rendered parent label; skipped if parent is
        //     absent (root taxa) or if the parent's own label matches
        //     the row's parent-side render (unlikely).
        const isSynRow = r.isSynonym && r.matched && r.matched !== r.name;
        const isHomonym = (nameCounts.get(r.name) || 0) > 1;
        const showParent = r.parent && (isSynRow || isHomonym);
        const hasHint = isSynRow || showParent;
        if (hasHint) cls.push("has-hint");
        // Screen-reader label folds the hint clauses into one
        // sentence so listeners hear the same disambiguating context
        // sighted curators get from the second line.
        let ariaLabel;
        if (isSynRow || showParent) {
          const parts = [r.name];
          if (isSynRow) parts.push(`matched via synonym ${r.matched}`);
          if (showParent) {
            const rank = r.parent.rank
              ? r.parent.rank.toLowerCase()
              : "parent";
            parts.push(`in ${rank} ${r.parent.label?.text || ""}`);
          }
          ariaLabel = parts.filter(Boolean).join(", ");
        }
        // Hint fragments: prefer HTML from server (italicises genus /
        // species labels) but fall back to text if HTML absent.
        const hintFragments = [];
        if (isSynRow) {
          hintFragments.push(html`<span>via ${r.matched}</span>`);
        }
        if (showParent) {
          const rank = r.parent.rank ? r.parent.rank.toLowerCase() : "";
          const parentLabel = r.parent.label?.html
            ? unsafeHTML(r.parent.label.html)
            : r.parent.label?.text || "";
          hintFragments.push(
            html`<span>in ${rank ? rank + " " : ""}${parentLabel}</span>`,
          );
        }
        return html`
          <li
            class=${cls.join(" ")}
            aria-label=${ariaLabel ?? nothing}
            @mousedown=${(e) => {
              e.preventDefault();
              this._pick(r);
            }}
            @mouseenter=${() => (this._hover = nActions + i)}
          >
            <span class="row-primary">${r.name || "(unset)"}</span>
            ${hasHint
              ? html`<span class="row-hint" aria-hidden="true">
                  ${hintFragments.map(
                    (frag, idx) => html`${idx > 0 ? " · " : ""}${frag}`,
                  )}
                </span>`
              : ""}
            ${r.badge
              ? html`<button
                  type="button"
                  class=${"row-badge variant-" + (r.badge.variant || "warn")}
                  tabindex="-1"
                  title=${r.badge.tooltip || "issue on this item"}
                  aria-label=${r.badge.tooltip || "issue on this item"}
                  @mousedown=${(e) => this._onBadgeClick(e, r.id, r.badge)}
                >
                  ${renderIcon(r.badge.icon || "triangle-alert", 14)}
                </button>`
              : ""}
          </li>
        `;
      });
    }
    const dropdown = html`${filterRow}${actionRows}${resultRows}`;

    const hasBadge = !!this._valueBadge;
    return html`
      <div class=${"wrap" + (hasBadge ? " has-value-badge" : "")}>
        <input
          type="text"
          placeholder=${this.placeholder}
          .value=${this._input}
          @input=${(e) => this._onInput(e)}
          @focus=${() => this._onFocus()}
          @blur=${() => this._onBlur()}
          @keydown=${(e) => this._onKeyDown(e)}
        />
        ${hasBadge
          ? html`<button
              class=${"value-badge variant-" +
              (this._valueBadge.variant || "warn")}
              type="button"
              tabindex="-1"
              title=${this._valueBadge.tooltip || "issue on this item"}
              aria-label=${this._valueBadge.tooltip || "issue on this item"}
              @mousedown=${(e) =>
                this._onBadgeClick(e, this.value, this._valueBadge)}
            >
              ${renderIcon(this._valueBadge.icon || "triangle-alert", 14)}
            </button>`
          : ""}
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

  // Reference implementation for the WUI design system. Styles compose
  // from formFieldStyles + buttonStyles (shared vocabulary) plus a small
  // block of component-specific layout. All spacings, radii, and font
  // sizes read from the tokens defined in styles.css — no raw px/rem
  // outside the 60rem content max-width, which is intentionally kept
  // hardcoded until it appears in enough other components to earn a
  // token of its own. See DESIGN.md for the extension protocol.
  static styles = [
    formFieldStyles,
    buttonStyles,
    css`
      :host {
        display: block;
        font-family: var(--font-body);
        color: var(--fg);
        padding: var(--sp-4) var(--sp-5);
      }
      h2 {
        margin: 0 0 var(--sp-1) 0;
        font-size: var(--fs-lg);
      }
      hr {
        border: 0;
        border-top: 1px solid var(--border);
        margin: var(--sp-2) 0 var(--sp-4) 0;
      }
      dl {
        margin: 0;
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: var(--sp-1) var(--sp-4);
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
        max-width: 60rem;
      }
      dt {
        color: var(--dim);
        align-self: start;
      }
      dd {
        margin: 0;
        white-space: pre-wrap;
        word-break: break-word;
      }
      /* Cells containing an <sfga-agent-section> reset white-space so
         the newlines/indentation between <dd> and the child element
         don't render as visible blank lines. pre-wrap is kept on the
         default dd for the Description field's multi-line text. */
      dd.agents-cell {
        white-space: normal;
      }
      form {
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: var(--sp-2) var(--sp-4);
        max-width: 60rem;
        margin-top: var(--sp-2);
      }
      /* Center-align labels next to single-line inputs by default.
         Agent-label rows contain a tall card block, so top-align that
         specific label so it stays anchored to the top of the cell. */
      form label {
        align-self: center;
      }
      form label.agent-label {
        align-self: start;
      }
      .toolbar {
        grid-column: 1 / -1;
        display: flex;
        gap: var(--sp-2);
        margin-top: var(--sp-2);
      }
      .error {
        color: var(--error);
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
      }
      .empty {
        color: var(--dim);
      }
      /* Star rating for confidence. Filled stars use the accent color
         so the palette stays within fg + accent + severity — no gold
         off-palette exception. Filled and empty spans sit flush so the
         row reads as a single rating; only the numeric count is
         separated by a gap. */
      .stars {
        display: inline-flex;
        align-items: baseline;
        font-family: var(--font-mono);
      }
      .star-filled {
        color: var(--accent);
      }
      .star-empty {
        color: var(--border);
      }
      .star-count {
        color: var(--dim);
        font-size: var(--fs-sm);
        margin-left: var(--sp-2);
      }
    `,
  ];

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
      // Data resolved (or errored) — signal the shell so the header
      // pencil renders now that hasEditAffordance() has a real answer.
      this._notifyActionsChanged();
    }
  }

  _startEdit() {
    this._draft = {};
    this._saveError = "";
    this._editing = true;
    this._notifyActionsChanged();
  }

  _cancelEdit() {
    this._draft = {};
    this._saveError = "";
    this._editing = false;
    this._notifyActionsChanged();
  }

  // renderHeaderActions is the public contract every screen component
  // implements to project its actions into the app header (see DESIGN.md
  // § Screen actions). Return an html`...` template of buttons or ""
  // when no actions should render right now. Dispatch
  // "screen-actions-changed" whenever the output would change so the
  // shell knows to re-render.
  renderHeaderActions() {
    if (!this.editable || this._editing || !this._metadata) return "";
    return html`
      <button
        class="icon-btn subtle"
        @click=${() => this._startEdit()}
        title="edit metadata"
        aria-label="edit metadata"
      >
        ${renderIcon("pencil", 18)}
      </button>
    `;
  }

  _notifyActionsChanged() {
    this.dispatchEvent(
      new CustomEvent("screen-actions-changed", {
        bubbles: true,
        composed: true,
      }),
    );
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
      // Edit mode ended — header pencil becomes available again.
      this._notifyActionsChanged();
    } catch (err) {
      this._saveError =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    } finally {
      this._saving = false;
    }
  }

  // hasUnsavedChanges reports true when the edit form has draft
  // values that haven't been saved. Consulted by the shell before
  // switching screens so a stray alt+t or sidebar click can't
  // discard an in-progress edit without asking.
  hasUnsavedChanges() {
    return this._editing && Object.keys(this._draft || {}).length > 0;
  }

  // save is the public entry the shell calls when the user picks
  // "Save" from the unsaved-changes dialog. Returns true when the
  // save succeeded (draft cleared, edit closed), false when the
  // form still has unsaved state so the shell can abort its
  // pending navigation.
  async save() {
    await this._save();
    return !this.hasUnsavedChanges();
  }

  // discardChanges is the public entry the shell calls when the
  // user picks "Discard" — drops the draft and returns to read view.
  discardChanges() {
    this._cancelEdit();
  }

  render() {
    if (this._loading && !this._metadata) {
      return html`<div class="empty" role="status">Loading…</div>`;
    }
    if (this._error) {
      return html`<div class="error" role="alert">${this._error}</div>`;
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
              ? html`<button @click=${() => this._startEdit()}>Seed metadata</button>`
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
    // Agent rows live inline in the dl so cards align with the
    // rest of the metadata field grid. Order matches curator
    // priority: contact (who to reach), then creators (primary
    // authors), then editors / publishers / contributors. Read
    // mode hides the "+ Add" tile — adding requires entering
    // edit mode via the header pencil — but leaves existing
    // cards clickable so curators can still open an agent's
    // detail modal without leaving the read view.
    const agentRow = (role, label) => html`
      <dt>${label}</dt>
      <dd class="agents-cell"><sfga-agent-section
        .role=${role}
        .editable=${this.editable}
        .noheader=${true}
        .noAdd=${true}
      ></sfga-agent-section></dd>
    `;
    return html`
      <dl>
        ${row("Title", m.title || "(untitled)")}
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
        ${agentRow("contact", "Contact")}
        ${agentRow("creator", "Creators")}
        ${agentRow("editor", "Editors")}
        ${agentRow("publisher", "Publishers")}
        ${agentRow("contributor", "Contributors")}
      </dl>
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
        ${this._renderEditAgentRows()}
        <div class="toolbar">
          <button class="primary" @click=${() => this._save()} ?disabled=${this._saving}>
            save
          </button>
          <button @click=${() => this._cancelEdit()} ?disabled=${this._saving}>
            cancel
          </button>
          ${this._saveError
            ? html`<span class="error" role="alert">${this._saveError}</span>`
            : ""}
        </div>
      </form>
    `;
  }

  // Agent rows for the edit form. Same visual grid as the read
  // view (labels aligned with the metadata field labels), but the
  // "+ Add" tile is enabled so curators can add new agents while
  // in edit mode. Card clicks still open the modal in either
  // mode — this only gates creation, not editing.
  _renderEditAgentRows() {
    const editRow = (role, label) => html`
      <label class="agent-label">${label}</label>
      <div class="agents-cell"><sfga-agent-section
        .role=${role}
        .editable=${true}
        .noheader=${true}
      ></sfga-agent-section></div>
    `;
    return html`
      ${editRow("contact", "Contact")}
      ${editRow("creator", "Creators")}
      ${editRow("editor", "Editors")}
      ${editRow("publisher", "Publishers")}
      ${editRow("contributor", "Contributors")}
    `;
  }
}

// ---------- <sfga-references> ----------
// References screen — flat paginated list on the left, full reference
// detail on the right. Read-only in this slice; add-by-DOI / add-by-
// BibTeX and edit come later. Fetches its own data on connect.

class SfgaReferences extends LitElement {
  static properties = {
    // Public: when set (e.g. by the shell routing an Issues click),
    // the component selects that reference on the next render. The
    // shell clears its side of the state via the `reference-selected`
    // event so subsequent property assignments (even to the same id)
    // still trigger a reveal.
    selectId: { attribute: false },
    // Public: shell-provided workspace tab hint. Set from the URL
    // hash on load / navigation; the component switches its
    // internal _workspaceTab to match. Empty string means "let
    // the component keep its default." Curator tab clicks flow
    // back to the shell via workspace-tab-changed events.
    workspaceTab: { attribute: false },
    _hits: { state: true },
    _selectedId: { state: true },
    _current: { state: true },
    _loading: { state: true },
    _error: { state: true },
    // Free-text search across author / title / citation / DOI.
    // Empty query returns the paginated default list; non-empty
    // hits /api/reference/search via a trailing-edge debounce.
    _query: { state: true },
    // Non-empty when the reference-edit modal is open for the
    // currently-selected reference. Mirrors SfgaDetail's pattern
    // of owning the modal-mount state locally so each screen
    // that offers editing gets its own modal instance.
    _editRefID: { state: true },
    // True when the modal is open in add mode (curator hit the +
    // button). Separate from _editRefID because the modal keys off
    // the presence of editID to switch between edit and add flows.
    _addingNew: { state: true },
    // _jatsRefEdit holds the descriptor sent by the reader's hover
    // pencil / warning icon on a JATS <ref>. When non-null, the
    // add-reference modal opens in a special jats-ref mode: seeds
    // from these fields (not api.reference.get), and its save path
    // POSTs a ref-edit correction (not api.reference.patch).
    // Shape: { sha, refID, fields, suspect }.
    _jatsRefEdit: { state: true },
    // True when the host is wide enough (~1300px+) for a 3-column
    // layout (list | detail | reader). Narrow viewports collapse
    // the reader into the detail pane below the structured info,
    // scroll-away style, so a single browser half-screen doesn't
    // leave one column empty. Set by a ResizeObserver in
    // connectedCallback.
    _wide: { state: true },
    // True when the reader pane has been expanded to cover the
    // detail column too. Only meaningful in the 3-column layout;
    // curator toggles via ⤢ in the detail header when they want
    // to dedicate the whole non-list area to reading.
    _readerExpanded: { state: true },
    // Workspace tab controlling what fills the two-pane area on
    // the right of the list:
    //
    //   "bibliography" — bibliographic detail + copy buttons | reader
    //   "digitize"     — reader | PDF (compare digitization quality)
    //   "assertions"   — extracted assertions | reader (placeholder
    //                    until the extraction pipeline lands)
    //
    // Tabs make each combination one click away — a curator moving
    // between "read a reference", "verify JATS digitization", and
    // (future) "review extracted assertions" doesn't need to hunt
    // for a modal. Bibliography is the default because "look at
    // the reference and copy a citation" is the most common visit.
    _workspaceTab: { state: true },
    // _pdfPage drives the PDF iframe's #page=N fragment in the
    // Digitize tab. Reactive because clicking a fig-xref in the
    // reader posts a message that updates this — Lit needs to
    // observe the change and re-render the iframe with the new
    // src fragment so the PDF viewer scrolls to the target page.
    _pdfPage: { state: true },
    // Sort order for the reference list. "recent" (default)
    // orders by col__modified desc — newly-imported and recently-
    // edited references land at the top, the useful landing view
    // for a large archive where alphabetical scan is impractical.
    // "author" is the alphabetical fallback for curators who
    // specifically want to scan by name. Persisted in localStorage.
    _sort: { state: true },
    // Infinite-scroll state: true when there are more references
    // to fetch past the currently-loaded window. False once we've
    // paginated to the end (or a search returns everything at once).
    _hasMore: { state: true },
    // Aggregated progress across the 4-step reference workflow —
    // fetched from /api/reference/dashboard on mount and refreshed
    // when a ref completes a step (save, add, upload, import).
    // Rendered as the right-pane landing view when no reference is
    // selected. Null while the initial fetch is in flight.
    _dashboard: { state: true },
    // Digitize-tab correction tools: which tool is active in the
    // toolbar ('read' | 'bbox'), which corrections apply to the
    // currently-selected reference, and whether the bbox-form
    // modal is open (populated with a fresh draw's page + bbox
    // waiting for content_type + label + save).
    _activeTool: { state: true },
    _corrections: { state: true },
    _bboxForm: { state: true },
    _bboxFormBusy: { state: true },
    // GROBID-extracted regions from the JATS sidecar — rendered
    // as muted overlays on the PDF viewer alongside the curator
    // corrections so the curator sees every detected region at a
    // glance. Curator additions render in the primary color;
    // GROBID regions render dimmed so they stay in the background.
    _grobidRegions: { state: true },
    // File dropped on the references pane, waiting to be handed
    // off to the newly-opened add-reference modal. Non-null value
    // triggers the modal to auto-upload it on first render.
    _pendingDropFile: { state: true },
    // Visual state during an active dragover — shows a subtle
    // outline on the references pane so curators know they can
    // drop the file here to import it.
    _dropActive: { state: true },
  };

  static styles = css`
    :host {
      display: grid;
      /* 2-column layout: list on the left, workspace on the right.
         Workspace has its own tabs + two-pane layout inside so
         the outer host grid stays simple. */
      grid-template-columns: minmax(18rem, 22rem) 1fr;
      overflow: hidden;
      height: 100%;
      position: relative;
    }
    /* Drop-zone highlight lives at document level (see styles.css
       body[data-drop-active]) so it can extend across iframes and
       the full viewport. Component-local styles wouldn't reach
       past the shadow DOM boundary. */
    .workspace {
      display: flex;
      flex-direction: column;
      overflow: hidden;
      min-width: 0;
    }
    .workspace-tabs {
      display: flex;
      gap: var(--sp-1);
      padding: var(--sp-2) var(--sp-2) 0;
      border-bottom: 1px solid var(--border);
    }
    .workspace-tabs button {
      background: transparent;
      color: var(--fg);
      border: 1px solid transparent;
      border-bottom: none;
      border-radius: var(--radius-md) var(--radius-md) 0 0;
      padding: var(--sp-1) var(--sp-3);
      cursor: pointer;
      font: inherit;
      font-size: var(--fs-sm);
    }
    .workspace-tabs button[disabled] {
      opacity: 0.5;
      cursor: not-allowed;
    }
    .workspace-tabs button.active {
      border-color: var(--border);
      background: var(--bg);
      position: relative;
      top: 1px;
    }
    /* Two-pane area under the tabs — a 2-column grid that fills
       the remaining vertical space. Each pane scrolls
       independently so reading one doesn't drag the other. */
    .workspace-panes {
      flex: 1;
      display: grid;
      min-height: 0;
      overflow: hidden;
    }
    /* Every tab-content stacks in the same grid cell so iframes stay
       mounted while inactive tabs sit hidden. Preserves scroll and
       iframe state per-tab within a single reference; switching to a
       different reference re-mounts because iframe srcs point at the
       new sha. */
    .workspace-panes .tab-content {
      grid-column: 1;
      grid-row: 1;
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
      min-height: 0;
      overflow: hidden;
    }
    .workspace-panes .tab-content[hidden] {
      display: none;
    }
    .workspace-panes .pane {
      overflow: auto;
      min-width: 0;
    }
    .workspace-panes iframe.pane {
      border: 0;
      border-left: 1px solid var(--border);
      width: 100%;
      height: 100%;
    }
    .workspace-panes sfga-pdf-viewer.pane {
      width: 100%;
      height: 100%;
    }
    /* Digitize tab drops the default two-column grid because it
       has its own inner .digitize-grid that owns the reader/pdf
       split. Toolbars now live inside each column, not in a
       shared header row above the grid. */
    .workspace-panes .tab-content.digitize {
      grid-template-columns: 1fr;
      grid-template-rows: 1fr;
    }
    /* Frame-scoped toolbars — each side of the Digitize view has its
       own row of tool buttons above the pane the tools act on, so
       curators can see at a glance that (say) BBox is a PDF-side tool
       and Remove-text is a reader-side tool. Prevents the footgun of
       trying to draw a bbox on the reader iframe. */
    .pdf-toolbar,
    .reader-toolbar {
      display: flex;
      gap: var(--sp-2);
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid var(--border);
      background: var(--surface);
      min-height: 2rem;
      align-items: center;
    }
    .pdf-toolbar .tool-btn,
    .reader-toolbar .tool-btn {
      background: transparent;
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-1) var(--sp-3);
      font: inherit;
      font-size: var(--fs-sm);
      cursor: pointer;
    }
    .pdf-toolbar .tool-btn.active,
    .reader-toolbar .tool-btn.active {
      background: color-mix(in oklab, var(--sev-info) 25%, transparent);
      border-color: color-mix(in oklab, var(--sev-info) 60%, transparent);
    }
    .reader-toolbar .toolbar-hint {
      color: var(--dim);
      font-size: var(--fs-xs);
      font-style: italic;
    }
    .digitize-grid {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
      min-height: 0;
      overflow: hidden;
    }
    .reader-column,
    .pdf-column {
      display: flex;
      flex-direction: column;
      min-height: 0;
    }
    .pdf-column {
      border-left: 1px solid var(--border);
    }
    .chip-strip {
      display: flex;
      gap: var(--sp-1);
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid var(--border);
      background: var(--surface);
      overflow-x: auto;
      min-height: 2rem;
      align-items: center;
    }
    .chip-strip .empty-hint {
      color: var(--dim);
      font-size: var(--fs-xs);
      font-style: italic;
    }
    .bbox-chip {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: 0 var(--sp-2);
      font-size: var(--fs-xs);
      white-space: nowrap;
    }
    .bbox-chip .chip-label {
      cursor: pointer;
    }
    .bbox-chip .chip-delete {
      background: transparent;
      border: 1px solid var(--border);
      border-radius: var(--radius-xs);
      color: var(--fg);
      cursor: pointer;
      font-size: var(--fs-md);
      line-height: 1;
      padding: 0 var(--sp-1);
      margin-left: var(--sp-1);
      min-width: 1.2rem;
    }
    .bbox-chip .chip-delete:hover {
      background: var(--error);
      border-color: var(--error);
      color: white;
    }
    /* BBox form modal — appears after a bbox is drawn to collect
       content type + label before the correction is saved. */
    .bbox-form-backdrop {
      position: fixed;
      inset: 0;
      background: rgba(0, 0, 0, 0.4);
      z-index: 20;
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .bbox-form {
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-4);
      width: 22rem;
      max-width: 90vw;
      display: flex;
      flex-direction: column;
      gap: var(--sp-3);
    }
    .bbox-form.wide {
      width: 32rem;
    }
    .bbox-form h3 {
      margin: 0;
      font-size: var(--fs-md);
    }
    .bbox-preview {
      max-height: 20rem;
      overflow: auto;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-1);
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .bbox-preview-img {
      max-width: 100%;
      height: auto;
      display: block;
    }
    .bbox-preview-loading,
    .bbox-preview-empty {
      color: var(--dim);
      font-size: var(--fs-sm);
      padding: var(--sp-3);
      text-align: center;
    }
    .bbox-detected {
      font-size: var(--fs-sm);
      color: var(--fg);
    }
    .bbox-detected.dim {
      color: var(--dim);
      font-style: italic;
    }
    .bbox-warn {
      background: color-mix(in oklab, var(--sev-warn) 15%, transparent);
      border: 1px solid color-mix(in oklab, var(--sev-warn) 40%, transparent);
      border-radius: var(--radius-sm);
      padding: var(--sp-1) var(--sp-2);
      font-size: var(--fs-xs);
    }
    .bbox-existing-labels {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-2);
      max-height: 8rem;
      overflow: auto;
    }
    .bbox-existing-labels-title {
      font-size: var(--fs-xs);
      color: var(--dim);
      margin-bottom: var(--sp-1);
    }
    .bbox-existing-labels ul {
      margin: 0;
      padding: 0;
      list-style: none;
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr));
      gap: var(--sp-1);
      font-size: var(--fs-xs);
    }
    .bbox-existing-labels .dim {
      color: var(--dim);
    }
    .bbox-form label {
      display: flex;
      flex-direction: column;
      gap: var(--sp-1);
      font-size: var(--fs-sm);
    }
    .bbox-form input,
    .bbox-form select,
    .bbox-form textarea {
      padding: var(--sp-1) var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--surface);
      color: var(--fg);
      font: inherit;
      font-family: var(--font-body);
      resize: vertical;
    }
    .bbox-form-actions {
      display: flex;
      gap: var(--sp-2);
      justify-content: flex-end;
    }
    .workspace-panes .tab-content > .pane:first-child {
      border-left: none;
    }
    .workspace-panes .pane.detail {
      padding: var(--sp-2);
    }
    .workspace-panes .pane-empty {
      padding: var(--sp-4);
      color: var(--dim);
      font-style: italic;
      display: flex;
      align-items: center;
      justify-content: center;
      text-align: center;
    }
    aside {
      overflow: auto;
      padding: var(--sp-2);
      display: grid;
      grid-template-rows: auto auto 1fr;
      gap: var(--sp-1);
      align-content: start;
    }
    /* Right pane stacks structured detail and (when present) the
       augmented reader in one scrolling column. Detail scrolls
       away as curator scrolls down, so the reader takes the full
       pane below it — matches the "reading takes priority once
       you're reading" pattern from the reader design. */
    section {
      overflow: auto;
      padding: var(--sp-2);
      display: flex;
      flex-direction: column;
      gap: var(--sp-3);
    }
    section .reader {
      flex: 1;
      min-height: 40rem;
      width: 100%;
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
    }
    section .detail-header {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: var(--sp-2);
    }
    section .header-actions {
      display: flex;
      align-items: center;
      gap: var(--sp-1);
    }
    section .icon-btn {
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-1);
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      line-height: 1;
    }
    section .icon-btn:hover {
      border-color: var(--accent);
      color: var(--accent);
    }
    section .icon-btn.danger:hover {
      border-color: var(--error);
      color: var(--error);
    }
    /* Citation copy toolbar in the center column — sits at the
       bottom of the structured detail. Matches the reader's
       cite-toolbar visual language so a curator moving between
       the two surfaces recognizes the affordance. */
    section .cite-toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: var(--sp-1);
      margin-top: var(--sp-3);
    }
    section .cite-toolbar button {
      font: inherit;
      font-size: var(--fs-sm);
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      cursor: pointer;
    }
    section .cite-toolbar button:hover {
      border-color: var(--accent);
    }
    section .cite-toolbar button.copied {
      border-color: var(--accent);
      color: var(--accent);
    }
    aside {
      border-right: 1px solid var(--border);
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
      grid-template-rows: auto auto 1fr;
    }
    aside .search-row {
      display: flex;
      gap: var(--sp-1);
      align-items: stretch;
    }
    aside .search {
      flex: 1;
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      font: inherit;
      box-sizing: border-box;
      min-width: 0;
    }
    aside .search:focus {
      outline: none;
      border-color: var(--accent);
    }
    aside .add-btn {
      padding: 0 var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      cursor: pointer;
      font: inherit;
      font-size: var(--fs-lg);
      line-height: 1;
    }
    aside .add-btn:hover {
      border-color: var(--accent);
      color: var(--accent);
    }
    aside .sort-row {
      /* Grid over flex — deterministic 50/50 columns that don't
         re-flow when the button's border-color changes on active.
         The earlier flex attempt widened visibly on click because
         browsers' default button min-width and the flex-basis
         interpretation combined unexpectedly. */
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: var(--sp-1);
    }
    aside .sort-chip {
      min-width: 0;
      box-sizing: border-box;
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-1) var(--sp-2);
      cursor: pointer;
      font: inherit;
      font-size: var(--fs-xs);
      color: var(--dim);
    }
    aside .sort-chip:focus {
      outline: none;
    }
    aside .sort-chip:hover:not([disabled]) {
      border-color: var(--accent);
    }
    aside .sort-chip.active {
      border-color: var(--accent);
      color: var(--accent);
    }
    aside .sort-chip[disabled] {
      opacity: 0.5;
      cursor: not-allowed;
    }
    aside .scroll-sentinel {
      height: 1px;
    }
    ul.list {
      list-style: none;
      margin: 0;
      padding: 0;
    }
    ul.list li {
      padding: var(--sp-1) var(--sp-2);
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
    .year { color: var(--dim); margin-left: var(--sp-1); }
    .title { display: block; color: var(--dim); font-weight: normal; margin-top: var(--sp-1); }
    ul.list li.selected .year,
    ul.list li.selected .title { color: var(--accent-fg); }
    section h2 {
      margin: 0 0 var(--sp-1) 0;
      font-size: var(--fs-lg);
    }
    section hr {
      border: 0;
      border-top: 1px solid var(--border);
      margin: var(--sp-2) 0;
    }
    section dl {
      margin: 0;
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: var(--sp-1) var(--sp-4);
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    section dt { color: var(--dim); }
    section dd { margin: 0; word-break: break-word; }
    .citation {
      margin-top: var(--sp-4);
      color: var(--dim);
      font-family: var(--font-body);
      line-height: 1.4;
    }
    .attached-pdfs {
      margin-top: var(--sp-4);
      padding: var(--sp-2) var(--sp-3);
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
    }
    .attached-pdfs-title {
      font-size: var(--fs-xs);
      color: var(--dim);
      margin-bottom: var(--sp-1);
    }
    .attached-pdf-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--sp-2);
      font-size: var(--fs-sm);
      font-family: var(--font-mono);
    }
    .attached-pdf-sha {
      color: var(--fg);
    }
    .empty { color: var(--dim); padding: var(--sp-2); }
    .error { color: var(--error); font-family: var(--font-mono); padding: var(--sp-2); }
    /* Dashboard landing view — shown in the right pane when no ref
       is selected. Fills the workspace column so the 4 step bars +
       hot list read as a full page, not a floating widget. */
    .dashboard {
      padding: var(--sp-5) var(--sp-6);
      overflow: auto;
      color: var(--fg);
    }
    .dashboard h2 {
      margin: 0 0 var(--sp-2);
      font-size: var(--fs-lg);
    }
    .dashboard-total {
      color: var(--dim);
      font-size: var(--fs-sm);
      margin-bottom: var(--sp-5);
    }
    .dashboard-steps {
      display: flex;
      flex-direction: column;
      gap: var(--sp-3);
      margin-bottom: var(--sp-6);
    }
    .dashboard-step {
      display: grid;
      grid-template-columns: minmax(10rem, 14rem) 1fr auto;
      align-items: center;
      gap: var(--sp-3);
      font-size: var(--fs-sm);
    }
    .dashboard-step-label {
      color: var(--fg);
    }
    .dashboard-step-bar {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      height: 0.9rem;
      overflow: hidden;
    }
    .dashboard-step-fill {
      background: color-mix(in oklab, var(--sev-info) 55%, transparent);
      height: 100%;
      transition: width 200ms ease-out;
    }
    .dashboard-step-count {
      color: var(--dim);
      font-variant-numeric: tabular-nums;
      font-size: var(--fs-xs);
      min-width: 4.5rem;
      text-align: right;
    }
    .dashboard details {
      margin-bottom: var(--sp-6);
      color: var(--dim);
      font-size: var(--fs-sm);
    }
    .dashboard details summary {
      cursor: pointer;
      color: var(--fg);
      margin-bottom: var(--sp-2);
    }
    .dashboard details dl {
      margin: 0 0 0 var(--sp-3);
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: var(--sp-1) var(--sp-3);
    }
    .dashboard details dt { color: var(--fg); }
    .dashboard details dd { margin: 0; }
    .dashboard-attention h3 {
      font-size: var(--fs-md);
      margin: 0 0 var(--sp-2);
    }
    .dashboard-attention ul {
      list-style: none;
      margin: 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: var(--sp-1);
    }
    .dashboard-attention li {
      font-size: var(--fs-sm);
      color: var(--fg);
    }
    .dashboard-attention .count {
      font-weight: 600;
      font-variant-numeric: tabular-nums;
    }
    .dashboard-loading {
      color: var(--dim);
      padding: var(--sp-4);
    }
  `;

  constructor() {
    super();
    this.selectId = "";
    this.workspaceTab = "";
    this._hits = [];
    this._selectedId = "";
    this._current = null;
    this._loading = false;
    this._error = "";
    this._query = "";
    this._editRefID = "";
    this._addingNew = false;
    this._jatsRefEdit = null;
    this._wide = false;
    this._readerExpanded = false;
    this._workspaceTab = "bibliography";
    // _pdfPage drives the PDF iframe's #page=N fragment. Updated
    // when a curator clicks a figure xref in the reader — see
    // _onReaderMessage. Starts at 1 for each new reference; the
    // constructor runs once but _select() also resets it on
    // reference switch so navigating to a new reference doesn't
    // leave a stale page number.
    this._pdfPage = 1;
    this._sort =
      localStorage.getItem("hive.references.sort") === "author"
        ? "author"
        : "recent";
    this._hasMore = true;
    this._dashboard = null;
    this._activeTool = "read";
    this._corrections = [];
    this._bboxForm = null;
    this._bboxFormBusy = false;
    this._grobidRegions = [];
    this._pendingDropFile = null;
    this._dropActive = false;
  }

  async connectedCallback() {
    super.connectedCallback();
    // Responsive layout gate: wide viewports (~1300px+) get a
    // 3-column layout with the reader in its own pane; narrower
    // ones stack the reader below the detail in a 2-column
    // layout. ResizeObserver on the host watches its own width
    // rather than window.innerWidth so nesting the component
    // inside a narrower container (e.g. a future sidebar-heavy
    // shell) still adapts correctly.
    this._resizeObserver = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width || 0;
      this._wide = w >= 1300;
    });
    this._resizeObserver.observe(this);
    // Reader iframes post messages when a curator clicks a figure
    // xref — the reader wants to switch us to the Digitize tab
    // and jump the PDF pane to the figure's source page.
    // Bound handler stored so disconnectedCallback can remove it.
    this._onReaderMessage = (e) => {
      const d = e.data;
      if (!d || typeof d !== "object") return;
      // Stale-iframe guard shared across message types — a leftover
      // iframe from a prior _current shouldn't hijack the current
      // view.
      const currentSha =
        this._current &&
        this._current.attached_pdf_shas &&
        this._current.attached_pdf_shas[0];
      if (d.sha && currentSha && d.sha !== currentSha) return;
      if (d.type === "hive:jump-to-pdf") {
        if (d.page && Number.isFinite(d.page)) {
          this._pdfPage = d.page;
        }
        this._workspaceTab = "digitize";
        return;
      }
      if (d.type === "hive:merge-ref" && d.refID && this._current) {
        this._postRefMergeCorrection(d.refID, d.sha || currentSha);
        return;
      }
      if (d.type === "hive:edit-ref" && d.refID && this._current) {
        this._openJATSRefEditModal({
          sha: d.sha || currentSha,
          referenceID: this._current.id,
          refID: d.refID,
          fields: d.fields || {},
          suspect: !!d.suspect,
        });
        return;
      }
      if (d.type === "hive:remove-text" && d.text && this._current) {
        this._postTextRemoveCorrection({
          sha: d.sha || currentSha,
          text: d.text,
          pdfPage: d.pdfPage || 0,
        });
        return;
      }
      if (d.type === "hive:restore-text" && d.removeCorrectionID && this._current) {
        this._postTextRestoreCorrection({
          sha: d.sha || currentSha,
          removeCorrectionID: d.removeCorrectionID,
        });
        return;
      }
    };
    window.addEventListener("message", this._onReaderMessage);
    await this._fetchPage(true);
    // Deep-link from the shell (Issues click, deep-link on reload,
    // etc.) auto-selects the requested id — but only when the
    // updated() handler hasn't already selected it. Lit sets the
    // selectId property before connectedCallback runs and the
    // updated() hook fires from the same microtask; whichever gets
    // to _select first wins. The second call would see
    // id === this._selectedId and treat it as a click-to-deselect
    // gesture, snapping the right pane back to the dashboard.
    if (this.selectId && this.selectId !== this._selectedId) {
      this._select(this.selectId);
    }
    // Fetch the dashboard rollup in parallel — small query, needed
    // as soon as the landing view renders. No await; a slow fetch
    // just delays the dashboard's populated state, not the shell.
    this._fetchDashboard();
    // Window-level drag-and-drop capture. Attached on window (not
    // the host) so drops anywhere on the References screen —
    // including over the reader iframe or the pdf viewer — reach
    // us instead of being swallowed by the target element or the
    // browser's default file:/// navigation.
    //
    // Only mounted while SfgaReferences is on-screen, so the
    // capture is naturally scoped to the References screen (drops
    // on Taxa / Metadata / Issues screens fall through to the
    // browser).
    //
    // Iframes present a specific hazard: drops on same-origin
    // iframe content don't bubble out to the parent document. We
    // work around this by setting `body[data-drop-active]` during
    // dragover; global CSS then applies `pointer-events: none` to
    // iframes so the browser routes the drop event to the underlying
    // body element instead of into the iframe's document.
    // Drag state uses two independent signals to stay in sync with
    // reality even when browser events fire unevenly (a common
    // symptom of iframes + pointer-events flipping mid-drag):
    //   1. relatedTarget === null on dragleave — fires exactly
    //      once when the pointer leaves the window entirely, so
    //      no accumulator drift like the enter/leave counter has.
    //   2. Watchdog timer, refreshed on every dragover — if
    //      dragover stops firing for 400 ms the drag ended
    //      without a leave/drop we caught (browser bug, iframe
    //      swallowed it, whatever). Clear the state.
    const clearDropActive = () => {
      if (this._dropActive) {
        this._dropActive = false;
        document.body.removeAttribute("data-drop-active");
      }
      if (this._dropWatchdog) {
        clearTimeout(this._dropWatchdog);
        this._dropWatchdog = null;
      }
    };
    this._clearDropActive = clearDropActive;
    const armWatchdog = () => {
      if (this._dropWatchdog) clearTimeout(this._dropWatchdog);
      this._dropWatchdog = setTimeout(clearDropActive, 400);
    };
    this._onWinDragEnter = (e) => {
      if (!e.dataTransfer) return;
      if (!Array.from(e.dataTransfer.types || []).includes("Files")) return;
      if (!this._dropActive) {
        this._dropActive = true;
        document.body.setAttribute("data-drop-active", "");
      }
      armWatchdog();
    };
    this._onWinDragOver = (e) => {
      if (!e.dataTransfer) return;
      if (!Array.from(e.dataTransfer.types || []).includes("Files")) return;
      // preventDefault on both dragover AND drop is required to
      // suppress the browser's default file:/// navigation.
      e.preventDefault();
      armWatchdog();
    };
    this._onWinDragLeave = (e) => {
      if (!e.dataTransfer) return;
      // relatedTarget is null when the pointer actually left the
      // window; non-null for internal element crossings which we
      // want to ignore.
      if (e.relatedTarget === null) {
        clearDropActive();
      }
    };
    this._onWinDrop = (e) => {
      if (!e.dataTransfer) return;
      // preventDefault regardless of whether we actually process
      // the drop — otherwise a stray drop navigates to file:///.
      e.preventDefault();
      clearDropActive();
      const file = e.dataTransfer.files && e.dataTransfer.files[0];
      if (!file) return;
      this._pendingDropFile = file;
      this._addingNew = true;
    };
    window.addEventListener("dragenter", this._onWinDragEnter);
    window.addEventListener("dragover", this._onWinDragOver);
    window.addEventListener("dragleave", this._onWinDragLeave);
    window.addEventListener("drop", this._onWinDrop);
  }

  disconnectedCallback() {
    if (this._resizeObserver) this._resizeObserver.disconnect();
    if (this._scrollObserver) this._scrollObserver.disconnect();
    if (this._onReaderMessage) {
      window.removeEventListener("message", this._onReaderMessage);
    }
    if (this._onWinDragEnter) {
      window.removeEventListener("dragenter", this._onWinDragEnter);
      window.removeEventListener("dragover", this._onWinDragOver);
      window.removeEventListener("dragleave", this._onWinDragLeave);
      window.removeEventListener("drop", this._onWinDrop);
      document.body.removeAttribute("data-drop-active");
    }
    if (this._dropWatchdog) {
      clearTimeout(this._dropWatchdog);
      this._dropWatchdog = null;
    }
    super.disconnectedCallback();
  }

  firstUpdated() {
    // Attach infinite-scroll observer whose root is the scrolling
    // aside pane. IntersectionObserver fires when the sentinel
    // scrolls into the viewport, triggering the next page fetch.
    // Sentinel re-observation happens in updated() below because
    // the sentinel doesn't exist until _hits has content (lit
    // template branch); observing here alone would be a no-op on
    // first mount with an empty list.
    this._scrollObserver = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && this._hasMore && !this._loading && !this._query.trim()) {
            this._fetchPage(false);
          }
        }
      },
      { root: this.renderRoot.querySelector("aside"), rootMargin: "200px" },
    );
    this._observeSentinel();
  }

  _observeSentinel() {
    if (!this._scrollObserver) return;
    const sentinel = this.renderRoot.querySelector(".scroll-sentinel");
    if (!sentinel || sentinel === this._observedSentinel) return;
    if (this._observedSentinel) this._scrollObserver.unobserve(this._observedSentinel);
    this._scrollObserver.observe(sentinel);
    this._observedSentinel = sentinel;
  }

  // _fetchPage loads references. reset=true starts over (query
  // change / sort change / mount). reset=false appends the next
  // page for infinite scroll. Search mode doesn't paginate — the
  // search endpoint returns top-relevance hits in one shot.
  async _fetchPage(reset) {
    if (!reset && (!this._hasMore || this._loading)) return;
    this._loading = true;
    this._error = "";
    const q = this._query.trim();
    const offset = reset ? 0 : this._hits.length;
    const limit = 200;
    try {
      let items;
      if (q) {
        // Search returns relevance-ranked results in one shot; no
        // pagination. Sort toggle doesn't apply during search.
        const page = await api.reference.search({ q, limit });
        items = page.items || [];
        this._hasMore = false;
      } else {
        const opts = { limit, offset };
        if (this._sort === "recent") opts.sort = "recent";
        const page = await api.reference.list(opts);
        items = page.items || [];
        this._hasMore = items.length === limit;
      }
      this._hits = reset ? items : [...this._hits, ...items];
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      this._loading = false;
    }
  }

  _setSort(sort) {
    if (this._sort === sort) return;
    this._sort = sort;
    try {
      localStorage.setItem("hive.references.sort", sort);
    } catch {}
    this._fetchPage(true);
  }

  // Post-mount: if the shell hands us a new `selectId` (e.g. curator
  // clicked another reference-scoped issue while already on this
  // screen), honor it. Ignored during initial load — that path runs
  // through connectedCallback.
  updated(changed) {
    if (changed.has("selectId") && this.selectId && this.selectId !== this._selectedId) {
      this._select(this.selectId);
    }
    // _dropActive state → document.body[data-drop-active] is
    // synced imperatively in the window listeners (connectedCallback)
    // so the CSS at document level can reach across iframes.
    // Sync incoming workspaceTab (from the URL hash via the shell)
    // into internal state. The suppressTabEmit flag guards against
    // an echo — accepting the hint shouldn't fire another event
    // back to the shell that would just re-apply the same value.
    if (changed.has("workspaceTab") && this.workspaceTab &&
        this.workspaceTab !== this._workspaceTab) {
      this._suppressTabEmit = true;
      this._workspaceTab = this.workspaceTab;
    }
    if (changed.has("_workspaceTab")) {
      if (this._suppressTabEmit) {
        this._suppressTabEmit = false;
      } else {
        this.dispatchEvent(
          new CustomEvent("workspace-tab-changed", {
            detail: { tab: this._workspaceTab },
            bubbles: true,
            composed: true,
          }),
        );
      }
    }
    // When the selected reference changes, if the current tab is
    // no longer meaningful (Digitize tab without an attached PDF)
    // fall back to Bibliography. Skip when _current is null —
    // that's the mid-load state during _select, not a real
    // "loaded ref with no PDF" state. Falling back here would
    // clobber a workspaceTab hint the shell just set from the URL.
    if (
      changed.has("_current") &&
      this._current &&
      this._workspaceTab === "digitize"
    ) {
      const hasReader =
        this._current.attached_pdf_shas &&
        this._current.attached_pdf_shas.length > 0;
      if (!hasReader) this._workspaceTab = "bibliography";
    }
    // The infinite-scroll sentinel lives inside a lit template
    // branch that only renders when _hits is non-empty. Re-observe
    // whenever the sentinel enters the DOM (first data load, or
    // after a sort/search change that wiped and refilled the list).
    if (changed.has("_hits")) {
      this._observeSentinel();
    }
    // Imperative PDF-page nav. Firefox's PDF.js often ignores
    // src-attribute changes that only differ in the fragment
    // (`#page=N`) — the iframe holds its current view instead of
    // scrolling. Setting contentWindow.location.hash directly
    // triggers a real hashchange event that PDF.js listens for and
    // acts on. Same-origin (both served by hive) so cross-frame
    // access is permitted. Guarded on _pdfPage change so it only
    // fires when the curator actually clicked a fig-xref.
    if (changed.has("_pdfPage") && this._pdfPage) {
      this._navPdfPane(this._pdfPage);
    }
  }

  // _navPdfPane imperatively scrolls the Digitize tab's PDF pane
  // to the given page. Called from updated() when _pdfPage changes
  // (fig-xref click in the reader posts a message that ends up
  // setting _pdfPage). Uses requestAnimationFrame so the DOM
  // update from the same render cycle (workspace-tab switch) has
  // flushed before we look for the viewer element.
  //
  // Retries when the viewer hasn't finished rendering pages yet —
  // pdf.js loads asynchronously; a fig-xref click during the
  // initial load would otherwise silently do nothing.
  _navPdfPane(page) {
    let attempts = 0;
    const tryScroll = () => {
      const viewer = this.renderRoot.querySelector("sfga-pdf-viewer.pane.pdf");
      if (viewer && typeof viewer.goToPage === "function") {
        const canvas = viewer.renderRoot?.querySelector(
          `canvas[data-page="${page}"]`,
        );
        if (canvas) {
          viewer.goToPage(page);
          return;
        }
      }
      // Retry up to ~2s while the PDF finishes loading. Bail after
      // that so a truly-broken viewer doesn't spin forever.
      if (++attempts < 20) {
        setTimeout(tryScroll, 100);
      }
    };
    requestAnimationFrame(tryScroll);
  }

  async _select(id) {
    if (!id) return;
    // Clicking the already-selected reference deselects it, which
    // returns the right pane to the dashboard landing view. Matches
    // the sidebar-Refs re-click return path documented in
    // REFERENCES_DASHBOARD_PLAN.
    if (id === this._selectedId) {
      this._selectedId = "";
      this._current = null;
      this._corrections = [];
      this._grobidRegions = [];
      this._activeTool = "read";
      this._fetchDashboard();
      return;
    }
    this._selectedId = id;
    this._current = null;
    // Reset per-ref correction state so the previous ref's chips
    // don't flash on-screen while the new ref's corrections load.
    this._corrections = [];
    this._grobidRegions = [];
    this._activeTool = "read";
    this._fetchCorrections(id);
    // Fresh reference → fresh PDF page. Otherwise the new
    // reference's PDF iframe would open at whatever page the
    // previous reference was last showing.
    this._pdfPage = 1;
    // Announce so the shell can clear its pending-select state; this
    // matters when the same id is routed twice (a second click on the
    // same Issues row should still reveal it, even if the property
    // value hasn't changed).
    this.dispatchEvent(
      new CustomEvent("reference-selected", {
        detail: { id },
        bubbles: true,
        composed: true,
      }),
    );
    try {
      this._current = await api.reference.get(id);
      // Now that we know the sha, fetch the GROBID-extracted
      // regions so the PDF viewer can render them alongside
      // curator corrections.
      const sha =
        this._current?.attached_pdf_shas &&
        this._current.attached_pdf_shas[0];
      if (sha) this._fetchGrobidRegions(sha);
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    }
  }

  // _fetchGrobidRegions pulls every fig/table-wrap region from the
  // sidecar JATS (both curator-added and GROBID-extracted) and
  // filters to just GROBID ones — curator additions come through
  // _fetchCorrections. Rendered as muted overlays so a curator
  // sees the full set of detected regions at a glance without
  // confusing them for correction targets.
  async _fetchGrobidRegions(sha) {
    try {
      const r = await fetch(
        `/api/reference/library/${encodeURIComponent(sha)}/regions`,
      );
      if (!r.ok) return;
      const body = await r.json();
      const items = (body.items || []).filter((rg) => rg.origin === "grobid");
      this._grobidRegions = items;
    } catch (err) {
      // Silent — regions are a visualization aid, not critical.
    }
  }

  // _mergedBBoxes returns the union of curator corrections and
  // GROBID-detected regions in the shape the pdf viewer expects.
  // Three visual states via the `state` field, which the viewer
  // maps to a CSS class for color styling:
  //
  //   - "auto"      — GROBID-detected, no modification (blue)
  //   - "modified"  — GROBID-detected + a bbox-modify targets it
  //                   (blue-green)
  //   - "curator"   — curator-added new region via bbox-add
  //                   (green)
  _mergedBBoxes() {
    const out = [];
    // Build sets of GROBID target ids under active corrections.
    // modifiedTargets → render blue-green; removedTargets → skip
    // entirely (the reapply loop will drop them from the JATS
    // on the next rebuild).
    const modifiedTargets = new Set();
    const removedTargets = new Set();
    for (const c of this._corrections || []) {
      if (c.status !== "active" || !c.target_element_id) continue;
      if (c.kind === "bbox-modify") modifiedTargets.add(c.target_element_id);
      if (c.kind === "bbox-remove") removedTargets.add(c.target_element_id);
    }
    // Curator additions (bbox-add).
    for (const c of this._corrections || []) {
      if (c.kind !== "bbox-add") continue;
      if (!c.bbox || !c.bbox_page) continue;
      out.push({
        id: c.id,
        page: c.bbox_page,
        bbox: c.bbox,
        label: c.label,
        origin: "curator",
        state: "curator",
      });
    }
    // GROBID regions, tagged as auto or modified based on whether
    // an active bbox-modify targets them. The label and bbox for
    // modified regions come from the corrected payload when
    // available so both the rectangle and its label match the
    // curator's fix immediately, rather than only after the sidecar
    // reapply pass.
    for (const g of this._grobidRegions || []) {
      if (!g.bbox || !g.page) continue;
      if (removedTargets.has(g.id)) continue;
      const isModified = modifiedTargets.has(g.id);
      let label = g.label;
      let bbox = g.bbox;
      if (isModified) {
        const mod = (this._corrections || []).find(
          (c) => c.kind === "bbox-modify" && c.target_element_id === g.id,
        );
        if (mod && mod.payload) {
          try {
            const p = JSON.parse(mod.payload);
            if (p.corrected && p.corrected.label) label = p.corrected.label;
            if (p.corrected && p.corrected.bbox) bbox = p.corrected.bbox;
          } catch (err) {
            // ignore — keep grobid label + bbox
          }
        }
      }
      out.push({
        id: g.id,
        page: g.page,
        bbox,
        label,
        origin: "grobid",
        state: isModified ? "modified" : "auto",
      });
    }
    return out;
  }

  // _fetchDashboard pulls the 4-step progress rollup from the
  // server. Called on mount, on deselect, and after any operation
  // that could change step counts (add / save / upload). Silently
  // absorbs failures — dashboard is a landing-page convenience,
  // not a critical view; a fetch error just leaves the previous
  // (or null) rollup in place rather than blocking the shell.
  async _fetchDashboard() {
    try {
      const r = await fetch("/api/reference/dashboard");
      if (r.ok) this._dashboard = await r.json();
    } catch (err) {
      // Intentionally silent — see method doc.
    }
  }

  // _fetchCorrections pulls all corrections attached to the given
  // reference and populates _corrections. The bbox list drives the
  // chip strip above the PDF pane and the overlay rectangles on
  // the pdf viewer. Silent on failure — same rationale as
  // _fetchDashboard.
  async _fetchCorrections(refID) {
    if (!refID) return;
    try {
      const r = await fetch(
        `/api/reference/${encodeURIComponent(refID)}/corrections`,
      );
      if (r.ok) {
        const body = await r.json();
        this._corrections = body.items || [];
      }
    } catch (err) {
      // Intentionally silent.
    }
  }

  // _onBBoxDrawn receives the pdf viewer's bbox-drawn event and
  // opens the bbox form pre-populated with the drawn page + bbox.
  // Fires a preview fetch in the background so the form can show
  // the PNG crop + auto-detected content type + real caption label
  // as soon as the server responds.
  _onBBoxDrawn(e) {
    if (!this._current) return;
    // Open with a naive starting label so the form is usable even
    // if the preview fetch is slow or fails. Preview overwrites
    // the label + content type once it lands.
    this._bboxForm = {
      page: e.detail.page,
      bbox: e.detail.bbox,
      contentType: "figure",
      label: this._suggestBBoxLabel("figure"),
      previewLoading: true,
      previewPNG: "",
      detectedType: "",
      detectedLabel: "",
      extractedText: "",
      existingLabels: [],
      editingID: "", // empty = new correction; set = edit existing
    };
    this._fetchBBoxPreview();
  }

  // _onBBoxEdit opens the form pre-populated with an existing
  // region's values. Two paths:
  //
  //   - Curator addition (bbox-add correction) → editingID is set
  //     to the correction id; save runs as delete-then-create.
  //   - GROBID region → targetElementID is set to the JATS
  //     element id; save creates a bbox-modify correction that
  //     patches the element in place. If a bbox-modify already
  //     targets this element, the form pre-populates with the
  //     corrected values so the curator can iterate on their fix.
  _onBBoxEdit(id, origin) {
    if (!id || !this._current) return;
    if (origin === "grobid") {
      const grobid = (this._grobidRegions || []).find((g) => g.id === id);
      if (!grobid || !grobid.bbox) return;
      // Check for an existing bbox-modify targeting this element.
      const existingMod = (this._corrections || []).find(
        (c) => c.kind === "bbox-modify" && c.target_element_id === id,
      );
      let ctype = grobid.kind || "figure";
      let label = grobid.label || "";
      let caption = "";
      if (existingMod && existingMod.payload) {
        try {
          const p = JSON.parse(existingMod.payload);
          if (p.corrected) {
            if (p.corrected.content_type) ctype = p.corrected.content_type;
            if (p.corrected.label) label = p.corrected.label;
            if (p.corrected.caption) caption = p.corrected.caption;
          }
        } catch (err) {
          // ignore — fall back to grobid defaults
        }
      }
      this._bboxForm = {
        page: grobid.page,
        bbox: grobid.bbox,
        contentType: ctype,
        label,
        caption,
        previewLoading: true,
        previewPNG: "",
        detectedType: "",
        detectedLabel: "",
        extractedText: "",
        existingLabels: [],
        editingID: existingMod ? existingMod.id : "",
        targetElementID: id,
        originalContentType: grobid.kind || "figure",
        originalLabel: grobid.label || "",
        originalCaption: "", // populated by preview fetch's extracted_text
      };
      this._fetchBBoxPreview();
      return;
    }
    const c = (this._corrections || []).find((x) => x.id === id);
    if (!c || !c.bbox) return;
    this._bboxForm = {
      page: c.bbox_page,
      bbox: c.bbox,
      contentType: c.content_type || "figure",
      label: c.label || "",
      caption: "",
      previewLoading: true,
      previewPNG: "",
      detectedType: "",
      detectedLabel: "",
      extractedText: "",
      existingLabels: [],
      editingID: id,
      targetElementID: "",
    };
    this._fetchBBoxPreview();
  }

  // _suggestBBoxLabel returns "figure-N" or "table-N" based on the
  // count of existing bboxes of the same type plus one. Used as
  // the pre-preview fallback; a real caption-derived label
  // supersedes it once the preview lands.
  _suggestBBoxLabel(contentType) {
    const same = (this._corrections || []).filter(
      (c) => c.content_type === contentType,
    );
    return `${contentType}-${same.length + 1}`;
  }

  // _fetchBBoxPreview posts to the correction-preview endpoint
  // and folds the response into the current bbox form: PNG for
  // visual review, detected content type + label for smart
  // defaults, existing labels for curator context.
  //
  // Preview failures degrade gracefully — the form still works
  // without preview data; the curator just picks the fields
  // manually.
  async _fetchBBoxPreview() {
    if (!this._bboxForm || !this._current) return;
    const sha =
      this._current.attached_pdf_shas && this._current.attached_pdf_shas[0];
    if (!sha) {
      this._bboxForm = { ...this._bboxForm, previewLoading: false };
      return;
    }
    try {
      const r = await fetch("/api/reference/correction/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          sidecar_sha: sha,
          page: this._bboxForm.page,
          bbox: this._bboxForm.bbox,
        }),
      });
      if (!r.ok) throw new Error("preview " + r.status);
      const body = await r.json();
      if (!this._bboxForm) return; // curator closed form during fetch
      const next = {
        ...this._bboxForm,
        previewLoading: false,
        previewPNG: body.png_data_url || "",
        detectedType: body.detected_type || "",
        detectedLabel: body.detected_label || "",
        extractedText: body.extracted_text || "",
        existingLabels: body.existing_labels || [],
      };
      // Prefer the detected values when they're present — real
      // caption text always beats a numeric fallback.
      if (body.detected_type) next.contentType = body.detected_type;
      if (body.detected_label) next.label = body.detected_label;
      this._bboxForm = next;
    } catch (err) {
      if (this._bboxForm) {
        this._bboxForm = { ...this._bboxForm, previewLoading: false };
      }
    }
  }

  _closeBBoxForm() {
    this._bboxForm = null;
    this._bboxFormBusy = false;
  }

  // _saveBBoxForm posts the pending bbox as a correction. Two
  // save paths:
  //
  //   - bbox-add (curator drew a new region OR is editing a
  //     previous curator addition) → delete + create. Delete
  //     rebuilds the JATS from TEI, create re-adds with new values.
  //
  //   - bbox-modify (curator is editing a GROBID-detected region)
  //     → delete any existing bbox-modify targeting the same
  //     element, then create fresh with a payload capturing both
  //     original state and corrected state for training-corpus use.
  async _saveBBoxForm() {
    if (!this._bboxForm || !this._current) return;
    const sha =
      this._current.attached_pdf_shas && this._current.attached_pdf_shas[0];
    if (!sha) return;
    this._bboxFormBusy = true;
    const f = this._bboxForm;
    const editingID = f.editingID || "";
    const targetID = f.targetElementID || "";
    try {
      if (editingID) {
        await fetch(
          `/api/reference/correction/${encodeURIComponent(editingID)}`,
          { method: "DELETE" },
        );
      }
      let body;
      if (targetID) {
        // bbox-modify path — patch an existing GROBID element.
        const payload = {
          original: {
            content_type: f.originalContentType || "",
            label: f.originalLabel || "",
            caption: f.originalCaption || "",
          },
          corrected: {},
        };
        if (f.contentType && f.contentType !== f.originalContentType) {
          payload.corrected.content_type = f.contentType;
        }
        if (f.label && f.label !== f.originalLabel) {
          payload.corrected.label = f.label;
        }
        if (f.caption && f.caption !== f.originalCaption) {
          payload.corrected.caption = f.caption;
        }
        body = {
          reference_id: this._current.id,
          sidecar_sha: sha,
          kind: "bbox-modify",
          content_type: f.contentType,
          target_element_id: targetID,
          label: f.label,
          payload: JSON.stringify(payload),
        };
      } else {
        // bbox-add path — brand new curator region.
        body = {
          reference_id: this._current.id,
          sidecar_sha: sha,
          kind: "bbox-add",
          content_type: f.contentType,
          bbox_page: f.page,
          bbox: f.bbox,
          label: f.label,
        };
      }
      const r = await fetch("/api/reference/correction", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (r.ok) {
        this._closeBBoxForm();
        await this._fetchCorrections(this._current.id);
        if (sha) this._fetchGrobidRegions(sha);
        this._reloadReaderIframe();
      } else {
        this._bboxFormBusy = false;
      }
    } catch (err) {
      this._bboxFormBusy = false;
    }
  }

  // _deleteCorrection handles the trash-icon click on a bbox.
  // Two paths depending on the bbox origin:
  //
  //   - "curator" — id is a correction row id. DELETE the row;
  //     server re-derives JATS from TEI + reapply remaining
  //     corrections. Optimistic UI drops the correction locally
  //     while the server catches up.
  //
  //   - "grobid" — id is the JATS element id (not a correction
  //     id). POST a bbox-remove correction that suppresses the
  //     element on this + future reapplies. Optimistic UI drops
  //     the region from _grobidRegions.
  async _deleteCorrection(id, origin) {
    if (!id || !this._current) return;
    if (this._pendingDeletes && this._pendingDeletes.has(id)) return;
    if (!this._pendingDeletes) this._pendingDeletes = new Set();
    this._pendingDeletes.add(id);
    const sha =
      this._current.attached_pdf_shas &&
      this._current.attached_pdf_shas[0];
    if (origin === "grobid") {
      // Optimistic: hide the GROBID region.
      const beforeRegions = this._grobidRegions;
      this._grobidRegions = (this._grobidRegions || []).filter(
        (g) => g.id !== id,
      );
      try {
        const region = (beforeRegions || []).find((g) => g.id === id);
        const contentType = region?.kind || "figure";
        const r = await fetch("/api/reference/correction", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            reference_id: this._current.id,
            sidecar_sha: sha,
            kind: "bbox-remove",
            content_type: contentType,
            target_element_id: id,
          }),
        });
        if (r.ok) {
          await this._fetchCorrections(this._current.id);
          if (sha) this._fetchGrobidRegions(sha);
          this._reloadReaderIframe();
        } else {
          this._grobidRegions = beforeRegions;
        }
      } catch (err) {
        this._grobidRegions = beforeRegions;
      } finally {
        this._pendingDeletes.delete(id);
      }
      return;
    }
    // Curator delete path.
    const before = this._corrections;
    this._corrections = (this._corrections || []).filter((c) => c.id !== id);
    try {
      const r = await fetch(
        `/api/reference/correction/${encodeURIComponent(id)}`,
        { method: "DELETE" },
      );
      if (r.ok) {
        await this._fetchCorrections(this._current.id);
        if (sha) this._fetchGrobidRegions(sha);
        this._reloadReaderIframe();
      } else {
        this._corrections = before;
      }
    } catch (err) {
      this._corrections = before;
    } finally {
      this._pendingDeletes.delete(id);
    }
  }

  // _onBBoxResize persists a new bbox rectangle after a curator
  // drags a corner handle. Two paths mirror the edit-form save
  // logic — the resize is just an edit that changes bbox coords
  // instead of label/content-type/caption.
  //
  //   - "curator" origin — the id is a bbox-add correction row.
  //     Delete + recreate with the new coords, preserving the
  //     original content_type + label so downstream JATS references
  //     to the region survive the id change.
  //
  //   - "grobid" origin — the id is a JATS element id. Create or
  //     replace a bbox-modify correction whose payload.corrected.bbox
  //     records the new coords; apply-to-JATS rewrites the
  //     element's hive:pdf-bbox attribute.
  //
  // Re-extraction of the content under the new bbox (figure PNG
  // re-crop, table row re-parse) is deferred — this pass only
  // persists coord changes so the visible rectangle stays put
  // across reloads.
  async _onBBoxResize(detail) {
    if (!detail || !detail.id || !this._current) return;
    const sha =
      this._current.attached_pdf_shas && this._current.attached_pdf_shas[0];
    if (!sha) return;
    const { id, origin, page, bbox } = detail;
    if (origin === "curator") {
      const existing = (this._corrections || []).find((c) => c.id === id);
      if (!existing) return;
      try {
        await fetch(
          `/api/reference/correction/${encodeURIComponent(id)}`,
          { method: "DELETE" },
        );
        const body = {
          reference_id: this._current.id,
          sidecar_sha: sha,
          kind: "bbox-add",
          content_type: existing.content_type || "figure",
          bbox_page: page,
          bbox,
          label: existing.label || "",
        };
        const r = await fetch("/api/reference/correction", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        if (r.ok) {
          await this._fetchCorrections(this._current.id);
          this._fetchGrobidRegions(sha);
          this._reloadReaderIframe();
        }
      } catch (err) {
        // Best-effort — the viewer will re-sync from the current
        // corrections list on the next _mergedBBoxes render.
      }
      return;
    }
    // grobid origin
    const grobid = (this._grobidRegions || []).find((g) => g.id === id);
    if (!grobid) return;
    const existingMod = (this._corrections || []).find(
      (c) => c.kind === "bbox-modify" && c.target_element_id === id,
    );
    let contentType = grobid.kind || "figure";
    let label = grobid.label || "";
    let caption = "";
    // page travels alongside bbox so the server can drive
    // pdftext.ExtractTable without a JATS lookup. Resize stays on
    // the same page today; the field exists so a future
    // reposition-across-pages flow doesn't need a schema change.
    const corrected = { bbox, page };
    if (existingMod) {
      // Preserve any prior content_type / label / caption edits
      // rather than silently reverting them on resize.
      try {
        const prior = JSON.parse(existingMod.payload || "{}");
        if (prior.corrected) {
          if (prior.corrected.content_type)
            corrected.content_type = prior.corrected.content_type;
          if (prior.corrected.label) corrected.label = prior.corrected.label;
          if (prior.corrected.caption) corrected.caption = prior.corrected.caption;
        }
      } catch (err) {
        // Malformed prior payload — treat as no prior edits.
      }
      try {
        await fetch(
          `/api/reference/correction/${encodeURIComponent(existingMod.id)}`,
          { method: "DELETE" },
        );
      } catch (err) {
        // Ignore — POST below still runs; a stale row would just
        // shadow the new one until re-derive drops it.
      }
    }
    const payload = {
      original: {
        content_type: grobid.kind || "figure",
        label: grobid.label || "",
        caption: "",
        bbox: grobid.bbox,
        page: grobid.page,
      },
      corrected,
    };
    const body = {
      reference_id: this._current.id,
      sidecar_sha: sha,
      kind: "bbox-modify",
      content_type: contentType,
      target_element_id: id,
      label: corrected.label || label,
      payload: JSON.stringify(payload),
    };
    try {
      const r = await fetch("/api/reference/correction", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (r.ok) {
        await this._fetchCorrections(this._current.id);
        this._fetchGrobidRegions(sha);
        this._reloadReaderIframe();
      }
    } catch (err) {
      // Best-effort — see curator branch.
    }
  }

  // _postRefMergeCorrection posts a ref-merge correction for the
  // given <ref> id. Triggered by a hive:merge-ref postMessage from
  // the reader's suspect-refs banner. On success reloads the reader
  // iframe so the JATS with the merged ref becomes visible.
  //
  // Idempotency: the button is enabled per-render; a duplicate
  // click that fires while a merge is in-flight lands another row.
  // That's benign — ApplyRefMergeToJATS is idempotent (missing
  // target returns the JATS unchanged), so a second row is a no-op
  // on regen. Guarding here would need a per-ref pending set; not
  // worth the state until we see it happen.
  async _postRefMergeCorrection(refID, sha) {
    if (!refID || !sha || !this._current) return;
    try {
      const r = await fetch("/api/reference/correction", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          reference_id: this._current.id,
          sidecar_sha: sha,
          kind: "ref-merge",
          content_type: "reference",
          target_element_id: refID,
        }),
      });
      if (r.ok) {
        await this._fetchCorrections(this._current.id);
        this._reloadReaderIframe();
      }
    } catch (err) {
      // Swallow; the user will re-click if the network hiccupped.
    }
  }

  // _setActiveTool switches the active correction tool and broadcasts
  // the new state to the reader iframe so it can activate the
  // matching interaction mode (e.g. hover-highlight paragraphs when
  // text-remove goes active, cursor style, click-to-target). The PDF
  // viewer picks up its own tool state via the .mode property binding
  // in the render tree — no message needed there.
  _setActiveTool(tool) {
    this._activeTool = tool;
    const iframes = this.renderRoot?.querySelectorAll("iframe.pane.reader") || [];
    for (const iframe of iframes) {
      if (iframe.contentWindow) {
        iframe.contentWindow.postMessage(
          { type: "hive:set-tool", tool },
          "*",
        );
      }
    }
  }

  // _postTextRestoreCorrection posts a text-restore correction that
  // reverses a prior text-remove. Triggered by clicking the inline
  // red-dot marker in the reader. The pair (remove + restore) stays
  // in the audit log as training-corpus signal that the curator
  // changed their mind about the removal.
  async _postTextRestoreCorrection({ sha, removeCorrectionID }) {
    if (!sha || !removeCorrectionID || !this._current) return;
    try {
      const r = await fetch("/api/reference/correction", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          reference_id: this._current.id,
          sidecar_sha: sha,
          kind: "text-restore",
          content_type: "text",
          payload: JSON.stringify({
            remove_correction_id: removeCorrectionID,
          }),
        }),
      });
      if (r.ok) {
        await this._fetchCorrections(this._current.id);
        this._reloadReaderIframe();
      }
    } catch (err) {
      // Same swallow pattern as _postRefMergeCorrection.
    }
  }

  // _postTextRemoveCorrection posts a text-remove correction for a
  // paragraph the curator flagged via the reader's remove-text tool.
  // Payload carries the paragraph's exact text (authoritative training-
  // data source) and pdf_page hint (disambiguates identical noise text
  // repeated on multiple pages). Applier finds the matching <p> by
  // text-content match and wraps it in <hive:noise>.
  async _postTextRemoveCorrection({ sha, text, pdfPage }) {
    if (!sha || !text || !this._current) return;
    try {
      const r = await fetch("/api/reference/correction", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          reference_id: this._current.id,
          sidecar_sha: sha,
          kind: "text-remove",
          content_type: "text",
          payload: JSON.stringify({ text, pdf_page: pdfPage || 0 }),
        }),
      });
      if (r.ok) {
        await this._fetchCorrections(this._current.id);
        this._reloadReaderIframe();
      }
    } catch (err) {
      // Same swallow pattern as _postRefMergeCorrection — the tool is
      // one-shot per click, curator can retry.
    }
  }

  // _reloadReaderIframe forces the reader pane to re-fetch its
  // sidecar so the JATS mutation (new fig/table-wrap added by the
  // just-saved correction) becomes visible. Simplest reliable
  // mechanism: reassign the iframe's src to the same URL with a
  // cache-busting query param.
  _reloadReaderIframe() {
    const iframes = this.renderRoot?.querySelectorAll("iframe.pane.reader") || [];
    for (const iframe of iframes) {
      const base = iframe.src.replace(/[?&]t=\d+$/, "");
      const sep = base.includes("?") ? "&" : "?";
      iframe.src = `${base}${sep}t=${Date.now()}`;
    }
  }

  // _renderChipStrip renders the row of bbox chips above the PDF
  // pane in the Digitize tab. Each chip shows the label + a delete
  // control; clicking the label scrolls the pdf viewer to the
  // bbox's page.
  _renderChipStrip() {
    const chips = (this._corrections || []).filter(
      (c) => c.kind === "bbox-add" && c.status === "active",
    );
    if (chips.length === 0) {
      return html`<div class="chip-strip">
        <span class="empty-hint"
          >Draw a box on the PDF while BBox tool is active to add a
          missed figure or table.</span
        >
      </div>`;
    }
    return html`<div class="chip-strip">
      ${chips.map(
        (c) => html`
          <span class="bbox-chip" title=${"page " + c.bbox_page}>
            <span
              class="chip-label"
              @click=${() => this._scrollPdfToPage(c.bbox_page)}
              >${c.label || c.content_type || "bbox"}</span
            >
            <button
              class="chip-delete"
              type="button"
              aria-label="Delete correction"
              @click=${() => this._deleteCorrection(c.id)}
            >
              ×
            </button>
          </span>
        `,
      )}
    </div>`;
  }

  _scrollPdfToPage(page) {
    const viewer = this.renderRoot?.querySelector("sfga-pdf-viewer.pane.pdf");
    if (viewer && typeof viewer.goToPage === "function") viewer.goToPage(page);
  }

  // _renderBBoxForm renders the modal that pops up after a bbox is
  // drawn on the PDF. Shows the PNG preview of the cropped region,
  // any auto-detected content type + label from the caption text,
  // and the list of existing figs/tables in the paper for context.
  //
  // Preview data is fetched asynchronously; the form is usable
  // immediately with placeholder values and refreshes when the
  // preview lands.
  _renderBBoxForm() {
    if (!this._bboxForm) return nothing;
    const f = this._bboxForm;
    const detectionMismatch =
      f.detectedType && f.detectedType !== f.contentType;
    return html`
      <div
        class="bbox-form-backdrop"
        @click=${(e) => {
          if (e.target === e.currentTarget) this._closeBBoxForm();
        }}
      >
        <div class="bbox-form wide" role="dialog" aria-modal="true" aria-labelledby="bbox-form-title">
          <h3 id="bbox-form-title">
            ${f.editingID ? "Edit region" : "Add missed region"} — page ${f.page}
          </h3>
          <div class="bbox-preview">
            ${f.previewLoading
              ? html`<div class="bbox-preview-loading" role="status">
                  Loading preview…
                </div>`
              : f.previewPNG
                ? html`<img
                    class="bbox-preview-img"
                    src=${f.previewPNG}
                    alt="Preview of the cropped bbox region"
                  />`
                : html`<div class="bbox-preview-empty">
                    Preview unavailable — pdftocairo missing on the server or
                    the source PDF isn't accessible. Save still works.
                  </div>`}
          </div>
          ${f.detectedLabel
            ? html`<div class="bbox-detected">
                Detected caption: <strong>${f.detectedLabel}</strong>
              </div>`
            : f.previewLoading
              ? nothing
              : html`<div class="bbox-detected dim">
                  No caption prefix detected — pick content type and
                  label manually.
                </div>`}
          <label>
            Content type
            <select
              .value=${f.contentType}
              @change=${(e) => {
                const next = e.target.value;
                // Preserve the label unless it was still the naive
                // pre-preview suggestion — curator override wins.
                const naive = this._suggestBBoxLabel(f.contentType);
                const label =
                  f.label === naive
                    ? this._suggestBBoxLabel(next)
                    : f.label;
                this._bboxForm = { ...f, contentType: next, label };
              }}
            >
              <option value="figure">Figure</option>
              <option value="table">Table</option>
            </select>
          </label>
          ${detectionMismatch
            ? html`<div class="bbox-warn" role="alert">
                Caption says "${f.detectedLabel}" but content type is set
                to "${f.contentType}". Double-check before saving.
              </div>`
            : nothing}
          <label>
            Label
            <input
              type="text"
              placeholder=${f.contentType === "table"
                ? "e.g. Table 1"
                : "e.g. Figure 1"}
              .value=${f.label}
              @input=${(e) => (this._bboxForm = { ...f, label: e.target.value })}
            />
          </label>
          ${f.targetElementID
            ? html`<label>
                Caption
                <textarea
                  rows="3"
                  placeholder="Corrected caption text — leave blank to keep the extractor's caption"
                  .value=${f.caption || ""}
                  @input=${(e) =>
                    (this._bboxForm = { ...f, caption: e.target.value })}
                ></textarea>
              </label>`
            : nothing}
          ${f.existingLabels && f.existingLabels.length > 0
            ? html`<div class="bbox-existing-labels">
                <div class="bbox-existing-labels-title">
                  Already extracted from this paper:
                </div>
                <ul>
                  ${f.existingLabels.map(
                    (l) => html`<li>
                      <strong>${l.label}</strong>
                      ${l.page ? html`<span class="dim"> — page ${l.page}</span>` : nothing}
                    </li>`,
                  )}
                </ul>
              </div>`
            : nothing}
          <div class="bbox-form-actions">
            <button type="button" @click=${() => this._closeBBoxForm()}>
              Cancel
            </button>
            <button
              type="button"
              class="primary"
              ?disabled=${this._bboxFormBusy}
              @click=${() => this._saveBBoxForm()}
            >
              ${this._bboxFormBusy ? "Saving…" : "Save"}
            </button>
          </div>
        </div>
      </div>
    `;
  }

  // _renderDashboard renders the landing view shown in the right
  // pane when no reference is selected. Mirrors the ASCII mockup
  // in REFERENCES_DASHBOARD_PLAN § Dashboard layout: 4 step bars,
  // collapsed explainer, hot list of attention buckets. Renders
  // "Loading…" while the initial fetch resolves.
  _renderDashboard() {
    if (!this._dashboard) {
      return html`<div class="dashboard dashboard-loading" role="status">Loading dashboard…</div>`;
    }
    const d = this._dashboard;
    const bar = (label, step, hint) => {
      const pct = step.total > 0 ? Math.round((step.done / step.total) * 100) : 0;
      return html`
        <div class="dashboard-step">
          <span class="dashboard-step-label" title=${hint}>${label}</span>
          <div
            class="dashboard-step-bar"
            role="progressbar"
            aria-label=${label + ": " + step.done + " of " + step.total}
            aria-valuenow=${step.done}
            aria-valuemin="0"
            aria-valuemax=${step.total}
          >
            <div class="dashboard-step-fill" style=${"width: " + pct + "%;"}></div>
          </div>
          <span class="dashboard-step-count">${step.done} / ${step.total} · ${pct}%</span>
        </div>
      `;
    };
    return html`
      <div class="dashboard">
        <h2>References</h2>
        <div class="dashboard-total">${d.total} total</div>
        <div class="dashboard-steps">
          ${bar("Step 1 — Bibliographic", d.steps.bibliographic,
            "Author, publication date, and title all populated")}
          ${bar("Step 2 — Digitized", d.steps.digitized,
            "PDF attached and converted to a JATS sidecar")}
          ${bar("Step 3 — Assertions extracted", d.steps.assertions_extracted,
            "Structured claims staged for curator review")}
          ${bar("Step 4 — Imported", d.steps.imported,
            "Reviewed assertions written into the archive")}
        </div>
        <details>
          <summary>What does each step mean?</summary>
          <dl>
            <dt>Bibliographic</dt>
            <dd>Author, publication date, and title are captured so the reference can compose a citation and match against external DBs.</dd>
            <dt>Digitized</dt>
            <dd>The attached PDF has been converted to a structured JATS sidecar and is readable in hive's reader.</dd>
            <dt>Assertions extracted</dt>
            <dd>Extraction has produced structured claims (taxa, distributions, vernaculars, ...) from the JATS, awaiting curator review.</dd>
            <dt>Imported</dt>
            <dd>The reviewed assertions have been written into the archive proper and appear in the taxonomic tree.</dd>
          </dl>
        </details>
        ${d.attention && d.attention.length > 0
          ? html`
              <div class="dashboard-attention">
                <h3>Needs attention</h3>
                <ul>
                  ${d.attention.map(
                    (a) => html`<li>
                      <span class="count">${a.count}</span>
                      ${a.count === 1 ? " ref " : " refs "}${a.description}
                    </li>`,
                  )}
                </ul>
              </div>
            `
          : html`<div class="dashboard-attention"><h3>Needs attention</h3><p class="empty">All caught up.</p></div>`}
      </div>
    `;
  }

  render() {
    if (this._error) {
      return html`<div class="error" role="alert">${this._error}</div>`;
    }
    return html`
      <aside>
        <div class="search-row">
          <input
            class="search"
            type="text"
            placeholder="Search author / title / citation / DOI…"
            .value=${this._query}
            @input=${(e) => this._onQueryChange(e.target.value)}
          />
          <button
            class="add-btn"
            type="button"
            title="Add reference"
            aria-label="Add reference"
            @click=${() => (this._addingNew = true)}
          >
            +
          </button>
        </div>
        <div class="sort-row" role="group" aria-label="Sort references">
          <button
            class=${"sort-chip" + (this._sort === "recent" ? " active" : "")}
            type="button"
            title="Sort by most recently added or edited"
            ?disabled=${!!this._query.trim()}
            @click=${() => this._setSort("recent")}
          >
            Recent
          </button>
          <button
            class=${"sort-chip" + (this._sort === "author" ? " active" : "")}
            type="button"
            title="Sort alphabetically by author"
            ?disabled=${!!this._query.trim()}
            @click=${() => this._setSort("author")}
          >
            A–Z
          </button>
        </div>
        ${this._loading && this._hits.length === 0
          ? html`<div class="empty" role="status">Loading…</div>`
          : this._hits.length === 0
            ? html`<div class="empty">
                ${this._query
                  ? "(No references match the search)"
                  : "(No references)"}
              </div>`
            : html`
                <ul class="list" role="listbox" aria-label="References">
                  ${this._hits.map(
                    (h) => html`
                      <li
                        role="option"
                        aria-selected=${h.id === this._selectedId ? "true" : "false"}
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
                <div class="scroll-sentinel" aria-hidden="true"></div>
                ${this._loading && this._hits.length > 0
                  ? html`<div class="empty" role="status">Loading more…</div>`
                  : nothing}
              `}
      </aside>
      ${this._renderRightPanes()}
      ${this._editRefID || this._addingNew || this._jatsRefEdit
        ? html`<sfga-add-reference-modal
            .editID=${this._editRefID}
            .jatsRef=${this._jatsRefEdit}
            .defaultTab=${this._pendingDropFile
              ? "file"
              : this._jatsRefEdit
                ? "manual"
                : this._addingNew
                  ? "project"
                  : "manual"}
            .initialFile=${this._pendingDropFile}
            @reference-updated=${(e) => this._onEditSaved(e)}
            @reference-picked=${(e) => this._onReferenceAdded(e)}
            @jats-ref-saved=${() => this._onJATSRefEditSaved()}
            @close=${() => this._closeModal()}
          ></sfga-add-reference-modal>`
        : nothing}
      ${this._renderBBoxForm()}
    `;
  }

  _closeModal() {
    this._editRefID = "";
    this._addingNew = false;
    this._pendingDropFile = null;
    this._jatsRefEdit = null;
  }

  // _openJATSRefEditModal reveals the ref-edit modal with the JATS
  // ref descriptor sent by the reader iframe. Reuses the same
  // add-reference modal component; the modal switches into
  // jats-ref mode when the .jatsRef prop is non-null. Reload of
  // the reader after save happens in _onJATSRefEditSaved.
  _openJATSRefEditModal(desc) {
    if (!desc || !desc.refID) return;
    this._jatsRefEdit = desc;
  }

  // _onJATSRefEditSaved closes the modal and refreshes the reader
  // iframe so the JATS mutation from the ref-edit correction shows
  // up immediately. Also refreshes the corrections list so the new
  // row appears if the curator opens the corrections view.
  async _onJATSRefEditSaved() {
    this._closeModal();
    if (this._current) {
      await this._fetchCorrections(this._current.id);
    }
    this._reloadReaderIframe();
  }

  async _onReferenceAdded(e) {
    const id = (e && e.detail && e.detail.id) || "";
    this._closeModal();
    // Refresh list so the new reference appears; select it so the
    // curator lands on the detail they just created. Also refresh
    // the dashboard so its totals reflect the new ref immediately.
    await this._fetchPage(true);
    this._fetchDashboard();
    if (id) this._select(id);
  }

  // _renderRightPanes lays out the detail + optional reader
  // according to viewport width:
  //
  //   Wide (_wide=true) + hasReader:
  //     3-column layout — detail in its own middle column, reader
  //     in a 3rd column that fills the remaining width. Each
  //     pane scrolls independently so the reader can be read
  //     without losing the structured detail off-screen.
  //
  //   Narrow OR no reader:
  //     2-column layout — detail (and optional reader below it)
  //     stack in the second column, sharing one scrolling area.
  //     Detail scrolls away naturally when the curator scrolls
  //     into the reader.
  _renderRightPanes() {
    // Landing view — no reference selected → show the dashboard
    // rollup instead of an empty workspace. Curator picks a
    // reference from the list to enter the detail view; clicking
    // the same reference again (or Refs in the sidebar) returns
    // here.
    if (!this._current) {
      return this._renderDashboard();
    }
    const hasReader =
      this._current &&
      this._current.attached_pdf_shas &&
      this._current.attached_pdf_shas.length > 0;
    const readerSrc = hasReader
      ? `/read.html?sha=${encodeURIComponent(this._current.attached_pdf_shas[0])}`
      : "";
    const pdfSrc = hasReader
      ? `/api/reference/library/${encodeURIComponent(this._current.attached_pdf_shas[0])}/source#page=${this._pdfPage || 1}`
      : "";
    // Tabs are always present so curators discover the alternative
    // side-by-side workflows (digitize, assertion-review) without
    // hunting for a modal. Assertions tab renders a "coming soon"
    // placeholder until the extraction pipeline lands — showing it
    // now teaches the eventual UX shape.
    return html`
      <div class="workspace">
        <div class="workspace-tabs" role="tablist">
          <button
            role="tab"
            aria-selected=${this._workspaceTab === "bibliography"}
            class=${this._workspaceTab === "bibliography" ? "active" : ""}
            @click=${() => (this._workspaceTab = "bibliography")}
          >
            Bibliography
          </button>
          <button
            role="tab"
            aria-selected=${this._workspaceTab === "digitize"}
            class=${this._workspaceTab === "digitize" ? "active" : ""}
            ?disabled=${!hasReader}
            title=${hasReader
              ? "Compare the augmented JATS reader with the source PDF"
              : "No PDF attached to this reference"}
            @click=${() => (this._workspaceTab = "digitize")}
          >
            Digitize
          </button>
          <button
            role="tab"
            aria-selected=${this._workspaceTab === "assertions"}
            class=${this._workspaceTab === "assertions" ? "active" : ""}
            title="Review structured assertions extracted from the paper against the source (coming soon)"
            @click=${() => (this._workspaceTab = "assertions")}
          >
            Assertions
          </button>
        </div>
        <div class="workspace-panes">
          ${this._renderWorkspaceContent(hasReader, readerSrc, pdfSrc)}
        </div>
      </div>
    `;
  }

  // _renderWorkspaceContent always renders all three tabs (each in
  // its own wrapper div) and toggles visibility via CSS. Keeping
  // inactive tabs mounted preserves per-tab scroll positions and
  // iframe state (reader-page, PDF-page) across tab switches within
  // the same reference — so a curator who scrolls the reader to
  // page 5 in Bibliography, jumps to Digitize to check the PDF,
  // and toggles back to Bibliography lands exactly where they left
  // off. Switching to a different reference re-mounts everything
  // (Lit re-renders when this._current changes; iframe srcs point
  // at the new sha, forcing a fresh load) — which is the intended
  // "different reference = clean slate" behavior.
  _renderWorkspaceContent(hasReader, readerSrc, pdfSrc) {
    return html`
      <div
        class="tab-content"
        ?hidden=${this._workspaceTab !== "bibliography"}
      >
        <section class="pane detail">${this._renderDetail()}</section>
        ${hasReader
          ? html`<iframe class="pane reader" title="Reader" src=${readerSrc}></iframe>`
          : html`<div class="pane pane-empty">
              No source PDF attached — the reader pane will appear
              once a PDF is attached via the reference-edit modal.
            </div>`}
      </div>
      <div
        class="tab-content digitize"
        ?hidden=${this._workspaceTab !== "digitize"}
      >
        ${hasReader
          ? html`
              <div class="digitize-grid">
                <div class="reader-column">
                  <div class="reader-toolbar" role="toolbar" aria-label="Reader tools">
                    <button
                      type="button"
                      class=${"tool-btn" + (this._activeTool === "read" ? " active" : "")}
                      aria-pressed=${this._activeTool === "read"}
                      title="Read — no active reader-side correction tool"
                      @click=${() => this._setActiveTool("read")}
                    >Read</button>
                    <button
                      type="button"
                      class=${"tool-btn" + (this._activeTool === "text-remove" ? " active" : "")}
                      aria-pressed=${this._activeTool === "text-remove"}
                      title="Remove noise — click a paragraph in the reader to remove header/footer text GROBID pulled into the body"
                      @click=${() => this._setActiveTool("text-remove")}
                    >Remove noise</button>
                  </div>
                  <iframe class="pane reader" title="Reader" src=${readerSrc}></iframe>
                </div>
                <div class="pdf-column">
                  <div class="pdf-toolbar" role="toolbar" aria-label="PDF tools">
                    <button
                      type="button"
                      class=${"tool-btn" + (this._activeTool === "read" ? " active" : "")}
                      aria-pressed=${this._activeTool === "read"}
                      title="Read — no active correction tool"
                      @click=${() => this._setActiveTool("read")}
                    >Read</button>
                    <button
                      type="button"
                      class=${"tool-btn" + (this._activeTool === "bbox" ? " active" : "")}
                      aria-pressed=${this._activeTool === "bbox"}
                      title="BBox — click and drag on the PDF to mark a missed figure or table"
                      @click=${() => this._setActiveTool("bbox")}
                    >BBox</button>
                  </div>
                  ${this._renderChipStrip()}
                  <sfga-pdf-viewer
                    class="pane pdf"
                    .src=${pdfSrc.replace(/#.*$/, "")}
                    .mode=${this._activeTool}
                    .bboxes=${this._mergedBBoxes()}
                    @bbox-drawn=${(e) => this._onBBoxDrawn(e)}
                    @bbox-delete=${(e) => this._deleteCorrection(e.detail.id, e.detail.origin)}
                    @bbox-edit=${(e) => this._onBBoxEdit(e.detail.id, e.detail.origin)}
                    @bbox-resize=${(e) => this._onBBoxResize(e.detail)}
                  ></sfga-pdf-viewer>
                </div>
              </div>
            `
          : html`<div class="pane pane-empty">
              No source PDF attached to this reference — upload one
              via the reference-edit modal to enable the digitization
              comparison view.
            </div>`}
      </div>
      <div class="tab-content" ?hidden=${this._workspaceTab !== "assertions"}>
        <div class="pane pane-empty">
          <strong>Assertions view — coming soon.</strong> Once the
          extraction pipeline lands, this pane will show the
          scientific names, nomenclatural acts, distributions, and
          other structured claims hive pulled from the paper.
          Clicking an assertion will jump the reader on the right
          to the source paragraph so you can verify.
        </div>
        ${hasReader
          ? html`<iframe class="pane reader" title="Reader" src=${readerSrc}></iframe>`
          : html`<div class="pane pane-empty">
              No source available — attach a PDF to enable the reader pane.
            </div>`}
      </div>
    `;
  }

  _openEdit() {
    if (this._selectedId) this._editRefID = this._selectedId;
  }

  async _onEditSaved(e) {
    const id = (e && e.detail && e.detail.id) || this._editRefID;
    this._editRefID = "";
    // Reload the current detail so structured-field changes made
    // in the modal are visible immediately. Also refresh the list
    // so any author / title / citation edits update the visible
    // list row.
    if (id) {
      try {
        this._current = await api.reference.get(id);
      } catch {}
    }
    await this._fetchPage(true);
    // Edits can move a reference across step boundaries (filling in
    // author / issued / title advances step 1). Refresh the
    // dashboard so its counts stay in sync with the change.
    this._fetchDashboard();
  }

  // _onQueryChange debounces the search endpoint. Empty query
  // restores the default paginated list (first 200) — same as
  // the initial mount fetch. Trailing-edge 200 ms matches the
  // reference-modal Project-tab search debounce so keystrokes
  // feel consistent across the two surfaces.
  _onQueryChange(q) {
    this._query = q;
    clearTimeout(this._searchDebounce);
    this._searchDebounce = setTimeout(() => this._fetchPage(true), 200);
  }

  _renderDetail() {
    if (!this._current) return html`<div class="empty" role="status">Loading…</div>`;
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
      <div class="detail-header">
        <h2>${r.title || r.citation || r.id}</h2>
        <div class="header-actions">
          <button
            class="icon-btn"
            type="button"
            @click=${() => (this._addingNew = true)}
            title="Add reference"
            aria-label="Add reference"
          >
            ${renderIcon("plus", 16)}
          </button>
          <button
            class="icon-btn"
            type="button"
            @click=${() => this._openEdit()}
            title="Edit reference"
            aria-label="Edit reference"
          >
            ${renderIcon("pencil", 16)}
          </button>
          <button
            class="icon-btn danger"
            type="button"
            @click=${() => this._deleteReference()}
            title="Delete reference"
            aria-label="Delete reference"
          >
            ${renderIcon("trash-2", 16)}
          </button>
        </div>
      </div>
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
      ${r.attached_pdf_shas && r.attached_pdf_shas.length > 0
        ? html`<div class="attached-pdfs">
            <div class="attached-pdfs-title">Attached PDF</div>
            ${r.attached_pdf_shas.map(
              (sha) => html`<div class="attached-pdf-row">
                <span class="attached-pdf-sha" title=${sha}
                  >${sha.slice(0, 12)}…</span
                >
                <button
                  class="icon-btn danger"
                  type="button"
                  title="Detach this PDF — removes the attachment, all extraction corrections for it, and (if not shared with another reference) the sidecar files on disk"
                  aria-label="Detach PDF"
                  @click=${() => this._detachPDF(sha)}
                >
                  ${renderIcon("trash-2", 16)}
                </button>
              </div>`,
            )}
          </div>`
        : nothing}
      <div class="cite-toolbar">
        <button @click=${(e) => this._copy(r.citation || r.title || "", e)}>
          Copy citation
        </button>
        <button @click=${(e) => this._copy(this._buildCSLJSON(r), e)}>
          Copy CSL-JSON
        </button>
        <button @click=${(e) => this._copy(this._buildBibTeX(r), e)}>
          Copy BibTeX
        </button>
        ${r.doi
          ? html`<button @click=${(e) => this._copy(`https://doi.org/${r.doi}`, e)}>
              Copy DOI URL
            </button>`
          : nothing}
      </div>
    `;
  }

  // _detachPDF removes the (reference, sha) attachment and cascades:
  //   - Correction rows for this (reference, sha) are deleted so they
  //     don't silently re-apply on a future re-attach.
  //   - If no other reference still uses this sha, sidecar files
  //     (pdf, tei.xml, jats.xml, figures/, meta.json) are wiped from
  //     disk and every correction row targeting the sha is removed.
  //
  // Confirms first — this is destructive and the curator's bboxes
  // for this PDF disappear.
  async _detachPDF(sha) {
    if (!this._selectedId || !sha) return;
    if (
      !confirm(
        `Detach this PDF?\n\nThis removes the attachment and every extraction correction you made against it. If no other reference is using this PDF, the sidecar files (rendered pages, extracted figures, JATS) will also be deleted from disk. You can re-upload the same PDF later, but the corrections won't come back.`,
      )
    ) {
      return;
    }
    try {
      const r = await fetch(
        `/api/reference/${encodeURIComponent(this._selectedId)}/attached-pdf/${encodeURIComponent(sha)}`,
        { method: "DELETE" },
      );
      if (!r.ok) {
        alert("Detach failed: HTTP " + r.status);
        return;
      }
      // Refetch the reference to update attached_pdf_shas and
      // clear any correction / region UI that depended on it.
      this._current = await api.reference.get(this._selectedId);
      this._corrections = [];
      this._grobidRegions = [];
      this._fetchDashboard();
    } catch (err) {
      alert(
        "Detach failed: " +
          (err instanceof Problem ? err.detail || err.title : String(err)),
      );
    }
  }

  async _deleteReference() {
    if (!this._selectedId || !this._current) return;
    const label =
      this._current.title || this._current.citation || this._selectedId;
    if (
      !confirm(
        `Delete this reference?\n\n${label}\n\nThe reference row is removed only when no other record cites it; otherwise the delete fails and you'll need to un-cite it first.`,
      )
    ) {
      return;
    }
    try {
      await api.reference.delete(this._selectedId);
    } catch (err) {
      alert(
        `Delete failed: ${
          err instanceof Problem ? err.detail || err.title : String(err)
        }`,
      );
      return;
    }
    this._current = null;
    this._selectedId = "";
    // Refresh list and pick a neighboring reference so the pane
    // isn't blank after the delete.
    await this._fetchPage(true);
    if (this._hits.length > 0) this._select(this._hits[0].id);
  }

  async _copy(text, event) {
    const btn = event.currentTarget;
    const orig = btn ? btn.textContent : "";
    try {
      await navigator.clipboard.writeText(text);
      if (btn) {
        btn.textContent = "Copied";
        btn.classList.add("copied");
        setTimeout(() => {
          btn.textContent = orig;
          btn.classList.remove("copied");
        }, 1500);
      }
    } catch (e) {
      alert("Copy failed: " + e.message);
    }
  }

  // _buildCSLJSON / _buildBibTeX mirror the reader's formatters,
  // built off the reference row's structured fields. Kept inline
  // here rather than shared with reader.js because the reader
  // reads from parsed JATS while this reads from the row's JSON;
  // the two formatters end up divergent enough that shared code
  // would be more overhead than value.
  _buildCSLJSON(r) {
    const parts = (r.author || "").split(";").map((s) => s.trim()).filter(Boolean);
    const authorList = parts.map((a) => {
      const p = a.split(" ");
      return p.length >= 2
        ? { family: p.slice(0, -1).join(" "), given: p[p.length - 1] }
        : { family: a };
    });
    const year = r.issued ? parseInt(String(r.issued).slice(0, 4), 10) : null;
    const rec = {
      id: (parts[0] || "unknown").toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 20) + (year || ""),
      type: r.container_title ? "article-journal" : r.publisher ? "book" : "article",
    };
    if (r.title) rec.title = r.title;
    if (authorList.length) rec.author = authorList;
    if (year) rec.issued = { "date-parts": [[year]] };
    if (r.container_title) rec["container-title"] = r.container_title;
    if (r.volume) rec.volume = r.volume;
    if (r.issue) rec.issue = r.issue;
    if (r.page) rec.page = r.page;
    if (r.publisher) rec.publisher = r.publisher;
    if (r.publisher_place) rec["publisher-place"] = r.publisher_place;
    if (r.doi) rec.DOI = r.doi;
    return JSON.stringify(rec, null, 2);
  }

  _buildBibTeX(r) {
    const esc = (s) =>
      String(s || "")
        .replace(/\\/g, "\\\\")
        .replace(/[{}]/g, "\\$&")
        .replace(/[&%#_~$]/g, "\\$&");
    const parts = (r.author || "").split(";").map((s) => s.trim()).filter(Boolean);
    const bibAuthors = parts
      .map((a) => {
        const p = a.split(" ");
        return p.length >= 2
          ? `${p.slice(0, -1).join(" ")}, ${p[p.length - 1]}`
          : a;
      })
      .join(" and ");
    const year = r.issued ? String(r.issued).slice(0, 4) : "";
    const firstAuthorKey = (parts[0] || "unknown").toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 20);
    const kind = r.container_title ? "article" : r.publisher ? "book" : "misc";
    const lines = [`@${kind}{${firstAuthorKey}${year},`];
    if (bibAuthors) lines.push(`  author = {${bibAuthors}},`);
    if (r.title) lines.push(`  title = {{${esc(r.title)}}},`);
    if (r.container_title) lines.push(`  journal = {${esc(r.container_title)}},`);
    if (year) lines.push(`  year = {${year}},`);
    if (r.volume) lines.push(`  volume = {${r.volume}},`);
    if (r.issue) lines.push(`  number = {${r.issue}},`);
    if (r.page) lines.push(`  pages = {${r.page.replace(/-/g, "--")}},`);
    if (r.publisher) lines.push(`  publisher = {${esc(r.publisher)}},`);
    if (r.publisher_place) lines.push(`  address = {${esc(r.publisher_place)}},`);
    if (r.doi) lines.push(`  doi = {${r.doi}},`);
    if (lines.length > 1) lines[lines.length - 1] = lines[lines.length - 1].replace(/,$/, "");
    lines.push("}");
    return lines.join("\n");
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

  static styles = [
    buttonStyles,
    css`
      :host {
        display: block;
      }
      .backdrop {
        position: fixed;
        inset: 0;
        background: color-mix(in oklab, var(--fg) 18%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
      }
      /* Nested-modal hide — see openModal / isTopModal helpers. */
      .backdrop.is-covered {
        visibility: hidden;
      }
      .modal {
        background: var(--bg);
        color: var(--fg);
        border: 1px solid var(--accent);
        border-radius: var(--radius-md);
        padding: var(--sp-4) var(--sp-5);
        width: min(var(--modal-md), 95vw);
        max-height: var(--modal-max-h);
        overflow: auto;
        display: grid;
        gap: var(--sp-3);
        font-family: var(--font-body);
      }
      header {
        display: flex;
        justify-content: space-between;
        align-items: center;
      }
      header h3 {
        margin: 0;
        font-size: var(--fs-lg);
        color: var(--accent);
      }
      .hint {
        color: var(--dim);
        font-style: italic;
        font-size: var(--fs-sm);
      }
      section {
        display: grid;
        gap: var(--sp-1);
      }
      section h4 {
        margin: 0 0 var(--sp-1) 0;
        font-size: var(--fs-md);
        border-bottom: 1px solid var(--border);
        padding-bottom: var(--sp-1);
      }
      /* Fixed first-column width so the description column starts at
         the same left edge across every section. Auto-sizing per <dl>
         staggers the second column because the widest shortcut per
         section varies (Global's longest is Alt+I; Tree includes
         → / l / Enter which is much wider). 11rem accommodates the
         widest key combo currently in the keymap. */
      dl {
        margin: 0;
        display: grid;
        grid-template-columns: 11rem 1fr;
        column-gap: var(--sp-4);
        row-gap: var(--sp-1);
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
    `,
  ];

  render() {
    const covered =
      this._modalStackID && !isTopModal(this._modalStackID)
        ? " is-covered"
        : "";
    return html`
      <div
        class=${"backdrop" + covered}
        @click=${(e) => {
          // Click on the backdrop (not the modal) closes.
          if (e.target === e.currentTarget) this._close();
        }}
      >
        <div class="modal" role="dialog" aria-label="Keyboard shortcuts">
          <header>
            <h3>Keyboard shortcuts</h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._close()}
              title="close"
              aria-label="close"
            >
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

  firstUpdated() {
    // Trap focus so Tab cycles through the shortcut entries and the
    // close button instead of leaking out to the underlying app. The
    // release() restores focus to the help toggle (or wherever
    // triggered the modal) on dismissal.
    this._releaseFocus = trapFocus(this.renderRoot, {
      initialFocus: "button.close-x",
    });
  }

  connectedCallback() {
    super.connectedCallback();
    this._modalStackID = openModal();
    this._unsubModalStack = subscribeModalStack(() => this.requestUpdate());
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    if (this._releaseFocus) {
      this._releaseFocus();
      this._releaseFocus = null;
    }
    if (this._unsubModalStack) {
      this._unsubModalStack();
      this._unsubModalStack = null;
    }
    if (this._modalStackID) {
      closeModal(this._modalStackID);
      this._modalStackID = null;
    }
  }

  _close() {
    this.dispatchEvent(
      new CustomEvent("close", { bubbles: true, composed: true }),
    );
  }
}

// SfgaIssues is the Alt+I "Issues" screen — a dashboard over
// __gsvalidator_results backed by GET /api/issue/summary + /api/issue.
//
// Layout (two panes):
//   left  — summary sidebar: per-rule counts within the active
//           severity filter, plus model-scope tabs. Clicking a row
//           narrows the list on the right to that rule.
//   right — paginated issue list: severity chip + rule label + record
//           label + emit-time message. Row click dispatches
//           `issue-navigate` with a taxon_id when link_taxon_id is set;
//           orphan rows are dim and non-clickable.
//
// Facets:
//   • severity chips (error / warn / info / debug). Default set is
//     error + warn per CLAUDE.md § Validation — "diagnostic" chips
//     start off and require an explicit click to reveal info + debug
//     issues, so a curator eyeballing the screen isn't misled into
//     "fixing" dev-oriented signals like parse quality tier 2.
//   • model tabs (Name / — future: Taxon, Reference). Inactive tabs
//     with zero issues render dim; hive currently only fires
//     name-scoped rules, so the tab bar has one active entry.
//
// Recompute button (top-right) fires POST /api/reindex/validation,
// refreshes both summary and list. Same target as the `hive validate`
// CLI.
class SfgaIssues extends LitElement {
  static properties = {
    _summary: { state: true },
    _issues: { state: true },
    _total: { state: true },
    _loading: { state: true },
    _reindexing: { state: true },
    _error: { state: true },

    // Filter state — severities is a Set for cheap chip toggle;
    // ruleFilter and tableFilter are plain strings ("" means "all").
    _severities: { state: true },
    _ruleFilter: { state: true },
    _tableFilter: { state: true },
    _offset: { state: true },
  };

  static PAGE_SIZE = 50;
  static DEFAULT_SEVERITIES = new Set(["error", "warn"]);

  static styles = css`
    :host {
      display: grid;
      grid-template-columns: minmax(18rem, 28%) 1fr;
      overflow: hidden;
      height: 100%;
    }
    aside,
    section {
      overflow: auto;
      padding: var(--sp-3);
    }
    aside {
      border-right: 1px solid var(--border);
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
    }
    h3 {
      margin: 0 0 var(--sp-2) 0;
      font-size: var(--fs-md);
      color: var(--dim);
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: var(--sp-2);
      align-items: center;
      margin-bottom: var(--sp-3);
    }
    .toolbar .grow {
      flex: 1;
    }
    button.filter-chip {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      padding: var(--sp-1) var(--sp-3);
      border-radius: var(--radius-pill);
      font-size: var(--fs-sm);
      font-weight: 600;
      cursor: pointer;
      background: var(--bg);
      color: var(--dim);
      border: 1px solid var(--border);
      font-family: inherit;
    }
    button.filter-chip.on {
      color: var(--fg);
      background: color-mix(in oklab, var(--fg) 8%, transparent);
      border-color: color-mix(in oklab, var(--fg) 30%, var(--border));
    }
    button.filter-chip.on.sev-error { color: var(--sev-error); border-color: var(--sev-error); background: var(--sev-error-bg); }
    button.filter-chip.on.sev-warn  { color: var(--sev-warn);  border-color: var(--sev-warn);  background: var(--sev-warn-bg); }
    button.filter-chip.on.sev-info  { color: var(--sev-info);  border-color: var(--sev-info);  background: var(--sev-info-bg); }
    button.filter-chip.on.sev-debug { color: var(--sev-debug); border-color: var(--sev-debug); background: var(--sev-debug-bg); }
    /* Compound button — icon + text label. Not the shared .icon-btn
       (icon-only). Kept local until enough compound buttons appear
       elsewhere to justify a shared variant. */
    button.icon-btn {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      cursor: pointer;
      font-family: inherit;
      font-size: var(--fs-sm);
    }
    button.icon-btn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    button.icon-btn:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    ul.rules {
      list-style: none;
      margin: 0;
      padding: 0;
    }
    ul.rules li {
      display: grid;
      grid-template-columns: 1fr auto;
      align-items: baseline;
      gap: var(--sp-2);
      padding: var(--sp-1) var(--sp-2);
      cursor: pointer;
      border-bottom: 1px solid color-mix(in oklab, var(--border) 60%, transparent);
    }
    ul.rules li:hover {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    ul.rules li.selected {
      background: color-mix(in oklab, var(--accent) 20%, transparent);
    }
    ul.rules .rule-label {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    /* Rule-row severity coloring. The row's leading glyph + label
       take the severity color so the sidebar reads as a heat-map
       instead of a wall of chip pills. Filter buttons above stay
       button-shaped so their affordance is unambiguous. */
    ul.rules li.sev-error .rule-label { color: var(--sev-error); }
    ul.rules li.sev-warn  .rule-label { color: var(--sev-warn); }
    ul.rules li.sev-info  .rule-label { color: var(--sev-info); }
    ul.rules li.sev-debug .rule-label { color: var(--sev-debug); }
    ul.rules .sev-glyph {
      font-weight: 700;
      margin-right: var(--sp-1);
    }
    ul.rules .count {
      color: var(--dim);
      font-variant-numeric: tabular-nums;
    }
    ul.issues {
      list-style: none;
      margin: 0;
      padding: 0;
    }
    ul.issues li {
      display: block;
      padding: var(--sp-2);
      border-bottom: 1px solid color-mix(in oklab, var(--border) 60%, transparent);
      cursor: pointer;
    }
    ul.issues li:hover:not(.orphan) {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    ul.issues li.orphan {
      cursor: default;
      opacity: 0.75;
    }
    /* Issue-row severity coloring. Matches the sidebar's
       glyph+colored-label approach — the record label carries the
       severity color so the row reads at a glance without pulling in
       a chip pill. */
    ul.issues li.sev-error .issue-record { color: var(--sev-error); }
    ul.issues li.sev-warn  .issue-record { color: var(--sev-warn); }
    ul.issues li.sev-info  .issue-record { color: var(--sev-info); }
    ul.issues li.sev-debug .issue-record { color: var(--sev-debug); }
    ul.issues .sev-glyph {
      font-weight: 700;
      margin-right: var(--sp-1);
    }
    .issue-record {
      font-weight: 600;
      color: var(--fg);
    }
    .issue-record.orphan-label {
      color: var(--dim);
      font-style: italic;
    }
    .issue-rule {
      color: var(--dim);
      font-size: var(--fs-sm);
      margin-top: var(--sp-1);
    }
    .issue-msg {
      margin-top: var(--sp-1);
      color: var(--fg);
      line-height: 1.35;
    }
    .pager {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-top: var(--sp-3);
      padding-top: var(--sp-2);
      border-top: 1px solid var(--border);
      color: var(--dim);
      font-size: var(--fs-sm);
    }
    .empty { color: var(--dim); padding: var(--sp-2); font-style: italic; }
    .error { color: var(--error); font-family: var(--font-mono); padding: var(--sp-2); }
    .diag-note {
      margin-top: var(--sp-2);
      color: var(--dim);
      font-size: var(--fs-sm);
      line-height: 1.35;
    }
  `;

  constructor() {
    super();
    this._summary = [];
    this._issues = [];
    this._total = 0;
    this._loading = false;
    this._reindexing = false;
    this._error = "";
    this._severities = new Set(SfgaIssues.DEFAULT_SEVERITIES);
    this._ruleFilter = "";
    this._tableFilter = "";
    this._offset = 0;
  }

  async connectedCallback() {
    super.connectedCallback();
    await this._refresh();
  }

  async _refresh() {
    this._loading = true;
    this._error = "";
    try {
      const [summary, page] = await Promise.all([
        api.issue.summary(),
        this._fetchPage(),
      ]);
      this._summary = summary.items || [];
      this._issues = page.items || [];
      this._total = page.total ?? this._issues.length;
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      this._loading = false;
    }
  }

  _fetchPage() {
    return api.issue.list({
      table: this._tableFilter || undefined,
      rule_id: this._ruleFilter || undefined,
      severity: [...this._severities],
      limit: SfgaIssues.PAGE_SIZE,
      offset: this._offset,
    });
  }

  async _reloadList() {
    try {
      const page = await this._fetchPage();
      this._issues = page.items || [];
      this._total = page.total ?? this._issues.length;
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    }
  }

  _toggleSeverity(sev) {
    const next = new Set(this._severities);
    if (next.has(sev)) next.delete(sev);
    else next.add(sev);
    this._severities = next;
    this._offset = 0;
    this._reloadList();
  }

  _selectRule(row) {
    // Clicking the currently-selected rule clears the filter, matching
    // the "same-key toggles" chip convention elsewhere in the app.
    const already =
      this._ruleFilter === row.rule_id && this._tableFilter === row.table;
    if (already) {
      this._ruleFilter = "";
      this._tableFilter = "";
    } else {
      this._ruleFilter = row.rule_id;
      this._tableFilter = row.table;
    }
    this._offset = 0;
    this._reloadList();
  }

  async _reindex() {
    if (this._reindexing) return;
    this._reindexing = true;
    try {
      await api.issue.reindex();
      this._offset = 0;
      await this._refresh();
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      this._reindexing = false;
    }
  }

  _pagePrev() {
    if (this._offset === 0) return;
    this._offset = Math.max(0, this._offset - SfgaIssues.PAGE_SIZE);
    this._reloadList();
  }

  _pageNext() {
    if (this._offset + SfgaIssues.PAGE_SIZE >= this._total) return;
    this._offset += SfgaIssues.PAGE_SIZE;
    this._reloadList();
  }

  // Sum the summary rows that pass the current severity filter, so
  // the rule list on the left shows counts that agree with the paged
  // result on the right (rather than the archive-wide totals).
  _filteredRuleRows() {
    return this._summary
      .filter((r) => this._severities.has(r.severity))
      .sort((a, b) => b.count - a.count);
  }

  // _openIssue asks the shell to navigate to the flagged record's
  // natural edit surface. Payload always carries the target table +
  // record_id so the shell can pre-select on the destination screen
  // (references, in particular, use record_id to focus the row).
  // Taxon-linked issues additionally carry a taxon_id so the shell
  // can reveal the row in the tree. Orphan rows (no owning taxon and
  // no known routing) are silently ignored — the row was already
  // rendered dim to hint that clicking won't do anything.
  _openIssue(issue) {
    if (!SfgaIssues._isRoutableTable(issue.table) && !issue.link_taxon_id) {
      return;
    }
    const detail = { table: issue.table, record_id: issue.record_id };
    if (issue.link_taxon_id) {
      detail.taxon_id = issue.link_taxon_id;
    }
    this.dispatchEvent(
      new CustomEvent("issue-navigate", {
        detail,
        bubbles: true,
        composed: true,
      }),
    );
  }

  // Tables the shell knows how to route to without a taxon hint.
  // Kept as a static set so the click-enable check in _openIssue
  // and the orphan-dim check in _renderIssueRow stay aligned.
  static ROUTABLE_TABLES = new Set([
    "metadata",
    "reference",
    "creator",
    "contact",
    "contributor",
    "editor",
    "publisher",
  ]);
  static _isRoutableTable(table) {
    return SfgaIssues.ROUTABLE_TABLES.has(table);
  }

  render() {
    if (this._loading && this._summary.length === 0 && this._issues.length === 0) {
      return html`<div class="empty" role="status">Loading…</div>`;
    }
    if (this._error) {
      return html`<div class="error" role="alert">${this._error}</div>`;
    }
    return html`
      ${this._renderSidebar()}
      ${this._renderList()}
    `;
  }

  _renderSidebar() {
    const rows = this._filteredRuleRows();
    const totalShown = rows.reduce((sum, r) => sum + r.count, 0);
    const anyDiag = this._severities.has("info") || this._severities.has("debug");
    return html`
      <aside>
        <h3>Filter by severity</h3>
        <div class="toolbar">
          ${["error", "warn", "info", "debug"].map((sev) => this._renderSevChip(sev))}
        </div>
        ${anyDiag
          ? html`<div class="diag-note">
              Diagnostic issues (info, debug) surface parser and validator hints
              that are usually not curator-fixable. Editing records to silence
              them can degrade data quality.
            </div>`
          : ""}
        <h3 style="margin-top:1rem;">
          Rules (${totalShown} issue${totalShown === 1 ? "" : "s"})
        </h3>
        ${rows.length === 0
          ? html`<div class="empty" role="status">No issues match the current filter.</div>`
          : html`<ul class="rules">
              ${rows.map((r) => this._renderRuleRow(r))}
            </ul>`}
      </aside>
    `;
  }

  _renderSevChip(sev) {
    const on = this._severities.has(sev);
    const meta = SEV_META[sev] || SEV_META.warn;
    return html`
      <button
        class="filter-chip ${on ? "on" : ""} sev-${sev}"
        @click=${() => this._toggleSeverity(sev)}
        title="toggle ${sev} issues"
      >
        <span>${meta.glyph}</span>${meta.label}
      </button>
    `;
  }

  _renderRuleRow(r) {
    const selected =
      this._ruleFilter === r.rule_id && this._tableFilter === r.table;
    const sev = (r.severity || "warn").toLowerCase();
    const classes = ["sev-" + sev];
    if (selected) classes.push("selected");
    // No per-row glyph: every severity now uses the same triangle
    // icon (see SEV_META), so a leading glyph adds nothing beyond
    // what the row's severity color already conveys — and it steals
    // characters from the rule name in a narrow sidebar.
    return html`
      <li
        class=${classes.join(" ")}
        @click=${() => this._selectRule(r)}
        title="${r.rule_id} (${r.table})"
      >
        <span class="rule-label">${r.rule_name || r.rule_id}</span>
        <span class="count">${r.count}</span>
      </li>
    `;
  }

  _renderList() {
    const from = this._issues.length === 0 ? 0 : this._offset + 1;
    const to = this._offset + this._issues.length;
    const canPrev = this._offset > 0;
    const canNext = this._offset + this._issues.length < this._total;
    return html`
      <section>
        <div class="toolbar">
          <div class="grow">
            <strong>${this._ruleFilter
              ? this._summary.find((r) => r.rule_id === this._ruleFilter)?.rule_name || this._ruleFilter
              : "All rules"}</strong>
            <span style="color:var(--dim); margin-left:0.5rem;">
              ${this._total > 0 ? `${from}-${to} of ${this._total}` : "No issues"}
            </span>
          </div>
          <button
            class="icon-btn"
            @click=${() => this._reindex()}
            ?disabled=${this._reindexing}
            title="re-run every rule and rewrite the issue cache"
          >
            ${renderIcon("refresh-cw", 14)}
            ${this._reindexing ? "recomputing…" : "Recompute"}
          </button>
        </div>
        ${this._issues.length === 0
          ? html`<div class="empty" role="status">No issues match the current filter.</div>`
          : html`<ul class="issues">
              ${this._issues.map((i) => this._renderIssueRow(i))}
            </ul>`}
        ${this._total > SfgaIssues.PAGE_SIZE
          ? html`<div class="pager">
              <button
                class="icon-btn"
                @click=${() => this._pagePrev()}
                ?disabled=${!canPrev}
              >
                ← Previous
              </button>
              <span>${from}-${to} of ${this._total}</span>
              <button
                class="icon-btn"
                @click=${() => this._pageNext()}
                ?disabled=${!canNext}
              >
                Next →
              </button>
            </div>`
          : ""}
      </section>
    `;
  }

  _renderIssueRow(i) {
    const orphan = !i.link_taxon_id && !SfgaIssues._isRoutableTable(i.table);
    const label = i.record_label || `(record ${i.record_id.slice(0, 8)}…)`;
    const sev = (i.severity || "warn").toLowerCase();
    const classes = ["sev-" + sev];
    if (orphan) classes.push("orphan");
    // Same rationale as _renderRuleRow — glyph dropped because color
    // already conveys severity and the uniform triangle adds nothing.
    return html`
      <li
        class=${classes.join(" ")}
        @click=${() => this._openIssue(i)}
        title=${orphan ? "cannot navigate to this record type yet" : "open this record"}
      >
        <div class="issue-record ${orphan ? "orphan-label" : ""}">${label}</div>
        <div class="issue-rule">
          ${i.rule_name || i.rule_id}${i.field_name ? ` · ${i.field_name}` : ""}
        </div>
        <div class="issue-msg">${i.message}</div>
      </li>
    `;
  }
}

// ---------- <sfga-agent-card> ----------

// One card in an agent list. Rendered inside <sfga-agent-section>;
// dispatches `agent-edit` when clicked so the section can open the
// modal. Presentation-only — no fetches, no writes.
//
// Layout mirrors ChecklistBank's AgentPresentation: family+given
// underlined, ORCID line (icon + id, linked), organisation,
// ROR line (icon + id, linked), department, city/state/country,
// email as mailto, url, italic note.
class SfgaAgentCard extends LitElement {
  static properties = {
    agent: { attribute: false },
    // Highest severity across issues attached to this agent row.
    // Optional; when set, a small glyph anchors the card in the
    // upper-right corner so curators can spot problem rows without
    // opening each one.
    issueSeverity: { attribute: false },
  };

  static styles = css`
    /* Host is a grid cell (see SfgaAgentSection .cards). Grid stretch
       is on by default so the host fills the row height; the .card
       inside also stretches to fill the host so its border and hit
       area extend to the bottom of the row even for cards with less
       content. */
    :host {
      display: block;
      height: 100%;
    }
    .card {
      position: relative;
      display: flex;
      flex-direction: column;
      gap: 0.05rem;
      padding: var(--sp-1) var(--sp-2);
      width: 100%;
      height: 100%;
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      background: color-mix(in oklab, var(--bg) 94%, var(--fg));
      cursor: pointer;
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
      line-height: 1.15;
      color: var(--fg);
      text-align: left;
      /* Clip long content — each field ellipsizes on its own so
         the card stays uniform-width and lines up with siblings in
         the grid. Hovering the card shows the tooltip attribute
         for anything the curator can't fully read. */
      overflow: hidden;
      min-width: 0;
    }
    .card:hover {
      border-color: var(--accent);
    }
    /* Ellipsize any per-line block so the card never grows beyond
       its grid track. Applied broadly to keep the card CSS tight;
       specific rows override where wrapping is desired (note). */
    .card > * {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      min-width: 0;
    }
    .name {
      font-family: var(--font-body);
      font-weight: 600;
      text-decoration: underline;
    }
    .line {
      display: flex;
      align-items: center;
      gap: var(--sp-1);
    }
    /* Badge icons sit next to the ORCID / ROR id text; sizing them
       em-relative keeps them proportional to the card's text height
       instead of dominating the row at a fixed 14/16px. */
    .line img {
      flex: 0 0 auto;
      width: 1em;
      height: 1em;
    }
    .line img.ror {
      width: 1.15em;
      height: 1.15em;
    }
    .dim { color: var(--dim); }
    .note {
      font-family: var(--font-body);
      font-style: italic;
      color: var(--dim);
      margin-top: var(--sp-1);
      /* Notes are the one field allowed to wrap so the curator sees
         the full contribution note in situ; other fields ellipsize
         to preserve the card grid. */
      white-space: normal;
    }
    .sev-badge {
      position: absolute;
      top: var(--sp-1);
      right: var(--sp-1);
      font-size: var(--fs-sm);
      font-weight: 700;
    }
    .sev-badge.sev-error { color: var(--sev-error); }
    .sev-badge.sev-warn  { color: var(--sev-warn); }
    .sev-badge.sev-info  { color: var(--sev-info); }
    .sev-badge.sev-debug { color: var(--sev-debug); }
  `;

  _onClick() {
    this.dispatchEvent(
      new CustomEvent("agent-edit", {
        detail: { agent: this.agent },
        bubbles: true,
        composed: true,
      }),
    );
  }

  render() {
    const a = this.agent || {};
    const parts = [a.family, a.given].filter(Boolean).join(", ");
    const locale = [a.city, a.state, a.country].filter(Boolean).join(", ");
    const sev = (this.issueSeverity || "").toLowerCase();
    const glyph = SEV_META[sev]?.glyph || "";
    return html`
      <button class="card" type="button" @click=${() => this._onClick()}>
        ${sev
          ? html`<span
              class="sev-badge sev-${sev}"
              title="${sev}: this record has open issues"
              aria-label="severity ${sev}"
              >${glyph}</span
            >`
          : ""}
        ${parts ? html`<span class="name">${parts}</span>` : ""}
        ${a.orcid
          ? html`<span class="line"
              ><img src="/vendor/logos/orcid.png" alt="ORCID" />${a.orcid}</span
            >`
          : ""}
        ${a.organisation ? html`<span>${a.organisation}</span>` : ""}
        ${a.rorid
          ? html`<span class="line"
              ><img class="ror" src="/vendor/logos/ror.png" alt="ROR" />${a.rorid}</span
            >`
          : ""}
        ${a.department ? html`<span>${a.department}</span>` : ""}
        ${locale ? html`<span class="dim">${locale}</span>` : ""}
        ${a.email ? html`<span class="dim">${a.email}</span>` : ""}
        ${a.url ? html`<span class="dim">${a.url}</span>` : ""}
        ${a.note ? html`<span class="note">${a.note}</span>` : ""}
      </button>
    `;
  }
}

// ---------- <sfga-agent-modal> ----------

// Modal form for creating or editing one agent. Two entry modes:
//   - create: agent is null; sets defaults, POSTs on save.
//   - edit:   agent is a full apiAgent; PATCHes on save.
//
// In edit mode also offers a quick-copy affordance: pick another
// role from the dropdown and click "Copy to <role>" to duplicate
// this agent into that table (blanking the role/contribution
// note by default). The source row is left in place — copy is
// not move.
//
// Events:
//   agent-saved   { agent }   — saved (create or edit); parent reloads.
//   agent-deleted { id, role} — delete confirmed; parent removes card.
//   close                     — modal closes with no change.
class SfgaAgentModal extends LitElement {
  static properties = {
    // The role of the source row (in edit mode) or the target role
    // (in create mode). Never changes during a modal session.
    role: { attribute: false },
    // Existing agent for edit; null/undefined for create.
    agent: { attribute: false },
    _draft: { state: true },
    _saving: { state: true },
    _error: { state: true },
    _copyTo: { state: true },
    // Open validation issues for the row being edited. Rendered at
    // the top of the modal so curators see what needs fixing without
    // going back to the Issues screen. Empty on create-mode and on
    // rows the validator has never flagged.
    _issues: { state: true },
  };

  static styles = [
    formFieldStyles,
    buttonStyles,
    css`
      .backdrop {
        position: fixed;
        inset: 0;
        background: color-mix(in oklab, var(--fg) 18%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
      }
      /* Nested-modal hide — see openModal / isTopModal helpers. */
      .backdrop.is-covered {
        visibility: hidden;
      }
      .modal {
        background: var(--bg);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-4) var(--sp-5);
        width: min(var(--modal-lg), 95vw);
        max-height: var(--modal-max-h);
        overflow: auto;
        display: grid;
        gap: var(--sp-2);
        font-family: var(--font-body);
        color: var(--fg);
      }
      h3 {
        margin: 0;
        font-size: var(--fs-lg);
      }
      /* Modal header hosts the title and the close (×) button.
         Backdrop clicks are intentionally not wired to close — a
         misclick shouldn't wipe out an in-progress edit. Curators
         dismiss via X, Cancel, or Esc, all of which prompt when the
         form is dirty. */
      .modal-header {
        display: flex;
        justify-content: space-between;
        align-items: center;
        gap: var(--sp-2);
      }
      form {
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: var(--sp-2) var(--sp-3);
        align-items: center;
      }
      /* Role/contribution note is usually a phrase, not a paragraph —
         override the shared 4rem textarea min-height for a tighter fit. */
      form textarea {
        min-height: 3rem;
      }
      .toolbar {
        display: flex;
        justify-content: space-between;
        gap: var(--sp-2);
        margin-top: var(--sp-2);
        border-top: 1px solid var(--border);
        padding-top: var(--sp-2);
      }
      .copy-row {
        display: flex;
        gap: var(--sp-1);
        align-items: center;
        margin-top: var(--sp-1);
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      .error {
        color: var(--error);
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
        margin-top: var(--sp-1);
      }
      .hint {
        grid-column: 2 / -1;
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      /* Open-issues banner. Matches the taxon detail's warning-banner
         so curators recognize the pattern across screens. Each row
         carries its own severity color via the shared sev-* variables;
         glyph makes severity legible under color loss. */
      .warning-banner {
        border: 1px solid var(--border);
        background: color-mix(in oklab, var(--fg) 4%, var(--bg));
        color: var(--fg);
        padding: var(--sp-2) var(--sp-3);
        border-radius: var(--radius-sm);
        font-size: var(--fs-sm);
      }
      .warning-banner ul {
        list-style: none;
        margin: var(--sp-1) 0 0 0;
        padding: 0;
      }
      .warning-banner li {
        margin: var(--sp-1) 0;
        display: grid;
        grid-template-columns: auto 1fr;
        gap: var(--sp-2);
        align-items: baseline;
      }
      .warning-banner .warning-rule {
        font-weight: 600;
        color: var(--fg);
      }
      .warning-banner .sev-glyph {
        font-weight: 700;
      }
      .warning-banner .sev-error {
        color: var(--sev-error);
      }
      .warning-banner .sev-warn {
        color: var(--sev-warn);
      }
      .warning-banner .sev-info {
        color: var(--sev-info);
      }
      .warning-banner .sev-debug {
        color: var(--sev-debug);
      }
    `,
  ];

  constructor() {
    super();
    this.role = "creator";
    this.agent = null;
    this._draft = {};
    this._saving = false;
    this._error = "";
    this._copyTo = "";
    this._issues = [];
  }

  async connectedCallback() {
    super.connectedCallback();
    // Seed the draft with the existing row so uncontrolled inputs
    // (labeled fields) display the current values. On create, the
    // draft stays empty and everything renders blank.
    this._draft = { ...(this.agent || {}) };
    // Default copy-target: first role that isn't the source role.
    this._copyTo = SfgaAgentModal._otherRoles(this.role)[0] || "";
    // Escape closes the modal via the same guarded path as Cancel
    // and the X button. Registered at document level so it fires
    // regardless of which element in the shadow DOM has focus.
    this._onDocKey = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        this._requestClose();
      }
    };
    document.addEventListener("keydown", this._onDocKey);
    // Fetch open validation issues for this specific row so the
    // curator sees them without leaving the modal. Best-effort —
    // a fetch failure just skips the banner.
    if (this.agent && this.agent.id) {
      try {
        const page = await api.issue.list({
          table: this.role,
          limit: 500,
        });
        const idStr = String(this.agent.id);
        this._issues = (page.items || []).filter(
          (i) => i.record_id === idStr,
        );
      } catch (_) {
        this._issues = [];
      }
    }
    this._modalStackID = openModal();
    this._unsubModalStack = subscribeModalStack(() => this.requestUpdate());
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    if (this._onDocKey) {
      document.removeEventListener("keydown", this._onDocKey);
      this._onDocKey = null;
    }
    if (this._releaseFocus) {
      this._releaseFocus();
      this._releaseFocus = null;
    }
    if (this._unsubModalStack) {
      this._unsubModalStack();
      this._unsubModalStack = null;
    }
    if (this._modalStackID) {
      closeModal(this._modalStackID);
      this._modalStackID = null;
    }
  }

  firstUpdated() {
    // Trap focus inside the edit form and land the caret in the first
    // input so keyboard curators can start typing immediately. Release
    // in disconnectedCallback restores focus to the card / add tile
    // that opened the modal.
    this._releaseFocus = trapFocus(this.renderRoot, {
      initialFocus: "input, textarea, select",
    });
  }

  static _otherRoles(role) {
    return ["creator", "contact", "editor", "contributor", "publisher"]
      .filter((r) => r !== role);
  }

  _isEdit() {
    return this.agent && this.agent.id;
  }

  _change(field, value) {
    this._draft = { ...this._draft, [field]: value };
  }

  _fieldValue(field) {
    if (Object.hasOwn(this._draft, field)) return this._draft[field] ?? "";
    return "";
  }

  _close() {
    this.dispatchEvent(
      new CustomEvent("close", { bubbles: true, composed: true }),
    );
  }

  // _isDirty compares the draft against the loaded row. Used to
  // gate the close-with-unsaved-changes prompt. Create mode is
  // "dirty" whenever any field has been typed into.
  _isDirty() {
    if (!this.agent) {
      return Object.values(this._draft).some((v) => (v || "") !== "");
    }
    for (const [k, v] of Object.entries(this._draft)) {
      if (k === "id" || k === "role") continue;
      if ((this.agent[k] || "") !== (v || "")) return true;
    }
    return false;
  }

  // _requestClose is the user-facing close path for Cancel / X /
  // Esc — asks for confirmation when the form has unsaved edits so
  // a stray click doesn't discard curator work. Backdrop clicks are
  // intentionally NOT wired here; only explicit close affordances
  // (Cancel, X, Esc) can dismiss the modal.
  async _requestClose() {
    if (this._isDirty()) {
      const choice = await confirmDirty({
        heading: "Unsaved agent edits",
        message:
          "You have unsaved changes. Save them, discard them, or keep editing?",
        canSave: true,
      });
      if (choice === "cancel") return;
      if (choice === "save") {
        // _save closes on success; on failure it surfaces _error and
        // leaves the modal open so the curator can fix and retry.
        await this._save();
        return;
      }
      // discard → fall through to close
    }
    this._close();
  }

  async _save() {
    this._saving = true;
    this._error = "";
    try {
      let saved;
      if (this._isEdit()) {
        // Patch only fields whose value differs from the loaded row.
        const patch = {};
        for (const [k, v] of Object.entries(this._draft)) {
          if (k === "id" || k === "role") continue;
          if ((this.agent[k] || "") !== (v || "")) patch[k] = v || "";
        }
        if (Object.keys(patch).length === 0) {
          this._close();
          return;
        }
        saved = await api.agent.patch(this.role, this.agent.id, patch);
      } else {
        const body = { ...this._draft };
        delete body.id;
        delete body.role;
        saved = await api.agent.create(this.role, body);
      }
      this.dispatchEvent(
        new CustomEvent("agent-saved", {
          detail: { agent: saved },
          bubbles: true,
          composed: true,
        }),
      );
      this._close();
    } catch (err) {
      this._error =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
    } finally {
      this._saving = false;
    }
  }

  async _delete() {
    if (!this._isEdit()) return;
    const ok = await confirmAction({
      heading: `Delete this ${this.role}?`,
      message: "This cannot be undone.",
      actionLabel: "Delete",
    });
    if (!ok) return;
    this._saving = true;
    this._error = "";
    try {
      await api.agent.delete(this.role, this.agent.id);
      this.dispatchEvent(
        new CustomEvent("agent-deleted", {
          detail: { id: this.agent.id, role: this.role },
          bubbles: true,
          composed: true,
        }),
      );
      this._close();
    } catch (err) {
      this._error =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
      this._saving = false;
    }
  }

  async _copy() {
    return this._copyOrMove(false);
  }

  async _move() {
    if (!this._isEdit() || !this._copyTo) return;
    // Move deletes the source row — cheap-to-undo it isn't, so
    // confirm before the round-trip. Skip the extra prompt for
    // copy since that leaves the source intact.
    const ok = await confirmAction({
      heading: `Move ${this.role} to ${this._copyTo}?`,
      message: `The ${this.role} row will be deleted and re-created as a ${this._copyTo}.`,
      actionLabel: `Move to ${this._copyTo}`,
      actionVariant: "primary",
    });
    if (!ok) return;
    return this._copyOrMove(true);
  }

  async _copyOrMove(move) {
    if (!this._isEdit() || !this._copyTo) return;
    this._saving = true;
    this._error = "";
    try {
      // blank_note defaults to true; the role/contribution note is
      // role-specific in practice ("primary curator" for a creator
      // doesn't survive a copy to publisher) so the curator writes
      // a fresh one on the target after the copy or move.
      const fn = move ? api.agent.move : api.agent.copy;
      const created = await fn(
        this.role,
        this.agent.id,
        this._copyTo,
        true,
      );
      // Move deletes the source — signal deletion too so the source
      // section drops the row without a second round-trip.
      if (move) {
        this.dispatchEvent(
          new CustomEvent("agent-deleted", {
            detail: { id: this.agent.id, role: this.role },
            bubbles: true,
            composed: true,
          }),
        );
      }
      this.dispatchEvent(
        new CustomEvent("agent-saved", {
          detail: { agent: created },
          bubbles: true,
          composed: true,
        }),
      );
      this._close();
    } catch (err) {
      this._error =
        err instanceof Problem ? `${err.title}: ${err.detail || err.message}` : String(err);
      this._saving = false;
    }
  }

  // _renderIssuesBanner shows the open gsvalidator issues for the
  // row being edited. Same visual pattern as SfgaDetail's warning
  // banner so curators recognize it across screens. Hidden in
  // create-mode (no row exists yet) and when the fetch returns
  // nothing.
  _renderIssuesBanner() {
    if (!this._issues || this._issues.length === 0) return "";
    const heading = `${this._issues.length} open issue${this._issues.length > 1 ? "s" : ""}:`;
    return html`
      <div class="warning-banner">
        <strong>${heading}</strong>
        <ul>
          ${this._issues.map((w) => {
            const sev = (w.severity || "warn").toLowerCase();
            const glyph = (SEV_META[sev] || SEV_META.warn).glyph;
            return html`<li>
              <span class="sev-glyph sev-${sev}" aria-label="severity ${sev}"
                >${glyph}</span
              >
              <span>
                <span class="warning-rule">${w.rule_name || w.rule_id}</span>${w.field_name
                  ? html` · <span class="warning-rule">${w.field_name}</span>`
                  : ""}:
                ${w.message}
              </span>
            </li>`;
          })}
        </ul>
      </div>
    `;
  }

  render() {
    const editing = this._isEdit();
    const isPublisher = this.role === "publisher";
    const isContact = this.role === "contact";
    const covered =
      this._modalStackID && !isTopModal(this._modalStackID)
        ? " is-covered"
        : "";
    return html`
      <div class=${"backdrop" + covered}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="agent-modal-title"
        >
          <div class="modal-header">
            <h3 id="agent-modal-title">
              ${editing ? "Edit" : "Add"}
              ${this.role.charAt(0).toUpperCase() + this.role.slice(1)}
            </h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._requestClose()}
              title="close"
              aria-label="close"
            >
              ×
            </button>
          </div>
          ${this._renderIssuesBanner()}
          <form @submit=${(e) => e.preventDefault()}>
            <label class=${isPublisher ? "" : "req"}>Given name</label>
            <input
              type="text"
              .value=${this._fieldValue("given")}
              @input=${(e) => this._change("given", e.target.value)}
            />
            <label class=${isPublisher ? "" : "req"}>Family name</label>
            <input
              type="text"
              .value=${this._fieldValue("family")}
              @input=${(e) => this._change("family", e.target.value)}
            />
            <label>ORCID iD</label>
            <input
              type="text"
              placeholder="0000-0000-0000-0000"
              .value=${this._fieldValue("orcid")}
              @input=${(e) => this._change("orcid", e.target.value)}
            />
            <label>Organisation</label>
            <input
              type="text"
              .value=${this._fieldValue("organisation")}
              @input=${(e) => this._change("organisation", e.target.value)}
            />
            <label>ROR ID</label>
            <input
              type="text"
              placeholder="e.g. 05dxps055"
              .value=${this._fieldValue("rorid")}
              @input=${(e) => this._change("rorid", e.target.value)}
            />
            <label>Department</label>
            <input
              type="text"
              .value=${this._fieldValue("department")}
              @input=${(e) => this._change("department", e.target.value)}
            />
            <label>City</label>
            <input
              type="text"
              .value=${this._fieldValue("city")}
              @input=${(e) => this._change("city", e.target.value)}
            />
            <label>State/Region</label>
            <input
              type="text"
              .value=${this._fieldValue("state")}
              @input=${(e) => this._change("state", e.target.value)}
            />
            <label>Country</label>
            <input
              type="text"
              placeholder="ISO alpha-2 (e.g. US)"
              .value=${this._fieldValue("country")}
              @input=${(e) => this._change("country", e.target.value)}
            />
            <label class=${isContact ? "req" : ""}>Email</label>
            <input
              type="email"
              .value=${this._fieldValue("email")}
              @input=${(e) => this._change("email", e.target.value)}
            />
            <label>URL</label>
            <input
              type="url"
              .value=${this._fieldValue("url")}
              @input=${(e) => this._change("url", e.target.value)}
            />
            <label>Role / contribution note</label>
            <textarea
              .value=${this._fieldValue("note")}
              placeholder="What is this person's role or contribution?"
              @input=${(e) => this._change("note", e.target.value)}
            ></textarea>
            ${editing
              ? html`<div class="hint">
                    Copy adds a duplicate under the target role;
                    Move relocates this row and deletes the source.
                  </div>
                  <div class="copy-row" style="grid-column: 1 / -1;">
                    <select
                      .value=${this._copyTo}
                      @change=${(e) => (this._copyTo = e.target.value)}
                    >
                      ${SfgaAgentModal._otherRoles(this.role).map(
                        (r) => html`<option value=${r}>${r}</option>`,
                      )}
                    </select>
                    <button
                      type="button"
                      @click=${() => this._copy()}
                      ?disabled=${this._saving || !this._copyTo}
                      title="creates a new agent in the chosen role; leaves this row in place"
                    >
                      Copy to ${this._copyTo}
                    </button>
                    <button
                      type="button"
                      @click=${() => this._move()}
                      ?disabled=${this._saving || !this._copyTo}
                      title="reassigns this agent to the chosen role; deletes the source row"
                    >
                      Move to ${this._copyTo}
                    </button>
                  </div>`
              : ""}
          </form>
          ${this._error
            ? html`<div class="error" role="alert">${this._error}</div>`
            : ""}
          <div class="toolbar">
            <div>
              ${editing
                ? html`<button
                    class="danger"
                    type="button"
                    @click=${() => this._delete()}
                    ?disabled=${this._saving}
                  >
                    Delete
                  </button>`
                : ""}
            </div>
            <div style="display:flex; gap:0.5rem;">
              <button type="button" @click=${() => this._requestClose()}>
                Cancel
              </button>
              <button
                class="primary"
                type="button"
                @click=${() => this._save()}
                ?disabled=${this._saving}
              >
                ${this._saving ? "Saving…" : editing ? "Save" : "Add"}
              </button>
            </div>
          </div>
        </div>
      </div>
    `;
  }
}

// ---------- <sfga-agent-section> ----------

// One section (Creators, Contacts, …) inside SfgaMetadata's view.
// Owns fetch, modal orchestration, and card rendering for its
// role. Renders read-only for anyone (public archives can browse
// dataset metadata), edit affordances when .editable is true.
class SfgaAgentSection extends LitElement {
  static properties = {
    role: { attribute: false },
    editable: { type: Boolean, attribute: false },
    // When true, skip the section's own <h3> title. Callers that
    // embed the section inside another labelled grid (dl row,
    // form) supply their own label and don't want a duplicate.
    noheader: { type: Boolean, attribute: false },
    // When true, suppress the "+ Add" tile. Used in the metadata
    // read view where adding is gated on entering edit mode —
    // cards remain clickable so existing agents can still be
    // opened and edited from either mode.
    noAdd: { type: Boolean, attribute: false },
    _agents: { state: true },
    _issuesByID: { state: true }, // agent id → highest severity
    _loading: { state: true },
    _error: { state: true },
    _modalAgent: { state: true }, // agent for edit modal; null = create; false = closed
  };

  static styles = css`
    :host {
      display: block;
    }
    :host(.spaced) {
      margin-top: 1.2rem;
    }
    h3 {
      margin: 0 0 var(--sp-1) 0;
      font-size: var(--fs-md);
      font-family: var(--font-body);
      color: var(--fg);
    }
    .empty {
      color: var(--dim);
      font-style: italic;
      font-size: var(--fs-sm);
    }
    /* Card grid: fixed-width columns sized to fit an ORCID iD
       (16-char logo + 19 chars of monospace ≈ 15rem). auto-fill
       packs as many cards per row as the value column has room
       for and wraps the rest. Grid items use the default stretch
       alignment so every card in a row matches the tallest card's
       height — content stays top-aligned inside each card (the
       card is a flex column with default flex-start) but the
       card's border and hit area extend to the row height for
       visual consistency. The Add tile shares the same track
       width so it drops into the flow as another card. */
    .cards {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr));
      gap: var(--sp-1);
    }
    .add-btn {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 100%;
      min-height: 100%;
      background: transparent;
      color: var(--dim);
      border: 1px dashed var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-2);
      cursor: pointer;
      font: inherit;
      font-size: var(--fs-sm);
    }
    .add-btn:hover { border-color: var(--accent); color: var(--accent); }
  `;

  constructor() {
    super();
    this.role = "creator";
    this.editable = false;
    this.noheader = false;
    this.noAdd = false;
    this._agents = [];
    this._issuesByID = new Map();
    this._loading = false;
    this._error = "";
    this._modalAgent = false;
  }

  async connectedCallback() {
    super.connectedCallback();
    await this._reload();
  }

  async _reload() {
    this._loading = true;
    this._error = "";
    try {
      const page = await api.agent.list(this.role);
      this._agents = page.items || [];
      // Fetch issues for this role and index by record_id → highest
      // severity. Best-effort — a validation-cache miss just leaves
      // the badge off; the card remains clickable.
      try {
        const issues = await api.issue.list({
          table: this.role,
          limit: 500,
        });
        const byID = new Map();
        for (const iss of issues.items || []) {
          const cur = byID.get(iss.record_id);
          if (!cur || SfgaAgentSection._sevRank(iss.severity) > SfgaAgentSection._sevRank(cur)) {
            byID.set(iss.record_id, iss.severity);
          }
        }
        this._issuesByID = byID;
      } catch (_) {
        this._issuesByID = new Map();
      }
    } catch (err) {
      this._error =
        err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      this._loading = false;
    }
  }

  static _sevRank(sev) {
    switch ((sev || "").toLowerCase()) {
      case "error": return 4;
      case "warn":  return 3;
      case "info":  return 2;
      case "debug": return 1;
      default:      return 0;
    }
  }

  _title() {
    // Plural section headers (Creators, Contacts, …). Simple + s;
    // sfga role table names all pluralize with "s".
    return this.role.charAt(0).toUpperCase() + this.role.slice(1) + "s";
  }

  _onCardClick(e) {
    if (!this.editable) return;
    this._modalAgent = e.detail.agent;
  }

  _openCreate() {
    this._modalAgent = null; // null → create-mode
  }

  _onModalClose() {
    this._modalAgent = false;
  }

  async _onAgentSaved() {
    // Reload the list — a create may add, an edit may change name/
    // sort order, a copy may drop a new agent in this section (from
    // another). Either way a full refresh is the safest cheap path.
    await this._reload();
  }

  async _onAgentDeleted() {
    await this._reload();
  }

  render() {
    const header = this.noheader ? "" : html`<h3>${this._title()}</h3>`;
    if (this._loading && this._agents.length === 0) {
      return html`${header}<div class="empty">loading…</div>`;
    }
    if (this._error) {
      return html`${header}<div class="empty">error: ${this._error}</div>`;
    }
    return html`
      ${header}
      <div class="cards">
        ${this._agents.length === 0
          ? html`<span class="empty">
              ${this.noheader ? "(none)" : `No ${this.role}s.`}
            </span>`
          : this._agents.map(
              (a) => html`<sfga-agent-card
                .agent=${a}
                .issueSeverity=${this._issuesByID.get(String(a.id)) || ""}
                @agent-edit=${(e) => this._onCardClick(e)}
              ></sfga-agent-card>`,
            )}
        ${this.editable && !this.noAdd
          ? html`<button
              class="add-btn"
              type="button"
              @click=${() => this._openCreate()}
              title="add ${this.role}"
            >
              + Add
            </button>`
          : ""}
      </div>
      ${this._modalAgent !== false
        ? html`<sfga-agent-modal
            .role=${this.role}
            .agent=${this._modalAgent}
            @close=${() => this._onModalClose()}
            @agent-saved=${(e) => this._onAgentSaved(e)}
            @agent-deleted=${(e) => this._onAgentDeleted(e)}
          ></sfga-agent-modal>`
        : ""}
    `;
  }
}

// ---------- vocab-editor adapters ----------
// Per-vocab config for the vocab editor. Vocabs with no entry here
// aren't editable — the picker's "Edit vocabulary…" action stays
// disabled. Add an entry when a vocab is safe to let curators
// extend (background: for the core taxonomic-model vocabs like
// taxonomic_status, rank, nom_code we deliberately don't let
// curators customize because that hurts interoperability with CLB
// and other researchers; see conversation history / DEFERRED).
const VOCAB_EDITOR_ADAPTERS = {
  species_interaction_type: {
    label: "species interaction types",
    // Adapter surfaces the rich columns sfga already models for this
    // vocab: description, obo (ontology URI, typically RO), inverse
    // (which is itself a term in this vocab), symmetrical bool,
    // superTypes (parent-term ids).
    editable: true,
    hasDescription: true,
    hasObo: true,
    hasInverse: true,
    hasSymmetrical: true,
    hasSuperTypes: true,
    // Documentation hint for the OBO field — nudges curators to use
    // canonical RO PURLs (the shipped vocab is 100% RO-covered).
    oboHint:
      "Ontology URI (recommended: Relations Ontology PURL, e.g. http://purl.obolibrary.org/obo/RO_0002442)",
  },
};

// vocabEditorAdapter returns the adapter for a vocab name, or null
// if the vocab isn't editable. Callers use the null return to gate
// the picker's "Edit vocabulary…" affordance.
function vocabEditorAdapter(name) {
  return VOCAB_EDITOR_ADAPTERS[name] || null;
}

// ---------- <sfga-vocab-editor> ----------
//
// Modal editor for controlled vocabularies. Renders a table of the
// vocab's current terms with edit + delete row actions, plus an
// "add term" flow. Rich fields (description, obo, inverse,
// symmetrical, super_types) are gated by the per-vocab adapter so
// the same component covers every editable vocab as more get schema
// support.
//
// Wiring: SfgaDetail owns a `_vocabEditor` state. Setting it to a
// vocab name mounts sfga-vocab-editor; a close event unmounts.
// Registers with the shared modal stack so nested modals hide it
// via .is-covered (see openModal / isTopModal helpers).
class SfgaVocabEditor extends LitElement {
  static properties = {
    // vocab is the sfga table name (e.g. "species_interaction_type").
    vocab: { type: String },
    _terms: { state: true },
    // _editingTerm is null when the list view is showing, an object
    // {mode: "create"|"edit", draft: {...}} when the term form is
    // open. Sub-view inside the same modal — no second nested modal.
    _editingTerm: { state: true },
    _loading: { state: true },
    _error: { state: true },
  };

  static styles = [
    buttonStyles,
    css`
      :host {
        display: block;
      }
      .backdrop {
        position: fixed;
        inset: 0;
        background: color-mix(in oklab, var(--fg) 18%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
      }
      .backdrop.is-covered {
        visibility: hidden;
      }
      .modal {
        background: var(--bg);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-4) var(--sp-5);
        width: min(var(--modal-lg), 95vw);
        max-height: var(--modal-max-h);
        overflow: auto;
        display: grid;
        gap: var(--sp-2);
        color: var(--fg);
      }
      .modal-header {
        display: flex;
        align-items: baseline;
        justify-content: space-between;
        gap: var(--sp-2);
      }
      .modal-header h3 {
        margin: 0;
        font-family: var(--font-body);
        font-size: var(--fs-lg);
      }
      table {
        width: 100%;
        border-collapse: collapse;
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
      }
      th {
        text-align: left;
        color: var(--dim);
        font-weight: normal;
        padding: var(--sp-1) var(--sp-2);
        border-bottom: 1px solid var(--border);
      }
      td {
        padding: var(--sp-1) var(--sp-2);
        vertical-align: baseline;
      }
      td.desc,
      td.obo {
        color: var(--dim);
        font-size: var(--fs-xs);
        overflow-wrap: break-word;
        max-width: 20em;
      }
      td.actions {
        text-align: right;
        white-space: nowrap;
      }
      /* Standard row-actions pattern (hide-until-hover) — matches
         section.vernaculars / section.distributions rows. */
      td.actions .row-actions {
        display: inline-flex;
        gap: 0;
        visibility: hidden;
      }
      tr:hover td.actions .row-actions,
      tr:focus-within td.actions .row-actions {
        visibility: visible;
      }
      .toolbar {
        display: flex;
        gap: var(--sp-2);
        justify-content: space-between;
        align-items: center;
        margin-top: var(--sp-2);
      }
      .toolbar .right {
        display: flex;
        gap: var(--sp-2);
      }
      .term-form {
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: var(--sp-2);
        align-items: baseline;
      }
      .term-form label {
        color: var(--dim);
        text-align: right;
      }
      .term-form .toolbar {
        grid-column: 1 / -1;
        justify-content: flex-end;
      }
      .term-form input[type="text"],
      .term-form textarea {
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
        padding: 0.3rem 0.4rem;
        background: var(--bg);
        color: var(--fg);
        border: 1px solid var(--border);
        border-radius: var(--radius-sm);
      }
      .term-form textarea {
        min-height: 3rem;
      }
      .term-form .req {
        color: var(--error);
      }
      .term-form .hint {
        color: var(--dim);
        font-size: var(--fs-xs);
        grid-column: 2;
        margin-top: -0.3rem;
      }
      .error {
        color: var(--error);
        font-family: var(--font-mono);
        font-size: var(--fs-sm);
      }
      .empty {
        color: var(--dim);
        font-style: italic;
        padding: var(--sp-3);
        text-align: center;
      }
    `,
  ];

  constructor() {
    super();
    this.vocab = "";
    this._terms = [];
    this._editingTerm = null;
    this._loading = false;
    this._error = "";
  }

  connectedCallback() {
    super.connectedCallback();
    this._modalStackID = openModal();
    this._unsubModalStack = subscribeModalStack(() => this.requestUpdate());
    this._onDocKey = (e) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      if (this._editingTerm) {
        this._cancelTermForm();
      } else {
        this._close();
      }
    };
    document.addEventListener("keydown", this._onDocKey);
    this._reload();
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    if (this._onDocKey) {
      document.removeEventListener("keydown", this._onDocKey);
      this._onDocKey = null;
    }
    if (this._unsubModalStack) {
      this._unsubModalStack();
      this._unsubModalStack = null;
    }
    if (this._modalStackID) {
      closeModal(this._modalStackID);
      this._modalStackID = null;
    }
  }

  async _reload() {
    if (!this.vocab) return;
    this._loading = true;
    this._error = "";
    try {
      const resp = await api.vocab.listFull(this.vocab);
      this._terms = resp.items || [];
    } catch (err) {
      this._error =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    } finally {
      this._loading = false;
    }
  }

  _close() {
    // Reload the shared vocab bundle so pickers get fresh data on
    // next open; parent components can also listen for
    // vocab-changed to re-render if a picker is already mounted.
    api.vocab.reload().catch(() => {});
    this.dispatchEvent(
      new CustomEvent("close", { bubbles: true, composed: true }),
    );
  }

  _openAddTerm() {
    this._editingTerm = {
      mode: "create",
      draft: {
        id: "",
        name: "",
        description: "",
        obo: "",
        inverse: "",
        symmetrical: false,
        super_types: "",
      },
      error: "",
    };
  }

  _openEditTerm(t) {
    this._editingTerm = {
      mode: "edit",
      draft: { ...t },
      error: "",
    };
  }

  _cancelTermForm() {
    this._editingTerm = null;
  }

  _termDraftChange(field, value) {
    if (!this._editingTerm) return;
    this._editingTerm = {
      ...this._editingTerm,
      draft: { ...this._editingTerm.draft, [field]: value },
    };
  }

  async _submitTermForm() {
    const f = this._editingTerm;
    if (!f) return;
    const id = (f.draft.id || "").trim();
    const name = (f.draft.name || "").trim();
    if (f.mode === "create" && !id) {
      this._editingTerm = { ...f, error: "id is required" };
      return;
    }
    if (!name) {
      this._editingTerm = { ...f, error: "name is required" };
      return;
    }
    try {
      if (f.mode === "create") {
        await api.vocab.add(this.vocab, f.draft);
      } else {
        await api.vocab.patch(this.vocab, f.draft.id, f.draft);
      }
      this._editingTerm = null;
      await this._reload();
    } catch (err) {
      this._editingTerm = {
        ...f,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  async _deleteTerm(t) {
    const ok = await confirmAction({
      heading: "Delete vocabulary term?",
      message: `"${t.name || t.id}" will be permanently deleted. Existing records using this term must first be updated to a different value — the delete will fail if any row still references it.`,
      actionLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api.vocab.delete(this.vocab, t.id);
      await this._reload();
    } catch (err) {
      this._error =
        err instanceof Problem
          ? `${err.title}: ${err.detail || err.message}`
          : String(err);
    }
  }

  render() {
    const adapter = vocabEditorAdapter(this.vocab);
    if (!adapter) return "";
    const covered =
      this._modalStackID && !isTopModal(this._modalStackID)
        ? " is-covered"
        : "";
    return html`
      <div class=${"backdrop" + covered}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="vocab-editor-title"
          @keydown=${(e) => e.stopPropagation()}
        >
          <div class="modal-header">
            <h3 id="vocab-editor-title">
              Edit ${adapter.label}
            </h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._close()}
              title="close"
              aria-label="close"
            >
              ×
            </button>
          </div>
          ${this._error
            ? html`<div class="error" role="alert">${this._error}</div>`
            : ""}
          ${this._editingTerm
            ? this._renderTermForm(adapter)
            : this._renderTermList(adapter)}
        </div>
      </div>
    `;
  }

  _renderTermList(adapter) {
    const items = this._terms || [];
    return html`
      ${this._loading
        ? html`<div class="empty">loading…</div>`
        : items.length === 0
          ? html`<div class="empty">No terms yet.</div>`
          : html`
              <table>
                <thead>
                  <tr>
                    <th>id</th>
                    <th>name</th>
                    ${adapter.hasDescription
                      ? html`<th>description</th>`
                      : ""}
                    ${adapter.hasObo ? html`<th>uri</th>` : ""}
                    <th aria-label="actions"></th>
                  </tr>
                </thead>
                <tbody>
                  ${items.map(
                    (t) => html`
                      <tr>
                        <td>${t.id}</td>
                        <td>${t.name || ""}</td>
                        ${adapter.hasDescription
                          ? html`<td class="desc">${t.description || ""}</td>`
                          : ""}
                        ${adapter.hasObo
                          ? html`<td class="obo">
                              ${t.obo
                                ? html`<a
                                    href=${t.obo}
                                    target="_blank"
                                    rel="noopener"
                                    >${t.obo}</a
                                  >`
                                : ""}
                            </td>`
                          : ""}
                        <td class="actions">
                          <span class="row-actions">
                            <button
                              class="icon-btn subtle"
                              type="button"
                              @click=${() => this._openEditTerm(t)}
                              title="edit term"
                              aria-label="edit term"
                            >
                              ${renderIcon("pencil", 14)}
                            </button>
                            <button
                              class="icon-btn subtle danger"
                              type="button"
                              @click=${() => this._deleteTerm(t)}
                              title="delete term"
                              aria-label="delete term"
                            >
                              ${renderIcon("trash-2", 14)}
                            </button>
                          </span>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            `}
      <div class="toolbar">
        <button type="button" @click=${() => this._openAddTerm()}>
          ${renderIcon("plus", 14)} Add term
        </button>
        <div class="right">
          <button type="button" class="primary" @click=${() => this._close()}>
            Done
          </button>
        </div>
      </div>
    `;
  }

  _renderTermForm(adapter) {
    const f = this._editingTerm;
    const d = f.draft;
    const set = (field) => (e) =>
      this._termDraftChange(field, e.target.value);
    const setCheck = (field) => (e) =>
      this._termDraftChange(field, e.target.checked);
    return html`
      ${f.error
        ? html`<div class="error" role="alert">${f.error}</div>`
        : ""}
      <form
        class="term-form"
        @submit=${(e) => {
          e.preventDefault();
          this._submitTermForm();
        }}
      >
        <label for="vt-id">id <span class="req">*</span></label>
        <input
          id="vt-id"
          type="text"
          .value=${d.id || ""}
          @input=${set("id")}
          ?disabled=${f.mode === "edit"}
          placeholder="SCREAMING_SNAKE_CASE"
        />
        ${f.mode === "edit"
          ? html`<span class="hint"
              >The id is the primary key and can't be edited in place.
              Delete + re-add to rename.</span
            >`
          : ""}

        <label for="vt-name">name <span class="req">*</span></label>
        <input
          id="vt-name"
          type="text"
          .value=${d.name || ""}
          @input=${set("name")}
          placeholder="human-readable name"
        />

        ${adapter.hasDescription
          ? html`
              <label for="vt-desc">description</label>
              <textarea
                id="vt-desc"
                .value=${d.description || ""}
                @input=${set("description")}
                placeholder="what this term means; when to use it"
              ></textarea>
            `
          : ""}

        ${adapter.hasObo
          ? html`
              <label for="vt-obo">uri</label>
              <input
                id="vt-obo"
                type="text"
                .value=${d.obo || ""}
                @input=${set("obo")}
                placeholder="http://purl.obolibrary.org/obo/…"
              />
              ${adapter.oboHint
                ? html`<span class="hint">${adapter.oboHint}</span>`
                : ""}
            `
          : ""}

        ${adapter.hasInverse
          ? html`
              <label for="vt-inv">inverse</label>
              <input
                id="vt-inv"
                type="text"
                .value=${d.inverse || ""}
                @input=${set("inverse")}
                placeholder="id of the inverse term (e.g. EATEN_BY)"
              />
            `
          : ""}

        ${adapter.hasSymmetrical
          ? html`
              <label for="vt-sym">symmetrical</label>
              <label class="checkbox-row">
                <input
                  id="vt-sym"
                  type="checkbox"
                  .checked=${!!d.symmetrical}
                  @change=${setCheck("symmetrical")}
                />
                The relation is its own inverse (A ↔ B).
              </label>
            `
          : ""}

        ${adapter.hasSuperTypes
          ? html`
              <label for="vt-super">super types</label>
              <input
                id="vt-super"
                type="text"
                .value=${d.super_types || ""}
                @input=${set("super_types")}
                placeholder="comma-separated parent term ids"
              />
            `
          : ""}

        <div class="toolbar">
          <button type="button" @click=${() => this._cancelTermForm()}>
            Cancel
          </button>
          <button type="submit" class="primary">
            ${f.mode === "edit" ? "Save changes" : "Add term"}
          </button>
        </div>
      </form>
    `;
  }
}

// ---------- <sfga-confirm-modal> ----------
//
// Generic imperatively-mounted confirmation modal. Renders a heading, a
// message, and an arbitrary buttons array; resolves the caller's Promise
// with the chosen button's `result` value. Two thin wrappers below
// (confirmDirty / confirmAction) cover the standard shapes:
// dirty-check (Keep editing / Discard / optional Save) and
// destructive-action (Cancel / Delete-or-similar).
class SfgaConfirmModal extends LitElement {
  static properties = {
    heading: { type: String },
    message: { type: String },
    // Array of {label, variant, result, focus?}.
    // First entry renders left-aligned (safe/cancel); the rest render
    // right-aligned as the action group. Variants: "default",
    // "primary", "danger", "danger-primary".
    buttons: { attribute: false },
  };

  static styles = [
    buttonStyles,
    css`
      .backdrop {
        position: fixed;
        inset: 0;
        background: color-mix(in oklab, var(--fg) 18%, transparent);
        display: grid;
        place-items: center;
        /* Confirm modal sits above other modals (z-popover) so a
           confirm prompt over an open form is unambiguously topmost.
           Nested-hide still applies via the shared .is-covered rule. */
        z-index: var(--z-popover);
      }
      .backdrop.is-covered {
        visibility: hidden;
      }
      .modal {
        background: var(--bg);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        padding: var(--sp-4) var(--sp-5);
        min-width: 22rem;
        max-width: var(--modal-sm);
        display: grid;
        gap: var(--sp-2);
        font-family: var(--font-body);
        color: var(--fg);
      }
      h3 {
        margin: 0;
        font-size: var(--fs-lg);
      }
      p {
        margin: 0;
        color: var(--dim);
        font-size: var(--fs-sm);
      }
      .toolbar {
        display: flex;
        align-items: center;
        gap: var(--sp-2);
        margin-top: var(--sp-2);
      }
      .spacer {
        flex: 1;
      }
    `,
  ];

  constructor() {
    super();
    this.heading = "";
    this.message = "";
    this.buttons = [];
  }

  connectedCallback() {
    super.connectedCallback();
    // Esc resolves as "cancel" iff a button carries that result value.
    // If no cancel path exists, Esc is a no-op so the caller must
    // handle every enumerated outcome explicitly.
    this._onDocKey = (e) => {
      if (e.key !== "Escape") return;
      const cancel = (this.buttons || []).find((b) => b.result === "cancel");
      if (!cancel) return;
      e.preventDefault();
      e.stopPropagation();
      this._choose("cancel");
    };
    document.addEventListener("keydown", this._onDocKey, true);
    this._modalStackID = openModal();
    this._unsubModalStack = subscribeModalStack(() => this.requestUpdate());
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    if (this._onDocKey) {
      document.removeEventListener("keydown", this._onDocKey, true);
      this._onDocKey = null;
    }
    if (this._releaseFocus) {
      this._releaseFocus();
      this._releaseFocus = null;
    }
    if (this._unsubModalStack) {
      this._unsubModalStack();
      this._unsubModalStack = null;
    }
    if (this._modalStackID) {
      closeModal(this._modalStackID);
      this._modalStackID = null;
    }
  }

  firstUpdated() {
    // Trap focus inside the modal and land initial focus on the
    // button flagged focus:true (usually cancel — safest default so
    // an accidental Enter doesn't destroy work). Tab / Shift+Tab
    // cycle within the modal; release() on disconnect restores focus
    // to whoever opened the modal.
    this._releaseFocus = trapFocus(this.renderRoot, {
      initialFocus: "button[data-focus]",
    });
  }

  _choose(result) {
    this.dispatchEvent(
      new CustomEvent("confirm-result", {
        detail: result,
        bubbles: true,
        composed: true,
      }),
    );
  }

  render() {
    const btns = this.buttons || [];
    const left = btns.slice(0, 1);
    const right = btns.slice(1);
    const renderBtn = (b) => html`
      <button
        class=${b.variant || "default"}
        type="button"
        ?data-focus=${!!b.focus}
        @click=${() => this._choose(b.result)}
      >
        ${b.label}
      </button>
    `;
    const covered =
      this._modalStackID && !isTopModal(this._modalStackID)
        ? " is-covered"
        : "";
    return html`
      <div class=${"backdrop" + covered}>
        <div
          class="modal"
          role="alertdialog"
          aria-modal="true"
          aria-labelledby="hive-confirm-title"
        >
          <h3 id="hive-confirm-title">${this.heading}</h3>
          <p>${this.message}</p>
          <div class="toolbar">
            ${left.map(renderBtn)}
            <div class="spacer"></div>
            ${right.map(renderBtn)}
          </div>
        </div>
      </div>
    `;
  }
}

// _mountConfirm mounts an SfgaConfirmModal, awaits the user's choice,
// removes the element, and resolves with the chosen button's result.
async function _mountConfirm({ heading, message, buttons }) {
  return new Promise((resolve) => {
    const el = document.createElement("sfga-confirm-modal");
    el.heading = heading;
    el.message = message;
    el.buttons = buttons;
    const handler = (e) => {
      el.removeEventListener("confirm-result", handler);
      if (el.parentNode) el.parentNode.removeChild(el);
      resolve(e.detail);
    };
    el.addEventListener("confirm-result", handler);
    document.body.appendChild(el);
  });
}

// confirmDirty prompts the user before dismissing a form or navigating
// away from unsaved edits. Two-choice by default (Discard / Keep
// editing); pass canSave: true to add the Save option. Returns
// "save" | "discard" | "cancel". On "save", the caller runs its own
// save path and treats a save failure as a signal to abort the
// surrounding action.
async function confirmDirty({
  heading = "Unsaved changes",
  message = "You have unsaved changes.",
  canSave = false,
} = {}) {
  const buttons = [
    { label: "Keep editing", variant: "default", result: "cancel", focus: true },
    { label: "Discard", variant: "danger", result: "discard" },
  ];
  if (canSave) {
    buttons.push({ label: "Save", variant: "primary", result: "save" });
  }
  return _mountConfirm({ heading, message, buttons });
}

// confirmAction prompts before a destructive or otherwise consequential
// action (delete, move, reset). Two-choice (Cancel / <action>). Returns
// true when the user confirms, false otherwise. Default action variant
// is "danger-primary" (filled red) for destructive intents; pass
// actionVariant: "primary" for constructive intents that still warrant
// a checkpoint (e.g. reassignments).
async function confirmAction({
  heading,
  message,
  actionLabel,
  actionVariant = "danger-primary",
  cancelLabel = "Cancel",
}) {
  const buttons = [
    { label: cancelLabel, variant: "default", result: "cancel", focus: true },
    { label: actionLabel, variant: actionVariant, result: "confirm" },
  ];
  const result = await _mountConfirm({ heading, message, buttons });
  return result === "confirm";
}

// SfgaPdfViewer renders a PDF as a stack of canvases using pdf.js
// (vendored under /vendor/pdfjs-*). Replaces the browser's built-in
// PDF iframe viewer specifically in the Digitize tab because a
// self-rendered canvas is the only surface hive can overlay
// bbox-drawing tools on. Read-only reference viewing elsewhere keeps
// using the iframe viewer since it needs no interactive overlay.
//
// Contract:
//   - src (property): PDF URL. Changing src reloads and re-renders.
//   - goToPage(n) (public method): scrolls the given 1-indexed page
//     into view. Preserves the current jump-to-page UX driven by
//     fig-xref clicks in the reader (which post messages that end
//     up here via SfgaReferences._navPdfPane).
//
// Rendering strategy: all pages up-front at a fixed scale, stacked
// vertically inside a scrollable container. This is simpler than
// virtualized lazy rendering and adequate for the paper-sized PDFs
// hive processes (typically <30 pages). Optimizing to lazy render
// is a later concern once we see performance issues with large PDFs.
//
// pdf.js worker: uses the vendored worker under /vendor/pdfjs-*.
// Same-origin so no CORS worries. GlobalWorkerOptions.workerSrc is
// set the first time the component loads a PDF; subsequent loads
// reuse the same worker.
// PDF_RENDER_SCALE controls both the canvas resolution and the
// canvas-coord ↔ PDF-point conversion. Higher = sharper text but
// bigger canvases. 1.5 = 108 DPI equivalent, readable at typical
// laptop screens without excessive memory use on longer PDFs.
const PDF_RENDER_SCALE = 1.5;

class SfgaPdfViewer extends LitElement {
  static properties = {
    src: { type: String },
    // 'read' — pointer interaction is passive (default)
    // 'bbox' — dragging inside a page's overlay draws a bounding
    //          box; on release the viewer fires bbox-drawn with
    //          {page, bbox: {x,y,w,h}} in PDF points.
    mode: { type: String },
    // Existing corrections to render as visible rectangles over
    // the appropriate pages. Each entry: {id, label, page, bbox}
    // where bbox is in PDF points and page is 1-indexed. The
    // parent (SfgaReferences) owns this list — the viewer only
    // renders and reports interactions.
    bboxes: { attribute: false },
    _loading: { state: true },
    _error: { state: true },
    _numPages: { state: true },
    _currentPage: { state: true },
  };

  static styles = css`
    :host {
      display: block;
      overflow: auto;
      background: var(--surface, #333);
      height: 100%;
      position: relative;
    }
    .pages {
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: var(--sp-2, 8px);
      padding: var(--sp-2, 8px);
    }
    .page-wrapper {
      position: relative;
      display: block;
    }
    .pages canvas {
      background: white;
      display: block;
      box-shadow: 0 1px 4px rgba(0, 0, 0, 0.3);
      max-width: 100%;
      height: auto;
    }
    /* Overlay that captures pointer events for bbox drawing and
       hosts the visible bbox rectangles. Position sized to match
       the canvas exactly via matching absolute inset. */
    .bbox-layer {
      position: absolute;
      inset: 0;
      pointer-events: none;
    }
    :host([mode="bbox"]) .bbox-layer {
      pointer-events: auto;
      cursor: crosshair;
    }
    .bbox-existing,
    .bbox-drawing {
      position: absolute;
      border: 2px solid var(--sev-info, #4a90e2);
      background: color-mix(in oklab, var(--sev-info, #4a90e2) 15%, transparent);
      pointer-events: none;
      box-sizing: border-box;
    }
    /* Enable hover interaction on saved (existing) bboxes so a
       curator can hover to reveal the delete button. Drawing
       previews stay pointer-events:none. */
    .bbox-existing {
      pointer-events: auto;
      cursor: default;
    }
    .bbox-existing.selected {
      border-color: var(--accent, #ff8c00);
      background: color-mix(in oklab, var(--accent, #ff8c00) 20%, transparent);
    }
    /* Three-color visual state for bbox overlays:
         auto     — GROBID-detected, no modification (blue, default
                    inherited from .bbox-existing above)
         modified — GROBID-detected + curator edited (blue-green)
         curator  — curator-added new region (green)
       Distinguishes fully-automatic extraction, curator-corrected
       extraction, and pure-curator annotation at a glance without
       needing to hover or open a label. */
    .bbox-existing.bbox-state-modified {
      border-color: #2e8b8b;
      background: color-mix(in oklab, #2e8b8b 18%, transparent);
    }
    .bbox-existing.bbox-state-modified .bbox-label {
      background: #2e8b8b;
    }
    .bbox-existing.bbox-state-curator {
      border-color: #2e8b3f;
      background: color-mix(in oklab, #2e8b3f 18%, transparent);
    }
    .bbox-existing.bbox-state-curator .bbox-label {
      background: #2e8b3f;
    }
    .bbox-label {
      position: absolute;
      top: -1.4em;
      left: 0;
      background: var(--sev-info, #4a90e2);
      color: white;
      font-size: var(--fs-xs, 11px);
      padding: 0 var(--sp-1, 4px);
      border-radius: var(--radius-xs, 2px) var(--radius-xs, 2px) 0 0;
      white-space: nowrap;
    }
    /* Delete button appears on hover in the top-right corner of
       each saved bbox — the intuitive spot for "remove this
       thing" affordance. Always visible on touch devices via
       @media (hover:none) since hover isn't a reliable signal. */
    .bbox-delete {
      position: absolute;
      top: 2px;
      right: 2px;
      background: rgba(200, 30, 30, 0.9);
      color: white;
      border: none;
      border-radius: 2px;
      width: 1.6rem;
      height: 1.6rem;
      line-height: 0;
      cursor: pointer;
      display: none;
      padding: 0;
      align-items: center;
      justify-content: center;
    }
    .bbox-existing:hover .bbox-delete {
      display: inline-flex;
    }
    @media (hover: none) {
      .bbox-delete { display: inline-flex; }
    }
    /* Hover reveal + always-on for touch consolidated with the
       .bbox-delete rule above (uses inline-flex for icon center). */
    /* Corner resize handles. Hover-revealed on pointer devices,
       always visible on touch (mirrors the delete-button pattern).
       Sit slightly outside the bbox border so they're easy to grab
       without occluding the region under the box. */
    .bbox-resize-handle {
      position: absolute;
      width: 12px;
      height: 12px;
      background: white;
      border: 1px solid var(--border, #555);
      pointer-events: auto;
      display: none;
      z-index: 2;
      box-sizing: border-box;
    }
    .bbox-existing:hover .bbox-resize-handle {
      display: block;
    }
    @media (hover: none) {
      .bbox-resize-handle { display: block; }
    }
    .bbox-resize-handle.nw { top: -6px; left: -6px; cursor: nw-resize; }
    .bbox-resize-handle.ne { top: -6px; right: -6px; cursor: ne-resize; }
    .bbox-resize-handle.sw { bottom: -6px; left: -6px; cursor: sw-resize; }
    .bbox-resize-handle.se { bottom: -6px; right: -6px; cursor: se-resize; }
    .empty,
    .loading,
    .error {
      padding: var(--sp-4, 16px);
      color: var(--dim, #aaa);
      text-align: center;
    }
    .error {
      color: var(--error, #c00);
      font-family: var(--font-mono, monospace);
    }
    .status {
      position: sticky;
      top: 0;
      background: var(--surface, #333);
      color: var(--dim, #aaa);
      font-size: var(--fs-xs, 11px);
      padding: var(--sp-1, 4px) var(--sp-2, 8px);
      z-index: 2;
      display: flex;
      justify-content: space-between;
      border-bottom: 1px solid var(--border, #555);
    }
  `;

  constructor() {
    super();
    this.src = "";
    this.mode = "read";
    this.bboxes = [];
    this._loading = false;
    this._error = "";
    this._numPages = 0;
    this._currentPage = 1;
    this._pdfDoc = null;
    // Track last successful load so src property re-assignments to
    // the same URL don't retrigger loading.
    this._loadedSrc = "";
    // Per-page CSS-pixel viewport dimensions, indexed by 1-based
    // page number. Set during _load; used by _syncBboxes to
    // position existing bbox rectangles when the parent updates
    // the bboxes property (either the initial fetch or after a
    // save / delete).
    this._pageViewports = {};
    // Active drag state during a bbox draw. Cleared on pointerup.
    this._drag = null;
  }

  updated(changed) {
    if (changed.has("src") && this.src && this.src !== this._loadedSrc) {
      this._load();
    }
    if (changed.has("bboxes") && this._loadedSrc) {
      this._syncBboxes();
    }
    if (changed.has("mode")) {
      this.setAttribute("mode", this.mode || "read");
    }
  }

  async _load() {
    this._loading = true;
    this._error = "";
    this._numPages = 0;
    this._pageViewports = {};
    // Clear old page wrappers before rendering the new PDF.
    // Imperative because Lit doesn't manage the .pages children.
    const container = this.renderRoot?.querySelector(".pages");
    if (container) container.innerHTML = "";
    // Attach scroll listener once. Saves current scrollTop into
    // localStorage keyed by the PDF URL so a reload/return to the
    // same reference lands roughly where the curator was reading.
    if (!this._scrollListenerAttached) {
      this._scrollListenerAttached = true;
      let saveTimer = 0;
      this.addEventListener("scroll", () => {
        if (!this._loadedSrc) return;
        if (saveTimer) clearTimeout(saveTimer);
        saveTimer = setTimeout(() => {
          try {
            localStorage.setItem(
              "hive:pdf-scroll:" + this._loadedSrc,
              String(this.scrollTop),
            );
          } catch (err) {
            // Storage disabled / quota — best-effort feature.
          }
        }, 250);
      });
    }
    try {
      const pdfjsLib = await import("/vendor/pdfjs-6.2.108/pdf.mjs");
      pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs-6.2.108/pdf.worker.mjs";
      // pdf.js 6.x requires an options object — a bare string URL
      // is no longer accepted (getDocument returns the "expected
      // either data, range, or url" error).
      const loadingTask = pdfjsLib.getDocument({ url: this.src });
      const doc = await loadingTask.promise;
      this._pdfDoc = doc;
      this._numPages = doc.numPages;
      this._loadedSrc = this.src;
      // Await one render cycle so the .pages container exists after
      // _numPages assignment triggered a re-render.
      await this.updateComplete;
      const pagesContainer = this.renderRoot.querySelector(".pages");
      for (let i = 1; i <= doc.numPages; i++) {
        const page = await doc.getPage(i);
        const viewport = page.getViewport({ scale: PDF_RENDER_SCALE });
        // Wrap canvas + bbox overlay in a position:relative div so
        // the overlay's absolute-positioned rectangles align with
        // the canvas coordinate space regardless of CSS scaling.
        const wrapper = document.createElement("div");
        wrapper.className = "page-wrapper";
        wrapper.dataset.page = String(i);
        wrapper.style.width = viewport.width + "px";
        const canvas = document.createElement("canvas");
        canvas.width = viewport.width;
        canvas.height = viewport.height;
        canvas.dataset.page = String(i);
        canvas.className = "pdf-page";
        wrapper.appendChild(canvas);
        const overlay = document.createElement("div");
        overlay.className = "bbox-layer";
        overlay.dataset.page = String(i);
        overlay.addEventListener("pointerdown", (e) => this._onDragStart(e, i));
        wrapper.appendChild(overlay);
        pagesContainer.appendChild(wrapper);
        this._pageViewports[i] = { width: viewport.width, height: viewport.height };
        await page.render({
          canvasContext: canvas.getContext("2d"),
          viewport,
        }).promise;
      }
      // Overlay existing bboxes now that dimensions are known.
      this._syncBboxes();
      // Restore last scroll position for this PDF so reload
      // lands on the same page the curator was reading.
      try {
        const saved = localStorage.getItem("hive:pdf-scroll:" + this._loadedSrc);
        if (saved != null) {
          const n = Number.parseInt(saved, 10);
          if (Number.isFinite(n)) this.scrollTop = n;
        }
      } catch (err) {
        // Ignore — restore is best-effort.
      }
    } catch (err) {
      this._error = "Failed to load PDF: " + (err?.message || String(err));
    } finally {
      this._loading = false;
    }
  }

  // goToPage scrolls the given 1-indexed page canvas into view.
  // Called imperatively by SfgaReferences when a fig-xref click in
  // the reader requests a jump. No-op when the page hasn't rendered
  // yet — the caller retries after loading completes.
  goToPage(n) {
    const num = Number.parseInt(n, 10);
    if (!Number.isFinite(num) || num < 1) return;
    const canvas = this.renderRoot?.querySelector(`canvas[data-page="${num}"]`);
    if (!canvas) return;
    canvas.scrollIntoView({ block: "start", behavior: "smooth" });
    this._currentPage = num;
  }

  // _syncBboxes rebuilds the visible bbox rectangles from the
  // current bboxes property. Called after _load finishes rendering
  // pages and whenever the parent updates the bboxes property (a
  // new correction was saved, an existing one was deleted). Cheap
  // to fully rebuild — bbox counts per paper stay small.
  _syncBboxes() {
    const bboxes = Array.isArray(this.bboxes) ? this.bboxes : [];
    // Clear old bbox rectangles from every overlay.
    const overlays = this.renderRoot?.querySelectorAll(".bbox-layer") || [];
    for (const overlay of overlays) {
      overlay.querySelectorAll(".bbox-existing").forEach((el) => el.remove());
    }
    for (const b of bboxes) {
      const page = b.page || b.bbox_page;
      const bbox = b.bbox;
      if (!page || !bbox) continue;
      const overlay = this.renderRoot?.querySelector(
        `.bbox-layer[data-page="${page}"]`,
      );
      if (!overlay) continue;
      const rect = this._pdfBBoxToCanvasRect(bbox);
      const el = document.createElement("div");
      const origin = b.origin || (b.id && b.id.startsWith("curator_") ? "curator" : "grobid");
      const state = b.state || (origin === "curator" ? "curator" : "auto");
      el.className = "bbox-existing bbox-state-" + state;
      el.dataset.correctionId = b.id || "";
      el.dataset.origin = origin;
      el.dataset.page = String(page);
      el.style.left = rect.left + "px";
      el.style.top = rect.top + "px";
      el.style.width = rect.width + "px";
      el.style.height = rect.height + "px";
      if (b.label) {
        const label = document.createElement("span");
        label.className = "bbox-label";
        label.textContent = b.label;
        el.appendChild(label);
      }
      // Curator additions (bbox-add) and GROBID-detected regions
      // (from the sidecar's fig/table-wrap elements) are both
      // click-to-edit. Curator additions also expose a delete
      // button on hover; GROBID regions don't (deleting a GROBID
      // region would need a different correction kind that
      // records "this element shouldn't exist" — deferred).
      if (b.id) {
        el.style.cursor = "pointer";
        el.title =
          origin === "curator"
            ? "Click to edit — × to delete"
            : "Click to edit — original extractor output preserved for training";
        el.addEventListener("click", (e) => {
          e.stopPropagation();
          this.dispatchEvent(
            new CustomEvent("bbox-edit", {
              detail: { id: b.id, origin },
              bubbles: true,
              composed: true,
            }),
          );
        });
      }
      // Delete button on both curator additions AND GROBID
       // regions. Semantics differ (curator: delete the row and
       // its JATS element; GROBID: create a bbox-remove correction
       // that suppresses the element on this + future reapplies),
       // but the affordance and icon are the same to the curator.
      if (b.id) {
        const delBtn = document.createElement("button");
        delBtn.className = "bbox-delete";
        delBtn.type = "button";
        delBtn.setAttribute("aria-label", "Delete correction");
        delBtn.title = "Delete this correction";
        // trash-2 icon (matching the sidebar / detail-header delete
        // affordance) so this control reads as "delete", not "close".
        // Same viewBox and stroke conventions as renderIcon() above.
        delBtn.innerHTML =
          '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" ' +
          'stroke="currentColor" stroke-width="2" stroke-linecap="round" ' +
          'stroke-linejoin="round">' +
          '<path d="M3 6h18" />' +
          '<path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />' +
          '<path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />' +
          '<line x1="10" x2="10" y1="11" y2="17" />' +
          '<line x1="14" x2="14" y1="11" y2="17" />' +
          "</svg>";
        delBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          this.dispatchEvent(
            new CustomEvent("bbox-delete", {
              detail: { id: b.id, origin },
              bubbles: true,
              composed: true,
            }),
          );
        });
        el.appendChild(delBtn);
      }
      // Corner resize handles. Attached to any bbox with an id so
      // both curator additions and GROBID regions can be reshaped;
      // the parent decides how to persist the new coords (delete +
      // recreate for curator, bbox-modify for GROBID).
      if (b.id) {
        for (const corner of ["nw", "ne", "sw", "se"]) {
          const handle = document.createElement("div");
          handle.className = "bbox-resize-handle " + corner;
          handle.dataset.corner = corner;
          handle.setAttribute("aria-hidden", "true");
          handle.addEventListener("pointerdown", (e) =>
            this._onResizeStart(e, el, corner, page, b.id, origin),
          );
          // Suppress the bbox-existing click (edit) when the pointer
          // lands on a handle — dragging a corner is not a request
          // to open the edit form.
          handle.addEventListener("click", (e) => e.stopPropagation());
          el.appendChild(handle);
        }
      }
      overlay.appendChild(el);
    }
  }

  // _pdfBBoxToCanvasRect converts a PDF-point bbox (x, y, w, h with
  // origin at top-left in PDF-point space) to a canvas-pixel rect
  // (left, top, width, height) suitable for CSS positioning inside
  // the overlay layer. Multiplies by PDF_RENDER_SCALE to get pixels
  // at the current render resolution.
  _pdfBBoxToCanvasRect(bbox) {
    return {
      left: bbox.x * PDF_RENDER_SCALE,
      top: bbox.y * PDF_RENDER_SCALE,
      width: bbox.w * PDF_RENDER_SCALE,
      height: bbox.h * PDF_RENDER_SCALE,
    };
  }

  // Drag lifecycle: pointerdown starts the draw, pointermove
  // resizes a temporary rectangle, pointerup emits the bbox-drawn
  // event with PDF-point coords and cleans up.
  //
  // Uses setPointerCapture so a drag that leaves the overlay still
  // receives move + up events on the original overlay.
  _onDragStart(e, pageNum) {
    if (this.mode !== "bbox") return;
    e.preventDefault();
    const overlay = e.currentTarget;
    overlay.setPointerCapture(e.pointerId);
    // Overlay's client rect vs canvas rect: identical since the
    // overlay is `position: absolute; inset: 0` inside the wrapper.
    // Use overlay for coord math to avoid a separate canvas lookup.
    const rect = overlay.getBoundingClientRect();
    const startX = e.clientX - rect.left;
    const startY = e.clientY - rect.top;
    const drawing = document.createElement("div");
    drawing.className = "bbox-drawing";
    drawing.style.left = startX + "px";
    drawing.style.top = startY + "px";
    drawing.style.width = "0";
    drawing.style.height = "0";
    overlay.appendChild(drawing);
    this._drag = {
      pageNum,
      overlay,
      startX,
      startY,
      rect,
      element: drawing,
      pointerId: e.pointerId,
    };
    overlay.addEventListener("pointermove", this._onDragMove);
    overlay.addEventListener("pointerup", this._onDragEnd);
    overlay.addEventListener("pointercancel", this._onDragCancel);
  }

  _onDragMove = (e) => {
    if (!this._drag) return;
    const rect = this._drag.overlay.getBoundingClientRect();
    // Recompute cursor position relative to the current (possibly
    // scrolled) overlay rect. Clamp inside 0..overlay dims so a
    // drag past the edge doesn't produce out-of-range coords.
    const cx = Math.max(0, Math.min(e.clientX - rect.left, rect.width));
    const cy = Math.max(0, Math.min(e.clientY - rect.top, rect.height));
    const left = Math.min(this._drag.startX, cx);
    const top = Math.min(this._drag.startY, cy);
    const width = Math.abs(cx - this._drag.startX);
    const height = Math.abs(cy - this._drag.startY);
    const el = this._drag.element;
    el.style.left = left + "px";
    el.style.top = top + "px";
    el.style.width = width + "px";
    el.style.height = height + "px";
  };

  _onDragEnd = (e) => {
    if (!this._drag) return;
    const d = this._drag;
    d.overlay.removeEventListener("pointermove", this._onDragMove);
    d.overlay.removeEventListener("pointerup", this._onDragEnd);
    d.overlay.removeEventListener("pointercancel", this._onDragCancel);
    d.overlay.releasePointerCapture(d.pointerId);
    // Convert the drawn rect (CSS pixels relative to overlay = CSS
    // pixels relative to canvas since overlay is inset:0) to PDF
    // points. Overlay's rendered CSS size may not equal the
    // canvas.width (which is at PDF_RENDER_SCALE) if CSS scales
    // the canvas — divide by the ratio to normalize.
    const rect = d.overlay.getBoundingClientRect();
    const scaleFactorX = d.overlay.parentElement.querySelector("canvas").width / rect.width;
    const scaleFactorY = d.overlay.parentElement.querySelector("canvas").height / rect.height;
    const cx = Math.max(0, Math.min(e.clientX - rect.left, rect.width));
    const cy = Math.max(0, Math.min(e.clientY - rect.top, rect.height));
    const cssLeft = Math.min(d.startX, cx);
    const cssTop = Math.min(d.startY, cy);
    const cssWidth = Math.abs(cx - d.startX);
    const cssHeight = Math.abs(cy - d.startY);
    // Remove the temporary drawing rect — the parent will re-add
    // it as a stable one via the bboxes property once the
    // correction is saved server-side.
    d.element.remove();
    this._drag = null;
    // Ignore tiny drags (misclicks) — a real bbox should be at
    // least ~10 CSS pixels in each dimension.
    if (cssWidth < 10 || cssHeight < 10) return;
    // Convert CSS-pixel → canvas-pixel → PDF-point.
    const pxLeft = cssLeft * scaleFactorX;
    const pxTop = cssTop * scaleFactorY;
    const pxWidth = cssWidth * scaleFactorX;
    const pxHeight = cssHeight * scaleFactorY;
    const pdfBBox = {
      x: pxLeft / PDF_RENDER_SCALE,
      y: pxTop / PDF_RENDER_SCALE,
      w: pxWidth / PDF_RENDER_SCALE,
      h: pxHeight / PDF_RENDER_SCALE,
    };
    this.dispatchEvent(
      new CustomEvent("bbox-drawn", {
        detail: { page: d.pageNum, bbox: pdfBBox },
        bubbles: true,
        composed: true,
      }),
    );
  };

  _onDragCancel = () => {
    if (!this._drag) return;
    this._drag.element.remove();
    this._drag.overlay.removeEventListener("pointermove", this._onDragMove);
    this._drag.overlay.removeEventListener("pointerup", this._onDragEnd);
    this._drag.overlay.removeEventListener("pointercancel", this._onDragCancel);
    this._drag = null;
  };

  // Resize lifecycle. Handle pointerdown captures the pointer on
  // the handle itself so the drag survives leaving the handle's
  // 12x12 hit region. Coordinates stay in CSS pixels during the
  // interactive phase (fast, no rounding drift) and convert to PDF
  // points only on release when the bbox-resize event is emitted.
  //
  // stopPropagation on pointerdown keeps the overlay's draw-a-new-
  // bbox pointerdown handler out of the picture even when mode is
  // "bbox" — a curator resizing an existing box shouldn't
  // accidentally start drawing a new one on top of it.
  _onResizeStart(e, bboxEl, corner, pageNum, correctionID, origin) {
    e.preventDefault();
    e.stopPropagation();
    const overlay = bboxEl.parentElement;
    const canvas = overlay.parentElement.querySelector("canvas");
    const overlayRect = overlay.getBoundingClientRect();
    const scaleX = canvas.width / overlayRect.width;
    const scaleY = canvas.height / overlayRect.height;
    const startBBox = {
      left: parseFloat(bboxEl.style.left) || 0,
      top: parseFloat(bboxEl.style.top) || 0,
      width: parseFloat(bboxEl.style.width) || 0,
      height: parseFloat(bboxEl.style.height) || 0,
    };
    const handle = e.currentTarget;
    handle.setPointerCapture(e.pointerId);
    this._resize = {
      bboxEl,
      handle,
      corner,
      pageNum,
      correctionID,
      origin,
      overlay,
      overlayRect,
      scaleX,
      scaleY,
      startBBox,
      startPointer: { x: e.clientX, y: e.clientY },
      pointerId: e.pointerId,
    };
    handle.addEventListener("pointermove", this._onResizeMove);
    handle.addEventListener("pointerup", this._onResizeEnd);
    handle.addEventListener("pointercancel", this._onResizeCancel);
  }

  _onResizeMove = (e) => {
    if (!this._resize) return;
    const r = this._resize;
    // Overlay rect can shift if the viewer scrolled during the
    // drag; recompute so cursor tracking stays honest.
    const overlayRect = r.overlay.getBoundingClientRect();
    const dx = e.clientX - r.startPointer.x;
    const dy = e.clientY - r.startPointer.y;
    let left = r.startBBox.left;
    let top = r.startBBox.top;
    let right = r.startBBox.left + r.startBBox.width;
    let bottom = r.startBBox.top + r.startBBox.height;
    if (r.corner === "nw" || r.corner === "sw") left = r.startBBox.left + dx;
    if (r.corner === "ne" || r.corner === "se") right = r.startBBox.left + r.startBBox.width + dx;
    if (r.corner === "nw" || r.corner === "ne") top = r.startBBox.top + dy;
    if (r.corner === "sw" || r.corner === "se") bottom = r.startBBox.top + r.startBBox.height + dy;
    // Normalize so a drag past the opposite edge flips corner
    // ownership cleanly rather than producing negative width/height.
    const cssLeft = Math.max(0, Math.min(left, right));
    const cssRight = Math.min(overlayRect.width, Math.max(left, right));
    const cssTop = Math.max(0, Math.min(top, bottom));
    const cssBottom = Math.min(overlayRect.height, Math.max(top, bottom));
    r.bboxEl.style.left = cssLeft + "px";
    r.bboxEl.style.top = cssTop + "px";
    r.bboxEl.style.width = Math.max(0, cssRight - cssLeft) + "px";
    r.bboxEl.style.height = Math.max(0, cssBottom - cssTop) + "px";
  };

  _onResizeEnd = (e) => {
    if (!this._resize) return;
    const r = this._resize;
    r.handle.removeEventListener("pointermove", this._onResizeMove);
    r.handle.removeEventListener("pointerup", this._onResizeEnd);
    r.handle.removeEventListener("pointercancel", this._onResizeCancel);
    try { r.handle.releasePointerCapture(r.pointerId); } catch (_) {}
    // Convert the final CSS-pixel rect back to PDF points.
    const cssLeft = parseFloat(r.bboxEl.style.left) || 0;
    const cssTop = parseFloat(r.bboxEl.style.top) || 0;
    const cssWidth = parseFloat(r.bboxEl.style.width) || 0;
    const cssHeight = parseFloat(r.bboxEl.style.height) || 0;
    // Reject a resize that collapsed the box below the same 10px
    // threshold used for new draws — treat it as a misdrag and let
    // the parent re-sync from the unchanged server state.
    if (cssWidth < 10 || cssHeight < 10) {
      this._resize = null;
      this._syncBboxes();
      return;
    }
    const pxLeft = cssLeft * r.scaleX;
    const pxTop = cssTop * r.scaleY;
    const pxWidth = cssWidth * r.scaleX;
    const pxHeight = cssHeight * r.scaleY;
    const pdfBBox = {
      x: pxLeft / PDF_RENDER_SCALE,
      y: pxTop / PDF_RENDER_SCALE,
      w: pxWidth / PDF_RENDER_SCALE,
      h: pxHeight / PDF_RENDER_SCALE,
    };
    const { pageNum, correctionID, origin } = r;
    this._resize = null;
    this.dispatchEvent(
      new CustomEvent("bbox-resize", {
        detail: { id: correctionID, origin, page: pageNum, bbox: pdfBBox },
        bubbles: true,
        composed: true,
      }),
    );
  };

  _onResizeCancel = () => {
    if (!this._resize) return;
    const r = this._resize;
    r.handle.removeEventListener("pointermove", this._onResizeMove);
    r.handle.removeEventListener("pointerup", this._onResizeEnd);
    r.handle.removeEventListener("pointercancel", this._onResizeCancel);
    this._resize = null;
    // Restore visual state from the server-known bboxes.
    this._syncBboxes();
  };

  render() {
    if (this._error) {
      return html`<div class="error" role="alert">${this._error}</div>`;
    }
    return html`
      ${this._numPages > 0
        ? html`<div class="status">
            <span>Page ${this._currentPage} of ${this._numPages}</span>
            ${this._loading ? html`<span>Rendering…</span>` : nothing}
          </div>`
        : nothing}
      ${this._loading && this._numPages === 0
        ? html`<div class="loading" role="status">Loading PDF…</div>`
        : nothing}
      <div class="pages"></div>
    `;
  }
}

customElements.define("sfga-app", SfgaApp);
customElements.define("sfga-tree", SfgaTree);
customElements.define("sfga-detail", SfgaDetail);
customElements.define("sfga-metadata", SfgaMetadata);
customElements.define("sfga-references", SfgaReferences);
customElements.define("sfga-issues", SfgaIssues);
customElements.define("sfga-combobox", SfgaCombobox);
customElements.define("sfga-add-reference-modal", SfgaAddReferenceModal);
customElements.define("sfga-help-modal", SfgaHelpModal);
customElements.define("sfga-agent-card", SfgaAgentCard);
customElements.define("sfga-agent-modal", SfgaAgentModal);
customElements.define("sfga-agent-section", SfgaAgentSection);
customElements.define("sfga-confirm-modal", SfgaConfirmModal);
customElements.define("sfga-vocab-editor", SfgaVocabEditor);
customElements.define("sfga-pdf-viewer", SfgaPdfViewer);
