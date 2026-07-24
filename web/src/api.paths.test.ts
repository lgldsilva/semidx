import { describe, expect, it } from 'vitest'

/** Mirrors admin API path construction for project-scoped endpoints. */
export function projectApiPath(project: string, suffix: string, query = ''): string {
  const q = query ? `?${query}` : ''
  return `/admin/api/projects/${encodeURIComponent(project)}/${suffix}${q}`
}

describe('projectApiPath', () => {
  it('encodes slashes in project names', () => {
    expect(projectApiPath('acme/app', 'sbom', 'format=cyclonedx-json')).toBe(
      '/admin/api/projects/acme%2Fapp/sbom?format=cyclonedx-json',
    )
  })

  it('builds dead-code path', () => {
    expect(projectApiPath('demo', 'dead-code', 'limit=50')).toBe(
      '/admin/api/projects/demo/dead-code?limit=50',
    )
  })

  it('builds a graph subgraph path, omitting unset budgets', () => {
    const q = new URLSearchParams()
    q.set('seed', 'internal/store/store.go')
    q.set('depth', '2')
    expect(projectApiPath('demo', 'graph/subgraph', q.toString())).toBe(
      '/admin/api/projects/demo/graph/subgraph?seed=internal%2Fstore%2Fstore.go&depth=2',
    )
    expect(projectApiPath('demo', 'graph/subgraph')).toBe(
      '/admin/api/projects/demo/graph/subgraph',
    )
  })

  it('builds a graph path query with the undirected flag', () => {
    const q = new URLSearchParams({ from: 'a/b.go', to: 'c/d.go' })
    q.set('undirected', '1')
    expect(projectApiPath('demo', 'graph/path', q.toString())).toBe(
      '/admin/api/projects/demo/graph/path?from=a%2Fb.go&to=c%2Fd.go&undirected=1',
    )
  })
})
