package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Credential yields a secret value on demand. Adapters call
// [Credential.Reveal] once per HTTP request; nothing else touches
// the underlying secret. The interface (rather than a plain string
// field) lets the encryption-at-rest work (task #216) slot in as a
// new implementation without churning every adapter's construction
// path — see project_llm_credential_encryption_arc.
type Credential interface {
	Reveal(ctx context.Context) (string, error)
}

// EnvCredential resolves the secret from an environment variable at
// call time. The variable name is stored in config; the secret
// itself never touches disk under hive's control.
type EnvCredential struct{ VarName string }

// Reveal reads the env var. Returns an error (not a zero string)
// when unset so callers see a clear failure rather than a silent
// "bearer " authorization header.
func (e EnvCredential) Reveal(_ context.Context) (string, error) {
	v := os.Getenv(e.VarName)
	if v == "" {
		return "", fmt.Errorf("credential: env var %s is unset", e.VarName)
	}
	return v, nil
}

// PlainCredential holds the secret in memory as-is. Useful for
// tests, for local Ollama deployments that ignore Authorization
// headers entirely, and (with a warning) as a "just paste the key
// in the config file" convenience until encryption-at-rest lands.
type PlainCredential struct{ Secret string }

// Reveal returns the in-memory secret.
func (p PlainCredential) Reveal(_ context.Context) (string, error) {
	if p.Secret == "" {
		return "", errors.New("credential: plaintext value is empty")
	}
	return p.Secret, nil
}

// CredentialConfig is the YAML-mapped shape of a provider's
// credential entry. Source discriminates the implementation to
// build.
type CredentialConfig struct {
	// Source is the discriminator. Supported values:
	//   "env"       — read from an environment variable named EnvVar
	//   "plaintext" — read the Value field directly (unencrypted)
	//   "keyring"   — read from the OS keyring under
	//                 ([KeyringService], Value) — macOS Keychain,
	//                 Windows DPAPI, Linux Secret Service. When
	//                 Value is empty, [NewProvider] defaults it to
	//                 the provider Name so the common one-key-per-
	//                 provider case is zero-config.
	Source string `yaml:"source"`

	// EnvVar is the environment variable name, used when Source ==
	// "env". Ignored otherwise.
	EnvVar string `yaml:"env_var,omitempty"`

	// Value carries the plaintext (Source == "plaintext") or the
	// keyring entry label (Source == "keyring"). Ignored otherwise.
	Value string `yaml:"value,omitempty"`
}

// NewCredential constructs the Credential implementation described
// by cfg. Returns an error for missing / unknown sources so callers
// fail loud at startup rather than at first-use.
func NewCredential(cfg CredentialConfig) (Credential, error) {
	switch cfg.Source {
	case "env":
		if cfg.EnvVar == "" {
			return nil, errors.New("credential: env source requires env_var")
		}
		return EnvCredential{VarName: cfg.EnvVar}, nil
	case "plaintext":
		return PlainCredential{Secret: cfg.Value}, nil
	case "keyring":
		if cfg.Value == "" {
			return nil, errors.New("credential: keyring source requires a label (Value)")
		}
		return KeyringCredential{Label: cfg.Value}, nil
	case "":
		return nil, errors.New("credential: source is required")
	default:
		return nil, fmt.Errorf("credential: unknown source %q", cfg.Source)
	}
}
