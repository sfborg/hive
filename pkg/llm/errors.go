package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ProviderError is a non-2xx response from an LLM provider. It keeps
// the status and body so callers can tell account-level refusals
// (spend limit reached, credits exhausted, rejected key) apart from
// failures specific to one request.
type ProviderError struct {
	Adapter string // KindAnthropic or KindOpenAICompat
	Status  int
	Body    string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s http %d: %s", e.Adapter, e.Status, e.Body)
}

// accountLimitMarkers are error codes providers put in the response
// body when a request is refused because of the account's spend
// limit, quota or credit balance. Each comes from the provider's
// published error documentation.
var accountLimitMarkers = []string{
	// Anthropic: tier spend cap reached (HTTP 429).
	"enforced_spend_limit_reached",
	// OpenAI: billing, spend and quota refusals (HTTP 429). The
	// broader error type can be insufficient_quota for all of them.
	"insufficient_quota",
	"credit_balance_exhausted",
	"organization_spend_limit_exceeded",
	"project_spend_limit_exceeded",
	"organization_usage_limit_exceeded",
}

// AccountLimit reports whether err is a provider refusal that will
// repeat for every request made with the same credentials until the
// account holder acts, and returns a short reason. It returns false
// for errors that are not a [ProviderError], and for provider errors
// that are specific to one request or temporary (ordinary rate
// limits, oversized input, server errors).
//
// Providers disagree on the status code for "out of money": some use
// 402, OpenAI uses 429 and Anthropic uses 400 or 429. Providers with
// undocumented formats fall through as ordinary failures.
func AccountLimit(err error) (string, bool) {
	var pe *ProviderError
	if !errors.As(err, &pe) {
		return "", false
	}
	switch pe.Status {
	case http.StatusUnauthorized:
		return "the provider rejected the API key", true
	case http.StatusPaymentRequired:
		// OpenRouter documents its in-flight budget 402 as a
		// wait-and-retry case, not an exhausted balance.
		if strings.Contains(pe.Body, "openrouter_in_flight_budget") {
			return "", false
		}
		return "insufficient credits or balance with the provider", true
	case http.StatusTooManyRequests:
		for _, m := range accountLimitMarkers {
			if strings.Contains(pe.Body, m) {
				return "spend limit or quota reached with the provider", true
			}
		}
	case http.StatusBadRequest:
		// Anthropic: a spend limit set on the account or a workspace.
		// "You have reached your specified [workspace] API usage limits"
		if strings.Contains(pe.Body, "reached your specified") &&
			strings.Contains(pe.Body, "usage limits") {
			return "spend limit reached with the provider", true
		}
	}
	return "", false
}
