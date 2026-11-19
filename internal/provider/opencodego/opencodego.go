// Package opencodego implements the Provider interface for the OpenCode Go
// plan. It needs no API key: it asks the local OpenCode service for the 5-hour,
// weekly and monthly usage windows.
//
// The service advertises itself in $XDG_STATE_HOME/opencode/service.json
// (default ~/.local/state/opencode/service.json) with its base URL and a
// password for HTTP basic auth. The password is never logged or placed in an
// error. The path is unverified on macOS and Windows.
//
// Budgets in the response are out of scope for v1 and are not emitted. The
// monthly window has no published length, so it is reported without one and
// is never projected.
package opencodego

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
	timeout      = 10 * time.Second
	maxBodyBytes = 1 << 20
	limitsPath   = "/api/experimental/usage/limits"

	notSignedIn = "Sign in to OpenCode Console: run `opencode auth login opencode --method device`"
)

var errNoEndpoint = errors.New("opencode-go: this OpenCode build has no usage endpoint; update OpenCode")

type Provider struct {
	client      *http.Client
	serviceFile string // "" means the default location
	baseURL     string // "" means the url in the service file
	homeDir     func() (string, error)
	now         func() time.Time
}

func New() *Provider {
	return &Provider{
		client: &http.Client{
			Timeout: timeout,
			// Never follow a redirect with the service password attached.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		homeDir: os.UserHomeDir,
		now:     time.Now,
	}
}

func (p *Provider) Name() string         { return "opencode_go" }
func (p *Provider) DisplayName() string  { return "OpenCode Go" }
func (p *Provider) Description() string  { return "OpenCode Go plan (via the local OpenCode service)" }
func (p *Provider) DashboardURL() string { return "https://opencode.ai/auth" }

// IsConfigured reports whether the OpenCode service file exists. It makes no
// network call.
func (p *Provider) IsConfigured() bool { return p.SetupStatus().IsReady() }

// SetupStatus names the step that is missing. It makes no network call.
func (p *Provider) SetupStatus() provider.SetupStatus {
	path, err := p.servicePath()
	if err != nil {
		return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "cannot locate the OpenCode service file"}
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "start OpenCode's local service with `opencode serve --service`"}
	}
	return provider.SetupStatus{State: provider.SetupReady, Detail: "OpenCode local service file found"}
}

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
		return serviceInfo{}, fmt.Errorf("opencode-go: cannot locate OpenCode service file: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return serviceInfo{}, errors.New("opencode-go: OpenCode service file not found; start OpenCode's local service with `opencode serve --service`")
		}
		return serviceInfo{}, errors.New("opencode-go: cannot read OpenCode service file")
	}
	var info serviceInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return serviceInfo{}, errors.New("opencode-go: malformed OpenCode service file")
	}
	if p.baseURL != "" {
		info.URL = p.baseURL
	}
	u, err := url.Parse(info.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return serviceInfo{}, errors.New("opencode-go: OpenCode service file has no usable url")
	}
	if info.Password == "" {
		return serviceInfo{}, errors.New("opencode-go: OpenCode service file has no password")
	}
	return info, nil
}

type meter struct {
	Percent  *float64 `json:"percent"`
	Limited  bool     `json:"limited"`
	ResetsAt *int64   `json:"resetsAt"`
}

type limitsResponse struct {
	Data *struct {
		Go *struct {
			Rolling *meter `json:"rolling"`
			Weekly  *meter `json:"weekly"`
			Monthly *meter `json:"monthly"`
		} `json:"go"`
	} `json:"data"`
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	svc, err := p.readService()
	if err != nil {
		return nil, err
	}
	home, err := p.homeDir()
	if err != nil {
		return nil, fmt.Errorf("opencode-go: cannot determine home directory: %w", err)
	}

	endpoint := strings.TrimRight(svc.URL, "/") + limitsPath + "?" +
		url.Values{"location[directory]": {home}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("opencode-go: cannot build request")
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
			return nil, errors.New("opencode-go: OpenCode service is not running; start it with `opencode serve --service`")
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, errors.New("opencode-go: OpenCode service did not answer in time; restart it with `opencode serve --service`")
		}
		return nil, errors.New("opencode-go: cannot reach the OpenCode service")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errors.New("opencode-go: OpenCode service rejected the password in service.json (http 401); restart OpenCode")
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, errNoEndpoint
	default:
		return nil, fmt.Errorf("opencode-go: unexpected status http %d", resp.StatusCode)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "text/html" {
		return nil, errNoEndpoint
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, errors.New("opencode-go: cannot read usage response")
	}
	if len(body) > maxBodyBytes {
		return nil, errors.New("opencode-go: usage response too large")
	}
	var parsed limitsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, errors.New("opencode-go: malformed usage response (decode failed)")
	}
	if parsed.Data == nil {
		return nil, errors.New(notSignedIn)
	}
	if parsed.Data.Go == nil {
		return nil, errors.New("opencode-go: usage response is missing Go plan usage; the account may not have an OpenCode Go plan")
	}

	var windows []provider.UsageWindow
	for _, spec := range []struct {
		m           *meter
		name, label string
		length      time.Duration // zero means the length is not published
	}{
		{parsed.Data.Go.Rolling, "5h", "5 hours", 5 * time.Hour},
		{parsed.Data.Go.Weekly, "7d", "7 days", 168 * time.Hour},
		// OpenCode gives the monthly window only as its reset instant. A length
		// worked out from that instant would be a guess, so none is claimed.
		{parsed.Data.Go.Monthly, "monthly", "Monthly", 0},
	} {
		if spec.m == nil {
			continue
		}
		if spec.m.Percent == nil || spec.m.ResetsAt == nil {
			return nil, fmt.Errorf("opencode-go: malformed usage response (%s window missing percent or reset)", spec.name)
		}
		util := *spec.m.Percent
		switch {
		case spec.m.Limited:
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
			ResetsAt:      time.UnixMilli(*spec.m.ResetsAt),
		})
	}
	if len(windows) == 0 {
		return nil, errors.New("opencode-go: usage response is missing Go plan usage windows")
	}
	return &provider.UsageData{Provider: p.Name(), FetchedAt: p.now(), Windows: windows}, nil
}
