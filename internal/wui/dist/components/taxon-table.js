/**
 * <sfga-taxon-table>
 *
 * The Search screen: every name usage in the archive (accepted taxa and
 * synonyms) in a filterable, sortable table with optional columns from
 * the classification, the supplemental tables and validation issues.
 * Filters: name, usage, rank, a taxon to search within, data a row
 * has or lacks, and validation issues. Double-click or Enter on a row
 * opens its taxon.
 *
 * Properties: icon — the app's icon renderer, passed to the table.
 */

import { LitElement, html, css, nothing } from "/vendor/lit-3.x.x.min.js";
import { api } from "/lib/api.js";
import { getSetting, setSetting } from "/lib/settings.js";
import "/components/data-table.js";
import "/components/export-menu.js";

const SEVERITIES = ["error", "warn", "info", "debug"];
const ABOVE_GENUS = new Set([
  "DOMAIN", "SUPERKINGDOM", "KINGDOM", "SUBKINGDOM", "INFRAKINGDOM", "SUPERPHYLUM", "PHYLUM",
  "SUBPHYLUM", "DIVISION", "SUBDIVISION", "SUPERCLASS", "CLASS", "SUBCLASS", "INFRACLASS",
  "SUPERORDER", "ORDER", "SUBORDER", "INFRAORDER", "SUPERFAMILY", "FAMILY", "SUBFAMILY",
  "TRIBE", "SUBTRIBE", "UNRANKED", "OTHER", "REALM",
]);

const dash = html`<span class="dim">—</span>`;
const list = (items) => (items && items.length ? items.join(", ") : dash);
const count = (n) => (n ? n.toLocaleString() : dash);

// nameCell italicizes names at genus rank and below, as printed in
// taxonomic works.
function nameCell(r) {
  const italic = r.rank ? !ABOVE_GENUS.has(r.rank.toUpperCase()) : r.name.includes(" ");
  return italic ? html`<i>${r.name}</i>` : r.name;
}

function issuesCell(r) {
  const by = r.issues || {};
  const parts = SEVERITIES.filter((s) => by[s]);
  if (!parts.length) return dash;
  return html`<span class="issue-counts">${parts.map(
    (s) => html`<span class=${"sev sev-" + s} title=${`${by[s]} ${s}`}>${by[s]} ${s}</span>`,
  )}</span>`;
}

const COLUMNS = [
  { id: "name", label: "Name", group: "name", sort: "name", shown: true, cell: nameCell },
  { id: "authorship", label: "Authorship", group: "name", sort: "authorship", shown: true, cell: (r) => r.authorship || "" },
  { id: "rank", label: "Rank", group: "name", sort: "rank", shown: true, cell: (r) => (r.rank || "").toLowerCase() },
  {
    id: "usage", label: "Usage", group: "name", sort: "usage", shown: true,
    title: "Accepted taxon or synonym",
    cell: (r) => (r.usage === "synonym" ? html`<span class="dim">synonym</span>` : "accepted"),
  },
  {
    id: "related", label: "Parent / accepted", group: "name", shown: true,
    title: "The parent of an accepted taxon; the accepted name of a synonym",
    cell: (r) => r.related || "",
  },
  { id: "status", label: "Status", group: "name", sort: "status", cell: (r) => (r.status || "").toLowerCase().replaceAll("_", " ") },
  { id: "code", label: "Code", group: "name", sort: "code", cell: (r) => (r.code || "").toLowerCase() },
  { id: "extinct", label: "Extinct", group: "name", cell: (r) => (r.extinct ? "†" : "") },
  { id: "modified", label: "Modified", group: "name", sort: "modified", cell: (r) => (r.modified || "").slice(0, 10) },
  { classification: true },
  {
    id: "vernaculars", label: "Vernacular names", group: "supplemental", sort: "vernaculars", shown: true,
    title: "Vernacular names of the taxon (accepted rows); sorts by how many",
    cell: (r) => list(r.vernaculars),
  },
  {
    id: "distribution", label: "Distribution", group: "supplemental", sort: "distribution",
    title: "Distribution areas of the taxon (accepted rows); sorts by how many",
    cell: (r) => list(r.distribution),
  },
  {
    id: "interactions", label: "Interactions", group: "supplemental", sort: "interactions",
    title: "Species interactions of the taxon (accepted rows); sorts by how many",
    cell: (r) => list(r.interactions),
  },
  {
    id: "type_material", label: "Type material", group: "supplemental", sort: "type_material",
    title: "Type material of the name; sorts by how many",
    cell: (r) => list(r.type_material),
  },
  {
    id: "media", label: "Media", group: "supplemental", sort: "media",
    title: "Media of the taxon (accepted rows); sorts by how many",
    cell: (r) => list(r.media),
  },
  {
    id: "properties", label: "Properties", group: "supplemental", sort: "properties",
    title: "Taxon properties (accepted rows); sorts by how many",
    cell: (r) => list(r.properties),
  },
  {
    id: "estimates", label: "Species estimates", group: "supplemental", sort: "estimates",
    title: "Species estimates of the taxon (accepted rows); sorts by how many",
    cell: (r) => list(r.estimates),
  },
  {
    id: "synonyms", label: "Synonyms", group: "supplemental", sort: "synonyms", numeric: true,
    title: "Number of synonyms of the taxon",
    cell: (r) => count(r.synonyms),
  },
  {
    id: "name_relations", label: "Name relations", group: "supplemental", sort: "name_relations", numeric: true,
    title: "Number of name relations (basionym, homonym, …) involving the name",
    cell: (r) => count(r.name_relations),
  },
  {
    id: "issues", label: "Issues", group: "issues", sort: "issues", shown: true,
    title: "Open validation issues on the name, taxon or synonym, by severity; sorts most serious first",
    cell: issuesCell,
  },
  {
    id: "issue_rules", label: "Issue types", group: "issues", sort: "issue_rules",
    title: "The validation rules with open issues; sorts by rule name",
    cell: (r) => list(r.issue_rules),
  },
];

// FALLBACK_RANKS are the classification columns offered when the
// archive's ranks cannot be listed.
const FALLBACK_RANKS = ["kingdom", "phylum", "class", "order", "family", "genus", "subgenus"].map((id) => ({
  id,
  name: id,
  linnean: true,
}));

// buildColumns places a classification column for each rank where
// COLUMNS has its placeholder: the Linnaean ranks under
// "Classification", the common ranks between them under "Intermediate
// ranks", every other rank under "All ranks".
function buildColumns(ranks) {
  const rankColumns = ranks.map((rk) => {
    const name = (rk.name || rk.id).replaceAll("_", " ");
    const label = name[0].toUpperCase() + name.slice(1);
    return {
      id: rk.id,
      label,
      group: rk.linnean ? "classification" : rk.intermediate ? "intermediate" : "ranks",
      sort: rk.id,
      shown: rk.id === "family",
      reload: true,
      title: `The accepted taxon's ${name}, from its parent chain`,
      hint: rk.taxa ? `${rk.taxa.toLocaleString()} taxa` : "none in this archive",
      dim: !rk.taxa,
      cell: (r) => (r.classification && r.classification[rk.id]) || "",
    };
  });
  return COLUMNS.flatMap((c) => (c.classification ? rankColumns : [c]));
}

// DATA lists the kinds of supplemental data the Data filter can require
// or exclude.
const DATA = [
  ["vernaculars", "vernacular names"],
  ["distribution", "distribution"],
  ["interactions", "interactions"],
  ["type_material", "type material"],
  ["synonyms", "synonyms"],
  ["name_relations", "name relations"],
  ["media", "media"],
  ["properties", "properties"],
  ["estimates", "species estimates"],
];

// listSource returns a combobox source over fixed options: all of them
// on focus, narrowed as the curator types, with options whose name (or
// a word of it) starts with the text first.
function listSource(options) {
  return (q) => {
    const needle = (q || "").trim().toLowerCase();
    const all = typeof options === "function" ? options() : options;
    if (!needle) return all;
    const score = (o) => {
      const name = o.name.toLowerCase();
      if (name.startsWith(needle)) return 0;
      if (name.split(/\s+/).some((w) => w.startsWith(needle))) return 1;
      return name.includes(needle) ? 2 : 3;
    };
    return all
      .map((o) => [score(o), o])
      .filter(([sc]) => sc < 3)
      .sort((a, b) => a[0] - b[0])
      .map(([, o]) => o);
  };
}

const USAGES = [
  { id: "accepted", name: "Accepted" },
  { id: "synonym", name: "Synonyms" },
];

const DATA_OPTIONS = [
  ...DATA.map(([k, l]) => ({ id: "has:" + k, name: "Has " + l })),
  ...DATA.map(([k, l]) => ({ id: "missing:" + k, name: "Missing " + l })),
];

// withinSource searches the archive's taxa for the Within picker.
async function withinSource(q) {
  if (!q || q.length < 2) return [];
  try {
    const page = await api.taxon.search({ q, limit: 20 });
    return (page.items || []).map((h) => ({ id: h.id, name: h.label?.text || h.name }));
  } catch (_) {
    return [];
  }
}

const GROUPS = [
  { id: "name", label: "Name" },
  { id: "classification", label: "Classification" },
  { id: "intermediate", label: "Intermediate ranks" },
  { id: "ranks", label: "All ranks", collapsible: true, filter: "Filter ranks…" },
  { id: "supplemental", label: "Supplemental data" },
  { id: "issues", label: "Issues" },
];

const EXPORT_FORMATS = [
  { id: "csv", label: "CSV", hint: "For spreadsheets" },
  { id: "sqlite", label: "SQLite", hint: "One flat table, as shown" },
  {
    id: "sfga",
    label: "SFGA archive",
    hint: "The matching records with their related tables; taxon_table_view shows them as here",
  },
];

const EXPORT_OPTIONS = [
  {
    id: "linnaean",
    label: "Include all Linnaean ranks in CSV and SQLite (recommended if you plan to edit the classification and re-import)",
    title: "Add kingdom, phylum, class, order, family, genus and subgenus columns to the ones shown",
    checked: false,
    formats: ["csv", "sqlite"],
  },
  {
    id: "validation",
    label: "Include validation results in the SFGA archive",
    title: "Copy the validation results on the exported records into the archive",
    checked: true,
    formats: ["sfga"],
  },
];

class SfgaTaxonTable extends LitElement {
  static properties = {
    icon: { attribute: false },
    _q: { state: true },
    _usage: { state: true },
    _rank: { state: true },
    _issue: { state: true },
    _within: { state: true },
    _data: { state: true },
    _rules: { state: true },
    _total: { state: true },
    _columns: { state: true },
  };

  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      height: 100%;
      min-height: 0;
      padding: var(--sp-3) var(--sp-4) 0;
      box-sizing: border-box;
    }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: var(--sp-2);
      align-items: center;
      margin-bottom: var(--sp-2);
      font-size: var(--fs-sm);
    }
    h2 {
      margin: 0 var(--sp-2) 0 0;
      font-size: var(--fs-lg);
    }
    .total {
      color: var(--dim);
    }
    input[type="search"] {
      flex: 1 1 18rem;
      max-width: 32rem;
      font: inherit;
      padding: var(--sp-1) var(--sp-2);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
    }
    select {
      font: inherit;
      padding: var(--sp-1);
      background: var(--bg);
      color: var(--fg);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
    }
    label {
      color: var(--dim);
      font-size: var(--fs-xs);
    }
    sfga-combobox.within,
    sfga-combobox.pick-issue {
      width: 14rem;
    }
    sfga-combobox.pick-usage {
      width: 8rem;
    }
    sfga-combobox.pick-rank,
    sfga-combobox.pick-data {
      width: 11rem;
    }
    sfga-data-table {
      flex: 1 1 auto;
      min-height: 0;
    }
  `;

  constructor() {
    super();
    const f = getSetting("taxa.table.filters") || {};
    this._q = "";
    this._usage = f.usage || "";
    this._rank = f.rank || "";
    this._issue = f.issue || "";
    this._within = f.within || null; // {id, name}
    this._data = f.data || ""; // "has:<kind>" | "missing:<kind>"
    this._rules = [];
    this._total = null;
    this.icon = () => nothing;
    this._usageSource = listSource(USAGES);
    this._dataSource = listSource(DATA_OPTIONS);
    this._rankSource = listSource(() =>
      (api.vocab.get("rank") || []).filter((r) => r.id).map((r) => ({ id: r.id, name: r.name || r.id })),
    );
    this._issueSource = listSource(() => [
      { id: "any", name: "With issues" },
      ...SEVERITIES.map((sv) => ({ id: sv, name: "With " + sv })),
      ...this._rules.map((r) => ({ id: "rule:" + r.id, name: `${r.name} (${r.count})` })),
    ]);
    this._columns = null;
    this._fetch = (page) =>
      api.taxon.table({
        ...this._filterParams(),
        lineage: this._lineage(page.columns),
        sort: page.sort,
        limit: page.limit,
        offset: page.offset,
      });
  }

  connectedCallback() {
    super.connectedCallback();
    // The rank columns come from the archive's vocabulary; the table
    // waits for them so its first page fills the saved columns.
    api.taxon
      .tableRanks()
      .then((res) => (this._columns = buildColumns(res.items?.length ? res.items : FALLBACK_RANKS)))
      .catch(() => (this._columns = buildColumns(FALLBACK_RANKS)));
    api.taxon
      .tableIssueRules()
      .then((res) => (this._rules = res.items || []))
      .catch(() => {});
  }

  _changed(field, value) {
    this[field] = value;
    if (field !== "_q") {
      setSetting("taxa.table.filters", {
        usage: this._usage,
        rank: this._rank,
        issue: this._issue,
        within: this._within,
        data: this._data,
      });
    }
    clearTimeout(this._debounce);
    this._debounce = setTimeout(() => this.renderRoot.querySelector("sfga-data-table")?.reload(), field === "_q" ? 250 : 0);
  }

  // _picker renders a type-to-filter combobox over fixed options for
  // one filter field; clearing it means "any".
  _picker(field, label, placeholder, source, value, nameOf) {
    return html`<label for=${field}>${label}</label>
      <sfga-combobox
        id=${field}
        class=${"pick-" + field}
        min-search-chars="0"
        placeholder=${placeholder}
        .source=${source}
        .value=${value || ""}
        .valueName=${value ? nameOf(value) || value : ""}
        @pick=${(e) => this._changed("_" + field, e.detail.id || "")}
      ></sfga-combobox>`;
  }

  // _lineage lists the classification ranks among the shown columns.
  _lineage(shown) {
    const ranks = new Set((this._columns || []).filter((c) => c.reload).map((c) => c.id));
    return (shown || []).filter((id) => ranks.has(id)).join(",");
  }

  _rankName(id) {
    return (api.vocab.get("rank") || []).find((r) => r.id === id)?.name || id.toLowerCase();
  }

  _issueName(id) {
    if (id === "any") return "With issues";
    if (SEVERITIES.includes(id)) return "With " + id;
    const rule = this._rules.find((r) => "rule:" + r.id === id);
    return rule ? rule.name : id;
  }

  // _filterParams are the API parameters for the current filters.
  _filterParams() {
    return {
      q: this._q.trim(),
      usage: this._usage,
      rank: this._rank,
      issue: this._issue,
      within: this._within?.id || "",
      has: this._data.startsWith("has:") ? this._data.slice(4) : "",
      missing: this._data.startsWith("missing:") ? this._data.slice(8) : "",
    };
  }

  // _exportURL downloads the table with the current filters, sort and
  // shown columns.
  _exportURL(format, opts = {}) {
    const table = this.renderRoot.querySelector("sfga-data-table");
    const params = { ...this._filterParams(), ...(table?.exportParams() || {}), format };
    if (opts.validation === false) params.validation = "0";
    if (opts.linnaean) params.linnaean = "1";
    const qs = new URLSearchParams(Object.entries(params).filter(([, v]) => v));
    return "/api/taxon/table/export?" + qs;
  }

  _open(row) {
    location.hash = "#/taxon/" + encodeURIComponent(row.taxon_id);
  }

  render() {
    return html`
      <div class="toolbar" role="search">
        <h2>Search</h2>
        <input
          type="search"
          placeholder="Name contains…"
          aria-label="Name contains"
          .value=${this._q}
          @input=${(e) => this._changed("_q", e.target.value)}
        />
        ${this._picker("usage", "Usage", "Any", this._usageSource, this._usage, (id) => USAGES.find((o) => o.id === id)?.name)}
        ${this._picker("rank", "Rank", "Any", this._rankSource, this._rank, (id) => this._rankName(id))}
        <label for="within">Within</label>
        <sfga-combobox
          id="within"
          class="within"
          min-search-chars="2"
          placeholder="Any taxon"
          .source=${withinSource}
          .value=${this._within?.id || ""}
          .valueName=${this._within?.name || ""}
          @pick=${(e) => this._changed("_within", e.detail.id ? { id: e.detail.id, name: e.detail.name } : null)}
        ></sfga-combobox>
        ${this._picker("data", "Data", "Any", this._dataSource, this._data, (id) => DATA_OPTIONS.find((o) => o.id === id)?.name)}
        ${this._picker("issue", "Issues", "Any or none", this._issueSource, this._issue, (id) => this._issueName(id))}
        <span class="total" role="status">${this._total === null ? "" : `${this._total.toLocaleString()} names`}</span>
        <sfga-export-menu
          .formats=${EXPORT_FORMATS}
          .options=${EXPORT_OPTIONS}
          .href=${(format, opts) => this._exportURL(format, opts)}
        ></sfga-export-menu>
      </div>
      ${this._columns
        ? this._renderTable()
        : html`<p class="total" role="status">Loading…</p>`}
    `;
  }

  _renderTable() {
    return html`
      <sfga-data-table
        .columns=${this._columns}
        .groups=${GROUPS}
        .settingsKey=${"taxa.table"}
        .fetchPage=${this._fetch}
        .rowKey=${(r) => `${r.usage}:${r.taxon_id}:${r.name_id}:${r.synonym_id || ""}`}
        .icon=${this.icon}
        .defaultSort=${[{ column: "name", desc: false }]}
        @total-changed=${(e) => (this._total = e.detail.total)}
        @row-activate=${(e) => this._open(e.detail.row)}
      ></sfga-data-table>
    `;
  }
}

customElements.define("sfga-taxon-table", SfgaTaxonTable);
