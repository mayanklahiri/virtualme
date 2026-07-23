// Package controller owns the web assets embedded in the controller binary.
package controller

import "embed"

// WebFS contains the minified same-origin control-plane SPA built by
// scripts/build-web.sh. The explicit font entries make a fontless or
// distless build fail outright.
//
//go:embed web/dist/fonts/InterVariable.woff2 web/dist/fonts/InterVariable-Italic.woff2
//go:embed all:web/dist
var WebFS embed.FS
