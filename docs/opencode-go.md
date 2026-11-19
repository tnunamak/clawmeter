# OpenCode Go plan

`clawmeter opencode` (alias `opencode_go`) shows the OpenCode Go plan's usage
windows: 5 hours, 7 days and monthly. It needs no API key. It asks the OpenCode
service that runs on this machine.

## What it needs

1. OpenCode's local service running: `opencode serve --service`. The service
   writes `service.json` under `$XDG_STATE_HOME/opencode/`, or under
   `~/.local/state/opencode/` when that variable is unset. The file holds the
   service URL and a password. clawmeter reads the file and never prints the
   password.
2. Signed in to OpenCode Console on that service:
   `opencode auth login opencode --method device`.

When a step is missing, the status line names the step to run.

## What it shows

| Window | Length | Forecast |
| --- | --- | --- |
| 5 hours | 5 hours, from first use | Yes, from average use since the window began |
| 7 days | 7 days, resets Monday 00:00 UTC | Yes, from average use since the window began |
| Monthly | Not published; resets when a new paid period starts | None; clawmeter shows use and reset only |

OpenCode gives the monthly window only as its reset time. A length worked out
from that time would be a guess, so clawmeter claims no length for it and makes
no projection from it. Budgets in the response are not shown.

## Plan rules

OpenCode sets the limits and the reset times. clawmeter shows what the service
returns and does not convert percentages to dollars.

- A percentage is the share of that window's own limit that is used. At 100%,
  the window is used up until it resets.
- The limits are shares of one monthly allowance. The 5-hour limit is 20% of
  it, and the weekly limit is 50%.
- The 5-hour window starts when you first use the allowance and lasts five
  hours. The weekly window resets on Monday at 00:00 UTC.
- Some models have their own monthly limits. OpenCode lists them on its
  pricing and console pages. The service response does not include them, so
  clawmeter does not show them.
- OpenCode does not publish the monthly allowance in dollars.
- A forecast uses the average use since the window began. Early in a window it
  rests on little data: the 5-hour forecast appears after 30 minutes, and the
  7-day forecast after one hour. Treat early forecasts as rough.

## Errors

| Message says | Cause | Fix |
| --- | --- | --- |
| service file not found | the OpenCode service has not run here | `opencode serve --service` |
| not running | nothing listens at the service URL | start the service |
| did not answer in time | the service is hung | restart it |
| rejected the password (401) | the password in `service.json` is stale | restart OpenCode |
| not signed in | no OpenCode Console session | `opencode auth login opencode --method device` |
| no usage endpoint | this OpenCode build is too old | update OpenCode |

## Not verified

The `service.json` path is confirmed on Linux only. The macOS and Windows paths
are not checked. On Windows, a refused connection is recognized by its dial
operation rather than an error number. That has not been run on Windows.
