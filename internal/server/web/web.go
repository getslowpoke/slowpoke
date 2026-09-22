// Package web embeds the built user interface.
package web

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

//go:embed all:dist
var dist embed.FS

const notBuilt = `<!doctype html><html><head><meta charset="utf-8"><title>Slowpoke</title></head>
<body style="font-family:system-ui;padding:3rem;max-width:40rem;margin:auto">
<h1>🐢 Slowpoke</h1><p>The API is running, but the web interface was not built into this binary.</p>
<p>Build it with <code>make web</code> (or use the Docker image).</p></body></html>`

// FS returns the built UI, or a page explaining how to build it.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	return fstest.MapFS{"index.html": {Data: []byte(notBuilt)}}
}
