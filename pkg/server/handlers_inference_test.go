package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestInferenceGatewayRoutesRewritesAndSanitizes(t *testing.T) {
	var gotHeader, gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Fleet-Target-Cluster")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = body["model"].(string)
		if _, exists := body["tenant_id"]; exists {
			t.Fatal("tenant metadata leaked to upstream body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"response"}`)
	}))
	defer upstream.Close()

	fc := newTestFleetController(t)
	fc.PraxisURL = upstream.URL
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{
		ID: "arena", Name: "arena", Status: "Running",
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"logical-model","tenant_id":"tenant-a","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("X-Fleet-Target-Cluster", "brutus")
	req.Header.Set("X-Request-ID", "test-request")
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected upstream status, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotHeader != "arena" || rr.Header().Get("X-Fleet-Routed-To") != "arena" {
		t.Fatalf("spoofed target was not replaced: upstream=%q response=%q", gotHeader, rr.Header().Get("X-Fleet-Routed-To"))
	}
	if gotModel != defaultCPUModel || rr.Header().Get("X-Fleet-Actual-Model") != defaultCPUModel {
		t.Fatalf("physical model not rewritten: body=%q header=%q", gotModel, rr.Header().Get("X-Fleet-Actual-Model"))
	}
}

func TestInferenceGatewayNoCapacityIsStructured503(t *testing.T) {
	fc := newTestFleetController(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", bytes.NewBufferString(`{"model":"x","prompt":"hello"}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Error map[string]string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error["code"] != "no_compatible_capacity" || payload.Error["request_id"] == "" {
		t.Fatalf("unexpected error: %#v", payload.Error)
	}
}

func TestInferenceGatewayExactGPUModelCannotDowngrade(t *testing.T) {
	var gotCluster, gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCluster = r.Header.Get("X-Fleet-Target-Cluster")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = body["model"].(string)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	fc := newTestFleetController(t)
	fc.PraxisURL = upstream.URL
	for _, cluster := range []string{"oberon-cpu", brutusGPUCluster} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: cluster, Name: cluster, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"ibm-granite/granite-3.1-8b-instruct","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotCluster != brutusGPUCluster || gotModel != defaultGPUModel {
		t.Fatalf("GPU request downgraded: cluster=%q model=%q", gotCluster, gotModel)
	}
	if got := rr.Header().Get("X-Fleet-Routing-Reason"); got != "explicit-model" {
		t.Fatalf("routing reason = %q", got)
	}
}

func TestInferenceGatewayExactGPUModelUnavailableIsStructured503(t *testing.T) {
	fc := newTestFleetController(t)
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "oberon-cpu", Name: "oberon-cpu", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"ibm-granite/granite-3.1-8b-instruct","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "no_compatible_capacity") {
		t.Fatalf("expected structured 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayRejectsInvalidPayloads(t *testing.T) {
	fc := newTestFleetController(t)
	tests := []struct{ path, body string }{
		{"/v1/chat/completions", `{"messages":[]}`},
		{"/v1/completions", `{"prompt":""}`},
		{"/v1/completions", `{`},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
		rr := httptest.NewRecorder()
		fc.SetupRoutes("inference").ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", tc.path, rr.Code)
		}
	}
}
