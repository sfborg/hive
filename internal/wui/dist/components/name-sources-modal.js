/**
 * <sfga-name-sources-modal>
 *
 * Picks the GNverifier data sources the archive's name-mention index
 * verifies tagged names against, in preference order (the first wins
 * ties in the reference list's classification columns). Saving starts
 * re-indexing every reference against the new sources; "Re-index now"
 * rebuilds the index without changing them (after a source publishes a
 * new release). Progress shows while the job runs.
 *
 * Project-wide: the choice is stored in the archive, for every user.
 * Opens from the settings menu. Dispatches "close" when dismissed and
 * "name-sources-changed" with detail {sources} after a save.
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";
import { api, Problem } from "/lib/api.js";

class SfgaNameSourcesModal extends LitElement {
  static properties = {
    readOnly: { type: Boolean, attribute: false },
    _picked: { state: true },
    _saved: { state: true },
    _available: { state: true },
    _filter: { state: true },
    _status: { state: true },
    _error: { state: true },
    _busy: { state: true },
  };

  static styles = css`
    :host {
      display: contents;
    }
    .backdrop {
      position: fixed;
      inset: 0;
      background: color-mix(in oklab, var(--fg) 18%, transparent);
      display: grid;
      place-items: center;
      z-index: var(--z-modal-backdrop);
    }
    .modal {
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-4) var(--sp-5);
      width: min(40rem, calc(100vw - 2rem));
      max-height: var(--modal-max-h);
      overflow: auto;
      display: grid;
      gap: var(--sp-3);
      font-family: var(--font-body);
      font-size: var(--fs-sm);
      z-index: var(--z-modal-content);
    }
    header {
      display: flex;
      align-items: baseline;
      gap: var(--sp-3);
    }
    h2 {
      margin: 0;
      font-size: var(--fs-lg);
      flex: 1;
    }
    h3 {
      margin: 0;
      font-size: var(--fs-sm);
    }
    .close-btn {
      background: none;
      border: none;
      color: var(--dim);
      cursor: pointer;
      font-size: var(--fs-lg);
      padding: 0 var(--sp-2);
    }
    .hint,
    .meta {
      color: var(--dim);
      margin: 0;
    }
    ol,
    ul {
      list-style: none;
      margin: 0;
      padding: 0;
      display: grid;
      gap: 2px;
    }
    li {
      display: flex;
      align-items: center;
      gap: var(--sp-2);
      padding: 2px var(--sp-2);
      border-radius: var(--radius-sm);
    }
    ol li {
      border: 1px solid var(--border);
    }
    li .name {
      flex: 1;
    }
    .available {
      max-height: 16rem;
      overflow: auto;
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-1);
    }
    .available li:hover {
      background: color-mix(in oklab, var(--fg) 6%, transparent);
    }
    input[type="search"] {
      box-sizing: border-box;
      width: 100%;
      font: inherit;
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
    }
    button {
      font: inherit;
      cursor: pointer;
      padding: var(--sp-1) var(--sp-3);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--bg);
      color: var(--fg);
      white-space: nowrap;
    }
    button.small {
      padding: 0 var(--sp-2);
    }
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    button:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .actions {
      display: flex;
      justify-content: flex-end;
      gap: var(--sp-2);
      align-items: center;
    }
    .actions .status {
      flex: 1;
      color: var(--dim);
    }
    progress {
      width: 100%;
    }
    .error {
      color: var(--error);
    }
  `;

  constructor() {
    super();
    this.readOnly = false;
    this._picked = [];
    this._saved = [];
    this._available = null;
    this._filter = "";
    this._status = null;
    this._error = "";
    this._busy = false;
  }

  connectedCallback() {
    super.connectedCallback();
    this._load();
    this._onKey = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        this._close();
      }
    };
    document.addEventListener("keydown", this._onKey, true);
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    document.removeEventListener("keydown", this._onKey, true);
    clearTimeout(this._poll);
  }

  async _load() {
    try {
      const res = await api.nameSources.get();
      this._saved = res.sources || [];
      this._picked = this._saved.map((s) => ({ id: s.id, title: s.label, short: s.short }));
      this._setStatus(res.reindex);
    } catch (err) {
      this._error = this._message(err);
    }
    try {
      const res = await api.nameSources.available();
      this._available = res.items || [];
    } catch (err) {
      this._available = [];
      this._error = `Could not list GNverifier's sources: ${this._message(err)}`;
    }
  }

  _message(err) {
    return err instanceof Problem ? err.detail || err.title : String(err);
  }

  // _setStatus shows the re-index job and polls while it runs.
  _setStatus(status) {
    this._status = status || null;
    clearTimeout(this._poll);
    if (status?.running) {
      this._poll = setTimeout(async () => {
        try {
          this._setStatus(await api.nameSources.reindexStatus());
        } catch (_) {
          /* keep the last status */
        }
      }, 1000);
    }
  }

  _changed() {
    const a = this._picked.map((s) => s.id).join(",");
    const b = this._saved.map((s) => s.id).join(",");
    return a !== b;
  }

  _add(src) {
    if (this._picked.some((s) => s.id === src.id)) return;
    this._picked = [...this._picked, { id: src.id, title: src.title, short: src.short }];
  }

  _remove(id) {
    this._picked = this._picked.filter((s) => s.id !== id);
  }

  _move(i, d) {
    const next = [...this._picked];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    this._picked = next;
  }

  async _save() {
    this._busy = true;
    this._error = "";
    try {
      const res = await api.nameSources.put(this._picked.map((s) => s.id));
      this._saved = res.sources || [];
      this._setStatus(res.reindex);
      this.dispatchEvent(
        new CustomEvent("name-sources-changed", { detail: { sources: this._saved }, bubbles: true, composed: true }),
      );
    } catch (err) {
      this._error = this._message(err);
    } finally {
      this._busy = false;
    }
  }

  async _reindex() {
    this._error = "";
    try {
      this._setStatus(await api.nameSources.reindex());
    } catch (err) {
      this._error = this._message(err);
    }
  }

  _close() {
    this.dispatchEvent(new CustomEvent("close", { bubbles: true, composed: true }));
  }

  _renderStatus() {
    const st = this._status;
    if (!st || (!st.running && !st.finished_at)) return nothing;
    if (st.running) {
      return html`<div role="status">
        <p class="meta">${st.stage}${st.total ? ` — ${st.done.toLocaleString()} of ${st.total.toLocaleString()}` : ""}</p>
        <progress max=${st.total || 1} value=${st.total ? st.done : 0}></progress>
      </div>`;
    }
    if (st.error) return html`<p class="error" role="alert">Re-index failed: ${st.error}</p>`;
    return html`<p class="meta" role="status">
      Re-indexed ${st.papers?.toLocaleString() || 0} papers (${st.names?.toLocaleString() || 0} name
      strings) at ${new Date(st.finished_at).toLocaleTimeString()}.
    </p>`;
  }

  render() {
    const picked = new Set(this._picked.map((s) => s.id));
    const needle = this._filter.trim().toLowerCase();
    const available = (this._available || []).filter(
      (s) => !picked.has(s.id) && (!needle || `${s.title} ${s.short || ""}`.toLowerCase().includes(needle)),
    );
    const running = !!this._status?.running;
    return html`
      <div class="backdrop" @click=${(e) => e.target === e.currentTarget && this._close()}>
        <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ns-title">
          <header>
            <h2 id="ns-title">Name sources</h2>
            <button class="close-btn" @click=${this._close} aria-label="close" title="close">✕</button>
          </header>
          <p class="hint">
            Hive verifies the scientific names found in papers against these GNverifier data sources
            to fill in their higher classification for discovery. The first source is preferred when
            sources tie. This applies to everyone using this archive. Saving re-indexes every
            reference, which calls GNverifier for each name and can take a few minutes.
          </p>
          ${this._error ? html`<p class="error" role="alert">${this._error}</p>` : nothing}
          <h3>In use</h3>
          <ol>
            ${this._picked.map(
              (s, i) => html`<li>
                <span class="name">${s.title}${s.short && s.short !== s.title ? html` <span class="meta">(${s.short})</span>` : nothing}</span>
                <button class="small" title="Move up" aria-label=${`Move ${s.title} up`} ?disabled=${i === 0 || this.readOnly}
                  @click=${() => this._move(i, -1)}>↑</button>
                <button class="small" title="Move down" aria-label=${`Move ${s.title} down`}
                  ?disabled=${i === this._picked.length - 1 || this.readOnly} @click=${() => this._move(i, 1)}>↓</button>
                <button class="small" title="Remove" aria-label=${`Remove ${s.title}`}
                  ?disabled=${this._picked.length === 1 || this.readOnly} @click=${() => this._remove(s.id)}>×</button>
              </li>`,
            )}
          </ol>
          <h3>Add a source</h3>
          <input type="search" placeholder="Filter GNverifier's sources…" aria-label="Filter sources"
            .value=${this._filter} @input=${(e) => (this._filter = e.target.value)} />
          <ul class="available" aria-label="Available sources">
            ${this._available === null
              ? html`<li class="meta">Loading…</li>`
              : available.map(
                  (s) => html`<li>
                    <span class="name" title=${s.description || nothing}>${s.title}
                      <span class="meta">${[s.curation, s.records ? `${s.records.toLocaleString()} names` : ""]
                        .filter(Boolean)
                        .join(" · ")}</span></span>
                    <button class="small" ?disabled=${this.readOnly} @click=${() => this._add(s)}>Add</button>
                  </li>`,
                )}
          </ul>
          ${this._renderStatus()}
          <div class="actions">
            <button type="button" ?disabled=${running || this.readOnly || this._busy} @click=${() => this._reindex()}
              title="Rebuild the index with the sources in use, e.g. after a source publishes a new release">
              Re-index now
            </button>
            <button type="button" @click=${this._close}>Close</button>
            <button type="button" class="primary" ?disabled=${!this._changed() || running || this.readOnly || this._busy}
              @click=${() => this._save()}>Save and re-index</button>
          </div>
        </div>
      </div>
    `;
  }
}

customElements.define("sfga-name-sources-modal", SfgaNameSourcesModal);
