import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, ApiError, type ProjectDetail } from '../api'
import { JobAlert } from '../features/jobs/JobAlert'
import { useJobPoll } from '../features/jobs/useJobPoll'
import { AnalyzePanel } from '../features/project/AnalyzePanel'
import { ChatPanel } from '../features/project/ChatPanel'
import { ExplorePanel } from '../features/project/ExplorePanel'
import { FilesPanel } from '../features/project/FilesPanel'
import { IngestPanel } from '../features/project/IngestPanel'
import { OverviewPanel } from '../features/project/OverviewPanel'

type Tab = 'overview' | 'files' | 'explore' | 'analyze' | 'chat' | 'ingest'

const TABS: Tab[] = ['overview', 'files', 'ingest', 'explore', 'analyze', 'chat']

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

  const { job, err: pollErr, start: startJob } = useJobPoll(reload)

  useEffect(() => {
    void reload()
  }, [reload])

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

  const goToTabWithQuery = (t: Tab, q: string) => {
    const next = new URLSearchParams(params)
    next.set('tab', t)
    next.set('q', q)
    setParams(next)
  }

  async function onReindex() {
    try {
      const { job_id } = await api.reindex(name)
      startJob(name, { id: job_id, type: 'full', status: 'queued' })
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
      {job && <JobAlert job={job} />}
      {pollErr && <div className="alert error">{pollErr}</div>}

      <div className="tab-nav" role="tablist" aria-label="Project sections">
        {TABS.map((t) => (
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
          onExplorePath={(path) => goToTabWithQuery('explore', path)}
        />
      )}
      {tab === 'ingest' && <IngestPanel project={name} onDone={() => void reload()} />}
      {tab === 'explore' && (
        <ExplorePanel
          project={name}
          seedQuery={seedQ}
          onOpenFile={openFile}
          onAsk={(q) => goToTabWithQuery('chat', q)}
        />
      )}
      {tab === 'analyze' && (
        <AnalyzePanel project={name} seedPath={filePath} onOpenFile={openFile} />
      )}
      {tab === 'chat' && (
        <ChatPanel project={name} seedQuestion={seedQ} onOpenFile={openFile} />
      )}
    </div>
  )
}
