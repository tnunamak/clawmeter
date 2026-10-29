// Package extension holds the Clawmeter browser extension, embedded so the
// app can install the unpacked copy without the source tree.
package extension

import "embed"

// Files are the extension files a browser loads; tests and docs stay out.
//
//go:embed manifest.json background.js console.js resets.js openai.js openai-observer.js openai-relay.js popup.html popup.js icons/icon-16.png icons/icon-32.png icons/icon-16-off.png icons/icon-32-off.png icons/icon-48.png icons/icon-128.png
var Files embed.FS
