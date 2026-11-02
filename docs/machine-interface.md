# Machine interface

Clawmeter's supported automation boundary is its executable JSON interface. The Go
packages remain internal so provider implementation details can change without becoming
an SDK compatibility burden.

## Status

```bash
clawmeter --json
```

The response follows [`status-v1.schema.json`](schemas/status-v1.schema.json). This is
the interface for quota-aware tools such as agent orchestrators and status-line helpers.
Provider failures are represented inside the affected provider's `usage` object; a
successful process exit does not imply that every provider refreshed successfully.
Status JSON is local automation data, not a redacted issue-report artifact; use the
diagnostic command below when sharing troubleshooting output.

Source enrollment changes apply after restarting a running tray.

The top-level `view` object names every source in display order, and its
`accounts` keys are source keys. A single source with no reading yet has a
provider object that carries only `maturity` and a `view` whose reading state is
`unavailable`, so `view.accounts` never names a source that `providers` omits.
Its `usage` is absent, which means no reading, not zero use. `view.worst` names the
most at-risk window. Urgency is that one fact; `accounts` and each account's
`windows` stay in display order.

When a family has more than one enrolled source, its provider object keeps the legacy
family key and adds a `sources` array. Each array item contains only the configured
source `id` and optional `label`, plus that source's usage, forecast, and status. The
array is never aggregated. A single source keeps the existing shape. Source IDs and
labels are user-chosen display metadata and are emitted deliberately. Clawmeter rejects
common path, email, known key-prefix, and long high-entropy forms, but users must not put
secrets or account identifiers in them. Credential references and discovered paths,
tokens, emails, and account identifiers are not emitted. Raw fetch errors are reduced to
a closed, non-secret message before entering status output or the cache; provider-authored
status messages may still be present.

Consumers should:

- require a supported top-level `schema_version`;
- treat missing providers, provider errors, and stale readings as distinct from zero use;
- ignore unknown fields;
- use `usage.windows[].name` as the key into `forecast.windows`;
- impose their own timeout when invoking Clawmeter.

## Provider diagnostics

```bash
clawmeter providers diagnose codex --json --pretty
clawmeter providers diagnose all --json
```

The response follows [`diagnose-v1.schema.json`](schemas/diagnose-v1.schema.json).
Diagnosing one provider performs a live probe through Clawmeter's normal quota-fetch path
when its setup is ready, even if normal polling is disabled. Diagnosing `all` probes only
providers Clawmeter would normally poll and reports setup state for the others.

Diagnostic output is designed for local troubleshooting and issue reports. It includes
quota percentages, reset times, balance remaining values, and reset-credit counts that
Clawmeter already displays. It never includes auth tokens, cookies, emails, account or
organization IDs, raw provider responses, raw request bodies, or raw error text. Errors
are reduced to a closed category and safe message.

Provider identity is the canonical provider key, without account- or source-derived
display metadata. Quota windows and balances are numbered. A window name is included
only when it belongs to Clawmeter's safe vocabulary. Fixed names are allowlisted;
Anthropic's model-scoped weekly names are included only when they match the
bounded Claude family/version shape such as `7d Fable 5.1` or `7d Claude 4
Sonnet`. Other provider-supplied labels are omitted because they could contain
account-specific text.

The diagnostic never invokes quota-reset redemption or other quota-consuming actions.
Providers that normally refresh expired local OAuth credentials may do so during a live
probe, just as they do during an ordinary Clawmeter refresh.

## Usage history

```bash
clawmeter history --json [--since 168h] [--provider claude]
```

The command prints one JSON object per line and reads only local files. Each line
follows [`history-v1.schema.json`](schemas/history-v1.schema.json). `reading_time` is
when the provider reading was fetched; `recorded_at` is when Clawmeter wrote it;
`age_seconds` records the reading's age at write time. `account` is the source label
shown by Clawmeter, falling back to its source ID. `source` is that source ID. The
history does not contain credentials, account IDs, or email addresses. A zero
`window_minutes` means Clawmeter has no duration for that quota window.

Clawmeter records successful readings by default in monthly JSONL files beside
`usage.json`, with mode `0600`, a 50 MB total cap, and hourly heartbeats for unchanged
windows. Turn recording off with `clawmeter config set usage_history false`; use
`--no-history` on a status refresh to suppress recording for that invocation.
Re-enable it with `clawmeter config set usage_history true`.

A malformed final record without a newline is treated as an interrupted write:
reads skip it, and the next append truncates only those final bytes under the
history lock. Complete records without a final newline are preserved and given a
newline before appending. Malformed newline-terminated records, interior corruption,
and unsupported schema versions return an error without changing history files.

After a clock rollback, future `recorded_at` values remain in the history but do
not control deduplication. The latest record at or before the current time is the
heartbeat anchor; if none exists, the next successful observation starts one.
Retention removes non-current month files in filename order, including future
months, before trimming the current month to the total byte cap.

## Compatibility policy

Both interfaces use an integer major `schema_version`:

- adding optional fields does not change the version;
- removing fields, changing their meaning or type, changing required structure, or adding
  values to a closed enum bumps the version;
- consumers must ignore unknown fields and reject unsupported schema versions;
- Clawmeter keeps committed success and partial-failure fixtures for each current schema.

Schema files describe the stable interoperability contract, not every incidental field
that a provider may add to its internal response.
