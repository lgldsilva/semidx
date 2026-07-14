import { useState, type FormEvent } from 'react'
import { Navigate } from 'react-router-dom'
import { ApiError } from '../api'
import { Alert } from '../components/Alert'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { Checkbox, Input } from '../components/Input'
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
    <div className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-[min(380px,92vw)]">
        <form onSubmit={(e) => void onSubmit(e)}>
          <h1 className="mb-1 text-[1.45rem] font-bold">semidx admin</h1>
          <p className="m-0 text-muted">
            Same product surface as the CLI — projects, search, reindex — on the
            server store.
          </p>
          {err && <Alert kind="error">{err}</Alert>}
          <label htmlFor="login-username" className="my-2 block text-sm font-medium">
            Username
            <Input
              id="login-username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              required
              className="mt-1"
            />
          </label>
          <label htmlFor="login-password" className="my-2 block text-sm font-medium">
            Password
            <Input
              id="login-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              className="mt-1"
            />
          </label>
          <Checkbox
            label="Remember me"
            checked={remember}
            onChange={(e) => setRemember(e.target.checked)}
            className="my-2.5"
          />
          <Button type="submit" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>
      </Card>
    </div>
  )
}
