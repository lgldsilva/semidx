import { useEffect, useMemo, useState } from 'react'
import { api, ApiError, type FileEntry } from '../../api'
import { Alert } from '../../components/Alert'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Input } from '../../components/Input'
import { Snippet } from '../../components/Snippet'
import { cx } from '../../lib/cx'
import { buildFileTree } from './buildFileTree'
import { TreeView } from './TreeView'

export function FilesPanel({
  project,
  initialPath,
  line,
  onExplorePath,
}: {
  project: string
  initialPath: string
  line?: number
  onExplorePath: (path: string) => void
}) {
  const [files, setFiles] = useState<FileEntry[]>([])
  const [total, setTotal] = useState(0)
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState(initialPath)
  const [content, setContent] = useState('')
  const [chunks, setChunks] = useState<
    { start_line: number; end_line: number; content: string }[]
  >([])
  const [callers, setCallers] = useState<string[]>([])
  const [deps, setDeps] = useState<string[]>([])
  const [graphErr, setGraphErr] = useState('')
  const [truncated, setTruncated] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    void api
      .projectFiles(project, { q: filter || undefined, limit: 3000 })
      .then((r) => {
        setFiles(r.files)
        setTotal(r.total)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : 'load files failed'))
      .finally(() => setLoading(false))
  }, [project, filter])

  useEffect(() => {
    if (!selected) {
      setContent('')
      setChunks([])
      return
    }
    setErr('')
    void api
      .projectFileContent(project, selected)
      .then((r) => {
        setContent(r.content)
        setChunks(r.chunks || [])
        setTruncated(r.truncated)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : 'load content failed'))
  }, [project, selected])

  useEffect(() => {
    if (!selected) {
      setCallers([])
      setDeps([])
      return
    }
    setGraphErr('')
    void Promise.all([
      api.projectCallers(project, selected),
      api.projectDeps(project, selected),
    ])
      .then(([c, d]) => {
        setCallers(c.callers || [])
        setDeps(d.dependencies || [])
      })
      .catch((e) =>
        setGraphErr(e instanceof ApiError ? e.message : 'graph lookup failed'),
      )
  }, [project, selected])

  useEffect(() => {
    if (initialPath) setSelected(initialPath)
  }, [initialPath])

  useEffect(() => {
    if (!line || !content) return
    const el = document.getElementById(`L${line}`)
    el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [line, content, selected])

  const tree = useMemo(
    () => buildFileTree(files.map((f) => f.path)),
    [files],
  )

  const numbered = useMemo(() => {
    if (!content) return [] as { n: number; text: string; hi: boolean }[]
    // Reconstruct approximate line numbers from chunk metadata when available.
    const lines = content.split('\n')
    const out: { n: number; text: string; hi: boolean }[] = []
    if (chunks.length > 0) {
      for (const ch of chunks) {
        const part = ch.content.split('\n')
        for (let j = 0; j < part.length; j++) {
          const n = ch.start_line + j
          out.push({ n, text: part[j], hi: !!line && n === line })
        }
      }
      // if rebuild shorter than raw content lines, fall back
      if (out.length >= lines.length * 0.5) return out
    }
    return lines.map((text, idx) => ({
      n: idx + 1,
      text,
      hi: !!line && idx + 1 === line,
    }))
  }, [content, chunks, line])

  return (
    <div className="grid min-h-[420px] gap-3.5 md:grid-cols-[minmax(220px,32%)_1fr]">
      <Card>
        <Input
          className="mb-2"
          placeholder="Filter paths…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <p className="text-xs text-muted">
          {loading ? 'Loading…' : `${total} files`}
        </p>
        <div className="max-h-[55vh] overflow-auto">
          <TreeView
            nodes={tree}
            selected={selected}
            onSelect={setSelected}
          />
        </div>
      </Card>
      <Card>
        {err && <Alert kind="error">{err}</Alert>}
        {!selected ? (
          <p className="text-muted">Select a file from the tree (content from the index).</p>
        ) : (
          <>
            <div className="flex justify-between gap-3 max-sm:flex-wrap">
              <code className="font-mono text-sm break-all">{selected}</code>
              <Button
                variant="link"
                size="sm"
                className="shrink-0"
                onClick={() => onExplorePath(selected)}
              >
                Find related
              </Button>
            </div>
            {truncated && (
              <Alert kind="error">Showing first chunks only (truncated).</Alert>
            )}
            {chunks.length > 0 && (
              <details className="my-2 text-sm" open>
                <summary className="cursor-pointer font-semibold text-muted">
                  Chunks ({chunks.length})
                </summary>
                <ul className="m-0 list-none p-0">
                  {chunks.map((c) => (
                    <li key={`${c.start_line}-${c.end_line}`} className="flex items-baseline gap-2">
                      <Button
                        variant="link"
                        size="sm"
                        className="shrink-0"
                        onClick={() => {
                          const el = document.getElementById(`L${c.start_line}`)
                          el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
                        }}
                      >
                        L{c.start_line}–{c.end_line}
                      </Button>
                      <span className="truncate text-xs text-muted">
                        {c.content.slice(0, 80)}
                        {c.content.length > 80 ? '…' : ''}
                      </span>
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {(callers.length > 0 || deps.length > 0 || graphErr) && (
              <div className="my-2 text-sm">
                {graphErr && <p className="text-xs text-muted">{graphErr}</p>}
                {deps.length > 0 && (
                  <details open>
                    <summary className="cursor-pointer font-semibold text-muted">
                      Fan-out ({deps.length})
                    </summary>
                    <ul className="m-0 list-none p-0">
                      {deps.map((d) => (
                        <li key={d}>
                          <Button variant="link" size="sm" onClick={() => setSelected(d)}>
                            {d}
                          </Button>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
                {callers.length > 0 && (
                  <details open>
                    <summary className="cursor-pointer font-semibold text-muted">
                      Fan-in ({callers.length})
                    </summary>
                    <ul className="m-0 list-none p-0">
                      {callers.map((c) => (
                        <li key={c}>
                          <Button variant="link" size="sm" onClick={() => setSelected(c)}>
                            {c}
                          </Button>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </div>
            )}
            <Snippet className="max-h-[60vh] overflow-y-auto text-[0.78rem]">
              {numbered.length === 0
                ? '(empty or not in index)'
                : numbered.map((row) => (
                    <div
                      key={row.n + row.text.slice(0, 8)}
                      id={`L${row.n}`}
                      className={cx(row.hi && 'block bg-warning/25')}
                    >
                      <span className="mr-3 inline-block w-[3.2rem] text-right text-muted select-none">
                        {row.n}
                      </span>
                      {row.text}
                    </div>
                  ))}
            </Snippet>
          </>
        )}
      </Card>
    </div>
  )
}
