package server

import (
	"context"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestModelProviderMappingIsExactAndDeterministic(t *testing.T) {
	fc := newTestFleetController(t)
	fc.ModelProviderClusters = map[string][]string{
		"model-a": {"cluster-b", "cluster-a"},
		"model-b": {"cluster-gpu"},
	}
	for _, id := range []string{"cluster-a", "cluster-b", "cluster-gpu"} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: id, Name: id, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := fc.providersForModel("model-a"); len(got) != 2 || got[0] != "cluster-a" || got[1] != "cluster-b" {
		t.Fatalf("providers = %v", got)
	}
	if fc.providerServesModel("cluster-gpu", "model-a") {
		t.Fatal("provider from another exact-model pool was admitted")
	}
	if first, second := fc.nextHealthyProvider(context.Background(), "model-a"), fc.nextHealthyProvider(context.Background(), "model-a"); first != "cluster-a" || second != "cluster-b" {
		t.Fatalf("round robin = %q, %q", first, second)
	}
	if got := fc.nextHealthyProvider(context.Background(), "unknown"); got != "" {
		t.Fatalf("unknown model selected %q", got)
	}
}
