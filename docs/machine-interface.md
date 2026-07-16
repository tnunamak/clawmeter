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
only when it belongs to Clawmeter's fixed safe vocabulary; provider-supplied labels are
omitted because they could contain account-specific text.

The diagnostic never invokes quota-reset redemption or other quota-consuming actions.
Providers that normally refresh expired local OAuth credentials may do so during a live
probe, just as they do during an ordinary Clawmeter refresh.

## Compatibility policy

Both interfaces use an integer major `schema_version`:

- adding optional fields does not change the version;
- removing fields, changing their meaning or type, changing required structure, or adding
  values to a closed enum bumps the version;
- consumers must ignore unknown fields and reject unsupported schema versions;
- Clawmeter keeps committed success and partial-failure fixtures for each current schema.

Schema files describe the stable interoperability contract, not every incidental field
that a provider may add to its internal response.
