const SERVER_API_BASE = process.env.FLEET_API_URL || process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'

function apiBase(): string {
  return typeof window === 'undefined' ? SERVER_API_BASE : process.env.NEXT_PUBLIC_API_URL || '/api/fleet'
}

// --- Type Definitions ---

export interface Cluster {
  id: string
  name: string
  region: string
  status: string
  gpuAvailable: number
  gpuTotal: number
  gpuType: string
  throughput: number
  ttftP99: number
  kvCacheHitRate: number
}

export interface ClusterAssignment {
  cluster: string
  replicas: number
  gpuType: string
  status: string
}

export interface FleetPool {
  id: string
  name: string
  model: string
  modelVersion: string
  status: string
  clusters: ClusterAssignment[]
  rolloutStrategy: string
  totalThroughput: number
  avgTTFT: number
  kvCacheHitRate: number
}

export interface Tenant {
  id: string
  name: string
  priority: number
  maxTokensPerMinute: number
  maxConcurrentRequests: number
  maxModels: number
  gpuBudget: number
  monthlyBudget: string
  alertThreshold: number
  tokensConsumed: number
  currentMonthCost: string
  avgLatency: number
  activeModels: number
  currentConcurrentRequests: number
}

export interface TenantUsage {
  tenantId: string
  tenantName: string
  tokensConsumed: number
  currentMonthCost: string
  totalRequests: number
  avgLatency: number
  modelBreakdown: ModelUsage[]
}

export interface ModelUsage {
  model: string
  tokensConsumed: number
  requests: number
  cost: string
}

export interface Rollout {
  id: string
  model: string
  version: string
  strategy: 'canary' | 'rolling' | 'blue-green'
  phase: 'Pending' | 'Progressing' | 'Paused' | 'Complete' | 'RolledBack' | 'Failed'
  weight: number
  startTime: string
  completionTime?: string
  clusterStatus: RolloutClusterStatus[]
}

export interface RolloutClusterStatus {
  cluster: string
  phase: string
  currentWeight: number
  sloMet: boolean
  lastCheckTime: string
}

export interface FleetMetrics {
  totalClusters: number
  totalGpus: number
  gpusAvailable: number
  activeModels: number
  totalThroughput: number
  avgTtft: number
  avgKvCacheHitRate: number
}

export interface ModelMetrics {
  model: string
  throughput: number
  ttftP50: number
  ttftP99: number
  kvCacheHitRate: number
  gpuUtilization: number
  requestsPerSecond: number
}

export interface ChainVerification {
  chainType: string
  valid: boolean
  entriesChecked: number
  lastVerified: string
  latestEntryTime: string
}

// --- API Functions ---

async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const serverToken = typeof window === 'undefined' ? process.env.FLEET_API_TOKEN : undefined
  const res = await fetch(`${apiBase()}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(serverToken ? { Authorization: `Bearer ${serverToken}` } : {}),
      ...options?.headers,
    },
    ...(options?.method ? { cache: 'no-store' as const } : { next: { revalidate: 30 } }),
  })

  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`)
  }

  const body = await res.text()
  return (body ? JSON.parse(body) : undefined) as T
}

export async function fetchClusters(): Promise<Cluster[]> {
  const rows = await apiFetch<Record<string, unknown>[]>('/api/v1/clusters')
  return rows.map(normalizeCluster)
}
export async function fetchPools(): Promise<FleetPool[]> {
  const rows = await apiFetch<Record<string, unknown>[]>('/api/v1/pools')
  return rows.map(normalizePool)
}
export async function fetchTenants(): Promise<Tenant[]> {
  const rows = await apiFetch<Record<string, unknown>[]>('/api/v1/tenants')
  return rows.map(normalizeTenant)
}

export async function fetchTenantUsage(id: string): Promise<TenantUsage> {
  const row = await apiFetch<Record<string, unknown>>(`/api/v1/tenants/${id}/usage`)
  return normalizeTenantUsage(row)
}

export async function fetchFleetMetrics(): Promise<FleetMetrics> {
  const row = await apiFetch<Record<string, unknown>>('/api/v1/metrics/fleet')
  const clusters = arrayValue(row.Clusters ?? row.clusters)
  return {
    totalClusters: clusters.length,
    totalGpus: numberValue(row.TotalGPUs ?? row.totalGpus),
    gpusAvailable: numberValue(row.GPUsAvailable ?? row.gpusAvailable),
    activeModels: numberValue(row.ActiveModels ?? row.activeModels),
    totalThroughput: numberValue(row.TotalThroughput ?? row.totalThroughput),
    avgTtft: numberValue(row.AvgTTFT_Ms ?? row.avgTtft),
    avgKvCacheHitRate: numberValue(row.AvgKVCacheHitRate ?? row.avgKvCacheHitRate),
  }
}

export async function fetchModelMetrics(model: string): Promise<ModelMetrics> {
  return apiFetch<ModelMetrics>(`/api/v1/metrics/model/${model}`)
}

export async function fetchRollouts(): Promise<Rollout[]> {
  const rows = await apiFetch<Record<string, unknown>[]>('/api/v1/rollouts')
  return rows.map(normalizeRollout)
}

export async function registerCluster(input: { name: string; region: string }): Promise<void> {
  await apiFetch('/api/v1/clusters', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function promoteRollout(id: string): Promise<void> {
  await apiFetch<void>(`/api/v1/rollouts/${id}/promote`, { method: 'POST' })
}

export async function rollbackRollout(id: string): Promise<void> {
  await apiFetch<void>(`/api/v1/rollouts/${id}/rollback`, { method: 'POST' })
}

export async function verifyChains(): Promise<Record<string, ChainVerification>> {
  const rows = await apiFetch<Record<string, Record<string, unknown>>>('/api/v1/verify/chains')
  return Object.fromEntries(
    Object.entries(rows).map(([key, row]) => {
      const verifiedAt = stringValue(row.VerifiedAt ?? row.verifiedAt)
      return [
        key,
        {
          chainType: stringValue(row.ChainType ?? row.chainType) || key,
          valid: booleanValue(row.Valid ?? row.valid),
          entriesChecked: numberValue(row.EntriesChecked ?? row.entriesChecked),
          lastVerified: verifiedAt,
          latestEntryTime: verifiedAt,
        },
      ]
    }),
  )
}

function recordValue(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {}
}

function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function booleanValue(value: unknown): boolean {
  return value === true
}

function money(value: unknown): string {
  return `$${numberValue(value).toLocaleString(undefined, { maximumFractionDigits: 2 })}`
}

function normalizeCluster(row: Record<string, unknown>): Cluster {
  const capacity = recordValue(row.gpu_capacity ?? row.GPUCapacity)
  const labels = recordValue(row.labels ?? row.Labels)
  return {
    id: stringValue(row.id ?? row.ID),
    name: stringValue(row.name ?? row.Name),
    region: stringValue(row.region ?? row.Region),
    status: stringValue(row.status ?? row.Status) || 'Pending',
    gpuAvailable: numberValue(capacity.available ?? capacity.Available ?? row.GPUAvailable),
    gpuTotal: numberValue(capacity.total ?? capacity.Total ?? row.GPUTotal),
    gpuType: arrayValue(capacity.types ?? capacity.Types)[0]?.toString() || stringValue(labels['gpu-type']) || 'n/a',
    throughput: numberValue(row.throughput ?? row.Throughput),
    ttftP99: numberValue(row.ttftP99 ?? row.TTFT_P99_Ms),
    kvCacheHitRate: numberValue(row.kvCacheHitRate ?? row.KVCacheHitRate),
  }
}

function normalizePool(row: Record<string, unknown>): FleetPool {
  const desired = arrayValue(row.DesiredClusters ?? row.desiredClusters)
  const model = stringValue(row.Model ?? row.ModelName ?? row.model)
  return {
    id: stringValue(row.ID ?? row.Name ?? row.id),
    name: stringValue(row.Name ?? row.name) || model,
    model,
    modelVersion: stringValue(row.ModelVersion ?? row.modelVersion) || 'n/a',
    status: stringValue(row.Phase ?? row.Status ?? row.status) || 'Pending',
    rolloutStrategy: stringValue(row.RolloutStrategy ?? row.rolloutStrategy) || 'n/a',
    totalThroughput: numberValue(row.TotalThroughput ?? row.totalThroughput),
    avgTTFT: numberValue(row.AvgTTFT ?? row.avgTTFT),
    kvCacheHitRate: numberValue(row.KVCacheHitRate ?? row.kvCacheHitRate),
    clusters: desired.map((cluster) => ({
      cluster: String(cluster),
      replicas: 0,
      gpuType: 'n/a',
      status: 'Desired',
    })),
  }
}

function normalizeTenant(row: Record<string, unknown>): Tenant {
  const quotas = recordValue(row.Quotas ?? row.quotas)
  const cost = recordValue(row.CostControl ?? row.costControl)
  return {
    id: stringValue(row.ID ?? row.id),
    name: stringValue(row.Name ?? row.name),
    priority: numberValue(row.Priority ?? row.priority),
    maxTokensPerMinute: numberValue(quotas.max_tokens_per_minute ?? quotas.maxTokensPerMinute),
    maxConcurrentRequests: numberValue(quotas.max_concurrent_requests ?? quotas.maxConcurrentRequests),
    maxModels: numberValue(quotas.max_models ?? quotas.maxModels),
    gpuBudget: numberValue(quotas.gpu_budget ?? quotas.gpuBudget),
    monthlyBudget: money(cost.monthly_budget_usd ?? cost.monthlyBudget),
    alertThreshold: numberValue(cost.alert_threshold ?? cost.alertThreshold) || 0.8,
    tokensConsumed: 0,
    currentMonthCost: '$0',
    avgLatency: 0,
    activeModels: 0,
    currentConcurrentRequests: 0,
  }
}

function normalizeTenantUsage(row: Record<string, unknown>): TenantUsage {
  return {
    tenantId: stringValue(row.TenantID ?? row.tenantId),
    tenantName: stringValue(row.TenantName ?? row.tenantName),
    tokensConsumed: numberValue(row.TokensConsumed ?? row.tokensConsumed),
    currentMonthCost: stringValue(row.Cost ?? row.currentMonthCost) || '$0',
    totalRequests: numberValue(row.TotalRequests ?? row.totalRequests),
    avgLatency: numberValue(row.AvgLatencyMs ?? row.avgLatency),
    modelBreakdown: [],
  }
}

function normalizeRollout(row: Record<string, unknown>): Rollout {
  const strategy = recordValue(row.Strategy ?? row.strategy)
  const rawPhase = stringValue(row.Status ?? row.phase).toLowerCase()
  const phases: Record<string, Rollout['phase']> = {
    pending: 'Pending',
    canary: 'Progressing',
    active: 'Progressing',
    progressing: 'Progressing',
    paused: 'Paused',
    complete: 'Complete',
    completed: 'Complete',
    rolledback: 'RolledBack',
    'rolled-back': 'RolledBack',
    failed: 'Failed',
  }
  return {
    id: stringValue(row.ID ?? row.id),
    model: stringValue(row.PoolID ?? row.model),
    version: stringValue(row.ModelVersion ?? row.version),
    strategy: (stringValue(strategy.type) || 'canary') as Rollout['strategy'],
    phase: phases[rawPhase] || 'Pending',
    weight: numberValue(row.CurrentWeight ?? row.weight),
    startTime: stringValue(row.StartedAt ?? row.startTime),
    completionTime: stringValue(row.CompletedAt ?? row.completionTime) || undefined,
    clusterStatus: [],
  }
}
