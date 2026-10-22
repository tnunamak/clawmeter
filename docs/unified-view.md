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
  Worst         *Target   the one quota a single-quota surface shows
Account
  Key           source key ("claude", "claude:odl")
  Provider      provider name; Label: display label (see Names)
  Reading       fresh | stale | error | expired | unavailable, with
                ObservedAt, Reason (closed, non-secret text) and Action
  Windows       []Window, in provider order, after the visibility policy
  Hidden        count of windows the policy left out
  ResetCredits  effective inventory (count usable now, next expiry, freshness, action)
  Pools         API credit pools with effective state (see Money)
  Announcement  the visible announced reset, or nil
Window
  Name, Code, Qualifier, Label
  Used          percent
  Forecast      State known | warming | stale | none; Pct when known
  Exhaustion    State out | runs_out | lasts | unknown;
                RunsOutIn, EarlyBy when runs_out; OutSince when out and observed
  Reset         At, or Policy text, or unknown
Target
  Account key and window name, with the reason it was chosen
```

## States, decided once

- Reading. A stale, errored or expired reading keeps its last windows for
  display, labelled with their age, but establishes nothing current: its
  windows have Forecast `stale` and Exhaustion `unknown`, and it is never the
  Worst target while any fresh reset-backed window exists.
- Exhaustion. `out` when a fresh window is at 100% or more, whatever the
  forecast says (a window can be used up before its forecast warms up).
  `runs_out` when the forecast is known and ends before the reset. `lasts`
  otherwise. `unknown` when the reading is not fresh.
- Forecast. `warming` before the window has run long enough to project,
  `none` for a window without a reset, `known` with a percentage otherwise.
  Machine output never encodes an unknown forecast as a number.
- Reset. Unknown resets are `unknown`, never zero seconds.
- Out since. The time comes from the run-out record, which any process may
  read; it is shown only when the record was observed for the account's
  current identity, in the same reset period, with a gap of at most two poll
  intervals, and its last observation is no older than two poll intervals.
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

One policy for every list surface: idle windows (0% used with nothing
projected) are left out and counted in `Hidden`. A surface that cannot show
every window says how many it left out; nothing is truncated silently.

## What each surface shows

- Tray menu and CLI plain/color: every account, window, credit, pool and
  announcement, with actions (clickable in the tray, text in the CLI).
- Tooltip, icon and statusline: the Worst target only, with used percent,
  forecast or its state, exhaustion and reset, and the reading's freshness.
- `--json`: the existing v1 fields stay; each provider object adds a `view`
  object with the account's states as above (optional fields, so v1 holds).
- `--agent`: one line per account in display order, the same states as
  fields, plus `worst=`; errors, setup needs, actions, next credit and update
  availability are included.
- T3 hub: a protocol subset. It serves only fresh readings and only the
  windows T3's client understands; this is documented, not hidden.

## Stages

1. Correctness: build `internal/view` with readings, windows, exhaustion,
   forecast, reset and Worst; switch agent, statusline, tooltip and JSON to
   it; fix the money estimate wording.
2. Completeness: out since from the shared record, pools and credits with
   effective state and actions, errors and setup in machine output, update
   state from the tray's cache.
3. Names, order and visibility everywhere, then the tray menu and CLI
   plain/color on the snapshot, with a test that renders every surface from
   one snapshot and checks each fact appears consistently.
