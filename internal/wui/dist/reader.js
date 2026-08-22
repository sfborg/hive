/**
 * hive WUI — augmented-JATS reader.
 *
 * Standalone page that renders the TaxPub-augmented XML produced by
 * pkg/annotator for a given PDF upload. Curator opens the reader
 * either by navigating to /read.html?sha=... directly or via a
 * popout from a reference detail view. Popout is the multi-monitor
 * use case — a separate browser window lets curators park the
 * reader on one screen and their curation UI on another.
 *
 * Rendering: parses the augmented XML into a DOM, walks the
 * <text><body> subtree, and emits readable HTML with each
 * <tp:taxon-name> element rendered as a styled chip. Chip color
 * (green / amber / orange / red) reflects the gn:source attribute
 * from the annotator's abbreviation-expansion pass — verified,
 * ambiguous, unverified, unresolved.
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
      display: block;
      min-height: 100vh;
      background: var(--bg);
      color: var(--fg);
      font-family: var(--font-body);
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
      height: calc(100vh - 3rem);
      overflow: hidden;
      position: relative;
    }
    .layout.paginated article {
      height: 100%;
      max-width: none;
      margin: 0;
      padding: var(--sp-4) var(--sp-6);
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
    /* Fallback wrapper for GROBID elements without a specific
       renderer (figure, table, unknown). Gives content basic block
       layout so it doesn't collapse into an inline run beside
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
       author/title/imprint text that renders as an inline run per
       entry is still readable, and hanging indent so wrapped lines
       align to the text rather than the number. */
    article .references li {
      margin: var(--sp-2) 0;
      padding-left: var(--sp-1);
    }
    /* Inline reference citations ("(Doe 2020)"). Dimmer than body
       text so they read as parenthetical. */
    article .cite {
      color: var(--dim);
    }
    /* Chip styling per gn:source. Verified is the confident state
       (accent-tinted); ambiguous / unverified sit in the middle
       (amber / orange); unresolved is red. Untagged chips (plain
       full-form names GNfinder detected) get a subtle underline —
       "we found this" without a color claim about the resolution. */
    .taxon-name {
      display: inline;
      padding: 0.05rem 0.3rem;
      border-radius: 3px;
      border: 1px solid transparent;
      cursor: help;
      font-style: italic;
    }
    .taxon-name.plain {
      border-bottom-color: var(--dim);
      border-bottom-style: dashed;
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
      case "PageDown":
      case " ":
        e.preventDefault();
        this._pageForward();
        break;
      case "ArrowLeft":
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

  _recomputePages() {
    if (!this._paginated) {
      this._pageCount = 1;
      this._page = 0;
      return;
    }
    const article = this.renderRoot.querySelector(".layout.paginated article");
    if (!article) return;
    // Reset transform so scrollWidth reflects true content width
    // (transform doesn't affect layout metrics but reset for clarity).
    article.style.transform = "";
    const paneWidth = article.offsetWidth;
    const contentWidth = article.scrollWidth;
    const pages = Math.max(1, Math.ceil(contentWidth / paneWidth));
    this._pageCount = pages;
    if (this._page >= pages) this._page = pages - 1;
    this._applyPageTransform();
  }

  _applyPageTransform() {
    const article = this.renderRoot.querySelector(".layout.paginated article");
    if (!article) return;
    const offset = this._page * article.offsetWidth;
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
              >
                ${this._renderBody()}
              </div>
            `
        : nothing}
    `;
  }

  // _renderBody walks the TEI <text><body> subtree of the augmented
  // document, converting known elements to their HTML equivalents.
  // Anything outside <text> (the header, sourceDesc, encoding
  // metadata) is skipped — the reader is for paper text, not
  // machine-readable metadata. Reference-list divs inside <back>
  // are also skipped for the same reason (references have their
  // own curator UI).
  _renderBody() {
    const teiNS = "http://www.tei-c.org/ns/1.0";
    const text =
      this._doc.getElementsByTagNameNS(teiNS, "text")[0] ||
      this._doc.getElementsByTagName("text")[0];
    if (!text) {
      // No <text> wrapper — fall back to <body> alone.
      const bodies =
        this._doc.getElementsByTagNameNS(teiNS, "body").length > 0
          ? this._doc.getElementsByTagNameNS(teiNS, "body")
          : this._doc.getElementsByTagName("body");
      if (bodies.length === 0) {
        return html`<p class="empty">document has no &lt;body&gt; content</p>`;
      }
      return html`<article>${this._renderNodes(bodies[0].childNodes)}</article>`;
    }
    // Paper identity header (title / byline / abstract) at the
    // top of the article. Copy buttons live in the sticky top
    // nav (see render()), not here — keeps the article clean.
    // Render <body> and <back> contents in document order.
    const meta = this._extractPaperMeta();
    return html`
      <article>
        ${this._renderPaperHeader(meta)}
        ${this._renderNodes(text.childNodes)}
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

  // _extractPaperMeta pulls the paper-identity fields out of
  // <teiHeader> and precomputes the three copy-buffer strings
  // (plain / CSL-JSON / BibTeX). Called once per render — cheap;
  // no memoization needed. Returns null when teiHeader is absent.
  _extractPaperMeta() {
    const teiNS = "http://www.tei-c.org/ns/1.0";
    const teiHeader = this._doc.getElementsByTagNameNS(teiNS, "teiHeader")[0];
    if (!teiHeader) return null;
    const biblStruct = this._findDescendant(teiHeader, "biblStruct");
    const data = biblStruct ? this._biblStructToData(biblStruct) : null;
    const title = this._paperTitle(teiHeader, data);
    const authors = data ? data.authors : [];
    const abstractParas = this._extractAbstract(teiHeader);
    const plain = data ? this._formatPlainCitation(title, data) : "";
    const bibtex = data ? this._formatBibTeX(title, data) : "";
    const cslJSON = data ? this._formatCSLJSON(title, data) : "";
    const doiURL = data && data.doi ? `https://doi.org/${data.doi}` : "";
    return { title, authors, abstractParas, plain, bibtex, cslJSON, doiURL };
  }

  _paperTitle(teiHeader, biblData) {
    // Prefer titleStmt/title (canonical GROBID location); fall
    // back to the analytic title in the biblStruct.
    const ts = this._findDescendant(teiHeader, "titleStmt");
    if (ts) {
      const t = this._directChild(ts, "title");
      if (t) return (t.textContent || "").trim();
    }
    if (biblData && biblData.analyticTitleText) return biblData.analyticTitleText;
    return "";
  }

  _extractAbstract(teiHeader) {
    const abs = this._findDescendant(teiHeader, "abstract");
    if (!abs) return [];
    const paras = [];
    for (const child of abs.children) {
      if (child.localName === "p") {
        const t = (child.textContent || "").trim();
        if (t) paras.push(t);
      }
    }
    // Fallback: some GROBID output puts abstract text directly
    // inside <abstract> without a <p> wrapper.
    if (paras.length === 0) {
      const t = (abs.textContent || "").trim();
      if (t) paras.push(t);
    }
    return paras;
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
  // from the biblStruct data. Same shape as the reference-list
  // items but flattened to text — no HTML, so it copies cleanly
  // into a chat / email.
  _formatPlainCitation(title, data) {
    const parts = [];
    if (data.authors.length) parts.push(data.authors.join(", "));
    if (data.year) parts.push(` (${data.year}).`);
    else if (data.authors.length) parts.push(".");
    if (title) parts.push(` ${title}.`);
    if (data.monogrTitleText) parts.push(` ${data.monogrTitleText}`);
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
  // the biblStruct data. Values wrap in double braces to preserve
  // scientific-name capitalization (BibTeX lowercases title-cased
  // words by default in most bibliography styles). Author list
  // joined with " and " per BibTeX convention.
  _formatBibTeX(title, data) {
    const kind = data.monogrTitleText ? "article" : "misc";
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
    if (title) lines.push(`  title = {{${bibTexEscape(title)}}},`);
    if (data.monogrTitleText)
      lines.push(`  journal = {${bibTexEscape(data.monogrTitleText)}},`);
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
  // Type inference is minimal: monogr title present → article-
  // journal; monogr title absent but publisher present → book;
  // otherwise misc. Book chapters need a future refinement (need
  // to detect "analytic AND monogr AND publisher" as the chapter
  // signal).
  _formatCSLJSON(title, data) {
    let type = "article-journal";
    if (!data.monogrTitleText && data.publisher) type = "book";
    else if (!data.monogrTitleText && !data.publisher) type = "article";
    const firstAuthor = data.authors[0] || "unknown";
    const firstAuthorKey = firstAuthor
      .toLowerCase()
      .replace(/[^a-z0-9]/g, "")
      .slice(0, 20);
    const record = {
      id: `${firstAuthorKey}${data.year || ""}`,
      type,
    };
    if (title) record.title = title;
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
    if (data.monogrTitleText) record["container-title"] = data.monogrTitleText;
    if (data.volume) record.volume = data.volume;
    if (data.issue) record.issue = data.issue;
    if (data.page) record.page = data.page;
    if (data.publisher) record.publisher = data.publisher;
    if (data.pubPlace) record["publisher-place"] = data.pubPlace;
    if (data.doi) record.DOI = data.doi;
    return JSON.stringify(record, null, 2);
  }

  // _biblStructToData extracts the fields hive cares about from a
  // GROBID biblStruct into a plain object. Shared by the header
  // citation block and the reference-list formatter — the input
  // shape is identical for the paper's own biblStruct in
  // <teiHeader> and each cited reference in <back>.
  _biblStructToData(el) {
    const analytic = this._directChild(el, "analytic");
    const monogr = this._directChild(el, "monogr");
    const authorsFrom = analytic || monogr;
    const authors = authorsFrom ? this._collectAuthors(authorsFrom) : [];
    const year = monogr ? this._extractYear(monogr) : "";
    const analyticTitle = analytic ? this._directChild(analytic, "title") : null;
    const monogrTitle = monogr ? this._directChild(monogr, "title") : null;
    const imprint = monogr ? this._directChild(monogr, "imprint") : null;
    return {
      analytic,
      monogr,
      analyticTitle,
      analyticTitleText: analyticTitle ? (analyticTitle.textContent || "").trim() : "",
      monogrTitle,
      monogrTitleText: monogrTitle ? (monogrTitle.textContent || "").trim() : "",
      authors,
      year,
      volume: this._extractBiblScope(imprint, "volume"),
      issue: this._extractBiblScope(imprint, "issue"),
      page: this._extractPage(imprint),
      publisher: imprint ? this._directChildText(imprint, "publisher") : "",
      pubPlace: imprint ? this._directChildText(imprint, "pubPlace") : "",
      doi: this._extractIdno(el, "DOI"),
    };
  }

  // _findDescendant walks a subtree looking for the first element
  // with the given local name. Namespace-agnostic. Used to locate
  // teiHeader-nested elements (titleStmt, biblStruct, abstract)
  // without needing to hard-code the intermediate wrappers.
  _findDescendant(root, localName) {
    if (!root) return null;
    const stack = [root];
    while (stack.length) {
      const el = stack.shift();
      for (const child of el.children) {
        if (child.localName === localName) return child;
        stack.push(child);
      }
    }
    return null;
  }

  // _renderNodes converts a NodeList into Lit-html templates,
  // dispatching per element/text kind. Recursive: element children
  // render via the same function so nested content (div > p > span)
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
      // <back> in TEI/GROBID holds acknowledgements, funding
      // statements, author contributions, annexes, and the
      // reference list. Recurse — all of it belongs in the reader.
      if (local === "back") {
        out.push(this._renderNodes(node.childNodes));
        continue;
      }
      // <tp:taxon-name> — the annotated chip.
      if (
        (node.namespaceURI === TAXPUB_NS || node.prefix === "tp") &&
        local === "taxon-name"
      ) {
        out.push(this._renderTaxonName(node));
        continue;
      }
      if (local === "head") {
        out.push(html`<h2>${this._renderNodes(node.childNodes)}</h2>`);
        continue;
      }
      // GROBID emits <label>N</label> next to <head>Figure N .</head>
      // and <head>Table N .</head> — the number in the label just
      // duplicates the number already in the head text. Suppress.
      if (local === "label") {
        continue;
      }
      if (local === "p") {
        out.push(html`<p>${this._renderNodes(node.childNodes)}</p>`);
        continue;
      }
      if (local === "div") {
        out.push(html`<section>${this._renderNodes(node.childNodes)}</section>`);
        continue;
      }
      // GROBID often misclassifies dense passages (species lists,
      // synonymy sections, taxonomic keys) as <figure>. Render them
      // as blocks so the text stays visible instead of collapsing
      // into an inline run alongside siblings.
      if (local === "figure" || local === "figDesc" || local === "table") {
        out.push(html`<div class="passthrough">${this._renderNodes(node.childNodes)}</div>`);
        continue;
      }
      if (local === "list") {
        out.push(html`<ul>${this._renderNodes(node.childNodes)}</ul>`);
        continue;
      }
      if (local === "item") {
        out.push(html`<li>${this._renderNodes(node.childNodes)}</li>`);
        continue;
      }
      // Reference list — GROBID emits <listBibl><biblStruct>… per
      // cited reference. Render as an ordered list so numbering
      // matches "as cited in [12]" body-text references. The
      // biblStruct's internal author/title/imprint structure falls
      // through as inline spans, which produces a run-together
      // reference line — good enough for reading; slice 5's
      // reference-parsing will replace with structured display.
      if (local === "listBibl") {
        out.push(html`<ol class="references">${this._renderNodes(node.childNodes)}</ol>`);
        continue;
      }
      if (local === "biblStruct") {
        out.push(html`<li>${this._renderBiblStruct(node)}</li>`);
        continue;
      }
      // Cited-reference marker inside body text. Render inline —
      // "as seen in (Doe 2020)" — so it flows with surrounding
      // sentence text. Passthrough of children preserves any nested
      // <tp:taxon-name> chips.
      if (local === "ref") {
        out.push(html`<span class="cite">${this._renderNodes(node.childNodes)}</span>`);
        continue;
      }
      // Unknown element — wrap in an inline span. TEI's vocabulary
      // skews heavily inline (rs, hi, seg, name, persName, …); the
      // few block-ish elements we've seen in GROBID output (div,
      // figure, table, list) are all handled explicitly above. A
      // wrong-way-round mistake on an inline element (wrapping rs
      // in a block) shatters paragraphs into per-name lines; the
      // reverse mistake (inline wrapper on a truly block element)
      // just runs adjacent content together, which is milder.
      out.push(html`<span>${this._renderNodes(node.childNodes)}</span>`);
    }
    return out;
  }

  _renderTaxonName(el) {
    const original = el.textContent || "";
    const fullName = el.getAttribute("full-name") || "";
    // gn:source lives in the GN namespace; access via getAttributeNS
    // so a re-encoded document with different prefix still resolves.
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
    const display =
      this._showExpanded && isAbbrev && fullName ? fullName : original;
    let title = fullName || original;
    if (source === "verified" && score) title += ` (verified · score ${score})`;
    else if (source === "ambiguous") title += ` (ambiguous · candidates: ${candidates})`;
    else if (source === "unverified") title += ` (unverified)`;
    else if (source === "unresolved") title += ` (unresolved abbreviation)`;
    return html`<span
      class="taxon-name ${cls}"
      title=${title}
      data-full-name=${fullName}
      >${display}</span
    >`;
  }

  // _renderBiblStruct pulls fields from a GROBID biblStruct and
  // formats them as a readable citation. Structure differs by
  // publication kind:
  //
  //   Journal article: <analytic> holds article title + authors;
  //                    <monogr> holds journal title + imprint.
  //   Book:            <monogr> holds title + authors + imprint;
  //                    no <analytic>.
  //   Book chapter:    <analytic> holds chapter title + authors;
  //                    <monogr> holds book title + editors +
  //                    imprint.
  //
  // Output format: "Author A, Coauthor B (Year). Article title.
  // Journal (italic) Volume(Issue): pages. doi:X". Missing fields
  // elide gracefully. Nested <tp:taxon-name> chips in titles are
  // preserved because titles render via _renderNodes recursion.
  _renderBiblStruct(el) {
    const d = this._biblStructToData(el);
    // Compose. Each segment renders only when it has content;
    // punctuation between segments joins conditionally so
    // omitted fields don't leave stranded commas or periods.
    const parts = [];
    if (d.authors.length) parts.push(html`${d.authors.join(", ")}`);
    if (d.year) parts.push(html` (${d.year}).`);
    else if (d.authors.length) parts.push(html`.`);
    if (d.analyticTitle) {
      parts.push(html` ${this._renderNodes(d.analyticTitle.childNodes)}.`);
    }
    if (d.monogrTitle) {
      parts.push(html` <i>${this._renderNodes(d.monogrTitle.childNodes)}</i>`);
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
      if (d.pubPlace) parts.push(`, ${d.pubPlace}`);
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
  // so it works whether GROBID's TEI is in the tei-c namespace or
  // was flattened during a round-trip.
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

  // _collectAuthors returns an array of formatted author strings
  // ("Smith AB") from the direct-child <author><persName> elements
  // under the given parent. Handles both forename + surname and
  // surname-only cases; ignores authors GROBID couldn't parse.
  _collectAuthors(parent) {
    const authors = [];
    for (const child of parent.children) {
      if (child.localName !== "author") continue;
      const persName = this._directChild(child, "persName");
      if (!persName) continue;
      const forenames = [];
      let surname = "";
      for (const p of persName.children) {
        if (p.localName === "forename") {
          const t = (p.textContent || "").trim();
          if (t) forenames.push(t[0]); // initial only, standard biblio style
        } else if (p.localName === "surname") {
          surname = (p.textContent || "").trim();
        }
      }
      if (!surname) continue;
      const initials = forenames.join("");
      authors.push(initials ? `${surname} ${initials}` : surname);
    }
    return authors;
  }

  _extractYear(monogr) {
    const imprint = this._directChild(monogr, "imprint");
    if (!imprint) return "";
    const date = this._directChild(imprint, "date");
    if (!date) return "";
    const when = date.getAttribute("when") || date.textContent || "";
    const m = /(\d{4})/.exec(when);
    return m ? m[1] : "";
  }

  _extractBiblScope(imprint, unit) {
    if (!imprint) return "";
    for (const child of imprint.children) {
      if (child.localName === "biblScope" && child.getAttribute("unit") === unit) {
        return (child.textContent || "").trim();
      }
    }
    return "";
  }

  // _extractPage handles both inline-text pages ("1-20") and the
  // from/to attribute form GROBID emits for numeric page ranges.
  _extractPage(imprint) {
    if (!imprint) return "";
    for (const child of imprint.children) {
      if (child.localName !== "biblScope") continue;
      if (child.getAttribute("unit") !== "page") continue;
      const from = child.getAttribute("from");
      const to = child.getAttribute("to");
      if (from && to && from !== to) return `${from}-${to}`;
      if (from) return from;
      const text = (child.textContent || "").trim();
      if (text) return text;
    }
    return "";
  }

  _extractIdno(el, type) {
    for (const child of el.children) {
      if (
        child.localName === "idno" &&
        (child.getAttribute("type") || "").toUpperCase() === type.toUpperCase()
      ) {
        return (child.textContent || "").trim();
      }
    }
    return "";
  }
}

// bibTexEscape escapes BibTeX value special characters so
// pasted-into-.bib output doesn't parse-fail. Curly braces would
// close the value's outer brace early; backslash / percent /
// ampersand / hash / underscore / tilde are BibTeX-reserved and
// need escaping via backslash. Scientific-name titles rarely
// trigger any of these, but a defensive escape keeps output
// robust across GROBID's variety.
function bibTexEscape(s) {
  return String(s || "")
    .replace(/\\/g, "\\\\")
    .replace(/[{}]/g, "\\$&")
    .replace(/[&%#_~$]/g, "\\$&");
}

customElements.define("sfga-reader", SfgaReader);
