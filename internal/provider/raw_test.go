package provider

import (
	"strings"
	"testing"
)

func TestRedactRawRemovesIdentityButKeepsData(t *testing.T) {
	body := `{"five_hour":{"utilization":7},"account":{"email_address":"a@b.com","uuid":"123e4567-e89b-12d3-a456-426614174000"},"organization_name":"Acme","note":"mail me at x@y.org","extra_usage":{"monthly_limit":50000}}`
	got := RedactRaw(body)
	for _, leak := range []string{"a@b.com", "x@y.org", "123e4567", "Acme"} {
		if strings.Contains(got, leak) {
			t.Fatalf("leaked %q in %s", leak, got)
		}
	}
	for _, keep := range []string{`"utilization":7`, `"monthly_limit":50000`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("lost %q in %s", keep, got)
		}
	}
}
