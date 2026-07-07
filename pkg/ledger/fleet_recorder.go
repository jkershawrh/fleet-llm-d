package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// FleetRecorder provides typed methods for recording specific fleet decisions
// to the ARE immutable ledger.
type FleetRecorder struct {
	client   LedgerClient
	agentID  string
	sourceID string
}

// NewFleetRecorder creates a new FleetRecorder.
func NewFleetRecorder(client LedgerClient, agentID, sourceID string) *FleetRecorder {
	return &FleetRecorder{
		client:   client,
		agentID:  agentID,
		sourceID: sourceID,
	}
}

// computeInputHash returns the hex-encoded SHA-256 hash of the given data.
func computeInputHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}

// RecordDecision records a generic fleet decision to the ARE immutable ledger.
func (r *FleetRecorder) RecordDecision(ctx context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	if decision.AgentID == "" {
		decision.AgentID = r.agentID
	}
	if decision.SourceID == "" {
		decision.SourceID = r.sourceID
	}
	return r.client.RecordDecision(ctx, decision)
}

// RecordPlacement records a model placement decision.
func (r *FleetRecorder) RecordPlacement(ctx context.Context, model, cluster string, replicas int, gpuType, reason string) (*LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"model":    model,
		"cluster":  cluster,
		"replicas": replicas,
		"gpu_type": gpuType,
		"reason":   reason,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal placement: %w", err)
	}
	return r.client.RecordDecision(ctx, FleetDecision{
		Type:        "fleet.placement.assigned",
		AgentID:     r.agentID,
		SourceID:    r.sourceID,
		Content:     content,
		ContentType: "application/json",
		InputHash:   computeInputHash(content),
	})
}

// RecordRoutingChange records a traffic routing change.
func (r *FleetRecorder) RecordRoutingChange(ctx context.Context, model, fromCluster, toCluster string, weightDelta float64, reason string) (*LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"model":        model,
		"from_cluster": fromCluster,
		"to_cluster":   toCluster,
		"weight_delta": weightDelta,
		"reason":       reason,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal routing change: %w", err)
	}
	return r.client.RecordDecision(ctx, FleetDecision{
		Type:        "fleet.routing.shifted",
		AgentID:     r.agentID,
		SourceID:    r.sourceID,
		Content:     content,
		ContentType: "application/json",
		InputHash:   computeInputHash(content),
	})
}

// RecordScalingEvent records an autoscaling decision.
func (r *FleetRecorder) RecordScalingEvent(ctx context.Context, cluster, pool string, fromReplicas, toReplicas int, reason string) (*LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"cluster":       cluster,
		"pool":          pool,
		"from_replicas": fromReplicas,
		"to_replicas":   toReplicas,
		"reason":        reason,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal scaling event: %w", err)
	}
	return r.client.RecordDecision(ctx, FleetDecision{
		Type:        "fleet.scaling.adjusted",
		AgentID:     r.agentID,
		SourceID:    r.sourceID,
		Content:     content,
		ContentType: "application/json",
		InputHash:   computeInputHash(content),
	})
}

// RecordTenantUsage records tenant metering with tamper-proof storage.
func (r *FleetRecorder) RecordTenantUsage(ctx context.Context, tenantID, model, cluster string, tokensConsumed int64, cost string) (*LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"tenant_id":       tenantID,
		"model":           model,
		"cluster":         cluster,
		"tokens_consumed": tokensConsumed,
		"cost":            cost,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tenant usage: %w", err)
	}
	return r.client.RecordDecision(ctx, FleetDecision{
		Type:        "fleet.tenant.usage",
		AgentID:     r.agentID,
		SourceID:    r.sourceID,
		Content:     content,
		ContentType: "application/json",
		InputHash:   computeInputHash(content),
	})
}

// RecordLifecycleEvent records a model lifecycle event (deploy, promote, rollback).
func (r *FleetRecorder) RecordLifecycleEvent(ctx context.Context, model, version, action, cluster string, details map[string]interface{}) (*LedgerReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"model":   model,
		"version": version,
		"action":  action,
		"cluster": cluster,
		"details": details,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal lifecycle event: %w", err)
	}
	return r.client.RecordDecision(ctx, FleetDecision{
		Type:        "fleet.lifecycle." + action,
		AgentID:     r.agentID,
		SourceID:    r.sourceID,
		Content:     content,
		ContentType: "application/json",
		InputHash:   computeInputHash(content),
	})
}

// RecordKVCacheTransfer records a KV cache transfer with proof receipt.
func (r *FleetRecorder) RecordKVCacheTransfer(ctx context.Context, sourceCluster, targetCluster, model string, bytesTransferred int64, cacheHash string) (*ProofReceipt, error) {
	content, err := json.Marshal(map[string]interface{}{
		"source_cluster":    sourceCluster,
		"target_cluster":    targetCluster,
		"model":             model,
		"bytes_transferred": bytesTransferred,
		"cache_hash":        cacheHash,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal kv cache transfer: %w", err)
	}
	return r.client.IssueProofReceipt(ctx, FleetDecision{
		Type:        "fleet.kvcache.transferred",
		AgentID:     r.agentID,
		SourceID:    r.sourceID,
		Content:     content,
		ContentType: "application/json",
		InputHash:   cacheHash,
	})
}

// VerifyAllChains verifies integrity of all fleet decision chains.
func (r *FleetRecorder) VerifyAllChains(ctx context.Context) (map[string]*ChainVerification, error) {
	chainTypes := []string{
		"fleet.placement.assigned",
		"fleet.routing.shifted",
		"fleet.scaling.adjusted",
		"fleet.tenant.usage",
		"fleet.kvcache.transferred",
	}
	results := make(map[string]*ChainVerification)
	for _, ct := range chainTypes {
		v, err := r.client.VerifyDecisionChain(ctx, ct)
		if err != nil {
			return nil, fmt.Errorf("verify chain %s: %w", ct, err)
		}
		results[ct] = v
	}
	return results, nil
}
