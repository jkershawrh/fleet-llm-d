interface ProgressBarProps {
  value: number
  max: number
  label?: string
  showPercentage?: boolean
  size?: 'sm' | 'md' | 'lg'
  thresholds?: {
    warning: number  // percentage threshold for warning (yellow)
    critical: number // percentage threshold for critical (red)
  }
}

export function ProgressBar({
  value,
  max,
  label,
  showPercentage = true,
  size = 'md',
  thresholds = { warning: 80, critical: 95 },
}: ProgressBarProps) {
  const percentage = max > 0 ? Math.round((value / max) * 100) : 0

  let barColor = 'bg-emerald-500'
  if (percentage >= thresholds.critical) {
    barColor = 'bg-red-500'
  } else if (percentage >= thresholds.warning) {
    barColor = 'bg-yellow-500'
  }

  const heightClass = {
    sm: 'h-1.5',
    md: 'h-2.5',
    lg: 'h-4',
  }[size]

  return (
    <div className="w-full">
      {(label || showPercentage) && (
        <div className="mb-1.5 flex items-center justify-between">
          {label && <span className="text-xs text-muted-foreground">{label}</span>}
          {showPercentage && (
            <span className="text-xs font-medium text-muted-foreground">
              {percentage}%
            </span>
          )}
        </div>
      )}
      <div className={`w-full overflow-hidden rounded-full bg-muted ${heightClass}`}>
        <div
          className={`${heightClass} rounded-full transition-all duration-500 ${barColor}`}
          style={{ width: `${Math.min(percentage, 100)}%` }}
        />
      </div>
      {max > 0 && (
        <p className="mt-1 text-xs text-muted-foreground/60">
          {value.toLocaleString()} / {max.toLocaleString()}
        </p>
      )}
    </div>
  )
}
