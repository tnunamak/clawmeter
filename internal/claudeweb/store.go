package claudeweb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// GrantSummary is the saved browser reset snapshot for one Claude source.
// MatchHash is AccountHash(MatchSalt, org) for the organization the browser
// proved, so a later account switch in that source invalidates the snapshot.
type GrantSummary struct {
	Credits            []time.Time `json:"credits,omitempty"`
	ObservedAt         time.Time   `json:"observed_at"`
	ResetCreditsTarget string      `json:"reset_credits_target"`
	MatchSalt          string      `json:"match_salt"`
	MatchHash          string      `json:"match_hash"`
}

// MatchesOrg reports whether the snapshot was taken for orgUUID.
func (g GrantSummary) MatchesOrg(orgUUID string) bool {
	return orgUUID != "" && g.MatchSalt != "" && g.MatchHash == AccountHash(g.MatchSalt, orgUUID)
}

// ReadSummaryFor returns the saved snapshot for a Claude source key. Callers
// must still check MatchesOrg against the source's current organization.
func ReadSummaryFor(target string) (GrantSummary, error) {
	path, err := summaryPathFor(target)
	if err != nil {
		return GrantSummary{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GrantSummary{}, err
	}
	var summary GrantSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return GrantSummary{}, err
	}
	if summary.ObservedAt.IsZero() || len(summary.Credits) > 100 || summary.MatchSalt == "" || summary.MatchHash == "" || summary.ResetCreditsTarget != target {
		return GrantSummary{}, fmt.Errorf("invalid saved reset inventory")
	}
	for _, expiry := range summary.Credits {
		if expiry.IsZero() {
			return GrantSummary{}, fmt.Errorf("invalid saved reset inventory")
		}
	}
	return summary, nil
}

// WriteSummary stores a summary under its own ResetCreditsTarget.
func WriteSummary(summary GrantSummary) error {
	if len(summary.Credits) > 100 {
		return fmt.Errorf("too many reset credits")
	}
	path, err := summaryPathFor(summary.ResetCreditsTarget)
	if err != nil {
		return err
	}
	sort.Slice(summary.Credits, func(i, j int) bool { return summary.Credits[i].Before(summary.Credits[j]) })
	data, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode reset inventory: %w", err)
	}
	return writePrivateFile(path, data)
}

func summaryPathFor(target string) (string, error) {
	if !validResetCreditsTarget(target) {
		return "", fmt.Errorf("invalid reset-credit source")
	}
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	sourceHash := sha256.Sum256([]byte(target))
	return filepath.Join(dir, "claude-web-resets-"+hex.EncodeToString(sourceHash[:8])+".json"), nil
}

func stateDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "clawmeter"), nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Clawmeter state directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-web-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect %s: %w", filepath.Base(path), err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("finish %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save %s: %w", filepath.Base(path), err)
	}
	return nil
}
