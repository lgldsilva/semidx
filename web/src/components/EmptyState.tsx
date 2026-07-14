import { cx } from '../lib/cx'

export interface EmptyStateProps {
  title: string
  /** Supporting copy shown under the title. */
  children?: React.ReactNode
  /** Optional call to action (e.g. a Button). */
  action?: React.ReactNode
  className?: string
}

export function EmptyState({ title, children, action, className }: EmptyStateProps) {
  return (
    <div
      className={cx(
        'flex flex-col items-center gap-2 rounded-[10px] border border-dashed border-border px-6 py-10 text-center',
        className,
      )}
    >
      <p className="font-medium text-fg">{title}</p>
      {children && <p className="m-0 text-sm text-muted">{children}</p>}
      {action}
    </div>
  )
}
