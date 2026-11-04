// Package web holds the web application as the binary serves it. Running it needs no Node.js: a build
// writes into static, and the binary embeds whatever is there. A tree that was never built holds only
// the placeholder page, so a plain checkout still builds (D28).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var files embed.FS

// Assets is the built application, with its files at the root.
func Assets() (fs.FS, error) { return fs.Sub(files, "static") }
