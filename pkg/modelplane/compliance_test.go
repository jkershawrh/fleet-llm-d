package modelplane

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

func TestRecordClusterProvisioned(t *testing.T) {
	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "modelplane-test")
	bridge := NewComplianceBridge(fr)
	ctx := context.Background()

	cluster := InferenceCluster{
		Name:     "prod-east",
		Region:   "us-east-1",
		Provider: "gke",
		Status:   ClusterStatus{Phase: "Ready", Nodes: 10},
		Pools:    []NodePool{{Name: "pool-1", GPUType: "H200", Count: 8, Available: 6}},
	}

	receipt, err := bridge.RecordClusterProvisioned(ctx, cluster)
	if err != nil {
		t.Fatalf("RecordClusterProvisioned: %v", err)
	}
	if receipt == nil {
		t.Fatal("receipt should not be nil")
	}
	if receipt.EntryID == "" {
		t.Fatal("receipt.EntryID should not be empty")
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(entries))
	}
	if entries[0].Type != "modelplane.cluster.provisioned" {
		t.Fatalf("type = %q, want 'modelplane.cluster.provisioned'", entries[0].Type)
	}

	// Verify content includes cluster details
	var content map[string]interface{}
	if err := json.Unmarshal(entries[0].Content, &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content["cluster"] != "prod-east" {
		t.Fatalf("content cluster = %v, want 'prod-east'", content["cluster"])
	}
	if content["region"] != "us-east-1" {
		t.Fatalf("content region = %v, want 'us-east-1'", content["region"])
	}
}

func TestRecordDeploymentCreated(t *testing.T) {
	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "modelplane-test")
	bridge := NewComplianceBridge(fr)
	ctx := context.Background()

	deployment := ModelDeployment{
		Name:      "granite-deploy",
		Namespace: "fleet-ns",
		Model:     "granite-3b",
		Engine:    "vllm",
		Replicas:  4,
		Status: DeploymentStatus{
			Phase:         "Running",
			ReadyReplicas: 4,
			Clusters:      []string{"us-east", "eu-west"},
		},
	}

	receipt, err := bridge.RecordDeploymentCreated(ctx, deployment)
	if err != nil {
		t.Fatalf("RecordDeploymentCreated: %v", err)
	}
	if receipt == nil {
		t.Fatal("receipt should not be nil")
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(entries))
	}
	if entries[0].Type != "modelplane.deployment.created" {
		t.Fatalf("type = %q, want 'modelplane.deployment.created'", entries[0].Type)
	}

	var content map[string]interface{}
	if err := json.Unmarshal(entries[0].Content, &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content["deployment"] != "granite-deploy" {
		t.Fatalf("content deployment = %v, want 'granite-deploy'", content["deployment"])
	}
	if content["model"] != "granite-3b" {
		t.Fatalf("content model = %v, want 'granite-3b'", content["model"])
	}
}

func TestRecordDeploymentScaled(t *testing.T) {
	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "modelplane-test")
	bridge := NewComplianceBridge(fr)
	ctx := context.Background()

	deployment := ModelDeployment{
		Name:      "granite-deploy",
		Namespace: "fleet-ns",
		Model:     "granite-3b",
		Engine:    "vllm",
		Replicas:  8, // new replicas
	}

	receipt, err := bridge.RecordDeploymentScaled(ctx, deployment, 4, 8)
	if err != nil {
		t.Fatalf("RecordDeploymentScaled: %v", err)
	}
	if receipt == nil {
		t.Fatal("receipt should not be nil")
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(entries))
	}
	if entries[0].Type != "modelplane.deployment.scaled" {
		t.Fatalf("type = %q, want 'modelplane.deployment.scaled'", entries[0].Type)
	}

	var content map[string]interface{}
	if err := json.Unmarshal(entries[0].Content, &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content["old_replicas"].(float64) != 4 {
		t.Fatalf("old_replicas = %v, want 4", content["old_replicas"])
	}
	if content["new_replicas"].(float64) != 8 {
		t.Fatalf("new_replicas = %v, want 8", content["new_replicas"])
	}
}
