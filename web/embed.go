// Package web embeds the PWA static assets shipped inside the hive binary.
//
// The single exported symbol is DistFS — a virtual filesystem rooted so
// that DistFS's "dist/" subdirectory contains index.html, app.js, styles.css,
// lib/api.js, and vendor/lit-*.min.js. Callers use fs.Sub(DistFS, "dist")
// to expose the flattened tree to http.FileServer.
//
// Assets are checked into the repo, not fetched at build time. The vendored
// Lit bundle is pinned via `just vendor-lit`; see web/dist/vendor/README.md
// for version and SHA-256.
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
