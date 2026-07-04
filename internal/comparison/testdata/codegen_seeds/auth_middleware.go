package middleware

import (
	"net/http"
	"strings"
)

// secretKey is the shared secret for validating bearer tokens.
var secretKey = "default-secret"

// SetSecretKey configures the bearer token secret.
func SetSecretKey(key string) {
	secretKey = key
}

// AuthMiddleware validates bearer tokens from the Authorization header.
// If the token is missing or invalid, it returns 401 Unauthorized.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "invalid authorization format", http.StatusUnauthorized)
			return
		}

		token := parts[1]
		if token != secretKey {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
