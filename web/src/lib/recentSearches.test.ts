import { afterEach, describe, expect, it } from 'vitest'
import { loadRecentSearches, rememberSearch } from './recentSearches'

afterEach(() => {
  localStorage.clear()
})

describe('recentSearches', () => {
  it('dedupes and keeps the newest query first', () => {
    rememberSearch('auth', 'demo')
    rememberSearch('graph', 'demo')
    rememberSearch('auth', 'demo')
    const items = loadRecentSearches()
    expect(items.map((i) => i.query)).toEqual(['auth', 'graph'])
  })
})
