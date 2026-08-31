package server

import (
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

func TestMetricsForPoolFiltersPoolAndPlacement(t *testing.T) {
	metrics := []collector.ClusterMetrics{
		{ClusterID: "east", Pools: []collector.PoolMetrics{{PoolName: "target", Replicas: 2}, {PoolName: "other", Replicas: 9}}},
		{ClusterID: "west", Pools: []collector.PoolMetrics{{PoolName: "target", Replicas: 3}}},
		{ClusterID: "outside", Pools: []collector.PoolMetrics{{PoolName: "target", Replicas: 4}}},
	}

	got := metricsForPool(metrics, "target", []string{"east", "west"})
	if len(got) != 2 {
		t.Fatalf("got %d clusters, want 2", len(got))
	}
	for _, cluster := range got {
		if len(cluster.Pools) != 1 || cluster.Pools[0].PoolName != "target" {
			t.Fatalf("unrelated policy metrics leaked into result: %#v", cluster.Pools)
		}
		if cluster.ClusterID == "outside" {
			t.Fatal("metrics from an undesired cluster were included")
		}
	}
}

func TestAggregateScalingActionsProducesOneFleetTotal(t *testing.T) {
	metrics := []collector.ClusterMetrics{
		{ClusterID: "east", Pools: []collector.PoolMetrics{{PoolName: "target", Replicas: 2}}},
		{ClusterID: "west", Pools: []collector.PoolMetrics{{PoolName: "target", Replicas: 3}}},
	}
	actions := []optimizer.ScalingAction{
		{ClusterID: "west", CurrentReplicas: 3, DesiredReplicas: 4, Reason: "overloaded"},
		{ClusterID: "east", CurrentReplicas: 2, DesiredReplicas: 3, Reason: "overloaded"},
	}

	got, ok := aggregateScalingActions("target", metrics, actions)
	if !ok {
		t.Fatal("expected an aggregate action")
	}
	if got.CurrentReplicas != 5 || got.DesiredReplicas != 7 {
		t.Fatalf("aggregate replicas = %d -> %d, want 5 -> 7", got.CurrentReplicas, got.DesiredReplicas)
	}
	if got.ClusterID != "east,west" {
		t.Fatalf("cluster list = %q, want deterministic east,west", got.ClusterID)
	}
}

func TestRoutingHealthFingerprintIncludesLiveSignals(t *testing.T) {
	base := []policy.ClusterHealth{{ClusterID: "east", Healthy: true, CapacityRemaining: 0.8, CurrentLoad: 0.2}}
	changed := append([]policy.ClusterHealth(nil), base...)
	changed[0].CurrentLoad = 0.7
	if routingHealthFingerprint(base) == routingHealthFingerprint(changed) {
		t.Fatal("live load change did not alter routing fingerprint")
	}

	permuted := []policy.ClusterHealth{{ClusterID: "west", Healthy: true}, base[0]}
	ordered := []policy.ClusterHealth{base[0], {ClusterID: "west", Healthy: true}}
	if routingHealthFingerprint(permuted) != routingHealthFingerprint(ordered) {
		t.Fatal("fingerprint should be independent of collector ordering")
	}
}

func TestRoutableClusterFailsClosed(t *testing.T) {
	for name, cluster := range map[string]policy.ClusterHealth{
		"unhealthy":      {Healthy: false},
		"load-saturated": {Healthy: true, CurrentLoad: 1},
		"pool-saturated": {Healthy: true, PoolSaturation: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if routableCluster(cluster) {
				t.Fatal("cluster should not be routable")
			}
		})
	}
	if !routableCluster(policy.ClusterHealth{Healthy: true, CurrentLoad: 0.5, PoolSaturation: 0.5}) {
		t.Fatal("healthy cluster with spare capacity should be routable")
	}
}
