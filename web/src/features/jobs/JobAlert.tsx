import type { Job } from '../../api'

/**
 * JobAlert renders the live status banner for an index job. The optional
 * project label is shown by the projects list (which polls jobs across
 * projects) and omitted by the single-project workspace.
 */
export function JobAlert({ job, project }: { job: Job; project?: string }) {
  // A failed job must read as an error (red), not a green "ok" banner.
  const cls = job.status === 'failed' ? 'alert error' : 'alert ok'
  return (
    <div className={cls}>
      Job #{job.id}
      {project ? ` (${project})` : ''}: <strong>{job.status}</strong>
      {job.progress_percent != null && job.status === 'running' && (
        <span> · {job.progress_percent}%</span>
      )}
      {job.files_indexed != null &&
        ` · files ${job.files_indexed}${job.progress_total ? `/${job.progress_total}` : ''} · chunks ${job.chunks_created ?? 0}`}
      {job.error && (
        <div className="small" style={{ marginTop: '0.3rem' }}>
          {job.error}
        </div>
      )}
    </div>
  )
}
