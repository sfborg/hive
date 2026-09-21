package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const invokeOK = `{"run_id":"r","envelope":{"input_tokens":10,"output_tokens":5},"assertions_materialized":0}`

// invokeServer answers /api/llm/invoke by calling respond with the
// 1-based call number and returns the call counter.
func invokeServer(t *testing.T, respond func(n int64, w http.ResponseWriter)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/llm/invoke" {
			http.NotFound(w, r)
			return
		}
		respond(calls.Add(1), w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func writeProblemJSON(w http.ResponseWriter, status int, typ string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":%q,"title":"t","status":%d,"detail":"d"}`, typ, status)
}

func batchOpts(srv *httptest.Server, nRefs int) llmBatchOpts {
	refs := make([]string, nRefs)
	for i := range refs {
		refs[i] = fmt.Sprintf("ref-%04d-0000-0000-0000-000000000000", i)
	}
	return llmBatchOpts{
		HTTPClient:  srv.Client(),
		Server:      srv.URL,
		Refs:        refs,
		Provider:    "p",
		Prompt:      "ner_v9",
		Purpose:     "ner",
		Concurrency: 1,
		Quiet:       true,
		inputFn: func(context.Context, string) (string, string, string, error) {
			return "input", "", "", nil
		},
	}
}

func TestRunBatchStopsOnProviderAccountLimit(t *testing.T) {
	srv, calls := invokeServer(t, func(n int64, w http.ResponseWriter) {
		if n == 1 {
			_, _ = w.Write([]byte(invokeOK))
			return
		}
		writeProblemJSON(w, http.StatusBadGateway, problemProviderAccountLimit)
	})

	err := runBatch(context.Background(), batchOpts(srv, 10))

	if got := calls.Load(); got != 2 {
		t.Fatalf("invoke calls = %d, want 2 (one success, then the refusal)", got)
	}
	var limit *providerLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("want providerLimitError, got %v", err)
	}
	if !strings.Contains(err.Error(), "8 reference(s) not attempted") {
		t.Fatalf("error should count not-attempted refs: %v", err)
	}
}

func TestRunBatchStopsAfterConsecutiveFailures(t *testing.T) {
	srv, calls := invokeServer(t, func(_ int64, w http.ResponseWriter) {
		writeProblemJSON(w, http.StatusInternalServerError, "urn:hive:error:internal")
	})

	err := runBatch(context.Background(), batchOpts(srv, 20))

	if got := calls.Load(); got != maxConsecutiveInvokeFailures {
		t.Fatalf("invoke calls = %d, want %d", got, maxConsecutiveInvokeFailures)
	}
	if err == nil || !strings.Contains(err.Error(), "consecutive requests failed") {
		t.Fatalf("want consecutive-failure stop, got %v", err)
	}
}

// Scattered failures (one bad paper here and there) must not stop a
// batch: a success resets the streak.
func TestRunBatchKeepsGoingThroughScatteredFailures(t *testing.T) {
	srv, calls := invokeServer(t, func(n int64, w http.ResponseWriter) {
		if n%2 == 1 {
			writeProblemJSON(w, http.StatusInternalServerError, "urn:hive:error:internal")
			return
		}
		_, _ = w.Write([]byte(invokeOK))
	})

	err := runBatch(context.Background(), batchOpts(srv, 12))

	if got := calls.Load(); got != 12 {
		t.Fatalf("invoke calls = %d, want all 12", got)
	}
	if err == nil || !strings.Contains(err.Error(), "6 reference(s) failed") ||
		strings.Contains(err.Error(), "stopped early") {
		t.Fatalf("want plain failure count without an early stop, got %v", err)
	}
}

// With several invokes in flight, the ones already dispatched finish
// but no new ones start once the refusal comes back.
func TestRunBatchStopsWithConcurrency(t *testing.T) {
	const concurrency = 4
	srv, calls := invokeServer(t, func(n int64, w http.ResponseWriter) {
		if n == 1 {
			_, _ = w.Write([]byte(invokeOK))
			return
		}
		writeProblemJSON(w, http.StatusBadGateway, problemProviderAccountLimit)
	})
	opts := batchOpts(srv, 40)
	opts.Concurrency = concurrency

	err := runBatch(context.Background(), opts)

	if got := calls.Load(); got > 1+concurrency {
		t.Fatalf("invoke calls = %d, want at most %d", got, 1+concurrency)
	}
	var limit *providerLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("want providerLimitError, got %v", err)
	}
}
