package claudeweb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	claudeOrigin = "https://claude.ai"
	maxBodyBytes = 16 * 1024
)

// UsageURL is the Claude page that shows a plan's usage and reset grants.
const UsageURL = claudeOrigin + "/settings/usage"

const (
	msgUnreadable = "Claude sent reset data Clawmeter can't read. Nothing was saved."
	msgSaveFailed = "Couldn't save the result."
)

func validResetCreditsTarget(target string) bool {
	if target == "claude" {
		return true
	}
	id, ok := strings.CutPrefix(target, "claude:")
	if !ok || len(id) == 0 || len(id) > 64 {
		return false
	}
	if first := id[0]; (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// Account is a local Claude source that may receive a browser reading.
type Account struct {
	Key     string
	Label   string
	OrgUUID string
}

// AccountHash binds an organization ID to a random per-snapshot salt, so a
// saved snapshot can be matched to its organization without storing the ID.
func AccountHash(salt, orgUUID string) string {
	sum := sha256.Sum256([]byte("clawmeter-claude-reset\x00" + salt + "\x00" + orgUUID))
	return hex.EncodeToString(sum[:])
}

type grantPayload struct {
	ResetsLeft *int   `json:"resets_left"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
	Paused     *bool  `json:"paused"`
}

// Result is one saved browser reading: Resets or APICredits.
type Result struct {
	Resets     GrantSummary
	APICredits APICreditPool
}

// IsResets reports whether the result holds reset grants.
func (r Result) IsResets() bool { return r.Resets.ResetCreditsTarget != "" }

func randomHex() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("create salt: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// admit allows only the given origins, answers preflights, and reports
// whether the caller should handle the request.
func admit(w http.ResponseWriter, r *http.Request, method string, origins ...string) bool {
	origin := r.Header.Get("Origin")
	allowed := false
	for _, candidate := range origins {
		allowed = allowed || origin == candidate
	}
	if !allowed {
		http.Error(w, "not allowed", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Clawmeter-Token")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != method {
		http.Error(w, "not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func activeGrantExpiries(grants []grantPayload, now time.Time) ([]time.Time, error) {
	if len(grants) > 100 {
		return nil, fmt.Errorf("too many grants")
	}
	credits := make([]time.Time, 0)
	for _, grant := range grants {
		// A missing count must not read as zero and erase saved resets.
		if grant.ResetsLeft == nil || *grant.ResetsLeft < 0 || *grant.ResetsLeft > 100 {
			return nil, fmt.Errorf("invalid remaining reset count")
		}
		left := *grant.ResetsLeft
		if left == 0 || (grant.Paused != nil && *grant.Paused) {
			continue
		}
		if grant.Paused == nil {
			return nil, fmt.Errorf("missing grant pause status")
		}
		startsAt, err := time.Parse(time.RFC3339Nano, grant.StartsAt)
		if err != nil {
			return nil, fmt.Errorf("invalid grant start")
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, grant.EndsAt)
		if err != nil {
			return nil, fmt.Errorf("invalid grant expiry")
		}
		if !expiresAt.After(now) || startsAt.After(now) {
			continue
		}
		if len(credits)+left > 100 {
			return nil, fmt.Errorf("too many active grants")
		}
		for range left {
			credits = append(credits, expiresAt)
		}
	}
	return credits, nil
}
