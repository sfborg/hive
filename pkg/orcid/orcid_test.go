package orcid

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sfborg/hive/pkg/orcid/config"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		// Josiah Carberry — ORCID's canonical demo iD.
		{"0000-0002-1825-0097", "0000-0002-1825-0097", false},
		{"0000000218250097", "0000-0002-1825-0097", false},
		{"https://orcid.org/0000-0002-1825-0097", "0000-0002-1825-0097", false},
		{"HTTPS://ORCID.ORG/0000-0002-1825-0097", "0000-0002-1825-0097", false},
		{"  0000-0002-1825-0097  ", "0000-0002-1825-0097", false},
		// X-terminated iD.
		{"0000-0002-1694-233X", "0000-0002-1694-233X", false},
		{"0000-0002-1694-233x", "0000-0002-1694-233X", false},
		// Bad checksum (all-zero base → check digit is 1, not 2).
		{"0000-0000-0000-0002", "", true},
		// Wrong length.
		{"0000-0002-1825-009", "", true},
		// Non-digit body.
		{"0000-0002-182A-0097", "", true},
		// X in wrong position.
		{"X000-0002-1825-0097", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := Normalize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q) = %q, want error", tc.in, got)
				continue
			}
			if !errors.Is(err, ErrInvalidID) {
				t.Errorf("Normalize(%q) err = %v, want ErrInvalidID", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeSearchTerm(t *testing.T) {
	// All Solr operator characters get replaced with spaces.
	in := `Smith AND family-name:foo OR (bar)`
	got := SanitizeSearchTerm(in)
	// ":" and "(" ")" are replaced; letters/spaces preserved.
	if got != `Smith AND family name foo OR  bar ` {
		t.Errorf("Sanitize = %q", got)
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(config.OptBaseURL(baseURL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNewRejectsInsecureBaseURL(t *testing.T) {
	_, err := New(config.OptBaseURL("http://example.com/v3.0"))
	if err == nil {
		t.Fatal("want ConfigError")
	}
	if !errors.Is(err, ErrConfig) {
		t.Errorf("err = %v, want ErrConfig", err)
	}
}

func TestNewAllowsInsecureWhenOptedIn(t *testing.T) {
	_, err := New(
		config.OptBaseURL("http://example.com/v3.0"),
		config.OptAllowInsecure(),
	)
	if err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestNewAllowsLoopbackHTTP(t *testing.T) {
	_, err := New(config.OptBaseURL("http://127.0.0.1:9999"))
	if err != nil {
		t.Errorf("loopback http rejected: %v", err)
	}
}

func TestLookup(t *testing.T) {
	const fixture = `{
		"name": {
			"given-names": {"value": "Josiah"},
			"family-name": {"value": "Carberry"},
			"credit-name": {"value": "Josiah S. Carberry"}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0000-0002-1825-0097/personal-details" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.orcid+json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	p, err := newTestClient(t, srv.URL).Lookup(context.Background(), "0000-0002-1825-0097")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if p.ORCID != "0000-0002-1825-0097" {
		t.Errorf("ORCID = %q", p.ORCID)
	}
	if p.GivenNames != "Josiah" || p.FamilyName != "Carberry" {
		t.Errorf("Given/Family = %q / %q", p.GivenNames, p.FamilyName)
	}
	if p.CreditName != "Josiah S. Carberry" {
		t.Errorf("CreditName = %q", p.CreditName)
	}
	if got := p.DisplayName(); got != "Josiah S. Carberry" {
		t.Errorf("DisplayName = %q", got)
	}
}

func TestLookupHidesUnsetFields(t *testing.T) {
	const fixture = `{
		"name": {
			"given-names": {"value": "Given"},
			"family-name": null,
			"credit-name": null
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	p, err := newTestClient(t, srv.URL).Lookup(context.Background(), "0000-0002-1825-0097")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if p.FamilyName != "" || p.CreditName != "" {
		t.Errorf("null fields not zeroed: %+v", p)
	}
	if got := p.DisplayName(); got != "Given" {
		t.Errorf("DisplayName = %q", got)
	}
}

func TestLookupNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Lookup(context.Background(), "0000-0002-1825-0097")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
	if nfe.ID != "0000-0002-1825-0097" {
		t.Errorf("NotFoundError.ID = %q", nfe.ID)
	}
}

func TestLookupInvalidORCID(t *testing.T) {
	// No server needed — validation is client-side.
	c := newTestClient(t, "http://127.0.0.1:9")
	_, err := c.Lookup(context.Background(), "not-an-orcid")
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("err = %v, want ErrInvalidID", err)
	}
}

func TestLookupRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	err := errorsIsCheck(t, srv.URL, ErrRateLimited)
	var rle *RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitedError", err)
	}
	if rle.RetryAfter.Seconds() != 5 {
		t.Errorf("RetryAfter = %v", rle.RetryAfter)
	}
}

func TestLookupServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	err := errorsIsCheck(t, srv.URL, ErrServer)
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *ServerError", err)
	}
	if se.Status != http.StatusBadGateway {
		t.Errorf("Status = %d", se.Status)
	}
}

func errorsIsCheck(t *testing.T, baseURL string, target error) error {
	t.Helper()
	_, err := newTestClient(t, baseURL).Lookup(context.Background(), "0000-0002-1825-0097")
	if !errors.Is(err, target) {
		t.Errorf("err = %v, want %v", err, target)
	}
	return err
}

func TestSearch(t *testing.T) {
	const fixture = `{
		"expanded-result": [
			{
				"orcid-id": "0000-0002-1825-0097",
				"given-names": "Josiah",
				"family-names": "Carberry",
				"credit-name": "Josiah S. Carberry",
				"institution-name": ["Brown University", "Wesleyan University"]
			},
			{
				"orcid-id": "0000-0002-1694-233X",
				"given-names": "Jane",
				"family-names": "Doe",
				"institution-name": []
			}
		],
		"num-found": 2
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/expanded-search/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "carberry" {
			t.Errorf("q = %q", got)
		}
		if got := r.URL.Query().Get("rows"); got != "50" {
			t.Errorf("rows = %q", got)
		}
		if got := r.URL.Query().Get("start"); got != "20" {
			t.Errorf("start = %q", got)
		}
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	hits, err := newTestClient(t, srv.URL).Search(
		context.Background(),
		"carberry",
		&SearchOptions{Rows: 50, Start: 20},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].Institution != "Brown University" {
		t.Errorf("hits[0].Institution = %q", hits[0].Institution)
	}
	if hits[1].Institution != "" {
		t.Errorf("hits[1].Institution = %q, want empty", hits[1].Institution)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	// Empty query short-circuits — no server hit expected.
	c := newTestClient(t, "http://127.0.0.1:9")
	hits, err := c.Search(context.Background(), "   ", nil)
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil", hits)
	}
}

func TestSearchDefaultsRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("rows"); got != "20" {
			t.Errorf("rows = %q, want 20", got)
		}
		_, _ = w.Write([]byte(`{"expanded-result":[]}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).Search(context.Background(), "x", nil); err != nil {
		t.Fatal(err)
	}
}
