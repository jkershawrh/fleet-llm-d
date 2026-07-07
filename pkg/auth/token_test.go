package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestGenerateToken_ValidClaims(t *testing.T) {
	claims := Claims{
		Subject:   "test-user",
		Role:      RoleAdmin,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	token, err := GenerateToken("test-secret", claims)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Token should have two parts separated by a dot.
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("expected token with two parts, got %d", len(parts))
	}

	// Both parts should be valid base64.
	if _, err := base64.RawURLEncoding.DecodeString(parts[0]); err != nil {
		t.Fatalf("claims part is not valid base64: %v", err)
	}
	if _, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
		t.Fatalf("signature part is not valid base64: %v", err)
	}
}

func TestValidateToken_ValidToken(t *testing.T) {
	secret := "my-secret-key"
	original := Claims{
		Subject:   "svc-account",
		Role:      RoleOperator,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	token, err := GenerateToken(secret, original)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got, err := ValidateToken(secret, token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	if got.Subject != original.Subject {
		t.Errorf("subject: got %q, want %q", got.Subject, original.Subject)
	}
	if got.Role != original.Role {
		t.Errorf("role: got %q, want %q", got.Role, original.Role)
	}
}

func TestValidateToken_ExpiredToken(t *testing.T) {
	secret := "my-secret-key"
	claims := Claims{
		Subject:   "old-user",
		Role:      RoleViewer,
		IssuedAt:  time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour), // expired 1 hour ago
	}

	token, err := GenerateToken(secret, claims)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	_, err = ValidateToken(secret, token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected 'expired' in error, got: %v", err)
	}
}

func TestValidateToken_TamperedToken(t *testing.T) {
	secret := "my-secret-key"
	claims := Claims{
		Subject:   "honest-user",
		Role:      RoleViewer,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	token, err := GenerateToken(secret, claims)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Tamper with the claims portion (replace first character).
	parts := strings.SplitN(token, ".", 2)
	tampered := "A" + parts[0][1:] + "." + parts[1]

	_, err = ValidateToken(secret, tampered)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("expected 'signature' in error, got: %v", err)
	}
}

func TestValidateToken_InvalidFormat(t *testing.T) {
	_, err := ValidateToken("secret", "not-a-valid-token")
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token format") {
		t.Errorf("expected 'invalid token format' in error, got: %v", err)
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	claims := Claims{
		Subject:   "test-user",
		Role:      RoleAdmin,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	token, err := GenerateToken("correct-secret", claims)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	_, err = ValidateToken("wrong-secret", token)
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("expected 'signature' in error, got: %v", err)
	}
}
