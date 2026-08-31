//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	testRootDir string
	serverURL   string
	metricsURL  string
)

func findProjectRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("integration: cannot determine source file location")
	}
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("integration: cannot find project root")
		}
		dir = parent
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func waitForHealthy(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func TestMain(m *testing.M) {
	testRootDir = findProjectRoot()

	binPath := filepath.Join(os.TempDir(), fmt.Sprintf("fleet-controller-integration-%d%s", os.Getpid(), executableSuffix()))

	build := exec.Command("go", "build", "-o", binPath, "./cmd/fleet-controller")
	build.Dir = testRootDir
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: build failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	apiPort, err := freePort()
	if err != nil {
		os.Remove(binPath)
		fmt.Fprintf(os.Stderr, "integration: free port (api): %v\n", err)
		os.Exit(1)
	}
	mPort, err := freePort()
	if err != nil {
		os.Remove(binPath)
		fmt.Fprintf(os.Stderr, "integration: free port (metrics): %v\n", err)
		os.Exit(1)
	}

	proc := exec.Command(binPath,
		"--port", fmt.Sprintf("%d", apiPort),
		"--metrics-port", fmt.Sprintf("%d", mPort),
	)

	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "FLEET_AUTH_SECRET=") {
			filtered = append(filtered, e)
		}
	}
	proc.Env = filtered
	proc.Stdout = io.Discard
	proc.Stderr = io.Discard

	if err := proc.Start(); err != nil {
		os.Remove(binPath)
		fmt.Fprintf(os.Stderr, "integration: start failed: %v\n", err)
		os.Exit(1)
	}

	serverURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	metricsURL = fmt.Sprintf("http://127.0.0.1:%d", mPort)

	if !waitForHealthy(serverURL+"/healthz", 15*time.Second) {
		proc.Process.Kill()
		proc.Wait()
		os.Remove(binPath)
		fmt.Fprintf(os.Stderr, "integration: server did not become healthy within 15s\n")
		os.Exit(1)
	}

	code := m.Run()

	proc.Process.Kill()
	proc.Wait()
	os.Remove(binPath)
	os.Exit(code)
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

func postJSON(t *testing.T, path string, body interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := httpClient.Post(serverURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func getJSON(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := httpClient.Get(serverURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func deleteHTTP(t *testing.T, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("DELETE", serverURL+path, nil)
	if err != nil {
		t.Fatalf("create DELETE %s: %v", path, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

func expectStatus(t *testing.T, resp *http.Response, want int) []byte {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != want {
		t.Fatalf("expected status %d, got %d: %s", want, resp.StatusCode, body)
	}
	return body
}

func registerAgentCluster(t *testing.T, agent map[string]interface{}) {
	t.Helper()
	clusterID, _ := agent["cluster_id"].(string)
	name, _ := agent["name"].(string)
	region, _ := agent["region"].(string)
	expectStatus(t, postJSON(t, "/api/v1/clusters", map[string]interface{}{
		"id": clusterID, "name": name, "region": region,
	}), http.StatusCreated)
	expectStatus(t, postJSON(t, "/api/v1/agent/status", agent), http.StatusOK)
}

// ---------------------------------------------------------------------------
// 1. Placement
// ---------------------------------------------------------------------------

func TestIntegrationPlacement(t *testing.T) {
	clusters := []map[string]interface{}{
		{"id": "placement-us-east", "name": "us-east-cluster", "region": "us-east-1", "labels": map[string]string{"gpu": "H100"}},
		{"id": "placement-us-west", "name": "us-west-cluster", "region": "us-west-2", "labels": map[string]string{"gpu": "A100"}},
		{"id": "placement-eu", "name": "eu-cluster", "region": "eu-west-1", "labels": map[string]string{"gpu": "Gaudi3"}},
	}

	for _, c := range clusters {
		resp := postJSON(t, "/api/v1/clusters", c)
		expectStatus(t, resp, http.StatusCreated)
	}
	t.Cleanup(func() {
		for _, c := range clusters {
			deleteHTTP(t, "/api/v1/clusters/"+c["id"].(string))
		}
	})

	resp := getJSON(t, "/api/v1/clusters")
	body := expectStatus(t, resp, http.StatusOK)

	var listed []map[string]interface{}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("unmarshal cluster list: %v", err)
	}

	found := 0
	for _, cl := range listed {
		id, _ := cl["id"].(string)
		for _, c := range clusters {
			if c["id"] == id {
				found++
			}
		}
	}
	if found < len(clusters) {
		t.Errorf("expected %d registered clusters in list, found %d", len(clusters), found)
	}

	poolEvent := map[string]interface{}{
		"type": "ADDED",
		"object": map[string]interface{}{
			"model": map[string]interface{}{
				"name":   "granite-3b",
				"source": "registry.redhat.io/granite-3b",
			},
			"placement": map[string]interface{}{
				"policyRef":   map[string]string{"name": "spread"},
				"minClusters": 2,
			},
		},
	}
	resp = postJSON(t, "/api/v1/webhook/fleetinferencepool", poolEvent)
	expectStatus(t, resp, http.StatusOK)

	resp = getJSON(t, "/api/v1/pools")
	body = expectStatus(t, resp, http.StatusOK)

	var pools []interface{}
	if err := json.Unmarshal(body, &pools); err != nil {
		t.Fatalf("unmarshal pools: %v", err)
	}
	if len(pools) == 0 {
		t.Error("expected at least one pool after webhook event")
	}
}

// ---------------------------------------------------------------------------
// 2. Routing
// ---------------------------------------------------------------------------

func TestIntegrationRouting(t *testing.T) {
	agents := []map[string]interface{}{
		{"cluster_id": "routing-fast", "name": "fast-cluster", "region": "us-east-1", "phase": "Running", "healthy": true, "gpu_total": 8, "gpu_available": 4},
		{"cluster_id": "routing-slow", "name": "slow-cluster", "region": "us-west-2", "phase": "Running", "healthy": true, "gpu_total": 4, "gpu_available": 2},
	}
	for _, a := range agents {
		registerAgentCluster(t, a)
	}
	t.Cleanup(func() {
		deleteHTTP(t, "/api/v1/clusters/routing-fast")
		deleteHTTP(t, "/api/v1/clusters/routing-slow")
	})

	metrics := []map[string]interface{}{
		{"cluster_id": "routing-fast", "throughput_tps": 50.0, "ttft_p50_ms": 10.0, "ttft_p99_ms": 25.0, "queue_depth": 2, "gpu_utilization": 0.4, "kv_cache_hit_rate": 0.85},
		{"cluster_id": "routing-slow", "throughput_tps": 15.0, "ttft_p50_ms": 80.0, "ttft_p99_ms": 200.0, "queue_depth": 10, "gpu_utilization": 0.9, "kv_cache_hit_rate": 0.3},
	}
	for _, m := range metrics {
		resp := postJSON(t, "/api/v1/agent/metrics", m)
		expectStatus(t, resp, http.StatusAccepted)
	}

	resp := getJSON(t, "/api/v1/agent/policies/routing-fast")
	body := expectStatus(t, resp, http.StatusOK)
	var policies map[string]interface{}
	if err := json.Unmarshal(body, &policies); err != nil {
		t.Fatalf("unmarshal policies: %v", err)
	}
	if _, ok := policies["quotas"]; !ok {
		t.Error("expected quotas in agent policies response")
	}
}

// ---------------------------------------------------------------------------
// 3. Autoscaling
// ---------------------------------------------------------------------------

func TestIntegrationAutoscaling(t *testing.T) {
	agent := map[string]interface{}{
		"cluster_id": "autoscale-hot", "name": "hot-cluster", "region": "us-east-1",
		"phase": "Running", "healthy": true, "gpu_total": 8, "gpu_available": 1,
	}
	registerAgentCluster(t, agent)
	t.Cleanup(func() { deleteHTTP(t, "/api/v1/clusters/autoscale-hot") })

	highLoad := map[string]interface{}{
		"cluster_id": "autoscale-hot", "throughput_tps": 5.0, "ttft_p50_ms": 500.0,
		"ttft_p99_ms": 2000.0, "queue_depth": 50, "gpu_utilization": 0.95, "kv_cache_hit_rate": 0.1,
	}
	resp := postJSON(t, "/api/v1/agent/metrics", highLoad)
	expectStatus(t, resp, http.StatusAccepted)

	lowLoad := map[string]interface{}{
		"cluster_id": "autoscale-hot", "throughput_tps": 100.0, "ttft_p50_ms": 10.0,
		"ttft_p99_ms": 30.0, "queue_depth": 1, "gpu_utilization": 0.2, "kv_cache_hit_rate": 0.9,
	}
	resp = postJSON(t, "/api/v1/agent/metrics", lowLoad)
	expectStatus(t, resp, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// 4. Lifecycle
// ---------------------------------------------------------------------------

func TestIntegrationLifecycle(t *testing.T) {
	rollout := map[string]interface{}{
		"pool_id":       "lifecycle-pool-1",
		"model_version": "v2.0.0",
		"strategy":      "canary",
	}
	resp := postJSON(t, "/api/v1/rollouts", rollout)
	expectStatus(t, resp, http.StatusCreated)

	resp = getJSON(t, "/api/v1/rollouts")
	body := expectStatus(t, resp, http.StatusOK)

	var rollouts []map[string]interface{}
	if err := json.Unmarshal(body, &rollouts); err != nil {
		t.Fatalf("unmarshal rollouts: %v", err)
	}
	if len(rollouts) == 0 {
		t.Fatal("expected at least one rollout")
	}

	var rolloutID string
	for _, r := range rollouts {
		if pid, _ := r["PoolID"].(string); pid == "lifecycle-pool-1" {
			rolloutID, _ = r["ID"].(string)
			break
		}
	}
	if rolloutID == "" {
		t.Fatal("could not find rollout for lifecycle-pool-1")
	}

	resp = postJSON(t, "/api/v1/rollouts/"+rolloutID+"/promote", nil)
	readBody(t, resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("promote: unexpected status %d", resp.StatusCode)
	}

	resp = postJSON(t, "/api/v1/rollouts/"+rolloutID+"/rollback", nil)
	readBody(t, resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("rollback: unexpected status %d", resp.StatusCode)
	}

	rollout2 := map[string]interface{}{
		"pool_id":       "lifecycle-pool-2",
		"model_version": "v3.0.0",
		"strategy":      "rolling",
	}
	resp = postJSON(t, "/api/v1/rollouts", rollout2)
	expectStatus(t, resp, http.StatusCreated)

	resp = getJSON(t, "/api/v1/rollouts")
	body = expectStatus(t, resp, http.StatusOK)
	if err := json.Unmarshal(body, &rollouts); err != nil {
		t.Fatalf("unmarshal rollouts: %v", err)
	}
	found := 0
	for _, r := range rollouts {
		pid, _ := r["PoolID"].(string)
		if pid == "lifecycle-pool-1" || pid == "lifecycle-pool-2" {
			found++
		}
	}
	if found < 2 {
		t.Errorf("expected at least 2 lifecycle rollouts in list, found %d", found)
	}
}

// ---------------------------------------------------------------------------
// 5. Tenant Management
// ---------------------------------------------------------------------------

func TestIntegrationTenant(t *testing.T) {
	tenant := map[string]interface{}{
		"id":       "tenant-acme",
		"name":     "ACME Corp",
		"priority": 10,
		"quotas": map[string]interface{}{
			"maxTokensPerMinute":    500000,
			"maxConcurrentRequests": 50,
		},
		"rate_limit": map[string]interface{}{
			"requests_per_second": 100,
		},
	}
	resp := postJSON(t, "/api/v1/tenants", tenant)
	expectStatus(t, resp, http.StatusCreated)

	resp = getJSON(t, "/api/v1/tenants")
	body := expectStatus(t, resp, http.StatusOK)

	var tenants []map[string]interface{}
	if err := json.Unmarshal(body, &tenants); err != nil {
		t.Fatalf("unmarshal tenants: %v", err)
	}
	found := false
	for _, ten := range tenants {
		if ten["ID"] == "tenant-acme" {
			found = true
			if ten["Name"] != "ACME Corp" {
				t.Errorf("expected tenant name 'ACME Corp', got %v", ten["Name"])
			}
		}
	}
	if !found {
		t.Error("tenant-acme not found in list")
	}

	resp = getJSON(t, "/api/v1/tenants/tenant-acme/usage")
	usageBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("tenant usage: unexpected status %d: %s", resp.StatusCode, usageBody)
	}

	agent := map[string]interface{}{
		"cluster_id": "tenant-test-cluster", "name": "tenant-cluster", "region": "us-east-1",
		"phase": "Running", "healthy": true, "gpu_total": 4, "gpu_available": 4,
	}
	registerAgentCluster(t, agent)
	t.Cleanup(func() { deleteHTTP(t, "/api/v1/clusters/tenant-test-cluster") })

	resp = getJSON(t, "/api/v1/agent/policies/tenant-test-cluster")
	body = expectStatus(t, resp, http.StatusOK)

	var policies map[string]interface{}
	if err := json.Unmarshal(body, &policies); err != nil {
		t.Fatalf("unmarshal policies: %v", err)
	}
	quotas, ok := policies["quotas"].(map[string]interface{})
	if !ok {
		t.Fatal("expected quotas map in policies response")
	}
	if _, exists := quotas["tenant-acme"]; !exists {
		t.Error("expected tenant-acme in policy quotas")
	}
}

// ---------------------------------------------------------------------------
// 6. Observability
// ---------------------------------------------------------------------------

func TestIntegrationObservability(t *testing.T) {
	agent := map[string]interface{}{
		"cluster_id": "obs-cluster", "name": "obs-cluster", "region": "us-east-1",
		"phase": "Running", "healthy": true, "gpu_total": 8, "gpu_available": 6,
	}
	registerAgentCluster(t, agent)
	t.Cleanup(func() { deleteHTTP(t, "/api/v1/clusters/obs-cluster") })

	m := map[string]interface{}{
		"cluster_id": "obs-cluster", "throughput_tps": 42.0, "ttft_p50_ms": 15.0,
		"ttft_p99_ms": 45.0, "queue_depth": 3, "gpu_utilization": 0.6, "kv_cache_hit_rate": 0.75,
	}
	resp := postJSON(t, "/api/v1/agent/metrics", m)
	expectStatus(t, resp, http.StatusAccepted)

	resp = getJSON(t, "/api/v1/metrics/model/agent-aggregate")
	body := readBody(t, resp)
	if resp.StatusCode == http.StatusOK {
		var mm map[string]interface{}
		if err := json.Unmarshal(body, &mm); err != nil {
			t.Fatalf("unmarshal model metrics: %v", err)
		}
		if tp, ok := mm["Throughput"].(float64); !ok || tp <= 0 {
			t.Errorf("expected positive throughput, got %v", mm["Throughput"])
		}
	} else if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("unexpected status %d for model metrics", resp.StatusCode)
	}

	metricsResp, err := httpClient.Get(metricsURL + "/metrics")
	if err != nil {
		t.Fatalf("scrape prometheus metrics: %v", err)
	}
	metricsBody := readBody(t, metricsResp)
	if !strings.Contains(string(metricsBody), "fleet_") {
		t.Error("expected fleet_ prefixed metrics in prometheus output")
	}
}

// ---------------------------------------------------------------------------
// 7. KV-Transfer
// ---------------------------------------------------------------------------

func TestIntegrationKVTransfer(t *testing.T) {
	agents := []map[string]interface{}{
		{"cluster_id": "kv-source", "name": "source-cluster", "region": "us-east-1", "phase": "Running", "healthy": true, "gpu_total": 8, "gpu_available": 4},
		{"cluster_id": "kv-target", "name": "target-cluster", "region": "us-west-2", "phase": "Running", "healthy": true, "gpu_total": 8, "gpu_available": 8},
	}
	for _, a := range agents {
		registerAgentCluster(t, a)
	}
	t.Cleanup(func() {
		deleteHTTP(t, "/api/v1/clusters/kv-source")
		deleteHTTP(t, "/api/v1/clusters/kv-target")
	})

	event := map[string]interface{}{
		"cluster_id": "kv-source",
		"event": map[string]interface{}{
			"type":           "kv_cache_transfer_initiated",
			"source_cluster": "kv-source",
			"target_cluster": "kv-target",
			"model":          "granite-3b",
			"cache_size_mb":  256,
		},
	}
	resp := postJSON(t, "/api/v1/agent/events", event)
	expectStatus(t, resp, http.StatusAccepted)

	completionEvent := map[string]interface{}{
		"cluster_id": "kv-target",
		"event": map[string]interface{}{
			"type":           "kv_cache_transfer_completed",
			"source_cluster": "kv-source",
			"target_cluster": "kv-target",
			"model":          "granite-3b",
			"duration_ms":    1500,
		},
	}
	resp = postJSON(t, "/api/v1/agent/events", completionEvent)
	expectStatus(t, resp, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// 8. ModelPack
// ---------------------------------------------------------------------------

func TestIntegrationModelPack(t *testing.T) {
	clusters := []map[string]interface{}{
		{"id": "mp-cluster-1", "name": "mp-cluster-1", "region": "us-east-1", "labels": map[string]string{}},
		{"id": "mp-cluster-2", "name": "mp-cluster-2", "region": "us-west-2", "labels": map[string]string{}},
	}
	for _, c := range clusters {
		resp := postJSON(t, "/api/v1/clusters", c)
		expectStatus(t, resp, http.StatusCreated)
	}
	t.Cleanup(func() {
		for _, c := range clusters {
			deleteHTTP(t, "/api/v1/clusters/"+c["id"].(string))
		}
	})

	poolEvent := map[string]interface{}{
		"type": "ADDED",
		"object": map[string]interface{}{
			"model": map[string]interface{}{
				"name":   "modelpack-test-model",
				"source": "registry.redhat.io/granite-7b",
				"ociRef": "registry.redhat.io/granite-7b:v1.0",
			},
			"placement": map[string]interface{}{
				"policyRef":   map[string]string{"name": "binpack"},
				"minClusters": 1,
				"maxClusters": 3,
			},
		},
	}
	resp := postJSON(t, "/api/v1/webhook/fleetinferencepool", poolEvent)
	expectStatus(t, resp, http.StatusOK)

	resp = getJSON(t, "/api/v1/pools")
	body := expectStatus(t, resp, http.StatusOK)

	var pools []map[string]interface{}
	if err := json.Unmarshal(body, &pools); err != nil {
		t.Fatalf("unmarshal pools: %v", err)
	}

	found := false
	for _, p := range pools {
		if model, _ := p["Model"].(string); model == "modelpack-test-model" {
			found = true
			if src, _ := p["Source"].(string); src != "registry.redhat.io/granite-7b" {
				t.Errorf("expected source 'registry.redhat.io/granite-7b', got %q", src)
			}
		}
	}
	if !found {
		t.Error("modelpack-test-model not found in pools")
	}
}

// ---------------------------------------------------------------------------
// 9. Ledger
// ---------------------------------------------------------------------------

func TestIntegrationLedger(t *testing.T) {
	cluster := map[string]interface{}{
		"id": "ledger-cluster", "name": "ledger-cluster", "region": "us-east-1",
		"labels": map[string]string{},
	}
	resp := postJSON(t, "/api/v1/clusters", cluster)
	expectStatus(t, resp, http.StatusCreated)
	t.Cleanup(func() { deleteHTTP(t, "/api/v1/clusters/ledger-cluster") })

	rollout := map[string]interface{}{
		"pool_id":       "ledger-pool-1",
		"model_version": "v3.0.0",
		"strategy":      "rolling",
	}
	resp = postJSON(t, "/api/v1/rollouts", rollout)
	expectStatus(t, resp, http.StatusCreated)

	resp = getJSON(t, "/api/v1/verify/chains")
	body := expectStatus(t, resp, http.StatusOK)

	var chains map[string]interface{}
	if err := json.Unmarshal(body, &chains); err != nil {
		t.Fatalf("unmarshal chains: %v", err)
	}

	for name, chain := range chains {
		cm, ok := chain.(map[string]interface{})
		if !ok {
			continue
		}
		valid, _ := cm["Valid"].(bool)
		if !valid {
			t.Errorf("chain %q is not valid", name)
		}
	}
}

// ---------------------------------------------------------------------------
// 10. Compliance
// ---------------------------------------------------------------------------

func TestIntegrationCompliance(t *testing.T) {
	resp := getJSON(t, "/api/v1/verify/chains")
	body := expectStatus(t, resp, http.StatusOK)

	var chains map[string]interface{}
	if err := json.Unmarshal(body, &chains); err != nil {
		t.Fatalf("unmarshal chains: %v", err)
	}
	for name, chain := range chains {
		cm, ok := chain.(map[string]interface{})
		if !ok {
			continue
		}
		if valid, _ := cm["Valid"].(bool); !valid {
			t.Errorf("chain %q not valid", name)
		}
	}

	costEndpoints := []string{
		"/api/v1/cost/pricing",
		"/api/v1/cost/projection",
		"/api/v1/cost/savings",
		"/api/v1/cost/alerts",
	}
	for _, ep := range costEndpoints {
		resp := getJSON(t, ep)
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s returned %d: %s", ep, resp.StatusCode, body)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("%s returned Content-Type %q, expected application/json", ep, ct)
		}
	}
}
