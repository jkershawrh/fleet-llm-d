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

func TestArenaRouterQualificationIsPinnedAndCoreOnly(t *testing.T) {
	root := findProjectRoot()
	overlay, err := os.ReadFile(filepath.Join(root, "deploy", "llmd-router-beta", "arena", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(overlay)
	for _, required := range []string{
		"digest: sha256:",
		"FLEET_STATIC_PROVIDER_IDS_JSON",
		"oberon-cpu",
		"arena-xeon6",
		"brutus-h100",
		"fleet-router-qualification-to-postgres",
		"$patch: delete",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Arena Router overlay missing %q", required)
		}
	}
	if strings.Contains(content, "newTag: latest") {
		t.Error("Arena Router overlay uses a mutable gateway image tag")
	}

	job, err := os.ReadFile(filepath.Join(root, "deploy", "certification", "arena-router-local", "job.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	jobContent := string(job)
	for _, required := range []string{
		"apps.arena.fm2aihpcsed.com",
		"duration-per-level=900",
		"source-cluster=arena",
		"transport=external-route",
		"fleet-certification@sha256:",
	} {
		if !strings.Contains(jobContent, required) {
			t.Errorf("Arena-local certification Job missing %q", required)
		}
	}
}

func TestArenaRouterDurabilityProfileIsBounded(t *testing.T) {
	path := filepath.Join(findProjectRoot(), "deploy", "certification", "arena-durability", "kustomization.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, required := range []string{
		"activeDeadlineSeconds",
		"value: 36000",
		"--duration-per-level=28800",
		"--request-interval=14",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Arena durability profile missing bound %q", required)
		}
	}
}
