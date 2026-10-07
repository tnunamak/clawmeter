package shellpath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func (r *sessionEnvironmentResolver) ResolveSessionExecutable(name string) (string, error) {
	Init()
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("executable name must not contain a path")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(home, ".local", "bin", name)
	if runtime.GOOS == "windows" {
		candidate += ".exe"
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("executable %q not found", name)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("executable %q is not executable", name)
	}
	return candidate, nil
}

type sessionEnvironmentCacheKey struct {
	name     string
	fallback bool
}

type sessionEnvironmentCacheEntry struct {
	value     string
	found     bool
	expiresAt time.Time
}

// sessionEnvironmentCacheTTL bounds how long a login-shell observation (value
// or miss) is reused. Each shell recovery runs the user's rc files, which costs
// over a second of CPU on a typical zsh setup, so the TTL must be much longer
// than the tray poll interval.
const sessionEnvironmentCacheTTL = 10 * time.Minute

type sessionEnvironmentResolver struct {
	mu       sync.Mutex
	cache    map[sessionEnvironmentCacheKey]sessionEnvironmentCacheEntry
	uncached func(provider.SessionEnvironmentRequest) map[string]string
	now      func() time.Time
}

// NewSessionEnvironmentResolver creates a lazy, process-local resolver.
// It caches values and misses without mutating or logging the environment.
func NewSessionEnvironmentResolver() provider.SessionEnvironmentResolver {
	return newSessionEnvironmentResolver(resolveSessionEnvironment)
}

func newSessionEnvironmentResolver(uncached func(provider.SessionEnvironmentRequest) map[string]string) provider.SessionEnvironmentResolver {
	return &sessionEnvironmentResolver{
		cache: make(map[sessionEnvironmentCacheKey]sessionEnvironmentCacheEntry), uncached: uncached, now: time.Now,
	}
}

// ResolveSessionEnvironment caches per variable name. When any requested name
// is stale, every other cached name with the same fallback policy that is also
// stale is refreshed in the same resolution, so one login shell serves all
// providers instead of one shell per distinct name set.
func (r *sessionEnvironmentResolver) ResolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	names := canonicalEnvNames(request.EnvNames)
	fallback := request.AllowSessionEnvironmentFallback
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	fresh := func(name string) bool {
		entry, ok := r.cache[sessionEnvironmentCacheKey{name: name, fallback: fallback}]
		return ok && now.Before(entry.expiresAt)
	}
	stale := false
	for _, name := range names {
		if !fresh(name) {
			stale = true
			break
		}
	}
	if stale {
		refresh := append([]string(nil), names...)
		for key := range r.cache {
			if key.fallback == fallback && !fresh(key.name) {
				refresh = append(refresh, key.name)
			}
		}
		refresh = canonicalEnvNames(refresh)
		request.EnvNames = refresh
		values := r.uncached(request)
		for _, name := range refresh {
			value, found := values[name]
			r.cache[sessionEnvironmentCacheKey{name: name, fallback: fallback}] = sessionEnvironmentCacheEntry{
				value: value, found: found, expiresAt: now.Add(sessionEnvironmentCacheTTL),
			}
		}
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		if entry := r.cache[sessionEnvironmentCacheKey{name: name, fallback: fallback}]; entry.found {
			out[name] = entry.value
		}
	}
	return out
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
