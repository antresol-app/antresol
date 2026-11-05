// Package webui serves the embedded single-page app.
package webui

import (
	"embed"
	"io/fs"

	"github.com/antresol-app/antresol/web"
)

//go:embed placeholder
var placeholder embed.FS

// FS is the root of the built frontend (contains index.html). When web/dist
// has not been built (fresh checkout, `go test` without `make build`) a
// minimal placeholder page is served instead.
var FS = pick()

func pick() fs.FS {
	if dist, err := fs.Sub(web.Dist, "dist"); err == nil {
		if _, err := fs.Stat(dist, "index.html"); err == nil {
			return dist
		}
	}
	p, err := fs.Sub(placeholder, "placeholder")
	if err != nil {
		panic(err)
	}
	return p
}
