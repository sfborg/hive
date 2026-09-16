// Package pandoc is hive's subprocess adapter for pandoc
// (https://pandoc.org). Its one job in the ingest pipeline is
// EPUB → JATS conversion — the counterpart to GROBID's PDF →
// TEI step so that every ingested reference eventually reaches
// the same downstream JATS-annotation surface.
//
// Deliberately narrow: this package shells out to `pandoc`,
// nothing else. No sidecar file I/O, no annotation, no database.
// Callers compose it with the annotator to produce augmented
// JATS from an EPUB the same way tryGrobidConvert composes
// GROBID with jatsconv from a PDF.
//
// The pandoc binary must be on PATH inside the process. The
// docker image installs it; local development installs it via
// the OS package manager (`apt install pandoc`, `brew install
// pandoc`).
package pandoc

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// DefaultTimeout bounds a single conversion. Real EPUBs convert
// in a few seconds; a much longer timeout still cuts off a
// truly stuck pandoc rather than letting the upload handler
// hang forever.
const DefaultTimeout = 5 * time.Minute

// Client runs pandoc as a subprocess for EPUB → JATS
// conversion. Safe for concurrent use — each call spawns its
// own process; no shared state beyond the immutable fields.
type Client struct {
	Binary  string // path to pandoc; empty means look up "pandoc" on PATH
	Timeout time.Duration
}

// NewClient returns a Client that will invoke pandoc from PATH
// with the default per-conversion timeout. Set Binary to an
// absolute path when the process env has a non-standard
// installation. Set Timeout to override the default.
func NewClient() *Client {
	return &Client{
		Timeout: DefaultTimeout,
	}
}

// Ping checks whether pandoc is invocable and reports its
// version. Called from the upload handler when a pipeline
// attempt would otherwise fail — surfaces a friendly "install
// pandoc" message rather than a bare exec ENOENT.
func (c *Client) Ping(ctx context.Context) error {
	bin := c.binary()
	cmd := exec.CommandContext(ctx, bin, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pandoc: %s --version: %w", bin, err)
	}
	return nil
}

// EpubToJATS reads the EPUB at path and returns pandoc's JATS
// output. Uses the passed ctx for cancellation; the caller's
// per-request deadline propagates through so a client
// disconnect kills the subprocess rather than leaking a
// goroutine (contrast with the byte-scan loops that had to be
// audited separately for cancel-honoring).
//
// The output is JATS 1.x "article" form — pandoc's default
// EPUB → JATS conversion. Structure quality varies with the
// EPUB's own semantic HTML: EPUBs authored as clean XHTML
// sections come through with sensible <sec> / <title> / <p>
// nesting; heavily-styled publisher EPUBs may produce flatter
// output. Both are valid JATS the annotator can walk for
// scientific-name detection.
func (c *Client) EpubToJATS(ctx context.Context, path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("pandoc: EpubToJATS: path required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	// Overlay a timeout on top of whatever the caller passed;
	// the subprocess dies at the earlier of the two deadlines.
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin := c.binary()
	// -f epub / -t jats: format flags. Output is written to
	// stdout so we don't need a temp file for the destination.
	// --wrap=preserve keeps line wrapping close to source rather
	// than reflowing at 72 columns — makes byte-offset splices
	// (from the annotator) less fragile against pandoc's own
	// formatting.
	// --standalone asks pandoc to emit a complete document with a
	// single root element instead of a fragment; without it,
	// pandoc's default EPUB → JATS output emits siblings at the
	// top level (frontmatter <boxed-text>, then chapter <sec>s)
	// with no wrapping <article>, and XML parsers reject that as
	// "junk after document element".
	cmd := exec.CommandContext(subCtx, bin,
		"-f", "epub",
		"-t", "jats",
		"--standalone",
		"--wrap=preserve",
		"-o", "-",
		path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Prefer stderr's message when pandoc printed one — its
		// error text ("could not open file", "unsupported input
		// format") is more actionable than "exit status 1".
		if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
			return nil, fmt.Errorf("pandoc: EpubToJATS %s: %s", path, msg)
		}
		return nil, fmt.Errorf("pandoc: EpubToJATS %s: %w", path, err)
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("pandoc: EpubToJATS %s: empty output", path)
	}
	return ensureSingleRoot(stdout.Bytes()), nil
}

// ensureSingleRoot defensively wraps pandoc output in a synthetic
// <article><body>…</body></article> when it lacks a single XML
// root element. Older pandoc versions (or edge inputs) emit
// fragments even with --standalone; the annotator only needs a
// well-formed root with a walkable <body> subtree, so wrapping
// with a minimal JATS scaffold makes the pipeline robust to
// pandoc-version drift without normalizing the interior.
func ensureSingleRoot(out []byte) []byte {
	trimmed := bytes.TrimSpace(out)
	// Skip past any XML prolog and DOCTYPE the emitter attached
	// before the first element start; those are legal and do not
	// count as a root element for the "junk after document
	// element" check.
	body := trimmed
	body = skipXMLProlog(body)
	if isSingleRooted(body) {
		return out
	}
	// Wrap. Keep the original prolog / DOCTYPE from pandoc, then
	// synthesize an article envelope around the remaining bytes.
	var wrapped bytes.Buffer
	prologLen := len(trimmed) - len(body)
	wrapped.Write(trimmed[:prologLen])
	wrapped.WriteString(`<article xml:lang="en"><body>`)
	wrapped.Write(body)
	wrapped.WriteString(`</body></article>`)
	return wrapped.Bytes()
}

// skipXMLProlog returns src with any leading XML prolog
// (<?xml…?>) and DOCTYPE declaration stripped. Case-sensitive on
// the tag markers; whitespace between chunks is skipped.
func skipXMLProlog(src []byte) []byte {
	for {
		src = bytes.TrimLeft(src, " \t\r\n")
		switch {
		case bytes.HasPrefix(src, []byte("<?xml")):
			end := bytes.Index(src, []byte("?>"))
			if end < 0 {
				return src
			}
			src = src[end+2:]
		case bytes.HasPrefix(src, []byte("<!DOCTYPE")):
			end := bytes.IndexByte(src, '>')
			if end < 0 {
				return src
			}
			src = src[end+1:]
		case bytes.HasPrefix(src, []byte("<!--")):
			end := bytes.Index(src, []byte("-->"))
			if end < 0 {
				return src
			}
			src = src[end+3:]
		default:
			return src
		}
	}
}

// isSingleRooted reports whether src (with prolog / DOCTYPE
// already stripped) parses cleanly as a single-root XML document.
// Uses encoding/xml's decoder, which is what the reader-side
// browser parser also enforces — if the Go parser hits the "junk
// after root element" case, the browser will too.
func isSingleRooted(src []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Strict = false
	depth := 0
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return depth == 0 && sawRoot
		}
		switch tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				if sawRoot {
					return false
				}
				sawRoot = true
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

func (c *Client) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "pandoc"
}
