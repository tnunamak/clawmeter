package provider

import (
	"encoding/json"
	"reflect"
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
