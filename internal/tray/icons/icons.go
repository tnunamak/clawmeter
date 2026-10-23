package icons

import _ "embed"

// App is the extension's gauge logo, shared by the desktop app.
//
//go:embed app.png
var App []byte

// AppOff is the dimmed logo for an unavailable or errored app.
//
//go:embed app-off.png
var AppOff []byte
