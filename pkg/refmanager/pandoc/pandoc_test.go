package pandoc

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureSingleRootWrapsFragment covers the case that hit the
// reader in production: pandoc emitted two top-level siblings
// (a boxed-text frontmatter followed by a sec), the browser XML
// parser rejected the second sibling as "junk after document
// element". The wrapper must produce a single-rooted document.
func TestEnsureSingleRootWrapsFragment(t *testing.T) {
	in := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<boxed-text id="frontmatter"><p>preface</p></boxed-text>
<sec id="chapter1"><title>Introduction</title><p>Body.</p></sec>`)
	out := ensureSingleRoot(in)
	if !isSingleRooted(skipXMLProlog(bytes.TrimSpace(out))) {
		t.Errorf("wrapper output still not single-rooted:\n%s", out)
	}
	if !strings.Contains(string(out), "<article") {
		t.Errorf("expected synthesized <article> envelope; got:\n%s", out)
	}
	if !strings.Contains(string(out), "boxed-text") ||
		!strings.Contains(string(out), "chapter1") {
		t.Errorf("original content dropped during wrap:\n%s", out)
	}
}

// TestEnsureSingleRootPreservesValid confirms an already-valid
// single-root document passes through untouched — the wrapper
// must not re-wrap output that pandoc already emitted correctly.
func TestEnsureSingleRootPreservesValid(t *testing.T) {
	in := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<article><body><p>Text.</p></body></article>`)
	out := ensureSingleRoot(in)
	if !bytes.Equal(out, in) {
		t.Errorf("valid input mutated; want unchanged.\nin:  %s\nout: %s", in, out)
	}
}

// TestPingWithoutBinary reports ENOENT-ish when pandoc is not on
// PATH. Confirms Ping surfaces a real error rather than silently
// succeeding — the server's startup probe depends on that
// contract.
func TestPingWithoutBinary(t *testing.T) {
	c := &Client{Binary: "/no/such/pandoc/binary", Timeout: DefaultTimeout}
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping should error when binary path does not exist")
	}
}

// TestEpubToJATSRoundtrip synthesizes a minimal EPUB, runs the
// adapter against it, and asserts the output is JATS-shaped
// (contains an <article> or <book> root and the body text). Skips
// when pandoc is not installed on the dev machine — Docker
// images have it, CI should install it, but local runs without
// pandoc don't fail the build.
func TestEpubToJATSRoundtrip(t *testing.T) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not on PATH — skipping round-trip test")
	}
	dir := t.TempDir()
	epubPath := filepath.Join(dir, "minimal.epub")
	if err := writeMinimalEpub(epubPath); err != nil {
		t.Fatalf("writeMinimalEpub: %v", err)
	}
	c := NewClient()
	out, err := c.EpubToJATS(context.Background(), epubPath)
	if err != nil {
		t.Fatalf("EpubToJATS: %v", err)
	}
	s := string(out)
	// Pandoc's EPUB → JATS emits either <article> (default) or
	// <book> depending on structure; either is legitimate. The
	// body text we planted must survive the conversion.
	if !strings.Contains(s, "<article") && !strings.Contains(s, "<book") {
		t.Errorf("output missing root element:\n%s", s)
	}
	if !strings.Contains(s, "Panthera onca is a wildcat") {
		t.Errorf("body text dropped in conversion:\n%s", s)
	}
}

// writeMinimalEpub creates a bare EPUB 3 archive at path with a
// container.xml, a package.opf declaring one XHTML chapter, and
// that chapter's XHTML — the minimum pandoc will accept without
// errors. Body content includes a taxonomic sentence so tests
// downstream (annotator, etc.) can spot-check it.
func writeMinimalEpub(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	write := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}

	if err := write("mimetype", "application/epub+zip"); err != nil {
		return err
	}
	if err := write("META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`); err != nil {
		return err
	}
	if err := write("EPUB/package.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="en">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:00000000-0000-0000-0000-000000000000</dc:identifier>
    <dc:title>Minimal Test Article</dc:title>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
  </spine>
</package>`); err != nil {
		return err
	}
	if err := write("EPUB/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <head><title>Nav</title></head>
  <body>
    <nav epub:type="toc"><ol><li><a href="chapter1.xhtml">Chapter 1</a></li></ol></nav>
  </body>
</html>`); err != nil {
		return err
	}
	if err := write("EPUB/chapter1.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>Chapter 1</title></head>
  <body>
    <h1>Introduction</h1>
    <p>Panthera onca is a wildcat native to the Americas.</p>
  </body>
</html>`); err != nil {
		return err
	}
	return nil
}
