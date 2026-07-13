import { useEffect, useState } from 'react'
import { api, ApiError } from '../../api'

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
  const [graphStats, setGraphStats] = useState<{
    nodes: number
    edges: number
    top_depends: { node: string; degree: number }[]
    top_depended: { node: string; degree: number }[]
  } | null>(null)
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

  return (
    <div className="workspace-grid">
      <div className="card full">
        <h2>Dependency graph & explain</h2>
        <p className="muted">
          Graph uses <code>file_dependencies</code>. Explain uses disk when available, else index chunks.
        </p>
        {err && <div className="alert error">{err}</div>}
        <div className="row">
          <label className="grow">
            File path
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="internal/auth/token.go"
            />
          </label>
          <label>
            Line
            <input
              type="number"
              min={1}
              value={line}
              onChange={(e) => setLine(Number(e.target.value) || 1)}
              style={{ width: '5rem' }}
            />
          </label>
          <button type="button" disabled={!!busy} onClick={() => void runExplain()}>
            {busy === 'explain' ? '…' : 'Explain'}
          </button>
          <button type="button" disabled={!!busy} onClick={() => void runGraph()}>
            {busy === 'graph' ? '…' : 'Callers + deps'}
          </button>
          <button type="button" disabled={!!busy} onClick={() => void runDead()}>
            {busy === 'dead' ? '…' : 'Dead code scan'}
          </button>
          <button type="button" disabled={!!busy} onClick={() => void runSbom()}>
            {busy === 'sbom' ? '…' : 'SBOM'}
          </button>
          <button type="button" disabled={!!busy} onClick={() => void runGraphStats()}>
            {busy === 'graphstats' ? '…' : 'Graph overview'}
          </button>
        </div>
        {explain && (
          <div className="card" style={{ marginTop: '0.75rem' }}>
            <h3>
              {(explain.symbol as string) || path}
              {explain.kind ? ` — ${String(explain.kind)}` : ''}
            </h3>
            <p className="muted small">
              source: {String(explain.source || '—')} · lines{' '}
              {String(explain.start_line ?? '?')}–{String(explain.end_line ?? '?')}
            </p>
            {Array.isArray(explain.dependencies) && (
              <p>
                <strong>Dependencies:</strong>{' '}
                {(explain.dependencies as string[]).join(', ') || '(none)'}
              </p>
            )}
            {Array.isArray(explain.importers) && (
              <p>
                <strong>Imported by:</strong>{' '}
                {(explain.importers as string[]).length
                  ? (explain.importers as string[]).map((imp) => (
                      <button
                        key={imp}
                        type="button"
                        className="link"
                        style={{ marginRight: '0.5rem' }}
                        onClick={() => onOpenFile(imp)}
                      >
                        {imp}
                      </button>
                    ))
                  : '(none)'}
              </p>
            )}
            {Array.isArray(explain.tests) && (
              <p>
                <strong>Tests:</strong>{' '}
                {(explain.tests as string[]).join(', ') || '(none)'}
              </p>
            )}
            {typeof explain.snippet === 'string' && (
              <pre className="snippet">{explain.snippet as string}</pre>
            )}
          </div>
        )}
      </div>
      <div className="card">
        <h2>Callers ({callers.length})</h2>
        {callers.length === 0 ? (
          <p className="muted">No importers (or not run yet).</p>
        ) : (
          <ul>
            {callers.map((c) => (
              <li key={c}>
                <button type="button" className="link" onClick={() => onOpenFile(c)}>
                  {c}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="card">
        <h2>Dependencies ({deps.length})</h2>
        {deps.length === 0 ? (
          <p className="muted">No outbound edges (or not run yet).</p>
        ) : (
          <ul>
            {deps.map((d) => (
              <li key={d}>
                <code>{d}</code>
              </li>
            ))}
          </ul>
        )}
      </div>
      {graphStats && (
        <div className="card full">
          <h2>
            Graph overview — {graphStats.nodes} nodes · {graphStats.edges} edges
          </h2>
          <p className="muted small">
            Progressive-disclosure summary of the dependency graph (no external
            viz library, CSP-safe). Click a node to open it.
          </p>
          <div className="workspace-grid">
            <div className="card">
              <h3>Most dependencies (out-degree)</h3>
              {graphStats.top_depends.length === 0 ? (
                <p className="muted">No edges.</p>
              ) : (
                <ul>
                  {graphStats.top_depends.map((e) => (
                    <li key={e.node}>
                      <button type="button" className="link" onClick={() => onOpenFile(e.node)}>
                        {e.node}
                      </button>{' '}
                      <span className="pill">{e.degree}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div className="card">
              <h3>Most depended-upon (in-degree)</h3>
              {graphStats.top_depended.length === 0 ? (
                <p className="muted">No edges.</p>
              ) : (
                <ul>
                  {graphStats.top_depended.map((e) => (
                    <li key={e.node}>
                      <button type="button" className="link" onClick={() => onOpenFile(e.node)}>
                        {e.node}
                      </button>{' '}
                      <span className="pill">{e.degree}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}
      <div className="card full">
        <h2>CLI-only analysis tools</h2>
        <p className="muted">
          These run on your machine against the same index (or a server checkout). Use the
          project name shown in the workspace header.
        </p>
        <ul>
          <li>
            <code>semidx sbom generate --project {project}</code> — dependency SBOM (also available
            via the SBOM button above)
          </li>
          <li>
            <code>semidx diff --project {project}</code> — compare index vs working tree
          </li>
          <li>
            <code>semidx alerts list --project {project}</code> — saved search alerts (local JSON)
          </li>
          <li>
            <code>semidx insights show</code> — query trend charts (local JSON)
          </li>
        </ul>
        {sbom && (
          <p>
            Last SBOM: <span className="pill">{sbom.format}</span>{' '}
            <strong>{sbom.component_count}</strong> components — CLI:{' '}
            <code>{sbom.cli_equivalent}</code>
          </p>
        )}
      </div>
      <div className="card full">
        <h2>
          Dead code
          {deadStats
            ? ` — ${deadStats.total} findings (${deadStats.confirmed} confirmed, ${deadStats.public_api} public-api)`
            : ''}
        </h2>
        <p className="muted">
          Requires project path on the server disk (git checkout / docs path). Same as{' '}
          <code>semidx dead-code</code>.
        </p>
        {dead.length === 0 ? (
          <p className="muted">No findings yet — run scan.</p>
        ) : (
          <table>
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
                    <code>{f.symbol}</code>
                  </td>
                  <td>{f.kind}</td>
                  <td>
                    <button
                      type="button"
                      className="link"
                      onClick={() => onOpenFile(f.file, f.start_line)}
                    >
                      {f.file}:{f.start_line}
                    </button>
                  </td>
                  <td>
                    <span className="pill">{f.confidence}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
