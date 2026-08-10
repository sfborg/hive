// Package config owns hive's user-config file: a small YAML document
// at $XDG_CONFIG_HOME/sfborg/hive/config.yml (or the platform
// equivalent), plus the merged flag > env > config Identity that
// downstream packages read via CurrentIdentity.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config is the hive user-config file — small YAML document at
// $XDG_CONFIG_HOME/sfborg/hive/config.yml (or the platform equivalent).
// Fields default to empty strings; downstream code merges with flag /
// env values (see cmd/ for the precedence order).
//
// New fields go here as additive YAML keys — no schema versioning yet
// because the file's small and hive tolerates unknown fields silently.
type Config struct {
	// ORCID iD stamped into col__modified_by on every write. Format-
	// validated to \d{4}-\d{4}-\d{4}-\d{3}[\dX] on save; local mode
	// is an attribution convention, not authentication.
	ORCID string `yaml:"orcid,omitempty"`
	// OpenAlexEmail identifies the curator to OpenAlex's polite
	// request pool. Optional — blank still works but lands in the
	// slower anonymous pool. Per-curator (not project-wide) so
	// OpenAlex sees real accountability signals.
	OpenAlexEmail string `yaml:"openalex_email,omitempty"`
}

// Path resolves the on-disk location of hive's config file. Follows
// the XDG Base Directory spec on Linux, uses the platform-native
// equivalent elsewhere. Path is returned even when the file doesn't
// exist yet — callers seeding a fresh config Save into it.
//
// Precedence:
//  1. $XDG_CONFIG_HOME/sfborg/hive/config.yml  (Linux + explicit)
//  2. $HOME/.config/sfborg/hive/config.yml     (Linux default)
//  3. $HOME/Library/Application Support/sfborg/hive/config.yml  (macOS)
//  4. %AppData%/sfborg/hive/config.yml         (Windows)
//
// The parent "sfborg" directory is shared with future SFBorg tools
// (sf, harvester, gndb) — each keeps its own hive-scoped subdir.
func Path() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "sfborg", "hive", "config.yml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: user home dir: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "sfborg", "hive", "config.yml"), nil
	case "windows":
		if appdata := os.Getenv("AppData"); appdata != "" {
			return filepath.Join(appdata, "sfborg", "hive", "config.yml"), nil
		}
		// Fall through to home if AppData isn't set — unusual, but
		// keeps hive functional in stripped-down environments.
	}
	return filepath.Join(home, ".config", "sfborg", "hive", "config.yml"), nil
}

// Load reads the config file. A missing file is not an error — returns
// a zero-value Config so callers see empty strings for every field and
// can decide what to fall back to (flag > env > empty).
//
// Any other error (malformed YAML, permission denied) surfaces so an
// operator can fix it before hive silently ignores their overrides.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config to disk, creating parent directories as
// needed. Perms: 0700 on the directory (config carries a personal
// identifier — ORCID — and a curator email, both worth keeping out of
// group-readable trees), 0600 on the file for the same reason.
func Save(c *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	// yaml.Marshal writes empty map ("{}\n") for a Config with no
	// fields set; that's readable but confusing. Emit a helpful header
	// comment so a curator opening the file for the first time sees
	// what it's for.
	header := []byte(`# hive user config. See CLAUDE.md § Authentication and collaboration.
# Managed by ` + "`hive config set …`" + ` — hand-editing is fine.
`)
	if err := os.WriteFile(path, append(header, data...), 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// Identity is the merged flag > env > config result used at runtime.
// Distinct from Config (the on-disk file) because callers should never
// mutate Config in-memory to reflect flag overrides — flags override
// for the process lifetime but must not silently rewrite the file.
//
// Client packages (OpenAlex, future ORCID auth middleware) read the
// current process identity via CurrentIdentity; the top-level cmd
// package seeds it after resolving precedence via SetIdentity.
type Identity struct {
	ORCID         string
	OpenAlexEmail string
}

var currentIdentity Identity

// SetIdentity records the merged identity for the process. Called once
// from main after resolving flag > env > config precedence. Safe to
// call multiple times but not concurrent-safe — should only happen
// during startup before any goroutines read it.
func SetIdentity(id Identity) { currentIdentity = id }

// CurrentIdentity returns the merged identity set by SetIdentity, or a
// zero-value Identity if SetIdentity was never called (e.g. in tests).
// Client packages that need the ORCID actor for auditing or the
// OpenAlex email for polite-pool headers read from here.
func CurrentIdentity() Identity { return currentIdentity }
