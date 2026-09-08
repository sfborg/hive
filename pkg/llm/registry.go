package llm

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds instantiated Providers keyed by name. Callers look
// up a Provider by the name the curator gave it in config (e.g.
// "anthropic-main"), then invoke it via [Invoke]. The registry is
// the runtime lookup structure; persistence lives in pkg/config
// (Config.LLMProviders).
//
// Concurrent-safe. Register happens once at startup, Get can be
// called from any request goroutine after.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry. Callers Register providers
// after constructing them from config entries.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds p under p.Name(). Overwrites any existing entry
// with the same name — the last-registered wins, matching how
// config-load treats duplicates (later entries shadow earlier).
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get returns the Provider registered under name.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("llm: no provider registered as %q", name)
	}
	return p, nil
}

// Names returns the sorted list of registered provider names. Used
// by the WUI dropdown, the /api/llm/providers endpoint, and CLI
// listings — anywhere a caller needs to enumerate the current
// choice set.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
