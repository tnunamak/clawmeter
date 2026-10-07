package anthropic

import (
	"encoding/json"
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

func backoffPath() (string, error) {
	dir, err := userCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clawmeter", "claude-backoff.json"), nil
}

func readBackoff() map[string]backoffState {
	states := map[string]backoffState{}
	path, err := backoffPath()
	if err != nil {
		return states
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &states)
	}
	return states
}

func writeBackoff(states map[string]backoffState) {
	path, err := backoffPath()
	if err != nil {
		return
	}
	data, err := json.Marshal(states)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// backoffUntil returns the time before which this source must not be polled.
func (p *Provider) backoffUntil() (time.Time, bool) {
	state, ok := readBackoff()[p.backoffKey()]
	if !ok || !now().Before(state.Until) {
		return time.Time{}, false
	}
	return state.Until, true
}

// recordRateLimit starts or extends the backoff after a 429 and returns when
// polling may resume. A positive retry-after longer than the computed delay wins.
func (p *Provider) recordRateLimit(retryAfter string) time.Time {
	states := readBackoff()
	key := p.backoffKey()
	state := states[key]
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
	states[key] = state
	writeBackoff(states)
	return state.Until
}

func (p *Provider) clearBackoff() {
	states := readBackoff()
	if _, ok := states[p.backoffKey()]; ok {
		delete(states, p.backoffKey())
		writeBackoff(states)
	}
}

func rateLimitedMessage(until time.Time) string {
	return "rate limited (429), next try " + until.Local().Format("15:04")
}
