import { StatusBadge } from '@/components/status-badge'
import {
  MOCK_POOLS,
  // In production: fetchPools
} from '@/lib/api-client'

// Model metadata (would come from ModelPack CRD in production)
const MODEL_META: Record<string, { params: string; precision: string; gpuMemory: string }> = {
  'llama-3.1-70b-instruct': { params: '70B', precision: 'FP16', gpuMemory: '140 GB' },
  'mistral-7b-instruct-v0.3': { params: '7B', precision: 'FP16', gpuMemory: '14 GB' },
  'granite-3.2-8b-instruct': { params: '8B', precision: 'FP16', gpuMemory: '16 GB' },
  'llama-3.1-405b-instruct': { params: '405B', precision: 'FP8', gpuMemory: '405 GB' },
}

export default async function ModelsPage() {
  // TODO: Replace with live API call
  // const pools = await fetchPools()
  const pools = MOCK_POOLS

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div>
        <h1 className="text-2xl font-bold text-foreground">Model Placement</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Models deployed across the fleet with cluster assignments and replica counts
        </p>
      </div>

      {/* Summary */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Total Models</p>
          <p className="mt-1 text-2xl font-bold text-foreground">{pools.length}</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Running</p>
          <p className="mt-1 text-2xl font-bold text-emerald-400">
            {pools.filter((p) => p.status === 'Running').length}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Aggregate Throughput</p>
          <p className="mt-1 text-2xl font-bold text-foreground">
            {(pools.reduce((sum, p) => sum + p.totalThroughput, 0) / 1000).toFixed(1)}K t/s
          </p>
        </div>
      </div>

      {/* Model Cards */}
      <div className="space-y-4">
        {pools.map((pool) => {
          const meta = MODEL_META[pool.model] || { params: '?', precision: '?', gpuMemory: '?' }
          return (
            <div
              key={pool.id}
              className="rounded-xl border border-border bg-card p-6"
            >
              {/* Model Header */}
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div className="flex items-center gap-4">
                  <div className="flex h-12 w-12 items-center justify-center rounded-lg bg-blue-600/10">
                    <svg
                      className="h-6 w-6 text-blue-400"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                    >
                      <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
                      <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
                      <line x1="12" y1="22.08" x2="12" y2="12" />
                    </svg>
                  </div>
                  <div>
                    <h3 className="text-lg font-semibold text-foreground">{pool.model}</h3>
                    <div className="mt-1 flex items-center gap-2">
                      <span className="text-xs text-muted-foreground">
                        Version: {pool.modelVersion}
                      </span>
                      <span className="text-muted-foreground/30">|</span>
                      <span className="text-xs text-muted-foreground">
                        Strategy: {pool.rolloutStrategy}
                      </span>
                    </div>
                  </div>
                </div>
                <StatusBadge status={pool.status} />
              </div>

              {/* Model Specs */}
              <div className="mt-4 flex flex-wrap gap-3">
                <span className="rounded-lg border border-border bg-accent/50 px-3 py-1.5 text-xs font-medium text-muted-foreground">
                  {meta.params} params
                </span>
                <span className="rounded-lg border border-border bg-accent/50 px-3 py-1.5 text-xs font-medium text-muted-foreground">
                  {meta.precision}
                </span>
                <span className="rounded-lg border border-border bg-accent/50 px-3 py-1.5 text-xs font-medium text-muted-foreground">
                  {meta.gpuMemory} VRAM
                </span>
              </div>

              {/* Cluster Assignments */}
              <div className="mt-5">
                <p className="mb-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                  Cluster Assignments
                </p>
                <div className="flex flex-wrap gap-3">
                  {pool.clusters.map((ca) => (
                    <div
                      key={ca.cluster}
                      className="flex items-center gap-3 rounded-lg border border-border bg-background p-3"
                    >
                      <StatusIndicatorDot status={ca.status} />
                      <div>
                        <p className="text-sm font-medium text-foreground">{ca.cluster}</p>
                        <p className="text-xs text-muted-foreground">
                          {ca.replicas} replica{ca.replicas !== 1 ? 's' : ''} &middot; {ca.gpuType}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Metrics Row */}
              {pool.totalThroughput > 0 && (
                <div className="mt-5 grid grid-cols-3 gap-4 border-t border-border/50 pt-4">
                  <div>
                    <p className="text-xs text-muted-foreground">Total Throughput</p>
                    <p className="text-sm font-semibold text-foreground">
                      {(pool.totalThroughput / 1000).toFixed(1)}K t/s
                    </p>
                  </div>
                  <div>
                    <p className="text-xs text-muted-foreground">Avg TTFT</p>
                    <p className="text-sm font-semibold text-foreground">{pool.avgTTFT}ms</p>
                  </div>
                  <div>
                    <p className="text-xs text-muted-foreground">KV Cache Hit Rate</p>
                    <p className="text-sm font-semibold text-foreground">
                      {(pool.kvCacheHitRate * 100).toFixed(0)}%
                    </p>
                  </div>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function StatusIndicatorDot({ status }: { status: string }) {
  const colorMap: Record<string, string> = {
    Running: 'bg-emerald-500',
    Degraded: 'bg-orange-500',
    Pending: 'bg-yellow-500',
    Failed: 'bg-red-500',
    ScaledToZero: 'bg-zinc-500',
  }
  return (
    <div className={`h-2.5 w-2.5 rounded-full ${colorMap[status] || 'bg-zinc-500'}`} />
  )
}
