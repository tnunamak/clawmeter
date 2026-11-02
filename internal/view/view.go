// Package view builds one snapshot of the quota facts every surface shows.
//
// Build is a pure function of its inputs and one clock: it reads no files,
// makes no network calls and touches no globals. Surfaces format a Snapshot;
// they do not decide states, order or risk themselves. See
// docs/unified-view.md for the rules.
package view

import (
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// snapshotStale is how old a browser reset snapshot may be before it is stale.
// It matches provider.UsageResetCredits.Summary.
const snapshotStale = 5 * time.Minute

// Snapshot is the state of every account at one time.
type Snapshot struct {
	At time.Time
	// Accounts are in display order: by Label, then Key.
	Accounts []Account
	// Worst is the most at-risk window, or nil when there is none.
	Worst *Target
	// Selected is the user's pinned window when it exists, else Worst.
	Selected *Target
}

// Account is one configured source and what it reports.
type Account struct {
	// Key is the source key, such as "claude" or "claude:odl".
	Key      string
	Provider string
	// Label is the provider's display name, plus " · <source label>" when
	// the provider family has more than one configured source.
	Label   string
	Reading Reading
	// Windows keeps every window in provider order.
	Windows []Window
	// Shown names the windows people see. See meter.IdleAt.
	Shown []string
	// ResetCredits is nil when the source reports no reset inventory.
	ResetCredits *Credits
	// Pools are copies of the source's credit pools. Each Action is
	// evaluated at the snapshot time with the extension contact input.
	Pools []provider.UsageCreditPool
	// CreditGrants are non-renewing balances, evaluated at this snapshot.
	CreditGrants []CreditGrant
	LocalModels  []provider.LocalModelUsage
	// Announcement is the announced reset the source carries, or nil.
	Announcement *provider.ResetAnnouncement
}

// ReadingState says how far a reading can be trusted now.
type ReadingState string

const (
	ReadingFresh       ReadingState = "fresh"
	ReadingStale       ReadingState = "stale"
	ReadingError       ReadingState = "error"
	ReadingExpired     ReadingState = "expired"
	ReadingUnavailable ReadingState = "unavailable"
)

// Reading is the state of one source's latest reading.
type Reading struct {
	State ReadingState
	// ObservedAt is when the provider data was fetched. Zero when unavailable.
	ObservedAt time.Time
	// Reason is closed, non-secret text: a humanized error or a stale cause.
	Reason string
}

// Window is one quota window with its derived states.
type Window struct {
	Name      string
	Code      string
	Qualifier string
	Label     string
	Scope     string
	// Used is the utilization in percent.
	Used float64
	// Money is the window's money detail, or "" when it has none.
	Money string
	// Pace is how far an even pace would have used by the snapshot time. It is
	// meaningful only when HasPace, which needs a known reset and a length.
	Pace       float64
	HasPace    bool
	Forecast   Forecast
	Exhaustion Exhaustion
	Reset      Reset
	// Risk is the window's place in the Worst order over every window in the
	// snapshot. 1 is the most at risk, and no two windows share a value.
	Risk int
}

// ForecastState says whether a projection of use at reset exists.
type ForecastState string

const (
	// ForecastKnown means Pct holds the projected use at reset.
	ForecastKnown ForecastState = "known"
	// ForecastWarming means too little of the window has elapsed to project.
	ForecastWarming ForecastState = "warming"
	// ForecastStale means the reading is too old or unusable, or the reset has passed.
	ForecastStale ForecastState = "stale"
	// ForecastNone means the window has no reset, or no known length.
	ForecastNone ForecastState = "none"
)

// Forecast is the projected use at reset. Pct is meaningful only when known.
type Forecast struct {
	State ForecastState
	// Reason qualifies a withheld forecast without changing the state contract.
	Reason string
	Pct    float64
}

// ExhaustionState says whether a window is used up, and how.
type ExhaustionState string

const (
	// ExhaustionOut means the window is at 100% or more now.
	ExhaustionOut ExhaustionState = "out"
	// ExhaustionRunsOut means the current rate uses it up before the reset.
	ExhaustionRunsOut ExhaustionState = "runs_out"
	// ExhaustionLasts means the current rate lasts to the reset.
	ExhaustionLasts ExhaustionState = "lasts"
	// ExhaustionUnknown means the reading cannot establish a current run-out.
	ExhaustionUnknown ExhaustionState = "unknown"
)

// Exhaustion is when a window is used up. RunsOutIn is set for runs_out.
// BlockedFor is set for runs_out and out: for runs_out, how long before the
// reset the window runs out; for out, how long until the reset. OutSince is
// set for out when the tray saw the window run out.
type Exhaustion struct {
	State      ExhaustionState
	RunsOutIn  time.Duration
	BlockedFor time.Duration
	OutSince   time.Time
}

// Reset is when a window resets. Known is true only when At is set, and In is
// then the time from the snapshot to At. A window can have a Policy text
// without a time.
type Reset struct {
	At     time.Time
	In     time.Duration
	Policy string
	Known  bool
}

// Credits is the effective reset inventory of one source.
type Credits struct {
	// Count is the number of resets usable at the snapshot time.
	Count int
	// NextExpiry is the earliest known expiry, or zero.
	NextExpiry time.Time
	// ObservedAt is when the inventory was fetched.
	ObservedAt time.Time
	// Snapshot is true for a manual browser snapshot.
	Snapshot bool
	// Stale is true for a snapshot older than five minutes.
	Stale bool
}

// Target names one window of one account.
type Target struct {
	AccountKey string
	Window     string
	Reason     string
}

// Inputs are everything Build reads. Maps may be nil.
type Inputs struct {
	// MaxReadingAge allows a retained reading to forecast up to this age.
	// It also bounds current credit-grant balances. Zero disables retained
	// forecasts and permits current credit only at its observation instant.
	MaxReadingAge time.Duration
	// Results maps a source key to its latest provider data. A nil value is
	// a source with no data.
	Results map[string]*provider.UsageData
	// DisplayNames maps a source key to its provider's display name.
	DisplayNames map[string]string
	// SourceLabels maps a source key to its source label.
	SourceLabels map[string]string
	// Families maps a source key to its provider family. A missing entry
	// uses the data's Provider name.
	Families map[string]string
	// FamilySourceCounts maps a provider family to its configured source count.
	FamilySourceCounts map[string]int
	// OutSince maps a source key and window name to when the tray saw the
	// window run out. The caller has validated each time.
	OutSince map[string]map[string]time.Time
	// ExtensionContact is when the browser extension last reached Clawmeter.
	ExtensionContact time.Time
	// Pin is the user's pinned window, or nil.
	Pin *Target
}

// Build returns the snapshot of in as of at.
func Build(in Inputs, at time.Time) Snapshot {
	keys := make([]string, 0, len(in.Results))
	for key := range in.Results {
		keys = append(keys, key)
	}
	accounts := make([]Account, 0, len(keys))
	for _, key := range keys {
		accounts = append(accounts, buildAccount(in, key, in.Results[key], at))
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		a, b := strings.ToLower(accounts[i].Label), strings.ToLower(accounts[j].Label)
		if a != b {
			return a < b
		}
		return accounts[i].Key < accounts[j].Key
	})

	ranked := rankWindows(accounts)
	snap := Snapshot{At: at, Accounts: accounts}
	if len(ranked) == 0 {
		return snap
	}
	for place, ref := range ranked {
		accounts[ref.account].Windows[ref.window].Risk = place + 1
	}
	worst := ranked[0]
	snap.Worst = &Target{
		AccountKey: accounts[worst.account].Key,
		Window:     accounts[worst.account].Windows[worst.window].Name,
		Reason:     "most at risk",
	}
	snap.Selected = snap.Worst
	if pin := in.Pin; pin != nil {
		for _, account := range accounts {
			if account.Key != pin.AccountKey {
				continue
			}
			for _, window := range account.Windows {
				if window.Name == pin.Window {
					snap.Selected = &Target{AccountKey: account.Key, Window: window.Name, Reason: "pinned"}
				}
			}
		}
	}
	return snap
}

func buildAccount(in Inputs, key string, data *provider.UsageData, at time.Time) Account {
	account := Account{Key: key, Label: label(in, key, data)}
	if data == nil {
		account.Reading = Reading{State: ReadingUnavailable}
		return account
	}
	account.Provider = data.Provider
	account.Reading = reading(data)
	account.LocalModels = provider.LocalModelsAt(data.LocalModels, data.FetchedAt, at, account.Reading.State == ReadingFresh && !data.FetchedAt.IsZero() && at.Sub(data.FetchedAt) <= in.MaxReadingAge)
	usable := account.Reading.State == ReadingFresh
	projectionAt := at
	if account.Reading.State == ReadingStale && !data.FetchedAt.IsZero() && in.MaxReadingAge > 0 {
		age := at.Sub(data.FetchedAt)
		if age >= 0 && age <= in.MaxReadingAge {
			usable = true
			projectionAt = data.FetchedAt
		}
	}

	for _, grant := range data.CreditGrants {
		account.CreditGrants = append(account.CreditGrants, buildCreditGrant(grant, account.Reading, at, in.MaxReadingAge))
	}
	account.Windows = make([]Window, 0, len(data.Windows))
	for _, w := range data.Windows {
		account.Windows = append(account.Windows, buildWindow(in, key, w, usable, projectionAt, at))
	}
	showAll := allIdle(data.Windows, at)
	for i, w := range data.Windows {
		if showAll || !meter.IdleAt(w, at) {
			account.Shown = append(account.Shown, account.Windows[i].Name)
		}
	}

	if r := data.ResetCredits; r != nil {
		credits := &Credits{
			Count:      r.DisplayCount(at),
			ObservedAt: r.FetchedAt,
			Snapshot:   r.Snapshot,
			Stale:      r.Snapshot && !r.FetchedAt.IsZero() && at.Sub(r.FetchedAt) > snapshotStale,
		}
		if expiry, ok := r.EarliestExpiry(at); ok {
			credits.NextExpiry = expiry
		}
		account.ResetCredits = credits
	}

	account.Pools = make([]provider.UsageCreditPool, 0, len(data.CreditPools))
	for _, pool := range data.CreditPools {
		if pool.ObservationSource == "extension" {
			pool.ExtensionContactAt = in.ExtensionContact
		}
		if pool.MonthSpend != nil {
			spend := *pool.MonthSpend
			pool.MonthSpend = &spend
		}
		account.Pools = append(account.Pools, pool)
	}

	if a := data.AnnouncedReset; a != nil {
		announcement := *a
		announcement.Windows = append([]string(nil), a.Windows...)
		account.Announcement = &announcement
	}
	return account
}

// label is the provider's display name, plus the source label when the family
// has more than one configured source.
func label(in Inputs, key string, data *provider.UsageData) string {
	name := in.DisplayNames[key]
	if name == "" {
		name = key
	}
	family := in.Families[key]
	if family == "" && data != nil {
		family = data.Provider
	}
	if in.FamilySourceCounts[family] <= 1 {
		return name
	}
	// Several sources of one provider need telling apart; a source without a
	// label is named by its ID.
	suffix := in.SourceLabels[key]
	if suffix == "" && data != nil {
		suffix = data.SourceID
	}
	if suffix == "" {
		return name
	}
	return name + " · " + suffix
}

// reading classifies data as the tray and the CLI always have: an expired
// credential is expired even when its error text is also set.
func reading(data *provider.UsageData) Reading {
	r := Reading{ObservedAt: data.FetchedAt}
	switch {
	case data.IsExpired:
		r.State = ReadingExpired
		if data.Error != "" {
			r.Reason = format.HumanizeError(data.Error)
		}
	case data.Error != "":
		r.State, r.Reason = ReadingError, format.HumanizeError(data.Error)
	case data.Stale:
		r.State, r.Reason = ReadingStale, "usage unavailable"
		if data.Warning != "" {
			r.Reason = format.HumanizeError(data.Warning)
		}
	default:
		r.State = ReadingFresh
	}
	return r
}

func allIdle(windows []provider.UsageWindow, at time.Time) bool {
	for _, w := range windows {
		if !meter.IdleAt(w, at) {
			return false
		}
	}
	return true
}

func buildWindow(in Inputs, key string, w provider.UsageWindow, usable bool, projectionAt, at time.Time) Window {
	out := Window{
		Name:      w.Name,
		Code:      meter.Code(w),
		Qualifier: meter.Qualifier(w),
		Label:     meter.Label(w),
		Scope:     w.Scope,
		Used:      w.Utilization,
		Reset:     Reset{Policy: w.ResetPolicy},
	}
	if !w.ResetsAt.IsZero() {
		out.Reset.At, out.Reset.Known = w.ResetsAt, true
		out.Reset.In = w.ResetsAt.Sub(at)
	}
	out.Money = w.MoneyDetail()

	length := forecast.WindowLength(w)
	if out.Reset.Known && length > 0 {
		out.HasPace = true
		out.Pace = meter.ExpectedPctAt(w.ResetsAt, length, at)
	}
	var proj forecast.Projection
	switch {
	case !usable:
		out.Forecast.State = ForecastStale
	case w.ResetsAt.IsZero():
		out.Forecast.State = ForecastNone
	case !w.ResetsAt.After(at):
		out.Forecast.State = ForecastStale
	case length <= 0:
		out.Forecast.State = ForecastNone
	default:
		proj = forecast.ProjectAt(w.Utilization, w.ResetsAt, length, projectionAt)
		proj.RunsOutIn -= at.Sub(projectionAt)
		switch {
		case proj.Unknown:
			out.Forecast.State = ForecastWarming
			if w.Utilization == 0 {
				out.Forecast.Reason = "no_usage"
			}
		default:
			out.Forecast = Forecast{State: ForecastKnown, Pct: proj.ProjectedPct}
		}
	}

	known := out.Forecast.State == ForecastKnown
	switch {
	case !usable, !w.ResetsAt.IsZero() && !w.ResetsAt.After(at):
		// A reading from before the window's reset says nothing about now.
		out.Exhaustion.State = ExhaustionUnknown
	case w.Utilization >= 100:
		out.Exhaustion.State = ExhaustionOut
		if since, ok := in.OutSince[key][w.Name]; ok {
			out.Exhaustion.OutSince = since
		}
		if out.Reset.Known && out.Reset.In > 0 {
			out.Exhaustion.BlockedFor = out.Reset.In
		}
	case known && proj.WillLastToReset:
		out.Exhaustion.State = ExhaustionLasts
	case known && proj.RunsOutIn <= 0:
		out.Exhaustion.State = ExhaustionUnknown
	case known:
		out.Exhaustion.State = ExhaustionRunsOut
		out.Exhaustion.RunsOutIn = proj.RunsOutIn
		if proj.RunsOutEarlyBy > 0 {
			out.Exhaustion.BlockedFor = proj.RunsOutEarlyBy
		}
	default:
		out.Exhaustion.State = ExhaustionUnknown
	}
	return out
}

// candidate is a window's place in the snapshot, for ranking.
type candidate struct {
	account, window int
	// class is 0 fresh with a reset, 1 fresh without, 2 not fresh with a
	// reset, 3 not fresh without.
	class int
}

// rankWindows orders every window of the accounts from most to least at risk.
//
// The order is, in turn:
//  1. fresh before not fresh, and in each a window with a known reset first;
//  2. out before runs_out before lasts or unknown;
//  3. among runs_out, the soonest RunsOutIn first;
//  4. a known forecast before an unknown one, then the higher Pct first;
//  5. the higher Used first;
//  6. account order, then window order.
//
// The last rule makes every tie break the same way on every surface.
func rankWindows(accounts []Account) []candidate {
	var ranked []candidate
	for a, account := range accounts {
		for w, win := range account.Windows {
			// Fresh before not fresh, and within each, a quota window with
			// a reset before a money or policy-only one.
			class := 0
			if account.Reading.State != ReadingFresh {
				class += 2
			}
			if !win.Reset.Known {
				class++
			}
			ranked = append(ranked, candidate{account: a, window: w, class: class})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return moreAtRisk(accounts, ranked[i], ranked[j])
	})
	return ranked
}

func moreAtRisk(accounts []Account, a, b candidate) bool {
	if a.class != b.class {
		return a.class < b.class
	}
	x := accounts[a.account].Windows[a.window]
	y := accounts[b.account].Windows[b.window]
	if rx, ry := exhaustionRank(x), exhaustionRank(y); rx != ry {
		return rx < ry
	}
	if x.Exhaustion.State == ExhaustionRunsOut && x.Exhaustion.RunsOutIn != y.Exhaustion.RunsOutIn {
		return x.Exhaustion.RunsOutIn < y.Exhaustion.RunsOutIn
	}
	xKnown, yKnown := x.Forecast.State == ForecastKnown, y.Forecast.State == ForecastKnown
	if xKnown != yKnown {
		return xKnown
	}
	if xKnown && x.Forecast.Pct != y.Forecast.Pct {
		return x.Forecast.Pct > y.Forecast.Pct
	}
	if x.Used != y.Used {
		return x.Used > y.Used
	}
	if a.account != b.account {
		return a.account < b.account
	}
	return a.window < b.window
}

func exhaustionRank(w Window) int {
	switch w.Exhaustion.State {
	case ExhaustionOut:
		return 0
	case ExhaustionRunsOut:
		return 1
	}
	return 2
}
