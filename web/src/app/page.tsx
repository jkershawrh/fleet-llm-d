import { StatCard } from '@/components/stat-card'
import { StatusBadge } from '@/components/status-badge'
import { ProgressBar } from '@/components/progress-bar'
import {
  MOCK_FLEET_METRICS,
  MOCK_CLUSTERS,
  MOCK_EVENTS,
  // In production, replace mocks with:
  // fetchFleetMetrics,
  // fetchClusters,
} from '@/lib/api-client'

export default async function DashboardPage() {
  // TODO: Replace with live API calls
  // const metrics = await fetchFleetMetrics()
  // const clusters = await fetchClusters()
  const metrics = MOCK_FLEET_METRICS
  const clusters = MOCK_CLUSTERS
  const events = MOCK_EVENTS

  return (
    <div className="space-y-8">
      {/* Page Header */}
      <div>
        <h1 className="text-2xl font-bold text-foreground">Fleet Overview</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Real-time status of your inference fleet across all clusters
        </p>
      </div>

      {/* Fleet Summary Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title="Total Clusters"
          value={metrics.totalClusters}
          subtitle={`${clusters.filter((c) => c.status === 'Running').length} healthy`}
          trend={{ value: 0, label: 'vs last week' }}
          icon={<ServerSvg />}
        />
        <StatCard
          title="Active Models"
          value={metrics.activeModels}
          subtitle="Deployed across fleet"
          trend={{ value: 33, label: 'vs last month' }}
          icon={<CubeSvg />}
        />
        <StatCard
          title="Total GPUs"
          value={`${metrics.gpusAvailable}/${metrics.totalGpus}`}
          subtitle="Available / Total"
          trend={{ value: -5, label: 'utilization change' }}
          icon={<ChipSvg />}
        />
        <StatCard
          title="Fleet Throughput"
          value={`${(metrics.totalThroughput / 1000).toFixed(1)}K`}
          subtitle="tokens/sec aggregate"
          trend={{ value: 12, label: 'vs yesterday' }}
          icon={<BoltSvg />}
        />
      </div>

      {/* Cluster Health Grid */}
      <div>
        <h2 className="mb-4 text-lg font-semibold text-foreground">Cluster Health</h2>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {clusters.map((cluster) => (
            <div
              key={cluster.id}
              className="rounded-xl border border-border bg-card p-5 transition-colors hover:border-border/80"
            >
              <div className="flex items-start justify-between">
                <div>
                  <h3 className="font-semibold text-foreground">{cluster.name}</h3>
                  <p className="text-xs text-muted-foreground">{cluster.region}</p>
                </div>
                <StatusBadge status={cluster.status} size="sm" />
              </div>
              <div className="mt-4 space-y-3">
                <ProgressBar
                  value={cluster.gpuTotal - cluster.gpuAvailable}
                  max={cluster.gpuTotal}
                  label={`GPU Utilization (${cluster.gpuType})`}
                  size="sm"
                />
                <div className="grid grid-cols-3 gap-2">
                  <div>
                    <p className="text-xs text-muted-foreground">Throughput</p>
                    <p className="text-sm font-medium text-foreground">
                      {(cluster.throughput / 1000).toFixed(1)}K t/s
                    </p>
                  </div>
                  <div>
                    <p className="text-xs text-muted-foreground">TTFT p99</p>
                    <p className="text-sm font-medium text-foreground">{cluster.ttftP99}ms</p>
                  </div>
                  <div>
                    <p className="text-xs text-muted-foreground">KV Cache</p>
                    <p className="text-sm font-medium text-foreground">
                      {(cluster.kvCacheHitRate * 100).toFixed(0)}%
                    </p>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Recent Events */}
      <div>
        <h2 className="mb-4 text-lg font-semibold text-foreground">Recent Events</h2>
        <div className="rounded-xl border border-border bg-card">
          <div className="divide-y divide-border/50">
            {events.map((event) => (
              <div key={event.id} className="flex items-start gap-4 px-5 py-4">
                <div className="mt-1 shrink-0">
                  <EventIcon severity={event.severity} />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-sm text-foreground">{event.message}</p>
                  <div className="mt-1 flex items-center gap-3">
                    <span className="rounded bg-accent px-1.5 py-0.5 text-xs text-muted-foreground">
                      {event.type}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {formatTimestamp(event.timestamp)}
                    </span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffMins = Math.floor(diffMs / 60000)
  if (diffMins < 60) return `${diffMins}m ago`
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours}h ago`
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

function EventIcon({ severity }: { severity: 'info' | 'warning' | 'error' }) {
  const colors = {
    info: 'text-blue-400',
    warning: 'text-yellow-400',
    error: 'text-red-400',
  }
  return (
    <div className={`h-4 w-4 ${colors[severity]}`}>
      {severity === 'info' && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10" />
          <line x1="12" y1="16" x2="12" y2="12" />
          <line x1="12" y1="8" x2="12.01" y2="8" />
        </svg>
      )}
      {severity === 'warning' && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
      )}
      {severity === 'error' && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10" />
          <line x1="15" y1="9" x2="9" y2="15" />
          <line x1="9" y1="9" x2="15" y2="15" />
        </svg>
      )}
    </div>
  )
}

// Inline SVG icons for stat cards
function ServerSvg() {
  return (
    <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="2" y="2" width="20" height="8" rx="2" />
      <rect x="2" y="14" width="20" height="8" rx="2" />
      <line x1="6" y1="6" x2="6.01" y2="6" />
      <line x1="6" y1="18" x2="6.01" y2="18" />
    </svg>
  )
}

function CubeSvg() {
  return (
    <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
    </svg>
  )
}

function ChipSvg() {
  return (
    <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <rect x="9" y="9" width="6" height="6" />
      <line x1="9" y1="1" x2="9" y2="4" /><line x1="15" y1="1" x2="15" y2="4" />
      <line x1="9" y1="20" x2="9" y2="23" /><line x1="15" y1="20" x2="15" y2="23" />
      <line x1="20" y1="9" x2="23" y2="9" /><line x1="20" y1="14" x2="23" y2="14" />
      <line x1="1" y1="9" x2="4" y2="9" /><line x1="1" y1="14" x2="4" y2="14" />
    </svg>
  )
}

function BoltSvg() {
  return (
    <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
    </svg>
  )
}
