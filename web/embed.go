// Package webassets embeds the browser UI and its local third-party assets.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

func Files() (fs.FS, error) { return fs.Sub(files, "static") }
