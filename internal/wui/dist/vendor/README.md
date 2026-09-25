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
all the latter (ingested papers, curator entry and imports), so they
are shown with the unauthenticated icon and followed by
"(unauthenticated)".

The authenticated archive also carries mono (black/white) and reversed
(white) colourways, and both archives carry 16/24/32 px PNGs; only the
default-colourway SVGs are vendored.

To refresh: download the archives, `sha256sum` each SVG, update this
entry. Never resize or re-encode the files; size them with CSS.

## RapiDoc

OpenAPI viewer used by hive's `/docs` page to render the API spec at
`/api/openapi.yaml`. Ships as a single Lit-based web component
(`<rapi-doc>`) — same Lit runtime hive already uses, no additional
runtime dependency chain.

- **Directory:** `rapidoc-9.3.8/`
- **Version:** 9.3.8
- **Source URL:** `https://registry.npmjs.org/rapidoc/-/rapidoc-9.3.8.tgz`
  (file: `package/dist/rapidoc-min.js`)
- **License:** MIT (see `rapidoc-9.3.8/rapidoc-min.js.LICENSE.txt`)

### Vendored files

- **`rapidoc-min.js`** — bundled web component, 863,099 bytes
  - **SHA-256:** `affbd173a65e870e18f392cca963113bcdafb6e50e88cd2f6a834a65b22db94d`
- **`rapidoc-min.js.LICENSE.txt`** — MIT + attribution for inlined
  libraries (js-yaml, Prism, JSON-Patch, ieee754, buffer polyfill),
  1,293 bytes

### RapiDoc upgrade procedure

1. Fetch the newer tarball from the source URL above with the new version.
2. Extract `package/dist/rapidoc-min.js` and `rapidoc-min.js.LICENSE.txt`.
3. `sha256sum rapidoc-min.js` and update this README.
4. Create a new directory `rapidoc-<new-version>/` alongside the old one.
5. Update the `<script>` tag in `internal/wui/dist/docs.html` to point at
   the new directory.
6. Load `/docs` in a browser and confirm the spec renders end-to-end
   before committing, then delete the old directory in the same commit.

## pdf.js

Mozilla's ES-module PDF renderer. Powers the Digitize tab's PDF pane
so hive can overlay bbox-drawing tools on the same canvases the
pages render into — something a plain `<iframe>` viewer can't offer.

- **Directory:** `pdfjs-6.2.108/`
- **Version:** 6.2.108
- **Source URL:** `https://github.com/mozilla/pdf.js/releases/download/v6.2.108/pdfjs-6.2.108-dist.zip`
- **License:** Apache 2.0 (see `pdfjs-6.2.108/LICENSE`)

### Vendored files

Only the minimum needed to render PDF pages to canvases in the
browser. Optional assets (source maps, standard fonts, CMaps,
viewer.mjs, sandbox) are deliberately excluded — hive builds its
own PDF viewer around the raw API.

- **`pdf.mjs`** — main API, 853,537 bytes
  - **SHA-256:** `e0ccc62fbfa69942eb7dd46c89d4b3ea8fc08f61b234e65f32e6d5c76efc04c8`
- **`pdf.worker.mjs`** — worker for parsing PDFs, 2,222,991 bytes
  - **SHA-256:** `1a7607f28cfbc63f0e4e0a41927c89f991e353e4f3fb4565ecfd621ac5975089`
- **`LICENSE`** — Apache 2.0, 10,174 bytes
  - **SHA-256:** `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`

Standard fonts and CMaps (for non-Latin scripts + CJK text) will be
added on demand when hive encounters PDFs that need them. CJK is the
most likely first ask.

### pdf.js upgrade procedure

1. Fetch the newer `pdfjs-<version>-dist.zip` from the releases page.
2. Extract `build/pdf.mjs`, `build/pdf.worker.mjs`, and `LICENSE`.
3. `sha256sum` each and update this README.
4. Create a new directory `pdfjs-<new-version>/` alongside the old
   one; leave both in place until the WUI is switched over.
5. Update the import in `internal/wui/dist/app.js` to point at the
   new directory path.
6. Test bbox drawing and page navigation in a browser before
   committing, then delete the old `pdfjs-<old-version>/`
   directory in the same commit.
