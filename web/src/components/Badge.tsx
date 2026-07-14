import type { HTMLAttributes } from 'react'
import { cx } from '../lib/cx'

export type BadgeTone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

const TONE: Record<BadgeTone, string> = {
  neutral: 'bg-surface-2 text-muted',
  accent: 'bg-accent/10 text-accent',
  success: 'bg-success/10 text-success',
  warning: 'bg-warning/10 text-warning',
  danger: 'bg-danger/10 text-danger',
}

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: BadgeTone
}

export function Badge({ tone = 'accent', className, ...rest }: BadgeProps) {
  return (
    <span
      className={cx('inline-block rounded-full px-2 py-0.5 text-xs font-medium', TONE[tone], className)}
      {...rest}
    />
  )
}
