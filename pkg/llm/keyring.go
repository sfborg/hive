package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// KeyringService is the constant "service" string hive uses for
// every keyring entry. It's what appears in Keychain Access /
// secret-service listings, so a curator recognising "hive"
// alongside their other stored credentials matters more than
// namespacing under "sfborg-hive".
const KeyringService = "hive"

// KeyringCredential reads a secret from the OS keyring — macOS
// Keychain (via the `security` CLI), Windows DPAPI (via wincred),
// Linux Secret Service (via D-Bus / GNOME Keyring / KWallet).
// Curators configure it with `credential: {source: keyring, value:
// <label>}`; the factory defaults Label to the provider name when
// Value is empty, so `credential: {source: keyring}` alone is
// enough for the common one-key-per-provider case.
//
// Writes happen via [SetKeyringSecret] — not through this struct —
// because keyring writes are one-shot curator actions from the WUI
// paste flow, not part of the read hot path an adapter walks on
// every invocation.
type KeyringCredential struct {
	Label string
}

// Reveal reads the secret from the OS keyring. Wraps the underlying
// error with the label so a "keyring locked" or "entry not found"
// diagnostic is legible in logs without leaking the secret itself.
func (k KeyringCredential) Reveal(_ context.Context) (string, error) {
	v, err := keyring.Get(KeyringService, k.Label)
	if err != nil {
		return "", fmt.Errorf("credential: keyring get %q: %w", k.Label, err)
	}
	if v == "" {
		return "", fmt.Errorf("credential: keyring entry %q is empty", k.Label)
	}
	return v, nil
}

// SetKeyringSecret writes value into the OS keyring under
// (KeyringService, label), overwriting any existing entry.
// Called from the WUI paste-flow handler: curator pastes an API
// key in the LLM-provider modal, the handler persists it here,
// and the config file records only the label — the key itself
// never touches a plaintext file under hive's control.
func SetKeyringSecret(label, value string) error {
	if label == "" {
		return errors.New("credential: keyring label required")
	}
	if err := keyring.Set(KeyringService, label, value); err != nil {
		return fmt.Errorf("credential: keyring set %q: %w", label, err)
	}
	return nil
}

// DeleteKeyringSecret removes the (KeyringService, label) entry.
// Called from the WUI provider-delete flow so an orphaned secret
// doesn't linger after a curator removes the provider from config.
// Missing entries are not an error — the caller's intent (no such
// entry exists) is already satisfied.
func DeleteKeyringSecret(label string) error {
	err := keyring.Delete(KeyringService, label)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("credential: keyring delete %q: %w", label, err)
}
