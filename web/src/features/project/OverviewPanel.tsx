import { useMemo } from 'react'
import type { Job, Project } from '../../api'
import { Badge, type BadgeTone } from '../../components/Badge'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Code, Snippet } from '../../components/Snippet'
import { Table } from '../../components/Table'

const H2 = 'mb-2 text-[1.1rem] font-bold'
const DT = 'font-semibold text-muted'
const DD = 'm-0 break-all'

// A failed job must read red and a succeeded one green, not neutral.
const STATUS_TONE: Record<string, BadgeTone> = {
  failed: 'danger',
  succeeded: 'success',
  running: 'accent',
  queued: 'accent',
}

export function OverviewPanel({
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
    <div className="grid gap-3.5 md:grid-cols-2">
      <Card>
        <h2 className={H2}>Metadata</h2>
        <dl className="m-0 grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1.5 text-sm max-sm:grid-cols-1 max-sm:gap-y-0.5">
          <dt className={DT}>Identity</dt>
          <dd className={DD}>
            <Code>{p.identity || '—'}</Code>
          </dd>
          <dt className={DT}>Path</dt>
          <dd className={DD}>
            <Code>{p.path || '—'}</Code>
          </dd>
          <dt className={DT}>Git</dt>
          <dd className={DD}>
            {p.git_url || '—'}
            {p.branch ? ` @ ${p.branch}` : ''}
          </dd>
          <dt className={DT}>Model / dims</dt>
          <dd className={DD}>
            {p.model || '—'}
            {p.dims ? ` · ${p.dims}d` : ''}
          </dd>
          <dt className={DT}>Data policy</dt>
          <dd className={DD}>{p.privacy_mode || 'hybrid'}</dd>
          <dt className={DT}>License</dt>
          <dd className={DD}>{p.license || '—'}</dd>
          <dt className={DT}>Last commit</dt>
          <dd className={DD}>
            <Code>{p.last_commit ? p.last_commit.slice(0, 12) : '—'}</Code>
          </dd>
          <dt className={DT}>Files / chunks</dt>
          <dd className={DD}>
            {p.total_files ?? '—'} / {p.total_chunks ?? '—'}
          </dd>
          <dt className={DT}>Last job</dt>
          <dd className={DD}>
            {p.last_job
              ? `#${p.last_job.id} ${p.last_job.status} (${p.last_job.type})`
              : '—'}
          </dd>
        </dl>
        <div className="mt-4 flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
          <Button onClick={onFiles}>Browse files</Button>
          <Button onClick={onExplore}>Explore / search</Button>
          <Button onClick={onChat}>Chat about project</Button>
        </div>
      </Card>

      <Card>
        <h2 className={H2}>Language / extension mix</h2>
        {exts.length === 0 ? (
          <p className="text-muted">No file stats yet.</p>
        ) : (
          <Table>
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
                    <Code>.{ext}</Code>
                  </td>
                  <td>{n}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <Card className="md:col-span-2">
        <h2 className={H2}>Recent jobs</h2>
        {jobs.length === 0 ? (
          <p className="text-muted">No jobs recorded.</p>
        ) : (
          <Table>
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
                    <Badge tone={STATUS_TONE[j.status] ?? 'neutral'}>{j.status}</Badge>
                  </td>
                  <td>{j.files_indexed ?? '—'}</td>
                  <td>{j.chunks_created ?? '—'}</td>
                  <td className={j.error ? 'text-xs text-danger' : 'text-xs text-muted'}>
                    {j.error || '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <Card className="md:col-span-2">
        <h2 className={H2}>CLI</h2>
        <Snippet>{`semidx search --project ${p.name} --query "…"
semidx status --project ${p.name}
semidx push --project .   # if this is a push project
semidx --local search --query "…"  # ignore server login`}</Snippet>
      </Card>
    </div>
  )
}
