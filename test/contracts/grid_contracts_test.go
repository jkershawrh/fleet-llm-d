//go:build contracts

package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Grid CRD Contract Tests (C04-C06)
//
// These tests validate the CRD YAML schemas without requiring a running
// fleet-controller. They verify structural contracts between the CRD
// definitions and the code that consumes them.
// ---------------------------------------------------------------------------

// TestGridSiteCRD_SchemaValid reads api/crds/gridsite.yaml and asserts it
// has the expected CRD structure with all required spec schema fields.
func TestGridSiteCRD_SchemaValid(t *testing.T) {
	specPath := filepath.Join(testRootDir, "api", "crds", "gridsite.yaml")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("failed to read gridsite.yaml: %v", err)
	}
	content := string(data)

	// Top-level CRD structure.
	if !strings.Contains(content, "apiVersion: apiextensions.k8s.io/v1") {
		t.Fatal("gridsite.yaml missing apiVersion: apiextensions.k8s.io/v1")
	}
	if !strings.Contains(content, "kind: CustomResourceDefinition") {
		t.Fatal("gridsite.yaml missing kind: CustomResourceDefinition")
	}
	if !strings.Contains(content, "group: grid.praxis-proxy.io") {
		t.Fatal("gridsite.yaml missing spec.group: grid.praxis-proxy.io")
	}

	// Spec schema fields.
	requiredFields := []string{"gridNetworkRef", "region", "zone", "egress"}
	for _, field := range requiredFields {
		if !strings.Contains(content, field+":") {
			t.Fatalf("gridsite.yaml schema missing required field: %s", field)
		}
	}
}

// TestInferenceProviderCRD_SchemaValid reads api/crds/inferenceprovider.yaml
// and asserts it has the expected CRD structure with all required spec fields.
func TestInferenceProviderCRD_SchemaValid(t *testing.T) {
	specPath := filepath.Join(testRootDir, "api", "crds", "inferenceprovider.yaml")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("failed to read inferenceprovider.yaml: %v", err)
	}
	content := string(data)

	// Top-level CRD structure.
	if !strings.Contains(content, "apiVersion: apiextensions.k8s.io/v1") {
		t.Fatal("inferenceprovider.yaml missing apiVersion: apiextensions.k8s.io/v1")
	}
	if !strings.Contains(content, "kind: CustomResourceDefinition") {
		t.Fatal("inferenceprovider.yaml missing kind: CustomResourceDefinition")
	}
	if !strings.Contains(content, "group: grid.praxis-proxy.io") {
		t.Fatal("inferenceprovider.yaml missing spec.group: grid.praxis-proxy.io")
	}

	// Spec schema fields.
	requiredFields := []string{
		"providerKind",
		"backendKind",
		"models",
		"metricsConfig",
		"siteSelector",
		"matchLabels",
		"matchExpressions",
	}
	for _, field := range requiredFields {
		if !strings.Contains(content, field+":") {
			t.Fatalf("inferenceprovider.yaml schema missing required field: %s", field)
		}
	}
}

// TestGridCRD_APIVersionCorrect verifies that both Grid CRDs use the
// grid.praxis-proxy.io group with v1alpha1 version.
func TestGridCRD_APIVersionCorrect(t *testing.T) {
	crdFiles := []struct {
		name string
		path string
	}{
		{"GridSite", filepath.Join(testRootDir, "api", "crds", "gridsite.yaml")},
		{"InferenceProvider", filepath.Join(testRootDir, "api", "crds", "inferenceprovider.yaml")},
	}

	for _, crd := range crdFiles {
		data, err := os.ReadFile(crd.path)
		if err != nil {
			t.Fatalf("failed to read %s CRD: %v", crd.name, err)
		}
		content := string(data)

		if !strings.Contains(content, "group: grid.praxis-proxy.io") {
			t.Fatalf("%s CRD missing group: grid.praxis-proxy.io", crd.name)
		}
		if !strings.Contains(content, "name: v1alpha1") {
			t.Fatalf("%s CRD missing version: v1alpha1", crd.name)
		}
	}
}
