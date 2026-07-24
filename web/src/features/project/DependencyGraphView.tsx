import { useMemo, useState } from 'react'
import type { GraphEdge, GraphNode } from '../../api'

type Pos = { x: number; y: number }

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

export function DependencyGraphView({
  nodes,
  edges,
  highlightPath = [],
  onOpenNode,
  height = 360,
}: {
  nodes: GraphNode[]
  edges: GraphEdge[]
  highlightPath?: string[]
  onOpenNode?: (id: string, kind: string) => void
  height?: number
}) {
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
        {edges.map((e, i) => {
          const a = positions.get(e.source)
          const b = positions.get(e.target)
          if (!a || !b) return null
          const hi = pathEdge.has(`${e.source}\0${e.target}`)
          const dim = highlightPath.length > 0 && !hi
          return (
            <line
              key={i}
              x1={a.x}
              y1={a.y}
              x2={b.x}
              y2={b.y}
              stroke={hi ? '#0f766e' : '#94a3b8'}
              strokeWidth={hi ? 2.5 : 1.2}
              strokeOpacity={dim ? 0.25 : 0.85}
              strokeDasharray={e.reverse ? '4 3' : undefined}
              markerEnd={hi ? 'url(#arrow-hi)' : 'url(#arrow)'}
              onMouseEnter={() => setHover(`${e.source} → ${e.target} (${e.kind}${e.reverse ? ', reverse' : ''})`)}
              onMouseLeave={() => setHover(null)}
            />
          )
        })}
        {nodes.map((n) => {
          const p = positions.get(n.id)
          if (!p) return null
          const hi = pathSet.has(n.id) || n.seed
          const dim = highlightPath.length > 0 && !pathSet.has(n.id) && !n.seed
          const isPkg = n.kind === 'package'
          const r = n.seed ? 14 : isPkg ? 10 : 11
          return (
            <g
              key={n.id}
              transform={`translate(${p.x},${p.y})`}
              style={{ cursor: onOpenNode && !isPkg ? 'pointer' : 'default', opacity: dim ? 0.35 : 1 }}
              onClick={() => onOpenNode?.(n.id, n.kind)}
              onMouseEnter={() => setHover(n.id)}
              onMouseLeave={() => setHover(null)}
            >
              {isPkg ? (
                <rect
                  x={-r}
                  y={-r}
                  width={r * 2}
                  height={r * 2}
                  rx={3}
                  fill={hi ? '#ccfbf1' : '#f1f5f9'}
                  stroke={hi ? '#0f766e' : '#64748b'}
                  strokeWidth={hi ? 2 : 1}
                />
              ) : (
                <circle
                  r={r}
                  fill={hi ? '#ccfbf1' : '#fff'}
                  stroke={hi ? '#0f766e' : '#334155'}
                  strokeWidth={hi ? 2.5 : 1.5}
                />
              )}
              <title>{n.id}</title>
              <text
                y={r + 12}
                textAnchor="middle"
                fontSize={10}
                fill="#334155"
                style={{ pointerEvents: 'none' }}
              >
                {n.label.length > 18 ? `${n.label.slice(0, 16)}…` : n.label}
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}
