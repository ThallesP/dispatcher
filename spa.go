package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed all:web/build/client
var webBuild embed.FS

// Cache policies for the embedded frontend. Vite fingerprints everything under
// assets/, so those URLs can never change meaning and are cached forever. The
// shell and favicons keep their URL across deploys, so they are revalidated
// instead: a CDN may still store them, it just has to ask before reusing them.
const (
	immutableCacheControl = "public, max-age=31536000, immutable"
	staticCacheControl    = "public, max-age=3600, stale-while-revalidate=86400"
	shellCacheControl     = "no-cache"
)

// spaHandler serves the built frontend. Real files are served as-is; anything
// else falls back to index.html for HTML navigations (so deep links like
// /settings work on refresh) and 404s otherwise, so missing assets fail loudly
// instead of coming back as the app shell.
func spaHandler() http.Handler {
	static, err := fs.Sub(webBuild, "web/build/client")
	if err != nil {
		log.Fatal(err)
	}
	etags := buildETags(static)
	fileServer := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		info, err := fs.Stat(static, path)
		if err == nil && !info.IsDir() {
			setStaticHeaders(w, path, etags)
			fileServer.ServeHTTP(w, r)
			return
		}

		// The same URL answers with the shell or a 404 depending on Accept, so
		// a shared cache has to key on it too.
		w.Header().Set("Vary", "Accept")
		if !strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		setStaticHeaders(w, "index.html", etags)
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// setStaticHeaders picks the cache policy for a built file and attaches its
// content ETag, which lets http.ServeContent answer revalidations with a 304.
func setStaticHeaders(w http.ResponseWriter, path string, etags map[string]string) {
	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", immutableCacheControl)
	} else if path == "index.html" {
		w.Header().Set("Cache-Control", shellCacheControl)
	} else {
		w.Header().Set("Cache-Control", staticCacheControl)
	}
	if etag, ok := etags[path]; ok {
		w.Header().Set("ETag", etag)
	}
}

// buildETags hashes the embedded build once at startup. The files never change
// while the process runs, so a deploy is the only thing that moves an ETag.
func buildETags(static fs.FS) map[string]string {
	etags := map[string]string{}
	err := fs.WalkDir(static, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		etags[path] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	return etags
}
