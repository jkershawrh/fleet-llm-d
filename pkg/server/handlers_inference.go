package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/quota"
)

const (
	defaultCPUModel  = "granite-2b-cpu"
	defaultGPUModel  = "ibm-granite/granite-3.1-8b-instruct"
	defaultGPUAlias  = "granite-8b-gpu"
	brutusGPUCluster = "brutus-h100"
)

type inferenceEnvelope struct {
	Model     string          `json:"model"`
	Prompt    json.RawMessage `json:"prompt,omitempty"`
	Messages  json.RawMessage `json:"messages,omitempty"`
	TenantID  string          `json:"tenant_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Policy    string          `json:"policy,omitempty"`
	MaxTokens int64           `json:"max_tokens,omitempty"`
}

func (fc *FleetController) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	fc.handleInference(w, r, true)
}

func (fc *FleetController) handleCompletions(w http.ResponseWriter, r *http.Request) {
	fc.handleInference(w, r, false)
}

func (fc *FleetController) handleInference(w http.ResponseWriter, r *http.Request, chat bool) {
	start := time.Now()
	requestsTotal.Inc()
	defer ObserveRequest(start)
	if fc.InferenceSlots != nil {
		select {
		case fc.InferenceSlots <- struct{}{}:
			defer func() { <-fc.InferenceSlots }()
		case <-r.Context().Done():
			return
		default:
			inferenceErrors.Inc("concurrency_limit")
			w.Header().Set("Retry-After", "1")
			writeInferenceError(w, http.StatusTooManyRequests, "concurrency_limit", "inference gateway is at capacity", requestID(r))
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var envelope inferenceEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	text, err := inferenceText(envelope, chat)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	requestID := requestID(r)
	modelClass := fc.requestedModelClass(envelope.Model)
	tenantID := envelope.TenantID
	if claims := auth.GetClaims(r); claims != nil {
		if claims.Role == auth.RoleTenant || tenantID == "" {
			tenantID = claims.Subject
		}
	}
	requestedTokens := envelope.MaxTokens
	if requestedTokens <= 0 {
		requestedTokens = 256
	}
	decision, err := fc.classifyAndRoute(r.Context(), routeRequest{
		Text: text, RequestID: requestID, Model: envelope.Model,
		TenantID: tenantID, SessionID: envelope.SessionID, Policy: envelope.Policy,
	})
	if err != nil {
		inferenceErrors.Inc("no_eligible_provider")
		writeInferenceError(w, http.StatusServiceUnavailable, "no_compatible_capacity", err.Error(), requestID)
		return
	}
	if modelClass == "gpu" {
		if !fc.clusterIsHealthy(r.Context(), brutusGPUCluster) {
			inferenceErrors.Inc("no_eligible_provider")
			writeInferenceError(w, http.StatusServiceUnavailable, "no_compatible_capacity", "the requested GPU model has no compatible healthy provider", requestID)
			return
		}
		decision.TargetCluster = brutusGPUCluster
		decision.Reason = "explicit-model"
		if envelope.SessionID != "" && fc.Routing != nil && fc.Routing.SessionTable != nil {
			fc.Routing.SessionTable.Unbind(envelope.SessionID)
			fc.Routing.SessionTable.Bind(envelope.SessionID, brutusGPUCluster)
		}
	}
	physicalModel := fc.CPUPhysicalModel
	if physicalModel == "" {
		physicalModel = defaultCPUModel
	}
	if modelClass == "gpu" || (modelClass == "" && (decision.SemanticLabel == "COMPLEX" || decision.SemanticLabel == "REASONING" || strings.HasPrefix(decision.TargetCluster, "brutus"))) {
		physicalModel = fc.GPUPhysicalModel
		if physicalModel == "" {
			physicalModel = defaultGPUModel
		}
	}
	if physicalModel != fc.cpuPhysicalModel() {
		if !fc.clusterIsHealthy(r.Context(), brutusGPUCluster) {
			inferenceErrors.Inc("no_eligible_provider")
			writeInferenceError(w, http.StatusServiceUnavailable, "no_compatible_capacity", "the selected GPU model has no compatible healthy provider", requestID)
			return
		}
		decision.TargetCluster = brutusGPUCluster
		if modelClass == "" {
			decision.Reason = "semantic-escalation"
		}
		if envelope.SessionID != "" && fc.Routing != nil && fc.Routing.SessionTable != nil {
			fc.Routing.SessionTable.Unbind(envelope.SessionID)
			fc.Routing.SessionTable.Bind(envelope.SessionID, brutusGPUCluster)
		}
	}
	if physicalModel == fc.cpuPhysicalModel() && decision.Reason != "session-affinity" {
		if selected := fc.nextHealthyCPUProvider(r.Context()); selected != "" {
			decision.TargetCluster = selected
			decision.Reason = "compatible-round-robin"
			if envelope.SessionID != "" && fc.Routing != nil && fc.Routing.SessionTable != nil {
				fc.Routing.SessionTable.Unbind(envelope.SessionID)
				fc.Routing.SessionTable.Bind(envelope.SessionID, selected)
			}
		}
	}
	if tenantID != "" && fc.QuotaEnforcer != nil {
		check := quota.QuotaCheckRequest{TokensRequested: requestedTokens, Model: physicalModel, ClusterID: decision.TargetCluster}
		var result quota.QuotaCheckResult
		if consumer, ok := fc.QuotaEnforcer.(*quota.DefaultQuotaEnforcer); ok {
			result, err = consumer.ReserveQuota(r.Context(), tenantID, check)
			if err == nil && result.Allowed {
				defer func() {
					releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if releaseErr := consumer.ReleaseQuota(releaseCtx, tenantID); releaseErr != nil {
						slog.Error("release tenant quota reservation", "tenant_id", tenantID, "error", releaseErr)
					}
				}()
			}
		} else {
			result, err = fc.QuotaEnforcer.CheckQuota(r.Context(), tenantID, check)
		}
		if err != nil {
			inferenceErrors.Inc("quota_unavailable")
			writeInferenceError(w, http.StatusServiceUnavailable, "quota_unavailable", "tenant quota service is unavailable", requestID)
			return
		}
		if !result.Allowed {
			inferenceErrors.Inc("quota_denied")
			w.Header().Set("Retry-After", "60")
			writeInferenceError(w, http.StatusTooManyRequests, "quota_exceeded", result.Reason, requestID)
			return
		}
	}
	var forwarded map[string]json.RawMessage
	_ = json.Unmarshal(body, &forwarded)
	modelJSON, _ := json.Marshal(physicalModel)
	forwarded["model"] = modelJSON
	delete(forwarded, "tenant_id")
	delete(forwarded, "session_id")
	delete(forwarded, "policy")
	body, _ = json.Marshal(forwarded)
	target, err := fc.inferenceTarget(physicalModel)
	if err != nil {
		writeInferenceError(w, http.StatusServiceUnavailable, "gateway_not_configured", err.Error(), requestID)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.BaseURL+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		writeInferenceError(w, http.StatusInternalServerError, "proxy_error", "could not create upstream request", requestID)
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", r.Header.Get("Accept"))
	upstream.Header.Set("X-Request-ID", requestID)
	upstream.Header.Set("X-Fleet-Target-Cluster", decision.TargetCluster)
	upstream.Header.Set("X-Fleet-Routing-Reason", decision.Reason)
	if target.APIToken != "" {
		upstream.Header.Set("Authorization", "Bearer "+target.APIToken)
	}
	client := fc.InferenceClient
	if client == nil {
		client = &http.Client{Timeout: 180 * time.Second}
	}
	inferenceActive.Inc()
	defer inferenceActive.Dec()
	resp, err := client.Do(upstream)
	if err != nil {
		inferenceErrors.Inc("upstream_unavailable")
		slog.Warn("inference upstream failed", "request_id", requestID, "cluster", decision.TargetCluster, "error", err)
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			writeInferenceError(w, http.StatusGatewayTimeout, "upstream_timeout", "inference upstream timed out", requestID)
			return
		}
		writeInferenceError(w, http.StatusBadGateway, "upstream_unavailable", "inference upstream failed", requestID)
		return
	}
	defer resp.Body.Close()
	if target.Provider == InferenceProviderLLMD {
		actualCluster, evidenceErr := fc.routerClusterForUpstream(resp.Header.Get("X-Fleet-Router-Upstream"), physicalModel)
		if evidenceErr != nil {
			inferenceErrors.Inc("routing_evidence_mismatch")
			_, _ = io.Copy(io.Discard, resp.Body)
			slog.Error("Router execution evidence rejected", "request_id", requestID, "error", evidenceErr)
			writeInferenceError(w, http.StatusBadGateway, "routing_evidence_mismatch", "Router execution identity did not match the fleet-qualified provider set", requestID)
			return
		}
		decision.TargetCluster = actualCluster
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		inferenceErrors.Inc("no_eligible_provider")
		_, _ = io.Copy(io.Discard, resp.Body)
		writeInferenceError(w, http.StatusServiceUnavailable, "no_compatible_capacity", "the selected provider is unavailable", requestID)
		return
	}
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Fleet-Routed-To", decision.TargetCluster)
	w.Header().Set("X-Fleet-Routing-Reason", decision.Reason)
	w.Header().Set("X-Fleet-Actual-Model", physicalModel)
	w.Header().Set("X-Fleet-Data-Plane", string(target.Provider))
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		inferenceErrors.Inc("stream_interrupted")
	}
	inferenceRequests.Inc(decision.TargetCluster + ":" + physicalModel)
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (fc *FleetController) requiresGPUModel(requested string) bool {
	return fc.requestedModelClass(requested) == "gpu"
}

func (fc *FleetController) requestedModelClass(requested string) string {
	if requested == "" {
		return ""
	}
	if requested == fc.cpuPhysicalModel() || requested == defaultCPUModel {
		return "cpu"
	}
	gpuModel := fc.GPUPhysicalModel
	if gpuModel == "" {
		gpuModel = defaultGPUModel
	}
	if requested == gpuModel || requested == defaultGPUModel || requested == defaultGPUAlias {
		return "gpu"
	}
	return ""
}

func (fc *FleetController) cpuPhysicalModel() string {
	if fc.CPUPhysicalModel != "" {
		return fc.CPUPhysicalModel
	}
	return defaultCPUModel
}

func (fc *FleetController) nextHealthyCPUProvider(ctx context.Context) string {
	allowed := map[string]bool{"oberon-cpu": true, "arena-xeon6": true}
	providers := make([]string, 0, len(allowed))
	for _, cluster := range fc.BuildInferenceClusterHealth(ctx) {
		if allowed[cluster.ClusterID] && cluster.Healthy {
			providers = append(providers, cluster.ClusterID)
		}
	}
	if len(providers) == 0 {
		return ""
	}
	sort.Strings(providers)
	index := (fc.cpuRouteCounter.Add(1) - 1) % uint64(len(providers))
	return providers[index]
}

func (fc *FleetController) clusterIsHealthy(ctx context.Context, clusterID string) bool {
	for _, cluster := range fc.BuildInferenceClusterHealth(ctx) {
		if cluster.ClusterID == clusterID {
			return cluster.Healthy
		}
	}
	return false
}

func inferenceText(req inferenceEnvelope, chat bool) (string, error) {
	if chat {
		var messages []struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(req.Messages, &messages); err != nil || len(messages) == 0 {
			return "", fmt.Errorf("messages must be a non-empty array")
		}
		for i := len(messages) - 1; i >= 0; i-- {
			var text string
			if json.Unmarshal(messages[i].Content, &text) == nil && strings.TrimSpace(text) != "" {
				return text, nil
			}
		}
		return "", fmt.Errorf("messages must contain text content")
	}
	var prompt string
	if err := json.Unmarshal(req.Prompt, &prompt); err != nil || strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt must be a non-empty string")
	}
	return prompt, nil
}

func requestID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Request-ID")); id != "" && len(id) <= 128 {
		return id
	}
	return fmt.Sprintf("fleet-%d", time.Now().UnixNano())
}

func copyResponseHeaders(dst, src http.Header) {
	for _, key := range []string{"Content-Type", "Cache-Control", "Retry-After"} {
		if value := src.Get(key); value != "" {
			dst.Set(key, value)
		}
	}
}

func writeInferenceError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("X-Request-ID", requestID)
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID}})
}
