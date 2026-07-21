package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
)

// handleRefreshToken issues a new token with an extended expiry for
// an authenticated caller. The original bearer token must still be valid.
func (fc *FleetController) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "valid token required for refresh")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if len(authHeader) < 8 || !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	newToken, err := auth.RefreshToken(fc.AuthSecret, authHeader[7:], 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": newToken})
}
