package intents

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

var supportedIntentTypes = map[IntentType]struct{}{
	IntentDeploy: {}, IntentScale: {}, IntentRoute: {}, IntentPreWarm: {},
	IntentShedLoad: {}, IntentMigrate: {}, IntentKVTransfer: {},
}

// ValidateGovernedIntent enforces the v2 trust envelope. The v1 compatibility
// adapter deliberately does not call this function.
func ValidateGovernedIntent(intent FleetIntent, now time.Time) error {
	if _, ok := supportedIntentTypes[intent.Type]; !ok {
		return fmt.Errorf("unsupported intent type %q", intent.Type)
	}
	if math.IsNaN(intent.Confidence) || math.IsInf(intent.Confidence, 0) || intent.Confidence < 0 || intent.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if intent.HorizonSeconds <= 0 {
		return fmt.Errorf("horizon_seconds must be greater than zero")
	}
	if strings.TrimSpace(intent.Justification) == "" {
		return fmt.Errorf("justification is required")
	}
	if intent.StateSnapshot == nil {
		return fmt.Errorf("state_snapshot is required")
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
	if len(intent.Evidence) == 0 {
		return fmt.Errorf("at least one evidence reference is required")
	}
	for i, evidence := range intent.Evidence {
		if strings.TrimSpace(evidence.URI) == "" || !validSHA256(evidence.SHA256) {
			return fmt.Errorf("evidence[%d] requires a URI and SHA-256 digest", i)
		}
	}
	for name, value := range map[string]int{
		"target_replicas":  intent.TargetReplicas,
		"desired_replicas": intent.DesiredReplicas,
		"current_replicas": intent.CurrentReplicas,
		"max_inflight":     intent.MaxInflight,
		"duration_seconds": intent.DurationSeconds,
	} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	if err := validateTargetClusters(intent.TargetClusters); err != nil {
		return err
	}
	return validateActionParameters(intent)
}

func validateActionParameters(intent FleetIntent) error {
	switch intent.Type {
	case IntentDeploy:
		if strings.TrimSpace(intent.Model) == "" {
			return fmt.Errorf("deploy intent requires model")
		}
		if intent.DesiredReplicas <= 0 {
			return fmt.Errorf("deploy intent requires desired_replicas greater than zero")
		}
	case IntentScale:
		if intent.DesiredReplicas <= 0 {
			return fmt.Errorf("scale intent requires desired_replicas greater than zero")
		}
	case IntentRoute:
		if len(intent.TargetClusters) == 0 {
			return fmt.Errorf("route intent requires at least one target_cluster")
		}
	case IntentPreWarm:
		if intent.TargetReplicas <= 0 {
			return fmt.Errorf("pre_warm intent requires target_replicas greater than zero")
		}
	case IntentShedLoad:
		if intent.MaxInflight <= 0 {
			return fmt.Errorf("shed_load intent requires max_inflight greater than zero")
		}
		if intent.DurationSeconds <= 0 {
			return fmt.Errorf("shed_load intent requires duration_seconds greater than zero")
		}
	case IntentMigrate:
		targetPool, err := requiredSnapshotParameter(intent.StateSnapshot, "target_pool")
		if err != nil {
			return fmt.Errorf("migrate intent: %w", err)
		}
		if targetPool == strings.TrimSpace(intent.Pool) {
			return fmt.Errorf("migrate intent target_pool must differ from pool")
		}
	case IntentKVTransfer:
		sourceCluster, err := requiredSnapshotParameter(intent.StateSnapshot, "source_cluster")
		if err != nil {
			return fmt.Errorf("kv_transfer intent: %w", err)
		}
		targetCluster, err := requiredSnapshotParameter(intent.StateSnapshot, "target_cluster")
		if err != nil {
			return fmt.Errorf("kv_transfer intent: %w", err)
		}
		if sourceCluster == targetCluster {
			return fmt.Errorf("kv_transfer intent source_cluster and target_cluster must differ")
		}
	}
	return nil
}

func validateTargetClusters(clusters []string) error {
	for i, cluster := range clusters {
		if strings.TrimSpace(cluster) == "" {
			return fmt.Errorf("target_clusters[%d] must not be empty", i)
		}
	}
	return nil
}

func requiredSnapshotParameter(snapshot map[string]interface{}, name string) (string, error) {
	rawParameters, ok := snapshot["parameters"]
	if !ok {
		return "", fmt.Errorf("state_snapshot.parameters.%s is required", name)
	}
	parameters, ok := rawParameters.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("state_snapshot.parameters must be an object")
	}
	rawValue, ok := parameters[name]
	if !ok {
		return "", fmt.Errorf("state_snapshot.parameters.%s is required", name)
	}
	value, ok := rawValue.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("state_snapshot.parameters.%s must be a non-empty string", name)
	}
	return strings.TrimSpace(value), nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
