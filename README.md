# Hive

Hive is a viewer and editor for [SFGA](https://github.com/sfborg/sfga)
taxonomic archives (SQLite). It opens an archive in a terminal interface
or in the browser, checks it against validation rules, and stamps edits
with the editor's ORCID™ iD.

> **Status:** v0.0.1 is an early prototype. Expect rough edges and
> changes between releases, and keep backups of archives you edit.

## Features

- **Terminal interface.** `hive view` browses an archive read-only;
  `hive edit` opens it for editing.
- **Web interface.** `hive serve` runs a local web app for browsing and
  editing at http://127.0.0.1:2365.
- **Validation.** `hive validate` checks an archive with
  [gsvalidator](https://github.com/sfborg/gsvalidator) against three
  built-in rulesets (`hive`, `clb`, `tw`). Each can be switched off per
  archive with `hive ruleset`.
- **Attribution.** Edits are stamped with the configured ORCID iD.

## Install

Download a binary for Linux, macOS or Windows from the
[releases page](https://github.com/sfborg/hive/releases), or install
with Go 1.26.5 or later:

```
go install github.com/sfborg/hive@latest
```

Release binaries are not signed. On macOS, the first launch may need to
be approved in System Settings → Privacy & Security; on Windows,
SmartScreen may ask for confirmation.

## Development quick start

Development dependencies:

- [Go](https://go.dev/dl/) 1.26.5 or later
- [just](https://github.com/casey/just)

Clone this repo, build Hive and generate a small demo archive:

```
just build    # bin/hive and bin/mkdemo
just demo     # writes ./demo.db
```

Open it:

```
./bin/hive view demo.db     # read-only terminal interface
./bin/hive edit demo.db     # editing terminal interface
./bin/hive serve demo.db    # web interface at http://127.0.0.1:2365
```

Run the validation rules, and choose which rulesets apply:

```
./bin/hive validate demo.db
./bin/hive ruleset list demo.db
./bin/hive ruleset disable demo.db clb    # rulesets: hive, clb, tw
```

Run the tests with `just test`; `just --list` shows every recipe.

## Configuration

Settings live in `~/.config/sfborg/hive/config.yml`; `hive config path`
prints the location.

```
hive config set orcid 0000-0002-1825-0097
hive config set openalex-email you@example.org
```

- `orcid`: the iD stamped on edits (`col__modified_by`). Also set with
  `--orcid` or `HIVE_ORCID`.
- `openalex-email`: a contact address included with reference lookups
  to OpenAlex, as its polite pool requests. Also set with
  `--openalex-email` or `HIVE_OPENALEX_EMAIL`.

`hive serve` listens on 127.0.0.1 by default. Binding to any other
address requires authentication to be configured.

## License

MIT. See [LICENSE](LICENSE). The web interface bundles
[Lit](https://lit.dev) (BSD-3-Clause), whose license ships alongside it
in `internal/wui/dist/vendor/`.

ORCID™, the ORCID logo, and the iD logo are trademarks of ORCID, Inc.
and are used in accordance with the
[ORCID Brand Guidelines](https://info.orcid.org/brand-guidelines/).
