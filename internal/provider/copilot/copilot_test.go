package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTransformUsageUsesOnlyProviderResetMetadata(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantReset time.Time
		wantWarn  string
	}{
		{"known snake case reset", `{"quota_snapshots":{"chat":{"percentRemaining":0}},"quota_reset_date":"2026-08-01T12:34:56.123Z"}`, time.Date(2026, 8, 1, 12, 34, 56, 123000000, time.UTC), ""},
		{"known camel case reset", `{"quotaSnapshots":{"chat":{"percentRemaining":50}},"quotaResetDate":"2026-08-02"}`, time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), ""},
		{"conflicting reset keys prefer camel case", `{"quotaSnapshots":{"chat":{"percentRemaining":50}},"quotaResetDate":"2026-08-03","quota_reset_date":"2026-08-04"}`, time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), ""},
		{"missing reset keeps usage", `{"quotaSnapshots":{"premiumInteractions":{"percentRemaining":25}}}`, time.Time{}, "Copilot quota reset date is not available; reset is unknown"},
		{"malformed reset stays unknown", `{"quotaSnapshots":{"chat":{"percentRemaining":100}},"quota_reset_date":"not-a-date"}`, time.Time{}, "Copilot returned an invalid quota reset date; reset is unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response userResponse
			if err := json.Unmarshal([]byte(tt.payload), &response); err != nil {
				t.Fatal(err)
			}
			data := (&Provider{}).transformUsage(&response)
			if len(data.Windows) != 1 || !data.Windows[0].ResetsAt.Equal(tt.wantReset) {
				t.Fatalf("windows = %#v, want reset %v", data.Windows, tt.wantReset)
			}
			if data.Warning != tt.wantWarn {
				t.Fatalf("warning = %q, want %q", data.Warning, tt.wantWarn)
			}
		})
	}
}

func TestTransformUsagePreservesZeroAndIgnoresUnknownSnapshots(t *testing.T) {
	var response userResponse
	if err := json.Unmarshal([]byte(`{"quotaSnapshots":{"premiumInteractions":{"percentRemaining":100},"unknown":{"percentRemaining":-20}}}`), &response); err != nil {
		t.Fatal(err)
	}
	data := (&Provider{}).transformUsage(&response)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 {
		t.Fatalf("windows = %#v, want one zero-utilization premium window", data.Windows)
	}
	if data.Windows[0].ResetsAt.IsZero() == false {
		t.Fatal("zero-value snapshot unexpectedly received a reset")
	}
}

func TestTransformUsageDoesNotTurnMissingPercentageIntoExhaustion(t *testing.T) {
	for _, payload := range []string{
		`{"quotaSnapshots":{"chat":{}}}`,
		`{"quotaSnapshots":{"chat":{"percentRemaining":null}}}`,
	} {
		var response userResponse
		if err := json.Unmarshal([]byte(payload), &response); err != nil {
			t.Fatal(err)
		}
		data := (&Provider{}).transformUsage(&response)
		if len(data.Windows) != 0 || data.Error == "" {
			t.Fatalf("data = %#v, want unavailable usage", data)
		}
	}
}

func TestFetchUsageHTTPStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "token fixture-token" {
					t.Errorf("authorization = %q", got)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("fixture-token must not appear in errors"))
			}))
			defer server.Close()

			data, err := (&Provider{}).fetchUsage(context.Background(), server.Client(), server.URL, "fixture-token")
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("got data=%#v err=%v, want expired data", data, err)
				}
				return
			}
			if err == nil || data != nil || strings.Contains(err.Error(), "fixture-token") {
				t.Fatalf("got data=%#v err=%v, want redacted error", data, err)
			}
		})
	}
}
