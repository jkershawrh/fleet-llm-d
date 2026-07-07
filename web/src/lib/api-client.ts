const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'

// --- Type Definitions ---

export interface Cluster {
  id: string
  name: string
  region: string
  status: 'Running' | 'Degraded' | 'Failed' | 'Pending' | 'ScaledToZero'
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
  status: 'Pending' | 'Placing' | 'Running' | 'Degraded' | 'Failed'
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

export interface LedgerEntry {
  id: string
  chainType: string
  source: string
  timestamp: string
  hash: string
  parentHash: string
  verified: boolean
}

// --- API Functions ---

async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
    next: { revalidate: 30 }, // ISR: revalidate every 30 seconds
  })

  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`)
  }

  return res.json()
}

export async function fetchClusters(): Promise<Cluster[]> {
  return apiFetch<Cluster[]>('/api/v1/clusters')
}

export async function fetchPools(): Promise<FleetPool[]> {
  return apiFetch<FleetPool[]>('/api/v1/pools')
}

export async function fetchTenants(): Promise<Tenant[]> {
  return apiFetch<Tenant[]>('/api/v1/tenants')
}

export async function fetchTenantUsage(id: string): Promise<TenantUsage> {
  return apiFetch<TenantUsage>(`/api/v1/tenants/${id}/usage`)
}

export async function fetchFleetMetrics(): Promise<FleetMetrics> {
  return apiFetch<FleetMetrics>('/api/v1/metrics/fleet')
}

export async function fetchModelMetrics(model: string): Promise<ModelMetrics> {
  return apiFetch<ModelMetrics>(`/api/v1/metrics/model/${model}`)
}

export async function fetchRollouts(): Promise<Rollout[]> {
  return apiFetch<Rollout[]>('/api/v1/rollouts')
}

export async function promoteRollout(id: string): Promise<void> {
  await apiFetch<void>(`/api/v1/rollouts/${id}/promote`, { method: 'POST' })
}

export async function rollbackRollout(id: string): Promise<void> {
  await apiFetch<void>(`/api/v1/rollouts/${id}/rollback`, { method: 'POST' })
}

export async function verifyChains(): Promise<Record<string, ChainVerification>> {
  return apiFetch<Record<string, ChainVerification>>('/api/v1/verify/chains')
}

// --- Mock Data (used when API is unavailable during development) ---

export const MOCK_CLUSTERS: Cluster[] = [
  { id: 'cl-1', name: 'us-east-prod', region: 'us-east-1', status: 'Running', gpuAvailable: 24, gpuTotal: 32, gpuType: 'A100-80GB', throughput: 14200, ttftP99: 125, kvCacheHitRate: 0.87 },
  { id: 'cl-2', name: 'us-west-prod', region: 'us-west-2', status: 'Running', gpuAvailable: 16, gpuTotal: 24, gpuType: 'A100-80GB', throughput: 10800, ttftP99: 118, kvCacheHitRate: 0.91 },
  { id: 'cl-3', name: 'eu-central-prod', region: 'eu-central-1', status: 'Degraded', gpuAvailable: 4, gpuTotal: 16, gpuType: 'H100-80GB', throughput: 8400, ttftP99: 195, kvCacheHitRate: 0.72 },
  { id: 'cl-4', name: 'ap-southeast-prod', region: 'ap-southeast-1', status: 'Running', gpuAvailable: 8, gpuTotal: 8, gpuType: 'A100-40GB', throughput: 5200, ttftP99: 142, kvCacheHitRate: 0.85 },
  { id: 'cl-5', name: 'us-east-staging', region: 'us-east-1', status: 'Running', gpuAvailable: 4, gpuTotal: 4, gpuType: 'L40S', throughput: 2100, ttftP99: 210, kvCacheHitRate: 0.68 },
]

export const MOCK_POOLS: FleetPool[] = [
  {
    id: 'fp-1', name: 'llama-3.1-70b-pool', model: 'llama-3.1-70b-instruct', modelVersion: 'v1.2.0',
    status: 'Running', rolloutStrategy: 'Canary', totalThroughput: 18600, avgTTFT: 135, kvCacheHitRate: 0.86,
    clusters: [
      { cluster: 'us-east-prod', replicas: 4, gpuType: 'A100-80GB', status: 'Running' },
      { cluster: 'us-west-prod', replicas: 3, gpuType: 'A100-80GB', status: 'Running' },
      { cluster: 'eu-central-prod', replicas: 2, gpuType: 'H100-80GB', status: 'Degraded' },
    ],
  },
  {
    id: 'fp-2', name: 'mistral-7b-pool', model: 'mistral-7b-instruct-v0.3', modelVersion: 'v0.3.1',
    status: 'Running', rolloutStrategy: 'Rolling', totalThroughput: 12400, avgTTFT: 45, kvCacheHitRate: 0.92,
    clusters: [
      { cluster: 'us-east-prod', replicas: 2, gpuType: 'A100-80GB', status: 'Running' },
      { cluster: 'ap-southeast-prod', replicas: 2, gpuType: 'A100-40GB', status: 'Running' },
    ],
  },
  {
    id: 'fp-3', name: 'granite-code-pool', model: 'granite-3.2-8b-instruct', modelVersion: 'v3.2.0',
    status: 'Running', rolloutStrategy: 'AllAtOnce', totalThroughput: 8900, avgTTFT: 62, kvCacheHitRate: 0.88,
    clusters: [
      { cluster: 'us-west-prod', replicas: 2, gpuType: 'A100-80GB', status: 'Running' },
      { cluster: 'us-east-staging', replicas: 1, gpuType: 'L40S', status: 'Running' },
    ],
  },
  {
    id: 'fp-4', name: 'llama-3.1-405b-pool', model: 'llama-3.1-405b-instruct', modelVersion: 'v1.0.0',
    status: 'Placing', rolloutStrategy: 'BlueGreen', totalThroughput: 0, avgTTFT: 0, kvCacheHitRate: 0,
    clusters: [
      { cluster: 'us-east-prod', replicas: 0, gpuType: 'H100-80GB', status: 'Pending' },
    ],
  },
]

export const MOCK_TENANTS: Tenant[] = [
  { id: 't-1', name: 'platform-team', priority: 900, maxTokensPerMinute: 500000, maxConcurrentRequests: 200, maxModels: 10, gpuBudget: 16, monthlyBudget: '$25,000', alertThreshold: 0.8, tokensConsumed: 12400000, currentMonthCost: '$18,750', avgLatency: 128, activeModels: 4, currentConcurrentRequests: 45 },
  { id: 't-2', name: 'ml-research', priority: 700, maxTokensPerMinute: 300000, maxConcurrentRequests: 100, maxModels: 8, gpuBudget: 8, monthlyBudget: '$15,000', alertThreshold: 0.8, tokensConsumed: 8900000, currentMonthCost: '$13,200', avgLatency: 145, activeModels: 3, currentConcurrentRequests: 22 },
  { id: 't-3', name: 'customer-support', priority: 500, maxTokensPerMinute: 100000, maxConcurrentRequests: 50, maxModels: 2, gpuBudget: 4, monthlyBudget: '$5,000', alertThreshold: 0.8, tokensConsumed: 4200000, currentMonthCost: '$4,800', avgLatency: 95, activeModels: 1, currentConcurrentRequests: 18 },
  { id: 't-4', name: 'internal-tools', priority: 300, maxTokensPerMinute: 50000, maxConcurrentRequests: 20, maxModels: 3, gpuBudget: 2, monthlyBudget: '$2,000', alertThreshold: 0.9, tokensConsumed: 1100000, currentMonthCost: '$850', avgLatency: 112, activeModels: 2, currentConcurrentRequests: 5 },
]

export const MOCK_TENANT_USAGE: TenantUsage = {
  tenantId: 't-1', tenantName: 'platform-team', tokensConsumed: 12400000, currentMonthCost: '$18,750',
  totalRequests: 245000, avgLatency: 128,
  modelBreakdown: [
    { model: 'llama-3.1-70b-instruct', tokensConsumed: 6200000, requests: 120000, cost: '$9,800' },
    { model: 'mistral-7b-instruct-v0.3', tokensConsumed: 3800000, requests: 85000, cost: '$5,200' },
    { model: 'granite-3.2-8b-instruct', tokensConsumed: 2400000, requests: 40000, cost: '$3,750' },
  ],
}

export const MOCK_ROLLOUTS: Rollout[] = [
  {
    id: 'ro-1', model: 'llama-3.1-70b-instruct', version: 'v1.3.0', strategy: 'canary',
    phase: 'Progressing', weight: 35, startTime: '2026-07-06T08:00:00Z',
    clusterStatus: [
      { cluster: 'us-east-prod', phase: 'Progressing', currentWeight: 35, sloMet: true, lastCheckTime: '2026-07-06T14:30:00Z' },
      { cluster: 'us-west-prod', phase: 'Progressing', currentWeight: 35, sloMet: true, lastCheckTime: '2026-07-06T14:30:00Z' },
      { cluster: 'eu-central-prod', phase: 'Pending', currentWeight: 0, sloMet: true, lastCheckTime: '2026-07-06T14:30:00Z' },
    ],
  },
  {
    id: 'ro-2', model: 'granite-3.2-8b-instruct', version: 'v3.2.1', strategy: 'rolling',
    phase: 'Complete', weight: 100, startTime: '2026-07-05T10:00:00Z', completionTime: '2026-07-05T14:30:00Z',
    clusterStatus: [
      { cluster: 'us-west-prod', phase: 'Complete', currentWeight: 100, sloMet: true, lastCheckTime: '2026-07-05T14:30:00Z' },
      { cluster: 'us-east-staging', phase: 'Complete', currentWeight: 100, sloMet: true, lastCheckTime: '2026-07-05T14:30:00Z' },
    ],
  },
  {
    id: 'ro-3', model: 'mistral-7b-instruct-v0.3', version: 'v0.4.0', strategy: 'canary',
    phase: 'Paused', weight: 15, startTime: '2026-07-06T06:00:00Z',
    clusterStatus: [
      { cluster: 'us-east-prod', phase: 'Paused', currentWeight: 15, sloMet: false, lastCheckTime: '2026-07-06T12:00:00Z' },
      { cluster: 'ap-southeast-prod', phase: 'Pending', currentWeight: 0, sloMet: true, lastCheckTime: '2026-07-06T12:00:00Z' },
    ],
  },
]

export const MOCK_FLEET_METRICS: FleetMetrics = {
  totalClusters: 5,
  totalGpus: 84,
  gpusAvailable: 56,
  activeModels: 4,
  totalThroughput: 40700,
  avgTtft: 134,
  avgKvCacheHitRate: 0.84,
}

export const MOCK_CHAIN_VERIFICATIONS: Record<string, ChainVerification> = {
  placement: { chainType: 'placement', valid: true, entriesChecked: 1247, lastVerified: '2026-07-06T14:30:00Z', latestEntryTime: '2026-07-06T14:28:00Z' },
  scaling: { chainType: 'scaling', valid: true, entriesChecked: 892, lastVerified: '2026-07-06T14:30:00Z', latestEntryTime: '2026-07-06T14:25:00Z' },
  routing: { chainType: 'routing', valid: true, entriesChecked: 2103, lastVerified: '2026-07-06T14:30:00Z', latestEntryTime: '2026-07-06T14:29:00Z' },
  lifecycle: { chainType: 'lifecycle', valid: false, entriesChecked: 456, lastVerified: '2026-07-06T14:30:00Z', latestEntryTime: '2026-07-06T13:15:00Z' },
  tenant: { chainType: 'tenant', valid: true, entriesChecked: 634, lastVerified: '2026-07-06T14:30:00Z', latestEntryTime: '2026-07-06T14:20:00Z' },
}

export const MOCK_LEDGER_ENTRIES: LedgerEntry[] = [
  { id: 'le-1', chainType: 'placement', source: 'fleet-controller', timestamp: '2026-07-06T14:28:00Z', hash: 'a3f2c1d8e9b4', parentHash: '7c6d5e4f3a2b', verified: true },
  { id: 'le-2', chainType: 'routing', source: 'fleet-controller', timestamp: '2026-07-06T14:29:00Z', hash: 'b4e3d2c1f0a5', parentHash: '8d7e6f5a4b3c', verified: true },
  { id: 'le-3', chainType: 'scaling', source: 'fleet-autoscaler', timestamp: '2026-07-06T14:25:00Z', hash: 'c5f4e3d2a1b6', parentHash: '9e8f7a6b5c4d', verified: true },
  { id: 'le-4', chainType: 'lifecycle', source: 'rollout-controller', timestamp: '2026-07-06T13:15:00Z', hash: 'd6a5f4e3b2c7', parentHash: '0f9a8b7c6d5e', verified: false },
  { id: 'le-5', chainType: 'tenant', source: 'tenant-controller', timestamp: '2026-07-06T14:20:00Z', hash: 'e7b6a5f4c3d8', parentHash: '1a0b9c8d7e6f', verified: true },
  { id: 'le-6', chainType: 'placement', source: 'fleet-controller', timestamp: '2026-07-06T14:15:00Z', hash: 'f8c7b6a5d4e9', parentHash: '2b1c0d9e8f7a', verified: true },
  { id: 'le-7', chainType: 'routing', source: 'fleet-controller', timestamp: '2026-07-06T14:10:00Z', hash: 'a9d8c7b6e5f0', parentHash: '3c2d1e0f9a8b', verified: true },
  { id: 'le-8', chainType: 'scaling', source: 'fleet-autoscaler', timestamp: '2026-07-06T14:05:00Z', hash: 'b0e9d8c7f6a1', parentHash: '4d3e2f1a0b9c', verified: true },
]

export const MOCK_EVENTS = [
  { id: 'ev-1', type: 'rollout', message: 'Canary rollout for llama-3.1-70b v1.3.0 advanced to 35%', timestamp: '2026-07-06T14:30:00Z', severity: 'info' as const },
  { id: 'ev-2', type: 'alert', message: 'Cluster eu-central-prod degraded: 4/16 GPUs available', timestamp: '2026-07-06T14:15:00Z', severity: 'warning' as const },
  { id: 'ev-3', type: 'rollout', message: 'Rollout mistral-7b v0.4.0 paused: SLO gate failed (TTFT p99 > 200ms)', timestamp: '2026-07-06T12:00:00Z', severity: 'error' as const },
  { id: 'ev-4', type: 'scaling', message: 'Auto-scaled granite-3.2-8b in us-west-prod: 2 -> 3 replicas', timestamp: '2026-07-06T11:45:00Z', severity: 'info' as const },
  { id: 'ev-5', type: 'tenant', message: 'Tenant customer-support approaching budget threshold (96%)', timestamp: '2026-07-06T11:30:00Z', severity: 'warning' as const },
  { id: 'ev-6', type: 'compliance', message: 'All ARE ledger chains verified successfully', timestamp: '2026-07-06T10:00:00Z', severity: 'info' as const },
  { id: 'ev-7', type: 'rollout', message: 'Rolling update granite-3.2-8b v3.2.1 completed successfully', timestamp: '2026-07-05T14:30:00Z', severity: 'info' as const },
]
