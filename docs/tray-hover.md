# Native tray hover

The hover answers which quota the icon represents, how much is used, and whether
it is expected to last until reset. Click opens the full account/window list,
numeric forecasts, exact reset expiry schedule and actions.

Keep the existing native desktop appearance. The title identifies the account
and window, including a pin when present. The body states usage beside a
ten-cell meter, one estimate, the reset horizon and the estimate's average-since-reset basis. Last-known usage
is labelled beside the value, after its freshness qualification. Missing or
stale forecasts never become reassuring estimates.

For example, a frozen quota snapshot renders:

```text
Codex · 7-Day
92% used  █████████░
Est. run-out in 8h27m
Resets in 2d22h
Average pace since reset
```

The meter reuses the menu and CLI glyphs on their fixed 0–100% scale. It shows
observed usage only; forecast shading and the pace marker stay in the clicked
menu, where their numeric context is available. The exact percentage remains
beside the rounded bar. A fresh zero reading draws an empty bar; missing or
future observation times, unusable readings and a passed reset draw no bar.
Stale bars remain explicitly last known.

The clicked menu retains the projected percentage and how long before reset the
quota is estimated to run out. The hover does not repeat that risk three ways.
Banked resets are inventory rather than active quota, so their full schedule
stays in the menu. Error fallbacks retain inventory information when no quota
can be displayed. Updates keep their existing tooltip behavior.

Linux transports the title and body separately through StatusNotifierItem.
The body must encode literal provider/account text safely for hosts that accept
markup. Emphasize its first line: current usage, or the freshness qualification. The desktop supplies typography, color, spacing, placement and lifetime;
an HTML illustration is not a guarantee of native pixels. macOS and Windows
receive the same meter in native text tooltips. There is no desktop-specific
image renderer. Windows' fixed text capacity still applies; the ordinary pinned
Codex example above fits its 127 UTF-16-unit limit. Long identities or stale
explanations can still be truncated by the existing Windows transport.

Verification must cover fresh, pinned, stale, unknown, exhausted and missing
readings; literal markup and Unicode; initial registration and later tooltip
updates; and preservation of all non-hover surfaces. Compare the actual native
hover and clicked menu, including keyboard and assistive-technology access,
before claiming a complete host journey. Offscreen component rendering alone
does not verify pointer activation, focus or screen-reader output.

Research and the source/experiment limitations are recorded in Divan:
`ai-quota-monitoring/native-tray-hover-and-click-have-different-host-capabilities-and-need-a-shared-information-contract.md`.
