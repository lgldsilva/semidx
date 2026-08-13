import { describe, expect, it } from 'vitest'
import type { GraphNode } from '../../api'
import { ringsFor, sonarLayout } from './sonarLayout'

const nodes: GraphNode[] = [
  { id: 'seed.go', label: 'seed.go', kind: 'file', seed: true, depth: 0 },
  { id: 'pkg/', label: 'pkg', kind: 'package', depth: 1 },
  { id: 'pkg/a.go', label: 'a.go', kind: 'file', depth: 2 },
]

describe('sonarLayout', () => {
  it('puts the seed at the canvas center', () => {
    const pos = sonarLayout(nodes, 800, 400)
    expect(pos.get('seed.go')).toEqual({ x: 400, y: 200 })
  })

  it('places deeper nodes farther from the center than hop-1 nodes', () => {
    const pos = sonarLayout(nodes, 800, 400)
    const seed = pos.get('seed.go')!
    const pkg = pos.get('pkg/')!
    const file = pos.get('pkg/a.go')!
    const d1 = Math.hypot(pkg.x - seed.x, pkg.y - seed.y)
    const d2 = Math.hypot(file.x - seed.x, file.y - seed.y)
    expect(d2).toBeGreaterThan(d1)
  })

  it('emits a ring per positive depth', () => {
    expect(ringsFor(nodes, 800, 400)).toHaveLength(2)
  })
})
