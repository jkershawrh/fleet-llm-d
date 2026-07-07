'use client'

import { useState } from 'react'
import { StatusBadge } from '@/components/status-badge'
import { MOCK_ROLLOUTS, type Rollout } from '@/lib/api-client'

export default function RolloutsPage() {
  const [rollouts] = useState(MOCK_ROLLOUTS)

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div>
        <h1 className="text-2xl font-bold text-foreground">Lifecycle Management</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Track model rollouts, canary progressions, and SLO gate status
        </p>
      </div>

      {/* Summary */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-4">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Active Rollouts</p>
          <p className="mt-1 text-2xl font-bold text-foreground">
            {rollouts.filter((r) => r.phase === 'Progressing' || r.phase === 'Paused').length}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Completed</p>
          <p className="mt-1 text-2xl font-bold text-emerald-400">
            {rollouts.filter((r) => r.phase === 'Complete').length}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Paused</p>
          <p className="mt-1 text-2xl font-bold text-yellow-400">
            {rollouts.filter((r) => r.phase === 'Paused').length}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Failed / Rolled Back</p>
          <p className="mt-1 text-2xl font-bold text-red-400">
            {rollouts.filter((r) => r.phase === 'Failed' || r.phase === 'RolledBack').length}
          </p>
        </div>
      </div>

      {/* Rollout Cards */}
      <div className="space-y-4">
        {rollouts.map((rollout) => (
          <RolloutCard key={rollout.id} rollout={rollout} />
        ))}
      </div>
    </div>
  )
}

function RolloutCard({ rollout }: { rollout: Rollout }) {
  const isActive = rollout.phase === 'Progressing' || rollout.phase === 'Paused'
  const canPromote = rollout.phase === 'Progressing' || rollout.phase === 'Paused'
  const canRollback = rollout.phase !== 'Complete' && rollout.phase !== 'RolledBack'

  const handlePromote = () => {
    // TODO: await promoteRollout(rollout.id)
    console.log('Promote rollout:', rollout.id)
  }

  const handleRollback = () => {
    // TODO: await rollbackRollout(rollout.id)
    console.log('Rollback rollout:', rollout.id)
  }

  return (
    <div className="rounded-xl border border-border bg-card p-6">
      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h3 className="text-lg font-semibold text-foreground">{rollout.model}</h3>
            <span className="rounded bg-accent px-2 py-0.5 text-xs font-medium text-muted-foreground">
              {rollout.version}
            </span>
          </div>
          <div className="mt-1 flex items-center gap-3">
            <span className="text-xs text-muted-foreground">
              Strategy: <span className="font-medium text-foreground">{rollout.strategy}</span>
            </span>
            <span className="text-muted-foreground/30">|</span>
            <span className="text-xs text-muted-foreground">
              Started: {new Date(rollout.startTime).toLocaleString()}
            </span>
            {rollout.completionTime && (
              <>
                <span className="text-muted-foreground/30">|</span>
                <span className="text-xs text-muted-foreground">
                  Completed: {new Date(rollout.completionTime).toLocaleString()}
                </span>
              </>
            )}
          </div>
        </div>
        <div className="flex items-center gap-3">
          <StatusBadge status={rollout.phase} />
          {canPromote && (
            <button
              onClick={handlePromote}
              className="rounded-lg bg-emerald-600 px-4 py-2 text-xs font-medium text-white transition-colors hover:bg-emerald-700"
            >
              Promote
            </button>
          )}
          {canRollback && (
            <button
              onClick={handleRollback}
              className="rounded-lg border border-red-500/30 bg-red-500/10 px-4 py-2 text-xs font-medium text-red-400 transition-colors hover:bg-red-500/20"
            >
              Rollback
            </button>
          )}
        </div>
      </div>

      {/* Canary Progress Bar */}
      {isActive && (
        <div className="mt-5">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-xs font-medium text-muted-foreground">Canary Weight</span>
            <span className="text-sm font-bold text-foreground">{rollout.weight}%</span>
          </div>
          <div className="h-3 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-3 rounded-full bg-gradient-to-r from-blue-600 to-blue-400 transition-all duration-1000"
              style={{ width: `${rollout.weight}%` }}
            />
          </div>
          <div className="mt-1.5 flex justify-between text-xs text-muted-foreground/60">
            <span>0%</span>
            <span>25%</span>
            <span>50%</span>
            <span>75%</span>
            <span>100%</span>
          </div>
        </div>
      )}

      {/* Cluster Status */}
      <div className="mt-5">
        <p className="mb-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
          Cluster Status
        </p>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="pb-2 pr-4 text-xs font-medium text-muted-foreground">Cluster</th>
                <th className="pb-2 pr-4 text-xs font-medium text-muted-foreground">Phase</th>
                <th className="pb-2 pr-4 text-xs font-medium text-muted-foreground">Weight</th>
                <th className="pb-2 pr-4 text-xs font-medium text-muted-foreground">SLO Gate</th>
                <th className="pb-2 text-xs font-medium text-muted-foreground">Last Check</th>
              </tr>
            </thead>
            <tbody>
              {rollout.clusterStatus.map((cs) => (
                <tr key={cs.cluster} className="border-b border-border/30">
                  <td className="py-2.5 pr-4 font-medium text-foreground">{cs.cluster}</td>
                  <td className="py-2.5 pr-4">
                    <StatusBadge status={cs.phase} size="sm" />
                  </td>
                  <td className="py-2.5 pr-4 font-mono text-foreground">{cs.currentWeight}%</td>
                  <td className="py-2.5 pr-4">
                    <SloGateIndicator met={cs.sloMet} />
                  </td>
                  <td className="py-2.5 text-xs text-muted-foreground">
                    {new Date(cs.lastCheckTime).toLocaleTimeString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

function SloGateIndicator({ met }: { met: boolean }) {
  return (
    <div className="flex items-center gap-1.5">
      {met ? (
        <>
          <svg
            className="h-4 w-4 text-emerald-400"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
          >
            <polyline points="20 6 9 17 4 12" />
          </svg>
          <span className="text-xs font-medium text-emerald-400">Pass</span>
        </>
      ) : (
        <>
          <svg
            className="h-4 w-4 text-red-400"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
          >
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
          <span className="text-xs font-medium text-red-400">Fail</span>
        </>
      )}
    </div>
  )
}
