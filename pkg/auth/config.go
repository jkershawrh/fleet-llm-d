package auth

import (
	"os"
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
func ConfigFromEnv() Config {
	secret := os.Getenv("FLEET_AUTH_SECRET")
	return Config{
		Secret:   secret,
		TokenTTL: 24 * time.Hour,
		Enabled:  secret != "",
	}
}
