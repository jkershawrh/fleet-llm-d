package intents

import (
	"strings"
	"testing"
	"time"
)

func governedTestIntent(now time.Time) FleetIntent {
	expires := now.Add(time.Hour)
	authorityExpires := now.Add(30 * time.Minute)
	return FleetIntent{
		Type: IntentScale, Confidence: 0.9, Justification: "forecast capacity shortfall",
		IdempotencyKey: "decision-1-scale", ExpiresAt: &expires, Pool: "granite-prod",
		DecisionPackageRef: "oci://decisions/decision-1", DecisionPackageDigest: strings.Repeat("a", 64),
		Proposer: &ProposerAuthority{Subject: "spiffe://example/gcl", AuthorityRef: "authority/1", ExpiresAt: &authorityExpires},
		Evidence: []EvidenceDigest{{URI: "oci://evidence/forecast-1", SHA256: strings.Repeat("b", 64)}},
	}
}

func TestValidateGovernedIntent(t *testing.T) {
	now := time.Now().UTC()
	if err := ValidateGovernedIntent(governedTestIntent(now), now); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGovernedIntentRejectsMissingTrustEnvelope(t *testing.T) {
	err := ValidateGovernedIntent(FleetIntent{Type: IntentScale, Confidence: 0.9, Justification: "scale", Pool: "p"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "idempotency_key") {
		t.Fatalf("expected trust-envelope validation error, got %v", err)
	}
}
