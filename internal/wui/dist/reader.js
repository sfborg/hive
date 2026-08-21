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
  };

  static styles = css`
    :host {
      display: block;
      min-height: 100vh;
      background: var(--bg);
      color: var(--fg);
      font-family: var(--font-body);
    }
    header {
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
    }
    header h1 {
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
    /* Layout modes: single-column reader by default (article max-
       width centered); split when the PDF pane is visible (two
       equal columns filling the viewport height below the sticky
       header). The article in split mode drops its centered max-
       width so the text can breathe in the narrower column. */
    .layout {
      display: block;
    }
    .layout.split {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
      height: calc(100vh - 3rem);
      gap: 0;
    }
    .layout.split article {
      max-width: none;
      overflow: auto;
      padding: var(--sp-4);
      margin: 0;
    }
    .layout.split .pdf {
      width: 100%;
      height: 100%;
      border: 0;
      border-right: 1px solid var(--border);
    }
    article {
      max-width: 44rem;
      margin: 0 auto;
      padding: var(--sp-4) var(--sp-6) var(--sp-8);
      font-size: var(--fs-md);
      line-height: 1.6;
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
    article ul {
      margin: var(--sp-2) 0;
      padding-left: var(--sp-4);
    }
    article li {
      margin: var(--sp-1) 0;
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
              @change=${(e) => (this._showPDF = e.target.checked)}
            />
            Show PDF
          </label>
        </div>
      </header>
      ${this._busy ? html`<p class="empty" role="status">Loading…</p>` : nothing}
      ${this._error
        ? html`<p class="error" role="alert">${this._error}</p>`
        : nothing}
      ${this._doc
        ? html`
            <div class=${"layout" + (this._showPDF ? " split" : "")}>
              ${this._showPDF
                ? html`<iframe
                    class="pdf"
                    src=${`/api/reference/library/${encodeURIComponent(this._sha)}/source#page=1`}
                    title="Source PDF"
                  ></iframe>`
                : nothing}
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
    // Render <body> and <back> contents in document order — the
    // reader shows the whole paper (introduction through
    // references + acknowledgements). The DOI-from-references
    // guard against picking a cited-work DOI as the paper's own
    // DOI lives elsewhere (pkg/grobid.ExtractDOI, scoped to
    // <teiHeader>) so it doesn't need a filter here.
    return html`<article>${this._renderNodes(text.childNodes)}</article>`;
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
      // Cited-reference marker inside body text. Render inline —
      // "as seen in (Doe 2020)" — so it flows with surrounding
      // sentence text. Passthrough of children preserves any nested
      // <tp:taxon-name> chips.
      if (local === "ref") {
        out.push(html`<span class="cite">${this._renderNodes(node.childNodes)}</span>`);
        continue;
      }
      // Unknown element — wrap in a passthrough block so the text
      // gets visible layout instead of collapsing into an inline
      // run. Better than a blank spot in the reader.
      out.push(html`<div class="passthrough">${this._renderNodes(node.childNodes)}</div>`);
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
}

customElements.define("sfga-reader", SfgaReader);
