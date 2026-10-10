/**
 * <sfga-export-menu>
 *
 * An "Export" button with a menu of download formats. The caller
 * supplies href(format) → URL of the export endpoint with the screen's
 * filters, sort and columns; choosing a format downloads it.
 *
 * Properties: href — (format) => string; formats — [{id, label, hint}]
 * (defaults to CSV and SQLite).
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";

const DEFAULT_FORMATS = [
  { id: "csv", label: "CSV", hint: "For spreadsheets" },
  { id: "sqlite", label: "SQLite", hint: "One flat table, as shown" },
];

class SfgaExportMenu extends LitElement {
  static properties = {
    href: { attribute: false },
    formats: { attribute: false },
    _open: { state: true },
    _right: { state: true },
  };

  static styles = css`
    :host {
      position: relative;
      display: inline-block;
    }
    button {
      font: inherit;
      font-size: var(--fs-sm);
      cursor: pointer;
      padding: var(--sp-1) var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--bg);
      color: var(--fg);
      white-space: nowrap;
    }
    .menu {
      position: absolute;
      left: 0;
      top: calc(100% + 4px);
      z-index: var(--z-popover);
      display: grid;
      min-width: 14rem;
      max-width: 22rem;
      padding: var(--sp-1) 0;
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      box-shadow: 0 4px 16px color-mix(in oklab, var(--fg) 20%, transparent);
    }
    .menu.right {
      left: auto;
      right: 0;
    }
    .menu button {
      border: 0;
      border-radius: 0;
      text-align: left;
      white-space: normal;
      display: grid;
      padding: var(--sp-1) var(--sp-3);
    }
    .menu button:hover,
    .menu button:focus-visible {
      background: color-mix(in oklab, var(--fg) 8%, transparent);
      outline: none;
    }
    .hint {
      color: var(--dim);
      font-size: var(--fs-xs);
    }
  `;

  constructor() {
    super();
    this.formats = DEFAULT_FORMATS;
    this._open = false;
  }

  _toggle() {
    this._open = !this._open;
    if (this._open) {
      // Open toward the side with more room.
      const box = this.getBoundingClientRect();
      this._right = box.left + box.width / 2 > window.innerWidth / 2;
      this._outside = (e) => {
        if (!e.composedPath().includes(this)) this._close();
      };
      window.addEventListener("pointerdown", this._outside, true);
      this.updateComplete.then(() => this.renderRoot.querySelector(".menu button")?.focus());
    } else {
      this._close();
    }
  }

  _close() {
    this._open = false;
    if (this._outside) window.removeEventListener("pointerdown", this._outside, true);
    this._outside = null;
  }

  _download(format) {
    this._close();
    const a = document.createElement("a");
    a.href = this.href(format);
    a.download = "";
    document.body.append(a);
    a.click();
    a.remove();
  }

  render() {
    return html`
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded=${this._open ? "true" : "false"}
        title="Download every row matching the current filters, with the columns shown"
        @click=${() => this._toggle()}
      >
        Export ▾
      </button>
      ${this._open
        ? html`<div
            class=${this._right ? "menu right" : "menu"}
            role="menu"
            @keydown=${(e) => {
              if (e.key === "Escape") {
                e.preventDefault();
                this._close();
                this.renderRoot.querySelector("button")?.focus();
              }
            }}
          >
            ${this.formats.map(
              (f) => html`<button role="menuitem" @click=${() => this._download(f.id)}>
                <span>${f.label}</span>${f.hint ? html`<span class="hint">${f.hint}</span>` : nothing}
              </button>`,
            )}
          </div>`
        : nothing}
    `;
  }
}

customElements.define("sfga-export-menu", SfgaExportMenu);
