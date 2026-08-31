package auth

import (
	"fmt"
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

// MinSecretLength is the shortest HMAC signing secret accepted. Tokens are
// signed with HMAC-SHA256, so a secret shorter than the 32-byte output adds
// no strength and usually indicates a placeholder was left in place.
const MinSecretLength = 32

// ConfigFromEnv creates a Config from environment variables.
// Auth is enabled when FLEET_AUTH_SECRET is set.
// FLEET_AUTH_TTL optionally overrides the default 24h token lifetime
// (parsed via time.ParseDuration, e.g. "1h", "30m").
//
// It returns an error rather than silently disabling authentication when the
// operator's intent to enable it is clear but the configuration is unusable —
// an unreadable secret file, or a secret too short to be meaningful. Failing
// to start is the correct outcome: a controller that serves traffic with
// auth_enabled=false after a mis-mounted Secret is the worst case.
func ConfigFromEnv() (Config, error) {
	secret := os.Getenv("FLEET_AUTH_SECRET")

	// FLEET_AUTH_SECRET_FILE takes precedence (for K8s Secret volume mounts).
	if secretFile := os.Getenv("FLEET_AUTH_SECRET_FILE"); secretFile != "" {
		// #nosec G304 G703 -- path comes from FLEET_AUTH_SECRET_FILE, which only
		// the operator sets; it is deployment configuration, not request input.
		data, err := os.ReadFile(secretFile)
		if err != nil {
			return Config{}, fmt.Errorf("FLEET_AUTH_SECRET_FILE %q is set but unreadable: %w", secretFile, err)
		}
		secret = strings.TrimSpace(string(data))
		if secret == "" {
			return Config{}, fmt.Errorf("FLEET_AUTH_SECRET_FILE %q is empty", secretFile)
		}
	}

	if secret != "" && len(secret) < MinSecretLength {
		return Config{}, fmt.Errorf("auth secret is %d bytes; at least %d are required", len(secret), MinSecretLength)
	}

	ttl := 24 * time.Hour
	if raw := os.Getenv("FLEET_AUTH_TTL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FLEET_AUTH_TTL %q is not a valid duration: %w", raw, err)
		}
		ttl = parsed
	}

	return Config{
		Secret:   secret,
		TokenTTL: ttl,
		Enabled:  secret != "",
	}, nil
}
