/** Session-authenticated admin API client (cookie + CSRF). */

export type User = {
  id: number
  username: string
  role: string
}

export type SystemInfo = {
  product: string
  mode: string
  storage: string
  version: string
  caps: string[]
  chat_enabled?: boolean
  cli_hints: string[]
}

export type Job = {
  id: number
  type: string
  status: string
  error?: string
  files_indexed?: number
  chunks_created?: number
  deleted_files?: number
  error_count?: number
  progress_done?: number
  progress_total?: number
  progress_percent?: number
}

export type Project = {
  name: string
  identity?: string
  path?: string
  model: string
  status: string
  source_type?: string
  git_url?: string
  branch?: string
  dims?: number
  license?: string
  total_files?: number
  total_chunks?: number
  last_commit?: string
  last_job?: Job
  ext_breakdown?: Record<string, number>
}

export type ProjectDetail = {
  project: Project
  jobs: Job[]
}

export type FileEntry = { path: string; hash: string }

export type FileContent = {
  path: string
  dims: number
  content: string
  truncated: boolean
  chunks: { start_line: number; end_line: number; content: string }[]
}

export type SearchHit = {
  project?: string
  path: string
  start_line: number
  end_line: number
  score: number
  content: string
}

export type SearchResponse = {
  results: SearchHit[]
  fallback: boolean
  /** True when the embed circuit is open and only keyword results are served. */
  degraded?: boolean
  /** Hint (ms) for when the embedding provider may be retried. */
  retry_after_ms?: number
  project_count?: number
  resolved_project?: string
}

export type ChatSource = {
  file: string
  start_line: number
  end_line: number
  content: string
  score: number
  keyword?: boolean
  project?: string // set only by the global (all-projects) chat
}

export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
  sources?: ChatSource[]
}

/** Mid-stream agent tool invocation (SSE `tool_call`). */
export type ChatToolCallEvent = {
  type: 'tool_call'
  id?: string
  name: string
  args?: Record<string, unknown>
}

/** Outcome of a tool invocation (SSE `tool_result`), correlated by id. */
export type ChatToolResultEvent = {
  type: 'tool_result'
  id?: string
  name: string
  preview?: string
  is_error?: boolean
  elapsed_ms?: number
  truncated?: boolean
}

export type ChatStreamEvent =
  | { type: 'sources'; sources: ChatSource[]; model?: string; fallback?: boolean }
  | { type: 'chunk'; content: string }
  | ChatToolCallEvent
  | ChatToolResultEvent
  | { type: 'error'; message: string }
  | { type: 'done' }

export type ChatStreamOptions = {
  /** Abort the fetch/stream (Stop button). */
  signal?: AbortSignal
  /** Preferred chat model (sent only when non-empty). */
  model?: string
  /** Chat mode, e.g. rag | agent (sent only when non-empty). */
  mode?: string
}

/** One selectable chat model from GET /admin/api/chat/config. */
export type ChatModelInfo = {
  id: string
  provider: string
  default?: boolean
}

/** GET /admin/api/chat/config — selector options for the chat UI. A 404
 * (older server) or enabled:false hides the mode/model selector entirely. */
export type ChatConfig = {
  enabled: boolean
  modes?: string[]
  models?: ChatModelInfo[]
  default_mode?: string
  default_model?: string
  agent_actions?: string
}

export type Conversation = {
  id: number
  project: string
  title: string
  created_at: string
  updated_at: string
}

export type ConversationDetail = Conversation & {
  messages: {
    id: number
    role: 'user' | 'assistant'
    content: string
    sources?: ChatSource[]
  }[]
}

export type MeResponse = {
  user: User
  csrf: string
}

export type TokenRow = {
  id: number
  name: string
  scopes: string[]
  kind: string
  created_at?: string
  expires_at?: string
}

export type UserRow = {
  id: number
  username: string
  role: string
  disabled: boolean
}

export type GitCredentialRow = {
  id: number
  scope: 'project' | 'host'
  project_id?: number
  project_name?: string
  host?: string
  kind: 'https' | 'ssh'
  username: string
  label: string
  ssh_known_hosts?: string
  ssh_fingerprint?: string
  created_at: string
  updated_at: string
}

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

let csrfToken = ''

export function setCsrf(token: string) {
  csrfToken = token
}

export function getCsrf() {
  return csrfToken
}

// onUnauthorized is invoked whenever any request gets a 401, so the auth layer
// can clear the session and the router can redirect to /login. Without this a
// mid-session expiry left each page showing a generic error and stranded.
let onUnauthorized: (() => void) | null = null

export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn
}

async function parseError(res: Response): Promise<string> {
  try {
    const body = await res.json()
    if (body && typeof body.error === 'string') return body.error
  } catch {
    /* ignore */
  }
  return res.statusText || `HTTP ${res.status}`
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const method = (init.method || 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  })
  if (res.status === 401) {
    if (onUnauthorized) onUnauthorized()
    throw new ApiError(401, 'unauthorized')
  }
  if (!res.ok) {
    throw new ApiError(res.status, await parseError(res))
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

// chatBase returns the chat endpoint prefix: a named project scopes the chat to
// that project; an empty name is the global (all-projects) chat.
function chatBase(name: string): string {
  return name ? `/admin/api/projects/${encodeURIComponent(name)}` : '/admin/api'
}

export const api = {
  me: () => request<MeResponse>('/admin/api/me'),
  system: () => request<SystemInfo>('/admin/api/system'),
  login: (username: string, password: string, remember: boolean) =>
    request<MeResponse>('/admin/api/login', {
      method: 'POST',
      body: JSON.stringify({
        username,
        password,
        remember_me: remember,
      }),
    }),
  logout: () =>
    request<{ ok: boolean }>('/admin/api/logout', { method: 'POST' }),
  projects: () =>
    request<{ projects: Project[] }>('/admin/api/projects').then(
      (r) => r.projects ?? [],
    ),
  projectDetail: (name: string) =>
    request<ProjectDetail>(
      `/admin/api/projects/${encodeURIComponent(name)}`,
    ),
  createProject: (body: {
    name: string
    model: string
    source_type: string
    git_url?: string
    branch?: string
    index?: boolean
  }) =>
    request<{ project: Project; job_id?: number; push_hint?: string }>(
      '/admin/api/projects',
      {
        method: 'POST',
        body: JSON.stringify(body),
      },
    ),
  listJobs: (limit = 20) =>
    request<{
      jobs: (Job & { project_id: number; project_name?: string })[]
    }>(`/admin/api/jobs?limit=${limit}`).then((r) => r.jobs ?? []),
  listKeys: () =>
    request<{ keys: TokenRow[] }>('/admin/api/keys').then((r) => r.keys ?? []),
  createKey: (name: string, scopes: string[]) =>
    request<{ token: string; id: number; message: string }>('/admin/api/keys', {
      method: 'POST',
      body: JSON.stringify({ name, scopes }),
    }),
  revokeKey: (id: number) =>
    request<{ ok: boolean }>(`/admin/api/keys/${id}`, { method: 'DELETE' }),
  listTokens: () =>
    request<{ enabled: boolean; tokens: TokenRow[] }>('/admin/api/tokens'),
  createToken: (name: string, scopes: string[], ttl_days: number) =>
    request<{ token: string; id: number; message: string }>('/admin/api/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, scopes, ttl_days }),
    }),
  revokeToken: (id: number) =>
    request<{ ok: boolean }>(`/admin/api/tokens/${id}`, { method: 'DELETE' }),
  changePassword: (current: string, next: string) =>
    request<{ ok: boolean }>('/admin/api/account/password', {
      method: 'POST',
      body: JSON.stringify({ current, new: next }),
    }),
  listUsers: () =>
    request<{ users: UserRow[] }>('/admin/api/users').then((r) => r.users ?? []),
  createUser: (username: string, password: string, role: string) =>
    request<{ ok: boolean }>('/admin/api/users', {
      method: 'POST',
      body: JSON.stringify({ username, password, role }),
    }),
  setUserDisabled: (id: number, disabled: boolean) =>
    request<{ ok: boolean }>(`/admin/api/users/${id}/disabled`, {
      method: 'POST',
      body: JSON.stringify({ disabled }),
    }),
  listGitCredentials: () =>
    request<{ credentials: GitCredentialRow[] }>('/admin/api/git-credentials').then(
      (r) => r.credentials ?? [],
    ),
  createGitCredential: (body: {
    project_id?: number
    host?: string
    kind: string
    username?: string
    secret: string
    label?: string
    ssh_known_hosts?: string
  }) =>
    request<GitCredentialRow>('/admin/api/git-credentials', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateGitCredential: (
    id: number,
    body: {
      kind: string
      username?: string
      secret?: string
      label?: string
      ssh_known_hosts?: string
    },
  ) =>
    request<GitCredentialRow>(`/admin/api/git-credentials/${id}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteGitCredential: (id: number) =>
    request<void>(`/admin/api/git-credentials/${id}`, { method: 'DELETE' }),
  projectCallers: (name: string, path: string) =>
    request<{ callers: string[]; count: number; package: string }>(
      `/admin/api/projects/${encodeURIComponent(name)}/callers?path=${encodeURIComponent(path)}`,
    ),
  projectDeps: (name: string, path: string) =>
    request<{ dependencies: string[]; count: number }>(
      `/admin/api/projects/${encodeURIComponent(name)}/deps?path=${encodeURIComponent(path)}`,
    ),
  projectGraphStats: (name: string) =>
    request<{
      nodes: number
      edges: number
      top_depends: { node: string; degree: number }[]
      top_depended: { node: string; degree: number }[]
    }>(`/admin/api/projects/${encodeURIComponent(name)}/graph-stats`),
  projectDeadCode: (name: string, limit = 100) =>
    request<{
      findings: {
        symbol: string
        kind: string
        file: string
        start_line: number
        confidence: string
      }[]
      stats: { total: number; confirmed: number; public_api: number }
      truncated: boolean
    }>(
      `/admin/api/projects/${encodeURIComponent(name)}/dead-code?limit=${limit}`,
    ),
  projectSbom: (name: string, format = 'cyclonedx-json') =>
    request<{
      format: string
      component_count: number
      document: Record<string, unknown>
      cli_equivalent: string
    }>(
      `/admin/api/projects/${encodeURIComponent(name)}/sbom?format=${encodeURIComponent(format)}`,
    ),
  deleteProject: (name: string) =>
    request<{ ok: boolean }>(
      `/admin/api/projects/${encodeURIComponent(name)}`,
      { method: 'DELETE' },
    ),
  projectStatus: (name: string) =>
    request<Project & { total_files: number }>(
      `/admin/api/projects/${encodeURIComponent(name)}/status`,
    ),
  projectFiles: (
    name: string,
    opts?: { prefix?: string; q?: string; limit?: number; offset?: number },
  ) => {
    const qs = new URLSearchParams()
    if (opts?.prefix) qs.set('prefix', opts.prefix)
    if (opts?.q) qs.set('q', opts.q)
    if (opts?.limit) qs.set('limit', String(opts.limit))
    if (opts?.offset) qs.set('offset', String(opts.offset))
    const q = qs.toString()
    const suffix = q ? '?' + q : ''
    return request<{ files: FileEntry[]; total: number }>(
      '/admin/api/projects/' + encodeURIComponent(name) + '/files' + suffix,
    )
  },
  projectFileContent: (name: string, path: string) =>
    request<FileContent>(
      `/admin/api/projects/${encodeURIComponent(name)}/files/content?path=${encodeURIComponent(path)}`,
    ),
  projectJobs: (name: string, limit = 10) =>
    request<{ jobs: Job[] }>(
      `/admin/api/projects/${encodeURIComponent(name)}/jobs?limit=${limit}`,
    ).then((r) => r.jobs ?? []),
  reindex: (name: string, type = 'full') =>
    request<{ job_id: number; status: string }>(
      `/admin/api/projects/${encodeURIComponent(name)}/reindex`,
      {
        method: 'POST',
        body: JSON.stringify({ type }),
      },
    ),
  job: (project: string, id: number) =>
    request<Job>(
      `/admin/api/projects/${encodeURIComponent(project)}/jobs/${id}`,
    ),
  search: (body: {
    query: string
    project?: string
    all?: boolean
    top?: number
    graph?: boolean
    graph_depth?: number
  }) =>
    request<SearchResponse>('/admin/api/search', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  projectIngest: (
    name: string,
    files: { path: string; content: string }[],
    del: string[] = [],
  ) =>
    request<{
      indexed: number
      chunks: number
      deleted: number
      errors: number
      file_errors?: { path: string; error: string }[]
    }>(`/admin/api/projects/${encodeURIComponent(name)}/files/batch`, {
      method: 'POST',
      body: JSON.stringify({ files, delete: del }),
    }),
  projectIngestArchive: (name: string, archive: File) => {
    const form = new FormData()
    form.append('archive', archive)
    return request<{
      indexed: number
      chunks: number
      deleted: number
      errors: number
      file_errors?: { path: string; error: string }[]
    }>(`/admin/api/projects/${encodeURIComponent(name)}/files/archive`, {
      method: 'POST',
      body: form,
    })
  },
  projectExplain: (name: string, path: string, line: number) =>
    request<{
      symbol?: string
      kind?: string
      path: string
      start_line?: number
      end_line?: number
      dependencies?: string[]
      importers?: string[]
      tests?: string[]
      source?: string
      snippet?: string
    }>(
      `/admin/api/projects/${encodeURIComponent(name)}/analyze/explain?path=${encodeURIComponent(path)}&line=${line}`,
    ),
  conversations: () =>
    request<{ conversations: Conversation[] }>('/admin/api/conversations').then(
      (r) => r.conversations ?? [],
    ),
  createConversation: (project: string, title: string) =>
    request<Conversation>('/admin/api/conversations', {
      method: 'POST',
      body: JSON.stringify({ project, title }),
    }),
  conversation: (id: number) =>
    request<ConversationDetail>(`/admin/api/conversations/${id}`),
  renameConversation: (id: number, title: string) =>
    request<{ ok: boolean }>(`/admin/api/conversations/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ title }),
    }),
  deleteConversation: (id: number) =>
    request<{ ok: boolean }>(`/admin/api/conversations/${id}`, {
      method: 'DELETE',
    }),
  addMessage: (
    id: number,
    role: 'user' | 'assistant',
    content: string,
    sources?: ChatSource[],
  ) =>
    request<{ id: number }>(`/admin/api/conversations/${id}/messages`, {
      method: 'POST',
      body: JSON.stringify({ role, content, sources: sources ?? [] }),
    }),
  chatConfig: () => request<ChatConfig>('/admin/api/chat/config'),
  chat: (
    name: string,
    question: string,
    history: { role: string; content: string }[],
  ) =>
    request<{
      content: string
      model: string
      sources: ChatSource[]
      fallback?: boolean
    }>(`${chatBase(name)}/chat`, {
      method: 'POST',
      body: JSON.stringify({ question, history }),
    }),
  chatStream: async function* (
    name: string,
    question: string,
    history: { role: string; content: string }[],
    opts: ChatStreamOptions = {},
  ): AsyncGenerator<ChatStreamEvent> {
    const res = await fetch(
      `${chatBase(name)}/chat/stream`,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
        },
        credentials: 'same-origin',
        signal: opts.signal,
        body: JSON.stringify({
          question,
          history,
          ...(opts.model ? { model: opts.model } : {}),
          ...(opts.mode ? { mode: opts.mode } : {}),
        }),
      },
    )
    if (!res.ok) {
      throw new ApiError(res.status, await parseError(res))
    }
    const reader = res.body?.getReader()
    if (!reader) {
      throw new ApiError(500, 'no stream body')
    }
    const decoder = new TextDecoder()
    let buf = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      const parts = buf.split('\n\n')
      buf = parts.pop() ?? ''
      for (const part of parts) {
        const line = part
          .split('\n')
          .find((l) => l.startsWith('data: '))
        if (!line) continue
        try {
          yield JSON.parse(line.slice(6))
        } catch {
          /* ignore bad frames */
        }
      }
    }
  },
}

export { ApiError }
