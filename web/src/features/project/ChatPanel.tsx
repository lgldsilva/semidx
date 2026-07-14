import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import {
  api,
  ApiError,
  type ChatMessage,
  type ChatSource,
  type ChatToolResultEvent,
} from '../../api'
import { Alert } from '../../components/Alert'
import { Button } from '../../components/Button'
import { Card } from '../../components/Card'
import { Select, Textarea } from '../../components/Input'
import { Markdown } from '../../components/Markdown'
import { Code } from '../../components/Snippet'
import { ToolCallChip, type ToolCall } from '../../components/ToolCallChip'
import { useChatPrefs } from '../../hooks/useChatPrefs'

// UiMessage augments a persisted ChatMessage with per-turn ephemera: the tool
// activity chips and the model that answered (neither is persisted).
type UiMessage = ChatMessage & { tools?: ToolCall[]; model?: string }

/**
 * applyToolResult attaches a tool_result to its pending tool_call: by id when
 * present, else the oldest same-name call without a result (FIFO), else a
 * standalone completed chip (result arrived without a visible call).
 */
export function applyToolResult(tools: ToolCall[], ev: ChatToolResultEvent): ToolCall[] {
  const result = {
    preview: ev.preview,
    is_error: ev.is_error,
    elapsed_ms: ev.elapsed_ms,
    truncated: ev.truncated,
  }
  let idx = ev.id ? tools.findIndex((t) => t.id === ev.id && !t.result) : -1
  if (idx < 0) idx = tools.findIndex((t) => t.name === ev.name && !t.result)
  if (idx < 0) return [...tools, { id: ev.id, name: ev.name, result }]
  const copy = [...tools]
  copy[idx] = { ...copy[idx], result }
  return copy
}

function isAbortError(e: unknown): boolean {
  return (
    (e instanceof DOMException || e instanceof Error) && e.name === 'AbortError'
  )
}

export function ChatPanel({
  project,
  seedQuestion,
  onOpenFile,
  initialMessages,
  onPersist,
}: {
  project: string
  seedQuestion: string
  // project is passed for global-chat sources (empty for project-scoped chat).
  onOpenFile: (path: string, line?: number, project?: string) => void
  // initialMessages seeds the log when reopening a persisted conversation; a new
  // reference (e.g. switching conversations) resets the log.
  initialMessages?: ChatMessage[]
  // onPersist, when set, is called once per completed turn (user + assistant) so
  // the caller can save it to a conversation.
  onPersist?: (m: ChatMessage) => void
}) {
  const global = project === ''
  const [messages, setMessages] = useState<UiMessage[]>(initialMessages ?? [])
  const [input, setInput] = useState(seedQuestion)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [chatOk, setChatOk] = useState<boolean | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const prefs = useChatPrefs()

  useEffect(() => {
    void api.system().then((s) => setChatOk(!!s.chat_enabled)).catch(() => setChatOk(false))
  }, [])

  useEffect(() => {
    if (initialMessages) setMessages(initialMessages)
  }, [initialMessages])

  useEffect(() => {
    setInput(seedQuestion)
  }, [seedQuestion])

  // Abort a live stream when the panel unmounts (tab away mid-answer).
  useEffect(() => () => abortRef.current?.abort(), [])

  // patchAssistant updates (or creates) the trailing assistant bubble of the
  // in-flight turn with the latest streamed state.
  function patchAssistant(patch: Partial<UiMessage>) {
    setMessages((m) => {
      const copy = [...m]
      const last = copy[copy.length - 1]
      if (last?.role === 'assistant') {
        copy[copy.length - 1] = { ...last, ...patch }
      } else {
        copy.push({ role: 'assistant', content: '', ...patch })
      }
      return copy
    })
  }

  async function sendNonStream(
    q: string,
    history: { role: string; content: string }[],
  ) {
    const res = await api.chat(project, q, history)
    setMessages((m) => [
      ...m.filter((x, i) => !(i === m.length - 1 && x.role === 'assistant' && !x.content)),
      { role: 'assistant', content: res.content, sources: res.sources, model: res.model },
    ])
    onPersist?.({ role: 'assistant', content: res.content, sources: res.sources })
  }

  function stop() {
    abortRef.current?.abort()
  }

  async function send(e?: { preventDefault(): void }) {
    e?.preventDefault()
    const q = input.trim()
    if (!q || busy) return
    setBusy(true)
    setErr('')
    const history = messages.map((m) => ({ role: m.role, content: m.content }))
    setMessages((m) => [...m, { role: 'user', content: q }])
    setInput('')
    onPersist?.({ role: 'user', content: q })

    const ac = new AbortController()
    abortRef.current = ac
    let assistant = ''
    let model = ''
    let sawEvent = false
    const sources: ChatSource[] = []
    let tools: ToolCall[] = []
    try {
      for await (const ev of api.chatStream(project, q, history, {
        signal: ac.signal,
        mode: prefs.mode || undefined,
        model: prefs.model || undefined,
      })) {
        if (ev.type === 'sources') {
          sawEvent = true
          if (ev.model) model = ev.model
          sources.push(...(ev.sources || []))
          // Sources may arrive after the tokens (agent mode), so re-attach them
          // to the current assistant message instead of only during chunks.
          patchAssistant({ sources: [...sources], model })
        } else if (ev.type === 'chunk') {
          sawEvent = true
          assistant += ev.content
          patchAssistant({ content: assistant, sources: [...sources], model })
        } else if (ev.type === 'tool_call') {
          sawEvent = true
          tools = [...tools, { id: ev.id, name: ev.name, args: ev.args }]
          patchAssistant({ tools, model })
        } else if (ev.type === 'tool_result') {
          sawEvent = true
          tools = applyToolResult(tools, ev)
          patchAssistant({ tools, model })
        } else if (ev.type === 'error') {
          sawEvent = true
          setErr(ev.message)
        }
      }
      if (!sawEvent) {
        // empty stream body — fall back to non-stream once
        await sendNonStream(q, history)
      } else if (assistant) {
        onPersist?.({ role: 'assistant', content: assistant, sources })
      }
    } catch (ex) {
      if (isAbortError(ex)) {
        // User pressed Stop: keep the partial answer and do NOT fall back to
        // the non-stream endpoint (that would re-ask the question).
        if (assistant) {
          onPersist?.({ role: 'assistant', content: assistant, sources })
        }
      } else {
        // Stream endpoint failed (e.g. older server, proxy) — try non-stream once.
        try {
          await sendNonStream(q, history)
        } catch {
          setErr(ex instanceof ApiError ? ex.message : 'chat failed')
        }
      }
    } finally {
      abortRef.current = null
      setBusy(false)
    }
  }

  function onComposerKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      void send()
    }
  }

  if (chatOk === false) {
    return (
      <Card>
        <h2 className="mb-2 text-[1.1rem] font-bold">Chat not configured</h2>
        <p className="m-0 text-muted">
          Set <Code>GEMINI_API_KEY</Code> or <Code>OPENROUTER_API_KEY</Code> on the
          server and restart <Code>semidx serve</Code>. You can still use Explore
          for semantic search without an LLM.
        </p>
      </Card>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <Card
        className="flex max-h-[55vh] flex-col gap-3 overflow-auto"
        role="log"
        aria-live="polite"
        aria-atomic="false"
      >
        {messages.length === 0 && (
          <p className="m-0 text-muted">
            {global ? (
              <>
                Ask anything across <strong>all indexed projects</strong>. Answers
                cite the project each source came from.
              </>
            ) : (
              <>
                Ask anything about <strong>{project}</strong>. Answers use RAG over
                the index; sources open in the Files tab.
              </>
            )}
          </p>
        )}
        {messages.map((m, i) => (
          <div key={i}>
            <div className="mb-1 flex items-center gap-2 text-xs font-bold tracking-wide text-muted uppercase">
              {m.role}
              {m.role === 'assistant' && m.model && (
                <span className="font-mono font-normal normal-case">{m.model}</span>
              )}
            </div>
            {m.tools && m.tools.length > 0 && (
              <div className="mb-1">
                {m.tools.map((t, j) => (
                  <ToolCallChip key={t.id ?? `${t.name}-${j}`} call={t} />
                ))}
              </div>
            )}
            {m.role === 'assistant' ? (
              <Markdown>{m.content}</Markdown>
            ) : (
              <div className="rounded-md bg-accent/10 px-3 py-2 text-sm whitespace-pre-wrap">
                {m.content}
              </div>
            )}
            {m.sources && m.sources.length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-x-3.5 gap-y-2">
                {m.sources.map((s, j) => {
                  // Real deep-link (authenticated SPA route) so a source can be
                  // opened in a new tab / copied, while a plain click still does
                  // in-app navigation. Global-chat sources carry their project.
                  const proj = s.project || project
                  const href = proj
                    ? `/admin/projects/${encodeURIComponent(proj)}?tab=files&path=${encodeURIComponent(s.file)}${s.start_line ? `&line=${s.start_line}` : ''}`
                    : undefined
                  return (
                    <a
                      key={j}
                      className="text-xs text-accent hover:underline"
                      href={href ?? '#'}
                      onClick={(e) => {
                        e.preventDefault()
                        onOpenFile(s.file, s.start_line, s.project)
                      }}
                    >
                      {s.project ? `${s.project} · ` : ''}
                      {s.file}:{s.start_line}
                    </a>
                  )
                })}
              </div>
            )}
          </div>
        ))}
        {err && <Alert kind="error">{err}</Alert>}
      </Card>
      <Card>
        <form onSubmit={(e) => void send(e)}>
          <Textarea
            rows={3}
            className="mb-2"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={onComposerKeyDown}
            placeholder="How does authentication work in this project? (Ctrl/⌘+Enter to send)"
          />
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={busy || !input.trim()}>
              {busy ? 'Thinking…' : 'Send'}
            </Button>
            {busy && (
              <Button variant="secondary" onClick={stop}>
                Stop
              </Button>
            )}
            {prefs.enabled && (
              <span className="ml-auto flex flex-wrap items-center gap-2">
                {(prefs.config?.modes?.length ?? 0) > 0 && (
                  <label
                    htmlFor="chat-mode"
                    className="flex items-center gap-1.5 text-xs text-muted"
                  >
                    mode
                    <Select
                      id="chat-mode"
                      className="w-auto text-xs"
                      value={prefs.mode}
                      onChange={(e) => prefs.setMode(e.target.value)}
                    >
                      <option value="">default</option>
                      {prefs.config?.modes?.map((m) => (
                        <option key={m} value={m}>
                          {m}
                        </option>
                      ))}
                    </Select>
                  </label>
                )}
                {(prefs.config?.models?.length ?? 0) > 0 && (
                  <label
                    htmlFor="chat-model"
                    className="flex items-center gap-1.5 text-xs text-muted"
                  >
                    model
                    <Select
                      id="chat-model"
                      className="w-auto text-xs"
                      value={prefs.model}
                      onChange={(e) => prefs.setModel(e.target.value)}
                    >
                      <option value="">default</option>
                      {prefs.config?.models?.map((m) => (
                        <option key={m} value={m}>
                          {m}
                        </option>
                      ))}
                    </Select>
                  </label>
                )}
              </span>
            )}
          </div>
        </form>
      </Card>
    </div>
  )
}
