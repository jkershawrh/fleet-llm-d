package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/classifier"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

type routeRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
}

type routeResponse struct {
	TargetCluster   string            `json:"target_cluster"`
	Reason          string            `json:"reason"`
	SemanticLabel   string            `json:"semantic_label,omitempty"`
	SemanticScore   float64           `json:"semantic_score,omitempty"`
	SemanticMargin  float64           `json:"semantic_margin,omitempty"`
	ClassifierID    string            `json:"classifier_id,omitempty"`
	HeadersToInject map[string]string `json:"headers_to_inject,omitempty"`
	LatencyMs       float64           `json:"latency_ms"`
}

func (fc *FleetController) handleClassifyAndRoute(w http.ResponseWriter, r *http.Request) {
	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text field is required", http.StatusBadRequest)
		return
	}

	start := time.Now()
	ctx := r.Context()

	result, err := fc.classifyPrompt(ctx, req.Text, req.RequestID)
	if err != nil {
		slog.Warn("classification failed, routing without semantic signal", "error", err)
	}

	routingReq := policy.RoutingRequest{
		Model:     req.Model,
		TenantID:  req.TenantID,
		SessionID: req.SessionID,
	}
	if result != nil {
		routingReq.SemanticLabel = result.TopLabel
		routingReq.SemanticScore = result.TopScore
		routingReq.SemanticMargin = result.Margin
	}

	health := fc.BuildClusterHealth(ctx)
	routingPolicy := fc.getDefaultRoutingPolicy()

	decision, err := fc.RoutingEvaluator.Evaluate(ctx, routingReq, health, routingPolicy)
	if err != nil {
		http.Error(w, fmt.Sprintf("routing failed: %v", err), http.StatusServiceUnavailable)
		return
	}

	resp := routeResponse{
		TargetCluster:   decision.TargetCluster,
		Reason:          decision.Reason,
		HeadersToInject: decision.HeadersToInject,
		LatencyMs:       float64(time.Since(start).Microseconds()) / 1000,
	}
	if result != nil {
		resp.SemanticLabel = result.TopLabel
		resp.SemanticScore = result.TopScore
		resp.SemanticMargin = result.Margin
		resp.ClassifierID = result.ClassifierID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (fc *FleetController) classifyPrompt(ctx context.Context, text, requestID string) (*classifier.ClassifyResult, error) {
	if fc.ClassifierClient == nil {
		return nil, nil
	}

	if cached, ok := fc.ClassifierCache.Get(text); ok {
		return cached, nil
	}

	if requestID == "" {
		requestID = fmt.Sprintf("fleet-%d", time.Now().UnixNano())
	}

	result, err := fc.ClassifierClient.Classify(ctx, text, requestID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}

	fc.ClassifierCache.Put(text, result)
	return result, nil
}

func (fc *FleetController) getDefaultRoutingPolicy() v1alpha1.FleetRoutingPolicySpec {
	return v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
	}
}

func (fc *FleetController) semanticTierDistribution() map[string]float64 {
	if fc.ClassifierCache == nil {
		return nil
	}
	return fc.ClassifierCache.TierDistribution()
}
