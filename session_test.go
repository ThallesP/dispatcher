package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSessionSurvivesTheAccessTokenItWasIssuedWith(t *testing.T) {
	db := cliAuthTestDB(t, "session-refresh.duckdb")
	t.Setenv("RAILWAY_PROJECT_ID", "project-1")
	oldClient := client
	refreshes := 0
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != railwayTokenURL {
			t.Errorf("unexpected request to %s", req.URL)
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		}
		refreshes++
		return testHTTPResponse(req, http.StatusOK,
			`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`), nil
	})}
	defer func() { client = oldClient }()

	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	creds, err := gorm.G[RailwayCredentials](db).First(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expired := session{AccessToken: "old-access", RefreshToken: "refresh", ExpiresAt: now.Add(-time.Minute)}

	resolved, rotated, err := resolveSession(t.Context(), db, creds, expired, now)
	if err != nil {
		t.Fatal(err)
	}
	if !rotated || refreshes != 1 || resolved.AccessToken != "fresh-access" || resolved.RefreshToken != "rotated-refresh" {
		t.Fatalf("resolved = %+v, rotated = %t, refreshes = %d", resolved, rotated, refreshes)
	}
	if !resolved.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires at %s", resolved.ExpiresAt)
	}
	// Background jobs share the grant and must not be left on the spent token.
	stored, err := gorm.G[RailwayCredentials](db).First(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "fresh-access" || stored.RefreshToken != "rotated-refresh" {
		t.Fatalf("stored credentials = %+v", stored)
	}

	// A token with life left in it is used as is.
	fresh := session{AccessToken: "fresh-access", RefreshToken: "refresh", ExpiresAt: now.Add(time.Hour)}
	resolved, rotated, err = resolveSession(t.Context(), db, creds, fresh, now)
	if err != nil || rotated || refreshes != 1 || resolved.AccessToken != "fresh-access" {
		t.Fatalf("resolved = %+v, rotated = %t, refreshes = %d, err = %v", resolved, rotated, refreshes, err)
	}
}

func TestResolveSessionKeepsRefreshTokenRailwayDidNotRotate(t *testing.T) {
	db := cliAuthTestDB(t, "session-keep-refresh.duckdb")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, `{"access_token":"fresh-access","expires_in":3600}`), nil
	})}
	defer func() { client = oldClient }()

	now := time.Now()
	creds, err := gorm.G[RailwayCredentials](db).First(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := resolveSession(t.Context(), db, creds,
		session{AccessToken: "old", RefreshToken: "long-lived-refresh", ExpiresAt: now.Add(-time.Minute)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RefreshToken != "long-lived-refresh" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveSessionFailsWhenRailwayRejectsTheGrant(t *testing.T) {
	db := cliAuthTestDB(t, "session-revoked.duckdb")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusBadRequest, `{"error":"invalid_grant"}`), nil
	})}
	defer func() { client = oldClient }()

	now := time.Now()
	creds, err := gorm.G[RailwayCredentials](db).First(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveSession(t.Context(), db, creds,
		session{AccessToken: "old", RefreshToken: "revoked", ExpiresAt: now.Add(-time.Minute)}, now); err == nil {
		t.Fatal("a revoked grant was accepted")
	}
	if _, _, err := resolveSession(t.Context(), db, creds,
		session{AccessToken: "old", ExpiresAt: now.Add(-time.Minute)}, now); err == nil {
		t.Fatal("a session without a refresh token was accepted")
	}
}

func TestSessionCookieIsEncryptedAndTamperProof(t *testing.T) {
	s := session{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).UTC()}

	value, err := encodeSession("client-secret", s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(value, "access") || strings.Contains(value, "refresh") {
		t.Fatalf("session value leaks the grant: %s", value)
	}
	decoded, err := decodeSession("client-secret", value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AccessToken != s.AccessToken || decoded.RefreshToken != s.RefreshToken || !decoded.ExpiresAt.Equal(s.ExpiresAt) {
		t.Fatalf("decoded = %+v, want %+v", decoded, s)
	}
	if _, err := decodeSession("another-secret", value); err == nil {
		t.Fatal("a session sealed with another secret was accepted")
	}
	tampered := value[:len(value)-2] + "AA"
	if _, err := decodeSession("client-secret", tampered); err == nil {
		t.Fatal("a tampered session was accepted")
	}
	if _, err := decodeSession("client-secret", "not-base64!"); err == nil {
		t.Fatal("a malformed session was accepted")
	}
	if _, err := encodeSession("", s); err == nil {
		t.Fatal("a session was sealed without a secret")
	}
}

// Browsers drop cookies over 4096 bytes, and the sealed session carries two
// Railway tokens instead of one.
func TestSealedSessionFitsInACookie(t *testing.T) {
	token := strings.Repeat("t", 1024)
	value, err := encodeSession("client-secret", session{
		AccessToken: token, RefreshToken: token, ExpiresAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(value)+len(authCookieName) > 4096 {
		t.Fatalf("sealed session is %d bytes", len(value))
	}
}

func TestRequireAuthRefreshesAndReissuesTheSession(t *testing.T) {
	db := cliAuthTestDB(t, "require-auth-refresh.duckdb")
	t.Setenv("RAILWAY_PROJECT_ID", "project-1")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case railwayTokenURL:
			return testHTTPResponse(req, http.StatusOK,
				`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`), nil
		case railwayGraphQLURL:
			return testHTTPResponse(req, http.StatusOK,
				`{"data":{"project":{"workspaceId":"workspace-1"},"me":{"id":"user-1","workspaces":[{"id":"workspace-1"}]}}}`), nil
		}
		t.Errorf("unexpected request to %s", req.URL)
		return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
	})}
	defer func() { client = oldClient }()

	var seen authUser
	handler := requireAuth(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = authUserFrom(r)
		w.WriteHeader(http.StatusOK)
	}))
	expired, err := encodeSession("client-secret", session{
		AccessToken: "old-access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: expired})
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK || seen.ID != "user-1" {
		t.Fatalf("status = %d, user = %+v, body = %s", res.Code, seen, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName || cookies[0].Value == expired {
		t.Fatalf("cookies = %+v", cookies)
	}
	renewed, err := decodeSession("client-secret", cookies[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.AccessToken != "fresh-access" || renewed.RefreshToken != "rotated-refresh" {
		t.Fatalf("renewed session = %+v", renewed)
	}
}

func TestRequireAuthRejectsSessionsRailwayNoLongerHonours(t *testing.T) {
	db := cliAuthTestDB(t, "require-auth-reject.duckdb")
	t.Setenv("RAILWAY_PROJECT_ID", "project-1")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusBadRequest, `{"error":"invalid_grant"}`), nil
	})}
	defer func() { client = oldClient }()

	handler := requireAuth(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler ran for a rejected session")
	}))
	revoked, err := encodeSession("client-secret", session{
		AccessToken: "old-access", RefreshToken: "revoked", ExpiresAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, value := range []string{revoked, "garbage"} {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: value})
		res := httptest.NewRecorder()

		handler.ServeHTTP(res, req)

		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
		}
		cookies := res.Result().Cookies()
		if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
			t.Fatalf("rejected session cookies = %+v", cookies)
		}
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: status = %d", res.Code)
	}
}

func TestConcurrentRequestsRefreshTheGrantOnce(t *testing.T) {
	db := cliAuthTestDB(t, "session-singleflight.duckdb")
	oldClient := client
	var mu sync.Mutex
	refreshes := 0
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		refreshes++
		mu.Unlock()
		// Hold the refresh open long enough for the other callers to pile up.
		time.Sleep(50 * time.Millisecond)
		return testHTTPResponse(req, http.StatusOK,
			`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`), nil
	})}
	defer func() { client = oldClient }()

	now := time.Now()
	creds, err := gorm.G[RailwayCredentials](db).First(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expired := session{AccessToken: "old", RefreshToken: "shared-refresh", ExpiresAt: now.Add(-time.Hour)}

	var wg sync.WaitGroup
	results := make([]session, 5)
	errs := make([]error, 5)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _, errs[i] = resolveSession(t.Context(), db, creds, expired, now)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if results[i].AccessToken != "fresh-access" {
			t.Fatalf("caller %d resolved %+v", i, results[i])
		}
	}
	if refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes)
	}
}
