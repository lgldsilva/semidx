import { useMemo } from 'react'
import type { Job, Project } from '../../api'

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
