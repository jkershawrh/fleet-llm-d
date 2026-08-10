package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
)

// GridSitePhase represents the lifecycle phase of a GridSite.
type GridSitePhase string

const (
	GridSiteActive      GridSitePhase = "Active"
	GridSiteDiscovered  GridSitePhase = "Discovered"
	GridSiteConnecting  GridSitePhase = "Connecting"
	GridSitePending     GridSitePhase = "Pending"
	GridSiteUnreachable GridSitePhase = "Unreachable"
	GridSiteLeft        GridSitePhase = "Left"
)

// GridSiteStatus is the status read from a GridSite CRD.
type GridSiteStatus struct {
	Phase   GridSitePhase `json:"phase"`
	Reason  string        `json:"reason,omitempty"`
	Message string        `json:"message,omitempty"`
}

type gridSiteItem struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		GridNetworkRef string `json:"gridNetworkRef"`
		Region         string `json:"region"`
		Zone           string `json:"zone"`
	} `json:"spec"`
	Status GridSiteStatus `json:"status"`
}

type gridSiteList struct {
	Items []gridSiteItem `json:"items"`
}

// SWIMSyncAdapter watches GridSite CRDs and synchronizes their health
// status back to FleetCluster records in the fleet-llm-d cluster repository.
type SWIMSyncAdapter struct {
	apiServer   string
	token       string
	httpClient  *http.Client
	clusterRepo postgres.ClusterRepository
	lastPhases  map[string]GridSitePhase
}

// NewSWIMSyncAdapter creates an adapter that reads GridSite status from
// the K8s API and updates FleetCluster health accordingly.
func NewSWIMSyncAdapter(apiServer, token string, clusterRepo postgres.ClusterRepository) *SWIMSyncAdapter {
	tlsConfig, err := tlsutil.NewTLSConfig(tlsutil.KubernetesTLSOptions())
	if err != nil {
		slog.Warn("SWIM sync: failed to load Kubernetes CA", "error", err)
		tlsConfig, _ = tlsutil.NewTLSConfig(tlsutil.TLSOptions{})
	}
	return &SWIMSyncAdapter{
		apiServer:   strings.TrimRight(apiServer, "/"),
		token:       token,
		clusterRepo: clusterRepo,
		lastPhases:  make(map[string]GridSitePhase),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
	}
}

// SetClusterRepository rewires the persistence target during controller
// startup when PostgreSQL replaces the development in-memory repository.
func (s *SWIMSyncAdapter) SetClusterRepository(repo postgres.ClusterRepository) {
	s.clusterRepo = repo
	// Re-evaluate every observed phase against the new authoritative store.
	s.lastPhases = make(map[string]GridSitePhase)
}

// Sync reads all GridSite resources and updates FleetCluster records
// when a site's phase changes. Returns the number of clusters updated.
func (s *SWIMSyncAdapter) Sync(ctx context.Context) (int, error) {
	sites, err := s.listGridSites(ctx)
	if err != nil {
		return 0, fmt.Errorf("list GridSites: %w", err)
	}

	updated := 0
	for _, site := range sites {
		name := site.Metadata.Name
		phase := site.Status.Phase
		if phase == "" {
			phase = GridSitePending
		}

		if prev, ok := s.lastPhases[name]; ok && prev == phase {
			continue
		}
		fleetStatus := gridPhaseToFleetStatus(phase)
		recordPtr, err := s.clusterRepo.Get(ctx, name)
		if err != nil {
			slog.Debug("SWIM sync: cluster not in fleet registry", "site", name)
			continue
		}
		record := *recordPtr

		if record.Status != fleetStatus {
			record.Status = fleetStatus
			if err := s.clusterRepo.Update(ctx, record); err != nil {
				slog.Warn("SWIM sync: failed to update cluster", "site", name, "error", err)
				continue
			}
			slog.Info("SWIM sync: cluster health updated",
				"site", name,
				"grid_phase", phase,
				"fleet_status", fleetStatus,
			)
			updated++
		}
		// Cache only after the authoritative record was successfully read and,
		// when needed, updated. Transient repository errors must be retried.
		s.lastPhases[name] = phase
	}

	return updated, nil
}

func gridPhaseToFleetStatus(phase GridSitePhase) string {
	switch phase {
	case GridSiteActive:
		return "Running"
	case GridSiteDiscovered, GridSiteConnecting, GridSitePending:
		return "Pending"
	case GridSiteUnreachable:
		return "Degraded"
	case GridSiteLeft:
		return "Disconnected"
	default:
		return "Unknown"
	}
}

func (s *SWIMSyncAdapter) listGridSites(ctx context.Context) ([]gridSiteItem, error) {
	url := fmt.Sprintf("%s/apis/grid.praxis-proxy.io/v1alpha1/gridsites", s.apiServer)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list GridSites: %d", resp.StatusCode)
	}

	var list gridSiteList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode GridSites: %w", err)
	}
	return list.Items, nil
}
