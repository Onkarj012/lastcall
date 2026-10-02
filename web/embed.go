// Package web holds the UI, embedded into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js app.css logos.js analytics.js
var files embed.FS

var FS fs.FS = files
