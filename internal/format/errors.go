package format

import "strings"

// HumanizeError converts raw Go error strings into short, human-readable messages.
// It strips URL noise, Go error wrapping chains, and maps common patterns to
// actionable messages. Errors that are already human-readable pass through unchanged.
func HumanizeError(errMsg string) string {
	if errMsg == "" {
		return errMsg
	}

	// First, try to extract the core message from Go error chains.
	// Pattern: "request failed: Get \"https://...\": actual error"
	core := unwrapErrorChain(errMsg)

	// Map known patterns to human-friendly messages.
	lowered := strings.ToLower(core)

	// Also check against the full original message for patterns that may
	// appear in the wrapping prefix (e.g., "API returned 401: invalid token").
	fullLowered := strings.ToLower(errMsg)

	switch {
	case strings.Contains(lowered, "authentication required to read rate limits"):
		return "rate limits unavailable — check plan at platform.openai.com"
	case strings.Contains(lowered, "context deadline exceeded") || strings.Contains(lowered, "client.timeout"):
		return "connection timed out"
	case strings.Contains(lowered, "connection refused"):
		return "connection refused"
	case strings.Contains(lowered, "no such host"):
		return "DNS lookup failed"
	case strings.Contains(lowered, "certificate") || strings.Contains(lowered, "x509"):
		return "TLS certificate error"
	case strings.Contains(lowered, "rate limited") || strings.Contains(lowered, "429"):
		return "rate limited"
	case strings.Contains(fullLowered, "unauthorized") || strings.Contains(fullLowered, "401"):
		// Already actionable — keep the full message for context.
		return truncate(errMsg, 80)
	case strings.Contains(fullLowered, "forbidden") || strings.Contains(fullLowered, "403"):
		return truncate(errMsg, 80)
	}

	// Strip remaining URLs if present.
	if strings.Contains(core, "https://") || strings.Contains(core, "http://") {
		core = stripURLs(core)
		core = strings.TrimSpace(core)
		if core == "" {
			// Edge case: the entire message was a URL.
			return truncate(errMsg, 80)
		}
	}

	return truncate(core, 80)
}

// unwrapErrorChain extracts the innermost error message from a Go-style
// error wrapping chain like "outer: middle: inner".
// It skips segments that are just URL-laden Go HTTP client noise.
func unwrapErrorChain(errMsg string) string {
	// Split on ": " to find segments.
	parts := strings.Split(errMsg, ": ")
	if len(parts) <= 1 {
		return errMsg
	}

	// Walk from the end to find the first meaningful segment.
	// Skip segments that are just Go HTTP method+URL fragments like:
	//   Get "https://api.anthropic.com/..."
	for i := len(parts) - 1; i >= 0; i-- {
		segment := strings.TrimSpace(parts[i])
		// Remove surrounding quotes from error wrapping.
		segment = strings.Trim(segment, "\"")

		if segment == "" {
			continue
		}

		// Skip segments that are HTTP method + URL (e.g., Get "https://...")
		if isHTTPMethodURL(segment) {
			continue
		}

		// Skip segments that are just bare URLs.
		if strings.HasPrefix(segment, "https://") || strings.HasPrefix(segment, "http://") {
			continue
		}

		return segment
	}

	// Fallback: return the last segment.
	return strings.TrimSpace(parts[len(parts)-1])
}

// isHTTPMethodURL checks if a string looks like `Get "https://..."` or similar.
func isHTTPMethodURL(s string) bool {
	methods := []string{"Get ", "Post ", "Put ", "Delete ", "Patch ", "Head "}
	for _, m := range methods {
		if strings.HasPrefix(s, m) {
			return true
		}
	}
	return false
}

// stripURLs removes https:// and http:// URLs from a string.
func stripURLs(s string) string {
	words := strings.Fields(s)
	var kept []string
	for _, w := range words {
		// Strip surrounding quotes/parens from URLs.
		clean := strings.Trim(w, "\"'()")
		if strings.HasPrefix(clean, "https://") || strings.HasPrefix(clean, "http://") {
			continue
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, " ")
}

// truncate limits a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
