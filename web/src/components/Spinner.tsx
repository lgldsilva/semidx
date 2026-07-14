import { cx } from '../lib/cx'

export function Spinner({
  label = 'Loading',
  className,
}: Readonly<{ label?: string; className?: string }>) {
  return (
    <output
      aria-label={label}
      className={cx(
        'inline-block size-4 animate-spin rounded-full border-2 border-border border-t-accent',
        className,
      )}
    />
  )
}
