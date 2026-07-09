import { useEffect, useState } from 'react'
import { NavLink } from 'react-router-dom'
import { api, type SystemInfo } from './api'
import { useAuth } from './auth'

export function Layout({ children }: { children: React.ReactNode }) {
  const { user, logout } = useAuth()
  const [sys, setSys] = useState<SystemInfo | null>(null)

  useEffect(() => {
    void api.system().then(setSys).catch(() => setSys(null))
  }, [])

  return (
    <div className="app-shell">
      <header className="nav">
        <span className="brand">semidx</span>
        <NavLink to="/" end>
          Projects
        </NavLink>
        <NavLink to="/search">Search</NavLink>
        <NavLink to="/cli">CLI guide</NavLink>
        <span className="spacer" />
        {user && (
          <span className="muted user-chip">
            {user.username}
            <span className="pill">{user.role}</span>
          </span>
        )}
        <button type="button" className="link" onClick={() => void logout()}>
          Log out
        </button>
      </header>

      {sys && (
        <div className="system-banner">
          <strong>Server mode</strong>
          <span className="pill">{sys.mode}</span>
          <span className="muted">{sys.storage}</span>
          <span className="muted caps">
            caps: {sys.caps.join(', ')}
          </span>
        </div>
      )}

      <main className="content">{children}</main>
    </div>
  )
}
