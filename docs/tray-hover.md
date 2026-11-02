# Native tray hover

The hover answers which quota the icon represents, how much is used, and whether
it is expected to last until reset. Click opens the full account/window list,
numeric forecasts, exact reset expiry schedule and actions.

Keep the existing native desktop appearance. The title identifies the account
and window, including a pin when present. The body states usage beside a
ten-cell meter, one run-out estimate, the reset horizon and the forecast amount
with its average-since-reset basis. Last-known usage is labelled beside the
value, after its freshness qualification. Missing or stale forecasts never
become reassuring estimates.

For example, a frozen quota snapshot renders:

```text
Codex · 7-Day
92% used  ██████┃███▒
Est. run-out in 8h27m
Resets in 2d22h
Est. 158% at reset (avg. since reset)
```

The meter is the same selected window row used by the menu and CLI: `█` is
observed use, `▒` is additional use projected by reset, `░` is the remainder of the limit,
and `┃` marks even pace. Its fixed 0–100% scale never expands to fit overflow;
the exact used percentage and numeric projected percentage carry that detail.
The forecast states its average-since-reset basis. A fresh zero reading can draw
an empty bar with an even-pace marker; it differs from an unknown reading.
Missing/future observations, unusable readings and passed resets draw no bar.
Last-known readings retain their explicit freshness qualification, and the
snapshot decides whether any retained forecast is still usable.

The clicked menu also retains how long before reset the quota is estimated to
run out. Banked resets are inventory rather than active quota, so their full
schedule stays in the menu. Error fallbacks retain inventory information when no quota
can be displayed. Updates keep their existing tooltip behavior.

Linux transports the title and body separately through StatusNotifierItem.
The body must encode literal provider/account text safely for hosts that accept
markup. Emphasize its first line: current usage, or the freshness qualification. The desktop supplies typography, color, spacing, placement and lifetime;
an HTML illustration is not a guarantee of native pixels. macOS and Windows
receive the same meter in native text tooltips. Windows' fixed text capacity
still applies; the ordinary pinned Codex example fits its 127 UTF-16-unit
limit. Long identities or stale explanations can still be truncated by the
existing Windows transport.

Semantic color follows native capabilities:

| Surface | Meter color |
| --- | --- |
| CLI color table and tray icon | Existing color rendering |
| Plasma hover with known window appearance | Generated colored SVG replaces the same canonical bar; its alt text retains the glyphs |
| macOS clicked menu | Native attributed text with dynamic red/green colors, muted future cells and an emphasized pace marker |
| macOS hover, Windows text, other Linux hosts/menu labels | Complete plain meter and numeric values |

Red means usage ahead of even pace; green marks the unused interval before
that pace. Behind takes precedence over muting for projected/free cells, as in
the CLI. Color supplements the fixed scale, glyphs and exact numbers.
The Linux renderer reads window appearance separately from a forced panel icon
palette, follows portal changes without fetching quotas, and falls back to text
when the host, appearance or bar replacement is unknown. Provider text is
escaped; only generated geometry and closed colors enter the SVG. macOS uses
native disabled text for disabled rows and restores styles when enabled. Plain
updates clear old styles. Other native APIs receive no ANSI or HTML labels.

Verification must cover fresh, pinned, stale, unknown, exhausted and missing
readings; literal markup and Unicode; initial registration and later tooltip
updates; and preservation of all non-hover surfaces. Compare the actual native
hover and clicked menu, including keyboard and assistive-technology access,
before claiming a complete host journey. Offscreen component rendering alone
does not verify pointer activation, focus or screen-reader output.

Research and the source/experiment limitations are recorded in Divan:
`ai-quota-monitoring/native-tray-hover-and-click-have-different-host-capabilities-and-need-a-shared-information-contract.md`.
