//go:build bdd

package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// GridTestState holds state for Grid integration BDD scenarios.
type GridTestState struct {
	Translator  *routing.GridCRDTranslator
	SWIMAdapter *client.SWIMSyncAdapter
	ClusterRepo *postgres.InMemoryClusterRepository
	MockServer  *httptest.Server

	// Fleet data for translation.
	FleetClusters []routing.FleetClusterInfo
	FleetPools    []routing.FleetPoolInfo

	// Track CRD applies received by the mock K8s API.
	mu                 sync.Mutex
	AppliedGridSites   map[string]routing.GridSiteSpec
	AppliedProviders   map[string]routing.InferenceProviderSpec
	GridSiteResponses  []GridSiteResponseItem
	ClusterUpdates     int
}

// GridSiteResponseItem mirrors the structure the SWIM adapter expects from the
// K8s API list response.
type GridSiteResponseItem struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		GridNetworkRef string `json:"gridNetworkRef"`
		Region         string `json:"region"`
		Zone           string `json:"zone"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// NewGridTestState creates a GridTestState with a mock K8s API server that
// captures GridSite and InferenceProvider apply requests and serves GridSite
// list responses for the SWIM adapter.
func NewGridTestState(gridNetwork, namespace string) *GridTestState {
	gs := &GridTestState{
		AppliedGridSites:  make(map[string]routing.GridSiteSpec),
		AppliedProviders:  make(map[string]routing.InferenceProviderSpec),
		GridSiteResponses: []GridSiteResponseItem{},
	}

	gs.MockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// PATCH: GridSite apply
		if r.Method == http.MethodPatch && strings.Contains(path, "/gridsites/") {
			parts := strings.Split(path, "/gridsites/")
			name := parts[len(parts)-1]
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			specRaw, _ := json.Marshal(body["spec"])
			var spec routing.GridSiteSpec
			_ = json.Unmarshal(specRaw, &spec)
			gs.mu.Lock()
			gs.AppliedGridSites[name] = spec
			gs.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}

		// PATCH: InferenceProvider apply
		if r.Method == http.MethodPatch && strings.Contains(path, "/inferenceproviders/") {
			parts := strings.Split(path, "/inferenceproviders/")
			name := parts[len(parts)-1]
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			specRaw, _ := json.Marshal(body["spec"])
			var spec routing.InferenceProviderSpec
			_ = json.Unmarshal(specRaw, &spec)
			gs.mu.Lock()
			gs.AppliedProviders[name] = spec
			gs.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}

		// GET: list GridSites (for SWIM sync)
		if r.Method == http.MethodGet && strings.HasSuffix(path, "/gridsites") {
			gs.mu.Lock()
			items := gs.GridSiteResponses
			gs.mu.Unlock()
			resp := map[string]interface{}{
				"items": items,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))

	gs.ClusterRepo = postgres.NewInMemoryClusterRepository()

	gs.Translator = routing.NewGridCRDTranslator(
		gs.MockServer.URL,
		namespace,
		"test-token",
		gridNetwork,
	)

	gs.SWIMAdapter = client.NewSWIMSyncAdapter(
		gs.MockServer.URL,
		"test-token",
		gs.ClusterRepo,
	)

	return gs
}

// Close shuts down the mock server.
func (gs *GridTestState) Close() {
	gs.MockServer.Close()
}

// ---------------------------------------------------------------------------
// World methods for Grid integration steps
// ---------------------------------------------------------------------------

// SetupGridClusters registers clusters with regions, zones, and egress for Grid
// CRD translation tests.
func (w *World) SetupGridClusters(clusters []routing.FleetClusterInfo) {
	if w.GridState == nil {
		w.GridState = NewGridTestState("fleet-mesh", "fleet-system")
	}
	w.GridState.FleetClusters = clusters
}

// SetupGridPool registers a pool for InferenceProvider translation.
func (w *World) SetupGridPool(pool routing.FleetPoolInfo) {
	if w.GridState == nil {
		w.GridState = NewGridTestState("fleet-mesh", "fleet-system")
	}
	w.GridState.FleetPools = append(w.GridState.FleetPools, pool)
}

// SyncClustersToGrid translates cluster state into GridSite CRDs via the mock
// K8s API.
func (w *World) SyncClustersToGrid() error {
	gs := w.GridState
	if gs == nil {
		return fmt.Errorf("grid test state not initialized")
	}
	return gs.Translator.SyncFromFleetState(w.Ctx, gs.FleetClusters, nil)
}

// SyncPoolsToGrid translates pool state into InferenceProvider CRDs via the
// mock K8s API.
func (w *World) SyncPoolsToGrid() error {
	gs := w.GridState
	if gs == nil {
		return fmt.Errorf("grid test state not initialized")
	}
	return gs.Translator.SyncFromFleetState(w.Ctx, nil, gs.FleetPools)
}

// SyncAllToGrid translates both clusters and pools.
func (w *World) SyncAllToGrid() error {
	gs := w.GridState
	if gs == nil {
		return fmt.Errorf("grid test state not initialized")
	}
	return gs.Translator.SyncFromFleetState(w.Ctx, gs.FleetClusters, gs.FleetPools)
}

// AssertGridSiteCount checks the number of GridSite CRDs applied.
func (w *World) AssertGridSiteCount(expected int) error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if len(gs.AppliedGridSites) != expected {
		return fmt.Errorf("expected %d GridSite CRDs, got %d", expected, len(gs.AppliedGridSites))
	}
	return nil
}

// AssertInferenceProviderCount checks the number of InferenceProvider CRDs applied.
func (w *World) AssertInferenceProviderCount(expected int) error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if len(gs.AppliedProviders) != expected {
		return fmt.Errorf("expected %d InferenceProvider CRDs, got %d", expected, len(gs.AppliedProviders))
	}
	return nil
}

// AssertGridSiteNetworkRef checks that every applied GridSite references the
// expected gridNetworkRef.
func (w *World) AssertGridSiteNetworkRef(expected string) error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	for name, spec := range gs.AppliedGridSites {
		if spec.GridNetworkRef != expected {
			return fmt.Errorf("GridSite %q: expected gridNetworkRef %q, got %q",
				name, expected, spec.GridNetworkRef)
		}
	}
	return nil
}

// AssertGridSiteRegionAndEgress checks a specific GridSite's region and egress.
func (w *World) AssertGridSiteRegionAndEgress(name, expectedRegion, expectedEgress string) error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	spec, ok := gs.AppliedGridSites[name]
	if !ok {
		return fmt.Errorf("GridSite %q not found in applied CRDs", name)
	}
	if spec.Region != expectedRegion {
		return fmt.Errorf("GridSite %q: expected region %q, got %q", name, expectedRegion, spec.Region)
	}
	if spec.Egress == nil {
		return fmt.Errorf("GridSite %q: expected egress, got nil", name)
	}
	if spec.Egress.Address != expectedEgress {
		return fmt.Errorf("GridSite %q: expected egress address %q, got %q",
			name, expectedEgress, spec.Egress.Address)
	}
	return nil
}

// AssertProviderEndpointContains checks that a provider endpoint contains a substring.
func (w *World) AssertProviderEndpointContains(name, substring string) error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	spec, ok := gs.AppliedProviders[name]
	if !ok {
		return fmt.Errorf("InferenceProvider %q not found in applied CRDs", name)
	}
	if !strings.Contains(spec.Endpoint, substring) {
		return fmt.Errorf("InferenceProvider %q: endpoint %q does not contain %q",
			name, spec.Endpoint, substring)
	}
	return nil
}

// AssertProviderModel checks that a provider lists the expected model.
func (w *World) AssertProviderModel(name, expectedModel string) error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	spec, ok := gs.AppliedProviders[name]
	if !ok {
		return fmt.Errorf("InferenceProvider %q not found in applied CRDs", name)
	}
	for _, m := range spec.Models {
		if m.Name == expectedModel {
			return nil
		}
	}
	return fmt.Errorf("InferenceProvider %q: model %q not found in %v",
		name, expectedModel, spec.Models)
}

// SetupSWIMCluster seeds the in-memory cluster repository with a cluster record
// for SWIM sync tests.
func (w *World) SetupSWIMCluster(id, region, status string) error {
	gs := w.GridState
	if gs == nil {
		return fmt.Errorf("grid test state not initialized")
	}
	return gs.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{
		ID:     id,
		Name:   id,
		Region: region,
		Status: status,
	})
}

// SetGridSitePhaseResponse configures the mock K8s API to return a GridSite
// with the given phase.
func (w *World) SetGridSitePhaseResponse(name, phase string) {
	gs := w.GridState
	item := GridSiteResponseItem{}
	item.Metadata.Name = name
	item.Status.Phase = phase
	gs.mu.Lock()
	gs.GridSiteResponses = append(gs.GridSiteResponses, item)
	gs.mu.Unlock()
}

// PreloadSWIMPhase simulates a previous SWIM sync cycle having already observed
// a phase, so the adapter's lastPhases cache is populated.
func (w *World) PreloadSWIMPhase(name, phase string) {
	gs := w.GridState
	// Run a sync cycle with the given phase to populate lastPhases.
	// We set up the response, run sync, then clear updates.
	gs.mu.Lock()
	gs.GridSiteResponses = []GridSiteResponseItem{}
	gs.mu.Unlock()

	w.SetGridSitePhaseResponse(name, phase)

	// Run sync to populate the cache.
	_, _ = gs.SWIMAdapter.Sync(context.Background())

	// Reset the update counter so we can measure subsequent syncs.
	gs.mu.Lock()
	gs.ClusterUpdates = 0
	gs.mu.Unlock()
}

// RunSWIMSync executes a SWIM sync cycle and records the update count.
func (w *World) RunSWIMSync() (int, error) {
	gs := w.GridState
	updated, err := gs.SWIMAdapter.Sync(w.Ctx)
	gs.mu.Lock()
	gs.ClusterUpdates = updated
	gs.mu.Unlock()
	return updated, err
}

// AssertClusterFleetStatus checks a cluster's status in the repository.
func (w *World) AssertClusterFleetStatus(id, expectedStatus string) error {
	gs := w.GridState
	record, err := gs.ClusterRepo.Get(context.Background(), id)
	if err != nil {
		return fmt.Errorf("cluster %q not found: %w", id, err)
	}
	if record.Status != expectedStatus {
		return fmt.Errorf("cluster %q: expected status %q, got %q",
			id, expectedStatus, record.Status)
	}
	return nil
}

// ClearGridSiteResponses resets the mock GridSite list responses.
func (w *World) ClearGridSiteResponses() {
	gs := w.GridState
	gs.mu.Lock()
	gs.GridSiteResponses = []GridSiteResponseItem{}
	gs.mu.Unlock()
}

// AssertNoClusterUpdates checks that no cluster updates were issued.
func (w *World) AssertNoClusterUpdates() error {
	gs := w.GridState
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.ClusterUpdates != 0 {
		return fmt.Errorf("expected 0 cluster updates, got %d", gs.ClusterUpdates)
	}
	return nil
}

