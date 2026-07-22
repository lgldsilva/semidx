import { useEffect, useState } from 'react'
import { api, ApiError, type Dependency, type DependencyUsage, type RuntimeEdge } from '../../api'
import { Alert } from '../../components/Alert'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Input } from '../../components/Input'
import { Code, Snippet } from '../../components/Snippet'
import { Table } from '../../components/Table'

const H2 = 'mb-2 text-[1.1rem] font-bold'
const LIST = 'my-2 list-disc pl-5'

export function AnalyzePanel({
  project,
  seedPath,
  onOpenFile,
}: {
  project: string
  seedPath: string
  onOpenFile: (path: string, line?: number) => void
}) {
  const [path, setPath] = useState(seedPath)
  const [line, setLine] = useState(1)
  const [callers, setCallers] = useState<string[]>([])
  const [deps, setDeps] = useState<string[]>([])
  const [explain, setExplain] = useState<Record<string, unknown> | null>(null)
  const [dead, setDead] = useState<
    { symbol: string; kind: string; file: string; start_line: number; confidence: string }[]
  >([])
  const [deadStats, setDeadStats] = useState<{ total: number; confirmed: number; public_api: number } | null>(null)
  const [sbom, setSbom] = useState<{ format: string; component_count: number; cli_equivalent: string } | null>(null)
  const [catalog, setCatalog] = useState<Dependency[]>([])
  const [sharedCatalog, setSharedCatalog] = useState<DependencyUsage[]>([])
  const [catalogLoaded, setCatalogLoaded] = useState(false)
  const [graphStats, setGraphStats] = useState<{
    nodes: number
    edges: number
    top_depends: { node: string; degree: number }[]
    top_depended: { node: string; degree: number }[]
  } | null>(null)
  const [runtimeEdges, setRuntimeEdges] = useState<RuntimeEdge[]>([])
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')

  useEffect(() => {
    setPath(seedPath)
  }, [seedPath])

  async function runGraph() {
    if (!path.trim()) return
    setBusy('graph')
    setErr('')
    try {
      const [c, d] = await Promise.all([
        api.projectCallers(project, path),
        api.projectDeps(project, path),
      ])
      setCallers(c.callers || [])
      setDeps(d.dependencies || [])
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'graph failed')
    } finally {
      setBusy('')
    }
  }

  async function runExplain() {
    if (!path.trim()) return
    setBusy('explain')
    setErr('')
    try {
      setExplain(await api.projectExplain(project, path, line))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'explain failed')
    } finally {
      setBusy('')
    }
  }

  async function runGraphStats() {
    setBusy('graphstats')
    setErr('')
    try {
      setGraphStats(await api.projectGraphStats(project))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'graph stats failed')
    } finally {
      setBusy('')
    }
  }

  async function runDead() {
    setBusy('dead')
    setErr('')
    try {
      const r = await api.projectDeadCode(project, 150)
      setDead(r.findings || [])
      setDeadStats(r.stats)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'dead-code failed')
    } finally {
      setBusy('')
    }
  }

  async function runSbom() {
    setBusy('sbom')
    setErr('')
    try {
      const r = await api.projectSbom(project)
      setSbom({
        format: r.format,
        component_count: r.component_count,
        cli_equivalent: r.cli_equivalent,
      })
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'SBOM failed')
    } finally {
      setBusy('')
    }
  }

  async function runDependencies() {
    setBusy('dependencies')
    setErr('')
    try {
      const [own, shared] = await Promise.all([
        api.projectDependencies(project),
        api.projectSharedDependencies(project),
      ])
      setCatalog(own.dependencies || [])
      setSharedCatalog(shared.dependencies || [])
      setCatalogLoaded(true)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'dependency catalog failed')
    } finally {
      setBusy('')
    }
  }

  async function runRuntimeGraph() {
    setBusy('runtime')
    setErr('')
    try {
      const result = await api.projectRuntimeEdges(project)
      setRuntimeEdges(result.edges || [])
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'runtime graph failed')
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="grid gap-3.5 md:grid-cols-2">
      <Card className="md:col-span-2">
        <h2 className={H2}>Dependency graph &amp; explain</h2>
        <p className="m-0 text-muted">
          Graph uses <Code>file_dependencies</Code>. Explain uses disk when available, else index chunks.
        </p>
        {err && <Alert kind="error">{err}</Alert>}
        <div className="mt-2 flex flex-wrap items-end gap-3.5">
          <label htmlFor="analyze-path" className="block min-w-[180px] flex-1 text-sm font-medium">
            File path
            <Input
              id="analyze-path"
              className="mt-1"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="internal/auth/token.go"
            />
          </label>
          <label htmlFor="analyze-line" className="block text-sm font-medium">
            Line
            <Input
              id="analyze-line"
              type="number"
              min={1}
              className="mt-1 w-20"
              value={line}
              onChange={(e) => setLine(Number(e.target.value) || 1)}
            />
          </label>
          <Button disabled={!!busy} onClick={() => void runExplain()}>
            {busy === 'explain' ? '…' : 'Explain'}
          </Button>
          <Button disabled={!!busy} onClick={() => void runGraph()}>
            {busy === 'graph' ? '…' : 'Callers + deps'}
          </Button>
          <Button disabled={!!busy} onClick={() => void runDead()}>
            {busy === 'dead' ? '…' : 'Dead code scan'}
          </Button>
          <Button disabled={!!busy} onClick={() => void runSbom()}>
            {busy === 'sbom' ? '…' : 'SBOM'}
          </Button>
          <Button disabled={!!busy} onClick={() => void runDependencies()}>
            {busy === 'dependencies' ? '…' : 'Dependencies'}
          </Button>
          <Button disabled={!!busy} onClick={() => void runGraphStats()}>
            {busy === 'graphstats' ? '…' : 'Graph overview'}
          </Button>
          <Button disabled={!!busy} onClick={() => void runRuntimeGraph()}>
            {busy === 'runtime' ? '…' : 'Runtime calls'}
          </Button>
        </div>
        {explain && (
          <Card className="mt-3">
            <h3 className="mb-1 font-bold">
              {(explain.symbol as string) || path}
              {explain.kind ? ` — ${String(explain.kind)}` : ''}
            </h3>
            <p className="m-0 text-xs text-muted">
              source: {String(explain.source || '—')} · lines{' '}
              {String(explain.start_line ?? '?')}–{String(explain.end_line ?? '?')}
            </p>
            {Array.isArray(explain.dependencies) && (
              <p className="my-1">
                <strong>Dependencies:</strong>{' '}
                {(explain.dependencies as string[]).join(', ') || '(none)'}
              </p>
            )}
            {Array.isArray(explain.importers) && (
              <p className="my-1">
                <strong>Imported by:</strong>{' '}
                {(explain.importers as string[]).length
                  ? (explain.importers as string[]).map((imp) => (
                      <Button
                        key={imp}
                        variant="link"
                        size="sm"
                        className="mr-2"
                        onClick={() => onOpenFile(imp)}
                      >
                        {imp}
                      </Button>
                    ))
                  : '(none)'}
              </p>
            )}
            {Array.isArray(explain.tests) && (
              <p className="my-1">
                <strong>Tests:</strong>{' '}
                {(explain.tests as string[]).join(', ') || '(none)'}
              </p>
            )}
            {typeof explain.snippet === 'string' && (
              <Snippet>{explain.snippet as string}</Snippet>
            )}
          </Card>
        )}
      </Card>
      <Card>
        <h2 className={H2}>Callers ({callers.length})</h2>
        {callers.length === 0 ? (
          <p className="text-muted">No importers (or not run yet).</p>
        ) : (
          <ul className={LIST}>
            {callers.map((c) => (
              <li key={c}>
                <Button variant="link" size="sm" onClick={() => onOpenFile(c)}>
                  {c}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Card>
        <h2 className={H2}>Dependencies ({deps.length})</h2>
        {deps.length === 0 ? (
          <p className="text-muted">No outbound edges (or not run yet).</p>
        ) : (
          <ul className={LIST}>
            {deps.map((d) => (
              <li key={d}>
                <Code>{d}</Code>
              </li>
            ))}
          </ul>
        )}
      </Card>
      {graphStats && (
        <Card className="md:col-span-2">
          <h2 className={H2}>
            Graph overview — {graphStats.nodes} nodes · {graphStats.edges} edges
          </h2>
          <p className="m-0 text-xs text-muted">
            Progressive-disclosure summary of the dependency graph (no external
            viz library, CSP-safe). Click a node to open it.
          </p>
          <div className="mt-3 grid gap-3.5 md:grid-cols-2">
            <Card>
              <h3 className="mb-1 font-bold">Most dependencies (out-degree)</h3>
              {graphStats.top_depends.length === 0 ? (
                <p className="text-muted">No edges.</p>
              ) : (
                <ul className={LIST}>
                  {graphStats.top_depends.map((e) => (
                    <li key={e.node}>
                      <Button variant="link" size="sm" onClick={() => onOpenFile(e.node)}>
                        {e.node}
                      </Button>{' '}
                      <Badge tone="neutral">{e.degree}</Badge>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
            <Card>
              <h3 className="mb-1 font-bold">Most depended-upon (in-degree)</h3>
              {graphStats.top_depended.length === 0 ? (
                <p className="text-muted">No edges.</p>
              ) : (
                <ul className={LIST}>
                  {graphStats.top_depended.map((e) => (
                    <li key={e.node}>
                      <Button variant="link" size="sm" onClick={() => onOpenFile(e.node)}>
                        {e.node}
                      </Button>{' '}
                      <Badge tone="neutral">{e.degree}</Badge>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>
        </Card>
      )}

      {runtimeEdges.length > 0 && (
        <Card className="md:col-span-2">
          <h2 className={H2}>Observed runtime communication</h2>
          <p className="m-0 text-muted">
            Evidence submitted by an agent or telemetry adapter. It is kept separate from static imports.
          </p>
          <Table>
            <thead>
              <tr>
                <th>Target</th>
                <th>Protocol</th>
                <th>Environment</th>
                <th>Requests</th>
                <th>Errors</th>
                <th>p95</th>
              </tr>
            </thead>
            <tbody>
              {runtimeEdges.map((edge, index) => (
                <tr key={`${edge.target_project}-${edge.protocol}-${index}`}>
                  <td>{edge.target_project}</td>
                  <td>{edge.protocol || '—'}</td>
                  <td>{edge.environment || '—'}</td>
                  <td>{edge.request_count}</td>
                  <td>{edge.error_count}</td>
                  <td>{edge.p95_latency_ms.toFixed(1)}ms</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      {catalogLoaded && (
        <Card className="md:col-span-2">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2 className={H2}>Dependency catalog — {catalog.length} declarations</h2>
            <span className="text-xs text-muted">manifest + resolved metadata</span>
          </div>
          {catalog.length > 0 && <Table className="mt-2">
            <thead>
              <tr>
                <th>Package</th>
                <th>Ecosystem</th>
                <th>Declared</th>
                <th>Resolved</th>
                <th>Scope</th>
              </tr>
            </thead>
            <tbody>
              {catalog.map((dep) => (
                <tr key={`${dep.ecosystem}:${dep.normalized_name}:${dep.scope}`}>
                  <td><Code>{dep.name}</Code></td>
                  <td><Badge tone="neutral">{dep.ecosystem}</Badge></td>
                  <td>{dep.constraint || '—'}</td>
                  <td>{dep.resolved_version || 'not resolved'}</td>
                  <td>{dep.scope || '—'}</td>
                </tr>
              ))}
            </tbody>
          </Table>}
          <h3 className="mt-4 mb-1 font-bold">Also used by other projects</h3>
          {sharedCatalog.length === 0 ? (
            <p className="text-muted">No shared packages found in this workspace.</p>
          ) : (
            <ul className={LIST}>
              {sharedCatalog.map((dep, i) => (
                <li key={`${dep.project_id}:${dep.ecosystem}:${dep.normalized_name}:${i}`}>
                  <Code>{dep.name}</Code> · {dep.ecosystem} · {dep.project_name}
                  {dep.resolved_version ? ` · ${dep.resolved_version}` : ''}
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}
      <Card className="md:col-span-2">
        <h2 className={H2}>CLI-only analysis tools</h2>
        <p className="m-0 text-muted">
          These run on your machine against the same index (or a server checkout). Use the
          project name shown in the workspace header.
        </p>
        <ul className={LIST}>
          <li>
            <Code>semidx sbom generate --project {project}</Code> — dependency SBOM (also available
            via the SBOM button above)
          </li>
          <li>
            <Code>semidx diff --project {project}</Code> — compare index vs working tree
          </li>
          <li>
            <Code>semidx alerts list --project {project}</Code> — saved search alerts (local JSON)
          </li>
          <li>
            <Code>semidx insights show</Code> — query trend charts (local JSON)
          </li>
        </ul>
        {sbom && (
          <p className="my-1">
            Last SBOM: <Badge tone="neutral">{sbom.format}</Badge>{' '}
            <strong>{sbom.component_count}</strong> components — CLI:{' '}
            <Code>{sbom.cli_equivalent}</Code>
          </p>
        )}
      </Card>
      <Card className="md:col-span-2">
        <h2 className={H2}>
          Dead code
          {deadStats
            ? ` — ${deadStats.total} findings (${deadStats.confirmed} confirmed, ${deadStats.public_api} public-api)`
            : ''}
        </h2>
        <p className="m-0 text-muted">
          Requires project path on the server disk (git checkout / docs path). Same as{' '}
          <Code>semidx dead-code</Code>.
        </p>
        {dead.length === 0 ? (
          <p className="text-muted">No findings yet — run scan.</p>
        ) : (
          <Table className="mt-2">
            <thead>
              <tr>
                <th>Symbol</th>
                <th>Kind</th>
                <th>File</th>
                <th>Confidence</th>
              </tr>
            </thead>
            <tbody>
              {dead.map((f, i) => (
                <tr key={i}>
                  <td>
                    <Code>{f.symbol}</Code>
                  </td>
                  <td>{f.kind}</td>
                  <td>
                    <Button
                      variant="link"
                      size="sm"
                      onClick={() => onOpenFile(f.file, f.start_line)}
                    >
                      {f.file}:{f.start_line}
                    </Button>
                  </td>
                  <td>
                    <Badge tone="neutral">{f.confidence}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  )
}
