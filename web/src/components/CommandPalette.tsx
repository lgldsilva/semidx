import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Project } from '../api'
import { loadRecentSearches } from '../lib/recentSearches'
import { cx } from '../lib/cx'

type Item = {
  id: string
  label: string
  hint?: string
  run: () => void
}

export function CommandPalette() {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [projects, setProjects] = useState<Project[]>([])
  const [active, setActive] = useState(0)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const inField =
        e.target instanceof HTMLElement &&
        (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.isContentEditable)
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setOpen((v) => !v)
        return
      }
      if (e.key === '/' && !inField && !e.metaKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault()
        setOpen(true)
        return
      }
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    if (!open) return
    setQ('')
    setActive(0)
    void api.projects().then(setProjects).catch(() => setProjects([]))
  }, [open])

  const items = useMemo(() => {
    const go = (to: string) => {
      setOpen(false)
      navigate(to)
    }
    const all: Item[] = [
      { id: 'search', label: 'Search the index', hint: 'Search', run: () => go('/search') },
      { id: 'projects', label: 'All projects', hint: 'Projects', run: () => go('/') },
      { id: 'chat', label: 'Chat', run: () => go('/chat') },
      { id: 'jobs', label: 'Jobs', run: () => go('/jobs') },
      { id: 'usage', label: 'Usage', run: () => go('/usage') },
      { id: 'settings', label: 'Settings', run: () => go('/settings') },
    ]
    for (const p of projects) {
      all.push({
        id: `p-${p.name}`,
        label: `Open ${p.name}`,
        hint: 'Project',
        run: () => go(`/projects/${encodeURIComponent(p.name)}`),
      })
      all.push({
        id: `g-${p.name}`,
        label: `Graph: ${p.name}`,
        hint: 'Graph',
        run: () => go(`/projects/${encodeURIComponent(p.name)}?tab=graph`),
      })
      all.push({
        id: `s-${p.name}`,
        label: `Search in ${p.name}`,
        hint: 'Search',
        run: () => go(`/search?project=${encodeURIComponent(p.name)}`),
      })
    }
    for (const recent of loadRecentSearches()) {
      const href = recent.project
        ? `/search?q=${encodeURIComponent(recent.query)}&project=${encodeURIComponent(recent.project)}`
        : `/search?q=${encodeURIComponent(recent.query)}&all=1`
      all.push({
        id: `r-${recent.at}`,
        label: recent.query,
        hint: recent.project ? `Recent · ${recent.project}` : 'Recent',
        run: () => go(href),
      })
    }
    const query = q.trim()
    if (query) {
      all.unshift({
        id: 'run-search',
        label: `Search for “${query}”`,
        hint: 'All projects',
        run: () => go(`/search?q=${encodeURIComponent(query)}&all=1`),
      })
    }
    const needle = q.trim().toLowerCase()
    if (!needle) return all.slice(0, 18)
    return all.filter((item) => `${item.label} ${item.hint || ''}`.toLowerCase().includes(needle)).slice(0, 18)
  }, [projects, q, navigate])

  useEffect(() => {
    setActive(0)
  }, [q, open])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-fg/25 px-3 pt-[12vh]">
      <button
        type="button"
        className="absolute inset-0 cursor-default border-0 bg-transparent"
        aria-label="Close command palette"
        onClick={() => setOpen(false)}
      />
      <dialog
        open
        aria-label="Command palette"
        className="relative m-0 w-full max-w-xl overflow-hidden rounded-lg border border-border bg-surface p-0 shadow-lg"
      >
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search, open a project, jump to graph…"
          className="w-full border-0 border-b border-border bg-transparent px-3 py-3 text-sm text-fg outline-none placeholder:text-muted"
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setActive((i) => Math.min(items.length - 1, i + 1))
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setActive((i) => Math.max(0, i - 1))
            } else if (e.key === 'Enter' && items[active]) {
              e.preventDefault()
              items[active].run()
            }
          }}
        />
        <ul className="m-0 max-h-[50vh] list-none overflow-auto p-1">
          {items.length === 0 && <li className="px-3 py-2 text-sm text-muted">No matches.</li>}
          {items.map((item, i) => (
            <li key={item.id}>
              <button
                type="button"
                className={cx(
                  'flex w-full cursor-pointer items-center justify-between gap-3 rounded-md border-0 px-3 py-2 text-left text-sm',
                  i === active ? 'bg-accent/15 text-fg' : 'bg-transparent text-fg hover:bg-surface-2',
                )}
                onMouseEnter={() => setActive(i)}
                onClick={item.run}
              >
                <span>{item.label}</span>
                {item.hint && <span className="text-xs text-muted">{item.hint}</span>}
              </button>
            </li>
          ))}
        </ul>
      </dialog>
    </div>
  )
}
