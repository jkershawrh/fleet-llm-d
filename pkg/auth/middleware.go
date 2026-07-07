package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// contextKey is the type used for storing values in request context.
type contextKey string

// ClaimsContextKey is the context key for storing validated claims.
const ClaimsContextKey contextKey = "auth-claims"

// GetClaims retrieves validated claims from the request context.
// Returns nil if no claims are present.
func GetClaims(r *http.Request) *Claims {
	v := r.Context().Value(ClaimsContextKey)
	if v == nil {
		return nil
	}
	claims, ok := v.(*Claims)
	if !ok {
		return nil
	}
	return claims
}

// AuthMiddleware wraps an http.Handler and requires valid bearer tokens.
// Exempt paths (e.g. health/readiness/metrics) bypass authentication.
// When auth is disabled (cfg.Enabled == false), all requests pass through.
func AuthMiddleware(cfg Config, exempt []string, next http.Handler) http.Handler {
	exemptSet := make(map[string]bool, len(exempt))
	for _, p := range exempt {
		exemptSet[p] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If auth is disabled, pass through.
		if !cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		// Check if the path is exempt.
		if exemptSet[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Extract bearer token from Authorization header.
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeAuthError(w, "missing Authorization header")
			log.Printf("fleet.security.auth.failed: remote=%s reason=missing_header path=%s",
				r.RemoteAddr, r.URL.Path)
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeAuthError(w, "invalid Authorization header format, expected 'Bearer <token>'")
			log.Printf("fleet.security.auth.failed: remote=%s reason=invalid_header_format path=%s",
				r.RemoteAddr, r.URL.Path)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Validate the token.
		claims, err := ValidateToken(cfg.Secret, token)
		if err != nil {
			writeAuthError(w, err.Error())
			log.Printf("fleet.security.auth.failed: remote=%s reason=%s path=%s",
				r.RemoteAddr, err.Error(), r.URL.Path)
			return
		}

		// Store claims in context and call next handler.
		ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeAuthError writes a 401 JSON error response.
func writeAuthError(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  "unauthorized",
		"detail": detail,
	})
}
