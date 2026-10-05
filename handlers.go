package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	browserOAuthStateCookie = "railway_oauth_state"
	browserOAuthStateTTL    = 10 * time.Minute
)

func handleHealth(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sqlDB, err := db.DB()
		if err == nil {
			err = sqlDB.PingContext(r.Context())
		}
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handleAuthCallback(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		if code == "" && q.Get("error") == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing code parameter"})
			return
		}

		creds, err := gorm.G[RailwayCredentials](db).First(r.Context())
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no client credentials, start at /api/auth/redirect"})
			return
		}
		var cliState *cliOAuthState
		stateValue := q.Get("state")
		// issueCLI marks a round trip the dashboard started to mint a CLI
		// login; it is a browser flow, so it shares the state-cookie check.
		issueCLI := false
		if strings.HasPrefix(stateValue, cliOAuthStatePrefix) {
			state, err := decodeCLIState(creds.ClientSecret, stateValue, time.Now())
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired CLI auth state"})
				return
			}
			cliState = &state
		} else if err := validateBrowserOAuthState(w, r); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		} else {
			issueCLI = strings.HasPrefix(stateValue, cliIssueStatePrefix)
		}
		if oauthErr := q.Get("error"); oauthErr != "" {
			message := q.Get("error_description")
			if message == "" {
				message = "Railway login was cancelled"
			}
			writeRailwayAuthFailure(w, cliState, &railwayAuthError{
				Status: http.StatusBadRequest, Err: errors.New(oauthErr), CLIMessage: message,
			})
			return
		}

		tok, authErr := completeRailwayAuth(r.Context(), db, creds, code)
		if authErr != nil {
			writeRailwayAuthFailure(w, cliState, authErr)
			return
		}
		now := time.Now()
		value, err := encodeSession(creds.ClientSecret, newSession(tok, now))
		if err != nil {
			writeRailwayAuthFailure(w, cliState, &railwayAuthError{
				Status: http.StatusInternalServerError, Err: err, CLIMessage: "Dispatcher could not issue a session",
			})
			return
		}
		if issueCLI {
			// The fresh grant belongs to the CLI alone: Railway rotates refresh
			// tokens, so the browser keeps its own session untouched.
			token, expiresAt, err := cliAuthHandoffs.issue(value, now.Add(sessionCookieMaxAge), now)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			setCLILoginTokenCookie(w, token, expiresAt)
			http.Redirect(w, r, "/?cli=ready", http.StatusFound)
			return
		}
		if cliState != nil {
			// The grant refreshes itself, so the CLI only has to stop trusting
			// the session once the cookie it was handed would have aged out.
			if !completeCLIAuth(*cliState, value, now.Add(sessionCookieMaxAge), "") {
				writeCLIAuthPage(w, "CLI login expired", "Return to the terminal and start login again.")
				return
			}
			writeCLIAuthPage(w, "CLI login complete", "You can close this window and return to the terminal.")
			return
		}

		setSessionCookie(w, value, now)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func handleAuthRedirect(db *gorm.DB) http.HandlerFunc {
	return startBrowserOAuth(db, "")
}

// handleCLIIssueRedirect starts the round trip behind the dashboard's
// "Generate login command": a Railway consent that mints a grant for the CLI,
// which the callback parks behind a one-time token.
func handleCLIIssueRedirect(db *gorm.DB) http.HandlerFunc {
	return startBrowserOAuth(db, cliIssueStatePrefix)
}

// startBrowserOAuth sends the browser to Railway with a state bound to a
// cookie. statePrefix tells the callback what the round trip is for.
func startBrowserOAuth(db *gorm.DB, statePrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		creds, err := loadOrCreateRailwayCredentials(r.Context(), db)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		token, err := randomURLToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start Railway login"})
			return
		}
		state := statePrefix + token
		setBrowserOAuthStateCookie(w, state, int(browserOAuthStateTTL/time.Second))
		http.Redirect(w, r, railwayAuthorizationURL(creds, state), http.StatusFound)
	}
}

type railwayAuthError struct {
	Status     int
	Err        error
	CLIMessage string
}

func (e *railwayAuthError) Error() string { return e.Err.Error() }

// completeRailwayAuth is the single OAuth completion path for both browser and
// CLI logins: exchange the code, enforce workspace access, and persist the
// refreshable Railway credentials used by the rest of Dispatcher.
func completeRailwayAuth(ctx context.Context, db *gorm.DB, creds RailwayCredentials, code string) (tokenResponse, *railwayAuthError) {
	tok, err := exchangeAuthCode(ctx, creds, code)
	if err != nil {
		return tokenResponse{}, &railwayAuthError{
			Status: http.StatusBadGateway, Err: err, CLIMessage: "Railway login failed",
		}
	}
	if _, err := getAuthUser(ctx, tok.AccessToken); err != nil {
		return tokenResponse{}, &railwayAuthError{
			Status: http.StatusForbidden, Err: err, CLIMessage: "the Railway user cannot access this workspace",
		}
	}
	if err := saveToken(ctx, db, creds.ID, tok); err != nil {
		return tokenResponse{}, &railwayAuthError{
			Status: http.StatusInternalServerError, Err: err, CLIMessage: "Dispatcher could not save the Railway session",
		}
	}
	return tok, nil
}

func writeRailwayAuthFailure(w http.ResponseWriter, cliState *cliOAuthState, failure *railwayAuthError) {
	if cliState == nil {
		writeJSON(w, failure.Status, map[string]string{"error": failure.Error()})
		return
	}
	completeCLIAuth(*cliState, "", time.Time{}, failure.CLIMessage)
	writeCLIAuthPage(w, "CLI login failed", failure.CLIMessage+". Return to the terminal and try again.")
}

func loadOrCreateRailwayCredentials(ctx context.Context, db *gorm.DB) (RailwayCredentials, error) {
	creds, err := gorm.G[RailwayCredentials](db).First(ctx)
	if err == nil {
		return creds, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return RailwayCredentials{}, fmt.Errorf("load Railway OAuth credentials: %w", err)
	}
	creds, err = createRailwayCredentials()
	if err != nil {
		return RailwayCredentials{}, err
	}
	if err := gorm.G[RailwayCredentials](db).Create(ctx, &creds); err != nil {
		return RailwayCredentials{}, fmt.Errorf("save Railway OAuth credentials: %w", err)
	}
	return creds, nil
}

func railwayAuthorizationURL(creds RailwayCredentials, state string) string {
	values := url.Values{
		"response_type": {"code"},
		"client_id":     {creds.ClientID},
		"redirect_uri":  {os.Getenv("CALLBACK_URL")},
		// offline_access + prompt=consent is what yields a refresh token.
		"scope":  {"openid profile email offline_access workspace:admin"},
		"prompt": {"consent"},
	}
	if state != "" {
		values.Set("state", state)
	}
	return (&url.URL{
		Scheme:   "https",
		Host:     "backboard.railway.com",
		Path:     "/oauth/auth",
		RawQuery: values.Encode(),
	}).String()
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authUserFrom(r))
}

func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func validateBrowserOAuthState(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(browserOAuthStateCookie)
	setBrowserOAuthStateCookie(w, "", -1)
	state := r.URL.Query().Get("state")
	if err != nil || cookie.Value == "" || state == "" {
		return errors.New("invalid or expired OAuth state")
	}
	want := sha256.Sum256([]byte(cookie.Value))
	got := sha256.Sum256([]byte(state))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return errors.New("invalid or expired OAuth state")
	}
	return nil
}

func setBrowserOAuthStateCookie(w http.ResponseWriter, value string, maxAge int) {
	cookie := &http.Cookie{
		Name:     browserOAuthStateCookie,
		Value:    value,
		Path:     "/api/auth/callback",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secureAuthCookies(),
		SameSite: http.SameSiteLaxMode,
	}
	if maxAge < 0 {
		cookie.Expires = time.Unix(1, 0)
	} else {
		cookie.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	}
	http.SetCookie(w, cookie)
}

func secureAuthCookies() bool {
	return strings.HasPrefix(os.Getenv("CALLBACK_URL"), "https://")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
