import type { HTMLAttributes } from 'react'
import { cx } from '../lib/cx'

export function Card({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cx('rounded-[10px] border border-border bg-surface px-5 py-4 shadow-sm', className)}
      {...rest}
    />
  )
}
