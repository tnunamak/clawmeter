package format

import "testing"

func TestHumanizeError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "context deadline exceeded with URL wrapping",
			input: `request failed: Get "https://api.anthropic.com/api/oauth/usage": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			want:  "connection timed out",
		},
		{
			name:  "plain context deadline exceeded",
			input: "context deadline exceeded",
			want:  "connection timed out",
		},
		{
			name:  "Client.Timeout",
			input: "Client.Timeout exceeded",
			want:  "connection timed out",
		},
		{
			name:  "connection refused",
			input: `request failed: Get "https://api.example.com/v1/usage": dial tcp 127.0.0.1:443: connect: connection refused`,
			want:  "connection refused",
		},
		{
			name:  "DNS lookup failure",
			input: `request failed: Get "https://api.example.com/v1/usage": dial tcp: lookup api.example.com: no such host`,
			want:  "DNS lookup failed",
		},
		{
			name:  "TLS certificate error",
			input: `request failed: Get "https://api.example.com/v1": x509: certificate signed by unknown authority`,
			want:  "TLS certificate error",
		},
		{
			name:  "rate limited with 429",
			input: "rate limited (429)",
			want:  "rate limited",
		},
		{
			name:  "just 429",
			input: "HTTP 429 Too Many Requests",
			want:  "rate limited",
		},
		{
			name:  "codex rate limits unavailable",
			input: "chatgpt authentication required to read rate limits",
			want:  "rate limits unavailable — check plan at platform.openai.com",
		},
		{
			name:  "401 kept as-is",
			input: "API returned 401: invalid token",
			want:  "API returned 401: invalid token",
		},
		{
			name:  "forbidden kept as-is",
			input: "forbidden: insufficient permissions",
			want:  "forbidden: insufficient permissions",
		},
		{
			name:  "already human-readable short error",
			input: "token expired",
			want:  "token expired",
		},
		{
			name:  "URL stripped from generic error",
			input: `request failed: unexpected status 500 from https://api.example.com/v1/usage`,
			want:  "unexpected status 500 from",
		},
		{
			name:  "showing cached suffix preserved",
			input: "connection timed out (showing cached)",
			want:  "connection timed out (showing cached)",
		},
		{
			name:  "long error truncated to 80 chars",
			input: "this is a very long error message that goes on and on and on and on and on and on and on and we need to truncate it",
			want:  "this is a very long error message that goes on and on and on and on and on an...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HumanizeError(tt.input)
			if got != tt.want {
				t.Errorf("HumanizeError(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 80, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"this is longer than five", 5, "th..."},
		{"ab", 3, "ab"},
		{"abcd", 3, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestUnwrapErrorChain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple error",
			input: "something went wrong",
			want:  "something went wrong",
		},
		{
			name:  "wrapped with URL",
			input: `request failed: Get "https://api.example.com/v1": actual error here`,
			want:  "actual error here",
		},
		{
			name:  "deeply nested",
			input: `outer: middle: Get "https://example.com": inner error`,
			want:  "inner error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unwrapErrorChain(tt.input)
			if got != tt.want {
				t.Errorf("unwrapErrorChain(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
