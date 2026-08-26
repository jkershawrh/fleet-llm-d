package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
)

func (fc *FleetController) runGridSyncLoop(ctx context.Context) {
	provider := fc.effectiveRoutingProvider()
	if provider == nil {
		slog.Info("routing provider sync loop disabled", "provider", fc.RoutingProviderName)
		return
	}

	interval := 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("routing provider sync loop started", "provider", provider.Name(), "interval", interval)

	for {
		select {
		case <-ctx.Done():
			slog.Info("grid sync loop stopped")
			return
		case <-ticker.C:
			fc.runGridSyncCycle(ctx)
		}
	}
}

// RunGridSyncCycle runs a single grid synchronization cycle. Exported for
// architecture tests that verify event flow.
func (fc *FleetController) RunGridSyncCycle(ctx context.Context) {
	fc.runGridSyncCycle(ctx)
}

func (fc *FleetController) runGridSyncCycle(ctx context.Context) {
	clusters, err := fc.ClusterRepo.List(ctx)
	if err != nil {
		slog.Warn("grid sync: failed to list clusters", "error", err)
		return
	}

	pools, err := fc.PoolRepo.List(ctx)
	if err != nil {
		slog.Warn("grid sync: failed to list pools", "error", err)
		return
	}

	clusterInfos := make([]routing.FleetClusterInfo, 0, len(clusters))
	for _, c := range clusters {
		egressAddress := ""
		if c.Labels != nil {
			egressAddress = c.Labels["fleet.llm-d.ai/egress-address"]
		}
		clusterInfos = append(clusterInfos, routing.FleetClusterInfo{
			ID:              c.ID,
			Name:            c.Name,
			Region:          c.Region,
			Labels:          c.Labels,
			EgressAddress:   egressAddress,
			MetricsEndpoint: c.Labels["fleet.llm-d.ai/metrics-endpoint"],
			Status:          c.Status,
			UpdatedAt:       c.UpdatedAt,
			Draining:        c.Labels["fleet.llm-d.ai/draining"] == "true",
			Authorized:      c.Labels["fleet.llm-d.ai/authorized"] != "false",
			GPUAvailable:    c.GPUAvailable,
			GPUTotal:        c.GPUTotal,
		})
	}

	poolInfos := make([]routing.FleetPoolInfo, 0, len(pools))
	for _, p := range pools {
		poolInfos = append(poolInfos, routing.FleetPoolInfo{
			Name:          p.Name,
			ModelName:     p.ModelName,
			PhysicalModel: p.ModelName,
			ModelSource:   p.ModelSource,
			Clusters:      append([]string(nil), p.DesiredClusters...),
			TargetPorts:   append([]int(nil), p.TargetPorts...),
		})
	}

	syncSucceeded := true
	provider := fc.effectiveRoutingProvider()
	if provider == nil {
		return
	}
	if err := provider.Sync(ctx, clusterInfos, poolInfos); err != nil {
		slog.Warn("routing provider sync failed", "provider", provider.Name(), "error", err)
		syncSucceeded = false
	}

	if fc.SWIMSyncAdapter != nil {
		updated, err := fc.SWIMSyncAdapter.Sync(ctx)
		if err != nil {
			slog.Warn("grid sync: SWIM sync failed", "error", err)
			syncSucceeded = false
		} else if updated > 0 {
			slog.Info("grid sync: SWIM updated cluster health", "updated", updated)
			_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
				Type: events.EventSWIMHealthUpdated, Source: "urn:fleet-llm-d:controller",
				Subject: "swim-sync", Timestamp: time.Now().UTC(),
				Payload: map[string]interface{}{"updated": updated},
			})
		}
	}
	if !syncSucceeded {
		return
	}

	_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
		Type: events.EventGridSynced, Source: "urn:fleet-llm-d:controller",
		Subject: "routing-provider-sync", Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{"adapter": provider.Name(), "sites": len(clusterInfos), "providers": len(poolInfos)},
	})
}

func (fc *FleetController) effectiveRoutingProvider() routing.RoutingProvider {
	if fc.RoutingProvider != nil {
		return fc.RoutingProvider
	}
	// Compatibility for embedders and tests that configured the legacy field.
	if fc.GridCRDTranslator != nil {
		return fc.GridCRDTranslator
	}
	return nil
}
