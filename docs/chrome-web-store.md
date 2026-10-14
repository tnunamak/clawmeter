# Chrome Web Store Submission

Everything the Chrome Web Store asks for when publishing the Clawmeter
extension. Copy each field as written.

## Steps

1. Register a developer account at
   <https://chrome.google.com/webstore/devconsole> and pay the one-time US$5
   fee.
2. Run `make extension-zip`. It writes `clawmeter-extension-<version>.zip`
   without the manifest `key`, which the store rejects.
3. In the developer console, choose **New item** and upload the zip.
4. Copy the item ID the console shows (32 letters a–p). Clawmeter must accept
   that ID before the store version can send it anything: add it to
   `ExtensionOrigins` in `internal/claudeweb/extension.go` and release
   Clawmeter.
5. Fill in the tabs below, then choose **Submit for review**.

## Store listing

**Name:** Clawmeter

**Summary** (132 characters at most):

> Live Claude reset grants and prepaid Claude API credits in the Clawmeter tray app on your computer.

**Description:**

> Clawmeter is a tray app that shows how much of your AI coding quota is left.
> This extension adds two things Claude does not expose to Clawmeter directly:
>
> • Claude reset grants from claude.ai: how many resets you have and when they expire.
> • Prepaid Claude API credits from the Claude Console: balance, spend this month, and when the credit expires.
>
> The extension reads them once a minute with your signed-in browser session
> and sends them only to the Clawmeter app on the same computer
> (127.0.0.1). It sends nothing anywhere else. Keep a claude.ai tab open for
> reset grants: claude.ai answers only requests from its own pages.
>
> Requires the Clawmeter app: https://github.com/tnunamak/clawmeter

**Category:** Developer Tools

**Language:** English

**Icon:** `extension/icons/icon-128.png` (already in the package)

**Screenshot:** `assets/chrome-web-store/screenshot-1280x800.png`

**Small promo tile:** `assets/chrome-web-store/promo-tile-440x280.png`

**Homepage URL:** https://github.com/tnunamak/clawmeter

**Support URL:** https://github.com/tnunamak/clawmeter/issues

The images are rendered from the HTML files next to them; re-render with
`google-chrome --headless=new --window-size=1280,800 --screenshot=… file://…`.

## Privacy practices

**Single purpose:**

> Show the user's Claude reset grants and prepaid Claude API credits in the Clawmeter app on the same computer.

**Permission justifications:**

| Permission | Justification |
| --- | --- |
| `alarms` | Runs the once-a-minute check of claude.ai and the Claude Console. |
| `storage` | Keeps the latest daily Claude Console cost totals, refreshed every 30 minutes, so the extension does not request the cost report every minute. |
| `scripting` | Runs the claude.ai read inside an open claude.ai tab. claude.ai refuses the same request from the extension's background worker. |
| Host `https://claude.ai/*` | Reads the user's organizations and reset grants. |
| Host `https://platform.claude.com/*` | Reads the user's Console organizations, prepaid credit balance, credit grants, and usage costs. |
| Host `http://127.0.0.1/*` | Sends the readings to the Clawmeter app on the same computer. |

**Remote code:** No, I am not using remote code. All code is in the package.

**Data usage.** The extension handles the following; tick these boxes:

- **Personally identifiable information:** the Console organization name,
  which can contain the user's name.
- **Financial and payment information:** the prepaid credit balance, grants,
  and spend.
- **Website content:** reset grant counts and expiry dates from claude.ai.

Leave every other category unticked. Tick all three certifications: the data
is not sold to third parties, not used or transferred for purposes unrelated
to the single purpose, and not used to determine creditworthiness or for
lending.

**Privacy policy URL:**
https://github.com/tnunamak/clawmeter/blob/main/PRIVACY.md#browser-extension

## Distribution

**Visibility:** Public, or Unlisted to install from a direct link only.

**Regions:** All regions.
