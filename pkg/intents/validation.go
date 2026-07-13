package intents

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

var supportedIntentTypes = map[IntentType]struct{}{
	IntentPreWarm: {}, IntentScale: {}, IntentShedLoad: {}, IntentAlert: {}, IntentMigrate: {},
	IntentRoute: {}, IntentDeploy: {}, IntentKVTransfer: {}, IntentNoAction: {},
}

// ValidateGovernedIntent enforces the v2 trust envelope. The v1 compatibility
// adapter deliberately does not call this function.
func ValidateGovernedIntent(intent FleetIntent, now time.Time) error {
	if _, ok := supportedIntentTypes[intent.Type]; !ok {
		return fmt.Errorf("unsupported intent type %q", intent.Type)
	}
	if intent.Confidence < 0 || intent.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if strings.TrimSpace(intent.Justification) == "" {
		return fmt.Errorf("justification is required")
	}
	if strings.TrimSpace(intent.IdempotencyKey) == "" {
		return fmt.Errorf("idempotency_key is required")
	}
	if intent.ExpiresAt == nil {
		return fmt.Errorf("expires_at is required")
	}
	if !intent.ExpiresAt.After(now) {
		return fmt.Errorf("intent is expired")
	}
	if strings.TrimSpace(intent.DecisionPackageRef) == "" {
		return fmt.Errorf("decision_package_ref is required")
	}
	if !validSHA256(intent.DecisionPackageDigest) {
		return fmt.Errorf("decision_package_digest must be a 64-character SHA-256 digest")
	}
	if intent.Proposer == nil || strings.TrimSpace(intent.Proposer.Subject) == "" || strings.TrimSpace(intent.Proposer.AuthorityRef) == "" {
		return fmt.Errorf("proposer subject and authority_ref are required")
	}
	if intent.Proposer.ExpiresAt != nil && !intent.Proposer.ExpiresAt.After(now) {
		return fmt.Errorf("proposer authority is expired")
	}
	if strings.TrimSpace(intent.Pool) == "" {
		return fmt.Errorf("pool is required")
	}
	for i, evidence := range intent.Evidence {
		if strings.TrimSpace(evidence.URI) == "" || !validSHA256(evidence.SHA256) {
			return fmt.Errorf("evidence[%d] requires a URI and SHA-256 digest", i)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
