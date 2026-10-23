package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

// outputView is the snapshot one machine output reads. Every invocation builds
// exactly one, so all the states it prints share one clock and one ranking.
type outputView struct {
	snap view.Snapshot
	// entries are the formatters in m.Providers order, with the source key
	// each one is known by in the snapshot.
	entries []viewEntry
}

type viewEntry struct {
	key string
	pf  *ProviderFormatter
}

// buildView builds the snapshot of m as of now from the formatters' data,
// display names, source labels, families and family source counts.
func (m *MultiProviderOutput) buildView(now time.Time) outputView {
	keys := m.sourceKeys()
	in := view.Inputs{
		Results:            make(map[string]*provider.UsageData, len(keys)),
		DisplayNames:       make(map[string]string, len(keys)),
		SourceLabels:       make(map[string]string, len(keys)),
		Families:           make(map[string]string, len(keys)),
		FamilySourceCounts: make(map[string]int),
	}
	v := outputView{entries: make([]viewEntry, 0, len(keys))}
	for i := range m.Providers {
		pf := &m.Providers[i]
		key := keys[i]
		v.entries = append(v.entries, viewEntry{key: key, pf: pf})

		in.Results[key] = pf.Data
		in.DisplayNames[key] = pf.DisplayName
		if in.DisplayNames[key] == "" {
			in.DisplayNames[key] = pf.Display
		}
		in.SourceLabels[key] = pf.SourceLabel
		family := familyOf(pf)
		in.Families[key] = family
		if pf.SourceCount > in.FamilySourceCounts[family] {
			in.FamilySourceCounts[family] = pf.SourceCount
		}
	}
	v.snap = view.Build(in, now)
	return v
}

// sourceKeys names each formatter by its source key. A formatter with no name,
// which hand-built test outputs can have, gets a key of its own.
func (m *MultiProviderOutput) sourceKeys() []string {
	keys := make([]string, len(m.Providers))
	used := make(map[string]bool, len(m.Providers))
	for i, pf := range m.Providers {
		key := pf.Name
		if key == "" || used[key] {
			key = fmt.Sprintf("unnamed-%d", i)
		}
		used[key] = true
		keys[i] = key
	}
	return keys
}

// familyOf is the provider family a formatter belongs to.
func familyOf(pf *ProviderFormatter) string {
	switch {
	case pf.Family != "":
		return pf.Family
	case pf.Data != nil && pf.Data.Provider != "":
		return pf.Data.Provider
	}
	return pf.Name
}

// account returns the snapshot account for a source key, or nil.
func (v outputView) account(key string) *view.Account {
	for i := range v.snap.Accounts {
		if v.snap.Accounts[i].Key == key {
			return &v.snap.Accounts[i]
		}
	}
	return nil
}

// display is the name a formatter shows people.
func (v outputView) display(key string) string {
	for _, e := range v.entries {
		if e.key == key {
			return e.pf.Display
		}
	}
	return key
}

// worst returns the account and window the snapshot names as most at risk.
func (v outputView) worst() (*view.Account, view.Window, bool) {
	target := v.snap.Worst
	if target == nil {
		return nil, view.Window{}, false
	}
	acct := v.account(target.AccountKey)
	if acct == nil {
		return nil, view.Window{}, false
	}
	for _, w := range acct.Windows {
		if w.Name == target.Window {
			return acct, w, true
		}
	}
	return nil, view.Window{}, false
}

// agentStatus is the status word of a window. stale covers a reading that is
// not fresh and a reset that has passed. at_risk is used up or projected past
// 100%, tight is projected past 90%, and unknown means the forecast cannot say.
func agentStatus(acct *view.Account, w view.Window) string {
	switch {
	case acct.Reading.State != view.ReadingFresh, w.Forecast.State == view.ForecastStale:
		return "stale"
	case w.Exhaustion.State == view.ExhaustionOut:
		return "at_risk"
	case w.Forecast.State != view.ForecastKnown:
		return "unknown"
	case w.Forecast.Pct >= 100:
		return "at_risk"
	case w.Forecast.Pct >= 90:
		return "tight"
	}
	return "on_track"
}

// projectionPct is the projected use at reset, or unknown.
func projectionPct(w view.Window) string {
	if w.Forecast.State != view.ForecastKnown {
		return "unknown"
	}
	return formatPrecisePct(w.Forecast.Pct)
}

// untilReset is the time from at to the window's reset. It is false when the
// reset is unknown, so no caller can mistake an unknown reset for zero.
func untilReset(w view.Window, at time.Time) (time.Duration, bool) {
	if !w.Reset.Known {
		return 0, false
	}
	return clampDuration(w.Reset.At.Sub(at)), true
}

// blockedFor is how long before the reset the window is, or will be, used up.
// The view gives it for runs_out. For out, it is the time left until the reset.
func blockedFor(w view.Window, at time.Time) (time.Duration, bool) {
	switch w.Exhaustion.State {
	case view.ExhaustionRunsOut:
		if w.Exhaustion.EarlyBy > 0 {
			return clampDuration(w.Exhaustion.EarlyBy), true
		}
	case view.ExhaustionOut:
		if d, ok := untilReset(w, at); ok && d > 0 {
			return d, true
		}
	}
	return 0, false
}

// runsOutFields are a window's run-out fields: runs_out=now when it is used
// up, otherwise the time until it runs out, when the current rate exhausts it
// before the reset.
func runsOutFields(w view.Window, at time.Time) []string {
	var fields []string
	switch w.Exhaustion.State {
	case view.ExhaustionOut:
		fields = append(fields, "runs_out=now")
	case view.ExhaustionRunsOut:
		in := clampDuration(w.Exhaustion.RunsOutIn)
		fields = append(fields,
			fmt.Sprintf("runs_out_in_seconds=%d", int64(in.Seconds())),
			"runs_out_in="+formatExactDuration(in),
		)
	}
	if early, ok := blockedFor(w, at); ok {
		fields = append(fields,
			fmt.Sprintf("runs_out_early_by_seconds=%d", int64(early.Seconds())),
			"runs_out_early_by="+formatExactDuration(early),
		)
	}
	return fields
}

// agentWindowFields are the fields of one window for agents. seconds adds
// reset_in_seconds, which the headline keeps for exact boundary math.
func agentWindowFields(acct *view.Account, w view.Window, at time.Time, seconds bool) []string {
	fields := []string{
		"current=" + formatPrecisePct(w.Used),
		"projected_at_reset=" + projectionPct(w),
	}
	if d, ok := untilReset(w, at); ok {
		if seconds {
			fields = append(fields, fmt.Sprintf("reset_in_seconds=%d", int64(d.Seconds())))
		}
		fields = append(fields, "reset_in="+formatExactDuration(d))
	} else {
		fields = append(fields, "reset_in=unknown")
	}
	fields = append(fields, "status="+agentStatus(acct, w))
	fields = append(fields, runsOutFields(w, at)...)
	if acct.Reading.State != view.ReadingFresh {
		fields = append(fields, "data="+string(acct.Reading.State))
	}
	return fields
}

// agentQuotaSummaries lists every window of every source. Providers are ordered
// by the existing urgency tier, then by the snapshot's risk rank.
func (v outputView) agentQuotaSummaries() []string {
	type quota struct {
		pf     *ProviderFormatter
		acct   *view.Account
		window view.Window
		tier   int
	}
	quotas := make([]quota, 0)
	for _, e := range v.entries {
		acct := v.account(e.key)
		if acct == nil {
			continue
		}
		tier := classifyProvider(e.pf).tier
		for _, w := range acct.Windows {
			quotas = append(quotas, quota{pf: e.pf, acct: acct, window: w, tier: tier})
		}
	}

	sort.SliceStable(quotas, func(i, j int) bool {
		a, b := quotas[i], quotas[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.window.Risk != b.window.Risk {
			return a.window.Risk < b.window.Risk
		}
		if a.pf.Display != b.pf.Display {
			return a.pf.Display < b.pf.Display
		}
		return a.window.Name < b.window.Name
	})

	out := make([]string, 0, len(quotas))
	for _, q := range quotas {
		fields := agentWindowFields(q.acct, q.window, v.snap.At, false)
		// The announcement is printed once per account, on its first qualifying window.
		if q.pf.Data != nil {
			if announced := q.pf.Data.AnnouncedReset; announced != nil && len(announced.Windows) > 0 && q.window.Name == announced.Windows[0] {
				fields = append(fields, announced.AgentField())
			}
		}
		out = append(out, fmt.Sprintf("%s %s(%s)", q.pf.Display, q.window.Name, strings.Join(fields, ",")))
	}
	return out
}

// JSONView is the optional top-level view. Keys are source keys, such as
// "claude" or "claude:odl".
type JSONView struct {
	At string `json:"at"`
	// Accounts lists the source keys in snapshot order.
	Accounts []string        `json:"accounts,omitempty"`
	Worst    *JSONViewTarget `json:"worst,omitempty"`
}

// JSONViewTarget names one window of one source.
type JSONViewTarget struct {
	Account string `json:"account"`
	Window  string `json:"window"`
}

// JSONAccountView is one source's snapshot. It appears on a provider object
// and on each entry of its sources array.
type JSONAccountView struct {
	Reading JSONViewReading  `json:"reading"`
	Windows []JSONViewWindow `json:"windows"`
}

// JSONViewReading says how far the source's reading can be trusted.
type JSONViewReading struct {
	State      string `json:"state"`
	ObservedAt string `json:"observed_at,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// JSONViewWindow is one window with its states. Shown says whether people see
// the window in the list surfaces. Machine output keeps every window.
type JSONViewWindow struct {
	Name       string             `json:"name"`
	Code       string             `json:"code,omitempty"`
	Label      string             `json:"label,omitempty"`
	Used       float64            `json:"used"`
	Forecast   JSONViewForecast   `json:"forecast"`
	Exhaustion JSONViewExhaustion `json:"exhaustion"`
	Reset      JSONViewReset      `json:"reset"`
	Shown      bool               `json:"shown"`
}

// JSONViewForecast carries pct only when the state is known.
type JSONViewForecast struct {
	State string   `json:"state"`
	Pct   *float64 `json:"pct,omitempty"`
}

// JSONViewExhaustion carries the run-out figures only when they are known.
type JSONViewExhaustion struct {
	State            string `json:"state"`
	RunsOutInSeconds *int64 `json:"runs_out_in_seconds,omitempty"`
	EarlyBySeconds   *int64 `json:"early_by_seconds,omitempty"`
	OutSince         string `json:"out_since,omitempty"`
}

// JSONViewReset carries at only when the reset time is known. Policy is the
// provider's reset description, when it gives one.
type JSONViewReset struct {
	At     string `json:"at,omitempty"`
	Policy string `json:"policy,omitempty"`
	Known  bool   `json:"known"`
}

// jsonView is the top-level view.
func (v outputView) jsonView() *JSONView {
	out := &JSONView{At: v.snap.At.Format(time.RFC3339)}
	for _, acct := range v.snap.Accounts {
		out.Accounts = append(out.Accounts, acct.Key)
	}
	if acct, w, ok := v.worst(); ok {
		out.Worst = &JSONViewTarget{Account: acct.Key, Window: w.Name}
	}
	return out
}

// jsonAccount is the view of one source, or nil when the snapshot has none.
func (v outputView) jsonAccount(key string) *JSONAccountView {
	acct := v.account(key)
	if acct == nil {
		return nil
	}
	shown := make(map[string]bool, len(acct.Shown))
	for _, name := range acct.Shown {
		shown[name] = true
	}
	out := &JSONAccountView{
		Reading: JSONViewReading{State: string(acct.Reading.State), Reason: acct.Reading.Reason},
		Windows: make([]JSONViewWindow, 0, len(acct.Windows)),
	}
	if !acct.Reading.ObservedAt.IsZero() {
		out.Reading.ObservedAt = acct.Reading.ObservedAt.Format(time.RFC3339)
	}
	for _, w := range acct.Windows {
		out.Windows = append(out.Windows, jsonWindowView(w, shown[w.Name], v.snap.At))
	}
	return out
}

func jsonWindowView(w view.Window, shown bool, at time.Time) JSONViewWindow {
	out := JSONViewWindow{
		Name:       w.Name,
		Code:       w.Code,
		Label:      w.Label,
		Used:       w.Used,
		Forecast:   JSONViewForecast{State: string(w.Forecast.State)},
		Exhaustion: JSONViewExhaustion{State: string(w.Exhaustion.State)},
		Reset:      JSONViewReset{Policy: w.Reset.Policy, Known: w.Reset.Known},
		Shown:      shown,
	}
	if w.Forecast.State == view.ForecastKnown {
		pct := roundPct(w.Forecast.Pct)
		out.Forecast.Pct = &pct
	}
	if w.Reset.Known {
		out.Reset.At = w.Reset.At.Format(time.RFC3339)
	}
	if w.Exhaustion.State == view.ExhaustionRunsOut {
		secs := int64(clampDuration(w.Exhaustion.RunsOutIn).Seconds())
		out.Exhaustion.RunsOutInSeconds = &secs
	}
	if early, ok := blockedFor(w, at); ok {
		secs := int64(early.Seconds())
		out.Exhaustion.EarlyBySeconds = &secs
	}
	if !w.Exhaustion.OutSince.IsZero() {
		out.Exhaustion.OutSince = w.Exhaustion.OutSince.Format(time.RFC3339)
	}
	return out
}
