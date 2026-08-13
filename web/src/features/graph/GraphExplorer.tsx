import { useEffect, useMemo, useState } from 'react'
import {
  api,
  ApiError,
  type GraphPath,
  type GraphSubgraph,
} from '../../api'
import { Alert } from '../../components/Alert'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Checkbox, Input } from '../../components/Input'
import { Code } from '../../components/Snippet'
import { GraphCanvas } from './GraphCanvas'

export function GraphExplorer({
  project,
  seedPath,
  onOpenFile,
  onAsk,
  onSeedChange,
}: {
  project: string
  seedPath: string
  onOpenFile: (path: string, line?: number) => void
  onAsk: (q: string) => void
  onSeedChange?: (path: string) => void
}) {
  const [seed, setSeed] = useState(seedPath)
  const [depth, setDepth] = useState(2)
  const [both, setBoth] = useState(true)
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<string | null>(seedPath || null)
  const [pathTo, setPathTo] = useState('')
  const [undirected, setUndirected] = useState(true)
  const [subgraph, setSubgraph] = useState<GraphSubgraph | null>(null)
  const [graphPath, setGraphPath] = useState<GraphPath | null>(null)
  const [stats, setStats] = useState<{
    nodes: number
    edges: number
    top_depends: { node: string; degree: number }[]
    top_depended: { node: string; degree: number }[]
  } | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setSeed(seedPath)
    if (seedPath) setSelected(seedPath)
  }, [seedPath])

  async function load(nextSeed = seed) {
    const seedVal = nextSeed.trim()
    const walkDepth = both && depth < 2 ? 2 : depth
    setBusy(true)
    setErr('')
    try {
      const [sg, overview] = await Promise.all([
        api.projectGraphSubgraph(project, seedVal, walkDepth, 800, both),
        api.projectGraphStats(project),
      ])
      setSubgraph(sg)
      setStats(overview)
      setGraphPath(null)
      if (seedVal) setSelected(seedVal)
      if (seedVal !== seedPath) onSeedChange?.(seedVal)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'graph load failed')
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => {
    setStats(null)
    setSubgraph(null)
    void load(seedPath)
    // seedPath from the URL; typing in the seed field does not remount.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- load reads current depth/both
  }, [project, seedPath])

  async function runPath() {
    if (!seed.trim() || !pathTo.trim()) return
    setBusy(true)
    setErr('')
    try {
      const result = await api.projectGraphPath(project, seed.trim(), pathTo.trim(), undirected)
      setGraphPath(result)
      if (result.found && !subgraph) {
        setSubgraph(await api.projectGraphSubgraph(project, seed.trim(), depth, 800, both))
      }
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'path search failed')
    } finally {
      setBusy(false)
    }
  }

  const filtered = useMemo(() => {
    if (!subgraph || !filter.trim()) return subgraph
    const q = filter.toLowerCase()
    const keep = new Set(
      subgraph.nodes.filter((n) => n.id.toLowerCase().includes(q) || n.label.toLowerCase().includes(q)).map((n) => n.id),
    )
    return {
      ...subgraph,
      nodes: subgraph.nodes.filter((n) => keep.has(n.id)),
      edges: subgraph.edges.filter((e) => keep.has(e.source) && keep.has(e.target)),
    }
  }, [subgraph, filter])

  const selectedNode = subgraph?.nodes.find((n) => n.id === selected)

  return (
    <div className="grid gap-3.5 lg:grid-cols-[minmax(0,1fr)_minmax(240px,320px)]">
      <div>
        <Card className="mb-3">
          <div className="flex flex-wrap items-end gap-3">
            <label htmlFor="graph-seed" className="block min-w-[180px] flex-1 text-sm font-medium">
              Seed file or package
              <Input
                id="graph-seed"
                className="mt-1"
                value={seed}
                onChange={(e) => setSeed(e.target.value)}
                placeholder="internal/search/service.go — empty = hubs"
              />
            </label>
            <label htmlFor="graph-depth" className="block text-sm font-medium">
              Depth
              <Input
                id="graph-depth"
                type="number"
                min={both ? 2 : 1}
                max={5}
                className="mt-1 w-16"
                value={both && depth < 2 ? 2 : depth}
                onChange={(e) => setDepth(Number(e.target.value) || 2)}
              />
            </label>
            <Checkbox label="Include importers" checked={both} onChange={(e) => setBoth(e.target.checked)} />
            <Button disabled={busy} onClick={() => void load()}>
              {busy ? 'Loading…' : 'Load graph'}
            </Button>
          </div>
          <div className="mt-3 flex flex-wrap items-end gap-3">
            <label htmlFor="graph-filter" className="block min-w-[160px] flex-1 text-sm font-medium">
              Filter nodes
              <Input
                id="graph-filter"
                className="mt-1"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="auth, store, handler…"
              />
            </label>
            <label htmlFor="graph-path-to" className="block min-w-[160px] flex-1 text-sm font-medium">
              Trace path to
              <Input
                id="graph-path-to"
                className="mt-1"
                value={pathTo}
                onChange={(e) => setPathTo(e.target.value)}
                placeholder="pkg/client/client.go"
              />
            </label>
            <Checkbox
              label="Allow reverse hops"
              checked={undirected}
              onChange={(e) => setUndirected(e.target.checked)}
            />
            <Button variant="secondary" disabled={busy || !seed.trim() || !pathTo.trim()} onClick={() => void runPath()}>
              Trace
            </Button>
          </div>
        </Card>
        {err && <Alert kind="error">{err}</Alert>}
        {subgraph?.truncated && (
          <Alert kind="warning">Truncated by the walk budget — narrow the seed or lower the depth.</Alert>
        )}
        {graphPath && (
          <p className="my-2 text-sm">
            {graphPath.found ? (
              <>
                <Badge tone={graphPath.directed ? 'neutral' : 'warning'}>
                  {graphPath.directed ? 'directed' : 'undirected'}
                </Badge>{' '}
                {graphPath.length} hops: <Code>{(graphPath.hops || []).join(' → ')}</Code>
              </>
            ) : (
              <>
                No path from <Code>{graphPath.from}</Code> to <Code>{graphPath.to}</Code>
                {graphPath.truncated ? ' (search truncated)' : ''}
              </>
            )}
          </p>
        )}
        {filtered && (
          <GraphCanvas
            nodes={filtered.nodes}
            edges={filtered.edges}
            highlightPath={graphPath?.found ? graphPath.hops || [] : []}
            selected={selected}
            onSelect={(id) => setSelected(id)}
            onFocus={(id) => {
              setSeed(id)
              setSelected(id)
              void load(id)
            }}
          />
        )}
        {subgraph && (
          <p className="mt-2 text-xs text-muted">
            {subgraph.nodes.length} nodes · {subgraph.edges.length} edges
            {stats ? ` · project has ${stats.nodes} files in the adjacency map` : ''}
          </p>
        )}
      </div>

      <aside className="flex flex-col gap-3.5">
        <Card>
          <h2 className="mb-1 text-sm font-bold">Selected</h2>
          {selectedNode ? (
            <>
              <Code className="break-all text-xs">{selectedNode.id}</Code>
              <p className="mt-1 text-xs text-muted">
                {selectedNode.kind}
                {typeof selectedNode.degree_in === 'number' ? ` · in ${selectedNode.degree_in}` : ''}
                {typeof selectedNode.degree_out === 'number' ? ` · out ${selectedNode.degree_out}` : ''}
                {typeof selectedNode.depth === 'number' ? ` · depth ${selectedNode.depth}` : ''}
              </p>
              <div className="mt-2 flex flex-col items-start gap-1">
                {selectedNode.kind !== 'package' && (
                  <Button variant="link" size="sm" onClick={() => onOpenFile(selectedNode.id)}>
                    Open file
                  </Button>
                )}
                <Button
                  variant="link"
                  size="sm"
                  onClick={() => {
                    setSeed(selectedNode.id)
                    void load(selectedNode.id)
                  }}
                >
                  Focus neighborhood
                </Button>
                <Button
                  variant="link"
                  size="sm"
                  onClick={() => onAsk(`What does ${selectedNode.id} do, and who depends on it?`)}
                >
                  Ask about this node
                </Button>
              </div>
            </>
          ) : (
            <p className="text-sm text-muted">Click a node to inspect it.</p>
          )}
        </Card>
        {stats && (
          <Card>
            <h2 className="mb-1 text-sm font-bold">Hubs</h2>
            <p className="m-0 mb-2 text-xs text-muted">Most imported packages/files — start here on a large repo.</p>
            <ul className="m-0 list-none p-0 text-sm">
              {stats.top_depended.slice(0, 8).map((item) => (
                <li key={item.node} className="mb-1">
                  <button
                    type="button"
                    className="cursor-pointer border-0 bg-transparent p-0 text-left font-mono text-xs text-accent hover:underline"
                    onClick={() => {
                      setSeed(item.node)
                      void load(item.node)
                    }}
                  >
                    {item.node}
                  </button>{' '}
                  <Badge tone="neutral">{item.degree}</Badge>
                </li>
              ))}
            </ul>
          </Card>
        )}
        <Card>
          <h2 className="mb-1 text-sm font-bold">How to read this</h2>
          <ul className="m-0 list-disc pl-4 text-xs text-muted">
            <li>Circles are files; squares are packages.</li>
            <li>Rings are hop distance from the seed.</li>
            <li>Dashed edges were walked against the stored import direction.</li>
            <li>Include importers to see who depends on the seed, not only what it imports.</li>
          </ul>
        </Card>
      </aside>
    </div>
  )
}
