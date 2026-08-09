package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
)

func (fc *FleetController) runGridSyncLoop(ctx context.Context) {
	if fc.GridCRDTranslator == nil {
		slog.Info("grid sync loop disabled: no GridCRDTranslator configured")
		return
	}

	interval := 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("grid sync loop started", "interval", interval)

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
		clusterInfos = append(clusterInfos, routing.FleetClusterInfo{
			ID:           c.ID,
			Name:         c.Name,
			Region:       c.Region,
			Labels:       c.Labels,
			GPUAvailable: c.GPUAvailable,
			GPUTotal:     c.GPUTotal,
		})
	}

	poolInfos := make([]routing.FleetPoolInfo, 0, len(pools))
	for _, p := range pools {
		poolInfos = append(poolInfos, routing.FleetPoolInfo{
			Name:      p.Name,
			ModelName: p.ModelName,
		})
	}

	if err := fc.GridCRDTranslator.SyncFromFleetState(ctx, clusterInfos, poolInfos); err != nil {
		slog.Warn("grid sync: CRD translation failed", "error", err)
	}

	if fc.SWIMSyncAdapter != nil {
		updated, err := fc.SWIMSyncAdapter.Sync(ctx)
		if err != nil {
			slog.Warn("grid sync: SWIM sync failed", "error", err)
		} else if updated > 0 {
			slog.Info("grid sync: SWIM updated cluster health", "updated", updated)
			_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
				Type: events.EventSWIMHealthUpdated, Source: "urn:fleet-llm-d:controller",
				Subject: "swim-sync", Timestamp: time.Now().UTC(),
				Payload: map[string]interface{}{"updated": updated},
			})
		}
	}

	_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
		Type: events.EventGridSynced, Source: "urn:fleet-llm-d:controller",
		Subject: "grid-sync", Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{"sites": len(clusterInfos), "providers": len(poolInfos)},
	})
}
