# mktwrules

Generates the TaxonWorks-derived validation bundles that hive embeds:

| File | Ruleset | Applies to |
|---|---|---|
| `pkg/tw_rules.json` | `tw` | names of any code, name relations, type material, references |
| `pkg/tw_iczn_rules.json` | `tw_iczn` | names with `col__code_id` ZOOLOGICAL |
| `pkg/tw_icn_rules.json` | `tw_icn` | names with `col__code_id` BOTANICAL |
| `pkg/tw_icnp_rules.json` | `tw_icnp` | names with `col__code_id` BACTERIAL |
| `pkg/tw_icvcn_rules.json` | `tw_icvcn` | names with `col__code_id` VIRUS |
| `pkg/tw_ext_rules.json` | `tw_ext` | any code, on archives with the TaxonWorks round-trip columns |
| `pkg/tw_ext_<code>_rules.json` | `tw_ext_<code>` | one code each, same condition |

The `tw_ext*` bundles read the `tw__` columns TaxonWorks round-trips,
which are a proposal against the sfga schema. Each declares them in its
`requires` block; hive skips the bundle and reports the missing columns
when an archive lacks them, and `ApplyTaxonWorksExtension`
(`pkg/tw_extension.go`) adds them for the fixtures and the demo
archive. A bundle's `supersedes` array lists core rule ids it replaces
while it is loaded.

It also writes `pkg/testdata/tw_rulefixtures.json` (a Bad and a Good case
for every rule, run by `TestTWRuleFixtures` and applied by `tools/mkdemo`),
and adds the relations the rules use to `pkg/hive_rules.json` when they are
missing.

Edit the generator, not the generated JSON. From the repository root:

```
python3 tools/mktwrules/gen.py
go test ./pkg -run TestTWRuleFixtures
```

## Layout

- `gen.py` — entry point; bundle metadata, relations, output.
- `lib.py` — rule and fixture builders.
- `twdata.py` — rank lists per code and status (NOMEN URI) sets.
- `names.py`, `placement.py`, `status.py`, `completeness.py`,
  `relations.py`, `typerefs.py` — one module per rule family.
- `ext.py` — the rules that need the TaxonWorks round-trip columns.
  Each `add(..., ext=True, needs=[...], supersedes=[...])` call declares
  the columns the rule reads and the core rules it replaces; the
  requires blocks are built from those.
- `tw_classifications.jsonl` — one line per TaxonWorks
  `TaxonNameClassification` class: NOMEN URI, code, applicable-rank
  constant, year window, constant memberships, and whether the class
  defines its own `sv_not_specific_classes` message. The values were
  resolved by loading the TaxonWorks class files at TaxonWorks commit
  `70f884140a`.

Each rule's `description` names the TaxonWorks class and method it is
ported from. Rule ids start with `tw_` (code-general) or `tw_<code>_`.
