package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()

	if err := SetKeyringSecret("test-label", "sk-secret-1"); err != nil {
		t.Fatalf("SetKeyringSecret: %v", err)
	}

	c := KeyringCredential{Label: "test-label"}
	got, err := c.Reveal(context.Background())
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if got != "sk-secret-1" {
		t.Errorf("Reveal = %q, want sk-secret-1", got)
	}

	// Overwrite semantics: a second Set replaces the value with no
	// bookkeeping required.
	if err := SetKeyringSecret("test-label", "sk-secret-2"); err != nil {
		t.Fatalf("SetKeyringSecret (overwrite): %v", err)
	}
	got, err = c.Reveal(context.Background())
	if err != nil {
		t.Fatalf("Reveal (after overwrite): %v", err)
	}
	if got != "sk-secret-2" {
		t.Errorf("Reveal after overwrite = %q, want sk-secret-2", got)
	}

	if err := DeleteKeyringSecret("test-label"); err != nil {
		t.Fatalf("DeleteKeyringSecret: %v", err)
	}
	if _, err := c.Reveal(context.Background()); err == nil {
		t.Fatalf("expected error revealing deleted entry")
	}
}

func TestKeyringDeleteMissingIsNoError(t *testing.T) {
	keyring.MockInit()
	if err := DeleteKeyringSecret("never-existed"); err != nil {
		t.Fatalf("delete of missing entry should be a no-op, got: %v", err)
	}
}

func TestSetKeyringSecretRequiresLabel(t *testing.T) {
	keyring.MockInit()
	err := SetKeyringSecret("", "value")
	if err == nil || !strings.Contains(err.Error(), "label") {
		t.Fatalf("expected label-required error, got %v", err)
	}
}

func TestNewProviderKeyringLabelDefaultsToProviderName(t *testing.T) {
	keyring.MockInit()
	if err := SetKeyringSecret("anthropic-main", "sk-real"); err != nil {
		t.Fatalf("seed keyring: %v", err)
	}

	p, err := NewProvider(ProviderConfig{
		Name:         "anthropic-main",
		Kind:         KindAnthropic,
		DefaultModel: "claude-opus-4-7",
		Credential:   CredentialConfig{Source: "keyring"}, // no Value — factory defaults it
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// Cast to Anthropic to reach the Credential for verification —
	// the public Provider interface intentionally doesn't expose
	// credentials.
	a, ok := p.(*Anthropic)
	if !ok {
		t.Fatalf("expected *Anthropic, got %T", p)
	}
	got, err := a.cred.Reveal(context.Background())
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if got != "sk-real" {
		t.Errorf("Reveal = %q, want sk-real", got)
	}
}
