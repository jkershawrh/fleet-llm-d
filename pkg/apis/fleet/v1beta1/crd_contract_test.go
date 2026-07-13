package v1beta1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCRDVersionAndStatusContracts(t *testing.T) {
	crdDir := filepath.Join("..", "..", "..", "..", "api", "crds")
	existing := []string{
		"fleetinferencepool.yaml",
		"fleetroutingpolicy.yaml",
		"fleetscalingpolicy.yaml",
		"kvcachetransferpolicy.yaml",
		"modellifecycle.yaml",
		"placementpolicy.yaml",
		"tenantprofile.yaml",
	}
	operational := []string{"fleetcluster.yaml", "fleetintent.yaml", "fleetoperation.yaml"}

	for _, name := range existing {
		t.Run(name, func(t *testing.T) {
			body := readCRD(t, crdDir, name)
			alpha := strings.Index(body, "    - name: v1alpha1")
			beta := strings.Index(body, "    - name: v1beta1")
			if alpha < 0 || beta < 0 || alpha >= beta {
				t.Fatalf("expected served v1alpha1 followed by v1beta1")
			}
			if !strings.Contains(body[alpha:beta], "served: true\n      storage: false") {
				t.Fatal("v1alpha1 must remain served and stop being storage")
			}
			assertBetaStorageAndStatus(t, body[beta:])
		})
	}

	for _, name := range operational {
		t.Run(name, func(t *testing.T) {
			body := readCRD(t, crdDir, name)
			if strings.Contains(body, "name: v1alpha1") {
				t.Fatal("new operational CRDs must not invent an alpha compatibility version")
			}
			beta := strings.Index(body, "    - name: v1beta1")
			if beta < 0 {
				t.Fatal("missing v1beta1")
			}
			assertBetaStorageAndStatus(t, body[beta:])
		})
	}
}

func TestProviderEnumsArePublishedInCRDs(t *testing.T) {
	crdDir := filepath.Join("..", "..", "..", "..", "api", "crds")
	tests := map[string][]string{
		"fleetinferencepool.yaml":    {"enum: [ModelPlane, DirectAgent]", "default: ModelPlane"},
		"fleetroutingpolicy.yaml":    {"enum: [ModelPlaneGateway, FleetGateway]", "default: ModelPlaneGateway"},
		"kvcachetransferpolicy.yaml": {"enum: [LlmDNative, FleetTransfer]", "enum: [GRPC, NIXL]", "enum: [Deny, GRPC]"},
	}
	for name, expected := range tests {
		body := readCRD(t, crdDir, name)
		for _, fragment := range expected {
			if !strings.Contains(body, fragment) {
				t.Errorf("%s missing %q", name, fragment)
			}
		}
	}
}

func TestOperationCreationAndIntentParameterSchemas(t *testing.T) {
	crdDir := filepath.Join("..", "..", "..", "..", "api", "crds")
	operation := readCRD(t, crdDir, "fleetoperation.yaml")
	if !strings.Contains(operation, "required: [intentRef, actionClass, targetRef, idempotencyKey]") {
		t.Fatal("FleetOperation creation schema must require only stable admission fields")
	}
	for _, invariant := range []string{
		"planDigest and provider are required at PLANNED",
		"authorizationRef is required at AUTHORIZED",
	} {
		if !strings.Contains(operation, invariant) {
			t.Errorf("FleetOperation schema missing lifecycle invariant %q", invariant)
		}
	}

	intent := readCRD(t, crdDir, "fleetintent.yaml")
	for _, fragment := range []string{"parameters:", "maxProperties: 64", "x-kubernetes-preserve-unknown-fields: true"} {
		if !strings.Contains(intent, fragment) {
			t.Errorf("FleetIntent parameters schema missing %q", fragment)
		}
	}
}

func TestAuthorizationReferenceSchemasAreCompleteAndBound(t *testing.T) {
	crdDir := filepath.Join("..", "..", "..", "..", "api", "crds")
	crds := []string{
		"fleetcluster.yaml",
		"fleetinferencepool.yaml",
		"fleetintent.yaml",
		"fleetoperation.yaml",
		"fleetroutingpolicy.yaml",
		"fleetscalingpolicy.yaml",
		"kvcachetransferpolicy.yaml",
		"modellifecycle.yaml",
		"placementpolicy.yaml",
		"tenantprofile.yaml",
	}

	for _, name := range crds {
		t.Run(name, func(t *testing.T) {
			body := readCRD(t, crdDir, name)
			beta := strings.Index(body, "    - name: v1beta1")
			if beta < 0 {
				t.Fatal("missing v1beta1 storage schema")
			}
			body = body[beta:]
			authStart := strings.Index(body, "authorizationRef:")
			if authStart < 0 {
				t.Fatal("missing authorizationRef schema")
			}
			statusStart := strings.Index(body[authStart:], "\n            status:")
			if statusStart < 0 {
				t.Fatal("missing status schema after authorizationRef")
			}
			auth := body[authStart : authStart+statusStart]
			properties := strings.Index(auth, "properties:")
			if properties < 0 {
				t.Fatal("authorizationRef schema is missing properties")
			}
			required := auth[:properties]
			for _, field := range []string{"subject", "objectUid"} {
				if !strings.Contains(required, field) {
					t.Errorf("authorizationRef required list is missing %s", field)
				}
			}
			for _, fragment := range []string{
				"subject:",
				"objectUid:",
				"self.audience == 'fleet-llm-d'",
				"size(self.incidentRef) > 0",
			} {
				if !strings.Contains(auth, fragment) {
					t.Errorf("authorizationRef schema is missing %q", fragment)
				}
			}
			for _, fieldPair := range [][2]string{{"subject:", "actionClass:"}, {"objectUid:", "specDigest:"}} {
				fields := auth[properties:]
				start := strings.Index(fields, fieldPair[0])
				if start < 0 {
					t.Errorf("authorizationRef is missing %s", strings.TrimSuffix(fieldPair[0], ":"))
					continue
				}
				end := strings.Index(fields[start:], fieldPair[1])
				if end < 0 || !strings.Contains(fields[start:start+end], "minLength: 1") {
					t.Errorf("authorizationRef.%s must reject empty values", strings.TrimSuffix(fieldPair[0], ":"))
				}
			}
		})
	}

	operation := readCRD(t, crdDir, "fleetoperation.yaml")
	for _, binding := range []string{
		"self.spec.authorizationRef.actionClass == self.spec.actionClass",
		"self.spec.authorizationRef.specDigest == self.spec.planDigest",
		"self.spec.authorizationRef.idempotencyKey == self.spec.idempotencyKey",
		"self.spec.authorizationRef.audience == 'fleet-llm-d'",
	} {
		if !strings.Contains(operation, binding) {
			t.Errorf("FleetOperation schema is missing authorization binding %q", binding)
		}
	}
}

func assertBetaStorageAndStatus(t *testing.T, beta string) {
	t.Helper()
	if !strings.Contains(beta, "served: true\n      storage: true") {
		t.Fatal("v1beta1 must be served and storage")
	}
	if !strings.Contains(beta, "subresources:\n        status: {}") {
		t.Fatal("v1beta1 must expose the status subresource")
	}
	for _, field := range []string{"observedGeneration:", "conditions:", "specDigest:", "providerRefs:", "freshness:", "correlationId:", "lastSuccessfulReconciliation:"} {
		if !strings.Contains(beta, field) {
			t.Errorf("v1beta1 status schema missing %q", field)
		}
	}
}

func readCRD(t *testing.T, dir, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}
