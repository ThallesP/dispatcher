package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duckdb "github.com/vogo/duckdb/v2"
	"gorm.io/gorm"
)

func TestCLIStateIsSignedAndExpires(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	loginCode := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32))
	challenge := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	want := cliOAuthState{
		LoginCode: loginCode,
		Challenge: challenge,
		ExpiresAt: now.Add(time.Minute).Unix(),
	}
	encoded, err := encodeCLIState("client-secret", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCLIState("client-secret", encoded, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded state = %+v, want %+v", got, want)
	}

	parts := strings.Split(encoded, ".")
	parts[1] = "A" + parts[1][1:]
	if _, err := decodeCLIState("client-secret", strings.Join(parts, "."), now); err == nil {
		t.Fatal("tampered state was accepted")
	}
	if _, err := decodeCLIState("client-secret", encoded, now.Add(time.Minute)); err == nil {
		t.Fatal("expired state was accepted")
	}
}

func TestCLIAuthStartReturnsRailwayURLWithSignedState(t *testing.T) {
	db := cliAuthTestDB(t, "cli-start.duckdb")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	cliAuthHandoffs = cliAuthHandoffStore{entries: make(map[string]cliAuthHandoff)}
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	payload, _ := json.Marshal(map[string]string{"challenge": cliLoginChallenge(verifier)})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/cli/start", bytes.NewReader(payload))
	res := httptest.NewRecorder()

	handleCLIAuthStart(db).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", res.Header().Get("Cache-Control"))
	}
	var response struct {
		AuthorizationURL string    `json:"authorizationUrl"`
		Code             string    `json:"code"`
		ExpiresAt        time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !validCLISecret(response.Code) || !response.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid start response: %+v", response)
	}
	authorizationURL, err := url.Parse(response.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if authorizationURL.Host != "backboard.railway.com" || authorizationURL.Path != "/oauth/auth" {
		t.Fatalf("authorization URL = %s", authorizationURL)
	}
	state, err := decodeCLIState("client-secret", authorizationURL.Query().Get("state"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if state.LoginCode != response.Code || state.Challenge != cliLoginChallenge(verifier) {
		t.Fatalf("state = %+v", state)
	}
}

func TestBrowserAuthRedirectUsesStateCookie(t *testing.T) {
	db := cliAuthTestDB(t, "browser-redirect.duckdb")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/redirect", nil)
	res := httptest.NewRecorder()

	handleAuthRedirect(db).ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	authorizationURL, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	if !validCLISecret(state) {
		t.Fatalf("invalid OAuth state %q", state)
	}
	var stateCookie *http.Cookie
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == browserOAuthStateCookie {
			stateCookie = cookie
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("browser OAuth state cookie was not set")
	}
	if stateCookie.Value != state || stateCookie.Path != "/api/auth/callback" ||
		!stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie = %+v", stateCookie)
	}
}

func TestBrowserAuthCallbackRejectsInvalidStateBeforeExchange(t *testing.T) {
	db := cliAuthTestDB(t, "browser-invalid-state.duckdb")
	oldClient := client
	requestCount := 0
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
	})}
	defer func() { client = oldClient }()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=railway-code&state=wrong", nil)
	req.AddCookie(&http.Cookie{Name: browserOAuthStateCookie, Value: "expected"})
	res := httptest.NewRecorder()

	handleAuthCallback(db).ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid or expired OAuth state") {
		t.Fatalf("callback: status = %d, body = %s", res.Code, res.Body.String())
	}
	if requestCount != 0 {
		t.Fatalf("OAuth exchange received %d requests before state validation", requestCount)
	}
}

func TestBrowserAuthCallbackUsesSharedRailwayCompletion(t *testing.T) {
	db := cliAuthTestDB(t, "browser-callback.duckdb")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	t.Setenv("RAILWAY_PROJECT_ID", "project-1")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.String() {
		case railwayTokenURL:
			body = `{"access_token":"railway-session","refresh_token":"refresh","expires_in":3600}`
		case railwayGraphQLURL:
			body = `{"data":{"project":{"workspaceId":"workspace-1"},"me":{"id":"user-1","workspaces":[{"id":"workspace-1"}]}}}`
		default:
			t.Errorf("unexpected request to %s", req.URL)
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		}
		return testHTTPResponse(req, http.StatusOK, body), nil
	})}
	defer func() { client = oldClient }()

	state := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?"+url.Values{
		"code":  {"railway-code"},
		"state": {state},
	}.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: browserOAuthStateCookie, Value: state})
	res := httptest.NewRecorder()

	handleAuthCallback(db).ServeHTTP(res, req)

	if res.Code != http.StatusFound || res.Header().Get("Location") != "/" {
		t.Fatalf("callback: status = %d, location = %q, body = %s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == authCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure {
		t.Fatalf("session cookie = %+v", sessionCookie)
	}
	if strings.Contains(sessionCookie.Value, "railway-session") || strings.Contains(sessionCookie.Value, "refresh") {
		t.Fatal("session cookie exposed the Railway grant in the clear")
	}
	// The cookie outlives the hour-long Railway token sealed inside it.
	if sessionCookie.MaxAge < int(24*time.Hour/time.Second) {
		t.Fatalf("session cookie MaxAge = %d", sessionCookie.MaxAge)
	}
	decoded, err := decodeSession("client-secret", sessionCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AccessToken != "railway-session" || decoded.RefreshToken != "refresh" {
		t.Fatalf("session = %+v", decoded)
	}
}

func TestCLIAuthExchangePollsThenConsumesCompletedLogin(t *testing.T) {
	cliAuthHandoffs = cliAuthHandoffStore{entries: make(map[string]cliAuthHandoff)}
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	code, _, err := cliAuthHandoffs.start(cliLoginChallenge(verifier), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier})
	exchange := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/cli/exchange", bytes.NewReader(payload))
		res := httptest.NewRecorder()
		handleCLIAuthExchange(res, req)
		return res
	}
	if pending := exchange(); pending.Code != http.StatusAccepted || !strings.Contains(pending.Body.String(), "pending") {
		t.Fatalf("pending exchange: status = %d, body = %s", pending.Code, pending.Body.String())
	}
	if !cliAuthHandoffs.complete(code, "railway-session", time.Now().Add(time.Hour), "", time.Now()) {
		t.Fatal("could not complete pending login")
	}
	ready := exchange()
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), "railway-session") {
		t.Fatalf("ready exchange: status = %d, body = %s", ready.Code, ready.Body.String())
	}
	if second := exchange(); second.Code != http.StatusUnauthorized {
		t.Fatalf("second exchange status = %d, want 401", second.Code)
	}
}

func TestCLIAuthExchangeRejectsWrongVerifier(t *testing.T) {
	store := cliAuthHandoffStore{entries: make(map[string]cliAuthHandoff)}
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	wrongVerifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	code, _, err := store.start(cliLoginChallenge(verifier), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, status := store.exchange(code, wrongVerifier, time.Now()); status != cliHandoffInvalid {
		t.Fatal("wrong verifier was accepted")
	}
	if _, status := store.exchange(code, verifier, time.Now()); status != cliHandoffPending {
		t.Fatal("correct verifier could not continue polling after a rejected attempt")
	}
}

func TestAuthCallbackCompletesPendingCLIFlowWithoutExposingSession(t *testing.T) {
	db := cliAuthTestDB(t, "cli-callback.duckdb")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	t.Setenv("RAILWAY_PROJECT_ID", "project-1")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.String() {
		case railwayTokenURL:
			body = `{"access_token":"railway-session","refresh_token":"refresh","expires_in":3600}`
		case railwayGraphQLURL:
			body = `{"data":{"project":{"workspaceId":"workspace-1"},"me":{"id":"user-1","workspaces":[{"id":"workspace-1"}]}}}`
		default:
			t.Errorf("unexpected request to %s", req.URL)
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		}
		return testHTTPResponse(req, http.StatusOK, body), nil
	})}
	defer func() { client = oldClient }()
	cliAuthHandoffs = cliAuthHandoffStore{entries: make(map[string]cliAuthHandoff)}

	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	loginCode, expiresAt, err := cliAuthHandoffs.start(cliLoginChallenge(verifier), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, err := encodeCLIState("client-secret", cliOAuthState{
		LoginCode: loginCode,
		Challenge: cliLoginChallenge(verifier),
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?"+url.Values{
		"code":  {"railway-code"},
		"state": {state},
	}.Encode(), nil)
	res := httptest.NewRecorder()

	handleAuthCallback(db).ServeHTTP(res, req)

	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "CLI login complete") {
		t.Fatalf("callback: status = %d, body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "railway-session") || strings.Contains(res.Header().Get("Location"), "railway-session") {
		t.Fatal("Railway session was exposed to the browser")
	}
	handoff, status := cliAuthHandoffs.exchange(loginCode, verifier, time.Now())
	if status != cliHandoffReady || handoff.Session == "" {
		t.Fatalf("handoff = %+v, status = %d", handoff, status)
	}
	decoded, err := decodeSession("client-secret", handoff.Session)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AccessToken != "railway-session" || decoded.RefreshToken != "refresh" {
		t.Fatalf("session = %+v", decoded)
	}
}

func cliAuthTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(duckdb.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&RailwayCredentials{}); err != nil {
		t.Fatal(err)
	}
	creds := RailwayCredentials{ClientID: "client-id", ClientSecret: "client-secret"}
	if err := gorm.G[RailwayCredentials](db).Create(t.Context(), &creds); err != nil {
		t.Fatal(err)
	}
	return db
}

func testHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCLIIssueRedirectMarksTheBrowserState(t *testing.T) {
	db := cliAuthTestDB(t, "cli-issue-redirect.duckdb")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	res := httptest.NewRecorder()

	handleCLIIssueRedirect(db).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/auth/cli/issue", nil))

	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	authorizationURL, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	if !strings.HasPrefix(state, cliIssueStatePrefix) || !validCLISecret(strings.TrimPrefix(state, cliIssueStatePrefix)) {
		t.Fatalf("state = %q, want an issue-prefixed secret", state)
	}
	if authorizationURL.Query().Get("prompt") != "consent" {
		t.Fatal("a CLI grant needs prompt=consent to come with a refresh token")
	}
	cookie := findCookie(res.Result().Cookies(), browserOAuthStateCookie)
	if cookie == nil || cookie.Value != state {
		t.Fatalf("state cookie = %+v, want it bound to %q", cookie, state)
	}
}

func TestCLIIssueCallbackParksAFreshGrantBehindASingleUseToken(t *testing.T) {
	db := cliAuthTestDB(t, "cli-issue-callback.duckdb")
	t.Setenv("CALLBACK_URL", "https://dispatcher.example/api/auth/callback")
	t.Setenv("RAILWAY_PROJECT_ID", "project-1")
	oldClient := client
	client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.String() {
		case railwayTokenURL:
			body = `{"access_token":"cli-grant","refresh_token":"cli-refresh","expires_in":3600}`
		case railwayGraphQLURL:
			body = `{"data":{"project":{"workspaceId":"workspace-1"},"me":{"id":"user-1","workspaces":[{"id":"workspace-1"}]}}}`
		default:
			t.Errorf("unexpected request to %s", req.URL)
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		}
		return testHTTPResponse(req, http.StatusOK, body), nil
	})}
	defer func() { client = oldClient }()
	cliAuthHandoffs = cliAuthHandoffStore{entries: make(map[string]cliAuthHandoff)}

	state := cliIssueStatePrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32))
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?"+url.Values{
		"code":  {"railway-code"},
		"state": {state},
	}.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: browserOAuthStateCookie, Value: state})
	res := httptest.NewRecorder()

	handleAuthCallback(db).ServeHTTP(res, req)

	if res.Code != http.StatusFound || res.Header().Get("Location") != "/?cli=ready" {
		t.Fatalf("callback: status = %d, location = %q, body = %s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
	cookies := res.Result().Cookies()
	// Railway rotates refresh tokens, so the browser must keep its own grant.
	if findCookie(cookies, authCookieName) != nil {
		t.Fatal("minting a CLI login replaced the browser's session")
	}
	tokenCookie := findCookie(cookies, cliLoginTokenCookie)
	if tokenCookie == nil || !validCLISecret(tokenCookie.Value) || tokenCookie.Path != cliLoginPickupPath ||
		!tokenCookie.HttpOnly || !tokenCookie.Secure || tokenCookie.MaxAge <= 0 {
		t.Fatalf("token cookie = %+v", tokenCookie)
	}

	// The dashboard picks the token up once; the cookie is cleared with it.
	pickupReq := httptest.NewRequest(http.MethodPost, cliLoginPickupPath, nil)
	pickupReq.AddCookie(tokenCookie)
	pickup := httptest.NewRecorder()
	handleCLILoginPickup(pickup, pickupReq)
	var picked struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(pickup.Body.Bytes(), &picked); err != nil || pickup.Code != http.StatusOK {
		t.Fatalf("pickup: status = %d, body = %s", pickup.Code, pickup.Body.String())
	}
	if picked.Token != tokenCookie.Value || !picked.ExpiresAt.After(time.Now()) {
		t.Fatalf("picked = %+v", picked)
	}
	if cleared := findCookie(pickup.Result().Cookies(), cliLoginTokenCookie); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("pickup did not clear the token cookie: %+v", cleared)
	}

	redeem := func() *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		body := strings.NewReader(`{"token":"` + picked.Token + `"}`)
		handleCLILoginRedeem(res, httptest.NewRequest(http.MethodPost, "/api/auth/cli/redeem", body))
		return res
	}
	first := redeem()
	var redeemed storedSessionResponse
	if err := json.Unmarshal(first.Body.Bytes(), &redeemed); err != nil || first.Code != http.StatusOK {
		t.Fatalf("redeem: status = %d, body = %s", first.Code, first.Body.String())
	}
	decoded, err := decodeSession("client-secret", redeemed.Session)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AccessToken != "cli-grant" || decoded.RefreshToken != "cli-refresh" {
		t.Fatalf("session = %+v", decoded)
	}
	if again := redeem(); again.Code != http.StatusUnauthorized {
		t.Fatalf("second redeem: status = %d, want the token to be single use", again.Code)
	}
}

func TestCLIRedeemOnlyAcceptsDashboardMintedTokens(t *testing.T) {
	store := cliAuthHandoffStore{entries: make(map[string]cliAuthHandoff)}
	now := time.Now()

	// A polling-flow code is released only against its verifier.
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	code, _, err := store.start(cliLoginChallenge(verifier), now)
	if err != nil {
		t.Fatal(err)
	}
	store.complete(code, "session", now.Add(time.Hour), "", now)
	if _, ok := store.redeem(code, now); ok {
		t.Fatal("redeem released a polling-flow login without its verifier")
	}

	// And a minted token cannot be pulled through exchange.
	token, _, err := store.issue("session", now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, status := store.exchange(token, verifier, now); status != cliHandoffInvalid {
		t.Fatalf("exchange status = %d for a minted token", status)
	}
	if _, ok := store.redeem(token, now.Add(cliOAuthStateTTL+time.Second)); ok {
		t.Fatal("redeem accepted an expired token")
	}
}

type storedSessionResponse struct {
	Session   string    `json:"session"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
