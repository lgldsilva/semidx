import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type Project, type SearchHit } from '../api'

export function SearchPage() {
  const [projects, setProjects] = useState<Project[]>([])
  const [project, setProject] = useState('')
  const [all, setAll] = useState(false)
  const [query, setQuery] = useState('')
  const [top, setTop] = useState(10)
  const [results, setResults] = useState<SearchHit[]>([])
  const [fallback, setFallback] = useState(false)
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
      setMeta(
        res.resolved_project
          ? `resolved: ${res.resolved_project}`
          : res.project_count
            ? `searched ${res.project_count} projects`
            : '',
      )
    } catch (ex) {
      setResults([])
      setErr(ex instanceof ApiError ? ex.message : 'search failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div className="search-hero">
        <h1>Search</h1>
        <p className="muted">
          Semantic search on the server index — same engine as{' '}
          <code>semidx search</code> after <code>semidx login</code>.
        </p>
      </div>

      <form className="search-form" onSubmit={(e) => void onSubmit(e)}>
        <div className="row">
          <label className="grow">
            Query
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="where is authentication handled?"
              required
            />
          </label>
          <label>
            Project
            <select
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
            </select>
          </label>
          <button className="search-btn" type="submit" disabled={busy}>
            {busy ? 'Searching…' : 'Search'}
          </button>
        </div>
        <div className="top-selector">
          <label className="search-all-toggle">
            <input
              type="checkbox"
              checked={all}
              onChange={(e) => setAll(e.target.checked)}
            />
            Search all projects
          </label>
          <span className="top-selector-sep muted">top</span>
          {[5, 10, 20, 50].map((n) => (
            <button
              key={n}
              type="button"
              className={`pill-btn ${top === n ? 'active' : ''}`}
              onClick={() => setTop(n)}
            >
              {n}
            </button>
          ))}
        </div>
      </form>

      {err && <div className="alert error">{err}</div>}
      {fallback && (
        <div className="alert error">
          Keyword fallback — embeddings unavailable for this query.
        </div>
      )}
      {meta && <p className="muted">{meta}</p>}

      {ran && !err && results.length === 0 && (
        <p className="muted">No matches.</p>
      )}

      {results.map((hit, i) => {
        const scorePct = Math.round(hit.score * 100)
        const scoreClass =
          scorePct >= 75 ? 'score-high' : scorePct >= 45 ? 'score-mid' : 'score-low'
        return (
          <article key={`${hit.path}-${hit.start_line}-${i}`} className={`card result-card ${scoreClass}`}>
            <div className="result-header">
              <div className="result-loc">
                {hit.project && (
                  <span className="result-project">{hit.project}</span>
                )}
                <code className="result-path">
                  {hit.path}:{hit.start_line}
                  {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
                </code>
              </div>
              <span className={`score-badge ${scoreClass}`}>{scorePct}%</span>
            </div>
            <pre className="snippet">{hit.content}</pre>
          </article>
        )
      })}
    </div>
  )
}
