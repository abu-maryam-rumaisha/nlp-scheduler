// Package web embeds the built single-page app (web/dist) into the API
// binary. Build it with `pnpm build` in this directory before `go build`.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// UI returns the built app, or nil if it has not been built.
func UI() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
