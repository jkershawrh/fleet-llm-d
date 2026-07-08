package intents

import (
	"context"
	"testing"
)

func TestEvaluate_AcceptsHighConfidence(t *testing.T) {
	intent := FleetIntent{
		ID:             "test-1",
		Type:           IntentPreWarm,
		Confidence:     0.85,
		HorizonSeconds: 1800,
		Justification:  "Event pre-warming",
		Model:          "granite-2b",
		TargetReplicas: 4,
	}

	resp := Evaluate(context.Background(), intent, DefaultPolicyConfig())
	if resp.Status != StatusExecuted {
		t.Errorf("expected executed, got %s: %s", resp.Status, resp.Reason)
	}
}

func TestEvaluate_DefersLowConfidence(t *testing.T) {
	intent := FleetIntent{
		ID:            "test-2",
		Type:          IntentScale,
		Confidence:    0.3,
		Justification: "Weak signal",
	}

	resp := Evaluate(context.Background(), intent, DefaultPolicyConfig())
	if resp.Status != StatusDeferred {
		t.Errorf("expected deferred, got %s", resp.Status)
	}
}

func TestEvaluate_RefusesExcessiveReplicas(t *testing.T) {
	intent := FleetIntent{
		ID:             "test-3",
		Type:           IntentPreWarm,
		Confidence:     0.9,
		TargetReplicas: 20,
		Justification:  "Aggressive scale",
	}

	resp := Evaluate(context.Background(), intent, DefaultPolicyConfig())
	if resp.Status != StatusRefused {
		t.Errorf("expected refused, got %s", resp.Status)
	}
}

func TestEvaluate_DefersCriticalAlert(t *testing.T) {
	intent := FleetIntent{
		ID:         "test-4",
		Type:       IntentAlert,
		Confidence: 0.95,
		Severity:   "critical",
		Message:    "SLO breach imminent",
	}

	policy := DefaultPolicyConfig()
	policy.RequireHumanGate = true

	resp := Evaluate(context.Background(), intent, policy)
	if resp.Status != StatusDeferred {
		t.Errorf("expected deferred for critical with human gate, got %s", resp.Status)
	}
}

func TestEvaluate_ExecutesCriticalWithoutGate(t *testing.T) {
	intent := FleetIntent{
		ID:         "test-5",
		Type:       IntentAlert,
		Confidence: 0.95,
		Severity:   "critical",
		Message:    "SLO breach imminent",
	}

	policy := DefaultPolicyConfig()
	policy.RequireHumanGate = false

	resp := Evaluate(context.Background(), intent, policy)
	if resp.Status != StatusExecuted {
		t.Errorf("expected executed without human gate, got %s", resp.Status)
	}
}
