// Package ocr is hive's HTTP client for the OCR sidecar service
// (docker/ocr/), a tiny FastAPI shim around ocrmypdf that adds a
// searchable text layer to image-only scanned PDFs. Hive invokes
// it whenever a first GROBID pass returns near-empty body content —
// the signal that GROBID had no text to parse.
//
// Deliberately narrow: this package speaks to the OCR service's
// HTTP API only. No sidecar I/O, no GROBID orchestration, no
// text-emptiness detection — callers compose ocr with those.
//
// LIFT-TO-SFLIB CANDIDATE. Same rationale as pkg/grobid and
// pkg/annotator: every SFBorg tool that ingests scholarly PDFs
// benefits from OCR-when-needed. Kept self-contained so the
// eventual migration is a clean copy.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// DefaultURL matches the port docker/ocr/Dockerfile's uvicorn
// listens on. Operators running the OCR service on a different
// host / port set HIVE_OCR_URL to override.
const DefaultURL = "http://localhost:8080"

// Client speaks to a single OCR service. Safe for concurrent use
// across goroutines.
type Client struct {
	URL        string
	HTTPClient *http.Client
}

// NewClient returns a Client with a generous timeout. ocrmypdf on
// a long scanned paper can take several minutes — the service side
// has its own 10-minute cap; the client-side timeout matches so
// slow scans don't fail spuriously before ocrmypdf gives up.
func NewClient(url string) *Client {
	if url == "" {
		url = DefaultURL
	}
	return &Client{
		URL: strings.TrimRight(url, "/"),
		HTTPClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

// Ping checks whether the OCR service is reachable. Returns nil on
// success.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/isalive", nil)
	if err != nil {
		return fmt.Errorf("ocr: build ping request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("ocr: ping %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ocr: ping returned status %d", resp.StatusCode)
	}
	return nil
}

// ProcessPDF sends a PDF to the OCR service and returns the
// text-layer-augmented PDF bytes. Filename hint appears in the
// container logs for traceability; empty string is fine.
func (c *Client) ProcessPDF(ctx context.Context, pdf io.Reader, filename string) ([]byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if filename == "" {
		filename = "upload.pdf"
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("ocr: create form file: %w", err)
	}
	if _, err := io.Copy(part, pdf); err != nil {
		return nil, fmt.Errorf("ocr: copy pdf to multipart: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("ocr: close multipart writer: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/ocr", &body)
	if err != nil {
		return nil, fmt.Errorf("ocr: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/pdf")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr: POST /ocr: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ocr: status %d: %s", resp.StatusCode, string(snippet))
	}
	return io.ReadAll(resp.Body)
}
