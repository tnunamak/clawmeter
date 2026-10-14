# Clawmeter browser extension

Reads Claude reset grants from claude.ai and API credits from the Claude Console with your browser session and sends them to Clawmeter on this computer, once a minute.

claude.ai answers 403 to requests from the extension's background worker, so `background.js` runs the claude.ai read inside an open claude.ai tab, in an isolated script world, and falls back to the worker when no tab is open. Console reads run in the worker.

- `resets.js` reads reset grants and posts them to `/v1/resets`.
- `console.js` reads prepaid API credits and posts them to `/v1/api-credits`.
- The receiver is `internal/claudeweb/extension.go`. It admits only the extension IDs in `ExtensionOrigins`; the manifest `key` fixes the unpacked ID.

Run the tests with `node --test extension/*.test.js`. `go test ./internal/claudeweb` also runs both readers in Node and posts their output to the Go receiver.

`make extension-zip` builds the Chrome Web Store upload; see [docs/chrome-web-store.md](../docs/chrome-web-store.md).
