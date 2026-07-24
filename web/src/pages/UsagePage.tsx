import { useEffect, useState } from 'react'
import { api, ApiError, type UsageCount, type UsageReport } from '../api'
import { Alert } from '../components/Alert'
import { Badge, type BadgeTone } from '../components/Badge'
import { Card } from '../components/Card'
import { EmptyState } from '../components/EmptyState'
import { Select } from '../components/Input'
import { Spinner } from '../components/Spinner'

const SEVERITY_TONE: Record<string, BadgeTone> = {
  warning: 'warning',
  info: 'accent',
}

function CountList({ title, rows }: Readonly<{ title: string; rows: UsageCount[] }>) {
  return (
    <Card>
      <h2 className="mb-2 text-sm font-semibold text-fg">{title}</h2>
      {rows.length === 0 ? (
        <p className="m-0 text-sm text-muted">None.</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-1 p-0 text-sm">
          {rows.map((r) => (
            <li key={r.key} className="flex items-center justify-between gap-2">
              <code className="text-xs text-muted">{r.key}</code>
              <span className="font-medium">{r.count}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

export function UsagePage() {
  const [days, setDays] = useState(30)
  const [report, setReport] = useState<UsageReport | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    setErr('')
    void api
      .searchUsage(days)
      .then(setReport)
      .catch((e: unknown) => setErr(e instanceof ApiError ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [days])

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="mb-1 text-[1.45rem] font-bold">Usage</h1>
          <p className="m-0 text-muted">
            Search analytics by project, source (mcp/cli/admin), and outcome.
          </p>
        </div>
        <label className="flex items-center gap-1.5 text-sm text-muted" htmlFor="usage-window">
          Window
          <Select
            id="usage-window"
            className="w-auto py-1 text-xs"
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
          >
            <option value={7}>7 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
          </Select>
        </label>
      </div>

      {err && <Alert kind="error">{err}</Alert>}
      {loading && !report && (
        <div className="flex items-center gap-2 text-muted">
          <Spinner /> Loading…
        </div>
      )}

      {report && (
        <div className="flex flex-col gap-3.5">
          <Card>
            <p className="m-0 text-sm">{report.summary}</p>
          </Card>

          <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-3">
            <CountList title="By project" rows={report.by_project} />
            <CountList title="By source" rows={report.by_source} />
            <CountList title="By outcome" rows={report.by_outcome} />
          </div>

          <Card>
            <h2 className="mb-2 text-sm font-semibold text-fg">Findings</h2>
            {report.findings.length === 0 ? (
              <EmptyState title="No findings" />
            ) : (
              <ul className="m-0 flex list-none flex-col gap-2 p-0 text-sm">
                {report.findings.map((f) => (
                  <li key={f.kind} className="flex items-start gap-2">
                    <Badge tone={SEVERITY_TONE[f.severity] ?? 'neutral'}>{f.severity}</Badge>
                    <span>{f.message}</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <Card>
            <h2 className="mb-2 text-sm font-semibold text-fg">Blind spots</h2>
            <ul className="m-0 flex list-none flex-col gap-1 p-0 text-xs text-muted">
              {report.blind_spots.map((b) => (
                <li key={b}>{b}</li>
              ))}
            </ul>
          </Card>
        </div>
      )}
    </div>
  )
}
