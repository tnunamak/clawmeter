package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLocalModelStatusNewest(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	models := []LocalModelUsage{
		{Model: "old", LimitHitAt: at.Add(-2 * time.Hour)},
		{Model: "new", Label: "New model", LimitHitAt: at.Add(-time.Hour)},
		{Model: "never"},
	}
	got := SummarizeLocalModelStatus(models, at)
	if got.LatestLimitModel != "new" || got.LatestLimitLabel != "New model" ||
		!got.LatestLimitAt.Equal(at.Add(-time.Hour)) ||
		got.Text != "Latest recorded model limit: New model · Oct 29 11:00 UTC · remaining quota and reset unknown" ||
		got.RemainingQuotaKnown || got.ResetKnown {
		t.Fatalf("unexpected summary: %+v", got)
	}
}

func TestLocalModelStatusTies(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	models := []LocalModelUsage{
		{Model: "z", Label: "First label", LimitHitAt: at},
		{Model: "a", Label: "Z label", LimitHitAt: at},
		{Model: "m", Label: "A label", LimitHitAt: at},
	}
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		input := []LocalModelUsage{models[order[0]], models[order[1]], models[order[2]]}
		got := SummarizeLocalModelStatus(input, at)
		if got.LatestLimitModel != "a" || got.LatestLimitLabel != "Z label" {
			t.Fatalf("tie depended on input order %v: %+v", order, got)
		}
	}
}

func TestLocalModelStatusActivityIsNotQuota(t *testing.T) {
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	zero, positive := int64(0), int64(99)
	cases := [][]LocalModelUsage{
		nil, {}, {{Model: "nil"}},
		{{Model: "zero", Day: "2026-10-30", AssistantMessages: &zero}},
		{{Model: "used", Day: "2026-10-29", AssistantMessages: &positive, InputTokens: &positive, OutputTokens: &positive}},
		{{Model: "hint", RetryAt: at.Add(-time.Minute), ResetAt: at.Add(-time.Minute)}},
	}
	for i, models := range cases {
		got := SummarizeLocalModelStatus(models, at)
		if got != (LocalModelStatus{Text: "Remaining quota not reported · reset unknown"}) {
			t.Fatalf("case %d inferred capacity: %+v", i, got)
		}
	}
}

func TestLocalModelStatusFutureIgnored(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	future := LocalModelUsage{Model: "future", LimitHitAt: at.Add(time.Nanosecond)}
	if got := SummarizeLocalModelStatus([]LocalModelUsage{future}, at); !got.LatestLimitAt.IsZero() {
		t.Fatalf("future observation accepted: %+v", got)
	}
	got := SummarizeLocalModelStatus([]LocalModelUsage{future, {Model: "present", LimitHitAt: at}}, at)
	if got.LatestLimitModel != "present" {
		t.Fatalf("inclusive current-time event lost: %+v", got)
	}
}

func TestLocalModelStatusOldEventAndRetry(t *testing.T) {
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	when := time.Date(2026, 10, 27, 23, 4, 0, 0, time.FixedZone("local", -5*60*60))
	got := SummarizeLocalModelStatus([]LocalModelUsage{{
		Model: "old", Label: "Old model", Day: "2026-10-27",
		LimitHitAt: when, RetryAt: at.Add(-time.Hour), ResetAt: at.Add(-time.Hour),
	}}, at)
	if got.Text != "Latest recorded model limit: Old model · Oct 28 04:04 UTC · remaining quota and reset unknown" ||
		got.RemainingQuotaKnown || got.ResetKnown || got.LatestLimitAt.Location() != time.UTC {
		t.Fatalf("old observation became reset/availability: %+v", got)
	}
}

func TestLocalModelStatusLabelFallback(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	got := SummarizeLocalModelStatus([]LocalModelUsage{{Model: "model-id", LimitHitAt: at}}, at)
	if got.LatestLimitLabel != "model-id" ||
		got.Text != "Latest recorded model limit: model-id · Oct 29 12:00 UTC · remaining quota and reset unknown" {
		t.Fatalf("missing label fallback: %+v", got)
	}
}

func TestLocalModelStatusDoesNotMutate(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	count := int64(7)
	input := []LocalModelUsage{{Model: "z", AssistantMessages: &count, LimitHitAt: at},
		{Model: "a", InputTokens: &count, LimitHitAt: at}}
	before := append([]LocalModelUsage(nil), input...)
	rawBefore, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	SummarizeLocalModelStatus(input, at)
	rawAfter, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, before) || string(rawBefore) != string(rawAfter) ||
		input[0].AssistantMessages != &count || input[1].InputTokens != &count || count != 7 {
		t.Fatal("input, order or counter pointers changed")
	}
}

func TestLocalModelStatusJSONUnknown(t *testing.T) {
	got := SummarizeLocalModelStatus(nil, time.Time{})
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["remaining_quota_known"] != false || fields["reset_known"] != false ||
		fields["text"] != "Remaining quota not reported · reset unknown" {
		t.Fatalf("unknown booleans missing or changed: %s", data)
	}
	for _, name := range []string{"latest_limit_model", "latest_limit_label", "latest_limit_at"} {
		if _, exists := fields[name]; exists {
			t.Fatalf("empty event leaked %s: %s", name, data)
		}
	}
}

func TestLocalModelStatusSanitizesDisplayLabel(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	limit := at.Add(-time.Minute)
	for _, tc := range []struct {
		name  string
		model LocalModelUsage
		want  string // the label as the status line shows it
	}{
		{"escape", LocalModelUsage{Model: "model-a", Label: "Model A\x1b[2JFORGED", LimitHitAt: limit}, `Model A\u001b[2JFORGED`},
		{"carriage return", LocalModelUsage{Model: "model-a", Label: "Model A\rFORGED", LimitHitAt: limit}, `Model A\u000dFORGED`},
		{"line feed", LocalModelUsage{Model: "model-a", Label: "Model A\nFORGED", LimitHitAt: limit}, `Model A\u000aFORGED`},
		{"tab", LocalModelUsage{Model: "model-a", Label: "Model A\tFORGED", LimitHitAt: limit}, `Model A\u0009FORGED`},
		{"delete", LocalModelUsage{Model: "model-a", Label: "Model A\x7fFORGED", LimitHitAt: limit}, `Model A\u007fFORGED`},
		{"c1 control", LocalModelUsage{Model: "model-a", Label: "Model A" + string(rune(0x9b)) + "FORGED", LimitHitAt: limit}, "Model A" + escapedRune(0x9b) + "FORGED"},
		{"line separator", LocalModelUsage{Model: "model-a", Label: "Model A" + string(rune(0x2028)) + "FORGED", LimitHitAt: limit}, "Model A" + escapedRune(0x2028) + "FORGED"},
		{"paragraph separator", LocalModelUsage{Model: "model-a", Label: "Model A" + string(rune(0x2029)) + "FORGED", LimitHitAt: limit}, "Model A" + escapedRune(0x2029) + "FORGED"},
		{"model fallback", LocalModelUsage{Model: "model-a\x1b[2J\rFORGED", LimitHitAt: limit}, `model-a\u001b[2J\u000dFORGED`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SummarizeLocalModelStatus([]LocalModelUsage{tc.model}, at)
			prefix := "Latest recorded model limit: " + tc.want + " · Oct 29 11:59 UTC · "
			if !strings.HasPrefix(got.Text, prefix) {
				t.Fatalf("text = %q, want prefix %q", got.Text, prefix)
			}
			if strings.ContainsFunc(got.Text, breaksLine) {
				t.Fatalf("text is not one safe line: %q", got.Text)
			}
			raw := tc.model.Label
			if raw == "" {
				raw = tc.model.Model
			}
			if got.LatestLimitLabel != raw {
				t.Fatalf("typed label = %q, want raw %q", got.LatestLimitLabel, raw)
			}
		})
	}
}

func TestLocalModelStatusDisplayLabelIsBounded(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("ü", 60)
	got := SummarizeLocalModelStatus([]LocalModelUsage{{Model: "model-a", Label: long, LimitHitAt: at}}, at)
	want := "Latest recorded model limit: " + strings.Repeat("ü", 37) + "... · Oct 29 12:00 UTC · remaining quota and reset unknown"
	if got.Text != want || got.LatestLimitLabel != long {
		t.Fatalf("text = %q, typed label kept = %v", got.Text, got.LatestLimitLabel == long)
	}
}

func TestLocalModelStatusJSONIsAdditive(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	got := SummarizeLocalModelStatus([]LocalModelUsage{{Model: "model-a", Label: "Model A", LimitHitAt: at.Add(-time.Minute)}}, at)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"text":"Latest recorded model limit: Model A · Oct 29 11:59 UTC · remaining quota and reset unknown","remaining_quota_known":false,"reset_known":false,"latest_limit_model":"model-a","latest_limit_label":"Model A","latest_limit_at":"2026-10-29T11:59:00Z"}`
	if string(data) != want {
		t.Fatalf("JSON changed:\n got %s\nwant %s", data, want)
	}
}
