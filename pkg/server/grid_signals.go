package server

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

func (fc *FleetController) runGridSignalLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		fc.pollGridSignals(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (fc *FleetController) pollGridSignals(ctx context.Context) {
	if fc.GridSignalPoller == nil {
		return
	}
	bySite, err := fc.GridSignalPoller.Poll(ctx)
	if err != nil {
		slog.Warn("grid signal polling partially failed", "error", err)
	}
	for site, samples := range bySite {
		byPool := map[string]collector.PoolMetrics{}
		for _, sample := range samples {
			poolName := sample.Labels["grid_provider"]
			pm := byPool[poolName]
			pm.PoolName = poolName
			switch sample.Name {
			case "llm_d_epp_average_queue_size":
				pm.QueueDepth = int(sample.Value)
			case "llm_d_epp_average_kv_cache_utilization":
				pm.KVCacheUtilization = sample.Value
			case "llm_d_epp_ready_endpoints":
				pm.ReadyEndpoints = int(sample.Value)
			case "llm_d_epp_flow_control_pool_saturation":
				pm.PoolSaturation = sample.Value
			}
			byPool[poolName] = pm
		}
		poolNames := make([]string, 0, len(byPool))
		for name := range byPool {
			poolNames = append(poolNames, name)
		}
		sort.Strings(poolNames)
		pools := make([]collector.PoolMetrics, 0, len(poolNames))
		for _, name := range poolNames {
			pools = append(pools, byPool[name])
		}
		fc.MetricsCollector.Add(collector.ClusterMetrics{ClusterID: site, Pools: pools, Timestamp: time.Now().UTC()})
	}
}
