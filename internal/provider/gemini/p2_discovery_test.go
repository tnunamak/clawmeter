package gemini

import (
	"os"
	"path/filepath"
	"testing"
)

func TestP2NpmShimOAuthDiscovery(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "npm")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gemini"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "node_modules", "@google", "gemini-cli", "node_modules", "@google", "gemini-cli-core", "dist", "src", "code_assist", "oauth2.js")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("OAUTH_CLIENT_ID = 'synthetic-id'; OAUTH_CLIENT_SECRET = 'synthetic-secret';"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("APPDATA", root)
	got, err := discoverOAuthCredentials()
	if err != nil {
		t.Fatalf("npm shim discovery: %v", err)
	}
	if got.clientID != "synthetic-id" || got.clientSecret != "synthetic-secret" {
		t.Fatal("wrong OAuth fixture")
	}
}
