package modelplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyPlacementAnnotations(t *testing.T) {
	var receivedMethod, receivedPath, receivedContentType string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedContentType = r.Header.Get("Content-Type")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pi := NewPolicyInjector(server.URL, "test-token")
	ctx := context.Background()

	constraints := map[string]string{
		"fleet.llm-d.ai/region":   "us-east-1",
		"fleet.llm-d.ai/gpu-type": "H200",
	}
	err := pi.ApplyPlacementAnnotations(ctx, "granite-deploy", "fleet-ns", constraints)
	if err != nil {
		t.Fatalf("ApplyPlacementAnnotations: %v", err)
	}

	if receivedMethod != http.MethodPatch {
		t.Fatalf("method = %q, want PATCH", receivedMethod)
	}
	if receivedPath != "/apis/modelplane.ai/v1alpha1/namespaces/fleet-ns/modeldeployments/granite-deploy" {
		t.Fatalf("path = %q, unexpected", receivedPath)
	}
	if receivedContentType != "application/merge-patch+json" {
		t.Fatalf("content-type = %q, want 'application/merge-patch+json'", receivedContentType)
	}

	var patch map[string]interface{}
	if err := json.Unmarshal(receivedBody, &patch); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	metadata, ok := patch["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("patch missing 'metadata' object")
	}
	annotations, ok := metadata["annotations"].(map[string]interface{})
	if !ok {
		t.Fatal("patch missing 'metadata.annotations' object")
	}
	if annotations["fleet.llm-d.ai/region"] != "us-east-1" {
		t.Fatalf("annotation region = %v, want 'us-east-1'", annotations["fleet.llm-d.ai/region"])
	}
}

func TestSetReplicaCount(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pi := NewPolicyInjector(server.URL, "test-token")
	ctx := context.Background()

	err := pi.SetReplicaCount(ctx, "granite-deploy", "fleet-ns", 8)
	if err != nil {
		t.Fatalf("SetReplicaCount: %v", err)
	}

	var patch map[string]interface{}
	if err := json.Unmarshal(receivedBody, &patch); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	spec, ok := patch["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("patch missing 'spec' object")
	}
	replicas, ok := spec["replicas"].(float64)
	if !ok {
		t.Fatal("patch missing 'spec.replicas'")
	}
	if int(replicas) != 8 {
		t.Fatalf("replicas = %d, want 8", int(replicas))
	}
}

func TestSetServiceWeights(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pi := NewPolicyInjector(server.URL, "test-token")
	ctx := context.Background()

	weights := map[string]int{
		"canary-ep": 10,
		"stable-ep": 90,
	}
	err := pi.SetServiceWeights(ctx, "granite-svc", "fleet-ns", weights)
	if err != nil {
		t.Fatalf("SetServiceWeights: %v", err)
	}

	var patch map[string]interface{}
	if err := json.Unmarshal(receivedBody, &patch); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	spec, ok := patch["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("patch missing 'spec' object")
	}
	endpoints, ok := spec["endpoints"].([]interface{})
	if !ok {
		t.Fatal("patch missing 'spec.endpoints' array")
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints len = %d, want 2", len(endpoints))
	}

	// Verify total weight sums to 100
	totalWeight := 0
	for _, ep := range endpoints {
		epMap := ep.(map[string]interface{})
		totalWeight += int(epMap["weight"].(float64))
	}
	if totalWeight != 100 {
		t.Fatalf("total weight = %d, want 100", totalWeight)
	}
}
