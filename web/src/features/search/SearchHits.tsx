import { Link } from 'react-router-dom'
import type { SearchHit } from '../../api'
import { Badge, type BadgeTone } from '../../components/Badge'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Code, Snippet } from '../../components/Snippet'
import { cx } from '../../lib/cx'

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
  if (results.length === 0) return null
  return (
    <div>
      {results.map((hit, i) => {
        const scorePct = Math.round(hit.score * 100)
        const grade = scoreGrade(scorePct)
        const project = hit.project || fallbackProject || ''
        return (
          <Card
            key={`${hit.project || ''}-${hit.path}-${hit.start_line}-${i}`}
            className={cx('my-3.5 border-l-4', SCORE_BORDER[grade])}
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
                {hit.stale && <Badge tone="danger">stale</Badge>}
                {hit.confidence && hit.confidence !== 'AMBIGUOUS' && (
                  <Badge tone="neutral">
                    {hit.confidence}
                    {hit.symbol ? ` ${hit.symbol}` : ''}
                  </Badge>
                )}
                <Badge tone={SCORE_TONE[grade]} className="font-semibold">
                  {scorePct}%
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
                  onClick={() => void navigator.clipboard.writeText(`${hit.path}:${hit.start_line}`)}
                >
                  Copy path
                </Button>
              </div>
            )}
          </Card>
        )
      })}
    </div>
  )
}
