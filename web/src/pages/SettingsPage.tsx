import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type TokenRow, type UserRow } from '../api'
import { Alert } from '../components/Alert'
import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { Checkbox, Input, Select } from '../components/Input'
import { Code } from '../components/Snippet'
import { Table } from '../components/Table'
import { Tabs, type TabItem } from '../components/Tabs'
import { useAuth } from '../auth'

type SettingsTab = 'keys' | 'tokens' | 'account' | 'users'

const BASE_TABS: ReadonlyArray<TabItem<SettingsTab>> = [
  { id: 'keys', label: 'API keys' },
  { id: 'tokens', label: 'Control tokens' },
  { id: 'account', label: 'Account' },
]

const H2 = 'mb-2 text-[1.1rem] font-bold'
const LABEL = 'block text-sm font-medium'
const FORM_ROW = 'flex flex-wrap items-end gap-3.5'
// Red text link for destructive row actions (Button's `link` variant is accent).
const DANGER_LINK =
  'cursor-pointer border-0 bg-transparent px-1 py-0.5 text-sm font-medium text-danger hover:underline'

export function SettingsPage() {
  const { user } = useAuth()
  const [tab, setTab] = useState<SettingsTab>('keys')

  const tabs: ReadonlyArray<TabItem<SettingsTab>> =
    user?.role === 'admin' ? [...BASE_TABS, { id: 'users', label: 'Users' }] : BASE_TABS

  return (
    <div>
      <h1 className="mb-1 text-[1.45rem] font-bold">Settings</h1>
      <p className="m-0 text-muted">
        API keys for <Code>semidx login</Code>, control tokens, account, and users.
      </p>
      <Tabs
        tabs={tabs}
        active={tab}
        onSelect={setTab}
        label="Settings sections"
        className="mt-3 mb-4"
      />
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
    <div className="my-2 flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
      {all.map((s) => (
        <Checkbox
          key={s}
          label={s}
          checked={scopes.includes(s)}
          onChange={() => toggle(s)}
        />
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
    <Card className="my-3.5">
      <h2 className={H2}>API keys</h2>
      <p className="m-0 text-muted">
        Opaque tokens for CLI: <Code>semidx login &lt;url&gt; --token …</Code>
      </p>
      {err && <Alert kind="error">{err}</Alert>}
      {flash && <Alert kind="success">{flash}</Alert>}
      {fresh && <Code className="my-2 block w-fit break-all">{fresh}</Code>}
      <form onSubmit={(e) => void create(e)}>
        <div className={FORM_ROW}>
          <label htmlFor="key-name" className={`${LABEL} min-w-[180px] flex-1`}>
            Name
            <Input
              id="key-name"
              className="mt-1"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </label>
        </div>
        <ScopePicker
          scopes={scopes}
          setScopes={setScopes}
          allowAdmin={user?.role === 'admin'}
        />
        <Button type="submit">Create key</Button>
      </form>
      <Table className="mt-3">
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
                  className={DANGER_LINK}
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
      </Table>
    </Card>
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
      <Card className="my-3.5">
        <h2 className={H2}>Control tokens</h2>
        <p className="m-0 text-muted">
          Disabled. Set <Code>SEMIDX_JWT_SECRET</Code> on the server to enable JWT control tokens.
        </p>
      </Card>
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
    <Card className="my-3.5">
      <h2 className={H2}>Control tokens (JWT)</h2>
      {err && <Alert kind="error">{err}</Alert>}
      {fresh && <Code className="my-2 block w-fit break-all">{fresh}</Code>}
      <form onSubmit={(e) => void create(e)}>
        <div className={FORM_ROW}>
          <label htmlFor="token-name" className={`${LABEL} min-w-[180px] flex-1`}>
            Name
            <Input
              id="token-name"
              className="mt-1"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label htmlFor="token-ttl" className={LABEL}>
            TTL days (0=never)
            <Input
              id="token-ttl"
              className="mt-1"
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
        <Button type="submit">Mint token</Button>
      </form>
      <Table className="mt-3">
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
                  className={DANGER_LINK}
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
      </Table>
    </Card>
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
    <Card className="my-3.5 w-[min(380px,92vw)]">
      <form onSubmit={(e) => void save(e)}>
        <h2 className={H2}>Change password</h2>
        {err && <Alert kind="error">{err}</Alert>}
        {ok && <Alert kind="success">{ok}</Alert>}
        <label htmlFor="password-current" className={`${LABEL} my-2`}>
          Current
          <Input
            id="password-current"
            className="mt-1"
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </label>
        <label htmlFor="password-next" className={`${LABEL} my-2`}>
          New (≥8 chars)
          <Input
            id="password-next"
            className="mt-1"
            type="password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
            minLength={8}
          />
        </label>
        <Button type="submit" className="mt-1">
          Update
        </Button>
      </form>
    </Card>
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
    <Card className="my-3.5">
      <h2 className={H2}>Users</h2>
      {err && <Alert kind="error">{err}</Alert>}
      <form onSubmit={(e) => void create(e)} className={FORM_ROW}>
        <label htmlFor="user-name" className={LABEL}>
          Username
          <Input
            id="user-name"
            className="mt-1"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
        <label htmlFor="user-password" className={LABEL}>
          Password
          <Input
            id="user-password"
            className="mt-1"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={8}
          />
        </label>
        <label htmlFor="user-role" className={LABEL}>
          Role
          <Select
            id="user-role"
            className="mt-1"
            value={role}
            onChange={(e) => setRole(e.target.value)}
          >
            <option value="member">member</option>
            <option value="admin">admin</option>
          </Select>
        </label>
        <Button type="submit">Create user</Button>
      </form>
      <Table className="mt-3">
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
                <Button
                  variant="link"
                  size="sm"
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
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  )
}
