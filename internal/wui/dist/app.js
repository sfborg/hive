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
  try {
    const n = await api.name.get(id);
    return [
      n.scientific_name || n.canonical_simple || n.canonical_full,
      n.authorship,
    ]
      .filter(Boolean)
      .join(" ");
  } catch (_) {
    return id;
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
    // Reference id to pre-select on the References screen — set by
    // _onIssueNavigate when a reference-scoped issue is opened, so
    // the References component focuses that row on first render.
    _pendingReferenceId: { state: true },
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
    .screen.references {
      grid-template-columns: minmax(20rem, 1fr) 1fr;
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
              @reference-selected=${() => (this._pendingReferenceId = "")}
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
    // Modal state for the name-editor (invoked from the pencil on any
    // synonym or basionym row in Nomenclatural history). Null when
    // closed; {phase: "loading"|"edit", id, original, draft, etag,
    // busy, error} while open. Loads the full name row from
    // /api/name/{id} on open so the form can seed all fields; PATCH
    // sends only fields that differ.
    _nameEditor: { state: true },
    // Classification chain from GET /api/taxon/{id}/classification —
    // root-down list including the taxon itself as the last entry.
    // Backs the breadcrumb strip above the taxon heading.
    _classification: { state: true },
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
    // and _pickedReferenceLabel carries the label into the combobox
    // after a successful pick so the display updates immediately without
    // waiting on the resolver's second fetch.
    // _addingReferenceFor tracks which form context requested the
    // add-reference modal — "edit" or "create" — so _onReferencePicked
    // routes the picked id back into the right draft. Empty string
    // means the modal is closed. Replaces the earlier boolean
    // _addingReference now that the create form uses the same modal
    // (see DESIGN.md § Reference-picker on every data-entry form).
    _addingReferenceFor: { state: true },
    _pickedReferenceLabel: { state: true },
    // Same idea for the create form's picker — carries the freshly-
    // added reference label into the combobox display without waiting
    // on the resolver's second GET.
    _pickedCreateReferenceLabel: { state: true },
    // Edit-mode progressive-disclosure toggle for the atomized name +
    // atomized authorship blocks. Mirrors _createShowAtomized on the
    // create form; edit and create both surface the same widget set
    // so curators learn one form.
    _editShowAtomized: { state: true },
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
      visibility: visible;
      color: var(--sev-warn);
    }
    section.vernaculars td.actions .row-actions .warn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
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
      visibility: visible;
      color: var(--sev-warn);
    }
    section.distributions td.actions .row-actions .warn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
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
         row that needs attention. */
      visibility: visible;
      color: var(--sev-warn);
    }
    section.nomen-history li.history .row-actions .warn:hover:not(:disabled) {
      /* Hover tints the button background with a soft warn wash for
         feedback without escalating the icon color itself. */
      background: color-mix(in oklab, var(--sev-warn) 18%, transparent);
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
      background: color-mix(in oklab, var(--bg) 60%, transparent);
      display: grid;
      place-items: center;
      z-index: 10;
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
    this._synonymDelete = null;
    this._nameEditor = null;
    this._standardizedAuthorship =
      localStorage.getItem("hive-standardized-authorship") === "true";
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
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = null;
    this._editShowAtomized = readAtomizedPref();
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
      changed.has("_editing") ||
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
  }

  async _load() {
    if (!this.taxonId) {
      this._taxon = this._name = null;
      this._etag = "";
      this._nameEtag = "";
      this._nomenHistory = null;
      this._vernaculars = [];
      this._distributions = [];
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
      // classification in parallel — none depend on the others and
      // all are needed before the pane finishes rendering. Per-
      // section failure drops that one section; the rest of the
      // taxon still renders.
      const [nomen, vern, dist, cls] = await Promise.all([
        api.taxon.nomenHistory(requested).catch(() => ({ clusters: [] })),
        api.taxon.vernaculars(requested).catch(() => ({ items: [] })),
        api.taxon.distributions(requested).catch(() => ({ items: [] })),
        api.taxon.classification(requested).catch(() => ({ items: [] })),
      ]);
      if (this.taxonId !== requested) return;
      this._nomenHistory = nomen;
      this._vernaculars = vern.items || [];
      this._distributions = dist.items || [];
      this._classification = cls.items || [];
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
    this._createStep = 0;
    this._createError = "";
    this._createBusy = false;
    this._createParentID = parentID;
    this._createParentLabel = parentLabel;
    this._creating = true;
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
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
    this._creatingSynonymFor = null;
    this._creatingSynonymForName = "";
    this._createBasionymInline = null;
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
      // Preserve draft-only meta fields that gnparser knows nothing
      // about — synonym_status (set by _openCreateSynonym), and
      // basionym_name_id (set either by _openCreateSynonym's cluster-`+`
      // shortcut or by the curator picking an existing name in the
      // Original combination section of a prior step 1 render).
      if (this._createDraft.synonym_status) {
        merged.synonym_status = this._createDraft.synonym_status;
      }
      if (this._createDraft.basionym_name_id) {
        merged.basionym_name_id = this._createDraft.basionym_name_id;
      }
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
    // selection changes since.
    const parentLabel = this._createParentLabel || "(root)";
    const heading = this._creatingBasionymFor
      ? html`Add original combination for
          <em>${this._creatingBasionymForName}</em>`
      : this._creatingSynonymFor
        ? html`Add synonym of
            <em>${this._creatingSynonymForName}</em>`
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
          ? html`<div class="error" role="alert">${this._createError}</div>`
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
          ${this._createBusy ? "Parsing…" : "Next → preview"}
        </button>
        <button @click=${() => this._cancelCreate()}>Cancel</button>
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
          placeholder="Rank…"
          .source=${childRankSource(this._createChildRanks)}
          .resolver=${vocabResolver("rank")}
          .value=${d.rank || ""}
          @pick=${(e) => this._createFieldChange("rank", e.detail.id)}
        ></sfga-combobox>

        <label>Verbatim authorship</label>
        <input type="text" .value=${d.authorship || ""} @input=${set("authorship")} />
      </fieldset>

      ${this._creatingSynonymFor
        ? html`
            <fieldset>
              <legend>synonym type</legend>
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
        Show atomized fields
        <span class="hint">
          (verify or override the parse; hive parses in the background
          regardless)
        </span>
      </label>

      ${this._createShowAtomized ? this._renderAtomizedFieldset(d, set) : ""}

      <fieldset>
        <legend>publication</legend>
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
          @pick=${(e) => this._createFieldChange("reference_id", e.detail.id)}
        ></sfga-combobox>
        <label>Published in page</label>
        <input type="text" .value=${d.published_in_page || ""} @input=${set("published_in_page")} />
      </fieldset>

      <fieldset>
        <legend>metadata</legend>
        <label>Nom status</label>
        <sfga-combobox
          placeholder="Nomenclatural status…"
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
          placeholder="Nomenclatural code…"
          .source=${vocabSource("nom_code")}
          .resolver=${vocabResolver("nom_code")}
          .value=${d.code || ""}
          @pick=${(e) => this._createFieldChange("code", e.detail.id)}
        ></sfga-combobox>
      </fieldset>

      ${this._creatingBasionymFor
        ? ""
        : this._renderOriginalCombinationSection(d, set)}

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
              : this._creatingSynonymFor
                ? "add synonym"
                : "create"}
        </button>
        <button @click=${() => this._cancelCreate()}>Cancel</button>
      </div>
      ${this._addingReferenceFor ? this._renderAddReferenceModal() : ""}
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
        <legend>original combination (optional)</legend>
        ${inline
          ? html`
              <p class="hint" style="grid-column: 1 / -1; margin: 0 0 var(--sp-1) 0;">
                Creating a new original combination inline. Both records
                save together.
              </p>
              <label>Scientific name <span class="req">*</span></label>
              <input
                type="text"
                placeholder="e.g. Aus bus L."
                .value=${bDraft.scientific_name || ""}
                @input=${bSet("scientific_name")}
              />
              <label>Verbatim authorship</label>
              <input
                type="text"
                .value=${bDraft.authorship || ""}
                @input=${bSet("authorship")}
              />
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
              <label>Original nomenclatural act citation</label>
              <sfga-combobox
                min-search-chars="2"
                placeholder="Search author / title / citation / DOI…"
                .source=${referenceSource}
                .resolver=${referenceResolver}
                .value=${bDraft.reference_id || ""}
                @pick=${(e) =>
                  this._createBasionymInlineFieldChange(
                    "reference_id",
                    e.detail.id,
                  )}
              ></sfga-combobox>
              <label>Remarks</label>
              <input
                type="text"
                .value=${bDraft.remarks || ""}
                @input=${bSet("remarks")}
              />
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
                @pick=${(e) =>
                  this._createFieldChange("basionym_name_id", e.detail.id)}
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

  // _openCreateBasionymInline expands the inline basionym subform,
  // seeding it with the primary draft's code so the common ICN/ICZN
  // case doesn't need an extra pick. Clears any picker selection —
  // the two paths are mutually exclusive.
  _openCreateBasionymInline() {
    this._createBasionymInline = {
      draft: { code: this._createDraft?.code || "" },
    };
    // Ensure the picker's basionym_name_id doesn't also submit.
    this._createFieldChange("basionym_name_id", "");
  }

  _cancelCreateBasionymInline() {
    this._createBasionymInline = null;
  }

  _createBasionymInlineFieldChange(field, value) {
    if (!this._createBasionymInline) return;
    this._createBasionymInline = {
      ...this._createBasionymInline,
      draft: { ...this._createBasionymInline.draft, [field]: value },
    };
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
      this._addingReferenceFor === "distribution";
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
    // Modal supplies {id, label}. Three call sites:
    //   * "create" — new-taxon create pane picked / added a reference
    //   * "edit"   — taxon edit form picked / added a reference
    //   * "section" — References section's + button; no form to route
    //     the pick into, just close (the reference is already in the
    //     archive; it'll appear in the References section once some
    //     row on this page cites it).
    // Legacy callers with an unrecognised target fall through to the
    // edit-form routing so a stale caller doesn't drop the pick on
    // the floor.
    const { id, label } = e.detail;
    const target = this._addingReferenceFor;
    if (target === "create") {
      this._createFieldChange("reference_id", id);
      this._pickedCreateReferenceLabel = label || "";
    } else if (target === "vernacular") {
      // Pick flows into the vernacular add/edit modal's draft. The
      // combobox will resolve the display label from the id on its
      // own; no need to shortcut a label cache for this flow.
      this._vernacularFieldChange("reference_id", id);
    } else if (target === "distribution") {
      this._distributionFieldChange("reference_id", id);
    } else if (target === "nameEditor") {
      // Pick flows into the pencil-edit name editor's draft.
      this._nameEditorFieldChange("reference_id", id);
    } else if (target !== "section") {
      this._nameFieldChange("reference_id", id);
      this._pickedReferenceLabel = label || "";
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
      <div class="modal-backdrop" @click=${() => this._cancelDelete()}>
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
    return html`
      <div class="detail-header">
        <h2>${heading}</h2>
      </div>
      <hr />
      ${this._renderBreadcrumbs()}
      ${this._renderPendingWarnings()}
      ${this._editing
        ? this._renderEditForm()
        : html`
            ${this._renderNomenclaturalHistory()}
            ${this._renderVernaculars()}
            ${this._renderDistributions()}
            ${this._renderReferences()}
            ${this._renderViewFields()}
          `}
      ${this._vernacularForm ? this._renderVernacularModal() : ""}
      ${this._distributionForm ? this._renderDistributionModal() : ""}
      ${this._synonymDelete ? this._renderSynonymDeleteModal() : ""}
      ${this._nameEditor ? this._renderNameEditorModal() : ""}
      ${this._addingReferenceFor === "section" ||
      this._addingReferenceFor === "vernacular" ||
      this._addingReferenceFor === "distribution" ||
      this._addingReferenceFor === "nameEditor"
        ? this._renderAddReferenceModal()
        : ""}
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
    if (this._editing || this._creating) return "";
    if (!this._taxon) return "";
    return html`
      <button
        class="icon-btn subtle"
        @click=${() => this._startEdit()}
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
      case "edit": this._startEdit(); break;
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
          placeholder="Type to search, or leave empty for a top-level taxon"
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
            ${this._saving ? "Saving…" : "Save"}
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
    // Merge the current name + any in-flight draft so nomActFieldCopy
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
        placeholder="Verbatim string with authorship"
        .value=${this._nameFieldValue("scientific_name_string")}
        @input=${(e) => this._nameFieldChange("scientific_name_string", e.target.value)}
      />

      <label>Rank</label>
      <sfga-combobox
        placeholder="Rank…"
        .source=${vocabSource("rank")}
        .resolver=${vocabResolver("rank")}
        .value=${this._nameFieldValue("rank")}
        @pick=${(e) => this._nameFieldChange("rank", e.detail.id)}
      ></sfga-combobox>

      <label>Code</label>
      <sfga-combobox
        placeholder="Nomenclatural code…"
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
        Show atomized fields
        <span class="hint">
          (verify or override the parse — hive parses in the background
          regardless)
        </span>
      </label>

      ${this._editShowAtomized ? this._renderAtomizedEditFields() : ""}

      <label>Nom status</label>
      <sfga-combobox
        placeholder="Nomenclatural status…"
        .source=${nomenSource(this._nameFieldValue("code"))}
        .resolver=${nomenResolver}
        .value=${this._nameFieldValue("status")}
        @pick=${(e) => this._nameFieldChange("status", e.detail.id)}
      ></sfga-combobox>

      <label>${nomActFieldLabel(merged)}</label>
      <sfga-combobox
        min-search-chars="2"
        placeholder="Search references…"
        .source=${referenceSource}
        .resolver=${referenceResolver}
        .value=${this._nameFieldValue("reference_id")}
        .valueName=${this._pickedReferenceLabel !== undefined
          ? this._pickedReferenceLabel
          : Object.hasOwn(this._nameDraft, "reference_id")
            ? ""
            : this._name?.reference_label || ""}
        .actions=${[
          {
            label: "Add new reference…",
            icon: "plus",
            handler: () => (this._addingReferenceFor = "edit"),
          },
        ]}
        @pick=${(e) => this._nameFieldChange("reference_id", e.detail.id)}
      ></sfga-combobox>
      ${this._addingReferenceFor ? this._renderAddReferenceModal() : ""}

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
    this._createStep = 0;
    this._createError = "";
    this._createBusy = false;
    // Parent doesn't apply to synonym mode; clear so the render's
    // parent-picker code path is bypassed cleanly.
    this._createParentID = "";
    this._createParentLabel = "";
    this._createChildRanks = [];
    this._creatingBasionymFor = null;
    this._creatingBasionymForName = "";
    this._creatingSynonymFor = taxonID;
    this._creatingSynonymForName = taxonLabel;
    this._creating = true;
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
        ${isAcceptedName ? "" : this._renderNomenRowActions(n)}
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

  // _renderNomenRowActions draws the hover-reveal action strip on a
  // synonym / basionym row: warning icon (when the name has open
  // validation issues), pencil (open name editor), delete (remove the
  // synonym link). Accepted-name rows skip this — the accepted taxon
  // has its own edit/delete in the app header. See DESIGN.md § List-row
  // actions.
  //
  // Placeholder wiring for now: each button dispatches a bubbling event
  // (edit-name / delete-synonym / open-name-issues) with the relevant
  // ids in detail. No parent listens today — the actual behaviours
  // require:
  //   * name-editor UI (standalone edit form for a name row)
  //   * DELETE /api/synonym/{id} endpoint
  //   * per-name issue-count field on apiNomenName so the warning
  //     icon knows when to render
  // See DEFERRED.md when those follow-up slices land.
  _renderNomenRowActions(n) {
    const hasIssues = (n.issue_count || 0) > 0;
    // The + button surfaces on cluster-anchor rows (is_basionym:
    // true) as a shortcut for "add another subsequent combination
    // sharing this original combination." Opens the standard
    // create-synonym form with basionym_name_id pre-populated to
    // this row's name_id, so the curator skips the picker step.
    // Placed first because on a cluster anchor, "add another
    // recomb" is often why the curator hovered the row in the first
    // place.
    const isClusterAnchor = !!n.is_basionym;
    return html`
      <span class="row-actions">
        ${isClusterAnchor
          ? html`<button
              class="icon-btn subtle"
              @click=${(e) => this._onNomenRowAddRecomb(e, n)}
              title="add subsequent combination of this original combination"
              aria-label="add subsequent combination"
            >
              ${renderIcon("plus", 14)}
            </button>`
          : ""}
        ${hasIssues
          ? html`<button
              class="icon-btn subtle warn"
              @click=${(e) => this._onNomenRowIssues(e, n)}
              title="open name editor at validation issues"
              aria-label="validation issues on this name"
            >
              ${renderIcon("triangle-alert", 14)}
            </button>`
          : ""}
        <button
          class="icon-btn subtle"
          @click=${(e) => this._onNomenRowEdit(e, n)}
          title="edit name"
          aria-label="edit name"
        >
          ${renderIcon("pencil", 14)}
        </button>
        <button
          class="icon-btn subtle danger"
          @click=${(e) => this._onNomenRowDelete(e, n)}
          title="delete synonym"
          aria-label="delete synonym"
          ?disabled=${!n.synonym_id}
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
    // Pass the synonym context (id + current accepted-taxon id) so the
    // modal can render an accepted-taxon combobox for the "fix the
    // mis-linked synonym" flow. If the row has no synonym_id (rare
    // for a Nomen History row but possible on the accepted-name row
    // if we ever surface actions there), the modal falls back to a
    // name-only editor.
    this._openNameEditor(n.name_id, {
      synonymID: n.synonym_id || "",
      currentTaxonID: this.taxonId || "",
    });
  }

  // _openNameEditor loads the name row and opens the editor modal.
  // Two-phase: "loading" while api.name.get is in-flight, then
  // "edit" once the row is hydrated. Draft starts empty — the
  // form's field values fall through to `original` until the curator
  // touches something; only fields the curator actually changed go
  // into the PATCH body.
  //
  // `ctx` (optional) carries synonym context so the modal can render
  // the accepted-taxon combobox for the move-synonym flow:
  //   { synonymID, currentTaxonID }
  async _openNameEditor(id, ctx = {}) {
    this._nameEditor = {
      phase: "loading",
      id,
      synonymID: ctx.synonymID || "",
      currentTaxonID: ctx.currentTaxonID || "",
      newTaxonID: ctx.currentTaxonID || "",
      original: null,
      draft: {},
      etag: "",
      issues: [],
      busy: false,
      error: "",
    };
    try {
      // Fetch the name row + any open validation issues on it in
      // parallel. Issues render as a banner at the top of the modal
      // so the curator sees the problem before editing — same shape
      // as _renderPendingWarnings. Issue-fetch failure is silent (the
      // banner just won't render) so a transient issue-endpoint hiccup
      // doesn't block editing.
      const [name, issueResp] = await Promise.all([
        api.name.get(id),
        api.issue
          .list({ table: "name", record_id: id, limit: 100 })
          .catch(() => ({ items: [] })),
      ]);
      if (!this._nameEditor || this._nameEditor.id !== id) return;
      this._nameEditor = {
        ...this._nameEditor,
        phase: "edit",
        original: name,
        etag: name.__etag || "",
        issues: issueResp.items || [],
      };
    } catch (err) {
      if (!this._nameEditor || this._nameEditor.id !== id) return;
      this._nameEditor = {
        ...this._nameEditor,
        phase: "edit",
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  _cancelNameEditor() {
    this._nameEditor = null;
  }

  _nameEditorFieldChange(field, value) {
    if (!this._nameEditor) return;
    this._nameEditor = {
      ...this._nameEditor,
      draft: { ...this._nameEditor.draft, [field]: value },
    };
  }

  _nameEditorValue(field) {
    const ed = this._nameEditor;
    if (!ed) return "";
    if (Object.hasOwn(ed.draft, field)) return ed.draft[field];
    return ed.original?.[field] ?? "";
  }

  async _submitNameEditor() {
    const ed = this._nameEditor;
    if (!ed || !ed.original) return;
    const patch = { ...ed.draft };
    const nameChanged = Object.keys(patch).length > 0;
    const taxonChanged =
      ed.synonymID &&
      ed.newTaxonID &&
      ed.newTaxonID !== ed.currentTaxonID;
    if (!nameChanged && !taxonChanged) {
      this._nameEditor = null;
      return;
    }
    this._nameEditor = { ...ed, busy: true, error: "" };
    try {
      // Move first, then name-patch. Order chosen so the curator's
      // subsequent nav decision (below) is based on the completed
      // move state; a name-patch failure after a successful move
      // still leaves the synonym at the intended taxon.
      if (taxonChanged) {
        await api.synonym.move(ed.synonymID, ed.newTaxonID);
      }
      if (nameChanged) {
        await api.name.patch(ed.id, patch, ed.etag);
      }
      const moved = taxonChanged;
      const newTaxon = ed.newTaxonID;
      this._nameEditor = null;
      if (moved) {
        // The synonym is no longer attached to the taxon the curator
        // was viewing — jump to the new accepted taxon so the moved
        // synonym is still in view.
        this.dispatchEvent(
          new CustomEvent("taxon-moved", {
            detail: { id: newTaxon },
            bubbles: true,
            composed: true,
          }),
        );
      } else {
        // Name changed only — refetch the section so the label updates
        // in place.
        await this._refreshNomenHistory();
      }
    } catch (err) {
      this._nameEditor = {
        ...this._nameEditor,
        busy: false,
        error:
          err instanceof Problem
            ? `${err.title}: ${err.detail || err.message}`
            : String(err),
      };
    }
  }

  _renderNameEditorModal() {
    const ed = this._nameEditor;
    if (!ed) return "";
    if (ed.phase === "loading") {
      return html`
        <div class="modal-backdrop" @click=${(e) => e.stopPropagation()}>
          <div
            class="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="name-editor-loading"
          >
            <h3 id="name-editor-loading">Edit name</h3>
            <p role="status">Loading name…</p>
          </div>
        </div>
      `;
    }
    const set = (field) => (e) =>
      this._nameEditorFieldChange(field, e.target.value);
    const referenceID = this._nameEditorValue("reference_id");
    // Dynamic label / hint for the reference field. The name-model
    // reference is single-valued and is meant to be the nomenclatural
    // act — the paper where this specific name (or subsequent
    // combination) was established. Prefix Original / Subsequent from
    // the authorship's parenthetical shape so curators authoring a
    // recomb see the right guidance without having to guess.
    // Build a draft-shaped object from the editor state so
    // nomActFieldLabel sees the effective values (draft override on
    // top of the loaded original).
    const draftView = {
      authorship: this._nameEditorValue("authorship"),
      basionym_authorship: this._nameEditorValue("basionym_authorship"),
      basionym_authorship_year: this._nameEditorValue(
        "basionym_authorship_year",
      ),
      combination_authorship: this._nameEditorValue("combination_authorship"),
      combination_authorship_year: this._nameEditorValue(
        "combination_authorship_year",
      ),
    };
    const nomActLabel = nomActFieldLabel(draftView);
    return html`
      <div class="modal-backdrop" @click=${(e) => e.stopPropagation()}>
        <div
          class="modal name-editor"
          role="dialog"
          aria-modal="true"
          aria-labelledby="name-editor-heading"
          @keydown=${(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              this._cancelNameEditor();
            }
          }}
        >
          <div class="modal-header">
            <h3 id="name-editor-heading">
              ${ed.synonymID ? "Edit synonym" : "Edit name"}
            </h3>
            <button
              class="close-x"
              type="button"
              @click=${() => this._cancelNameEditor()}
              title="close"
              aria-label="close"
            >
              ×
            </button>
          </div>
          ${ed.issues && ed.issues.length > 0
            ? html`
                <div class="warning-banner">
                  <strong>
                    ${ed.issues.length} open
                    issue${ed.issues.length > 1 ? "s" : ""} on this name:
                  </strong>
                  <ul>
                    ${ed.issues.map(
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
          ${ed.error
            ? html`<div class="error" role="alert">${ed.error}</div>`
            : ""}
          <form
            class="name-editor-form"
            @submit=${(e) => {
              e.preventDefault();
              this._submitNameEditor();
            }}
          >
            ${ed.synonymID
              ? html`
                  <label>Accepted taxon</label>
                  <sfga-combobox
                    min-search-chars="2"
                    placeholder="Search taxa…"
                    .source=${taxonSource}
                    .resolver=${taxonResolver}
                    .value=${ed.newTaxonID}
                    @pick=${(e) =>
                      (this._nameEditor = {
                        ...this._nameEditor,
                        newTaxonID: e.detail.id || "",
                      })}
                  ></sfga-combobox>
                `
              : ""}

            <label for="ne-sci">Scientific name</label>
            <input
              id="ne-sci"
              type="text"
              .value=${this._nameEditorValue("scientific_name")}
              @input=${set("scientific_name")}
              autofocus
            />

            <label for="ne-auth">Authorship</label>
            <input
              id="ne-auth"
              type="text"
              .value=${this._nameEditorValue("authorship")}
              @input=${set("authorship")}
            />

            <label>Rank</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Rank…"
              .source=${vocabSource("rank")}
              .resolver=${vocabResolver("rank")}
              .value=${this._nameEditorValue("rank")}
              @pick=${(e) =>
                this._nameEditorFieldChange("rank", e.detail.id)}
            ></sfga-combobox>

            <label>Code</label>
            <sfga-combobox
              min-search-chars="0"
              placeholder="Nomenclatural code…"
              .source=${vocabSource("nom_code")}
              .resolver=${vocabResolver("nom_code")}
              .value=${this._nameEditorValue("code")}
              @pick=${(e) =>
                this._nameEditorFieldChange("code", e.detail.id)}
            ></sfga-combobox>

            <label>${nomActLabel}</label>
            <sfga-combobox
              min-search-chars="2"
              placeholder="Search author / title / citation / DOI…"
              .source=${referenceSource}
              .resolver=${referenceResolver}
              .value=${referenceID}
              .actions=${[
                {
                  label: "Add new reference",
                  icon: "plus",
                  handler: () => (this._addingReferenceFor = "nameEditor"),
                },
              ]}
              @pick=${(e) =>
                this._nameEditorFieldChange("reference_id", e.detail.id)}
            ></sfga-combobox>

            <label for="ne-year">Published year</label>
            <input
              id="ne-year"
              type="text"
              placeholder="YYYY"
              .value=${this._nameEditorValue("published_in_year")}
              @input=${set("published_in_year")}
            />

            <label for="ne-remarks">Remarks</label>
            <textarea
              id="ne-remarks"
              rows="2"
              .value=${this._nameEditorValue("remarks")}
              @input=${set("remarks")}
            ></textarea>

            <div class="toolbar">
              <button
                type="button"
                @click=${() => this._cancelNameEditor()}
                ?disabled=${ed.busy}
              >
                Cancel
              </button>
              <button
                type="submit"
                class="primary"
                ?disabled=${ed.busy || !ed.original}
              >
                ${ed.busy ? "Saving…" : "Save changes"}
              </button>
            </div>
          </form>
        </div>
      </div>
    `;
  }

  _onNomenRowIssues(e, n) {
    // Same target as the pencil — the editor modal fetches and
    // renders open issues at the top, so opening it "on the issues"
    // is just opening it.
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
        <div class="modal-backdrop" @click=${(e) => e.stopPropagation()}>
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
      <div class="modal-backdrop" @click=${(e) => e.stopPropagation()}>
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
              ? html`<button
                  class="icon-btn subtle warn"
                  @click=${() => this._openVernacularEdit(v)}
                  title="open editor at validation issues"
                  aria-label="validation issues on this vernacular"
                >
                  ${renderIcon("triangle-alert", 14)}
                </button>`
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
        class="modal-backdrop"
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
              ? html`<button
                  class="icon-btn subtle warn"
                  @click=${() => this._openDistributionEdit(d)}
                  title="open editor at validation issues"
                  aria-label="validation issues on this distribution"
                >
                  ${renderIcon("triangle-alert", 14)}
                </button>`
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
      <div class="modal-backdrop" @click=${(e) => e.stopPropagation()}>
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
    // Values: "project" | "bhlnames" | "doi" | "bibtex". Callers with
    // a name context (curator most likely adding the protologue paper)
    // typically want "project"; non-name contexts (vernacular,
    // distribution, section-level References) prefer "doi" since the
    // curator usually has a paper reference in hand. See DESIGN.md
    // § Reference-picker on every data-entry form.
    defaultTab: { attribute: false },
    _tab: { state: true }, // 0..3
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
    _preview: { state: true },
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
        background: color-mix(in oklab, var(--bg) 60%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
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
  }

  connectedCallback() {
    super.connectedCallback();
    // Land on the caller-requested tab (name-editor callers keep the
    // project search default; non-name contexts prefer DOI).
    this._tab = SfgaAddReferenceModal._tabIndex(this.defaultTab);
  }

  static _tabIndex(name) {
    switch (name) {
      case "bhlnames":
        return 1;
      case "doi":
        return 2;
      case "bibtex":
        return 3;
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
  }

  connectedCallback() {
    super.connectedCallback();
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
  }

  firstUpdated() {
    // Trap focus inside the modal and land initial focus in the first
    // input of the active tab (project search is the default tab).
    this._releaseFocus = trapFocus(this.renderRoot, {
      initialFocus: "input, textarea",
    });
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
    return html`
      <div class="backdrop">
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="add-ref-title"
        >
          <div class="modal-header">
            <h3 id="add-ref-title">Add reference</h3>
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
              <button @click=${() => (this._preview = h.reference)}>Preview</button>
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
          <button @click=${() => (this._preview = null)}>Discard</button>
          <button class="primary" @click=${() => this._createAndPick(r)}>
            ${this._busy ? "Saving…" : "Add & pick"}
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
          </li>
        `;
      });
    }
    const dropdown = html`${filterRow}${actionRows}${resultRows}`;

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
    _hits: { state: true },
    _selectedId: { state: true },
    _current: { state: true },
    _loading: { state: true },
    _error: { state: true },
  };

  static styles = css`
    :host {
      display: grid;
      /* Matches the taxa screen's 50/50 split — reference list left,
         detail right — so the two primary editing surfaces feel
         consistent when a curator moves between screens. */
      grid-template-columns: minmax(18rem, 1fr) 1fr;
      overflow: hidden;
      height: 100%;
    }
    aside,
    section {
      overflow: auto;
      padding: var(--sp-2);
    }
    aside {
      border-right: 1px solid var(--border);
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
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
    .empty { color: var(--dim); padding: var(--sp-2); }
    .error { color: var(--error); font-family: var(--font-mono); padding: var(--sp-2); }
  `;

  constructor() {
    super();
    this.selectId = "";
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
      // Prefer the caller-requested id (deep-link from an Issues
      // click); fall back to the first hit. `_select` fetches by id
      // directly, so a requested id that isn't in the first-page
      // list still resolves — it just won't be highlighted in the
      // sidebar until the curator scrolls or filters to it.
      if (this.selectId) {
        this._select(this.selectId);
      } else if (this._hits.length > 0) {
        this._select(this._hits[0].id);
      }
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    } finally {
      this._loading = false;
    }
  }

  // Post-mount: if the shell hands us a new `selectId` (e.g. curator
  // clicked another reference-scoped issue while already on this
  // screen), honor it. Ignored during initial load — that path runs
  // through connectedCallback.
  updated(changed) {
    if (changed.has("selectId") && this.selectId && this.selectId !== this._selectedId) {
      this._select(this.selectId);
    }
  }

  async _select(id) {
    if (!id || id === this._selectedId) return;
    this._selectedId = id;
    this._current = null;
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
    } catch (err) {
      this._error = err instanceof Problem ? err.detail || err.title : String(err);
    }
  }

  render() {
    if (this._loading && this._hits.length === 0) {
      return html`<div class="empty" role="status">Loading…</div>`;
    }
    if (this._error) {
      return html`<div class="error" role="alert">${this._error}</div>`;
    }
    if (this._hits.length === 0) {
      return html`<div class="empty">(No references)</div>`;
    }
    return html`
      <aside>
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
      </aside>
      <section>${this._renderDetail()}</section>
    `;
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

  static styles = [
    buttonStyles,
    css`
      :host {
        display: block;
      }
      .backdrop {
        position: fixed;
        inset: 0;
        background: color-mix(in oklab, var(--bg) 60%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
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

  disconnectedCallback() {
    super.disconnectedCallback();
    if (this._releaseFocus) {
      this._releaseFocus();
      this._releaseFocus = null;
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
        background: color-mix(in oklab, var(--bg) 60%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-modal-backdrop);
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
    return html`
      <div class="backdrop">
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
        background: color-mix(in oklab, var(--bg) 60%, transparent);
        display: grid;
        place-items: center;
        z-index: var(--z-popover);
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
    return html`
      <div class="backdrop">
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
