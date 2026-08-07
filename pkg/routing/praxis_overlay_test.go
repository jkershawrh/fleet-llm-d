package routing

import (
	"strings"
	"testing"
)

func TestRenderConfig_SingleModel(t *testing.T) {
	overlay := NewPraxisOverlay([]PraxisClusterEndpoint{
		{ClusterID: "oberon-sno", Endpoint: "ovms-granite-2b.fleet-llm-d.svc:8080"},
	})

	placements := []PoolPlacement{
		{ModelName: "granite-2b-cpu", Clusters: []string{"oberon-sno"}},
	}

	out, err := overlay.RenderConfig(placements)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "granite-2b-cpu") {
		t.Errorf("expected model name in config, got:\n%s", out)
	}
	if !strings.Contains(out, "ovms-granite-2b.fleet-llm-d.svc:8080") {
		t.Errorf("expected endpoint in config, got:\n%s", out)
	}
	if !strings.Contains(out, "model_to_header") {
		t.Errorf("expected model_to_header filter, got:\n%s", out)
	}
}

func TestRenderConfig_MultiCluster(t *testing.T) {
	overlay := NewPraxisOverlay([]PraxisClusterEndpoint{
		{ClusterID: "oberon-sno", Endpoint: "ovms.fleet-llm-d.svc:8080"},
		{ClusterID: "brutus-h100", Endpoint: "brutus-vllm-external.fleet-llm-d.svc:8000"},
	})

	placements := []PoolPlacement{
		{
			ModelName:    "granite-2b-cpu",
			ModelAliases: []string{"granite-sovereign"},
			Clusters:     []string{"oberon-sno"},
		},
		{
			ModelName:    "ibm-granite/granite-3.1-8b-instruct",
			ModelAliases: []string{"granite-8b-gpu"},
			Clusters:     []string{"brutus-h100"},
		},
	}

	out, err := overlay.RenderConfig(placements)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "granite-sovereign") {
		t.Errorf("expected alias in config")
	}
	if !strings.Contains(out, "granite-8b-gpu") {
		t.Errorf("expected GPU alias in config")
	}
	if !strings.Contains(out, "brutus-vllm-external") {
		t.Errorf("expected brutus endpoint in config")
	}
}

func TestRenderConfig_UnknownClusterSkipped(t *testing.T) {
	overlay := NewPraxisOverlay([]PraxisClusterEndpoint{
		{ClusterID: "oberon-sno", Endpoint: "ovms:8080"},
	})

	placements := []PoolPlacement{
		{ModelName: "model-a", Clusters: []string{"unknown-cluster"}},
	}

	out, err := overlay.RenderConfig(placements)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "model-a") {
		t.Errorf("expected unknown cluster to be skipped, got:\n%s", out)
	}
}

func TestRenderConfig_EmptyPlacements(t *testing.T) {
	overlay := NewPraxisOverlay(nil)

	out, err := overlay.RenderConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "listeners") {
		t.Errorf("expected valid config even with no placements, got:\n%s", out)
	}
}

func TestSanitizeClusterName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"granite-2b-cpu", "granite-2b-cpu"},
		{"ibm-granite/granite-3.1-8b-instruct", "ibm-granite-granite-3-1-8b-instruct"},
		{"Model With Spaces", "model-with-spaces"},
	}
	for _, tt := range tests {
		got := sanitizeClusterName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeClusterName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
