package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// dummyHandler is a simple handler that returns 200 with a JSON body.
func dummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

func TestMiddleware_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	handler := AuthMiddleware(cfg, nil, dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 when auth disabled, got %d", rr.Code)
	}
}

func TestMiddleware_ExemptPath(t *testing.T) {
	cfg := Config{
		Secret:  "test-secret",
		Enabled: true,
	}
	exempt := []string{"/healthz", "/readyz", "/metrics"}
	handler := AuthMiddleware(cfg, exempt, dummyHandler())

	for _, path := range exempt {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("path %s: expected 200 for exempt path, got %d", path, rr.Code)
		}
	}
}

func TestMiddleware_ValidToken(t *testing.T) {
	secret := "test-secret"
	cfg := Config{
		Secret:  secret,
		Enabled: true,
	}
	exempt := []string{"/healthz"}

	// Generate a valid token.
	claims := Claims{
		Subject:   "test-user",
		Role:      RoleAdmin,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	token, err := GenerateToken(secret, claims)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	// Create a handler that checks claims are in context.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := GetClaims(r)
		if c == nil {
			t.Error("expected claims in context, got nil")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if c.Subject != "test-user" {
			t.Errorf("expected subject 'test-user', got %q", c.Subject)
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware(cfg, exempt, inner)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestMiddleware_MissingHeader(t *testing.T) {
	cfg := Config{
		Secret:  "test-secret",
		Enabled: true,
	}
	handler := AuthMiddleware(cfg, nil, dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "unauthorized" {
		t.Errorf("expected error 'unauthorized', got %q", resp["error"])
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	cfg := Config{
		Secret:  "test-secret",
		Enabled: true,
	}
	handler := AuthMiddleware(cfg, nil, dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	cfg := Config{
		Secret:  secret,
		Enabled: true,
	}

	// Generate an expired token.
	claims := Claims{
		Subject:   "expired-user",
		Role:      RoleViewer,
		IssuedAt:  time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	token, err := GenerateToken(secret, claims)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	handler := AuthMiddleware(cfg, nil, dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "unauthorized" {
		t.Errorf("expected error 'unauthorized', got %q", resp["error"])
	}
}
