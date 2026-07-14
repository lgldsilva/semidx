import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError, type Job } from '../api'
import { Alert } from '../components/Alert'
import { Badge, type BadgeTone } from '../components/Badge'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { Checkbox } from '../components/Input'
import { Table } from '../components/Table'

type JobRow = Job & { project_id: number; project_name?: string }

// A failed job must read red and a succeeded one green, not neutral.
const STATUS_TONE: Record<string, BadgeTone> = {
  failed: 'danger',
  succeeded: 'success',
  running: 'accent',
  queued: 'accent',
}

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
      <div className="mb-2 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="mb-1 text-[1.45rem] font-bold">Jobs</h1>
          <p className="m-0 text-muted">
            Global index job feed ({active.length} active). Auto-refresh{' '}
            {auto ? 'on' : 'off'}.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
          <Checkbox
            label="Auto-refresh"
            checked={auto}
            onChange={(e) => setAuto(e.target.checked)}
          />
          <Button variant="secondary" onClick={() => void reload()}>
            Refresh
          </Button>
        </div>
      </div>
      {err && <Alert kind="error">{err}</Alert>}
      <Card className="my-3.5">
        <Table>
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
                <td colSpan={7} className="text-muted">
                  No jobs yet.
                </td>
              </tr>
            ) : (
              jobs.map((j) => (
                <tr key={j.id}>
                  <td>{j.id}</td>
                  <td>
                    {j.project_name ? (
                      <Link
                        to={`/projects/${encodeURIComponent(j.project_name)}`}
                        className="text-accent hover:underline"
                      >
                        {j.project_name}
                      </Link>
                    ) : (
                      `#${j.project_id}`
                    )}
                  </td>
                  <td>{j.type}</td>
                  <td>
                    <Badge tone={STATUS_TONE[j.status] ?? 'neutral'}>{j.status}</Badge>
                  </td>
                  <td>{j.files_indexed ?? '—'}</td>
                  <td>{j.chunks_created ?? '—'}</td>
                  <td className={j.error ? 'text-xs text-danger' : 'text-xs text-muted'}>
                    {j.error || '—'}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </Table>
      </Card>
    </div>
  )
}
