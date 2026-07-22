import { useEffect, useState } from 'react'
import { NavLink } from 'react-router-dom'
import { api, getWorkspaceSelection, setWorkspaceSelection, type SystemInfo, type Workspace } from './api'
import { Badge } from './components/Badge'
import { Button } from './components/Button'
import { ThemeToggle } from './components/ThemeToggle'
import { cx } from './lib/cx'
import { Select } from './components/Input'
import { useAuth } from './auth'

const NAV_LINK = 'no-underline hover:underline'

function navClass({ isActive }: { isActive: boolean }) {
  return cx(NAV_LINK, isActive ? 'font-semibold text-accent' : 'text-fg')
}

export function Layout({ children }: Readonly<{ children: React.ReactNode }>) {
  const { user, logout } = useAuth()
  const [sys, setSys] = useState<SystemInfo | null>(null)
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [workspace, setWorkspace] = useState(getWorkspaceSelection)

  useEffect(() => {
    void api.system().then(setSys).catch(() => setSys(null))
    void api.workspaces().then((items) => {
      setWorkspaces(items)
      if (items.length && !items.some((item) => item.slug === getWorkspaceSelection())) {
        setWorkspace(items[0].slug)
        setWorkspaceSelection(items[0].slug)
      }
    }).catch(() => setWorkspaces([]))
  }, [])

  function onWorkspaceChange(slug: string) {
    setWorkspace(slug)
    setWorkspaceSelection(slug)
    window.location.reload()
  }

  return (
    <div className="min-h-screen">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-surface px-5 py-3 text-sm max-sm:px-3.5">
        <span className="font-bold">semidx</span>
        <NavLink to="/" end className={navClass}>
          Projects
        </NavLink>
        <NavLink to="/search" className={navClass}>
          Search
        </NavLink>
        <NavLink to="/chat" className={navClass}>
          Chat
        </NavLink>
        <NavLink to="/jobs" className={navClass}>
          Jobs
        </NavLink>
        <NavLink to="/settings" className={navClass}>
          Settings
        </NavLink>
        <NavLink to="/cli" className={navClass}>
          CLI guide
        </NavLink>
        <span className="flex-1" />
        {workspaces.length > 0 && (
          <label className="flex items-center gap-1.5 text-muted" htmlFor="workspace-select">
            Workspace
            <Select
              id="workspace-select"
              className="py-1 text-xs"
              value={workspace}
              onChange={(e) => onWorkspaceChange(e.target.value)}
            >
              {workspaces.map((item) => (
                <option key={item.slug} value={item.slug}>{item.name}</option>
              ))}
            </Select>
          </label>
        )}
        <ThemeToggle />
        {user && (
          <span className="inline-flex items-center gap-1.5 text-muted">
            {user.username}
            <Badge>{user.role}</Badge>
          </span>
        )}
        <Button variant="link" size="sm" onClick={() => void logout()}>
          Log out
        </Button>
      </header>

      {sys && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-accent/20 bg-accent/10 px-5 py-2 text-sm max-sm:px-3.5">
          <strong>Server mode</strong>
          <Badge>{sys.mode}</Badge>
          <span className="text-muted">{sys.storage}</span>
          <span className="ml-auto text-muted max-sm:ml-0">caps: {sys.caps.join(', ')}</span>
        </div>
      )}

      <main className="mx-auto my-[1.4rem] max-w-[1100px] px-[1.2rem] pb-10 max-sm:mt-4 max-sm:px-3 max-sm:pb-8">
        {children}
      </main>
    </div>
  )
}
