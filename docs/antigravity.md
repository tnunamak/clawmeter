# Antigravity quotas

The native source runs `agy --print /usage --output-format json`. This is the
CLI's built-in quota command, rather than a model prompt. Clawmeter requires a
successful `usage` report with explicit zero model turns and token counters.
The CLI owns its existing login and may perform its normal authentication
refresh. Clawmeter does not change saved settings or select a reasoning model.

Groups, model membership descriptions, bucket IDs, reset times, and durations
come from the report. There is no supported-model allowlist. New groups and
bucket IDs are retained. Unknown durations and absent reset times do not acquire
a guessed weekly forecast. The two original weekly bucket IDs keep their old
internal window names so existing pins and caches continue to match; those
aliases do not determine labels, model membership, or supported buckets.

Models inside one reported group share that group's limits. A balance for one
group says nothing about a missing group or about whether a model request will
succeed. `agy models` is a model catalog, not a quota report. Use `clawmeter
antigravity --json` for reported pool scope and measurements.

An unused window without enough elapsed time says `no usage yet`, rather than
promising a forecast soon. Its evaluated JSON forecast retains `state: warming`
and adds `reason: no_usage`; no percentage estimate is emitted. A zero reading
with a mature, known window can still project zero under the existing formula.

The command has a 25-second outer timeout, a 20-second CLI timeout, and a 1 MiB
output limit. It runs in a private temporary directory. Automatic CLI updating
is disabled for this command. Private PATH launchers reject ordinary `open` and
`xdg-open` sign-in requests; this is not an operating-system browser sandbox.
Errors do not expose raw CLI stderr. Owned process groups are stopped on Unix;
Windows uses a kill-on-close job and tree cancellation. These paths have not
been validated against a live Windows or macOS login.

An explicit `token-file` source never invokes the ambient CLI login. It uses
the selected token's remote quota summary. That endpoint can report fewer pools
than the CLI; missing pools are unknown, not unused. A failing native CLI query
does not silently fall back to that incomplete summary.

Structured response changes can still require an adapter update. Missing or
malformed utilization and unrecognized command reports fail rather than
becoming zero usage. New model names alone do not require a code change.
