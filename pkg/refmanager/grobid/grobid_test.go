package grobid

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClientDefaults(t *testing.T) {
	c := NewClient("")
	if c.URL != DefaultURL {
		t.Errorf("URL = %q, want %q", c.URL, DefaultURL)
	}
	c2 := NewClient("http://elsewhere:9999")
	if c2.URL != "http://elsewhere:9999" {
		t.Errorf("URL = %q", c2.URL)
	}
}

func TestPingSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/isalive" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte("true"))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestPingUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // reserved / unbound
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProcessFulltextDocumentSuccess(t *testing.T) {
	const wantTEI = `<?xml version="1.0"?><TEI><teiHeader/><text><body><p>hi</p></body></text></TEI>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/processFulltextDocument" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		// Verify the file part is present under 'input'.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		_, _, err := r.FormFile("input")
		if err != nil {
			t.Errorf("form file 'input': %v", err)
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(wantTEI))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	pdf := bytes.NewBufferString("fake pdf bytes")
	tei, err := c.ProcessFulltextDocument(context.Background(), pdf, "test.pdf")
	if err != nil {
		t.Fatalf("ProcessFulltextDocument: %v", err)
	}
	if string(tei) != wantTEI {
		t.Errorf("TEI mismatch")
	}
}

func TestProcessFulltextDocumentHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	_, err := c.ProcessFulltextDocument(context.Background(),
		bytes.NewBufferString("x"), "test.pdf")
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error missing status hint: %v", err)
	}
}

// Sanity: the multipart body we send parses back to a plausible shape.
func TestProcessFulltextDocumentMultipartShape(t *testing.T) {
	var (
		receivedField    string
		receivedFilename string
		receivedBody     []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			if part.FormName() != "" && receivedField == "" {
				receivedField = part.FormName()
				receivedFilename = part.FileName()
				receivedBody, _ = io.ReadAll(part)
			}
		}
		w.Write([]byte("<TEI/>"))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	_, _ = c.ProcessFulltextDocument(context.Background(),
		bytes.NewBufferString("body-bytes"), "example.pdf")
	if receivedField != "input" {
		t.Errorf("field = %q, want 'input'", receivedField)
	}
	if receivedFilename != "example.pdf" {
		t.Errorf("filename = %q", receivedFilename)
	}
	if string(receivedBody) != "body-bytes" {
		t.Errorf("body = %q", string(receivedBody))
	}
}

// Guard: unused import of multipart in this test file otherwise.
var _ = multipart.NewWriter
