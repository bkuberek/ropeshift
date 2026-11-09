// Package web holds the web application as the binary serves it. Running it needs no Node.js: a build
// writes into static, and the binary embeds whatever is there. A release build embeds the application
// GoReleaser builds there first. A plain checkout holds only the placeholder page, so it builds without
// Node.js and the binary serves that page.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var files embed.FS

// Assets is the built application, with its files at the root.
func Assets() (fs.FS, error) { return fs.Sub(files, "static") }
