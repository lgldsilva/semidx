import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
} from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, ApiError, type Project } from '../api'
import { Alert } from '../components/Alert'
import { Badge } from '../components/Badge'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { EmptyState } from '../components/EmptyState'
import { Checkbox, Input, Select } from '../components/Input'
import { Code } from '../components/Snippet'
import { Table } from '../components/Table'
import { Tabs } from '../components/Tabs'
import { JobAlert } from '../features/jobs/JobAlert'
import { useJobPoll } from '../features/jobs/useJobPoll'

const FIELD_LABEL = 'block text-sm font-medium'

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

  const { job, project: jobProject, err: pollErr, start: startJob } = useJobPoll(reload)

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
      <div className="mb-2 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="mb-1 text-[1.45rem] font-bold">Projects</h1>
          <p className="m-0 text-muted">
            Server index workbench — open a project to browse files, explore, and chat.
          </p>
        </div>
        <Button onClick={() => setShowCreate(true)}>Add project</Button>
      </div>

      {err && <Alert kind="error">{err}</Alert>}
      {flash && <Alert kind="success">{flash}</Alert>}
      {job && <JobAlert job={job} project={jobProject} />}
      {pollErr && <Alert kind="error">{pollErr}</Alert>}

      <Card className="mb-3">
        <div className="flex flex-wrap items-end gap-3.5">
          <label htmlFor="projects-filter" className={`${FIELD_LABEL} min-w-[180px] flex-1`}>
            Filter
            <Input
              id="projects-filter"
              className="mt-1"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="name, identity, path, git url…"
            />
          </label>
          <label htmlFor="projects-status" className={FIELD_LABEL}>
            Status
            <Select
              id="projects-status"
              className="mt-1"
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value)}
            >
              <option value="">all</option>
              {statuses.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </Select>
          </label>
          <label htmlFor="projects-source" className={FIELD_LABEL}>
            Source
            <Select
              id="projects-source"
              className="mt-1"
              value={sourceFilter}
              onChange={(e) => setSourceFilter(e.target.value)}
            >
              <option value="">all</option>
              {sources.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </Select>
          </label>
          <label htmlFor="projects-sort" className={FIELD_LABEL}>
            Sort
            <Select
              id="projects-sort"
              className="mt-1"
              value={sort}
              onChange={(e) => setSort(e.target.value as 'name' | 'files' | 'status')}
            >
              <option value="name">name</option>
              <option value="files">files</option>
              <option value="status">status</option>
            </Select>
          </label>
        </div>
      </Card>

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

      <Card className="my-3.5">
        {loading ? (
          <p className="text-muted">Loading…</p>
        ) : filtered.length === 0 && projects.length === 0 ? (
          <EmptyState
            title="No projects yet"
            action={
              <Button onClick={() => setShowCreate(true)}>Add your first project</Button>
            }
          >
            Add a git-backed project (<strong>Add project</strong> → paste the
            repository URL → Reindex), push files from your machine with{' '}
            <Code>semidx push --project . --name myapp</Code>, or stay local with{' '}
            <Code>semidx --local index --project .</Code>.
          </EmptyState>
        ) : filtered.length === 0 ? (
          <p className="text-muted">No projects match the current filters.</p>
        ) : (
          <Table>
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
                    <Link
                      to={`/projects/${encodeURIComponent(p.name)}`}
                      className="font-bold text-accent hover:underline"
                    >
                      {p.name}
                    </Link>
                    {p.identity && (
                      <div className="text-xs text-muted" title={p.identity}>
                        {p.identity.length > 40
                          ? p.identity.slice(0, 40) + '…'
                          : p.identity}
                      </div>
                    )}
                    {p.license && (
                      <div className="text-xs text-muted">license: {p.license}</div>
                    )}
                  </td>
                  <td>
                    {p.source_type || '—'}
                    {p.git_url && (
                      <div className="text-xs text-muted" title={p.git_url}>
                        {p.git_url.length > 36
                          ? p.git_url.slice(0, 36) + '…'
                          : p.git_url}
                      </div>
                    )}
                    {p.branch && <div className="text-xs text-muted">@{p.branch}</div>}
                    {p.path && !p.git_url && (
                      <div className="text-xs text-muted" title={p.path}>
                        {p.path.length > 36 ? p.path.slice(0, 36) + '…' : p.path}
                      </div>
                    )}
                  </td>
                  <td>
                    <Badge>{p.status}</Badge>
                  </td>
                  <td>
                    {p.model || '—'}
                    {p.dims ? <div className="text-xs text-muted">{p.dims}d</div> : null}
                    {p.last_commit ? (
                      <div className="text-xs text-muted">{p.last_commit.slice(0, 8)}</div>
                    ) : null}
                  </td>
                  <td>{p.total_files ?? '—'}</td>
                  <td className="text-xs">
                    {p.last_job ? `#${p.last_job.id} ${p.last_job.status}` : '—'}
                  </td>
                  <td className="whitespace-nowrap">
                    <ProjectRowActions
                      name={p.name}
                      onReindex={onReindex}
                      onDelete={onDelete}
                    />
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
  return (
    <div className="inline-flex items-center gap-1.5">
      <Button size="sm" onClick={() => navigate(`/projects/${encodeURIComponent(name)}`)}>
        Open
      </Button>
      <Button size="sm" variant="secondary" onClick={() => void onReindex(name)}>
        Reindex
      </Button>
      <Button
        size="sm"
        variant="ghost"
        className="text-danger"
        onClick={() => void onDelete(name)}
      >
        Delete
      </Button>
    </div>
  )
}

const CREATE_MODES = [
  { id: 'git', label: 'Git clone (server)' },
  { id: 'push', label: 'Push from CLI' },
] as const

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
  const [privacyMode, setPrivacyMode] = useState<'cloud' | 'hybrid' | 'edge'>('hybrid')
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
        privacy_mode: privacyMode,
      })
      onCreated(res.job_id, name.trim(), res.push_hint)
    } catch (ex) {
      onError(ex instanceof ApiError ? ex.message : 'create failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card className="my-3.5">
      <form onSubmit={(e) => void onSubmit(e)}>
        <h2 className="mb-2 text-[1.1rem] font-bold">Add project</h2>
        <Tabs
          tabs={CREATE_MODES}
          active={mode}
          onSelect={setMode}
          label="Project source"
          className="mb-3"
        />
        {mode === 'git' ? (
          <p className="m-0 text-muted">
            Server clones the repo and queues an index job (same as{' '}
            <Code>semidx repo add</Code>).
          </p>
        ) : (
          <p className="m-0 text-muted">
            Creates an empty project shell. From your laptop:{' '}
            <Code>semidx push --project . --name &lt;name&gt;</Code> after login.
          </p>
        )}
        {mode === 'git' && (
          <div className="mt-2 flex flex-wrap items-end gap-3.5">
            <label htmlFor="create-git-url" className={`${FIELD_LABEL} min-w-[180px] flex-1`}>
              Git URL
              <Input
                id="create-git-url"
                className="mt-1"
                value={gitURL}
                onChange={(e) => setGitURL(e.target.value)}
                placeholder="https://github.com/org/repo.git"
                required
              />
            </label>
          </div>
        )}
        <div className="mt-2 flex flex-wrap items-end gap-3.5">
          <label htmlFor="create-name" className={`${FIELD_LABEL} min-w-[180px] flex-1`}>
            Project name {mode === 'push' ? '' : '(optional)'}
            <Input
              id="create-name"
              className="mt-1"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={mode === 'git' ? 'defaults from repo basename' : 'my-app'}
              required={mode === 'push'}
            />
          </label>
          {mode === 'git' && (
            <label htmlFor="create-branch" className={FIELD_LABEL}>
              Branch
              <Input
                id="create-branch"
                className="mt-1"
                value={branch}
                onChange={(e) => setBranch(e.target.value)}
              />
            </label>
          )}
          <label htmlFor="create-model" className={FIELD_LABEL}>
            Model
            <Input
              id="create-model"
              className="mt-1"
              value={model}
              onChange={(e) => setModel(e.target.value)}
            />
          </label>
          <label htmlFor="create-privacy" className={FIELD_LABEL}>
            Data policy
            <Select
              id="create-privacy"
              className="mt-1"
              value={privacyMode}
              onChange={(e) => setPrivacyMode(e.target.value as 'cloud' | 'hybrid' | 'edge')}
            >
              <option value="hybrid">Hybrid (recommended)</option>
              <option value="cloud">Cloud</option>
              <option value="edge">Edge-only</option>
            </Select>
          </label>
        </div>
        {mode === 'git' && (
          <Checkbox
            label="Start full index job now"
            className="mt-2.5"
            checked={indexNow}
            onChange={(e) => setIndexNow(e.target.checked)}
          />
        )}
        <div className="mt-3 flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
          <Button type="submit" disabled={busy}>
            {busy ? 'Creating…' : 'Create'}
          </Button>
          <Button variant="link" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </form>
    </Card>
  )
}
