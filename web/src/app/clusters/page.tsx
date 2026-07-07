'use client'

import { useState } from 'react'
import { DataTable, type Column } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { ProgressBar } from '@/components/progress-bar'
import { MOCK_CLUSTERS, type Cluster } from '@/lib/api-client'

export default function ClustersPage() {
  const [clusters] = useState(MOCK_CLUSTERS)
  const [showRegisterForm, setShowRegisterForm] = useState(false)
  const [formData, setFormData] = useState({ name: '', region: '', gpuType: '', gpuCount: '' })

  const columns: Column<Cluster>[] = [
    {
      key: 'name',
      header: 'Name',
      sortable: true,
      render: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          <p className="text-xs text-muted-foreground">{row.id}</p>
        </div>
      ),
    },
    {
      key: 'region',
      header: 'Region',
      sortable: true,
      render: (row) => (
        <span className="rounded bg-accent px-2 py-1 text-xs font-medium text-muted-foreground">
          {row.region}
        </span>
      ),
    },
    {
      key: 'gpuAvailable',
      header: 'GPUs',
      sortable: true,
      render: (row) => (
        <div className="w-36">
          <ProgressBar
            value={row.gpuTotal - row.gpuAvailable}
            max={row.gpuTotal}
            size="sm"
            showPercentage={false}
          />
          <p className="mt-1 text-xs text-muted-foreground">
            {row.gpuAvailable} available / {row.gpuTotal} total ({row.gpuType})
          </p>
        </div>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      sortable: true,
      render: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'throughput',
      header: 'Throughput',
      sortable: true,
      render: (row) => (
        <span className="font-mono text-sm text-foreground">
          {(row.throughput / 1000).toFixed(1)}K t/s
        </span>
      ),
    },
    {
      key: 'ttftP99',
      header: 'TTFT p99',
      sortable: true,
      render: (row) => (
        <span
          className={`font-mono text-sm ${
            row.ttftP99 > 200 ? 'text-red-400' : row.ttftP99 > 150 ? 'text-yellow-400' : 'text-foreground'
          }`}
        >
          {row.ttftP99}ms
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <div className="flex items-center gap-2">
          <button className="rounded-lg border border-border px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">
            Details
          </button>
          {row.status === 'Degraded' && (
            <button className="rounded-lg border border-yellow-500/30 bg-yellow-500/10 px-3 py-1.5 text-xs text-yellow-400 transition-colors hover:bg-yellow-500/20">
              Investigate
            </button>
          )}
        </div>
      ),
    },
  ]

  const handleRegister = (e: React.FormEvent) => {
    e.preventDefault()
    // TODO: POST /api/v1/clusters with formData
    console.log('Register cluster:', formData)
    setShowRegisterForm(false)
    setFormData({ name: '', region: '', gpuType: '', gpuCount: '' })
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Clusters</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Manage inference clusters across regions
          </p>
        </div>
        <button
          onClick={() => setShowRegisterForm(true)}
          className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
        >
          Register Cluster
        </button>
      </div>

      {/* Summary Stats */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-4">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Total Clusters</p>
          <p className="mt-1 text-2xl font-bold text-foreground">{clusters.length}</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Healthy</p>
          <p className="mt-1 text-2xl font-bold text-emerald-400">
            {clusters.filter((c) => c.status === 'Running').length}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Degraded</p>
          <p className="mt-1 text-2xl font-bold text-orange-400">
            {clusters.filter((c) => c.status === 'Degraded').length}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs text-muted-foreground">Total GPUs</p>
          <p className="mt-1 text-2xl font-bold text-foreground">
            {clusters.reduce((sum, c) => sum + c.gpuTotal, 0)}
          </p>
        </div>
      </div>

      {/* Clusters Table */}
      <DataTable
        columns={columns}
        data={clusters}
        keyField="id"
      />

      {/* Register Cluster Modal */}
      {showRegisterForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
          <div className="w-full max-w-lg rounded-xl border border-border bg-card p-6 shadow-2xl">
            <h2 className="text-lg font-semibold text-foreground">Register Cluster</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Add a new Kubernetes cluster to the inference fleet
            </p>
            <form onSubmit={handleRegister} className="mt-6 space-y-4">
              <div>
                <label className="block text-sm font-medium text-muted-foreground">
                  Cluster Name
                </label>
                <input
                  type="text"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  placeholder="us-east-prod-2"
                  className="mt-1 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground/50 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-muted-foreground">Region</label>
                <select
                  value={formData.region}
                  onChange={(e) => setFormData({ ...formData, region: e.target.value })}
                  className="mt-1 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                  required
                >
                  <option value="">Select region</option>
                  <option value="us-east-1">us-east-1</option>
                  <option value="us-west-2">us-west-2</option>
                  <option value="eu-central-1">eu-central-1</option>
                  <option value="ap-southeast-1">ap-southeast-1</option>
                </select>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-muted-foreground">
                    GPU Type
                  </label>
                  <select
                    value={formData.gpuType}
                    onChange={(e) => setFormData({ ...formData, gpuType: e.target.value })}
                    className="mt-1 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    required
                  >
                    <option value="">Select GPU</option>
                    <option value="A100-80GB">A100-80GB</option>
                    <option value="A100-40GB">A100-40GB</option>
                    <option value="H100-80GB">H100-80GB</option>
                    <option value="L40S">L40S</option>
                  </select>
                </div>
                <div>
                  <label className="block text-sm font-medium text-muted-foreground">
                    GPU Count
                  </label>
                  <input
                    type="number"
                    value={formData.gpuCount}
                    onChange={(e) => setFormData({ ...formData, gpuCount: e.target.value })}
                    placeholder="8"
                    min="1"
                    className="mt-1 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground/50 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    required
                  />
                </div>
              </div>
              <div className="flex justify-end gap-3 pt-4">
                <button
                  type="button"
                  onClick={() => setShowRegisterForm(false)}
                  className="rounded-lg border border-border px-4 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
                >
                  Register
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
