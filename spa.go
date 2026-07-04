package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed all:web/build/client
var webBuild embed.FS

// spaHandler serves the built frontend. Real files are served as-is; anything
// else falls back to index.html for HTML navigations (so deep links like
// /settings work on refresh) and 404s otherwise, so missing assets fail loudly
// instead of coming back as the app shell.
func spaHandler() http.Handler {
	static, err := fs.Sub(webBuild, "web/build/client")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		info, err := fs.Stat(static, path)
		if err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		if !strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
