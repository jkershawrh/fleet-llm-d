//go:build contracts

package contracts

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second}

func postBody(t *testing.T, path string, body interface{}) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := client.Post(serverURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func getPath(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := client.Get(serverURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func deletePath(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", serverURL+path, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func expectJSON(t *testing.T, resp *http.Response, status int) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("expected %d, got %d: %s", status, resp.StatusCode, body)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return result
}

func expectJSONArray(t *testing.T, resp *http.Response, status int) []interface{} {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("expected %d, got %d: %s", status, resp.StatusCode, body)
	}
	var result []interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Placement
// ---------------------------------------------------------------------------

func TestPlacementContract(t *testing.T) {
	requireServer(t)

	t.Run("cluster registration returns status and id", func(t *testing.T) {
		resp := postBody(t, "/api/v1/clusters", map[string]interface{}{
			"id": "contract-place-1", "name": "contract-cluster", "region": "us-east-1", "labels": map[string]string{},
		})
		result := expectJSON(t, resp, http.StatusCreated)
		if result["status"] != "registered" {
			t.Errorf("expected status=registered, got %v", result["status"])
		}
		if result["id"] != "contract-place-1" {
			t.Errorf("expected id=contract-place-1, got %v", result["id"])
		}
		defer deletePath(t, "/api/v1/clusters/contract-place-1")
	})

	t.Run("cluster list returns array", func(t *testing.T) {
		resp := getPath(t, "/api/v1/clusters")
		expectJSONArray(t, resp, http.StatusOK)
	})

	t.Run("pool list returns array", func(t *testing.T) {
		resp := getPath(t, "/api/v1/pools")
		expectJSONArray(t, resp, http.StatusOK)
	})

	t.Run("cluster deregister returns status", func(t *testing.T) {
		postBody(t, "/api/v1/clusters", map[string]interface{}{
			"id": "contract-place-del", "name": "del-cluster", "region": "us-east-1", "labels": map[string]string{},
		})
		resp := deletePath(t, "/api/v1/clusters/contract-place-del")
		result := expectJSON(t, resp, http.StatusOK)
		if result["status"] != "deregistered" {
			t.Errorf("expected status=deregistered, got %v", result["status"])
		}
	})
}

// ---------------------------------------------------------------------------
// Autoscaling
// ---------------------------------------------------------------------------

func TestAutoscalingContract(t *testing.T) {
	requireServer(t)

	t.Run("agent metrics accepted with 202", func(t *testing.T) {
		resp := postBody(t, "/api/v1/agent/metrics", map[string]interface{}{
			"cluster_id": "contract-auto-1", "throughput_tps": 50.0, "ttft_p50_ms": 10.0,
			"ttft_p99_ms": 25.0, "queue_depth": 2, "gpu_utilization": 0.4, "kv_cache_hit_rate": 0.8,
		})
		result := expectJSON(t, resp, http.StatusAccepted)
		if result["status"] != "accepted" {
			t.Errorf("expected status=accepted, got %v", result["status"])
		}
	})

	t.Run("agent metrics rejects negative values", func(t *testing.T) {
		resp := postBody(t, "/api/v1/agent/metrics", map[string]interface{}{
			"cluster_id": "contract-auto-1", "throughput_tps": -1.0, "ttft_p50_ms": 10.0,
			"ttft_p99_ms": 25.0, "queue_depth": 2, "gpu_utilization": 0.4, "kv_cache_hit_rate": 0.8,
		})
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for negative metrics, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

func TestLifecycleContract(t *testing.T) {
	requireServer(t)

	t.Run("rollout creation returns 201 with status", func(t *testing.T) {
		resp := postBody(t, "/api/v1/rollouts", map[string]interface{}{
			"pool_id": "contract-lifecycle-pool", "model_version": "v1.0.0", "strategy": "canary",
		})
		result := expectJSON(t, resp, http.StatusCreated)
		if result["status"] != "created" {
			t.Errorf("expected status=created, got %v", result["status"])
		}
		if result["pool_id"] != "contract-lifecycle-pool" {
			t.Errorf("expected pool_id=contract-lifecycle-pool, got %v", result["pool_id"])
		}
	})

	t.Run("rollout list returns array", func(t *testing.T) {
		resp := getPath(t, "/api/v1/rollouts")
		expectJSONArray(t, resp, http.StatusOK)
	})

	t.Run("rollout creation requires pool_id", func(t *testing.T) {
		resp := postBody(t, "/api/v1/rollouts", map[string]interface{}{
			"model_version": "v1.0.0",
		})
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing pool_id, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// Tenant
// ---------------------------------------------------------------------------

func TestTenantContract(t *testing.T) {
	requireServer(t)

	t.Run("tenant creation returns 201 with id", func(t *testing.T) {
		resp := postBody(t, "/api/v1/tenants", map[string]interface{}{
			"id": "contract-tenant-1", "name": "Contract Tenant", "priority": 5,
		})
		result := expectJSON(t, resp, http.StatusCreated)
		if result["id"] != "contract-tenant-1" {
			t.Errorf("expected id=contract-tenant-1, got %v", result["id"])
		}
		if result["status"] != "created" {
			t.Errorf("expected status=created, got %v", result["status"])
		}
	})

	t.Run("tenant list returns array", func(t *testing.T) {
		resp := getPath(t, "/api/v1/tenants")
		expectJSONArray(t, resp, http.StatusOK)
	})

	t.Run("duplicate tenant returns 409", func(t *testing.T) {
		resp := postBody(t, "/api/v1/tenants", map[string]interface{}{
			"id": "contract-tenant-1", "name": "Duplicate",
		})
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("expected 409 for duplicate tenant, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// Observability
// ---------------------------------------------------------------------------

func TestObservabilityContract(t *testing.T) {
	requireServer(t)

	t.Run("model metrics returns an empty result for unknown model", func(t *testing.T) {
		resp := getPath(t, "/api/v1/metrics/model/granite-3b")
		expectJSON(t, resp, http.StatusOK)
	})

	t.Run("agent status requires registered cluster", func(t *testing.T) {
		// Register the cluster first
		postBody(t, "/api/v1/clusters", map[string]interface{}{
			"id": "contract-obs-1", "name": "obs-cluster", "region": "us-east-1",
		})
		defer deletePath(t, "/api/v1/clusters/contract-obs-1")

		resp := postBody(t, "/api/v1/agent/status", map[string]interface{}{
			"cluster_id": "contract-obs-1", "name": "obs-cluster", "region": "us-east-1",
			"phase": "Running", "healthy": true, "gpu_total": 8, "gpu_available": 6,
		})
		result := expectJSON(t, resp, http.StatusOK)
		if result["status"] != "accepted" {
			t.Errorf("expected status=accepted, got %v", result["status"])
		}
	})
}

// ---------------------------------------------------------------------------
// KV-Transfer
// ---------------------------------------------------------------------------

func TestKVTransferContract(t *testing.T) {
	requireServer(t)

	t.Run("agent event accepted with 202", func(t *testing.T) {
		resp := postBody(t, "/api/v1/agent/events", map[string]interface{}{
			"cluster_id": "contract-kv-src",
			"event": map[string]interface{}{
				"type":           "kv_cache_transfer",
				"source_cluster": "contract-kv-src",
				"target_cluster": "contract-kv-tgt",
			},
		})
		result := expectJSON(t, resp, http.StatusAccepted)
		if result["status"] != "accepted" {
			t.Errorf("expected status=accepted, got %v", result["status"])
		}
	})

	t.Run("agent event rejects missing fields", func(t *testing.T) {
		resp := postBody(t, "/api/v1/agent/events", map[string]interface{}{
			"cluster_id": "",
		})
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing cluster_id, got %d", resp.StatusCode)
		}
	})

	t.Run("agent policies returns quotas shape", func(t *testing.T) {
		resp := getPath(t, "/api/v1/agent/policies/contract-kv-src")
		result := expectJSON(t, resp, http.StatusOK)
		if _, ok := result["quotas"]; !ok {
			t.Error("expected quotas field in policies response")
		}
	})
}

// ---------------------------------------------------------------------------
// ModelPack
// ---------------------------------------------------------------------------

func TestModelPackContract(t *testing.T) {
	requireServer(t)

	t.Run("modelplane clusters returns JSON", func(t *testing.T) {
		resp := getPath(t, "/api/v1/modelplane/clusters")
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("expected application/json, got %q", ct)
		}
	})

	t.Run("modelplane deployments returns JSON", func(t *testing.T) {
		resp := getPath(t, "/api/v1/modelplane/deployments")
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("expected application/json, got %q", ct)
		}
	})

	t.Run("modelplane cost returns JSON", func(t *testing.T) {
		resp := getPath(t, "/api/v1/modelplane/cost/test-deployment")
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("expected application/json, got %q", ct)
		}
	})
}
