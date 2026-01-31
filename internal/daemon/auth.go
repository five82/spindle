package daemon

import (
	"net/http"
	"strings"
)

// authMiddleware returns a middleware that validates bearer tokens.
// If token is empty, no authentication is required and all requests pass through.
// Otherwise, requests must include "Authorization: Bearer <token>" header.
func authMiddleware(token string, next http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if strings.TrimPrefix(auth, "Bearer ") != token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
