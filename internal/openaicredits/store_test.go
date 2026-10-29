package openaicredits

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func clock() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
func receipt(n int) Snapshot {
	return Snapshot{Pool: fmt.Sprintf("%064x", n), Name: "Synthetic pool",
		Credits: &Credits{Currency: "USD", Balance: 2788, ObservedAt: clock(), Grants: []Grant{{Name: "API credit", Granted: 3000, Remaining: 2788, GrantedAt: clock().Add(-time.Hour)}}}}
}
func benefitReceipt(n int) Snapshot {
	yes := true
	claim := int64(3500)
	return Snapshot{Pool: fmt.Sprintf("%064x", n), Name: "Synthetic pool", Benefit: &Benefit{Eligible: &yes, Currency: "USD", Cadence: "monthly", PeriodCredit: 7000, ClaimableCredit: &claim, ExpiresAt: clock().Add(time.Hour), ObservedAt: clock(), LinkState: "unlinked"}}
}
func mustSave(t *testing.T, s *Store, r Snapshot) {
	t.Helper()
	if err := s.Save(r, clock()); err != nil {
		t.Fatal(err)
	}
}
func one(t *testing.T, s *Store) Snapshot {
	t.Helper()
	a, e := s.ReadAll()
	if e != nil || len(a) != 1 {
		t.Fatalf("read: %v %v", a, e)
	}
	return a[0]
}
func TestMergeIndependentTimesAndAtomicStaleRejection(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	c := receipt(1)
	c.Credits.ObservedAt = clock().Add(-2 * time.Minute)
	mustSave(t, s, c)
	b := benefitReceipt(1)
	mustSave(t, s, b)
	got := one(t, s)
	if !got.Credits.ObservedAt.Equal(c.Credits.ObservedAt) || !got.Benefit.ObservedAt.Equal(b.Benefit.ObservedAt) {
		t.Fatal("section timestamp refreshed")
	}
	path := filepath.Join(s.Dir, c.Pool+".json")
	before, _ := os.ReadFile(path)
	u := benefitReceipt(1)
	u.Benefit.ObservedAt = clock().Add(time.Second)
	u.Credits = receipt(1).Credits
	u.Credits.ObservedAt = clock().Add(-3 * time.Minute)
	if s.Save(u, clock()) == nil {
		t.Fatal("stale mixed update accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed update changed receipt")
	}
}

func TestDiscoveryNameDoesNotDependOnFinancialClock(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	link := Snapshot{Pool: receipt(1).Pool, Name: "OpenAI organization", Link: &Link{State: "unlinked", ObservedAt: clock()}}
	mustSave(t, s, link)
	offer := benefitReceipt(1)
	offer.Name = "Named synthetic organization"
	offer.Benefit.ObservedAt = clock().Add(-time.Minute)
	mustSave(t, s, offer)
	if one(t, s).Name != offer.Name {
		t.Fatal("discovery label was discarded because its offer predates the link check")
	}
}
func TestEqualIdempotenceConflictAndClockGate(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	r := receipt(1)
	mustSave(t, s, r)
	mustSave(t, s, r)
	r.Credits.Balance++
	if s.Save(r, clock()) == nil {
		t.Fatal("equal conflict accepted")
	}
	for _, dt := range []time.Duration{-5*time.Minute - time.Nanosecond, time.Minute + time.Nanosecond} {
		r := receipt(2)
		r.Credits.ObservedAt = clock().Add(dt)
		if s.Save(r, clock()) == nil {
			t.Fatal("receive clock gate bypass")
		}
	}
	for _, dt := range []time.Duration{-5 * time.Minute, time.Minute} {
		r := receipt(2)
		r.Credits.ObservedAt = clock().Add(dt)
		mustSave(t, s, r)
	}
}
func TestValidation(t *testing.T) {
	cases := []func(*Snapshot){
		func(r *Snapshot) { r.Pool = "../bad" }, func(r *Snapshot) { r.Pool = strings.Repeat("A", 64) }, func(r *Snapshot) { r.Name = " bad" },
		func(r *Snapshot) { r.Name = "bad\nname" }, func(r *Snapshot) { r.Name = strings.Repeat("a", 121) }, func(r *Snapshot) { r.Credits = nil },
		func(r *Snapshot) { r.Credits.Currency = "EUR" }, func(r *Snapshot) { r.Credits.Balance = maxAmount + 1 },
		func(r *Snapshot) { r.Credits.ObservedAt = time.Time{} }, func(r *Snapshot) { r.Credits.Grants[0].Remaining = 3001 },
		func(r *Snapshot) { r.Credits.Grants[0].Granted = -1 }, func(r *Snapshot) { r.Credits.Grants[0].GrantedAt = time.Time{} },
		func(r *Snapshot) { r.Credits.Grants[0].ExpiresAt = r.Credits.Grants[0].GrantedAt },
		func(r *Snapshot) { r.Credits.Grants = make([]Grant, 201) },
	}
	for i, change := range cases {
		r := receipt(1)
		change(&r)
		if r.Validate() == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	r := receipt(1)
	r.Credits.Balance = 0
	r.Credits.Grants = []Grant{}
	if r.Validate() != nil {
		t.Fatal("genuine zero rejected")
	}
	r.Credits.Balance = -maxAmount
	if r.Validate() != nil {
		t.Fatal("debt rejected")
	}
}
func TestOfferIsNotBalanceAndExpiredOfferIsValid(t *testing.T) {
	r := benefitReceipt(1)
	r.Benefit.ExpiresAt = clock().Add(-time.Hour)
	if r.Validate() != nil {
		t.Fatal("expired structurally valid offer rejected")
	}
	r.Benefit.Cadence = "annual"
	if r.Validate() != nil {
		t.Fatal("annual rejected")
	}
	r.Benefit.Eligible = nil
	if r.Validate() == nil {
		t.Fatal("missing eligible accepted")
	}
	no := false
	r.Benefit = &Benefit{Eligible: &no, ObservedAt: clock(), LinkState: "unknown"}
	if r.Validate() != nil {
		t.Fatal("false eligible rejected")
	}
	r.Benefit.Currency = "USD"
	if r.Validate() == nil {
		t.Fatal("false eligible with misleading money fields accepted")
	}
}
func TestHistoricalReadsMalformedAndPoolMismatch(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	r := receipt(1)
	mustSave(t, s, r)
	r.Credits.ObservedAt = clock().Add(-24 * time.Hour)
	data, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(s.Dir, r.Pool+".json"), data, 0600)
	if !one(t, s).Credits.ObservedAt.Equal(r.Credits.ObservedAt) {
		t.Fatal("historical receipt rejected")
	}
	os.WriteFile(filepath.Join(s.Dir, fmt.Sprintf("%064x.json", 2)), data, 0600)
	os.WriteFile(filepath.Join(s.Dir, fmt.Sprintf("%064x.json", 3)), []byte("{broken"), 0600)
	os.WriteFile(filepath.Join(s.Dir, fmt.Sprintf("%064x.json", 4)), []byte(strings.Repeat("x", maxFile+1)), 0600)
	if lenMust(t, s) != 1 {
		t.Fatal("bad files included")
	}
	data = []byte(strings.Replace(string(data), "\"balance\":2788,", "", 1))
	os.WriteFile(filepath.Join(s.Dir, r.Pool+".json"), data, 0600)
	if lenMust(t, s) != 0 {
		t.Fatal("missing balance became zero")
	}
}
func lenMust(t *testing.T, s *Store) int {
	t.Helper()
	a, e := s.ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	return len(a)
}
func TestNoSymlinksPrivatePermissionsAndAtomicCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink/permission assertions")
	}
	root := t.TempDir()
	s := &Store{Dir: filepath.Join(root, "receipts")}
	r := receipt(1)
	mustSave(t, s, r)
	d, _ := os.Stat(s.Dir)
	f, _ := os.Stat(filepath.Join(s.Dir, r.Pool+".json"))
	if d.Mode().Perm() != 0700 || f.Mode().Perm() != 0600 {
		t.Fatal("not private")
	}
	target := filepath.Join(root, "target")
	data, _ := json.Marshal(receipt(2))
	os.WriteFile(target, data, 0600)
	link := filepath.Join(s.Dir, fmt.Sprintf("%064x.json", 2))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if lenMust(t, s) != 1 {
		t.Fatal("symlink read")
	}
	if s.Save(receipt(2), clock()) == nil {
		t.Fatal("symlink save")
	}
	after, _ := os.ReadFile(target)
	if string(after) != string(data) {
		t.Fatal("target modified")
	}
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".receipt-") {
			t.Fatal("temporary file remains")
		}
	}
	bad := &Store{Dir: filepath.Join(root, "linkdir")}
	os.Symlink(s.Dir, bad.Dir)
	if bad.Save(receipt(3), clock()) == nil {
		t.Fatal("directory symlink accepted")
	}
}
func TestLimitsSortAndMissingDirectory(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "missing")}
	if lenMust(t, s) != 0 {
		t.Fatal("missing not empty")
	}
	for i := 1; i <= 20; i++ {
		r := receipt(i)
		r.Name = fmt.Sprintf("pool%02d", 21-i)
		mustSave(t, s, r)
	}
	if s.Save(receipt(21), clock()) == nil {
		t.Fatal("pool cap bypass")
	}
	a, e := s.ReadAll()
	if e != nil || len(a) != 20 || a[0].Name != "pool01" || a[19].Name != "pool20" {
		t.Fatal("sort/limit")
	}
	if (&Store{}).Save(receipt(1), clock()) == nil {
		t.Fatal("empty directory")
	}
	r := receipt(1)
	r.Credits.Balance++
	r.Credits.ObservedAt = clock().Add(time.Second)
	mustSave(t, s, r)
}
func TestConcurrentIndependentMergesAndReads(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, r := range []Snapshot{receipt(1), benefitReceipt(1)} {
		wg.Add(1)
		go func(r Snapshot) { defer wg.Done(); errs <- s.Save(r, clock()) }(r)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	got := one(t, s)
	if got.Credits == nil || got.Benefit == nil {
		t.Fatal("concurrent section lost")
	}
	// Repeated reads observe a complete JSON receipt, never a temporary partial write.
	for i := 0; i < 10; i++ {
		if _, e := s.ReadAll(); e != nil {
			t.Fatal(e)
		}
	}
}
