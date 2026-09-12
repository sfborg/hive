// rcc5-venn-picker.js — a five-pane Venn-diagram picker for
// selecting the CoLDP taxon_concept_rel_type relation between two
// taxonomic concepts. Curator picks by clicking a visual
// arrangement; the component emits the chosen CoLDP vocab value
// (EQUALS / INCLUDES / INCLUDED_IN / OVERLAPS / EXCLUDES) or "" for
// the "unclear / don't record" default.
//
// Deliberately format-agnostic and free-standing. Consumers embed
// the picker inside their own assimilate / merge / duplicate-
// resolution modal and supply the surrounding context (which two
// concepts are being compared, classification ancestors, source
// citations). This component does NOT render that context — it
// only handles the relation-pick interaction.
//
// See TAXONOMIC_CONCEPTS.md § Interface designs for the design
// rationale, and pkg/conceptimport/ for the storage vocabulary the
// emitted values map to.
//
// Events emitted:
//   - relation-change (bubbles, composed) — detail: { relTypeID }
//     Fires on every user pick. relTypeID is one of the CoLDP
//     vocab values above, or "" for unclear.
//
// Properties:
//   - value: currently-selected relTypeID (string). Two-way via
//     the relation-change event; parent can also set it to
//     pre-highlight a suggested relation.
//   - leftLabel, rightLabel: short labels for the two concepts
//     under comparison (e.g., "This paper" and "Existing DB
//     taxon"). Rendered under the picker.
//   - showOperators: when true, appends the RCC5 operator symbol
//     (==, <, >, ><, |) to each pane label for curators who
//     prefer the formal notation. Default false.
//
// Design-system compliance: SVG shapes use existing --fg,
// --bg, --border, --dim tokens plus --sev-info for the
// selected highlight. No new color values invented. Spacing via
// --sp-*, typography via --fs-*.

import { LitElement, html, css, nothing } from "../vendor/lit-3.x.x.min.js";

const RELATIONS = [
  {
    id: "EQUALS",
    label: "Same concept",
    operator: "==",
    description: "The two concepts refer to the same set of organisms.",
  },
  {
    id: "INCLUDED_IN",
    label: "Narrower concept",
    operator: "<",
    description:
      "This paper's concept is entirely inside the other concept, which is broader.",
  },
  {
    id: "INCLUDES",
    label: "Broader concept",
    operator: ">",
    description:
      "This paper's concept fully contains the other concept, which is narrower.",
  },
  {
    id: "OVERLAPS",
    label: "Overlapping concepts",
    operator: "><",
    description:
      "The two concepts share some organisms; each also includes some the other doesn't.",
  },
  {
    id: "EXCLUDES",
    label: "Different taxa",
    operator: "|",
    description:
      "The two concepts share no organisms — effectively different taxa that happen to share a name.",
  },
  {
    id: "",
    label: "Unclear / don't record",
    operator: "?",
    description:
      "The source doesn't provide enough context to pick a relation. No taxon_concept_relation row will be written.",
  },
];

function vennSVG(id) {
  switch (id) {
    case "EQUALS":
      return html`
        <circle class="a" cx="50" cy="30" r="22"></circle>
        <circle class="b" cx="50" cy="30" r="22"></circle>
      `;
    case "INCLUDED_IN":
      return html`
        <circle class="b" cx="50" cy="30" r="26"></circle>
        <circle class="a" cx="42" cy="30" r="12"></circle>
      `;
    case "INCLUDES":
      return html`
        <circle class="a" cx="50" cy="30" r="26"></circle>
        <circle class="b" cx="58" cy="30" r="12"></circle>
      `;
    case "OVERLAPS":
      return html`
        <circle class="a" cx="35" cy="30" r="22"></circle>
        <circle class="b" cx="65" cy="30" r="22"></circle>
      `;
    case "EXCLUDES":
      return html`
        <circle class="a" cx="28" cy="30" r="18"></circle>
        <circle class="b" cx="72" cy="30" r="18"></circle>
      `;
    default:
      return html`
        <circle
          class="unclear"
          cx="50"
          cy="30"
          r="22"
          stroke-dasharray="4 3"
        ></circle>
        <text
          x="50"
          y="35"
          text-anchor="middle"
          font-size="16"
          class="unclear-glyph"
        >
          ?
        </text>
      `;
  }
}

export class Rcc5VennPicker extends LitElement {
  static properties = {
    value: { type: String, reflect: true },
    leftLabel: { type: String, attribute: "left-label" },
    rightLabel: { type: String, attribute: "right-label" },
    showOperators: { type: Boolean, attribute: "show-operators" },
  };

  constructor() {
    super();
    this.value = "";
    this.leftLabel = "";
    this.rightLabel = "";
    this.showOperators = false;
  }

  static styles = css`
    :host {
      display: block;
      font-size: var(--fs-sm);
      color: var(--fg);
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
      gap: var(--sp-2);
    }
    .pane {
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      padding: var(--sp-2);
      cursor: pointer;
      display: grid;
      gap: var(--sp-1);
      text-align: center;
      transition: border-color 120ms ease, background 120ms ease;
    }
    .pane:hover {
      border-color: var(--dim);
    }
    .pane:focus-visible {
      outline: 2px solid var(--sev-info);
      outline-offset: 2px;
    }
    .pane.selected {
      border-color: var(--sev-info);
      background: var(--sev-info-bg);
    }
    .pane svg {
      width: 100%;
      height: auto;
      max-height: 64px;
    }
    .pane svg circle.a {
      fill: color-mix(in oklab, var(--sev-info) 30%, transparent);
      stroke: var(--sev-info);
      stroke-width: 1.5;
    }
    .pane svg circle.b {
      fill: color-mix(in oklab, var(--fg) 12%, transparent);
      stroke: var(--fg);
      stroke-width: 1.5;
    }
    .pane svg circle.unclear {
      fill: transparent;
      stroke: var(--dim);
      stroke-width: 1.5;
    }
    .pane svg text.unclear-glyph {
      fill: var(--dim);
      font-family: var(--font-mono);
    }
    .pane .label {
      font-weight: 600;
      font-size: var(--fs-sm);
    }
    .pane .operator {
      font-family: var(--font-mono);
      color: var(--dim);
      font-size: var(--fs-xs);
    }
    .legend {
      margin-top: var(--sp-3);
      padding-top: var(--sp-2);
      border-top: 1px solid var(--border);
      display: flex;
      justify-content: space-between;
      font-size: var(--fs-xs);
      color: var(--dim);
    }
    .legend .a-swatch,
    .legend .b-swatch {
      display: inline-block;
      width: 10px;
      height: 10px;
      border-radius: 50%;
      vertical-align: middle;
      margin-right: 4px;
    }
    .legend .a-swatch {
      background: color-mix(in oklab, var(--sev-info) 30%, transparent);
      border: 1px solid var(--sev-info);
    }
    .legend .b-swatch {
      background: color-mix(in oklab, var(--fg) 12%, transparent);
      border: 1px solid var(--fg);
    }
    .description {
      margin-top: var(--sp-2);
      color: var(--dim);
      font-style: italic;
      min-height: 2em;
    }
  `;

  _onPick(relTypeID) {
    if (this.value === relTypeID) return;
    this.value = relTypeID;
    this.dispatchEvent(
      new CustomEvent("relation-change", {
        detail: { relTypeID },
        bubbles: true,
        composed: true,
      }),
    );
  }

  _onKey(event, relTypeID) {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      this._onPick(relTypeID);
    }
  }

  render() {
    const current = RELATIONS.find((r) => r.id === this.value) || RELATIONS[5];
    return html`
      <div
        class="grid"
        role="radiogroup"
        aria-label="Choose the concept relation between the two taxonomic concepts"
      >
        ${RELATIONS.map(
          (rel) => html`
            <div
              class="pane ${this.value === rel.id ? "selected" : ""}"
              role="radio"
              tabindex="0"
              aria-checked=${this.value === rel.id ? "true" : "false"}
              @click=${() => this._onPick(rel.id)}
              @keydown=${(e) => this._onKey(e, rel.id)}
            >
              <svg
                viewBox="0 0 100 60"
                xmlns="http://www.w3.org/2000/svg"
                aria-hidden="true"
              >
                ${vennSVG(rel.id)}
              </svg>
              <div class="label">${rel.label}</div>
              ${this.showOperators
                ? html`<div class="operator">${rel.operator}</div>`
                : nothing}
            </div>
          `,
        )}
      </div>
      <div class="description" role="status" aria-live="polite">
        ${current.description}
      </div>
      ${this.leftLabel || this.rightLabel
        ? html`
            <div class="legend">
              <span
                ><span class="a-swatch"></span>${this.leftLabel ||
                "This concept"}</span
              >
              <span
                ><span class="b-swatch"></span>${this.rightLabel ||
                "Other concept"}</span
              >
            </div>
          `
        : nothing}
    `;
  }
}

customElements.define("rcc5-venn-picker", Rcc5VennPicker);
