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
})
