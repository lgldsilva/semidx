import { cx } from '../lib/cx'

export interface TabItem<T extends string> {
  id: T
  label: string
}

export interface TabsProps<T extends string> {
  tabs: ReadonlyArray<TabItem<T>>
  active: T
  onSelect: (id: T) => void
  /** Accessible name for the tablist (e.g. "Project sections"). */
  label?: string
  className?: string
}

/** Pill tab strip — the accessible tablist pattern used by ProjectWorkspace. */
export function Tabs<T extends string>({ tabs, active, onSelect, label, className }: TabsProps<T>) {
  return (
    <div role="tablist" aria-label={label} className={cx('flex flex-wrap gap-1.5', className)}>
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={active === t.id}
          className={cx(
            'cursor-pointer rounded-full border px-3.5 py-1.5 text-sm transition-colors',
            active === t.id
              ? 'border-accent bg-accent text-accent-fg'
              : 'border-border bg-transparent text-fg hover:border-accent hover:bg-accent hover:text-accent-fg',
          )}
          onClick={() => onSelect(t.id)}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
