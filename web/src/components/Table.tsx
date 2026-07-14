import type { TableHTMLAttributes } from 'react'
import { cx } from '../lib/cx'

/** Data table with themed th/td defaults; compose thead/tbody as children. */
export function Table({ className, ...rest }: TableHTMLAttributes<HTMLTableElement>) {
  return (
    <div className="w-full overflow-x-auto">
      <table
        className={cx(
          'w-full border-collapse text-sm',
          '[&_th]:border-b [&_th]:border-border [&_th]:px-2 [&_th]:py-2 [&_th]:text-left [&_th]:align-top [&_th]:font-semibold [&_th]:text-muted',
          '[&_td]:border-b [&_td]:border-border [&_td]:px-2 [&_td]:py-2 [&_td]:text-left [&_td]:align-top',
          className,
        )}
        {...rest}
      />
    </div>
  )
}
