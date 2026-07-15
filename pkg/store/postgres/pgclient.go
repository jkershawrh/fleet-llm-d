package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PGClient provides PostgreSQL-backed implementations of all repository
// interfaces. It takes a *sql.DB so the caller chooses the driver (lib/pq,
// pgx/stdlib, etc.).
type PGClient struct {
	db *sql.DB
}

// NewPGClient creates a connection to PostgreSQL using the given connection
// string and the "postgres" driver. The caller must ensure the driver is
// imported (e.g., _ "github.com/lib/pq").
func NewPGClient(connString string) (*PGClient, error) {
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PGClient{db: db}, nil
}

// NewPGClientFromDB wraps an existing *sql.DB so the driver choice is the
// caller's problem. Useful for testing with any sql-compatible driver.
func NewPGClientFromDB(db *sql.DB) *PGClient {
	return &PGClient{db: db}
}

// Close closes the underlying database connection pool.
func (c *PGClient) Close() error {
	return c.db.Close()
}

// Ping verifies the database is reachable.
func (c *PGClient) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

// EnsureSchema creates all required tables if they do not already exist.
func (c *PGClient) EnsureSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS clusters (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL UNIQUE, region TEXT NOT NULL,
			labels JSONB NOT NULL DEFAULT '{}',
			gpu_available INT NOT NULL DEFAULT 0, gpu_total INT NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS fleet_pools (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL UNIQUE, model_name TEXT NOT NULL, model_source TEXT NOT NULL,
			placement_policy JSONB NOT NULL DEFAULT '{}',
			routing_policy JSONB NOT NULL DEFAULT '{}',
			scaling_policy JSONB NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS tenants (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL UNIQUE, priority INT NOT NULL DEFAULT 0,
			quotas JSONB NOT NULL DEFAULT '{}',
			rate_limit JSONB NOT NULL DEFAULT '{}',
			cost_control JSONB NOT NULL DEFAULT '{}',
			cluster_scope JSONB NOT NULL DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS tenant_usage (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL, model TEXT NOT NULL, cluster_id UUID,
			tokens_consumed BIGINT NOT NULL DEFAULT 0,
			cost_usd NUMERIC NOT NULL DEFAULT 0,
			request_count BIGINT NOT NULL DEFAULT 0,
			period_start TIMESTAMPTZ NOT NULL, period_end TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS rollouts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			pool_id UUID NOT NULL, model_version TEXT NOT NULL,
			strategy JSONB NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending',
			current_weight INT NOT NULL DEFAULT 0,
			started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			completed_at TIMESTAMPTZ)`,
		`CREATE TABLE IF NOT EXISTS fleet_events (
			id UUID NOT NULL DEFAULT gen_random_uuid(),
			event_type TEXT NOT NULL, payload JSONB NOT NULL DEFAULT '{}',
			source TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		) PARTITION BY RANGE (created_at)`,
	}
	for _, stmt := range statements {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("schema init: %w", err)
		}
	}
	now := time.Now()
	partName := fmt.Sprintf("fleet_events_%d_%02d", now.Year(), now.Month())
	start := fmt.Sprintf("%d-%02d-01", now.Year(), now.Month())
	nextMonth := now.AddDate(0, 1, 0)
	end := fmt.Sprintf("%d-%02d-01", nextMonth.Year(), nextMonth.Month())
	partSQL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF fleet_events FOR VALUES FROM ('%s') TO ('%s')",
		partName, start, end)
	if _, err := c.db.ExecContext(ctx, partSQL); err != nil {
		return fmt.Errorf("partition init: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ClusterRepository implementation
// ---------------------------------------------------------------------------

// CreateCluster inserts a new cluster record. If the ID is empty the database
// generates a UUID via gen_random_uuid().
func (c *PGClient) CreateCluster(ctx context.Context, cluster ClusterRecord) error {
	labelsJSON, err := json.Marshal(cluster.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	query := `
		INSERT INTO clusters (id, name, region, labels, gpu_available, gpu_total, status, registered_at, updated_at)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()::text), $2, $3, $4, $5, $6, $7,
		        COALESCE(NULLIF($8::timestamptz, '0001-01-01'::timestamptz), now()),
		        COALESCE(NULLIF($9::timestamptz, '0001-01-01'::timestamptz), now()))
	`
	_, err = c.db.ExecContext(ctx, query,
		cluster.ID, cluster.Name, cluster.Region, labelsJSON,
		cluster.GPUAvailable, cluster.GPUTotal, defaultString(cluster.Status, "pending"),
		cluster.RegisteredAt, cluster.UpdatedAt,
	)
	return err
}

// GetCluster retrieves a cluster by ID. Returns an error if not found.
func (c *PGClient) GetCluster(ctx context.Context, id string) (*ClusterRecord, error) {
	query := `
		SELECT id, name, region, labels, gpu_available, gpu_total, status, registered_at, updated_at
		FROM clusters WHERE id = $1
	`
	var rec ClusterRecord
	var labelsJSON []byte
	err := c.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.Name, &rec.Region, &labelsJSON,
		&rec.GPUAvailable, &rec.GPUTotal, &rec.Status,
		&rec.RegisteredAt, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("cluster %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(labelsJSON, &rec.Labels); err != nil {
		return nil, fmt.Errorf("unmarshal labels: %w", err)
	}
	return &rec, nil
}

// ListClusters returns all clusters.
func (c *PGClient) ListClusters(ctx context.Context) ([]ClusterRecord, error) {
	query := `
		SELECT id, name, region, labels, gpu_available, gpu_total, status, registered_at, updated_at
		FROM clusters ORDER BY registered_at
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ClusterRecord
	for rows.Next() {
		var rec ClusterRecord
		var labelsJSON []byte
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.Region, &labelsJSON,
			&rec.GPUAvailable, &rec.GPUTotal, &rec.Status,
			&rec.RegisteredAt, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labelsJSON, &rec.Labels); err != nil {
			return nil, fmt.Errorf("unmarshal labels: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// UpdateCluster updates an existing cluster. The ID must match an existing row.
func (c *PGClient) UpdateCluster(ctx context.Context, cluster ClusterRecord) error {
	labelsJSON, err := json.Marshal(cluster.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	query := `
		UPDATE clusters
		SET name = $2, region = $3, labels = $4, gpu_available = $5,
		    gpu_total = $6, status = $7, updated_at = now()
		WHERE id = $1
	`
	res, err := c.db.ExecContext(ctx, query,
		cluster.ID, cluster.Name, cluster.Region, labelsJSON,
		cluster.GPUAvailable, cluster.GPUTotal, cluster.Status,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res, "cluster", cluster.ID)
}

// DeleteCluster removes a cluster by ID.
func (c *PGClient) DeleteCluster(ctx context.Context, id string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM clusters WHERE id = $1`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res, "cluster", id)
}

// ---------------------------------------------------------------------------
// FleetPoolRepository implementation
// ---------------------------------------------------------------------------

// CreatePool inserts a new fleet pool record.
func (c *PGClient) CreatePool(ctx context.Context, pool FleetPoolRecord) error {
	query := `
		INSERT INTO fleet_pools (id, name, model_name, model_source,
		    placement_policy, routing_policy, scaling_policy, status,
		    created_at, updated_at)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()::text), $2, $3, $4,
		        $5::jsonb, $6::jsonb, $7::jsonb, $8,
		        COALESCE(NULLIF($9::timestamptz, '0001-01-01'::timestamptz), now()),
		        COALESCE(NULLIF($10::timestamptz, '0001-01-01'::timestamptz), now()))
	`
	_, err := c.db.ExecContext(ctx, query,
		pool.ID, pool.Name, pool.ModelName, pool.ModelSource,
		defaultString(pool.PlacementPolicy, "{}"),
		defaultString(pool.RoutingPolicy, "{}"),
		defaultString(pool.ScalingPolicy, "{}"),
		defaultString(pool.Status, "pending"),
		pool.CreatedAt, pool.UpdatedAt,
	)
	return err
}

// GetPool retrieves a fleet pool by ID.
func (c *PGClient) GetPool(ctx context.Context, id string) (*FleetPoolRecord, error) {
	query := `
		SELECT id, name, model_name, model_source,
		       placement_policy, routing_policy, scaling_policy,
		       status, created_at, updated_at
		FROM fleet_pools WHERE id = $1
	`
	var rec FleetPoolRecord
	err := c.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.Name, &rec.ModelName, &rec.ModelSource,
		&rec.PlacementPolicy, &rec.RoutingPolicy, &rec.ScalingPolicy,
		&rec.Status, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("fleet pool %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// ListPools returns all fleet pools.
func (c *PGClient) ListPools(ctx context.Context) ([]FleetPoolRecord, error) {
	query := `
		SELECT id, name, model_name, model_source,
		       placement_policy, routing_policy, scaling_policy,
		       status, created_at, updated_at
		FROM fleet_pools ORDER BY created_at
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FleetPoolRecord
	for rows.Next() {
		var rec FleetPoolRecord
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.ModelName, &rec.ModelSource,
			&rec.PlacementPolicy, &rec.RoutingPolicy, &rec.ScalingPolicy,
			&rec.Status, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// UpdatePool updates an existing fleet pool.
func (c *PGClient) UpdatePool(ctx context.Context, pool FleetPoolRecord) error {
	query := `
		UPDATE fleet_pools
		SET name = $2, model_name = $3, model_source = $4,
		    placement_policy = $5::jsonb, routing_policy = $6::jsonb,
		    scaling_policy = $7::jsonb, status = $8, updated_at = now()
		WHERE id = $1
	`
	res, err := c.db.ExecContext(ctx, query,
		pool.ID, pool.Name, pool.ModelName, pool.ModelSource,
		defaultString(pool.PlacementPolicy, "{}"),
		defaultString(pool.RoutingPolicy, "{}"),
		defaultString(pool.ScalingPolicy, "{}"),
		pool.Status,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res, "fleet pool", pool.ID)
}

// DeletePool removes a fleet pool by ID.
func (c *PGClient) DeletePool(ctx context.Context, id string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM fleet_pools WHERE id = $1`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res, "fleet pool", id)
}

// ---------------------------------------------------------------------------
// TenantRepository implementation
// ---------------------------------------------------------------------------

// CreateTenant inserts a new tenant record.
func (c *PGClient) CreateTenant(ctx context.Context, tenant TenantRecord) error {
	quotasJSON, err := json.Marshal(tenant.Quotas)
	if err != nil {
		return fmt.Errorf("marshal quotas: %w", err)
	}
	rateLimitJSON, err := json.Marshal(tenant.RateLimit)
	if err != nil {
		return fmt.Errorf("marshal rate_limit: %w", err)
	}
	costControlJSON, err := json.Marshal(tenant.CostControl)
	if err != nil {
		return fmt.Errorf("marshal cost_control: %w", err)
	}
	clusterScopeJSON, err := json.Marshal(tenant.ClusterScope)
	if err != nil {
		return fmt.Errorf("marshal cluster_scope: %w", err)
	}
	query := `
		INSERT INTO tenants (id, name, priority, quotas, rate_limit, cost_control, cluster_scope, created_at)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()::text), $2, $3,
		        $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb,
		        COALESCE(NULLIF($8::timestamptz, '0001-01-01'::timestamptz), now()))
	`
	_, err = c.db.ExecContext(ctx, query,
		tenant.ID, tenant.Name, tenant.Priority,
		quotasJSON, rateLimitJSON, costControlJSON, clusterScopeJSON,
		tenant.CreatedAt,
	)
	return err
}

// GetTenant retrieves a tenant by ID.
func (c *PGClient) GetTenant(ctx context.Context, id string) (*TenantRecord, error) {
	query := `
		SELECT id, name, priority, quotas, rate_limit, cost_control, cluster_scope, created_at
		FROM tenants WHERE id = $1
	`
	var rec TenantRecord
	var quotasJSON, rateLimitJSON, costControlJSON, clusterScopeJSON []byte
	err := c.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.Name, &rec.Priority,
		&quotasJSON, &rateLimitJSON, &costControlJSON, &clusterScopeJSON,
		&rec.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tenant %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	if err := unmarshalJSONFields(&rec, quotasJSON, rateLimitJSON, costControlJSON, clusterScopeJSON); err != nil {
		return nil, err
	}
	return &rec, nil
}

// ListTenants returns all tenants.
func (c *PGClient) ListTenants(ctx context.Context) ([]TenantRecord, error) {
	query := `
		SELECT id, name, priority, quotas, rate_limit, cost_control, cluster_scope, created_at
		FROM tenants ORDER BY created_at
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TenantRecord
	for rows.Next() {
		var rec TenantRecord
		var quotasJSON, rateLimitJSON, costControlJSON, clusterScopeJSON []byte
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.Priority,
			&quotasJSON, &rateLimitJSON, &costControlJSON, &clusterScopeJSON,
			&rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := unmarshalJSONFields(&rec, quotasJSON, rateLimitJSON, costControlJSON, clusterScopeJSON); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// UpdateTenant updates an existing tenant record.
func (c *PGClient) UpdateTenant(ctx context.Context, tenant TenantRecord) error {
	quotasJSON, err := json.Marshal(tenant.Quotas)
	if err != nil {
		return fmt.Errorf("marshal quotas: %w", err)
	}
	rateLimitJSON, err := json.Marshal(tenant.RateLimit)
	if err != nil {
		return fmt.Errorf("marshal rate_limit: %w", err)
	}
	costControlJSON, err := json.Marshal(tenant.CostControl)
	if err != nil {
		return fmt.Errorf("marshal cost_control: %w", err)
	}
	clusterScopeJSON, err := json.Marshal(tenant.ClusterScope)
	if err != nil {
		return fmt.Errorf("marshal cluster_scope: %w", err)
	}
	query := `
		UPDATE tenants
		SET name = $2, priority = $3, quotas = $4::jsonb, rate_limit = $5::jsonb,
		    cost_control = $6::jsonb, cluster_scope = $7::jsonb
		WHERE id = $1
	`
	res, err := c.db.ExecContext(ctx, query,
		tenant.ID, tenant.Name, tenant.Priority,
		quotasJSON, rateLimitJSON, costControlJSON, clusterScopeJSON,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res, "tenant", tenant.ID)
}

// DeleteTenant removes a tenant by ID.
func (c *PGClient) DeleteTenant(ctx context.Context, id string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res, "tenant", id)
}

// RecordUsage inserts a tenant usage record.
func (c *PGClient) RecordUsage(ctx context.Context, usage TenantUsageRecord) error {
	query := `
		INSERT INTO tenant_usage (id, tenant_id, model, cluster_id, tokens_consumed,
		    cost_usd, request_count, period_start, period_end)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()::text), $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := c.db.ExecContext(ctx, query,
		usage.ID, usage.TenantID, usage.Model, nullableString(usage.ClusterID),
		usage.TokensConsumed, usage.CostUSD, usage.RequestCount,
		usage.PeriodStart, usage.PeriodEnd,
	)
	return err
}

// GetUsage retrieves usage records for a tenant within a time window.
func (c *PGClient) GetUsage(ctx context.Context, tenantID string, start, end time.Time) ([]TenantUsageRecord, error) {
	query := `
		SELECT id, tenant_id, model, COALESCE(cluster_id::text, ''), tokens_consumed,
		       cost_usd, request_count, period_start, period_end
		FROM tenant_usage
		WHERE tenant_id = $1 AND period_start >= $2 AND period_end <= $3
		ORDER BY period_start
	`
	rows, err := c.db.QueryContext(ctx, query, tenantID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TenantUsageRecord
	for rows.Next() {
		var rec TenantUsageRecord
		if err := rows.Scan(
			&rec.ID, &rec.TenantID, &rec.Model, &rec.ClusterID,
			&rec.TokensConsumed, &rec.CostUSD, &rec.RequestCount,
			&rec.PeriodStart, &rec.PeriodEnd,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// RolloutRepository implementation
// ---------------------------------------------------------------------------

// CreateRollout inserts a new rollout record.
func (c *PGClient) CreateRollout(ctx context.Context, rollout RolloutRecord) error {
	strategyJSON, err := json.Marshal(rollout.Strategy)
	if err != nil {
		return fmt.Errorf("marshal strategy: %w", err)
	}
	query := `
		INSERT INTO rollouts (id, pool_id, model_version, strategy, status,
		    current_weight, started_at, completed_at)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()::text), $2, $3,
		        $4::jsonb, $5, $6,
		        COALESCE(NULLIF($7::timestamptz, '0001-01-01'::timestamptz), now()), $8)
	`
	_, err = c.db.ExecContext(ctx, query,
		rollout.ID, rollout.PoolID, rollout.ModelVersion,
		strategyJSON, defaultString(rollout.Status, "pending"),
		rollout.CurrentWeight, rollout.StartedAt, rollout.CompletedAt,
	)
	return err
}

// GetRollout retrieves a rollout by ID.
func (c *PGClient) GetRollout(ctx context.Context, id string) (*RolloutRecord, error) {
	query := `
		SELECT id, pool_id, model_version, strategy, status,
		       current_weight, started_at, completed_at
		FROM rollouts WHERE id = $1
	`
	var rec RolloutRecord
	var strategyJSON []byte
	err := c.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.PoolID, &rec.ModelVersion, &strategyJSON,
		&rec.Status, &rec.CurrentWeight, &rec.StartedAt, &rec.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("rollout %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(strategyJSON, &rec.Strategy); err != nil {
		return nil, fmt.Errorf("unmarshal strategy: %w", err)
	}
	return &rec, nil
}

// ListRollouts returns all rollouts.
func (c *PGClient) ListRollouts(ctx context.Context) ([]RolloutRecord, error) {
	query := `
		SELECT id, pool_id, model_version, strategy, status,
		       current_weight, started_at, completed_at
		FROM rollouts ORDER BY started_at
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RolloutRecord
	for rows.Next() {
		var rec RolloutRecord
		var strategyJSON []byte
		if err := rows.Scan(
			&rec.ID, &rec.PoolID, &rec.ModelVersion, &strategyJSON,
			&rec.Status, &rec.CurrentWeight, &rec.StartedAt, &rec.CompletedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(strategyJSON, &rec.Strategy); err != nil {
			return nil, fmt.Errorf("unmarshal strategy: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// UpdateRollout updates an existing rollout record.
func (c *PGClient) UpdateRollout(ctx context.Context, rollout RolloutRecord) error {
	strategyJSON, err := json.Marshal(rollout.Strategy)
	if err != nil {
		return fmt.Errorf("marshal strategy: %w", err)
	}
	query := `
		UPDATE rollouts
		SET pool_id = $2, model_version = $3, strategy = $4::jsonb,
		    status = $5, current_weight = $6, completed_at = $7
		WHERE id = $1
	`
	res, err := c.db.ExecContext(ctx, query,
		rollout.ID, rollout.PoolID, rollout.ModelVersion,
		strategyJSON, rollout.Status, rollout.CurrentWeight,
		rollout.CompletedAt,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res, "rollout", rollout.ID)
}

// ---------------------------------------------------------------------------
// EventRepository implementation
// ---------------------------------------------------------------------------

// CreateEvent inserts an event into the fleet_events partitioned table.
func (c *PGClient) CreateEvent(ctx context.Context, event EventRecord) error {
	payloadJSON, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	query := `
		INSERT INTO fleet_events (id, event_type, payload, source, created_at)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()::text), $2, $3::jsonb, $4,
		        COALESCE(NULLIF($5::timestamptz, '0001-01-01'::timestamptz), now()))
	`
	_, err = c.db.ExecContext(ctx, query,
		event.ID, event.EventType, payloadJSON, event.Source, event.CreatedAt,
	)
	return err
}

// QueryEvents returns events matching the given filter.
func (c *PGClient) QueryEvents(ctx context.Context, filter EventFilter) ([]EventRecord, error) {
	// Build the query dynamically based on filter fields.
	query := `SELECT id, event_type, payload, source, created_at FROM fleet_events WHERE 1=1`
	args := make([]interface{}, 0)
	argIdx := 1

	if filter.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argIdx)
		args = append(args, filter.EventType)
		argIdx++
	}
	if filter.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, filter.Source)
		argIdx++
	}
	if filter.Since != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, *filter.Since)
		argIdx++
	}
	if filter.Until != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argIdx)
		args = append(args, *filter.Until)
		argIdx++
	}

	query += " ORDER BY created_at"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var rec EventRecord
		var payloadJSON []byte
		if err := rows.Scan(
			&rec.ID, &rec.EventType, &payloadJSON, &rec.Source, &rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payloadJSON, &rec.Payload); err != nil {
			return nil, fmt.Errorf("unmarshal payload: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Adapter types
// ---------------------------------------------------------------------------
// Each repository interface uses the same method names (Create, Get, List,
// Update, Delete) but with different record types, so a single struct cannot
// implement them all. Thin adapters bridge PGClient to each interface.

// ---------------------------------------------------------------------------
// Adapter: PGClusterRepository
// ---------------------------------------------------------------------------

// PGClusterRepository adapts PGClient to the ClusterRepository interface.
type PGClusterRepository struct {
	pg *PGClient
}

// NewPGClusterRepository returns a ClusterRepository backed by PostgreSQL.
func NewPGClusterRepository(pg *PGClient) *PGClusterRepository {
	return &PGClusterRepository{pg: pg}
}

func (r *PGClusterRepository) Create(ctx context.Context, cluster ClusterRecord) error {
	return r.pg.CreateCluster(ctx, cluster)
}

func (r *PGClusterRepository) Get(ctx context.Context, id string) (*ClusterRecord, error) {
	return r.pg.GetCluster(ctx, id)
}

func (r *PGClusterRepository) List(ctx context.Context) ([]ClusterRecord, error) {
	return r.pg.ListClusters(ctx)
}

func (r *PGClusterRepository) Update(ctx context.Context, cluster ClusterRecord) error {
	return r.pg.UpdateCluster(ctx, cluster)
}

func (r *PGClusterRepository) Delete(ctx context.Context, id string) error {
	return r.pg.DeleteCluster(ctx, id)
}

var _ ClusterRepository = (*PGClusterRepository)(nil)

// ---------------------------------------------------------------------------
// Adapter: PGTenantRepository
// ---------------------------------------------------------------------------

// PGTenantRepository adapts PGClient to the TenantRepository interface.
type PGTenantRepository struct {
	pg *PGClient
}

// NewPGTenantRepository returns a TenantRepository backed by PostgreSQL.
func NewPGTenantRepository(pg *PGClient) *PGTenantRepository {
	return &PGTenantRepository{pg: pg}
}

func (r *PGTenantRepository) Create(ctx context.Context, tenant TenantRecord) error {
	return r.pg.CreateTenant(ctx, tenant)
}

func (r *PGTenantRepository) Get(ctx context.Context, id string) (*TenantRecord, error) {
	return r.pg.GetTenant(ctx, id)
}

func (r *PGTenantRepository) List(ctx context.Context) ([]TenantRecord, error) {
	return r.pg.ListTenants(ctx)
}

func (r *PGTenantRepository) Update(ctx context.Context, tenant TenantRecord) error {
	return r.pg.UpdateTenant(ctx, tenant)
}

func (r *PGTenantRepository) Delete(ctx context.Context, id string) error {
	return r.pg.DeleteTenant(ctx, id)
}

func (r *PGTenantRepository) RecordUsage(ctx context.Context, usage TenantUsageRecord) error {
	return r.pg.RecordUsage(ctx, usage)
}

func (r *PGTenantRepository) GetUsage(ctx context.Context, tenantID string, start, end time.Time) ([]TenantUsageRecord, error) {
	return r.pg.GetUsage(ctx, tenantID, start, end)
}

var _ TenantRepository = (*PGTenantRepository)(nil)

// ---------------------------------------------------------------------------
// Adapter: PGRolloutRepository
// ---------------------------------------------------------------------------

// PGRolloutRepository adapts PGClient to the RolloutRepository interface.
type PGRolloutRepository struct {
	pg *PGClient
}

// NewPGRolloutRepository returns a RolloutRepository backed by PostgreSQL.
func NewPGRolloutRepository(pg *PGClient) *PGRolloutRepository {
	return &PGRolloutRepository{pg: pg}
}

func (r *PGRolloutRepository) Create(ctx context.Context, rollout RolloutRecord) error {
	return r.pg.CreateRollout(ctx, rollout)
}

func (r *PGRolloutRepository) Get(ctx context.Context, id string) (*RolloutRecord, error) {
	return r.pg.GetRollout(ctx, id)
}

func (r *PGRolloutRepository) List(ctx context.Context) ([]RolloutRecord, error) {
	return r.pg.ListRollouts(ctx)
}

func (r *PGRolloutRepository) Update(ctx context.Context, rollout RolloutRecord) error {
	return r.pg.UpdateRollout(ctx, rollout)
}

var _ RolloutRepository = (*PGRolloutRepository)(nil)

// ---------------------------------------------------------------------------
// Adapter: PGFleetPoolRepository
// ---------------------------------------------------------------------------

// PGFleetPoolRepository adapts PGClient to the FleetPoolRepository interface.
type PGFleetPoolRepository struct {
	pg *PGClient
}

// NewPGFleetPoolRepository returns a FleetPoolRepository backed by PostgreSQL.
func NewPGFleetPoolRepository(pg *PGClient) *PGFleetPoolRepository {
	return &PGFleetPoolRepository{pg: pg}
}

func (r *PGFleetPoolRepository) Create(ctx context.Context, pool FleetPoolRecord) error {
	return r.pg.CreatePool(ctx, pool)
}

func (r *PGFleetPoolRepository) Get(ctx context.Context, id string) (*FleetPoolRecord, error) {
	return r.pg.GetPool(ctx, id)
}

func (r *PGFleetPoolRepository) List(ctx context.Context) ([]FleetPoolRecord, error) {
	return r.pg.ListPools(ctx)
}

func (r *PGFleetPoolRepository) Update(ctx context.Context, pool FleetPoolRecord) error {
	return r.pg.UpdatePool(ctx, pool)
}

func (r *PGFleetPoolRepository) Delete(ctx context.Context, id string) error {
	return r.pg.DeletePool(ctx, id)
}

var _ FleetPoolRepository = (*PGFleetPoolRepository)(nil)

// ---------------------------------------------------------------------------
// Adapter: PGEventRepository
// ---------------------------------------------------------------------------

// PGEventRepository adapts PGClient to the EventRepository interface.
type PGEventRepository struct {
	pg *PGClient
}

// NewPGEventRepository returns an EventRepository backed by PostgreSQL.
func NewPGEventRepository(pg *PGClient) *PGEventRepository {
	return &PGEventRepository{pg: pg}
}

func (r *PGEventRepository) Create(ctx context.Context, event EventRecord) error {
	return r.pg.CreateEvent(ctx, event)
}

func (r *PGEventRepository) Query(ctx context.Context, filter EventFilter) ([]EventRecord, error) {
	return r.pg.QueryEvents(ctx, filter)
}

var _ EventRepository = (*PGEventRepository)(nil)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// defaultString returns s if non-empty, otherwise dflt.
func defaultString(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

// nullableString returns a *string that is nil when s is empty, for SQL NULL
// handling on nullable UUID columns.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// expectOneRow verifies exactly one row was affected.
func expectOneRow(res sql.Result, entity, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s %q not found", entity, id)
	}
	return nil
}

// unmarshalJSONFields unmarshals the four JSONB tenant fields.
func unmarshalJSONFields(rec *TenantRecord, quotas, rateLimit, costControl, clusterScope []byte) error {
	if len(quotas) > 0 {
		if err := json.Unmarshal(quotas, &rec.Quotas); err != nil {
			return fmt.Errorf("unmarshal quotas: %w", err)
		}
	}
	if len(rateLimit) > 0 {
		if err := json.Unmarshal(rateLimit, &rec.RateLimit); err != nil {
			return fmt.Errorf("unmarshal rate_limit: %w", err)
		}
	}
	if len(costControl) > 0 {
		if err := json.Unmarshal(costControl, &rec.CostControl); err != nil {
			return fmt.Errorf("unmarshal cost_control: %w", err)
		}
	}
	if len(clusterScope) > 0 {
		if err := json.Unmarshal(clusterScope, &rec.ClusterScope); err != nil {
			return fmt.Errorf("unmarshal cluster_scope: %w", err)
		}
	}
	return nil
}
