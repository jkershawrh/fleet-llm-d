interface StatusBadgeProps {
  status: string
  size?: 'sm' | 'md'
}

const statusConfig: Record<string, { color: string; dotColor: string }> = {
  Running: { color: 'bg-emerald-400/10 text-emerald-400', dotColor: 'bg-emerald-400' },
  Complete: { color: 'bg-emerald-400/10 text-emerald-400', dotColor: 'bg-emerald-400' },
  Progressing: { color: 'bg-blue-400/10 text-blue-400', dotColor: 'bg-blue-400' },
  Placing: { color: 'bg-blue-400/10 text-blue-400', dotColor: 'bg-blue-400' },
  Pending: { color: 'bg-yellow-400/10 text-yellow-400', dotColor: 'bg-yellow-400' },
  Paused: { color: 'bg-yellow-400/10 text-yellow-400', dotColor: 'bg-yellow-400' },
  Degraded: { color: 'bg-orange-400/10 text-orange-400', dotColor: 'bg-orange-400' },
  Failed: { color: 'bg-red-400/10 text-red-400', dotColor: 'bg-red-400' },
  RolledBack: { color: 'bg-red-400/10 text-red-400', dotColor: 'bg-red-400' },
  ScaledToZero: { color: 'bg-zinc-400/10 text-zinc-400', dotColor: 'bg-zinc-400' },
}

export function StatusBadge({ status, size = 'md' }: StatusBadgeProps) {
  const config = statusConfig[status] || { color: 'bg-zinc-400/10 text-zinc-400', dotColor: 'bg-zinc-400' }

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full font-medium ${config.color} ${
        size === 'sm' ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-xs'
      }`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${config.dotColor}`} />
      {status}
    </span>
  )
}
