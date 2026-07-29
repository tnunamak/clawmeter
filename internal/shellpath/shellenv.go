package shellpath

import (
	"sort"
	"strings"
	"sync"

	"github.com/tnunamak/clawmeter/internal/provider"
)

type sessionEnvironmentCacheKey struct {
	names    string
	fallback bool
}

type sessionEnvironmentResolver struct {
	mu       sync.Mutex
	cache    map[sessionEnvironmentCacheKey]map[string]string
	uncached func(provider.SessionEnvironmentRequest) map[string]string
}

// NewSessionEnvironmentResolver creates a lazy, process-local resolver.
// It caches values and misses without mutating or logging the environment.
func NewSessionEnvironmentResolver() provider.SessionEnvironmentResolver {
	return newSessionEnvironmentResolver(resolveSessionEnvironment)
}

func newSessionEnvironmentResolver(uncached func(provider.SessionEnvironmentRequest) map[string]string) provider.SessionEnvironmentResolver {
	return &sessionEnvironmentResolver{cache: make(map[sessionEnvironmentCacheKey]map[string]string), uncached: uncached}
}

func (r *sessionEnvironmentResolver) ResolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	names := canonicalEnvNames(request.EnvNames)
	key := sessionEnvironmentCacheKey{names: strings.Join(names, "\x00"), fallback: request.AllowSessionEnvironmentFallback}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[key]; ok {
		return cloneSessionEnvironmentValues(cached)
	}
	request.EnvNames = names
	values := r.uncached(request)
	r.cache[key] = cloneSessionEnvironmentValues(values)
	return cloneSessionEnvironmentValues(values)
}

func canonicalEnvNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	canonical := make([]string, 0, len(names))
	for _, name := range names {
		if !validEnvName(name) || seen[name] {
			continue
		}
		seen[name] = true
		canonical = append(canonical, name)
	}
	sort.Strings(canonical)
	return canonical
}

func cloneSessionEnvironmentValues(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for name, value := range values {
		clone[name] = value
	}
	return clone
}
