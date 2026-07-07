package modelplane

import (
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

func TestInferenceClusterToClusterInfo(t *testing.T) {
	ic := InferenceCluster{
		Name:     "prod-east",
		Region:   "us-east-1",
		Provider: "gke",
		Labels:   map[string]string{"env": "prod", "region": "us-east-1"},
		Status:   ClusterStatus{Phase: "Ready", Nodes: 10},
		Pools: []NodePool{
			{Name: "pool-a", GPUType: "H200", Count: 8, Available: 6},
			{Name: "pool-b", GPUType: "A100", Count: 4, Available: 2},
		},
	}

	ci := InferenceClusterToClusterInfo(ic)

	if ci.ID != "prod-east" {
		t.Fatalf("ID = %q, want 'prod-east'", ci.ID)
	}
	if ci.Name != "prod-east" {
		t.Fatalf("Name = %q, want 'prod-east'", ci.Name)
	}
	if ci.Region != "us-east-1" {
		t.Fatalf("Region = %q, want 'us-east-1'", ci.Region)
	}
	if ci.Labels["env"] != "prod" {
		t.Fatalf("Labels[env] = %q, want 'prod'", ci.Labels["env"])
	}
	// Total GPU: 8 + 4 = 12, Available: 6 + 2 = 8
	if ci.GPUCapacity.Total != 12 {
		t.Fatalf("GPUCapacity.Total = %d, want 12", ci.GPUCapacity.Total)
	}
	if ci.GPUCapacity.Available != 8 {
		t.Fatalf("GPUCapacity.Available = %d, want 8", ci.GPUCapacity.Available)
	}
	if len(ci.GPUCapacity.Types) != 2 {
		t.Fatalf("GPUCapacity.Types len = %d, want 2", len(ci.GPUCapacity.Types))
	}
	// Utilization = 1 - 8/12 = 1/3 ~ 0.333
	if ci.Utilization < 0.3 || ci.Utilization > 0.4 {
		t.Fatalf("Utilization = %f, want ~0.333", ci.Utilization)
	}
}

func TestModelEndpointToBackend(t *testing.T) {
	me := ModelEndpoint{
		Name:      "granite-vllm-ep",
		Namespace: "fleet",
		URL:       "http://granite-vllm.fleet.svc:8000",
		Model:     "granite-3b",
		Cluster:   "prod-east",
		Ready:     true,
	}

	b := ModelEndpointToBackend(me)

	if b.Name != "granite-vllm-ep" {
		t.Fatalf("Name = %q, want 'granite-vllm-ep'", b.Name)
	}
	if b.URL != "http://granite-vllm.fleet.svc:8000" {
		t.Fatalf("URL = %q, want 'http://granite-vllm.fleet.svc:8000'", b.URL)
	}
	if !b.Healthy {
		t.Fatal("Healthy should be true when Ready is true")
	}
	if b.Runtime != "vllm" {
		t.Fatalf("Runtime = %q, want 'vllm'", b.Runtime)
	}
}

func TestModelEndpointToBackend_NotReady(t *testing.T) {
	me := ModelEndpoint{
		Name:  "offline-ep",
		URL:   "http://offline.svc:8000",
		Model: "test-model",
		Ready: false,
	}

	b := ModelEndpointToBackend(me)

	if b.Healthy {
		t.Fatal("Healthy should be false when Ready is false")
	}
}

func TestModelDeploymentToFleetPool(t *testing.T) {
	md := ModelDeployment{
		Name:      "granite-deploy",
		Namespace: "fleet",
		Model:     "granite-3b",
		Engine:    "vllm",
		Replicas:  4,
		Status: DeploymentStatus{
			Phase:         "Running",
			ReadyReplicas: 4,
			Clusters:      []string{"us-east", "eu-west"},
		},
	}

	fp := ModelDeploymentToFleetPool(md)

	if fp.Name != "granite-deploy" {
		t.Fatalf("Name = %q, want 'granite-deploy'", fp.Name)
	}
	if fp.Model != "granite-3b" {
		t.Fatalf("Model = %q, want 'granite-3b'", fp.Model)
	}
	if fp.Phase != v1alpha1.FleetPhaseRunning {
		t.Fatalf("Phase = %q, want 'Running'", fp.Phase)
	}
	if len(fp.DesiredClusters) != 2 {
		t.Fatalf("DesiredClusters len = %d, want 2", len(fp.DesiredClusters))
	}
	if fp.Source != "modelplane" {
		t.Fatalf("Source = %q, want 'modelplane'", fp.Source)
	}
}

func TestInferenceClassToGPUType(t *testing.T) {
	ic := InferenceClass{
		Name:    "h200-8gpu",
		GPUType: "H200",
		Count:   8,
		Memory:  141,
	}

	gpuType := InferenceClassToGPUType(ic)

	if gpuType != "H200" {
		t.Fatalf("GPUType = %q, want 'H200'", gpuType)
	}
}
