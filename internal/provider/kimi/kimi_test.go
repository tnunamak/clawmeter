package kimi

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestIsConfigured_RequiresUsableKimiCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := New(config.ProviderConfig{})
	if p.IsConfigured() {
		t.Fatal("missing Kimi credentials should not be configured")
	}

	writeKimiCredentials(t, home, Credentials{
		AccessToken: "expired",
		ExpiresAt:   float64(time.Now().Add(-time.Hour).Unix()),
	})
	if p.IsConfigured() {
		t.Fatal("expired Kimi access token without refresh token should not be configured")
	}

	writeKimiCredentials(t, home, Credentials{
		AccessToken:  "expired",
		RefreshToken: "refresh",
		ExpiresAt:    float64(time.Now().Add(-time.Hour).Unix()),
	})
	if !p.IsConfigured() {
		t.Fatal("expired Kimi access token with refresh token should be configured")
	}
}

func writeKimiCredentials(t *testing.T, home string, creds Credentials) {
	t.Helper()
	dir := filepath.Join(home, ".kimi", "credentials")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"access_token":"` + creds.AccessToken + `","refresh_token":"` + creds.RefreshToken + `","expires_at":` + formatFloat(creds.ExpiresAt) + `}`)
	if err := os.WriteFile(filepath.Join(dir, "kimi-code.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
