// Package openaicredits persists normalized receipts, never vendor responses.
package openaicredits

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxAmount int64 = 100000000000
const maxFile = 256 * 1024
const maxPools = 20

var poolPattern = regexp.MustCompile("^[0-9a-f]{64}$")
var errReceipt = errors.New("invalid OpenAI credit receipt")

type Snapshot struct {
	Pool    string   `json:"pool"`
	Name    string   `json:"name"`
	Credits *Credits `json:"credits,omitempty"`
	Benefit *Benefit `json:"benefit,omitempty"`
	Link    *Link    `json:"link,omitempty"`
}

// Link is observed independently: learning that an account was linked must
// invalidate a claim without making an old monetary offer look fresh.
type Link struct {
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observed_at"`
}
type Credits struct {
	Currency    string    `json:"currency"`
	Balance     int64     `json:"balance"`
	PaidBalance *int64    `json:"paid_balance,omitempty"`
	ObservedAt  time.Time `json:"observed_at"`
	Grants      []Grant   `json:"grants"`
}

func (c *Credits) UnmarshalJSON(data []byte) error {
	type value Credits
	var v value
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errReceipt
	}
	for _, key := range []string{"balance", "grants"} {
		if len(fields[key]) == 0 || bytes.Equal(fields[key], []byte("null")) {
			return errReceipt
		}
	}
	*c = Credits(v)
	return nil
}

type Grant struct {
	Name      string    `json:"name"`
	Granted   int64     `json:"granted"`
	Remaining int64     `json:"remaining"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func (g *Grant) UnmarshalJSON(data []byte) error {
	type value Grant
	var v value
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errReceipt
	}
	for _, key := range []string{"granted", "remaining"} {
		if len(fields[key]) == 0 || bytes.Equal(fields[key], []byte("null")) {
			return errReceipt
		}
	}
	*g = Grant(v)
	return nil
}

type Benefit struct {
	Eligible        *bool     `json:"eligible"`
	Currency        string    `json:"currency,omitempty"`
	Cadence         string    `json:"cadence,omitempty"`
	PeriodCredit    int64     `json:"period_credit,omitempty"`
	ClaimableCredit *int64    `json:"claimable_credit,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitzero"`
	ObservedAt      time.Time `json:"observed_at"`
	LinkState       string    `json:"link_state"`
}
type Store struct {
	Dir string
	mu  sync.Mutex
}

func printable(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) != s || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 120 {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
func amount(n int64) bool        { return n >= 0 && n <= maxAmount }
func (s Snapshot) Validate() error {
	if !poolPattern.MatchString(s.Pool) || !printable(s.Name) || (s.Credits == nil && s.Benefit == nil && s.Link == nil) {
		return errReceipt
	}
	if l := s.Link; l != nil {
		if (l.State != "unknown" && l.State != "unlinked" && l.State != "linked") || !validTime(l.ObservedAt) {
			return errReceipt
		}
	}
	if c := s.Credits; c != nil {
		if c.Currency != "USD" || c.Balance < -maxAmount || c.Balance > maxAmount || !validTime(c.ObservedAt) || len(c.Grants) > 200 {
			return errReceipt
		}
		if c.PaidBalance != nil && (*c.PaidBalance < -maxAmount || *c.PaidBalance > maxAmount) {
			return errReceipt
		}
		for _, g := range c.Grants {
			if !printable(g.Name) || !amount(g.Granted) || !amount(g.Remaining) || g.Remaining > g.Granted || !validTime(g.GrantedAt) {
				return errReceipt
			}
			if !g.ExpiresAt.IsZero() && (!validTime(g.ExpiresAt) || !g.ExpiresAt.After(g.GrantedAt)) {
				return errReceipt
			}
		}
	}
	if b := s.Benefit; b != nil {
		if b.Eligible == nil || !validTime(b.ObservedAt) || (b.LinkState != "unknown" && b.LinkState != "unlinked" && b.LinkState != "linked") {
			return errReceipt
		}
		if *b.Eligible {
			if b.Currency != "USD" || (b.Cadence != "monthly" && b.Cadence != "annual") || !amount(b.PeriodCredit) || b.PeriodCredit == 0 || !validTime(b.ExpiresAt) {
				return errReceipt
			}
			if b.ClaimableCredit != nil && (!amount(*b.ClaimableCredit) || *b.ClaimableCredit > b.PeriodCredit) {
				return errReceipt
			}
		} else if b.Currency != "" || b.Cadence != "" || b.PeriodCredit != 0 || b.ClaimableCredit != nil || !b.ExpiresAt.IsZero() {
			return errReceipt
		}
	}
	return nil
}
func New() (*Store, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	s := &Store{Dir: filepath.Join(base, "clawmeter", "openai-api-credits")}
	if err := s.prepare(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) directory() error {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return errors.New("empty receipt directory")
	}
	st, err := os.Lstat(s.Dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("receipt directory is not a regular directory")
	}
	return nil
}
func (s *Store) prepare() error {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return errors.New("empty receipt directory")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	if err := s.directory(); err != nil {
		return err
	}
	return os.Chmod(s.Dir, 0700)
}

// readReceipt checks identity before reading; symlink and replaced files are skipped.
// The private directory is the trust boundary; other Store instances must serialize writes.
func readReceipt(path, pool string) (Snapshot, error) {
	var s Snapshot
	before, err := os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxFile {
		return s, errReceipt
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(before, actual) {
		return s, errReceipt
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(actual, after) {
		return s, errReceipt
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || len(data) > maxFile {
		return s, errReceipt
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, errReceipt
	}
	if dec.Decode(new(any)) != io.EOF || s.Pool != pool {
		return s, errReceipt
	}
	// Required zero-valued money fields must still be present in persisted JSON.
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return s, errReceipt
	}
	if s.Credits != nil {
		var c map[string]json.RawMessage
		if json.Unmarshal(fields["credits"], &c) != nil || len(c["balance"]) == 0 || bytes.Equal(c["balance"], []byte("null")) || len(c["grants"]) == 0 {
			return s, errReceipt
		}
		var gs []map[string]json.RawMessage
		if json.Unmarshal(c["grants"], &gs) != nil {
			return s, errReceipt
		}
		for _, g := range gs {
			for _, key := range []string{"granted", "remaining"} {
				if len(g[key]) == 0 || bytes.Equal(g[key], []byte("null")) {
					return s, errReceipt
				}
			}
		}
	}
	return s, s.Validate()
}
func (s *Store) readAll() ([]Snapshot, error) {
	result := []Snapshot{}
	if err := s.directory(); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(64)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			pool := strings.TrimSuffix(e.Name(), ".json")
			if !poolPattern.MatchString(pool) {
				continue
			}
			receipt, err := readReceipt(filepath.Join(s.Dir, e.Name()), pool)
			if err != nil {
				continue
			}
			result = append(result, receipt)
			sort.Slice(result, func(i, j int) bool {
				if result[i].Name != result[j].Name {
					return result[i].Name < result[j].Name
				}
				return result[i].Pool < result[j].Pool
			})
			if len(result) > maxPools {
				result = result[:maxPools]
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Pool < result[j].Pool
	})
	if len(result) > maxPools {
		result = result[:maxPools]
	}
	return result, nil
}
func (s *Store) ReadAll() ([]Snapshot, error) {
	if s == nil {
		return nil, errReceipt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAll()
}
func fresh(t, now time.Time) bool {
	return !t.Before(now.Add(-5*time.Minute)) && !t.After(now.Add(time.Minute))
}

// Save either persists the entire accepted merge or leaves the existing receipt unchanged.
func (s *Store) Save(update Snapshot, now time.Time) error {
	if s == nil {
		return errReceipt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := update.Validate(); err != nil {
		return err
	}
	// Copy caller-owned slices/pointers and canonicalize time representation for idempotence.
	update = canonical(update)
	if update.Link != nil && !fresh(update.Link.ObservedAt, now) {
		return errors.New("stale or future link observation")
	}
	if !validTime(now) || (update.Credits != nil && !fresh(update.Credits.ObservedAt, now)) || (update.Benefit != nil && !fresh(update.Benefit.ObservedAt, now)) {
		return errors.New("stale or future receipt")
	}
	if err := s.prepare(); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, update.Pool+".json")
	old, err := readReceipt(path, update.Pool)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("existing receipt cannot be safely merged: %w", err)
	}
	if os.IsNotExist(err) {
		dir, err := os.Open(s.Dir)
		if err != nil {
			return err
		}
		count := 0
		for {
			entries, readErr := dir.ReadDir(64)
			if readErr != nil && readErr != io.EOF {
				dir.Close()
				return readErr
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".json") && poolPattern.MatchString(strings.TrimSuffix(e.Name(), ".json")) {
					count++
				}
			}
			if count >= maxPools {
				dir.Close()
				return errors.New("receipt pool limit reached")
			}
			if readErr == io.EOF {
				break
			}
		}
		if err := dir.Close(); err != nil {
			return err
		}
		old = Snapshot{Pool: update.Pool, Name: update.Name}
	}
	old = canonical(old)
	merged := old
	if update.Link != nil {
		if old.Link != nil {
			cmp := update.Link.ObservedAt.Compare(old.Link.ObservedAt)
			if cmp < 0 || (cmp == 0 && !reflect.DeepEqual(update.Link, old.Link)) {
				return errors.New("older or conflicting link observation")
			}
		}
		merged.Link = update.Link
	}
	if update.Credits != nil {
		if old.Credits != nil {
			cmp := update.Credits.ObservedAt.Compare(old.Credits.ObservedAt)
			if cmp < 0 || (cmp == 0 && !reflect.DeepEqual(update.Credits, old.Credits)) {
				return errors.New("older or conflicting credit observation")
			}
		}
		merged.Credits = update.Credits
	}
	if update.Benefit != nil {
		if old.Benefit != nil {
			cmp := update.Benefit.ObservedAt.Compare(old.Benefit.ObservedAt)
			if cmp < 0 || (cmp == 0 && !reflect.DeepEqual(update.Benefit, old.Benefit)) {
				return errors.New("older or conflicting benefit observation")
			}
		}
		merged.Benefit = update.Benefit
	}
	// The opaque pool identifies the organization. Its discovery label may
	// arrive with an earlier offer timestamp than a separate link check.
	merged.Name = update.Name
	if err := merged.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if len(data) > maxFile {
		return errors.New("receipt size limit reached")
	}
	f, err := os.CreateTemp(s.Dir, ".receipt-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Refuse an intervening symlink/nonregular destination before atomic replacement.
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return errReceipt
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	return os.Rename(temp, path)
}

func canonical(s Snapshot) Snapshot {
	data, _ := json.Marshal(s)
	var result Snapshot
	_ = json.Unmarshal(data, &result)
	if c := result.Credits; c != nil {
		c.ObservedAt = c.ObservedAt.UTC()
		for i := range c.Grants {
			c.Grants[i].GrantedAt = c.Grants[i].GrantedAt.UTC()
			c.Grants[i].ExpiresAt = c.Grants[i].ExpiresAt.UTC()
		}
	}
	if b := result.Benefit; b != nil {
		b.ObservedAt = b.ObservedAt.UTC()
		b.ExpiresAt = b.ExpiresAt.UTC()
	}
	if result.Link != nil {
		result.Link.ObservedAt = result.Link.ObservedAt.UTC()
	}
	return result
}
