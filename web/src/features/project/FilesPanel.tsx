import { useEffect, useMemo, useState } from 'react'
import { api, ApiError, type FileEntry } from '../../api'
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
    <div className="files-layout">
      <aside className="files-tree card">
        <input
          placeholder="Filter paths…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <p className="muted small">
          {loading ? 'Loading…' : `${total} files`}
        </p>
        <div className="tree-scroll">
          <TreeView
            nodes={tree}
            selected={selected}
            onSelect={setSelected}
          />
        </div>
      </aside>
      <section className="files-viewer card">
        {err && <div className="alert error">{err}</div>}
        {!selected ? (
          <p className="muted">Select a file from the tree (content from the index).</p>
        ) : (
          <>
            <div className="result-header">
              <code className="result-path">{selected}</code>
              <div className="row-actions">
                <button
                  type="button"
                  className="link"
                  onClick={() => onExplorePath(selected)}
                >
                  Find related
                </button>
              </div>
            </div>
            {truncated && (
              <div className="alert error">Showing first chunks only (truncated).</div>
            )}
            {chunks.length > 0 && (
              <details className="file-detail-chunks" open>
                <summary>Chunks ({chunks.length})</summary>
                <ul className="chunk-list">
                  {chunks.map((c) => (
                    <li key={`${c.start_line}-${c.end_line}`}>
                      <button
                        type="button"
                        className="link"
                        onClick={() => {
                          const el = document.getElementById(`L${c.start_line}`)
                          el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
                        }}
                      >
                        L{c.start_line}–{c.end_line}
                      </button>
                      <span className="muted small">
                        {c.content.slice(0, 80)}
                        {c.content.length > 80 ? '…' : ''}
                      </span>
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {(callers.length > 0 || deps.length > 0 || graphErr) && (
              <div className="file-graph-stats">
                {graphErr && <p className="muted small">{graphErr}</p>}
                {deps.length > 0 && (
                  <details open>
                    <summary>Fan-out ({deps.length})</summary>
                    <ul>
                      {deps.map((d) => (
                        <li key={d}>
                          <button type="button" className="link" onClick={() => setSelected(d)}>
                            {d}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
                {callers.length > 0 && (
                  <details open>
                    <summary>Fan-in ({callers.length})</summary>
                    <ul>
                      {callers.map((c) => (
                        <li key={c}>
                          <button type="button" className="link" onClick={() => setSelected(c)}>
                            {c}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </div>
            )}
            <pre className="snippet file-body">
              {numbered.length === 0
                ? '(empty or not in index)'
                : numbered.map((row) => (
                    <div
                      key={row.n + row.text.slice(0, 8)}
                      id={`L${row.n}`}
                      className={row.hi ? 'line-hi' : undefined}
                    >
                      <span className="ln">{row.n}</span>
                      {row.text}
                    </div>
                  ))}
            </pre>
          </>
        )}
      </section>
    </div>
  )
}
