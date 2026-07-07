package controller

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// ValidationResult represents the outcome of validating a fleet CRD.
type ValidationResult struct {
	Valid   bool     `json:"valid"`
	Reason  string  `json:"reason,omitempty"`
	Details []string `json:"details,omitempty"`
}

// validRolloutStrategies is the set of accepted rollout strategy values.
var validRolloutStrategies = map[string]bool{
	"":              true, // empty means unset / default
	"Canary":        true,
	"RollingUpdate": true,
	"BlueGreen":     true,
	"Recreate":      true,
}

// ValidateFleetInferencePool validates a FleetInferencePoolSpec.
// Checks:
//   - Model name is non-empty
//   - At least one target port > 0
//   - If placement policyRef is set, minClusters >= 1
//   - rolloutStrategy is a valid enum value
func ValidateFleetInferencePool(spec v1alpha1.FleetInferencePoolSpec) ValidationResult {
	var details []string

	if spec.Model.Name == "" {
		details = append(details, "model name must not be empty")
	}

	if len(spec.Serving.InferencePoolTemplate.Spec.TargetPorts) == 0 {
		details = append(details, "at least one target port is required")
	} else {
		for i, port := range spec.Serving.InferencePoolTemplate.Spec.TargetPorts {
			if port <= 0 {
				details = append(details, fmt.Sprintf("target port at index %d must be > 0, got %d", i, port))
			}
		}
	}

	if spec.Placement.PolicyRef != "" && spec.Placement.MinClusters < 1 {
		details = append(details, "when placement policyRef is set, minClusters must be >= 1")
	}

	strategy := spec.Lifecycle.RolloutStrategy
	if !validRolloutStrategies[strategy] {
		details = append(details, fmt.Sprintf("rolloutStrategy %q is not a valid value (allowed: Canary, RollingUpdate, BlueGreen, Recreate)", strategy))
	}

	if len(details) > 0 {
		return ValidationResult{
			Valid:   false,
			Reason:  "FleetInferencePool spec validation failed",
			Details: details,
		}
	}
	return ValidationResult{Valid: true}
}

// ValidateTenantProfile validates a TenantProfileSpec.
// Checks:
//   - Priority >= 0
//   - MaxTokensPerMinute > 0
//   - MaxConcurrentRequests > 0
//   - MonthlyBudget parseable as positive number (if set)
//   - AlertThreshold between 0 and 1 (if set)
func ValidateTenantProfile(spec v1alpha1.TenantProfileSpec) ValidationResult {
	var details []string

	if spec.Priority < 0 {
		details = append(details, fmt.Sprintf("priority must be >= 0, got %d", spec.Priority))
	}

	if spec.Quotas.MaxTokensPerMinute <= 0 {
		details = append(details, fmt.Sprintf("maxTokensPerMinute must be > 0, got %d", spec.Quotas.MaxTokensPerMinute))
	}

	if spec.Quotas.MaxConcurrentRequests <= 0 {
		details = append(details, fmt.Sprintf("maxConcurrentRequests must be > 0, got %d", spec.Quotas.MaxConcurrentRequests))
	}

	if spec.CostControl != nil {
		if spec.CostControl.MonthlyBudget != "" {
			budget := strings.TrimPrefix(spec.CostControl.MonthlyBudget, "$")
			val, err := strconv.ParseFloat(budget, 64)
			if err != nil {
				details = append(details, fmt.Sprintf("monthlyBudget %q is not a valid number", spec.CostControl.MonthlyBudget))
			} else if val <= 0 {
				details = append(details, fmt.Sprintf("monthlyBudget must be positive, got %s", spec.CostControl.MonthlyBudget))
			}
		}

		if spec.CostControl.AlertThreshold != 0 {
			if spec.CostControl.AlertThreshold < 0 || spec.CostControl.AlertThreshold > 1 {
				details = append(details, fmt.Sprintf("alertThreshold must be between 0 and 1, got %f", spec.CostControl.AlertThreshold))
			}
		}
	}

	if len(details) > 0 {
		return ValidationResult{
			Valid:   false,
			Reason:  "TenantProfile spec validation failed",
			Details: details,
		}
	}
	return ValidationResult{Valid: true}
}

// ValidatePlacementPolicy validates a PlacementPolicySpec.
// Checks:
//   - At least one constraint or affinity rule
//   - Affinity weights between 0 and 1
//   - Affinity weights sum to approximately 1.0
func ValidatePlacementPolicy(spec v1alpha1.PlacementPolicySpec) ValidationResult {
	var details []string

	if len(spec.Constraints) == 0 && len(spec.Affinity) == 0 {
		details = append(details, "at least one constraint or affinity rule is required")
	}

	weightSum := 0.0
	for i, rule := range spec.Affinity {
		if rule.Weight < 0 || rule.Weight > 1 {
			details = append(details, fmt.Sprintf("affinity rule %d weight must be between 0 and 1, got %f", i, rule.Weight))
		}
		weightSum += rule.Weight
	}

	if len(spec.Affinity) > 0 && math.Abs(weightSum-1.0) > 0.01 {
		details = append(details, fmt.Sprintf("affinity weights must sum to approximately 1.0, got %f", weightSum))
	}

	if len(details) > 0 {
		return ValidationResult{
			Valid:   false,
			Reason:  "PlacementPolicy spec validation failed",
			Details: details,
		}
	}
	return ValidationResult{Valid: true}
}

// --- Kubernetes Admission Webhook Protocol ---

// admissionReview is a minimal representation of a Kubernetes AdmissionReview.
type admissionReview struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Request    *admissionRequest `json:"request,omitempty"`
	Response   *admissionResponse `json:"response,omitempty"`
}

type admissionRequest struct {
	UID    string          `json:"uid"`
	Kind   admissionKind   `json:"kind"`
	Object json.RawMessage `json:"object"`
}

type admissionKind struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

type admissionResponse struct {
	UID     string          `json:"uid"`
	Allowed bool            `json:"allowed"`
	Status  *admissionStatus `json:"status,omitempty"`
}

type admissionStatus struct {
	Message string `json:"message"`
}

// WebhookHandler returns an HTTP handler for validation webhook requests.
// It accepts AdmissionReview JSON (Kubernetes admission webhook protocol)
// and validates FleetInferencePool, TenantProfile, and PlacementPolicy CRDs.
func WebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var review admissionReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if review.Request == nil {
			http.Error(w, "missing request in AdmissionReview", http.StatusBadRequest)
			return
		}

		var result ValidationResult
		switch review.Request.Kind.Kind {
		case "FleetInferencePool":
			var spec v1alpha1.FleetInferencePoolSpec
			if err := json.Unmarshal(review.Request.Object, &spec); err != nil {
				result = ValidationResult{
					Valid:  false,
					Reason: "failed to unmarshal FleetInferencePoolSpec: " + err.Error(),
				}
			} else {
				result = ValidateFleetInferencePool(spec)
			}
		case "TenantProfile":
			var spec v1alpha1.TenantProfileSpec
			if err := json.Unmarshal(review.Request.Object, &spec); err != nil {
				result = ValidationResult{
					Valid:  false,
					Reason: "failed to unmarshal TenantProfileSpec: " + err.Error(),
				}
			} else {
				result = ValidateTenantProfile(spec)
			}
		case "PlacementPolicy":
			var spec v1alpha1.PlacementPolicySpec
			if err := json.Unmarshal(review.Request.Object, &spec); err != nil {
				result = ValidationResult{
					Valid:  false,
					Reason: "failed to unmarshal PlacementPolicySpec: " + err.Error(),
				}
			} else {
				result = ValidatePlacementPolicy(spec)
			}
		default:
			result = ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("unsupported kind: %s", review.Request.Kind.Kind),
			}
		}

		// Build the response message.
		message := ""
		if !result.Valid {
			message = result.Reason
			if len(result.Details) > 0 {
				message += ": " + strings.Join(result.Details, "; ")
			}
		}

		resp := admissionReview{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
			Response: &admissionResponse{
				UID:     review.Request.UID,
				Allowed: result.Valid,
			},
		}
		if !result.Valid {
			resp.Response.Status = &admissionStatus{Message: message}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
