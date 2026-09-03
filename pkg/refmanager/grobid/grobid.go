// Package grobid is hive's HTTP client for GROBID
// (https://github.com/kermitt2/grobid) — the ML-based tool that
// converts a scientific PDF into structured TEI XML. Hive uses
// GROBID as the PDF → TEI step of the reference-ingest pipeline;
// a downstream converter maps the TEI onto JATS (the sidecar's
// canonical annotation format — see REFERENCE_PDF_PLAN.md).
//
// Deliberately narrow: this package speaks to GROBID's HTTP API,
// nothing else. No sidecar file I/O, no JATS conversion, no
// database. Callers compose grobid with sidecar to persist the
// TEI output and with the JATS converter to derive the annotation
// surface.
//
// GROBID is a Java service; hive expects operators to run it
// alongside (typically via the docker-compose stack shipped in
// the repo root). Config: HIVE_GROBID_URL env var, defaulting to
// http://localhost:8070 which matches GROBID's Docker default.
package grobid

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// DefaultURL matches the port GROBID's official Docker image
// exposes by default. Operators running GROBID on a different
// host / port set HIVE_GROBID_URL to override.
const DefaultURL = "http://localhost:8070"

// Client speaks to a single GROBID service. Safe for concurrent
// use across goroutines — the underlying http.Client handles
// connection pooling, and each call constructs an independent
// multipart body.
type Client struct {
	URL        string
	HTTPClient *http.Client
}

// NewClient returns a Client with a sensible timeout. GROBID's
// ML passes can take a while on long PDFs (10-30s is normal, a
// minute for a big paper is not unheard of). 3-minute timeout
// covers even edge cases without leaving requests hanging
// forever on a stuck server.
func NewClient(url string) *Client {
	if url == "" {
		url = DefaultURL
	}
	return &Client{
		URL: url,
		HTTPClient: &http.Client{
			Timeout: 3 * time.Minute,
		},
	}
}

// Ping checks whether the GROBID service at c.URL is reachable
// and responding. Returns nil on success, an error describing the
// failure otherwise. Called from the upload handler when a
// pipeline attempt would otherwise fail — surfaces a friendly
// "start GROBID" message rather than a raw connection refused.
//
// Uses GROBID's /api/isalive endpoint which returns "true" as
// plain text when the service is healthy.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/isalive", nil)
	if err != nil {
		return fmt.Errorf("grobid: build ping request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("grobid: ping %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("grobid: ping returned status %d", resp.StatusCode)
	}
	return nil
}

// ProcessFulltextDocument sends a PDF to GROBID's full-text
// extraction endpoint and returns the TEI XML response.
//
// Uses /api/processFulltextDocument which runs the complete
// pipeline (header + references + body + figures + tables). For
// hive's current MVP we consume the whole response and pass it
// downstream; future callers may want the lighter
// /api/processHeaderDocument endpoint if they only need
// bibliographic metadata.
//
// The filename hint helps GROBID's logging but doesn't affect
// output; pass the sha-derived name (e.g. "<sha>.pdf") for
// traceability. Pass an empty string when caller has no name.
func (c *Client) ProcessFulltextDocument(ctx context.Context, pdf io.Reader, filename string) ([]byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if filename == "" {
		filename = "upload.pdf"
	}
	part, err := mw.CreateFormFile("input", filename)
	if err != nil {
		return nil, fmt.Errorf("grobid: create form file: %w", err)
	}
	if _, err := io.Copy(part, pdf); err != nil {
		return nil, fmt.Errorf("grobid: copy pdf to multipart: %w", err)
	}
	// teiCoordinates asks GROBID to emit source-PDF coord attrs
	// (page + bounding box) on the named element types. Passed as
	// separate multipart fields with the same name — GROBID's API
	// parses repeated fields as a list, not comma-joined values.
	// Downstream jatsconv preserves the coords as hive:pdf-page +
	// hive:pdf-bbox on <fig> and <table-wrap>, which figextract uses
	// to render per-figure/table PNG crops via pdftocairo.
	//
	// Element list is intentionally minimal — coord annotations
	// add output size, so we only ask for the types we actually
	// crop. Additional types (persName, formula, biblStruct, p)
	// become worthwhile once we build features that need them.
	for _, elem := range []string{"figure", "table", "head", "biblStruct"} {
		if err := mw.WriteField("teiCoordinates", elem); err != nil {
			return nil, fmt.Errorf("grobid: write teiCoordinates: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("grobid: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.URL+"/api/processFulltextDocument", &body)
	if err != nil {
		return nil, fmt.Errorf("grobid: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/xml")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grobid: POST /api/processFulltextDocument: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Read a bounded snippet of the body for the error message —
		// GROBID often returns structured HTML on failure that's
		// noisy but sometimes helpful.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("grobid: status %d: %s",
			resp.StatusCode, string(snippet))
	}
	tei, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("grobid: read TEI body: %w", err)
	}
	return tei, nil
}
