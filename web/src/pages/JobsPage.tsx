import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError, type Job } from '../api'

type JobRow = Job & { project_id: number; project_name?: string }

export function JobsPage() {
  const [jobs, setJobs] = useState<JobRow[]>([])
  const [err, setErr] = useState('')
  const [auto, setAuto] = useState(true)

  const reload = useCallback(async () => {
    try {
      setJobs((await api.listJobs(30)) as JobRow[])
      setErr('')
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'load failed')
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  useEffect(() => {
    if (!auto) return
    const t = setInterval(() => void reload(), 2500)
    return () => clearInterval(t)
  }, [auto, reload])

  const active = jobs.filter(
    (j) => j.status === 'queued' || j.status === 'running',
  )

  return (
    <div>
      <div className="page-head">
        <div>
          <h1>Jobs</h1>
          <p className="muted">
            Global index job feed ({active.length} active). Auto-refresh{' '}
            {auto ? 'on' : 'off'}.
          </p>
        </div>
        <div className="row-actions">
          <label className="checkbox">
            <input
              type="checkbox"
              checked={auto}
              onChange={(e) => setAuto(e.target.checked)}
            />
            Auto-refresh
          </label>
          <button type="button" onClick={() => void reload()}>
            Refresh
          </button>
        </div>
      </div>
      {err && <div className="alert error">{err}</div>}
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Project</th>
              <th>Type</th>
              <th>Status</th>
              <th>Files</th>
              <th>Chunks</th>
              <th>Error</th>
            </tr>
          </thead>
          <tbody>
            {jobs.length === 0 ? (
              <tr>
                <td colSpan={7} className="muted">
                  No jobs yet.
                </td>
              </tr>
            ) : (
              jobs.map((j) => (
                <tr key={j.id}>
                  <td>{j.id}</td>
                  <td>
                    {j.project_name ? (
                      <Link to={`/projects/${encodeURIComponent(j.project_name)}`}>
                        {j.project_name}
                      </Link>
                    ) : (
                      `#${j.project_id}`
                    )}
                  </td>
                  <td>{j.type}</td>
                  <td>
                    <span className="pill">{j.status}</span>
                  </td>
                  <td>{j.files_indexed ?? '—'}</td>
                  <td>{j.chunks_created ?? '—'}</td>
                  <td className="muted small">{j.error || ''}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
