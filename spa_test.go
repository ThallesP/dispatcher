package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// firstAsset returns any fingerprinted file from the embedded build, since the
// exact hashed names change on every frontend build.
func firstAsset(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(webBuild, "web/build/client/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return "/assets/" + entry.Name()
		}
	}
	t.Fatal("no built assets to serve")
	return ""
}

func TestFingerprintedAssetsAreCachedForever(t *testing.T) {
	recorder := httptest.NewRecorder()
	spaHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, firstAsset(t), nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != immutableCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, immutableCacheControl)
	}
	if recorder.Header().Get("ETag") == "" {
		t.Error("asset served without an ETag")
	}
}

func TestAssetRevalidationReturnsNotModified(t *testing.T) {
	handler := spaHandler()
	asset := firstAsset(t)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, asset, nil))

	request := httptest.NewRequest(http.MethodGet, asset, nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request)

	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried %d bytes of body", second.Body.Len())
	}
}

func TestAppShellIsRevalidatedNotReused(t *testing.T) {
	for _, path := range []string{"/", "/analytics"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", "text/html")
		recorder := httptest.NewRecorder()
		spaHandler().ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, recorder.Code)
		}
		if got := recorder.Header().Get("Cache-Control"); got != shellCacheControl {
			t.Errorf("%s Cache-Control = %q, want %q", path, got, shellCacheControl)
		}
		if recorder.Header().Get("ETag") == "" {
			t.Errorf("%s served the shell without an ETag", path)
		}
	}
}

// The shell fallback only applies to HTML navigations, so a shared cache must
// not reuse one answer for the other kind of request.
func TestClientRouteFallbackVariesOnAccept(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/analytics", nil)
	request.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	spaHandler().ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Vary"); !strings.Contains(got, "Accept") {
		t.Errorf("Vary = %q, want it to include Accept", got)
	}
}

func TestMissingAssetIsNotCached(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/assets/gone-DEADBEEF.js", nil)
	request.Header.Set("Accept", "*/*")
	recorder := httptest.NewRecorder()
	spaHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestAPIResponsesAreNeverCached(t *testing.T) {
	handler := noStoreAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/payouts", nil))
	if got := api.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("/api Cache-Control = %q, want no-store", got)
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if got := asset.Header().Get("Cache-Control"); got != "" {
		t.Errorf("static Cache-Control = %q, want the SPA handler to decide", got)
	}
}
