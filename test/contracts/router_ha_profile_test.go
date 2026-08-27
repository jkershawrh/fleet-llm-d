//go:build contracts

package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouterQualificationEPPUsesActivePassiveHA(t *testing.T) {
	root := findProjectRoot()
	for _, pool := range []string{"cpu", "gpu"} {
		path := filepath.Join(root, "deploy", "llmd-router-beta", "values-"+pool+".yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s Router values: %v", pool, err)
		}
		content := string(data)
		for _, required := range []string{
			"replicas: 2",
			"main@sha256:b1eb81f01ea9b56271f0fd536380454d0d82bc03bc59dab7197764da11ffa2ff",
			"pullPolicy: IfNotPresent",
			"preferredDuringSchedulingIgnoredDuringExecution:",
		} {
			if !strings.Contains(content, required) {
				t.Errorf("%s Router values missing HA requirement %q", pool, required)
			}
		}
		if strings.Contains(content, "tag: main\n") {
			t.Errorf("%s Router values use mutable main tag", pool)
		}
	}
}

func TestRouterQualificationEPPHasDisruptionBudgets(t *testing.T) {
	path := filepath.Join(findProjectRoot(), "deploy", "llmd-router-beta", "epp-pdb.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Router EPP PDBs: %v", err)
	}
	content := string(data)
	for _, required := range []string{
		"name: fleet-router-cpu-epp",
		"name: fleet-router-gpu-epp",
		"minAvailable: 1",
		"llm-d-router-standalone: fleet-router-cpu-epp",
		"llm-d-router-standalone: fleet-router-gpu-epp",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Router EPP disruption budgets missing %q", required)
		}
	}
}
