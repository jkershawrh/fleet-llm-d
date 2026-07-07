package postgres

import "time"

// ClusterRecord matches the clusters table.
type ClusterRecord struct {
	ID           string
	Name         string
	Region       string
	Labels       map[string]string
	GPUAvailable int
	GPUTotal     int
	Status       string
	RegisteredAt time.Time
	UpdatedAt    time.Time
}

// FleetPoolRecord matches the fleet_pools table.
type FleetPoolRecord struct {
	ID              string
	Name            string
	ModelName       string
	ModelSource     string
	PlacementPolicy string
	RoutingPolicy   string
	ScalingPolicy   string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TenantRecord matches the tenants table.
type TenantRecord struct {
	ID           string
	Name         string
	Priority     int
	Quotas       map[string]interface{}
	RateLimit    map[string]interface{}
	CostControl  map[string]interface{}
	ClusterScope map[string]interface{}
	CreatedAt    time.Time
}

// TenantUsageRecord matches the tenant_usage table.
type TenantUsageRecord struct {
	ID             string
	TenantID       string
	Model          string
	ClusterID      string
	TokensConsumed int64
	CostUSD        float64
	RequestCount   int64
	PeriodStart    time.Time
	PeriodEnd      time.Time
}

// RolloutRecord matches the rollouts table.
type RolloutRecord struct {
	ID            string
	PoolID        string
	ModelVersion  string
	Strategy      map[string]interface{}
	Status        string
	CurrentWeight int
	StartedAt     time.Time
	CompletedAt   *time.Time
}

// EventRecord matches the fleet_events table.
type EventRecord struct {
	ID        string
	EventType string
	Payload   map[string]interface{}
	Source    string
	CreatedAt time.Time
}

// EventFilter for querying events.
type EventFilter struct {
	EventType string
	Source    string
	Since    *time.Time
	Until    *time.Time
	Limit    int
}
