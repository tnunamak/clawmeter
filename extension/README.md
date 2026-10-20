# Clawmeter browser extension

Reads Claude reset grants from claude.ai and API credits from the Claude Console with your browser session and sends them to Clawmeter on this computer, once a minute.

claude.ai answers 403 to requests from the extension's background worker, so `background.js` runs the claude.ai read inside each open claude.ai tab, in an isolated script world, and falls back to the worker when no tab answers. Console reads run in each open Console tab and always once in the worker, which has the browser's main session, so a signed-out container tab cannot hide a signed-in main session. Each cached Console organization records whether the main session holds it. One the main session no longer holds is dropped at once; one held only by a container stays cached for a week, because its tab may just be closed, and while any such organization is cached the extension does not tell Clawmeter that the Console is signed out. The popup says when a tab did not answer, since accounts signed in only there are missing from that reading. Tabs in different browser containers can hold different accounts' sessions, so every tab is read; ordinary tabs share one session, and an organization seen in one tab is not read again.

- `resets.js` reads reset grants and posts them to `/v1/resets`.
- `console.js` reads prepaid API credits and posts them to `/v1/api-credits`.
- `popup.html` shows the latest check: whether Clawmeter received it, what each source returned, and links to the pages that fix a problem. `background.js` keeps that check in session storage, so names never reach the disk.
- The receiver is `internal/claudeweb/extension.go`. It admits only the extension IDs in `ExtensionOrigins`; the manifest `key` fixes the unpacked ID.

Run the tests with `node --test extension/*.test.js`. `go test ./internal/claudeweb` also runs both readers in Node and posts their output to the Go receiver.

`make extension-zip` builds the Chrome Web Store upload; see [docs/chrome-web-store.md](../docs/chrome-web-store.md).
