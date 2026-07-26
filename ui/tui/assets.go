// Package tuiassets exposes the deterministic bundled Ink terminal client.
package tuiassets

import _ "embed"

// Bundle is the self-contained Node.js terminal artifact.
//
//go:embed dist/ion-tui.mjs
var Bundle []byte
