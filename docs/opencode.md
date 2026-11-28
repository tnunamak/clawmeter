# OpenCode account

`clawmeter opencode` shows the usage windows of your OpenCode Go plan: 5 hours,
7 days and monthly, and your OpenCode Console budgets in dollars. It reads the
plan by one of two routes, in this order:

1. **The OpenCode service on this machine.** Needs `opencode serve --service`
   running and a sign-in to OpenCode Console on that service.
2. **`OPENCODE_API_KEY`.** Used only when the service route fails, so a machine
   that runs no service can still read the plan.

Both routes return the same Go plan windows, and each window the response includes
is shown. Budgets come from the service route only. An account without the Go plan
has no Go windows and no error, but it still shows any budgets. Free-model
activity is a separate provider, `opencode-free`; see
[opencode-free.md](opencode-free.md).

## Which OpenCode is which

OpenCode has several surfaces, and clawmeter reads each one differently:

- **The Go plan** is read by `opencode`. It shows the 5-hour, weekly and monthly
  windows, in percent.
- **OpenCode Console budgets** are read by `opencode`. They are dollar limits set
  in the Console workspace, and they come from the service route only.
- **Hosted free models** run on OpenCode's servers. Your local OpenCode data
  records their use under the provider `opencode`. `opencode-free` counts the
  zero-priced ones for the current UTC day. That is observed activity, not a
  quota. See [opencode-free.md](opencode-free.md).
- **OpenCode's temporary free DeepSeek route** is a separate inference service.
  The DeepSeek balance provider does not measure its availability, usage fees or
  limits.

Earlier releases mapped `clawmeter opencode` to `opencode-free`. Use
`clawmeter opencode-free` for local activity.

## What it needs

Either route:

- **Service.** OpenCode's local service running (`opencode serve --service`).
  The service writes `service.json` under `$XDG_STATE_HOME/opencode/`, or under
  `~/.local/state/opencode/` when that variable is unset. The file holds the
  service URL and a password. clawmeter reads the file and never prints the
  password. Sign in on that service with
  `opencode auth login opencode --method device`.
- **Key.** `OPENCODE_API_KEY` in the environment clawmeter runs in. clawmeter
  sends it as a bearer token to `https://opencode.ai/zen/go/v1/usage`. The key
  is never printed, logged or placed in an error, and it is not sent to a
  redirect target.

When both routes fail, the status line names each failure and the step to run.

## What it shows

| Window | Length | Forecast |
| --- | --- | --- |
| 5 hours | 5 hours, from first use | Yes, from average use since the window began |
| 7 days | 7 days, resets Monday 00:00 UTC | Yes, from average use since the window began |
| 1M (monthly) | Not published; OpenCode gives only the reset time | None; clawmeter shows use and reset only |

A length worked out from the monthly reset time would be a guess, so clawmeter
claims no length for the monthly window and makes no projection from it.

A forecast needs a warm-up. The 5-hour forecast appears after 30 minutes, and
the 7-day forecast after one hour. The one-hour cap on warm-up is shared by all
providers, and for the 7-day window it is short: 2% used after one hour projects
to 336% by the end of the week. Treat early 7-day forecasts as rough.

## Console budgets

A Console budget is a dollar limit that the OpenCode Console workspace sets on
you. clawmeter shows each budget that has a positive limit as one row, in
dollars: the amount spent, the limit, and when the budget resets. The row name
says what the budget counts, for example `Budget (all models)` or
`Per-model budget (GLM-5.3, Kimi K2.5)`. The tray does not show whether a budget
is shared with the workspace, where reaching it pauses model use for everyone, or
whether it is pooled across members. OpenCode Console shows both.

A budget has no published length, so its row has no forecast, like the monthly
window. Spend over the limit is shown as it is, with `over cap`. For a per-model
budget, the spent figure is the most spent on any one model, so the row tracks
the model nearest its limit.

## Plan rules

OpenCode sets the limits and the reset times. clawmeter shows what the service
or usage route returns and does not convert percentages to dollars.

- A percentage is the share of that window's own limit that is used. At 100%,
  the window is used up until it resets.
- The limits are shares of one monthly allowance. The 5-hour limit is 20% of
  it, and the weekly limit is 50%.
- The 5-hour window starts when you first use the allowance and lasts five
  hours. The weekly window resets on Monday at 00:00 UTC.
- OpenCode lists per-model monthly limits on its pricing page. clawmeter does
  not read them.
- OpenCode does not publish the monthly allowance in dollars.

## Errors

| Message says | Cause | Fix |
| --- | --- | --- |
| service file not found | the OpenCode service has not run here | `opencode serve --service` |
| not running | nothing listens at the service URL | start the service |
| did not answer in time | the service is hung | restart it |
| rejected the password (401) | the password in `service.json` is stale | restart OpenCode |
| Sign in to OpenCode Console | no OpenCode Console session on the service | `opencode auth login opencode --method device` |
| no usage endpoint | this OpenCode build is too old | update OpenCode |
| OPENCODE_API_KEY was rejected (http 401) | the key is wrong or has been revoked | set a current key |
| usage route answered http N | OpenCode's usage route failed | retry later |
| the OPENCODE_API_KEY route also failed | both routes failed; the message gives each reason | fix the route your setup uses |

A 403 from the usage route means OpenCode reports no Go plan for the key's
account. The provider then shows no windows and no error, the same as a service
that reports no Go plan.

## Not verified

The `service.json` path is the same relative path on every OS in OpenCode's
code, but clawmeter has run it only on Linux. macOS and Windows have not been
run. On Windows, a refused connection is recognized by its dial operation rather
than an error number; that has not been run on Windows either.

On 2026-11-19 the key route returned HTTP 200 with a real key, and its three
percentages matched OpenCode Console.

No budget has been checked against a live response. The workspace checked has no
budgets, so the budget field names and dollar units come from OpenCode's public
schema. Check the first real budget against OpenCode Console before relying on its
figures.
