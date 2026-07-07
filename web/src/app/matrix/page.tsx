'use client'

import { useState } from 'react'
import {
  Radar,
  RadarChart,
  PolarGrid,
  PolarAngleAxis,
  PolarRadiusAxis,
  ResponsiveContainer,
} from 'recharts'

// --- Mock Test Matrix Data ---

type CellStatus = 'pass' | 'warn' | 'fail' | 'skip'

interface MatrixCell {
  status: CellStatus
  score?: number
  details?: string
}

const capabilities = [
  'Multi-Cluster Placement',
  'Canary Rollout',
  'Blue-Green Rollout',
  'Rolling Update',
  'Tenant Isolation',
  'Quota Enforcement',
  'KV Cache Transfer',
  'ARE Ledger Integrity',
  'Auto-Scaling',
  'SLO Gate Validation',
  'Fleet Routing',
  'Model Hot-Swap',
]

const testTypes = ['Unit', 'Integration', 'E2E', 'Chaos', 'Performance', 'Security']

const matrixData: Record<string, Record<string, MatrixCell>> = {
  'Multi-Cluster Placement': {
    Unit: { status: 'pass', score: 95 },
    Integration: { status: 'pass', score: 92 },
    E2E: { status: 'pass', score: 88 },
    Chaos: { status: 'warn', score: 72, details: 'Network partition recovery slow' },
    Performance: { status: 'pass', score: 90 },
    Security: { status: 'pass', score: 96 },
  },
  'Canary Rollout': {
    Unit: { status: 'pass', score: 98 },
    Integration: { status: 'pass', score: 94 },
    E2E: { status: 'pass', score: 91 },
    Chaos: { status: 'pass', score: 85 },
    Performance: { status: 'pass', score: 89 },
    Security: { status: 'pass', score: 93 },
  },
  'Blue-Green Rollout': {
    Unit: { status: 'pass', score: 96 },
    Integration: { status: 'pass', score: 90 },
    E2E: { status: 'warn', score: 78, details: 'Switchover latency spike detected' },
    Chaos: { status: 'warn', score: 70, details: 'Resource contention during switch' },
    Performance: { status: 'pass', score: 82 },
    Security: { status: 'pass', score: 91 },
  },
  'Rolling Update': {
    Unit: { status: 'pass', score: 97 },
    Integration: { status: 'pass', score: 93 },
    E2E: { status: 'pass', score: 90 },
    Chaos: { status: 'pass', score: 86 },
    Performance: { status: 'pass', score: 88 },
    Security: { status: 'pass', score: 94 },
  },
  'Tenant Isolation': {
    Unit: { status: 'pass', score: 99 },
    Integration: { status: 'pass', score: 95 },
    E2E: { status: 'pass', score: 92 },
    Chaos: { status: 'pass', score: 88 },
    Performance: { status: 'pass', score: 85 },
    Security: { status: 'pass', score: 98 },
  },
  'Quota Enforcement': {
    Unit: { status: 'pass', score: 96 },
    Integration: { status: 'pass', score: 91 },
    E2E: { status: 'pass', score: 87 },
    Chaos: { status: 'warn', score: 74, details: 'Burst traffic exceeds quota briefly' },
    Performance: { status: 'pass', score: 83 },
    Security: { status: 'pass', score: 95 },
  },
  'KV Cache Transfer': {
    Unit: { status: 'pass', score: 94 },
    Integration: { status: 'pass', score: 89 },
    E2E: { status: 'warn', score: 76, details: 'Cross-region transfer latency' },
    Chaos: { status: 'fail', score: 45, details: 'Data corruption on network partition' },
    Performance: { status: 'warn', score: 68, details: 'Serialization overhead' },
    Security: { status: 'pass', score: 92 },
  },
  'ARE Ledger Integrity': {
    Unit: { status: 'pass', score: 99 },
    Integration: { status: 'pass', score: 97 },
    E2E: { status: 'pass', score: 95 },
    Chaos: { status: 'pass', score: 90 },
    Performance: { status: 'pass', score: 88 },
    Security: { status: 'pass', score: 99 },
  },
  'Auto-Scaling': {
    Unit: { status: 'pass', score: 95 },
    Integration: { status: 'pass', score: 90 },
    E2E: { status: 'pass', score: 84 },
    Chaos: { status: 'warn', score: 72, details: 'Oscillation under rapid load changes' },
    Performance: { status: 'pass', score: 80 },
    Security: { status: 'pass', score: 90 },
  },
  'SLO Gate Validation': {
    Unit: { status: 'pass', score: 97 },
    Integration: { status: 'pass', score: 93 },
    E2E: { status: 'pass', score: 89 },
    Chaos: { status: 'pass', score: 82 },
    Performance: { status: 'pass', score: 86 },
    Security: { status: 'pass', score: 94 },
  },
  'Fleet Routing': {
    Unit: { status: 'pass', score: 96 },
    Integration: { status: 'pass', score: 92 },
    E2E: { status: 'pass', score: 88 },
    Chaos: { status: 'warn', score: 75, details: 'Failover to secondary takes >5s' },
    Performance: { status: 'pass', score: 87 },
    Security: { status: 'pass', score: 93 },
  },
  'Model Hot-Swap': {
    Unit: { status: 'pass', score: 93 },
    Integration: { status: 'pass', score: 87 },
    E2E: { status: 'fail', score: 55, details: 'Request drop during swap window' },
    Chaos: { status: 'fail', score: 40, details: 'Concurrent swaps cause deadlock' },
    Performance: { status: 'warn', score: 65, details: 'Memory spike during swap' },
    Security: { status: 'pass', score: 90 },
  },
}

// Rubric radar chart data
const rubricData = [
  { metric: 'Reliability', score: 92 },
  { metric: 'Performance', score: 84 },
  { metric: 'Security', score: 95 },
  { metric: 'Chaos Resilience', score: 74 },
  { metric: 'Scalability', score: 86 },
  { metric: 'Compliance', score: 97 },
]

// Stage gates
const stageGates = [
  { name: 'Unit Tests', status: 'pass' as CellStatus, coverage: '96%' },
  { name: 'Integration', status: 'pass' as CellStatus, coverage: '91%' },
  { name: 'E2E Suite', status: 'warn' as CellStatus, coverage: '85%' },
  { name: 'Chaos Tests', status: 'fail' as CellStatus, coverage: '72%' },
  { name: 'Security Scan', status: 'pass' as CellStatus, coverage: '94%' },
  { name: 'Perf Baseline', status: 'warn' as CellStatus, coverage: '82%' },
]

export default function MatrixPage() {
  const [hoveredCell, setHoveredCell] = useState<{ cap: string; test: string } | null>(null)

  const cellColor = (status: CellStatus) => {
    switch (status) {
      case 'pass': return 'bg-emerald-600/80 hover:bg-emerald-600'
      case 'warn': return 'bg-yellow-600/80 hover:bg-yellow-600'
      case 'fail': return 'bg-red-600/80 hover:bg-red-600'
      case 'skip': return 'bg-zinc-700/80 hover:bg-zinc-700'
    }
  }

  const totalCells = capabilities.length * testTypes.length
  const passCells = capabilities.reduce((sum, cap) =>
    sum + testTypes.filter((t) => matrixData[cap]?.[t]?.status === 'pass').length, 0)
  const warnCells = capabilities.reduce((sum, cap) =>
    sum + testTypes.filter((t) => matrixData[cap]?.[t]?.status === 'warn').length, 0)
  const failCells = capabilities.reduce((sum, cap) =>
    sum + testTypes.filter((t) => matrixData[cap]?.[t]?.status === 'fail').length, 0)

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div>
        <h1 className="text-2xl font-bold text-foreground">Test Matrix</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Capability coverage matrix, rubric scores, and stage gate status
        </p>
      </div>

      {/* Summary */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-4">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Total Test Cells</p>
          <p className="mt-1 text-2xl font-bold text-foreground">{totalCells}</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Passing</p>
          <p className="mt-1 text-2xl font-bold text-emerald-400">{passCells}</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Warnings</p>
          <p className="mt-1 text-2xl font-bold text-yellow-400">{warnCells}</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Failures</p>
          <p className="mt-1 text-2xl font-bold text-red-400">{failCells}</p>
        </div>
      </div>

      {/* Test Matrix Grid */}
      <div>
        <h2 className="mb-4 text-lg font-semibold text-foreground">Capability Coverage</h2>
        <div className="overflow-x-auto rounded-xl border border-border bg-card">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="px-4 py-3 text-left font-medium text-muted-foreground">
                  Capability
                </th>
                {testTypes.map((t) => (
                  <th
                    key={t}
                    className="px-2 py-3 text-center font-medium text-muted-foreground"
                  >
                    {t}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {capabilities.map((cap) => (
                <tr key={cap} className="border-b border-border/30">
                  <td className="px-4 py-2 text-sm font-medium text-foreground">{cap}</td>
                  {testTypes.map((test) => {
                    const cell = matrixData[cap]?.[test] || { status: 'skip' as CellStatus }
                    const isHovered =
                      hoveredCell?.cap === cap && hoveredCell?.test === test
                    return (
                      <td key={test} className="px-2 py-2 text-center">
                        <div className="relative inline-block">
                          <button
                            className={`h-8 w-12 rounded ${cellColor(cell.status)} text-xs font-bold text-white transition-colors`}
                            onMouseEnter={() => setHoveredCell({ cap, test })}
                            onMouseLeave={() => setHoveredCell(null)}
                          >
                            {cell.score ?? '--'}
                          </button>
                          {isHovered && cell.details && (
                            <div className="absolute bottom-full left-1/2 z-50 mb-2 w-48 -translate-x-1/2 rounded-lg border border-border bg-card p-2 text-xs text-foreground shadow-xl">
                              <p className="font-medium">
                                {cap} / {test}
                              </p>
                              <p className="mt-1 text-muted-foreground">{cell.details}</p>
                            </div>
                          )}
                        </div>
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Rubric Radar Chart */}
        <div>
          <h2 className="mb-4 text-lg font-semibold text-foreground">Rubric Scores</h2>
          <div className="rounded-xl border border-border bg-card p-6">
            <ResponsiveContainer width="100%" height={320}>
              <RadarChart data={rubricData} cx="50%" cy="50%" outerRadius="75%">
                <PolarGrid stroke="hsl(217.2, 32.6%, 17.5%)" />
                <PolarAngleAxis
                  dataKey="metric"
                  tick={{ fill: 'hsl(215, 20.2%, 55%)', fontSize: 12 }}
                />
                <PolarRadiusAxis
                  angle={90}
                  domain={[0, 100]}
                  tick={{ fill: 'hsl(215, 20.2%, 55%)', fontSize: 10 }}
                />
                <Radar
                  name="Score"
                  dataKey="score"
                  stroke="#3b82f6"
                  fill="#3b82f6"
                  fillOpacity={0.2}
                  strokeWidth={2}
                />
              </RadarChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* Stage Gate Status */}
        <div>
          <h2 className="mb-4 text-lg font-semibold text-foreground">Stage Gate Status</h2>
          <div className="space-y-3">
            {stageGates.map((gate, idx) => (
              <div
                key={gate.name}
                className="flex items-center gap-4 rounded-xl border border-border bg-card p-4"
              >
                {/* Gate number */}
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent text-sm font-bold text-muted-foreground">
                  {idx + 1}
                </div>

                {/* Gate info */}
                <div className="flex-1">
                  <p className="font-medium text-foreground">{gate.name}</p>
                  <p className="text-xs text-muted-foreground">Coverage: {gate.coverage}</p>
                </div>

                {/* Status indicator */}
                <div className="flex items-center gap-2">
                  {gate.status === 'pass' && (
                    <div className="flex items-center gap-1.5 rounded-full bg-emerald-500/10 px-3 py-1">
                      <svg className="h-3.5 w-3.5 text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3">
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                      <span className="text-xs font-medium text-emerald-400">Pass</span>
                    </div>
                  )}
                  {gate.status === 'warn' && (
                    <div className="flex items-center gap-1.5 rounded-full bg-yellow-500/10 px-3 py-1">
                      <svg className="h-3.5 w-3.5 text-yellow-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3">
                        <line x1="12" y1="9" x2="12" y2="13" />
                        <line x1="12" y1="17" x2="12.01" y2="17" />
                      </svg>
                      <span className="text-xs font-medium text-yellow-400">Warn</span>
                    </div>
                  )}
                  {gate.status === 'fail' && (
                    <div className="flex items-center gap-1.5 rounded-full bg-red-500/10 px-3 py-1">
                      <svg className="h-3.5 w-3.5 text-red-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3">
                        <line x1="18" y1="6" x2="6" y2="18" />
                        <line x1="6" y1="6" x2="18" y2="18" />
                      </svg>
                      <span className="text-xs font-medium text-red-400">Fail</span>
                    </div>
                  )}
                </div>

                {/* Connector line to next gate */}
                {idx < stageGates.length - 1 && (
                  <div className="absolute -bottom-3 left-[2.25rem] h-3 w-px bg-border" />
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
