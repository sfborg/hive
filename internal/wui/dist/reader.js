/**
 * hive WUI — augmented-JATS reader.
 *
 * Standalone page that renders the TaxPub-augmented JATS produced
 * by pkg/annotator for a given PDF upload. Curator opens the reader
 * either by navigating to /read.html?sha=... directly or via a
 * popout from a reference detail view. Popout is the multi-monitor
 * use case — a separate browser window lets curators park the
 * reader on one screen and their curation UI on another.
 *
 * Rendering: parses the augmented XML into a DOM, walks the
 * <article><body> subtree (plus <back> for the reference list), and
 * emits readable HTML with each <tp:taxon-name> element rendered
 * as a styled chip. Chip color (green / amber / orange / red)
 * reflects the gn:source attribute from the annotator's
 * abbreviation-expansion pass — verified, ambiguous, unverified,
 * unresolved.
 *
 * Interactions:
 *   - "Expand abbreviations" toggle swaps abbreviated chip text
 *     for the resolved full-name attribute across the whole
 *     document. Reader-side only; underlying XML unchanged.
 *   - Chip tooltip shows the resolved full-name plus a status hint.
 *
 * No sfga-app dependency — this page is intentionally decoupled so
 * a curator can pop it out into another window / monitor without
 * dragging the main SPA along.
 */

import { LitElement, html, css, nothing, svg } from "/vendor/lit-3.x.x.min.js";

// TaxPub + GN + hive namespaces from pkg/annotator and pkg/jatsconv.
// Kept in sync manually since Go and JS can't share constants — if
// these ever change, update both places.
//
// gn:   GN-toolchain provenance on tp:taxon-name (source, score,
//       candidates). Populated by ExpandAbbreviations / verify pass.
// hive: hive-native annotations that aren't GN-scoped. Currently
//       pdf-page + pdf-bbox on <fig> / <table-wrap>.
const TAXPUB_NS = "http://exsansi.pensoft.net/pub-med/pub-med-tp/1.0";
const GN_NS = "https://hive.sfborg.org/ns/gn/1.0";
const HIVE_NS = "https://hive.sfborg.org/ns/1.0";

// Lucide "pencil" — same glyph the shell uses for its edit buttons
// (see renderIcon("pencil", …) in app.js). Inlined here so the
// reader stays a standalone module without importing from the shell.
// currentColor lets the CSS context pick up light/dark theme accent.
function pencilIcon(size = 14) {
  return svg`<svg
    xmlns="http://www.w3.org/2000/svg"
    width=${size}
    height=${size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z" />
    <path d="m15 5 4 4" />
  </svg>`;
}

class SfgaReader extends LitElement {
  static properties = {
    _busy: { state: true },
    _error: { state: true },
    _doc: { state: true },
    _sha: { state: true },
    _showExpanded: { state: true },
    // Side-by-side PDF pane: when enabled, the PDF loads on the
    // left half of the viewport and the reader text on the right,
    // so a curator can eyeball the source next to the augmented
    // text (verify a chip's expansion, check for content GROBID
    // dropped, spot-check the reading order).
    _showPDF: { state: true },
    // Print layout: multi-column paginated mode where the article
    // fills the viewport and turns pages horizontally (arrow /
    // space / click-to-turn) instead of scrolling. Multi-column
    // matches the density of a print journal and doesn't suffer
    // the "scroll back up to find column-2's start" problem
    // because each viewport holds one full spread. Mutually
    // exclusive with _showPDF — the PDF pane preempts the
    // paginated layout.
    _paginated: { state: true },
    _page: { state: true },
    _pageCount: { state: true },
    // ID of the currently-open figure/table modal, or "" when
    // closed. Set when a curator clicks a fig-xref inline or a
    // list entry in the trailing "Figures and tables" sec; the
    // modal renders the figure's caption + extracted PNG (or the
    // full table structure) + a link back to the source PDF page.
    _modalFigID: { state: true },
    // Which modal tab is active — "digitized" (structured HTML,
    // e.g. rendered table) or "original" (extracted PDF crop
    // image). Resets when the modal opens; default depends on
    // what's available (tables prefer Digitized, figures fall
    // back to Original since there's no structured view yet).
    _modalTab: { state: true },
    // When false, the extraction-warning banner is collapsed to a
    // one-line hint the curator can re-expand. Default true so
    // curators see extraction gaps on load; hide-away is opt-in
    // for readers who want the reading pane back at full height.
    _showExtractionWarning: { state: true },
    _showRefWarning: { state: true },
    // _activeTool tracks the correction tool the shell has activated
    // via hive:set-tool postMessage. "read" (or empty) means no
    // interactive mode — the reader is a passive viewer. "text-
    // remove" toggles hover-highlight + click-to-remove on body
    // paragraphs. Standalone reader (no parent shell) stays "read".
    _activeTool: { state: true },
  };

  static styles = css`
    :host {
      /* Flex column so the paginated article can flex-fill the
         remaining height below the header regardless of how many
         rows the header wraps into. calc(100vh - Nrem) was
         fragile — the header actually varies with controls and
         window width, causing pages to overflow by ~1-2 lines
         and forcing intra-page scroll. */
      display: flex;
      flex-direction: column;
      min-height: 100vh;
      height: 100vh;
      background: var(--bg);
      color: var(--fg);
      font-family: var(--font-body);
      overflow: hidden;
    }
    :host > header {
      flex: 0 0 auto;
    }
    /* Scope the sticky-nav layout to the top-level <header> —
       without :host >, this rule also hit <header class="paper-
       header"> inside the article and turned that block into a
       horizontal flex row. */
    :host > header {
      position: sticky;
      top: 0;
      background: var(--bg);
      border-bottom: 1px solid var(--border);
      padding: var(--sp-2) var(--sp-4);
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: var(--sp-2);
      z-index: 1;
      flex-wrap: wrap;
    }
    :host > header h1 {
      margin: 0;
      font-size: var(--fs-md);
      color: var(--dim);
      font-weight: 400;
      font-family: var(--font-mono);
    }
    .controls {
      display: flex;
      gap: var(--sp-2);
      align-items: center;
    }
    .controls label {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      cursor: pointer;
      font-size: var(--fs-sm);
      color: var(--dim);
    }
    /* Show PDF replaces the reader entirely rather than splitting.
       Curator toggles between the two views; when they want the
       PDF alongside the augmented text (fact-checking flow), the
       full-screen reference editor is where that side-by-side
       affordance lives. Keeps this reader focused on one thing
       at a time — matches the intent of a reading surface. */
    .layout {
      display: block;
      /* Non-paginated (scrolling) mode fills remaining vertical
         space below the header and scrolls vertically. Curator
         reads by scrolling down like a normal document; the
         paginated mode below overrides these for its horizontal
         page-turn behavior. */
      flex: 1;
      min-height: 0;
      overflow-y: auto;
    }
    .pdf-full {
      width: 100%;
      height: calc(100vh - 3rem);
      border: 0;
      display: block;
    }
    /* Paginated mode: multi-column layout that fills the viewport
       and pages horizontally instead of scrolling. Solves the
       "scroll back up to find column 2's start" problem inherent
       to scrolling multi-column — each viewport is one complete
       spread. Column width caps at 32ch for readability; the
       browser packs as many columns as fit at that width. */
    .layout.paginated {
      flex: 1;
      min-height: 0;
      overflow: hidden;
      position: relative;
    }
    .layout.paginated article {
      height: 100%;
      max-width: none;
      margin: 0;
      padding: var(--sp-4) var(--sp-6);
      box-sizing: border-box;
      column-width: 32ch;
      column-gap: var(--sp-6);
      column-rule: 1px solid color-mix(in oklab, var(--border) 50%, transparent);
      transition: transform 0.25s ease;
      cursor: pointer;
    }
    /* Avoid mid-element breaks that would split a chip or a
       heading across columns / pages. */
    .layout.paginated h1,
    .layout.paginated h2,
    .layout.paginated .taxon-name,
    .layout.paginated .passthrough {
      break-inside: avoid;
    }
    .page-indicator {
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
      color: var(--dim);
    }
    article {
      max-width: 44rem;
      margin: 0 auto;
      padding: var(--sp-4) var(--sp-6) var(--sp-8);
      font-size: var(--fs-md);
      line-height: 1.6;
    }
    /* Paper header — title / authors / abstract. Centered so
       the paper's identity reads as a formal title block. Copy
       buttons for citation formats live in the top nav bar
       (.cite-toolbar), not here, so they stay accessible while
       the reader body scrolls without cluttering the paper's
       title area. */
    .paper-header {
      margin-bottom: var(--sp-4);
      padding-bottom: var(--sp-4);
      border-bottom: 1px solid var(--border);
      text-align: center;
    }
    .paper-title {
      font-size: 1.5rem;
      line-height: 1.3;
      margin: 0 0 var(--sp-2);
      font-weight: 600;
    }
    .paper-authors {
      margin: 0 0 var(--sp-3);
      color: var(--dim);
    }
    .paper-abstract {
      margin: 0;
      text-align: left;
    }
    .paper-abstract summary {
      cursor: pointer;
      font-weight: 500;
      padding: var(--sp-1) 0;
    }
    .paper-abstract p {
      margin: var(--sp-1) 0;
      color: var(--fg);
    }
    /* Citation copy toolbar in the sticky top nav — sits with
       the other reader controls, wraps to a new row when narrow. */
    .cite-toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: var(--sp-1);
    }
    .cite-toolbar button {
      font: inherit;
      font-size: var(--fs-sm);
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      cursor: pointer;
    }
    .cite-toolbar button:hover {
      border-color: var(--accent);
    }
    /* "Copied" feedback: accent border + accent text for ~1.5s. */
    .cite-toolbar button.copied {
      border-color: var(--accent);
      color: var(--accent);
    }
    article h2 {
      font-size: var(--fs-lg);
      margin-top: var(--sp-6);
      margin-bottom: var(--sp-2);
      color: var(--fg);
    }
    article p {
      margin: var(--sp-2) 0;
    }
    /* text-remove tool mode: body paragraphs become click-to-remove
       targets. Cursor + outline signal hover; a small delete glyph
       inline-left of the paragraph makes the target unambiguous.
       Skips the paper header, references list, figure captions, and
       already-wrapped hive:noise elements so curators can only click
       actual body paragraphs. */
    .tool-text-remove article :is(p, h1, h2, h3, h4, h5, h6):not(.page-break):hover {
      outline: 2px dashed color-mix(in oklab, var(--sev-warn) 60%, transparent);
      outline-offset: 2px;
      cursor: pointer;
      background: color-mix(in oklab, var(--sev-warn) 8%, transparent);
    }
    .tool-text-remove article :is(p, h1, h2, h3, h4, h5, h6):not(.page-break):hover::before {
      content: "✕ ";
      color: var(--sev-warn);
      font-weight: bold;
    }
    /* Paper header stays untouchable — metadata is not body noise. */
    .tool-text-remove article .paper-header :is(p, h1, h2, h3, h4, h5, h6):hover {
      outline: none;
      background: transparent;
      cursor: default;
    }
    .tool-text-remove article .paper-header :is(p, h1, h2, h3, h4, h5, h6):hover::before {
      content: "";
    }
    /* Inline marker for an applied text-remove correction. Compact
       red dot the curator can hover to see the removed text
       (native title tooltip) and click to restore. Kept small so it
       doesn't disrupt reading flow; interactive affordances only
       fire on the marker itself, not adjacent text. */
    .hive-noise-marker {
      display: inline-block;
      width: 1em;
      height: 1em;
      line-height: 1em;
      text-align: center;
      margin: 0 0.15em;
      padding: 0 0.1em;
      border-radius: 50%;
      background: var(--sev-err);
      color: var(--bg);
      font-weight: bold;
      cursor: pointer;
      vertical-align: middle;
      user-select: none;
    }
    .hive-noise-marker:hover,
    .hive-noise-marker:focus-visible {
      outline: 2px solid color-mix(in oklab, var(--sev-err) 40%, transparent);
      outline-offset: 2px;
    }
    /* Page-break divider — horizontal dashed line with the
       journal-labeled source-PDF page number sitting mid-line.
       Curators cite by that page for ZooBank / ICZN references.
       Uses two flex spacers with border-top around a page-num
       label so the label sits mid-line without escape. */
    article .page-break {
      display: flex;
      align-items: center;
      gap: var(--sp-2);
      margin: var(--sp-3) 0;
      color: var(--dim);
      font-size: var(--fs-xs);
      break-inside: avoid;
    }
    article .page-break::before,
    article .page-break::after {
      content: "";
      flex: 1;
      border-top: 1px dashed var(--border);
    }
    article .page-break-num {
      font-family: var(--font-mono);
      letter-spacing: 0.02em;
      background: var(--bg);
      padding: 0 var(--sp-2);
      border-radius: var(--radius-xs);
    }
    /* Fallback wrapper for JATS block elements without a specific
       renderer (fig, table-wrap, unknown). Gives content basic
       block layout so it doesn't collapse into an inline run beside
       siblings. Kept visually neutral — no borders or shading — so
       misclassified prose reads as prose. */
    article .passthrough {
      margin: var(--sp-2) 0;
    }
    article ul,
    article ol {
      margin: var(--sp-2) 0;
      padding-left: var(--sp-4);
    }
    article li {
      margin: var(--sp-1) 0;
    }
    /* References list — extra vertical space per entry so the
       author + title + source text that renders as an inline run
       per entry is still readable, and hanging indent so wrapped
       lines align to the text rather than the number. */
    article .references li {
      margin: var(--sp-2) 0;
      padding-left: var(--sp-1);
    }
    /* Inline reference citations ("(Doe 2020)"). Dimmer than body
       text so they read as parenthetical. */
    article .cite {
      color: var(--dim);
    }
    /* Figure xrefs are clickable — jump the PDF pane to the
       referenced page. Distinct-link styling (accent color, always
       underlined, bolder weight) rather than "invisible until
       hover" — curators should be able to scan a paragraph and
       immediately see which numeric spans are figure references
       vs. plain-text digits (mortality percentages, sample sizes,
       etc.). Cursor: pointer telegraphs the click affordance. */
    article .fig-xref,
    article .ref-xref {
      color: var(--accent);
      text-decoration: underline;
      text-decoration-thickness: 1px;
      text-underline-offset: 2px;
      font-weight: 500;
      cursor: pointer;
    }
    article .fig-xref:hover,
    article .ref-xref:hover {
      text-decoration-thickness: 2px;
    }
    article .fig-xref:focus-visible,
    article .ref-xref:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
      border-radius: 2px;
    }
    /* Ref-jump highlight — 1.5-second pulse on the target <li>
       after jumping so the reader visually confirms where the
       citation landed. Keyframes fade a background wash so text
       stays readable throughout. */
    article ol.references li.ref-flash {
      animation: refFlash 1.5s ease-out;
    }
    /* Trailing "[merged: …]" fragment appended to a <ref> when a
       curator merged a spurious sibling ref into it. Dimmed +
       italicized to read as annotation rather than structured
       citation data — the field boundaries were lost during
       GROBID's split, so we don't pretend otherwise. */
    article ol.references .ref-merged {
      color: var(--dim);
      font-style: italic;
    }
    /* Per-ref row layout: warning-icon gutter on the left, body in
       the middle, edit pencil pinned to the right on hover. Grid
       lives on an inner wrapper so the outer <li> keeps its
       list-item display and the ol's decimal marker still renders.
       Setting display: grid directly on the <li> would suppress
       the marker in every browser. */
    article ol.references li.ref-row {
      position: relative;
    }
    article ol.references .ref-row-inner {
      display: grid;
      grid-template-columns: 1.25rem 1fr auto;
      column-gap: var(--sp-1);
      align-items: baseline;
    }
    article ol.references .ref-warn-icon,
    article ol.references .ref-warn-icon-placeholder {
      display: inline-block;
      width: 1.25rem;
      text-align: center;
    }
    article ol.references .ref-warn-icon {
      color: var(--sev-warn);
      cursor: pointer;
      font-size: var(--fs-sm);
    }
    article ol.references .ref-warn-icon:hover {
      transform: scale(1.15);
    }
    article ol.references .ref-warn-icon:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
      border-radius: var(--radius-xs);
    }
    /* Edit pencil hidden until the row is hovered or focused; keeps
       the ref list from looking like a spreadsheet at rest. Sized
       so click target is comfortable but the icon doesn't shout. */
    article ol.references .ref-edit-pencil {
      background: none;
      border: 1px solid transparent;
      border-radius: var(--radius-xs);
      padding: 0 var(--sp-1);
      font-size: var(--fs-sm);
      color: var(--dim);
      cursor: pointer;
      opacity: 0;
      transition: opacity 120ms ease;
    }
    article ol.references .ref-row-inner:hover .ref-edit-pencil,
    article ol.references .ref-row-inner:focus-within .ref-edit-pencil {
      opacity: 1;
    }
    article ol.references .ref-edit-pencil:hover {
      color: var(--fg);
      border-color: var(--border);
      background: color-mix(in oklab, var(--fg) 4%, transparent);
    }
    @keyframes refFlash {
      0% { background: color-mix(in srgb, var(--accent) 35%, transparent); }
      100% { background: transparent; }
    }
    /* Figure-jump affordance: a small "Open in PDF — page N" link
       above each figure's caption in the trailing "Figures" sec.
       The discovery-path click for curators who scroll to a
       figure and want to see the source page. Emphasis subtle so
       it doesn't fight the caption text below it. */
    article .fig-block {
      margin: var(--sp-3) 0;
    }
    article a.fig-jump {
      display: inline-block;
      font-size: var(--fs-sm);
      color: var(--accent);
      text-decoration: none;
      padding: var(--sp-1) 0;
      margin-bottom: var(--sp-1);
      cursor: pointer;
    }
    article a.fig-jump:hover {
      text-decoration: underline;
    }
    /* Trailing "Figures and tables" list — compact clickable
       entries, one per figure/table. Modal is where the actual
       content lives; the list is just discoverability. */
    article .figures-list ol {
      list-style: none;
      padding: 0;
      margin: var(--sp-2) 0;
      display: flex;
      flex-direction: column;
      gap: var(--sp-1);
    }
    article .figures-list li {
      margin: 0;
    }
    article a.fig-list-link,
    article a.fig-inline-link {
      color: var(--accent);
      text-decoration: none;
      cursor: pointer;
      font-weight: 500;
    }
    article a.fig-list-link:hover,
    article a.fig-inline-link:hover {
      text-decoration: underline;
    }
    /* Figure/table modal. Full-screen backdrop dims the page;
       centered modal box scales to the viewport with scroll for
       overflow (tables can be wide, images may be tall). */
    .fig-modal-backdrop {
      position: fixed;
      inset: 0;
      background: color-mix(in oklab, var(--fg) 50%, transparent);
      display: flex;
      align-items: center;
      justify-content: center;
      padding: var(--sp-4);
      z-index: 100;
    }
    .fig-modal {
      background: var(--bg);
      color: var(--fg);
      border-radius: var(--radius-md);
      max-width: 90vw;
      max-height: 90vh;
      display: flex;
      flex-direction: column;
      overflow: hidden;
      box-shadow: 0 8px 32px color-mix(in oklab, var(--fg) 30%, transparent);
    }
    .fig-modal-header {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      padding: var(--sp-2) var(--sp-3);
      border-bottom: 1px solid var(--border);
      gap: var(--sp-2);
    }
    .fig-modal-header h2 {
      margin: 0;
      font-size: var(--fs-lg);
    }
    .fig-modal-title-group {
      display: flex;
      align-items: baseline;
      gap: var(--sp-2);
      flex: 1 1 auto;
      min-width: 0;
    }
    .fig-modal-header-page {
      color: color-mix(in oklab, var(--fg) 65%, var(--bg));
      text-decoration: none;
      font-size: var(--fs-sm);
      white-space: nowrap;
      display: inline-flex;
      align-items: center;
      gap: 4px;
    }
    .fig-modal-header-page:hover {
      color: var(--accent);
      text-decoration: underline;
    }
    .fig-modal-header-page svg {
      width: 12px;
      height: 12px;
      flex: 0 0 auto;
    }
    .fig-modal-close {
      background: transparent;
      border: 0;
      font-size: var(--fs-lg);
      cursor: pointer;
      color: var(--fg);
      padding: 0 var(--sp-1);
    }
    .fig-modal-tabs {
      display: flex;
      gap: var(--sp-1);
      padding: var(--sp-1) var(--sp-3);
      border-bottom: 1px solid var(--border);
    }
    .fig-modal-tab {
      background: transparent;
      border: 0;
      border-bottom: 2px solid transparent;
      color: color-mix(in oklab, var(--fg) 70%, var(--bg));
      padding: var(--sp-1) var(--sp-2);
      cursor: pointer;
      font: inherit;
      font-size: var(--fs-sm);
    }
    .fig-modal-tab.active {
      color: var(--accent);
      border-bottom-color: var(--accent);
    }
    .fig-modal-tab:hover:not(.active) {
      color: var(--fg);
    }
    .fig-modal-body {
      padding: var(--sp-3);
      overflow: auto;
      display: flex;
      flex-direction: column;
      gap: var(--sp-2);
    }
    .fig-modal-img {
      max-width: 100%;
      height: auto;
      border: 1px solid var(--border);
      align-self: center;
    }
    .fig-modal-table {
      overflow: auto;
    }
    .fig-modal-table table {
      border-collapse: collapse;
      width: 100%;
    }
    .fig-modal-table th,
    .fig-modal-table td {
      border: 1px solid var(--border);
      padding: var(--sp-1) var(--sp-2);
      text-align: left;
      vertical-align: top;
    }
    .fig-modal-table th {
      background: color-mix(in oklab, var(--fg) 5%, var(--bg));
      font-weight: 600;
    }
    .fig-modal-table td.num {
      text-align: right;
      font-variant-numeric: tabular-nums;
    }
    .fig-modal-caption {
      font-size: var(--fs-sm);
      color: color-mix(in oklab, var(--fg) 80%, var(--bg));
      line-height: 1.5;
    }
    /* Chip styling per gn:source. Verified is the confident state
       (accent-tinted); ambiguous / unverified sit in the middle
       (amber / orange); unresolved is red. Every full-form chip
       carries a gn:source — VerifyFullNames stamps verified and
       unwraps unverified — so there is no "no source" case to
       style. Abbreviations that don't resolve stay wrapped with
       gn:source="unresolved" (red). */
    .taxon-name {
      display: inline;
      padding: 0.05rem 0.3rem;
      border-radius: 3px;
      border: 1px solid transparent;
      cursor: help;
      font-style: italic;
    }
    .taxon-name.verified {
      background: color-mix(in oklab, var(--sev-info) 15%, transparent);
      border-color: color-mix(in oklab, var(--sev-info) 40%, transparent);
    }
    .taxon-name.ambiguous {
      background: color-mix(in oklab, var(--sev-warn) 15%, transparent);
      border-color: color-mix(in oklab, var(--sev-warn) 40%, transparent);
    }
    .taxon-name.unverified {
      background: color-mix(in oklab, var(--sev-warn) 15%, transparent);
      border-color: color-mix(in oklab, var(--sev-warn) 40%, transparent);
    }
    .taxon-name.unresolved {
      background: color-mix(in oklab, var(--sev-error) 15%, transparent);
      border-color: color-mix(in oklab, var(--sev-error) 40%, transparent);
    }
    .empty,
    .error {
      color: var(--dim);
      font-style: italic;
      padding: var(--sp-4);
      text-align: center;
    }
    .error {
      color: var(--error);
      font-style: normal;
      font-family: var(--font-mono);
    }
    .legend {
      display: flex;
      gap: var(--sp-2);
      font-size: var(--fs-xs);
      color: var(--dim);
      flex-wrap: wrap;
    }
    .legend .swatch {
      display: inline-flex;
      align-items: center;
      gap: 0.2rem;
    }
    .legend .dot {
      display: inline-block;
      width: 0.7rem;
      height: 0.7rem;
      border-radius: 2px;
    }
    .legend .dot.verified {
      background: color-mix(in oklab, var(--sev-info) 40%, transparent);
    }
    .legend .dot.ambiguous {
      background: color-mix(in oklab, var(--sev-warn) 40%, transparent);
    }
    .legend .dot.unresolved {
      background: color-mix(in oklab, var(--sev-error) 40%, transparent);
    }
    .extract-warn {
      background: color-mix(in oklab, var(--sev-warn) 15%, transparent);
      border: 1px solid color-mix(in oklab, var(--sev-warn) 40%, transparent);
      border-radius: var(--radius-sm);
      margin: var(--sp-2) var(--sp-3);
      padding: var(--sp-2) var(--sp-3);
      font-size: var(--fs-sm);
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: var(--sp-3);
    }
    .extract-warn.collapsed {
      padding: var(--sp-1) var(--sp-3);
      font-size: var(--fs-xs);
      color: var(--dim);
    }
    .extract-warn-body {
      flex: 1;
      min-width: 0;
    }
    .extract-warn-title {
      font-weight: 600;
      margin-bottom: var(--sp-1);
    }
    .extract-warn-list {
      margin: 0;
      padding: 0;
      list-style: none;
      display: flex;
      flex-wrap: wrap;
      gap: var(--sp-2);
    }
    .extract-warn-list li {
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: var(--radius-xs);
      padding: 0 var(--sp-1);
      font-family: var(--font-mono);
      font-size: var(--fs-xs);
    }
    .extract-warn-toggle {
      background: none;
      border: 1px solid var(--border);
      border-radius: var(--radius-xs);
      padding: 0 var(--sp-2);
      font-size: var(--fs-xs);
      cursor: pointer;
      color: var(--fg);
      flex-shrink: 0;
    }
    .extract-warn-toggle:hover {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
    /* Suspect-ref chips inside the ref-warning banner are
       clickable — clicking scrolls the ref list to the flagged
       entry (same _jumpToRef used by citation clicks). Cursor +
       accent underline on the leading id telegraph the affordance
       without cluttering the reason text. */
    .extract-warn-list li.suspect-ref {
      cursor: pointer;
      display: inline-flex;
      align-items: baseline;
      gap: var(--sp-1);
    }
    .extract-warn-list li.suspect-ref .suspect-ref-id {
      color: var(--accent);
      text-decoration: underline;
      text-decoration-thickness: 1px;
      text-underline-offset: 2px;
      font-weight: 500;
    }
    .extract-warn-list li.suspect-ref:hover .suspect-ref-id {
      text-decoration-thickness: 2px;
    }
    .extract-warn-list li.suspect-ref .suspect-ref-reason {
      color: var(--dim);
    }
    .extract-warn-list li.suspect-ref .suspect-ref-merge {
      background: none;
      border: 1px solid var(--border);
      border-radius: var(--radius-xs);
      padding: 0 var(--sp-1);
      font-size: var(--fs-xs);
      cursor: pointer;
      color: var(--fg);
    }
    .extract-warn-list li.suspect-ref .suspect-ref-merge:hover {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
    }
  `;

  constructor() {
    super();
    this._busy = false;
    this._error = "";
    this._doc = null;
    this._showExpanded = false;
    this._showPDF = false;
    // Columned view is on by default — journal-style multi-column
    // layout with paginated navigation. column-width naturally
    // collapses to a single column when the viewport is narrow or
    // the font is enlarged (accessibility), so this default doesn't
    // penalize readers who need larger text.
    this._paginated = true;
    this._page = 0;
    this._pageCount = 1;
    this._pageWidth = 0;
    this._showExtractionWarning = true;
    this._showRefWarning = true;
    this._activeTool = "read";
    const params = new URLSearchParams(location.search);
    this._sha = params.get("sha") || "";
  }

  connectedCallback() {
    super.connectedCallback();
    if (!this._sha) {
      this._error = "no ?sha= parameter — nothing to read";
      return;
    }
    this._load();
    // Keyboard navigation for paginated mode. Bound at the
    // document level so keys work regardless of what's focused
    // inside the reader. No-op when not in paginated mode.
    this._onKeyDown = (e) => this._handleKey(e);
    document.addEventListener("keydown", this._onKeyDown);
    // Recompute page count on window resize so the "Page N of M"
    // indicator and page-turn translation stay accurate.
    this._onResize = () => this._recomputePages();
    window.addEventListener("resize", this._onResize);
    // Shell → reader message channel. The shell sends hive:set-tool
    // when the curator switches the active correction tool from the
    // reader-toolbar; the reader updates its interaction mode
    // (cursor, hover-highlight, click semantics) accordingly.
    this._onShellMessage = (e) => {
      const d = e.data;
      if (!d || typeof d !== "object") return;
      if (d.type === "hive:set-tool" && typeof d.tool === "string") {
        this._activeTool = d.tool;
      }
    };
    window.addEventListener("message", this._onShellMessage);
  }

  disconnectedCallback() {
    document.removeEventListener("keydown", this._onKeyDown);
    window.removeEventListener("resize", this._onResize);
    window.removeEventListener("message", this._onShellMessage);
    super.disconnectedCallback();
  }

  updated(changed) {
    // Recompute pages whenever the doc, paginated mode, expand-
    // abbreviations toggle, or PDF-toggle changes — any of these
    // can affect layout dimensions.
    if (
      changed.has("_doc") ||
      changed.has("_paginated") ||
      changed.has("_showExpanded") ||
      changed.has("_showPDF")
    ) {
      // Wait a frame so Lit finishes committing DOM changes
      // before we measure.
      requestAnimationFrame(() => this._recomputePages());
    }
    if (changed.has("_page") || changed.has("_pageCount")) {
      this._applyPageTransform();
    }
    // Apply any pending position restore now that pagination is
    // set up. Only fires once — clears the pending record after
    // applying so subsequent renders don't re-apply the stale
    // position over the curator's live navigation.
    if (changed.has("_pageCount") && this._pendingPositionRestore) {
      const pos = this._pendingPositionRestore;
      this._pendingPositionRestore = null;
      if (this._paginated && Number.isFinite(pos?.page)) {
        this._page = Math.max(0, Math.min(pos.page, this._pageCount - 1));
      }
      if (Number.isFinite(pos?.scrollY)) {
        window.scrollTo(0, pos.scrollY);
      }
    }
    // Persist position on page-turn so paginated reload restores
    // the same page. Guarded on _savePos existing (i.e. _load has
    // wired the listener + save function).
    if (changed.has("_page") && this._savePos) {
      this._savePos();
    }
  }

  _handleKey(e) {
    // Esc closes the figure/table modal when one is open — takes
    // priority over any other key handling so the modal is always
    // dismissable regardless of paginated-view state.
    if (e.key === "Escape" && this._modalFigID) {
      e.preventDefault();
      this._closeFigModal();
      return;
    }
    if (!this._paginated) return;
    // Ignore keys when the curator is focused in an input.
    const tag = (document.activeElement && document.activeElement.tagName) || "";
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    switch (e.key) {
      case "ArrowRight":
      case "ArrowDown":
      case "PageDown":
      case " ":
        // Down maps to forward-page because curators used to
        // reading PDFs reach for the down-arrow instinctively.
        e.preventDefault();
        this._pageForward();
        break;
      case "ArrowLeft":
      case "ArrowUp":
      case "PageUp":
        e.preventDefault();
        this._pageBack();
        break;
      case "Home":
        e.preventDefault();
        this._page = 0;
        break;
      case "End":
        e.preventDefault();
        this._page = Math.max(0, this._pageCount - 1);
        break;
    }
  }

  _pageForward() {
    if (this._page < this._pageCount - 1) this._page += 1;
  }

  _pageBack() {
    if (this._page > 0) this._page -= 1;
  }

  // _onLayoutClick dispatches based on the active correction tool.
  // In text-remove mode: intercept clicks on body paragraphs and
  // send a hive:remove-text postMessage; other click behavior
  // (page-turn, xref jump-to-pdf) is suppressed so a click doesn't
  // simultaneously turn the page and stage a removal. In "read"
  // mode: fall through to the paginated page-turn handler.
  _onLayoutClick(e) {
    if (this._activeTool === "text-remove") {
      this._handleTextRemoveClick(e);
      return;
    }
    if (this._paginated) this._onPageClick(e);
  }

  // _handleTextRemoveClick walks up from the click target to find a
  // removable block element (paragraph or section heading — a JATS
  // <p> or <title>). Both are common noise carriers: <p> for running
  // headers/footers GROBID pulled into body prose, <title> for
  // spurious section headings like "Corresponding author" that ended
  // up misclassified. Rejects page-break wrappers, hive:noise
  // markers themselves (already removed), and any target inside the
  // paper's own <header> block (metadata, not body noise).
  //
  // Standalone reader (no parent shell): warns and no-ops since the
  // correction needs archive context the shell holds.
  _handleTextRemoveClick(e) {
    let el = e.target;
    // Walk up to the nearest removable block. Bail if we escape the
    // article.
    const removable = new Set(["P", "H1", "H2", "H3", "H4", "H5", "H6"]);
    while (el && !removable.has(el.tagName)) {
      if (el.tagName === "ARTICLE") return;
      el = el.parentElement;
    }
    if (!el) return;
    // Reject infrastructure elements the tool isn't meant for.
    if (el.classList.contains("page-break")) return;
    if (el.classList.contains("hive-noise-marker")) return;
    // Reject anything inside the paper-header block (title, byline,
    // abstract) — that's metadata, not body noise.
    if (el.closest(".paper-header")) return;
    if (!(window.parent && window.parent !== window)) {
      // eslint-disable-next-line no-console
      console.warn("text-remove requires the shell — open via References tab");
      return;
    }
    const text = (el.textContent || "").trim();
    if (!text) return;
    // Recover pdf_page from the nearest preceding .page-break
    // element in DOM order. Walk previousElementSibling of the
    // target, then step up to the parent and repeat — so a
    // paragraph inside a <section> can still find the page-break
    // that preceded its section.
    let pdfPage = 0;
    let cursor = el;
    outer: while (cursor) {
      let prev = cursor.previousElementSibling;
      while (prev) {
        if (prev.classList && prev.classList.contains("page-break")) {
          const n = parseInt(prev.dataset.pdfPage || "", 10);
          if (Number.isFinite(n) && n > 0) {
            pdfPage = n;
          }
          break outer;
        }
        prev = prev.previousElementSibling;
      }
      cursor = cursor.parentElement;
      if (!cursor || cursor.tagName === "ARTICLE") break;
    }
    // eslint-disable-next-line no-console
    console.log("hive:remove-text →", { sha: this._sha, text: text.slice(0, 60), pdfPage });
    window.parent.postMessage(
      {
        type: "hive:remove-text",
        sha: this._sha,
        text,
        pdfPage,
      },
      "*",
    );
  }

  // Click-to-turn: left third goes back, right third goes forward,
  // middle third is a dead zone so accidental clicks (or selecting
  // text) don't flip pages.
  _onPageClick(e) {
    const layout = this.renderRoot.querySelector(".layout.paginated");
    if (!layout) return;
    const rect = layout.getBoundingClientRect();
    const x = e.clientX - rect.left;
    if (x < rect.width / 3) this._pageBack();
    else if (x > (rect.width * 2) / 3) this._pageForward();
  }

  // Wheel-to-turn: scroll gestures flip pages instead of scrolling
  // within the container (there's nothing to scroll — pages are
  // fixed to the container). Debounced so a trackpad's rapid
  // small-deltaY events fire one turn per gesture rather than
  // burning through the whole document in one flick.
  _onPageWheel(e) {
    if (!this._paginated) return;
    if (Math.abs(e.deltaY) < 5) return;
    e.preventDefault();
    const now = Date.now();
    if (this._lastWheelTurn && now - this._lastWheelTurn < 300) return;
    this._lastWheelTurn = now;
    if (e.deltaY > 0) this._pageForward();
    else this._pageBack();
  }

  _recomputePages() {
    if (!this._paginated) {
      this._pageCount = 1;
      this._page = 0;
      this._pageWidth = 0;
      return;
    }
    const article = this.renderRoot.querySelector(".layout.paginated article");
    if (!article) return;
    // Reset transform so scrollWidth reflects true content width.
    article.style.transform = "";
    // Measure the actual column layout so page-turns advance by a
    // whole number of columns and always land a column's left edge
    // at the pane's left edge. Subtle point: getComputedStyle
    // returns the *specified* column-width (e.g. "32ch"), not the
    // *actual* width the browser rendered — with `column-width` set,
    // the browser packs as many columns as fit at MINIMUM that
    // width, then stretches each to fill. Using the specified value
    // for pagination underestimates the page-width and drifts
    // mid-column. Compute the actual column width from the pane
    // content width and the column count the browser chose.
    const style = getComputedStyle(article);
    const colGap = parseFloat(style.columnGap) || 0;
    const specifiedColWidth = parseFloat(style.columnWidth) || 0;
    const padLeft = parseFloat(style.paddingLeft) || 0;
    const padRight = parseFloat(style.paddingRight) || 0;
    const contentAreaWidth = article.clientWidth - padLeft - padRight;
    let colsPerPage = 1;
    if (specifiedColWidth > 0 && contentAreaWidth > 0) {
      colsPerPage = Math.max(
        1,
        Math.floor(
          (contentAreaWidth + colGap) / (specifiedColWidth + colGap),
        ),
      );
    }
    // Actual per-column width: browser divides the available
    // content-area width by the chosen column count, minus the
    // internal gaps.
    const actualColWidth =
      colsPerPage > 0
        ? (contentAreaWidth - (colsPerPage - 1) * colGap) / colsPerPage
        : 0;
    const actualPitch = actualColWidth + colGap;
    this._pageWidth = colsPerPage * actualPitch;
    const contentWidth = article.scrollWidth - padLeft - padRight;
    const pages =
      this._pageWidth > 0
        ? Math.max(1, Math.ceil(contentWidth / this._pageWidth))
        : 1;
    this._pageCount = pages;
    if (this._page >= pages) this._page = pages - 1;
    this._applyPageTransform();
  }

  _applyPageTransform() {
    const article = this.renderRoot.querySelector(".layout.paginated article");
    if (!article) return;
    // Translate by page-width increments; each increment is a
    // whole number of column-pitches, so the left edge of the
    // "next page" is always the left edge of a column.
    const offset = this._page * (this._pageWidth || article.offsetWidth);
    article.style.transform = `translateX(-${offset}px)`;
  }

  async _load() {
    this._busy = true;
    this._error = "";
    try {
      const res = await fetch(
        `/api/reference/library/${encodeURIComponent(this._sha)}/taxpub`,
      );
      if (res.status === 404) {
        this._error = "no augmented XML for this reference — either the upload predated the annotator or the annotation pass failed";
        return;
      }
      if (!res.ok) {
        this._error = `fetch failed: HTTP ${res.status}`;
        return;
      }
      const xmlText = await res.text();
      const parser = new DOMParser();
      const doc = parser.parseFromString(xmlText, "application/xml");
      const err = doc.querySelector("parsererror");
      if (err) {
        this._error = "XML parse error: " + err.textContent;
        return;
      }
      this._doc = doc;
      document.title = `hive · reader · ${this._sha.slice(0, 12)}`;
      // Queue a position restore for after pagination is set up.
      // _recomputePages runs on an rAF after _doc changes and is
      // what actually sets _pageCount from the DOM layout; a naive
      // rAF here would race and read _pageCount as its initial
      // value of 1, collapsing the restored page to 0. Instead,
      // stash the pending position and let updated() apply it
      // when _pageCount transitions to its real value.
      try {
        const saved = localStorage.getItem("hive:reader-pos:" + this._sha);
        if (saved != null) {
          const pos = JSON.parse(saved);
          this._pendingPositionRestore = pos;
        }
      } catch (err) {
        // Ignore — restore is best-effort.
      }
      // Attach listeners once. Debounced save so rapid
      // scrolling / page-turns don't hammer localStorage.
      if (!this._posListenerAttached) {
        this._posListenerAttached = true;
        let saveTimer = 0;
        const save = () => {
          if (!this._sha) return;
          if (saveTimer) clearTimeout(saveTimer);
          saveTimer = setTimeout(() => {
            try {
              const pos = {
                scrollY: window.scrollY,
                page: this._page,
              };
              localStorage.setItem(
                "hive:reader-pos:" + this._sha,
                JSON.stringify(pos),
              );
            } catch (err) {
              // Storage disabled / quota — best-effort feature.
            }
          }, 250);
        };
        window.addEventListener("scroll", save);
        // Also save on page-turn — _page is a reactive property so
        // the change goes through updated(), but we don't have a
        // direct hook. Instead, tap the existing keyboard handlers
        // and page-click paths via an updated() addition below.
        this._savePos = save;
      }
    } catch (e) {
      this._error = String(e);
    } finally {
      this._busy = false;
    }
  }

  render() {
    // Compute paper metadata once per render so the top nav's
    // copy buttons and the article header share a single source
    // of truth. Cheap (single XML walk); no need to memoize.
    const meta = this._doc ? this._extractPaperMeta() : null;
    return html`
      <header>
        <h1>${this._sha ? this._sha.slice(0, 16) + "…" : "(no sha)"}</h1>
        <div class="controls">
          <div class="legend" aria-label="chip legend">
            <span class="swatch"><span class="dot verified"></span> verified</span>
            <span class="swatch"><span class="dot ambiguous"></span> ambiguous / unverified</span>
            <span class="swatch"><span class="dot unresolved"></span> unresolved</span>
          </div>
          <label>
            <input
              type="checkbox"
              .checked=${this._showExpanded}
              @change=${(e) => (this._showExpanded = e.target.checked)}
            />
            Expand abbreviations
          </label>
          <label>
            <input
              type="checkbox"
              .checked=${this._showPDF}
              @change=${(e) => {
                this._showPDF = e.target.checked;
                // PDF pane preempts paginated layout — the
                // horizontal-scroll pagination and the split-view
                // both want the viewport, so make them mutually
                // exclusive to avoid a broken compound state.
                if (this._showPDF) this._paginated = false;
              }}
            />
            Show PDF
          </label>
          <label>
            <input
              type="checkbox"
              .checked=${this._paginated}
              @change=${(e) => {
                this._paginated = e.target.checked;
                if (this._paginated) this._showPDF = false;
                this._page = 0;
              }}
            />
            Columned view
          </label>
          ${this._paginated
            ? html`<span class="page-indicator">
                Page ${this._page + 1} of ${this._pageCount}
              </span>`
            : nothing}
          ${meta && meta.plain
            ? html`
                <div class="cite-toolbar" aria-label="copy citation">
                  <button @click=${(e) => this._copy(meta.plain, e)}>Copy citation</button>
                  <button @click=${(e) => this._copy(meta.cslJSON, e)}>Copy CSL-JSON</button>
                  <button @click=${(e) => this._copy(meta.bibtex, e)}>Copy BibTeX</button>
                  ${meta.doiURL
                    ? html`<button @click=${(e) => this._copy(meta.doiURL, e)}>Copy DOI URL</button>`
                    : nothing}
                </div>
              `
            : nothing}
        </div>
      </header>
      ${this._busy ? html`<p class="empty" role="status">Loading…</p>` : nothing}
      ${this._error
        ? html`<p class="error" role="alert">${this._error}</p>`
        : nothing}
      ${this._renderExtractionWarning()}
      ${this._renderRefWarning()}
      ${this._doc
        ? this._showPDF
          ? html`<iframe
              class="pdf-full"
              src=${`/api/reference/library/${encodeURIComponent(this._sha)}/source#page=1`}
              title="Source PDF"
            ></iframe>`
          : html`
              <div class=${"layout tool-" + this._activeTool + (this._paginated ? " paginated" : "")}
                @click=${(e) => this._onLayoutClick(e)}
                @wheel=${(e) => this._paginated && this._onPageWheel(e)}
              >
                ${this._renderBody()}
              </div>
            `
        : nothing}
      ${this._renderFigModal()}
    `;
  }

  // _renderBody walks the JATS <body> subtree (plus <back>) of the
  // augmented document, converting known elements to their HTML
  // equivalents. Content in <front> is surfaced via the paper header
  // (title / byline / abstract) at the top of the article, not
  // rendered inline — front-matter is machine-readable metadata, not
  // prose. Copy buttons live in the sticky top nav (see render()),
  // not in the paper header, so the article stays clean.
  _renderBody() {
    const bodies = this._doc.getElementsByTagName("body");
    if (bodies.length === 0) {
      return html`<p class="empty">document has no &lt;body&gt; content</p>`;
    }
    const body = bodies[0];
    const back = this._doc.getElementsByTagName("back")[0];
    const meta = this._extractPaperMeta();
    return html`
      <article>
        ${this._renderPaperHeader(meta)}
        ${this._renderNodes(body.childNodes)}
        ${back ? this._renderNodes(back.childNodes) : nothing}
      </article>
    `;
  }

  // _renderPaperHeader emits the top-of-paper block: title,
  // author byline, and collapsible abstract. Copy affordances
  // live in the top nav bar (via _extractPaperMeta) so they stay
  // one click away without cluttering the paper's identity block.
  _renderPaperHeader(meta) {
    if (!meta) return nothing;
    return html`
      <header class="paper-header">
        ${meta.title ? html`<h1 class="paper-title">${meta.title}</h1>` : nothing}
        ${meta.authors.length
          ? html`<p class="paper-authors">${meta.authors.join(", ")}</p>`
          : nothing}
        ${meta.abstractParas.length
          ? html`
              <details class="paper-abstract" open>
                <summary>Abstract</summary>
                ${meta.abstractParas.map((p) => html`<p>${p}</p>`)}
              </details>
            `
          : nothing}
      </header>
    `;
  }

  // _extractPaperMeta pulls the paper-identity fields out of the
  // JATS <front> subtree and precomputes the three copy-buffer
  // strings (plain / CSL-JSON / BibTeX). Called once per render —
  // cheap; no memoization needed. Returns null when <front> is
  // absent.
  _extractPaperMeta() {
    const front = this._doc.getElementsByTagName("front")[0];
    if (!front) return null;
    const data = this._paperMetaToData(front);
    const abstractParas = this._extractAbstract(front);
    const plain = this._formatPlainCitation(data);
    const bibtex = this._formatBibTeX(data);
    const cslJSON = this._formatCSLJSON(data);
    const doiURL = data.doi ? `https://doi.org/${data.doi}` : "";
    return {
      title: data.title,
      authors: data.authors,
      abstractParas,
      plain,
      bibtex,
      cslJSON,
      doiURL,
    };
  }

  // _extractAbstract pulls each <abstract><p> under <article-meta>
  // into a plain string. Falls through to the raw abstract text
  // when there's no <p> wrapper (an unusual JATS producer might
  // put text directly under <abstract>).
  _extractAbstract(front) {
    const articleMeta = this._directChild(front, "article-meta");
    if (!articleMeta) return [];
    const abs = this._directChild(articleMeta, "abstract");
    if (!abs) return [];
    const paras = [];
    for (const child of abs.children) {
      if (child.localName === "p") {
        const t = (child.textContent || "").trim();
        if (t) paras.push(t);
      }
    }
    if (paras.length === 0) {
      const t = (abs.textContent || "").trim();
      if (t) paras.push(t);
    }
    return paras;
  }

  // _jumpToFig fires when a curator clicks a figure xref in the
  // reader. Sends a postMessage to the parent frame (the app.js
  // shell) asking to switch to the Digitize tab and scroll the
  // PDF pane to the referenced figure's source page. The parent's
  // handler is what actually changes tab state and PDF src.
  //
  // Standalone reader (opened at /read.html directly, no parent
  // frame): fall back to opening the PDF in a new tab at the
  // right page. window.parent === window when not iframed, so
  // detect that and take the fallback path.
  _jumpToFig(event, figID, page) {
    event.preventDefault();
    // Stop propagation so the click doesn't bubble to the
    // .layout.paginated click handler that turns pages in
    // columned-view mode — otherwise a click on a fig-xref in
    // paginated mode both jumps to the PDF AND turns the reader
    // page, which looks like a random unrelated page flip.
    event.stopPropagation();
    const pageNum = page ? parseInt(page, 10) : null;
    if (window.parent && window.parent !== window) {
      window.parent.postMessage(
        {
          type: "hive:jump-to-pdf",
          sha: this._sha,
          figID: figID,
          page: pageNum,
        },
        "*",
      );
      return;
    }
    // Standalone-reader fallback: open PDF in a new tab at the page.
    if (this._sha && pageNum) {
      window.open(
        `/api/reference/library/${encodeURIComponent(this._sha)}/source#page=${pageNum}`,
        "_blank",
      );
    }
  }

  // _refPreviewText returns a compact plain-text formatted
  // citation for the given <ref> node — used as the `title`
  // attribute on inline citation spans so a hover surfaces the
  // reference without a jump. Same field-selection logic as
  // _renderRef, just flattened to a single string with punctuation.
  _refPreviewText(refNode) {
    const ec =
      this._directChild(refNode, "element-citation") ||
      this._directChild(refNode, "mixed-citation");
    if (!ec) {
      return (refNode.textContent || "").trim().slice(0, 400);
    }
    const d = this._elementCitationToData(ec);
    const parts = [];
    if (d.authors.length) parts.push(d.authors.join(", "));
    if (d.year) parts.push(` (${d.year}).`);
    else if (d.authors.length) parts.push(".");
    if (d.articleTitle) parts.push(` ${(d.articleTitle.textContent || "").trim()}.`);
    if (d.source) parts.push(` ${(d.source.textContent || "").trim()}`);
    if (d.volume) {
      let v = ` ${d.volume}`;
      if (d.issue) v += `(${d.issue})`;
      parts.push(v);
    }
    if (d.page) parts.push(`: ${d.page}`);
    parts.push(".");
    if (d.doi) parts.push(` doi:${d.doi}`);
    return parts.join("").replace(/\s+/g, " ").trim();
  }

  // _jumpToRef scrolls the referenced <ref> entry into view and
  // pulses a background flash so the curator sees where the
  // citation landed. In paginated (columned) mode the layout is
  // transform-based, so scrollIntoView doesn't move the visible
  // page — compute the target column from offsetLeft and set
  // _page directly instead.
  _jumpToRef(rid) {
    if (!rid) return;
    const target = this._doc.querySelector(`[id="${CSS.escape(rid)}"]`);
    if (!target) return;
    // The rendered document lives in the shadow root, keyed on
    // matching id — mirror the parser's target back to the rendered
    // element via querySelector on renderRoot.
    const rendered = this.renderRoot.querySelector(
      `article ol.references li[data-ref-id="${CSS.escape(rid)}"]`,
    );
    if (!rendered) {
      // No rendered target yet — most likely the ref-list hasn't
      // been walked because the doc is huge and we're in an
      // out-of-viewport column. Fall back to nothing; a later
      // render pass will bind the id when the list mounts.
      return;
    }
    if (this._paginated && this._pageWidth) {
      const article = this.renderRoot.querySelector(".layout.paginated article");
      if (article) {
        // The article is CSS-transform-translated for pagination.
        // getBoundingClientRect() returns transformed coordinates
        // for BOTH the article and its descendants, so the delta
        // (target.left - article.left) is already the target's
        // offset in the article's own coordinate system — no need
        // to add the current shift back in.
        const articleLeft = article.getBoundingClientRect().left;
        const targetLeft = rendered.getBoundingClientRect().left;
        const untransformedOffset = targetLeft - articleLeft;
        const page = Math.floor(untransformedOffset / this._pageWidth);
        this._page = Math.max(0, Math.min(page, this._pageCount - 1));
      }
    } else {
      rendered.scrollIntoView({ behavior: "smooth", block: "center" });
    }
    // Restart the CSS animation by removing + re-adding the class
    // in a rAF — otherwise re-clicking the same citation doesn't
    // re-trigger the flash.
    rendered.classList.remove("ref-flash");
    void rendered.offsetWidth;
    rendered.classList.add("ref-flash");
  }

  // _openFigModal opens the figure/table modal on a given fig id.
  // Called from inline xref clicks and from the trailing "Figures
  // and tables" list. The modal renders in main render() when
  // _modalFigID is non-empty. stopPropagation prevents the click
  // from bubbling to the paginated-layout page-turn handler.
  _openFigModal(event, figID) {
    if (event) {
      event.preventDefault();
      event.stopPropagation();
    }
    if (!figID) return;
    this._modalFigID = figID;
    // Default tab: prefer Digitized only when the target actually
    // has structured content to show (a <table> child inside a
    // <table-wrap>). Curator-added table-wraps have only a bbox +
    // caption + PNG crop, so Original is the useful default there.
    // Figures always default to Original since SVG tracing hasn't
    // landed yet.
    const target = this._doc &&
      this._doc.querySelector(`[id="${CSS.escape(figID)}"]`);
    const hasStructuredTable =
      target && target.localName === "table-wrap" &&
      !!this._directChild(target, "table");
    this._modalTab = hasStructuredTable ? "digitized" : "original";
  }

  // _closeFigModal closes the currently-open modal. Wired to the
  // close button, Esc key (via keydown handler in render), and
  // backdrop click. Idempotent — safe to call when no modal is
  // open.
  _closeFigModal() {
    this._modalFigID = "";
  }

  async _copy(text, event) {
    // Capture the button reference *before* the await —
    // event.currentTarget is nulled by the browser once the
    // synchronous portion of the handler returns, so a reference
    // read after `await` fires as `null`.
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

  // _formatPlainCitation composes a human-readable citation string
  // from the paper-meta data. Flattened to text — no HTML, so it
  // copies cleanly into a chat / email.
  _formatPlainCitation(data) {
    const parts = [];
    if (data.authors.length) parts.push(data.authors.join(", "));
    if (data.year) parts.push(` (${data.year}).`);
    else if (data.authors.length) parts.push(".");
    if (data.title) parts.push(` ${data.title}.`);
    if (data.journal) parts.push(` ${data.journal}`);
    if (data.volume) {
      let v = ` ${data.volume}`;
      if (data.issue) v += `(${data.issue})`;
      parts.push(v);
    }
    if (data.page) parts.push(`: ${data.page}`);
    parts.push(".");
    if (data.publisher) {
      parts.push(` ${data.publisher}`);
      if (data.pubPlace) parts.push(`, ${data.pubPlace}`);
      parts.push(".");
    }
    if (data.doi) parts.push(` https://doi.org/${data.doi}`);
    return parts.join("").trim();
  }

  // _formatBibTeX emits a BibTeX @article (or @book) entry from
  // the paper-meta data. Values wrap in double braces to preserve
  // scientific-name capitalization (BibTeX lowercases title-cased
  // words by default in most bibliography styles). Author list
  // joined with " and " per BibTeX convention.
  _formatBibTeX(data) {
    const kind = data.journal ? "article" : "misc";
    const firstAuthor = data.authors[0] || "unknown";
    const firstAuthorKey = firstAuthor
      .toLowerCase()
      .replace(/[^a-z0-9]/g, "")
      .slice(0, 20);
    const key = `${firstAuthorKey}${data.year || ""}`;
    const bibAuthors = data.authors
      .map((a) => {
        // "Smith AB" → "Smith, AB" for BibTeX's canonical form.
        const parts = a.split(" ");
        if (parts.length >= 2) {
          const surname = parts.slice(0, -1).join(" ");
          const initials = parts[parts.length - 1];
          return `${surname}, ${initials}`;
        }
        return a;
      })
      .join(" and ");
    const lines = [`@${kind}{${key},`];
    if (bibAuthors) lines.push(`  author = {${bibAuthors}},`);
    if (data.title) lines.push(`  title = {{${bibTexEscape(data.title)}}},`);
    if (data.journal)
      lines.push(`  journal = {${bibTexEscape(data.journal)}},`);
    if (data.year) lines.push(`  year = {${data.year}},`);
    if (data.volume) lines.push(`  volume = {${data.volume}},`);
    if (data.issue) lines.push(`  number = {${data.issue}},`);
    if (data.page) lines.push(`  pages = {${data.page.replace(/-/g, "--")}},`);
    if (data.publisher)
      lines.push(`  publisher = {${bibTexEscape(data.publisher)}},`);
    if (data.pubPlace)
      lines.push(`  address = {${bibTexEscape(data.pubPlace)}},`);
    if (data.doi) lines.push(`  doi = {${data.doi}},`);
    // Trim trailing comma on the last field for a slightly tidier
    // entry, then close.
    if (lines.length > 1) lines[lines.length - 1] = lines[lines.length - 1].replace(/,$/, "");
    lines.push("}");
    return lines.join("\n");
  }

  // _formatCSLJSON emits a CSL-JSON record (the format Zotero /
  // Pandoc / citation.js and any CSL processor consume). Once a
  // curator has this in the clipboard, they can paste into Zotero
  // or run it through citeproc-js to format in any of the
  // ~10,000 published citation styles. No BibTeX escaping /
  // name-parsing quirks and Unicode round-trips cleanly.
  //
  // Type inference is minimal: journal present → article-journal;
  // journal absent but publisher present → book; otherwise misc.
  // Book chapters need a future refinement.
  _formatCSLJSON(data) {
    let type = "article-journal";
    if (!data.journal && data.publisher) type = "book";
    else if (!data.journal && !data.publisher) type = "article";
    const firstAuthor = data.authors[0] || "unknown";
    const firstAuthorKey = firstAuthor
      .toLowerCase()
      .replace(/[^a-z0-9]/g, "")
      .slice(0, 20);
    const record = {
      id: `${firstAuthorKey}${data.year || ""}`,
      type,
    };
    if (data.title) record.title = data.title;
    if (data.authors.length) {
      record.author = data.authors.map((a) => {
        const parts = a.split(" ");
        if (parts.length >= 2) {
          return {
            family: parts.slice(0, -1).join(" "),
            given: parts[parts.length - 1],
          };
        }
        return { family: a };
      });
    }
    if (data.year) {
      record.issued = { "date-parts": [[parseInt(data.year, 10)]] };
    }
    if (data.journal) record["container-title"] = data.journal;
    if (data.volume) record.volume = data.volume;
    if (data.issue) record.issue = data.issue;
    if (data.page) record.page = data.page;
    if (data.publisher) record.publisher = data.publisher;
    if (data.pubPlace) record["publisher-place"] = data.pubPlace;
    if (data.doi) record.DOI = data.doi;
    return JSON.stringify(record, null, 2);
  }

  // _paperMetaToData flattens the JATS <front> subtree into a
  // normalized data object shared by the citation formatters.
  // Structurally different from a reference (<element-citation>),
  // where the fields live directly on the element rather than
  // nested under journal-meta / article-meta.
  _paperMetaToData(front) {
    const journalMeta = this._directChild(front, "journal-meta");
    const articleMeta = this._directChild(front, "article-meta");
    let journal = "";
    let publisher = "";
    let pubPlace = "";
    if (journalMeta) {
      const jtg = this._directChild(journalMeta, "journal-title-group");
      if (jtg) journal = this._directChildText(jtg, "journal-title");
      const pub = this._directChild(journalMeta, "publisher");
      if (pub) {
        publisher = this._directChildText(pub, "publisher-name");
        pubPlace = this._directChildText(pub, "publisher-loc");
      }
    }
    let title = "";
    let authors = [];
    let year = "";
    let volume = "";
    let issue = "";
    let fpage = "";
    let lpage = "";
    let doi = "";
    if (articleMeta) {
      const titleGroup = this._directChild(articleMeta, "title-group");
      if (titleGroup) title = this._directChildText(titleGroup, "article-title");
      const contribGroup = this._directChild(articleMeta, "contrib-group");
      if (contribGroup) authors = this._collectAuthors(contribGroup);
      const pubDate = this._directChild(articleMeta, "pub-date");
      if (pubDate) year = this._directChildText(pubDate, "year");
      volume = this._directChildText(articleMeta, "volume");
      issue = this._directChildText(articleMeta, "issue");
      fpage = this._directChildText(articleMeta, "fpage");
      lpage = this._directChildText(articleMeta, "lpage");
      doi = this._extractPubIdByAttr(articleMeta, "article-id", "pub-id-type", "doi");
    }
    const page = fpage && lpage && fpage !== lpage ? `${fpage}-${lpage}` : fpage;
    return {
      title,
      authors,
      journal,
      year,
      volume,
      issue,
      page,
      publisher,
      pubPlace,
      doi,
    };
  }

  // _elementCitationToData flattens a JATS <element-citation> (the
  // structured form used inside <ref>) into the shape _renderRef
  // consumes. Field extraction differs from _paperMetaToData
  // because element-citation puts everything at one level rather
  // than nested under journal-meta / article-meta wrappers.
  _elementCitationToData(ec) {
    const personGroup = this._directChild(ec, "person-group");
    const authors = personGroup ? this._collectAuthors(personGroup) : [];
    const articleTitle = this._directChild(ec, "article-title");
    const source = this._directChild(ec, "source");
    const year = this._directChildText(ec, "year");
    const volume = this._directChildText(ec, "volume");
    const issue = this._directChildText(ec, "issue");
    const fpage = this._directChildText(ec, "fpage");
    const lpage = this._directChildText(ec, "lpage");
    const page = fpage && lpage && fpage !== lpage ? `${fpage}-${lpage}` : fpage;
    const publisher = this._directChildText(ec, "publisher-name");
    const publisherLoc = this._directChildText(ec, "publisher-loc");
    const doi = this._extractPubIdByAttr(ec, "pub-id", "pub-id-type", "doi");
    return {
      articleTitle,
      source,
      authors,
      year,
      volume,
      issue,
      page,
      publisher,
      publisherLoc,
      doi,
    };
  }

  // _collectAuthors reads a JATS <contrib-group> or <person-group>
  // and returns "Surname Initials" strings in document order. Each
  // author's element is either <contrib><name>…</name></contrib>
  // (article-meta authors) or <name>…</name> (element-citation
  // person-group members); both shapes route through the same
  // <name><surname> + <given-names> pair.
  _collectAuthors(container) {
    const authors = [];
    for (const child of container.children) {
      const local = child.localName;
      let name = null;
      if (local === "contrib") name = this._directChild(child, "name");
      else if (local === "name") name = child;
      else continue;
      if (!name) continue;
      const surname = this._directChildText(name, "surname");
      if (!surname) continue;
      const given = this._directChildText(name, "given-names");
      const initials = given
        .split(/\s+/)
        .filter((s) => s)
        .map((s) => s[0])
        .join("");
      authors.push(initials ? `${surname} ${initials}` : surname);
    }
    return authors;
  }

  // _extractPubIdByAttr scans direct children of `el` for an
  // element with the given local name whose attribute value matches
  // `attrValue`, and returns its text content. Handles both
  // <article-id pub-id-type="doi"> (paper) and <pub-id
  // pub-id-type="doi"> (reference).
  _extractPubIdByAttr(el, localName, attrName, attrValue) {
    for (const child of el.children) {
      if (
        child.localName === localName &&
        (child.getAttribute(attrName) || "").toLowerCase() ===
          attrValue.toLowerCase()
      ) {
        return (child.textContent || "").trim();
      }
    }
    return "";
  }

  // _extractLeadingPageBreaks returns rendered page-break dividers
  // for every hive-page-break <p> that appears as the leading
  // block-level content of a <sec> (after any <label>/<title>
  // headers) before the first non-marker block. Used to visually
  // hoist section-first markers above the section header — see
  // the sec renderer for why the emit-side JATS can't place them
  // there directly. Multiple consecutive markers arise from
  // multi-page fan-outs (e.g., a figures-only page 89 followed by
  // the section body starting on page 90 → both markers hoist).
  _extractLeadingPageBreaks(secNode) {
    const out = [];
    for (const c of secNode.children) {
      const local = c.localName;
      if (local === "title" || local === "label") {
        continue;
      }
      if (
        local === "p" &&
        c.getAttribute("content-type") === "hive-page-break"
      ) {
        let pageNum = "";
        let approx = false;
        for (const gc of c.children) {
          if (gc.localName === "page-break") {
            pageNum = gc.getAttribute("page") || "";
            approx = gc.getAttribute("placement") === "approximate";
            break;
          }
        }
        if (pageNum) {
          const label = (approx ? "~" : "") + pageNum;
          const aria = approx
            ? "Source PDF page approximately " + pageNum + " begins"
            : "Source PDF page " + pageNum + " begins";
          out.push(html`<div
            class="page-break"
            role="separator"
            aria-label=${aria}
            data-pdf-page=${pageNum}
          >
            <span class="page-break-num">page ${label}</span>
          </div>`);
          continue;
        }
      }
      // Reached the first non-marker block — stop hoisting.
      break;
    }
    return out;
  }

  // _childNodesSkippingLeadingPageBreaks returns the sec's
  // childNodes with the leading hive-page-break <p> elements
  // removed, so the hoisted markers don't render a second time
  // inside the section. Only strips markers that appear before
  // the first non-marker block — mid-section markers stay put.
  _childNodesSkippingLeadingPageBreaks(secNode) {
    const kept = [];
    let stillStripping = true;
    for (const n of secNode.childNodes) {
      if (stillStripping && n.nodeType === Node.ELEMENT_NODE) {
        const local = n.localName;
        if (local === "title" || local === "label") {
          kept.push(n);
          continue;
        }
        if (
          local === "p" &&
          n.getAttribute &&
          n.getAttribute("content-type") === "hive-page-break"
        ) {
          continue;
        }
        stillStripping = false;
      } else if (
        stillStripping &&
        n.nodeType === Node.TEXT_NODE &&
        !n.textContent.trim()
      ) {
        // Whitespace between the title/markers — skip while
        // still in the leading run so we don't leak stray text.
        continue;
      } else if (stillStripping) {
        stillStripping = false;
      }
      kept.push(n);
    }
    return kept;
  }

  // _renderNodes converts a NodeList into Lit-html templates,
  // dispatching per element/text kind. Recursive: element children
  // render via the same function so nested content (sec > p > span)
  // survives.
  _renderNodes(nodes) {
    const out = [];
    for (const node of nodes) {
      if (node.nodeType === Node.TEXT_NODE) {
        out.push(node.textContent);
        continue;
      }
      if (node.nodeType !== Node.ELEMENT_NODE) continue;
      const local = node.localName;
      // <tp:taxon-name> — the annotated chip.
      if (
        (node.namespaceURI === TAXPUB_NS || node.prefix === "tp") &&
        local === "taxon-name"
      ) {
        out.push(this._renderTaxonName(node));
        continue;
      }
      // <hive:noise> — a text-remove correction's applied wrapper.
      // Renders as a compact red-dot marker sitting where the noise
      // used to be. Tooltip carries the removed text so curators
      // can verify without opening anything; click sends a
      // hive:restore-text postMessage that stages the un-remove.
      if (node.prefix === "hive" && local === "noise") {
        out.push(this._renderNoiseMarker(node));
        continue;
      }
      // <title> is a section (or ref-list / fig / table-wrap)
      // heading in JATS. Article/journal titles live under
      // <front> and never reach _renderNodes.
      if (local === "title") {
        out.push(html`<h2>${this._renderNodes(node.childNodes)}</h2>`);
        continue;
      }
      // JATS emits <label>N</label> next to figure/table titles;
      // the number in the label typically duplicates the number in
      // the caption title itself. Suppress to avoid double-numbering.
      if (local === "label") {
        continue;
      }
      if (local === "p") {
        // Page-break marker paragraphs (content-type="hive-page-break")
        // are jatsconv's word-boundary milestones — rendered as a
        // full-width divider with the journal-labeled source-PDF
        // page number so curators can cite pages for ZooBank /
        // ICZN references without leaving the reader.
        //
        // The child element is <hive:page-break>, a namespaced tag.
        // querySelector doesn't cross the namespace-prefix boundary
        // cleanly in XML docs, so scan children by localName and
        // read the `page` attribute directly.
        if (node.getAttribute("content-type") === "hive-page-break") {
          let pageNum = "";
          let approx = false;
          for (const c of node.children) {
            if (c.localName === "page-break") {
              pageNum = c.getAttribute("page") || "";
              approx = c.getAttribute("placement") === "approximate";
              break;
            }
          }
          const label = (approx ? "~" : "") + pageNum;
          const aria = approx
            ? "Source PDF page approximately " + pageNum + " begins"
            : "Source PDF page " + pageNum + " begins";
          out.push(html`<div class="page-break" role="separator"
            aria-label=${aria} data-pdf-page=${pageNum}>
            <span class="page-break-num">page ${label}</span>
          </div>`);
          continue;
        }
        out.push(html`<p>${this._renderNodes(node.childNodes)}</p>`);
        continue;
      }
      // <sec sec-type="figures"> gets a special compact-list
      // rendering: its <fig>/<table-wrap> children become clickable
      // list entries labeled with their extracted "Figure N" or
      // "Table N" label. Actual content (caption, image, table
      // structure) lives in the modal that opens on click — the
      // list itself stays scannable without cluttering the reader
      // with full captions. Non-figures secs render normally.
      if (local === "sec") {
        if (node.getAttribute("sec-type") === "figures") {
          out.push(this._renderFiguresList(node));
          continue;
        }
        // Visual hoist for page-break markers that emit as the
        // leading blocks after <title>. JATS forbids <p> at body /
        // sec-boundary level (body-model is para-level*, sec-level*),
        // so the JATS-legal place to attach "this section starts on
        // page N" markers is inside the section right after its
        // title. Rendering them above the title matches curators'
        // visual expectation — the page break sits above that
        // header in the source PDF. Multiple consecutive markers
        // arise from figures-only-page fan-outs (e.g., ~89 + ~90
        // both hoist above a section that starts on page 90).
        const hoisted = this._extractLeadingPageBreaks(node);
        out.push(html`${hoisted}<section>${this._renderNodes(
          hoisted.length ? this._childNodesSkippingLeadingPageBreaks(node) : node.childNodes,
        )}</section>`);
        continue;
      }
      // <fig> and <table-wrap> are only rendered inline when they
      // appear OUTSIDE the trailing "Figures and tables" sec.
      // Inside that sec, _renderFiguresList handles them as list
      // items; the modal shows the full content on click. Inline
      // appearances (rare — GROBID could theoretically put a fig
      // mid-paragraph) still get a "click to open" affordance
      // that opens the modal.
      if (local === "fig" || local === "table-wrap") {
        const figID = node.getAttribute("id") || "";
        const label = this._figLabelFromCaption(node) || figID;
        out.push(html`<a
          class="fig-inline-link"
          role="button"
          tabindex="0"
          @click=${(e) => this._openFigModal(e, figID)}
          @keydown=${(e) => {
            if (e.key === "Enter" || e.key === " ") this._openFigModal(e, figID);
          }}
          >${label}</a>`);
        continue;
      }
      if (local === "caption") {
        out.push(html`<div class="passthrough">${this._renderNodes(node.childNodes)}</div>`);
        continue;
      }
      // JATS table structure passes through to HTML equivalents.
      // Only reached via _renderTableWrap (inside the modal); the
      // top-level <table-wrap> renders as a compact link in the
      // main reader body.
      if (local === "thead") {
        out.push(html`<thead>${this._renderNodes(node.childNodes)}</thead>`);
        continue;
      }
      if (local === "tbody") {
        out.push(html`<tbody>${this._renderNodes(node.childNodes)}</tbody>`);
        continue;
      }
      if (local === "tr") {
        out.push(html`<tr>${this._renderNodes(node.childNodes)}</tr>`);
        continue;
      }
      if (local === "th") {
        out.push(html`<th>${this._renderNodes(node.childNodes)}</th>`);
        continue;
      }
      if (local === "td") {
        // JATS <td> may carry align="char" (decimal-alignment
        // signal from jatsconv). Pass through as a CSS class the
        // reader stylesheet can act on since <td align> is a
        // deprecated HTML attribute.
        const align = node.getAttribute("align") || "";
        const cls = align === "char" ? "num" : "";
        out.push(html`<td class=${cls}>${this._renderNodes(node.childNodes)}</td>`);
        continue;
      }
      if (local === "colgroup" || local === "col") {
        // Column-group hints are advisory only; browser default
        // table layout ignores them without CSS. Emit as-is so a
        // custom stylesheet could still target them.
        continue;
      }
      if (local === "list") {
        out.push(html`<ul>${this._renderNodes(node.childNodes)}</ul>`);
        continue;
      }
      if (local === "list-item") {
        out.push(html`<li>${this._renderNodes(node.childNodes)}</li>`);
        continue;
      }
      // Reference list — JATS <ref-list> contains <ref> entries
      // each wrapping an <element-citation>. Render as an ordered
      // list so numbering matches "(Doe 2020)" xref anchors in
      // body text.
      if (local === "ref-list") {
        out.push(html`<ol class="references">${this._renderNodes(node.childNodes)}</ol>`);
        continue;
      }
      if (local === "ref") {
        const refID = node.getAttribute("id") || "";
        // Warning icon shows on the left edge when the ref is on
        // the suspect list (URL fragment, address-only, empty, ...).
        // Edit pencil shows on the right on hover — clicking either
        // opens the shell's ref-edit modal via postMessage. Keeping
        // the affordances at the row edges avoids collision with
        // citation body links inside the ref text.
        const suspect = this._suspectRefIDs().has(refID);
        out.push(
          html`<li
            class="ref-row${suspect ? " ref-row-suspect" : ""}"
            data-ref-id=${refID}
          >
            <div class="ref-row-inner">
              ${suspect
                ? html`<span
                    class="ref-warn-icon"
                    title="Suspect reference — click to edit"
                    aria-label="Suspect reference"
                    role="button"
                    tabindex="0"
                    @click=${() => this._editRef(refID)}
                    @keydown=${(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        this._editRef(refID);
                      }
                    }}
                    >⚠</span
                  >`
                : html`<span class="ref-warn-icon-placeholder"
                    aria-hidden="true"
                  ></span>`}
              <span class="ref-body">${this._renderRef(node)}</span>
              <button
                class="ref-edit-pencil"
                type="button"
                title="Edit this reference"
                aria-label="Edit reference ${refID}"
                @click=${() => this._editRef(refID)}
              >
                ${pencilIcon(14)}
              </button>
            </div>
          </li>`,
        );
        continue;
      }
      // Cited-reference anchor inside body text — JATS <xref>.
      // For xrefs whose rid points at a <fig>, render as a
      // clickable link that posts a message to the parent frame
      // asking to switch to the Digitize tab and scroll the PDF
      // pane to the figure's source page (from GROBID's coords,
      // stored as hive:pdf-page). Non-fig xrefs (bibliography
      // references, other in-doc targets) render as inline styled
      // text — same behavior as before.
      if (local === "xref") {
        const rid = node.getAttribute("rid");
        // querySelector, not getElementById — for XML documents
        // (which DOMParser produces for application/xml), id-based
        // lookup only works when the DTD declares "id" as an
        // ID-type attribute. querySelector does attribute-value
        // matching directly, no DTD dependency. CSS.escape guards
        // against rids with selector-special characters.
        const target = rid
          ? this._doc.querySelector(`[id="${CSS.escape(rid)}"]`)
          : null;
        if (target && (target.localName === "fig" || target.localName === "table-wrap")) {
          // Fig/table xref opens the modal (shows the extracted
          // image + caption + link to source PDF page). Modal
          // approach preserves narrative reading flow — no tab
          // switch, no page turn. role=button + keydown handler
          // keep it keyboard-accessible.
          out.push(html`<span
            class="cite fig-xref"
            role="button"
            tabindex="0"
            @click=${(e) => this._openFigModal(e, rid)}
            @keydown=${(e) => {
              if (e.key === "Enter" || e.key === " ") {
                this._openFigModal(e, rid);
              }
            }}
            >${this._renderNodes(node.childNodes)}</span>`);
          continue;
        }
        if (target && target.localName === "ref") {
          // Bibliography xref — click jumps to the referenced
          // entry in the ref-list; hover shows a formatted
          // preview so curators don't have to jump for a quick
          // check. stopPropagation prevents the click from
          // bubbling to _onPageClick, which otherwise flips the
          // page based on x-coordinate.
          const preview = this._refPreviewText(target);
          out.push(html`<span
            class="cite ref-xref"
            role="button"
            tabindex="0"
            title=${preview}
            @click=${(e) => {
              e.stopPropagation();
              this._jumpToRef(rid);
            }}
            @keydown=${(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                this._jumpToRef(rid);
              }
            }}
            >${this._renderNodes(node.childNodes)}</span>`);
          continue;
        }
        out.push(html`<span class="cite">${this._renderNodes(node.childNodes)}</span>`);
        continue;
      }
      // JATS inline emphasis / phrase-level markup that flows in
      // running text. Convert to the corresponding HTML element so
      // the reader honors the source styling.
      if (local === "italic") {
        out.push(html`<i>${this._renderNodes(node.childNodes)}</i>`);
        continue;
      }
      if (local === "bold") {
        out.push(html`<b>${this._renderNodes(node.childNodes)}</b>`);
        continue;
      }
      if (local === "sup") {
        out.push(html`<sup>${this._renderNodes(node.childNodes)}</sup>`);
        continue;
      }
      if (local === "sub") {
        out.push(html`<sub>${this._renderNodes(node.childNodes)}</sub>`);
        continue;
      }
      // Unknown element — wrap in an inline span. JATS phrase-
      // level markup (named-content, monospace, styled-content,
      // ext-link, …) doesn't need block containers, and the few
      // block-ish elements JATS actually uses (sec, fig, list,
      // ref-list) are all handled explicitly above.
      out.push(html`<span>${this._renderNodes(node.childNodes)}</span>`);
    }
    return out;
  }

  // _renderFiguresList renders the trailing "Figures and tables"
  // sec as a compact list of clickable entries. Each item shows
  // the fig/table label ("Figure 1", "Table 2") and opens the
  // modal on click. Keeps the reader body uncluttered — the modal
  // is where the actual caption + image + PDF-jump live.
  //
  // Two-pass labeling:
  //   Pass 1 — extract "Figure N" / "Table N" from each caption
  //            where the prefix is present; record used numbers
  //            per kind.
  //   Pass 2 — for elements whose caption dropped the label prefix
  //            (a common GROBID extractor gap when the source
  //            paper has bilingual captions or unusual layouts),
  //            fill in the smallest unused number per kind.
  //
  // This gives the list a coherent "Figure 1, Figure 2, ..." /
  // "Table 1, Table 2, ..." sequence even when individual captions
  // are broken, without the raw JATS id ("tab_1") leaking through.
  _renderFiguresList(secNode) {
    const nodes = [];
    for (const child of secNode.children) {
      if (child.localName === "fig" || child.localName === "table-wrap") {
        nodes.push(child);
      }
    }
    // Sort by source PDF page (from hive:pdf-page) so the list
    // reads in the paper's reading order. Curator additions
    // inserted mid-paper (e.g., a missed Table 1 on page 3) end
    // up in the right spot instead of at the end of the list.
    // Elements with no pdf-page attribute sort last, preserving
    // document order among themselves.
    nodes.sort((a, b) => {
      const pa = this._pdfPageOf(a);
      const pb = this._pdfPageOf(b);
      if (pa === 0 && pb === 0) return 0;
      if (pa === 0) return 1;
      if (pb === 0) return -1;
      return pa - pb;
    });
    // Pass 1 — collect caption-extracted labels + track used
    // numbers per kind for the gap-fill pass.
    const usedFigures = new Set();
    const usedTables = new Set();
    const labels = nodes.map((n) => {
      const extracted = this._figLabelFromCaption(n);
      if (!extracted) return "";
      const m = /^(figure|table)\s+(\d+)$/i.exec(extracted);
      if (m) {
        const num = Number(m[2]);
        if (m[1].toLowerCase() === "table") usedTables.add(num);
        else usedFigures.add(num);
      }
      return extracted;
    });
    // Pass 2 — assign the smallest unused number per kind to
    // elements whose caption lacked the prefix.
    let nextFigure = 1;
    let nextTable = 1;
    const nextUnused = (used, cursorRef) => {
      while (used.has(cursorRef.n)) cursorRef.n++;
      const n = cursorRef.n;
      used.add(n);
      cursorRef.n++;
      return n;
    };
    const figCursor = { n: 1 };
    const tabCursor = { n: 1 };
    for (let i = 0; i < nodes.length; i++) {
      if (labels[i]) continue;
      const kind = nodes[i].localName === "table-wrap" ? "table" : "figure";
      const n = kind === "table"
        ? nextUnused(usedTables, tabCursor)
        : nextUnused(usedFigures, figCursor);
      labels[i] = (kind === "table" ? "Table " : "Figure ") + n;
    }
    // Silence unused let-hoist lint by referencing (nextFigure /
    // nextTable are declared for future reuse if the algorithm
    // ever needs a different cursor model).
    void nextFigure; void nextTable;

    const items = nodes.map((child, i) => {
      const figID = child.getAttribute("id") || "";
      const label = labels[i];
      const kindHint = child.localName === "table-wrap" ? "table" : "figure";
      return html`<li>
        <a
          class="fig-list-link"
          role="button"
          tabindex="0"
          @click=${(e) => this._openFigModal(e, figID)}
          @keydown=${(e) => {
            if (e.key === "Enter" || e.key === " ") this._openFigModal(e, figID);
          }}
          title="Open ${kindHint} in modal"
          >${label}</a>
      </li>`;
    });
    return html`
      <section class="figures-list">
        <h2>Figures and tables</h2>
        <ol>${items}</ol>
      </section>
    `;
  }

  // _gapFillLabelForTarget returns the "Figure N" / "Table N"
  // label that _renderFiguresList would assign to the given
  // fig/table-wrap element via its gap-fill pass. Called from the
  // modal so its header shows the same number the list shows,
  // even when the element's caption dropped its label prefix.
  //
  // Cheap: walks the trailing figures sec once. Called only on
  // modal open, not per render.
  _gapFillLabelForTarget(target) {
    if (!this._doc) return "";
    const trailingSec = this._doc.querySelector('sec[sec-type="figures"]');
    if (!trailingSec) return "";
    const nodes = [];
    for (const child of trailingSec.children) {
      if (child.localName === "fig" || child.localName === "table-wrap") {
        nodes.push(child);
      }
    }
    // Match _renderFiguresList's page-order sort so gap-fill
    // numbering mirrors what the list shows.
    nodes.sort((a, b) => {
      const pa = this._pdfPageOf(a);
      const pb = this._pdfPageOf(b);
      if (pa === 0 && pb === 0) return 0;
      if (pa === 0) return 1;
      if (pb === 0) return -1;
      return pa - pb;
    });
    const usedFigures = new Set();
    const usedTables = new Set();
    const labels = nodes.map((n) => {
      const extracted = this._figLabelFromCaption(n);
      if (!extracted) return "";
      const m = /^(figure|table)\s+(\d+)$/i.exec(extracted);
      if (m) {
        const num = Number(m[2]);
        if (m[1].toLowerCase() === "table") usedTables.add(num);
        else usedFigures.add(num);
      }
      return extracted;
    });
    const figCursor = { n: 1 };
    const tabCursor = { n: 1 };
    const nextUnused = (used, cursorRef) => {
      while (used.has(cursorRef.n)) cursorRef.n++;
      const n = cursorRef.n;
      used.add(n);
      cursorRef.n++;
      return n;
    };
    for (let i = 0; i < nodes.length; i++) {
      if (labels[i]) continue;
      const kind = nodes[i].localName === "table-wrap" ? "table" : "figure";
      const n = kind === "table"
        ? nextUnused(usedTables, tabCursor)
        : nextUnused(usedFigures, figCursor);
      labels[i] = (kind === "table" ? "Table " : "Figure ") + n;
    }
    const idx = nodes.indexOf(target);
    return idx >= 0 ? labels[idx] : "";
  }

  // _pdfPageOf reads the hive:pdf-page attribute off a fig or
  // table-wrap element and returns the parsed page number.
  // Returns 0 when the attribute is missing or unparseable so
  // sort routines can treat "no page" consistently.
  _pdfPageOf(el) {
    if (!el) return 0;
    const raw =
      el.getAttributeNS(HIVE_NS, "pdf-page") ||
      el.getAttribute("hive:pdf-page") ||
      "";
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? n : 0;
  }

  // _pdfPageOffset returns the hive:pdf-page-offset attribute on
  // <article> (added by jatsconv when a journal-labeled page
  // number could be inferred from the source PDF). Adding it to a
  // raw pdf-page yields the journal-labeled page curators cite.
  // Returns 0 when unset — callers should show the raw pdf-page
  // unchanged in that case.
  _pdfPageOffset() {
    if (!this._doc || !this._doc.documentElement) return 0;
    const root = this._doc.documentElement;
    const raw =
      root.getAttributeNS(HIVE_NS, "pdf-page-offset") ||
      root.getAttribute("hive:pdf-page-offset") ||
      "";
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? n : 0;
  }

  // _figLabelFromCaption extracts "Figure N" / "Table N" from a
  // fig/table-wrap's caption text. GROBID's caption always starts
  // with the label; the extractFigLabel logic in jatsconv follows
  // the same convention on the emit side.
  _figLabelFromCaption(figNode) {
    const caption = this._directChild(figNode, "caption");
    if (!caption) return "";
    const text = (caption.textContent || "").trim();
    const m = /^(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)/i.exec(text);
    if (!m) return "";
    const kind = m[1].toLowerCase();
    const num = m[2];
    if (kind.startsWith("tab")) return `Table ${num}`;
    return `Figure ${num}`;
  }

  // _missingFigTableLabels computes the set of Figure/Table labels
  // cited in body prose that don't have a matching extracted <fig>
  // or <table-wrap>. Mirrors pkg/citation_coverage_validator.go so
  // both surfaces (server-side Issues dashboard + this in-reader
  // banner) show the same list from the same JATS bytes.
  _missingFigTableLabels() {
    if (!this._doc) return [];
    const labelRe = /\b(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)\b/gi;
    const canon = (kind, num) =>
      kind.toLowerCase().startsWith("tab") ? `Table ${num}` : `Figure ${num}`;
    // Present set: scan each fig/table-wrap caption for its label.
    const present = new Set();
    const blocks = this._doc.querySelectorAll("fig, table-wrap");
    for (const b of blocks) {
      const cap = b.querySelector("caption");
      if (!cap) continue;
      const m = labelRe.exec(cap.textContent || "");
      labelRe.lastIndex = 0;
      if (m) present.add(canon(m[1], m[2]));
    }
    // Cited set: scan body text with fig/table-wrap blocks removed
    // so a fig's own caption doesn't count as a citation of itself.
    const body = this._doc.querySelector("body");
    if (!body) return [];
    const clone = body.cloneNode(true);
    for (const b of clone.querySelectorAll("fig, table-wrap")) b.remove();
    const cited = new Set();
    const text = clone.textContent || "";
    let m;
    while ((m = labelRe.exec(text)) !== null) {
      cited.add(canon(m[1], m[2]));
    }
    // Diff — sorted for stable banner text.
    const missing = [];
    for (const label of cited) if (!present.has(label)) missing.push(label);
    return missing.sort();
  }

  // _renderExtractionWarning renders the citation-coverage banner
  // at the top of the reader. Shown by default (curators should see
  // extraction gaps on load); a toggle collapses it to a one-line
  // hint that still surfaces the count so nothing is truly hidden.
  // Renders nothing when there are no missing labels.
  _renderExtractionWarning() {
    if (!this._doc) return nothing;
    const missing = this._missingFigTableLabels();
    if (missing.length === 0) return nothing;
    if (!this._showExtractionWarning) {
      return html`<div
        class="extract-warn collapsed"
        role="status"
      >
        <span
          >Extraction missed ${missing.length} cited
          ${missing.length === 1 ? "label" : "labels"}.</span
        >
        <button
          class="extract-warn-toggle"
          type="button"
          @click=${() => (this._showExtractionWarning = true)}
        >Show</button>
      </div>`;
    }
    return html`<div class="extract-warn" role="status">
      <div class="extract-warn-body">
        <div class="extract-warn-title">
          Extraction missed labels cited in the text
        </div>
        <ul class="extract-warn-list">
          ${missing.map((l) => html`<li>${l}</li>`)}
        </ul>
      </div>
      <button
        class="extract-warn-toggle"
        type="button"
        aria-label="Hide extraction warning"
        @click=${() => (this._showExtractionWarning = false)}
      >Hide</button>
    </div>`;
  }

  // _suspectRefs scans the parsed <ref-list> for entries that look
  // like GROBID reference-tokenizer glitches. Returns an array of
  // {id, reason} for the banner. Detection is intentionally
  // heuristic — we're flagging things that DESERVE a curator look,
  // not making structural claims about the archive. Reasons are
  // human-readable so the banner is self-explanatory without a
  // separate docs page.
  //
  // Kept field-agnostic (nothing taxonomy-specific) so this logic
  // rides along when the reference-manager module extracts out.
  _suspectRefs() {
    if (!this._doc) return [];
    const refs = this._doc.querySelectorAll("ref-list ref");
    const out = [];
    for (const ref of refs) {
      const id = ref.getAttribute("id") || "";
      const ec =
        this._directChild(ref, "element-citation") ||
        this._directChild(ref, "mixed-citation");
      if (!ec) continue;
      const d = this._elementCitationToData(ec);
      const titleText = (d.articleTitle?.textContent || "").trim();
      const sourceText = (d.source?.textContent || "").trim();
      // Heuristic 1 — URL-encoded fragment as the article title.
      // "%B3n%20", "%20previo" and friends are unambiguous — no
      // real title starts with a percent-escape triplet.
      if (/^%[0-9A-Fa-f]{2}/.test(titleText)) {
        out.push({
          id,
          reason: "URL fragment as title",
        });
        continue;
      }
      // Heuristic 2 — empty shell (no authors, no title, no source).
      // A ref with none of the identifying fields is almost always
      // a tokenizer split residue.
      if (d.authors.length === 0 && !titleText && !sourceText) {
        out.push({
          id,
          reason: "empty reference",
        });
        continue;
      }
      // Heuristic 3 — every "author" is a single-word surname with
      // no given-names and looks like a place fragment (short, all-
      // letters, could be a country / state / city). GROBID
      // sometimes tokenizes an address tail as a new reference
      // whose authors are ["Cary", "N C", "Usa"] — the address
      // lines from a publisher location that got detached.
      if (d.authors.length > 0 && !titleText && !sourceText) {
        const looksLikePlaces = d.authors.every((a) => {
          const bare = a.split(/\s+/)[0] || "";
          return (
            bare.length > 0 &&
            bare.length <= 12 &&
            /^[A-Za-z][A-Za-z.\-']*$/.test(bare)
          );
        });
        if (looksLikePlaces) {
          out.push({
            id,
            reason: "address-only, no title or source",
          });
          continue;
        }
      }
    }
    return out;
  }

  // _renderRefWarning renders the ref-fragmentation banner.
  // Same shape as the extraction-missed-labels banner so curators
  // recognize it as another "extraction-quality" prompt rather
  // than a hard failure. Each chip is clickable — jumps to the
  // flagged ref in the ref-list so the curator can inspect it in
  // full and (eventually) apply the merge-ref correction.
  _renderRefWarning() {
    if (!this._doc) return nothing;
    const suspects = this._suspectRefs();
    if (suspects.length === 0) return nothing;
    if (!this._showRefWarning) {
      return html`<div class="extract-warn collapsed" role="status">
        <span
          >Suspect ${suspects.length}
          ${suspects.length === 1 ? "reference" : "references"} — the
          citation numbering may be off.</span
        >
        <button
          class="extract-warn-toggle"
          type="button"
          @click=${() => (this._showRefWarning = true)}
        >Show</button>
      </div>`;
    }
    return html`<div class="extract-warn" role="status">
      <div class="extract-warn-body">
        <div class="extract-warn-title">
          Suspect references — citation numbering may be off
        </div>
        <ul class="extract-warn-list">
          ${suspects.map(
            (s) => html`<li class="suspect-ref">
              <span
                class="suspect-ref-id"
                role="button"
                tabindex="0"
                title="Jump to ${s.id} in the reference list"
                @click=${() => this._jumpToRef(s.id)}
                @keydown=${(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    this._jumpToRef(s.id);
                  }
                }}
                >${s.id}</span
              >
              <span class="suspect-ref-reason">${s.reason}</span>
              <button
                class="suspect-ref-merge"
                type="button"
                title="Merge ${s.id} into the previous reference"
                @click=${() => this._mergeRef(s.id)}
              >
                Merge into previous
              </button>
            </li>`,
          )}
        </ul>
      </div>
      <button
        class="extract-warn-toggle"
        type="button"
        aria-label="Hide reference warning"
        @click=${() => (this._showRefWarning = false)}
      >Hide</button>
    </div>`;
  }

  // _mergeRef asks the parent frame (References component) to
  // POST a ref-merge correction for the given ref id. The parent
  // owns the sha + reference id context and the fetch call so the
  // reader stays a pure viewer — same postMessage split as
  // hive:jump-to-pdf. Standalone reader (no parent iframe): no-op
  // with a console warning, since a correction needs the archive
  // context the shell holds.
  _mergeRef(refID) {
    if (!refID) return;
    if (!(window.parent && window.parent !== window)) {
      // eslint-disable-next-line no-console
      console.warn("merge-ref requires the shell — open via References tab");
      return;
    }
    window.parent.postMessage(
      {
        type: "hive:merge-ref",
        sha: this._sha,
        refID: refID,
      },
      "*",
    );
  }

  // _suspectRefIDs returns a Set of ref ids currently flagged by
  // _suspectRefs. Recomputed per render — cheap because there are
  // usually only a few dozen refs per paper and the heuristics are
  // O(1) per ref. Cached in _suspectRefsMemo when the doc reference
  // hasn't changed so per-row rendering doesn't rescan the whole
  // ref-list on every ref.
  _suspectRefIDs() {
    if (
      !this._suspectRefsMemo ||
      this._suspectRefsMemoDoc !== this._doc
    ) {
      const set = new Set();
      for (const s of this._suspectRefs()) set.add(s.id);
      this._suspectRefsMemo = set;
      this._suspectRefsMemoDoc = this._doc;
    }
    return this._suspectRefsMemo;
  }

  // _editRef asks the parent frame to open the ref-edit modal for
  // the given ref id. Sends the ref's structured fields inline so
  // the shell can seed the modal without re-parsing the JATS. Same
  // postMessage split as _mergeRef.
  _editRef(refID) {
    if (!refID) return;
    if (!(window.parent && window.parent !== window)) {
      // eslint-disable-next-line no-console
      console.warn("edit-ref requires the shell — open via References tab");
      return;
    }
    const target = this._doc.querySelector(`[id="${CSS.escape(refID)}"]`);
    if (!target) return;
    const fields = this._refToManualFields(target);
    window.parent.postMessage(
      {
        type: "hive:edit-ref",
        sha: this._sha,
        refID: refID,
        fields: fields,
        suspect: this._suspectRefIDs().has(refID),
      },
      "*",
    );
  }

  // _refToManualFields shapes a JATS <ref>'s element-citation into
  // the manualReference field set the shell's ref-edit modal
  // consumes. Same keys as emptyManualReference in app.js so the
  // modal can populate its form directly. Missing fields default
  // to "" so the form doesn't render "undefined".
  _refToManualFields(refNode) {
    const empty = {
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
    const ec =
      this._directChild(refNode, "element-citation") ||
      this._directChild(refNode, "mixed-citation");
    if (!ec) {
      empty.citation = (refNode.textContent || "").trim();
      return empty;
    }
    const d = this._elementCitationToData(ec);
    return {
      ...empty,
      author: d.authors.join(", "),
      title: d.articleTitle
        ? (d.articleTitle.textContent || "").trim()
        : "",
      container_title: d.source
        ? (d.source.textContent || "").trim()
        : "",
      issued: d.year || "",
      volume: d.volume || "",
      issue: d.issue || "",
      page: d.page || "",
      publisher: d.publisher || "",
      publisher_place: d.publisherLoc || "",
      doi: d.doi || "",
      citation: this._refPreviewText(refNode),
    };
  }

  // _renderFigModal renders the currently-open figure/table modal.
  // Two content tabs when both views are available:
  //   - Digitized — structured HTML (rendered table for
  //     table-wraps; SVG for figures once tracing lands)
  //   - Original — extracted PNG crop from the source PDF
  //
  // Tables get both tabs (Digitized is the default for the
  // structured, searchable, decimal-aligned view). Figures today
  // get only Original since we have no structured version; once
  // SVG tracing arrives figures will grow a Digitized tab.
  //
  // Also emits caption + "Open in PDF" link below the content
  // regardless of active tab. Close via X, Esc, backdrop click.
  _renderFigModal() {
    if (!this._modalFigID) return nothing;
    const target = this._doc.querySelector(`[id="${CSS.escape(this._modalFigID)}"]`);
    if (!target) return nothing;
    const isTable = target.localName === "table-wrap";
    // Prefer caption-extracted label; when a caption dropped its
    // "Figure N" / "Table N" prefix (a GROBID gap on some layouts)
    // reuse the same two-pass gap-fill logic that _renderFiguresList
    // applies so the modal header matches what the list shows.
    const label =
      this._figLabelFromCaption(target) ||
      this._gapFillLabelForTarget(target) ||
      this._modalFigID;
    const page =
      target.getAttributeNS(HIVE_NS, "pdf-page") ||
      target.getAttribute("hive:pdf-page") ||
      "";
    // pdf-page is a raw PDF index; the journal-labeled page
    // curators cite is pdf-page + offset (offset persisted on
    // <article> by jatsconv). Display uses journal page when we
    // have the offset; the PDF-jump URL still uses raw pdf-page
    // because pdf.js indexes pages from 1 without knowing about
    // journal labels.
    const offset = this._pdfPageOffset();
    const journalPage = page
      ? String((Number.parseInt(page, 10) || 0) + offset)
      : "";
    const displayPage = offset && journalPage ? journalPage : page;
    const caption = this._directChild(target, "caption");
    const imgURL = this._sha
      ? `/api/reference/library/${encodeURIComponent(this._sha)}/figures/${encodeURIComponent(this._modalFigID)}`
      : "";
    const pdfURL = page && this._sha
      ? `/api/reference/library/${encodeURIComponent(this._sha)}/source#page=${page}`
      : "";
    // Digitized tab requires structured content: a table-wrap with
    // a real <table> child (rendered as HTML in _renderTableWrap).
    // Curator-added regions (added via the Digitize-tab bbox tool)
    // are table-wraps *without* a <table> child — they only carry
    // a bbox + caption placeholder, so there's no structured view
    // to render yet. Default to Original for those.
    const hasDigitized = isTable && !!this._directChild(target, "table");
    const hasOriginal = !!imgURL;
    const activeTab = this._modalTab || (hasDigitized ? "digitized" : "original");
    return html`
      <div
        class="fig-modal-backdrop"
        @click=${(e) => {
          if (e.target === e.currentTarget) this._closeFigModal();
        }}
      >
        <div class="fig-modal" role="dialog" aria-modal="true" aria-labelledby="fig-modal-title">
          <header class="fig-modal-header">
            <div class="fig-modal-title-group">
              <h2 id="fig-modal-title">${label}</h2>
              ${page
                ? html`<a
                    class="fig-modal-header-page"
                    href=${pdfURL}
                    title="Open source PDF at page ${displayPage}"
                    @click=${(e) => this._jumpToFig(e, this._modalFigID, page)}
                  ><svg
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      aria-hidden="true"
                    ><path d="M15 3h6v6" /><path d="M10 14 21 3" /><path
                      d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"
                    /></svg>Page ${displayPage}</a>`
                : nothing}
            </div>
            <button
              class="fig-modal-close"
              type="button"
              aria-label="Close"
              @click=${() => this._closeFigModal()}
            >✕</button>
          </header>
          ${hasDigitized && hasOriginal
            ? html`<div class="fig-modal-tabs" role="tablist">
                <button
                  role="tab"
                  class=${"fig-modal-tab" + (activeTab === "digitized" ? " active" : "")}
                  aria-selected=${activeTab === "digitized"}
                  @click=${() => (this._modalTab = "digitized")}
                >Digitized</button>
                <button
                  role="tab"
                  class=${"fig-modal-tab" + (activeTab === "original" ? " active" : "")}
                  aria-selected=${activeTab === "original"}
                  @click=${() => (this._modalTab = "original")}
                >Original</button>
              </div>`
            : nothing}
          <div class="fig-modal-body">
            ${activeTab === "digitized" && isTable
              ? html`<div class="fig-modal-table">${this._renderTableWrap(target)}</div>`
              : nothing}
            ${activeTab === "original" && hasOriginal
              ? html`<img class="fig-modal-img" src=${imgURL} alt=${label}
                  @error=${(e) => (e.target.style.display = "none")}
                />`
              : nothing}
            ${caption
              ? html`<div class="fig-modal-caption">${this._renderNodes(caption.childNodes)}</div>`
              : nothing}
          </div>
        </div>
      </div>
    `;
  }

  // _renderTableWrap renders a <table-wrap>'s inner <table> as
  // HTML for modal display. Recurses via _renderNodes so cell
  // contents (including nested tp:taxon-name chips) survive.
  _renderTableWrap(tableWrap) {
    const table = this._directChild(tableWrap, "table");
    if (!table) return nothing;
    return html`<table>${this._renderNodes(table.childNodes)}</table>`;
  }

  // _renderNoiseMarker renders a <hive:noise correction-id="…">
  // element as a compact red-dot marker. The marker sits inline
  // where the removed paragraph used to be, so a curator sees at a
  // glance that noise was removed at that spot. Tooltip carries the
  // removed text (recovered from the child XML comment) so the
  // curator can verify without opening a modal. Click stages an
  // un-remove by sending hive:restore-text to the shell.
  //
  // Standalone reader (no parent shell): the marker still renders
  // but the click is a no-op with a console warning — restoring a
  // removal needs the correction context the shell holds.
  _renderNoiseMarker(el) {
    const correctionID = el.getAttribute("correction-id") || "";
    // The child comment is the removed-text hint. commentText is
    // the plain-text bytes between "<!--" and "-->". Reverse the
    // hyphen-ZWSP-hyphen sanitize so tooltips read naturally.
    let removedText = "";
    for (const child of el.childNodes) {
      if (child.nodeType === Node.COMMENT_NODE) {
        removedText = (child.nodeValue || "").trim();
        removedText = removedText.replace(/-​-/g, "--");
        break;
      }
    }
    const title = removedText
      ? "Removed: " + removedText + "\nClick to restore"
      : "Removed noise — click to restore";
    return html`<span
      class="hive-noise-marker"
      role="button"
      tabindex="0"
      title=${title}
      data-correction-id=${correctionID}
      @click=${(e) => {
        e.stopPropagation();
        this._restoreNoise(correctionID);
      }}
      @keydown=${(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          this._restoreNoise(correctionID);
        }
      }}
    >·</span>`;
  }

  // _restoreNoise asks the shell to post a text-restore correction
  // for the given remove-correction id. The shell owns the fetch
  // (it has the archive context + reference id); the reader stays a
  // pure viewer.
  _restoreNoise(removeCorrectionID) {
    if (!removeCorrectionID) return;
    if (!(window.parent && window.parent !== window)) {
      // eslint-disable-next-line no-console
      console.warn("restore-text requires the shell — open via References tab");
      return;
    }
    window.parent.postMessage(
      {
        type: "hive:restore-text",
        sha: this._sha,
        removeCorrectionID,
      },
      "*",
    );
  }

  // _renderTaxonName renders a <tp:taxon-name> as a styled chip.
  // Children render recursively via _renderNodes so nested
  // <tp:taxon-name> elements appear as chips-within-chips; because
  // the chip backgrounds use alpha color-mix, the nested region
  // shows visually darker where the two chip backgrounds stack —
  // no extra styling needed to make overlap distinctive.
  //
  // Nested chips occur when GNfinder returns overlapping matches
  // for one source range — e.g. "Mytilaspis (Lepidosaphes)
  // pallida" produces an outer detection of the binomial and an
  // inner detection of the subgenus (which is also a valid genus
  // name in some classifications). Both are correct
  // identifications and both survive into the rendered chip.
  //
  // Abbreviation expansion still works on the outer chip: when
  // the wrapped text starts with a letter-then-period abbrev
  // (e.g. "C. strepitans") and "Expand abbreviations" is toggled,
  // the display swaps to the resolved full-name. That branch
  // falls back to opaque text (no nested chip rendering) because
  // the resolved full-name is just a string.
  _renderTaxonName(el) {
    const original = el.textContent || "";
    const fullName = el.getAttribute("full-name") || "";
    const source =
      el.getAttributeNS(GN_NS, "source") ||
      el.getAttribute("gn:source") ||
      el.getAttribute("source") ||
      "";
    const score =
      el.getAttributeNS(GN_NS, "score") ||
      el.getAttribute("gn:score") ||
      el.getAttribute("score") ||
      "";
    const candidates =
      el.getAttributeNS(GN_NS, "candidates") ||
      el.getAttribute("gn:candidates") ||
      el.getAttribute("candidates") ||
      "";
    const cls = source || "plain";
    const isAbbrev = /^[A-Za-z]{1,3}\.\s/.test(original);
    const expanded = this._showExpanded && isAbbrev && fullName;
    let title = fullName || original;
    if (source === "verified" && score) title += ` (verified · score ${score})`;
    else if (source === "ambiguous") title += ` (ambiguous · candidates: ${candidates})`;
    else if (source === "unverified") title += ` (unverified)`;
    else if (source === "unresolved") title += ` (unresolved abbreviation)`;
    const body = expanded ? fullName : this._renderNodes(el.childNodes);
    return html`<span
      class="taxon-name ${cls}"
      title=${title}
      data-full-name=${fullName}
      >${body}</span
    >`;
  }

  // _renderRef formats a JATS <ref> as a readable citation line.
  // Accepts either <element-citation> (structured; the form jatsconv
  // emits) or <mixed-citation> (prose-with-embedded-tags; some
  // publishers use this style). Falls back to the raw text content
  // when neither wrapper is present.
  //
  // Output format: "Author A, Coauthor B (Year). Article title.
  // Journal (italic) Volume(Issue): pages. doi:X". Missing fields
  // elide gracefully. Nested <tp:taxon-name> chips in titles are
  // preserved because titles render via _renderNodes recursion.
  _renderRef(el) {
    const ec =
      this._directChild(el, "element-citation") ||
      this._directChild(el, "mixed-citation");
    if (!ec) {
      // Nothing structured to work with — render the raw children.
      return html`${this._renderNodes(el.childNodes)}`;
    }
    const d = this._elementCitationToData(ec);
    // Compose. Each segment renders only when it has content;
    // punctuation between segments joins conditionally so omitted
    // fields don't leave stranded commas or periods.
    const parts = [];
    if (d.authors.length) parts.push(html`${d.authors.join(", ")}`);
    if (d.year) parts.push(html` (${d.year}).`);
    else if (d.authors.length) parts.push(html`.`);
    if (d.articleTitle) {
      parts.push(html` ${this._renderNodes(d.articleTitle.childNodes)}.`);
    }
    if (d.source) {
      parts.push(html` <i>${this._renderNodes(d.source.childNodes)}</i>`);
    }
    if (d.volume) {
      let volPart = ` ${d.volume}`;
      if (d.issue) volPart += `(${d.issue})`;
      parts.push(volPart);
    }
    if (d.page) parts.push(`: ${d.page}`);
    parts.push(".");
    if (d.publisher) {
      parts.push(` ${d.publisher}`);
      if (d.publisherLoc) parts.push(`, ${d.publisherLoc}`);
      parts.push(".");
    }
    if (d.doi) {
      const doiURL = `https://doi.org/${d.doi}`;
      parts.push(html` <a href=${doiURL} target="_blank" rel="noopener">doi:${d.doi}</a>`);
    }
    // Trailing <comment> children carry text merged in from a
    // ref-merge correction — the fragment GROBID had originally
    // tokenized as its own ref. Show it in a distinct visual
    // register (dimmed + prefixed) so curators can see what was
    // absorbed without mistaking it for structured citation data.
    for (const child of el.querySelectorAll("comment")) {
      const t = (child.textContent || "").trim();
      if (t) {
        parts.push(html` <span class="ref-merged"> [merged: ${t}]</span>`);
      }
    }
    return html`${parts}`;
  }

  // _directChild returns the first child element with the given
  // local name, or null. Namespace-agnostic (matches on localName)
  // so prefix/namespace mismatches between the input document and
  // the code's expectations don't break lookup.
  _directChild(el, localName) {
    if (!el) return null;
    for (const child of el.children) {
      if (child.localName === localName) return child;
    }
    return null;
  }

  _directChildText(el, localName) {
    const c = this._directChild(el, localName);
    return c ? (c.textContent || "").trim() : "";
  }
}

// bibTexEscape escapes BibTeX value special characters so
// pasted-into-.bib output doesn't parse-fail. Curly braces would
// close the value's outer brace early; backslash / percent /
// ampersand / hash / underscore / tilde are BibTeX-reserved and
// need escaping via backslash. Scientific-name titles rarely
// trigger any of these, but a defensive escape keeps output
// robust across the variety of upstream sources.
function bibTexEscape(s) {
  return String(s || "")
    .replace(/\\/g, "\\\\")
    .replace(/[{}]/g, "\\$&")
    .replace(/[&%#_~$]/g, "\\$&");
}

customElements.define("sfga-reader", SfgaReader);
