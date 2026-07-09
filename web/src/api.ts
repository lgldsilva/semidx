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
  cli_hints: string[]
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
  total_files?: number
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
  project_count?: number
  resolved_project?: string
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
}

export type MeResponse = {
  user: User
  csrf: string
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

async function parseError(res: Response): Promise<string> {
  try {
    const body = await res.json()
    if (body && typeof body.error === 'string') return body.error
  } catch {
    /* ignore */
  }
  return res.statusText || `HTTP ${res.status}`
}

async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
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
  createProject: (body: {
    name: string
    model: string
    source_type: string
    git_url?: string
    branch?: string
    index?: boolean
  }) =>
    request<{ project: Project; job_id?: number }>('/admin/api/projects', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  deleteProject: (name: string) =>
    request<{ ok: boolean }>(
      `/admin/api/projects/${encodeURIComponent(name)}`,
      { method: 'DELETE' },
    ),
  projectStatus: (name: string) =>
    request<Project & { total_files: number }>(
      `/admin/api/projects/${encodeURIComponent(name)}/status`,
    ),
  reindex: (name: string, type = 'full') =>
    request<{ job_id: number; status: string }>(
      `/admin/api/projects/${encodeURIComponent(name)}/reindex`,
      {
        method: 'POST',
        body: JSON.stringify({ type }),
      },
    ),
  job: (id: number) => request<Job>(`/admin/api/jobs/${id}`),
  search: (body: {
    query: string
    project?: string
    all?: boolean
    top?: number
  }) =>
    request<SearchResponse>('/admin/api/search', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
}

export { ApiError }
