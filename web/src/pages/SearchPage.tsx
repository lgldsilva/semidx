import { useEffect, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, ApiError, type Project, type SearchHit, type SearchResponse } from '../api'
import { Alert } from '../components/Alert'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { EmptyState } from '../components/EmptyState'
import { Checkbox, Input, Select } from '../components/Input'
import { Code } from '../components/Snippet'
import { SearchHits } from '../features/search/SearchHits'
import { cx } from '../lib/cx'
import { loadRecentSearches, rememberSearch } from '../lib/recentSearches'

const PILL_BTN = 'cursor-pointer rounded-full border px-2.5 py-1 text-[0.82rem] transition-colors'
const PILL_ON = 'border-accent bg-accent text-accent-fg'
const PILL_OFF =
  'border-border bg-transparent text-fg hover:border-accent hover:bg-accent hover:text-accent-fg'

function responseRoute(response: SearchResponse): string {
  if (response.route) return response.route
  if (response.fallback) return 'fallback'
  if (response.keyword) return 'keyword'
  return 'hybrid'
}

function responseMeta(response: SearchResponse): string {
  if (response.resolved_project) return `resolved: ${response.resolved_project}`
  if (response.project_count) return `searched ${response.project_count} projects`
  return ''
}

function searchProject(scopeAll: boolean, project: string): string | undefined {
  return scopeAll ? undefined : project
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

export function SearchPage() {
  const [params, setParams] = useSearchParams()
  const urlQuery = params.get('q') || ''
  const urlProject = params.get('project') || ''
  const urlAll = params.get('all') === '1' || (!urlProject && params.has('q'))
  const urlTop = Number(params.get('top') || 10) || 10
  const urlGraph = params.get('graph') === '1'
  const urlDepth = Number(params.get('depth') || 2) || 2

  const [projects, setProjects] = useState<Project[]>([])
  const [project, setProject] = useState(urlProject)
  const [all, setAll] = useState(urlAll)
  const [query, setQuery] = useState(urlQuery)
  const [top, setTop] = useState(urlTop)
  const [graph, setGraph] = useState(urlGraph)
  const [graphDepth, setGraphDepth] = useState(urlDepth)
  const [results, setResults] = useState<SearchHit[]>([])
  const [fallback, setFallback] = useState(false)
  const [degraded, setDegraded] = useState(false)
  const [retryAfterMs, setRetryAfterMs] = useState(0)
  const [searchRoute, setSearchRoute] = useState('')
  const [searchModel, setSearchModel] = useState('')
  const [tookMs, setTookMs] = useState(0)
  const [ran, setRan] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [meta, setMeta] = useState('')
  const [recent, setRecent] = useState(loadRecentSearches)

  useEffect(() => {
    void api.projects().then(setProjects).catch(() => setProjects([]))
  }, [])

  useEffect(() => {
    setQuery(urlQuery)
    setProject(urlProject)
    setAll(urlAll)
    setTop(urlTop)
    setGraph(urlGraph)
    setGraphDepth(urlDepth)
  }, [urlQuery, urlProject, urlAll, urlTop, urlGraph, urlDepth])

  useEffect(() => {
    if (!urlQuery.trim()) {
      setBusy(false)
      return
    }
    const scopeAll = urlAll || !urlProject
    let cancelled = false
    const ac = new AbortController()
    void (async () => {
      setErr('')
      setBusy(true)
      setRan(true)
      try {
        const res = await api.search({
          query: urlQuery.trim(),
          project: searchProject(scopeAll, urlProject),
          all: scopeAll,
          top: urlTop,
          graph: urlGraph,
          graph_depth: urlDepth,
          signal: ac.signal,
        })
        if (cancelled) return
        setResults(res.results ?? [])
        setFallback(res.fallback)
        setDegraded(res.degraded ?? false)
        setRetryAfterMs(res.retry_after_ms ?? 0)
        setSearchRoute(responseRoute(res))
        setSearchModel(res.model ?? '')
        setTookMs(res.took_ms ?? 0)
        setMeta(responseMeta(res))
        setRecent(rememberSearch(urlQuery.trim(), scopeAll ? undefined : urlProject))
      } catch (ex) {
        if (isAbortError(ex)) return
        if (cancelled) return
        setResults([])
        setFallback(false)
        setDegraded(false)
        setSearchRoute('')
        setSearchModel('')
        setTookMs(0)
        setErr(ex instanceof ApiError ? ex.message : 'search failed')
      } finally {
        if (!cancelled) setBusy(false)
      }
    })()
    return () => {
      cancelled = true
      ac.abort()
      setBusy(false)
    }
  }, [urlQuery, urlProject, urlAll, urlTop, urlGraph, urlDepth])

  function commit(next: {
    query: string
    project: string
    all: boolean
    top: number
    graph: boolean
    graphDepth: number
  }) {
    const q = next.query.trim()
    if (!q) return
    const p = new URLSearchParams()
    p.set('q', q)
    if (next.all || !next.project) p.set('all', '1')
    else p.set('project', next.project)
    if (next.top !== 10) p.set('top', String(next.top))
    if (next.graph) p.set('graph', '1')
    if (next.graph && next.graphDepth !== 2) p.set('depth', String(next.graphDepth))
    setParams(p, { replace: true })
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    commit({ query, project, all, top, graph, graphDepth })
  }

  const retrySeconds = Math.round(retryAfterMs / 1000)

  return (
    <div>
      <div className="mb-4">
        <h1 className="mb-1 text-[1.45rem] font-bold">Search</h1>
        <p className="m-0 text-muted">
          Find code and documentation by intent, then inspect the evidence. Open a file,
          inspect its neighborhood, or ask the project agent — <Code>/</Code> or{' '}
          <Code>Ctrl/⌘K</Code> jumps anywhere.
        </p>
      </div>

      <Card className="mb-5">
        <form onSubmit={onSubmit}>
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
                onChange={(e) => {
                  setProject(e.target.value)
                  if (e.target.value) setAll(false)
                }}
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
            <Checkbox
              label="Expand via graph"
              className="ml-3"
              checked={graph}
              onChange={(e) => setGraph(e.target.checked)}
            />
            {graph && (
              <label htmlFor="search-graph-depth" className="flex items-center gap-1.5 text-xs text-muted">
                depth
                <Input
                  id="search-graph-depth"
                  type="number"
                  min={1}
                  max={5}
                  className="w-14"
                  value={graphDepth}
                  onChange={(e) => setGraphDepth(Number(e.target.value) || 2)}
                />
              </label>
            )}
          </div>
        </form>
        {recent.length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-muted">Recent</span>
            {recent.slice(0, 5).map((item) => (
              <button
                key={`${item.query}-${item.project || ''}-${item.at}`}
                type="button"
                className={cx(PILL_BTN, PILL_OFF)}
                onClick={() =>
                  commit({
                    query: item.query,
                    project: item.project || '',
                    all: !item.project,
                    top,
                    graph,
                    graphDepth,
                  })
                }
              >
                {item.query}
              </button>
            ))}
          </div>
        )}
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
      {(meta || tookMs > 0 || searchRoute) && (
        <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-muted" aria-live="polite">
          {meta && <span>{meta}</span>}
          {searchRoute && <span className="rounded-full border border-border px-2 py-0.5">route: {searchRoute}</span>}
          {searchModel && <span className="rounded-full border border-border px-2 py-0.5">model: {searchModel}</span>}
          {tookMs > 0 && <span className="rounded-full border border-border px-2 py-0.5">{tookMs} ms</span>}
          {ran && <span>{results.length} result{results.length === 1 ? '' : 's'}</span>}
        </div>
      )}

      {ran && !err && results.length === 0 && (
        <EmptyState title="No matches">
          Try a shorter intent query, switch project, or turn on graph expansion to pull in
          importers and imported packages.
        </EmptyState>
      )}

      <SearchHits results={results} fallbackProject={all || !project ? undefined : project} />
    </div>
  )
}
