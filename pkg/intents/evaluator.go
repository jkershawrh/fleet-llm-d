package intents

import (
	"context"
	"fmt"
	"log/slog"
)

// PolicyConfig defines thresholds for intent evaluation.
type PolicyConfig struct {
	MinConfidence       float64
	MaxReplicasPerModel int
	RequireHumanGate    bool // for critical actions
}

func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		MinConfidence:       0.5,
		MaxReplicasPerModel: 8,
		RequireHumanGate:    true,
	}
}

// Evaluate checks an intent against policy and returns a response.
func Evaluate(ctx context.Context, intent FleetIntent, policy PolicyConfig) IntentResponse {
	// Check confidence threshold
	if intent.Confidence < policy.MinConfidence {
		return IntentResponse{
			IntentID: intent.ID,
			Status:   StatusDeferred,
			Reason:   fmt.Sprintf("confidence %.2f below threshold %.2f", intent.Confidence, policy.MinConfidence),
		}
	}

	// Check replica limits
	if intent.TargetReplicas > policy.MaxReplicasPerModel || intent.DesiredReplicas > policy.MaxReplicasPerModel {
		replicas := intent.TargetReplicas
		if intent.DesiredReplicas > replicas {
			replicas = intent.DesiredReplicas
		}
		return IntentResponse{
			IntentID: intent.ID,
			Status:   StatusRefused,
			Reason:   fmt.Sprintf("requested %d replicas exceeds max %d", replicas, policy.MaxReplicasPerModel),
		}
	}

	// Human gate for critical alerts
	if intent.Type == IntentAlert && intent.Severity == "critical" && policy.RequireHumanGate {
		return IntentResponse{
			IntentID: intent.ID,
			Status:   StatusDeferred,
			Reason:   "critical alert requires human approval",
		}
	}

	slog.Info("intent accepted", "id", intent.ID, "type", intent.Type, "justification", intent.Justification)

	return IntentResponse{
		IntentID: intent.ID,
		Status:   StatusAccepted,
		Reason:   "policy checks passed; awaiting governance and observed actuation",
	}
}
