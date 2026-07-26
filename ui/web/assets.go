// Package webassets exposes the deterministic production browser bundle.
package webassets

import (
	"embed"
	"io/fs"
)

// Files contains the Vite production bundle checked into release builds.
//
//go:embed dist
var Files embed.FS

// Distribution returns the bundle root.
func Distribution() (fs.FS, error) {
	return fs.Sub(Files, "dist")
}
