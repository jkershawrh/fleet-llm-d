-- Align existing installations with the controller's externally supplied
-- resource IDs (for example "cluster-east" and "rollout-model-v2-1").
BEGIN;

ALTER TABLE pool_assignments DROP CONSTRAINT IF EXISTS pool_assignments_pool_id_fkey;
ALTER TABLE pool_assignments DROP CONSTRAINT IF EXISTS pool_assignments_cluster_id_fkey;
ALTER TABLE tenant_usage DROP CONSTRAINT IF EXISTS tenant_usage_tenant_id_fkey;
ALTER TABLE rollouts DROP CONSTRAINT IF EXISTS rollouts_pool_id_fkey;
ALTER TABLE rollout_cluster_status DROP CONSTRAINT IF EXISTS rollout_cluster_status_rollout_id_fkey;

ALTER TABLE clusters ALTER COLUMN id DROP DEFAULT;
ALTER TABLE fleet_pools ALTER COLUMN id DROP DEFAULT;
ALTER TABLE pool_assignments ALTER COLUMN id DROP DEFAULT;
ALTER TABLE tenants ALTER COLUMN id DROP DEFAULT;
ALTER TABLE tenant_usage ALTER COLUMN id DROP DEFAULT;
ALTER TABLE rollouts ALTER COLUMN id DROP DEFAULT;
ALTER TABLE rollout_cluster_status ALTER COLUMN id DROP DEFAULT;
ALTER TABLE fleet_events ALTER COLUMN id DROP DEFAULT;
ALTER TABLE kv_transfers ALTER COLUMN id DROP DEFAULT;

ALTER TABLE clusters ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE fleet_pools ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE pool_assignments ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE pool_assignments ALTER COLUMN pool_id TYPE TEXT USING pool_id::text;
ALTER TABLE pool_assignments ALTER COLUMN cluster_id TYPE TEXT USING cluster_id::text;
ALTER TABLE tenants ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE tenant_usage ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE tenant_usage ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE tenant_usage ALTER COLUMN cluster_id TYPE TEXT USING cluster_id::text;
ALTER TABLE rollouts ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE rollouts ALTER COLUMN pool_id TYPE TEXT USING pool_id::text;
ALTER TABLE rollout_cluster_status ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE rollout_cluster_status ALTER COLUMN rollout_id TYPE TEXT USING rollout_id::text;
ALTER TABLE rollout_cluster_status ALTER COLUMN cluster_id TYPE TEXT USING cluster_id::text;
ALTER TABLE fleet_events ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE kv_transfers ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE kv_transfers ALTER COLUMN source_cluster TYPE TEXT USING source_cluster::text;
ALTER TABLE kv_transfers ALTER COLUMN target_cluster TYPE TEXT USING target_cluster::text;

ALTER TABLE clusters ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE fleet_pools ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE pool_assignments ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE tenants ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE tenant_usage ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE rollouts ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE rollout_cluster_status ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE fleet_events ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE kv_transfers ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;

ALTER TABLE pool_assignments ADD CONSTRAINT pool_assignments_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES fleet_pools(id) ON DELETE CASCADE;
ALTER TABLE pool_assignments ADD CONSTRAINT pool_assignments_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE;
ALTER TABLE tenant_usage ADD CONSTRAINT tenant_usage_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE rollouts ADD CONSTRAINT rollouts_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES fleet_pools(id) ON DELETE CASCADE;
ALTER TABLE rollout_cluster_status ADD CONSTRAINT rollout_cluster_status_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES rollouts(id) ON DELETE CASCADE;

COMMIT;
