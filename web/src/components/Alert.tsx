import type { HTMLAttributes } from 'react'
import { cx } from '../lib/cx'

export type AlertKind = 'error' | 'success' | 'info'

const KIND: Record<AlertKind, string> = {
  error: 'bg-danger/10 text-danger',
  success: 'bg-success/10 text-success',
  info: 'bg-accent/10 text-accent',
}

export interface AlertProps extends HTMLAttributes<HTMLDivElement> {
  kind?: AlertKind
}

export function Alert({ kind = 'info', className, ...rest }: AlertProps) {
  return (
    <div
      role="alert"
      className={cx('my-2.5 rounded-md px-3 py-2 text-sm', KIND[kind], className)}
      {...rest}
    />
  )
}
