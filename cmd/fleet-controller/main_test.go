package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/routing"
)

// newTestController creates a minimal FleetController for testing route setup.
func newTestController() *FleetController {
	return NewFleetController("", "http://localhost:8000", "http://localhost:8080", "", "")
}

// routeExists sends a request to the mux and returns true when the mux
// dispatches it to a real handler (i.e. status != 404 && status != 405).
func routeExists(mux *http.ServeMux, method, path string) bool {
	var body *strings.Reader
	if method == "POST" {
		body = strings.NewReader("{}")
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	// 404 means the route is not mounted at all.
	return rr.Code != http.StatusNotFound
}

// ---------------------------------------------------------------------------
// Tests for the --mode flag behaviour
// ---------------------------------------------------------------------------

func TestSetupAPIServer_ModeAll_MountsBothControlAndInference(t *testing.T) {
	fc := newTestController()
	mux := fc.setupAPIServer("all")

	// Control plane routes should be present.
	if !routeExists(mux, "GET", "/api/v1/clusters") {
		t.Error("mode=all: expected /api/v1/clusters to be mounted")
	}
	if !routeExists(mux, "GET", "/api/v1/pools") {
		t.Error("mode=all: expected /api/v1/pools to be mounted")
	}

	// Inference proxy routes should be present.
	if !routeExists(mux, "POST", "/v1/chat/completions") {
		t.Error("mode=all: expected /v1/chat/completions to be mounted")
	}
	if !routeExists(mux, "POST", "/v1/completions") {
		t.Error("mode=all: expected /v1/completions to be mounted")
	}

	// Health probes should always be present.
	if !routeExists(mux, "GET", "/healthz") {
		t.Error("mode=all: expected /healthz to be mounted")
	}
	if !routeExists(mux, "GET", "/readyz") {
		t.Error("mode=all: expected /readyz to be mounted")
	}
}

func TestSetupAPIServer_ModeControl_OnlyMountsControlRoutes(t *testing.T) {
	fc := newTestController()
	mux := fc.setupAPIServer("control")

	// Control plane routes should be present.
	if !routeExists(mux, "GET", "/api/v1/clusters") {
		t.Error("mode=control: expected /api/v1/clusters to be mounted")
	}
	if !routeExists(mux, "GET", "/api/v1/pools") {
		t.Error("mode=control: expected /api/v1/pools to be mounted")
	}
	if !routeExists(mux, "GET", "/api/v1/tenants") {
		t.Error("mode=control: expected /api/v1/tenants to be mounted")
	}
	if !routeExists(mux, "GET", "/api/v1/rollouts") {
		t.Error("mode=control: expected /api/v1/rollouts to be mounted")
	}

	// Inference proxy routes should NOT be present.
	if routeExists(mux, "POST", "/v1/chat/completions") {
		t.Error("mode=control: expected /v1/chat/completions to NOT be mounted")
	}
	if routeExists(mux, "POST", "/v1/completions") {
		t.Error("mode=control: expected /v1/completions to NOT be mounted")
	}

	// Health probes should always be present.
	if !routeExists(mux, "GET", "/healthz") {
		t.Error("mode=control: expected /healthz to be mounted")
	}
}

func TestSetupAPIServer_ModeInference_OnlyMountsInferenceRoutes(t *testing.T) {
	fc := newTestController()
	mux := fc.setupAPIServer("inference")

	// Control plane routes should NOT be present.
	if routeExists(mux, "GET", "/api/v1/clusters") {
		t.Error("mode=inference: expected /api/v1/clusters to NOT be mounted")
	}
	if routeExists(mux, "GET", "/api/v1/pools") {
		t.Error("mode=inference: expected /api/v1/pools to NOT be mounted")
	}
	if routeExists(mux, "GET", "/api/v1/tenants") {
		t.Error("mode=inference: expected /api/v1/tenants to NOT be mounted")
	}
	if routeExists(mux, "GET", "/api/v1/rollouts") {
		t.Error("mode=inference: expected /api/v1/rollouts to NOT be mounted")
	}

	// Inference proxy routes should be present.
	if !routeExists(mux, "POST", "/v1/chat/completions") {
		t.Error("mode=inference: expected /v1/chat/completions to be mounted")
	}
	if !routeExists(mux, "POST", "/v1/completions") {
		t.Error("mode=inference: expected /v1/completions to be mounted")
	}

	// Health probes should always be present.
	if !routeExists(mux, "GET", "/healthz") {
		t.Error("mode=inference: expected /healthz to be mounted")
	}
	if !routeExists(mux, "GET", "/readyz") {
		t.Error("mode=inference: expected /readyz to be mounted")
	}
}

func TestSetupAPIServer_ModeControl_CostEndpointsMounted(t *testing.T) {
	fc := newTestController()
	mux := fc.setupAPIServer("control")

	costRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/cost/pricing"},
		{"GET", "/api/v1/cost/projection"},
		{"GET", "/api/v1/cost/savings"},
		{"GET", "/api/v1/cost/alerts"},
	}
	for _, r := range costRoutes {
		if !routeExists(mux, r.method, r.path) {
			t.Errorf("mode=control: expected %s %s to be mounted", r.method, r.path)
		}
	}
}

func TestSetupAPIServer_ModeInference_CostEndpointsNotMounted(t *testing.T) {
	fc := newTestController()
	mux := fc.setupAPIServer("inference")

	costRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/cost/pricing"},
		{"GET", "/api/v1/cost/projection"},
		{"GET", "/api/v1/cost/savings"},
		{"GET", "/api/v1/cost/alerts"},
	}
	for _, r := range costRoutes {
		if routeExists(mux, r.method, r.path) {
			t.Errorf("mode=inference: expected %s %s to NOT be mounted", r.method, r.path)
		}
	}
}

func TestSetupAPIServer_HealthAlwaysMounted(t *testing.T) {
	fc := newTestController()

	for _, mode := range []string{"all", "control", "inference"} {
		mux := fc.setupAPIServer(mode)

		if !routeExists(mux, "GET", "/healthz") {
			t.Errorf("mode=%s: expected /healthz to be mounted", mode)
		}
		if !routeExists(mux, "GET", "/readyz") {
			t.Errorf("mode=%s: expected /readyz to be mounted", mode)
		}
	}
}

func TestBackendsFlag_RegistersCustomBackends(t *testing.T) {
	backendsJSON := `[{"model":"test-model","url":"http://test:8000","runtime":"openvino","path_prefix":"/v3"}]`

	fc := NewFleetController("", "http://unused:8000", "http://unused:8080", "", "")

	var backendList []struct {
		Model      string `json:"model"`
		URL        string `json:"url"`
		Runtime    string `json:"runtime"`
		PathPrefix string `json:"path_prefix"`
	}
	if err := json.Unmarshal([]byte(backendsJSON), &backendList); err != nil {
		t.Fatal(err)
	}
	for _, b := range backendList {
		fc.InferenceProxy.RegisterBackend(b.Model, routing.Backend{
			Name:       b.Runtime + "-" + b.Model,
			URL:        b.URL,
			Runtime:    b.Runtime,
			PathPrefix: b.PathPrefix,
			Healthy:    true,
		})
	}

	backend, _, err := fc.InferenceProxy.SelectBackend("test-model", http.Header{})
	if err != nil {
		t.Fatalf("custom backend not found: %v", err)
	}
	if backend.URL != "http://test:8000" {
		t.Errorf("expected URL http://test:8000, got %s", backend.URL)
	}
	if backend.Runtime != "openvino" {
		t.Errorf("expected runtime openvino, got %s", backend.Runtime)
	}
}
