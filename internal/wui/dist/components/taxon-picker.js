/**
 * <sfga-taxon-picker>
 *
 * Text field for the higher-taxon reference filter with a popover
 * that browses one verifier source's classification of the names
 * mentioned in indexed papers, with a paper count on each taxon.
 *
 * Focusing the field opens the classification at its top levels.
 * Typing narrows it to the taxa whose names start with the text,
 * shown with their ancestors. Enter applies the highlighted taxon (or
 * the typed text when nothing matches); clicking a taxon applies it;
 * Esc closes and keeps the current filter. Chains of taxa with a
 * single child are shown as one row.
 *
 * Properties: value (the applied name), source ("ncbi" | "col"),
 * minMentions. Fires `pick` with detail {name} ("" clears).
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";
import { api } from "/lib/api.js";

const ROOT = "|";

// parentPath returns the path of a node's parent ("|A|B|" → "|A|").
const parentPath = (p) => p.slice(0, p.lastIndexOf("|", p.length - 2) + 1);

// newStore returns an empty view of the classification: the nodes
// loaded so far, each loaded parent's children in display order, and
// the expanded nodes.
const newStore = () => ({ children: new Map(), expanded: new Set(), matches: new Set() });

class SfgaTaxonPicker extends LitElement {
  static properties = {
    value: { type: String },
    source: { type: String },
    minMentions: { type: Number },
    placeholder: { type: String },

    _open: { state: true },
    _text: { state: true },
    _active: { state: true },
    _loading: { state: true },
    _error: { state: true },
    _version: { state: true },
  };

  static styles = css`
    :host {
      position: relative;
      display: inline-block;
    }
    input {
      box-sizing: border-box;
      width: 16rem;
      font: inherit;
      font-size: var(--fs-sm);
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
    }
    .pop {
      position: absolute;
      top: calc(100% + 2px);
      left: 0;
      z-index: var(--z-popover, 50);
      width: min(30rem, calc(100vw - 2rem));
      max-height: min(60vh, 32rem);
      display: flex;
      flex-direction: column;
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md, 6px);
      box-shadow: 0 6px 24px rgb(0 0 0 / 18%);
      font-size: var(--fs-sm);
    }
    .head,
    .foot {
      display: flex;
      justify-content: space-between;
      gap: var(--sp-2);
      padding: var(--sp-1) var(--sp-2);
      color: var(--dim);
      font-size: var(--fs-xs);
    }
    .head {
      border-bottom: 1px solid var(--border);
    }
    .foot {
      border-top: 1px solid var(--border);
    }
    .rows {
      overflow: auto;
      flex: 1;
      padding: var(--sp-1) 0;
    }
    .row {
      display: flex;
      align-items: baseline;
      gap: var(--sp-1);
      padding: 1px var(--sp-2) 1px calc(var(--sp-2) + var(--level) * 1.1rem);
      cursor: pointer;
      white-space: nowrap;
    }
    .row:hover {
      background: color-mix(in srgb, var(--accent) 8%, transparent);
    }
    .row.active {
      background: color-mix(in srgb, var(--accent) 18%, transparent);
    }
    .row.current .name {
      font-weight: 600;
    }
    .twisty {
      flex: none;
      width: 1rem;
      text-align: center;
      color: var(--dim);
      border: 0;
      background: none;
      padding: 0;
      font: inherit;
      cursor: pointer;
    }
    .label {
      flex: 1;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .chain {
      color: var(--dim);
    }
    .match mark {
      background: none;
      color: inherit;
      font-weight: 600;
      text-decoration: underline;
    }
    .rank {
      color: var(--dim);
      font-size: var(--fs-xs);
      margin-left: var(--sp-1);
    }
    .papers {
      flex: none;
      color: var(--dim);
      font-variant-numeric: tabular-nums;
    }
    .empty {
      padding: var(--sp-2);
      color: var(--dim);
    }
    button.link {
      border: 0;
      background: none;
      padding: 0;
      font: inherit;
      color: var(--accent);
      cursor: pointer;
    }
  `;

  constructor() {
    super();
    this.value = "";
    this.source = "ncbi";
    this.minMentions = 1;
    this.placeholder = "higher taxon, e.g. Bacillariophyceae";
    this._open = false;
    this._text = "";
    this._active = 0;
    this._loading = false;
    this._error = "";
    this._version = 0;
    this._nodes = new Map();
    this._browse = null;
    this._search = null;
    this._seq = 0;
  }

  willUpdate(changed) {
    if (changed.has("value") && !this._open) this._text = this.value || "";
    if (changed.has("source") || changed.has("minMentions")) {
      this._nodes = new Map();
      this._browse = null;
      this._search = null;
      if (this._open) this._refresh();
    }
  }

  _params(extra) {
    const p = { source: this.source, ...extra };
    if (this.minMentions > 1) p.min_mentions = this.minMentions;
    return p;
  }

  // _merge adds a depth-first node list to the node map and records,
  // in store, the children of every parent it covers.
  _merge(store, items) {
    const touched = new Set();
    for (const n of items) {
      this._nodes.set(n.path, n);
      const parent = parentPath(n.path);
      if (!touched.has(parent)) {
        store.children.set(parent, []);
        touched.add(parent);
      }
      store.children.get(parent).push(n.path);
      if (n.match) store.matches.add(n.path);
    }
  }

  async _load(fn) {
    const seq = ++this._seq;
    this._loading = true;
    this._error = "";
    try {
      await fn(() => seq === this._seq);
    } catch (err) {
      if (seq === this._seq) this._error = err?.message || String(err);
    } finally {
      if (seq === this._seq) this._loading = false;
      this._version++;
    }
  }

  _refresh() {
    const q = this._text.trim();
    this._active = 0;
    if (q && q !== this.value) {
      this._load(async (current) => {
        const page = await api.reference.taxonTree(this._params({ q, limit: 40 }));
        if (!current()) return;
        const store = newStore();
        this._merge(store, page.items || []);
        for (const p of store.children.keys()) store.expanded.add(p);
        this._search = store;
        // Highlight the first match.
        this._active = Math.max(0, this._rows().findIndex((r) => this._search.matches.has(r.tail)));
      });
      return;
    }
    this._search = null;
    if (this._browse) {
      this._version++;
      return;
    }
    this._load(async (current) => {
      const page = await api.reference.taxonTree(this._params({ depth: 2 }));
      if (!current()) return;
      const store = newStore();
      this._merge(store, page.items || []);
      // Open the top level when it is a few roots. A source whose
      // classification lacks upper levels for some groups has many
      // roots; those are listed closed, most papers first.
      const roots = store.children.get(ROOT) || [];
      if (roots.length <= 3) for (const p of roots) store.expanded.add(this._chain(store, p).at(-1));
      this._browse = store;
    });
  }

  get _store() {
    return this._search || this._browse;
  }

  // _chain returns path followed by its descendants while each has a
  // single child, so the chain shows as one row. In search results a
  // match ends a chain.
  _chain(store, path) {
    const chain = [path];
    for (;;) {
      const tail = chain.at(-1);
      const kids = store.children.get(tail);
      const node = this._nodes.get(tail);
      if (!kids || kids.length !== 1 || store.matches.has(tail)) break;
      if (store === this._browse && node?.children !== 1) break;
      chain.push(kids[0]);
    }
    return chain;
  }

  // _rows flattens the visible part of the current view.
  _rows() {
    const store = this._store;
    const out = [];
    if (!store) return out;
    const walk = (parent, level) => {
      for (const p of store.children.get(parent) || []) {
        const chain = this._chain(store, p);
        const tail = chain.at(-1);
        const node = this._nodes.get(tail);
        const open = store.expanded.has(tail) && store.children.has(tail);
        out.push({ chain, tail, node, level, open });
        if (open) walk(tail, level + 1);
      }
    };
    walk(ROOT, 0);
    return out;
  }

  async _toggle(row) {
    const store = this._store;
    if (!row.node.children) return;
    if (store.expanded.has(row.tail) && store.children.has(row.tail)) {
      store.expanded.delete(row.tail);
      this._version++;
      return;
    }
    store.expanded.add(row.tail);
    if (store.children.has(row.tail) && store.children.get(row.tail).length === row.node.children) {
      this._version++;
      return;
    }
    await this._load(async () => {
      const page = await api.reference.taxonTree(this._params({ path: row.tail, depth: 1 }));
      // A search view keeps its matches; a full child list replaces
      // the partial one.
      this._merge(store, page.items || []);
    });
  }

  _pick(name) {
    this._open = false;
    this._text = name;
    this.dispatchEvent(new CustomEvent("pick", { detail: { name }, bubbles: true, composed: true }));
  }

  _close() {
    this._open = false;
    this._text = this.value || "";
    this._search = null;
  }

  _onFocus() {
    if (this._open) return;
    this._open = true;
    this._refresh();
  }

  _onInput(e) {
    this._text = e.target.value;
    this._open = true;
    clearTimeout(this._timer);
    this._timer = setTimeout(() => this._refresh(), 150);
  }

  _onKey(e) {
    const rows = this._open ? this._rows() : [];
    const row = rows[this._active];
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (!this._open) return this._onFocus();
        this._active = Math.min(this._active + 1, rows.length - 1);
        break;
      case "ArrowUp":
        e.preventDefault();
        this._active = Math.max(this._active - 1, 0);
        break;
      case "ArrowRight":
        if (row && row.node.children && !row.open && this._caretAtEnd(e)) {
          e.preventDefault();
          this._toggle(row);
        }
        break;
      case "ArrowLeft":
        if (row && !this._search && this._caretAtEnd(e)) {
          e.preventDefault();
          if (row.open) this._toggle(row);
          else {
            const up = rows.findIndex((r) => r.tail === parentPath(row.chain[0]));
            if (up >= 0) this._active = up;
          }
        }
        break;
      case "Enter": {
        e.preventDefault();
        clearTimeout(this._timer);
        const typed = this._text.trim();
        if (row && this._open && (this._search || !typed || typed === this.value)) {
          this._pick(row.node.name);
        } else {
          this._pick(typed);
        }
        break;
      }
      case "Escape":
        if (this._open) {
          e.preventDefault();
          this._close();
        }
        break;
      case "Tab":
        this._close();
        break;
    }
    this.updateComplete.then(() =>
      this.renderRoot.querySelector(".row.active")?.scrollIntoView({ block: "nearest" }),
    );
  }

  // _caretAtEnd keeps the arrow keys for the text while the caret is
  // inside it.
  _caretAtEnd(e) {
    const t = e.target;
    return t.selectionStart === t.value.length && t.selectionEnd === t.value.length;
  }

  _onFocusOut(e) {
    if (!this.renderRoot.contains(e.relatedTarget)) this._close();
  }

  _renderName(name) {
    const q = this._search ? this._text.trim() : "";
    if (q && name.toLowerCase().startsWith(q.toLowerCase())) {
      return html`<span class="match"><mark>${name.slice(0, q.length)}</mark>${name.slice(q.length)}</span>`;
    }
    return name;
  }

  _renderRow(r, i) {
    const n = r.node;
    const current = this.value && n.name.toLowerCase() === this.value.toLowerCase();
    return html`<div
      class="row ${i === this._active ? "active" : ""} ${current ? "current" : ""}"
      style="--level:${r.level}"
      role="treeitem"
      aria-level=${r.level + 1}
      aria-expanded=${n.children ? String(r.open) : nothing}
      aria-selected=${i === this._active ? "true" : "false"}
      @mousedown=${(e) => e.preventDefault()}
      @click=${() => this._pick(n.name)}
    >
      <button
        class="twisty"
        tabindex="-1"
        aria-label=${r.open ? "Collapse" : "Expand"}
        style=${n.children ? "" : "visibility:hidden"}
        @click=${(e) => {
          e.stopPropagation();
          this._active = i;
          this._toggle(r);
        }}
      >
        ${n.children ? (r.open ? "▾" : "▸") : ""}
      </button>
      <span class="label" title=${r.chain.map((p) => this._nodes.get(p)?.name).join(" › ")}>
        ${r.chain.slice(0, -1).map(
          (p) => html`<span class="chain">${this._renderName(this._nodes.get(p)?.name || "")} › </span>`,
        )}<span class="name">${this._renderName(n.name)}</span>${n.rank
          ? html`<span class="rank">${n.rank}</span>`
          : nothing}
      </span>
      <span class="papers" title="${n.papers} ${n.papers === 1 ? "paper" : "papers"}">${n.papers}</span>
    </div>`;
  }

  _renderPopover() {
    const rows = this._rows();
    const label = this.source === "col" ? "Catalogue of Life" : "NCBI Taxonomy";
    let body;
    if (this._error) body = html`<div class="empty">Could not load the classification: ${this._error}</div>`;
    else if (!this._store) body = html`<div class="empty">Loading…</div>`;
    else if (rows.length === 0)
      body = html`<div class="empty">
        ${this._search
          ? html`No taxon in ${label} starts with “${this._text.trim()}”. Enter searches for it anyway.`
          : "No indexed papers yet."}
      </div>`;
    else body = rows.map((r, i) => this._renderRow(r, i));
    return html`<div class="pop" @mousedown=${(e) => e.preventDefault()}>
      <div class="head">
        <span>${label} classification of names in indexed papers</span>
        <span>Papers</span>
      </div>
      <div class="rows" role="tree" aria-label="Higher taxa">${body}</div>
      <div class="foot">
        <span>↑↓ move · → expand · Enter apply · Esc close</span>
        ${this.value
          ? html`<button class="link" @click=${() => this._pick("")}>Clear filter</button>`
          : nothing}
      </div>
    </div>`;
  }

  render() {
    void this._version;
    return html`
      <input
        type="search"
        part="input"
        role="combobox"
        aria-expanded=${String(this._open)}
        aria-label="Higher taxon"
        placeholder=${this.placeholder}
        .value=${this._text}
        @focus=${this._onFocus}
        @click=${this._onFocus}
        @input=${this._onInput}
        @keydown=${this._onKey}
        @focusout=${this._onFocusOut}
        @search=${(e) => e.target.value === "" && this.value && this._pick("")}
      />
      ${this._open ? this._renderPopover() : nothing}
    `;
  }
}

customElements.define("sfga-taxon-picker", SfgaTaxonPicker);
