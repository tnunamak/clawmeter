// Package cliproxy exposes Clawmeter's cached Claude and Codex readings through
// the read-only subset of CLIProxyAPI's management API that T3 Code reads
// (apps/server/src/usage/cliproxyApi.ts in pingdotgg/t3code). It answers from
// the cache the tray keeps and never forwards a request or returns a
// credential. Its only own request is for each account's identity, which T3
// uses to match the account with its own login.
package cliproxy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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

var (
	// identityBudget bounds the identity lookups of one account list, so a
	// slow provider cannot keep T3 from getting the list.
	identityBudget = 4 * time.Second
	// identityRetry is how long a failed lookup is not repeated for the
	// same login.
	identityRetry = 2 * time.Minute
)

type account struct {
	id       string
	provider string
	source   provider.Provider
}

type handler struct {
	key      string
	maxAge   time.Duration
	accounts []account

	mu sync.Mutex
	// identities holds each account's last identity lookup with the source
	// revision it was made under, keyed by account ID.
	identities map[string]knownIdentity
}

type knownIdentity struct {
	revision string
	identity provider.AccountIdentity
	failedAt time.Time
}

type authFilesResponse struct {
	ObservedAt time.Time  `json:"observed_at"`
	Files      []authFile `json:"files"`
}

type authFile struct {
	ID        string        `json:"id"`
	AuthIndex string        `json:"auth_index"`
	Provider  string        `json:"provider"`
	Email     string        `json:"email,omitempty"`
	IDToken   *authIDTokens `json:"id_token,omitempty"`
}

// authIDTokens holds the claims T3 reads from a Codex login's ID token.
type authIDTokens struct {
	ChatGPTAccountID string `json:"chatgpt_account_id,omitempty"`
	PlanType         string `json:"plan_type,omitempty"`
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
		key:        managementKey,
		maxAge:     maxAge,
		identities: make(map[string]knownIdentity, len(sources)),
	}
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		providerName, ok := hubProvider(source)
		// A source whose revision does not identify its login could show
		// another account's cached reading under its name; it is left out.
		if !ok || !provider.IsAccountBound(source) {
			continue
		}
		sourceID := provider.SourceID(source)
		id := providerName + ":" + sourceID
		if seen[id] {
			return nil, fmt.Errorf("duplicate hub source identity")
		}
		seen[id] = true
		h.accounts = append(h.accounts, account{id: id, provider: providerName, source: source})
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

	files := make([]authFile, len(h.accounts))
	// Identities are read in parallel within one budget, so a slow
	// provider delays the list by at most the budget.
	ctx, cancel := context.WithTimeout(r.Context(), identityBudget)
	defer cancel()
	var wg sync.WaitGroup
	for i, account := range h.accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			revision := provider.SourceRevision(account.source)
			file := authFile{ID: account.id, AuthIndex: loginIndex(account, revision), Provider: account.provider}
			if identity, ok := h.identity(ctx, account, revision); ok {
				file.Email = identity.Email
				if account.provider == "codex" && (identity.WorkspaceID != "" || identity.PlanType != "") {
					file.IDToken = &authIDTokens{ChatGPTAccountID: identity.WorkspaceID, PlanType: identity.PlanType}
				}
			}
			files[i] = file
		}()
	}
	wg.Wait()
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

	account, revision, ok := h.accountFor(request.AuthIndex)
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
		data, ok := cachedUsage(account, revision, h.maxAge)
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
		data, ok := cachedUsage(account, revision, h.maxAge)
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

// loginIndex is the auth_index of an account listed while its source read
// the login with this revision. T3 lists accounts and reads their usage in
// separate requests; because the index changes with the login, a read for an
// account listed before a new login finds no account, instead of returning
// the new login's numbers under the email listed for the old one.
func loginIndex(account account, revision string) string {
	sum := sha256.Sum256([]byte(account.provider + "\x00" + provider.SourceID(account.source) + "\x00" + revision))
	return hex.EncodeToString(sum[:8])
}

// accountFor finds the account whose current login has this auth_index, and
// returns the revision of that login.
func (h *handler) accountFor(index string) (account, string, bool) {
	for _, account := range h.accounts {
		revision := provider.SourceRevision(account.source)
		if loginIndex(account, revision) == index {
			return account, revision, true
		}
	}
	return account{}, "", false
}

// identity returns the identity of the login the account's source reads now.
// T3 shows the account's readings under the login with that email, so an
// identity read while the login changed is withheld: without one, T3 shows
// the account on its own row.
func (h *handler) identity(ctx context.Context, account account, revisionBefore string) (provider.AccountIdentity, bool) {
	source, ok := account.source.(provider.AccountIdentityCapability)
	if !ok {
		return provider.AccountIdentity{}, false
	}
	if revisionBefore == "" || !provider.IsAccountBound(account.source) {
		return provider.AccountIdentity{}, false
	}
	h.mu.Lock()
	known, found := h.identities[account.id]
	h.mu.Unlock()
	found = found && known.revision == revisionBefore
	if found && !known.failedAt.IsZero() && time.Since(known.failedAt) < identityRetry {
		return provider.AccountIdentity{}, false
	}
	identity := known.identity
	if !found || !known.failedAt.IsZero() {
		var err error
		identity, err = source.AccountIdentity(ctx)
		if err != nil || identity.Email == "" {
			h.rememberFailure(account, revisionBefore)
			return provider.AccountIdentity{}, false
		}
	}
	if provider.SourceRevision(account.source) != revisionBefore {
		return provider.AccountIdentity{}, false
	}
	h.remember(account, knownIdentity{revision: revisionBefore, identity: identity})
	return identity, true
}

func (h *handler) remember(account account, known knownIdentity) {
	h.mu.Lock()
	h.identities[account.id] = known
	h.mu.Unlock()
}

// rememberFailure records a failed lookup, unless a concurrent lookup for
// the same login has already succeeded.
func (h *handler) rememberFailure(account account, revision string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if known, ok := h.identities[account.id]; ok && known.revision == revision && known.failedAt.IsZero() {
		return
	}
	h.identities[account.id] = knownIdentity{revision: revision, failedAt: time.Now()}
}

// cachedUsage returns the source's cached reading when it belongs to the
// login with the listed revision, which is still the source's current one,
// and was taken at most maxAge ago, even if a later poll failed.
func cachedUsage(account account, listed string, maxAge time.Duration) (*provider.UsageData, bool) {
	key := provider.SourceKey(account.source)
	revisionBefore := provider.SourceRevision(account.source)
	if revisionBefore != listed {
		return nil, false
	}
	// Binding can change while the hub runs, for example when Codex moves
	// its login into the keyring. It is checked between the two revision
	// reads: a change before the check makes the source unbound, and one
	// after it changes the second revision.
	if !provider.IsAccountBound(account.source) {
		return nil, false
	}
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
	// A reading the tray kept because a later poll failed, for example on a
	// rate limit, is still an observation of FetchedAt; it is served for as
	// long as any other reading of that age.
	if data.IsExpired || data.Error != "" || data.FetchedAt.IsZero() || age < -time.Minute || age > maxAge {
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
