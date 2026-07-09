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

type Tab = 'overview' | 'files' | 'explore' | 'chat'

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
      void api.job(jobPoll.id).then((j) => {
        setJobPoll(j)
        if (j.status === 'succeeded' || j.status === 'failed') void reload()
      })
    }, 1500)
    return () => clearInterval(t)
  }, [jobPoll, reload])

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
          {jobPoll.error && ` · ${jobPoll.error}`}
        </div>
      )}

      <nav className="tab-nav">
        {(['overview', 'files', 'explore', 'chat'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            className={`tab-btn ${tab === t ? 'active' : ''}`}
            onClick={() => setTab(t)}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </nav>

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
      return
    }
    setErr('')
    void api
      .projectFileContent(project, selected)
      .then((r) => {
        setContent(r.content)
        setTruncated(r.truncated)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : 'load content failed'))
  }, [project, selected])

  const tree = useMemo(
    () => buildFileTree(files.map((f) => f.path)),
    [files],
  )

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
            {line ? (
              <p className="muted small">Hint: jump target line {line}</p>
            ) : null}
            <pre className="snippet file-body">{content || '(empty or not in index)'}</pre>
          </>
        )}
      </section>
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
      const res = await api.search({ query, project, top })
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
      <div className="chat-log card">
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
