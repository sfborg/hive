/**
 * <sfga-llm-invoke-modal>
 *
 * Per-reference LLM extraction pipeline surface. One-click flow:
 * curator picks an NER model + a Tree model, hits Run, backend
 * fetches the paper's JATS from the attached-PDF sidecar and
 * orchestrates NER → compose in sequence, persisting a linked
 * pair of hive__extraction_run rows and returning the composed
 * tree for immediate display.
 *
 * Curators do not see or edit the JATS — that's plumbing.
 * Prompts ARE editable via the picker + fork editor: pick a
 * shipped built-in or a curator fork per stage, preview its
 * content read-only, fork any prompt to a curator-owned copy
 * that's edited freely. Backend defaults kick in if no prompt
 * is selected. Fork storage is per-archive (hive__prompt).
 *
 * Requires an attached PDF on the reference — the empty state
 * hints at that when no PDF is attached.
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";
import { api, Problem } from "/lib/api.js";

class SfgaLlmInvokeModal extends LitElement {
  static properties = {
    referenceId: { attribute: false },
    referenceLabel: { attribute: false },
    attachedShas: { attribute: false },

    _providers: { state: true },
    _nerProvider: { state: true },
    _treeProvider: { state: true },
    // Per-step model overrides — free-text so curator can try any
    // model the chosen provider supports without registering a
    // whole new provider entry. Empty = use the provider's
    // configured default_model. Auto-fetched model lists from
    // the provider API is a future upgrade (each vendor has its
    // own endpoint shape).
    _nerModel: { state: true },
    _treeModel: { state: true },
    // Per-provider model list cache. Populated on first pick of
    // a provider that supports models listing (OpenRouter,
    // OpenAI, Ollama, Gemini-compat, etc.); rendered as a
    // datalist to give the model-override input autocomplete
    // over the actual available IDs. Cache key is provider name.
    _modelsCache: { state: true },
    _loadingProviders: { state: true },
    _running: { state: true },
    _result: { state: true },
    _error: { state: true },
    // Prompt picker state. _prompts is the merged shipped +
    // curator list from GET /api/prompt (populated on modal open).
    // _nerPromptID / _composePromptID hold the current picker
    // selection — empty falls back to the backend's shipped
    // default. _promptPreview holds { id, content } for the
    // currently-selected prompt of the "focused" picker, so
    // the preview panel can render read-only text.
    _prompts: { state: true },
    _promptsLoading: { state: true },
    _nerPromptID: { state: true },
    _composePromptID: { state: true },
    _promptPreview: { state: true },
    _previewPurpose: { state: true },
    _forkModalOpen: { state: true },
    _forkSource: { state: true },
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
      min-width: 28rem;
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
    .ref-label {
      color: var(--dim);
      font-size: var(--fs-sm);
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
    label {
      display: grid;
      gap: var(--sp-1);
      font-size: var(--fs-sm);
    }
    select {
      font-family: inherit;
      font-size: var(--fs-md);
      padding: var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      background: var(--bg);
      color: var(--fg);
    }
    select:focus {
      outline: 2px solid var(--accent);
      outline-offset: -1px;
    }
    .form-row {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: var(--sp-3);
    }
    .form-note {
      color: var(--dim);
      font-size: var(--fs-xs);
    }
    .prompt-note {
      color: var(--dim);
      font-size: var(--fs-xs);
      padding: var(--sp-2) var(--sp-3);
      border: 1px dashed var(--border);
      border-radius: var(--radius-md);
    }
    .prompt-pickers {
      display: grid;
      gap: var(--sp-2);
      padding: var(--sp-2) var(--sp-3);
      border: 1px dashed var(--border);
      border-radius: var(--radius-md);
    }
    .prompt-picker {
      display: grid;
      grid-template-columns: auto 1fr auto auto;
      gap: var(--sp-2);
      align-items: center;
      font-size: var(--fs-sm);
    }
    .prompt-picker label {
      color: var(--dim);
      font-weight: 600;
      white-space: nowrap;
    }
    .prompt-picker select {
      min-width: 0;
      font-family: inherit;
      font-size: var(--fs-sm);
      padding: var(--sp-1) var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--bg);
      color: var(--fg);
    }
    .prompt-preview-toggle,
    .prompt-fork-btn {
      font-family: inherit;
      font-size: var(--fs-xs);
      padding: var(--sp-1) var(--sp-2);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--bg);
      color: var(--fg);
      cursor: pointer;
    }
    .prompt-preview-toggle:hover:not(:disabled),
    .prompt-fork-btn:hover:not(:disabled) {
      background: color-mix(in oklab, var(--fg) 5%, var(--bg));
    }
    .prompt-preview-toggle:disabled,
    .prompt-fork-btn:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    .prompt-preview {
      display: flex;
      flex-direction: column;
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      background: color-mix(in oklab, var(--fg) 3%, var(--bg));
      max-height: 24rem;
      overflow: hidden;
    }
    .prompt-preview-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--sp-2);
      padding: var(--sp-2) var(--sp-3);
      border-bottom: 1px solid var(--border);
      background: color-mix(in oklab, var(--fg) 5%, var(--bg));
    }
    .prompt-preview-label {
      font-family: var(--font-mono);
      font-size: var(--fs-sm);
      color: var(--fg);
    }
    .prompt-preview-source {
      font-size: 0.65rem;
      font-weight: 700;
      letter-spacing: 0.05em;
      text-transform: uppercase;
      padding: 0 var(--sp-1);
      border-radius: var(--radius-sm);
      margin-left: var(--sp-1);
    }
    .prompt-preview-source.builtin {
      color: var(--dim);
      background: color-mix(in oklab, var(--dim) 15%, transparent);
    }
    .prompt-preview-source.curator {
      color: var(--accent);
      background: color-mix(in oklab, var(--accent) 15%, transparent);
    }
    .prompt-preview-close {
      background: transparent;
      border: none;
      cursor: pointer;
      color: var(--dim);
      font-size: 1rem;
      padding: 0 var(--sp-1);
    }
    .prompt-preview-close:hover {
      color: var(--fg);
    }
    .prompt-preview-body {
      margin: 0;
      padding: var(--sp-2) var(--sp-3);
      overflow: auto;
      font-family: var(--font-mono);
      font-size: var(--fs-xs);
      line-height: 1.4;
      color: var(--fg);
      white-space: pre-wrap;
      word-break: break-word;
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
      border-radius: var(--radius-md);
      padding: var(--sp-2) var(--sp-5);
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
    .empty-pdf {
      padding: var(--sp-4);
      border: 1px dashed var(--border);
      border-radius: var(--radius-md);
      color: var(--dim);
      text-align: center;
      font-size: var(--fs-sm);
    }
    .running {
      padding: var(--sp-4);
      display: grid;
      gap: var(--sp-2);
      text-align: center;
    }
    .running .spinner {
      font-size: var(--fs-xl);
    }
    .result {
      padding: var(--sp-3);
      border: 1px solid var(--sev-info);
      border-radius: var(--radius-md);
      background: var(--sev-info-bg);
      display: grid;
      gap: var(--sp-2);
    }
    .result .headline {
      font-weight: 600;
      font-size: var(--fs-md);
      color: var(--sev-info);
    }
    .result .step {
      display: grid;
      grid-template-columns: max-content 1fr max-content;
      gap: var(--sp-2) var(--sp-3);
      font-size: var(--fs-sm);
      align-items: baseline;
    }
    .result .step .name {
      font-weight: 600;
    }
    .result .step .meta {
      color: var(--dim);
      font-family: var(--font-mono);
      font-size: var(--fs-xs);
    }
    .result .step .cost {
      font-family: var(--font-mono);
    }
    .result .total {
      border-top: 1px solid color-mix(in oklab, var(--sev-info) 30%, transparent);
      padding-top: var(--sp-2);
      display: flex;
      justify-content: space-between;
      font-weight: 600;
    }
  `;

  constructor() {
    super();
    this.referenceId = "";
    this.referenceLabel = "";
    this.attachedShas = [];
    this._providers = [];
    this._nerProvider = "";
    this._treeProvider = "";
    this._nerModel = "";
    this._treeModel = "";
    this._modelsCache = {};
    this._loadingProviders = false;
    this._running = false;
    this._result = null;
    this._error = "";
    this._prompts = [];
    this._promptsLoading = false;
    this._nerPromptID = "";
    this._composePromptID = "";
    this._promptPreview = null;
    this._previewPurpose = "";
    this._forkModalOpen = false;
    this._forkSource = null;
  }

  connectedCallback() {
    super.connectedCallback();
    this._loadProviders();
    this._loadPrompts();
    this._onKey = (e) => {
      if (e.key === "Escape" && !this._running) {
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

  // _loadPrompts fetches the merged shipped + curator prompt list
  // once on modal open. Content is omitted here (lean list payload);
  // preview panel calls api.prompt.get() to fetch content on demand.
  async _loadPrompts() {
    this._promptsLoading = true;
    try {
      const resp = await api.prompt.list();
      this._prompts = resp.items || [];
      // Seed the picker selections to the newest curator fork for
      // each purpose if one exists; else leave empty so the backend
      // picks up its shipped default.
      if (!this._nerPromptID) {
        this._nerPromptID = this._defaultPromptID("ner");
      }
      if (!this._composePromptID) {
        this._composePromptID = this._defaultPromptID("compose");
      }
    } catch (err) {
      // Prompt-list failure isn't fatal — the pipeline still runs
      // with backend defaults. Surface subtly rather than blocking.
      console.warn("prompt list load failed", err);
    } finally {
      this._promptsLoading = false;
    }
  }

  // _defaultPromptID picks a sensible default for the given
  // purpose from the loaded prompt list. Prefers the newest
  // built-in with the highest version suffix so re-open of the
  // modal after a hive upgrade uses the latest shipped prompt.
  // Empty return means "let the backend use its shipped default"
  // — safest fallback when the list hasn't loaded yet.
  _defaultPromptID(purpose) {
    const shipped = (this._prompts || []).filter(
      (p) => p.purpose === purpose && p.source === "builtin",
    );
    if (shipped.length === 0) return "";
    // Sort by name descending — "ner_v6" > "ner_v5" lexically for
    // single-digit versions. Good enough for the near-term; a
    // stricter version-parse can slot in when we hit v10+.
    shipped.sort((a, b) => (a.name < b.name ? 1 : -1));
    return shipped[0].id;
  }

  // _openPreview fetches the selected prompt's content and pins
  // it in the preview panel. Called when the curator clicks the
  // preview affordance next to a picker OR when the picker
  // selection changes and the panel is already open for that
  // purpose.
  async _openPreview(purpose, promptID) {
    if (!promptID) {
      this._promptPreview = null;
      this._previewPurpose = "";
      return;
    }
    this._previewPurpose = purpose;
    try {
      const p = await api.prompt.get(promptID);
      this._promptPreview = {
        id: promptID,
        name: p.name,
        source: p.source,
        purpose: p.purpose,
        content: p.content || "",
      };
    } catch (err) {
      console.warn("prompt preview load failed", err);
      this._promptPreview = null;
    }
  }

  _closePreview() {
    this._promptPreview = null;
    this._previewPurpose = "";
  }

  _onPromptPick(purpose, promptID) {
    if (purpose === "ner") this._nerPromptID = promptID;
    else this._composePromptID = promptID;
    // If the preview panel is open for this purpose, refresh it
    // with the newly-selected prompt so curator sees the content
    // they just switched to.
    if (this._previewPurpose === purpose) {
      this._openPreview(purpose, promptID);
    }
  }

  // _openForkModal captures the source prompt (from the currently
  // selected picker) and opens the fork modal with its content
  // pre-populated. Fork always has a source — no create-from-
  // scratch flow in v0.
  async _openForkModal(purpose) {
    const sourceID =
      purpose === "ner" ? this._nerPromptID : this._composePromptID;
    if (!sourceID) return;
    try {
      const p = await api.prompt.get(sourceID);
      this._forkSource = {
        purpose,
        forkOfID: sourceID,
        forkOfName: p.name,
        content: p.content || "",
      };
      this._forkModalOpen = true;
    } catch (err) {
      this._error = `fork setup failed: ${this._errMsg(err)}`;
    }
  }

  _closeForkModal() {
    this._forkModalOpen = false;
    this._forkSource = null;
  }

  async _submitFork(name, content) {
    if (!this._forkSource) return;
    try {
      const created = await api.prompt.create({
        name,
        purpose: this._forkSource.purpose,
        content,
        fork_of: this._forkSource.forkOfID,
      });
      // Refresh the list and select the new fork in its purpose's
      // picker so the curator's next Run uses it.
      await this._loadPrompts();
      if (this._forkSource.purpose === "ner") {
        this._nerPromptID = created.id;
      } else {
        this._composePromptID = created.id;
      }
      this._closeForkModal();
    } catch (err) {
      // Keep the modal open so curator can fix the name / content.
      throw err;
    }
  }

  async _loadProviders() {
    this._loadingProviders = true;
    this._error = "";
    try {
      const resp = await api.llm.providers.list();
      this._providers = resp.items || [];
      if (this._providers.length > 0) {
        // Default: same provider for both steps. Curators who
        // want different models per step configure two provider
        // entries and switch on one of the two dropdowns.
        const first = this._providers[0].name;
        if (!this._nerProvider) this._nerProvider = first;
        if (!this._treeProvider) this._treeProvider = first;
        // Kick off model-list fetch for the default provider
        // so the datalist autocomplete populates before the
        // curator touches the override input.
        this._fetchModelsFor(first);
      }
    } catch (err) {
      this._error = this._errMsg(err);
    } finally {
      this._loadingProviders = false;
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
    if (e.target.classList.contains("backdrop") && !this._running) {
      this._close();
    }
  }

  _hasPDF() {
    return this.attachedShas && this.attachedShas.length > 0;
  }

  _canSubmit() {
    return (
      !this._running &&
      this._hasPDF() &&
      this._nerProvider &&
      this._treeProvider
    );
  }

  async _onSubmit(e) {
    e.preventDefault();
    if (!this._canSubmit()) return;
    this._running = true;
    this._error = "";
    try {
      const body = {
        reference_id: this.referenceId,
        ner_provider: this._nerProvider,
        tree_provider: this._treeProvider,
      };
      if (this._nerModel.trim()) body.ner_model = this._nerModel.trim();
      if (this._treeModel.trim()) body.tree_model = this._treeModel.trim();
      if (this._nerPromptID) body.ner_prompt_id = this._nerPromptID;
      if (this._composePromptID) body.compose_prompt_id = this._composePromptID;
      this._result = await api.llm.pipeline(body);
      this.dispatchEvent(
        new CustomEvent("run-created", {
          detail: this._result,
          bubbles: true,
          composed: true,
        }),
      );
    } catch (err) {
      this._error = `run failed: ${this._errMsg(err)}`;
    } finally {
      this._running = false;
    }
  }

  _formatUSD(n) {
    if (n == null) return "—";
    if (n < 0.01) return `~$${n.toFixed(4)}`;
    return `~$${n.toFixed(2)}`;
  }

  render() {
    return html`
      <div class="backdrop" @click=${this._onBackdropClick}>
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="invoke-title"
        >
          <header>
            <h2 id="invoke-title">Run extraction</h2>
            <button
              class="close-btn"
              @click=${this._close}
              ?disabled=${this._running}
              aria-label="close"
              title="close"
            >
              ✕
            </button>
          </header>
          <div class="ref-label" title=${this.referenceId}>
            ${this.referenceLabel || this.referenceId}
          </div>

          ${this._error
            ? html`<div class="error" role="alert">${this._error}</div>`
            : nothing}

          ${this._loadingProviders
            ? html`<p class="form-note">loading providers…</p>`
            : this._providers.length === 0
              ? html`<div class="empty-pdf">
                  No LLM providers configured. Click the ✨ icon in
                  the top-right to add one.
                </div>`
              : !this._hasPDF()
                ? html`<div class="empty-pdf">
                    No PDF attached to this reference. Upload one via
                    the reference-edit modal in the Bibliography tab
                    to enable extraction — hive reads the paper's
                    JATS body from the attached-PDF sidecar.
                  </div>`
                : this._running
                  ? this._renderRunning()
                  : this._result
                    ? this._renderResult()
                    : this._renderForm()}
        </div>
        ${this._forkModalOpen ? this._renderForkModal() : nothing}
      </div>
    `;
  }

  // _renderPromptPicker renders one purpose's picker + fork
  // button + preview toggle. Curator picks a prompt from the
  // dropdown, clicks "preview" to see the content read-only,
  // clicks "fork" to open the fork editor. Selection empty ↔
  // backend uses its shipped default.
  _renderPromptPicker(purpose, label) {
    const options = (this._prompts || []).filter(
      (p) => p.purpose === purpose,
    );
    const selectedID =
      purpose === "ner" ? this._nerPromptID : this._composePromptID;
    const previewOpenForThis = this._previewPurpose === purpose;
    return html`
      <div class="prompt-picker">
        <label>${label}</label>
        <select
          .value=${selectedID}
          @change=${(e) => this._onPromptPick(purpose, e.target.value)}
        >
          ${options.length === 0
            ? html`<option value="">(defaults)</option>`
            : nothing}
          ${options.map(
            (p) => html`
              <option value=${p.id} ?selected=${p.id === selectedID}>
                ${p.name}
                ${p.source === "curator" ? "(fork)" : ""}
              </option>
            `,
          )}
        </select>
        <button
          type="button"
          class="prompt-preview-toggle"
          title=${previewOpenForThis && this._promptPreview
            ? "hide preview"
            : "preview prompt content"}
          @click=${() =>
            previewOpenForThis && this._promptPreview
              ? this._closePreview()
              : this._openPreview(purpose, selectedID)}
          ?disabled=${!selectedID}
        >
          ${previewOpenForThis && this._promptPreview ? "hide" : "preview"}
        </button>
        <button
          type="button"
          class="prompt-fork-btn"
          title="fork this prompt into a curator-editable copy"
          @click=${() => this._openForkModal(purpose)}
          ?disabled=${!selectedID}
        >
          fork
        </button>
      </div>
    `;
  }

  _renderPromptPreview() {
    const p = this._promptPreview;
    return html`
      <div class="prompt-preview" role="region"
           aria-label="prompt preview: ${p.name}">
        <div class="prompt-preview-header">
          <span class="prompt-preview-label">
            ${p.name}
            <span class="prompt-preview-source ${p.source}">
              ${p.source}
            </span>
          </span>
          <button
            type="button"
            class="prompt-preview-close"
            @click=${this._closePreview}
            aria-label="close preview"
            title="close preview"
          >
            ✕
          </button>
        </div>
        <pre class="prompt-preview-body">${p.content}</pre>
      </div>
    `;
  }

  _renderForkModal() {
    const src = this._forkSource;
    if (!src) return nothing;
    return html`
      <sfga-prompt-fork-modal
        .forkOfName=${src.forkOfName}
        .forkOfID=${src.forkOfID}
        .purpose=${src.purpose}
        .initialContent=${src.content}
        @fork-cancel=${() => this._closeForkModal()}
        @fork-submit=${async (e) => {
          try {
            await this._submitFork(e.detail.name, e.detail.content);
          } catch (err) {
            // Re-throw as a CustomEvent the child can catch to
            // display in its own error area.
            e.detail.setError(this._errMsg(err));
          }
        }}
      ></sfga-prompt-fork-modal>
    `;
  }

  _renderForm() {
    const nerDefault = this._defaultModelFor(this._nerProvider);
    const treeDefault = this._defaultModelFor(this._treeProvider);
    const nerModels = this._modelListFor(this._nerProvider);
    const treeModels = this._modelListFor(this._treeProvider);
    const nerListID = `models-${this._nerProvider}-ner`;
    const treeListID = `models-${this._treeProvider}-tree`;
    return html`
      <form @submit=${this._onSubmit}>
        <div class="form-row">
          <label>
            <span>NER provider</span>
            <select
              .value=${this._nerProvider}
              @change=${(e) => {
                this._nerProvider = e.target.value;
                this._fetchModelsFor(e.target.value);
              }}
            >
              ${this._providers.map(
                (p) => html`
                  <option
                    value=${p.name}
                    ?selected=${p.name === this._nerProvider}
                  >
                    ${p.name}${p.default_model ? ` — ${p.default_model}` : ""}
                  </option>
                `,
              )}
            </select>
            <input
              type="text"
              list=${nerListID}
              .value=${this._nerModel}
              @input=${(e) => (this._nerModel = e.target.value)}
              placeholder=${nerDefault ? `default: ${nerDefault}` : "override model (optional)"}
              autocomplete="off"
              spellcheck="false"
            />
            ${nerModels.length > 0
              ? html`<datalist id=${nerListID}>
                  ${nerModels.map(
                    (m) => html`<option value=${m.id}>
                      ${m.display_name || m.id}
                    </option>`,
                  )}
                </datalist>`
              : nothing}
            <span class="form-note">
              Extracts per-fact assertions. A cheap model usually
              suffices. ${nerModels.length > 0
                ? html`Start typing to autocomplete from
                    ${nerModels.length} available models.`
                : "Type any model ID the provider supports."}
            </span>
          </label>
          <label>
            <span>Tree provider</span>
            <select
              .value=${this._treeProvider}
              @change=${(e) => {
                this._treeProvider = e.target.value;
                this._fetchModelsFor(e.target.value);
              }}
            >
              ${this._providers.map(
                (p) => html`
                  <option
                    value=${p.name}
                    ?selected=${p.name === this._treeProvider}
                  >
                    ${p.name}${p.default_model ? ` — ${p.default_model}` : ""}
                  </option>
                `,
              )}
            </select>
            <input
              type="text"
              list=${treeListID}
              .value=${this._treeModel}
              @input=${(e) => (this._treeModel = e.target.value)}
              placeholder=${treeDefault ? `default: ${treeDefault}` : "override model (optional)"}
              autocomplete="off"
              spellcheck="false"
            />
            ${treeModels.length > 0
              ? html`<datalist id=${treeListID}>
                  ${treeModels.map(
                    (m) => html`<option value=${m.id}>
                      ${m.display_name || m.id}
                    </option>`,
                  )}
                </datalist>`
              : nothing}
            <span class="form-note">
              Composes the tree from assertions + paper context.
              Benefits from a stronger reasoning model (Sonnet /
              Opus) especially for papers with many species.
            </span>
          </label>
        </div>

        <div class="prompt-pickers">
          ${this._renderPromptPicker("ner", "NER prompt")}
          ${this._renderPromptPicker("compose", "Compose prompt")}
        </div>
        ${this._promptPreview ? this._renderPromptPreview() : nothing}

        <div class="actions">
          <button type="button" class="secondary" @click=${this._close}>
            cancel
          </button>
          <button
            type="submit"
            class="primary"
            ?disabled=${!this._canSubmit()}
          >
            Run
          </button>
        </div>
      </form>
    `;
  }

  _renderRunning() {
    return html`
      <div class="running" role="status" aria-live="polite">
        <div class="spinner">⋯</div>
        <div>Running extraction pipeline…</div>
        <div class="form-note">
          NER pass first, then tree composition. This can take up
          to a minute or two for a full paper — don't close the
          modal.
        </div>
      </div>
    `;
  }

  _renderResult() {
    const r = this._result;
    const ner = r.ner_run || {};
    const tree = r.compose_run || {};
    const nerEnv = this._safeParse(ner.envelope) || {};
    const treeEnv = this._safeParse(tree.envelope) || {};
    const totalCost = (ner.cost_usd || 0) + (tree.cost_usd || 0);
    const failed = r.failed_stage;
    const assertionsN = r.assertions_materialized || 0;
    const materializeErr = r.ner_materialize_error;
    // Success requires both: no stage failure AND assertions
    // actually materialized. Zero assertions with no error means
    // NER produced valid JSON but the assertion set was empty
    // (unusual — often the paper has nothing recognizable). Zero
    // assertions with a materialize error means NER output was
    // malformed (often truncated at max_tokens).
    const partialSuccess =
      !failed && (materializeErr || assertionsN === 0);
    return html`
      <div class="result" role="status">
        <div class="headline">
          ${failed
            ? `⚠ ${failed} step failed`
            : partialSuccess
              ? "⚠ Extraction incomplete"
              : `✓ Extracted ${assertionsN.toLocaleString()} assertion${assertionsN === 1 ? "" : "s"}`}
        </div>

        <div class="step">
          <span class="name">NER</span>
          <span class="meta">
            ${nerEnv.model || ner.source_id || "—"}
            ·
            ${(nerEnv.input_tokens || 0).toLocaleString()}→${(nerEnv.output_tokens || 0).toLocaleString()} tok
          </span>
          <span class="cost">${this._formatUSD(ner.cost_usd)}</span>
        </div>

        ${tree.run_id
          ? html`<div class="step">
              <span class="name">Tree</span>
              <span class="meta">
                ${treeEnv.model || tree.source_id || "—"}
                ·
                ${(treeEnv.input_tokens || 0).toLocaleString()}→${(treeEnv.output_tokens || 0).toLocaleString()} tok
              </span>
              <span class="cost">${this._formatUSD(tree.cost_usd)}</span>
            </div>`
          : nothing}

        <div class="total">
          <span>Total</span>
          <span>${this._formatUSD(totalCost)}</span>
        </div>

        ${materializeErr
          ? html`<div class="error" role="alert">
              NER materialization failed: ${materializeErr}. Common
              cause is max_tokens truncation — the raw output is
              persisted so you can inspect it, but no per-fact
              assertions were staged. Try a model with a larger
              output budget (Sonnet / Opus) if this repeats.
            </div>`
          : nothing}
        ${failed && r.error
          ? html`<div class="error" role="alert">${r.error}</div>`
          : nothing}

        <div class="actions">
          <button
            type="button"
            class="secondary"
            @click=${() => {
              this._result = null;
              this._error = "";
            }}
          >
            run again
          </button>
          <button type="button" class="primary" @click=${this._close}>
            close
          </button>
        </div>
      </div>
    `;
  }

  _defaultModelFor(providerName) {
    const p = this._providers.find((p) => p.name === providerName);
    return p ? p.default_model || "" : "";
  }

  // _fetchModelsFor calls the backend proxy for the provider's
  // /models endpoint. Silent no-op when already cached (fetching
  // costs nothing here but the upstream call goes through the
  // per-server cache so a re-fetch would just hit that anyway).
  // Failure paths: log to console, leave cache empty for that
  // provider — the model-override input still works as a
  // free-text field.
  async _fetchModelsFor(providerName) {
    if (!providerName) return;
    if (this._modelsCache[providerName]) return;
    // Mark as in-flight with an empty array so parallel calls
    // don't stack up.
    this._modelsCache = { ...this._modelsCache, [providerName]: [] };
    try {
      const resp = await api.llm.providers.models(providerName);
      this._modelsCache = {
        ...this._modelsCache,
        [providerName]: resp.items || [],
      };
    } catch (err) {
      // Provider doesn't support models listing (501) or the
      // upstream failed — the datalist stays empty, the input
      // remains a free-text field.
    }
  }

  _modelListFor(providerName) {
    return this._modelsCache[providerName] || [];
  }

  _safeParse(v) {
    if (v == null) return null;
    if (typeof v === "object") return v;
    try {
      return JSON.parse(v);
    } catch {
      return null;
    }
  }
}

customElements.define("sfga-llm-invoke-modal", SfgaLlmInvokeModal);
