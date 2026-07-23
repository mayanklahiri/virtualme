// Package controller owns the web assets embedded in the controller binary.
package controller

import "embed"

// WebFS contains the complete same-origin control-plane SPA.
//
//go:embed web/static/fonts/InterVariable.woff2 web/static/fonts/InterVariable-Italic.woff2
//go:embed all:web/static
var WebFS embed.FS
