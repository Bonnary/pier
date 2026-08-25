// Package assets embeds pier's static assets (currently the
// application logo) into the binary via go:embed. The logo is
// used in the README, can be served by future commands, and is
// available to any package that imports this module.
package assets

import _ "embed"

//go:embed logo.png
var logoPNG []byte

// LogoPNG returns the raw bytes of the pier application logo
// (assets/logo.png). The returned slice is read-only and is shared
// across calls.
func LogoPNG() []byte { return logoPNG }
