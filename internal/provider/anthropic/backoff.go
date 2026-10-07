package anthropic

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The usage endpoint is shared with Claude Code's own /usage command, and it
// answers 429 with "retry-after: 0". Honoring that literally would make every
// CLI call retry at once, so after a 429 Clawmeter stops calling the endpoint
// for a floor of 5 minutes, doubling per consecutive 429 up to 1 hour. The
// state lives on disk because the CLI and the tray are separate processes.
const (
	backoffFloor = 5 * time.Minute
	backoffCap   = time.Hour
)

var (
	now          = time.Now
	userCacheDir = os.UserCacheDir
)

type backoffState struct {
	Until    time.Time `json:"until"`
	Failures int       `json:"failures"`
}

func (p *Provider) backoffKey() string {
	return p.SourceID() + "|" + p.configDir
}

// backoffPath gives each source its own file, so concurrent 429s from
// different sources (or from the CLI and the tray) never rewrite each other's
// state. Two writers for the same source compute near-identical state, so last
// writer wins is acceptable there.
func (p *Provider) backoffPath() (string, error) {
	dir, err := userCacheDir()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%x.json", sha256.Sum256([]byte(p.backoffKey())))
	return filepath.Join(dir, "clawmeter", "claude-backoff", name), nil
}

func (p *Provider) readBackoff() (backoffState, bool) {
	var state backoffState
	path, err := p.backoffPath()
	if err != nil {
		return state, false
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &state) != nil {
		return backoffState{}, false
	}
	return state, true
}

func (p *Provider) writeBackoff(state backoffState) {
	path, err := p.backoffPath()
	if err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backoff-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// backoffUntil returns the time before which this source must not be polled.
func (p *Provider) backoffUntil() (time.Time, bool) {
	state, ok := p.readBackoff()
	if !ok || !now().Before(state.Until) {
		return time.Time{}, false
	}
	return state.Until, true
}

// recordRateLimit starts or extends the backoff after a 429 and returns when
// polling may resume. A positive retry-after longer than the computed delay wins.
func (p *Provider) recordRateLimit(retryAfter string) time.Time {
	state, _ := p.readBackoff()
	state.Failures++
	delay := backoffFloor
	for i := 1; i < state.Failures && delay < backoffCap; i++ {
		delay *= 2
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
		if d := time.Duration(secs) * time.Second; d > delay {
			delay = d
		}
	}
	if delay > backoffCap {
		delay = backoffCap
	}
	state.Until = now().Add(delay)
	p.writeBackoff(state)
	return state.Until
}

func (p *Provider) clearBackoff() {
	if path, err := p.backoffPath(); err == nil {
		_ = os.Remove(path)
	}
}

func rateLimitedMessage(until time.Time) string {
	return "rate limited (429), next try " + until.Local().Format("15:04")
}
