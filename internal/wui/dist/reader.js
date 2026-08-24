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

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";

// TaxPub + GN namespaces from pkg/annotator. Kept in sync manually
// since Go and JS can't share constants — if these ever change,
// update both places (pkg/annotator/annotator.go and here).
const TAXPUB_NS = "http://exsansi.pensoft.net/pub-med/pub-med-tp/1.0";
const GN_NS = "https://hive.sfborg.org/ns/gn/1.0";

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
    article .fig-xref {
      color: var(--accent);
      text-decoration: underline;
      text-decoration-thickness: 1px;
      text-underline-offset: 2px;
      font-weight: 500;
      cursor: pointer;
    }
    article .fig-xref:hover {
      text-decoration-thickness: 2px;
    }
    article .fig-xref:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
      border-radius: 2px;
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
  }

  disconnectedCallback() {
    document.removeEventListener("keydown", this._onKeyDown);
    window.removeEventListener("resize", this._onResize);
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
  }

  _handleKey(e) {
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
      ${this._doc
        ? this._showPDF
          ? html`<iframe
              class="pdf-full"
              src=${`/api/reference/library/${encodeURIComponent(this._sha)}/source#page=1`}
              title="Source PDF"
            ></iframe>`
          : html`
              <div class=${"layout" + (this._paginated ? " paginated" : "")}
                @click=${(e) => this._paginated && this._onPageClick(e)}
                @wheel=${(e) => this._paginated && this._onPageWheel(e)}
              >
                ${this._renderBody()}
              </div>
            `
        : nothing}
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
        out.push(html`<p>${this._renderNodes(node.childNodes)}</p>`);
        continue;
      }
      if (local === "sec") {
        out.push(html`<section>${this._renderNodes(node.childNodes)}</section>`);
        continue;
      }
      // <fig> gets an explicit rendering: caption text stays as
      // block passthrough (chips inside caption prose remain
      // visible), but a "→ jump to page N" affordance is emitted
      // before the caption when the figure carries gn:pdf-page
      // (from GROBID's coords). The jump-affordance is what
      // curators actually click to reach the PDF — sparse inline
      // xrefs in body prose are easy to miss, so making the fig
      // itself clickable is the reliable discovery path.
      if (local === "fig") {
        const figID = node.getAttribute("id") || "";
        const page =
          node.getAttributeNS(GN_NS, "pdf-page") ||
          node.getAttribute("gn:pdf-page") ||
          "";
        out.push(html`
          <div class="passthrough fig-block">
            ${page
              ? html`<a
                  class="fig-jump"
                  href="#${figID}"
                  @click=${(e) => this._jumpToFig(e, figID, page)}
                  title="Open the source PDF at this figure's page"
                  >Open in PDF — page ${page}</a>`
              : nothing}
            ${this._renderNodes(node.childNodes)}
          </div>
        `);
        continue;
      }
      // Captions and table wrappers pass through as blocks so
      // their internal text (which often carries taxon chips)
      // stays legible instead of collapsing into an inline run
      // alongside siblings.
      if (local === "caption" || local === "table-wrap") {
        out.push(html`<div class="passthrough">${this._renderNodes(node.childNodes)}</div>`);
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
        out.push(html`<li>${this._renderRef(node)}</li>`);
        continue;
      }
      // Cited-reference anchor inside body text — JATS <xref>.
      // For xrefs whose rid points at a <fig>, render as a
      // clickable link that posts a message to the parent frame
      // asking to switch to the Digitize tab and scroll the PDF
      // pane to the figure's source page (from GROBID's coords,
      // stored as gn:pdf-page). Non-fig xrefs (bibliography
      // references, other in-doc targets) render as inline styled
      // text — same behavior as before.
      if (local === "xref") {
        const rid = node.getAttribute("rid");
        // querySelector, not getElementById — for XML documents
        // (which DOMParser produces for application/xml), id-based
        // lookup only works when the DTD declares "id" as an
        // ID-type attribute. Our JATS ships without a DTD load, so
        // getElementById returns null on every rid → every fig-xref
        // fell through to the non-clickable branch. querySelector
        // does attribute-value matching directly and works
        // regardless of DTD. CSS.escape guards against rids that
        // contain selector-special characters (colons, dots, etc.).
        const target = rid
          ? this._doc.querySelector(`[id="${CSS.escape(rid)}"]`)
          : null;
        if (target && target.localName === "fig") {
          const page =
            target.getAttributeNS(GN_NS, "pdf-page") ||
            target.getAttribute("gn:pdf-page") ||
            "";
          // No href — we don't want the browser to scroll the
          // reader iframe to the fig anchor in the trailing
          // "Figures" sec. The click handler is the ONLY thing
          // that fires: posts a message to the parent, which
          // switches to Digitize and jumps the PDF pane to the
          // figure's source page. role=button + tabindex keep it
          // keyboard-accessible without navigation semantics.
          out.push(html`<span
            class="cite fig-xref"
            role="button"
            tabindex="0"
            @click=${(e) => this._jumpToFig(e, rid, page)}
            @keydown=${(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                this._jumpToFig(e, rid, page);
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
