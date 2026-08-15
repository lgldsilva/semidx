import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { SearchHit } from '../../api'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Code, Snippet } from '../../components/Snippet'
import { cx } from '../../lib/cx'

function fileHref(project: string, path: string, line?: number) {
  const q = new URLSearchParams({ tab: 'files', path })
  if (line) q.set('line', String(line))
  return `/projects/${encodeURIComponent(project)}?${q.toString()}`
}

function graphHref(project: string, path: string) {
  return `/projects/${encodeURIComponent(project)}?tab=graph&path=${encodeURIComponent(path)}`
}

function chatHref(project: string, hit: SearchHit) {
  const q = `Explain ${hit.path}:${hit.start_line} and who depends on it.`
  return `/projects/${encodeURIComponent(project)}?tab=chat&q=${encodeURIComponent(q)}`
}

export function SearchHits({
  results,
  fallbackProject,
}: {
  results: SearchHit[]
  fallbackProject?: string
}) {
  const [copied, setCopied] = useState('')
  if (results.length === 0) return null
  return (
    <div>
      {results.map((hit, i) => {
        const project = hit.project || fallbackProject || ''
        return (
          <Card
            key={`${hit.project || ''}-${hit.path}-${hit.start_line}-${i}`}
            className={cx('my-3.5 border-l-4', hit.source === 'keyword' ? 'border-l-warning' : 'border-l-accent')}
          >
            <div className="flex justify-between gap-3 max-sm:flex-wrap">
              <div>
                {hit.project && (
                  <span className="text-xs font-semibold tracking-[0.03em] text-accent uppercase">
                    {hit.project}
                  </span>
                )}
                {project ? (
                  <Link
                    to={fileHref(project, hit.path, hit.start_line)}
                    className="block w-fit font-mono text-sm break-all text-accent hover:underline"
                  >
                    {hit.path}:{hit.start_line}
                    {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
                  </Link>
                ) : (
                  <Code className="block w-fit font-mono text-sm break-all">
                    {hit.path}:{hit.start_line}
                    {hit.end_line !== hit.start_line ? `-${hit.end_line}` : ''}
                  </Code>
                )}
              </div>
              <div className="flex flex-wrap items-start justify-end gap-1.5">
                {hit.source === 'graph' && (
                  <Badge tone="accent">graph{hit.graph_depth ? ` d=${hit.graph_depth}` : ''}</Badge>
                )}
                {hit.source === 'keyword' && <Badge tone="warning">keyword</Badge>}
                {hit.stale && <Badge tone="danger" title="The file changed after indexing; read it again before editing.">stale</Badge>}
                {hit.confidence && hit.confidence !== 'AMBIGUOUS' && (
                  <Badge tone="neutral">
                    {hit.confidence}
                    {hit.symbol ? ` ${hit.symbol}` : ''}
                  </Badge>
                )}
                <Badge tone="neutral" className="font-mono font-semibold" title="Ordinal score; it is not a probability.">
                  #{i + 1} · {hit.score.toFixed(3)}
                </Badge>
              </div>
            </div>
            <Snippet>{hit.content}</Snippet>
            {project && (
              <div className="mt-2 flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
                <Link to={fileHref(project, hit.path, hit.start_line)} className="text-sm text-accent hover:underline">
                  Open file
                </Link>
                <Link to={graphHref(project, hit.path)} className="text-sm text-accent hover:underline">
                  View in graph
                </Link>
                <Link to={chatHref(project, hit)} className="text-sm text-accent hover:underline">
                  Ask about this
                </Link>
                <Button
                  variant="link"
                  size="sm"
                  onClick={() => {
                    const value = `${hit.path}:${hit.start_line}`
                    void navigator.clipboard.writeText(value).then(() => {
                      setCopied(value)
                      window.setTimeout(() => setCopied((current) => (current === value ? '' : current)), 1600)
                    })
                  }}
                >
                  {copied === `${hit.path}:${hit.start_line}` ? 'Copied' : 'Copy path'}
                </Button>
              </div>
            )}
          </Card>
        )
      })}
    </div>
  )
}
