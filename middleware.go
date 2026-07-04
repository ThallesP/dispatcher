package main

import (
	"context"
	"net/http"
)

const authCookieName = "railway_token"

type userKey struct{}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(authCookieName)
		if err != nil || cookie.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		user, err := getAuthUser(r.Context(), cookie.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	})
}

// authUserFrom returns the user stored by requireAuth; zero value if the
// handler is not wrapped by it.
func authUserFrom(r *http.Request) authUser {
	user, _ := r.Context().Value(userKey{}).(authUser)
	return user
}
