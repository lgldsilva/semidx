import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import {
  api,
  ApiError,
  type ChatStreamEvent,
  type ChatStreamOptions,
  type SystemInfo,
} from '../../api'
import type { ToolCall } from '../../components/ToolCallChip'
import { applyToolResult, ChatPanel } from './ChatPanel'

const SYSTEM: SystemInfo = {
  product: 'semidx',
  mode: 'server',
  storage: 'postgres',
  version: 'test',
  caps: [],
  chat_enabled: true,
  cli_hints: [],
}

function mockSystem() {
  vi.spyOn(api, 'system').mockResolvedValue(SYSTEM)
  // Default: no /chat/config endpoint (older server) — selector hidden.
  vi.spyOn(api, 'chatConfig').mockRejectedValue(new ApiError(404, 'not found'))
}

// mockNonStream guards the non-stream fallback: tests assert it is NOT hit.
function mockNonStream() {
  return vi
    .spyOn(api, 'chat')
    .mockRejectedValue(new Error('non-stream fallback must not run'))
}

// mockStream replays the given SSE events; with hang=true the stream then
// blocks until the caller aborts (rejecting with AbortError, like fetch).
function mockStream(events: ChatStreamEvent[], opts?: { hang?: boolean }) {
  return vi.spyOn(api, 'chatStream').mockImplementation(
    (_p: string, _q: string, _h: { role: string; content: string }[], streamOpts?: ChatStreamOptions) =>
      (async function* () {
        for (const ev of events) yield ev
        if (opts?.hang) {
          await new Promise<never>((_, reject) => {
            const abort = () => reject(new DOMException('aborted', 'AbortError'))
            if (streamOpts?.signal?.aborted) abort()
            streamOpts?.signal?.addEventListener('abort', abort)
          })
        }
      })(),
  )
}

async function sendQuestion(q = 'how does auth work?') {
  render(<ChatPanel project="demo" seedQuestion="" onOpenFile={vi.fn()} />)
  const box = await screen.findByPlaceholderText(/How does authentication work/)
  fireEvent.change(box, { target: { value: q } })
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
}

// Warm the code-split markdown chunk so React.lazy resolves instantly and the
// Suspense fallback (raw text) doesn't linger past the findBy* timeouts.
beforeAll(async () => {
  await import('../../components/MarkdownContent')
})

afterEach(() => {
  vi.restoreAllMocks()
  window.localStorage.clear() // don't leak mode/model prefs across tests
})

describe('ChatPanel streaming', () => {
  it('renders a markdown answer and shows the model used', async () => {
    mockSystem()
    const chat = mockNonStream()
    mockStream([
      { type: 'sources', sources: [], model: 'gemini-test' },
      { type: 'chunk', content: 'Auth uses **argon2id** hashes.' },
      { type: 'done' },
    ])
    await sendQuestion()
    const bold = await screen.findByText('argon2id', undefined, { timeout: 10000 })
    expect(bold.tagName).toBe('STRONG') // markdown actually rendered
    expect(screen.getByText('gemini-test')).toBeInTheDocument()
    expect(chat).not.toHaveBeenCalled()
  })

  it('shows a tool chip that resolves to success with args and preview', async () => {
    mockSystem()
    mockNonStream()
    mockStream([
      { type: 'tool_call', id: 't1', name: 'semantic_search', args: { query: 'auth' } },
      {
        type: 'tool_result',
        id: 't1',
        name: 'semantic_search',
        preview: '3 hits',
        is_error: false,
        elapsed_ms: 42,
      },
      { type: 'chunk', content: 'Found it.' },
      { type: 'done' },
    ])
    await sendQuestion()
    expect(await screen.findByText('semantic_search')).toBeInTheDocument()
    expect(await screen.findByTitle('Tool succeeded')).toBeInTheDocument()
    expect(screen.getByText('3 hits')).toBeInTheDocument()
    expect(screen.getByText('42 ms')).toBeInTheDocument()
    expect(screen.getByText(/"query": "auth"/)).toBeInTheDocument()
  })

  it('marks the tool chip as failed on tool_result is_error', async () => {
    mockSystem()
    mockNonStream()
    mockStream([
      { type: 'tool_call', id: 't1', name: 'semantic_search', args: {} },
      {
        type: 'tool_result',
        id: 't1',
        name: 'semantic_search',
        preview: 'index unavailable',
        is_error: true,
      },
      { type: 'chunk', content: 'Could not search.' },
      { type: 'done' },
    ])
    await sendQuestion()
    expect(await screen.findByTitle('Tool failed')).toBeInTheDocument()
    expect(screen.getByText('index unavailable')).toBeInTheDocument()
  })

  it('surfaces an error event without falling back to non-stream', async () => {
    mockSystem()
    const chat = mockNonStream()
    mockStream([
      { type: 'sources', sources: [], model: 'm' },
      { type: 'error', message: 'provider exploded' },
      { type: 'done' },
    ])
    await sendQuestion()
    expect(await screen.findByRole('alert')).toHaveTextContent('provider exploded')
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument(),
    )
    expect(chat).not.toHaveBeenCalled()
  })

  it('Stop aborts the stream, keeps the partial answer and skips the fallback', async () => {
    mockSystem()
    const chat = mockNonStream()
    mockStream([{ type: 'chunk', content: 'partial answer' }], { hang: true })
    await sendQuestion()
    const stop = await screen.findByRole('button', { name: 'Stop' })
    expect(await screen.findByText('partial answer')).toBeInTheDocument()
    fireEvent.click(stop)
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument(),
    )
    // AbortError must not trigger the non-stream fallback nor an error alert.
    expect(chat).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText('partial answer')).toBeInTheDocument()
  })
})

describe('ChatPanel mode/model selector', () => {
  it('hides the selector when /chat/config is missing (404)', async () => {
    mockSystem()
    mockNonStream()
    mockStream([{ type: 'chunk', content: 'ok' }, { type: 'done' }])
    render(<ChatPanel project="demo" seedQuestion="" onOpenFile={vi.fn()} />)
    await screen.findByPlaceholderText(/How does authentication work/)
    expect(screen.queryByLabelText('mode')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('model')).not.toBeInTheDocument()
  })

  it('sends the selected mode and model with chatStream', async () => {
    vi.spyOn(api, 'system').mockResolvedValue(SYSTEM)
    vi.spyOn(api, 'chatConfig').mockResolvedValue({
      enabled: true,
      modes: ['rag', 'agent'],
      models: ['model-a', 'model-b'],
    })
    mockNonStream()
    const stream = mockStream([{ type: 'chunk', content: 'ok' }, { type: 'done' }])
    render(<ChatPanel project="demo" seedQuestion="" onOpenFile={vi.fn()} />)
    fireEvent.change(await screen.findByLabelText('mode'), {
      target: { value: 'agent' },
    })
    fireEvent.change(screen.getByLabelText('model'), {
      target: { value: 'model-b' },
    })
    fireEvent.change(screen.getByPlaceholderText(/How does authentication work/), {
      target: { value: 'q' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    await screen.findByText('ok')
    expect(stream).toHaveBeenCalledWith(
      'demo',
      'q',
      expect.anything(),
      expect.objectContaining({ mode: 'agent', model: 'model-b' }),
    )
  })
})

describe('applyToolResult correlation', () => {
  const pending: ToolCall[] = [
    { id: 'a', name: 'search', args: {} },
    { name: 'search', args: {} }, // no id — FIFO candidate
    { name: 'search', args: {} },
  ]

  it('matches by id first', () => {
    const out = applyToolResult(pending, {
      type: 'tool_result',
      id: 'a',
      name: 'search',
      preview: 'ok',
    })
    expect(out[0].result?.preview).toBe('ok')
    expect(out[1].result).toBeUndefined()
  })

  it('falls back to name + FIFO order when the id is unknown', () => {
    const out = applyToolResult(pending, {
      type: 'tool_result',
      id: 'zzz',
      name: 'search',
      preview: 'first-free',
    })
    // id 'zzz' matches nothing; the oldest unresolved same-name call wins.
    expect(out[0].result?.preview).toBe('first-free')
    expect(out[1].result).toBeUndefined()
  })

  it('appends a standalone completed chip when nothing matches', () => {
    const out = applyToolResult([], {
      type: 'tool_result',
      name: 'orphan',
      preview: 'late',
      is_error: true,
    })
    expect(out).toHaveLength(1)
    expect(out[0].name).toBe('orphan')
    expect(out[0].result?.is_error).toBe(true)
  })
})
