package provider

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// HTTPError preserves an upstream status without retaining a response body.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string       { return "http " + strconv.Itoa(e.StatusCode) }
func (e *HTTPError) HTTPStatusCode() int { return e.StatusCode }

// Legacy providers store errors as strings. Only an explicit HTTP/status prefix
// can supply a status; bare digits and port numbers cannot.
var httpErrorStatus = regexp.MustCompile(`(?i)\b(?:http(?:/\d(?:\.\d)?)?|api returned|refresh returned|token refresh returned|unexpected status)\s*[:=]?\s*([1-5][0-9]{2})\b`)

// ClassifyError is shared by cache sanitization, diagnostics, and display.
// Typed status takes precedence; text is retained for legacy provider errors.
func ClassifyError(err error) string {
	if err == nil {
		return "unknown"
	}
	var limited *RateLimitError
	if errors.As(err, &limited) {
		return "rate_limited"
	}
	code := 0
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		code = status.HTTPStatusCode()
	}
	if code < 100 || code > 599 {
		code = 0
		if match := httpErrorStatus.FindStringSubmatch(err.Error()); match != nil {
			code, _ = strconv.Atoi(match[1])
		}
	}
	switch code {
	case 401, 403:
		return "auth"
	case 429:
		return "rate_limited"
	default:
		if code >= 100 && code <= 599 {
			return "api"
		}
	}
	var network net.Error
	if errors.As(err, &network) {
		return "network"
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "rate limit"):
		return "rate_limited"
	case strings.Contains(lower, "unauthor"), strings.Contains(lower, "forbidden"), strings.Contains(lower, "expired"), strings.Contains(lower, "credential"), strings.Contains(lower, "token"):
		return "auth"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"), strings.Contains(lower, "connection"), strings.Contains(lower, "network"), strings.Contains(lower, "no such host"), strings.Contains(lower, "dns"), strings.Contains(lower, "no response"), strings.Contains(lower, "eof"), strings.Contains(lower, "context canceled"):
		return "network"
	case strings.Contains(lower, "decode"), strings.Contains(lower, "parse"), strings.Contains(lower, "malformed"), strings.Contains(lower, "missing"), strings.Contains(lower, "invalid character"):
		return "parse"
	case code != 0, strings.Contains(lower, "api"), strings.Contains(lower, "http"), strings.Contains(lower, "server"):
		return "api"
	default:
		return "unknown"
	}
}
