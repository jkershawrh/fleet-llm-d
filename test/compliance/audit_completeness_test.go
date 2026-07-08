//go:build compliance

// Package compliance verifies that every state-changing operation in fleet-llm-d
// records an entry to the ARE Immutable Ledger.
package compliance

import (
	"context"
	"strings"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

// allExpectedOperationTypes lists every operation type that fleet-llm-d
// must record to the ARE immutable ledger for compliance.
var allExpectedOperationTypes = []string{
	"fleet.placement.assigned",
	"fleet.routing.shifted",
	"fleet.scaling.adjusted",
	"fleet.tenant.usage",
	"fleet.lifecycle.deploy",
	"fleet.kvcache.transferred",
	"fleet.security.auth.failed",
	"fleet.security.rbac.denied",
}

// newAuditTestWorld creates a FleetRecorder backed by an InMemoryLedgerClient,
// similar to the ArchTestWorld used in architecture tests.
func newAuditTestWorld() (*ledger.FleetRecorder, *ledger.InMemoryLedgerClient) {
	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "compliance-agent", "compliance-test")
	return fr, lc
}

// recordAllOperations exercises every state-changing recorder method once.
func recordAllOperations(t *testing.T, ctx context.Context, fr *ledger.FleetRecorder) {
	t.Helper()

	if _, err := fr.RecordPlacement(ctx, "gpt-4", "cluster-us-east", 3, "A100", "lowest utilization"); err != nil {
		t.Fatalf("RecordPlacement: %v", err)
	}
	if _, err := fr.RecordRoutingChange(ctx, "gpt-4", "us-east", "eu-west", 0.3, "latency optimization"); err != nil {
		t.Fatalf("RecordRoutingChange: %v", err)
	}
	if _, err := fr.RecordScalingEvent(ctx, "cluster-1", "llm-pool", 2, 5, "high TTFT"); err != nil {
		t.Fatalf("RecordScalingEvent: %v", err)
	}
	if _, err := fr.RecordTenantUsage(ctx, "tenant-1", "gpt-4", "us-east", 50000, "$0.50"); err != nil {
		t.Fatalf("RecordTenantUsage: %v", err)
	}
	if _, err := fr.RecordLifecycleEvent(ctx, "gpt-4", "v2", "deploy", "us-east", map[string]interface{}{"strategy": "canary"}); err != nil {
		t.Fatalf("RecordLifecycleEvent: %v", err)
	}
	if _, err := fr.RecordKVCacheTransfer(ctx, "us-east", "eu-west", "gpt-4", 1024*1024, "sha256:abc123"); err != nil {
		t.Fatalf("RecordKVCacheTransfer: %v", err)
	}
	if _, err := fr.RecordAuthFailure(ctx, "192.168.1.100", "invalid API key"); err != nil {
		t.Fatalf("RecordAuthFailure: %v", err)
	}
	if _, err := fr.RecordRBACDenial(ctx, "viewer@example.com", "/api/v1/clusters", "DELETE"); err != nil {
		t.Fatalf("RecordRBACDenial: %v", err)
	}
}

func TestAuditCompleteness_AllOperationTypesRecorded(t *testing.T) {
	fr, lc := newAuditTestWorld()
	ctx := context.Background()

	recordAllOperations(t, ctx, fr)

	entries := lc.Entries()
	if len(entries) != len(allExpectedOperationTypes) {
		t.Fatalf("expected %d ledger entries, got %d", len(allExpectedOperationTypes), len(entries))
	}

	// Verify each expected operation type appears exactly once.
	recorded := make(map[string]int)
	for _, e := range entries {
		recorded[e.Type]++
	}

	for _, expectedType := range allExpectedOperationTypes {
		count, ok := recorded[expectedType]
		if !ok || count == 0 {
			t.Errorf("operation type %q was NOT recorded to the ledger", expectedType)
		}
		if count > 1 {
			t.Errorf("operation type %q was recorded %d times (expected 1)", expectedType, count)
		}
	}
}

func TestAuditCompleteness_CorrelationIDsPresent(t *testing.T) {
	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "compliance-agent", "compliance-test")
	ctx := context.Background()

	correlationID := "corr-deploy-gpt4-v2"

	// Record decisions with explicit correlation IDs via the generic method.
	decisions := []ledger.FleetDecision{
		{Type: "fleet.placement.assigned", CorrelationID: correlationID, Content: []byte(`{"model":"gpt-4"}`)},
		{Type: "fleet.routing.shifted", CorrelationID: correlationID, Content: []byte(`{"model":"gpt-4"}`)},
		{Type: "fleet.tenant.usage", CorrelationID: correlationID, Content: []byte(`{"tenant":"t1"}`)},
	}
	for _, d := range decisions {
		if _, err := fr.RecordDecision(ctx, d); err != nil {
			t.Fatalf("RecordDecision(%s): %v", d.Type, err)
		}
	}

	// Verify all entries have the correlation ID set.
	entries := lc.Entries()
	for _, e := range entries {
		if e.CorrelationID != correlationID {
			t.Errorf("entry type %q has correlationID=%q, expected %q", e.Type, e.CorrelationID, correlationID)
		}
	}

	// Verify we can filter entries by correlation ID.
	var correlated []ledger.FleetDecision
	for _, e := range entries {
		if e.CorrelationID == correlationID {
			correlated = append(correlated, e)
		}
	}
	if len(correlated) != 3 {
		t.Fatalf("expected 3 correlated decisions, got %d", len(correlated))
	}
}

func TestAuditCompleteness_VerifyProofRejectsUnknownHash(t *testing.T) {
	lc := ledger.NewInMemoryLedgerClient()

	// Record one decision
	_, err := lc.RecordDecision(context.Background(), ledger.FleetDecision{
		Type:      "fleet.placement.assigned",
		InputHash: "real-hash",
		Content:   []byte(`{"test":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify with a hash that was never recorded
	result, err := lc.VerifyProof(context.Background(), "fake-hash", "fleet.placement.assigned")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Error("VerifyProof should reject unknown hashes for compliance — tampered entries must be detectable")
	}
}

func TestAuditCompleteness_NoMissingOperationTypes(t *testing.T) {
	fr, lc := newAuditTestWorld()
	ctx := context.Background()

	recordAllOperations(t, ctx, fr)

	entries := lc.Entries()
	recorded := make(map[string]bool)
	for _, e := range entries {
		// Normalize lifecycle types: fleet.lifecycle.deploy -> fleet.lifecycle.*
		typ := e.Type
		if strings.HasPrefix(typ, "fleet.lifecycle.") {
			typ = "fleet.lifecycle.*"
		}
		recorded[typ] = true
	}

	// Build the set of required category prefixes.
	requiredPrefixes := []string{
		"fleet.placement.",
		"fleet.routing.",
		"fleet.scaling.",
		"fleet.tenant.",
		"fleet.lifecycle.",
		"fleet.kvcache.",
		"fleet.security.auth.",
		"fleet.security.rbac.",
	}

	for _, prefix := range requiredPrefixes {
		found := false
		for _, e := range entries {
			if strings.HasPrefix(e.Type, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no ledger entry found with prefix %q -- this operation category is NOT audited", prefix)
		}
	}
}
