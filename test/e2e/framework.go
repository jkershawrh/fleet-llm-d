package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TestCluster represents a Kind cluster for e2e testing.
type TestCluster struct {
	Name       string
	Kubeconfig string
	Context    string
}

// TestFleet manages a set of Kind clusters for e2e testing.
type TestFleet struct {
	Clusters   []TestCluster
	Controller *TestCluster // which cluster runs the fleet controller
	tmpDir     string
}

// SetupFleet creates Kind clusters and deploys fleet-llm-d.
// The first cluster is designated as the controller (hub) cluster.
func SetupFleet(clusterCount int) (*TestFleet, error) {
	if clusterCount < 1 {
		return nil, fmt.Errorf("cluster count must be at least 1, got %d", clusterCount)
	}

	tmpDir, err := os.MkdirTemp("", "fleet-e2e-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	fleet := &TestFleet{
		Clusters: make([]TestCluster, 0, clusterCount),
		tmpDir:   tmpDir,
	}

	for i := 0; i < clusterCount; i++ {
		name := fmt.Sprintf("fleet-e2e-%d", i)
		kubeconfig := filepath.Join(tmpDir, fmt.Sprintf("kubeconfig-%s.yaml", name))
		kindContext := fmt.Sprintf("kind-%s", name)

		cmd := exec.Command("kind", "create", "cluster",
			"--name", name,
			"--kubeconfig", kubeconfig,
			"--wait", "60s",
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Clean up any clusters already created before returning the error.
			_ = fleet.Teardown()
			return nil, fmt.Errorf("failed to create Kind cluster %s: %w", name, err)
		}

		cluster := TestCluster{
			Name:       name,
			Kubeconfig: kubeconfig,
			Context:    kindContext,
		}
		fleet.Clusters = append(fleet.Clusters, cluster)
	}

	fleet.Controller = &fleet.Clusters[0]
	return fleet, nil
}

// Teardown destroys all Kind clusters and cleans up temporary files.
func (f *TestFleet) Teardown() error {
	var errs []string

	for _, c := range f.Clusters {
		cmd := exec.Command("kind", "delete", "cluster", "--name", c.Name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Sprintf("failed to delete cluster %s: %v", c.Name, err))
		}
	}

	if f.tmpDir != "" {
		if err := os.RemoveAll(f.tmpDir); err != nil {
			errs = append(errs, fmt.Sprintf("failed to remove temp dir %s: %v", f.tmpDir, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// DeployController deploys the fleet-controller to the hub cluster using kustomize.
func (f *TestFleet) DeployController() error {
	if f.Controller == nil {
		return fmt.Errorf("no controller cluster configured")
	}

	cmd := exec.Command("kubectl", "apply",
		"-k", "deploy/kustomize/overlays/hub",
		"--kubeconfig", f.Controller.Kubeconfig,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to deploy controller to %s: %w", f.Controller.Name, err)
	}
	return nil
}

// DeployAgent deploys the fleet-agent to the named spoke cluster.
func (f *TestFleet) DeployAgent(cluster string) error {
	tc, err := f.findCluster(cluster)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "apply",
		"-k", "deploy/kustomize/overlays/spoke",
		"--kubeconfig", tc.Kubeconfig,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to deploy agent to %s: %w", cluster, err)
	}
	return nil
}

// WaitForReady waits for all fleet components to be ready within the given timeout.
// It polls the controller for fleet-controller readiness and each spoke for
// fleet-agent readiness.
func (f *TestFleet) WaitForReady(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Wait for the controller deployment.
	if err := f.waitForDeployment(ctx, f.Controller, "fleet-llm-d", "fleet-controller"); err != nil {
		return fmt.Errorf("controller not ready: %w", err)
	}

	// Wait for the agent deployment on each spoke cluster.
	for _, c := range f.Clusters[1:] {
		if err := f.waitForDeployment(ctx, &c, "fleet-llm-d", "fleet-agent"); err != nil {
			return fmt.Errorf("agent on %s not ready: %w", c.Name, err)
		}
	}

	return nil
}

// waitForDeployment polls until a deployment reaches the Available condition
// or the context deadline is exceeded.
func (f *TestFleet) waitForDeployment(ctx context.Context, tc *TestCluster, namespace, deployment string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for deployment %s/%s on %s", namespace, deployment, tc.Name)
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "kubectl", "rollout", "status",
				fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace,
				"--timeout=5s",
				"--kubeconfig", tc.Kubeconfig,
			)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}
}

// ApplyManifest applies a YAML manifest file to the named cluster.
func (f *TestFleet) ApplyManifest(cluster string, manifest string) error {
	tc, err := f.findCluster(cluster)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "apply",
		"-f", manifest,
		"--kubeconfig", tc.Kubeconfig,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to apply manifest %s to %s: %w", manifest, cluster, err)
	}
	return nil
}

// Kubectl runs a kubectl command against the named cluster and returns
// the combined stdout output.
func (f *TestFleet) Kubectl(cluster string, args ...string) (string, error) {
	tc, err := f.findCluster(cluster)
	if err != nil {
		return "", err
	}

	fullArgs := append([]string{"--kubeconfig", tc.Kubeconfig}, args...)
	cmd := exec.Command("kubectl", fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kubectl command failed on %s: %w\noutput: %s", cluster, err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// findCluster looks up a TestCluster by name.
func (f *TestFleet) findCluster(name string) (*TestCluster, error) {
	for i := range f.Clusters {
		if f.Clusters[i].Name == name {
			return &f.Clusters[i], nil
		}
	}
	return nil, fmt.Errorf("cluster %q not found in fleet", name)
}

// ControllerURL returns the base URL of the fleet-controller running on the hub.
func (f *TestFleet) ControllerURL() string {
	return "http://localhost:8080"
}

func (f *TestFleet) apiGet(path string) (int, map[string]interface{}, error) {
	resp, err := http.Get(f.ControllerURL() + path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	_ = json.Unmarshal(body, &result)
	return resp.StatusCode, result, nil
}

func (f *TestFleet) apiPost(path string, payload interface{}) (int, map[string]interface{}, http.Header, error) {
	data, _ := json.Marshal(payload)
	resp, err := http.Post(f.ControllerURL()+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	_ = json.Unmarshal(body, &result)
	return resp.StatusCode, result, resp.Header, nil
}

// RegisterCluster registers a cluster with the controller.
func (f *TestFleet) RegisterCluster(id, name, region string) error {
	status, _, _, err := f.apiPost("/api/v1/clusters", map[string]interface{}{
		"name": name, "region": region,
	})
	if err != nil {
		return err
	}
	if status != 201 && status != 409 {
		return fmt.Errorf("register cluster returned %d", status)
	}
	return nil
}

// ListClusters returns all registered clusters.
func (f *TestFleet) ListClusters() ([]interface{}, error) {
	resp, err := http.Get(f.ControllerURL() + "/api/v1/clusters")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var clusters []interface{}
	_ = json.Unmarshal(body, &clusters)
	return clusters, nil
}

// CreateTenant creates a tenant with quotas.
func (f *TestFleet) CreateTenant(name string, maxTokensPerHour int) error {
	status, _, _, err := f.apiPost("/api/v1/tenants", map[string]interface{}{
		"name": name,
		"quotas": map[string]interface{}{
			"max_tokens_per_hour": maxTokensPerHour,
		},
	})
	if err != nil {
		return err
	}
	if status != 201 && status != 409 {
		return fmt.Errorf("create tenant returned %d", status)
	}
	return nil
}

// CreateRollout creates a canary rollout.
func (f *TestFleet) CreateRollout(model, version string) (string, error) {
	status, result, _, err := f.apiPost("/api/v1/rollouts", map[string]interface{}{
		"model":   model,
		"version": version,
		"strategy": map[string]interface{}{
			"type":          "Canary",
			"initialWeight": 10,
			"stepWeight":    20,
		},
	})
	if err != nil {
		return "", err
	}
	if status != 201 && status != 200 {
		return "", fmt.Errorf("create rollout returned %d", status)
	}
	id, _ := result["id"].(string)
	return id, nil
}

// PromoteRollout promotes a canary rollout.
func (f *TestFleet) PromoteRollout(id string) error {
	status, _, _, err := f.apiPost(fmt.Sprintf("/api/v1/rollouts/%s/promote", id), nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("promote rollout returned %d", status)
	}
	return nil
}

// DrainCluster initiates a graceful drain.
func (f *TestFleet) DrainCluster(id string) error {
	status, _, _, err := f.apiPost(fmt.Sprintf("/api/v1/clusters/%s/drain", id), nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("drain cluster returned %d", status)
	}
	return nil
}

// SendInference sends a chat completion request and returns response headers.
func (f *TestFleet) SendInference(model, prompt string) (int, http.Header, error) {
	data, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	resp, err := http.Post(f.ControllerURL()+"/v1/chat/completions", "application/json", bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, nil
}

// GetFleetMetrics returns federated fleet metrics.
func (f *TestFleet) GetFleetMetrics() (map[string]interface{}, error) {
	_, result, err := f.apiGet("/api/v1/metrics/fleet")
	return result, err
}

// GetPrometheusMetrics scrapes the controller Prometheus endpoint.
func (f *TestFleet) GetPrometheusMetrics() (string, error) {
	resp, err := http.Get("http://localhost:9091/metrics")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}

// CheckHealth checks the controller health endpoint.
func (f *TestFleet) CheckHealth() error {
	status, _, err := f.apiGet("/healthz")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("healthz returned %d", status)
	}
	return nil
}

// VerifyLedgerChains checks ledger chain integrity.
func (f *TestFleet) VerifyLedgerChains() (bool, error) {
	_, result, err := f.apiGet("/api/v1/verify/chains")
	if err != nil {
		return false, err
	}
	valid, _ := result["all_valid"].(bool)
	return valid, nil
}
