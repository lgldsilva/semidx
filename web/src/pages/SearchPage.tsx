import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type Project, type SearchHit } from '../api'
import { Alert } from '../components/Alert'
import { Badge, type BadgeTone } from '../components/Badge'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { Checkbox, Input, Select } from '../components/Input'
import { Code, Snippet } from '../components/Snippet'
import { cx } from '../lib/cx'

const PILL_BTN = 'cursor-pointer rounded-full border px-2.5 py-1 text-[0.82rem] transition-colors'
const PILL_ON = 'border-accent bg-accent text-accent-fg'
const PILL_OFF =
  'border-border bg-transparent text-fg hover:border-accent hover:bg-accent hover:text-accent-fg'

type ScoreGrade = 'high' | 'mid' | 'low'

const SCORE_BORDER: Record<ScoreGrade, string> = {
  high: 'border-l-success',
  mid: 'border-l-warning',
  low: 'border-l-muted',
}

const SCORE_TONE: Record<ScoreGrade, BadgeTone> = {
  high: 'success',
  mid: 'warning',
  low: 'neutral',
}

function scoreGrade(scorePct: number): ScoreGrade {
  if (scorePct >= 75) return 'high'
  if (scorePct >= 45) return 'mid'
  return 'low'
}

export function SearchPage() {
  const [projects, setProjects] = useState<Project[]>([])
  const [project, setProject] = useState('')
  const [all, setAll] = useState(false)
  const [query, setQuery] = useState('')
  const [top, setTop] = useState(10)
  const [results, setResults] = useState<SearchHit[]>([])
  const [fallback, setFallback] = useState(false)
  const [degraded, setDegraded] = useState(false)
  const [retryAfterMs, setRetryAfterMs] = useState(0)
  const [ran, setRan] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [meta, setMeta] = useState('')

  useEffect(() => {
    void api
      .projects()
      .then((list) => {
        setProjects(list)
        if (list.length === 1) setProject(list[0].name)
      })
      .catch(() => undefined)
  }, [])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setErr('')
    setBusy(true)
    setRan(true)
    try {
      const res = await api.search({
        query,
        project: all ? undefined : project,
        all,
        top,
      })
      setResults(res.results ?? [])
      setFallback(res.fallback)
      setDegraded(res.degraded ?? false)
      setRetryAfterMs(res.retry_after_ms ?? 0)
      setMeta(
        res.resolved_project
          ? `resolved: ${res.resolved_project}`
          : res.project_count
            ? `searched ${res.project_count} projects`
            : '',
      )
    } catch (ex) {
      setResults([])
      setFallback(false)
      setDegraded(false)
      setErr(ex instanceof ApiError ? ex.message : 'search failed')
    } finally {
      setBusy(false)
    }
  }

  const retrySeconds = Math.round(retryAfterMs / 1000)

  return (
    <div>
      <div className="mb-4">
        <h1 className="mb-1 text-[1.45rem] font-bold">Search</h1>
        <p className="m-0 text-muted">
          Semantic search on the server index — same engine as{' '}
          <Code>semidx search</Code> after <Code>semidx login</Code>.
        </p>
      </div>

      <Card className="mb-5">
        <form onSubmit={(e) => void onSubmit(e)}>
          <div className="flex flex-wrap items-end gap-3.5">
            <label htmlFor="search-query" className="block min-w-[180px] flex-1 text-sm font-medium">
              Query
              <Input
                id="search-query"
                className="mt-1"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="where is authentication handled?"
                required
              />
            </label>
            <label htmlFor="search-project" className="block text-sm font-medium">
              Project
              <Select
                id="search-project"
                className="mt-1"
                value={project}
                onChange={(e) => setProject(e.target.value)}
                disabled={all}
              >
                <option value="">— select —</option>
                {projects.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </label>
            <Button type="submit" disabled={busy}>
              {busy ? 'Searching…' : 'Search'}
            </Button>
          </div>
          <div className="mt-3.5 flex flex-wrap items-center gap-1.5">
            <Checkbox
              label="Search all projects"
              checked={all}
              onChange={(e) => setAll(e.target.checked)}
            />
            <span className="ml-2 text-muted">top</span>
            {[5, 10, 20, 50].map((n) => (
              <button
                key={n}
                type="button"
                className={cx(PILL_BTN, top === n ? PILL_ON : PILL_OFF)}
                onClick={() => setTop(n)}
              >
                {n}
              </button>
            ))}
          </div>
        </form>
      </Card>

      {err && <Alert kind="error">{err}</Alert>}
      {degraded ? (
        <Alert kind="warning">
          Keyword results — embedding temporarily unavailable
          {retrySeconds > 0 ? ` (try again in ~${retrySeconds}s)` : ''}.
        </Alert>
      ) : (
        fallback && (
          <Alert kind="error">
            Keyword fallback — embeddings unavailable for this query.
          </Alert>
        )
      )}
      {meta && <p className="text-muted">{meta}</p>}

      {ran && !err && results.length === 0 && (
        <p className="text-muted">No matches.</p>
      )}

      {results.map((hit, i) => {
        const scorePct = Math.round(hit.score * 100)
        const grade = scoreGrade(scorePct)
        return (
          <Card
            key={`${hit.path}-${hit.start_line}-${i}`}
            className={cx('my-3.5 border-l-4', SCORE_BORDER[grade])}
          >
            <div className="flex justify-between gap-3 max-sm:flex-wrap">
              <div>
                {hit.project && (
                  <span className="text-xs font-semibold tracking-[0.03em] text-accent uppercase">
                    {hit.project}
                  </span>
                )}
                <Code className="block w-fit font-mono text-sm break-all">
                  {hit.path}:{hit.start_line}
                  {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
                </Code>
              </div>
              <Badge tone={SCORE_TONE[grade]} className="shrink-0 self-start font-semibold">
                {scorePct}%
              </Badge>
            </div>
            <Snippet>{hit.content}</Snippet>
          </Card>
        )
      })}
    </div>
  )
}
