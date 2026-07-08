package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCPUClusterPresent verifies the dev-cluster-1-cpu cluster exists in seed data
// with GPUType "CPU".
func TestCPUClusterPresent(t *testing.T) {
	clusters := seedClusters()
	var found bool
	for _, c := range clusters {
		if c.Name == "dev-cluster-1-cpu" {
			found = true
			if c.Region != "us-east-1" {
				t.Errorf("dev-cluster-1-cpu region = %q, want %q", c.Region, "us-east-1")
			}
			if len(c.Pools) == 0 {
				t.Fatal("dev-cluster-1-cpu has no pools")
			}
			if c.Pools[0].GPUType != "CPU" {
				t.Errorf("dev-cluster-1-cpu pool GPUType = %q, want %q", c.Pools[0].GPUType, "CPU")
			}
			if c.Pools[0].Count != 256 {
				t.Errorf("dev-cluster-1-cpu pool Count = %d, want %d", c.Pools[0].Count, 256)
			}
			if c.Pools[0].Available != 200 {
				t.Errorf("dev-cluster-1-cpu pool Available = %d, want %d", c.Pools[0].Available, 200)
			}
		}
	}
	if !found {
		t.Fatal("dev-cluster-1-cpu cluster not found in seed data")
	}
}

// TestCPUInferenceClassPresent verifies the cpu-intel-amx inference class exists.
func TestCPUInferenceClassPresent(t *testing.T) {
	classes := seedInferenceClasses()
	var found bool
	for _, c := range classes {
		if c.Name == "cpu-intel-amx" {
			found = true
			if c.GPUType != "CPU" {
				t.Errorf("cpu-intel-amx GPUType = %q, want %q", c.GPUType, "CPU")
			}
		}
	}
	if !found {
		t.Fatal("cpu-intel-amx inference class not found in seed data")
	}
}

// TestCPUDeploymentPresent verifies the granite-cpu-fleet deployment exists
// with engine "ovms" targeting the dev-cluster-1-cpu cluster.
func TestCPUDeploymentPresent(t *testing.T) {
	deployments := seedDeployments("default")
	var found bool
	for _, d := range deployments {
		if d.Name == "granite-cpu-fleet" {
			found = true
			if d.Model != "granite-3.2-sovereign" {
				t.Errorf("granite-cpu-fleet model = %q, want %q", d.Model, "granite-3.2-sovereign")
			}
			if d.Engine != "ovms" {
				t.Errorf("granite-cpu-fleet engine = %q, want %q", d.Engine, "ovms")
			}
			if d.Status.ReadyReplicas != 1 {
				t.Errorf("granite-cpu-fleet readyReplicas = %d, want %d", d.Status.ReadyReplicas, 1)
			}
			if len(d.Status.Clusters) == 0 || d.Status.Clusters[0] != "dev-cluster-1-cpu" {
				t.Errorf("granite-cpu-fleet clusters = %v, want [dev-cluster-1-cpu]", d.Status.Clusters)
			}
		}
	}
	if !found {
		t.Fatal("granite-cpu-fleet deployment not found in seed data")
	}
}

// TestCPUEndpointPresent verifies the granite-cpu-dev-cluster-1 endpoint exists.
func TestCPUEndpointPresent(t *testing.T) {
	endpoints := seedEndpoints("default")
	var found bool
	for _, e := range endpoints {
		if e.Name == "granite-cpu-dev-cluster-1" {
			found = true
			if e.Model != "granite-3.2-sovereign" {
				t.Errorf("granite-cpu-dev-cluster-1 model = %q, want %q", e.Model, "granite-3.2-sovereign")
			}
			if e.Cluster != "dev-cluster-1-cpu" {
				t.Errorf("granite-cpu-dev-cluster-1 cluster = %q, want %q", e.Cluster, "dev-cluster-1-cpu")
			}
			if !e.Ready {
				t.Error("granite-cpu-dev-cluster-1 should be ready")
			}
		}
	}
	if !found {
		t.Fatal("granite-cpu-dev-cluster-1 endpoint not found in seed data")
	}
}

// TestCPUClusterHTTPResponse verifies the HTTP endpoint returns the CPU cluster.
func TestCPUClusterHTTPResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/inferenceclusters", func(w http.ResponseWriter, r *http.Request) {
		writeList(w, "InferenceClusterList", seedClusters())
	})

	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/inferenceclusters", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Items []inferenceCluster `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found bool
	for _, c := range resp.Items {
		if c.Name == "dev-cluster-1-cpu" {
			found = true
			if len(c.Pools) == 0 || c.Pools[0].GPUType != "CPU" {
				t.Errorf("dev-cluster-1-cpu pool GPUType via HTTP = %v, want CPU", c.Pools)
			}
		}
	}
	if !found {
		t.Fatal("dev-cluster-1-cpu not found in HTTP response")
	}
}
