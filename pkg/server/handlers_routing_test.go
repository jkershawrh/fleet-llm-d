package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestClassifyAndRoute_EmptyText_Returns400(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	body := `{"text":"","model":"granite-2b"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/route", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	var errResp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if errResp["error"] == "" {
		t.Fatal("expected error field in JSON response")
	}
}

func TestClassifyAndRoute_NoHealthyClusters_Returns503JSON(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	body := `{"text":"explain quantum computing","model":"granite-8b"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/route", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var errResp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("503 response should be JSON, not plain text: %v", err)
	}
}

func TestClassifyAndRoute_WithHealthyCluster_ReturnsTarget(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{
		ID: "east-1", Name: "east-1", Region: "us-east", Status: "Running",
		GPUAvailable: 4, GPUTotal: 8,
	})

	body := `{"text":"what is 2+2","model":"granite-2b"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/route", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp routeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.TargetCluster != "east-1" {
		t.Fatalf("expected target_cluster east-1, got %q", resp.TargetCluster)
	}
	if resp.Reason == "" {
		t.Fatal("expected non-empty reason")
	}
}
