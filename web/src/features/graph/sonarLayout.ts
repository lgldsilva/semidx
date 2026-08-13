import type { GraphNode } from '../../api'

export type Pos = { x: number; y: number }

/** Place the seed at the center and each BFS depth on a sonar ring. */
export function sonarLayout(nodes: GraphNode[], width: number, height: number): Map<string, Pos> {
  const map = new Map<string, Pos>()
  const cx = width / 2
  const cy = height / 2
  if (nodes.length === 0) return map
  if (nodes.length === 1) {
    map.set(nodes[0].id, { x: cx, y: cy })
    return map
  }

  const layers = new Map<number, GraphNode[]>()
  for (const node of nodes) {
    const depth = node.seed ? 0 : Math.max(0, node.depth ?? 1)
    const list = layers.get(depth) ?? []
    list.push(node)
    layers.set(depth, list)
  }
  const maxDepth = Math.max(...layers.keys(), 1)
  const maxR = Math.min(width, height) * 0.42

  for (const [depth, list] of layers) {
    list.sort((a, b) => a.id.localeCompare(b.id))
    if (depth === 0 && list.length === 1) {
      map.set(list[0].id, { x: cx, y: cy })
      continue
    }
    const r = depth === 0 ? Math.min(48, maxR * 0.18) : (maxR * depth) / maxDepth
    list.forEach((node, i) => {
      const angle = (2 * Math.PI * i) / list.length - Math.PI / 2
      map.set(node.id, { x: cx + r * Math.cos(angle), y: cy + r * Math.sin(angle) })
    })
  }
  return map
}

export function ringsFor(nodes: GraphNode[], width: number, height: number): number[] {
  const depths = new Set<number>()
  for (const node of nodes) {
    const depth = node.seed ? 0 : Math.max(0, node.depth ?? 1)
    if (depth > 0) depths.add(depth)
  }
  const maxDepth = Math.max(...depths, 1)
  const maxR = Math.min(width, height) * 0.42
  return [...depths].sort((a, b) => a - b).map((d) => (maxR * d) / maxDepth)
}
