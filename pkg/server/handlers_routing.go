package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

type routeRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
	Policy    string `json:"policy,omitempty"`
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
	defer ObserveRequest(time.Now())
	var req routeRequest
	// Bounded like every other request body on this server: prompt text is
	// caller-supplied and must not be able to exhaust memory.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, "text field is required")
		return
	}

	resp, err := fc.classifyAndRoute(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("routing failed: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (fc *FleetController) classifyAndRoute(ctx context.Context, req routeRequest) (routeResponse, error) {
	start := time.Now()

	result, err := fc.Routing.ClassifyPrompt(ctx, req.Text, req.RequestID)
	if err != nil {
		slog.Warn("classification failed, routing without semantic signal", "error", err)
	}

	routingReq := policy.RoutingRequest{
		Model:     req.Model,
		TenantID:  req.TenantID,
		SessionID: req.SessionID,
	}
	if req.Model == defaultCPUModel || (fc.CPUPhysicalModel != "" && req.Model == fc.CPUPhysicalModel) {
		routingReq.AllowedClusters = []string{"oberon-cpu", "arena-xeon6"}
	} else if req.Model == defaultGPUModel || (fc.GPUPhysicalModel != "" && req.Model == fc.GPUPhysicalModel) {
		routingReq.AllowedClusters = []string{brutusGPUCluster}
	}
	if result != nil {
		routingReq.SemanticLabel = result.TopLabel
		routingReq.SemanticScore = result.TopScore
		routingReq.SemanticMargin = result.Margin
	}

	// Session affinity with tier escalation: if this session is already
	// bound to a cluster, keep it there UNLESS the new turn's complexity
	// tier is higher than the original binding. A conversation that starts
	// SIMPLE and escalates to REASONING should rebind to a GPU cluster.
	// Downgrading never breaks affinity — overspending on a big model is
	// better than losing coherence.
	escalated := false
	if req.SessionID != "" && fc.Routing != nil && fc.Routing.SessionTable != nil {
		if boundCluster, found := fc.Routing.SessionTable.Lookup(req.SessionID); found {
			if result != nil && tierRank(result.TopLabel) > fc.Routing.SessionTierRank(req.SessionID) {
				fc.Routing.SessionTable.Unbind(req.SessionID)
				escalated = true
				slog.Info("session tier escalated",
					"session", req.SessionID,
					"new_tier", result.TopLabel,
					"old_cluster", boundCluster)
			} else {
				resp := routeResponse{
					TargetCluster: boundCluster,
					Reason:        "session-affinity",
					LatencyMs:     float64(time.Since(start).Microseconds()) / 1000,
				}
				if result != nil {
					resp.SemanticLabel = result.TopLabel
					resp.SemanticScore = result.TopScore
					resp.SemanticMargin = result.Margin
					resp.ClassifierID = result.ClassifierID
				}
				return resp, nil
			}
		}
	}

	health := fc.BuildInferenceClusterHealth(ctx)
	routingPolicy := fc.getRoutingPolicy(req.Policy)

	decision, err := fc.Routing.Evaluator.Evaluate(ctx, routingReq, health, routingPolicy)
	if err != nil {
		return routeResponse{}, err
	}

	// Bind session to the selected cluster for future turns.
	if req.SessionID != "" && fc.Routing != nil && fc.Routing.SessionTable != nil {
		fc.Routing.SessionTable.Bind(req.SessionID, decision.TargetCluster)
		if result != nil {
			fc.Routing.SetSessionTier(req.SessionID, result.TopLabel)
		}
	}

	reason := decision.Reason
	if escalated {
		reason = "escalated:" + decision.Reason
	}

	resp := routeResponse{
		TargetCluster:   decision.TargetCluster,
		Reason:          reason,
		HeadersToInject: decision.HeadersToInject,
		LatencyMs:       float64(time.Since(start).Microseconds()) / 1000,
	}
	if result != nil {
		resp.SemanticLabel = result.TopLabel
		resp.SemanticScore = result.TopScore
		resp.SemanticMargin = result.Margin
		resp.ClassifierID = result.ClassifierID
	}

	return resp, nil
}

func (fc *FleetController) getRoutingPolicy(name string) v1alpha1.FleetRoutingPolicySpec {
	if fc.CRDWatcher != nil {
		if p := fc.CRDWatcher.GetRoutingPolicy(name); p != nil {
			return *p
		}
	}
	return v1alpha1.FleetRoutingPolicySpec{
		Strategy: "weighted",
	}
}

var tierRanks = map[string]int{
	"SIMPLE": 0, "MEDIUM": 1, "COMPLEX": 2, "REASONING": 3,
}

func tierRank(label string) int {
	if r, ok := tierRanks[label]; ok {
		return r
	}
	return -1
}
