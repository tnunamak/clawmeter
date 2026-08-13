package shellpath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
	names    string
	fallback bool
}

type sessionEnvironmentCacheEntry struct {
	values    map[string]string
	expiresAt time.Time
}

const sessionEnvironmentCacheTTL = 30 * time.Second

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

func (r *sessionEnvironmentResolver) ResolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	names := canonicalEnvNames(request.EnvNames)
	key := sessionEnvironmentCacheKey{names: strings.Join(names, "\x00"), fallback: request.AllowSessionEnvironmentFallback}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if cached, ok := r.cache[key]; ok && now.Before(cached.expiresAt) {
		return cloneSessionEnvironmentValues(cached.values)
	}
	request.EnvNames = names
	values := r.uncached(request)
	r.cache[key] = sessionEnvironmentCacheEntry{
		values: cloneSessionEnvironmentValues(values), expiresAt: now.Add(sessionEnvironmentCacheTTL),
	}
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
