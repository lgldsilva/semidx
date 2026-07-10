import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, ApiError, type Project } from '../api'
import { JobAlert } from '../features/jobs/JobAlert'
import { useJobPoll } from '../features/jobs/useJobPoll'

export function ProjectsPage() {
  const [projects, setProjects] = useState<Project[]>([])
  const [err, setErr] = useState('')
  const [flash, setFlash] = useState('')
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
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

  const { job, project: jobProject, start: startJob } = useJobPoll(reload)

  useEffect(() => {
    void reload()
  }, [reload])

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
      startJob(name, { id: job_id, type: 'full', status: 'queued' })
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
          Add project
        </button>
      </div>

      {err && <div className="alert error">{err}</div>}
      {flash && <div className="alert ok">{flash}</div>}
      {job && <JobAlert job={job} project={jobProject} />}

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
          onCreated={(jobId, projectName, pushHint) => {
            setShowCreate(false)
            setFlash(
              pushHint
                ? `Project created — ingest with: ${pushHint}`
                : 'Project created',
            )
            if (jobId && projectName) {
              startJob(projectName, { id: jobId, type: 'full', status: 'queued' })
            }
            void reload()
          }}
          onError={setErr}
        />
      )}

      <div className="card">
        {loading ? (
          <p className="muted">Loading…</p>
        ) : filtered.length === 0 && projects.length === 0 ? (
          <div className="empty-state">
            <h2>No projects yet</h2>
            <p className="muted">
              Add a git-backed project below, or index locally and push with the CLI.
            </p>
            <ol className="muted">
              <li>
                <strong>Git sync:</strong> Add project → paste repository URL → Reindex
              </li>
              <li>
                <strong>Push files:</strong>{' '}
                <code>semidx push --project . --name myapp</code>
              </li>
              <li>
                <strong>Local only:</strong>{' '}
                <code>semidx --local index --project .</code>
              </li>
            </ol>
            <button type="button" onClick={() => setShowCreate(true)}>
              Add your first project
            </button>
          </div>
        ) : filtered.length === 0 ? (
          <p className="muted">No projects match the current filters.</p>
        ) : (
          <div className="table-wrap">
            <table className="projects-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Source</th>
                  <th>Status</th>
                  <th>Model</th>
                  <th>Files</th>
                  <th>Last job</th>
                  <th>Actions</th>
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
                          {p.identity.length > 40
                            ? p.identity.slice(0, 40) + '…'
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
                        <div className="muted small" title={p.git_url}>
                          {p.git_url.length > 36
                            ? p.git_url.slice(0, 36) + '…'
                            : p.git_url}
                        </div>
                      )}
                      {p.branch && (
                        <div className="muted small">@{p.branch}</div>
                      )}
                      {p.path && !p.git_url && (
                        <div className="muted small" title={p.path}>
                          {p.path.length > 36
                            ? p.path.slice(0, 36) + '…'
                            : p.path}
                        </div>
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
                        <div className="muted small">
                          {p.last_commit.slice(0, 8)}
                        </div>
                      ) : null}
                    </td>
                    <td>{p.total_files ?? '—'}</td>
                    <td className="small">
                      {p.last_job
                        ? `#${p.last_job.id} ${p.last_job.status}`
                        : '—'}
                    </td>
                    <td className="table-actions">
                      <ProjectRowActions
                        name={p.name}
                        onReindex={onReindex}
                        onDelete={onDelete}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function ProjectRowActions({
  name,
  onReindex,
  onDelete,
}: {
  name: string
  onReindex: (name: string) => void | Promise<void>
  onDelete: (name: string) => void | Promise<void>
}) {
  const navigate = useNavigate()
  const [menuOpen, setMenuOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const base = `/projects/${encodeURIComponent(name)}`

  useEffect(() => {
    if (!menuOpen) return
    const onDoc = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenuOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
    }
  }, [menuOpen])

  return (
    <div className="table-actions-row" ref={rootRef}>
      <button
        type="button"
        className="btn-open"
        onClick={() => navigate(base)}
      >
        Open
      </button>
      <div className="actions-menu">
        <button
          type="button"
          className="btn-more"
          aria-expanded={menuOpen}
          aria-haspopup="menu"
          aria-label={`More actions for ${name}`}
          onClick={() => setMenuOpen((v) => !v)}
        >
          More
        </button>
        {menuOpen ? (
          <div className="actions-menu-panel" role="menu">
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setMenuOpen(false)
                navigate(`${base}?tab=explore`)
              }}
            >
              Explore
            </button>
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setMenuOpen(false)
                navigate(`${base}?tab=chat`)
              }}
            >
              Chat
            </button>
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setMenuOpen(false)
                void onReindex(name)
              }}
            >
              Reindex
            </button>
            <button
              type="button"
              role="menuitem"
              className="danger-text"
              onClick={() => {
                setMenuOpen(false)
                void onDelete(name)
              }}
            >
              Delete
            </button>
          </div>
        ) : null}
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
  onCreated: (jobId?: number, projectName?: string, pushHint?: string) => void
  onError: (msg: string) => void
}) {
  const [mode, setMode] = useState<'git' | 'push'>('git')
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
        source_type: mode,
        git_url: mode === 'git' ? gitURL : undefined,
        branch: mode === 'git' ? branch : undefined,
        index: mode === 'git' ? indexNow : false,
      })
      onCreated(res.job_id, name.trim(), res.push_hint)
    } catch (ex) {
      onError(ex instanceof ApiError ? ex.message : 'create failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="card create-form" onSubmit={(e) => void onSubmit(e)}>
      <h2>Add project</h2>
      <div className="row-actions" style={{ marginBottom: '0.75rem' }}>
        <button
          type="button"
          className={`pill-btn ${mode === 'git' ? 'active' : ''}`}
          onClick={() => setMode('git')}
        >
          Git clone (server)
        </button>
        <button
          type="button"
          className={`pill-btn ${mode === 'push' ? 'active' : ''}`}
          onClick={() => setMode('push')}
        >
          Push from CLI
        </button>
      </div>
      {mode === 'git' ? (
        <p className="muted">
          Server clones the repo and queues an index job (same as{' '}
          <code>semidx repo add</code>).
        </p>
      ) : (
        <p className="muted">
          Creates an empty project shell. From your laptop:{' '}
          <code>semidx push --project . --name &lt;name&gt;</code> after login.
        </p>
      )}
      {mode === 'git' && (
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
      )}
      <div className="row">
        <label className="grow">
          Project name {mode === 'push' ? '' : '(optional)'}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={mode === 'git' ? 'defaults from repo basename' : 'my-app'}
            required={mode === 'push'}
          />
        </label>
        {mode === 'git' && (
          <label>
            Branch
            <input value={branch} onChange={(e) => setBranch(e.target.value)} />
          </label>
        )}
        <label>
          Model
          <input value={model} onChange={(e) => setModel(e.target.value)} />
        </label>
      </div>
      {mode === 'git' && (
        <label className="checkbox">
          <input
            type="checkbox"
            checked={indexNow}
            onChange={(e) => setIndexNow(e.target.checked)}
          />
          Start full index job now
        </label>
      )}
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
