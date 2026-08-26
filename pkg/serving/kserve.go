package serving

import (
	"encoding/json"
	"fmt"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// KServeRenderer translates fleet placement into the KServe-owned
// LLMInferenceService lifecycle API. It deliberately does not create the
// underlying Deployment, InferencePool, Gateway, or Router resources.
type KServeRenderer struct {
	Namespace string
}

func (r KServeRenderer) Render(name string, spec v1alpha1.FleetInferencePoolSpec) ([]byte, error) {
	if spec.Serving.EffectiveTarget() != v1alpha1.ServingTargetKServeLLMInferenceService {
		return nil, fmt.Errorf("serving target is %q, not kserveLLMInferenceService", spec.Serving.EffectiveTarget())
	}
	modelURI := strings.TrimSpace(spec.Model.OciRef)
	if spec.Serving.KServe != nil && strings.TrimSpace(spec.Serving.KServe.ModelURI) != "" {
		modelURI = strings.TrimSpace(spec.Serving.KServe.ModelURI)
	}
	if modelURI == "" {
		modelURI = strings.TrimSpace(spec.Model.Source)
	}
	if modelURI == "" {
		return nil, fmt.Errorf("KServe model URI is required")
	}
	replicas := 1
	criticality := ""
	if spec.Serving.KServe != nil {
		if spec.Serving.KServe.Replicas > 0 {
			replicas = spec.Serving.KServe.Replicas
		}
		criticality = spec.Serving.KServe.Criticality
	}
	namespace := r.Namespace
	if namespace == "" {
		namespace = "default"
	}
	resource := map[string]interface{}{
		"apiVersion": "serving.kserve.io/v1alpha1",
		"kind":       "LLMInferenceService",
		"metadata": map[string]interface{}{
			"name": name, "namespace": namespace,
			"labels": map[string]string{"fleet.llm-d.ai/managed-by": "fleet-controller"},
		},
		"spec": map[string]interface{}{
			"model":    map[string]interface{}{"name": spec.Model.Name, "uri": modelURI, "criticality": criticality},
			"replicas": replicas,
			"router":   map[string]interface{}{"gateway": map[string]interface{}{}, "route": map[string]interface{}{}, "scheduler": map[string]interface{}{}},
		},
	}
	return json.Marshal(resource)
}

type KServeStatus struct {
	URL        string `json:"url,omitempty"`
	Conditions []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
	} `json:"conditions,omitempty"`
}

// Ready reports KServe's lifecycle status without attempting to reproduce its
// rollout or readiness logic.
func (s KServeStatus) Ready() bool {
	for _, condition := range s.Conditions {
		if condition.Type == "Ready" {
			return strings.EqualFold(condition.Status, "true")
		}
	}
	return false
}
