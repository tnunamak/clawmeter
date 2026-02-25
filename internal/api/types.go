package api

import "time"

type OAuthCredentials struct {
	ClaudeAiOauth struct {
		AccessToken      string   `json:"accessToken"`
		RefreshToken     string   `json:"refreshToken"`
		ExpiresAt        int64    `json:"expiresAt"`
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
		RateLimitTier    string   `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`

	tokenOnly string // set when credentials come from env var or raw keychain value
}

type UsageResponse struct {
	FiveHour UsageWindow `json:"five_hour"`
	SevenDay UsageWindow `json:"seven_day"`
}

type UsageWindow struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}
