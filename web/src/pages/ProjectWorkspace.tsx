import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, ApiError, type ProjectDetail } from '../api'
import { Alert } from '../components/Alert'
import { Badge } from '../components/Badge'
import { Button } from '../components/Button'
import { Spinner } from '../components/Spinner'
import { Tabs } from '../components/Tabs'
import { JobAlert } from '../features/jobs/JobAlert'
import { useJobPoll } from '../features/jobs/useJobPoll'
import { AnalyzePanel } from '../features/project/AnalyzePanel'
import { ChatPanel } from '../features/project/ChatPanel'
import { ExplorePanel } from '../features/project/ExplorePanel'
import { FilesPanel } from '../features/project/FilesPanel'
import { IngestPanel } from '../features/project/IngestPanel'
import { OverviewPanel } from '../features/project/OverviewPanel'

type Tab = 'overview' | 'files' | 'explore' | 'analyze' | 'chat' | 'ingest'

const TABS: ReadonlyArray<{ id: Tab; label: string }> = [
  { id: 'overview', label: 'Overview' },
  { id: 'files', label: 'Files' },
  { id: 'ingest', label: 'Ingest' },
  { id: 'explore', label: 'Explore' },
  { id: 'analyze', label: 'Analyze' },
  { id: 'chat', label: 'Chat' },
]

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
    return (
      <p className="flex items-center gap-2 text-muted">
        <Spinner /> Loading project…
      </p>
    )
  }
  if (err && !detail) {
    return (
      <div>
        <Alert kind="error">{err}</Alert>
        <Link to="/" className="text-accent hover:underline">
          ← Projects
        </Link>
      </div>
    )
  }
  if (!detail) return null
  const p = detail.project

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-start justify-between gap-4">
        <div>
          <Link to="/" className="text-xs text-muted hover:underline">
            ← Projects
          </Link>
          <h1 className="mb-1 text-[1.45rem] font-bold">{p.name}</h1>
          <p className="m-0 flex flex-wrap items-center gap-1.5 text-muted">
            <Badge>{p.status}</Badge>
            {p.source_type && <Badge tone="neutral">{p.source_type}</Badge>}
            {p.model && <span>· {p.model}</span>}
            {typeof p.total_files === 'number' && <span>· {p.total_files} files</span>}
            {typeof p.total_chunks === 'number' && <span>· {p.total_chunks} chunks</span>}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
          <Button onClick={() => void onReindex()}>Reindex</Button>
          <Button variant="link" className="text-danger" onClick={() => void onDelete()}>
            Delete
          </Button>
        </div>
      </div>

      {err && <Alert kind="error">{err}</Alert>}
      {job && <JobAlert job={job} />}
      {pollErr && <Alert kind="error">{pollErr}</Alert>}

      <Tabs
        tabs={TABS}
        active={tab}
        onSelect={setTab}
        label="Project sections"
        className="mb-4"
      />

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
