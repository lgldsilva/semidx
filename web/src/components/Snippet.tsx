import type { HTMLAttributes } from 'react'
import { cx } from '../lib/cx'

/** Code block on the themed code background; replaces legacy `pre.snippet`. */
export function Snippet({ className, ...rest }: HTMLAttributes<HTMLPreElement>) {
  return (
    <pre
      className={cx(
        'mt-1.5 overflow-x-auto rounded-md bg-code-bg px-3 py-2.5 text-xs whitespace-pre-wrap text-code-fg',
        className,
      )}
      {...rest}
    />
  )
}

/** Inline code chip on the themed code background; replaces legacy `code`. */
export function Code({ className, ...rest }: HTMLAttributes<HTMLElement>) {
  return (
    <code
      className={cx('rounded bg-code-bg px-1 py-0.5 text-[0.88em] text-code-fg', className)}
      {...rest}
    />
  )
}
