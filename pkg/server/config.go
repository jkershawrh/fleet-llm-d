package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/lifecycle/rollout"
	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/metering"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/quota"
)

// ConfigureLeaderElection enables Kubernetes Lease-based active/passive
// ownership for mutating APIs and control-plane watchers.
func (fc *FleetController) ConfigureLeaderElection(elector *controller.LeaderElector) {
	fc.LeaderElector = elector
}

// OverrideWithPostgres replaces in-memory repositories with PostgreSQL-backed
// stores. The caller is responsible for closing the database connection.
func (fc *FleetController) OverrideWithPostgres(db *sql.DB) error {
	pgClient := postgres.NewPGClientFromDB(db)
	if err := pgClient.Ping(context.Background()); err != nil {
		return fmt.Errorf("failed to ping postgres: %w", err)
	}
	if err := pgClient.EnsureSchema(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize postgres schema: %w", err)
	}
	slog.Info("connected to PostgreSQL — using persistent stores")

	fc.rewireRepositories(
		postgres.NewPGClusterRepository(pgClient),
		postgres.NewPGFleetPoolRepository(pgClient),
		postgres.NewPGTenantRepository(pgClient),
		postgres.NewPGRolloutRepository(pgClient),
	)
	return nil
}

// rewireRepositories replaces every repository consumer as one unit. Startup
// calls this before serving requests, so no component can retain an in-memory
// repository while the public handlers use PostgreSQL.
func (fc *FleetController) rewireRepositories(
	clusterRepo postgres.ClusterRepository,
	poolRepo postgres.FleetPoolRepository,
	tenantRepo postgres.TenantRepository,
	rolloutRepo postgres.RolloutRepository,
) {
	fc.ClusterRepo = clusterRepo
	fc.ClusterClient = client.NewRepositoryClusterClient(clusterRepo)
	fc.Reconciler.SetClusterLister(fc.ClusterClient.ListClusters)
	fc.PoolRepo = poolRepo
	fc.TenantRepo = tenantRepo
	fc.RolloutRepo = rolloutRepo
	fc.QuotaEnforcer = quota.NewQuotaEnforcer(tenantRepo)
	fc.UsageTracker = metering.NewUsageTracker(tenantRepo)
	fc.RolloutController = rollout.NewRolloutController(rolloutRepo)
	if fc.SWIMSyncAdapter != nil {
		fc.SWIMSyncAdapter.SetClusterRepository(clusterRepo)
	}
}

// WireModelPlane sets up ModelPlane integration with ledger recording callbacks.
func (fc *FleetController) WireModelPlane(apiURL, namespace string) {
	mpToken := ""
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		mpToken = string(data)
	}
	watcher := modelplane.NewModelPlaneWatcher(apiURL, namespace, mpToken)
	bridge := modelplane.NewComplianceBridge(fc.FleetRecorder)

	watcher.OnClusterChange(func(clusters []modelplane.InferenceCluster) {
		for _, c := range clusters {
			if _, err := bridge.RecordClusterProvisioned(context.Background(), c); err != nil {
				slog.Warn("failed to record cluster provisioned", "cluster", c.Name, "error", err)
			}
		}
	})
	watcher.OnDeploymentChange(func(deployments []modelplane.ModelDeployment) {
		for _, d := range deployments {
			if _, err := bridge.RecordDeploymentCreated(context.Background(), d); err != nil {
				slog.Warn("failed to record deployment created", "deployment", d.Name, "error", err)
			}
		}
	})
	watcher.OnEndpointChange(func(endpoints []modelplane.ModelEndpoint) {
		for _, e := range endpoints {
			if _, err := bridge.RecordEndpointReady(context.Background(), e); err != nil {
				slog.Warn("failed to record endpoint ready", "endpoint", e.Name, "error", err)
			}
		}
	})

	fc.ModelPlaneWatcher = watcher
	fc.ModelPlaneBridge = bridge
	slog.Info("ModelPlane integration enabled", "api", apiURL, "namespace", namespace)
}
