// Package cliproxy exposes Clawmeter's cached Claude and Codex readings through
// the read-only subset of CLIProxyAPI's management API that T3 Code reads
// (apps/server/src/usage/cliproxyApi.ts in pingdotgg/t3code). It answers from
// the cache the tray keeps; it never fetches, forwards a request, or sends a
// credential.
package cliproxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	authFilesPath       = "/v0/management/auth-files"
	apiCallPath         = "/v0/management/api-call"
	claudeUsageURL      = "https://api.anthropic.com/api/oauth/usage"
	codexUsageURL       = "https://chatgpt.com/backend-api/wham/usage"
	codexCreditsURL     = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	maxAPIRequestBytes  = 16 << 10
	cacheUnavailableMsg = `{"error":"no current Clawmeter reading for this account"}`
)

type account struct {
	id       string
	index    string
	provider string
	source   provider.Provider
}

type handler struct {
	key      string
	maxAge   time.Duration
	accounts []account
	byIndex  map[string]account
}

type authFilesResponse struct {
	ObservedAt time.Time  `json:"observed_at"`
	Files      []authFile `json:"files"`
}

type authFile struct {
	ID        string `json:"id"`
	AuthIndex string `json:"auth_index"`
	Provider  string `json:"provider"`
}

type apiCallRequest struct {
	AuthIndex string `json:"auth_index"`
	Method    string `json:"method"`
	URL       string `json:"url"`
}

type apiCallResponse struct {
	StatusCode int         `json:"status_code"`
	Header     http.Header `json:"header"`
	Body       string      `json:"body"`
}

type claudeUsageResponse struct {
	FiveHour *claudeWindow `json:"five_hour,omitempty"`
	SevenDay *claudeWindow `json:"seven_day,omitempty"`
	Limits   []claudeLimit `json:"limits,omitempty"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    *string `json:"resets_at"`
}

type claudeLimit struct {
	Kind     string      `json:"kind"`
	Percent  float64     `json:"percent"`
	ResetsAt *string     `json:"resets_at"`
	Scope    claudeScope `json:"scope"`
}

type claudeScope struct {
	Model claudeModel `json:"model"`
}

type claudeModel struct {
	DisplayName string `json:"display_name"`
}

type codexUsageResponse struct {
	RateLimit codexRateLimit `json:"rate_limit"`
}

type codexRateLimit struct {
	PrimaryWindow   *codexWindow `json:"primary_window,omitempty"`
	SecondaryWindow *codexWindow `json:"secondary_window,omitempty"`
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAt            *int64  `json:"reset_at"`
	LimitWindowSeconds *int64  `json:"limit_window_seconds,omitempty"`
}

// NewHandler serves the T3 Code management API subset for the configured
// Claude and Codex sources. T3 treats every successful answer as current, so a
// reading older than maxAge is reported unavailable instead.
func NewHandler(managementKey string, sources []provider.Provider, maxAge time.Duration) (http.Handler, error) {
	if managementKey == "" {
		return nil, fmt.Errorf("management key is empty")
	}
	if maxAge <= 0 {
		return nil, fmt.Errorf("maximum reading age must be positive")
	}

	h := &handler{
		key:     managementKey,
		maxAge:  maxAge,
		byIndex: make(map[string]account, len(sources)),
	}
	for _, source := range sources {
		providerName, ok := hubProvider(source)
		// A source whose revision does not identify its login could show
		// another account's cached reading under its name; it is left out.
		if !ok || !provider.IsAccountBound(source) {
			continue
		}
		sourceID := provider.SourceID(source)
		id := providerName + ":" + sourceID
		index := authIndex(providerName, sourceID)
		if _, exists := h.byIndex[index]; exists {
			return nil, fmt.Errorf("duplicate hub source identity")
		}
		entry := account{id: id, index: index, provider: providerName, source: source}
		h.accounts = append(h.accounts, entry)
		h.byIndex[index] = entry
	}
	return h.routes(), nil
}

func (h *handler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(authFilesPath, h.authFiles)
	mux.HandleFunc(apiCallPath, h.apiCall)
	return mux
}

func (h *handler) authFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.authorize(w, r) {
		return
	}

	files := make([]authFile, 0, len(h.accounts))
	for _, account := range h.accounts {
		files = append(files, authFile{ID: account.id, AuthIndex: account.index, Provider: account.provider})
	}
	writeJSON(w, http.StatusOK, authFilesResponse{ObservedAt: time.Now().UTC(), Files: files})
}

func (h *handler) apiCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !h.authorize(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAPIRequestBytes)
	decoder := json.NewDecoder(r.Body)
	var request apiCallRequest
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "api-call request too large"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid api-call request"})
		}
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid api-call request"})
		return
	}

	account, ok := h.byIndex[request.AuthIndex]
	if !ok {
		h.writeAPIResponse(w, http.StatusNotFound, `{"error":"unknown source"}`)
		return
	}
	if request.Method != http.MethodGet {
		h.writeAPIResponse(w, http.StatusMethodNotAllowed, `{"error":"read-only endpoint"}`)
		return
	}

	switch {
	case account.provider == "claude" && request.URL == claudeUsageURL:
		data, ok := cachedUsage(account, h.maxAge)
		if !ok {
			h.writeAPIResponse(w, http.StatusServiceUnavailable, cacheUnavailableMsg)
			return
		}
		body, ok := marshalClaudeUsage(data)
		if !ok {
			h.writeAPIResponse(w, http.StatusServiceUnavailable, cacheUnavailableMsg)
			return
		}
		h.writeAPIResponse(w, http.StatusOK, body)
	case account.provider == "codex" && request.URL == codexUsageURL:
		data, ok := cachedUsage(account, h.maxAge)
		if !ok {
			h.writeAPIResponse(w, http.StatusServiceUnavailable, cacheUnavailableMsg)
			return
		}
		body, ok := marshalCodexUsage(data)
		if !ok {
			h.writeAPIResponse(w, http.StatusServiceUnavailable, cacheUnavailableMsg)
			return
		}
		h.writeAPIResponse(w, http.StatusOK, body)
	case account.provider == "codex" && request.URL == codexCreditsURL:
		// Clawmeter knows how many reset credits there are but not their IDs,
		// which T3 needs to redeem one. A failed read makes T3 leave credits
		// out, where an empty list would claim there are none.
		h.writeAPIResponse(w, http.StatusNotFound, `{"error":"reset credits are not available through Clawmeter"}`)
	default:
		h.writeAPIResponse(w, http.StatusForbidden, `{"error":"upstream URL is not supported"}`)
	}
}

func (h *handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	expected := "Bearer " + h.key
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) == 1 {
		return true
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

func (h *handler) writeAPIResponse(w http.ResponseWriter, status int, body string) {
	writeJSON(w, http.StatusOK, apiCallResponse{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func hubProvider(source provider.Provider) (string, bool) {
	switch provider.FamilyName(source) {
	case "claude":
		return "claude", true
	case "openai":
		return "codex", true
	default:
		return "", false
	}
}

func authIndex(providerName, sourceID string) string {
	sum := sha256.Sum256([]byte(providerName + "\x00" + sourceID))
	return hex.EncodeToString(sum[:8])
}

// cachedUsage returns the source's cached reading when it belongs to the
// source's current credential and is at most maxAge old.
func cachedUsage(account account, maxAge time.Duration) (*provider.UsageData, bool) {
	// Binding can change while the hub runs, for example when Codex moves
	// its login into the keyring, so it is checked on every read.
	if !provider.IsAccountBound(account.source) {
		return nil, false
	}
	key := provider.SourceKey(account.source)
	revisionBefore := provider.SourceRevision(account.source)
	entry, err := cache.Read()
	if err != nil || entry == nil {
		return nil, false
	}
	data, ok := entry.GetProvider(key)
	if !ok || data == nil {
		return nil, false
	}
	revisionAfter := provider.SourceRevision(account.source)
	if revisionBefore != revisionAfter ||
		!provider.UsageDataMatchesSource(data, account.source.Name(), provider.SourceID(account.source)) ||
		!cache.SourceRevisionMatches(entry.SourceRevisions, key, revisionAfter) {
		return nil, false
	}
	age := time.Since(data.FetchedAt)
	if data.Stale || data.IsExpired || data.Error != "" || data.FetchedAt.IsZero() || age < -time.Minute || age > maxAge {
		return nil, false
	}
	return data, true
}

func marshalClaudeUsage(data *provider.UsageData) (string, bool) {
	response := claudeUsageResponse{}
	for _, window := range data.Windows {
		switch window.Name {
		case "5h":
			response.FiveHour = claudeWindowFrom(window)
		case "7d All":
			response.SevenDay = claudeWindowFrom(window)
		default:
			if !strings.HasPrefix(window.Name, "7d ") {
				continue
			}
			modelName := strings.TrimSpace(strings.TrimPrefix(window.Name, "7d "))
			if modelName == "" || modelName == "OAuth" {
				continue
			}
			response.Limits = append(response.Limits, claudeLimit{
				Kind:     "weekly_scoped",
				Percent:  window.Utilization,
				ResetsAt: resetTimestamp(window.ResetsAt),
				Scope:    claudeScope{Model: claudeModel{DisplayName: modelName}},
			})
		}
	}
	if response.FiveHour == nil && response.SevenDay == nil && len(response.Limits) == 0 {
		return "", false
	}
	body, err := json.Marshal(response)
	return string(body), err == nil
}

func claudeWindowFrom(window provider.UsageWindow) *claudeWindow {
	return &claudeWindow{Utilization: window.Utilization, ResetsAt: resetTimestamp(window.ResetsAt)}
}

func resetTimestamp(resetAt time.Time) *string {
	if resetAt.IsZero() {
		return nil
	}
	value := resetAt.UTC().Format(time.RFC3339Nano)
	return &value
}

func marshalCodexUsage(data *provider.UsageData) (string, bool) {
	response := codexUsageResponse{RateLimit: codexRateLimit{}}
	for _, window := range data.Windows {
		switch window.Name {
		case "5h":
			response.RateLimit.PrimaryWindow = codexWindowFrom(window)
		case "7d":
			response.RateLimit.SecondaryWindow = codexWindowFrom(window)
		}
	}
	if response.RateLimit.PrimaryWindow == nil && response.RateLimit.SecondaryWindow == nil {
		return "", false
	}
	body, err := json.Marshal(response)
	return string(body), err == nil
}

func codexWindowFrom(window provider.UsageWindow) *codexWindow {
	result := &codexWindow{UsedPercent: window.Utilization}
	if !window.ResetsAt.IsZero() {
		resetAt := window.ResetsAt.Unix()
		result.ResetAt = &resetAt
	}
	if window.Length > 0 {
		seconds := int64(window.Length / time.Second)
		result.LimitWindowSeconds = &seconds
	}
	return result
}
