/**
 * <sfga-account-modal>
 *
 * The curator's identity on this machine: display name, ORCID iD
 * (stamped on every change as col__modified_by) and the email sent to
 * OpenAlex's polite request pool. Saved in hive's config file, so it
 * applies to every archive opened on this machine. A value set by a
 * command-line flag or environment variable overrides the file and is
 * shown read-only.
 *
 * Opens from the settings menu in SfgaApp. Dispatches "close" when the
 * curator saves or dismisses via ✕, backdrop click, or Escape, and
 * "account-changed" with detail {account} after a save.
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";
import { api, Problem } from "/lib/api.js";

const LOCK_NOTE = {
  flag: "Set by a command-line flag when hive was started.",
  env: "Set by an environment variable when hive was started.",
};

class SfgaAccountModal extends LitElement {
  static properties = {
    _account: { state: true },
    _draft: { state: true },
    _error: { state: true },
    _busy: { state: true },
    _lookup: { state: true },
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
      min-width: 26rem;
      max-width: var(--modal-md);
      max-height: var(--modal-max-h);
      overflow: auto;
      display: grid;
      gap: var(--sp-3);
      font-family: var(--font-body);
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
    .close-btn {
      background: none;
      border: none;
      color: var(--dim);
      cursor: pointer;
      font-size: var(--fs-lg);
      padding: 0 var(--sp-2);
      line-height: 1;
    }
    .close-btn:hover {
      color: var(--fg);
    }
    p.hint,
    .note {
      color: var(--dim);
      font-size: var(--fs-sm);
      margin: 0;
    }
    form {
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: var(--sp-2) var(--sp-3);
      align-items: center;
    }
    label {
      font-size: var(--fs-sm);
      color: var(--dim);
    }
    .field {
      display: grid;
      gap: var(--sp-1);
    }
    .with-button {
      display: flex;
      gap: var(--sp-2);
    }
    input {
      box-sizing: border-box;
      width: 100%;
      font: inherit;
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
    }
    input:read-only {
      color: var(--dim);
      background: color-mix(in oklab, var(--fg) 4%, var(--bg));
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
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border-color: var(--accent);
    }
    button:disabled {
      opacity: 0.6;
      cursor: default;
    }
    .actions {
      display: flex;
      justify-content: flex-end;
      gap: var(--sp-2);
    }
    .error {
      color: var(--error);
      font-size: var(--fs-sm);
    }
  `;

  constructor() {
    super();
    this._account = null;
    this._draft = {};
    this._error = "";
    this._busy = false;
    this._lookup = "";
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
    if (this._onKey) {
      document.removeEventListener("keydown", this._onKey, true);
      this._onKey = null;
    }
  }

  async _load() {
    try {
      this._account = await api.account.get();
      await this.updateComplete;
      this.renderRoot.querySelector("input:not([readonly])")?.focus();
    } catch (err) {
      this._error = this._message(err);
    }
  }

  _message(err) {
    return err instanceof Problem ? err.detail || err.title : String(err);
  }

  _value(field) {
    if (Object.hasOwn(this._draft, field)) return this._draft[field];
    return this._account?.[field] ?? "";
  }

  _set(field, value) {
    this._draft = { ...this._draft, [field]: value };
  }

  // _lookupName fills the name from the ORCID iD's public record.
  async _lookupName() {
    const id = this._value("orcid").trim();
    if (!id) return;
    this._lookup = "Looking up…";
    try {
      const res = await api.account.orcidName(id);
      if (res.name) {
        this._set("name", res.name);
        this._lookup = "";
      } else {
        this._lookup = "That ORCID record has no public name.";
      }
    } catch (err) {
      this._lookup = this._message(err);
    }
  }

  async _save(e) {
    e?.preventDefault();
    if (Object.keys(this._draft).length === 0) {
      this._close();
      return;
    }
    this._busy = true;
    this._error = "";
    try {
      const account = await api.account.patch(this._draft);
      this.dispatchEvent(
        new CustomEvent("account-changed", { detail: { account }, bubbles: true, composed: true }),
      );
      this._close();
    } catch (err) {
      this._error = this._message(err);
    } finally {
      this._busy = false;
    }
  }

  _close() {
    this.dispatchEvent(new CustomEvent("close", { bubbles: true, composed: true }));
  }

  _onBackdropClick(e) {
    if (e.target === e.currentTarget) this._close();
  }

  _input(field, id, opts = {}) {
    const locked = this._account?.[`${field}_locked`];
    return html`<div class="field">
      <input
        id=${id}
        type=${opts.type || "text"}
        placeholder=${opts.placeholder || ""}
        autocomplete=${opts.autocomplete || "off"}
        ?readonly=${!!locked}
        .value=${this._value(field)}
        @input=${(e) => this._set(field, e.target.value)}
      />
      ${locked ? html`<span class="note">${LOCK_NOTE[locked]}</span>` : nothing}
    </div>`;
  }

  render() {
    const a = this._account;
    return html`
      <div class="backdrop" @click=${this._onBackdropClick}>
        <div class="modal" role="dialog" aria-modal="true" aria-labelledby="account-title">
          <header>
            <h2 id="account-title">Account</h2>
            <button class="close-btn" @click=${this._close} aria-label="close" title="close">✕</button>
          </header>
          <p class="hint">
            Your identity on this computer, for every archive you open here.
            Changes you make are recorded under your ORCID iD.
          </p>
          ${this._error ? html`<div class="error" role="alert">${this._error}</div>` : nothing}
          ${a
            ? html`<form @submit=${this._save}>
                <label for="acct-name">Name</label>
                <div class="field">
                  <div class="with-button">
                    <input
                      id="acct-name"
                      type="text"
                      autocomplete="name"
                      .value=${this._value("name")}
                      @input=${(e) => this._set("name", e.target.value)}
                    />
                    <button
                      type="button"
                      ?disabled=${!this._value("orcid").trim()}
                      title="Fill in the name from your public ORCID record"
                      @click=${() => this._lookupName()}
                    >From ORCID</button>
                  </div>
                  ${this._lookup ? html`<span class="note" role="status">${this._lookup}</span>` : nothing}
                </div>
                <label for="acct-orcid">ORCID iD</label>
                ${this._input("orcid", "acct-orcid", { placeholder: "0000-0000-0000-0000" })}
                <label for="acct-email">OpenAlex email</label>
                ${this._input("openalex_email", "acct-email", {
                  type: "email",
                  autocomplete: "email",
                  placeholder: "Speeds up reference lookups; optional",
                })}
              </form>`
            : this._error
              ? nothing
              : html`<p class="hint">Loading…</p>`}
          <div class="actions">
            <button type="button" @click=${this._close}>Cancel</button>
            <button type="button" class="primary" ?disabled=${this._busy || !a} @click=${(e) => this._save(e)}>
              Save
            </button>
          </div>
        </div>
      </div>
    `;
  }
}

customElements.define("sfga-account-modal", SfgaAccountModal);
