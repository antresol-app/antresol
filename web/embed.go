// Package web exposes the built frontend (web/dist) for embedding.
package web

import "embed"

// Dist holds web/dist. Only .gitkeep is committed; `make build` fills it.
//
//go:embed all:dist
var Dist embed.FS
