package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gorm.io/gorm"
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
		if oauthErr := q.Get("error"); oauthErr != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":       oauthErr,
				"description": q.Get("error_description"),
			})
			return
		}
		code := q.Get("code")
		if code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing code parameter"})
			return
		}

		creds, err := gorm.G[RailwayCredentials](db).First(r.Context())
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no client credentials, start at /api/auth/redirect"})
			return
		}

		tok, err := exchangeAuthCode(r.Context(), creds, code)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}

		if _, err := getAuthUser(r.Context(), tok.AccessToken); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}

		if err := saveToken(r.Context(), db, creds.ID, tok); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     authCookieName,
			Value:    tok.AccessToken,
			Path:     "/",
			MaxAge:   int(tok.ExpiresIn),
			HttpOnly: true,
			Secure:   strings.HasPrefix(os.Getenv("CALLBACK_URL"), "https://"),
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func handleAuthRedirect(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		creds, err := gorm.G[RailwayCredentials](db).First(r.Context())

		if err != nil {
			creds, err = createRailwayCredentials()
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			if err := gorm.G[RailwayCredentials](db).Create(r.Context(), &creds); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}

		u := url.URL{
			Scheme: "https",
			Host:   "backboard.railway.com",
			Path:   "/oauth/auth",
			RawQuery: url.Values{
				"response_type": {"code"},
				"client_id":     {creds.ClientID},
				"redirect_uri":  {os.Getenv("CALLBACK_URL")},
				// offline_access + prompt=consent is what yields a refresh
				// token (docs: both are required — auto-approved logins skip
				// consent and get none). Without it background collection
				// dies when the access token expires after an hour.
				"scope":  {"openid profile email offline_access workspace:admin"},
				"prompt": {"consent"},
			}.Encode(),
		}
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authUserFrom(r))
}

func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   strings.HasPrefix(os.Getenv("CALLBACK_URL"), "https://"),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
