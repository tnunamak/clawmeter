package provider

import (
	"regexp"
	"time"
)

// RawResponse is the last HTTP exchange that produced a provider reading. It
// exists so a user can compare what Clawmeter shows with what the provider
// sent. Body is stored verbatim; call RedactRaw before showing it.
type RawResponse struct {
	Status     int       `json:"http_status"`
	FetchedAt  time.Time `json:"fetched_at"`
	RetryAfter string    `json:"retry_after,omitempty"`
	Body       string    `json:"body"`
}

// RawReporter is implemented by providers that keep their last raw response.
type RawReporter interface {
	// LastRawResponse returns nil when the provider made no request.
	LastRawResponse() *RawResponse
}

var (
	rawEmail    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	rawUUID     = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	rawIdentKey = regexp.MustCompile(`(?i)("[a-z_]*(?:email|organization|account|org|user)[a-z_]*"\s*:\s*)"[^"]*"`)
)

// RedactRaw removes emails, UUIDs, and values of account/org/user-like keys
// from a raw JSON body. All other text, including key order, stays as sent.
func RedactRaw(body string) string {
	body = rawIdentKey.ReplaceAllString(body, `$1"[redacted]"`)
	body = rawEmail.ReplaceAllString(body, "[redacted-email]")
	return rawUUID.ReplaceAllString(body, "[redacted-id]")
}
