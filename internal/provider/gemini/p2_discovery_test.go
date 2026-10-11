package gemini

import (
	"os"
	"path/filepath"
	"testing"
)

func TestP2NpmShimOAuthDiscovery(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "npm")
	writeGeminiShim(t, bin)
	oauthPath := npmOAuthPath(bin)
	writeOAuthFixture(t, oauthPath)

	t.Setenv("PATH", bin)
	t.Setenv("APPDATA", filepath.Join(root, "unrelated-appdata"))
	assertDiscoveredOAuth(t)
}

func TestP2AppDataNpmOAuthDiscovery(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "other-bin")
	writeGeminiShim(t, bin)
	appData := filepath.Join(root, "appdata")
	if err := os.MkdirAll(appData, 0700); err != nil {
		t.Fatal(err)
	}
	writeOAuthFixture(t, npmOAuthPath(filepath.Join(appData, "npm")))

	t.Setenv("PATH", bin)
	t.Setenv("APPDATA", appData)
	assertDiscoveredOAuth(t)
}

func writeGeminiShim(t *testing.T, bin string) {
	t.Helper()
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gemini"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
}

func npmOAuthPath(root string) string {
	return filepath.Join(root, "node_modules", "@google", "gemini-cli", "node_modules", "@google", "gemini-cli-core", "dist", "src", "code_assist", "oauth2.js")
}

func writeOAuthFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("OAUTH_CLIENT_ID = 'synthetic-id'; OAUTH_CLIENT_SECRET = 'synthetic-secret';"), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertDiscoveredOAuth(t *testing.T) {
	t.Helper()
	got, err := discoverOAuthCredentials()
	if err != nil {
		t.Fatalf("npm shim discovery: %v", err)
	}
	if got.clientID != "synthetic-id" || got.clientSecret != "synthetic-secret" {
		t.Fatal("wrong OAuth fixture")
	}
}
