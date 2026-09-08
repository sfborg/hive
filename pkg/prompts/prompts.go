// Package prompts embeds the prompt library hive ships with. Every
// LLM-driven extraction path — NER tagging, assembly composition,
// future translation — loads its prompt from here rather than reading
// arbitrary files at runtime, so the shipped binary always carries a
// version-tagged prompt reachable via a stable name.
//
// Naming convention: "<name>_v<version>.md" (e.g., "ner_v5.md",
// "assembly_v1.md"). The version suffix is stored on every generated
// assertion (hive__assertion.prompt_version) so the training-corpus
// origin is traceable when prompts evolve — see the LLM-NER-bootstrap
// project memory for the provenance rationale.
//
// New prompts land here as new files; existing prompts are never
// edited in place. Bump the version suffix and add a new file so
// assertions produced under the old prompt keep matching their
// version tag.
package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed *.md
var promptFS embed.FS

// Load returns the prompt text for the given prompt name. The name
// includes the version suffix but not the .md extension: pass
// "ner_v5", not "ner_v5.md". Returns an error when no matching
// prompt is embedded, letting callers fail loudly rather than fall
// back to a silent default.
//
// A leading YAML-frontmatter block (delimited by "---" on its own
// line) is stripped before the prompt is returned. Frontmatter is
// documentation for humans reading the .md file directly — it
// records the invocation-envelope contract the caller applies to
// the model's output. Stripping keeps that documentation from
// leaking into the model's context.
func Load(name string) (string, error) {
	data, err := promptFS.ReadFile(name + ".md")
	if err != nil {
		return "", fmt.Errorf("prompts: load %q: %w", name, err)
	}
	return stripFrontmatter(string(data)), nil
}

// stripFrontmatter removes a leading "---\n...\n---\n" block when
// present. Malformed frontmatter (opening delimiter with no closing
// one) returns the input unchanged so a typo can't silently gut a
// prompt.
func stripFrontmatter(s string) string {
	const openDelim = "---\n"
	const closeDelim = "\n---\n"
	if !strings.HasPrefix(s, openDelim) {
		return s
	}
	_, body, found := strings.Cut(s[len(openDelim):], closeDelim)
	if !found {
		return s
	}
	return strings.TrimLeft(body, "\n")
}

// Available returns every embedded prompt name (without the .md
// extension), sorted lexically. Callers use this to render "which
// prompt is active" selectors and to enumerate the shipped set for
// documentation / debugging endpoints.
func Available() ([]string, error) {
	entries, err := fs.ReadDir(promptFS, ".")
	if err != nil {
		return nil, fmt.Errorf("prompts: list: %w", err)
	}
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			out = append(out, name)
		}
	}
	return out, nil
}
