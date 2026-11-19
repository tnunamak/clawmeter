// Package opencode implements the Provider interface for an OpenCode account.
// It reads the usage windows of the OpenCode Go plan. OpenCode's usage routes
// report no other plan's windows, so an account without Go has no windows and
// no error.
//
// Two routes give the same windows. The service is tried first; the key is used
// only when the service fails:
//
//   - the OpenCode service on this machine, which holds the OpenCode Console
//     sign-in. Its url and password are in $XDG_STATE_HOME/opencode/service.json
//     (default ~/.local/state/opencode/service.json);
//   - OPENCODE_API_KEY, sent as a bearer token to OpenCode's usage route, for a
//     machine that runs no service.
//
// Neither secret is logged, placed in an error, or sent to a redirect target.
// The monthly window has no published length, so it is reported without one and
// is never projected.
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	timeout        = 10 * time.Second
	maxBodyBytes   = 1 << 20
	limitsPath     = "/api/experimental/usage/limits"
	apiKeyUsageURL = "https://opencode.ai/zen/go/v1/usage"
	apiKeyEnv      = "OPENCODE_API_KEY"
	userAgent      = "Clawmeter"
	notSignedIn    = "Sign in to OpenCode Console: run `opencode auth login opencode --method device`"
)

var errNoEndpoint = errors.New("opencode:this OpenCode build has no usage endpoint; update OpenCode")

type Provider struct {
	client      *http.Client
	serviceFile string // "" means the default location
	baseURL     string // "" means the url in the service file
	apiKeyURL   string // "" means apiKeyUsageURL; tests point it at a local server
	homeDir     func() (string, error)
	now         func() time.Time
}

func New() *Provider {
	return &Provider{
		client: &http.Client{
			Timeout: timeout,
			// Never follow a redirect with the service password or the API key attached.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		homeDir: os.UserHomeDir,
		now:     time.Now,
	}
}

func (p *Provider) Name() string        { return "opencode" }
func (p *Provider) DisplayName() string { return "OpenCode" }
func (p *Provider) Description() string {
	return "OpenCode account usage (via the local OpenCode service or " + apiKeyEnv + ")"
}
func (p *Provider) DashboardURL() string { return "https://opencode.ai/auth" }

// IsConfigured reports whether a route is set up: the service file exists or the
// API key is set. It makes no network call.
func (p *Provider) IsConfigured() bool { return p.SetupStatus().IsReady() }

// SetupStatus names the setup step that is missing. It makes no network call.
func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.serviceFileExists() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "OpenCode local service file found"}
	}
	if p.apiKey() != "" {
		return provider.SetupStatus{State: provider.SetupReady, Detail: apiKeyEnv + " is set"}
	}
	return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "start OpenCode's local service with `opencode serve --service`, or set " + apiKeyEnv}
}

func (p *Provider) serviceFileExists() bool {
	path, err := p.servicePath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (p *Provider) apiKey() string { return strings.TrimSpace(os.Getenv(apiKeyEnv)) }

func (p *Provider) servicePath() (string, error) {
	if p.serviceFile != "" {
		return p.serviceFile, nil
	}
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, "opencode", "service.json"), nil
	}
	home, err := p.homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "opencode", "service.json"), nil
}

type serviceInfo struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

func (p *Provider) readService() (serviceInfo, error) {
	path, err := p.servicePath()
	if err != nil {
		return serviceInfo{}, fmt.Errorf("opencode:cannot locate OpenCode service file: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return serviceInfo{}, errors.New("opencode:OpenCode service file not found; start OpenCode's local service with `opencode serve --service`")
		}
		return serviceInfo{}, errors.New("opencode:cannot read OpenCode service file")
	}
	var info serviceInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return serviceInfo{}, errors.New("opencode:malformed OpenCode service file")
	}
	if p.baseURL != "" {
		info.URL = p.baseURL
	}
	u, err := url.Parse(info.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return serviceInfo{}, errors.New("opencode:OpenCode service file has no usable url")
	}
	if info.Password == "" {
		return serviceInfo{}, errors.New("opencode:OpenCode service file has no password")
	}
	return info, nil
}

// meter is one window as either route reports it. The service sends limited and
// a reset in epoch milliseconds; the key route sends status ("ok" or
// "rate-limited") and an RFC 3339 reset time.
type meter struct {
	Percent  *float64        `json:"percent"`
	Limited  bool            `json:"limited"`
	Status   string          `json:"status"`
	ResetsAt json.RawMessage `json:"resetsAt"`
}

func (m meter) refused() bool { return m.Limited || m.Status == "rate-limited" }

// reset reads resetsAt as epoch milliseconds or as an RFC 3339 string.
func (m meter) reset() (time.Time, bool) {
	if len(m.ResetsAt) == 0 || string(m.ResetsAt) == "null" {
		return time.Time{}, false
	}
	var ms int64
	if json.Unmarshal(m.ResetsAt, &ms) == nil {
		return time.UnixMilli(ms), true
	}
	var s string
	if json.Unmarshal(m.ResetsAt, &s) != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// goPlan holds the Go plan's windows. A route may omit any of them.
type goPlan struct {
	Rolling *meter `json:"rolling"`
	Weekly  *meter `json:"weekly"`
	Monthly *meter `json:"monthly"`
}

type limitsResponse struct {
	Data *struct {
		Go *goPlan `json:"go"`
	} `json:"data"`
}

type usageResponse struct {
	Usage *goPlan `json:"usage"`
}

// FetchUsage tries the service first. The API key route runs only when the
// service fails, so an account with a working service never needs the key.
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	data, serviceErr := p.fetchService(ctx)
	if serviceErr == nil {
		return data, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := p.apiKey()
	if key == "" {
		return nil, serviceErr
	}
	data, keyErr := p.fetchKey(ctx, key)
	if keyErr == nil {
		return data, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("opencode:%s; the %s route also failed: %s", detail(serviceErr), apiKeyEnv, detail(keyErr))
}

// detail is an error's message without its "opencode:" marker, for a message
// that names both routes.
func detail(err error) string { return strings.TrimPrefix(err.Error(), "opencode:") }

func (p *Provider) fetchService(ctx context.Context) (*provider.UsageData, error) {
	svc, err := p.readService()
	if err != nil {
		return nil, err
	}
	home, err := p.homeDir()
	if err != nil {
		return nil, fmt.Errorf("opencode:cannot determine home directory: %w", err)
	}

	endpoint := strings.TrimRight(svc.URL, "/") + limitsPath + "?" +
		url.Values{"location[directory]": {home}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("opencode:cannot build request")
	}
	req.SetBasicAuth("opencode", svc.Password)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Matching the dial operation does not depend on platform errno values.
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return nil, errors.New("opencode:OpenCode service is not running; start it with `opencode serve --service`")
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, errors.New("opencode:OpenCode service did not answer in time; restart it with `opencode serve --service`")
		}
		return nil, errors.New("opencode:cannot reach the OpenCode service")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errors.New("opencode:OpenCode service rejected the password in service.json (http 401); restart OpenCode")
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, errNoEndpoint
	default:
		return nil, fmt.Errorf("opencode:unexpected status http %d", resp.StatusCode)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "text/html" {
		return nil, errNoEndpoint
	}

	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var parsed limitsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, errors.New("opencode:malformed usage response (decode failed)")
	}
	if parsed.Data == nil {
		return nil, errors.New(notSignedIn)
	}
	if parsed.Data.Go == nil {
		// The account has no Go plan, so there are no windows to show. Not an error.
		return &provider.UsageData{Provider: p.Name(), FetchedAt: p.now()}, nil
	}
	return p.usageData(*parsed.Data.Go)
}

// fetchKey reads the same windows from OpenCode's usage route with the API key.
// OpenCode's own reader treats 403 from this route as no Go plan for the key,
// not as zero usage, and so does this reader.
func (p *Provider) fetchKey(ctx context.Context, key string) (*provider.UsageData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiKeyEndpoint(), nil)
	if err != nil {
		return nil, errors.New("opencode:cannot build request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("opencode:cannot reach OpenCode's usage route")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return &provider.UsageData{Provider: p.Name(), FetchedAt: p.now()}, nil
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("opencode:%s was rejected (http 401)", apiKeyEnv)
	default:
		return nil, fmt.Errorf("opencode:OpenCode's usage route answered http %d", resp.StatusCode)
	}

	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var parsed usageResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, errors.New("opencode:malformed usage response (decode failed)")
	}
	if parsed.Usage == nil {
		return nil, errors.New("opencode:usage response has no usage object")
	}
	return p.usageData(*parsed.Usage)
}

func (p *Provider) apiKeyEndpoint() string {
	if p.apiKeyURL != "" {
		return p.apiKeyURL
	}
	return apiKeyUsageURL
}

func readBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, errors.New("opencode:cannot read usage response")
	}
	if len(body) > maxBodyBytes {
		return nil, errors.New("opencode:usage response too large")
	}
	return body, nil
}

// usageData turns the Go plan's meters into windows.
func (p *Provider) usageData(plan goPlan) (*provider.UsageData, error) {
	var windows []provider.UsageWindow
	for _, spec := range []struct {
		m           *meter
		name, label string
		length      time.Duration // zero means the length is not published
	}{
		{plan.Rolling, "5h", "5 hours", 5 * time.Hour},
		{plan.Weekly, "7d", "7 days", 168 * time.Hour},
		// OpenCode gives the monthly window only as its reset instant. A length
		// worked out from that instant would be a guess, so none is claimed.
		{plan.Monthly, "monthly", "1M", 0},
	} {
		if spec.m == nil {
			continue
		}
		reset, ok := spec.m.reset()
		if spec.m.Percent == nil || !ok {
			return nil, fmt.Errorf("opencode:malformed usage response (%s window missing percent or reset)", spec.name)
		}
		util := *spec.m.Percent
		switch {
		case spec.m.refused():
			util = 100
		case util < 0:
			util = 0
		case util > 100:
			util = 100
		}
		windows = append(windows, provider.UsageWindow{
			Name:          spec.name,
			DisplayName:   spec.label,
			Utilization:   util,
			Length:        spec.length,
			LengthUnknown: spec.length == 0,
			ResetsAt:      reset,
		})
	}
	if len(windows) == 0 {
		return nil, errors.New("opencode:usage response is missing Go plan usage windows")
	}
	return &provider.UsageData{Provider: p.Name(), FetchedAt: p.now(), Windows: windows}, nil
}
