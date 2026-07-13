import { useEffect, useState } from 'react'
import { api, ApiError, type ChatMessage } from '../../api'

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
  const [messages, setMessages] = useState<ChatMessage[]>(initialMessages ?? [])
  const [input, setInput] = useState(seedQuestion)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [chatOk, setChatOk] = useState<boolean | null>(null)

  useEffect(() => {
    void api.system().then((s) => setChatOk(!!s.chat_enabled)).catch(() => setChatOk(false))
  }, [])

  useEffect(() => {
    if (initialMessages) setMessages(initialMessages)
  }, [initialMessages])

  useEffect(() => {
    setInput(seedQuestion)
  }, [seedQuestion])

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
    let assistant = ''
    const sources: ChatMessage['sources'] = []
    try {
      for await (const ev of api.chatStream(project, q, history)) {
        if (ev.type === 'sources') {
          sources.push(...(ev.sources || []))
          // Sources may arrive after the tokens (agent mode), so re-attach them
          // to the current assistant message instead of only during chunks.
          setMessages((m) => {
            const copy = [...m]
            const last = copy[copy.length - 1]
            if (last?.role === 'assistant') {
              copy[copy.length - 1] = { ...last, sources: [...sources] }
            }
            return copy
          })
        } else if (ev.type === 'chunk') {
          assistant += ev.content
          setMessages((m) => {
            const copy = [...m]
            const last = copy[copy.length - 1]
            if (last?.role === 'assistant') {
              copy[copy.length - 1] = { ...last, content: assistant, sources }
            } else {
              copy.push({ role: 'assistant', content: assistant, sources })
            }
            return copy
          })
        } else if (ev.type === 'error') {
          setErr(ev.error)
        }
      }
      if (!assistant) {
        // fallback non-stream
        const res = await api.chat(project, q, history)
        setMessages((m) => [
          ...m.filter((x, i) => !(i === m.length - 1 && x.role === 'assistant' && !x.content)),
          { role: 'assistant', content: res.content, sources: res.sources },
        ])
        onPersist?.({ role: 'assistant', content: res.content, sources: res.sources })
      } else {
        onPersist?.({ role: 'assistant', content: assistant, sources })
      }
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'chat failed')
    } finally {
      setBusy(false)
    }
  }

  if (chatOk === false) {
    return (
      <div className="card">
        <h2>Chat not configured</h2>
        <p className="muted">
          Set <code>GEMINI_API_KEY</code> or <code>OPENROUTER_API_KEY</code> on the
          server and restart <code>semidx serve</code>. You can still use Explore
          for semantic search without an LLM.
        </p>
      </div>
    )
  }

  return (
    <div className="chat-layout">
      <div className="chat-log card" role="log" aria-live="polite" aria-atomic="false">
        {messages.length === 0 && (
          <p className="muted">
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
          <div key={i} className={`chat-bubble ${m.role}`}>
            <div className="chat-role">{m.role}</div>
            <pre className="snippet">{m.content}</pre>
            {m.sources && m.sources.length > 0 && (
              <div className="chat-sources">
                {m.sources.map((s, j) => (
                  <button
                    key={j}
                    type="button"
                    className="link"
                    onClick={() => onOpenFile(s.file, s.start_line, s.project)}
                  >
                    {s.project ? `${s.project} · ` : ''}
                    {s.file}:{s.start_line}
                  </button>
                ))}
              </div>
            )}
          </div>
        ))}
        {err && <div className="alert error">{err}</div>}
      </div>
      <form className="chat-input card" onSubmit={(e) => void send(e)}>
        <textarea
          rows={3}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="How does authentication work in this project?"
        />
        <button type="submit" disabled={busy || !input.trim()}>
          {busy ? 'Thinking…' : 'Send'}
        </button>
      </form>
    </div>
  )
}
