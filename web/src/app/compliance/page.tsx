'use client'

import { useState } from 'react'
import {
  MOCK_CHAIN_VERIFICATIONS,
  MOCK_LEDGER_ENTRIES,
  type ChainVerification,
  type LedgerEntry,
} from '@/lib/api-client'

export default function CompliancePage() {
  const [chains] = useState(MOCK_CHAIN_VERIFICATIONS)
  const [entries] = useState(MOCK_LEDGER_ENTRIES)
  const [verifying, setVerifying] = useState(false)
  const [proofInput, setProofInput] = useState('')
  const [proofResult, setProofResult] = useState<'idle' | 'valid' | 'invalid'>('idle')

  const chainList = Object.values(chains)
  const allValid = chainList.every((c) => c.valid)

  const handleVerifyAll = async () => {
    setVerifying(true)
    // TODO: const result = await verifyChains()
    setTimeout(() => setVerifying(false), 2000)
  }

  const handleVerifyProof = () => {
    // TODO: POST /api/v1/verify/proof with proofInput
    if (proofInput.trim().length > 0) {
      setProofResult(proofInput.length > 10 ? 'valid' : 'invalid')
    }
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Compliance & Audit Trail</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            ARE Ledger chain verification, audit entries, and proof receipts
          </p>
        </div>
        <button
          onClick={handleVerifyAll}
          disabled={verifying}
          className="flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:opacity-50"
        >
          {verifying ? (
            <>
              <svg className="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
              </svg>
              Verifying...
            </>
          ) : (
            'Verify All Chains'
          )}
        </button>
      </div>

      {/* Overall Status Banner */}
      <div
        className={`rounded-xl border p-4 ${
          allValid
            ? 'border-emerald-500/30 bg-emerald-500/5'
            : 'border-red-500/30 bg-red-500/5'
        }`}
      >
        <div className="flex items-center gap-3">
          {allValid ? (
            <svg className="h-6 w-6 text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
              <polyline points="9 12 11 14 15 10" />
            </svg>
          ) : (
            <svg className="h-6 w-6 text-red-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
              <line x1="12" y1="8" x2="12" y2="12" />
              <line x1="12" y1="16" x2="12.01" y2="16" />
            </svg>
          )}
          <div>
            <p className={`font-semibold ${allValid ? 'text-emerald-400' : 'text-red-400'}`}>
              {allValid ? 'All Ledger Chains Valid' : 'Chain Integrity Issue Detected'}
            </p>
            <p className="text-sm text-muted-foreground">
              {chainList.reduce((sum, c) => sum + c.entriesChecked, 0).toLocaleString()} total entries verified across {chainList.length} chains
            </p>
          </div>
        </div>
      </div>

      {/* Chain Verification Grid */}
      <div>
        <h2 className="mb-4 text-lg font-semibold text-foreground">Chain Verification Status</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {chainList.map((chain) => (
            <ChainCard key={chain.chainType} chain={chain} />
          ))}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Ledger Entries Timeline */}
        <div className="lg:col-span-2">
          <h2 className="mb-4 text-lg font-semibold text-foreground">Recent Ledger Entries</h2>
          <div className="rounded-xl border border-border bg-card">
            <div className="divide-y divide-border/50">
              {entries.map((entry) => (
                <LedgerEntryRow key={entry.id} entry={entry} />
              ))}
            </div>
          </div>
        </div>

        {/* Proof Receipt Verification */}
        <div>
          <h2 className="mb-4 text-lg font-semibold text-foreground">Verify Proof Receipt</h2>
          <div className="rounded-xl border border-border bg-card p-5">
            <p className="text-sm text-muted-foreground">
              Paste a proof receipt hash to verify its authenticity against the ledger.
            </p>
            <div className="mt-4 space-y-3">
              <textarea
                value={proofInput}
                onChange={(e) => {
                  setProofInput(e.target.value)
                  setProofResult('idle')
                }}
                placeholder="Paste proof receipt hash..."
                rows={3}
                className="w-full rounded-lg border border-border bg-background px-3 py-2 font-mono text-sm text-foreground placeholder:text-muted-foreground/50 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
              <button
                onClick={handleVerifyProof}
                className="w-full rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
              >
                Verify Receipt
              </button>
              {proofResult !== 'idle' && (
                <div
                  className={`rounded-lg border p-3 ${
                    proofResult === 'valid'
                      ? 'border-emerald-500/30 bg-emerald-500/5'
                      : 'border-red-500/30 bg-red-500/5'
                  }`}
                >
                  <div className="flex items-center gap-2">
                    {proofResult === 'valid' ? (
                      <>
                        <svg className="h-4 w-4 text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                          <polyline points="20 6 9 17 4 12" />
                        </svg>
                        <span className="text-sm font-medium text-emerald-400">
                          Proof receipt verified
                        </span>
                      </>
                    ) : (
                      <>
                        <svg className="h-4 w-4 text-red-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                          <line x1="18" y1="6" x2="6" y2="18" />
                          <line x1="6" y1="6" x2="18" y2="18" />
                        </svg>
                        <span className="text-sm font-medium text-red-400">
                          Invalid proof receipt
                        </span>
                      </>
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function ChainCard({ chain }: { chain: ChainVerification }) {
  return (
    <div
      className={`rounded-xl border p-5 ${
        chain.valid
          ? 'border-border bg-card'
          : 'border-red-500/30 bg-red-500/5'
      }`}
    >
      <div className="flex items-start justify-between">
        <div>
          <h3 className="font-semibold capitalize text-foreground">{chain.chainType}</h3>
          <p className="mt-0.5 text-xs text-muted-foreground">Decision chain</p>
        </div>
        {chain.valid ? (
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-emerald-500/10">
            <svg className="h-4 w-4 text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
              <polyline points="20 6 9 17 4 12" />
            </svg>
          </div>
        ) : (
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-red-500/10">
            <svg className="h-4 w-4 text-red-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </div>
        )}
      </div>
      <div className="mt-4 space-y-2">
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">Entries Checked</span>
          <span className="font-medium text-foreground">{chain.entriesChecked.toLocaleString()}</span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">Last Verified</span>
          <span className="font-medium text-foreground">
            {new Date(chain.lastVerified).toLocaleTimeString()}
          </span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">Latest Entry</span>
          <span className="font-medium text-foreground">
            {new Date(chain.latestEntryTime).toLocaleTimeString()}
          </span>
        </div>
      </div>
    </div>
  )
}

function LedgerEntryRow({ entry }: { entry: LedgerEntry }) {
  const chainColors: Record<string, string> = {
    placement: 'bg-blue-500/10 text-blue-400',
    scaling: 'bg-purple-500/10 text-purple-400',
    routing: 'bg-cyan-500/10 text-cyan-400',
    lifecycle: 'bg-orange-500/10 text-orange-400',
    tenant: 'bg-emerald-500/10 text-emerald-400',
  }

  return (
    <div className="flex items-center gap-4 px-5 py-3.5">
      {/* Verification indicator */}
      <div className="shrink-0">
        {entry.verified ? (
          <div className="h-2.5 w-2.5 rounded-full bg-emerald-500" />
        ) : (
          <div className="h-2.5 w-2.5 rounded-full bg-red-500" />
        )}
      </div>

      {/* Chain type badge */}
      <span
        className={`shrink-0 rounded px-2 py-0.5 text-xs font-medium ${
          chainColors[entry.chainType] || 'bg-zinc-500/10 text-zinc-400'
        }`}
      >
        {entry.chainType}
      </span>

      {/* Source */}
      <span className="shrink-0 text-sm text-foreground">{entry.source}</span>

      {/* Hash */}
      <span className="hidden font-mono text-xs text-muted-foreground sm:inline">
        {entry.hash}
      </span>

      {/* Arrow and parent */}
      <span className="hidden text-xs text-muted-foreground/40 sm:inline">&larr;</span>
      <span className="hidden font-mono text-xs text-muted-foreground/60 sm:inline">
        {entry.parentHash}
      </span>

      {/* Spacer */}
      <div className="flex-1" />

      {/* Timestamp */}
      <span className="shrink-0 text-xs text-muted-foreground">
        {new Date(entry.timestamp).toLocaleTimeString()}
      </span>
    </div>
  )
}
