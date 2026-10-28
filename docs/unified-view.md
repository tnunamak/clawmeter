# One view, many surfaces

Status: design, 2026-10-22. Owner rule: every surface (tray menu, tooltip,
icon, `clawmeter status` plain and color, `--json`, `--agent`, statusline, the
T3 hub) is a view over one system. Whichever one you read, you see the same
facts with the same names, states, order and freshness, limited only by what
the medium can carry.

The audit in [surface-audit-2026-10-22.md](surface-audit-2026-10-22.md) found
the surfaces share quota math but each decides visibility, naming, order,
freshness and exhaustion on its own. This document defines the shared model
that replaces those decisions.

## The model

`internal/view` builds one immutable `Snapshot` from the provider results, the
current time and the local observations (run-out history, feed reading,
extension contact). Surfaces only format it; none of them computes a state.

```text
Snapshot
  At            time the snapshot describes
  Accounts      []Account, in display order
  Worst         *Target   the most at-risk quota, by one rule
  Selected      *Target   what the icon, tooltip and statusline show: the
                          user's pinned quota when set and present, else Worst
Account
  Key           source key ("claude", "claude:odl")
  Provider      provider name; Label: display label (see Names)
  Reading       fresh | stale | error | expired | unavailable, with
                ObservedAt, Reason (closed, non-secret text) and Action
  Windows       []Window, every window in provider order
  Shown         the windows list surfaces show (see Visibility)
  Balances      balance rows and money-only windows
  Setup         what the account needs from the user, with its Action
  ResetCredits  effective inventory (count usable now, next expiry, freshness, action)
  Pools         API credit pools with effective state (see Money)
  Announcement  the visible announced reset, or nil
Window
  Name, Code, Qualifier, Label
  Used          percent
  Forecast      State known | warming | stale | none; Pct when known
  Exhaustion    State out | runs_out | lasts | unknown;
                RunsOutIn, EarlyBy when runs_out; OutSince when out and observed
  Risk          the rank Worst is chosen by
  Reset         At, or Policy text, or unknown
Target
  Account key and window name, with the reason it was chosen
```

## States, decided once

- Reading. Missing or future observation times and usage older than two poll
  intervals are stale. A retained reading flagged stale after a failed poll
  can still forecast within that age allowance. Every usable reading uses its
  observation time for the rate; rendering only counts down the estimated ETA.
  Errors and expired credentials establish no forecast or exhaustion. All
  readings keep their last windows for display. Fresh readings rank first.
- Exhaustion. `unknown` when the reading is unusable. Otherwise `out` when
  the window is at 100% or more, whatever the forecast says (a window can be
  used up before its forecast warms up); `runs_out` or `lasts` only when the
  forecast is known; `unknown` in every other case (warming up, no reset, a
  reset already passed, or estimated exhaustion already passed). Nothing reads
  as "lasts" without a forecast. Estimates assume the average pace since reset
  continues; they do not measure recent demand.
- Forecast. `warming` before the window has run long enough to project,
  `none` for a window without a reset, `known` with a percentage otherwise.
  The evaluated machine view omits unknown percentages. Legacy JSON retains a
  zero placeholder, valid only when its indicator is a percentage.
- Reset. Unknown resets are `unknown`, never zero seconds.
- Out since. The time comes from the run-out record, which any process may
  read: a reading under 100% followed by one at 100% or more, with a positive
  gap of at most two poll intervals, for the account's current identity and
  reset period, and a last observation no older than two poll intervals. A
  corrupt record or a time in the future fails closed. It is the first full
  reading the tray saw, not a time the provider reported.
- Money. A projected figure is labelled as an estimate everywhere
  (`est. $40.00 unused at expiry`); only observed amounts are stated plainly.
  Each pool carries its effective freshness and its Action, computed at
  snapshot time.

## Names and order

- An account's label is the provider's display name, plus ` · <source label>`
  when the provider has more than one configured source.
- Lists are in display-label order everywhere. Urgency is a separate fact:
  `Worst` (and a per-window risk flag), never a different list order.
- Within an account: windows, then reset credits, then the announcement, then
  money.
- Window labels come from one function; machine output keeps the raw `name`
  as the key and adds the label.

## Visibility

The snapshot keeps every window. List surfaces for people (tray menu, CLI
plain and color) show `Shown`: idle windows (0% used, nothing projected) are
left out unless every window is idle; they carry no information. A surface
that shows fewer than `Shown`, such as the tray's fixed number of rows, ends
with "+N more".
Machine output carries every window, each flagged with whether people see it,
because an orchestrator routing work needs the idle ones most.

Worst is chosen among fresh windows first, then stale ones. Within each group,
observed exhaustion comes first, even without a reported reset. Known resets
then rank before unknown ones, followed by estimated exhaustion (soonest first),
forecast, current use and display order, so ties always break the same way.

## What each surface shows

- Tray menu and CLI plain/color: every account, window, credit, pool and
  announcement, with actions (clickable in the tray, text in the CLI).
- Tooltip, icon and statusline: the Worst target only, with used percent,
  forecast or its state, exhaustion and reset, and the reading's freshness.
- `--json`: every existing v1 field keeps its type and meaning (including
  `projected_pct`, which stays numeric). Each provider object, and each entry
  of its `sources` array, adds an optional `view` object with the account's
  states; the top level adds an optional `view` with `at`, the account order
  and the Worst and Selected references. Schema and fixtures grow with it.
- `--agent`: one line per account in display order, the same states as
  fields, plus `worst=`; errors, setup needs, actions, next credit and update
  availability are included.
- T3 hub: a protocol subset. It serves only fresh readings and only the
  windows T3's client understands; this is documented, not hidden.

## Building it

`view.Build(inputs, at)` is a pure function of its inputs and one clock: no
network, setup probing or file I/O inside it or in a formatter. Inputs that
need I/O (run-out record, feed reading, extension contact, update state) are
read before it, outside the tray's render lock.

## Stages

1. Correctness: build `internal/view` with readings, windows, exhaustion,
   forecast, reset, Worst and Selected (with the pin); switch agent,
   statusline, tooltip, icon selection and JSON to it, and apply the same
   exhaustion and freshness states to the tray menu and CLI plain and color
   rows now, keeping every fact those surfaces show today (out since,
   credits, announcement, update). Fix the money estimate wording. Tests:
   a frozen-clock matrix of states, v1 success and partial-failure fixtures,
   multi-source and all-idle accounts, the pin, and freshness and reset
   boundaries. Existing string tests stay, changed only where wording
   changes on purpose.
2. Completeness: out since from the shared record, pools and credits with
   effective state and actions, errors and setup in machine output, update
   state from the tray's cache.
3. Names, order and visibility everywhere, then the tray menu and CLI
   plain/color on the snapshot, with a test that renders every surface from
   one snapshot and checks each fact appears consistently.
