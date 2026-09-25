package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceWarnings(t *testing.T) {
	const server = "http://127.0.0.1:2365"
	tests := []struct {
		name        string
		svc         ingestServices
		pdfs, epubs int
		want        []string // substrings, one per expected warning
	}{
		{"all available", ingestServices{GROBID: "ok", Pandoc: "ok"}, 3, 1, nil},
		{"no PDFs in batch", ingestServices{GROBID: "not_configured", Pandoc: "ok"}, 0, 0, nil},
		{"GROBID not configured", ingestServices{GROBID: "not_configured", Pandoc: "ok"}, 2, 0,
			[]string{"is not connected to GROBID"}},
		{"GROBID down", ingestServices{GROBID: "unavailable", Pandoc: "ok"}, 1, 0,
			[]string{"GROBID is not responding"}},
		{"pandoc missing", ingestServices{GROBID: "ok", Pandoc: "unavailable"}, 0, 2,
			[]string{"pandoc is not installed"}},
		{"both", ingestServices{GROBID: "not_configured", Pandoc: "unavailable"}, 1, 1,
			[]string{"not connected to GROBID", "pandoc is not installed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceWarnings(tt.svc, server, tt.pdfs, tt.epubs)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d warnings %q, want %d", len(got), got, len(tt.want))
			}
			for i, sub := range tt.want {
				if !strings.Contains(got[i], sub) {
					t.Errorf("warning %d = %q, want it to contain %q", i, got[i], sub)
				}
			}
		})
	}
}

func TestCountByExt(t *testing.T) {
	pdfs, epubs := countByExt([]string{"a.pdf", "b.PDF", "c.epub", "d.jats.xml", "e.html"})
	if pdfs != 2 || epubs != 1 {
		t.Errorf("countByExt = (%d, %d), want (2, 1)", pdfs, epubs)
	}
}

func TestFetchServices(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"grobid":"not_configured","ocr":"not_configured","pandoc":"ok"}`))
	}))
	defer ok.Close()
	svc, err := fetchServices(http.DefaultClient, ok.URL)
	if err != nil || svc == nil || svc.GROBID != "not_configured" || svc.Pandoc != "ok" {
		t.Fatalf("fetchServices = %+v, %v", svc, err)
	}

	// A server without the endpoint: no services, no error.
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	svc, err = fetchServices(http.DefaultClient, old.URL)
	if err != nil || svc != nil {
		t.Fatalf("fetchServices on a server without /api/services = %+v, %v; want nil, nil", svc, err)
	}

	// An unreachable server is an error.
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	if _, err := fetchServices(http.DefaultClient, downURL); err == nil ||
		!strings.Contains(err.Error(), "cannot reach the hive server") {
		t.Fatalf("fetchServices on a stopped server: err = %v", err)
	}
}
