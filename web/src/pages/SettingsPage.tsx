import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type TokenRow, type UserRow } from '../api'
import { useAuth } from '../auth'

export function SettingsPage() {
  const { user } = useAuth()
  const [tab, setTab] = useState<'keys' | 'tokens' | 'account' | 'users'>('keys')

  return (
    <div>
      <h1>Settings</h1>
      <p className="muted">
        API keys for <code>semidx login</code>, control tokens, account, and users.
      </p>
      <div className="tab-nav" role="tablist" aria-label="Settings sections">
        {(
          [
            ['keys', 'API keys'],
            ['tokens', 'Control tokens'],
            ['account', 'Account'],
            ...(user?.role === 'admin' ? [['users', 'Users'] as const] : []),
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            className={`tab-btn ${tab === id ? 'active' : ''}`}
            onClick={() => setTab(id)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'keys' && <KeysPanel />}
      {tab === 'tokens' && <TokensPanel />}
      {tab === 'account' && <AccountPanel />}
      {tab === 'users' && user?.role === 'admin' && <UsersPanel />}
    </div>
  )
}

function ScopePicker({
  scopes,
  setScopes,
  allowAdmin,
}: {
  scopes: string[]
  setScopes: (s: string[]) => void
  allowAdmin: boolean
}) {
  const toggle = (s: string) => {
    setScopes(
      scopes.includes(s) ? scopes.filter((x) => x !== s) : [...scopes, s],
    )
  }
  const all = allowAdmin ? ['read', 'write', 'admin'] : ['read', 'write']
  return (
    <div className="row-actions">
      {all.map((s) => (
        <label key={s} className="checkbox">
          <input
            type="checkbox"
            checked={scopes.includes(s)}
            onChange={() => toggle(s)}
          />
          {s}
        </label>
      ))}
    </div>
  )
}

function KeysPanel() {
  const { user } = useAuth()
  const [keys, setKeys] = useState<TokenRow[]>([])
  const [name, setName] = useState('cli')
  const [scopes, setScopes] = useState<string[]>(['read', 'write'])
  const [fresh, setFresh] = useState('')
  const [err, setErr] = useState('')
  const [flash, setFlash] = useState('')

  const reload = useCallback(async () => {
    setKeys(await api.listKeys())
  }, [])

  useEffect(() => {
    void reload().catch((e) =>
      setErr(e instanceof ApiError ? e.message : 'load failed'),
    )
  }, [reload])

  async function create(e: FormEvent) {
    e.preventDefault()
    setErr('')
    setFresh('')
    try {
      const res = await api.createKey(name, scopes.length ? scopes : ['read'])
      setFresh(res.token)
      setFlash(res.message)
      await reload()
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'create failed')
    }
  }

  return (
    <div className="card">
      <h2>API keys</h2>
      <p className="muted">
        Opaque tokens for CLI: <code>semidx login &lt;url&gt; --token …</code>
      </p>
      {err && <div className="alert error">{err}</div>}
      {flash && <div className="alert ok">{flash}</div>}
      {fresh && (
        <code className="key" style={{ display: 'block', margin: '0.5rem 0' }}>
          {fresh}
        </code>
      )}
      <form onSubmit={(e) => void create(e)} className="create-form">
        <div className="row">
          <label className="grow">
            Name
            <input value={name} onChange={(e) => setName(e.target.value)} required />
          </label>
        </div>
        <ScopePicker
          scopes={scopes}
          setScopes={setScopes}
          allowAdmin={user?.role === 'admin'}
        />
        <button type="submit">Create key</button>
      </form>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Scopes</th>
            <th aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {keys.map((k) => (
            <tr key={k.id}>
              <td>{k.name}</td>
              <td>{(k.scopes || []).join(', ')}</td>
              <td>
                <button
                  type="button"
                  className="link danger-text"
                  onClick={() =>
                    void api.revokeKey(k.id).then(reload).catch((e) =>
                      setErr(e instanceof ApiError ? e.message : 'revoke failed'),
                    )
                  }
                >
                  Revoke
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function TokensPanel() {
  const { user } = useAuth()
  const [enabled, setEnabled] = useState(false)
  const [tokens, setTokens] = useState<TokenRow[]>([])
  const [name, setName] = useState('control')
  const [scopes, setScopes] = useState<string[]>(['read', 'write'])
  const [ttl, setTtl] = useState(30)
  const [fresh, setFresh] = useState('')
  const [err, setErr] = useState('')

  const reload = useCallback(async () => {
    const r = await api.listTokens()
    setEnabled(r.enabled)
    setTokens(r.tokens || [])
  }, [])

  useEffect(() => {
    void reload().catch((e) =>
      setErr(e instanceof ApiError ? e.message : 'load failed'),
    )
  }, [reload])

  if (!enabled) {
    return (
      <div className="card">
        <h2>Control tokens</h2>
        <p className="muted">
          Disabled. Set <code>SEMIDX_JWT_SECRET</code> on the server to enable JWT control tokens.
        </p>
      </div>
    )
  }

  async function create(e: FormEvent) {
    e.preventDefault()
    setErr('')
    setFresh('')
    try {
      const res = await api.createToken(name, scopes, ttl)
      setFresh(res.token)
      await reload()
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'create failed')
    }
  }

  return (
    <div className="card">
      <h2>Control tokens (JWT)</h2>
      {err && <div className="alert error">{err}</div>}
      {fresh && (
        <code className="key" style={{ display: 'block', margin: '0.5rem 0' }}>
          {fresh}
        </code>
      )}
      <form onSubmit={(e) => void create(e)}>
        <div className="row">
          <label className="grow">
            Name
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label>
            TTL days (0=never)
            <input
              type="number"
              value={ttl}
              onChange={(e) => setTtl(Number(e.target.value))}
            />
          </label>
        </div>
        <ScopePicker
          scopes={scopes}
          setScopes={setScopes}
          allowAdmin={user?.role === 'admin'}
        />
        <button type="submit">Mint token</button>
      </form>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Scopes</th>
            <th aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {tokens.map((t) => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td>{(t.scopes || []).join(', ')}</td>
              <td>
                <button
                  type="button"
                  className="link danger-text"
                  onClick={() =>
                    void api.revokeToken(t.id).then(reload).catch((e) =>
                      setErr(e instanceof ApiError ? e.message : 'revoke failed'),
                    )
                  }
                >
                  Revoke
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function AccountPanel() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [err, setErr] = useState('')
  const [ok, setOk] = useState('')

  async function save(e: FormEvent) {
    e.preventDefault()
    setErr('')
    setOk('')
    try {
      await api.changePassword(current, next)
      setOk('Password updated')
      setCurrent('')
      setNext('')
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'update failed')
    }
  }

  return (
    <form className="card narrow" onSubmit={(e) => void save(e)}>
      <h2>Change password</h2>
      {err && <div className="alert error">{err}</div>}
      {ok && <div className="alert ok">{ok}</div>}
      <label>
        Current
        <input
          type="password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          required
        />
      </label>
      <label>
        New (≥8 chars)
        <input
          type="password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
          minLength={8}
        />
      </label>
      <button type="submit">Update</button>
    </form>
  )
}

function UsersPanel() {
  const [users, setUsers] = useState<UserRow[]>([])
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('member')
  const [err, setErr] = useState('')

  const reload = useCallback(async () => {
    setUsers(await api.listUsers())
  }, [])

  useEffect(() => {
    void reload().catch((e) =>
      setErr(e instanceof ApiError ? e.message : 'load failed'),
    )
  }, [reload])

  async function create(e: FormEvent) {
    e.preventDefault()
    setErr('')
    try {
      await api.createUser(username, password, role)
      setUsername('')
      setPassword('')
      await reload()
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'create failed')
    }
  }

  return (
    <div className="card">
      <h2>Users</h2>
      {err && <div className="alert error">{err}</div>}
      <form onSubmit={(e) => void create(e)} className="row">
        <label>
          Username
          <input value={username} onChange={(e) => setUsername(e.target.value)} required />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={8}
          />
        </label>
        <label>
          Role
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="member">member</option>
            <option value="admin">admin</option>
          </select>
        </label>
        <button type="submit">Create user</button>
      </form>
      <table>
        <thead>
          <tr>
            <th>User</th>
            <th>Role</th>
            <th>Status</th>
            <th aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>{u.username}</td>
              <td>{u.role}</td>
              <td>{u.disabled ? 'disabled' : 'active'}</td>
              <td>
                <button
                  type="button"
                  className="link"
                  onClick={() =>
                    void api
                      .setUserDisabled(u.id, !u.disabled)
                      .then(reload)
                      .catch((e) =>
                        setErr(e instanceof ApiError ? e.message : 'update failed'),
                      )
                  }
                >
                  {u.disabled ? 'Enable' : 'Disable'}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
