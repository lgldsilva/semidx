import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, ApiError, type Job, type Project } from '../api'

export function ProjectsPage() {
  const navigate = useNavigate()
  const [projects, setProjects] = useState<Project[]>([])
  const [err, setErr] = useState('')
  const [flash, setFlash] = useState('')
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [jobPoll, setJobPoll] = useState<Job | null>(null)
  const [filter, setFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [sourceFilter, setSourceFilter] = useState('')
  const [sort, setSort] = useState<'name' | 'files' | 'status'>('name')

  const reload = useCallback(async () => {
    setLoading(true)
    setErr('')
    try {
      setProjects(await api.projects())
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'failed to load projects')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  useEffect(() => {
    if (!jobPoll || jobPoll.status === 'succeeded' || jobPoll.status === 'failed') {
      return
    }
    const t = setInterval(() => {
      void api
        .job(jobPoll.id)
        .then((j) => {
          setJobPoll(j)
          if (j.status === 'succeeded' || j.status === 'failed') {
            void reload()
          }
        })
        .catch(() => undefined)
    }, 1500)
    return () => clearInterval(t)
  }, [jobPoll, reload])

  const filtered = useMemo(() => {
    let list = [...projects]
    const q = filter.toLowerCase().trim()
    if (q) {
      list = list.filter(
        (p) =>
          p.name.toLowerCase().includes(q) ||
          (p.identity || '').toLowerCase().includes(q) ||
          (p.path || '').toLowerCase().includes(q) ||
          (p.git_url || '').toLowerCase().includes(q),
      )
    }
    if (statusFilter) list = list.filter((p) => p.status === statusFilter)
    if (sourceFilter) list = list.filter((p) => p.source_type === sourceFilter)
    list.sort((a, b) => {
      if (sort === 'files') return (b.total_files || 0) - (a.total_files || 0)
      if (sort === 'status') return (a.status || '').localeCompare(b.status || '')
      return a.name.localeCompare(b.name)
    })
    return list
  }, [projects, filter, statusFilter, sourceFilter, sort])

  const statuses = useMemo(
    () => [...new Set(projects.map((p) => p.status).filter(Boolean))].sort(),
    [projects],
  )
  const sources = useMemo(
    () => [...new Set(projects.map((p) => p.source_type).filter(Boolean))].sort() as string[],
    [projects],
  )

  async function onReindex(name: string) {
    setErr('')
    setFlash('')
    try {
      const { job_id } = await api.reindex(name)
      setFlash(`Reindex queued for ${name} (job #${job_id})`)
      setJobPoll({ id: job_id, type: 'full', status: 'queued' })
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'reindex failed')
    }
  }

  async function onDelete(name: string) {
    if (!confirm(`Delete project “${name}” and all its chunks?`)) return
    setErr('')
    try {
      await api.deleteProject(name)
      setFlash(`Deleted ${name}`)
      await reload()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'delete failed')
    }
  }

  return (
    <div>
      <div className="page-head">
        <div>
          <h1>Projects</h1>
          <p className="muted">
            Server index workbench — open a project to browse files, explore, and chat.
          </p>
        </div>
        <button type="button" onClick={() => setShowCreate(true)}>
          Add git project
        </button>
      </div>

      {err && <div className="alert error">{err}</div>}
      {flash && <div className="alert ok">{flash}</div>}
      {jobPoll && (
        <div className="alert ok">
          Job #{jobPoll.id}: <strong>{jobPoll.status}</strong>
          {jobPoll.files_indexed != null &&
            ` · files ${jobPoll.files_indexed} · chunks ${jobPoll.chunks_created ?? 0}`}
          {jobPoll.error && ` · ${jobPoll.error}`}
        </div>
      )}

      <div className="filters card">
        <div className="row">
          <label className="grow">
            Filter
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="name, identity, path, git url…"
            />
          </label>
          <label>
            Status
            <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
              <option value="">all</option>
              {statuses.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label>
            Source
            <select value={sourceFilter} onChange={(e) => setSourceFilter(e.target.value)}>
              <option value="">all</option>
              {sources.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label>
            Sort
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value as 'name' | 'files' | 'status')}
            >
              <option value="name">name</option>
              <option value="files">files</option>
              <option value="status">status</option>
            </select>
          </label>
        </div>
      </div>

      {showCreate && (
        <CreateProjectForm
          onClose={() => setShowCreate(false)}
          onCreated={(jobId) => {
            setShowCreate(false)
            setFlash('Project created')
            if (jobId) setJobPoll({ id: jobId, type: 'full', status: 'queued' })
            void reload()
          }}
          onError={setErr}
        />
      )}

      <div className="card">
        {loading ? (
          <p className="muted">Loading…</p>
        ) : filtered.length === 0 ? (
          <p className="muted">
            No projects match. Add a git repo or{' '}
            <code>semidx push --project .</code>
          </p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Source</th>
                <th>Status</th>
                <th>Model</th>
                <th>Files</th>
                <th>Last job</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {filtered.map((p) => (
                <tr key={p.name}>
                  <td>
                    <Link to={`/projects/${encodeURIComponent(p.name)}`}>
                      <strong>{p.name}</strong>
                    </Link>
                    {p.identity && (
                      <div className="muted small" title={p.identity}>
                        {p.identity.length > 48
                          ? p.identity.slice(0, 48) + '…'
                          : p.identity}
                      </div>
                    )}
                    {p.license && (
                      <div className="muted small">license: {p.license}</div>
                    )}
                  </td>
                  <td>
                    {p.source_type || '—'}
                    {p.git_url && (
                      <div className="muted small">{p.git_url}</div>
                    )}
                    {p.branch && <div className="muted small">@{p.branch}</div>}
                    {p.path && !p.git_url && (
                      <div className="muted small">{p.path}</div>
                    )}
                  </td>
                  <td>
                    <span className="pill">{p.status}</span>
                  </td>
                  <td>
                    {p.model || '—'}
                    {p.dims ? (
                      <div className="muted small">{p.dims}d</div>
                    ) : null}
                    {p.last_commit ? (
                      <div className="muted small">{p.last_commit.slice(0, 8)}</div>
                    ) : null}
                  </td>
                  <td>{p.total_files ?? '—'}</td>
                  <td className="small">
                    {p.last_job
                      ? `#${p.last_job.id} ${p.last_job.status}`
                      : '—'}
                  </td>
                  <td className="row-actions">
                    <button
                      type="button"
                      className="link"
                      onClick={() =>
                        navigate(`/projects/${encodeURIComponent(p.name)}`)
                      }
                    >
                      Open
                    </button>
                    <button
                      type="button"
                      className="link"
                      onClick={() =>
                        navigate(
                          `/projects/${encodeURIComponent(p.name)}?tab=explore`,
                        )
                      }
                    >
                      Explore
                    </button>
                    <button
                      type="button"
                      className="link"
                      onClick={() =>
                        navigate(
                          `/projects/${encodeURIComponent(p.name)}?tab=chat`,
                        )
                      }
                    >
                      Chat
                    </button>
                    <button
                      type="button"
                      className="link"
                      onClick={() => void onReindex(p.name)}
                    >
                      Reindex
                    </button>
                    <button
                      type="button"
                      className="link danger-text"
                      onClick={() => void onDelete(p.name)}
                    >
                      Delete
                    </button>
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

function CreateProjectForm({
  onClose,
  onCreated,
  onError,
}: {
  onClose: () => void
  onCreated: (jobId?: number) => void
  onError: (msg: string) => void
}) {
  const [name, setName] = useState('')
  const [gitURL, setGitURL] = useState('')
  const [branch, setBranch] = useState('main')
  const [model, setModel] = useState('bge-m3')
  const [indexNow, setIndexNow] = useState(true)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    onError('')
    try {
      const res = await api.createProject({
        name: name.trim(),
        model,
        source_type: 'git',
        git_url: gitURL,
        branch,
        index: indexNow,
      })
      onCreated(res.job_id)
    } catch (ex) {
      onError(ex instanceof ApiError ? ex.message : 'create failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="card create-form" onSubmit={(e) => void onSubmit(e)}>
      <h2>Add git repository</h2>
      <p className="muted">
        Server clones the repo and queues an index job (same as{' '}
        <code>semidx repo add</code>).
      </p>
      <div className="row">
        <label className="grow">
          Git URL
          <input
            value={gitURL}
            onChange={(e) => setGitURL(e.target.value)}
            placeholder="https://github.com/org/repo.git"
            required
          />
        </label>
      </div>
      <div className="row">
        <label className="grow">
          Project name (optional)
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="defaults from repo basename"
          />
        </label>
        <label>
          Branch
          <input value={branch} onChange={(e) => setBranch(e.target.value)} />
        </label>
        <label>
          Model
          <input value={model} onChange={(e) => setModel(e.target.value)} />
        </label>
      </div>
      <label className="checkbox">
        <input
          type="checkbox"
          checked={indexNow}
          onChange={(e) => setIndexNow(e.target.checked)}
        />
        Start full index job now
      </label>
      <div className="row-actions">
        <button type="submit" disabled={busy}>
          {busy ? 'Creating…' : 'Create'}
        </button>
        <button type="button" className="link" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  )
}
