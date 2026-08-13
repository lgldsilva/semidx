const KEY = 'semidx.recent-searches'
const MAX = 8

export type RecentSearch = {
  query: string
  project?: string
  at: number
}

export function loadRecentSearches(): RecentSearch[] {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as RecentSearch[]
    return Array.isArray(parsed) ? parsed.slice(0, MAX) : []
  } catch {
    return []
  }
}

export function rememberSearch(query: string, project?: string): RecentSearch[] {
  const q = query.trim()
  if (!q) return loadRecentSearches()
  const next: RecentSearch[] = [
    { query: q, project, at: Date.now() },
    ...loadRecentSearches().filter(
      (item) => !(item.query === q && (item.project || '') === (project || '')),
    ),
  ].slice(0, MAX)
  try {
    localStorage.setItem(KEY, JSON.stringify(next))
  } catch {
    /* ignore quota */
  }
  return next
}
