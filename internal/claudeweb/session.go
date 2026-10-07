package claudeweb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/browser"
)

const (
	listenAddress = "127.0.0.1:17343"
	claudeOrigin  = "https://claude.ai"
	maxBodyBytes  = 16 * 1024
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

// GrantSummary contains only the allowlisted fields shown by the tray.
type GrantSummary struct {
	AvailableCount     int         `json:"available_count,omitempty"`
	EarliestExpiresAt  time.Time   `json:"earliest_expires_at,omitempty"`
	Credits            []time.Time `json:"credits,omitempty"`
	ObservedAt         time.Time   `json:"observed_at"`
	ResetCreditsTarget string      `json:"reset_credits_target,omitempty"`
	SourceRevision     string      `json:"source_revision,omitempty"`
}

type resultPayload struct {
	Nonce    string         `json:"nonce"`
	Count    int            `json:"count,omitempty"`
	Earliest string         `json:"earliest,omitempty"`
	Credits  []string       `json:"credits,omitempty"`
	Grants   []grantPayload `json:"grants,omitempty"`
}

type grantPayload struct {
	ResetsLeft int    `json:"resets_left"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
	Paused     *bool  `json:"paused"`
}

type Session struct {
	server             *http.Server
	nonce              string
	result             chan GrantSummary
	resetCreditsTarget string
	sourceRevision     string
	once               sync.Once
}

// StartForSource starts a one-time browser handoff for an explicitly selected
// Clawmeter Claude source. There is deliberately no source-less variant.
func StartForSource(target, sourceRevision string) (*Session, error) {
	if !validResetCreditsTarget(target) {
		return nil, fmt.Errorf("Claude reset check requires an explicitly selected Claude source")
	}
	listener, err := net.Listen("tcp4", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("start local Claude Web check: %w", err)
	}
	var nonceBytes [32]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("create local Claude Web check token: %w", err)
	}
	s := &Session{nonce: hex.EncodeToString(nonceBytes[:]), result: make(chan GrantSummary, 1), resetCreditsTarget: target, sourceRevision: sourceRevision}
	s.server = &http.Server{Handler: s.handler(), ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

// Close lets an in-flight /result response reach the browser before the
// listener stops; Wait can return while that handler is still writing.
func (s *Session) Close() {
	if s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.server.Shutdown(ctx) != nil {
		_ = s.server.Close()
	}
}

func (s *Session) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/challenge", s.handleChallenge)
	mux.HandleFunc("/result", s.handleResult)
	return mux
}

func (s *Session) handleChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		if allowClaudeOrigin(w, r) {
			allowPreflight(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet || !allowClaudeOrigin(w, r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"nonce": s.nonce})
}

func (s *Session) handleResult(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		if allowClaudeOrigin(w, r) {
			allowPreflight(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost || !allowClaudeOrigin(w, r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	var payload resultPayload
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		http.Error(w, "invalid result", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "invalid result", http.StatusBadRequest)
		return
	}
	if payload.Nonce != s.nonce || payload.Count < 0 || payload.Count > 100 || len(payload.Credits) > 100 || len(payload.Grants) > 100 {
		http.Error(w, "invalid result", http.StatusBadRequest)
		return
	}
	now := time.Now()
	credits := make([]time.Time, 0, len(payload.Credits))
	earliest := time.Time{}
	if payload.Grants != nil {
		if payload.Credits != nil || payload.Count != 0 || payload.Earliest != "" {
			http.Error(w, "inconsistent reset inventory", http.StatusBadRequest)
			return
		}
		var err error
		credits, err = activeGrantExpiries(payload.Grants, now)
		if err != nil {
			http.Error(w, "invalid grant inventory", http.StatusBadRequest)
			return
		}
	} else {
		for _, value := range payload.Credits {
			expiresAt, err := time.Parse(time.RFC3339Nano, value)
			if err != nil || !expiresAt.After(now) {
				http.Error(w, "invalid expiry", http.StatusBadRequest)
				return
			}
			credits = append(credits, expiresAt)
			if earliest.IsZero() || expiresAt.Before(earliest) {
				earliest = expiresAt
			}
		}
		if payload.Credits == nil {
			if payload.Count == 0 {
				if payload.Earliest != "" {
					http.Error(w, "invalid expiry", http.StatusBadRequest)
					return
				}
			} else {
				legacyExpiry, err := time.Parse(time.RFC3339Nano, payload.Earliest)
				if err != nil || !legacyExpiry.After(now) {
					http.Error(w, "invalid expiry", http.StatusBadRequest)
					return
				}
				earliest = legacyExpiry
			}
		} else {
			if payload.Count != len(credits) || (payload.Count == 0 && payload.Earliest != "") {
				http.Error(w, "inconsistent reset inventory", http.StatusBadRequest)
				return
			}
			if payload.Count > 0 {
				payloadEarliest, err := time.Parse(time.RFC3339Nano, payload.Earliest)
				if err != nil || !payloadEarliest.Equal(earliest) {
					http.Error(w, "inconsistent reset inventory", http.StatusBadRequest)
					return
				}
			}
		}
	}
	if len(credits) > 0 {
		sort.Slice(credits, func(i, j int) bool { return credits[i].Before(credits[j]) })
		earliest = credits[0]
	}
	availableCount := len(credits)
	if payload.Grants == nil && payload.Credits == nil {
		availableCount = payload.Count
	}
	summary := GrantSummary{Credits: credits, AvailableCount: availableCount, EarliestExpiresAt: earliest, ObservedAt: now, ResetCreditsTarget: s.resetCreditsTarget, SourceRevision: s.sourceRevision}
	var saveErr error
	accepted := false
	s.once.Do(func() {
		if saveErr = WriteSummary(summary); saveErr != nil {
			return
		}
		s.result <- summary
		accepted = true
	})
	if saveErr != nil {
		http.Error(w, "could not save reset inventory", http.StatusInternalServerError)
		return
	}
	if !accepted {
		http.Error(w, "check already completed", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"result": "recorded"})
	go func() { time.Sleep(25 * time.Millisecond); s.Close() }()
}

func activeGrantExpiries(grants []grantPayload, now time.Time) ([]time.Time, error) {
	credits := make([]time.Time, 0)
	for _, grant := range grants {
		if grant.ResetsLeft < 0 || grant.ResetsLeft > 100 {
			return nil, fmt.Errorf("invalid remaining reset count")
		}
		if grant.ResetsLeft == 0 || (grant.Paused != nil && *grant.Paused) {
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
		if len(credits)+grant.ResetsLeft > 100 {
			return nil, fmt.Errorf("too many active grants")
		}
		for range grant.ResetsLeft {
			credits = append(credits, expiresAt)
		}
	}
	return credits, nil
}

// ReadSummaryFor returns the browser observation explicitly associated with a
// Clawmeter Claude source. Source keys, not provider account identifiers, key
// the local files.
func ReadSummaryFor(target string) (GrantSummary, error) {
	path, err := summaryPathFor(target)
	if err != nil {
		return GrantSummary{}, err
	}
	summary, err := readSummaryFile(path)
	if err != nil {
		return GrantSummary{}, err
	}
	if summary.ResetCreditsTarget != target {
		return GrantSummary{}, fmt.Errorf("saved reset inventory source mismatch")
	}
	return summary, nil
}

func readSummaryFile(path string) (GrantSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GrantSummary{}, err
	}
	var summary GrantSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return GrantSummary{}, err
	}
	if summary.ObservedAt.IsZero() || summary.AvailableCount < 0 || summary.AvailableCount > 100 || len(summary.Credits) > 100 {
		return GrantSummary{}, fmt.Errorf("invalid saved reset inventory")
	}
	if !validResetCreditsTarget(summary.ResetCreditsTarget) {
		return GrantSummary{}, fmt.Errorf("invalid saved reset inventory")
	}
	if len(summary.Credits) > 0 && summary.AvailableCount != len(summary.Credits) {
		return GrantSummary{}, fmt.Errorf("invalid saved reset inventory")
	}
	if summary.AvailableCount > 0 && summary.EarliestExpiresAt.IsZero() {
		return GrantSummary{}, fmt.Errorf("invalid saved reset inventory")
	}
	if summary.AvailableCount == 0 && !summary.EarliestExpiresAt.IsZero() {
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
	return WriteSummaryFor(summary.ResetCreditsTarget, summary)
}

func WriteSummaryFor(target string, summary GrantSummary) error {
	if !validResetCreditsTarget(target) {
		return fmt.Errorf("invalid reset-credit source")
	}
	summary.ResetCreditsTarget = target
	return writeSummaryFile(summary, func() (string, error) { return summaryPathFor(target) })
}

func writeSummaryFile(summary GrantSummary, pathFor func() (string, error)) error {
	if len(summary.Credits) > 100 {
		return fmt.Errorf("too many reset credits")
	}
	if len(summary.Credits) > 0 {
		sort.Slice(summary.Credits, func(i, j int) bool { return summary.Credits[i].Before(summary.Credits[j]) })
		summary.AvailableCount = len(summary.Credits)
		summary.EarliestExpiresAt = summary.Credits[0]
	} else if summary.AvailableCount == 0 {
		summary.EarliestExpiresAt = time.Time{}
	} else if summary.EarliestExpiresAt.IsZero() {
		return fmt.Errorf("reset inventory count requires an expiry")
	}
	path, err := pathFor()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create reset inventory directory: %w", err)
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode reset inventory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-web-resets-*")
	if err != nil {
		return fmt.Errorf("create reset inventory: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect reset inventory: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write reset inventory: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("finish reset inventory: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save reset inventory: %w", err)
	}
	return nil
}

// SetupStarted reports whether the reusable bookmark setup page was opened.
// The bookmark works for every Claude source, so the state is not per source.
func SetupStarted() bool {
	path, err := setupMarkerPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func MarkSetupStarted() error {
	path, err := setupMarkerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Claude reset setup directory: %w", err)
	}
	if err := os.WriteFile(path, []byte("setup opened\n"), 0o600); err != nil {
		return fmt.Errorf("save Claude reset setup state: %w", err)
	}
	return nil
}

func summaryPathFor(target string) (string, error) {
	if !validResetCreditsTarget(target) {
		return "", fmt.Errorf("invalid reset-credit source")
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	sourceHash := sha256.Sum256([]byte(target))
	return filepath.Join(cacheDir, "clawmeter", "claude-web-resets-"+hex.EncodeToString(sourceHash[:8])+".json"), nil
}

func setupMarkerPath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "clawmeter", "claude-web-reset-setup.started"), nil
}

func allowClaudeOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != claudeOrigin {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", claudeOrigin)
	w.Header().Set("Vary", "Origin")
	return true
}

func allowPreflight(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
}

func (s *Session) Wait(ctx context.Context) (GrantSummary, error) {
	select {
	case summary := <-s.result:
		return summary, nil
	case <-ctx.Done():
		return GrantSummary{}, ctx.Err()
	}
}

//go:embed bookmarklet.js
var bookmarkletBody string

// Bookmarklet prints a single javascript: URL intended for a user-created bookmark.
func Bookmarklet() string { return "javascript:" + strings.Join(strings.Fields(bookmarkletBody), " ") }

type setupPageData struct {
	Bookmark template.URL
}

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Set up Claude Web reset checks</title>
<style>
  body { max-width: 42rem; margin: 3rem auto; padding: 0 1.25rem; font: 16px/1.5 system-ui, sans-serif; color: #202124; }
  h1 { font-size: 1.5rem; }
  a, button { font: inherit; }
  .bookmark { display: inline-block; margin: 1rem 0; padding: .6rem .9rem; border: 1px solid #777; border-radius: 4px; color: #111; background: #f5f5f5; }
  button { margin-left: .5rem; padding: .6rem .9rem; }
  #status { min-height: 1.5em; }
</style>
<h1>Set up Claude Web reset checks</h1>
<p>Save this bookmark once. It reads only reset grant timing and remaining count from the Claude Usage page already open in your browser.</p>
<p><a class="bookmark" id="bookmarklet" href="{{.Bookmark}}" draggable="true">Clawmeter reset check</a><button id="copy" type="button">Copy bookmark URL</button></p>
<p>Do not click the link on this setup page. Save it as a bookmark, then click the saved bookmark on Claude’s Usage page.</p>
<p>Drag the link to your bookmarks bar, or use your browser’s bookmark manager to create a bookmark and paste the copied URL into its URL field. The bookmarks bar can stay hidden; run the saved bookmark from the Bookmarks menu. Then return to Clawmeter and choose <strong>Check Claude reset</strong>.</p>
<p>Save this bookmark once; it can be reused for any Claude source. Each check asks which local source will receive the result. Before confirming, make sure the browser is signed into the same Claude account as that source; Clawmeter cannot verify the match.</p>
<p>Your browser may ask whether claude.ai can connect to devices on your local network. That browser permission is broader than Clawmeter’s single local connection. The check runs only when you click the bookmark.</p>
<p id="status" role="status"></p>
<script>
  document.querySelector("#copy").addEventListener("click", async () => {
    const value = document.querySelector("#bookmarklet").getAttribute("href");
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      const field = document.createElement("textarea");
      field.value = value;
      document.body.append(field);
      field.select();
      document.execCommand("copy");
      field.remove();
    }
    document.querySelector("#status").textContent = "Copied. In your bookmark manager, add a bookmark and paste this into its URL field. The bookmarks bar is optional.";
  });
</script>
</html>`))

// OpenSetupPage writes a static setup page to the user's cache directory and
// opens it in the default browser. It contains no account data or credentials.
func OpenSetupPage() error {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("find user cache directory: %w", err)
	}
	dir := filepath.Join(cacheDir, "clawmeter")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create setup page directory: %w", err)
	}
	path := filepath.Join(dir, "claude-web-reset-setup.html")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write setup page: %w", err)
	}
	if err := setupPage.Execute(file, setupPageData{Bookmark: template.URL(Bookmarklet())}); err != nil {
		_ = file.Close()
		return fmt.Errorf("render setup page: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("finish setup page: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect setup page: %w", err)
	}
	return browser.OpenURL((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
}
