/**
 * <sfga-llm-provider-modal>
 *
 * Curator-facing management surface for hive's configured LLM
 * providers. Lists what's set up in ~/.config/sfborg/hive/config.yml,
 * lets the curator add a new one (name + kind + default model +
 * API key pasted once, stored in the OS keyring via
 * llm.SetKeyringSecret on the backend), and delete existing entries.
 *
 * Opens from the top-nav sparkles button in SfgaApp. Dispatches
 * "close" when the curator dismisses via ✕ button, backdrop click,
 * or Escape.
 *
 * Deliberately doesn't hook into the modal-stack helpers in app.js
 * (openModal / closeModal / isTopModal) — those are file-local
 * there. As those helpers migrate to lib/modal.js, this file will
 * import them and gain proper nested-modal coordination. Meanwhile
 * the modal renders correctly with the shared z-index tokens.
 */

import { LitElement, html, css, svg, nothing } from "/vendor/lit-3.x.x.min.js";
import { api, Problem } from "/lib/api.js";

// PROVIDER_PRESETS pre-fills the add-provider form with sensible
// defaults for well-known LLM services. Curator picks one from
// the dropdown at the top of the form and every field
// (name, kind, base_url, default_model, credential hints)
// snaps to that service's expected shape. "Custom" leaves the
// form blank for anything not on the list.
//
// Adding a new preset is a one-entry addition — the form logic
// is fully data-driven off this object.
const PROVIDER_PRESETS = {
  custom: { label: "Custom (blank)", fields: {} },
  openrouter: {
    label: "OpenRouter (100+ models, one key)",
    fields: {
      name: "openrouter",
      kind: "openai_compat",
      base_url: "https://openrouter.ai/api/v1",
      default_model: "anthropic/claude-haiku-4-5",
      source: "env",
      env_var: "OPENROUTER_API_KEY",
    },
    hint: "OpenRouter fronts many providers behind one OpenAI-compatible endpoint. Model IDs are prefixed like anthropic/… or openai/…. See openrouter.ai/models.",
  },
  anthropic: {
    label: "Anthropic (Claude — native)",
    fields: {
      name: "anthropic",
      kind: "anthropic",
      default_model: "claude-haiku-4-5",
      source: "env",
      env_var: "ANTHROPIC_API_KEY",
    },
    hint: "Native Anthropic /v1/messages. Unlocks prompt caching (~90% savings on repeated NER runs against the same prompt).",
  },
  openai: {
    label: "OpenAI (GPT-4x)",
    fields: {
      name: "openai",
      kind: "openai_compat",
      base_url: "https://api.openai.com/v1",
      default_model: "gpt-4o-mini",
      source: "env",
      env_var: "OPENAI_API_KEY",
    },
    hint: "OpenAI's own endpoint. Use gpt-4o for tree composition, gpt-4o-mini for bulk NER.",
  },
  gemini: {
    label: "Google Gemini (OpenAI-compat endpoint)",
    fields: {
      name: "gemini",
      kind: "openai_compat",
      base_url: "https://generativelanguage.googleapis.com/v1beta/openai",
      default_model: "gemini-2.5-flash",
      source: "env",
      env_var: "GEMINI_API_KEY",
    },
    hint: "Gemini via its OpenAI-compatible layer. Notably larger context windows (1M+ tokens) — the go-to for monograph-scale papers until chunking lands.",
  },
  ollama: {
    label: "Ollama (self-hosted local)",
    fields: {
      name: "ollama",
      kind: "openai_compat",
      base_url: "http://localhost:11434/v1",
      default_model: "llama3.1:70b",
      source: "plaintext",
      api_key: "ollama",
    },
    hint: "Local Ollama server. No real auth (any string works as api_key). Model IDs match what's pulled locally: run `ollama list`.",
  },
};

// Inline SVG icons matching the top-level app.js iconPaths style
// (Lucide). Duplicated here rather than imported to keep the
// modal component self-contained; extract to a shared lib/icons.js
// once a second component needs the same icons.
const iconPencil = svg`
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
       stroke-linecap="round" stroke-linejoin="round" width="14" height="14">
    <path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/>
    <path d="m15 5 4 4"/>
  </svg>
`;
const iconTrash = svg`
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
       stroke-linecap="round" stroke-linejoin="round" width="14" height="14">
    <path d="M3 6h18"/>
    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/>
    <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
    <line x1="10" x2="10" y1="11" y2="17"/>
    <line x1="14" x2="14" y1="11" y2="17"/>
  </svg>
`;

class SfgaLlmProviderModal extends LitElement {
  static properties = {
    _providers: { state: true },
    _loading: { state: true },
    _saving: { state: true },
    _error: { state: true },
    _form: { state: true },
    _showAddForm: { state: true },
    // When set, the form is in "edit existing provider" mode. The
    // value is the name of the provider being edited (which is
    // also the primary key — name is immutable via PATCH).
    _editingName: { state: true },
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
    p.hint {
      color: var(--dim);
      font-size: var(--fs-sm);
      margin: 0;
    }
    .provider-list {
      display: grid;
      gap: var(--sp-2);
    }
    .provider-list .empty {
      color: var(--dim);
      font-size: var(--fs-sm);
      font-style: italic;
      padding: var(--sp-3);
      border: 1px dashed var(--border);
      border-radius: var(--radius-md);
      text-align: center;
    }
    .provider-row {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: var(--sp-2);
      align-items: center;
      padding: var(--sp-2) var(--sp-3);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
    }
    .provider-row .name {
      font-weight: 600;
    }
    .provider-row .meta {
      color: var(--dim);
      font-size: var(--fs-sm);
    }
    .kind-chip {
      display: inline-block;
      font-size: var(--fs-xs);
      padding: 0 var(--sp-2);
      border-radius: var(--radius-pill);
      background: color-mix(in oklab, var(--accent) 15%, transparent);
      color: var(--accent);
      margin-left: var(--sp-2);
      vertical-align: baseline;
    }
    .row-actions {
      display: inline-flex;
      gap: var(--sp-1);
    }
    .icon-btn {
      background: none;
      border: 1px solid var(--border);
      color: var(--dim);
      border-radius: var(--radius-md);
      padding: var(--sp-1);
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      transition: color var(--transition-fast),
        border-color var(--transition-fast);
    }
    .icon-btn:hover {
      color: var(--fg);
      border-color: var(--accent);
    }
    .icon-btn.danger:hover {
      color: var(--sev-error);
      border-color: var(--sev-error);
    }
    .add-toggle {
      background: none;
      border: 1px solid var(--border);
      color: var(--fg);
      border-radius: var(--radius-md);
      padding: var(--sp-2) var(--sp-3);
      cursor: pointer;
      font-family: inherit;
      font-size: var(--fs-md);
    }
    .add-toggle:hover {
      border-color: var(--accent);
      color: var(--accent);
    }
    form {
      display: grid;
      gap: var(--sp-2);
      padding: var(--sp-3);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      background: color-mix(in oklab, var(--fg) 3%, var(--bg));
    }
    label {
      display: grid;
      gap: var(--sp-1);
      font-size: var(--fs-sm);
    }
    input,
    select {
      font-family: inherit;
      font-size: var(--fs-md);
      padding: var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      background: var(--bg);
      color: var(--fg);
    }
    input:focus,
    select:focus {
      outline: 2px solid var(--accent);
      outline-offset: -1px;
    }
    .form-note {
      color: var(--dim);
      font-size: var(--fs-xs);
    }
    .actions {
      display: flex;
      gap: var(--sp-2);
      justify-content: flex-end;
      margin-top: var(--sp-2);
    }
    button.primary {
      background: var(--accent);
      color: var(--accent-fg);
      border: none;
      border-radius: var(--radius-md);
      padding: var(--sp-2) var(--sp-4);
      font-family: inherit;
      font-size: var(--fs-md);
      cursor: pointer;
    }
    button.primary:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    button.secondary {
      background: none;
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-2) var(--sp-4);
      font-family: inherit;
      font-size: var(--fs-md);
      cursor: pointer;
    }
    .error {
      color: var(--sev-error);
      font-size: var(--fs-sm);
      padding: var(--sp-2);
      border-left: 3px solid var(--sev-error);
      background: var(--sev-error-bg);
    }
    .keyring-note {
      color: var(--dim);
      font-size: var(--fs-xs);
      font-style: italic;
    }
    .warn-note {
      color: var(--sev-warn);
      font-size: var(--fs-xs);
    }
  `;

  constructor() {
    super();
    this._providers = [];
    this._loading = false;
    this._saving = false;
    this._error = "";
    this._showAddForm = false;
    this._editingName = "";
    this._form = this._emptyForm();
  }

  _emptyForm() {
    return {
      preset: "custom",
      name: "",
      kind: "anthropic",
      default_model: "",
      base_url: "",
      // source picks how the API key gets stored (or looked up):
      //   "keyring"   — pasted key goes into the OS keyring
      //   "env"       — key is read from a named env var at
      //                 invoke time; nothing is stored by hive
      //   "plaintext" — key is written directly into config.yml
      // Keyring is the safest default when it works. In a Docker
      // container without a D-Bus bridge, keyring fails at save
      // time; env is the right fallback then.
      source: "keyring",
      api_key: "",
      env_var: "",
    };
  }

  _applyPreset(presetKey) {
    const preset = PROVIDER_PRESETS[presetKey];
    if (!preset) return;
    const patch = preset.fields || {};
    this._form = {
      ...this._emptyForm(),
      ...patch,
      preset: presetKey,
    };
  }

  connectedCallback() {
    super.connectedCallback();
    this._loadProviders();
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

  async _loadProviders() {
    this._loading = true;
    this._error = "";
    try {
      const resp = await api.llm.providers.list();
      this._providers = resp.items || [];
    } catch (err) {
      this._error = this._errMsg(err);
    } finally {
      this._loading = false;
    }
  }

  _errMsg(err) {
    if (err instanceof Problem) {
      return err.detail || err.title || `error ${err.status}`;
    }
    return String(err?.message || err);
  }

  _close() {
    this.dispatchEvent(
      new CustomEvent("close", { bubbles: true, composed: true }),
    );
  }

  _onBackdropClick(e) {
    if (e.target.classList.contains("backdrop")) {
      this._close();
    }
  }

  _onEdit(provider) {
    // Pre-fill the form with existing values. API key stays blank
    // — we don't return the secret from the API, and rotating is
    // opt-in per the patch endpoint's contract (blank api_key =
    // keep current value).
    this._form = {
      name: provider.name,
      kind: provider.kind,
      default_model: provider.default_model || "",
      base_url: "",
      // Note: we can't recover the credential source from the
      // list endpoint (it deliberately doesn't expose credential
      // internals). Default to keyring; the source picker lets
      // the curator switch modes if they want to.
      source: "keyring",
      api_key: "",
      env_var: "",
    };
    this._editingName = provider.name;
    this._showAddForm = true;
    this._error = "";
  }

  async _onDelete(provider) {
    const ok = window.confirm(
      `Delete provider "${provider.name}"?\n\n` +
        `This removes the config entry AND deletes the API key from ` +
        `your OS keyring. Not reversible without re-adding.`,
    );
    if (!ok) return;
    this._error = "";
    try {
      await api.llm.providers.delete(provider.name);
      await this._loadProviders();
    } catch (err) {
      this._error = `delete failed: ${this._errMsg(err)}`;
    }
  }

  _onFormInput(field, value) {
    this._form = { ...this._form, [field]: value };
  }

  _defaultModelPlaceholder() {
    // Suggest a sensible default model based on the selected kind
    // so curators unfamiliar with model identifiers get a starting
    // point. Placeholder only — the field is authoritative.
    switch (this._form.kind) {
      case "anthropic":
        return "claude-opus-4-7";
      case "openai_compat":
        return "gpt-4o (or llama3.1:70b, etc.)";
      default:
        return "";
    }
  }

  _baseUrlPlaceholder() {
    switch (this._form.kind) {
      case "anthropic":
        return "https://api.anthropic.com  (leave blank for default)";
      case "openai_compat":
        return "https://api.openai.com/v1  (or Ollama, LiteLLM, etc.)";
      default:
        return "";
    }
  }

  _canSubmit() {
    const f = this._form;
    if (!f.name || !f.kind) return false;
    // openai_compat needs a base URL on CREATE. On edit, an
    // unchanged base_url is fine (patch leaves empty fields
    // alone), so allow submit — the server-side check will
    // catch cases where kind was switched to openai_compat
    // without also supplying base_url.
    if (!this._editingName && f.kind === "openai_compat" && !f.base_url)
      return false;
    // On edit, source + credential fields are optional (patch
    // leaves the credential alone when api_key / env_var are
    // blank). On create, they're required by the mode.
    if (this._editingName) return true;
    switch (f.source) {
      case "env":
        return !!f.env_var;
      case "keyring":
      case "plaintext":
        return !!f.api_key;
      default:
        return false;
    }
  }

  async _onSubmit(e) {
    e.preventDefault();
    if (!this._canSubmit()) return;
    this._saving = true;
    this._error = "";
    const f = this._form;
    try {
      if (this._editingName) {
        // Patch: only send non-empty fields so the server-side
        // "leave alone" semantics apply for anything the curator
        // didn't change.
        const body = {};
        if (f.kind) body.kind = f.kind;
        if (f.default_model.trim()) body.default_model = f.default_model.trim();
        if (f.base_url.trim()) body.base_url = f.base_url.trim();
        if (f.api_key) {
          body.api_key = f.api_key;
          body.source = f.source; // ensure server knows target source
        } else if (f.source === "env" && f.env_var.trim()) {
          body.source = "env";
          body.env_var = f.env_var.trim();
        }
        await api.llm.providers.patch(this._editingName, body);
      } else {
        const body = {
          name: f.name.trim(),
          kind: f.kind,
          default_model: f.default_model.trim(),
          base_url: f.base_url.trim(),
          source: f.source,
        };
        if (f.source === "env") {
          body.env_var = f.env_var.trim();
        } else {
          body.api_key = f.api_key;
        }
        await api.llm.providers.create(body);
      }
      this._form = this._emptyForm();
      this._showAddForm = false;
      this._editingName = "";
      await this._loadProviders();
    } catch (err) {
      this._error = `${this._editingName ? "update" : "add"} failed: ${this._errMsg(err)}`;
    } finally {
      this._saving = false;
    }
  }

  render() {
    return html`
      <div class="backdrop" @click=${this._onBackdropClick}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="llm-modal-title"
        >
          <header>
            <h2 id="llm-modal-title">LLM Providers</h2>
            <button
              class="close-btn"
              @click=${this._close}
              aria-label="close"
              title="close"
            >
              ✕
            </button>
          </header>

          <p class="hint">
            Configure the LLM providers hive uses for NER + compose
            invocations. API keys are stored in your OS keyring
            (Keychain / DPAPI / Secret Service), never in the config
            file.
          </p>

          ${this._error
            ? html`<div class="error" role="alert">${this._error}</div>`
            : nothing}

          <section class="provider-list">
            ${this._loading
              ? html`<p class="hint">loading…</p>`
              : this._providers.length === 0
                ? html`<div class="empty">
                    No providers configured yet. Add one below to enable
                    LLM-backed extraction.
                  </div>`
                : this._providers.map(
                    (p) => html`
                      <div class="provider-row">
                        <div>
                          <span class="name">${p.name}</span>
                          <span class="kind-chip">${p.kind}</span>
                          ${p.default_model
                            ? html`<div class="meta">
                                default: ${p.default_model}
                              </div>`
                            : nothing}
                        </div>
                        <div class="row-actions">
                          <button
                            class="icon-btn"
                            @click=${() => this._onEdit(p)}
                            title="edit this provider"
                            aria-label="edit provider"
                          >
                            ${iconPencil}
                          </button>
                          <button
                            class="icon-btn danger"
                            @click=${() => this._onDelete(p)}
                            title="remove this provider"
                            aria-label="delete provider"
                          >
                            ${iconTrash}
                          </button>
                        </div>
                      </div>
                    `,
                  )}
          </section>

          ${this._showAddForm
            ? this._renderForm()
            : html`<button
                class="add-toggle"
                @click=${() => (this._showAddForm = true)}
              >
                + add provider
              </button>`}
        </div>
      </div>
    `;
  }

  _renderForm() {
    const preset = PROVIDER_PRESETS[this._form.preset || "custom"];
    return html`
      <form @submit=${this._onSubmit}>
        ${!this._editingName
          ? html`
              <label>
                <span>preset</span>
                <select
                  .value=${this._form.preset || "custom"}
                  @change=${(e) => this._applyPreset(e.target.value)}
                >
                  ${Object.entries(PROVIDER_PRESETS).map(
                    ([key, p]) => html`
                      <option value=${key}>${p.label}</option>
                    `,
                  )}
                </select>
                ${preset && preset.hint
                  ? html`<span class="form-note">${preset.hint}</span>`
                  : nothing}
              </label>
            `
          : nothing}
        <label>
          <span>name${this._editingName ? " (fixed)" : ""}</span>
          <input
            type="text"
            required
            ?disabled=${!!this._editingName}
            .value=${this._form.name}
            @input=${(e) => this._onFormInput("name", e.target.value)}
            placeholder="anthropic-main"
          />
          <span class="form-note">
            ${this._editingName
              ? "Rename is not supported — delete and re-add if you need a different name."
              : "Any label; also becomes the keyring entry identifier."}
          </span>
        </label>

        <label>
          <span>kind</span>
          <select
            .value=${this._form.kind}
            @change=${(e) => this._onFormInput("kind", e.target.value)}
          >
            <option value="anthropic">
              Anthropic (Claude — native /v1/messages)
            </option>
            <option value="openai_compat">
              OpenAI-compatible (OpenAI, Gemini, Ollama, LiteLLM, …)
            </option>
          </select>
        </label>

        <label>
          <span>default model</span>
          <input
            type="text"
            .value=${this._form.default_model}
            @input=${(e) =>
              this._onFormInput("default_model", e.target.value)}
            placeholder=${this._defaultModelPlaceholder()}
          />
        </label>

        ${this._form.kind === "openai_compat"
          ? html`<label>
              <span>base URL</span>
              <input
                type="text"
                required
                .value=${this._form.base_url}
                @input=${(e) => this._onFormInput("base_url", e.target.value)}
                placeholder=${this._baseUrlPlaceholder()}
              />
            </label>`
          : nothing}

        <label>
          <span>credential source</span>
          <select
            .value=${this._form.source}
            @change=${(e) => this._onFormInput("source", e.target.value)}
          >
            <option value="keyring">
              OS keyring (Keychain / DPAPI / Secret Service)
            </option>
            <option value="env">
              Environment variable (read at invoke time)
            </option>
            <option value="plaintext">
              Plaintext in config.yml (least secure)
            </option>
          </select>
          <span class="form-note">
            ${this._sourceHint()}
          </span>
        </label>

        ${this._renderCredentialInput()}

        <div class="actions">
          <button
            type="button"
            class="secondary"
            @click=${() => {
              this._showAddForm = false;
              this._editingName = "";
              this._form = this._emptyForm();
              this._error = "";
            }}
          >
            cancel
          </button>
          <button
            type="submit"
            class="primary"
            ?disabled=${this._saving || !this._canSubmit()}
          >
            ${this._saving
              ? "saving…"
              : this._editingName
                ? "save changes"
                : "add provider"}
          </button>
        </div>
      </form>
    `;
  }

  _sourceHint() {
    switch (this._form.source) {
      case "keyring":
        return "Best default on native desktop. Fails inside Docker containers without a D-Bus bridge to the host keyring — pick Environment variable there instead.";
      case "env":
        return "Server reads the key from the named env var on every invocation. No secret stored by hive; you set the env var out-of-band (docker-compose, systemd, .env).";
      case "plaintext":
        return "Key is written directly into ~/.config/sfborg/hive/config.yml (perms 0600). Convenient for quick tests but a plain grep target for info-stealers — prefer keyring or env when you can.";
      default:
        return "";
    }
  }

  _renderCredentialInput() {
    const editing = !!this._editingName;
    if (this._form.source === "env") {
      return html`
        <label>
          <span>env var name</span>
          <input
            type="text"
            ?required=${!editing}
            autocomplete="off"
            .value=${this._form.env_var}
            @input=${(e) => this._onFormInput("env_var", e.target.value)}
            placeholder=${this._envVarPlaceholder()}
          />
          <span class="form-note">
            ${editing
              ? "Leave blank to keep the current env var. Change to rename which variable the server reads."
              : "Set this variable in your container / shell environment. The server reads it at every invoke — no restart needed when you rotate the value."}
          </span>
        </label>
      `;
    }
    const isPlain = this._form.source === "plaintext";
    return html`
      <label>
        <span>${editing ? "new API key (optional)" : "API key"}</span>
        <input
          type="password"
          ?required=${!editing}
          autocomplete="off"
          .value=${this._form.api_key}
          @input=${(e) => this._onFormInput("api_key", e.target.value)}
          placeholder=${editing ? "leave blank to keep current key" : "sk-…"}
        />
        <span class=${isPlain ? "warn-note" : "keyring-note"}>
          ${editing
            ? "Supply a value only to rotate the key. Leave blank to keep the currently-stored credential."
            : isPlain
              ? "Will be stored in plaintext inside config.yml."
              : "Pasted once, stored in the OS keyring. Not written to config.yml."}
        </span>
      </label>
    `;
  }

  _envVarPlaceholder() {
    switch (this._form.kind) {
      case "anthropic":
        return "ANTHROPIC_API_KEY";
      case "openai_compat":
        return "OPENAI_API_KEY";
      default:
        return "";
    }
  }
}

customElements.define("sfga-llm-provider-modal", SfgaLlmProviderModal);
