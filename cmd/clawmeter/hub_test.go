package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

const validHubKey = "0123456789abcdef0123456789abcdef"

// runHubCmd runs hubCmd with stderr captured, so a test can tell which guard
// refused instead of only that something returned 1.
func runHubCmd(t *testing.T, args []string) (int, string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	previous := os.Stderr
	os.Stderr = writer
	code := hubCmd(args)
	os.Stderr = previous
	if err := writer.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	captured, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return code, string(captured)
}

func TestHubCmdRefusesWeakManagementKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"missing", ""},
		{"thirty one characters", strings.Repeat("k", 31)},
		{"one character", "k"},
		{"trailing space", validHubKey + " "},
		{"leading space", " " + validHubKey},
		{"surrounding newline", validHubKey + "\n"},
		{"surrounding tab", "\t" + validHubKey + "\t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(hubKeyEnv, c.key)
			code, stderr := runHubCmd(t, nil)
			if code != 1 {
				t.Fatalf("hubCmd exit code = %d, want 1 for key %q", code, c.key)
			}
			if !strings.Contains(stderr, hubKeyEnv) {
				t.Fatalf("stderr = %q, want the %s complaint", stderr, hubKeyEnv)
			}
		})
	}

	t.Run("exactly thirty two characters passes the key guard", func(t *testing.T) {
		// A valid key must not be rejected: the run must reach the listen
		// guard, which refuses this non-loopback plain-HTTP address.
		t.Setenv(hubKeyEnv, validHubKey)
		code, stderr := runHubCmd(t, []string{"--listen", "0.0.0.0:8318"})
		if code != 1 {
			t.Fatalf("hubCmd exit code = %d, want 1", code)
		}
		if strings.Contains(stderr, hubKeyEnv) {
			t.Fatalf("stderr = %q, want the key guard to accept a 32 character key", stderr)
		}
		if !strings.Contains(stderr, "non-loopback") {
			t.Fatalf("stderr = %q, want the non-loopback refusal", stderr)
		}
	})
}

func TestHubCmdRefusesPlainHTTPOnNonLoopbackListen(t *testing.T) {
	t.Setenv(hubKeyEnv, validHubKey)

	for _, listen := range []string{
		"0.0.0.0:8318",
		":8318",
		"192.168.1.20:8318",
		"10.0.0.5:8318",
		"example.com:8318",
	} {
		t.Run(listen, func(t *testing.T) {
			code, stderr := runHubCmd(t, []string{"--listen", listen})
			if code != 1 {
				t.Fatalf("hubCmd exit code = %d, want 1 for plain HTTP on %q", code, listen)
			}
			if !strings.Contains(stderr, "non-loopback") {
				t.Fatalf("stderr = %q, want the non-loopback refusal", stderr)
			}
		})
	}
}

func TestHubCmdRefusesHalfConfiguredTLS(t *testing.T) {
	t.Setenv(hubKeyEnv, validHubKey)

	t.Run("cert without key", func(t *testing.T) {
		code, stderr := runHubCmd(t, []string{"--tls-cert", "cert.pem"})
		if code != 1 {
			t.Fatalf("hubCmd exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr, "--tls-cert and --tls-key") {
			t.Fatalf("stderr = %q, want the TLS pairing complaint", stderr)
		}
	})

	t.Run("key without cert", func(t *testing.T) {
		code, stderr := runHubCmd(t, []string{"--tls-key", "key.pem"})
		if code != 1 {
			t.Fatalf("hubCmd exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr, "--tls-cert and --tls-key") {
			t.Fatalf("stderr = %q, want the TLS pairing complaint", stderr)
		}
	})
}

func TestHubCmdRefusesPositionalArguments(t *testing.T) {
	t.Setenv(hubKeyEnv, validHubKey)
	if code, _ := runHubCmd(t, []string{"serve"}); code != 1 {
		t.Fatalf("hubCmd exit code = %d, want 1", code)
	}
}

func TestIsLoopbackAddress(t *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{"127.0.0.1:8318", true},
		{"127.0.0.2:80", true},
		{"[::1]:8318", true},
		{"0.0.0.0:8318", false},
		{"[::]:8318", false},
		{"192.168.1.20:8318", false},
		{"10.0.0.5:8318", false},
		{"169.254.169.254:80", false},
		// Hostnames are not resolved, so they are refused conservatively.
		{"localhost:8318", false},
		{"example.com:8318", false},
		// A missing or malformed port is not a usable listen address.
		{"127.0.0.1", false},
		{":8318", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.address, func(t *testing.T) {
			if got := isLoopbackAddress(c.address); got != c.want {
				t.Fatalf("isLoopbackAddress(%q) = %v, want %v", c.address, got, c.want)
			}
		})
	}
}
