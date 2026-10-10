/**
 * <sfga-data-table>
 *
 * A sortable, column-configurable table that loads pages as it is
 * scrolled. Used for the taxon table; follows the reference table's
 * behaviour: click a header to sort, Shift-click to add a further
 * sort, a gear in the last header opens a menu to choose, order and
 * group columns and pick the row height, and the table scrolls inside
 * its own frame so the page controls stay in view. Column choice,
 * order, sort and row height are per-user settings under settingsKey.
 *
 * Properties:
 *   columns     [{id, label, group?, sort?, numeric?, shown?, title?,
 *                 cell(row) → template}]
 *   groups      [{id, label}] — sections of the column menu, in order
 *   settingsKey "taxa.table" → "taxa.table.columns" etc.
 *   fetchPage   async ({offset, limit, sort}) → {items, total}
 *   rowKey      (row) → string
 *   icon        (name, size) → template (the app's icon renderer)
 *   defaultSort [{column, desc}]
 *
 * Methods: reload() — refetch from the first page (after filters
 * change). Events: row-activate {row} on a row's double click or Enter;
 * total-changed {total}.
 */

import { LitElement, html, css, nothing, repeat, live } from "/vendor/lit-3.x.x.min.js";
import { getSetting, setSetting } from "/lib/settings.js";

const PAGE = 100;

class SfgaDataTable extends LitElement {
  static properties = {
    columns: { attribute: false },
    groups: { attribute: false },
    settingsKey: { attribute: false },
    fetchPage: { attribute: false },
    rowKey: { attribute: false },
    icon: { attribute: false },
    defaultSort: { attribute: false },
    _rows: { state: true },
    _total: { state: true },
    _loading: { state: true },
    _error: { state: true },
    _shown: { state: true },
    _sort: { state: true },
    _rowHeight: { state: true },
    _menu: { state: true },
    _drag: { state: true },
  };

  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      min-height: 0;
      font-size: var(--fs-sm);
    }
    .frame {
      flex: 1 1 auto;
      min-height: 12rem;
      overflow: auto;
    }
    table {
      width: 100%;
      border-collapse: collapse;
    }
    th {
      text-align: left;
      font-weight: 600;
      color: var(--dim);
      font-size: var(--fs-xs);
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid var(--border);
      position: sticky;
      top: 0;
      background: var(--bg);
      z-index: 1;
      white-space: nowrap;
    }
    td {
      padding: var(--sp-1) var(--sp-2);
      border-bottom: 1px solid color-mix(in oklab, var(--border) 60%, transparent);
      vertical-align: top;
    }
    tbody tr {
      cursor: default;
    }
    tbody tr:hover,
    tbody tr:focus-visible {
      background: color-mix(in oklab, var(--fg) 6%, transparent);
      outline: none;
    }
    .numeric {
      text-align: right;
      white-space: nowrap;
      font-variant-numeric: tabular-nums;
    }
    .th-wrap {
      display: flex;
      align-items: center;
      gap: var(--sp-1);
    }
    th.numeric .th-wrap {
      justify-content: flex-end;
    }
    .th-sort {
      font: inherit;
      color: inherit;
      background: none;
      border: 0;
      padding: 0;
      cursor: pointer;
      display: inline-flex;
      gap: var(--sp-1);
      align-items: baseline;
    }
    th.numeric .th-sort {
      flex-direction: row-reverse;
    }
    .th-sort:hover,
    .th-sort:focus-visible {
      color: var(--accent);
    }
    .sort-ind {
      font-size: 0.85em;
      color: var(--accent);
    }
    .icon-btn {
      display: inline-flex;
      align-items: center;
      background: none;
      border: 0;
      color: var(--dim);
      cursor: pointer;
      padding: 2px;
      border-radius: var(--radius-sm);
    }
    .icon-btn:hover:not(:disabled) {
      color: var(--fg);
    }
    .icon-btn:disabled {
      opacity: 0.4;
      cursor: default;
    }
    .menu-btn {
      margin-left: auto;
    }
    /* Row height: long cells clip to two lines or one. */
    .cell {
      display: block;
    }
    table.rows-two .cell {
      display: -webkit-box;
      -webkit-line-clamp: 2;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
    table.rows-one .cell {
      display: -webkit-box;
      -webkit-line-clamp: 1;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
    .status,
    .error {
      padding: var(--sp-2);
      color: var(--dim);
    }
    .error {
      color: var(--error);
    }
    .menu {
      position: fixed;
      z-index: var(--z-popover);
      max-height: calc(100vh - 6rem);
      overflow: auto;
      min-width: 15rem;
      padding: var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      box-shadow: 0 4px 16px color-mix(in oklab, var(--fg) 20%, transparent);
      font-size: var(--fs-sm);
    }
    .menu fieldset {
      border: 0;
      margin: 0 0 var(--sp-2);
      padding: 0;
      display: flex;
      flex-wrap: wrap;
      gap: var(--sp-1) var(--sp-2);
    }
    .menu legend,
    .menu h4 {
      color: var(--dim);
      font-size: var(--fs-xs);
      font-weight: 600;
      margin: var(--sp-2) 0 var(--sp-1);
      padding: 0;
    }
    .menu fieldset label {
      white-space: nowrap;
    }
    .menu ul {
      list-style: none;
      margin: 0;
      padding: 0;
    }
    .menu li {
      display: flex;
      align-items: center;
      gap: var(--sp-1);
      padding: 2px 0;
    }
    .menu li.drop-before {
      box-shadow: 0 -2px 0 var(--accent);
    }
    .menu li label {
      flex: 1;
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
      cursor: pointer;
    }
    .grip {
      display: inline-flex;
      width: 14px;
      color: var(--dim);
      cursor: grab;
      touch-action: none;
    }
    .hint {
      color: var(--dim);
      font-size: var(--fs-xs);
    }
    .reset {
      margin-top: var(--sp-2);
      font: inherit;
      color: var(--fg);
      background: transparent;
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: var(--sp-1) var(--sp-2);
      cursor: pointer;
    }
    /* Shared cell styles for column renderers. */
    .dim {
      color: var(--dim);
    }
    .issue-counts {
      display: inline-flex;
      gap: var(--sp-1);
      white-space: nowrap;
    }
    .sev {
      font-size: var(--fs-xs);
      padding: 0 var(--sp-1);
      border-radius: var(--radius-sm);
    }
    .sev-error {
      color: var(--sev-error);
      background: var(--sev-error-bg);
    }
    .sev-warn {
      color: var(--sev-warn);
      background: var(--sev-warn-bg);
    }
    .sev-info {
      color: var(--sev-info);
      background: var(--sev-info-bg);
    }
    .sev-debug {
      color: var(--sev-debug);
      background: var(--sev-debug-bg);
    }
    .sr-only {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
    }
  `;

  constructor() {
    super();
    this.columns = [];
    this.groups = [];
    this.settingsKey = "table";
    this.rowKey = (r) => r.id;
    this.icon = () => nothing;
    this.defaultSort = [];
    this._rows = [];
    this._total = null;
    this._loading = false;
    this._error = "";
    this._menu = null;
    this._drag = null;
    this._generation = 0;
  }

  _setting(name) {
    return `${this.settingsKey}.${name}`;
  }

  willUpdate(changed) {
    if (changed.has("columns") || changed.has("settingsKey")) {
      const ids = new Set(this.columns.map((c) => c.id));
      const saved = getSetting(this._setting("columns"));
      const kept = Array.isArray(saved) ? [...new Set(saved.filter((id) => ids.has(id)))] : [];
      this._shown = kept.length ? kept : this._defaultColumns();
      const sort = getSetting(this._setting("sort"));
      const sortable = new Set(this.columns.filter((c) => c.sort).map((c) => c.sort));
      this._sort = Array.isArray(sort)
        ? sort.filter((k) => k && sortable.has(k.column))
        : (this.defaultSort || []).slice();
      this._rowHeight = getSetting(this._setting("rowHeight")) || "two";
    }
  }

  firstUpdated() {
    this.reload();
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this._observer?.disconnect();
    this._closeMenu();
  }

  _defaultColumns() {
    return this.columns.filter((c) => c.shown).map((c) => c.id);
  }

  _sortParam() {
    return this._sort.map((k) => `${k.column}:${k.desc ? "desc" : "asc"}`).join(",");
  }

  // exportParams returns the shown columns and sort, for an export of
  // the same view.
  exportParams() {
    return { columns: (this._shown || []).join(","), sort: this._sortParam() };
  }

  // reload fetches the first page again, discarding loaded rows.
  reload() {
    this._generation++;
    this._rows = [];
    this._total = null;
    this._error = "";
    this.renderRoot.querySelector(".frame")?.scrollTo({ top: 0 });
    return this._loadMore();
  }

  async _loadMore() {
    if (this._loading || !this.fetchPage) return;
    if (this._total !== null && this._rows.length >= this._total) return;
    const generation = this._generation;
    this._loading = true;
    try {
      const page = await this.fetchPage({ offset: this._rows.length, limit: PAGE, sort: this._sortParam() });
      if (generation !== this._generation) return;
      this._rows = [...this._rows, ...(page.items || [])];
      this._total = page.total ?? this._rows.length;
      this.dispatchEvent(new CustomEvent("total-changed", { detail: { total: this._total } }));
    } catch (err) {
      if (generation === this._generation) this._error = err?.detail || err?.message || String(err);
    } finally {
      if (generation === this._generation) this._loading = false;
    }
    await this.updateComplete;
    this._observe();
  }

  // _observe loads the next page when the sentinel row scrolls into
  // the frame.
  _observe() {
    const frame = this.renderRoot.querySelector(".frame");
    const sentinel = this.renderRoot.querySelector(".sentinel");
    if (!frame || !sentinel) return;
    if (!this._observer || this._observerRoot !== frame) {
      this._observer?.disconnect();
      this._observer = new IntersectionObserver(
        (entries) => {
          if (entries.some((e) => e.isIntersecting)) this._loadMore();
        },
        { root: frame, rootMargin: "300px" },
      );
      this._observerRoot = frame;
    }
    this._observer.disconnect();
    this._observer.observe(sentinel);
  }

  // _onSort sorts by a header click: alone (reversing when it already
  // is the only sort), or with Shift as a further sort.
  _onSort(e, column) {
    const keys = this._sort;
    const i = keys.findIndex((k) => k.column === column);
    let next;
    if (e.shiftKey) {
      next = keys.slice();
      if (i >= 0) next[i] = { column, desc: !keys[i].desc };
      else next.push({ column, desc: false });
    } else if (i === 0 && keys.length === 1) {
      next = [{ column, desc: !keys[0].desc }];
    } else {
      next = [{ column, desc: false }];
    }
    this._sort = next;
    setSetting(this._setting("sort"), next);
    this.reload();
  }

  _setShown(ids) {
    this._shown = ids;
    setSetting(this._setting("columns"), ids);
  }

  _toggle(id, show) {
    if (show && !this._shown.includes(id)) this._setShown([...this._shown, id]);
    if (!show && this._shown.length > 1) this._setShown(this._shown.filter((c) => c !== id));
  }

  _move(id, delta) {
    const cols = this._shown.slice();
    const i = cols.indexOf(id);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= cols.length) return;
    [cols[i], cols[j]] = [cols[j], cols[i]];
    this._setShown(cols);
  }

  _dragStart(e, id) {
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    this._drag = { id, before: id };
  }

  _dragMove(e) {
    if (!this._drag) return;
    let before = "";
    for (const li of this.renderRoot.querySelectorAll(".menu li[data-col]")) {
      const r = li.getBoundingClientRect();
      if (e.clientY < r.top + r.height / 2) {
        before = li.dataset.col;
        break;
      }
    }
    if (before !== this._drag.before) this._drag = { ...this._drag, before };
  }

  _dragEnd() {
    const drag = this._drag;
    this._drag = null;
    if (!drag || drag.before === drag.id) return;
    const cols = this._shown.filter((c) => c !== drag.id);
    const at = drag.before ? cols.indexOf(drag.before) : cols.length;
    cols.splice(at < 0 ? cols.length : at, 0, drag.id);
    this._setShown(cols);
  }

  _openMenu(button) {
    const r = button.getBoundingClientRect();
    this._menu = { top: r.bottom + 4, right: Math.max(8, window.innerWidth - r.right) };
    this._onOutside = (e) => {
      const path = e.composedPath();
      const menu = this.renderRoot.querySelector(".menu");
      if (!path.includes(menu) && !path.includes(button)) this._closeMenu();
    };
    window.addEventListener("pointerdown", this._onOutside, true);
    this.updateComplete.then(() => this.renderRoot.querySelector(".menu input")?.focus());
  }

  _closeMenu() {
    this._menu = null;
    if (this._onOutside) {
      window.removeEventListener("pointerdown", this._onOutside, true);
      this._onOutside = null;
    }
  }

  _activate(row) {
    this.dispatchEvent(new CustomEvent("row-activate", { detail: { row } }));
  }

  render() {
    const byID = Object.fromEntries(this.columns.map((c) => [c.id, c]));
    const cols = (this._shown || []).map((id) => byID[id]).filter(Boolean);
    const more = this._total === null || this._rows.length < this._total;
    return html`
      <div class="frame">
        <table class=${"rows-" + this._rowHeight}>
          <thead>
            <tr>
              ${cols.map((c, i) => this._renderHeader(c, i === cols.length - 1))}
            </tr>
          </thead>
          <tbody>
            ${repeat(
              this._rows,
              (r) => this.rowKey(r),
              (r) => html`<tr
                tabindex="0"
                @dblclick=${() => this._activate(r)}
                @keydown=${(e) => e.key === "Enter" && this._activate(r)}
              >
                ${cols.map(
                  (c) => html`<td class=${(c.numeric ? "numeric " : "") + "col-" + c.id}>
                    <span class="cell">${c.cell(r)}</span>
                  </td>`,
                )}
              </tr>`,
            )}
          </tbody>
        </table>
        ${this._error ? html`<div class="error" role="alert">${this._error}</div>` : nothing}
        ${this._loading ? html`<div class="status" role="status">Loading…</div>` : nothing}
        ${!this._loading && this._total === 0 ? html`<div class="status">No rows match.</div>` : nothing}
        ${more && !this._error ? html`<div class="sentinel" aria-hidden="true">&nbsp;</div>` : nothing}
      </div>
      ${this._renderMenu()}
    `;
  }

  _renderHeader(col, last) {
    const i = this._sort.findIndex((k) => k.column === col.sort);
    const key = i >= 0 ? this._sort[i] : null;
    return html`<th
      scope="col"
      class=${col.numeric ? "numeric" : ""}
      aria-sort=${i === 0 ? (key.desc ? "descending" : "ascending") : nothing}
      title=${col.title || nothing}
    >
      <div class="th-wrap">
        ${col.sort
          ? html`<button
              type="button"
              class="th-sort"
              title=${"Sort by " + col.label.toLowerCase() + "; Shift-click to add it as a further sort"}
              @mousedown=${(e) => e.shiftKey && e.preventDefault()}
              @click=${(e) => this._onSort(e, col.sort)}
            >
              ${col.label}${key
                ? html`<span class="sort-ind" aria-hidden="true"
                      >${key.desc ? "▼" : "▲"}${this._sort.length > 1 ? i + 1 : ""}</span
                    ><span class="sr-only">${key.desc ? ", sorted descending" : ", sorted ascending"}</span>`
                : nothing}
            </button>`
          : col.label}
        ${last
          ? html`<button
              type="button"
              class="icon-btn menu-btn"
              title="Choose and order columns"
              aria-label="Choose and order columns"
              aria-haspopup="dialog"
              aria-expanded=${this._menu ? "true" : "false"}
              @click=${(e) => (this._menu ? this._closeMenu() : this._openMenu(e.currentTarget))}
            >${this.icon("settings", 14)}</button>`
          : nothing}
      </div>
    </th>`;
  }

  // _renderMenu lists the shown columns in order (drag or arrows to
  // reorder), then every other column under its group.
  _renderMenu() {
    if (!this._menu) return nothing;
    const byID = Object.fromEntries(this.columns.map((c) => [c.id, c]));
    const shown = this._shown.map((id) => byID[id]).filter(Boolean);
    const hidden = this.columns.filter((c) => !this._shown.includes(c.id));
    const groups = this.groups?.length ? this.groups : [{ id: undefined, label: "More columns" }];
    return html`<div
      class="menu"
      role="dialog"
      aria-label="Columns"
      style=${`top:${this._menu.top}px;right:${this._menu.right}px`}
      @keydown=${(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          this._closeMenu();
        }
      }}
    >
      <fieldset>
        <legend>Rows</legend>
        ${[
          ["full", "Full text"],
          ["two", "Two lines"],
          ["one", "One line"],
        ].map(
          ([value, label]) => html`<label>
            <input
              type="radio"
              name="row-height"
              .checked=${this._rowHeight === value}
              @change=${() => {
                this._rowHeight = value;
                setSetting(this._setting("rowHeight"), value);
              }}
            />
            ${label}
          </label>`,
        )}
      </fieldset>
      <h4>Shown <span class="hint">— drag or use the arrows to reorder</span></h4>
      <ul>
        ${repeat(
          shown,
          (c) => c.id,
          (c, i) => html`<li
            data-col=${c.id}
            class=${this._drag && this._drag.before === c.id && this._drag.id !== c.id ? "drop-before" : ""}
          >
            <span
              class="grip"
              title="Drag to reorder"
              aria-hidden="true"
              @pointerdown=${(e) => this._dragStart(e, c.id)}
              @pointermove=${(e) => this._dragMove(e)}
              @pointerup=${() => this._dragEnd()}
              @pointercancel=${() => (this._drag = null)}
            >${this.icon("grip-vertical", 14)}</span>
            <label>
              <input
                type="checkbox"
                .checked=${live(true)}
                ?disabled=${this._shown.length === 1}
                @change=${() => this._toggle(c.id, false)}
              />
              ${c.label}
            </label>
            <button
              type="button"
              class="icon-btn"
              title="Move left"
              aria-label=${"Move " + c.label + " left"}
              ?disabled=${i === 0}
              @click=${() => this._move(c.id, -1)}
            >${this.icon("chevron-up", 14)}</button>
            <button
              type="button"
              class="icon-btn"
              title="Move right"
              aria-label=${"Move " + c.label + " right"}
              ?disabled=${i === shown.length - 1}
              @click=${() => this._move(c.id, 1)}
            >${this.icon("chevron-down", 14)}</button>
          </li>`,
        )}
      </ul>
      ${groups.map((g) => {
        const cols = hidden.filter((c) => (g.id === undefined ? true : c.group === g.id));
        if (!cols.length) return nothing;
        return html`<h4>${g.label}</h4>
          <ul>
            ${repeat(
              cols,
              (c) => c.id,
              (c) => html`<li>
                <span class="grip" aria-hidden="true"></span>
                <label title=${c.title || nothing}>
                  <input type="checkbox" .checked=${live(false)} @change=${() => this._toggle(c.id, true)} />
                  ${c.label}
                </label>
              </li>`,
            )}
          </ul>`;
      })}
      <button type="button" class="reset" @click=${() => this._setShown(this._defaultColumns())}>
        Reset to defaults
      </button>
    </div>`;
  }
}

customElements.define("sfga-data-table", SfgaDataTable);
