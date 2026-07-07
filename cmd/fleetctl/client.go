package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FleetClient wraps HTTP calls to the fleet-controller API.
type FleetClient struct {
	BaseURL string
	Token   string // bearer token for authenticated requests
	HTTP    *http.Client
}

// NewFleetClient creates a new FleetClient targeting the given base URL.
func NewFleetClient(baseURL string) *FleetClient {
	return &FleetClient{
		BaseURL: baseURL,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ----------------------------------------------------------------------------
// Cluster types
// ----------------------------------------------------------------------------

// ClusterInfo mirrors solver.ClusterInfo returned by the API.
type ClusterInfo struct {
	ID          string            `json:"ID"`
	Name        string            `json:"Name"`
	Region      string            `json:"Region"`
	Labels      map[string]string `json:"Labels"`
	GPUCapacity struct {
		Available int      `json:"Available"`
		Total     int      `json:"Total"`
		Types     []string `json:"Types"`
	} `json:"GPUCapacity"`
	Utilization float64 `json:"Utilization"`
}

// ClusterRegistration is the request body for cluster registration.
type ClusterRegistration struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels"`
}

// ----------------------------------------------------------------------------
// Pool types
// ----------------------------------------------------------------------------

// PoolInfo mirrors postgres.FleetPoolRecord returned by the API.
type PoolInfo struct {
	ID              string `json:"ID"`
	Name            string `json:"Name"`
	ModelName       string `json:"ModelName"`
	ModelSource     string `json:"ModelSource"`
	PlacementPolicy string `json:"PlacementPolicy"`
	RoutingPolicy   string `json:"RoutingPolicy"`
	ScalingPolicy   string `json:"ScalingPolicy"`
	Status          string `json:"Status"`
}

// ----------------------------------------------------------------------------
// Tenant types
// ----------------------------------------------------------------------------

// TenantInfo mirrors postgres.TenantRecord returned by the API.
type TenantInfo struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	Priority int    `json:"Priority"`
}

// TenantUsage mirrors metering.TenantUsageSummary returned by the API.
type TenantUsage struct {
	TenantID       string `json:"TenantID"`
	TokensConsumed int64  `json:"TokensConsumed"`
	TotalCost      string `json:"TotalCost"`
	RequestCount   int64  `json:"RequestCount"`
	AvgLatencyMs   int    `json:"AvgLatencyMs"`
}

// ----------------------------------------------------------------------------
// Rollout types
// ----------------------------------------------------------------------------

// RolloutInfo mirrors postgres.RolloutRecord returned by the API.
type RolloutInfo struct {
	ID            string `json:"ID"`
	PoolID        string `json:"PoolID"`
	ModelVersion  string `json:"ModelVersion"`
	Status        string `json:"Status"`
	CurrentWeight int    `json:"CurrentWeight"`
	StartedAt     string `json:"StartedAt"`
}

// RolloutState mirrors rollout.RolloutState returned by promote/rollback.
type RolloutState struct {
	ID            string `json:"ID"`
	Phase         string `json:"Phase"`
	CurrentWeight int    `json:"CurrentWeight"`
}

// ----------------------------------------------------------------------------
// Metrics types
// ----------------------------------------------------------------------------

// FleetMetrics mirrors metrics.FleetMetrics returned by the API.
type FleetMetrics struct {
	TotalGPUs       int     `json:"TotalGPUs"`
	ActiveModels    int     `json:"ActiveModels"`
	TotalThroughput float64 `json:"TotalThroughput"`
	AvgTTFTMs       float64 `json:"AvgTTFT_Ms"`
}

// ModelMetrics mirrors metrics.ModelMetrics returned by the API.
type ModelMetrics struct {
	Model        string   `json:"Model"`
	Clusters     []string `json:"Clusters"`
	Throughput   float64  `json:"Throughput"`
	TTFTP50Ms    float64  `json:"TTFT_P50_Ms"`
	TTFTP95Ms    float64  `json:"TTFT_P95_Ms"`
	TTFTP99Ms    float64  `json:"TTFT_P99_Ms"`
	CacheHitRate float64  `json:"CacheHitRate"`
}

// ----------------------------------------------------------------------------
// Chain verification types
// ----------------------------------------------------------------------------

// ChainVerification mirrors ledger.ChainVerification returned by the API.
type ChainVerification struct {
	Valid          bool   `json:"Valid"`
	EntriesChecked int64  `json:"EntriesChecked"`
	ChainType      string `json:"ChainType"`
	VerifiedAt     string `json:"VerifiedAt"`
}

// ----------------------------------------------------------------------------
// API methods
// ----------------------------------------------------------------------------

// doRequest performs an HTTP request and returns the response body.
func (c *FleetClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	return data, resp.StatusCode, nil
}

// ListClusters retrieves all registered clusters.
func (c *FleetClient) ListClusters(ctx context.Context) ([]ClusterInfo, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/clusters", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var clusters []ClusterInfo
	if err := json.Unmarshal(data, &clusters); err != nil {
		return nil, fmt.Errorf("decode clusters: %w", err)
	}
	return clusters, nil
}

// RegisterCluster registers a new cluster.
func (c *FleetClient) RegisterCluster(ctx context.Context, reg ClusterRegistration) error {
	data, status, err := c.doRequest(ctx, http.MethodPost, "/api/v1/clusters", reg)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	return nil
}

// DeregisterCluster removes a cluster by ID.
func (c *FleetClient) DeregisterCluster(ctx context.Context, id string) error {
	data, status, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/clusters/"+id, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	return nil
}

// ListPools retrieves all fleet inference pools.
func (c *FleetClient) ListPools(ctx context.Context) ([]PoolInfo, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/pools", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var pools []PoolInfo
	if err := json.Unmarshal(data, &pools); err != nil {
		return nil, fmt.Errorf("decode pools: %w", err)
	}
	return pools, nil
}

// ListTenants retrieves all tenants.
func (c *FleetClient) ListTenants(ctx context.Context) ([]TenantInfo, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/tenants", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var tenants []TenantInfo
	if err := json.Unmarshal(data, &tenants); err != nil {
		return nil, fmt.Errorf("decode tenants: %w", err)
	}
	return tenants, nil
}

// GetTenantUsage retrieves usage for a specific tenant.
func (c *FleetClient) GetTenantUsage(ctx context.Context, tenantID string) (*TenantUsage, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/tenants/"+tenantID+"/usage", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var usage TenantUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		return nil, fmt.Errorf("decode tenant usage: %w", err)
	}
	return &usage, nil
}

// GetFleetMetrics retrieves fleet-wide metrics.
func (c *FleetClient) GetFleetMetrics(ctx context.Context) (*FleetMetrics, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/metrics/fleet", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var m FleetMetrics
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode fleet metrics: %w", err)
	}
	return &m, nil
}

// GetModelMetrics retrieves metrics for a specific model.
func (c *FleetClient) GetModelMetrics(ctx context.Context, model string) (*ModelMetrics, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/metrics/model/"+model, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var m ModelMetrics
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode model metrics: %w", err)
	}
	return &m, nil
}

// ListRollouts retrieves all rollouts.
func (c *FleetClient) ListRollouts(ctx context.Context) ([]RolloutInfo, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/rollouts", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var rollouts []RolloutInfo
	if err := json.Unmarshal(data, &rollouts); err != nil {
		return nil, fmt.Errorf("decode rollouts: %w", err)
	}
	return rollouts, nil
}

// PromoteRollout advances a canary rollout.
func (c *FleetClient) PromoteRollout(ctx context.Context, id string) (*RolloutState, error) {
	data, status, err := c.doRequest(ctx, http.MethodPost, "/api/v1/rollouts/"+id+"/promote", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var state RolloutState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode rollout state: %w", err)
	}
	return &state, nil
}

// RollbackRollout rolls back a rollout.
func (c *FleetClient) RollbackRollout(ctx context.Context, id string) (*RolloutState, error) {
	data, status, err := c.doRequest(ctx, http.MethodPost, "/api/v1/rollouts/"+id+"/rollback", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var state RolloutState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode rollout state: %w", err)
	}
	return &state, nil
}

// VerifyChains verifies all ledger decision chains.
func (c *FleetClient) VerifyChains(ctx context.Context) (map[string]*ChainVerification, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/api/v1/verify/chains", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", status, string(data))
	}
	var results map[string]*ChainVerification
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("decode chain verification: %w", err)
	}
	return results, nil
}
