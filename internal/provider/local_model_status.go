package provider

import "time"

// LocalModelStatus summarizes dated local evidence, never an allowance or recovery.
type LocalModelStatus struct {
	Text                string    `json:"text"`
	RemainingQuotaKnown bool      `json:"remaining_quota_known"`
	ResetKnown          bool      `json:"reset_known"`
	LatestLimitModel    string    `json:"latest_limit_model,omitempty"`
	LatestLimitLabel    string    `json:"latest_limit_label,omitempty"`
	LatestLimitAt       time.Time `json:"latest_limit_at,omitzero"`
}

// SummarizeLocalModelStatus is pure. Activity and retry hints cannot establish quota.
func SummarizeLocalModelStatus(models []LocalModelUsage, at time.Time) LocalModelStatus {
	status := LocalModelStatus{Text: "Remaining quota not reported · reset unknown"}
	for _, model := range models {
		when := model.LimitHitAt
		if when.IsZero() || when.After(at) {
			continue
		}
		label := model.Label
		if label == "" {
			label = model.Model
		}
		replace := status.LatestLimitAt.IsZero() || when.After(status.LatestLimitAt)
		if when.Equal(status.LatestLimitAt) {
			replace = model.Model < status.LatestLimitModel ||
				(model.Model == status.LatestLimitModel && label < status.LatestLimitLabel)
		}
		if replace {
			status.LatestLimitModel = model.Model
			status.LatestLimitLabel = label
			status.LatestLimitAt = when.UTC()
		}
	}
	if !status.LatestLimitAt.IsZero() {
		status.Text = "Latest recorded model limit: " + status.LatestLimitLabel + " · " +
			status.LatestLimitAt.Format("Jan 2 15:04 UTC") + " · remaining quota and reset unknown"
	}
	return status
}
