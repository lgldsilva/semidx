import { Spinner } from './Spinner'

export type ToolCallResult = {
  preview?: string
  is_error?: boolean
  elapsed_ms?: number
  truncated?: boolean
}

export type ToolCall = {
  id?: string
  name: string
  args?: unknown
  result?: ToolCallResult
}

function prettyArgs(args: unknown): string {
  if (args == null) return '{}'
  try {
    return JSON.stringify(args, null, 2)
  } catch {
    return String(args)
  }
}

function StatusIcon({ result }: Readonly<{ result?: ToolCallResult }>) {
  if (!result) return <Spinner label="Tool running" className="size-3" />
  if (result.is_error) {
    return (
      <span className="font-bold text-danger" title="Tool failed">
        ✕
      </span>
    )
  }
  return (
    <span className="font-bold text-success" title="Tool succeeded">
      ✓
    </span>
  )
}

/**
 * ToolCallChip renders one agent tool invocation as a collapsed <details>:
 * the summary shows a live status (spinner → ✓/✕ once the tool_result event
 * lands) and elapsed time; the body shows the pretty-printed call args and
 * the (possibly truncated) result preview.
 */
export function ToolCallChip({ call }: Readonly<{ call: ToolCall }>) {
  return (
    <details className="my-1 max-w-full rounded-md border border-border bg-surface-2 text-xs">
      <summary className="flex cursor-pointer items-center gap-2 px-2.5 py-1.5">
        <StatusIcon result={call.result} />
        <span className="font-mono font-semibold">{call.name}</span>
        {call.result?.elapsed_ms != null && (
          <span className="text-muted">{call.result.elapsed_ms} ms</span>
        )}
        {call.result?.truncated && <span className="text-muted">(truncated)</span>}
      </summary>
      <div className="border-t border-border px-2.5 py-2">
        <p className="m-0 font-semibold text-muted">args</p>
        <pre className="m-0 overflow-x-auto rounded bg-code-bg p-2 whitespace-pre-wrap text-code-fg">
          {prettyArgs(call.args)}
        </pre>
        {call.result?.preview != null && call.result.preview !== '' && (
          <>
            <p className="m-0 mt-2 font-semibold text-muted">result</p>
            <pre className="m-0 overflow-x-auto rounded bg-code-bg p-2 whitespace-pre-wrap text-code-fg">
              {call.result.preview}
              {call.result.truncated ? '…' : ''}
            </pre>
          </>
        )}
      </div>
    </details>
  )
}
