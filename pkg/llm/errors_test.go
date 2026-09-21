package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountLimit(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		expect bool
	}{
		{
			name: "anthropic spend limit set on the account",
			err: &ProviderError{KindAnthropic, 400, `{"type":"error","error":{"type":"invalid_request_error",` +
				`"message":"You have reached your specified API usage limits. You will regain access on 2026-10-01 at 00:00 UTC."}}`},
			expect: true,
		},
		{
			name: "anthropic spend limit set on a workspace",
			err: &ProviderError{KindAnthropic, 400, `{"type":"error","error":{"type":"invalid_request_error",` +
				`"message":"You have reached your specified workspace API usage limits. You will regain access on 2026-10-01 at 00:00 UTC."}}`},
			expect: true,
		},
		{
			name: "anthropic tier spend cap",
			err: &ProviderError{KindAnthropic, 429, `{"type":"error","error":{"type":"rate_limit_error",` +
				`"message":"You have reached your API usage limits","details":{"error_code":"enforced_spend_limit_reached"}}}`},
			expect: true,
		},
		{
			name:   "anthropic billing error",
			err:    &ProviderError{KindAnthropic, 402, `{"type":"error","error":{"type":"billing_error","message":"..."}}`},
			expect: true,
		},
		{
			name: "openai exhausted quota",
			err: &ProviderError{KindOpenAICompat, 429, `{"error":{"message":"You exceeded your current quota",` +
				`"type":"insufficient_quota","code":"insufficient_quota"}}`},
			expect: true,
		},
		{
			name: "openai project spend limit",
			err: &ProviderError{KindOpenAICompat, 429, `{"error":{"message":"...","type":"insufficient_quota",` +
				`"code":"project_spend_limit_exceeded"}}`},
			expect: true,
		},
		{
			name:   "openai credit balance exhausted",
			err:    &ProviderError{KindOpenAICompat, 429, `{"error":{"message":"...","code":"credit_balance_exhausted"}}`},
			expect: true,
		},
		{
			name:   "openrouter insufficient credits",
			err:    &ProviderError{KindOpenAICompat, 402, `{"error":{"code":402,"message":"Insufficient credits"}}`},
			expect: true,
		},
		{
			name:   "deepseek insufficient balance",
			err:    &ProviderError{KindOpenAICompat, 402, `{"error":{"message":"Insufficient Balance"}}`},
			expect: true,
		},
		{
			name:   "rejected api key",
			err:    &ProviderError{KindOpenAICompat, 401, `{"error":{"message":"Invalid API key"}}`},
			expect: true,
		},
		{
			name:   "wrapped provider error",
			err:    fmt.Errorf("llm run ner_v9: %w", &ProviderError{KindOpenAICompat, 402, `{}`}),
			expect: true,
		},

		// Look-alikes that must not stop a batch.
		{
			name: "openrouter in-flight budget is wait-and-retry",
			err: &ProviderError{KindOpenAICompat, 402, `{"error":{"code":402,"message":"...",` +
				`"metadata":{"limit_source":"openrouter_in_flight_budget"}}}`},
			expect: false,
		},
		{
			name: "anthropic ordinary rate limit",
			err: &ProviderError{KindAnthropic, 429, `{"type":"error","error":{"type":"rate_limit_error",` +
				`"message":"Number of request tokens has exceeded your per-minute rate limit"}}`},
			expect: false,
		},
		{
			name:   "openai slow_down rate limit",
			err:    &ProviderError{KindOpenAICompat, 429, `{"error":{"type":"rate_limit_error","code":"slow_down"}}`},
			expect: false,
		},
		{
			name: "one oversized request",
			err: &ProviderError{KindAnthropic, 400, `{"type":"error","error":{"type":"invalid_request_error",` +
				`"message":"prompt is too long: 250000 tokens > 200000 maximum"}}`},
			expect: false,
		},
		{
			name:   "provider server error",
			err:    &ProviderError{KindAnthropic, 500, `{"type":"error","error":{"type":"api_error"}}`},
			expect: false,
		},
		{
			name:   "not a provider error",
			err:    errors.New("dial tcp: connection refused"),
			expect: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, got := AccountLimit(c.err)
			if got != c.expect {
				t.Fatalf("AccountLimit = %v (%q), want %v", got, reason, c.expect)
			}
			if got && reason == "" {
				t.Fatal("account-level refusal returned an empty reason")
			}
		})
	}
}

// Both adapters must hand back a *ProviderError so AccountLimit can
// see the status and body.
func TestAdaptersReturnProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
	}))
	defer srv.Close()

	for _, p := range []Provider{
		NewAnthropic("a", srv.URL, "m", PlainCredential{Secret: "k"}),
		NewOpenAICompat("o", srv.URL, "m", PlainCredential{Secret: "k"}),
	} {
		_, err := p.Complete(context.Background(), Request{User: "u"})
		var pe *ProviderError
		if !errors.As(err, &pe) || pe.Status != http.StatusPaymentRequired {
			t.Fatalf("%T: want *ProviderError with 402, got %v", p, err)
		}
		if _, ok := AccountLimit(err); !ok {
			t.Fatalf("%T: 402 not classified as an account-level refusal", p)
		}
	}
}
