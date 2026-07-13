//go:build contracts

package contracts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestOpenAPIV2IntentLifecycleSurface(t *testing.T) {
	endpoints := parseOpenAPISpec(t)
	actual := make(map[string]bool, len(endpoints))
	for _, endpoint := range endpoints {
		actual[endpoint.Method+" "+endpoint.Path] = true
	}

	required := []string{
		"POST /api/v1/intents",
		"POST /api/v2/intents",
		"GET /api/v2/intents/{id}",
		"GET /api/v2/operations/{id}",
		"POST /api/v2/operations/{id}/approve",
		"POST /api/v2/operations/{id}/cancel",
	}
	for _, endpoint := range required {
		if !actual[endpoint] {
			t.Errorf("OpenAPI lifecycle surface missing %s", endpoint)
		}
	}
}

func TestOpenAPIV1IntentSemanticsRemainHonest(t *testing.T) {
	body := readOpenAPIContract(t)
	v1 := extractYAMLBlock(t, body, "  /api/v1/intents:", 2)
	for _, fragment := range []string{
		"deprecated: true",
		"/api/v2/intents",
		"Policy acceptance alone",
		"after the associated operation reached `SUCCEEDED`",
		"#/components/schemas/IntentResponseV1",
	} {
		if !strings.Contains(v1, fragment) {
			t.Errorf("deprecated v1 intent contract missing %q", fragment)
		}
	}

	v1Response := extractYAMLBlock(t, body, "    IntentResponseV1:", 4)
	if !strings.Contains(v1Response, "enum: [deferred, refused, executed]") {
		t.Error("v1 response must expose only deferred, refused, and executed")
	}
	if strings.Contains(v1Response, "enum: [accepted") {
		t.Error("v1 response must not present policy acceptance as a terminal result")
	}
}

func TestOpenAPIV2EvidenceProposerAndLifecycleSchemas(t *testing.T) {
	body := readOpenAPIContract(t)
	requiredSchemas := []string{
		"EvidenceDigest",
		"ProposerAuthority",
		"FleetIntentRequest",
		"OperatorCompatibilityIntentRequest",
		"GCLDecisionPackageCloudEvent",
		"FleetIntent",
		"OperationState",
		"OperationTransition",
		"FleetOperation",
		"SubmissionResponse",
	}
	for _, schema := range requiredSchemas {
		marker := "    " + schema + ":"
		if !strings.Contains(body, marker) {
			t.Errorf("OpenAPI components missing %s", schema)
		}
	}

	evidence := extractYAMLBlock(t, body, "    EvidenceDigest:", 4)
	for _, field := range []string{"required: [uri, sha256]", "pattern: \"^[a-fA-F0-9]{64}$\"", "media_type:"} {
		if !strings.Contains(evidence, field) {
			t.Errorf("EvidenceDigest missing %q", field)
		}
	}

	proposer := extractYAMLBlock(t, body, "    ProposerAuthority:", 4)
	for _, field := range []string{"required: [subject, authority_ref]", "max_action:", "expires_at:"} {
		if !strings.Contains(proposer, field) {
			t.Errorf("ProposerAuthority missing %q", field)
		}
	}

	states := extractYAMLBlock(t, body, "    OperationState:", 4)
	for _, state := range []string{
		"RECEIVED", "ACCEPTED", "PLANNED", "GOVERNANCE_PENDING",
		"AUTHORIZED", "ACTUATING", "OBSERVING", "VERIFIED",
		"EVIDENCE_PENDING", "SUCCEEDED", "REJECTED", "DEFERRED",
		"EXPIRED", "FAILED", "ROLLING_BACK", "ROLLED_BACK", "QUARANTINED",
	} {
		matched, err := regexp.MatchString(`(?m)^\s+- `+regexp.QuoteMeta(state)+`$`, states)
		if err != nil {
			t.Fatal(err)
		}
		if !matched {
			t.Errorf("OperationState missing %s", state)
		}
	}
	for _, statement := range []string{"`SUCCEEDED` is the only successful terminal", "`EVIDENCE_PENDING` means actuation may have occurred"} {
		if !strings.Contains(states, statement) {
			t.Errorf("OperationState honesty contract missing %q", statement)
		}
	}
}

func TestOpenAPIV2ProductionIngressFailsClosed(t *testing.T) {
	body := readOpenAPIContract(t)
	v2 := extractYAMLBlock(t, body, "  /api/v2/intents:", 2)
	for _, fragment := range []string{
		"application/cloudevents+json:",
		"#/components/schemas/GCLDecisionPackageCloudEvent",
		"x-development-only: true",
		"#/components/schemas/OperatorCompatibilityIntentRequest",
		`"415":`,
		`"422":`,
		`"503":`,
	} {
		if !strings.Contains(v2, fragment) {
			t.Errorf("v2 ingress contract missing %q", fragment)
		}
	}
	operator := extractYAMLBlock(t, body, "    OperatorCompatibilityIntentRequest:", 4)
	for _, fragment := range []string{
		"Unsigned, self-asserted operator compatibility input",
		"enum: [deploy, scale, route, pre_warm, shed_load, migrate, kv_transfer]",
		"minimum: 1",
		"minItems: 1",
		"source_cluster",
		"target_cluster",
		"target_pool",
	} {
		if !strings.Contains(operator, fragment) {
			t.Errorf("operator compatibility contract missing %q", fragment)
		}
	}
}

func readOpenAPIContract(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(testRootDir, "api", "openapi", "fleet-api.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

func extractYAMLBlock(t *testing.T, body, marker string, indent int) string {
	t.Helper()
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("OpenAPI document missing marker %q", marker)
	}
	rest := body[start+len(marker):]
	lines := strings.Split(rest, "\n")
	end := len(rest)
	offset := 0
	for _, line := range lines[1:] {
		offset += len(line) + 1
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lineIndent := len(line) - len(strings.TrimLeft(line, " "))
		if lineIndent == indent {
			end = offset
			break
		}
	}
	return rest[:end]
}
