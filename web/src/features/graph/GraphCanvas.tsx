import { useEffect, useMemo, useRef, useState, type PointerEvent } from 'react'
import type { GraphEdge, GraphNode } from '../../api'
import { ringsFor, sonarLayout } from './sonarLayout'

type View = { x: number; y: number; k: number }

const WIDTH = 860
const HEIGHT = 520

export function GraphCanvas({
  nodes,
  edges,
  highlightPath = [],
  selected,
  onSelect,
  onFocus,
  height = HEIGHT,
}: {
  nodes: GraphNode[]
  edges: GraphEdge[]
  highlightPath?: string[]
  selected?: string | null
  onSelect?: (id: string, kind: string) => void
  onFocus?: (id: string, kind: string) => void
  height?: number
}) {
  const [hover, setHover] = useState<string | null>(null)
  const [view, setView] = useState<View>({ x: 0, y: 0, k: 1 })
  const drag = useRef<{ x: number; y: number; vx: number; vy: number } | null>(null)
  const svgRef = useRef<SVGSVGElement | null>(null)
  const positions = useMemo(() => sonarLayout(nodes, WIDTH, height), [nodes, height])
  const rings = useMemo(() => ringsFor(nodes, WIDTH, height), [nodes, height])
  const pathSet = useMemo(() => new Set(highlightPath), [highlightPath])
  const pathEdge = useMemo(() => {
    const s = new Set<string>()
    for (let i = 0; i + 1 < highlightPath.length; i++) {
      s.add(`${highlightPath[i]}\0${highlightPath[i + 1]}`)
    }
    return s
  }, [highlightPath])

  useEffect(() => {
    const el = svgRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const factor = e.deltaY < 0 ? 1.08 : 0.92
      setView((v) => ({ ...v, k: Math.min(3, Math.max(0.35, v.k * factor)) }))
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [nodes.length])

  if (nodes.length === 0) {
    return <p className="text-sm text-muted">No graph nodes to display.</p>
  }

  function onPointerDown(e: PointerEvent<SVGSVGElement>) {
    if (e.button !== 0) return
    if ((e.target as Element).closest('[data-node]')) return
    drag.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y }
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  function onPointerMove(e: PointerEvent<SVGSVGElement>) {
    if (!drag.current) return
    setView({
      x: drag.current.vx + (e.clientX - drag.current.x),
      y: drag.current.vy + (e.clientY - drag.current.y),
      k: view.k,
    })
  }

  function endDrag() {
    drag.current = null
  }

  const cx = WIDTH / 2
  const cy = height / 2

  return (
    <div className="overflow-hidden rounded-md border border-border bg-surface">
      <p className="m-0 min-h-[1.4rem] truncate px-2 py-1 text-xs text-muted">
        {hover ?? 'Scroll to zoom · drag to pan · click a node · double-click to focus'}
      </p>
      <svg
        ref={svgRef}
        width="100%"
        viewBox={`0 0 ${WIDTH} ${height}`}
        aria-label="Dependency graph"
        className="block cursor-grab touch-none bg-bg active:cursor-grabbing"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
      >
        <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
          {rings.map((r) => (
            <circle
              key={r}
              cx={cx}
              cy={cy}
              r={r}
              fill="none"
              stroke="var(--border)"
              strokeDasharray="3 5"
              strokeOpacity={0.8}
            />
          ))}
          {edges.map((e, i) => {
            const a = positions.get(e.source)
            const b = positions.get(e.target)
            if (!a || !b) return null
            const hi = pathEdge.has(`${e.source}\0${e.target}`)
            const dim = highlightPath.length > 0 && !hi
            return (
              <line
                key={`${e.source}-${e.target}-${i}`}
                x1={a.x}
                y1={a.y}
                x2={b.x}
                y2={b.y}
                stroke={hi ? 'var(--accent)' : 'var(--muted)'}
                strokeWidth={hi ? 2.4 : 1.1}
                strokeOpacity={dim ? 0.22 : 0.75}
                strokeDasharray={e.reverse ? '4 3' : undefined}
                onMouseEnter={() =>
                  setHover(`${e.source} → ${e.target} (${e.kind}${e.reverse ? ', reverse' : ''})`)
                }
                onMouseLeave={() => setHover(null)}
              />
            )
          })}
          {nodes.map((n) => {
            const p = positions.get(n.id)
            if (!p) return null
            const isSel = selected === n.id
            const hi = pathSet.has(n.id) || n.seed || isSel
            const dim = highlightPath.length > 0 && !pathSet.has(n.id) && !n.seed && !isSel
            const isPkg = n.kind === 'package'
            const r = n.seed ? 13 : isPkg ? 9 : 10
            const showLabel = hi || nodes.length <= 40 || hover === n.id
            return (
              <g
                key={n.id}
                data-node={n.id}
                transform={`translate(${p.x},${p.y})`}
                style={{ cursor: 'pointer', opacity: dim ? 0.3 : 1 }}
                onClick={(ev) => {
                  ev.stopPropagation()
                  onSelect?.(n.id, n.kind)
                }}
                onDoubleClick={(ev) => {
                  ev.stopPropagation()
                  onFocus?.(n.id, n.kind)
                }}
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
                    fill={hi ? 'var(--accent)' : 'var(--surface-2)'}
                    stroke={isSel ? 'var(--fg)' : 'var(--border)'}
                    strokeWidth={isSel ? 2.2 : 1.2}
                  />
                ) : (
                  <circle
                    r={r}
                    fill={hi ? 'var(--accent)' : 'var(--surface)'}
                    stroke={isSel ? 'var(--fg)' : 'var(--accent)'}
                    strokeWidth={n.seed || isSel ? 2.4 : 1.4}
                  />
                )}
                <title>{n.id}</title>
                {showLabel && (
                  <text
                    y={r + 12}
                    textAnchor="middle"
                    fontSize={10}
                    fill="var(--fg)"
                    style={{ pointerEvents: 'none' }}
                  >
                    {n.label.length > 18 ? `${n.label.slice(0, 16)}…` : n.label}
                  </text>
                )}
              </g>
            )
          })}
        </g>
      </svg>
    </div>
  )
}
