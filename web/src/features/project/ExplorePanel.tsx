import { useEffect, useState } from 'react'
import { api, ApiError, type SearchHit } from '../../api'
import { Alert } from '../../components/Alert'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Checkbox, Input } from '../../components/Input'
import { Snippet } from '../../components/Snippet'
import { cx } from '../../lib/cx'

const PILL_BTN = 'cursor-pointer rounded-full border px-2.5 py-1 text-[0.82rem] transition-colors'
const PILL_ON = 'border-accent bg-accent text-accent-fg'
const PILL_OFF =
  'border-border bg-transparent text-fg hover:border-accent hover:bg-accent hover:text-accent-fg'

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
      <Card className="mb-5">
        <form onSubmit={(e) => void run(e)}>
          <div className="flex flex-wrap items-end gap-3.5">
            <label
              htmlFor="explore-query"
              className="block min-w-[180px] flex-1 text-sm font-medium"
            >
              Query in <strong>{project}</strong>
              <Input
                id="explore-query"
                className="mt-1"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="how is auth validated?"
              />
            </label>
            <Button type="submit" disabled={busy}>
              {busy ? 'Searching…' : 'Search'}
            </Button>
          </div>
          <div className="mt-3.5 flex flex-wrap items-center gap-1.5">
            <span className="text-muted">top</span>
            {[5, 10, 15, 30].map((n) => (
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
              label="Graph-RAG"
              className="ml-3"
              checked={graph}
              onChange={(e) => setGraph(e.target.checked)}
            />
            {graph && (
              <label
                htmlFor="explore-graph-depth"
                className="flex items-center gap-1.5 text-xs text-muted"
              >
                depth
                <Input
                  id="explore-graph-depth"
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
      </Card>
      {err && <Alert kind="error">{err}</Alert>}
      {fallback && (
        <Alert kind="error">Keyword fallback — embeddings unavailable.</Alert>
      )}
      {results.map((hit, i) => (
        <Card key={`${hit.path}-${i}`} className="my-3.5 border-l-4 border-l-border">
          <div className="flex justify-between gap-3 max-sm:flex-wrap">
            <button
              type="button"
              className="cursor-pointer border-0 bg-transparent p-0 text-left font-mono text-sm break-all text-accent hover:underline"
              onClick={() => onOpenFile(hit.path, hit.start_line)}
            >
              {hit.path}:{hit.start_line}
              {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
            </button>
            <Badge tone="neutral" className="shrink-0 self-start font-semibold">
              {Math.round(hit.score * 100)}%
            </Badge>
          </div>
          <Snippet>{hit.content}</Snippet>
          <div className="mt-2 flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
            <Button variant="link" size="sm" onClick={() => onOpenFile(hit.path, hit.start_line)}>
              Open file
            </Button>
            <Button
              variant="link"
              size="sm"
              onClick={() =>
                onAsk(
                  `Explain this code in ${hit.path}:${hit.start_line} and how it fits the project:\n${hit.content.slice(0, 500)}`,
                )
              }
            >
              Ask about this
            </Button>
          </div>
        </Card>
      ))}
    </div>
  )
}
