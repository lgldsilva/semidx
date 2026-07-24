import { useMemo, useState, type ReactNode } from 'react'
import type { GraphEdge, GraphNode } from '../../api'

type Pos = { x: number; y: number }

type DependencyGraphViewProps = Readonly<{
  nodes: GraphNode[]
  edges: GraphEdge[]
  highlightPath?: string[]
  onOpenNode?: (id: string, kind: string) => void
  height?: number
}>

/** Circular layout — no external force-layout dependency (CSP-safe, tiny). */
function layout(nodes: GraphNode[], width: number, height: number): Map<string, Pos> {
  const cx = width / 2
  const cy = height / 2
  const r = Math.min(width, height) * 0.38
  const map = new Map<string, Pos>()
  const n = nodes.length
  if (n === 0) return map
  if (n === 1) {
    map.set(nodes[0].id, { x: cx, y: cy })
    return map
  }
  nodes.forEach((node, i) => {
    const angle = (2 * Math.PI * i) / n - Math.PI / 2
    map.set(node.id, { x: cx + r * Math.cos(angle), y: cy + r * Math.sin(angle) })
  })
  return map
}

function edgeKey(e: GraphEdge): string {
  return `${e.source}->${e.target}:${e.kind}${e.reverse ? ':rev' : ''}`
}

function nodeRadius(n: GraphNode): number {
  if (n.seed) return 14
  if (n.kind === 'package') return 10
  return 11
}

function GraphNodeMark({
  node,
  pos,
  highlight,
  dim,
  onOpenNode,
  onHover,
}: Readonly<{
  node: GraphNode
  pos: Pos
  highlight: boolean
  dim: boolean
  onOpenNode?: (id: string, kind: string) => void
  onHover: (id: string | null) => void
}>): ReactNode {
  const isPkg = node.kind === 'package'
  const r = nodeRadius(node)
  const fill = highlight ? '#ccfbf1' : isPkg ? '#f1f5f9' : '#fff'
  const stroke = highlight ? '#0f766e' : isPkg ? '#64748b' : '#334155'
  const strokeWidth = highlight ? (isPkg ? 2 : 2.5) : isPkg ? 1 : 1.5
  const label = node.label.length > 18 ? `${node.label.slice(0, 16)}…` : node.label

  return (
    <g
      transform={`translate(${pos.x},${pos.y})`}
      style={{ cursor: onOpenNode && !isPkg ? 'pointer' : 'default', opacity: dim ? 0.35 : 1 }}
      onClick={() => onOpenNode?.(node.id, node.kind)}
      onMouseEnter={() => onHover(node.id)}
      onMouseLeave={() => onHover(null)}
    >
      {isPkg ? (
        <rect x={-r} y={-r} width={r * 2} height={r * 2} rx={3} fill={fill} stroke={stroke} strokeWidth={strokeWidth} />
      ) : (
        <circle r={r} fill={fill} stroke={stroke} strokeWidth={strokeWidth} />
      )}
      <title>{node.id}</title>
      <text y={r + 12} textAnchor="middle" fontSize={10} fill="#334155" style={{ pointerEvents: 'none' }}>
        {label}
      </text>
    </g>
  )
}

export function DependencyGraphView({
  nodes,
  edges,
  highlightPath = [],
  onOpenNode,
  height = 360,
}: DependencyGraphViewProps) {
  const width = 720
  const [hover, setHover] = useState<string | null>(null)
  const positions = useMemo(() => layout(nodes, width, height), [nodes, height])
  const pathSet = useMemo(() => new Set(highlightPath), [highlightPath])
  const pathEdge = useMemo(() => {
    const s = new Set<string>()
    for (let i = 0; i + 1 < highlightPath.length; i++) {
      s.add(`${highlightPath[i]}\0${highlightPath[i + 1]}`)
    }
    return s
  }, [highlightPath])

  if (nodes.length === 0) {
    return <p className="text-xs text-muted">No graph nodes to display.</p>
  }

  return (
    <div className="overflow-auto rounded-md border border-slate-200">
      {/* Reserve the hover line's height so hovering never reflows the canvas. */}
      <p className="m-0 min-h-[1.25rem] px-2 py-1 text-xs text-muted">{hover ?? '\u00a0'}</p>

      <svg width={width} height={height} aria-label="Dependency graph">
        <defs>
          <marker id="arrow" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
            <path d="M0,0 L6,3 L0,6 Z" fill="#64748b" />
          </marker>
          <marker id="arrow-hi" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
            <path d="M0,0 L6,3 L0,6 Z" fill="#0f766e" />
          </marker>
        </defs>
        {edges.map((e) => {
          const a = positions.get(e.source)
          const b = positions.get(e.target)
          if (!a || !b) return null
          const hi = pathEdge.has(`${e.source}\0${e.target}`)
          const dim = highlightPath.length > 0 && !hi
          const revSuffix = e.reverse ? ', reverse' : ''
          return (
            <line
              key={edgeKey(e)}
              x1={a.x}
              y1={a.y}
              x2={b.x}
              y2={b.y}
              stroke={hi ? '#0f766e' : '#94a3b8'}
              strokeWidth={hi ? 2.5 : 1.2}
              strokeOpacity={dim ? 0.25 : 0.85}
              strokeDasharray={e.reverse ? '4 3' : undefined}
              markerEnd={hi ? 'url(#arrow-hi)' : 'url(#arrow)'}
              onMouseEnter={() => setHover(`${e.source} → ${e.target} (${e.kind}${revSuffix})`)}
              onMouseLeave={() => setHover(null)}
            />
          )
        })}
        {nodes.map((n) => {
          const p = positions.get(n.id)
          if (!p) return null
          const highlight = pathSet.has(n.id) || Boolean(n.seed)
          const dim = highlightPath.length > 0 && !pathSet.has(n.id) && !n.seed
          return (
            <GraphNodeMark
              key={n.id}
              node={n}
              pos={p}
              highlight={highlight}
              dim={dim}
              onOpenNode={onOpenNode}
              onHover={setHover}
            />
          )
        })}
      </svg>
    </div>
  )
}
