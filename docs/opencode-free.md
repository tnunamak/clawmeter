# OpenCode Free local observations

`clawmeter opencode` (alias `opencode_free`) reports local activity for OpenCode's
zero-priced model candidates. It is separate from the paid DeepSeek API balance.
No model request or remote quota probe is made.

The collector requires `sqlite3`, an existing local `opencode.db`, and the
OpenCode `models.json` cache. On Linux these normally live under
`~/.local/share/opencode` and `~/.cache/opencode`; XDG data/cache directories are
respected. Other platforms need matching local data paths and a `sqlite3`
executable; their live OpenCode layouts have not been verified.

The model catalog supplies IDs and prices rather than a fixed model list. A
zero price is a candidate classification, not proof of eligibility or remaining
allowance. Unused candidates remain visible. Previously used `-free` IDs absent
from the current catalog remain local observations; currently paid catalog
entries are excluded. The collector is bounded to 256 catalog entries.

SQLite runs with `-readonly` and selects only model IDs, timestamps, token
counters and typed errors from assistant records. It does not copy or emit
prompts, responses, titles or account credentials. Assistant-message counts
exclude error records and are not backend request counts. Token counts are
locally recorded totals; a missing counter prevents a complete total for that
counter. Records from other OpenCode providers are excluded.

“Today” means the UTC reporting date, not the server's quota period. A typed
`provider.free-limit` / 429 record supplies the latest limit-hit time for that
model on that date. No recorded hit means only that no hit was observed locally.
It does not establish available capacity or independent allowances per model.

Reset time remains unknown. A recorded `retryAfterMs` is exposed in JSON and
agent output as `retry_hint_at`, not a verified reset time. Passing midnight or
a retry hint never clears an account's quota. Failed reads, old observations and
a new reporting date do not turn unknown data into fresh zero usage.

The tray lists candidates in a local-observations submenu. Detailed plain,
table, agent and JSON output carry the same observations. These facts do not
enter percentage bars, forecasts, quota risk selection or pinning.
Reading is limited to five seconds, 8 MiB of catalog data and 4 MiB of selected
SQLite output. Unsupported schemas or oversized results fail visibly. SQLite
and catalog format changes may require adapter updates. No log-only or
unassigned error is attributed to a model.
