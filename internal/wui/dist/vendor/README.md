# Vendored web assets

The web interface loads no packages at run time and uses no CDN. Its
JavaScript is either written for hive under `internal/wui/dist/` or
pinned in this directory, and the `hive` binary embeds all of it.

## Lit

- **File:** `lit-3.x.x.min.js`
- **Version:** Lit 3.x (bundled `lit-all` build from https://github.com/lit/dist@3)
- **Source URL:** `https://cdn.jsdelivr.net/gh/lit/dist@3/all/lit-all.min.js`
- **SHA-256:** `e14e8c5cece98af1f21c737187763f51fa4efba87be1641916d9bf475472bcb9`
- **Size:** 29,370 bytes
- **License:** BSD-3-Clause, Copyright (c) The Lit Project Contributors
  and Copyright (c) 2017 Google LLC (see `lit-3.x.x.min.js.LICENSE.txt`)

### Vendored files

- **`lit-3.x.x.min.js`**: bundled runtime, imported by `app.js`. Retains
  the upstream `@license` header (`SPDX-License-Identifier: BSD-3-Clause`).
- **`lit-3.x.x.min.js.LICENSE.txt`**: full BSD-3-Clause text from
  https://github.com/lit/lit/blob/main/LICENSE, 1,561 bytes
  - **SHA-256:** `c94b94b40b85a5083dea825ae844b1cedb40dd425685362c135e143265dd6cd4`

### Upgrading

`just vendor-lit` downloads the current bundle from the source URL,
replaces `lit-3.x.x.min.js`, and prints its SHA-256 and size. Update
this entry, then check the web interface in a browser, including the
offline app shell, before committing.

## ORCID iD icons

Both from the ORCID brand library
(https://orcid.filecamp.com/s/o/LdPTOOrMoSrjElD5/OJjCdaqbG7Jdu0ga),
default green colourway, SVG, dated 2024-11-12. The library is a web app
with no direct file URLs, so refreshing means downloading the archives
again by hand.

- **`orcid/ORCID-iD_icon_unauth_vector.svg`** — unauthenticated iD icon,
  1,307 bytes, from `Default.zip` → `Default/`.
  - **SHA-256:** `4666eec4ed5b890976f309c54de2e2694ab97d07b3af180e7889b49d5e0219ad`
- **`orcid/ORCID-iD_icon_vector.svg`** — authenticated iD icon, 973
  bytes, from `Authenticated iD Icons.zip` → `Authenticated iD Icons/Default/`.
  Not referenced yet; reserved for iDs a user has verified by signing in
  with ORCID.
  - **SHA-256:** `24e2805bb9409fb1a2c2ce9a4bf8c1406606e03a0f5014394b99a09824b099c0`

ORCID™, the ORCID logo, and the iD logo are trademarks of ORCID, Inc.
and are used in accordance with the ORCID Brand Guidelines
(https://info.orcid.org/brand-guidelines/). The icons are used as
provided and unmodified, and iDs are displayed in accordance with the
ORCID iD Display Guidelines
(https://info.orcid.org/documentation/integration-guide/orcid-id-display-guidelines/).
The display guidelines distinguish iDs collected through the ORCID
authenticated workflow from iDs entered manually or obtained from another
system where authentication status is unknown. hive's iDs are currently
all the latter (curator entry and imports), so they are shown with the
unauthenticated icon and followed by "(unauthenticated)".

The authenticated archive also carries mono (black/white) and reversed
(white) colourways, and both archives carry 16/24/32 px PNGs; only the
default-colourway SVGs are vendored.

To refresh: download the archives, `sha256sum` each SVG, update this
entry. Never resize or re-encode the files; size them with CSS.
