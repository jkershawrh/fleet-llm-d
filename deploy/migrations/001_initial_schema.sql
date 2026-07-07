-- 001_initial_schema.sql
-- Initial database schema for fleet-llm-d control plane.

BEGIN;

-- ---------------------------------------------------------------------------
-- clusters
-- ---------------------------------------------------------------------------
CREATE TABLE clusters (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT        NOT NULL UNIQUE,
    region          TEXT        NOT NULL,
    labels          JSONB       NOT NULL DEFAULT '{}',
    gpu_available   INT         NOT NULL DEFAULT 0,
    gpu_total       INT         NOT NULL DEFAULT 0,
    status          TEXT        NOT NULL DEFAULT 'pending',
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_clusters_region ON clusters (region);
CREATE INDEX idx_clusters_status ON clusters (status);
CREATE INDEX idx_clusters_labels ON clusters USING GIN (labels);

COMMENT ON TABLE clusters IS 'Kubernetes clusters registered with the fleet control plane.';

-- ---------------------------------------------------------------------------
-- fleet_pools
-- ---------------------------------------------------------------------------
CREATE TABLE fleet_pools (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL UNIQUE,
    model_name       TEXT        NOT NULL,
    model_source     TEXT        NOT NULL,
    placement_policy JSONB       NOT NULL DEFAULT '{}',
    routing_policy   JSONB       NOT NULL DEFAULT '{}',
    scaling_policy   JSONB       NOT NULL DEFAULT '{}',
    status           TEXT        NOT NULL DEFAULT 'pending',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_fleet_pools_model_name ON fleet_pools (model_name);
CREATE INDEX idx_fleet_pools_status     ON fleet_pools (status);

COMMENT ON TABLE fleet_pools IS 'Logical pools that group model deployments across clusters.';

-- ---------------------------------------------------------------------------
-- pool_assignments
-- ---------------------------------------------------------------------------
CREATE TABLE pool_assignments (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    pool_id     UUID        NOT NULL REFERENCES fleet_pools (id) ON DELETE CASCADE,
    cluster_id  UUID        NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    replicas    INT         NOT NULL DEFAULT 1,
    gpu_type    TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'pending',
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_pool_assignments_pool_id    ON pool_assignments (pool_id);
CREATE INDEX idx_pool_assignments_cluster_id ON pool_assignments (cluster_id);
CREATE INDEX idx_pool_assignments_status     ON pool_assignments (status);

COMMENT ON TABLE pool_assignments IS 'Maps fleet pools to clusters with replica and GPU configuration.';

-- ---------------------------------------------------------------------------
-- tenants
-- ---------------------------------------------------------------------------
CREATE TABLE tenants (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT        NOT NULL UNIQUE,
    priority      INT         NOT NULL DEFAULT 0,
    quotas        JSONB       NOT NULL DEFAULT '{}',
    rate_limit    JSONB       NOT NULL DEFAULT '{}',
    cost_control  JSONB       NOT NULL DEFAULT '{}',
    cluster_scope JSONB       NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tenants_priority ON tenants (priority);

COMMENT ON TABLE tenants IS 'Tenants with quota, rate-limit, and cost-control policies.';

-- ---------------------------------------------------------------------------
-- tenant_usage
-- ---------------------------------------------------------------------------
CREATE TABLE tenant_usage (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    model           TEXT        NOT NULL,
    cluster_id      UUID,
    tokens_consumed BIGINT      NOT NULL DEFAULT 0,
    cost_usd        NUMERIC     NOT NULL DEFAULT 0,
    request_count   BIGINT      NOT NULL DEFAULT 0,
    period_start    TIMESTAMPTZ NOT NULL,
    period_end      TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_tenant_usage_tenant_id    ON tenant_usage (tenant_id);
CREATE INDEX idx_tenant_usage_period       ON tenant_usage (period_start, period_end);
CREATE INDEX idx_tenant_usage_model        ON tenant_usage (model);
CREATE INDEX idx_tenant_usage_cluster_id   ON tenant_usage (cluster_id);

COMMENT ON TABLE tenant_usage IS 'Aggregated usage records per tenant, model, and billing period.';

-- ---------------------------------------------------------------------------
-- rollouts
-- ---------------------------------------------------------------------------
CREATE TABLE rollouts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    pool_id         UUID        NOT NULL REFERENCES fleet_pools (id) ON DELETE CASCADE,
    model_version   TEXT        NOT NULL,
    strategy        JSONB       NOT NULL DEFAULT '{}',
    status          TEXT        NOT NULL DEFAULT 'pending',
    current_weight  INT         NOT NULL DEFAULT 0,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_rollouts_pool_id ON rollouts (pool_id);
CREATE INDEX idx_rollouts_status  ON rollouts (status);

COMMENT ON TABLE rollouts IS 'Progressive rollout records for model version upgrades within a pool.';

-- ---------------------------------------------------------------------------
-- rollout_cluster_status
-- ---------------------------------------------------------------------------
CREATE TABLE rollout_cluster_status (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    rollout_id  UUID        NOT NULL REFERENCES rollouts (id) ON DELETE CASCADE,
    cluster_id  UUID        NOT NULL,
    phase       TEXT        NOT NULL DEFAULT 'pending',
    weight      INT         NOT NULL DEFAULT 0,
    slo_met     BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rollout_cluster_status_rollout_id ON rollout_cluster_status (rollout_id);
CREATE INDEX idx_rollout_cluster_status_cluster_id ON rollout_cluster_status (cluster_id);
CREATE INDEX idx_rollout_cluster_status_phase      ON rollout_cluster_status (phase);

COMMENT ON TABLE rollout_cluster_status IS 'Per-cluster status for an active rollout, tracking weight and SLO compliance.';

-- ---------------------------------------------------------------------------
-- fleet_events  (range-partitioned by created_at)
-- ---------------------------------------------------------------------------
CREATE TABLE fleet_events (
    id          UUID        NOT NULL DEFAULT gen_random_uuid(),
    event_type  TEXT        NOT NULL,
    payload     JSONB       NOT NULL DEFAULT '{}',
    source      TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_fleet_events_event_type ON fleet_events (event_type);
CREATE INDEX idx_fleet_events_source     ON fleet_events (source);
CREATE INDEX idx_fleet_events_created_at ON fleet_events (created_at);

COMMENT ON TABLE fleet_events IS 'Append-only event log for fleet-wide activity, partitioned by month.';

-- Initial partition: July 2026
CREATE TABLE fleet_events_2026_07 PARTITION OF fleet_events
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

-- ---------------------------------------------------------------------------
-- kv_transfers
-- ---------------------------------------------------------------------------
CREATE TABLE kv_transfers (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_cluster     UUID        NOT NULL,
    target_cluster     UUID        NOT NULL,
    model              TEXT        NOT NULL,
    transfer_type      TEXT        NOT NULL,
    status             TEXT        NOT NULL DEFAULT 'pending',
    bytes_transferred  BIGINT      NOT NULL DEFAULT 0,
    started_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ
);

CREATE INDEX idx_kv_transfers_source_cluster ON kv_transfers (source_cluster);
CREATE INDEX idx_kv_transfers_target_cluster ON kv_transfers (target_cluster);
CREATE INDEX idx_kv_transfers_status         ON kv_transfers (status);
CREATE INDEX idx_kv_transfers_model          ON kv_transfers (model);

COMMENT ON TABLE kv_transfers IS 'KV-cache transfer records between clusters for live migration and prefill offloading.';

COMMIT;
