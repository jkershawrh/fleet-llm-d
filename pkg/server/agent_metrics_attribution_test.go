package server

import (
	"context"
	"net/http"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// TestAgentMetricsReachTheAutoscaler covers the seam between the agent
// ingestion handler (the only production writer of cluster metrics) and
// metricsForPool (the autoscaler's reader).
//
// Both sides were previously unit-tested in isolation with fixtures that
// agreed with each other, while in production the writer stored every sample
// under the literal pool name "agent-aggregate" and the reader looked it up
// by FleetInferencePool name. The names never matched, so the autoscaling
// loop found no metrics for any real pool and never produced an action.
//
// This test drives the real HTTP path and asserts the autoscaler's own
// selector finds the sample, so a regression on either side fails here.
func TestAgentMetricsReachTheAutoscaler(t *testing.T) {
	const (
		clusterID = "brutus-h100"
		poolName  = "llama-70b"
	)

	fc := newTestFleetController(t)

	reconciler := controller.NewReconciler(
		solver.NewConstraintSolver(),
		func(context.Context) ([]solver.ClusterInfo, error) {
			return []solver.ClusterInfo{{
				ID:     clusterID,
				Name:   clusterID,
				Region: "us-east",
				Status: "Running",
				GPUCapacity: solver.GPUCapacity{
					Available: 8, Total: 8, Types: []string{"H100"},
				},
			}}, nil
		},
	)
	if err := reconciler.ReconcilePool(context.Background(), v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: poolName, Source: "hf://meta-llama/Llama-3-70B"},
	}); err != nil {
		t.Fatalf("reconcile pool: %v", err)
	}
	fc.Reconciler = reconciler

	pools := reconciler.ListPools()
	if len(pools) != 1 {
		t.Fatalf("expected 1 reconciled pool, got %d", len(pools))
	}
	pool := pools[0]
	if len(pool.DesiredClusters) == 0 {
		t.Fatalf("pool %q was not placed on any cluster; test fixture is wrong", pool.Name)
	}

	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{
		ID: clusterID, Name: clusterID, Region: "us-east", Status: "Running",
	}); err != nil {
		t.Fatal(err)
	}

	mux := fc.SetupRoutes("control")
	resp := postAgentJSON(t, mux, "/api/v1/agent/metrics", `{
		"cluster_id":"`+clusterID+`","throughput_tps":12.5,"ttft_p50_ms":40,
		"ttft_p99_ms":950,"queue_depth":64,"gpu_utilization":0.97,
		"kv_cache_hit_rate":0.42,"pool_saturation":0.98,"ready_endpoints":2
	}`)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("agent metrics returned %d: %s", resp.Code, resp.Body.String())
	}

	all, err := fc.MetricsCollector.CollectAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The autoscaler's own selector must find the sample under the real pool name.
	selected := metricsForPool(all, pool.Name, pool.DesiredClusters)
	if len(selected) == 0 {
		t.Fatalf("autoscaler found no metrics for pool %q on clusters %v; "+
			"the agent sample did not reach it", pool.Name, pool.DesiredClusters)
	}
	if got := selected[0].Pools[0].QueueDepth; got != 64 {
		t.Fatalf("queue depth = %d, want 64 (the reported value)", got)
	}
	if got := selected[0].Pools[0].PoolSaturation; got != 0.98 {
		t.Fatalf("pool saturation = %v, want 0.98 (the reported value)", got)
	}

	// The cluster-wide aggregate must survive alongside the attributed copies,
	// because routing health and the model metrics endpoint still read it.
	var sawAggregate bool
	for _, cm := range all {
		if cm.ClusterID != clusterID {
			continue
		}
		for _, pm := range cm.Pools {
			if pm.PoolName == agentAggregatePoolName {
				sawAggregate = true
			}
		}
	}
	if !sawAggregate {
		t.Fatal("cluster-wide agent-aggregate entry was dropped")
	}
}

// TestAttributeAgentSampleSkipsUnplacedPools asserts a cluster's load is only
// credited to pools actually placed on it.
func TestAttributeAgentSampleSkipsUnplacedPools(t *testing.T) {
	fc := newTestFleetController(t)

	reconciler := controller.NewReconciler(
		solver.NewConstraintSolver(),
		func(context.Context) ([]solver.ClusterInfo, error) {
			return []solver.ClusterInfo{{
				ID: "east", Name: "east", Region: "us-east", Status: "Running",
				GPUCapacity: solver.GPUCapacity{
					Available: 8, Total: 8, Types: []string{"H100"},
				},
			}}, nil
		},
	)
	if err := reconciler.ReconcilePool(context.Background(), v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "east-only-pool"},
	}); err != nil {
		t.Fatalf("reconcile pool: %v", err)
	}
	fc.Reconciler = reconciler

	// A cluster the pool was never placed on gets only the aggregate entry.
	got := fc.attributeAgentSample("west", collectorSample())
	if len(got) != 1 || got[0].PoolName != agentAggregatePoolName {
		t.Fatalf("unplaced cluster produced %d entries (%v), want only the aggregate",
			len(got), poolNames(got))
	}

	// The cluster it was placed on gets the aggregate plus the attributed copy.
	got = fc.attributeAgentSample("east", collectorSample())
	if len(got) != 2 {
		t.Fatalf("placed cluster produced %d entries (%v), want 2", len(got), poolNames(got))
	}
	if got[1].PoolName != "east-only-pool" {
		t.Fatalf("attributed pool name = %q, want %q", got[1].PoolName, "east-only-pool")
	}
}

// TestAttributeAgentSampleWithoutReconciler covers inference-mode and test
// wiring where no reconciler is attached.
func TestAttributeAgentSampleWithoutReconciler(t *testing.T) {
	fc := newTestFleetController(t)
	fc.Reconciler = nil

	got := fc.attributeAgentSample("anywhere", collectorSample())
	if len(got) != 1 || got[0].PoolName != agentAggregatePoolName {
		t.Fatalf("nil reconciler produced %d entries (%v), want only the aggregate",
			len(got), poolNames(got))
	}
}

func collectorSample() collector.PoolMetrics {
	return collector.PoolMetrics{
		PoolName:       agentAggregatePoolName,
		QueueDepth:     7,
		Throughput_TPS: 21.0,
	}
}

func poolNames(pools []collector.PoolMetrics) []string {
	names := make([]string, 0, len(pools))
	for _, p := range pools {
		names = append(names, p.PoolName)
	}
	return names
}
