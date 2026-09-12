# Assimilate-demo fixture

A fake JATS monograph + companion SQL seed for prototyping the
concept-alignment (RCC5) picker, duplicate-detection modal, and
assimilate flow in hive without depending on a real published
paper. Also serves as an end-to-end regression fixture once the
UI stabilizes.

## Everything here is fabricated

The subject is **Order Tribbulida** — an entirely fictional insect-
adjacent order of "tribbles," rapidly-reproducing spherical
organisms drawn from Star Trek. **No taxon, author, institution,
locality, date, or catalogue number in this fixture refers to a
real biological entity or a real person.** This is deliberate: a
clearly-fictional subject removes any risk that a scraper or
future reader mistakes the fixture for a genuine biodiversity
record.

Per the source-content policy in `CLAUDE.md § Source-document
content policy`:

- Every scientific name is invented (verify against GNverifier
  before committing to catch unlucky collisions with real names).
- Every author is invented; every year is invented; every
  institution code (e.g. `MFT`, `USTC`, `KHF`) is invented.
- Every locality is invented (planet names, not Earth
  coordinates).
- Every catalogue number, holotype designation, and collector
  name is invented.

## What this fixture is testing

The mini monograph is designed to exercise every UI branch of
the concept-alignment and duplicate-detection flow described in
[`TAXONOMIC_CONCEPTS.md`](../../TAXONOMIC_CONCEPTS.md) and the
Concept-Alignment subsection of
[`ANNOTATION_ASSEMBLY.md`](../../ANNOTATION_ASSEMBLY.md). Nine
scenarios are covered:

| # | Scenario                          | Taxa involved                                              | Expected UI behavior |
|---|-----------------------------------|------------------------------------------------------------|----------------------|
| 1 | Baseline new species              | *Tribbulus novellus* sp. nov., *Iotageminorius pallidus* sp. nov., etc. | Just insert; no picker fires. |
| 2 | New combination (homotypic)       | *Tribbulus glomivorans* (Coombes, 1972) comb. nov. — moved from *Glommivorus* | Basionym relation auto-detected; RCC5 auto-set to `==`; no picker. |
| 3 | Heterotypic `==` synonymy         | *Tribbulus multiplicans* Whittaker, 1969 with new junior synonym *Tribbulus fecundissimus* Ericksmith, 1974 | Picker fires; curator confirms `==` (same concept). |
| 4 | Heterotypic `<` synonymy          | *Iotageminorius ferrugineus* Marbleston, 1968 s. str. — treatment narrows the existing DB concept | Picker fires; pre-hint `<` (paper's concept is narrower). |
| 5 | `sensu lato` marker               | *Tribbulus tribbulus* Marbleston, 1967 s. l. — treatment uses broader concept | Deterministic RCC5 hint `>` in picker (no ambiguity warning). |
| 6 | `sensu <author>` (ambiguity)      | *Deepspacius rainbowicus* sensu Hallitonn, 2003            | Picker fires with ambiguity warning ("name author or concept author"). |
| 7 | Ambiguous synonym                 | *Tribbulus dubius* Ericksmith, 1974 — synonymized once under *T. tribbulus* and once under *T. multiplicans* in different treatments | Auto-flag `col__status_id = 'ambiguous synonym'`; surfaces as computed WIP in Revise view. |
| 8 | `auct. non <author>` misapplication | *Xenopsyllotribbulus parasiticus* auct. non Ericksmith, 1974 | Picker fires with RCC5 `\|` (disjoint) from Ericksmith's concept. |
| 9 | Homonym / duplicate case          | *Grexinia flava* sp. nov. described twice in the same paper for two different species | Warn-with-override; curator can force-create the intentional duplicate. |

Plus 3-4 baseline "boring" new species treatments padding the
monograph to ~14 total treatments (the paper's contribution) on
top of 5 existing DB entries (the tree the paper is being
assimilated *into*).

## Files

- **`mini_monograph.jats.xml`** — the fake paper in JATS + TaxPub
  format matching hive's ingest expectations (namespaces `tp:` for
  TaxPub inline chips, `gn:` for GlobalNames provenance, `hive:`
  for hive-specific attributes). Ingest this through hive's normal
  reference-manager upload path.
- **`existing_tree.sql`** — SQL seed for the existing DB state the
  paper is being assimilated into (5 accepted taxa across the
  fictional family). Executed against a fresh archive before the
  paper is uploaded.
- **`README.md`** — this file.

## Usage

The `justfile` target creates a fresh archive, seeds the existing
tree, and drops the JATS file where hive's ingest can pick it up:

```
just assimilate-demo
```

Result: an `assimilate_demo.db` archive in the working directory,
plus a staged copy of the JATS at
`assimilate_demo.db.library/<sha>.jats.xml`. Run
`hive serve assimilate_demo.db` to open the WUI and step through
the assimilate flow, or point an extraction run at the JATS to
produce assertions.

## Name verification

Before committing the fixture, every scientific name in the
monograph + SQL seed should be verified against GNverifier
(`https://verifier.globalnames.org/`) to catch accidental
collisions with real published names. Names to check:

```
Tribbulus tribbulus
Tribbulus multiplicans
Tribbulus novellus
Tribbulus glomivorans
Tribbulus fecundissimus
Tribbulus dubius
Iotageminorius ferrugineus
Iotageminorius pallidus
Iotageminorius nigrofasciatus
Glommivorus glomivorans
Deepspacius rainbowicus
Xenopsyllotribbulus parasiticus
Xenopsyllotribbulus argenteus
Grexinia flava
Grexinia cyanella
```

If any collide with real names, pick alternates and update the
fixture. Higher ranks (Tribbulida, Tribbulidae) should also be
checked.

## Extending the fixture

When new UI branches or design cases arise, add treatments here
rather than authoring a new fixture. Keep every extension
inside the "Order Tribbulida" universe so the fixture stays a
single coherent fake dataset. Update the scenario table above
whenever a new branch is added.
