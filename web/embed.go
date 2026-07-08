// Package web embeds the static frontend assets served by the HTTP server.
package web

import "embed"

//go:embed *.html *.css *.js
var Files embed.FS
