'use client'

import { useCallback, useEffect, useState } from 'react'
import { verifyChains, type ChainVerification } from '@/lib/api-client'

export default function CompliancePage() {
  const [chains, setChains] = useState<Record<string, ChainVerification>>({})
  const [verifying, setVerifying] = useState(true)
  const [error, setError] = useState('')

  const handleVerifyAll = useCallback(async () => {
    setVerifying(true)
    try {
      setChains(await verifyChains())
      setError('')
    } catch (verifyError) {
      setChains({})
      setError(verifyError instanceof Error ? verifyError.message : 'Ledger verification unavailable')
    } finally {
      setVerifying(false)
    }
  }, [])

  useEffect(() => {
    let active = true
    void verifyChains().then((result) => {
      if (active) setChains(result)
    }).catch((verifyError: unknown) => {
      if (active) setError(verifyError instanceof Error ? verifyError.message : 'Ledger verification unavailable')
    }).finally(() => {
      if (active) setVerifying(false)
    })
    return () => { active = false }
  }, [])

  const chainList = Object.values(chains)
  const allValid = chainList.length > 0 && chainList.every((chain) => chain.valid)

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Compliance & Audit Trail</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Verify the integrity of the controller&apos;s ARE Ledger decision chains
          </p>
        </div>
        <button
          onClick={() => void handleVerifyAll()}
          disabled={verifying}
          className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:opacity-50"
        >
          {verifying ? 'Verifying...' : 'Verify All Chains'}
        </button>
      </div>

      {error && <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-sm text-red-400">{error}</div>}

      {!error && chainList.length > 0 && (
        <div className={`rounded-xl border p-4 ${allValid ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-red-500/30 bg-red-500/5'}`}>
          <p className={`font-semibold ${allValid ? 'text-emerald-400' : 'text-red-400'}`}>
            {allValid ? 'All Ledger Chains Valid' : 'Chain Integrity Issue Detected'}
          </p>
          <p className="mt-1 text-sm text-muted-foreground">
            {chainList.reduce((sum, chain) => sum + chain.entriesChecked, 0).toLocaleString()} entries checked across {chainList.length} chains
          </p>
        </div>
      )}

      <div>
        <h2 className="mb-4 text-lg font-semibold text-foreground">Chain Verification Status</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {chainList.map((chain) => <ChainCard key={chain.chainType} chain={chain} />)}
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-5 text-sm text-muted-foreground">
        Ledger-entry browsing and individual proof verification are not exposed by the controller API, so this dashboard does not fabricate either result.
      </div>
    </div>
  )
}

function ChainCard({ chain }: { chain: ChainVerification }) {
  return (
    <div className={`rounded-xl border p-5 ${chain.valid ? 'border-border bg-card' : 'border-red-500/30 bg-red-500/5'}`}>
      <div className="flex items-start justify-between">
        <div>
          <h3 className="font-semibold capitalize text-foreground">{chain.chainType}</h3>
          <p className="mt-0.5 text-xs text-muted-foreground">Decision chain</p>
        </div>
        <span className={`rounded-full px-2 py-1 text-xs font-medium ${chain.valid ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {chain.valid ? 'Valid' : 'Invalid'}
        </span>
      </div>
      <dl className="mt-4 space-y-2 text-xs">
        <div className="flex justify-between">
          <dt className="text-muted-foreground">Entries Checked</dt>
          <dd className="font-medium text-foreground">{chain.entriesChecked.toLocaleString()}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-muted-foreground">Last Verified</dt>
          <dd className="font-medium text-foreground">{chain.lastVerified ? new Date(chain.lastVerified).toLocaleString() : 'n/a'}</dd>
        </div>
      </dl>
    </div>
  )
}
