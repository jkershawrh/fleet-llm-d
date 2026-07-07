'use client'

import { useState } from 'react'
import { ProgressBar } from '@/components/progress-bar'
import { MOCK_TENANTS, MOCK_TENANT_USAGE, type Tenant, type TenantUsage } from '@/lib/api-client'

export default function TenantsPage() {
  const [tenants] = useState(MOCK_TENANTS)
  const [selectedTenant, setSelectedTenant] = useState<Tenant | null>(null)
  // In production: const usage = await fetchTenantUsage(selectedTenant.id)
  const [usage] = useState<TenantUsage>(MOCK_TENANT_USAGE)

  function getBudgetPercentage(tenant: Tenant): number {
    const cost = parseFloat(tenant.currentMonthCost.replace(/[$,]/g, ''))
    const budget = parseFloat(tenant.monthlyBudget.replace(/[$,]/g, ''))
    return budget > 0 ? Math.round((cost / budget) * 100) : 0
  }

  function getAlertLevel(tenant: Tenant): 'none' | 'warning' | 'critical' {
    const pct = getBudgetPercentage(tenant) / 100
    if (pct >= 0.95) return 'critical'
    if (pct >= tenant.alertThreshold) return 'warning'
    return 'none'
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div>
        <h1 className="text-2xl font-bold text-foreground">Tenants</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Per-tenant quota management, usage tracking, and cost controls
        </p>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Tenant List */}
        <div className="lg:col-span-1">
          <div className="rounded-xl border border-border bg-card">
            <div className="border-b border-border px-4 py-3">
              <h2 className="text-sm font-semibold text-foreground">Tenant Profiles</h2>
            </div>
            <div className="divide-y divide-border/50">
              {tenants.map((tenant) => {
                const alertLevel = getAlertLevel(tenant)
                const isSelected = selectedTenant?.id === tenant.id
                return (
                  <button
                    key={tenant.id}
                    onClick={() => setSelectedTenant(tenant)}
                    className={`w-full px-4 py-4 text-left transition-colors hover:bg-accent/50 ${
                      isSelected ? 'bg-accent/50 border-l-2 border-l-blue-500' : ''
                    }`}
                  >
                    <div className="flex items-start justify-between">
                      <div>
                        <div className="flex items-center gap-2">
                          <p className="font-medium text-foreground">{tenant.name}</p>
                          {alertLevel !== 'none' && (
                            <span
                              className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                                alertLevel === 'critical'
                                  ? 'bg-red-500/10 text-red-400'
                                  : 'bg-yellow-500/10 text-yellow-400'
                              }`}
                            >
                              {alertLevel === 'critical' ? '>95%' : '>80%'}
                            </span>
                          )}
                        </div>
                        <p className="mt-0.5 text-xs text-muted-foreground">
                          Priority: {tenant.priority}
                        </p>
                      </div>
                      <p className="text-sm font-semibold text-foreground">
                        {tenant.currentMonthCost}
                      </p>
                    </div>
                    <div className="mt-3 space-y-2">
                      <ProgressBar
                        value={parseFloat(tenant.currentMonthCost.replace(/[$,]/g, ''))}
                        max={parseFloat(tenant.monthlyBudget.replace(/[$,]/g, ''))}
                        label="Budget"
                        size="sm"
                      />
                    </div>
                  </button>
                )
              })}
            </div>
          </div>
        </div>

        {/* Tenant Detail */}
        <div className="lg:col-span-2">
          {selectedTenant ? (
            <TenantDetail tenant={selectedTenant} usage={usage} />
          ) : (
            <div className="flex h-96 items-center justify-center rounded-xl border border-border bg-card">
              <div className="text-center">
                <svg
                  className="mx-auto h-12 w-12 text-muted-foreground/30"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                >
                  <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
                  <circle cx="9" cy="7" r="4" />
                  <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
                  <path d="M16 3.13a4 4 0 0 1 0 7.75" />
                </svg>
                <p className="mt-3 text-sm text-muted-foreground">
                  Select a tenant to view details
                </p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function TenantDetail({ tenant, usage }: { tenant: Tenant; usage: TenantUsage }) {
  return (
    <div className="space-y-6">
      {/* Tenant Header */}
      <div className="rounded-xl border border-border bg-card p-6">
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-xl font-bold text-foreground">{tenant.name}</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Priority: {tenant.priority} / 1000
            </p>
          </div>
          <div className="text-right">
            <p className="text-2xl font-bold text-foreground">{tenant.currentMonthCost}</p>
            <p className="text-sm text-muted-foreground">of {tenant.monthlyBudget} budget</p>
          </div>
        </div>
      </div>

      {/* Quota Usage Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="rounded-xl border border-border bg-card p-5">
          <h3 className="text-sm font-semibold text-foreground">Tokens Consumed</h3>
          <p className="mt-2 text-2xl font-bold text-foreground">
            {(tenant.tokensConsumed / 1_000_000).toFixed(1)}M
          </p>
          <div className="mt-3">
            <ProgressBar
              value={tenant.tokensConsumed}
              max={tenant.maxTokensPerMinute * 60 * 24 * 30}
              label="Monthly token usage"
              size="sm"
              showPercentage={false}
            />
          </div>
        </div>
        <div className="rounded-xl border border-border bg-card p-5">
          <h3 className="text-sm font-semibold text-foreground">Budget Utilization</h3>
          <p className="mt-2 text-2xl font-bold text-foreground">{tenant.currentMonthCost}</p>
          <div className="mt-3">
            <ProgressBar
              value={parseFloat(tenant.currentMonthCost.replace(/[$,]/g, ''))}
              max={parseFloat(tenant.monthlyBudget.replace(/[$,]/g, ''))}
              label={`Budget: ${tenant.monthlyBudget}`}
              size="sm"
            />
          </div>
        </div>
        <div className="rounded-xl border border-border bg-card p-5">
          <h3 className="text-sm font-semibold text-foreground">GPU Budget</h3>
          <p className="mt-2 text-2xl font-bold text-foreground">{tenant.gpuBudget} GPUs</p>
          <p className="mt-1 text-xs text-muted-foreground">Max allocated GPU slots</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-5">
          <h3 className="text-sm font-semibold text-foreground">Concurrent Requests</h3>
          <p className="mt-2 text-2xl font-bold text-foreground">
            {tenant.currentConcurrentRequests}
          </p>
          <div className="mt-3">
            <ProgressBar
              value={tenant.currentConcurrentRequests}
              max={tenant.maxConcurrentRequests}
              label={`Max: ${tenant.maxConcurrentRequests}`}
              size="sm"
            />
          </div>
        </div>
      </div>

      {/* Per-Model Breakdown */}
      <div className="rounded-xl border border-border bg-card">
        <div className="border-b border-border px-5 py-4">
          <h3 className="text-sm font-semibold text-foreground">Per-Model Breakdown</h3>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/50">
                <th className="px-5 py-3 font-medium text-muted-foreground">Model</th>
                <th className="px-5 py-3 font-medium text-muted-foreground">Tokens</th>
                <th className="px-5 py-3 font-medium text-muted-foreground">Requests</th>
                <th className="px-5 py-3 font-medium text-muted-foreground">Cost</th>
                <th className="px-5 py-3 font-medium text-muted-foreground">Share</th>
              </tr>
            </thead>
            <tbody>
              {usage.modelBreakdown.map((mb) => (
                <tr
                  key={mb.model}
                  className="border-b border-border/50 transition-colors hover:bg-accent/30"
                >
                  <td className="px-5 py-3 font-medium text-foreground">{mb.model}</td>
                  <td className="px-5 py-3 font-mono text-foreground">
                    {(mb.tokensConsumed / 1_000_000).toFixed(1)}M
                  </td>
                  <td className="px-5 py-3 font-mono text-foreground">
                    {mb.requests.toLocaleString()}
                  </td>
                  <td className="px-5 py-3 font-mono text-foreground">{mb.cost}</td>
                  <td className="px-5 py-3">
                    <div className="w-24">
                      <ProgressBar
                        value={mb.tokensConsumed}
                        max={usage.tokensConsumed}
                        size="sm"
                        showPercentage={true}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Usage Stats */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Total Requests</p>
          <p className="mt-1 text-xl font-bold text-foreground">
            {usage.totalRequests.toLocaleString()}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Avg Latency</p>
          <p className="mt-1 text-xl font-bold text-foreground">{usage.avgLatency}ms</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Active Models</p>
          <p className="mt-1 text-xl font-bold text-foreground">{tenant.activeModels}</p>
        </div>
      </div>
    </div>
  )
}
