package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
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
	log.Println("connected to PostgreSQL — using persistent stores")

	fc.ClusterRepo = postgres.NewPGClusterRepository(pgClient)
	fc.ClusterClient = client.NewRepositoryClusterClient(fc.ClusterRepo)
	fc.Reconciler.SetClusterLister(fc.ClusterClient.ListClusters)
	fc.PoolRepo = postgres.NewPGFleetPoolRepository(pgClient)
	fc.TenantRepo = postgres.NewPGTenantRepository(pgClient)
	fc.RolloutRepo = postgres.NewPGRolloutRepository(pgClient)
	return nil
}

// RegisterBackendsFromJSON parses a JSON array of backend configurations and
// registers each with the inference proxy.
func (fc *FleetController) RegisterBackendsFromJSON(backendsJSON string) error {
	var backendList []struct {
		Model      string `json:"model"`
		URL        string `json:"url"`
		Runtime    string `json:"runtime"`
		PathPrefix string `json:"path_prefix"`
	}
	if err := json.Unmarshal([]byte(backendsJSON), &backendList); err != nil {
		return fmt.Errorf("failed to parse --backends JSON: %w", err)
	}
	for _, b := range backendList {
		fc.InferenceProxy.RegisterBackend(b.Model, routing.Backend{
			Name:       fmt.Sprintf("%s-%s", b.Runtime, b.Model),
			URL:        b.URL,
			Runtime:    b.Runtime,
			PathPrefix: b.PathPrefix,
			Healthy:    true,
			LatencyMs:  500,
		})
		log.Printf("registered backend: model=%s url=%s runtime=%s", b.Model, b.URL, b.Runtime)
	}
	return nil
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
				log.Printf("failed to record cluster provisioned %s: %v", c.Name, err)
			}
		}
	})
	watcher.OnDeploymentChange(func(deployments []modelplane.ModelDeployment) {
		for _, d := range deployments {
			if _, err := bridge.RecordDeploymentCreated(context.Background(), d); err != nil {
				log.Printf("failed to record deployment created %s: %v", d.Name, err)
			}
		}
	})
	watcher.OnEndpointChange(func(endpoints []modelplane.ModelEndpoint) {
		for _, e := range endpoints {
			if _, err := bridge.RecordEndpointReady(context.Background(), e); err != nil {
				log.Printf("failed to record endpoint ready %s: %v", e.Name, err)
			}
		}
	})

	fc.ModelPlaneWatcher = watcher
	fc.ModelPlaneBridge = bridge
	log.Printf("ModelPlane integration enabled (api=%s, namespace=%s)", apiURL, namespace)
}
