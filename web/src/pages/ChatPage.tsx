import { useNavigate } from 'react-router-dom'
import { ChatPanel } from '../features/project/ChatPanel'

// ChatPage is the global (cross-project) chat: the agent searches every indexed
// project and cites the project each source came from. Clicking a source opens
// that file in its project's workspace.
export function ChatPage() {
  const navigate = useNavigate()
  const openFile = (path: string, line?: number, project?: string) => {
    if (!project) return
    const q = new URLSearchParams({ tab: 'files', path })
    if (line) q.set('line', String(line))
    navigate(`/projects/${encodeURIComponent(project)}?${q.toString()}`)
  }
  return (
    <div>
      <div className="page-head">
        <div>
          <h1>Chat</h1>
          <p className="muted">
            Ask across all indexed projects — each source cites its project.
          </p>
        </div>
      </div>
      <ChatPanel project="" seedQuestion="" onOpenFile={openFile} />
    </div>
  )
}
