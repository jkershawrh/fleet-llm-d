package postgres

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrClusterNotFound is returned when a cluster repository lookup misses.
	ErrClusterNotFound = errors.New("cluster not found")
	// ErrClusterAlreadyExists is returned when a cluster ID conflicts on create.
	ErrClusterAlreadyExists = errors.New("cluster already exists")
)

// ClusterRepository manages cluster records.
type ClusterRepository interface {
	Create(ctx context.Context, cluster ClusterRecord) error
	Get(ctx context.Context, id string) (*ClusterRecord, error)
	List(ctx context.Context) ([]ClusterRecord, error)
	Update(ctx context.Context, cluster ClusterRecord) error
	Delete(ctx context.Context, id string) error
}

// FleetPoolRepository manages fleet pool records.
type FleetPoolRepository interface {
	Create(ctx context.Context, pool FleetPoolRecord) error
	Get(ctx context.Context, id string) (*FleetPoolRecord, error)
	List(ctx context.Context) ([]FleetPoolRecord, error)
	Update(ctx context.Context, pool FleetPoolRecord) error
	Delete(ctx context.Context, id string) error
}

// TenantRepository manages tenant records.
type TenantRepository interface {
	Create(ctx context.Context, tenant TenantRecord) error
	Get(ctx context.Context, id string) (*TenantRecord, error)
	List(ctx context.Context) ([]TenantRecord, error)
	Update(ctx context.Context, tenant TenantRecord) error
	Delete(ctx context.Context, id string) error
	RecordUsage(ctx context.Context, usage TenantUsageRecord) error
	GetUsage(ctx context.Context, tenantID string, start, end time.Time) ([]TenantUsageRecord, error)
}

// RolloutRepository manages rollout records.
type RolloutRepository interface {
	Create(ctx context.Context, rollout RolloutRecord) error
	Get(ctx context.Context, id string) (*RolloutRecord, error)
	List(ctx context.Context) ([]RolloutRecord, error)
	Update(ctx context.Context, rollout RolloutRecord) error
}

// EventRepository manages fleet event records.
type EventRepository interface {
	Create(ctx context.Context, event EventRecord) error
	Query(ctx context.Context, filter EventFilter) ([]EventRecord, error)
}
