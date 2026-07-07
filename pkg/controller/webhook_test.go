package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// --- FleetInferencePool validation ---

func TestValidateFleetInferencePool_Valid(t *testing.T) {
	spec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "granite-3.3-2b", Source: "huggingface"},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "default",
			MinClusters: 1,
			MaxClusters: 3,
		},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}
	result := ValidateFleetInferencePool(spec)
	if !result.Valid {
		t.Fatalf("expected valid, got invalid: %s (%v)", result.Reason, result.Details)
	}
}

func TestValidateFleetInferencePool_EmptyModel(t *testing.T) {
	spec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "", Source: "huggingface"},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}
	result := ValidateFleetInferencePool(spec)
	if result.Valid {
		t.Fatal("expected invalid for empty model name")
	}
	found := false
	for _, d := range result.Details {
		if d == "model name must not be empty" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'model name must not be empty' in details, got %v", result.Details)
	}
}

func TestValidateFleetInferencePool_NoTargetPorts(t *testing.T) {
	spec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "granite", Source: "huggingface"},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{},
				},
			},
		},
	}
	result := ValidateFleetInferencePool(spec)
	if result.Valid {
		t.Fatal("expected invalid for no target ports")
	}
	found := false
	for _, d := range result.Details {
		if d == "at least one target port is required" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'at least one target port is required' in details, got %v", result.Details)
	}
}

// --- TenantProfile validation ---

func TestValidateTenantProfile_Valid(t *testing.T) {
	spec := v1alpha1.TenantProfileSpec{
		Priority: 1,
		Quotas: v1alpha1.TenantQuota{
			MaxTokensPerMinute:    1000,
			MaxConcurrentRequests: 10,
			MaxModels:             5,
		},
		CostControl: &v1alpha1.CostControlSpec{
			MonthlyBudget:  "$100.00",
			AlertThreshold: 0.8,
		},
	}
	result := ValidateTenantProfile(spec)
	if !result.Valid {
		t.Fatalf("expected valid, got invalid: %s (%v)", result.Reason, result.Details)
	}
}

func TestValidateTenantProfile_NegativePriority(t *testing.T) {
	spec := v1alpha1.TenantProfileSpec{
		Priority: -1,
		Quotas: v1alpha1.TenantQuota{
			MaxTokensPerMinute:    1000,
			MaxConcurrentRequests: 10,
		},
	}
	result := ValidateTenantProfile(spec)
	if result.Valid {
		t.Fatal("expected invalid for negative priority")
	}
	found := false
	for _, d := range result.Details {
		if d == "priority must be >= 0, got -1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected priority error in details, got %v", result.Details)
	}
}

func TestValidateTenantProfile_ZeroTokens(t *testing.T) {
	spec := v1alpha1.TenantProfileSpec{
		Priority: 0,
		Quotas: v1alpha1.TenantQuota{
			MaxTokensPerMinute:    0,
			MaxConcurrentRequests: 10,
		},
	}
	result := ValidateTenantProfile(spec)
	if result.Valid {
		t.Fatal("expected invalid for zero maxTokensPerMinute")
	}
	found := false
	for _, d := range result.Details {
		if d == "maxTokensPerMinute must be > 0, got 0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected maxTokensPerMinute error in details, got %v", result.Details)
	}
}

// --- PlacementPolicy validation ---

func TestValidatePlacementPolicy_Valid(t *testing.T) {
	spec := v1alpha1.PlacementPolicySpec{
		Constraints: []v1alpha1.PlacementConstraint{
			{Type: "gpu", Rule: "A100"},
		},
		Affinity: []v1alpha1.AffinityRule{
			{Type: "cost", Weight: 0.6},
			{Type: "locality", Weight: 0.4},
		},
	}
	result := ValidatePlacementPolicy(spec)
	if !result.Valid {
		t.Fatalf("expected valid, got invalid: %s (%v)", result.Reason, result.Details)
	}
}

func TestValidatePlacementPolicy_EmptyConstraints(t *testing.T) {
	spec := v1alpha1.PlacementPolicySpec{
		Constraints: []v1alpha1.PlacementConstraint{},
		Affinity:    []v1alpha1.AffinityRule{},
	}
	result := ValidatePlacementPolicy(spec)
	if result.Valid {
		t.Fatal("expected invalid for empty constraints and affinity")
	}
	found := false
	for _, d := range result.Details {
		if d == "at least one constraint or affinity rule is required" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected constraint error in details, got %v", result.Details)
	}
}

func TestValidatePlacementPolicy_WeightOutOfRange(t *testing.T) {
	spec := v1alpha1.PlacementPolicySpec{
		Affinity: []v1alpha1.AffinityRule{
			{Type: "cost", Weight: 1.5},
			{Type: "locality", Weight: -0.5},
		},
	}
	result := ValidatePlacementPolicy(spec)
	if result.Valid {
		t.Fatal("expected invalid for out-of-range weights")
	}
	// Should have at least two weight errors.
	weightErrors := 0
	for _, d := range result.Details {
		if len(d) > 0 && (d[0] == 'a' || d[0] == 'A') {
			// Check for "affinity rule N weight must be between..."
			weightErrors++
		}
	}
	if weightErrors < 2 {
		t.Fatalf("expected at least 2 weight errors, got %d in %v", weightErrors, result.Details)
	}
}

// --- Webhook HTTP handler ---

func TestWebhookHandler_ValidFleetInferencePool(t *testing.T) {
	spec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "granite-3.3-2b", Source: "huggingface"},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}
	objBytes, _ := json.Marshal(spec)

	review := map[string]interface{}{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request": map[string]interface{}{
			"uid":    "test-uid",
			"kind":   map[string]string{"group": "fleet.llm-d.ai", "version": "v1alpha1", "kind": "FleetInferencePool"},
			"object": json.RawMessage(objBytes),
		},
	}
	body, _ := json.Marshal(review)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	WebhookHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp admissionReview
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Response == nil || !resp.Response.Allowed {
		t.Fatalf("expected allowed, got %+v", resp.Response)
	}
}

func TestWebhookHandler_ValidFullKubernetesFleetInferencePoolObject(t *testing.T) {
	obj := map[string]interface{}{
		"apiVersion": "fleet.llm-d.ai/v1alpha1",
		"kind":       "FleetInferencePool",
		"metadata": map[string]interface{}{
			"name":      "granite",
			"namespace": "default",
		},
		"spec": v1alpha1.FleetInferencePoolSpec{
			Model: v1alpha1.ModelSpec{Name: "granite-3.3-2b", Source: "huggingface"},
			Serving: v1alpha1.ServingSpec{
				InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
					Spec: v1alpha1.InferencePoolTemplateSpec{
						TargetPorts: []int{8000},
					},
				},
			},
		},
	}
	objBytes, _ := json.Marshal(obj)

	review := map[string]interface{}{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request": map[string]interface{}{
			"uid":    "full-object-uid",
			"kind":   map[string]string{"group": "fleet.llm-d.ai", "version": "v1alpha1", "kind": "FleetInferencePool"},
			"object": json.RawMessage(objBytes),
		},
	}
	body, _ := json.Marshal(review)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	WebhookHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp admissionReview
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Response == nil || !resp.Response.Allowed {
		t.Fatalf("expected full Kubernetes object to be allowed, got %+v", resp.Response)
	}
}

func TestWebhookHandler_RejectsInvalidCRD(t *testing.T) {
	// Empty model name should cause rejection.
	spec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "", Source: "huggingface"},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}
	objBytes, _ := json.Marshal(spec)

	review := map[string]interface{}{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request": map[string]interface{}{
			"uid":    "test-uid-2",
			"kind":   map[string]string{"group": "fleet.llm-d.ai", "version": "v1alpha1", "kind": "FleetInferencePool"},
			"object": json.RawMessage(objBytes),
		},
	}
	body, _ := json.Marshal(review)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	WebhookHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (webhook protocol always returns 200), got %d", rec.Code)
	}

	var resp admissionReview
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Response == nil {
		t.Fatal("expected response in AdmissionReview")
	}
	if resp.Response.Allowed {
		t.Fatal("expected rejected (allowed=false) for empty model name")
	}
	if resp.Response.Status == nil || resp.Response.Status.Message == "" {
		t.Fatal("expected rejection message in status")
	}
}
