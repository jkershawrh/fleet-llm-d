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
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/classifier"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

type staticClassifier struct{ label string }

func (s staticClassifier) Classify(context.Context, string, string) (*classifier.ClassifyResult, error) {
	return &classifier.ClassifyResult{TopLabel: s.label, TopScore: 0.99, Margin: 0.9}, nil
}

func (staticClassifier) Close() error { return nil }

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
		ID: "cluster-b", Name: "cluster-b", Status: "Running",
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"logical-model","tenant_id":"tenant-a","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("X-Fleet-Target-Cluster", "gpu-site")
	req.Header.Set("X-Request-ID", "test-request")
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected upstream status, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotHeader != "cluster-b" || rr.Header().Get("X-Fleet-Routed-To") != "cluster-b" {
		t.Fatalf("spoofed target was not replaced: upstream=%q response=%q", gotHeader, rr.Header().Get("X-Fleet-Routed-To"))
	}
	if gotModel != defaultCPUModel || rr.Header().Get("X-Fleet-Actual-Model") != defaultCPUModel {
		t.Fatalf("physical model not rewritten: body=%q header=%q", gotModel, rr.Header().Get("X-Fleet-Actual-Model"))
	}
	if got := rr.Header().Get("X-Fleet-Data-Plane"); got != string(InferenceProviderPraxis) {
		t.Fatalf("data plane = %q", got)
	}
}

func TestInferenceGatewayUsesRouterPoolAndStripsInternalHeaders(t *testing.T) {
	var gotCluster, gotDestination, gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCluster = r.Header.Get("X-Fleet-Target-Cluster")
		gotDestination = r.Header.Get("X-Gateway-Destination-Endpoint")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = body["model"].(string)
		w.Header().Set("X-Fleet-Router-Upstream", "ovms.example:443")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	fc := newTestFleetController(t)
	fc.InferenceProviderName = InferenceProviderLLMD
	fc.LLMDCPUURL = upstream.URL
	fc.RouterUpstreamClusters = map[string]string{"ovms.example:443": "cpu-provider-a"}
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("X-Fleet-Target-Cluster", "gpu-provider-a")
	req.Header.Set("X-Gateway-Destination-Endpoint", "attacker.invalid:443")
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotCluster != "cpu-provider-a" || gotDestination != "" || gotModel != defaultCPUModel {
		t.Fatalf("sanitization/routing mismatch: cluster=%q destination=%q model=%q", gotCluster, gotDestination, gotModel)
	}
	if got := rr.Header().Get("X-Fleet-Data-Plane"); got != string(InferenceProviderLLMD) {
		t.Fatalf("data plane = %q", got)
	}
}

func TestInferenceGatewayRejectsUnknownRouterExecutionEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Fleet-Router-Upstream", "attacker.invalid:443")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"must not pass"}}]}`)
	}))
	defer upstream.Close()
	fc := newTestFleetController(t)
	fc.InferenceProviderName = InferenceProviderLLMD
	fc.LLMDCPUURL = upstream.URL
	fc.RouterUpstreamClusters = map[string]string{"ovms.example:443": "cpu-provider-a"}
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "routing_evidence_mismatch") || strings.Contains(rr.Body.String(), "must not pass") {
		t.Fatalf("expected evidence mismatch 502, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayRejectsMissingRouterExecutionEvidenceOnSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"must not pass"}}]}`)
	}))
	defer upstream.Close()
	fc := newTestFleetController(t)
	fc.InferenceProviderName = InferenceProviderLLMD
	fc.LLMDCPUURL = upstream.URL
	fc.RouterUpstreamClusters = map[string]string{"ovms.example:443": "cpu-provider-a"}
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "routing_evidence_mismatch") || strings.Contains(rr.Body.String(), "must not pass") {
		t.Fatalf("expected missing evidence to fail closed, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayPreservesRouterFailureWithoutExecutionEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"router failed before provider selection"}`)
	}))
	defer upstream.Close()
	fc := newTestFleetController(t)
	fc.InferenceProviderName = InferenceProviderLLMD
	fc.LLMDCPUURL = upstream.URL
	fc.RouterUpstreamClusters = map[string]string{"ovms.example:443": "cpu-provider-a"}
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), "routing_evidence_mismatch") {
		t.Fatalf("expected original Router 502 without an evidence mismatch, got %d: %s", rr.Code, rr.Body.String())
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
	for _, cluster := range []string{"cpu-provider-a", testGPUProvider} {
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
	if gotCluster != testGPUProvider || gotModel != defaultGPUModel {
		t.Fatalf("GPU request downgraded: cluster=%q model=%q", gotCluster, gotModel)
	}
	if got := rr.Header().Get("X-Fleet-Routing-Reason"); got != "explicit-model" {
		t.Fatalf("routing reason = %q", got)
	}
}

func TestInferenceGatewayExactGPUModelUnavailableIsStructured503(t *testing.T) {
	fc := newTestFleetController(t)
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
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

func TestInferenceGatewayGPUAliasCannotDowngradeToCPU(t *testing.T) {
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
	for _, cluster := range []string{"cpu-provider-a", testGPUProvider} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: cluster, Name: cluster, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-8b-gpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotCluster != testGPUProvider || gotModel != defaultGPUModel {
		t.Fatalf("GPU alias downgraded: cluster=%q model=%q", gotCluster, gotModel)
	}
}

func TestInferenceGatewaySemanticEscalationPinsGPUToExactProvider(t *testing.T) {
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
	fc.Routing.ClassifierClient = staticClassifier{label: "REASONING"}
	for _, cluster := range []string{"cpu-provider-a", testGPUProvider} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: cluster, Name: cluster, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"messages":[{"role":"user","content":"reason carefully"}],"session_id":"session-1"}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotCluster != testGPUProvider || gotModel != defaultGPUModel {
		t.Fatalf("semantic GPU escalation mismatched: cluster=%q model=%q", gotCluster, gotModel)
	}
	if got := rr.Header().Get("X-Fleet-Routing-Reason"); got != "semantic-escalation" {
		t.Fatalf("routing reason = %q", got)
	}
}

func TestInferenceGatewayExactCPUModelCannotEscalateToGPU(t *testing.T) {
	fc := newTestFleetController(t)
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: testGPUProvider, Name: testGPUProvider, Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "no_compatible_capacity") {
		t.Fatalf("expected CPU-only structured 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayDistributesAcrossHealthyCPUProviders(t *testing.T) {
	fc := newTestFleetController(t)
	for _, cluster := range []string{"cpu-provider-a", "cpu-provider-b"} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: cluster, Name: cluster, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	first := fc.nextHealthyProvider(context.Background(), fc.cpuPhysicalModel())
	second := fc.nextHealthyProvider(context.Background(), fc.cpuPhysicalModel())
	if first != "cpu-provider-a" || second != "cpu-provider-b" {
		t.Fatalf("CPU distribution = %q, %q", first, second)
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

func TestInferenceGatewayEnforcesTenantQuota(t *testing.T) {
	fc := newTestFleetController(t)
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"tenant_id":"tenant-a","model":"granite-2b-cpu","max_tokens":2000,"messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests || !strings.Contains(rr.Body.String(), "quota_exceeded") {
		t.Fatalf("expected quota 429, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayMapsTimeoutTo504(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	fc := newTestFleetController(t)
	fc.PraxisURL = upstream.URL
	fc.InferenceClient = &http.Client{Timeout: 10 * time.Millisecond}
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusGatewayTimeout || !strings.Contains(rr.Body.String(), "upstream_timeout") {
		t.Fatalf("expected structured 504, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayNormalizesProvider503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "<html>router unavailable</html>")
	}))
	defer upstream.Close()
	fc := newTestFleetController(t)
	fc.PraxisURL = upstream.URL
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: "cpu-provider-a", Name: "cpu-provider-a", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "no_compatible_capacity") || strings.Contains(rr.Body.String(), "<html>") {
		t.Fatalf("expected structured 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInferenceGatewayRetriesAlternateCPUProviderBeforeHeaders(t *testing.T) {
	var targets []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, r.Header.Get("X-Fleet-Target-Cluster"))
		if len(targets) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()

	fc := newTestFleetController(t)
	fc.PraxisURL = upstream.URL
	for _, cluster := range []string{"cpu-provider-a", "cpu-provider-b"} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: cluster, Name: cluster, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"granite-2b-cpu","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected retry success, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(targets) != 2 || targets[0] == targets[1] {
		t.Fatalf("retry targets = %v", targets)
	}
	if got := rr.Header().Get("X-Fleet-Routed-To"); got != targets[1] {
		t.Fatalf("routed-to = %q, retry target = %q", got, targets[1])
	}
	if got := rr.Header().Get("X-Fleet-Routing-Reason"); got != "health-failover" {
		t.Fatalf("routing reason = %q", got)
	}
}

func TestInferenceGatewayDoesNotRetryExactGPUProvider(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	fc := newTestFleetController(t)
	fc.PraxisURL = upstream.URL
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: testGPUProvider, Name: testGPUProvider, Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"ibm-granite/granite-3.1-8b-instruct","messages":[{"role":"user","content":"hello"}]}`))
	rr := httptest.NewRecorder()
	fc.SetupRoutes("inference").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway || attempts != 1 {
		t.Fatalf("status = %d, attempts = %d", rr.Code, attempts)
	}
}
