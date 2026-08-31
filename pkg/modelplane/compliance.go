package modelplane

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

// ComplianceBridge records ModelPlane resource events to the standalone
// immutable ledger.
type ComplianceBridge struct {
	recorder *ledger.FleetRecorder
}

// NewComplianceBridge creates a new ComplianceBridge.
func NewComplianceBridge(recorder *ledger.FleetRecorder) *ComplianceBridge {
	return &ComplianceBridge{recorder: recorder}
}

// inputHash computes a SHA-256 hex digest of the given bytes.
func inputHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}

// RecordClusterProvisioned records an InferenceCluster provisioning event.
func (b *ComplianceBridge) RecordClusterProvisioned(ctx context.Context, cluster InferenceCluster) (*ledger.LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"cluster":  cluster.Name,
		"region":   cluster.Region,
		"provider": cluster.Provider,
		"nodes":    cluster.Status.Nodes,
		"phase":    cluster.Status.Phase,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cluster event: %w", err)
	}
	return b.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        "modelplane.cluster.provisioned",
		Content:     content,
		ContentType: "application/json",
		InputHash:   inputHash(content),
	})
}

// RecordDeploymentCreated records a ModelDeployment creation event.
func (b *ComplianceBridge) RecordDeploymentCreated(ctx context.Context, deployment ModelDeployment) (*ledger.LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"deployment": deployment.Name,
		"namespace":  deployment.Namespace,
		"model":      deployment.Model,
		"engine":     deployment.Engine,
		"replicas":   deployment.Replicas,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal deployment event: %w", err)
	}
	return b.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        "modelplane.deployment.created",
		Content:     content,
		ContentType: "application/json",
		InputHash:   inputHash(content),
	})
}

// RecordDeploymentScaled records a ModelDeployment scaling event.
func (b *ComplianceBridge) RecordDeploymentScaled(ctx context.Context, deployment ModelDeployment, oldReplicas, newReplicas int) (*ledger.LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"deployment":   deployment.Name,
		"namespace":    deployment.Namespace,
		"model":        deployment.Model,
		"old_replicas": oldReplicas,
		"new_replicas": newReplicas,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal scaling event: %w", err)
	}
	return b.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        "modelplane.deployment.scaled",
		Content:     content,
		ContentType: "application/json",
		InputHash:   inputHash(content),
	})
}

// RecordEndpointReady records a ModelEndpoint readiness event.
func (b *ComplianceBridge) RecordEndpointReady(ctx context.Context, endpoint ModelEndpoint) (*ledger.LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"endpoint":  endpoint.Name,
		"namespace": endpoint.Namespace,
		"model":     endpoint.Model,
		"cluster":   endpoint.Cluster,
		"url":       endpoint.URL,
		"ready":     endpoint.Ready,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal endpoint event: %w", err)
	}
	return b.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        "modelplane.endpoint.ready",
		Content:     content,
		ContentType: "application/json",
		InputHash:   inputHash(content),
	})
}

// RecordServiceWeightChanged records a ModelService weight change event.
func (b *ComplianceBridge) RecordServiceWeightChanged(ctx context.Context, service ModelService) (*ledger.LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"service":   service.Name,
		"namespace": service.Namespace,
		"endpoints": service.Endpoints,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal service weight event: %w", err)
	}
	return b.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        "modelplane.service.weight_changed",
		Content:     content,
		ContentType: "application/json",
		InputHash:   inputHash(content),
	})
}

// RecordCacheHydrated records a ModelCache hydration event.
func (b *ComplianceBridge) RecordCacheHydrated(ctx context.Context, cache ModelCache) (*ledger.LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"cache":   cache.Name,
		"model":   cache.Model,
		"cluster": cache.Cluster,
		"ready":   cache.Ready,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cache event: %w", err)
	}
	return b.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        "modelplane.cache.hydrated",
		Content:     content,
		ContentType: "application/json",
		InputHash:   inputHash(content),
	})
}
