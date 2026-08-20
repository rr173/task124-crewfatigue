// Package webfs embeds the static frontend so both the main binary and the
// selfcheck smoke test can serve the same page without duplicating the
// //go:embed directive (the directive cannot escape its package directory, so
// the web source must live under this package).
package webfs

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var web embed.FS

// FS returns the embedded web/ filesystem rooted at the directory itself (so
// "index.html", "app.js", "styles.css" are top-level entries). Callers wrap it
// with http.FS to get an http.FileSystem.
func FS() fs.FS {
	sub, err := fs.Sub(web, "web")
	if err != nil {
		// fs.Sub only errors if the name is malformed; "web" is a valid dir.
		panic(err)
	}
	return sub
}

// IndexContent returns the raw index.html bytes, used by the smoke-test to
// verify the page was embedded and contains expected content.
func IndexContent() []byte {
	b, err := web.ReadFile("web/index.html")
	if err != nil {
		return nil
	}
	return b
}

// HTTPFS returns an http.FileSystem over the embedded web assets. nil-safe
// callers may pass the result directly to http.FileServer.
func HTTPFS() http.FileSystem { return http.FS(FS()) }
