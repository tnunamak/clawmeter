//go:build windows

package shellpath

import "github.com/tnunamak/clawmeter/internal/provider"

func resolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	return resolveSessionEnvironmentWithRegistry(request, readCurrentUserEnv)
}

func resolveSessionEnvironmentWithRegistry(request provider.SessionEnvironmentRequest, lookup func(string) (string, bool)) map[string]string {
	values, _ := inheritedEnv(request.EnvNames)
	if !request.AllowSessionEnvironmentFallback {
		return values
	}
	for _, name := range missingEnvNames(request.EnvNames) {
		if value, ok := lookup(name); ok && value != "" {
			values[name] = value
		}
	}
	return values
}

func newSessionEnvironmentResolverWithRegistryLookup(lookup func(string) (string, bool)) provider.SessionEnvironmentResolver {
	return newSessionEnvironmentResolver(func(request provider.SessionEnvironmentRequest) map[string]string {
		return resolveSessionEnvironmentWithRegistry(request, lookup)
	})
}

func captureMissingEnvFromShell(_ string, _ []string) map[string]string {
	return nil
}
