/**
 * <sfga-prompt-fork-modal>
 *
 * Fork editor for a curator prompt. Opens with a source prompt's
 * content pre-populated; curator names the fork and edits freely.
 * Save dispatches a fork-submit event with { name, content,
 * setError } for the parent to persist. Cancel dispatches
 * fork-cancel.
 *
 * The parent (invoke modal) owns the API call — this component
 * just gathers input + validates the name. Keeps the fork modal
 * portable to future places we might want to fork a prompt from
 * (e.g., a dedicated prompt-library page).
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";

class SfgaPromptForkModal extends LitElement {
  static properties = {
    forkOfName: { attribute: false },
    forkOfID: { attribute: false },
    purpose: { attribute: false },
    initialContent: { attribute: false },

    _name: { state: true },
    _content: { state: true },
    _saving: { state: true },
    _error: { state: true },
  };

  static styles = css`
    :host {
      display: contents;
    }
    .backdrop {
      position: fixed;
      inset: 0;
      background: color-mix(in oklab, var(--fg) 25%, transparent);
      display: grid;
      place-items: center;
      z-index: 60;
      padding: var(--sp-3);
    }
    .modal {
      background: var(--bg);
      color: var(--fg);
      border-radius: var(--radius-md);
      box-shadow: 0 20px 60px rgba(0, 0, 0, 0.35);
      display: grid;
      gap: var(--sp-3);
      padding: var(--sp-4);
      width: min(48rem, 95vw);
      max-height: 90vh;
      grid-template-rows: auto auto 1fr auto auto;
      min-height: 0;
    }
    header {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
    }
    header h3 {
      margin: 0;
      font-size: var(--fs-lg);
    }
    .close-btn {
      background: transparent;
      border: none;
      cursor: pointer;
      color: var(--dim);
      font-size: 1.2rem;
    }
    .fork-of {
      color: var(--dim);
      font-size: var(--fs-sm);
    }
    .fork-of code {
      font-family: var(--font-mono);
      background: color-mix(in oklab, var(--fg) 5%, transparent);
      padding: 0 var(--sp-1);
      border-radius: var(--radius-sm);
    }
    label.field {
      display: grid;
      gap: var(--sp-1);
      font-size: var(--fs-sm);
      color: var(--dim);
    }
    input[type="text"] {
      font-family: inherit;
      font-size: var(--fs-md);
      padding: var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--bg);
      color: var(--fg);
    }
    textarea {
      font-family: var(--font-mono);
      font-size: var(--fs-xs);
      padding: var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--bg);
      color: var(--fg);
      resize: vertical;
      min-height: 20rem;
      line-height: 1.4;
    }
    .error {
      color: hsl(0, 65%, 50%);
      background: color-mix(in oklab, hsl(0, 65%, 50%) 10%, var(--bg));
      padding: var(--sp-2) var(--sp-3);
      border-radius: var(--radius-sm);
      font-size: var(--fs-sm);
    }
    .actions {
      display: flex;
      gap: var(--sp-2);
      justify-content: flex-end;
    }
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border: none;
      padding: var(--sp-2) var(--sp-4);
      border-radius: var(--radius-sm);
      font-size: var(--fs-md);
      cursor: pointer;
    }
    button.primary:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    button.secondary {
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      padding: var(--sp-2) var(--sp-4);
      border-radius: var(--radius-sm);
      font-size: var(--fs-md);
      cursor: pointer;
    }
  `;

  constructor() {
    super();
    this.forkOfName = "";
    this.forkOfID = "";
    this.purpose = "";
    this.initialContent = "";
    this._name = "";
    this._content = "";
    this._saving = false;
    this._error = "";
  }

  connectedCallback() {
    super.connectedCallback();
    // Seed the name with a reasonable suggestion based on the
    // source name. Curator can override; validation happens on
    // save when the backend enforces per-purpose uniqueness.
    this._name = this._suggestName(this.forkOfName);
    this._content = this.initialContent || "";
    this._onKey = (e) => {
      if (e.key === "Escape" && !this._saving) {
        e.preventDefault();
        this._cancel();
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

  // _suggestName strips the version suffix from the source name
  // and appends "_fork" as a starting point. Curator almost
  // always overwrites this with a project-specific slug (e.g.
  // "ner_dascilloidea"); the suggestion just avoids an empty
  // input asking for creativity on modal open.
  _suggestName(sourceName) {
    if (!sourceName) return "my_prompt";
    // Strip trailing "_vNN" if present; otherwise use whole name.
    const stripped = sourceName.replace(/_v\d+$/, "");
    return `${stripped}_fork`;
  }

  _cancel() {
    this.dispatchEvent(
      new CustomEvent("fork-cancel", { bubbles: true, composed: true }),
    );
  }

  async _save() {
    const name = (this._name || "").trim();
    if (!name) {
      this._error = "name is required";
      return;
    }
    const content = this._content || "";
    if (!content.trim()) {
      this._error = "content cannot be empty";
      return;
    }
    this._saving = true;
    this._error = "";
    // Provide a setError callback in the event detail so the
    // parent (which owns the API call) can surface backend
    // errors here without needing to know our internals.
    const setError = (msg) => {
      this._error = msg;
      this._saving = false;
    };
    this.dispatchEvent(
      new CustomEvent("fork-submit", {
        detail: { name, content, setError },
        bubbles: true,
        composed: true,
      }),
    );
    // Parent normally closes us on success; if we're still open
    // after a moment it means setError was called (error path).
  }

  render() {
    return html`
      <div class="backdrop" @click=${(e) => {
        if (e.target.classList.contains("backdrop") && !this._saving) {
          this._cancel();
        }
      }}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="fork-title"
        >
          <header>
            <h3 id="fork-title">Fork prompt</h3>
            <button
              class="close-btn"
              @click=${this._cancel}
              ?disabled=${this._saving}
              aria-label="close"
              title="close"
            >
              ✕
            </button>
          </header>
          <div class="fork-of">
            Forking from
            <code>${this.forkOfName}</code>
            (purpose: <code>${this.purpose}</code>)
          </div>

          ${this._error
            ? html`<div class="error" role="alert">${this._error}</div>`
            : nothing}

          <div style="display: grid; gap: var(--sp-3); min-height: 0;">
            <label class="field">
              name
              <input
                type="text"
                .value=${this._name}
                @input=${(e) => (this._name = e.target.value)}
                placeholder="e.g. ner_dascilloidea"
                autocomplete="off"
                spellcheck="false"
                ?disabled=${this._saving}
                required
              />
            </label>
            <label class="field" style="min-height: 0;">
              content
              <textarea
                .value=${this._content}
                @input=${(e) => (this._content = e.target.value)}
                ?disabled=${this._saving}
                spellcheck="false"
              ></textarea>
            </label>
          </div>

          <div class="actions">
            <button
              type="button"
              class="secondary"
              @click=${this._cancel}
              ?disabled=${this._saving}
            >
              cancel
            </button>
            <button
              type="button"
              class="primary"
              @click=${this._save}
              ?disabled=${this._saving}
            >
              ${this._saving ? "saving…" : "save fork"}
            </button>
          </div>
        </div>
      </div>
    `;
  }
}

customElements.define("sfga-prompt-fork-modal", SfgaPromptForkModal);
