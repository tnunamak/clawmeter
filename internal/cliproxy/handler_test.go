package cliproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	testManagementKey = "test-management-key-0123456789abcdef"
	testMaxAge        = 10 * time.Minute
	leakToken         = "sk-ant-FAKE-TOKEN-MUST-NOT-LEAK-4b91"
)

// fakeProvider is the smallest provider.Provider that also carries the
// optional capabilities the hub reads: source identity, family and revision.
type fakeProvider struct {
	name       string
	family     string
	sourceID   string
	label      string
	revision   string
	secret     string
	configured bool
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) DisplayName() string { return "Fake " + f.name }
func (f *fakeProvider) Description() string { return "fake provider for hub tests" }
func (f *fakeProvider) DashboardURL() string {
	return "https://example.invalid/" + f.name
}
func (f *fakeProvider) IsConfigured() bool { return f.configured }
func (f *fakeProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return nil, errors.New("fake provider does not fetch")
}

func (f *fakeProvider) SourceID() string       { return f.sourceID }
func (f *fakeProvider) SourceLabel() string    { return f.label }
func (f *fakeProvider) ProviderFamily() string { return f.family }
func (f *fakeProvider) SourceRevision() string { return f.revision }

func claudeSource(sourceID string) *fakeProvider {
	return &fakeProvider{
		name: "claude", sourceID: sourceID, label: "Claude " + sourceID,
		revision: "rev-claude-" + sourceID, secret: leakToken, configured: true,
	}
}

func codexSource(sourceID string) *fakeProvider {
	return &fakeProvider{
		name: "openai", sourceID: sourceID, label: "Codex " + sourceID,
		revision: "rev-codex-" + sourceID, secret: leakToken, configured: true,
	}
}

func geminiSource(sourceID string) *fakeProvider {
	return &fakeProvider{
		name: "gemini", sourceID: sourceID, label: "Gemini " + sourceID,
		revision: "rev-gemini-" + sourceID, secret: leakToken, configured: true,
	}
}

func newHandler(t *testing.T, sources []provider.Provider, maxAge time.Duration) http.Handler {
	t.Helper()
	h, err := NewHandler(testManagementKey, sources, maxAge)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func isolatedCacheHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
}

type authFilesBody struct {
	ObservedAt time.Time        `json:"observed_at"`
	Files      []map[string]any `json:"files"`
}

type envelope struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

func doRequest(t *testing.T, h http.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	return recorder
}

func authFiles(t *testing.T, h http.Handler) authFilesBody {
	t.Helper()
	recorder := doRequest(t, h, http.MethodGet, authFilesPath, "", "Bearer "+testManagementKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("auth-files status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	var decoded authFilesBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode auth-files: %v; body = %s", err, recorder.Body.String())
	}
	return decoded
}

func indexFor(t *testing.T, h http.Handler, accountID string) string {
	t.Helper()
	for _, file := range authFiles(t, h).Files {
		if file["id"] == accountID {
			index, _ := file["auth_index"].(string)
			return index
		}
	}
	t.Fatalf("auth-files has no account %q", accountID)
	return ""
}

type apiCallBody struct {
	AuthIndex string            `json:"auth_index"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header,omitempty"`
	Data      any               `json:"data,omitempty"`
}

// callAPI sends one api-call request and decodes the T3 envelope.
func callAPI(t *testing.T, h http.Handler, request apiCallBody) envelope {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal api-call request: %v", err)
	}
	recorder := doRequest(t, h, http.MethodPost, apiCallPath, string(payload), "Bearer "+testManagementKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("api-call HTTP status = %d, want 200 envelope; body = %s", recorder.Code, recorder.Body.String())
	}
	var decoded envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode api-call envelope: %v; body = %s", err, recorder.Body.String())
	}
	return decoded
}

func decodeEnvelopeBody(t *testing.T, env envelope, into any) {
	t.Helper()
	if env.Body == "" {
		t.Fatalf("envelope body is empty; envelope = %+v", env)
	}
	if err := json.Unmarshal([]byte(env.Body), into); err != nil {
		t.Fatalf("envelope body is not JSON: %v; body = %s", err, env.Body)
	}
}

// writeCache stores one provider reading through the cache package's public
// API so the handler reads the same file a running tray would read.
func writeCache(t *testing.T, key string, data *provider.UsageData, revision string) {
	t.Helper()
	revisions := map[string]string{}
	if revision != "" {
		revisions[key] = revision
	}
	if err := cache.Write(&provider.MultiFetchResult{
		Results:         map[string]*provider.UsageData{key: data},
		SourceRevisions: revisions,
		FetchedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("cache.Write: %v", err)
	}
}

func freshClaudeReading(sourceID string, fetchedAt time.Time) *provider.UsageData {
	return &provider.UsageData{
		Provider:  "claude",
		SourceID:  sourceID,
		FetchedAt: fetchedAt,
		Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 12.5, ResetsAt: fetchedAt.Add(time.Hour)},
			{Name: "7d All", Utilization: 40, ResetsAt: fetchedAt.Add(96 * time.Hour)},
		},
	}
}

func TestAuthRequiresBearerKey(t *testing.T) {
	isolatedCacheHome(t)
	h := newHandler(t, []provider.Provider{claudeSource("work")}, testMaxAge)

	cases := []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"wrong key", "Bearer not-the-key"},
		{"wrong scheme", "Basic " + testManagementKey},
		{"key without scheme", testManagementKey},
		{"key with trailing space", "Bearer " + testManagementKey + " "},
	}
	for _, c := range cases {
		for _, path := range []string{authFilesPath, apiCallPath} {
			t.Run(c.name+" "+path, func(t *testing.T) {
				method := http.MethodGet
				if path == apiCallPath {
					method = http.MethodPost
				}
				recorder := doRequest(t, h, method, path, `{"auth_index":"x","method":"GET","url":"u"}`, c.header)
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", recorder.Code)
				}
				if got := recorder.Header().Get("WWW-Authenticate"); got != "Bearer" {
					t.Fatalf("WWW-Authenticate = %q, want %q", got, "Bearer")
				}
				if !strings.Contains(recorder.Body.String(), "unauthorized") {
					t.Fatalf("body = %s, want an unauthorized error", recorder.Body.String())
				}
			})
		}
	}

	t.Run("correct key", func(t *testing.T) {
		recorder := doRequest(t, h, http.MethodGet, authFilesPath, "", "Bearer "+testManagementKey)
		if recorder.Code != http.StatusOK {
			t.Fatalf("auth-files status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestRoutesRejectWrongMethodWithAllowHeader(t *testing.T) {
	isolatedCacheHome(t)
	h := newHandler(t, []provider.Provider{claudeSource("work")}, testMaxAge)

	cases := []struct {
		name    string
		method  string
		path    string
		allowed string
	}{
		{"auth-files rejects POST", http.MethodPost, authFilesPath, http.MethodGet},
		{"auth-files rejects DELETE", http.MethodDelete, authFilesPath, http.MethodGet},
		{"api-call rejects GET", http.MethodGet, apiCallPath, http.MethodPost},
		{"api-call rejects PUT", http.MethodPut, apiCallPath, http.MethodPost},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Authorized on purpose: the method check must win over auth.
			recorder := doRequest(t, h, c.method, c.path, "", "Bearer "+testManagementKey)
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}
			if got := recorder.Header().Get("Allow"); got != c.allowed {
				t.Fatalf("Allow = %q, want %q", got, c.allowed)
			}
		})
	}
}

func TestAuthFilesListsOnlyClaudeAndCodexSources(t *testing.T) {
	isolatedCacheHome(t)
	familyOverride := &fakeProvider{
		name: "claude_web", family: "claude", sourceID: "browser", label: "Browser",
		revision: "rev-claude-browser", configured: true,
	}
	sources := []provider.Provider{
		claudeSource("work"),
		codexSource("plus"),
		geminiSource("main"),
		familyOverride,
	}
	h := newHandler(t, sources, testMaxAge)

	body := authFiles(t, h)
	if body.ObservedAt.IsZero() {
		t.Fatalf("observed_at is zero")
	}
	if age := time.Since(body.ObservedAt); age < 0 || age > time.Minute {
		t.Fatalf("observed_at = %s is not current", body.ObservedAt)
	}

	got := map[string]string{}
	for _, file := range body.Files {
		id, _ := file["id"].(string)
		providerName, _ := file["provider"].(string)
		got[id] = providerName
	}
	want := map[string]string{
		"claude:work":    "claude",
		"codex:plus":     "codex",
		"claude:browser": "claude",
	}
	if len(got) != len(want) {
		t.Fatalf("auth-files = %v, want exactly %v", got, want)
	}
	for id, providerName := range want {
		if got[id] != providerName {
			t.Fatalf("auth-files[%q] provider = %q, want %q; all = %v", id, got[id], providerName, got)
		}
	}

	t.Run("only documented fields", func(t *testing.T) {
		for _, file := range body.Files {
			if len(file) != 3 {
				t.Fatalf("auth file has %d fields (%v), want exactly id, auth_index, provider", len(file), file)
			}
			for _, key := range []string{"id", "auth_index", "provider"} {
				if _, ok := file[key]; !ok {
					t.Fatalf("auth file missing %q: %v", key, file)
				}
				if value, _ := file[key].(string); strings.TrimSpace(value) == "" {
					t.Fatalf("auth file %q is empty: %v", key, file)
				}
			}
		}
	})

	t.Run("same source keeps its index across handlers", func(t *testing.T) {
		first := indexFor(t, h, "claude:work")
		second := newHandler(t, []provider.Provider{claudeSource("work")}, testMaxAge)
		if again := indexFor(t, second, "claude:work"); again != first {
			t.Fatalf("auth_index = %q then %q, want stable", first, again)
		}
	})

	t.Run("different sources get different indexes", func(t *testing.T) {
		work := indexFor(t, h, "claude:work")
		other := newHandler(t, []provider.Provider{claudeSource("personal")}, testMaxAge)
		if personal := indexFor(t, other, "claude:personal"); personal == work {
			t.Fatalf("both sources share auth_index %q", work)
		}
		plus := indexFor(t, h, "codex:plus")
		if plus == work {
			t.Fatalf("claude and codex sources share auth_index %q", work)
		}
	})
}

func TestAPICallRejectsUnknownAccountAndUnsupportedRequests(t *testing.T) {
	isolatedCacheHome(t)
	claude := claudeSource("work")
	codex := codexSource("plus")
	h := newHandler(t, []provider.Provider{claude, codex}, testMaxAge)
	claudeIndex := indexFor(t, h, "claude:work")
	codexIndex := indexFor(t, h, "codex:plus")

	t.Run("unknown auth_index is an envelope 404", func(t *testing.T) {
		env := callAPI(t, h, apiCallBody{AuthIndex: "no-such-index", Method: http.MethodGet, URL: claudeUsageURL})
		if env.StatusCode != http.StatusNotFound {
			t.Fatalf("status_code = %d, want 404", env.StatusCode)
		}
		var body map[string]any
		decodeEnvelopeBody(t, env, &body)
		if body["error"] == nil {
			t.Fatalf("body = %s, want an error", env.Body)
		}
	})

	t.Run("non-GET method is an envelope 405", func(t *testing.T) {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			env := callAPI(t, h, apiCallBody{AuthIndex: claudeIndex, Method: method, URL: claudeUsageURL})
			if env.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s status_code = %d, want 405", method, env.StatusCode)
			}
		}
	})

	t.Run("claude account cannot reach the codex URL", func(t *testing.T) {
		env := callAPI(t, h, apiCallBody{AuthIndex: claudeIndex, Method: http.MethodGet, URL: codexUsageURL})
		if env.StatusCode != http.StatusForbidden {
			t.Fatalf("codex URL status_code = %d, want 403", env.StatusCode)
		}
	})

	t.Run("claude account cannot reach any other URL", func(t *testing.T) {
		for _, url := range []string{
			"https://api.anthropic.com/v1/messages",
			claudeUsageURL + "/",
			claudeUsageURL + "?x=1",
			"",
			codexCreditsURL,
		} {
			env := callAPI(t, h, apiCallBody{AuthIndex: claudeIndex, Method: http.MethodGet, URL: url})
			if env.StatusCode != http.StatusForbidden {
				t.Fatalf("url %q status_code = %d, want 403", url, env.StatusCode)
			}
		}
	})

	t.Run("codex account cannot reach the claude URL", func(t *testing.T) {
		env := callAPI(t, h, apiCallBody{AuthIndex: codexIndex, Method: http.MethodGet, URL: claudeUsageURL})
		if env.StatusCode != http.StatusForbidden {
			t.Fatalf("claude URL status_code = %d, want 403", env.StatusCode)
		}
	})

	t.Run("oversized body is rejected before decoding", func(t *testing.T) {
		payload := fmt.Sprintf(`{"auth_index":%q,"method":"GET","url":%q,"data":%q}`,
			claudeIndex, claudeUsageURL, strings.Repeat("a", 32<<10))
		recorder := doRequest(t, h, http.MethodPost, apiCallPath, payload, "Bearer "+testManagementKey)
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("trailing JSON after the object is rejected", func(t *testing.T) {
		payload := fmt.Sprintf(`{"auth_index":%q,"method":"GET","url":%q} {"auth_index":"x"}`,
			claudeIndex, claudeUsageURL)
		recorder := doRequest(t, h, http.MethodPost, apiCallPath, payload, "Bearer "+testManagementKey)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		recorder := doRequest(t, h, http.MethodPost, apiCallPath, `{`, "Bearer "+testManagementKey)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("T3 header and data fields are accepted and ignored", func(t *testing.T) {
		writeCache(t, "claude:work", freshClaudeReading("work", time.Now()), claude.revision)
		env := callAPI(t, h, apiCallBody{
			AuthIndex: claudeIndex,
			Method:    http.MethodGet,
			URL:       claudeUsageURL,
			Header: map[string]string{
				"Authorization":  "Bearer $TOKEN$",
				"anthropic-beta": "oauth-2025-04-20",
				"content-type":   "application/json",
			},
			Data: map[string]any{"model": "claude-sonnet-4-5", "max_tokens": 1024},
		})
		if env.StatusCode != http.StatusOK {
			t.Fatalf("status_code = %d, want 200; body = %s", env.StatusCode, env.Body)
		}
		var body claudeUsageResponse
		decodeEnvelopeBody(t, env, &body)
		if body.FiveHour == nil || body.SevenDay == nil {
			t.Fatalf("body = %s, want the cached claude windows", env.Body)
		}
	})
}

func TestCachedReadingFreshness(t *testing.T) {
	const sourceID = "work"

	setup := func(t *testing.T) (http.Handler, *fakeProvider) {
		isolatedCacheHome(t)
		source := claudeSource(sourceID)
		return newHandler(t, []provider.Provider{source}, testMaxAge), source
	}
	call := func(t *testing.T, h http.Handler) envelope {
		return callAPI(t, h, apiCallBody{
			AuthIndex: indexFor(t, h, "claude:"+sourceID),
			Method:    http.MethodGet,
			URL:       claudeUsageURL,
		})
	}

	t.Run("fresh reading maps to 200", func(t *testing.T) {
		h, source := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading(sourceID, time.Now().Add(-time.Minute)), source.revision)
		env := call(t, h)
		if env.StatusCode != http.StatusOK {
			t.Fatalf("status_code = %d, want 200; body = %s", env.StatusCode, env.Body)
		}
		var body claudeUsageResponse
		decodeEnvelopeBody(t, env, &body)
		if body.FiveHour == nil || body.FiveHour.Utilization != 12.5 {
			t.Fatalf("body = %s, want the cached five_hour window", env.Body)
		}
	})

	t.Run("reading older than maxAge is 503", func(t *testing.T) {
		h, source := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading(sourceID, time.Now().Add(-testMaxAge-time.Minute)), source.revision)
		if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
		}
	})

	t.Run("reading just inside maxAge is 200", func(t *testing.T) {
		h, source := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading(sourceID, time.Now().Add(-testMaxAge+time.Minute)), source.revision)
		if env := call(t, h); env.StatusCode != http.StatusOK {
			t.Fatalf("status_code = %d, want 200; body = %s", env.StatusCode, env.Body)
		}
	})

	t.Run("future reading is 503", func(t *testing.T) {
		h, source := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading(sourceID, time.Now().Add(5*time.Minute)), source.revision)
		if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
		}
	})

	for _, flag := range []struct {
		name  string
		apply func(*provider.UsageData)
	}{
		{"stale reading", func(data *provider.UsageData) { data.Stale = true }},
		{"expired credentials", func(data *provider.UsageData) { data.IsExpired = true }},
		{"fetch error", func(data *provider.UsageData) { data.Error = "authentication failed" }},
		{"zero fetch time", func(data *provider.UsageData) { data.FetchedAt = time.Time{} }},
	} {
		t.Run(flag.name+" is 503", func(t *testing.T) {
			h, source := setup(t)
			data := freshClaudeReading(sourceID, time.Now())
			flag.apply(data)
			writeCache(t, "claude:"+sourceID, data, source.revision)
			if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
			}
		})
	}

	t.Run("reading for another source is 503", func(t *testing.T) {
		h, source := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading("someone-else", time.Now()), source.revision)
		if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
		}
	})

	t.Run("rotation since the cache write is 503", func(t *testing.T) {
		h, source := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading(sourceID, time.Now()), source.revision+"-rotated")
		if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
		}
	})

	t.Run("cache written without a revision for a revisioned source is 503", func(t *testing.T) {
		h, _ := setup(t)
		writeCache(t, "claude:"+sourceID, freshClaudeReading(sourceID, time.Now()), "")
		if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
		}
	})

	t.Run("missing cache file is 503", func(t *testing.T) {
		h, _ := setup(t)
		if env := call(t, h); env.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
		}
	})
}

func TestClaudeWindowMapping(t *testing.T) {
	isolatedCacheHome(t)
	source := claudeSource("work")
	h := newHandler(t, []provider.Provider{source}, testMaxAge)
	index := indexFor(t, h, "claude:work")

	now := time.Now().UTC().Truncate(time.Second)
	fiveHourReset := now.Add(2 * time.Hour)
	sevenDayReset := now.Add(4 * 24 * time.Hour)
	opusReset := now.Add(3 * 24 * time.Hour)
	sonnetReset := now.Add(5 * 24 * time.Hour)

	writeCache(t, "claude:work", &provider.UsageData{
		Provider:  "claude",
		SourceID:  "work",
		FetchedAt: now,
		Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 12.5, ResetsAt: fiveHourReset},
			{Name: "7d All", Utilization: 40, ResetsAt: sevenDayReset},
			{Name: "7d Opus", Utilization: 55.5, ResetsAt: opusReset},
			{Name: "7d Sonnet", Utilization: 10, ResetsAt: sonnetReset},
			{Name: "7d OAuth", Utilization: 99, ResetsAt: now.Add(time.Hour)},
			{Name: "bonus", Utilization: 99, ResetsAt: now.Add(time.Hour)},
			{Name: "extra", Utilization: 99, ResetsAt: now.Add(time.Hour)},
		},
	}, source.revision)

	env := callAPI(t, h, apiCallBody{AuthIndex: index, Method: http.MethodGet, URL: claudeUsageURL})
	if env.StatusCode != http.StatusOK {
		t.Fatalf("status_code = %d, want 200; body = %s", env.StatusCode, env.Body)
	}
	var body claudeUsageResponse
	decodeEnvelopeBody(t, env, &body)

	if body.FiveHour == nil {
		t.Fatalf("five_hour missing: %s", env.Body)
	}
	if body.FiveHour.Utilization != 12.5 {
		t.Fatalf("five_hour.utilization = %v, want 12.5", body.FiveHour.Utilization)
	}
	checkResetTime(t, "five_hour.resets_at", body.FiveHour.ResetsAt, fiveHourReset)

	if body.SevenDay == nil {
		t.Fatalf("seven_day missing: %s", env.Body)
	}
	if body.SevenDay.Utilization != 40 {
		t.Fatalf("seven_day.utilization = %v, want 40", body.SevenDay.Utilization)
	}
	checkResetTime(t, "seven_day.resets_at", body.SevenDay.ResetsAt, sevenDayReset)

	if len(body.Limits) != 2 {
		t.Fatalf("limits = %+v, want the Opus and Sonnet scoped windows only", body.Limits)
	}
	for i, want := range []struct {
		name    string
		percent float64
		reset   time.Time
	}{
		{"Opus", 55.5, opusReset},
		{"Sonnet", 10, sonnetReset},
	} {
		limit := body.Limits[i]
		if limit.Kind != "weekly_scoped" {
			t.Fatalf("limits[%d].kind = %q, want weekly_scoped", i, limit.Kind)
		}
		if limit.Percent != want.percent {
			t.Fatalf("limits[%d].percent = %v, want %v", i, limit.Percent, want.percent)
		}
		if got := limit.Scope.Model.DisplayName; got != want.name {
			t.Fatalf("limits[%d].scope.model.display_name = %q, want %q", i, got, want.name)
		}
		checkResetTime(t, fmt.Sprintf("limits[%d].resets_at", i), limit.ResetsAt, want.reset)
	}

	for _, omitted := range []string{"OAuth", "bonus", "extra"} {
		if strings.Contains(env.Body, omitted) {
			t.Fatalf("body mentions omitted window %q: %s", omitted, env.Body)
		}
	}
}

func checkResetTime(t *testing.T, label string, got *string, want time.Time) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s missing, want %s", label, want.Format(time.RFC3339))
	}
	parsed, err := time.Parse(time.RFC3339Nano, *got)
	if err != nil {
		t.Fatalf("%s = %q is not RFC3339: %v", label, *got, err)
	}
	if !parsed.Equal(want) {
		t.Fatalf("%s = %s, want %s", label, parsed, want)
	}
}

func TestCodexWindowMapping(t *testing.T) {
	isolatedCacheHome(t)
	source := codexSource("plus")
	h := newHandler(t, []provider.Provider{source}, testMaxAge)
	index := indexFor(t, h, "codex:plus")

	now := time.Now().UTC().Truncate(time.Second)
	primaryReset := now.Add(3 * time.Hour)
	secondaryReset := now.Add(6 * 24 * time.Hour)

	writeCache(t, "openai:plus", &provider.UsageData{
		Provider:  "openai",
		SourceID:  "plus",
		FetchedAt: now,
		Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 20, ResetsAt: primaryReset, Length: 5 * time.Hour},
			{Name: "7d", Utilization: 60, ResetsAt: secondaryReset, Length: 7 * 24 * time.Hour},
		},
	}, source.revision)

	env := callAPI(t, h, apiCallBody{AuthIndex: index, Method: http.MethodGet, URL: codexUsageURL})
	if env.StatusCode != http.StatusOK {
		t.Fatalf("status_code = %d, want 200; body = %s", env.StatusCode, env.Body)
	}
	var body codexUsageResponse
	decodeEnvelopeBody(t, env, &body)

	if body.RateLimit.PrimaryWindow == nil {
		t.Fatalf("rate_limit.primary_window missing: %s", env.Body)
	}
	if got := body.RateLimit.PrimaryWindow.UsedPercent; got != 20 {
		t.Fatalf("primary_window.used_percent = %v, want 20", got)
	}
	if body.RateLimit.PrimaryWindow.ResetAt == nil || *body.RateLimit.PrimaryWindow.ResetAt != primaryReset.Unix() {
		t.Fatalf("primary_window.reset_at = %v, want unix seconds %d",
			body.RateLimit.PrimaryWindow.ResetAt, primaryReset.Unix())
	}
	if body.RateLimit.PrimaryWindow.LimitWindowSeconds == nil || *body.RateLimit.PrimaryWindow.LimitWindowSeconds != 18000 {
		t.Fatalf("primary_window.limit_window_seconds = %v, want 18000",
			body.RateLimit.PrimaryWindow.LimitWindowSeconds)
	}

	if body.RateLimit.SecondaryWindow == nil {
		t.Fatalf("rate_limit.secondary_window missing: %s", env.Body)
	}
	if got := body.RateLimit.SecondaryWindow.UsedPercent; got != 60 {
		t.Fatalf("secondary_window.used_percent = %v, want 60", got)
	}
	if body.RateLimit.SecondaryWindow.ResetAt == nil || *body.RateLimit.SecondaryWindow.ResetAt != secondaryReset.Unix() {
		t.Fatalf("secondary_window.reset_at = %v, want unix seconds %d",
			body.RateLimit.SecondaryWindow.ResetAt, secondaryReset.Unix())
	}
	if body.RateLimit.SecondaryWindow.LimitWindowSeconds == nil ||
		*body.RateLimit.SecondaryWindow.LimitWindowSeconds != 7*24*3600 {
		t.Fatalf("secondary_window.limit_window_seconds = %v, want 604800",
			body.RateLimit.SecondaryWindow.LimitWindowSeconds)
	}

	t.Run("empty windows is still a 200 envelope", func(t *testing.T) {
		var raw map[string]any
		if err := json.Unmarshal([]byte(env.Body), &raw); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		if _, ok := raw["rate_limit"]; !ok {
			t.Fatalf("body = %s, want a rate_limit object", env.Body)
		}
	})
}

func TestUnmappableCachedWindowsAreUnavailable(t *testing.T) {
	isolatedCacheHome(t)
	claude := claudeSource("work")
	codex := codexSource("plus")
	h := newHandler(t, []provider.Provider{claude, codex}, testMaxAge)

	now := time.Now()
	writeCache(t, "claude:work", &provider.UsageData{
		Provider: "claude", SourceID: "work", FetchedAt: now,
		Windows: []provider.UsageWindow{{Name: "bonus", Utilization: 1, ResetsAt: now.Add(time.Hour)}},
	}, claude.revision)
	if env := callAPI(t, h, apiCallBody{
		AuthIndex: indexFor(t, h, "claude:work"), Method: http.MethodGet, URL: claudeUsageURL,
	}); env.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("claude status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
	}

	writeCache(t, "openai:plus", &provider.UsageData{
		Provider: "openai", SourceID: "plus", FetchedAt: now,
		Windows: []provider.UsageWindow{{Name: "monthly", Utilization: 1, ResetsAt: now.Add(time.Hour)}},
	}, codex.revision)
	if env := callAPI(t, h, apiCallBody{
		AuthIndex: indexFor(t, h, "codex:plus"), Method: http.MethodGet, URL: codexUsageURL,
	}); env.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("codex status_code = %d, want 503; body = %s", env.StatusCode, env.Body)
	}
}

func TestCodexResetCreditsAreNeverAnEmptyList(t *testing.T) {
	isolatedCacheHome(t)
	codex := codexSource("plus")
	h := newHandler(t, []provider.Provider{codex}, testMaxAge)

	// A current usage reading exists; credits still must not fake an empty list.
	writeCache(t, "openai:plus", &provider.UsageData{
		Provider: "openai", SourceID: "plus", FetchedAt: time.Now(),
		Windows: []provider.UsageWindow{{Name: "7d", Utilization: 10, ResetsAt: time.Now().Add(time.Hour)}},
	}, codex.revision)

	env := callAPI(t, h, apiCallBody{
		AuthIndex: indexFor(t, h, "codex:plus"),
		Method:    http.MethodGet,
		URL:       codexCreditsURL,
	})
	if env.StatusCode == http.StatusOK {
		t.Fatalf("status_code = 200, want a failure so T3 omits credits; body = %s", env.Body)
	}
	if env.StatusCode != http.StatusNotFound {
		t.Fatalf("status_code = %d, want 404", env.StatusCode)
	}
	var body map[string]any
	decodeEnvelopeBody(t, env, &body)
	if _, ok := body["credits"]; ok {
		t.Fatalf("body = %s, must not carry a credits list", env.Body)
	}
	if strings.TrimSpace(env.Body) == "" {
		t.Fatalf("body is empty, want an explanation")
	}
}

func TestNoCredentialLeakage(t *testing.T) {
	isolatedCacheHome(t)
	claude := claudeSource("work")
	codex := codexSource("plus")
	// A credential-shaped secret in every fake's fields, plus a realistic
	// hashed revision, must never reach a response.
	if claude.secret == "" || codex.secret == "" {
		t.Fatal("test setup: fake secrets are empty")
	}
	h := newHandler(t, []provider.Provider{claude, codex}, testMaxAge)
	claudeIndex := indexFor(t, h, "claude:work")
	codexIndex := indexFor(t, h, "codex:plus")

	now := time.Now()
	writeCache(t, "claude:work", freshClaudeReading("work", now), claude.revision)
	writeCache(t, "openai:plus", &provider.UsageData{
		Provider: "openai", SourceID: "plus", FetchedAt: now,
		Windows: []provider.UsageWindow{{Name: "7d", Utilization: 10, ResetsAt: now.Add(time.Hour)}},
	}, codex.revision)

	bodies := []string{authFilesRaw(t, h)}
	requests := []apiCallBody{
		{AuthIndex: claudeIndex, Method: http.MethodGet, URL: claudeUsageURL},
		{AuthIndex: codexIndex, Method: http.MethodGet, URL: codexUsageURL},
		{AuthIndex: codexIndex, Method: http.MethodGet, URL: codexCreditsURL},
		{AuthIndex: claudeIndex, Method: http.MethodGet, URL: codexUsageURL},
		{AuthIndex: "unknown", Method: http.MethodGet, URL: claudeUsageURL},
		{AuthIndex: claudeIndex, Method: http.MethodPost, URL: claudeUsageURL},
		{AuthIndex: claudeIndex, Method: http.MethodGet, URL: claudeUsageURL,
			Header: map[string]string{"Authorization": "Bearer " + leakToken}},
	}
	for _, request := range requests {
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		recorder := doRequest(t, h, http.MethodPost, apiCallPath, string(payload), "Bearer "+testManagementKey)
		bodies = append(bodies, recorder.Body.String())
	}

	// Also the unavailable-read path, where an account identifier is echoed.
	isolatedInvalid := newHandler(t, []provider.Provider{claudeSource("work")}, testMaxAge)
	bodies = append(bodies, authFilesRaw(t, isolatedInvalid))
	bodies = append(bodies, callAPI(t, isolatedInvalid, apiCallBody{
		AuthIndex: indexFor(t, isolatedInvalid, "claude:work"),
		Method:    http.MethodGet,
		URL:       claudeUsageURL,
	}).Body)

	for i, body := range bodies {
		if strings.Contains(body, leakToken) {
			t.Fatalf("response %d leaks the provider token: %s", i, body)
		}
		if strings.Contains(body, claude.revision) || strings.Contains(body, codex.revision) {
			t.Fatalf("response %d leaks a credential revision: %s", i, body)
		}
	}
}

func authFilesRaw(t *testing.T, h http.Handler) string {
	t.Helper()
	recorder := doRequest(t, h, http.MethodGet, authFilesPath, "", "Bearer "+testManagementKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("auth-files status = %d, want 200", recorder.Code)
	}
	return recorder.Body.String()
}

func TestNewHandlerValidation(t *testing.T) {
	if _, err := NewHandler("", []provider.Provider{claudeSource("work")}, time.Minute); err == nil {
		t.Fatal("NewHandler accepted an empty management key")
	}
	if _, err := NewHandler(testManagementKey, []provider.Provider{claudeSource("work")}, 0); err == nil {
		t.Fatal("NewHandler accepted a non-positive max age")
	}
}
