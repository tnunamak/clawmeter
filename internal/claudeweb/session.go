package claudeweb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	listenAddress = "127.0.0.1:17343"
	claudeOrigin  = "https://claude.ai"
	maxBodyBytes  = 16 * 1024
)

// Window is how long a check waits for the bookmark: long enough to save the
// bookmark first.
const Window = 10 * time.Minute

// CheckURL is the local page a check opens. It shows the steps and live
// progress while its session is running.
const CheckURL = "http://" + listenAddress + "/check"

// UsageURL is the Claude page where the bookmark runs.
const UsageURL = claudeOrigin + "/settings/usage"

// ErrTimedOut means the check window ended without a result.
var ErrTimedOut = errors.New("claude reset check timed out")

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

// Account is a local Claude source that may receive a browser result. OrgUUID
// never leaves this process: the browser proves a match with AccountHash.
type Account struct {
	Key     string
	Label   string
	OrgUUID string
}

// AccountHash binds an organization ID to a salt (the session nonce in the
// browser, a random per-snapshot salt on disk) without revealing the ID.
// bookmarklet.js computes the same value.
func AccountHash(salt, orgUUID string) string {
	sum := sha256.Sum256([]byte("clawmeter-claude-reset\x00" + salt + "\x00" + orgUUID))
	return hex.EncodeToString(sum[:])
}

type resultPayload struct {
	Nonce   string         `json:"nonce"`
	Account string         `json:"account"`
	Grants  []grantPayload `json:"grants"`
}

type grantPayload struct {
	ResetsLeft int    `json:"resets_left"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
	Paused     *bool  `json:"paused"`
}

// Check states shown on the local page. "retry" means the bookmark ran but
// the user must act and click it again.
const (
	stateWaiting = "waiting"
	stateRetry   = "retry"
	stateDone    = "done"
	stateEnded   = "ended"
)

// Status is what the check page polls.
type Status struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// Copy shown by both the bookmark alert (after "Clawmeter: ") and the page.
const (
	msgWaiting    = "Waiting for the bookmark…"
	msgOutdated   = "This bookmark is out of date. Save it again from the Clawmeter page."
	msgEnded      = "This check ended. Choose Check Claude resets in the tray to start again."
	msgTimedOut   = "Timed out. Choose Check Claude resets in the tray to start again."
	msgUnreadable = "Claude sent reset data Clawmeter can't read. Nothing was saved."
	msgNotSetUp   = "This Claude account isn't in Clawmeter. Switch to the account you use with Claude Code, then click the bookmark again."
	msgAmbiguous  = "Two Clawmeter Claude profiles use this account, so Clawmeter can't tell where to save it."
	msgSaveFailed = "Couldn't save the result. Click the bookmark again."
	msgDone       = "Already saved. You can close the Clawmeter tab."
)

type Session struct {
	server   *http.Server
	nonce    string
	accounts []Account
	timer    *time.Timer

	mu        sync.Mutex
	status    Status
	result    GrantSummary
	saved     bool
	done      chan struct{}
	closeOnce sync.Once
}

// Start opens the loopback handoff for Window. The browser result is filed
// under the single account whose organization matches the browser's.
func Start(accounts []Account) (*Session, error) {
	listener, err := net.Listen("tcp4", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("start Claude reset check: %w", err)
	}
	nonce, err := randomHex()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	s := newSession(nonce, accounts)
	s.server = &http.Server{Handler: s.handler(), ReadHeaderTimeout: 2 * time.Second}
	s.timer = time.AfterFunc(Window, s.timeOut)
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

func newSession(nonce string, accounts []Account) *Session {
	return &Session{nonce: nonce, accounts: accounts, status: Status{State: stateWaiting, Message: msgWaiting}, done: make(chan struct{})}
}

func randomHex() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("create Claude reset check token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Wait returns the saved result, or ErrTimedOut when the window ends first.
func (s *Session) Wait(ctx context.Context) (GrantSummary, error) {
	select {
	case <-s.done:
	case <-ctx.Done():
		return GrantSummary{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.saved {
		return GrantSummary{}, ErrTimedOut
	}
	return s.result, nil
}

// timeOut ends a check that got no result. If the bookmark never ran, it may
// be missing, so the next check shows the setup steps again.
func (s *Session) timeOut() {
	s.mu.Lock()
	if s.saved {
		s.mu.Unlock()
		return
	}
	s.status = Status{State: stateEnded, Message: msgTimedOut}
	s.mu.Unlock()
	s.end()
}

// end releases Wait. The listener keeps serving until Finish or Close so the
// page can still read the final state.
func (s *Session) end() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Finish gives the page a few polls to show the final state, then closes.
func (s *Session) Finish() {
	time.Sleep(5 * time.Second)
	s.Close()
}

// Close stops the listener, letting in-flight responses finish.
func (s *Session) Close() {
	if s.timer != nil {
		s.timer.Stop()
	}
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
	mux.HandleFunc("/check", s.handleCheckPage)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/challenge", s.handleChallenge)
	mux.HandleFunc("/result", s.handleResult)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject DNS-rebinding requests: only the loopback name is served.
		if r.Host != listenAddress {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Session) handleCheckPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = checkPage.Execute(w, checkPageData{Bookmark: template.URL(Bookmarklet()), UsageURL: UsageURL, Ended: msgEnded})
}

// handleStatus serves the page's poll. It has no CORS headers, so other
// sites cannot read it.
func (s *Session) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.currentStatus())
}

func (s *Session) currentStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Session) handleChallenge(w http.ResponseWriter, r *http.Request) {
	if !admitClaude(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"nonce": s.nonce})
}

// admitClaude allows only claude.ai, answers preflights, and reports whether
// the caller should handle the request.
func admitClaude(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Header.Get("Origin") != claudeOrigin {
		http.Error(w, "not allowed", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", claudeOrigin)
	w.Header().Set("Vary", "Origin")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// reply answers the bookmark and mirrors a retryable problem on the page.
func (s *Session) reply(w http.ResponseWriter, status int, message string, showOnPage bool) {
	if showOnPage {
		s.mu.Lock()
		if s.status.State == stateWaiting || s.status.State == stateRetry {
			s.status = Status{State: stateRetry, Message: message}
		}
		s.mu.Unlock()
	}
	writeJSON(w, status, map[string]string{"message": message})
}

func (s *Session) handleResult(w http.ResponseWriter, r *http.Request) {
	if !admitClaude(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	var payload resultPayload
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&payload) != nil || decoder.Decode(&trailing) != io.EOF || payload.Account == "" || payload.Grants == nil {
		s.reply(w, http.StatusBadRequest, msgOutdated, true)
		return
	}
	if payload.Nonce != s.nonce {
		s.reply(w, http.StatusBadRequest, msgEnded, false)
		return
	}
	now := time.Now()
	credits, err := activeGrantExpiries(payload.Grants, now)
	if err != nil {
		s.reply(w, http.StatusBadRequest, msgUnreadable, true)
		return
	}
	account, status, message := s.matchAccount(payload.Account)
	if status != http.StatusOK {
		s.reply(w, status, message, true)
		return
	}
	salt, err := randomHex()
	if err != nil {
		s.reply(w, http.StatusInternalServerError, msgSaveFailed, true)
		return
	}
	summary := GrantSummary{Credits: credits, ObservedAt: now, ResetCreditsTarget: account.Key, MatchSalt: salt, MatchHash: AccountHash(salt, account.OrgUUID)}

	s.mu.Lock()
	if s.saved {
		s.mu.Unlock()
		s.reply(w, http.StatusConflict, msgDone, false)
		return
	}
	if s.status.State == stateEnded {
		// Timed out or closed before this result arrived; nothing is saved.
		message := s.status.Message
		s.mu.Unlock()
		s.reply(w, http.StatusGone, message, false)
		return
	}
	if err := WriteSummary(summary); err != nil {
		s.mu.Unlock()
		s.reply(w, http.StatusInternalServerError, msgSaveFailed, true)
		return
	}
	found := FoundMessage(len(credits), account.Label)
	s.result, s.saved = summary, true
	s.status = Status{State: stateDone, Message: found + " You can close this tab."}
	s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": found})
	s.end()
}

// matchAccount finds the single local account whose organization hashes to
// the browser's proof under this session's nonce.
func (s *Session) matchAccount(proof string) (Account, int, string) {
	var matches []Account
	for _, account := range s.accounts {
		if account.OrgUUID != "" && validResetCreditsTarget(account.Key) && AccountHash(s.nonce, account.OrgUUID) == proof {
			matches = append(matches, account)
		}
	}
	switch len(matches) {
	case 0:
		return Account{}, http.StatusNotFound, msgNotSetUp
	case 1:
		return matches[0], http.StatusOK, ""
	default:
		return Account{}, http.StatusConflict, msgAmbiguous
	}
}

// FoundMessage reports a saved check, for example "Found 1 reset for ODL."
func FoundMessage(count int, label string) string {
	switch count {
	case 0:
		return "No resets found for " + label + "."
	case 1:
		return "Found 1 reset for " + label + "."
	default:
		return fmt.Sprintf("Found %d resets for %s.", count, label)
	}
}

func activeGrantExpiries(grants []grantPayload, now time.Time) ([]time.Time, error) {
	if len(grants) > 100 {
		return nil, fmt.Errorf("too many grants")
	}
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

//go:embed bookmarklet.js
var bookmarkletBody string

// Bookmarklet returns the single javascript: URL the user saves as a bookmark.
func Bookmarklet() string { return "javascript:" + strings.Join(strings.Fields(bookmarkletBody), " ") }
