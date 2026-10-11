package xai

import (
	"os"
	"path/filepath"
	"testing"
)

// A malformed sibling must not invalidate an otherwise usable credential.
func TestRedTeamGrokSkipsWrongTypedCredentialEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unrelated scalar", `{"unrelated":17,"https://accounts.x.ai/sign-in":{"key":"fake-good"}}`},
		{"preferred numeric key", `{"https://auth.x.ai::bad":{"key":17},"https://accounts.x.ai/sign-in":{"key":"fake-good"}}`},
		{"preferred numeric expiry", `{"https://auth.x.ai::bad":{"key":"fake-bad","expires_at":17},"https://accounts.x.ai/sign-in":{"key":"fake-good"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			p := &Provider{grokHome: dir}
			got, err := p.grokCredentials()
			if err != nil {
				t.Fatalf("malformed sibling prevented usable fallback: %v", err)
			}
			if got == nil || got.AccessToken != "fake-good" {
				t.Fatalf("usable fallback was not selected: %#v", got)
			}
		})
	}
}
