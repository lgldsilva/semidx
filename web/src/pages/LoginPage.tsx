import { useState, type FormEvent } from 'react'
import { Navigate } from 'react-router-dom'
import { ApiError } from '../api'
import { useAuth } from '../auth'

export function LoginPage() {
  const { user, loading, login } = useAuth()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  if (!loading && user) return <Navigate to="/" replace />

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      await login(username, password, remember)
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="centered">
      <form className="card narrow login-card" onSubmit={(e) => void onSubmit(e)}>
        <h1>semidx admin</h1>
        <p className="muted">
          Same product surface as the CLI — projects, search, reindex — on the
          server store.
        </p>
        {err && <div className="alert error">{err}</div>}
        <label>
          Username
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
          />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>
        <label className="checkbox">
          <input
            type="checkbox"
            checked={remember}
            onChange={(e) => setRemember(e.target.checked)}
          />
          Remember me
        </label>
        <button type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
