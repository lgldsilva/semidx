import { useRef } from 'react'
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
  const tabRefs = useRef<Record<string, HTMLButtonElement | null>>({})

  function selectAndFocus(id: T) {
    onSelect(id)
    tabRefs.current[id]?.focus()
  }

  function move(current: T, direction: 1 | -1) {
    const index = tabs.findIndex((tab) => tab.id === current)
    if (index < 0 || tabs.length === 0) return
    const next = (index + direction + tabs.length) % tabs.length
    selectAndFocus(tabs[next].id)
  }

  return (
    <div role="tablist" aria-label={label} className={cx('flex flex-wrap gap-1.5', className)}>
      {tabs.map((t) => (
        <button
          key={t.id}
          ref={(node) => { tabRefs.current[t.id] = node }}
          type="button"
          role="tab"
          id={`tab-${t.id}`}
          aria-selected={active === t.id}
          tabIndex={active === t.id ? 0 : -1}
          className={cx(
            'cursor-pointer rounded-full border px-3.5 py-1.5 text-sm transition-colors',
            active === t.id
              ? 'border-accent bg-accent text-accent-fg'
              : 'border-border bg-transparent text-fg hover:border-accent hover:bg-accent hover:text-accent-fg',
          )}
          onClick={() => onSelect(t.id)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowRight') {
              event.preventDefault()
              move(t.id, 1)
            } else if (event.key === 'ArrowLeft') {
              event.preventDefault()
              move(t.id, -1)
            } else if (event.key === 'Home') {
              event.preventDefault()
              selectAndFocus(tabs[0].id)
            } else if (event.key === 'End') {
              event.preventDefault()
              selectAndFocus(tabs[tabs.length - 1].id)
            }
          }}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
