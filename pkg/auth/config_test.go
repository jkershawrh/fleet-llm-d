package auth

import (
	"testing"
	"time"
)

func TestConfigFromEnv_NoVarsSet(t *testing.T) {
	// Ensure the relevant env vars are unset.
	t.Setenv("FLEET_AUTH_SECRET", "")
	t.Setenv("FLEET_AUTH_TTL", "")

	cfg := ConfigFromEnv()

	if cfg.Enabled {
		t.Error("expected Enabled to be false when FLEET_AUTH_SECRET is empty")
	}
	if cfg.Secret != "" {
		t.Errorf("expected empty Secret, got %q", cfg.Secret)
	}
	if cfg.TokenTTL != 24*time.Hour {
		t.Errorf("expected default TokenTTL of 24h, got %v", cfg.TokenTTL)
	}
}

func TestConfigFromEnv_SecretSet(t *testing.T) {
	t.Setenv("FLEET_AUTH_SECRET", "my-signing-key")
	t.Setenv("FLEET_AUTH_TTL", "")

	cfg := ConfigFromEnv()

	if !cfg.Enabled {
		t.Error("expected Enabled to be true when FLEET_AUTH_SECRET is set")
	}
	if cfg.Secret != "my-signing-key" {
		t.Errorf("expected Secret %q, got %q", "my-signing-key", cfg.Secret)
	}
	if cfg.TokenTTL != 24*time.Hour {
		t.Errorf("expected default TokenTTL of 24h, got %v", cfg.TokenTTL)
	}
}

func TestConfigFromEnv_TTLSet(t *testing.T) {
	t.Setenv("FLEET_AUTH_SECRET", "key")
	t.Setenv("FLEET_AUTH_TTL", "1h30m")

	cfg := ConfigFromEnv()

	expected := 90 * time.Minute
	if cfg.TokenTTL != expected {
		t.Errorf("expected TokenTTL %v, got %v", expected, cfg.TokenTTL)
	}
}

func TestConfigFromEnv_TTLInvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("FLEET_AUTH_SECRET", "key")
	t.Setenv("FLEET_AUTH_TTL", "not-a-duration")

	cfg := ConfigFromEnv()

	if cfg.TokenTTL != 24*time.Hour {
		t.Errorf("expected default TokenTTL of 24h for invalid TTL, got %v", cfg.TokenTTL)
	}
}

func TestConfigFromEnv_EnabledIsFalseForEmptySecret(t *testing.T) {
	t.Setenv("FLEET_AUTH_SECRET", "")

	cfg := ConfigFromEnv()

	if cfg.Enabled {
		t.Error("expected Enabled=false for empty secret")
	}
}
