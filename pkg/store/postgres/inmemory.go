package postgres

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// generateID produces a random hex ID suitable for use as a primary key.
func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// --------------------------------------------------------------------------
// InMemoryClusterRepository
// --------------------------------------------------------------------------

// InMemoryClusterRepository is a thread-safe, in-memory implementation of
// ClusterRepository for testing and development.
type InMemoryClusterRepository struct {
	mu       sync.RWMutex
	clusters map[string]ClusterRecord
}

// NewInMemoryClusterRepository creates a new InMemoryClusterRepository.
func NewInMemoryClusterRepository() *InMemoryClusterRepository {
	return &InMemoryClusterRepository{
		clusters: make(map[string]ClusterRecord),
	}
}

func (r *InMemoryClusterRepository) Create(_ context.Context, cluster ClusterRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cluster.ID == "" {
		cluster.ID = generateID()
	}
	if _, exists := r.clusters[cluster.ID]; exists {
		return fmt.Errorf("%w: %q", ErrClusterAlreadyExists, cluster.ID)
	}
	now := time.Now()
	if cluster.RegisteredAt.IsZero() {
		cluster.RegisteredAt = now
	}
	if cluster.UpdatedAt.IsZero() {
		cluster.UpdatedAt = now
	}
	r.clusters[cluster.ID] = cluster
	return nil
}

func (r *InMemoryClusterRepository) Get(_ context.Context, id string) (*ClusterRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	c, ok := r.clusters[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrClusterNotFound, id)
	}
	return &c, nil
}

func (r *InMemoryClusterRepository) List(_ context.Context) ([]ClusterRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ClusterRecord, 0, len(r.clusters))
	for _, c := range r.clusters {
		out = append(out, c)
	}
	return out, nil
}

func (r *InMemoryClusterRepository) Update(_ context.Context, cluster ClusterRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.clusters[cluster.ID]; !ok {
		return fmt.Errorf("%w: %q", ErrClusterNotFound, cluster.ID)
	}
	cluster.UpdatedAt = time.Now()
	r.clusters[cluster.ID] = cluster
	return nil
}

func (r *InMemoryClusterRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.clusters[id]; !ok {
		return fmt.Errorf("%w: %q", ErrClusterNotFound, id)
	}
	delete(r.clusters, id)
	return nil
}

// --------------------------------------------------------------------------
// InMemoryFleetPoolRepository
// --------------------------------------------------------------------------

// InMemoryFleetPoolRepository is a thread-safe, in-memory implementation of
// FleetPoolRepository for testing and development.
type InMemoryFleetPoolRepository struct {
	mu    sync.RWMutex
	pools map[string]FleetPoolRecord
}

// NewInMemoryFleetPoolRepository creates a new InMemoryFleetPoolRepository.
func NewInMemoryFleetPoolRepository() *InMemoryFleetPoolRepository {
	return &InMemoryFleetPoolRepository{
		pools: make(map[string]FleetPoolRecord),
	}
}

func (r *InMemoryFleetPoolRepository) Create(_ context.Context, pool FleetPoolRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pool.ID == "" {
		pool.ID = generateID()
	}
	if _, exists := r.pools[pool.ID]; exists {
		return fmt.Errorf("fleet pool %q already exists", pool.ID)
	}
	now := time.Now()
	if pool.CreatedAt.IsZero() {
		pool.CreatedAt = now
	}
	if pool.UpdatedAt.IsZero() {
		pool.UpdatedAt = now
	}
	r.pools[pool.ID] = pool
	return nil
}

func (r *InMemoryFleetPoolRepository) Get(_ context.Context, id string) (*FleetPoolRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.pools[id]
	if !ok {
		return nil, fmt.Errorf("fleet pool %q not found", id)
	}
	return &p, nil
}

func (r *InMemoryFleetPoolRepository) List(_ context.Context) ([]FleetPoolRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]FleetPoolRecord, 0, len(r.pools))
	for _, p := range r.pools {
		out = append(out, p)
	}
	return out, nil
}

func (r *InMemoryFleetPoolRepository) Update(_ context.Context, pool FleetPoolRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.pools[pool.ID]; !ok {
		return fmt.Errorf("fleet pool %q not found", pool.ID)
	}
	pool.UpdatedAt = time.Now()
	r.pools[pool.ID] = pool
	return nil
}

func (r *InMemoryFleetPoolRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.pools[id]; !ok {
		return fmt.Errorf("fleet pool %q not found", id)
	}
	delete(r.pools, id)
	return nil
}

// --------------------------------------------------------------------------
// InMemoryTenantRepository
// --------------------------------------------------------------------------

// InMemoryTenantRepository is a thread-safe, in-memory implementation of
// TenantRepository for testing and development.
type InMemoryTenantRepository struct {
	mu      sync.RWMutex
	tenants map[string]TenantRecord
	usage   []TenantUsageRecord
}

// NewInMemoryTenantRepository creates a new InMemoryTenantRepository.
func NewInMemoryTenantRepository() *InMemoryTenantRepository {
	return &InMemoryTenantRepository{
		tenants: make(map[string]TenantRecord),
		usage:   make([]TenantUsageRecord, 0),
	}
}

func (r *InMemoryTenantRepository) Create(_ context.Context, tenant TenantRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tenant.ID == "" {
		tenant.ID = generateID()
	}
	if _, exists := r.tenants[tenant.ID]; exists {
		return fmt.Errorf("tenant %q already exists", tenant.ID)
	}
	if tenant.CreatedAt.IsZero() {
		tenant.CreatedAt = time.Now()
	}
	r.tenants[tenant.ID] = tenant
	return nil
}

func (r *InMemoryTenantRepository) Get(_ context.Context, id string) (*TenantRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.tenants[id]
	if !ok {
		return nil, fmt.Errorf("tenant %q not found", id)
	}
	return &t, nil
}

func (r *InMemoryTenantRepository) List(_ context.Context) ([]TenantRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]TenantRecord, 0, len(r.tenants))
	for _, t := range r.tenants {
		out = append(out, t)
	}
	return out, nil
}

func (r *InMemoryTenantRepository) Update(_ context.Context, tenant TenantRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tenants[tenant.ID]; !ok {
		return fmt.Errorf("tenant %q not found", tenant.ID)
	}
	r.tenants[tenant.ID] = tenant
	return nil
}

func (r *InMemoryTenantRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tenants[id]; !ok {
		return fmt.Errorf("tenant %q not found", id)
	}
	delete(r.tenants, id)
	return nil
}

func (r *InMemoryTenantRepository) RecordUsage(_ context.Context, usage TenantUsageRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if usage.ID == "" {
		usage.ID = generateID()
	}
	r.usage = append(r.usage, usage)
	return nil
}

func (r *InMemoryTenantRepository) GetUsage(_ context.Context, tenantID string, start, end time.Time) ([]TenantUsageRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []TenantUsageRecord
	for _, u := range r.usage {
		if u.TenantID != tenantID {
			continue
		}
		if u.PeriodStart.Before(start) || u.PeriodEnd.After(end) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// --------------------------------------------------------------------------
// InMemoryRolloutRepository
// --------------------------------------------------------------------------

// InMemoryRolloutRepository is a thread-safe, in-memory implementation of
// RolloutRepository for testing and development.
type InMemoryRolloutRepository struct {
	mu       sync.RWMutex
	rollouts map[string]RolloutRecord
}

// NewInMemoryRolloutRepository creates a new InMemoryRolloutRepository.
func NewInMemoryRolloutRepository() *InMemoryRolloutRepository {
	return &InMemoryRolloutRepository{
		rollouts: make(map[string]RolloutRecord),
	}
}

func (r *InMemoryRolloutRepository) Create(_ context.Context, rollout RolloutRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rollout.ID == "" {
		rollout.ID = generateID()
	}
	if _, exists := r.rollouts[rollout.ID]; exists {
		return fmt.Errorf("rollout %q already exists", rollout.ID)
	}
	if rollout.StartedAt.IsZero() {
		rollout.StartedAt = time.Now()
	}
	r.rollouts[rollout.ID] = rollout
	return nil
}

func (r *InMemoryRolloutRepository) Get(_ context.Context, id string) (*RolloutRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ro, ok := r.rollouts[id]
	if !ok {
		return nil, fmt.Errorf("rollout %q not found", id)
	}
	return &ro, nil
}

func (r *InMemoryRolloutRepository) List(_ context.Context) ([]RolloutRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]RolloutRecord, 0, len(r.rollouts))
	for _, ro := range r.rollouts {
		out = append(out, ro)
	}
	return out, nil
}

func (r *InMemoryRolloutRepository) Update(_ context.Context, rollout RolloutRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.rollouts[rollout.ID]; !ok {
		return fmt.Errorf("rollout %q not found", rollout.ID)
	}
	r.rollouts[rollout.ID] = rollout
	return nil
}

// --------------------------------------------------------------------------
// InMemoryEventRepository
// --------------------------------------------------------------------------

// InMemoryEventRepository is a thread-safe, in-memory implementation of
// EventRepository for testing and development.
type InMemoryEventRepository struct {
	mu     sync.RWMutex
	events []EventRecord
}

// NewInMemoryEventRepository creates a new InMemoryEventRepository.
func NewInMemoryEventRepository() *InMemoryEventRepository {
	return &InMemoryEventRepository{
		events: make([]EventRecord, 0),
	}
}

func (r *InMemoryEventRepository) Create(_ context.Context, event EventRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if event.ID == "" {
		event.ID = generateID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	r.events = append(r.events, event)
	return nil
}

func (r *InMemoryEventRepository) Query(_ context.Context, filter EventFilter) ([]EventRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []EventRecord
	for _, e := range r.events {
		if filter.EventType != "" && e.EventType != filter.EventType {
			continue
		}
		if filter.Source != "" && e.Source != filter.Source {
			continue
		}
		if filter.Since != nil && e.CreatedAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && e.CreatedAt.After(*filter.Until) {
			continue
		}
		out = append(out, e)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}
