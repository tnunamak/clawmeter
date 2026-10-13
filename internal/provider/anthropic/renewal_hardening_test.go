package anthropic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

// A damaged cooldown state file (for example zero bytes after a crash between
// CreateTemp and Rename without fsync) must not disable renewal for this source forever.
func TestCorruptCooldownStateDoesNotDisableRenewal(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "garbage": "{not json"} {
		t.Run(name, func(t *testing.T) {
			p, now, _, _ := renewalTestProvider(t)
			creds, err := p.readCredentials()
			if err != nil {
				t.Fatal(err)
			}
			path, err := renewalStatePath(creds)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			doctorCalls := 0
			doctorRunner = func(context.Context, string, []string) error {
				doctorCalls++
				writeRenewalLogin(t, p.configDir, "new-access", "new-refresh", now.Add(time.Hour))
				return nil
			}
			// A day later, renewal is still disabled.
			*now = now.Add(24 * time.Hour)
			writeRenewalLogin(t, p.configDir, "expired-access", "synthetic-refresh", now.Add(-time.Hour))
			data, err := p.FetchUsage(context.Background())
			if err != nil || data == nil {
				t.Fatalf("FetchUsage: %#v %v", data, err)
			}
			if doctorCalls != 1 || data.IsExpired {
				t.Fatalf("corrupt cooldown state disabled renewal: doctor=%d expired=%v", doctorCalls, data.IsExpired)
			}
		})
	}
}

// An attempt recorded while the clock was ahead (RTC skew later fixed by NTP)
// must not block renewal for longer than the cooldown.
func TestFutureCooldownTimestampDoesNotBlockBeyondCooldown(t *testing.T) {
	p, now, _, _ := renewalTestProvider(t)
	creds, err := p.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	path, err := renewalStatePath(creds)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRenewalAttempt(path, renewalAttempt{AttemptedAt: now.Add(24 * time.Hour), Fingerprint: loginFingerprint(creds)}); err != nil {
		t.Fatal(err)
	}
	doctorCalls := 0
	doctorRunner = func(context.Context, string, []string) error { doctorCalls++; return nil }
	*now = now.Add(renewalCooldown + time.Minute)
	assertExpiredLogin(t, p)
	if doctorCalls != 1 {
		t.Fatalf("future-dated attempt blocks renewal for ~24h: doctor=%d", doctorCalls)
	}
}

// On macOS the unsuffixed "Claude Code-credentials" Keychain item belongs to the
// profile Claude Code uses when CLAUDE_CONFIG_DIR is unset; with CLAUDE_CONFIG_DIR
// set, Claude Code uses a hashed service name. The non-pinned default source reads
// the unsuffixed item but launches doctor with the ambient CLAUDE_CONFIG_DIR, so
// doctor renews a different profile than the one Clawmeter re-reads.
func TestKeychainDefaultDoctorUsesKeychainProfile(t *testing.T) {
	renewalTestProvider(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	oldRead := keychainRead
	t.Cleanup(func() { keychainRead = oldRead })
	keychainRead = func() ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"expired-keychain","refreshToken":"synthetic-refresh","expiresAt":1}}`), nil
	}
	p := New(config.ProviderConfig{})
	old, err := p.readKeychain()
	if err != nil || !old.fromKeychain {
		t.Fatalf("readKeychain: %v", err)
	}
	var got []string
	doctorRunner = func(_ context.Context, _ string, env []string) error { got = env; return nil }
	p.renewLogin(context.Background(), old)
	if got == nil {
		t.Fatal("doctor not invoked")
	}
	if v := environmentValue(got, "CLAUDE_CONFIG_DIR"); v != "" {
		t.Fatalf("doctor for the unsuffixed Keychain login ran with CLAUDE_CONFIG_DIR=%q (renews another profile)", v)
	}
}

func TestRenewalBackoffDoublesAndCaps(t *testing.T) {
	for failures, want := range map[int]time.Duration{
		0: renewalCooldown, 1: renewalCooldown, 2: 2 * renewalCooldown, 3: 4 * renewalCooldown, 20: maxRenewalBackoff,
	} {
		if got := (renewalAttempt{Failures: failures}).backoff(); got != want {
			t.Errorf("backoff(%d failures) = %v, want %v", failures, got, want)
		}
	}
	now := time.Now()
	a := renewalAttempt{AttemptedAt: now.Add(-6 * time.Minute), Fingerprint: "f", Failures: 2}
	if !a.coolingDown("f", now) {
		t.Fatal("second failure should cool down for 10 minutes")
	}
	if a.coolingDown("g", now) {
		t.Fatal("a changed login must bypass the cooldown")
	}
}
