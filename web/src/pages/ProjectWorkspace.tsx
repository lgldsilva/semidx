import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import {
  api,
  ApiError,
  type ChatMessage,
  type FileEntry,
  type Job,
  type Project,
  type ProjectDetail,
  type SearchHit,
} from '../api'
import { buildFileTree, type TreeNode } from '../features/project/buildFileTree'

type Tab = 'overview' | 'files' | 'explore' | 'analyze' | 'chat' | 'ingest'

export function ProjectWorkspace() {
  const { name = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const tab = (params.get('tab') as Tab) || 'overview'
  const filePath = params.get('path') || ''
  const seedQ = params.get('q') || ''

  const [detail, setDetail] = useState<ProjectDetail | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)
  const [jobPoll, setJobPoll] = useState<Job | null>(null)

  const setTab = (t: Tab) => {
    const next = new URLSearchParams(params)
    next.set('tab', t)
    setParams(next, { replace: true })
  }

  const openFile = (path: string, line?: number) => {
    const next = new URLSearchParams(params)
    next.set('tab', 'files')
    next.set('path', path)
    if (line) next.set('line', String(line))
    setParams(next)
  }

  const reload = useCallback(async () => {
    if (!name) return
    setLoading(true)
    setErr('')
    try {
      setDetail(await api.projectDetail(name))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'failed to load project')
    } finally {
      setLoading(false)
    }
  }, [name])

  useEffect(() => {
    void reload()
  }, [reload])

  useEffect(() => {
    if (!jobPoll || jobPoll.status === 'succeeded' || jobPoll.status === 'failed') return
    const t = setInterval(() => {
      void api
        .job(name, jobPoll.id)
        .then((j) => {
        setJobPoll(j)
        if (j.status === 'succeeded' || j.status === 'failed') void reload()
      })
    }, 1500)
    return () => clearInterval(t)
  }, [jobPoll, name, reload])

  async function onReindex() {
    try {
      const { job_id } = await api.reindex(name)
      setJobPoll({ id: job_id, type: 'full', status: 'queued' })
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'reindex failed')
    }
  }

  async function onDelete() {
    if (!confirm(`Delete project “${name}”?`)) return
    try {
      await api.deleteProject(name)
      navigate('/')
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'delete failed')
    }
  }

  if (loading && !detail) {
    return <p className="muted">Loading project…</p>
  }
  if (err && !detail) {
    return (
      <div>
        <div className="alert error">{err}</div>
        <Link to="/">← Projects</Link>
      </div>
    )
  }
  if (!detail) return null
  const p = detail.project

  return (
    <div className="workspace">
      <div className="workspace-head">
        <div>
          <Link to="/" className="muted small">
            ← Projects
          </Link>
          <h1>{p.name}</h1>
          <p className="muted">
            <span className="pill">{p.status}</span>
            {p.source_type && <span className="pill">{p.source_type}</span>}
            {p.model && <span className="muted"> · {p.model}</span>}
            {typeof p.total_files === 'number' && (
              <span className="muted"> · {p.total_files} files</span>
            )}
            {typeof p.total_chunks === 'number' && (
              <span className="muted"> · {p.total_chunks} chunks</span>
            )}
          </p>
        </div>
        <div className="row-actions">
          <button type="button" onClick={() => void onReindex()}>
            Reindex
          </button>
          <button type="button" className="link danger-text" onClick={() => void onDelete()}>
            Delete
          </button>
        </div>
      </div>

      {err && <div className="alert error">{err}</div>}
      {jobPoll && (
        <div className="alert ok">
          Job #{jobPoll.id}: <strong>{jobPoll.status}</strong>
          {jobPoll.progress_percent != null && jobPoll.status === 'running' && (
            <span> · {jobPoll.progress_percent}%</span>
          )}
          {jobPoll.files_indexed != null &&
            ` · files ${jobPoll.files_indexed}${jobPoll.progress_total ? `/${jobPoll.progress_total}` : ''} · chunks ${jobPoll.chunks_created ?? 0}`}
          {jobPoll.error && ` · ${jobPoll.error}`}
        </div>
      )}

      <div className="tab-nav" role="tablist" aria-label="Project sections">
        {(['overview', 'files', 'ingest', 'explore', 'analyze', 'chat'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            className={`tab-btn ${tab === t ? 'active' : ''}`}
            onClick={() => setTab(t)}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'overview' && (
        <OverviewPanel
          project={p}
          jobs={detail.jobs}
          onExplore={() => setTab('explore')}
          onChat={() => setTab('chat')}
          onFiles={() => setTab('files')}
        />
      )}
      {tab === 'files' && (
        <FilesPanel
          project={name}
          initialPath={filePath}
          line={Number(params.get('line') || 0) || undefined}
          onExplorePath={(path) => {
            const next = new URLSearchParams(params)
            next.set('tab', 'explore')
            next.set('q', path)
            setParams(next)
          }}
        />
      )}
      {tab === 'ingest' && <IngestPanel project={name} onDone={() => void reload()} />}
      {tab === 'explore' && (
        <ExplorePanel
          project={name}
          seedQuery={seedQ}
          onOpenFile={openFile}
          onAsk={(q) => {
            const next = new URLSearchParams(params)
            next.set('tab', 'chat')
            next.set('q', q)
            setParams(next)
          }}
        />
      )}
      {tab === 'analyze' && (
        <AnalyzePanel project={name} seedPath={filePath} onOpenFile={openFile} />
      )}
      {tab === 'chat' && (
        <ChatPanel
          project={name}
          seedQuestion={seedQ}
          onOpenFile={openFile}
        />
      )}
    </div>
  )
}

function OverviewPanel({
  project: p,
  jobs,
  onExplore,
  onChat,
  onFiles,
}: {
  project: Project
  jobs: Job[]
  onExplore: () => void
  onChat: () => void
  onFiles: () => void
}) {
  const exts = useMemo(() => {
    const b = p.ext_breakdown || {}
    return Object.entries(b).sort((a, b) => b[1] - a[1]).slice(0, 12)
  }, [p.ext_breakdown])

  return (
    <div className="workspace-grid">
      <div className="card">
        <h2>Metadata</h2>
        <dl className="meta-grid">
          <dt>Identity</dt>
          <dd>
            <code>{p.identity || '—'}</code>
          </dd>
          <dt>Path</dt>
          <dd>
            <code>{p.path || '—'}</code>
          </dd>
          <dt>Git</dt>
          <dd>
            {p.git_url || '—'}
            {p.branch ? ` @ ${p.branch}` : ''}
          </dd>
          <dt>Model / dims</dt>
          <dd>
            {p.model || '—'}
            {p.dims ? ` · ${p.dims}d` : ''}
          </dd>
          <dt>License</dt>
          <dd>{p.license || '—'}</dd>
          <dt>Last commit</dt>
          <dd>
            <code>{p.last_commit ? p.last_commit.slice(0, 12) : '—'}</code>
          </dd>
          <dt>Files / chunks</dt>
          <dd>
            {p.total_files ?? '—'} / {p.total_chunks ?? '—'}
          </dd>
          <dt>Last job</dt>
          <dd>
            {p.last_job
              ? `#${p.last_job.id} ${p.last_job.status} (${p.last_job.type})`
              : '—'}
          </dd>
        </dl>
        <div className="row-actions" style={{ marginTop: '1rem' }}>
          <button type="button" onClick={onFiles}>
            Browse files
          </button>
          <button type="button" onClick={onExplore}>
            Explore / search
          </button>
          <button type="button" onClick={onChat}>
            Chat about project
          </button>
        </div>
      </div>

      <div className="card">
        <h2>Language / extension mix</h2>
        {exts.length === 0 ? (
          <p className="muted">No file stats yet.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Ext</th>
                <th>Files</th>
              </tr>
            </thead>
            <tbody>
              {exts.map(([ext, n]) => (
                <tr key={ext}>
                  <td>
                    <code>.{ext}</code>
                  </td>
                  <td>{n}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card full">
        <h2>Recent jobs</h2>
        {jobs.length === 0 ? (
          <p className="muted">No jobs recorded.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Type</th>
                <th>Status</th>
                <th>Files</th>
                <th>Chunks</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((j) => (
                <tr key={j.id}>
                  <td>{j.id}</td>
                  <td>{j.type}</td>
                  <td>
                    <span className="pill">{j.status}</span>
                  </td>
                  <td>{j.files_indexed ?? '—'}</td>
                  <td>{j.chunks_created ?? '—'}</td>
                  <td className="muted small">{j.error || ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card full">
        <h2>CLI</h2>
        <pre className="snippet">{`semidx search --project ${p.name} --query "…"
semidx status --project ${p.name}
semidx push --project .   # if this is a push project
semidx --local search --query "…"  # ignore server login`}</pre>
      </div>
    </div>
  )
}

function FilesPanel({
  project,
  initialPath,
  line,
  onExplorePath,
}: {
  project: string
  initialPath: string
  line?: number
  onExplorePath: (path: string) => void
}) {
  const [files, setFiles] = useState<FileEntry[]>([])
  const [total, setTotal] = useState(0)
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState(initialPath)
  const [content, setContent] = useState('')
  const [chunks, setChunks] = useState<
    { start_line: number; end_line: number; content: string }[]
  >([])
  const [callers, setCallers] = useState<string[]>([])
  const [deps, setDeps] = useState<string[]>([])
  const [graphErr, setGraphErr] = useState('')
  const [truncated, setTruncated] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    void api
      .projectFiles(project, { q: filter || undefined, limit: 3000 })
      .then((r) => {
        setFiles(r.files)
        setTotal(r.total)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : 'load files failed'))
      .finally(() => setLoading(false))
  }, [project, filter])

  useEffect(() => {
    if (!selected) {
      setContent('')
      setChunks([])
      return
    }
    setErr('')
    void api
      .projectFileContent(project, selected)
      .then((r) => {
        setContent(r.content)
        setChunks(r.chunks || [])
        setTruncated(r.truncated)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : 'load content failed'))
  }, [project, selected])

  useEffect(() => {
    if (!selected) {
      setCallers([])
      setDeps([])
      return
    }
    setGraphErr('')
    void Promise.all([
      api.projectCallers(project, selected),
      api.projectDeps(project, selected),
    ])
      .then(([c, d]) => {
        setCallers(c.callers || [])
        setDeps(d.dependencies || [])
      })
      .catch((e) =>
        setGraphErr(e instanceof ApiError ? e.message : 'graph lookup failed'),
      )
  }, [project, selected])

  useEffect(() => {
    if (initialPath) setSelected(initialPath)
  }, [initialPath])

  useEffect(() => {
    if (!line || !content) return
    const el = document.getElementById(`L${line}`)
    el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [line, content, selected])

  const tree = useMemo(
    () => buildFileTree(files.map((f) => f.path)),
    [files],
  )

  const numbered = useMemo(() => {
    if (!content) return [] as { n: number; text: string; hi: boolean }[]
    // Reconstruct approximate line numbers from chunk metadata when available.
    const lines = content.split('\n')
    const out: { n: number; text: string; hi: boolean }[] = []
    if (chunks.length > 0) {
      let i = 0
      for (const ch of chunks) {
        const part = ch.content.split('\n')
        for (let j = 0; j < part.length; j++) {
          const n = ch.start_line + j
          out.push({ n, text: part[j], hi: !!line && n === line })
          i++
        }
      }
      // if rebuild shorter than raw content lines, fall back
      if (out.length >= lines.length * 0.5) return out
    }
    return lines.map((text, idx) => ({
      n: idx + 1,
      text,
      hi: !!line && idx + 1 === line,
    }))
  }, [content, chunks, line])

  return (
    <div className="files-layout">
      <aside className="files-tree card">
        <input
          placeholder="Filter paths…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <p className="muted small">
          {loading ? 'Loading…' : `${total} files`}
        </p>
        <div className="tree-scroll">
          <TreeView
            nodes={tree}
            selected={selected}
            onSelect={setSelected}
          />
        </div>
      </aside>
      <section className="files-viewer card">
        {err && <div className="alert error">{err}</div>}
        {!selected ? (
          <p className="muted">Select a file from the tree (content from the index).</p>
        ) : (
          <>
            <div className="result-header">
              <code className="result-path">{selected}</code>
              <div className="row-actions">
                <button
                  type="button"
                  className="link"
                  onClick={() => onExplorePath(selected)}
                >
                  Find related
                </button>
              </div>
            </div>
            {truncated && (
              <div className="alert error">Showing first chunks only (truncated).</div>
            )}
            {chunks.length > 0 && (
              <details className="file-detail-chunks" open>
                <summary>Chunks ({chunks.length})</summary>
                <ul className="chunk-list">
                  {chunks.map((c) => (
                    <li key={`${c.start_line}-${c.end_line}`}>
                      <button
                        type="button"
                        className="link"
                        onClick={() => {
                          const el = document.getElementById(`L${c.start_line}`)
                          el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
                        }}
                      >
                        L{c.start_line}–{c.end_line}
                      </button>
                      <span className="muted small">
                        {c.content.slice(0, 80)}
                        {c.content.length > 80 ? '…' : ''}
                      </span>
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {(callers.length > 0 || deps.length > 0 || graphErr) && (
              <div className="file-graph-stats">
                {graphErr && <p className="muted small">{graphErr}</p>}
                {deps.length > 0 && (
                  <details open>
                    <summary>Fan-out ({deps.length})</summary>
                    <ul>
                      {deps.map((d) => (
                        <li key={d}>
                          <button type="button" className="link" onClick={() => setSelected(d)}>
                            {d}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
                {callers.length > 0 && (
                  <details open>
                    <summary>Fan-in ({callers.length})</summary>
                    <ul>
                      {callers.map((c) => (
                        <li key={c}>
                          <button type="button" className="link" onClick={() => setSelected(c)}>
                            {c}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </div>
            )}
            <pre className="snippet file-body">
              {numbered.length === 0
                ? '(empty or not in index)'
                : numbered.map((row) => (
                    <div
                      key={row.n + row.text.slice(0, 8)}
                      id={`L${row.n}`}
                      className={row.hi ? 'line-hi' : undefined}
                    >
                      <span className="ln">{row.n}</span>
                      {row.text}
                    </div>
                  ))}
            </pre>
          </>
        )}
      </section>
    </div>
  )
}

function IngestPanel({
  project,
  onDone,
}: {
  project: string
  onDone: () => void
}) {
  const [status, setStatus] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const maxFileBytes = 512 * 1024
  const batchSize = 50

  async function ingestFileList(list: FileList) {
    setBusy(true)
    setErr('')
    const all: { path: string; content: string }[] = []
    for (let i = 0; i < list.length; i++) {
      const f = list[i]
      const path =
        (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name
      if (f.size > maxFileBytes) {
        setErr((e) => `${e}\n${path}: skipped (>512KiB)`.trim())
        continue
      }
      const text = await f.text()
      all.push({ path: path.replace(/^\/+/, ''), content: text })
    }
    let indexed = 0
    let chunks = 0
    let errors = 0
    for (let i = 0; i < all.length; i += batchSize) {
      const batch = all.slice(i, i + batchSize)
      setStatus(`Indexing batch ${Math.floor(i / batchSize) + 1} (${batch.length} files)…`)
      const res = await api.projectIngest(project, batch)
      indexed += res.indexed
      chunks += res.chunks
      errors += res.errors
      if (res.file_errors?.length) {
        setErr(
          (prev) =>
            `${prev}\n${res.file_errors!
              .slice(0, 3)
              .map((x) => `${x.path}: ${x.error}`)
              .join('\n')}`.trim(),
        )
      }
    }
    setStatus(`Done — indexed ${indexed}, chunks ${chunks}, errors ${errors}`)
    onDone()
  }

  async function onPick(e: { target: HTMLInputElement }) {
    const list = e.target.files
    if (!list || list.length === 0) return
    setStatus(`Reading ${list.length} file(s)…`)
    try {
      await ingestFileList(list)
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'ingest failed')
    } finally {
      setBusy(false)
      e.target.value = ''
    }
  }

  async function onPickArchive(e: { target: HTMLInputElement }) {
    const list = e.target.files
    if (!list || list.length === 0) return
    const archive = list[0]
    setBusy(true)
    setErr('')
    setStatus(`Uploading archive ${archive.name}…`)
    try {
      const res = await api.projectIngestArchive(project, archive)
      setStatus(`Done — indexed ${res.indexed}, chunks ${res.chunks}, errors ${res.errors}`)
      if (res.file_errors?.length) {
        setErr(
          res.file_errors
            .slice(0, 10)
            .map((x) => `${x.path}: ${x.error}`)
            .join('\n'),
        )
      }
      onDone()
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'archive ingest failed')
    } finally {
      setBusy(false)
      e.target.value = ''
    }
  }

  return (
    <div className="card">
      <h2>Ingest files</h2>
      <p className="muted">
        Upload text files, a whole folder, or a .zip archive (each file ≤512KiB). Large corpora:{' '}
        <code>semidx push --project . --name {project}</code>
      </p>
      {status && <div className="alert ok">{status}</div>}
      {err && (
        <pre className="alert error" style={{ whiteSpace: 'pre-wrap' }}>
          {err}
        </pre>
      )}
      <div className="row-actions">
        <label>
          Files
          <input type="file" multiple disabled={busy} onChange={(e) => void onPick(e)} />
        </label>
        <label>
          Folder
          <input
            type="file"
            // @ts-expect-error webkitdirectory is non-standard but widely supported
            webkitdirectory=""
            directory=""
            multiple
            disabled={busy}
            onChange={(e) => void onPick(e)}
          />
        </label>
        <label>
          .zip
          <input
            type="file"
            accept=".zip,application/zip"
            disabled={busy}
            onChange={(e) => void onPickArchive(e)}
          />
        </label>
      </div>
    </div>
  )
}

function TreeView({
  nodes,
  selected,
  onSelect,
  depth = 0,
}: {
  nodes: TreeNode[]
  selected: string
  onSelect: (path: string) => void
  depth?: number
}) {
  return (
    <ul className="tree" style={{ paddingLeft: depth ? 12 : 0 }}>
      {nodes.map((n) => (
        <li key={n.path}>
          {n.isFile ? (
            <button
              type="button"
              className={`tree-file ${selected === n.path ? 'active' : ''}`}
              onClick={() => onSelect(n.path)}
            >
              {n.name}
            </button>
          ) : (
            <details open={depth < 1}>
              <summary className="tree-dir">{n.name}/</summary>
              {n.children && (
                <TreeView
                  nodes={n.children}
                  selected={selected}
                  onSelect={onSelect}
                  depth={depth + 1}
                />
              )}
            </details>
          )}
        </li>
      ))}
    </ul>
  )
}

function AnalyzePanel({
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

function ExplorePanel({
  project,
  seedQuery,
  onOpenFile,
  onAsk,
}: {
  project: string
  seedQuery: string
  onOpenFile: (path: string, line?: number) => void
  onAsk: (q: string) => void
}) {
  const [query, setQuery] = useState(seedQuery)
  const [top, setTop] = useState(15)
  const [graph, setGraph] = useState(false)
  const [graphDepth, setGraphDepth] = useState(2)
  const [results, setResults] = useState<SearchHit[]>([])
  const [fallback, setFallback] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setQuery(seedQuery)
  }, [seedQuery])

  async function run(e?: { preventDefault(): void }) {
    e?.preventDefault()
    if (!query.trim()) return
    setBusy(true)
    setErr('')
    try {
      const res = await api.search({
        query,
        project,
        top,
        graph,
        graph_depth: graphDepth,
      })
      setResults(res.results ?? [])
      setFallback(!!res.fallback)
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'search failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <form className="search-form" onSubmit={(e) => void run(e)}>
        <div className="row">
          <label className="grow">
            Query in <strong>{project}</strong>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="how is auth validated?"
            />
          </label>
          <button type="submit" disabled={busy}>
            {busy ? 'Searching…' : 'Search'}
          </button>
        </div>
        <div className="top-selector">
          <span className="muted">top</span>
          {[5, 10, 15, 30].map((n) => (
            <button
              key={n}
              type="button"
              className={`pill-btn ${top === n ? 'active' : ''}`}
              onClick={() => setTop(n)}
            >
              {n}
            </button>
          ))}
          <label className="checkbox" style={{ marginLeft: '0.75rem' }}>
            <input
              type="checkbox"
              checked={graph}
              onChange={(e) => setGraph(e.target.checked)}
            />
            Graph-RAG
          </label>
          {graph && (
            <label className="muted small">
              depth
              <input
                type="number"
                min={1}
                max={5}
                value={graphDepth}
                onChange={(e) => setGraphDepth(Number(e.target.value) || 2)}
                style={{ width: '3.5rem', marginLeft: '0.35rem' }}
              />
            </label>
          )}
        </div>
      </form>
      {err && <div className="alert error">{err}</div>}
      {fallback && (
        <div className="alert error">Keyword fallback — embeddings unavailable.</div>
      )}
      {results.map((hit, i) => (
        <article key={`${hit.path}-${i}`} className="card result-card">
          <div className="result-header">
            <button
              type="button"
              className="link result-path"
              onClick={() => onOpenFile(hit.path, hit.start_line)}
            >
              {hit.path}:{hit.start_line}
              {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
            </button>
            <span className="score-badge">{Math.round(hit.score * 100)}%</span>
          </div>
          <pre className="snippet">{hit.content}</pre>
          <div className="row-actions">
            <button type="button" className="link" onClick={() => onOpenFile(hit.path, hit.start_line)}>
              Open file
            </button>
            <button
              type="button"
              className="link"
              onClick={() =>
                onAsk(
                  `Explain this code in ${hit.path}:${hit.start_line} and how it fits the project:\n${hit.content.slice(0, 500)}`,
                )
              }
            >
              Ask about this
            </button>
          </div>
        </article>
      ))}
    </div>
  )
}

function ChatPanel({
  project,
  seedQuestion,
  onOpenFile,
}: {
  project: string
  seedQuestion: string
  onOpenFile: (path: string, line?: number) => void
}) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState(seedQuestion)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [chatOk, setChatOk] = useState<boolean | null>(null)

  useEffect(() => {
    void api.system().then((s) => setChatOk(!!s.chat_enabled)).catch(() => setChatOk(false))
  }, [])

  useEffect(() => {
    setInput(seedQuestion)
  }, [seedQuestion])

  async function send(e?: { preventDefault(): void }) {
    e?.preventDefault()
    const q = input.trim()
    if (!q || busy) return
    setBusy(true)
    setErr('')
    const history = messages.map((m) => ({ role: m.role, content: m.content }))
    setMessages((m) => [...m, { role: 'user', content: q }])
    setInput('')
    let assistant = ''
    const sources: ChatMessage['sources'] = []
    try {
      for await (const ev of api.chatStream(project, q, history)) {
        if (ev.type === 'sources') {
          sources.push(...(ev.sources || []))
        } else if (ev.type === 'chunk') {
          assistant += ev.content
          setMessages((m) => {
            const copy = [...m]
            const last = copy[copy.length - 1]
            if (last?.role === 'assistant') {
              copy[copy.length - 1] = { ...last, content: assistant, sources }
            } else {
              copy.push({ role: 'assistant', content: assistant, sources })
            }
            return copy
          })
        } else if (ev.type === 'error') {
          setErr(ev.error)
        }
      }
      if (!assistant) {
        // fallback non-stream
        const res = await api.chat(project, q, history)
        setMessages((m) => [
          ...m.filter((x, i) => !(i === m.length - 1 && x.role === 'assistant' && !x.content)),
          { role: 'assistant', content: res.content, sources: res.sources },
        ])
      }
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'chat failed')
    } finally {
      setBusy(false)
    }
  }

  if (chatOk === false) {
    return (
      <div className="card">
        <h2>Chat not configured</h2>
        <p className="muted">
          Set <code>GEMINI_API_KEY</code> or <code>OPENROUTER_API_KEY</code> on the
          server and restart <code>semidx serve</code>. You can still use Explore
          for semantic search without an LLM.
        </p>
      </div>
    )
  }

  return (
    <div className="chat-layout">
      <div className="chat-log card" role="log" aria-live="polite" aria-atomic="false">
        {messages.length === 0 && (
          <p className="muted">
            Ask anything about <strong>{project}</strong>. Answers use RAG over the
            index; sources open in the Files tab.
          </p>
        )}
        {messages.map((m, i) => (
          <div key={i} className={`chat-bubble ${m.role}`}>
            <div className="chat-role">{m.role}</div>
            <pre className="snippet">{m.content}</pre>
            {m.sources && m.sources.length > 0 && (
              <div className="chat-sources">
                {m.sources.map((s, j) => (
                  <button
                    key={j}
                    type="button"
                    className="link"
                    onClick={() => onOpenFile(s.file, s.start_line)}
                  >
                    {s.file}:{s.start_line}
                  </button>
                ))}
              </div>
            )}
          </div>
        ))}
        {err && <div className="alert error">{err}</div>}
      </div>
      <form className="chat-input card" onSubmit={(e) => void send(e)}>
        <textarea
          rows={3}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="How does authentication work in this project?"
        />
        <button type="submit" disabled={busy || !input.trim()}>
          {busy ? 'Thinking…' : 'Send'}
        </button>
      </form>
    </div>
  )
}
