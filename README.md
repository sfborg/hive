# Hive

**hive assembles taxonomic databases from taxonomic literature.**

Drop a pile of taxonomy PDFs (or JATS XML, or EPUB) into hive
and, through a mostly-automated pipeline, get out a curated
SFGA / CoLDP-shaped SQLite archive: a browseable taxonomic tree,
synonymy chains, type material, distributions, concept relations,
and a durable record of which sentence within a paper asserted each fact. 
Hive also separates **what has been published in the literature**
from **taxonomic revisions not yet published**, making it easy to discover
where revision work is needed.

The workflow: ingest → digitize → tag → assemble → revise →
publish. Ingest is bulk; digitize and light tag run
automatically; heavy tagging is opt-in per paper (LLM cost); the
assembler is deterministic; revise is where curator judgment
enters; publishing exports one or more layers of the finished
tree.

## What problem hive solves

By tracking published assertions separately from unpublished revisions,
hive makes the curator's editorial work visible and actionable. **The
diff between literature and revisions is exactly where revision work
happens** — where contradictory papers disagree, where a curator decides
a different arrangement, where the synthesis needs a decision call.

This separation also enables **rapid corpus-scale ingestion**. If the
literature view can be assembled by machine from a large PDF pile,
curators spend their time on revisions, not on mechanical assembly.
The workbench becomes a research tool rather than a data-entry tool.

## The four layers

- **Layer 1 — Assertions.** Immutable per-paper facts extracted
  from each source document. One row per assertion, keyed by
  extraction run + reference.
- **Layer 2 — Literature view.** A tree computed from the union
  of Layer 1 assertions by a deterministic assembler with a
  configurable rule set. Recomputable at any time from Layer 1.
- **Layer 3 — Curator overlays.** Curator-authored hypotheses
  that diverge from Layer 2 — synonymy calls, placement changes,
  concept-alignment decisions. Stored separately so a Layer 2
  rebuild doesn't destroy them.
- **Layer 4 — Taxonomist view.** The rendered browse view: Layer
  2 with Layer 3 overlays applied. This is what curators see in
  the taxonomy view unless they explicitly toggle to Layer 2
  alone.

## Requirements

- **Docker + Docker Compose.** The hive stack ships as three
  containers: hive itself (Go binary + WUI),
  [GROBID](https://github.com/grobidOrg/grobid) (PDF →
  structure conversion), and OCR (fallback for scan-quality
  PDFs).
- **A shell.** The CLI is HTTP-based and talks to the running
  server; most commands run against `http://127.0.0.1:2365`
  by default.
- **Optional:** the `just` command runner if you want the
  recipe shortcuts. Everything can also be done by hand.

## Quickstart

### Start a fresh project

The docker stack takes an archive filename that lives under
`data/`. Archives are content-addressed SQLite files; the docker
CMD creates them on first use.

```bash
# stop any running stack
just docker-down

# start against a new archive; creates data/algae.db if missing
just docker-up algae.db

# WUI: http://localhost:2365
```

For CLI use, keep a shell open — `hive ingest` etc. hit the
running server over HTTP.

### Ingest a pile of papers

`hive ingest` uploads files to the running server, extracts DOI
+ basic metadata, and dedupes by DOI so multiple format
attachments (PDF + JATS XML + EPUB) for the same paper land as
one reference row with multiple sidecar attachments.

```bash
# preview what would get uploaded, grouped how
hive ingest --dry-run ~/papers/

# do it: takes a directory (top-level) or individual files
hive ingest ~/papers/

# recurse into subdirectories
hive ingest --recurse ~/papers/

# with a verified license (applied to every reference in the
# batch — split by license if your pile is mixed)
hive ingest --license CC-BY --license-source "https://europepmc.org/…" \
  ~/papers/
```

Filename grouping: hive groups files by basename with the
extension and any publisher `-vN` versioning suffix stripped.

**What happens server-side, automatically, per PDF:**

- **Digitize** — [GROBID](https://github.com/grobidOrg/grobid)
  converts PDF → TEI → JATS. Idempotent by sha
  (re-ingesting an already-processed PDF skips GROBID).
- **Light tag** — [GNfinder](https://github.com/gnames/gnfinder)
  scans the extracted text for scientific names and marks them up.

These two steps are free (no LLM cost). They run on every
upload without prompting.

### Extract taxonomic assertions

This is the LLM pipeline: NER extracts atomic assertions from
each paper; compose organizes them into a hierarchical tree. It
is opt-in and **incurs API costs** (roughly $1/paper on Sonnet,
cheaper on Haiku).

> [!WARNING]
> Hive does not cap spending. Set spending limits with your LLM
> provider before running extraction, particularly across many
> papers.

From the WUI, open a reference's Assertions tab and click the
sparkles icon. From the CLI:

```bash
# preview the references, provider and model; no LLM calls
hive compose --all --ner --dry-run

# NER (where missing) then compose, for specific references
hive compose --ner <ref-id> <ref-id>

# every reference in the archive; confirms first (--yes skips)
hive compose --all --ner
```

`--all` includes references that already have results, so
re-running it pays for those again. `hive ner` runs the NER
step on its own.

### Assemble the literature view

Once at least a couple of papers have compose runs, build
Layer 2:

```bash
hive reassemble data/algae.db
```

This runs the deterministic assembler across every compose run,
producing:

- Matched-existing taxa
- Newly-created taxa
- Synonyms decided by most-recent-paper-wins
- Bracketed species where the paper skipped a genus
  (`[Aus] bus L., 1970` convention for provisionally-accepted
  species without a resolved genus)
- Concept relations from sensu qualifiers
- Curator-actionable flags for unresolved ambiguity

Reassemble is idempotent — safe to run repeatedly. Curator
overlays (Layer 3) survive as long as their anchor taxon still
exists.

### Revise

Open the WUI, click **Assimilate** in the sidebar. Four-color
node coding tells you where to spend time:

- **Green** — machine-assembled cleanly. No action needed.
- **Amber** — machine assembled it but a flag warrants review.
  Common: contradictory synonymy across papers, missing sensu
  reference.
- **Red** — machine couldn't decide. Missing anchor, ambiguous
  auto-match. Needs curator authorship.
- **Blue** — you have a Layer 3 taxonomic revision overlay
  active on this node.

Click any row to open the preview panel. Add an overlay: pick
type (synonymy override, placement override, concept override),
edit the claim JSON, submit. Row goes blue.

Reassemble can be re-run at any time — the assembler is a pure
function of `(assertions, rules)`. Layer 3 overlays reference
Layer 2 nodes by ID, so most survive rebuilds; edge cases
(taxon demoted to synonym across papers, taxon reparented) can
orphan an overlay. Orphaned overlays surface as flags in a
dedicated Revise view for curator resolution — they're not a
data-loss condition, they're a curator-action-required state.

## Publishing

Two modes (choose per export):

- **Taxonomist view (Layer 4)** — literature view with overlaid
  taxonomic revisions that are still pending publication.
- **Literature view (Layer 2)** — the current state of the
  literature that has been imported into the project.

Export commands live under `hive` subcommands (see
`hive --help`).

## Cheat sheet

Enough to jog memory when you've done this before:

```bash
# fresh project
just docker-down
just docker-up algae.db

# preview groups, then ingest
hive ingest --dry-run ~/papers/algae/
hive ingest --license CC-BY ~/papers/algae/

# extract (or per paper from the WUI's Assertions tab)
hive compose --all --ner

# build the literature view
hive reassemble data/algae.db

# post-reassemble browse
open http://localhost:2365   # Assimilate tab
```

The loop: ingest → extract → reassemble → revise → author
overlays → repeat as more papers arrive.

## Architecture notes

- **Storage:** SQLite archives conforming to the SFGA schema (a
  superset of CoLDP with hive-managed metadata tables prefixed
  `hive__`).
- **Language:** Go (server + CLI + TUI). WUI is Lit + vanilla
  Web Components.
- **HTTP API:** documented at `/docs` on a running server (OpenAPI
  spec at `/api/openapi.yaml`).
- **Sidecar library:** source documents (PDF, JATS, EPUB) live
  next to the archive at `<archive>.library/`, content-addressed
  by sha512.
- **Deterministic assembler:** pure function of Layer 1
  assertions + rule set. Rules live in `hive__config_rules`
  (per-archive, editable).
- **Layered concept model:** CoLDP `taxon_concept_relation` with
  RCC5-style relations (EQUALS / INCLUDES / INCLUDED_IN /
  OVERLAPS / EXCLUDES) driven by parser-detected sensu markers.

## License

MIT — see [LICENSE](LICENSE).

hive bundles third-party components, and their licenses ship with the
binary: Lit (BSD-3-Clause), pdf.js (Apache-2.0) and RapiDoc (MIT) under
`internal/wui/dist/vendor/`, plus the JATS and W3C schemas under
`schema/vendor/`. Provenance, checksums and notices for each are in the
README in those directories.

