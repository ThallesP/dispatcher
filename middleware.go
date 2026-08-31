package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

const authCookieName = "railway_token"

type userKey struct{}

// requireAuth validates the caller against Railway on every request. The
// cookie carries the whole OAuth grant, so a login that outlived its access
// token is refreshed here instead of being rejected.
func requireAuth(db *gorm.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(authCookieName)
			if err != nil || cookie.Value == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
				return
			}
			creds, err := gorm.G[RailwayCredentials](db).First(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no client credentials, start at /api/auth/redirect"})
				return
			}
			current, err := decodeSession(creds.ClientSecret, cookie.Value)
			if err != nil {
				rejectSession(w)
				return
			}
			now := time.Now()
			resolved, refreshed, err := resolveSession(r.Context(), db, creds, current, now)
			if err != nil {
				rejectSession(w)
				return
			}
			user, err := getAuthUser(r.Context(), resolved.AccessToken)
			if err != nil {
				rejectSession(w)
				return
			}
			if refreshed {
				value, err := encodeSession(creds.ClientSecret, resolved)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not renew the session"})
					return
				}
				setSessionCookie(w, value, now)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
		})
	}
}

// rejectSession drops a session Railway no longer honours, so the client stops
// replaying it and logs in again.
func rejectSession(w http.ResponseWriter) {
	clearSessionCookie(w)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired session"})
}

// authUserFrom returns the user stored by requireAuth; zero value if the
// handler is not wrapped by it.
func authUserFrom(r *http.Request) authUser {
	user, _ := r.Context().Value(userKey{}).(authUser)
	return user
}

// noStoreAPI keeps every /api response out of shared caches. Behind a CDN the
// responses are per-user (payouts, withdraw settings, the session itself), and
// the OAuth routes answer with redirects that caches would otherwise reuse.
func noStoreAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
