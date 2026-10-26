# Tray recovery actions

A tray instruction offers the next step on the row where the problem appears. Ordinary measurements stay informational; quota rows still pin the icon.

| State | Action |
| --- | --- |
| Pinned source with retained usage | Keep the last-known usage meter and quota label, using the same forecast age policy as the menu. Preserve the stale warning in the tooltip. |
| Cloud-session promotional credit | Show its reported balance and expiry as a money row. It does not change session/weekly quota, forecasts or pinning. |
| Old Claude reset snapshot | Open Claude Usage. The user must select the browser account matching the source. |
| Old or signed-out API credit balance | Open Claude Console billing. If the extension has never contacted Clawmeter and its folder is absent, open extension setup instead. |
| Expired Claude Code or Codex login | Show sign-in steps with a Copy command button. Commands retain the source's configured profile directory; unknown, repointed or config-token routes get diagnostic steps. |
| Provider connection problem | Show connection diagnostics and the provider's dashboard link. |
| Failed Alibaba quota connection | Show the missing setup/sign-in steps and the terminal reconnect command. |
| Stale usage | Request a forced refresh after any running poll. Repeated requests coalesce. |
| Provider incident | Open the provider's configured service-status page. |
| No visible providers | Show provider setup commands and the setup guide. |
| Invalid configuration | Show the configuration file location and repair steps. Automatic retry continues. |
| Update applied but restart failed | Retry only the restart, using the installed executable path. |
| Homebrew-owned or ambiguous keg destination | Show `brew upgrade clawmeter` and repair instructions. |

The API Credits heading opens Claude Console billing. Claude's heading opens Claude Usage. Fresh balance rows keep their existing informational behavior. Codex's heading opens the plan-usage dashboard linked by [OpenAI Help](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan), rather than API spending. Opening any website does not select an account or renew a CLI login.

Cloud-session credits appear only on sources that report a grant. The tray and full CLI text/table share the balance and expiry wording; `--agent` and `--json` also expose the amounts and cloud-session scope. JSON keeps reported facts in `usage.credit_grants` and evaluated state in `view.credit_grants`. Stale or expired grants expose `last_observed_remaining` instead of a current `remaining` balance. Older percentage-only caches keep their known percentage and expiry without an assumed dollar amount. See [Anthropic's promotion details](https://support.claude.com/en/articles/17152539-cloud-sessions-bonus-credit-promotion).

Recovery help is a private local HTML file. Copy buttons copy commands for the user to run; Clawmeter does not run them. Rendering escapes source labels, instructions and command text. Rendering starts no server and sends no account data. Browser-opening failures produce a desktop error instead of being ignored.

Before dispatch, provider-row actions check current visibility, enablement and the login revision that produced the row, when available. Hiding a provider clears its reset and announcement targets. Update attempts serialize effects, and a successful restart request suppresses queued duplicate attempts. Background check results cannot replace a newer restart, retry or package-manager recovery action.

## Verification

- `go test -tags tray ./internal/tray -count=1`
- `go test -race -tags tray ./internal/tray -run 'Test(Recovery|Update(Action|Menu)|ManualRefresh|StatusRow|HeaderConnection|StaleReset|MissingExtension|HealthyHeader)' -count=1`
- `go test ./internal/update ./internal/provider/openai -count=1`
- `go vet -tags tray ./internal/tray ./internal/update ./internal/provider/openai`

For an isolated browser fixture, create a private directory and set `CLAWMETER_HELP_TEST_DIR` to it when running `go test ./internal/tray -run TestRecoveryHelpBrowserFixture -count=1`. This writes a synthetic page containing no credentials.

The local browser check covers light/dark rendering, narrow-window overflow, the link destination, exact Copy payload and the selection fallback when clipboard access fails. Clipboard callbacks are stubbed, so it does not establish native OS clipboard permissions. Live provider sign-in, automatic browser-account selection and the final authenticated dashboard after redirects are not verified by these fixtures.
