package auth

import (
	"os"
	"strings"
	"time"
)

// Config holds authentication configuration for the fleet controller.
type Config struct {
	Secret   string        // HMAC-SHA256 signing secret
	TokenTTL time.Duration // token lifetime (default 24h)
	Enabled  bool          // whether auth is enforced
}

// ConfigFromEnv creates a Config from environment variables.
// Auth is enabled when FLEET_AUTH_SECRET is set.
// FLEET_AUTH_TTL optionally overrides the default 24h token lifetime
// (parsed via time.ParseDuration, e.g. "1h", "30m").
func ConfigFromEnv() Config {
	secret := os.Getenv("FLEET_AUTH_SECRET")

	// FLEET_AUTH_SECRET_FILE takes precedence (for K8s Secret volume mounts)
	if secretFile := os.Getenv("FLEET_AUTH_SECRET_FILE"); secretFile != "" {
		if data, err := os.ReadFile(secretFile); err == nil {
			secret = strings.TrimSpace(string(data))
		}
	}

	ttl := 24 * time.Hour
	if raw := os.Getenv("FLEET_AUTH_TTL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			ttl = parsed
		}
	}

	return Config{
		Secret:   secret,
		TokenTTL: ttl,
		Enabled:  secret != "",
	}
}
