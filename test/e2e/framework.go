package e2e

import (
	"context"
	"fmt"
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
