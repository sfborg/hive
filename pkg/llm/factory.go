package llm

import (
	"errors"
	"fmt"
)

// ProviderConfig is the YAML-mapped shape of a single provider
// entry. Lives in pkg/config as Config.LLMProviders and is
// constructed into runtime Providers via [NewProvider] at startup.
//
// The Name is the curator-chosen slug used everywhere else
// (registry key, envelope Provider field, UI dropdown label);
// Kind selects the adapter.
type ProviderConfig struct {
	// Name is the curator-facing identifier for this provider
	// instance. Must be unique within a config; the registry treats
	// later-registered names as shadowing earlier ones. Examples:
	// "anthropic-main", "ollama-local", "openrouter-cheap".
	Name string `yaml:"name"`

	// Kind selects the adapter — see the Kind constants in llm.go
	// (KindOpenAICompat, KindAnthropic).
	Kind string `yaml:"kind"`

	// BaseURL points at the provider's API root. Adapters supply a
	// sensible default when empty (Anthropic → api.anthropic.com;
	// OpenAI-compat requires an explicit value since there is no
	// single default).
	BaseURL string `yaml:"base_url,omitempty"`

	// DefaultModel is the model identifier used when a Request
	// doesn't set Model. Curators pick this once in config so
	// day-to-day invocations can leave the model field empty.
	DefaultModel string `yaml:"default_model,omitempty"`

	// Credential describes how the API key is resolved. See
	// [CredentialConfig].
	Credential CredentialConfig `yaml:"credential"`

	// DisableCacheControl turns off the cache_control:ephemeral
	// marker on the system prompt when this provider is an
	// OpenAI-compat endpoint. Default (false) sends the marker so
	// gateways that support Anthropic-style prompt caching
	// (notably OpenRouter routing to Anthropic) get the cache-
	// read discount transparently; providers that don't recognize
	// the field ignore it. Set to true only for strict
	// OpenAI-compat endpoints (some Ollama configs, older
	// mimics) that reject unknown fields.
	//
	// Ignored by the Anthropic adapter, which uses its native
	// cache_control mechanism unconditionally.
	DisableCacheControl bool `yaml:"disable_cache_control,omitempty"`
}

// NewProvider constructs a Provider from cfg. Returns an error when
// Name is blank, Kind is unknown, or the credential can't be built.
// Callers should stop the boot on error (rather than silently drop
// providers) so a config typo is discovered at startup rather than
// on first invocation.
func NewProvider(cfg ProviderConfig) (Provider, error) {
	if cfg.Name == "" {
		return nil, errors.New("llm: provider name is required")
	}
	// Zero-config default: a keyring credential without an explicit
	// Value uses the provider name as its keyring entry label, so
	// `credential: {source: keyring}` alone works for the common
	// one-key-per-provider case.
	if cfg.Credential.Source == "keyring" && cfg.Credential.Value == "" {
		cfg.Credential.Value = cfg.Name
	}
	cred, err := NewCredential(cfg.Credential)
	if err != nil {
		return nil, fmt.Errorf("llm provider %q: %w", cfg.Name, err)
	}
	switch cfg.Kind {
	case KindOpenAICompat:
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("llm provider %q: base_url is required for %s", cfg.Name, cfg.Kind)
		}
		return NewOpenAICompatWithOptions(cfg.Name, cfg.BaseURL, cfg.DefaultModel, cred,
			OpenAICompatOptions{DisableCacheControl: cfg.DisableCacheControl}), nil
	case KindAnthropic:
		return NewAnthropic(cfg.Name, cfg.BaseURL, cfg.DefaultModel, cred), nil
	case "":
		return nil, fmt.Errorf("llm provider %q: kind is required", cfg.Name)
	default:
		return nil, fmt.Errorf("llm provider %q: unknown kind %q", cfg.Name, cfg.Kind)
	}
}
