package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunLoginPromptsForURLAndReusesIt(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	loginCode, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cli/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorizationUrl": "https://backboard.railway.com/oauth/auth?client_id=test&state=signed",
				"code":             loginCode,
				"expiresAt":        time.Now().Add(time.Minute),
			})
		case "/api/auth/cli/exchange":
			_ = json.NewEncoder(w).Encode(storedSession{Session: "saved-session", ExpiresAt: expiresAt})
		case "/api/auth/me":
			cookie, err := r.Cookie("railway_token")
			if err != nil || cookie.Value != "saved-session" {
				t.Errorf("session cookie = %v, %v", cookie, err)
			}
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		default:
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	credentialsPath := t.TempDir() + "/credentials.json"
	getenv := func(string) string { return "" }
	var stdout, stderr bytes.Buffer
	code := runWithBrowser(
		[]string{"--config", credentialsPath, "login"},
		getenv,
		strings.NewReader(server.URL+"\n"),
		&stdout,
		&stderr,
		func(string) error { return nil },
	)
	if code != 0 {
		t.Fatalf("login exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Dispatcher URL: ") {
		t.Fatalf("login did not prompt for URL: %q", stderr.String())
	}
	storedURL, ok, err := loadStoredURL(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || storedURL != server.URL {
		t.Fatalf("stored URL = %q, %v", storedURL, ok)
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithBrowser(
		[]string{"--config", credentialsPath, "whoami"},
		getenv,
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(string) error { return nil },
	)
	if code != 0 {
		t.Fatalf("whoami exit code = %d, stderr = %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "Dispatcher URL: ") {
		t.Fatalf("saved URL prompted again: %q", stderr.String())
	}
}

func TestRunPayoutsSendsSessionCookieAndQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/analytics/payout" || r.URL.Query().Get("days") != "7" {
			t.Errorf("URL = %s, want /api/analytics/payout?days=7", r.URL.String())
		}
		cookie, err := r.Cookie("railway_token")
		if err != nil || cookie.Value != "test-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[],"points":[]}`))
	}))
	defer server.Close()
	credentialsPath := t.TempDir() + "/credentials.json"
	if err := saveStoredSession(credentialsPath, server.URL, storedSession{
		Session: "test-session", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--url", server.URL, "--config", credentialsPath, "payouts", "--days", "7"},
		func(string) string { return "" },
		&stdout,
		&stderr,
	)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if got := stdout.String(); got != "{\n  \"series\": [],\n  \"points\": []\n}\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunUsesEnvironmentAndCompactOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analytics/templates" {
			t.Errorf("path = %s", r.URL.Path)
		}
		cookie, err := r.Cookie("railway_token")
		if err != nil || cookie.Value != "from-env" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		_, _ = w.Write([]byte(`{ "templates": [ ] }`))
	}))
	defer server.Close()

	env := map[string]string{
		"DISPATCHER_URL": server.URL,
	}
	credentialsPath := t.TempDir() + "/credentials.json"
	env["DISPATCHER_CONFIG"] = credentialsPath
	if err := saveStoredSession(credentialsPath, server.URL, storedSession{
		Session: "from-env", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--compact", "templates"},
		func(key string) string { return env[key] },
		&stdout,
		&stderr,
	)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if got := stdout.String(); got != "{\"templates\":[]}\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunHealthDoesNotRequireLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--url", server.URL, "health"},
		func(string) string { return "" },
		&stdout,
		&stderr,
	)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRequiresLoginBeforeProtectedRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	credentialsPath := t.TempDir() + "/credentials.json"
	code := run(
		[]string{"--url", "https://dispatcher.example", "--config", credentialsPath, "summary"},
		func(string) string { return "" },
		&stdout,
		&stderr,
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not logged in") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunSurfacesDispatcherError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"not authenticated"}`))
	}))
	defer server.Close()
	credentialsPath := t.TempDir() + "/credentials.json"
	if err := saveStoredSession(credentialsPath, server.URL, storedSession{
		Session: "expired-server-side", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--url", server.URL, "--config", credentialsPath, "summary"},
		func(string) string { return "" },
		&stdout,
		&stderr,
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "401 Unauthorized: not authenticated") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestParseCommandRejectsInvalidPayoutWindow(t *testing.T) {
	_, err := parseCommand([]string{"payouts", "--days", "0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "between 1 and 365") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunSavesSessionRotatedByDispatcher(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("railway_token")
		if err != nil || cookie.Value != "old-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		// Dispatcher refreshed the Railway grant behind the session.
		http.SetCookie(w, &http.Cookie{
			Name:   "railway_token",
			Value:  "renewed-session",
			Path:   "/",
			MaxAge: int((48 * time.Hour) / time.Second),
		})
		_, _ = w.Write([]byte(`{"templates":[]}`))
	}))
	defer server.Close()
	credentialsPath := t.TempDir() + "/credentials.json"
	if err := saveStoredSession(credentialsPath, server.URL, storedSession{
		Session: "old-session", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--url", server.URL, "--config", credentialsPath, "templates"},
		func(string) string { return "" },
		&stdout,
		&stderr,
	)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	session, ok, err := loadStoredSession(credentialsPath, server.URL, time.Now())
	if err != nil || !ok {
		t.Fatalf("load session: ok = %t, err = %v", ok, err)
	}
	if session.Session != "renewed-session" {
		t.Fatalf("stored session = %+v", session)
	}
	if session.ExpiresAt.Before(time.Now().Add(24 * time.Hour)) {
		t.Fatalf("stored session expires at %s", session.ExpiresAt)
	}
}

func TestParseCommandProjects(t *testing.T) {
	for _, args := range [][]string{
		{"projects", "my template", "--days", "7"},
		{"projects", "--days", "7", "my template"},
	} {
		cmd, err := parseCommand(args, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("parseCommand(%q): %v", args, err)
		}
		if want := "/api/analytics/templates/my%20template/projects?days=7"; cmd.path != want || !cmd.requiresAuth {
			t.Fatalf("parseCommand(%q) = %+v, want path %s", args, cmd, want)
		}
	}
	for _, args := range [][]string{
		{"projects"},
		{"projects", "a", "b"},
		{"projects", "a", "--days", "366"},
	} {
		if _, err := parseCommand(args, &bytes.Buffer{}); err == nil {
			t.Errorf("parseCommand(%q) accepted invalid arguments", args)
		}
	}
}

func TestParseCommandRaw(t *testing.T) {
	cmd, err := parseCommand([]string{"raw", "snapshots", "--template", "dispatcher", "--since", "2026-09-01", "--limit", "50"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/api/raw/snapshots?limit=50&since=2026-09-01&template=dispatcher"; cmd.path != want || !cmd.requiresAuth {
		t.Fatalf("cmd = %+v, want path %s", cmd, want)
	}

	cmd, err = parseCommand([]string{"raw", "payouts"}, &bytes.Buffer{})
	if err != nil || cmd.path != "/api/raw/payouts" {
		t.Fatalf("cmd = %+v, err = %v", cmd, err)
	}

	for _, args := range [][]string{
		{"raw"},
		{"raw", "credentials"},
		{"raw", "payouts", "--template", "dispatcher"},
	} {
		if _, err := parseCommand(args, &bytes.Buffer{}); err == nil {
			t.Errorf("parseCommand(%q) accepted invalid arguments", args)
		}
	}
}

func TestRunLoginWithTokenRedeemsWithoutABrowser(t *testing.T) {
	token, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/cli/redeem" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token != token {
			t.Errorf("redeem body = %+v, %v", body, err)
		}
		_ = json.NewEncoder(w).Encode(storedSession{Session: "minted-session", ExpiresAt: expiresAt})
	}))
	defer server.Close()

	credentialsPath := t.TempDir() + "/credentials.json"
	var stdout, stderr bytes.Buffer
	code := runWithBrowser(
		[]string{"--config", credentialsPath, "--url", server.URL, "login", "--token", token},
		func(string) string { return "" },
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(string) error { t.Error("token login must not open a browser"); return nil },
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	session, ok, err := loadStoredSession(credentialsPath, server.URL, time.Now())
	if err != nil || !ok || session.Session != "minted-session" {
		t.Fatalf("stored session = %+v, ok = %t, err = %v", session, ok, err)
	}
	if storedURL, ok, _ := loadStoredURL(credentialsPath); !ok || storedURL != server.URL {
		t.Fatalf("stored URL = %q, want the instance saved as the default", storedURL)
	}
}

func TestRunLoginRejectsMalformedToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--config", t.TempDir() + "/credentials.json", "--url", "https://dispatcher.example", "login", "--token", "nope"},
		func(string) string { return "" },
		&stdout,
		&stderr,
	)
	if code != 1 || !strings.Contains(stderr.String(), "invalid login token") {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
}
