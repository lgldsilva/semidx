import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, type ChatMessage, type Conversation } from '../api'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { ChatPanel } from '../features/project/ChatPanel'
import { cx } from '../lib/cx'

// ChatPage is the global (cross-project) chat with persistent, multiple
// conversations. The left panel lists the user's conversations; the right panel
// is the chat. Each turn is appended to the active conversation (lazily created
// on the first message). A ?conversation=<id> deep-link opens a specific thread.
export function ChatPage() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const [convs, setConvs] = useState<Conversation[]>([])
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [initial, setInitial] = useState<ChatMessage[] | undefined>([])
  // activeId mirrors selectedId for synchronous access inside persist().
  const activeId = useRef<number | null>(null)

  const openFile = (path: string, line?: number, project?: string) => {
    if (!project) return
    const q = new URLSearchParams({ tab: 'files', path })
    if (line) q.set('line', String(line))
    navigate(`/projects/${encodeURIComponent(project)}?${q.toString()}`)
  }

  const refresh = useCallback(async () => {
    try {
      setConvs(await api.conversations())
    } catch {
      /* listing failure is non-fatal */
    }
  }, [])

  const openConversation = useCallback(async (id: number) => {
    const d = await api.conversation(id)
    setSelectedId(id)
    activeId.current = id
    setParams({ conversation: String(id) }, { replace: true })
    setInitial(
      d.messages.map((m) => ({
        role: m.role,
        content: m.content,
        sources: m.sources,
      })),
    )
  }, [setParams])

  useEffect(() => {
    void api
      .system()
      .then((s) => {
        const ok = !!s.chat_enabled && (s.caps || []).includes('conversations')
        setEnabled(ok)
        if (ok) void refresh()
      })
      .catch(() => setEnabled(false))
  }, [refresh])

  // Honor a ?conversation=<id> deep-link once conversations are available.
  useEffect(() => {
    const id = Number(params.get('conversation') || 0)
    if (enabled && id && id !== activeId.current) {
      void openConversation(id).catch(() => {
        /* stale/foreign id: fall back to a new chat */
      })
    }
  }, [enabled, params, openConversation])

  function newChat() {
    setSelectedId(null)
    activeId.current = null
    setInitial([])
    setParams({}, { replace: true })
  }

  // persist saves one turn, lazily creating a conversation on the first message.
  const persist = async (m: ChatMessage) => {
    let id = activeId.current
    if (id == null) {
      const title = m.role === 'user' ? m.content.slice(0, 48) : 'New chat'
      const conv = await api.createConversation('', title)
      id = conv.id
      activeId.current = id
      setSelectedId(id)
      setConvs((c) => [conv, ...c])
      setParams({ conversation: String(id) }, { replace: true })
    }
    try {
      await api.addMessage(id, m.role, m.content, m.sources)
    } catch {
      /* persistence failure shouldn't break the live chat */
    }
  }

  async function remove(id: number) {
    try {
      await api.deleteConversation(id)
    } catch {
      return
    }
    setConvs((c) => c.filter((x) => x.id !== id))
    if (id === activeId.current) newChat()
  }

  async function rename(id: number) {
    const title = window.prompt('Rename conversation')?.trim()
    if (!title) return
    try {
      await api.renameConversation(id, title)
    } catch {
      return
    }
    setConvs((c) => c.map((x) => (x.id === id ? { ...x, title } : x)))
  }

  if (enabled === false) {
    // Conversations unsupported (e.g. non-Postgres store): plain global chat.
    return (
      <div>
        <div className="mb-2">
          <h1 className="mb-1 text-[1.45rem] font-bold">Chat</h1>
          <p className="m-0 text-muted">Ask across all indexed projects.</p>
        </div>
        <ChatPanel project="" seedQuestion="" onOpenFile={openFile} />
      </div>
    )
  }

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="mb-1 text-[1.45rem] font-bold">Chat</h1>
          <p className="m-0 text-muted">
            Ask across all indexed projects — conversations are saved.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
          {selectedId != null && (
            <Button
              variant="link"
              onClick={() => void navigator.clipboard?.writeText(window.location.href)}
              title="Copy a shareable link to this conversation"
            >
              Copy link
            </Button>
          )}
          <Button onClick={newChat}>New chat</Button>
        </div>
      </div>
      <div className="grid min-h-[420px] gap-3.5 md:grid-cols-[minmax(220px,32%)_1fr]">
        <Card>
          <div className="max-h-[55vh] overflow-auto">
            {convs.length === 0 && (
              <p className="text-xs text-muted">No saved conversations yet.</p>
            )}
            {convs.map((cv) => (
              <div
                key={cv.id}
                className={cx(
                  'flex items-center justify-between gap-1.5 rounded-md px-1 py-0.5 hover:bg-accent/10',
                  cv.id === selectedId && 'bg-accent/10',
                )}
              >
                <button
                  type="button"
                  className="min-w-0 flex-1 cursor-pointer overflow-hidden rounded border-0 bg-transparent px-1 py-0.5 text-left font-[inherit] text-sm text-ellipsis whitespace-nowrap text-fg"
                  onClick={() => void openConversation(cv.id)}
                  title={cv.title}
                >
                  {cv.title}
                </button>
                <span className="inline-flex shrink-0 gap-1">
                  <Button variant="link" size="sm" onClick={() => void rename(cv.id)}>
                    rename
                  </Button>
                  <Button
                    variant="link"
                    size="sm"
                    className="text-danger"
                    onClick={() => void remove(cv.id)}
                  >
                    delete
                  </Button>
                </span>
              </div>
            ))}
          </div>
        </Card>
        <ChatPanel
          project=""
          seedQuestion=""
          onOpenFile={openFile}
          initialMessages={initial}
          onPersist={(m) => void persist(m)}
        />
      </div>
    </div>
  )
}
