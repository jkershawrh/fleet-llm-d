//go:build security

package security

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
)

// newTestServer creates an httptest.Server with auth middleware wrapping
// a simple mux that mirrors the fleet-controller API shape.
func newTestServer(secret string) *httptest.Server {
	cfg := auth.Config{
		Secret:   secret,
		TokenTTL: 24 * time.Hour,
		Enabled:  secret != "",
	}
	exempt := []string{"/healthz", "/readyz", "/metrics"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/clusters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]string{})
	})
	mux.HandleFunc("DELETE /api/v1/clusters/{id}", func(w http.ResponseWriter, r *http.Request) {
		// Check RBAC.
		claims := auth.GetClaims(r)
		if claims != nil && !auth.CheckPermission(claims.Role, r.Method) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":  "forbidden",
				"detail": fmt.Sprintf("role %q cannot perform %s", claims.Role, r.Method),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	})

	handler := auth.AuthMiddleware(cfg, exempt, mux)
	return httptest.NewServer(handler)
}

func generateTestToken(secret, subject, role string, ttl time.Duration) string {
	claims := auth.Claims{
		Subject:   subject,
		Role:      role,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}
	token, err := auth.GenerateToken(secret, claims)
	if err != nil {
		panic(fmt.Sprintf("failed to generate test token: %v", err))
	}
	return token
}

func TestUnauthenticatedRequestReturns401(t *testing.T) {
	secret := "integration-test-secret"
	ts := newTestServer(secret)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/clusters")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthenticatedRequestReturns200(t *testing.T) {
	secret := "integration-test-secret"
	ts := newTestServer(secret)
	defer ts.Close()

	token := generateTestToken(secret, "admin-user", auth.RoleAdmin, 1*time.Hour)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestExpiredTokenReturns401(t *testing.T) {
	secret := "integration-test-secret"
	ts := newTestServer(secret)
	defer ts.Close()

	// Generate a token that expired 1 hour ago.
	token := generateTestToken(secret, "old-user", auth.RoleAdmin, -1*time.Hour)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestViewerCannotDelete(t *testing.T) {
	secret := "integration-test-secret"
	ts := newTestServer(secret)
	defer ts.Close()

	token := generateTestToken(secret, "viewer-user", auth.RoleViewer, 1*time.Hour)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/clusters/test-cluster", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestHealthProbeBypassesAuth(t *testing.T) {
	secret := "integration-test-secret"
	ts := newTestServer(secret)
	defer ts.Close()

	// No Authorization header, but health probe should pass.
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
