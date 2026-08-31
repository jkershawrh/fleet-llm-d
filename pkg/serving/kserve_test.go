package serving

import (
	"encoding/json"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

func TestKServeRendererDelegatesLifecycle(t *testing.T) {
	spec := v1alpha1.FleetInferencePoolSpec{
		Model:   v1alpha1.ModelSpec{Name: "granite", OciRef: "oci://models/granite:1"},
		Serving: v1alpha1.ServingSpec{Target: v1alpha1.ServingTargetKServeLLMInferenceService, KServe: &v1alpha1.KServeServingSpec{Replicas: 2}},
	}
	raw, err := (KServeRenderer{Namespace: "models"}).Render("granite", spec)
	if err != nil {
		t.Fatal(err)
	}
	var resource map[string]interface{}
	if err := json.Unmarshal(raw, &resource); err != nil {
		t.Fatal(err)
	}
	if resource["kind"] != "LLMInferenceService" {
		t.Fatalf("kind = %v", resource["kind"])
	}
	specMap := resource["spec"].(map[string]interface{})
	if specMap["replicas"] != float64(2) {
		t.Fatalf("replicas = %v", specMap["replicas"])
	}
	if _, ok := specMap["template"]; ok {
		t.Fatal("fleet renderer must not create KServe workload templates")
	}
}

func TestInferencePoolRemainsDefault(t *testing.T) {
	serving := v1alpha1.ServingSpec{}
	if serving.EffectiveTarget() != v1alpha1.ServingTargetInferencePool {
		t.Fatalf("default = %q", serving.EffectiveTarget())
	}
	_, err := (KServeRenderer{}).Render("model", v1alpha1.FleetInferencePoolSpec{Serving: serving})
	if err == nil {
		t.Fatal("expected non-KServe target rejection")
	}
}

func TestKServeStatusReady(t *testing.T) {
	status := KServeStatus{}
	status.Conditions = append(status.Conditions, struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
	}{Type: "Ready", Status: "True"})
	if !status.Ready() {
		t.Fatal("Ready condition was not honored")
	}
}
