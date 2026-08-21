package ocr

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPingHappy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/isalive" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPingBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

// TestProcessPDF exercises the multipart POST and PDF-body-back
// round-trip. The stub echoes a fixed byte string so we can verify
// the client returns exactly what the service produced.
func TestProcessPDF(t *testing.T) {
	echoed := []byte("%PDF-1.4\nOCR'd body\n%%EOF")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ocr" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("expected multipart body, got %s", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if r.MultipartForm.File["file"] == nil {
			t.Fatalf("missing file part")
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(echoed)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.ProcessPDF(context.Background(), bytes.NewReader([]byte("fake pdf")), "test.pdf")
	if err != nil {
		t.Fatalf("ProcessPDF: %v", err)
	}
	if !bytes.Equal(out, echoed) {
		t.Errorf("ProcessPDF returned %q, want %q", out, echoed)
	}
}

func TestProcessPDFError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"detail":"ocrmypdf failed"}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.ProcessPDF(context.Background(), bytes.NewReader([]byte("")), "x.pdf"); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}
