package actuator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScaleDeployment(t *testing.T) {
	var receivedMethod, receivedPath, receivedContentType, receivedAuth string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedContentType = r.Header.Get("Content-Type")
		receivedAuth = r.Header.Get("Authorization")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	actuator := NewModelPlaneActuator(server.URL, "test-token")
	ctx := context.Background()

	err := actuator.ScaleDeployment(ctx, "granite-deploy", "fleet-ns", 8)
	if err != nil {
		t.Fatalf("ScaleDeployment: %v", err)
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
	if receivedAuth != "Bearer test-token" {
		t.Fatalf("auth = %q, want 'Bearer test-token'", receivedAuth)
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

func TestScaleDeployment_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	actuator := NewModelPlaneActuator(server.URL, "test-token")
	ctx := context.Background()

	err := actuator.ScaleDeployment(ctx, "granite-deploy", "fleet-ns", 8)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}
