import { useEffect, useState } from 'react'
import { api, ApiError, type SearchHit } from '../../api'

export function ExplorePanel({
  project,
  seedQuery,
  onOpenFile,
  onAsk,
}: {
  project: string
  seedQuery: string
  onOpenFile: (path: string, line?: number) => void
  onAsk: (q: string) => void
}) {
  const [query, setQuery] = useState(seedQuery)
  const [top, setTop] = useState(15)
  const [graph, setGraph] = useState(false)
  const [graphDepth, setGraphDepth] = useState(2)
  const [results, setResults] = useState<SearchHit[]>([])
  const [fallback, setFallback] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setQuery(seedQuery)
  }, [seedQuery])

  async function run(e?: { preventDefault(): void }) {
    e?.preventDefault()
    if (!query.trim()) return
    setBusy(true)
    setErr('')
    try {
      const res = await api.search({
        query,
        project,
        top,
        graph,
        graph_depth: graphDepth,
      })
      setResults(res.results ?? [])
      setFallback(!!res.fallback)
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'search failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <form className="search-form" onSubmit={(e) => void run(e)}>
        <div className="row">
          <label className="grow">
            Query in <strong>{project}</strong>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="how is auth validated?"
            />
          </label>
          <button type="submit" disabled={busy}>
            {busy ? 'Searching…' : 'Search'}
          </button>
        </div>
        <div className="top-selector">
          <span className="muted">top</span>
          {[5, 10, 15, 30].map((n) => (
            <button
              key={n}
              type="button"
              className={`pill-btn ${top === n ? 'active' : ''}`}
              onClick={() => setTop(n)}
            >
              {n}
            </button>
          ))}
          <label className="checkbox" style={{ marginLeft: '0.75rem' }}>
            <input
              type="checkbox"
              checked={graph}
              onChange={(e) => setGraph(e.target.checked)}
            />
            Graph-RAG
          </label>
          {graph && (
            <label className="muted small">
              depth
              <input
                type="number"
                min={1}
                max={5}
                value={graphDepth}
                onChange={(e) => setGraphDepth(Number(e.target.value) || 2)}
                style={{ width: '3.5rem', marginLeft: '0.35rem' }}
              />
            </label>
          )}
        </div>
      </form>
      {err && <div className="alert error">{err}</div>}
      {fallback && (
        <div className="alert error">Keyword fallback — embeddings unavailable.</div>
      )}
      {results.map((hit, i) => (
        <article key={`${hit.path}-${i}`} className="card result-card">
          <div className="result-header">
            <button
              type="button"
              className="link result-path"
              onClick={() => onOpenFile(hit.path, hit.start_line)}
            >
              {hit.path}:{hit.start_line}
              {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
            </button>
            <span className="score-badge">{Math.round(hit.score * 100)}%</span>
          </div>
          <pre className="snippet">{hit.content}</pre>
          <div className="row-actions">
            <button type="button" className="link" onClick={() => onOpenFile(hit.path, hit.start_line)}>
              Open file
            </button>
            <button
              type="button"
              className="link"
              onClick={() =>
                onAsk(
                  `Explain this code in ${hit.path}:${hit.start_line} and how it fits the project:\n${hit.content.slice(0, 500)}`,
                )
              }
            >
              Ask about this
            </button>
          </div>
        </article>
      ))}
    </div>
  )
}
