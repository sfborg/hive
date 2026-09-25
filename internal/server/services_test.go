package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sfborg/hive/pkg/refmanager/grobid"
	"github.com/sfborg/hive/pkg/refmanager/ocr"
)

func TestHandleServices(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/isalive" {
			w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer alive.Close()

	// A server that has been shut down refuses connections.
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()

	tests := []struct {
		name string
		srv  *server
		want apiServices
	}{
		{
			name: "nothing configured",
			srv:  &server{},
			want: apiServices{GROBID: "not_configured", OCR: "not_configured", Pandoc: "unavailable"},
		},
		{
			name: "services responding",
			srv:  &server{grobid: grobid.NewClient(alive.URL), ocr: ocr.NewClient(alive.URL)},
			want: apiServices{GROBID: "ok", OCR: "ok", Pandoc: "unavailable"},
		},
		{
			name: "services configured but down",
			srv:  &server{grobid: grobid.NewClient(downURL), ocr: ocr.NewClient(downURL)},
			want: apiServices{GROBID: "unavailable", OCR: "unavailable", Pandoc: "unavailable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.srv.handleServices(rec, httptest.NewRequest(http.MethodGet, "/api/services", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var got apiServices
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
